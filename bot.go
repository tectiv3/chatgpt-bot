package main

// bot.go

import (
	"fmt"
	"log"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	tele "gopkg.in/telebot.v3"
)

const (
	cmdStart      = "/start"
	cmdReset      = "/reset"
	cmdModel      = "/model"
	cmdTemp       = "/temperature"
	cmdPrompt     = "/prompt"
	cmdPromptCL   = "/defaultprompt"
	cmdStream     = "/stream"
	cmdInfo       = "/info"
	cmdToJapanese = "/jp"
	cmdToEnglish  = "/en"
	msgStart      = "This bot will answer your messages with ChatGPT API"
	msgReset      = "This bots memory erased"
	masterPrompt  = "You are a helpful assistant. You always try to answer truthfully. If you don't know the answer, just say that you don't know, don't try to make up an answer."
)

var (
	menu   = &tele.ReplyMarkup{ResizeKeyboard: true}
	btn3   = tele.Btn{Text: "GPT3", Unique: "btnModel", Data: "gpt-3.5-turbo"}
	btn4   = tele.Btn{Text: "GPT4", Unique: "btnModel", Data: "gpt-4"}
	btn316 = tele.Btn{Text: "GPT3-16k", Unique: "btnModel", Data: "gpt-3.5-turbo-16k"}
	btnT0  = tele.Btn{Text: "0.0", Unique: "btntemp", Data: "0.0"}
	btnT2  = tele.Btn{Text: "0.2", Unique: "btntemp", Data: "0.2"}
	btnT4  = tele.Btn{Text: "0.4", Unique: "btntemp", Data: "0.4"}
	btnT6  = tele.Btn{Text: "0.6", Unique: "btntemp", Data: "0.6"}
	btnT8  = tele.Btn{Text: "0.8", Unique: "btntemp", Data: "0.8"}
	btnT10 = tele.Btn{Text: "1.0", Unique: "btntemp", Data: "1.0"}
)

// launch bot with given parameters
func (s Server) run() {
	pref := tele.Settings{
		Token:  s.conf.TelegramBotToken,
		Poller: &tele.LongPoller{Timeout: 10 * time.Second},
	}

	b, err := tele.NewBot(pref)
	if err != nil {
		log.Fatal(err)
		return
	}
	//b.Use(middleware.Logger())
	b.Use(whitelist(s.conf.AllowedTelegramUsers...))
	s.bot = b

	usage, err := s.getUsageMonth()
	if err != nil {
		log.Println(err)
	}
	log.Printf("Current usage: %0.2f", usage)

	b.Handle(cmdStart, func(c tele.Context) error {
		return c.Send(msgStart, "text", &tele.SendOptions{ReplyTo: c.Message()})
	})

	b.Handle(cmdModel, func(c tele.Context) error {
		menu.Inline(menu.Row(btn3, btn4, btn316))

		return c.Send("Select model", menu)
	})

	b.Handle(cmdTemp, func(c tele.Context) error {
		menu.Inline(menu.Row(btnT0, btnT2, btnT4, btnT6, btnT8, btnT10))
		chat := s.getChat(c.Chat().ID)

		return c.Send(fmt.Sprintf("Set temperature from less random (0.0) to more random (1.0.\nCurrent: %0.2f (default: 0.8)", chat.Temperature), menu)
	})

	b.Handle(cmdPrompt, func(c tele.Context) error {
		query := c.Message().Payload
		if len(query) < 3 {
			return c.Send("Please provide a longer prompt", "text", &tele.SendOptions{
				ReplyTo: c.Message(),
			})
			//return c.Send("Please provide a longer query", "text", &tele.SendOptions{
			//	ReplyTo:     c.Message(),
			//	ReplyMarkup: &tele.ReplyMarkup{ForceReply: true},
			//})
		}

		chat := s.getChat(c.Chat().ID)
		chat.MasterPrompt = query
		s.db.Save(&chat)

		return nil
	})

	b.Handle(cmdPromptCL, func(c tele.Context) error {
		chat := s.getChat(c.Chat().ID)
		chat.MasterPrompt = masterPrompt
		s.db.Save(&chat)

		return c.Send("Default prompt set", "text", &tele.SendOptions{ReplyTo: c.Message()})
	})

	b.Handle(cmdStream, func(c tele.Context) error {
		chat := s.getChat(c.Chat().ID)
		chat.Stream = !chat.Stream
		s.db.Save(&chat)
		status := "disabled"
		if chat.Stream {
			status = "enabled"
		}

		return c.Send("Stream is "+status, "text", &tele.SendOptions{ReplyTo: c.Message()})
	})

	b.Handle(cmdInfo, func(c tele.Context) error {
		chat := s.getChat(c.Chat().ID)
		status := "disabled"
		if chat.Stream {
			status = "enabled"
		}

		usage, err := s.getUsageMonth()
		if err != nil {
			log.Println(err)
		}
		log.Printf("Current usage: %0.2f", usage)

		return c.Send(fmt.Sprintf("Model: %s\nTemperature: %0.2f\nPrompt: %s\nStreaming: %s\nUsage: $%0.2f",
			chat.ModelName, chat.Temperature, chat.MasterPrompt, status, usage,
		),
			"text",
			&tele.SendOptions{ReplyTo: c.Message()},
		)
	})

	b.Handle(cmdToJapanese, func(c tele.Context) error {
		go s.onTranslate(c, "To Japanese: ")

		return nil
	})

	b.Handle(cmdToEnglish, func(c tele.Context) error {
		go s.onTranslate(c, "To English: ")

		return nil
	})

	b.Handle(&btn3, func(c tele.Context) error {
		log.Printf("%s selected", c.Data())
		chat := s.getChat(c.Chat().ID)
		chat.ModelName = c.Data()
		s.db.Save(&chat)

		return c.Edit("Model set to " + c.Data())
	})

	// On inline button pressed (callback)
	b.Handle(&btn316, func(c tele.Context) error {
		log.Printf("%s selected", c.Data())
		chat := s.getChat(c.Chat().ID)
		chat.ModelName = c.Data()
		s.db.Save(&chat)

		return c.Edit("Model set to " + c.Data())
	})

	// On inline button pressed (callback)
	b.Handle(&btnT0, func(c tele.Context) error {
		log.Printf("Temp: %s\n", c.Data())
		chat := s.getChat(c.Chat().ID)
		chat.Temperature, _ = strconv.ParseFloat(c.Data(), 64)
		s.db.Save(&chat)

		return c.Edit("Temperature set to " + c.Data())
	})

	b.Handle(cmdReset, func(c tele.Context) error {
		chat := s.getChat(c.Chat().ID)
		s.deleteHistory(chat.ID)

		return c.Send(msgReset, "text", &tele.SendOptions{ReplyTo: c.Message()})
	})

	b.Handle(tele.OnText, func(c tele.Context) error {
		go s.onText(c)

		return nil
	})

	b.Handle(tele.OnQuery, func(c tele.Context) error {
		query := c.Query().Text
		go s.complete(c, query, false)

		return nil
	})

	b.Handle(tele.OnDocument, func(c tele.Context) error {
		go s.onDocument(c)

		return nil
	})

	b.Handle(tele.OnPhoto, func(c tele.Context) error {
		log.Printf("Got a photo, size %d, caption: %s\n", c.Message().Photo.FileSize, c.Message().Photo.Caption)

		return nil
	})

	b.Handle(tele.OnVoice, func(c tele.Context) error {
		go s.onVoice(c)

		return nil
	})

	b.Start()
}

func (s Server) onDocument(c tele.Context) {
	// body
	log.Printf("Got a file: %d", c.Message().Document.FileSize)
	// c.Message().Photo
}

func (s Server) onText(c tele.Context) {
	defer func() {
		if err := recover(); err != nil {
			log.Println(string(debug.Stack()), err)
		}
	}()

	message := c.Message().Payload
	if len(message) == 0 {
		message = c.Message().Text
	}

	s.complete(c, message, true)
}

func (s Server) onVoice(c tele.Context) {
	defer func() {
		if err := recover(); err != nil {
			log.Println(string(debug.Stack()), err)
		}
	}()

	log.Printf("Got a voice, size %d, caption: %s\n", c.Message().Voice.FileSize, c.Message().Voice.Caption)

	s.handleVoice(c)
}

func (s Server) onTranslate(c tele.Context, prefix string) {
	defer func() {
		if err := recover(); err != nil {
			log.Println(string(debug.Stack()), err)
		}
	}()

	query := c.Message().Payload
	if len(query) < 1 {
		_ = c.Send("Please provide a longer prompt", "text", &tele.SendOptions{
			ReplyTo: c.Message(),
		})

		return
	}

	response, err := s.answer(prefix+query, c)
	if err != nil {
		log.Println(err)
		_ = c.Send(err.Error(), "text", &tele.SendOptions{ReplyTo: c.Message()})

		return
	}

	_ = c.Send(response, "text", &tele.SendOptions{
		ReplyTo:   c.Message(),
		ParseMode: tele.ModeMarkdown,
	})
}

func (s Server) complete(c tele.Context, message string, reply bool) {
	chat := s.getChat(c.Chat().ID)
	if strings.HasPrefix(strings.ToLower(message), "reset") {
		s.deleteHistory(chat.ID)
		_ = c.Send(msgReset, "text", &tele.SendOptions{
			ReplyTo: c.Message(),
		})
		return
	}

	text := "..."
	if !reply {
		text = fmt.Sprintf("_Transcript:_\n%s\n\n_Answer:_\n...", message)
		chat.SentMessage, _ = c.Bot().Send(c.Recipient(), text, "text", &tele.SendOptions{
			ReplyTo:   c.Message(),
			ParseMode: tele.ModeMarkdown,
		})
	}

	response, err := s.answer(message, c)
	if err != nil {
		return
	}
	log.Printf("User: %s. Response length: %d\n", c.Sender().Username, len(response))

	if len(response) == 0 {
		return
	}

	if len(response) > 4096 {
		file := tele.FromReader(strings.NewReader(response))
		_ = c.Send(&tele.Document{File: file, FileName: "answer.txt", MIME: "text/plain"})
		return
	}
	if !reply {
		text = text[:len(text)-3] + response
		if _, err := c.Bot().Edit(chat.SentMessage, text, "text", &tele.SendOptions{
			ReplyTo:   c.Message(),
			ParseMode: tele.ModeMarkdown,
		}); err != nil {
			c.Bot().Edit(chat.SentMessage, text)
		}
		return
	}

	_ = c.Send(response, "text", &tele.SendOptions{
		ReplyTo:   c.Message(),
		ParseMode: tele.ModeMarkdown,
	})
}

// getChat returns chat from db or creates a new one
func (s Server) getChat(chatID int64) Chat {
	var chat Chat
	s.db.FirstOrCreate(&chat, Chat{ChatID: chatID})
	if len(chat.MasterPrompt) == 0 {
		chat.MasterPrompt = masterPrompt
		chat.ModelName = "gpt-3.5-turbo"
		chat.Temperature = 0.8
		s.db.Save(&chat)
	}
	s.db.Find(&chat.History, "chat_id = ?", chat.ID)
	log.Printf("History %d, chatid %d\n", len(chat.History), chat.ID)

	return chat
}

func (s Server) deleteHistory(chatID uint) {
	s.db.Where("chat_id = ?", chatID).Delete(&ChatMessage{})
}

// generate a user-agent value
func userAgent(userID int64) string {
	return fmt.Sprintf("telegram-chatgpt-bot:%d", userID)
}

// Restrict returns a middleware that handles a list of provided
// usernames with the logic defined by In and Out functions.
// If the username is found in the Usernames field, In function will be called,
// otherwise Out function will be called.
func Restrict(v RestrictConfig) tele.MiddlewareFunc {
	return func(next tele.HandlerFunc) tele.HandlerFunc {
		if v.In == nil {
			v.In = next
		}
		if v.Out == nil {
			v.Out = next
		}
		return func(c tele.Context) error {
			for _, username := range v.Usernames {
				if username == c.Sender().Username {
					return v.In(c)
				}
			}
			return v.Out(c)
		}
	}
}

// Whitelist returns a middleware that skips the update for users
// NOT specified in the usernames field.
func whitelist(usernames ...string) tele.MiddlewareFunc {
	return func(next tele.HandlerFunc) tele.HandlerFunc {
		return Restrict(RestrictConfig{
			Usernames: usernames,
			In:        next,
			Out: func(c tele.Context) error {
				return c.Send(fmt.Sprintf("not allowed: %s", c.Sender().Username), "text", &tele.SendOptions{ReplyTo: c.Message()})
			},
		})(next)
	}
}
