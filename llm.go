package main

import (
	"context"
	"fmt"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/meinside/openai-go"
	"github.com/tectiv3/awsnova-go"
	tele "gopkg.in/telebot.v3"
)

// generate a user-agent value
func userAgent(userID int64) string {
	return fmt.Sprintf("telegram-chatgpt-bot:%d", userID)
}

func (s *Server) simpleAnswer(c tele.Context, request string) (string, error) {
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
		aiClient = s.anthropic
	} else if model.Provider != pOpenAI {
		modelID = s.conf.OpenAILatestModel
	}

	aiClient.Verbose = s.conf.Verbose
	history := []openai.ChatMessage{system}
	history = append(history, msg)

	response, err := aiClient.CreateChatCompletion(modelID, history,
		openai.ChatCompletionOptions{}.
			SetUser(userAgent(c.Sender().ID)).
			SetTemperature(chat.Temperature))
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
	_ = c.Notify(tele.Typing)
	msg := openai.NewChatUserMessage(request)
	system := openai.NewChatSystemMessage(masterPrompt)

	aiClient := s.openAI
	aiClient.Verbose = s.conf.Verbose
	history := []openai.ChatMessage{system}
	history = append(history, msg)

	response, err := aiClient.CreateChatCompletion(
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

	response, err := s.openAI.CreateChatCompletion(
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

	if chat.Stream {
		if err := s.getStreamAnswer(chat, c, msgPtr); err != nil {
			Log.WithField("user", c.Sender().Username).Error(err)
			_ = c.Send(err.Error(), "text", &tele.SendOptions{ReplyTo: c.Message()})
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
		aiClient = s.anthropic
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

	response, err := aiClient.CreateChatCompletion(modelID, history,
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
		chat.mutex.Lock()
		chat.TotalTokens += response.Usage.TotalTokens
		chat.mutex.Unlock()
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

// getStreamAnswer starts a stream with the given chat and history
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
		aiClient = s.anthropic
	} else if model.Provider != pOpenAI {
		modelID = s.conf.OpenAILatestModel
		// s.ai.APIKey = s.conf.OpenAIAPIKey
	}

	// s.ai.Verbose = s.conf.Verbose
	if _, err := aiClient.CreateChatCompletion(modelID, history,
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
			// every 20 tokens update the message
			if tokens%20 == 0 {
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
			chat.mutex.Lock()
			chat.TotalTokens += tokens
			chat.mutex.Unlock()
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

			response, err := s.openAI.CreateChatCompletion(miniModel, history,
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
				// every 20 tokens update the message
				if tokens%20 == 0 {
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

				chat.mutex.Lock()
				chat.TotalTokens += comp.Usage.InputTokens + comp.Usage.OutputTokens
				chat.mutex.Unlock()

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
