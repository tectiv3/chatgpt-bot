package main

import (
	"fmt"
	"github.com/meinside/openai-go"
	tele "gopkg.in/telebot.v3"
	"strconv"
	"strings"
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

	response, err := s.ai.CreateChatCompletion(s.getModel(chat.ModelName), history,
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
	s.ai.Verbose = s.conf.Verbose
	history := []openai.ChatMessage{system}
	history = append(history, msg)

	response, err := s.ai.CreateChatCompletion(
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

	response, err := s.ai.CreateChatCompletion(mGTP3, history, openai.ChatCompletionOptions{}.SetUser(userAgent(31337)).SetTemperature(0.5))

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
	// reply is a flag to indicate if we need to reply to another message, otherwise it is a voice transcription
	if !reply {
		text = fmt.Sprintf(chat.t("_Transcript:_\n%s\n\n_Answer:_ \n\n"), message)
		sentMessage, _ = c.Bot().Send(c.Recipient(), text, "text", &tele.SendOptions{
			ReplyTo:   c.Message(),
			ParseMode: tele.ModeMarkdown,
		})
		chat.MessageID = &([]string{strconv.Itoa(sentMessage.ID)}[0])
		c.Set("reply", *sentMessage)
	}

	msgPtr := &message
	if len(message) == 0 {
		msgPtr = nil
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
	options := openai.ChatCompletionOptions{}

	model := s.getModel(chat.ModelName)
	s.ai.APIKey = s.conf.OpenAIAPIKey
	options.SetTools(s.getFunctionTools())

	//s.ai.Verbose = s.conf.Verbose
	//options.SetMaxTokens(3000)
	history := chat.getDialog(question)
	Log.WithField("user", c.Sender().Username).WithField("history", len(history)).Info("Answer")
	chat.mutex.Lock()
	if chat.MessageID != nil {
		_, _ = c.Bot().EditReplyMarkup(tele.StoredMessage{MessageID: *chat.MessageID, ChatID: chat.ChatID}, removeMenu)
		chat.MessageID = nil
	}
	chat.mutex.Unlock()
	sentMessage := chat.getSentMessage(c)

	response, err := s.ai.CreateChatCompletion(model, history,
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

	Log.WithField("user", c.Sender().Username).WithField("length", len(answer)).Info("got an answer")

	if len(answer) == 0 {
		return nil
	}

	if len(answer) > 4000 {
		file := tele.FromReader(strings.NewReader(answer))
		_ = c.Send(&tele.Document{File: file, FileName: "answer.txt", MIME: "text/plain"}, replyMenu)
		return nil
	}
	if _, err := c.Bot().Edit(sentMessage, answer, "text", &tele.SendOptions{ParseMode: tele.ModeMarkdown}, replyMenu); err != nil {
		Log.Warn(err)
		if _, err := c.Bot().Edit(sentMessage, answer, replyMenu); err != nil {
			Log.Warn(err)
			_ = c.Send(answer, "text", &tele.SendOptions{ReplyTo: c.Message()})
		}
	}

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

	chat.mutex.Lock()
	if chat.MessageID != nil {
		_, _ = c.Bot().EditReplyMarkup(tele.StoredMessage{MessageID: *chat.MessageID, ChatID: chat.ChatID}, removeMenu)
		chat.MessageID = nil
	}
	chat.mutex.Unlock()

	sentMessage := chat.getSentMessage(c)

	model := s.getModel(chat.ModelName)
	s.ai.APIKey = s.conf.OpenAIAPIKey
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
			// every 10 tokens update the message
			if tokens%10 == 0 {
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

				_, _ = c.Bot().Edit(sentMessage, reply+result, "text", &tele.SendOptions{
					ReplyTo:   c.Message(),
					ParseMode: tele.ModeMarkdown,
				}, replyMenu)

				return nil
			}

			if len(result) == 0 {
				return nil
			}
			_, _ = c.Bot().Edit(sentMessage, reply+result, "text", &tele.SendOptions{
				ReplyTo:   c.Message(),
				ParseMode: tele.ModeMarkdown,
			}, replyMenu)

			Log.WithField("user", c.Sender().Username).WithField("tokens", tokens).Info("Stream finished")
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
