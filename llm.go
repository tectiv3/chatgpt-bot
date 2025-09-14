package main

import (
	"context"
	"fmt"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/meinside/openai-go"
	"github.com/tectiv3/anthropic-go"
	"github.com/tectiv3/awsnova-go"
	tele "gopkg.in/telebot.v3"
)

// generate a user-agent value
func userAgent(userID int64) string {
	return fmt.Sprintf("telegram-chatgpt-bot:%d", userID)
}

// convertDialogToResponseMessages converts OpenAI chat messages to ResponseMessage format
func (s *Server) convertDialogToResponseMessages(history []openai.ChatMessage) []openai.ResponseMessage {
	var messages []openai.ResponseMessage

	for _, msg := range history {
		// Skip system messages as they'll be handled via instructions
		if msg.Role == openai.ChatMessageRoleSystem {
			continue
		}
		// Convert tool messages to user messages with clear context for Responses API
		if msg.Role == openai.ChatMessageRoleTool {
			if str, err := msg.ContentString(); err == nil {
				messages = append(messages, openai.ResponseMessage{
					Role:    "user",
					Content: fmt.Sprintf("[Tool execution result]: %s", str),
				})
			}
			continue
		}
		if str, err := msg.ContentString(); err == nil {
			messages = append(messages, openai.ResponseMessage{Role: string(msg.Role), Content: str})
		} else if contentArr, err := msg.ContentArray(); err == nil {
			fixed := []openai.ChatMessageContent{}
			for _, c := range contentArr {
				if c.Type == "text" && c.Text != nil {
					fixed = append(fixed, openai.ChatMessageContent{Type: "input_text", Text: c.Text})
				} else if c.Type == "image_url" {
					// cast c.ImageURL to map[string]string and get "url"
					if url, ok := c.ImageURL.(map[string]string)["url"]; ok {
						fixed = append(fixed, openai.ChatMessageContent{Type: "input_image", ImageURL: url})
					} else {
						Log.Warnf("Image URL is not a string map: %v", c.ImageURL)
						continue
					}
				} else {
					fixed = append(fixed, c)
				}
			}
			messages = append(messages, openai.ResponseMessage{Role: string(msg.Role), Content: fixed})
		}
		//
		// 		// Convert content to string
		// 		content, err := msg.ContentString()
		// 		if err != nil {
		// 			// Try to get content from array format
		// 			if contentArr, arrErr := msg.ContentArray(); arrErr == nil {
		// 				for _, c := range contentArr {
		// 					if c.Type == "text" && c.Text != nil {
		// 						// content = *c.Text
		// 						break
		// } else if c.Type == "image_url" && c.ImageURL != nil {
		// 				}
		// 			}
		// 		}
		//

		// 		if content != "" {
		// messages = append(messages, openai.ResponseMessage{Role: string(msg.Role), Content: msg.Content})
		// }
	}

	return messages
}

// getResponseStream uses the Responses API for streaming when available
func (s *Server) getResponseStream(chat *Chat, c tele.Context, question *string) error {
	_ = c.Notify(tele.Typing)
	type completion struct {
		event openai.ResponseStreamEvent
		done  bool
		err   error
	}
	ch := make(chan completion, 1)

	history := chat.getDialog(question)
	Log.WithField("user", c.Sender().Username).WithField("history", len(history)).Info("Response Stream")

	chat.removeMenu(c)
	sentMessage := chat.getSentMessage(c)

	model := s.getModel(chat.ModelName)
	if model.Provider != pOpenAI {
		return s.getStreamAnswer(chat, c, question)
	}

	modelID := model.ModelID
	messages := s.convertDialogToResponseMessages(history)
	aiClient := s.openAI

	instructions := chat.MasterPrompt
	if chat.RoleID != nil {
		instructions = chat.Role.Prompt
	}
	// append current date and time to instructions
	instructions += fmt.Sprintf("\n\nCurrent date and time: %s", time.Now().Format(time.RFC3339))

	options := openai.ResponseOptions{}
	options.SetInstructions(instructions)
	options.SetMaxOutputTokens(4000)
	if !model.Reasoning {
		options.SetTemperature(chat.Temperature)
	}
	options.SetUser(userAgent(c.Sender().ID))
	options.SetStore(false)
	// aiClient.Verbose = s.conf.Verbose

	// Get custom function tools for responses API
	tools := s.getResponseTools()

	// Add built-in search tool if configured
	if len(model.SearchTool) > 0 {
		tools = append(tools, openai.NewBuiltinTool(model.SearchTool))
	}

	if len(tools) > 0 {
		options.SetTools(tools)
	}

	ctx, cancel := WithTimeout(LongTimeout)
	defer cancel()

	reply := ""
	tokens := 0
	var functionCalls []openai.ResponseOutput

	if err := aiClient.CreateResponseStreamWithContext(ctx, modelID, messages, options,
		func(event openai.ResponseStreamEvent, d bool, e error) {
			ch <- completion{event: event, done: d, err: e}
			if d {
				close(ch)
			}
		}); err != nil {
		Log.WithField("user", c.Sender().Username).Error("Response stream error:", err)
		return err
	}

	for comp := range ch {
		if comp.err != nil {
			Log.WithField("user", c.Sender().Username).Error(comp.err)

			return comp.err
		}

		if comp.done {
			// Handle function calls if any
			if len(functionCalls) > 0 {
				_ = c.Notify(tele.Typing)

				// First, add the assistant's message with tool calls to conversation history
				assistantMsg := openai.NewChatAssistantMessage(reply)
				// Convert ResponseOutput function calls to ToolCall format for history
				var toolCalls []openai.ToolCall
				for _, responseOutput := range functionCalls {
					if responseOutput.Type == "function_call" {
						toolCall := openai.ToolCall{
							ID:   responseOutput.CallID,
							Type: "function",
							Function: openai.ToolCallFunction{
								Name:      responseOutput.Name,
								Arguments: responseOutput.Arguments,
							},
						}
						toolCalls = append(toolCalls, toolCall)
					}
				}
				assistantMsg.ToolCalls = toolCalls
				chat.addMessageToDialog(assistantMsg)

				// Execute the tool calls
				result, err := s.handleResponseFunctionCalls(chat, c, functionCalls)
				if err != nil {
					return err
				}
				s.updateReply(chat, reply+result, c)

				return nil
			}

			// Update token count and save history
			if tokens > 0 {
				chat.updateTotalTokens(tokens)
			}

			if reply != "" {
				chat.addMessageToDialog(openai.NewChatAssistantMessage(reply))
				s.saveHistory(chat)
			}

			return nil
		}

		event := comp.event

		switch event.Type {
		case "response.output_item.added":
			if event.Item != nil {
				Log.WithField("item", event.Item.Type).Info("Output added")
				switch event.Item.Type {
				case "function_call":
					Log.WithField("function", event.Item.Name).Info("Function call started")
				case "web_search_call":
					_, _ = c.Bot().Edit(sentMessage, chat.t("Web search started, please wait..."))
				}
			}
		case "response.output_text.delta":
			if event.Delta != nil {
				reply += *event.Delta
				tokens++
				if tokens%16 == 0 {
					_, _ = c.Bot().Edit(sentMessage, reply)
				}
			}

		case "response.output_item.done":
			if event.Item != nil {
				switch event.Item.Type {
				case "message":
					Log.WithField("user", c.Sender().Username).Info("Message output completed")
					s.updateReply(chat, reply, c)
				case "function_call":
					functionCalls = append(functionCalls, *event.Item)
					Log.WithField("user", c.Sender().Username).WithField("function", event.Item.Name).Info("Function call completed")
				}
			}

		case "response.done":
			if event.Response != nil && event.Response.Usage != nil {
				tokens = event.Response.Usage.TotalTokens
			}
			Log.WithField("user", c.Sender().Username).WithField("tokens", tokens).Info("Response stream finished")
		}

	}

	return nil
}

// getResponse uses the non-streaming Responses API
func (s *Server) getResponse(chat *Chat, c tele.Context, question *string) error {
	_ = c.Notify(tele.Typing)

	history := chat.getDialog(question)
	Log.WithField("user", c.Sender().Username).WithField("history", len(history)).Info("Response Non-Stream")

	chat.removeMenu(c)

	model := s.getModel(chat.ModelName)
	if model.Provider != pOpenAI {
		return s.getAnswer(chat, c, question)
	}

	modelID := model.ModelID
	messages := s.convertDialogToResponseMessages(history)
	aiClient := s.openAI

	instructions := chat.MasterPrompt
	if chat.RoleID != nil {
		instructions = chat.Role.Prompt
	}
	// append current date and time to instructions
	instructions += fmt.Sprintf("\n\nCurrent date and time: %s", time.Now().Format(time.RFC3339))

	options := openai.ResponseOptions{}
	options.SetInstructions(instructions)
	options.SetMaxOutputTokens(4000)
	if !model.Reasoning {
		options.SetTemperature(chat.Temperature)
	}
	options.SetUser(userAgent(c.Sender().ID))
	options.SetStore(false)

	// Get custom function tools for responses API
	tools := s.getResponseTools()

	// Add built-in search tool if configured
	if len(model.SearchTool) > 0 {
		tools = append(tools, openai.NewBuiltinTool(model.SearchTool))
	}

	if len(tools) > 0 {
		options.SetTools(tools)
	}

	ctx, cancel := WithTimeout(LongTimeout)
	defer cancel()

	response, err := aiClient.CreateResponseWithContext(ctx, modelID, messages, options)
	if err != nil {
		Log.WithField("user", c.Sender().Username).Error("Response error:", err)
		return err
	}

	// Check if response has function calls
	var functionCalls []openai.ResponseOutput
	for _, output := range response.Output {
		if output.Type == "function_call" {
			functionCalls = append(functionCalls, output)
		}
	}

	// Handle function calls if any
	if len(functionCalls) > 0 {
		_ = c.Notify(tele.Typing)

		// First, add the assistant's message with tool calls to conversation history
		reply := ""
		if len(response.Output) > 0 {
			for _, output := range response.Output {
				if output.Type == "message" && len(output.Content) > 0 {
					for _, content := range output.Content {
						if content.Type == "text" && content.Text != "" {
							reply += content.Text
						}
					}
				}
			}
		}

		assistantMsg := openai.NewChatAssistantMessage(reply)
		// Convert ResponseOutput function calls to ToolCall format for history
		var toolCalls []openai.ToolCall
		for _, responseOutput := range functionCalls {
			if responseOutput.Type == "function_call" {
				toolCall := openai.ToolCall{
					ID:   responseOutput.CallID,
					Type: "function",
					Function: openai.ToolCallFunction{
						Name:      responseOutput.Name,
						Arguments: responseOutput.Arguments,
					},
				}
				toolCalls = append(toolCalls, toolCall)
			}
		}
		assistantMsg.ToolCalls = toolCalls
		chat.addMessageToDialog(assistantMsg)

		// Execute the tool calls
		result, err := s.handleResponseFunctionCalls(chat, c, functionCalls)
		if err != nil {
			return err
		}

		// Update UI with tool results and end conversation
		s.updateReply(chat, reply+"\n\n"+result, c)
		return nil
	}

	// No function calls - handle normal response
	reply := ""
	for _, output := range response.Output {
		if output.Type == "message" && len(output.Content) > 0 {
			for _, content := range output.Content {
				if content.Type == "text" && content.Text != "" {
					reply += content.Text
				}
			}
		}
	}

	// Update token count and save history
	if response.Usage != nil {
		chat.updateTotalTokens(response.Usage.TotalTokens)
	}

	if reply != "" {
		chat.addMessageToDialog(openai.NewChatAssistantMessage(reply))
		s.saveHistory(chat)
		s.updateReply(chat, reply, c)
	}

	return nil
}

func (s *Server) simpleAnswer(c tele.Context, request string) (string, error) {
	ctx, cancel := WithTimeout(DefaultTimeout)
	defer cancel()

	_ = c.Notify(tele.Typing)
	chat := s.getChat(c.Chat(), c.Sender())
	msg := openai.NewChatUserMessage(request)

	prompt := chat.MasterPrompt
	if chat.RoleID != nil {
		prompt = chat.Role.Prompt
	}
	system := openai.NewChatSystemMessage(prompt)

	aiClient := s.openAI
	model := s.getModel(chat.ModelName)
	modelID := model.ModelID
	if model.Provider == pAnthropic {
		aiClient = openai.NewClient(s.conf.AnthropicAPIKey, "").SetBaseURL("https://api.anthropic.com/v1")
	} else if model.Provider == pGemini {
		aiClient = s.gemini
	} else if model.Provider != pOpenAI {
		modelID = s.conf.OpenAILatestModel
	}

	aiClient.Verbose = s.conf.Verbose
	history := []openai.ChatMessage{system}
	history = append(history, msg)

	response, err := aiClient.CreateChatCompletionWithContext(ctx, modelID, history,
		openai.ChatCompletionOptions{}.SetUser(userAgent(c.Sender().ID)).SetTemperature(chat.Temperature))
	if err != nil {
		Log.WithField("user", c.Sender().Username).Error(err)
		return err.Error(), err
	}
	_ = c.Notify(tele.Typing)

	result := response.Choices[0].Message
	if len(result.ToolCalls) > 0 {
		return s.handleFunctionCall(chat, c, result)
	}

	var answer string
	if len(response.Choices) > 0 {
		answer, err = response.Choices[0].Message.ContentString()
		if err != nil {
			Log.WithField("user", c.Sender().Username).Error(err)
			return err.Error(), err
		}
		chat.TotalTokens += response.Usage.TotalTokens
		s.db.Save(&chat)
	} else {
		answer = "No response from API."
	}

	if s.conf.Verbose {
		Log.WithField("user", c.Sender().Username).Info(answer)
	}

	return answer, nil
}

func (s *Server) anonymousAnswer(c tele.Context, request string) (string, error) {
	ctx, cancel := WithTimeout(DefaultTimeout)
	defer cancel()

	_ = c.Notify(tele.Typing)
	msg := openai.NewChatUserMessage(request)
	system := openai.NewChatSystemMessage(masterPrompt)

	aiClient := s.openAI
	aiClient.Verbose = s.conf.Verbose
	history := []openai.ChatMessage{system}
	history = append(history, msg)

	response, err := aiClient.CreateChatCompletionWithContext(ctx,
		s.conf.OpenAILatestModel,
		history,
		openai.ChatCompletionOptions{}.SetUser(userAgent(c.Sender().ID)).SetTemperature(0.8),
	)
	if err != nil {
		Log.WithField("user", c.Sender().Username).Error(err)
		return err.Error(), err
	}
	_ = c.Notify(tele.Typing)

	// result := response.Choices[0].Message
	// if len(result.ToolCalls) > 0 {
	// 	return s.handleFunctionCall(chat, c, result)
	// }

	var answer string
	if len(response.Choices) > 0 {
		answer, err = response.Choices[0].Message.ContentString()
		if err != nil {
			Log.WithField("user", c.Sender().Username).Error(err)
			return err.Error(), err
		}
	} else {
		answer = "No response from API."
	}

	if s.conf.Verbose {
		Log.WithField("user", c.Sender().Username).Info(answer)
	}

	return answer, nil
}

// summarize summarizes the chat history
func (s *Server) summarize(chatHistory []ChatMessage) (*openai.ChatCompletion, error) {
	msg := openai.NewChatUserMessage(
		"Make a compressed summary of the conversation with the AI. Try to be as brief as possible and highlight key points. Use same language as the user.",
	)
	system := openai.NewChatSystemMessage("Be as brief as possible")

	history := []openai.ChatMessage{system}
	for _, h := range chatHistory {
		if h.Role == openai.ChatMessageRoleTool {
			continue
		}
		history = append(
			history,
			openai.ChatMessage{Role: h.Role, Content: []openai.ChatMessageContent{{
				Type: "text", Text: h.Content,
			}}},
		)
	}
	history = append(history, msg)
	Log.Info("Chat history len: ", len(history))

	ctx, cancel := WithTimeout(DefaultTimeout)
	defer cancel()

	response, err := s.openAI.CreateChatCompletionWithContext(ctx,
		miniModel,
		history,
		openai.ChatCompletionOptions{}.SetUser(userAgent(31337)).SetTemperature(0.5),
	)
	if err != nil {
		Log.Error(err)
		return nil, err
	}
	if response.Choices[0].Message.Content == nil {
		return nil, nil
	}

	return &response, nil
}

func (s *Server) complete(c tele.Context, message string, reply bool) {
	chat := s.getChat(c.Chat(), c.Sender())

	text := "..."
	sentMessage := c.Message()
	var err error
	// reply is a flag to indicate if we need to reply to another message, otherwise it is a voice transcription
	// TODO: refactor me, this is a mess
	if !reply {
		text = fmt.Sprintf(chat.t("_Transcript:_\n\n%s\n\n_Answer:_ \n\n"), message)
		sentMessage, err = c.Bot().Send(c.Recipient(), text, "text", &tele.SendOptions{
			ReplyTo:   c.Message(),
			ParseMode: tele.ModeMarkdown,
		})
		if err != nil {
			Log.WithField("user", c.Sender().Username).Error(err)
			sentMessage, _ = c.Bot().Send(c.Recipient(), err.Error())
		}
		chat.MessageID = &([]string{strconv.Itoa(sentMessage.ID)}[0])
		c.Set("reply", *sentMessage)
	}

	msgPtr := &message
	if len(message) == 0 {
		msgPtr = nil
	}

	model := s.getModel(chat.ModelName)
	if model.Provider == pAWS {
		s.getNovaAnswer(chat, c, msgPtr)
		return
	}

	if model.Provider == pAnthropic {
		s.getAnthropicAnswer(chat, c, msgPtr)
		return
	}

	if chat.Stream {
		// Use new Responses API for OpenAI models, fall back to regular streaming for others
		if model.Provider == pOpenAI {
			if err := s.getResponseStream(chat, c, msgPtr); err != nil {
				Log.WithField("user", c.Sender().Username).Error(err)
				_ = c.Reply(c.Message(), err.Error())
			}
		} else {
			if err := s.getStreamAnswer(chat, c, msgPtr); err != nil {
				Log.WithField("user", c.Sender().Username).Error(err)
				_ = c.Reply(c.Message(), err.Error())
			}
		}
		return
	}

	if err := s.getAnswer(chat, c, msgPtr); err != nil {
		Log.WithField("user", c.Sender().Username).Error(err)
		_ = c.Send(err.Error(), replyMenu)
	}
}

func (s *Server) getAnswer(chat *Chat, c tele.Context, question *string) error {
	_ = c.Notify(tele.Typing)

	aiClient := s.openAI
	model := s.getModel(chat.ModelName)
	modelID := model.ModelID
	if model.Provider == pAnthropic {
		s.getAnthropicAnswer(chat, c, question)
		return nil
	} else if model.Provider == pGemini {
		aiClient = s.gemini
	} else if model.Provider != pOpenAI {
		modelID = s.conf.OpenAILatestModel
	}

	options := openai.ChatCompletionOptions{}
	// s.ai.APIKey = s.conf.OpenAIAPIKey
	options.SetTools(s.getFunctionTools())

	// s.ai.Verbose = s.conf.Verbose
	// options.SetMaxTokens(3000)
	history := chat.getDialog(question)
	Log.WithField("user", c.Sender().Username).WithField("history", len(history)).Info("Answer")

	chat.removeMenu(c)

	ctx, cancel := WithTimeout(LongTimeout)
	defer cancel()

	response, err := aiClient.CreateChatCompletionWithContext(ctx, modelID, history,
		options.
			SetUser(userAgent(c.Sender().ID)).
			SetTemperature(chat.Temperature))
	if err != nil {
		Log.WithField("user", c.Sender().Username).Error(err)
		return err
	}

	var answer string
	result := response.Choices[0].Message
	if len(result.ToolCalls) > 0 {
		answer, err = s.handleFunctionCall(chat, c, result)
		if err != nil {
			return err
		}
	} else if len(response.Choices) == 0 {
		answer = chat.t("No response from API.")
	} else {
		answer, err = response.Choices[0].Message.ContentString()
		if err != nil {
			Log.WithField("user", c.Sender().Username).Error(err)
			return err
		}
		chat.updateTotalTokens(response.Usage.TotalTokens)
		chat.addMessageToDialog(openai.NewChatAssistantMessage(answer))
		s.saveHistory(chat)
	}

	Log.WithField("user", c.Sender().Username).
		WithField("length", len(answer)).
		Info("got an answer")

	if len(answer) == 0 {
		return nil
	}

	if len(answer) > 4000 {
		file := tele.FromReader(strings.NewReader(answer))
		_ = c.Send(
			&tele.Document{File: file, FileName: "answer.txt", MIME: "text/plain"},
			replyMenu,
		)
		// if err := c.Bot().React(c.Sender(), c.Message(), react.React(react.Brain)); err != nil {
		// 	Log.Warn(err)
		// 	return err
		// }

		return nil
	}
	s.updateReply(chat, answer, c)

	// if err := c.Bot().React(c.Sender(), c.Message(), react.React(react.Brain)); err != nil {
	// 	Log.Warn(err)
	// 	return err
	// }

	return nil
}

func (s *Server) getStreamAnswer(chat *Chat, c tele.Context, question *string) error {
	_ = c.Notify(tele.Typing)
	type completion struct {
		response openai.ChatCompletion
		done     bool
		err      error
	}
	ch := make(chan completion, 1)

	history := chat.getDialog(question)
	Log.WithField("user", c.Sender().Username).WithField("history", len(history)).Info("Stream")

	chat.removeMenu(c)

	sentMessage := chat.getSentMessage(c)

	aiClient := s.openAI
	model := s.getModel(chat.ModelName)
	Log.WithField("model", model.ModelID).WithField("provider", model.Provider).Info("Using model")
	modelID := model.ModelID
	if model.Provider == pAnthropic {
		s.getAnthropicAnswer(chat, c, question)
		return nil
	} else if model.Provider == pGemini {
		aiClient = s.gemini
	} else if model.Provider != pOpenAI {
		modelID = s.conf.OpenAILatestModel
		// s.ai.APIKey = s.conf.OpenAIAPIKey
	}

	// aiClient.Verbose = s.conf.Verbose
	ctx, cancel := WithTimeout(LongTimeout)
	defer cancel()

	if _, err := aiClient.CreateChatCompletionWithContext(ctx, modelID, history,
		openai.ChatCompletionOptions{}.
			SetTools(s.getFunctionTools()).
			SetToolChoice(openai.ChatCompletionToolChoiceAuto).
			SetUser(userAgent(c.Sender().ID)).
			SetTemperature(chat.Temperature).
			SetStream(func(r openai.ChatCompletion, d bool, e error) {
				ch <- completion{response: r, done: d, err: e}
				if d {
					close(ch)
				}
			})); err != nil {
		Log.WithField("user", c.Sender().Username).Error(err)
		return err
	}
	_ = c.Notify(tele.Typing)
	reply := ""
	result := ""
	if c.Get("reply") != nil {
		if msg, ok := c.Get("reply").(tele.Message); ok {
			result = msg.Text
		}
	}

	tokens := 0
	for comp := range ch {
		if comp.err != nil {
			Log.WithField("user", c.Sender().Username).Error(comp.err)
			return comp.err
		}
		if !comp.done {
			// streaming the result, append the response to the result
			if comp.response.Choices[0].Delta.Content != nil {
				if c, err := comp.response.Choices[0].Delta.ContentString(); err == nil {
					result += c
				}
				tokens++
			}
			// every 16 tokens update the message
			if tokens%16 == 0 {
				_, _ = c.Bot().Edit(sentMessage, result)
			}
		} else {
			// stream is done, send the final result
			if len(comp.response.Choices[0].Message.ToolCalls) > 0 &&
				comp.response.Choices[0].Message.ToolCalls[0].Function.Name != "" {
				_ = c.Notify(tele.Typing)
				result, err := s.handleFunctionCall(chat, c, comp.response.Choices[0].Message)
				if err != nil {
					return err
				}

				s.updateReply(chat, reply+result, c)
				// if err := c.Bot().React(c.Sender(), c.Message(), react.React(react.Brain)); err != nil {
				// 	Log.Warn(err)
				// 	return err
				// }

				return nil
			}

			if len(result) == 0 {
				return nil
			}
			s.updateReply(chat, reply+result, c)

			Log.WithField("user", c.Sender().Username).WithField("tokens", tokens).Info("Stream finished")
			// if err := c.Bot().React(c.Sender(), c.Message(), react.React(react.Brain)); err != nil {
			// 	Log.Warn(err)
			// }
			chat.updateTotalTokens(tokens)
			chat.addMessageToDialog(openai.NewChatAssistantMessage(result))
			s.saveHistory(chat)

			return nil
		}
	}

	return nil
}

func (s *Server) updateReply(chat *Chat, answer string, c tele.Context) {
	sentMessage := chat.getSentMessage(c)

	if chat.QA {
		go func() {
			defer func() {
				if err := recover(); err != nil {
					Log.WithField("error", err).Error("panic: ", string(debug.Stack()))
				}
			}()
			msg := openai.NewChatUserMessage(answer)
			system := openai.NewChatSystemMessage(fmt.Sprintf("You are a professional Q&A expert. Do not react in any way to the contents of the input, just use it as a reference information. Please provide three follow-up questions to user input. Be very concise and to the point. Do not have numbers in front of questions. Separate each question with a line break. Only output three questions in %s, no need for any explanation, introduction or disclaimers - this is important!", chat.Lang))
			s.openAI.Verbose = s.conf.Verbose
			history := []openai.ChatMessage{system}
			history = append(history, msg)

			ctx, cancel := WithTimeout(DefaultTimeout)
			defer cancel()

			response, err := s.openAI.CreateChatCompletionWithContext(ctx, miniModel, history,
				openai.ChatCompletionOptions{}.
					SetUser(userAgent(c.Sender().ID)).
					SetTemperature(chat.Temperature))
			if err != nil {
				Log.WithField("user", c.Sender().Username).Error(err)
			} else if len(response.Choices) > 0 {
				questions, err := response.Choices[0].Message.ContentString()
				if err != nil {
					Log.WithField("user", c.Sender().Username).Error(err)
				} else {
					menu := &tele.ReplyMarkup{ResizeKeyboard: true, OneTimeKeyboard: true}
					rows := []tele.Row{}
					for _, q := range strings.Split(questions, "\n\n") {
						rows = append(rows, menu.Row(tele.Btn{Text: q}))
					}
					// rows = append(rows, menu.Row(btnReset))
					menu.Reply(rows...)
					// menu.Inline(menu.Row(btnReset))
					// delete sentMessage
					_ = c.Bot().Delete(sentMessage)
					// send new one with reply keyboard
					_, _ = c.Bot().Send(c.Recipient(),
						ConvertMarkdownToTelegramMarkdownV2(answer),
						"text",
						&tele.SendOptions{ParseMode: tele.ModeMarkdownV2},
						menu)
				}

			}
		}()
		return
	}

	if len(answer) > 0 {
		if _, err := c.Bot().Edit(
			sentMessage,
			ConvertMarkdownToTelegramMarkdownV2(answer),
			"text",
			&tele.SendOptions{ParseMode: tele.ModeMarkdownV2},
			replyMenu,
		); err != nil {
			Log.Warn(err)
			if _, err := c.Bot().Edit(sentMessage, answer, replyMenu); err != nil {
				Log.Warn(err)
				_ = c.Send(err.Error())
			}
		}
	}
}

func (s *Server) saveHistory(chat *Chat) {
	// Log.WithField("user", chat.User.Username).WithField("history", len(chat.History)).Info("Saving chat history")

	// iterate over history
	// drop messages that are older than chat.ConversationAge days
	var history []ChatMessage
	chat.mutex.Lock()
	defer chat.mutex.Unlock()
	for _, h := range chat.History {
		if h.ID == 0 {
			history = append(history, h)
			continue
		}
		if h.CreatedAt.Before(time.Now().AddDate(0, 0, -int(chat.ConversationAge))) {
			s.db.Where("chat_id = ?", chat.ID).Where("id = ?", h.ID).Delete(&ChatMessage{})
		} else {
			history = append(history, h)
		}
	}
	chat.History = history
	// Log.WithField("user", chat.User.Username).WithField("history", len(chat.History)).Info("Saved chat history")
	if len(chat.History) < 100 {
		s.db.Save(&chat)
		return
	}

	Log.WithField("user", chat.User.Username).
		Infof("Chat history for chat ID %d is too long. Summarising...", chat.ID)
	response, err := s.summarize(chat.History)
	if err != nil {
		Log.Warn(err)
		return
	}
	summary, _ := response.Choices[0].Message.ContentString()

	if s.conf.Verbose {
		Log.Info(summary)
	}
	maxID := chat.History[len(chat.History)-3].ID
	Log.WithField("user", chat.User.Username).
		Infof("Deleting chat history for chat ID %d up to message ID %d", chat.ID, maxID)
	s.db.Where("chat_id = ?", chat.ID).Where("id <= ?", maxID).Delete(&ChatMessage{})

	chat.History = []ChatMessage{{
		Role:      openai.ChatMessageRoleAssistant,
		Content:   &summary,
		ChatID:    chat.ChatID,
		CreatedAt: time.Now(),
	}}

	Log.WithField("user", chat.User.Username).
		Info("Chat history length after summarising: ", len(chat.History))
	chat.TotalTokens += response.Usage.TotalTokens

	s.db.Save(&chat)
}

func (s *Server) getAnthropicAnswer(chat *Chat, c tele.Context, question *string) {
	maxTokens := 1000
	system := chat.MasterPrompt
	if chat.RoleID != nil {
		system = chat.Role.Prompt
	}

	chat.removeMenu(c)
	sentMessage := chat.getSentMessage(c)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	s.anthropic.Apply(anthropic.WithSystemPrompt(system))
	s.anthropic.Apply(anthropic.WithTools(anthropic.NewWebSearchTool(anthropic.WebSearchToolOptions{MaxUses: 5})))
	s.anthropic.Apply(anthropic.WithMaxTokens(maxTokens))
	// s.anthropic.Apply(anthropic.WithTemperature(chat.Temperature))

	stream, err := s.anthropic.Stream(ctx, chat.getAnthropicDialog(question))
	if err != nil {
		Log.WithField("user", c.Sender().Username).Error(err)
		_, _ = c.Bot().Edit(sentMessage, err.Error())
		return
	}
	defer stream.Close()

	var result strings.Builder
	searchQuery := ""
	_ = c.Notify(tele.Typing)
	tokens := 0
	accumulator := anthropic.NewResponseAccumulator()

	for stream.Next() {
		select {
		case <-ctx.Done():
			_, _ = c.Bot().Edit(sentMessage, "Timeout")
			return
		default:
			// Continue processing
		}
		event := stream.Event()
		accumulator.AddEvent(event)

		switch event.Type {
		case anthropic.EventTypeContentBlockStart:
			if event.ContentBlock != nil {
				switch event.ContentBlock.Type {
				case anthropic.ContentTypeServerToolUse:
					if event.ContentBlock.Name == "web_search" {
						_, _ = c.Bot().Edit(sentMessage, chat.t("Web search started, please wait..."))
					} else {
						Log.WithField("content_block", event.ContentBlock.Name).Info("Content block started")
					}
				}
			}
		case anthropic.EventTypeContentBlockDelta:
			if event.Delta == nil {
				continue
			}
			switch event.Delta.Type {
			case anthropic.EventDeltaTypeText:
				result.WriteString(event.Delta.Text)
				tokens++
				if tokens%10 == 0 {
					_, _ = c.Bot().Edit(sentMessage, result.String())
				}

			case anthropic.EventDeltaTypeInputJSON:
				searchQuery += event.Delta.PartialJSON
				// Log("Input JSON delta: %q", event.Delta.PartialJSON)
			}

		case anthropic.EventTypeMessageStop:
			Log.WithField("user", c.Sender().Username).WithField("tokens", tokens).Info("Response stream finished")
			// done
		}
	}
	Log.WithField("searchQuery", searchQuery).Info("Search query")

	if err := stream.Err(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			Log.WithField("user", c.Sender().Username).Error("Timeout after 60s. Partial response: ", result.String())
			_, _ = c.Bot().Edit(sentMessage, "Timeout after 60s. Partial response: "+result.String())
		} else if ctx.Err() == context.Canceled {
			Log.WithField("user", c.Sender().Username).Error("Request cancelled. Partial response: ", result.String())
			_, _ = c.Bot().Edit(sentMessage, "Request cancelled. Partial response: "+result.String())
		} else {
			Log.WithField("user", c.Sender().Username).Error("Streaming error: ", err)
			_, _ = c.Bot().Edit(sentMessage, "Streaming error: "+err.Error())
		}
	}

	if accumulator.IsComplete() {
		usage := accumulator.Usage()
		reply := result.String()
		s.updateReply(chat, reply, c)
		tokens = usage.InputTokens + usage.OutputTokens
		// Update token count and save history
		if tokens > 0 {
			chat.updateTotalTokens(tokens)
		}

		if reply != "" {
			chat.addMessageToDialog(openai.NewChatAssistantMessage(reply))
			s.saveHistory(chat)
		}
	}
}

func (s *Server) getNovaAnswer(chat *Chat, c tele.Context, question *string) {
	maxTokens := 1000
	system := chat.MasterPrompt
	if chat.RoleID != nil {
		system = chat.Role.Prompt
	}
	req := awsnova.Request{
		Messages: chat.getNovaDialog(question),
		InferenceConfig: awsnova.InferenceConfig{
			MaxTokens:   &maxTokens,
			Temperature: &chat.Temperature,
		},
		System: system,
	}

	chat.removeMenu(c)
	sentMessage := chat.getSentMessage(c)
	// Create a context with a timeout
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Invoke the model with response stream
	ch, err := s.nova.InvokeModelWithResponseStream(ctx, req)
	if err != nil {
		Log.WithField("user", c.Sender().Username).Error(err)
		_, _ = c.Bot().Edit(sentMessage, err.Error())
		return
	}

	result := ""
	_ = c.Notify(tele.Typing)
	tokens := 0

	for {
		select {
		case comp, ok := <-ch:
			if !ok {
				// channel closed
				return
			}
			// Log.WithField("user", c.Sender().Username).Info(comp)
			if comp.Error != "" {
				Log.WithField("user", c.Sender().Username).Error(comp.Error)
				_, _ = c.Bot().Edit(sentMessage, comp.Error)
				return
			}
			if comp.Content != "" {
				result += comp.Content
				tokens++
				// every 16 tokens update the message
				if tokens%16 == 0 {
					_, _ = c.Bot().Edit(sentMessage, result)
				}
			}
			if comp.Done {
				continue
			}
			if comp.Usage != nil {
				if len(result) == 0 {
					result = "No response from model."
				}
				s.updateReply(chat, result, c)

				Log.WithField("user", c.Sender().Username).
					WithField("tokens", tokens).
					WithFields(map[string]interface{}{
						"input_tokens":  comp.Usage.InputTokens,
						"output_tokens": comp.Usage.OutputTokens,
					}).Info("Nova stream finished")

				chat.updateTotalTokens(comp.Usage.InputTokens + comp.Usage.OutputTokens)

				chat.History = append(chat.History,
					ChatMessage{
						Role:      "assistant",
						Content:   &result,
						ChatID:    chat.ChatID,
						CreatedAt: time.Now(),
					})
				s.saveHistory(chat)

				return
			}
		case <-ctx.Done():
			_, _ = c.Bot().Edit(sentMessage, "Timeout")
			return
		}
	}
}

func (s *Server) processPDF(c tele.Context) {
	pdf := c.Message().Document.File
	var fileName string

	if s.conf.TelegramServerURL != "" {
		f, err := c.Bot().FileByID(pdf.FileID)
		if err != nil {
			Log.Warn("Error getting file ID", "error=", err)
			return
		}
		fileName = f.FilePath
	} else {
		out, err := os.Create("uploads/" + pdf.FileID + ".pdf")
		if err != nil {
			Log.Warn("Error creating file", "error=", err)
			return
		}
		if err := c.Bot().Download(&pdf, out.Name()); err != nil {
			Log.Warn("Error getting file content", "error=", err)
			return
		}
		fileName = out.Name()
	}

	chat := s.getChat(c.Chat(), c.Sender())
	chat.addFileToDialog(c.Message().Caption, fileName, c.Message().Document.FileName)
	s.db.Save(&chat)

	s.complete(c, "", true)
}
