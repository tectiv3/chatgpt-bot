package main

import (
	"encoding/json"
	"fmt"
	"github.com/meinside/openai-go"
	tele "gopkg.in/telebot.v3"
	"io"
	"log"
	"net/http"
	"time"
)

// generate an answer to given message and send it to the chat
func (s *Server) answer(message string, c tele.Context, image *string) (string, error) {
	_ = c.Notify(tele.Typing)
	chat := s.getChat(c.Chat().ID, c.Sender().Username)
	msg := openai.NewChatUserMessage(message)
	system := openai.NewChatSystemMessage(chat.MasterPrompt)

	chat.History = append(chat.History, ChatMessage{Role: msg.Role, Content: &message, ChatID: chat.ChatID, CreatedAt: time.Now()})
	history := []openai.ChatMessage{system}
	for _, h := range chat.History {
		if h.CreatedAt.After(time.Now().AddDate(0, 0, -int(chat.ConversationAge))) {
			content := []openai.ChatMessageContent{{Type: "text", Text: h.Content}}
			if image != nil && h.Content == &message {
				content = append(content, openai.NewChatMessageContentWithImageURL(*image))
			}
			history = append(history, openai.ChatMessage{Role: h.Role, Content: content})
		}
	}
	log.Printf("Chat history %d\n", len(history))

	if chat.Stream && image == nil {
		return s.launchStream(chat, c, history)
	}
	options := openai.ChatCompletionOptions{}
	if image == nil {
		options.SetTools(s.getTools())
	}
	s.ai.Verbose = s.conf.Verbose
	//options.SetMaxTokens(3000)
	model := chat.ModelName
	if image != nil {
		model = "gpt-4-vision-preview"
	}
	response, err := s.ai.CreateChatCompletion(model, history,
		options.
			SetUser(userAgent(c.Sender().ID)).
			SetTemperature(chat.Temperature))

	if err != nil {
		log.Printf("failed to create chat completion: %s", err)
		return err.Error(), err
	}
	if s.conf.Verbose {
		log.Printf("[verbose] %s ===> %+v", message, response.Choices)
	}

	_ = c.Notify(tele.Typing)

	result := response.Choices[0].Message
	if len(result.ToolCalls) > 0 {
		return s.handleFunctionCall(c, result)
	}

	var answer string
	if len(response.Choices) > 0 {
		answer, err = response.Choices[0].Message.ContentString()
		if err != nil {
			log.Printf("failed to get content string: %s", err)
			return err.Error(), err
		}
		chat.TotalTokens += response.Usage.TotalTokens
		s.saveHistory(chat, answer)
	} else {
		answer = "No response from API."
	}

	if s.conf.Verbose {
		log.Printf("[verbose] sending answer: '%s'", answer)
	}

	return answer, nil
}

func (s *Server) summarize(chatHistory []ChatMessage) (*openai.ChatCompletion, error) {
	msg := openai.NewChatUserMessage("Make a compressed summary of the conversation with the AI. Try to be as brief as possible and highlight key points. Use same language as the user.")
	system := openai.NewChatSystemMessage("Be as brief as possible")

	history := []openai.ChatMessage{system}
	for _, h := range chatHistory {
		history = append(history, openai.ChatMessage{Role: h.Role, Content: []openai.ChatMessageContent{{
			Type: "text", Text: h.Content,
		}}})
	}
	history = append(history, msg)

	log.Printf("Chat history %d\n", len(history))

	response, err := s.ai.CreateChatCompletion("gpt-3.5-turbo-16k", history, openai.ChatCompletionOptions{}.SetUser(userAgent(31337)).SetTemperature(0.5))

	if err != nil {
		log.Printf("failed to create chat completion: %s", err)
		return nil, err
	}
	if response.Choices[0].Message.Content == nil {
		return nil, nil
	}

	return &response, nil
}

// get billing usage
func (s *Server) getUsageMonth() (float64, error) {
	now := time.Now()
	//firstDay := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	//lastDay := firstDay.AddDate(0, 1, -1)

	client := &http.Client{}
	client.Timeout = 10 * time.Second

	req, err := http.NewRequest("GET", "https://api.openai.com/v1/usage", nil)
	if err != nil {
		return 0, err
	}

	query := req.URL.Query()
	query.Add("date", now.Format("2006-01-02"))
	//query.Add("end_date", lastDay.Format("2006-01-02"))
	req.URL.RawQuery = query.Encode()

	req.Header.Add("Authorization", "Bearer "+s.conf.OpenAIAPIKey)

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		// dump response body
		if body, err := io.ReadAll(resp.Body); err == nil {
			log.Printf("Response body: %s", string(body))
		}
		return 0, fmt.Errorf("http status %d", resp.StatusCode)
	}

	var usageData UsageResponseBody
	err = json.NewDecoder(resp.Body).Decode(&usageData)
	if err != nil {
		return 0, err
	}

	return usageData.CurrentUsageUsd / 100, nil
}

func (s *Server) launchStream(chat Chat, c tele.Context, history []openai.ChatMessage) (string, error) {
	data := make(chan openai.ChatCompletion)
	done := make(chan error)
	defer close(data)
	defer close(done)

	//type completion struct {
	//	response openai.ChatCompletion
	//	done     bool
	//	err      error
	//}
	//ch := make(chan completion, 1)
	//
	//SetStream(func(response openai.ChatCompletion, done bool, err error) {
	//	ch <- completion{response: response, done: done, err: err}
	//	if done {
	//		toolCall := response.Choices[0].Message.ToolCalls[0]
	//		function := toolCall.Function
	//
	//		// parse returned arguments into a struct
	//		type parsed struct {
	//			Locations []string `json:"locations"`
	//			Unit      string   `json:"unit"`
	//		}
	//		var arguments parsed
	//		if err := toolCall.ArgumentsInto(&arguments); err != nil {
	//			t.Errorf("failed to parse arguments into struct: %s", err)
	//		} else {
	//			t.Logf("will call %s(%+v, \"%s\")", function.Name, arguments.Locations, arguments.Unit)
	//
	//			// NOTE: get your local function's result with the generated arguments
	//		}
	//
	//		close(ch)
	//	}
	//})
	_, err := s.ai.CreateChatCompletion(chat.ModelName, history,
		openai.ChatCompletionOptions{}.
			SetTools(s.getTools()).
			SetToolChoice(openai.ChatCompletionToolChoiceAuto).
			SetUser(userAgent(c.Sender().ID)).
			SetTemperature(chat.Temperature).
			SetStream(func(r openai.ChatCompletion, d bool, e error) {
				if d {
					done <- e
				} else {
					data <- r
				}
			}))
	if err != nil {
		return err.Error(), err
	}

	result := ""
	reply := ""
	SentMessage := tele.Message{}
	if c.Get("reply") != nil {
		reply = c.Get("reply").(tele.Message).Text + "\n"
		SentMessage = c.Get("reply").(tele.Message)
	} else {
		msgPointer, _ := c.Bot().Send(c.Recipient(), "...", "text", &tele.SendOptions{
			ReplyTo: c.Message(),
		})
		SentMessage = *msgPointer
	}
	tokens := 0
	var msg *openai.ChatMessage
	for {
		select {
		case payload := <-data:
			if payload.Choices[0].Delta.Content != nil {
				if c, err := payload.Choices[0].Delta.ContentString(); err == nil {
					result += c
				}
				tokens++
			}
			// every 10 tokens update the message
			if tokens%10 == 0 {
				_, _ = c.Bot().Edit(&SentMessage, result)
			}
			if len(payload.Choices[0].Message.ToolCalls) > 0 {
				msg = &payload.Choices[0].Message
			}

		case err := <-done:
			if msg != nil {
				if len(msg.ToolCalls) > 0 {
					result, err = s.handleFunctionCall(c, *msg)

					_, _ = c.Bot().Edit(&SentMessage, reply+result, "text", &tele.SendOptions{
						ReplyTo:   c.Message(),
						ParseMode: tele.ModeMarkdown,
					}, replyMenu)
					return "", nil
				}
			}

			if len(result) == 0 {
				return "", err
			}
			_, _ = c.Bot().Edit(&SentMessage, reply+result, "text", &tele.SendOptions{
				ReplyTo:   c.Message(),
				ParseMode: tele.ModeMarkdown,
			}, replyMenu)
			log.Println("Stream total tokens: ", tokens)
			chat.TotalTokens += tokens
			if chat.Voice {
				s.sendAudio(c, result)
			}
			s.saveHistory(chat, result)

			return "", err
		}
	}
}

func (s *Server) getTools() []openai.ChatCompletionTool {
	return []openai.ChatCompletionTool{
		openai.NewChatCompletionTool(
			"set_reminder",
			"Set a reminder to do something at a specific time.",
			openai.NewToolFunctionParameters().
				AddPropertyWithDescription("reminder", "string", "A reminder of what to do, e.g. 'buy groceries'").
				AddPropertyWithDescription("time", "number", "A time at which to be reminded in minutes from now, e.g. 1440").
				SetRequiredParameters([]string{"reminder", "time"})),
		openai.NewChatCompletionTool(
			"make_summary",
			"Make a summary of a web page.",
			openai.NewToolFunctionParameters().
				AddPropertyWithDescription("url", "string", "A valid URL to a web page").
				SetRequiredParameters([]string{"url"})),
		openai.NewChatCompletionTool(
			"get_crypto_rate",
			"Get the current rate of various crypto currencies",
			openai.NewToolFunctionParameters().
				AddPropertyWithDescription("asset", "string", "Asset of the crypto").
				SetRequiredParameters([]string{"asset"})),
	}
}

func (s *Server) saveHistory(chat Chat, answer string) {
	msg := openai.NewChatAssistantMessage(answer)
	chat.History = append(chat.History, ChatMessage{Role: msg.Role, Content: &answer, ChatID: chat.ChatID})
	log.Printf("chat history len: %d", len(chat.History))

	// iterate over history
	// drop messages that are older than chat.ConversationAge days
	var history []ChatMessage
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
	log.Printf("chat history len: %d", len(chat.History))

	if len(chat.History) > 100 {
		log.Printf("Chat history for chat ID %d is too long. Summarising...\n", chat.ID)
		response, err := s.summarize(chat.History)
		if err != nil {
			log.Println("Failed to summarise chat history: ", err)
			return
		}
		summary, _ := response.Choices[0].Message.ContentString()

		if s.conf.Verbose {
			log.Println("Summary: ", summary)
		}
		maxID := chat.History[len(chat.History)-3].ID
		log.Printf("Deleting chat history for chat ID %d up to message ID %d\n", chat.ID, maxID)
		s.db.Where("chat_id = ?", chat.ID).Where("id <= ?", maxID).Delete(&ChatMessage{})
		msg = openai.NewChatUserMessage(summary)
		chat.History = []ChatMessage{{Role: msg.Role, Content: &summary, ChatID: chat.ChatID}}
		log.Println("Chat history after summarising: ", len(chat.History))
		chat.TotalTokens += response.Usage.TotalTokens
	}

	s.db.Save(&chat)
}
