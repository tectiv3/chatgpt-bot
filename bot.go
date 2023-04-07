package main

// bot.go

import (
	"fmt"
	"log"
	"runtime/debug"
	"strings"
	"time"

	openai "github.com/meinside/openai-go"
	tele "gopkg.in/telebot.v3"
)

const (
	intervalSeconds = 1

	cmdStart           = "/start"
	cmdReset           = "/reset"
	msgStart           = "This bot will answer your messages with ChatGPT API"
	msgReset           = "This bots memory erased"
	msgCmdNotSupported = "Unknown command: %s"
	msgTokenCount      = "%d tokens in %d chars"
)

// config struct for loading a configuration file
type config struct {
	// telegram bot api
	TelegramBotToken string `json:"telegram_bot_token"`

	// openai api
	OpenAIAPIKey         string `json:"openai_api_key"`
	OpenAIOrganizationID string `json:"openai_org_id"`

	// other configurations
	AllowedTelegramUsers []string `json:"allowed_telegram_users"`
	Verbose              bool     `json:"verbose,omitempty"`
	Model                string   `json:"openai_model"`
}

// DB contains chat history
type DB struct {
	chats map[int64]Chat
}

type Server struct {
	conf  config
	users map[string]bool
	ai    *openai.Client
	bot   *tele.Bot
	db    DB
}

// Chat is chat history by chatid
type Chat struct {
	history []openai.ChatMessage
}

// launch bot with given parameters
func (self Server) run() {
	pref := tele.Settings{
		Token:  self.conf.TelegramBotToken,
		Poller: &tele.LongPoller{Timeout: 10 * time.Second},
	}

	b, err := tele.NewBot(pref)
	if err != nil {
		log.Fatal(err)
		return
	}
	self.bot = b

	b.Handle("/start", func(c tele.Context) error {
		return c.Send(msgStart, "text", &tele.SendOptions{
			ReplyTo: c.Message(),
		})
	})

	b.Handle("/reset", func(c tele.Context) error {
		self.db.chats[c.Chat().ID] = Chat{history: []openai.ChatMessage{}}
		return c.Send(msgReset, "text", &tele.SendOptions{
			ReplyTo: c.Message(),
		})
	})

	b.Handle(tele.OnText, func(c tele.Context) error {
		go self.onText(c)

		return nil
	})

	b.Handle(tele.OnQuery, func(c tele.Context) error {
		query := c.Query().Text

		if len(query) < 3 {
			return nil
		}

		go func() {
			defer func() {
				if err := recover(); err != nil {
					log.Println(string(debug.Stack()), err)
				}
			}()

			result := &tele.ArticleResult{}
			//     URL:         fmt.Sprintf("https://store.steampowered.com/app/%d/", game.AppID),
			//     Title:       game.Name,
			//     Text:        text,
			//     Description: game.ShortDescription,
			//     ThumbURL:    game.HeaderImage,
			// }

			results := make(tele.Results, 1)
			results[0] = result
			// needed to set a unique string ID for each result
			results[0].SetResultID("sdsd") //strconv.Itoa(game.AppID))

			c.Answer(&tele.QueryResponse{
				Results:   results,
				CacheTime: 100,
			})

		}()

		return nil
	})

	b.Start()
}

func (self Server) onText(c tele.Context) {
	defer func() {
		if err := recover(); err != nil {
			log.Println(string(debug.Stack()), err)
		}
	}()

	if !self.isAllowed(c.Sender().Username) {
		c.Send(fmt.Sprintf("not allowed: %s", c.Sender().Username), "text", &tele.SendOptions{
			ReplyTo: c.Message(),
		})
		return
	}
	// if c.Message().IsReply() {
	//
	// }
	message := c.Message().Payload
	if len(message) == 0 {
		message = c.Message().Text
	}

	if message == "reset" {
		self.db.chats[c.Chat().ID] = Chat{history: []openai.ChatMessage{}}
		c.Send(msgReset, "text", &tele.SendOptions{
			ReplyTo: c.Message(),
		})
		return
	}

	response, err := self.answer(message, c)
	if err != nil {
		return
	}

	if len(response) > 4096 {
		file := tele.FromReader(strings.NewReader(response))
		c.Send(&tele.Message{Document: &tele.Document{File: file}})
		return
	}

	c.Send(response, "text", &tele.SendOptions{
		ReplyTo: c.Message(),
	})
}

// checks if given update is allowed or not
func (self Server) isAllowed(username string) bool {
	_, exists := self.users[username]

	return exists
}

// generate an answer to given message and send it to the chat
func (self Server) answer(message string, c tele.Context) (string, error) {
	c.Notify(tele.Typing)

	msg := openai.NewChatUserMessage(message)

	var chat Chat
	chat, ok := self.db.chats[c.Chat().ID]
	if !ok {
		chat = Chat{history: []openai.ChatMessage{}}
	}
	chat.history = append(chat.history, msg)

	response, err := self.ai.CreateChatCompletion(self.conf.Model, chat.history, openai.ChatCompletionOptions{}.SetUser(userAgent(c.Sender().ID)))

	if err != nil {
		log.Printf("failed to create chat completion: %s", err)
		return "", err
	}
	if self.conf.Verbose {
		log.Printf("[verbose] %s ===> %+v", message, response.Choices)
	}

	c.Notify(tele.Typing)

	var answer string
	if len(response.Choices) > 0 {
		answer = response.Choices[0].Message.Content
	} else {
		answer = "No response from API."
	}

	if self.conf.Verbose {
		log.Printf("[verbose] sending answer: '%s'", answer)
	}

	chat.history = append(chat.history, openai.NewChatAssistantMessage(answer))
	self.db.chats[c.Chat().ID] = chat

	if len(chat.history) > 8 {
		chat.history = chat.history[1:]
	}

	return answer, nil
}

// generate a user-agent value
func userAgent(userID int64) string {
	return fmt.Sprintf("telegram-chatgpt-bot:%d", userID)
}
