package main

import (
	"encoding/json"
	"github.com/meinside/openai-go"
	tele "gopkg.in/telebot.v3"
	"log"
	"net/http"
	"time"
)

// generate an answer to given message and send it to the chat
func (s Server) answer(message string, c tele.Context) (string, error) {
	_ = c.Notify(tele.Typing)
	chat := s.getChat(c.Chat().ID, c.Sender().Username)
	msg := openai.NewChatUserMessage(message)
	system := openai.NewChatSystemMessage(chat.MasterPrompt)

	chat.History = append(chat.History, ChatMessage{Role: msg.Role, Content: msg.Content, ChatID: chat.ChatID})
	history := []openai.ChatMessage{system}
	for _, h := range chat.History {
		history = append(history, openai.ChatMessage{Role: h.Role, Content: h.Content})
	}
	log.Printf("Chat history %d\n", len(history))

	if chat.Stream {
		return s.launchStream(chat, c, history)
	}
	options := openai.ChatCompletionOptions{}
	if chat.ModelName == "gpt-3.5-turbo-16k" {
		options.
			SetFunctions([]openai.ChatCompletionFunction{
				openai.NewChatCompletionFunction(
					"set_reminder",
					"Set a reminder to do something at a specific time.",
					map[string]any{
						"type": "object",
						"properties": map[string]any{
							"reminder": map[string]any{
								"type":        "string",
								"description": "A reminder of what to do, e.g. 'buy groceries'.",
							},
							"time": map[string]any{
								"type":        "number",
								"description": "A time at which to be reminded in minutes from now, e.g. 1440.",
							},
						},
						"required": []string{"reminder", "time"},
					},
				),
				openai.NewChatCompletionFunction(
					"make_summary",
					"Make a summary of a web page.",
					map[string]any{
						"type": "object",
						"properties": map[string]any{
							"url": map[string]any{
								"type":        "string",
								"description": "A valid URL to a web page.",
							},
						},
						"required": []string{"url"},
					},
				),
			}).
			SetFunctionCall(openai.ChatCompletionFunctionCallAuto)
	}
	response, err := s.ai.CreateChatCompletion(chat.ModelName, history,
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
	if result.FunctionCall != nil {
		return s.handleFunctionCall(c, result)
	}

	var answer string
	if len(response.Choices) > 0 {
		answer = *response.Choices[0].Message.Content
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

func (s Server) summarize(chatHistory []ChatMessage) (*openai.ChatCompletion, error) {
	msg := openai.NewChatUserMessage("Make a compressed summary of the conversation with the AI. Try to be as brief as possible and highlight key points. Use same language as the user.")
	system := openai.NewChatSystemMessage("Be as brief as possible")

	history := []openai.ChatMessage{system}
	for _, h := range chatHistory {
		history = append(history, openai.ChatMessage{Role: h.Role, Content: h.Content})
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
func (s Server) getUsageMonth() (float64, error) {
	now := time.Now()
	firstDay := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	lastDay := firstDay.AddDate(0, 1, -1)

	client := &http.Client{}
	req, err := http.NewRequest("GET", "https://api.openai.com/dashboard/billing/usage", nil)
	if err != nil {
		return 0, err
	}

	query := req.URL.Query()
	query.Add("start_date", firstDay.Format("2006-01-02"))
	query.Add("end_date", lastDay.Format("2006-01-02"))
	req.URL.RawQuery = query.Encode()

	req.Header.Add("Authorization", "Bearer "+s.conf.OpenAIAPIKey)

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	var billingData BillingData
	err = json.NewDecoder(resp.Body).Decode(&billingData)
	if err != nil {
		return 0, err
	}

	return billingData.TotalUsage / 100, nil
}

func (s Server) launchStream(chat Chat, c tele.Context, history []openai.ChatMessage) (string, error) {
	data := make(chan openai.ChatCompletion)
	done := make(chan error)
	defer close(data)
	defer close(done)

	options := openai.ChatCompletionOptions{}
	if chat.ModelName == "gpt-3.5-turbo-16k" {
		options.
			SetFunctions([]openai.ChatCompletionFunction{
				openai.NewChatCompletionFunction(
					"set_reminder",
					"Set a reminder to do something at a specific time.",
					map[string]any{
						"type": "object",
						"properties": map[string]any{
							"reminder": map[string]any{
								"type":        "string",
								"description": "A reminder of what to do, e.g. 'buy groceries'.",
							},
							"time": map[string]any{
								"type":        "number",
								"description": "A time at which to be reminded in minutes from now, e.g. 1440.",
							},
						},
						"required": []string{"reminder", "time"},
					},
				),
				openai.NewChatCompletionFunction(
					"make_summary",
					"Make a summary of a web page.",
					map[string]any{
						"type": "object",
						"properties": map[string]any{
							"url": map[string]any{
								"type":        "string",
								"description": "A valid URL to a web page.",
							},
						},
						"required": []string{"url"},
					},
				),
			}).
			SetFunctionCall(openai.ChatCompletionFunctionCallAuto)
	}

	_, err := s.ai.CreateChatCompletion(chat.ModelName, history,
		options.
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
	if chat.SentMessage == nil {
		chat.SentMessage, _ = c.Bot().Send(c.Recipient(), "...", "text", &tele.SendOptions{
			ReplyTo: c.Message(),
		})
	}
	result := ""
	tokens := 0
	var msg *openai.ChatMessage
	for {
		select {
		case payload := <-data:
			if payload.Choices[0].Delta.Content != nil {
				result += *payload.Choices[0].Delta.Content
			}
			tokens++
			// every 10 tokens update the message
			if tokens%10 == 0 {
				c.Bot().Edit(chat.SentMessage, result)
			}
			result := payload.Choices[0].Delta
			if result.FunctionCall != nil && result.FunctionCall.Name != "" {
				msg = &result
			}

		case err := <-done:
			if msg != nil {
				if msg.FunctionCall != nil {
					return s.handleFunctionCall(c, *msg)
				}
			}
			if len(result) == 0 {
				return "", err
			}
			c.Bot().Edit(chat.SentMessage, result, "text", &tele.SendOptions{
				ReplyTo:   c.Message(),
				ParseMode: tele.ModeMarkdown,
			})
			log.Println("Stream total tokens: ", tokens)
			chat.TotalTokens += tokens
			s.saveHistory(chat, result)

			return "", err
		}
	}
}

func (s Server) saveHistory(chat Chat, answer string) {
	msg := openai.NewChatAssistantMessage(answer)
	chat.History = append(chat.History, ChatMessage{Role: msg.Role, Content: msg.Content, ChatID: chat.ChatID})
	log.Printf("chat history len: %d", len(chat.History))

	if len(chat.History) > 8 {
		log.Printf("Chat history for chat ID %d is too long. Summarising...\n", chat.ID)
		response, err := s.summarize(chat.History)
		if err != nil {
			log.Println("Failed to summarise chat history: ", err)
			return
		}
		summary := *response.Choices[0].Message.Content

		if s.conf.Verbose {
			log.Println("Summary: ", summary)
		}
		maxID := chat.History[len(chat.History)-3].ID
		log.Printf("Deleting chat history for chat ID %d up to message ID %d\n", chat.ID, maxID)
		s.db.Where("chat_id = ?", chat.ID).Where("id <= ?", maxID).Delete(&ChatMessage{})
		msg = openai.NewChatUserMessage(summary)
		chat.History = []ChatMessage{{Role: msg.Role, Content: msg.Content, ChatID: chat.ChatID}}
		log.Println("Chat history after summarising: ", len(chat.History))
		chat.TotalTokens += response.Usage.TotalTokens
	}
	s.db.Save(&chat)
}
