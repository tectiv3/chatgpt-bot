package main

import (
	"fmt"
	"github.com/tectiv3/chatgpt-bot/i18n"
	"github.com/tectiv3/chatgpt-bot/tools"
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
	cmdToChinese  = "/cn"
	cmdDdg        = "/ddg"
	cmdUsers      = "/users"
	cmdAddUser    = "/add"
	cmdDelUser    = "/del"
	cmdChain      = "/chain"
	msgStart      = "This bot will answer your messages with ChatGPT API"
	masterPrompt  = "You are a helpful assistant. You always try to answer truthfully. If you don't know the answer, just say that you don't know, don't try to make up an answer. Don't explain yourself. Do not introduce yourself, just answer the user concisely."
	mOllama       = "ollama"
	mGPT4         = "gpt-4-turbo"
)

var (
	menu       = &tele.ReplyMarkup{ResizeKeyboard: true}
	replyMenu  = &tele.ReplyMarkup{ResizeKeyboard: true, OneTimeKeyboard: true}
	removeMenu = &tele.ReplyMarkup{RemoveKeyboard: true}
	btn3       = tele.Btn{Text: "GPT3", Unique: "btnModel", Data: "gpt-3.5-turbo"}
	btn4       = tele.Btn{Text: "GPT4", Unique: "btnModel", Data: mGPT4}
	btn5       = tele.Btn{Text: "Ollama", Unique: "btnModel", Data: mOllama}
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
		menu.Inline(menu.Row(btn3, btn4)) //, btn5,))

		return c.Send(chat.t("Select model"), menu)
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
		return s.textToSpeech(c, c.Message().Payload, "fr")
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
			chat.ModelName, chat.Temperature, chat.MasterPrompt, status, chat.ConversationAge,
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

	b.Handle(cmdToChinese, func(c tele.Context) error {
		go s.onTranslate(c, "To Chinese: ")

		return nil
	})

	b.Handle(cmdDdg, func(c tele.Context) error {
		param, err := tools.NewSearchParam(c.Message().Payload, "wt-wt")
		if err != nil {
			return c.Send("Error: " + err.Error())
		}
		result := tools.Search(param)
		if result.IsErr() {
			return c.Send("Error: " + result.Error().Error())
		}
		res := *result.Unwrap()
		if len(res) == 0 {
			return c.Send("No results found", "text", &tele.SendOptions{ReplyTo: c.Message()})
		}

		return c.Send(fmt.Sprintf("%s\n%s\n%s", res[0].Title, res[0].Snippet, res[0].Link), "text", &tele.SendOptions{ReplyTo: c.Message()})
	})

	b.Handle(cmdChain, func(c tele.Context) error {
		chat := s.getChat(c.Chat(), c.Sender())
		if chat.MessageID != nil {
			return c.Send("Chain is already running", "text", &tele.SendOptions{ReplyTo: c.Message()})
		}

		chat.MessageID = nil
		prompt := c.Message().Payload
		if prompt == "" {
			return c.Send(chat.t("Prompt is required"), "text", &tele.SendOptions{ReplyTo: c.Message()})
		}

		go s.onChain(c, chat)

		return nil
	})

	b.Handle("/ddi", func(c tele.Context) error {
		param, _ := tools.NewSearchImageParam(c.Message().Payload, "wt-wt", "photo")
		result := tools.SearchImages(param)

		if result.IsErr() {
			return c.Send("Error: " + result.Error().Error())
		}
		res := *result.Unwrap()
		if len(res) == 0 {
			return c.Send("No results found", "text", &tele.SendOptions{ReplyTo: c.Message()})
		}

		img := tele.FromURL(res[0].Image)
		return c.Send(&tele.Photo{
			File:    img,
			Caption: fmt.Sprintf("%s\n%s", res[0].Title, res[0].Link),
		}, "photo", &tele.SendOptions{ReplyTo: c.Message()})

	})

	b.Handle("/lang", func(c tele.Context) error {
		chat := s.getChat(c.Chat(), c.Sender())
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
		s.deleteHistory(chat.ID)
		chat.MessageID = nil
		s.db.Save(&chat)

		return nil
	})

	b.Handle(tele.OnText, func(c tele.Context) error {
		go s.onText(c)

		return nil
	})

	b.Handle(tele.OnQuery, func(c tele.Context) error {
		query := c.Query().Text
		go s.complete(c, query, false, nil)

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

func (s *Server) complete(c tele.Context, message string, reply bool, image *string) {
	chat := s.getChat(c.Chat(), c.Sender())
	if strings.HasPrefix(strings.ToLower(message), "reset") {
		s.deleteHistory(chat.ID)
		return
	}

	text := "..."
	sentMessage := c.Message()
	// reply is a flag to indicate if we need to reply to another message, or it is a voice transcription
	if !reply {
		text = fmt.Sprintf(chat.t("_Transcript:_\n%s\n\n_Answer:_ \n\n"), message)
		sentMessage, _ = c.Bot().Send(c.Recipient(), text, "text", &tele.SendOptions{
			ReplyTo:   c.Message(),
			ParseMode: tele.ModeMarkdown,
		})
		chat.MessageID = &([]string{strconv.Itoa(sentMessage.ID)}[0])
		c.Set("reply", *sentMessage)
	}

	response, err := s.answer(c, message, image)
	if err != nil {
		Log.WithField("user", c.Sender().Username).Error(err)
		_ = c.Send(response, replyMenu)
		return
	}
	Log.WithField("user", c.Sender().Username).WithField("length", len(response)).Info("Response")

	if len(response) == 0 || (chat.Stream && image == nil) {
		return
	}

	if len(response) > 4096 {
		file := tele.FromReader(strings.NewReader(response))
		_ = c.Send(&tele.Document{File: file, FileName: "answer.txt", MIME: "text/plain"}, replyMenu)
		return
	}

	if !reply {
		text = text[:len(text)-3] + response
		if _, err := c.Bot().Edit(sentMessage, text, "text", &tele.SendOptions{
			ReplyTo:   c.Message(),
			ParseMode: tele.ModeMarkdown,
		}, replyMenu); err != nil {
			_, _ = c.Bot().Edit(sentMessage, text, replyMenu)
		}
		return
	}

	_ = c.Send(response, "text", &tele.SendOptions{
		ReplyTo:   c.Message(),
		ParseMode: tele.ModeMarkdown,
	}, replyMenu)
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
