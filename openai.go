package main

import (
	"fmt"
	"github.com/meinside/openai-go"
	tele "gopkg.in/telebot.v3"
	"time"
)

// generate a user-agent value
func userAgent(userID int64) string {
	return fmt.Sprintf("telegram-chatgpt-bot:%d", userID)
}

func (s *Server) simpleAnswer(c tele.Context, request string) (string, error) {
	_ = c.Notify(tele.Typing)
	chat := s.getChat(c.Chat(), c.Sender())
	msg := openai.NewChatUserMessage(request)
	system := openai.NewChatSystemMessage(chat.MasterPrompt)
	s.ai.Verbose = s.conf.Verbose
	history := []openai.ChatMessage{system}
	history = append(history, msg)

	response, err := s.ai.CreateChatCompletion(chat.ModelName, history,
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

// generate an answer to given message and send it to the chat
func (s *Server) answer(c tele.Context, message string, image *string) (string, error) {
	_ = c.Notify(tele.Typing)
	chat := s.getChat(c.Chat(), c.Sender())
	history := chat.getConversationContext(&message, image)
	Log.WithField("user", c.Sender().Username).Info("Context=", len(history))

	if chat.Stream {
		chat.mutex.Lock()
		if chat.MessageID != nil {
			_, _ = c.Bot().EditReplyMarkup(tele.StoredMessage{MessageID: *chat.MessageID, ChatID: chat.ChatID}, removeMenu)
			chat.MessageID = nil
		}
		chat.mutex.Unlock()

		return s.getStreamAnswer(chat, c, history)
	}

	return s.getAnswer(chat, c, history, image != nil)
}

func (s *Server) getAnswer(
	chat *Chat,
	c tele.Context,
	history []openai.ChatMessage,
	vision bool,
) (string, error) {
	model := chat.ModelName
	options := openai.ChatCompletionOptions{}
	if model == mGPT4 || !vision {
		options.SetTools(s.getFunctionTools())
	}
	s.ai.Verbose = s.conf.Verbose
	//options.SetMaxTokens(3000)
	if vision && model == "gpt-4-turbo-preview" {
		model = "gpt-4-vision-preview"
	}
	if model == mOllama && len(s.conf.OllamaURL) > 0 {
		s.ai.SetBaseURL(s.conf.OllamaURL)
		s.ai.APIKey = "ollama"
		model = s.conf.OllamaModel
	} else {
		s.ai.SetBaseURL("")
		s.ai.APIKey = s.conf.OpenAIAPIKey
	}

	response, err := s.ai.CreateChatCompletion(model, history,
		options.
			SetUser(userAgent(c.Sender().ID)).
			SetTemperature(chat.Temperature))

	if err != nil {
		Log.WithField("user", c.Sender().Username).Error(err)
		return err.Error(), err
	}

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
		chat.mutex.Lock()
		chat.TotalTokens += response.Usage.TotalTokens
		chat.mutex.Unlock()
		chat.addMessageToHistory(openai.NewChatAssistantMessage(answer))
		s.saveHistory(chat)

		return answer, nil
	}

	return chat.t("No response from API."), nil
}

// summarize summarizes the chat history
func (s *Server) summarize(chatHistory []ChatMessage) (*openai.ChatCompletion, error) {
	msg := openai.NewChatUserMessage("Make a compressed summary of the conversation with the AI. Try to be as brief as possible and highlight key points. Use same language as the user.")
	system := openai.NewChatSystemMessage("Be as brief as possible")

	history := []openai.ChatMessage{system}
	for _, h := range chatHistory {
		if h.Role == openai.ChatMessageRoleTool {
			continue
		}
		history = append(history, openai.ChatMessage{Role: h.Role, Content: []openai.ChatMessageContent{{
			Type: "text", Text: h.Content,
		}}})
	}
	history = append(history, msg)
	Log.Info("Chat history len: ", len(history))

	response, err := s.ai.CreateChatCompletion("gpt-3.5-turbo-16k", history, openai.ChatCompletionOptions{}.SetUser(userAgent(31337)).SetTemperature(0.5))

	if err != nil {
		Log.Error(err)
		return nil, err
	}
	if response.Choices[0].Message.Content == nil {
		return nil, nil
	}

	return &response, nil
}

// getStreamAnswer starts a stream with the given chat and history
func (s *Server) getStreamAnswer(chat *Chat, c tele.Context, history []openai.ChatMessage) (string, error) {
	type completion struct {
		response openai.ChatCompletion
		done     bool
		err      error
	}
	ch := make(chan completion, 1)

	sentMessage := chat.getSentMessage(c)

	model := chat.ModelName
	if model == mOllama && len(s.conf.OllamaURL) > 0 {
		s.ai.SetBaseURL(s.conf.OllamaURL)
		s.ai.APIKey = "ollama"
		model = s.conf.OllamaModel
	} else {
		s.ai.SetBaseURL("")
		s.ai.APIKey = s.conf.OpenAIAPIKey
	}
	//s.ai.Verbose = s.conf.Verbose
	if _, err := s.ai.CreateChatCompletion(model, history,
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
		return err.Error(), err
	}

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
			return comp.err.Error(), comp.err
		}
		if !comp.done {
			// streaming the result, append the response to the result
			if comp.response.Choices[0].Delta.Content != nil {
				if c, err := comp.response.Choices[0].Delta.ContentString(); err == nil {
					result += c
				}
				tokens++
			}
			// every 10 tokens update the message
			if tokens%10 == 0 {
				_, _ = c.Bot().Edit(&sentMessage, result)
			}
		} else {
			// stream is done, send the final result
			if len(comp.response.Choices[0].Message.ToolCalls) > 0 &&
				comp.response.Choices[0].Message.ToolCalls[0].Function.Name != "" {
				result, err := s.handleFunctionCall(chat, c, comp.response.Choices[0].Message)
				if err != nil {
					return err.Error(), err
				}

				_, _ = c.Bot().Edit(&sentMessage, reply+result, "text", &tele.SendOptions{
					ReplyTo:   c.Message(),
					ParseMode: tele.ModeMarkdown,
				}, replyMenu)

				return result, nil
			}

			if len(result) == 0 {
				return "", nil
			}
			_, _ = c.Bot().Edit(&sentMessage, reply+result, "text", &tele.SendOptions{
				ReplyTo:   c.Message(),
				ParseMode: tele.ModeMarkdown,
			}, replyMenu)

			Log.WithField("user", c.Sender().Username).WithField("tokens", tokens).Info("Stream finished")
			chat.mutex.Lock()
			chat.TotalTokens += tokens
			chat.mutex.Unlock()
			chat.addMessageToHistory(openai.NewChatAssistantMessage(result))
			s.saveHistory(chat)

			return result, nil
		}
	}

	return "", nil
}

func (s *Server) saveHistory(chat *Chat) {
	//Log.WithField("user", chat.User.Username).WithField("history", len(chat.History)).Info("Saving chat history")

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
	//Log.WithField("user", chat.User.Username).WithField("history", len(chat.History)).Info("Saved chat history")
	if len(chat.History) < 100 {
		s.db.Save(&chat)
		return
	}

	Log.WithField("user", chat.User.Username).Infof("Chat history for chat ID %d is too long. Summarising...", chat.ID)
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
	Log.WithField("user", chat.User.Username).Infof("Deleting chat history for chat ID %d up to message ID %d", chat.ID, maxID)
	s.db.Where("chat_id = ?", chat.ID).Where("id <= ?", maxID).Delete(&ChatMessage{})

	chat.History = []ChatMessage{{
		Role:      openai.ChatMessageRoleAssistant,
		Content:   &summary,
		ChatID:    chat.ChatID,
		CreatedAt: time.Now(),
	}}

	Log.WithField("user", chat.User.Username).Info("Chat history length after summarising: ", len(chat.History))
	chat.TotalTokens += response.Usage.TotalTokens

	s.db.Save(&chat)
}
