package main

import (
	"fmt"
	"github.com/google/uuid"
	"github.com/tectiv3/chatgpt-bot/i18n"
	"strconv"
	"time"

	tele "gopkg.in/telebot.v3"
)

const (
	cmdStart      = "/start"
	cmdReset      = "/reset"
	cmdModel      = "/model"
	cmdTemp       = "/temperature"
	cmdPrompt     = "/prompt"
	cmdAge        = "/age"
	cmdPromptCL   = "/defaultprompt"
	cmdStream     = "/stream"
	cmdStop       = "/stop"
	cmdVoice      = "/voice"
	cmdInfo       = "/info"
	cmdToJapanese = "/ja"
	cmdToEnglish  = "/en"
	cmdToRussian  = "/ru"
	cmdToItalian  = "/it"
	cmdToSpanish  = "/es"
	cmdToChinese  = "/cn"
	cmdDdg        = "/ddg"
	cmdUsers      = "/users"
	cmdAddUser    = "/add"
	cmdDelUser    = "/del"
	msgStart      = "This bot will answer your messages with ChatGPT API"
	masterPrompt  = "You are a helpful assistant. You always try to answer truthfully. If you don't know the answer, just say that you don't know, don't try to make up an answer. Don't explain yourself. Do not introduce yourself, just answer the user concisely."
	mOllama       = "ollama"
	mGroq         = "groq"
	mGTP3         = "gpt-4o-mini"
	openAILatest  = "openAILatest"
)

var (
	menu       = &tele.ReplyMarkup{ResizeKeyboard: true}
	replyMenu  = &tele.ReplyMarkup{ResizeKeyboard: true, OneTimeKeyboard: true}
	removeMenu = &tele.ReplyMarkup{RemoveKeyboard: true}
	btn3       = tele.Btn{Text: "GPT3", Unique: "btnModel", Data: mGTP3}
	btn4       = tele.Btn{Text: "GPT4", Unique: "btnModel", Data: openAILatest}
	btnT0      = tele.Btn{Text: "0.0", Unique: "btntemp", Data: "0.0"}
	btnT2      = tele.Btn{Text: "0.2", Unique: "btntemp", Data: "0.2"}
	btnT4      = tele.Btn{Text: "0.4", Unique: "btntemp", Data: "0.4"}
	btnT6      = tele.Btn{Text: "0.6", Unique: "btntemp", Data: "0.6"}
	btnT8      = tele.Btn{Text: "0.8", Unique: "btntemp", Data: "0.8"}
	btnT10     = tele.Btn{Text: "1.0", Unique: "btntemp", Data: "1.0"}
	btnReset   = tele.Btn{Text: "New Thread", Unique: "btnreset", Data: "r"}
	btnEmpty   = tele.Btn{Text: "", Data: "no_data"}
)

func init() {
	replyMenu.Inline(menu.Row(btnReset))
	removeMenu.Inline(menu.Row(btnEmpty))
}

// run will launch bot with given parameters
func (s *Server) run() {
	b, err := tele.NewBot(tele.Settings{
		Token:  s.conf.TelegramBotToken,
		URL:    s.conf.TelegramServerURL,
		Poller: &tele.LongPoller{Timeout: 30 * time.Second},
	})
	if err != nil {
		Log.Fatal(err)
		return
	}
	//if done, err := b.Logout(); err != nil {
	//	log.Fatal(err)
	//	return
	//} else {
	//	log.Println("Logout: ", done)
	//	return
	//}

	//b.Use(middleware.Logger())
	s.loadUsers()

	s.Lock()
	b.Use(s.whitelist())
	s.bot = b
	s.Unlock()

	b.Handle(cmdStart, func(c tele.Context) error {
		return c.Send(l.GetWithLocale(c.Sender().LanguageCode, msgStart), "text", &tele.SendOptions{ReplyTo: c.Message()})
	})

	b.Handle(cmdModel, func(c tele.Context) error {
		chat := s.getChat(c.Chat(), c.Sender())
		model := c.Message().Payload
		if model == "" {
			menu.Inline(menu.Row(btn3, btn4)) //, btn5,))

			return c.Send(chat.t("Select model"), menu)
		}
		Log.WithField("user", c.Sender().Username).Info("Selected model ", model)
		chat.ModelName = model
		chat.Stream = true
		s.db.Save(&chat)

		return c.Send(chat.t("Model set to {{.model}}", &i18n.Replacements{"model": model}))
	})

	b.Handle(cmdTemp, func(c tele.Context) error {
		menu.Inline(menu.Row(btnT0, btnT2, btnT4, btnT6, btnT8, btnT10))
		chat := s.getChat(c.Chat(), c.Sender())

		return c.Send(fmt.Sprintf(chat.t("Set temperature from less random (0.0) to more random (1.0).\nCurrent: %0.2f (default: 0.8)"), chat.Temperature), menu)
	})

	b.Handle(cmdAge, func(c tele.Context) error {
		chat := s.getChat(c.Chat(), c.Sender())
		age, err := strconv.Atoi(c.Message().Payload)
		if err != nil {
			return c.Send(chat.t("Please provide a number"), "text", &tele.SendOptions{
				ReplyTo: c.Message(),
			})
		}
		chat.ConversationAge = int64(age)
		s.db.Save(&chat)

		return c.Send(fmt.Sprintf(chat.t("Conversation age set to %d days"), age), "text", &tele.SendOptions{ReplyTo: c.Message()})
	})

	b.Handle(cmdPrompt, func(c tele.Context) error {
		chat := s.getChat(c.Chat(), c.Sender())
		query := c.Message().Payload
		if len(query) < 3 {
			return c.Send(chat.t("Please provide a longer prompt"), "text", &tele.SendOptions{ReplyTo: c.Message()})
		}

		chat.MasterPrompt = query
		s.db.Save(&chat)

		return c.Send(chat.t("Prompt set"), "text", &tele.SendOptions{ReplyTo: c.Message()})
	})

	b.Handle(cmdPromptCL, func(c tele.Context) error {
		chat := s.getChat(c.Chat(), c.Sender())
		chat.MasterPrompt = masterPrompt
		s.db.Save(&chat)

		return c.Send(chat.t("Default prompt set"), "text", &tele.SendOptions{ReplyTo: c.Message()})
	})

	b.Handle(cmdStream, func(c tele.Context) error {
		chat := s.getChat(c.Chat(), c.Sender())
		chat.Stream = !chat.Stream
		s.db.Save(&chat)
		status := "disabled"
		if chat.Stream {
			status = "enabled"
		}
		text := chat.t("Stream is {{.status}}", &i18n.Replacements{"status": chat.t(status)})

		return c.Send(text, "text", &tele.SendOptions{ReplyTo: c.Message()})
	})

	b.Handle(cmdVoice, func(c tele.Context) error {
		go s.pageToSpeech(c, c.Message().Payload)

		return c.Send("Downloading page", "text", &tele.SendOptions{ReplyTo: c.Message()})
	})

	b.Handle(cmdStop, func(c tele.Context) error {

		return nil
	})

	b.Handle(cmdInfo, func(c tele.Context) error {
		chat := s.getChat(c.Chat(), c.Sender())
		status := "disabled"
		if chat.Stream {
			status = "enabled"
		}
		status = chat.t(status)

		return c.Send(fmt.Sprintf("Model: %s\nTemperature: %0.2f\nPrompt: %s\nStreaming: %s\nConvesation Age (days): %d",
			s.getModel(chat.ModelName), chat.Temperature, chat.MasterPrompt, status, chat.ConversationAge,
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

	b.Handle(cmdToRussian, func(c tele.Context) error {
		go s.onTranslate(c, "To Russian: ")

		return nil
	})

	b.Handle(cmdToItalian, func(c tele.Context) error {
		go s.onTranslate(c, "To Italian: ")

		return nil
	})

	b.Handle(cmdToSpanish, func(c tele.Context) error {
		go s.onTranslate(c, "To Spanish: ")

		return nil
	})

	b.Handle(cmdToChinese, func(c tele.Context) error {
		go s.onTranslate(c, "To Chinese: ")

		return nil
	})

	b.Handle("/image", func(c tele.Context) error {
		chat := s.getChat(c.Chat(), c.Sender())
		msg := chat.getSentMessage(c)
		msg, _ = c.Bot().Edit(msg, "Generating...")
		if err := s.textToImage(c, c.Message().Payload, true); err != nil {
			_, _ = c.Bot().Edit(msg, "Generating...")
			return c.Send("Error: " + err.Error())
		}
		_ = c.Bot().Delete(msg)

		return nil
	})

	b.Handle("/lang", func(c tele.Context) error {
		chat := s.getChat(c.Chat(), c.Sender())
		if c.Message().Payload == "" {
			return c.Send("Language code (e.g. ru) is required", "text", &tele.SendOptions{ReplyTo: c.Message()})
		}
		chat.Lang = c.Message().Payload
		s.db.Save(&chat)
		return c.Send(fmt.Sprintf("Language set to %s", chat.Lang), "text", &tele.SendOptions{ReplyTo: c.Message()})
	})

	b.Handle(&btn3, func(c tele.Context) error {
		Log.WithField("user", c.Sender().Username).Info("Selected model ", c.Data())
		chat := s.getChat(c.Chat(), c.Sender())
		chat.ModelName = c.Data()
		s.db.Save(&chat)

		return c.Edit(chat.t("Model set to {{.model}}", &i18n.Replacements{"model": c.Data()}))
	})

	b.Handle(&btnT0, func(c tele.Context) error {
		Log.WithField("user", c.Sender().Username).Info("Selected temperature ", c.Data())
		chat := s.getChat(c.Chat(), c.Sender())
		chat.Temperature, _ = strconv.ParseFloat(c.Data(), 64)
		s.db.Save(&chat)

		return c.Edit(chat.t("Temperature set to {{.temp}}", &i18n.Replacements{"temp": c.Data()}))
	})

	b.Handle(&btnReset, func(c tele.Context) error {
		chat := s.getChat(c.Chat(), c.Sender())
		chat.MessageID = nil
		s.db.Save(&chat)
		s.deleteHistory(chat.ID)

		return c.Edit(removeMenu)
	})

	b.Handle(cmdReset, func(c tele.Context) error {
		chat := s.getChat(c.Chat(), c.Sender())
		chat.MessageID = nil
		s.db.Save(&chat)
		s.deleteHistory(chat.ID)

		return nil
	})

	b.Handle(tele.OnText, func(c tele.Context) error {
		go s.onText(c)

		return nil
	})

	b.Handle(tele.OnQuery, func(c tele.Context) error {
		query := c.Query().Text
		article := &tele.ArticleResult{Title: "N/A"}
		result, err := s.anonymousAnswer(c, query)
		if err != nil {
			article = &tele.ArticleResult{
				Title: "Error!",
				Text:  err.Error(),
			}
		} else {
			article = &tele.ArticleResult{
				Title: query,
				Text:  result,
			}
		}

		results := make(tele.Results, 1)
		results[0] = article
		// needed to set a unique string ID for each result
		id := uuid.New()
		results[0].SetResultID(id.String())

		c.Answer(&tele.QueryResponse{Results: results, CacheTime: 100})
		return nil
	})

	b.Handle(tele.OnDocument, func(c tele.Context) error {
		chat := s.getChat(c.Chat(), c.Sender())
		go s.onDocument(c)

		return c.Send(chat.t("Processing document. Please wait..."))
	})

	b.Handle(tele.OnVoice, func(c tele.Context) error {
		go s.onVoice(c)

		return nil
	})

	b.Handle(tele.OnPhoto, func(c tele.Context) error {
		go s.onPhoto(c)

		return nil
	})

	b.Handle(cmdUsers, func(c tele.Context) error {
		if !in_array(c.Sender().Username, s.conf.AllowedTelegramUsers) {
			return nil
		}
		return s.onGetUsers(c)
	})

	b.Handle(cmdAddUser, func(c tele.Context) error {
		if !in_array(c.Sender().Username, s.conf.AllowedTelegramUsers) {
			return nil
		}
		name := c.Message().Payload
		if len(name) < 3 {
			return c.Send("Username is too short", "text", &tele.SendOptions{
				ReplyTo: c.Message(),
			})
		}
		s.addUser(name)
		s.loadUsers()

		return s.onGetUsers(c)
	})

	b.Handle(cmdDelUser, func(c tele.Context) error {
		if !in_array(c.Sender().Username, s.conf.AllowedTelegramUsers) {
			return nil
		}
		name := c.Message().Payload
		if len(name) < 3 {
			return c.Send("Username is too short", "text", &tele.SendOptions{
				ReplyTo: c.Message(),
			})
		}
		s.delUser(name)
		s.loadUsers()

		return s.onGetUsers(c)
	})

	b.Start()
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
func (s *Server) whitelist() tele.MiddlewareFunc {
	return func(next tele.HandlerFunc) tele.HandlerFunc {
		return Restrict(RestrictConfig{
			Usernames: s.users,
			In:        next,
			Out: func(c tele.Context) error {
				return c.Send(fmt.Sprintf("not allowed: %s", c.Sender().Username), "text", &tele.SendOptions{ReplyTo: c.Message()})
			},
		})(next)
	}
}
