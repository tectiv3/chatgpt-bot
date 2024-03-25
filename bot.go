package main

// bot.go

import (
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
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
	cmdAge        = "/age"
	cmdPromptCL   = "/defaultprompt"
	cmdStream     = "/stream"
	cmdStop       = "/stop"
	cmdInfo       = "/info"
	cmdToJapanese = "/ja"
	cmdToEnglish  = "/en"
	cmdToRussian  = "/ru"
	cmdToItalian  = "/it"
	cmdToChinese  = "/cn"
	cmdUsers      = "/users"
	cmdAddUser    = "/add"
	cmdDelUser    = "/del"
	msgStart      = "This bot will answer your messages with ChatGPT API"
	masterPrompt  = "You are a helpful assistant. You always try to answer truthfully. If you don't know the answer, just say that you don't know, don't try to make up an answer. Don't explain yourself. Do not introduce yourself, just answer the user concisely."
)

var (
	menu       = &tele.ReplyMarkup{ResizeKeyboard: true}
	replyMenu  = &tele.ReplyMarkup{ResizeKeyboard: true, OneTimeKeyboard: true}
	removeMenu = &tele.ReplyMarkup{RemoveKeyboard: true}
	btn3       = tele.Btn{Text: "GPT3", Unique: "btnModel", Data: "gpt-3.5-turbo"}
	btn4       = tele.Btn{Text: "GPT4", Unique: "btnModel", Data: "gpt-4-turbo-preview"}
	btnT0      = tele.Btn{Text: "0.0", Unique: "btntemp", Data: "0.0"}
	btnT2      = tele.Btn{Text: "0.2", Unique: "btntemp", Data: "0.2"}
	btnT4      = tele.Btn{Text: "0.4", Unique: "btntemp", Data: "0.4"}
	btnT6      = tele.Btn{Text: "0.6", Unique: "btntemp", Data: "0.6"}
	btnT8      = tele.Btn{Text: "0.8", Unique: "btntemp", Data: "0.8"}
	btnT10     = tele.Btn{Text: "1.0", Unique: "btntemp", Data: "1.0"}
	btnReset   = tele.Btn{Text: "Reset", Unique: "btnreset", Data: "r"}
	btnEmpty   = tele.Btn{Text: "", Data: "no_data"}
)

// launch bot with given parameters
func (s *Server) run() {
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
	s.loadUsers()

	s.Lock()
	b.Use(s.whitelist())
	s.bot = b
	s.Unlock()
	replyMenu.Inline(menu.Row(btnReset))
	removeMenu.Inline(menu.Row(btnEmpty))

	b.Handle(cmdStart, func(c tele.Context) error {
		return c.Send(msgStart, "text", &tele.SendOptions{ReplyTo: c.Message()})
	})

	b.Handle(cmdModel, func(c tele.Context) error {
		menu.Inline(menu.Row(btn3, btn4))

		return c.Send("Select model", menu)
	})

	b.Handle(cmdTemp, func(c tele.Context) error {
		menu.Inline(menu.Row(btnT0, btnT2, btnT4, btnT6, btnT8, btnT10))
		chat := s.getChat(c.Chat().ID, c.Sender().Username)

		return c.Send(fmt.Sprintf("Set temperature from less random (0.0) to more random (1.0.\nCurrent: %0.2f (default: 0.8)", chat.Temperature), menu)
	})

	b.Handle(cmdPrompt, func(c tele.Context) error {
		query := c.Message().Payload
		if len(query) < 3 {
			return c.Send("Please provide a longer prompt", "text", &tele.SendOptions{
				ReplyTo: c.Message(),
			})
		}

		chat := s.getChat(c.Chat().ID, c.Sender().Username)
		chat.MasterPrompt = query
		s.db.Save(&chat)

		return nil
	})

	b.Handle(cmdAge, func(c tele.Context) error {
		age, err := strconv.Atoi(c.Message().Payload)
		if err != nil {
			return c.Send("Please provide a number", "text", &tele.SendOptions{
				ReplyTo: c.Message(),
			})
		}
		chat := s.getChat(c.Chat().ID, c.Sender().Username)
		chat.ConversationAge = int64(age)
		s.db.Save(&chat)

		return c.Send(fmt.Sprintf("Conversation age set to %d days", age), "text", &tele.SendOptions{ReplyTo: c.Message()})
	})

	b.Handle(cmdPromptCL, func(c tele.Context) error {
		chat := s.getChat(c.Chat().ID, c.Sender().Username)
		chat.MasterPrompt = masterPrompt
		s.db.Save(&chat)

		return c.Send("Default prompt set", "text", &tele.SendOptions{ReplyTo: c.Message()})
	})

	b.Handle(cmdStream, func(c tele.Context) error {
		chat := s.getChat(c.Chat().ID, c.Sender().Username)
		chat.Stream = !chat.Stream
		s.db.Save(&chat)
		status := "disabled"
		if chat.Stream {
			status = "enabled"
		}

		return c.Send("Stream is "+status, "text", &tele.SendOptions{ReplyTo: c.Message()})
	})

	b.Handle("/voice", func(c tele.Context) error {
		chat := s.getChat(c.Chat().ID, c.Sender().Username)
		chat.Voice = !chat.Voice
		s.db.Save(&chat)
		status := "disabled"
		if chat.Voice {
			status = "enabled"
		}

		return c.Send("Voice is "+status, "text", &tele.SendOptions{ReplyTo: c.Message()})
	})

	b.Handle(cmdStop, func(c tele.Context) error {

		return nil
	})

	b.Handle(cmdInfo, func(c tele.Context) error {
		chat := s.getChat(c.Chat().ID, c.Sender().Username)
		status := "disabled"
		if chat.Stream {
			status = "enabled"
		}

		//usage, err := s.getUsageMonth()
		//if err != nil {
		//	log.Println(err)
		//}
		//log.Printf("Current usage: %0.2f", usage)

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

	b.Handle(&btn3, func(c tele.Context) error {
		log.Printf("%s selected", c.Data())
		chat := s.getChat(c.Chat().ID, c.Sender().Username)
		chat.ModelName = c.Data()
		s.db.Save(&chat)

		return c.Edit("Model set to " + c.Data())
	})

	// On inline button pressed (callback)
	b.Handle(&btnT0, func(c tele.Context) error {
		log.Printf("Temp: %s\n", c.Data())
		chat := s.getChat(c.Chat().ID, c.Sender().Username)
		chat.Temperature, _ = strconv.ParseFloat(c.Data(), 64)
		s.db.Save(&chat)

		return c.Edit("Temperature set to " + c.Data())
	})

	b.Handle(&btnReset, func(c tele.Context) error {
		chat := s.getChat(c.Chat().ID, c.Sender().Username)
		s.deleteHistory(chat.ID)
		chat.MessageID = nil
		s.db.Save(&chat)

		return c.Edit(removeMenu)
	})

	b.Handle(cmdReset, func(c tele.Context) error {
		chat := s.getChat(c.Chat().ID, c.Sender().Username)
		s.deleteHistory(chat.ID)
		chat.MessageID = nil
		s.db.Save(&chat)

		return nil //c.Send(msgReset, "text", &tele.SendOptions{ReplyTo: c.Message()})
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
		go s.onDocument(c)

		return nil
	})

	b.Handle(tele.OnVoice, func(c tele.Context) error {
		go s.onVoice(c)

		return nil
	})

	b.Handle(tele.OnPhoto, func(c tele.Context) error {
		go s.onPhoto(c)

		return nil
	})

	b.Handle(tele.OnUserShared, func(c tele.Context) error {
		user := c.Message().UserShared
		log.Println("Shared user ID:", user.UserID)

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

func (s *Server) loadUsers() {
	s.Lock()
	defer s.Unlock()
	admins := s.conf.AllowedTelegramUsers
	var usernames []string
	s.db.Model(&User{}).Pluck("username", &usernames)
	for _, username := range admins {
		if !in_array(username, usernames) {
			usernames = append(usernames, username)
		}
	}
	s.users = append(s.users, usernames...)
}

func (s *Server) onDocument(c tele.Context) {
	defer func() {
		if err := recover(); err != nil {
			log.Println(string(debug.Stack()), err)
		}
	}()
	log.Printf("Got a file: %s (%s), size: %d",
		c.Message().Document.FileName,
		c.Message().Document.MIME,
		c.Message().Document.FileSize)
	if c.Message().Document.MIME != "text/plain" {
		_ = c.Send("Please provide a text file", "text", &tele.SendOptions{ReplyTo: c.Message()})
		return
	}

	reader, err := s.bot.File(&c.Message().Document.File)
	if err != nil {
		_ = c.Send(err.Error(), "text", &tele.SendOptions{ReplyTo: c.Message()})
		return
	}
	defer reader.Close()
	bytes, err := io.ReadAll(reader)
	if err != nil {
		_ = c.Send(err.Error(), "text", &tele.SendOptions{ReplyTo: c.Message()})
		return
	}

	response, err := s.simpleAnswer(string(bytes), c)
	if err != nil {
		_ = c.Send(response)
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
	_ = c.Send(response, "text", &tele.SendOptions{
		ReplyTo:   c.Message(),
		ParseMode: tele.ModeMarkdown,
	})
}

func (s *Server) onText(c tele.Context) {
	defer func() {
		if err := recover(); err != nil {
			log.Println(string(debug.Stack()), err)
		}
	}()

	message := c.Message().Payload
	if len(message) == 0 {
		message = c.Message().Text
	}

	s.complete(c, message, true, nil)
}

func (s *Server) onVoice(c tele.Context) {
	defer func() {
		if err := recover(); err != nil {
			log.Println(string(debug.Stack()), err)
		}
	}()

	log.Printf("Got a voice, size %d, caption: %s\n", c.Message().Voice.FileSize, c.Message().Voice.Caption)

	s.handleVoice(c)
}

func (s *Server) onPhoto(c tele.Context) {
	defer func() {
		if err := recover(); err != nil {
			log.Println(string(debug.Stack()), err)
		}
	}()

	log.Printf("Got a photo, size %d, caption: %s\n", c.Message().Photo.FileSize, c.Message().Photo.Caption)

	s.handlePhoto(c)
}

func (s *Server) onShare(c tele.Context) {
	defer func() {
		if err := recover(); err != nil {
			log.Println(string(debug.Stack()), err)
		}
	}()

	//log.Printf("Got a share: %s\n", c.Message().Text)
	if c.Message().Photo != nil {
		s.handlePhoto(c)
		return
	}

	s.onText(c)
}

func (s *Server) onTranslate(c tele.Context, prefix string) {
	defer func() {
		if err := recover(); err != nil {
			log.Println(string(debug.Stack()), err)
		}
	}()

	query := c.Message().Text
	if len(query) < 1 {
		_ = c.Send("Please provide a longer prompt", "text", &tele.SendOptions{
			ReplyTo: c.Message(),
		})

		return
	}

	response, err := s.answer(fmt.Sprintf("%s\n%s", prefix, query), c, nil)
	if err != nil {
		log.Println(err)
		_ = c.Send(err.Error(), "text", &tele.SendOptions{ReplyTo: c.Message()})

		return
	}

	_ = c.Send(response, "text", &tele.SendOptions{
		ReplyTo:   c.Message(),
		ParseMode: tele.ModeMarkdown,
	}, replyMenu)
}

func (s *Server) onGetUsers(c tele.Context) error {
	users := s.getUsers()
	text := "Users:\n"
	for _, user := range users {
		threads := user.Threads
		var historyLen int64
		var updatedAt time.Time
		var totalTokens int
		var model string
		if len(threads) > 0 {
			s.db.Model(&ChatMessage{}).Where("chat_id = ?", threads[0].ID).Count(&historyLen)
			updatedAt = threads[0].UpdatedAt
			totalTokens = threads[0].TotalTokens
			model = threads[0].ModelName
		}

		text += fmt.Sprintf("*%s*, history: *%d*, last used: *%s*, usage: *%d*, model: *%s*\n", user.Username, historyLen, updatedAt.Format("2006/01/02 15:04"), totalTokens, model)
	}

	return c.Send(text, "text", &tele.SendOptions{ReplyTo: c.Message(), ParseMode: tele.ModeMarkdown})
}

func (s *Server) complete(c tele.Context, message string, reply bool, image *string) {
	chat := s.getChat(c.Chat().ID, c.Sender().Username)
	if strings.HasPrefix(strings.ToLower(message), "reset") {
		s.deleteHistory(chat.ID)
		return
	}

	text := "..."
	sentMessage := c.Message()
	if !reply {
		text = fmt.Sprintf("_Transcript:_\n%s\n\n_Answer:_ \n\n", message)
		sentMessage, _ = c.Bot().Send(c.Recipient(), text, "text", &tele.SendOptions{
			ReplyTo:   c.Message(),
			ParseMode: tele.ModeMarkdown,
		})
		c.Set("reply", *sentMessage)
	}

	response, err := s.answer(message, c, image)
	if err != nil {
		_ = c.Send(response, replyMenu)
		return
	}
	log.Printf("User: %s. Response length: %d\n", c.Sender().Username, len(response))

	if len(response) == 0 {
		return
	}

	if len(response) > 4096 {
		file := tele.FromReader(strings.NewReader(response))
		_ = c.Send(&tele.Document{File: file, FileName: "answer.txt", MIME: "text/plain"}, replyMenu)
		return
	}

	if chat.Voice {
		s.sendAudio(c, response)
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

// getChat returns chat from db or creates a new one
func (s *Server) getChat(chatID int64, username string) Chat {
	var chat Chat

	s.db.FirstOrCreate(&chat, Chat{ChatID: chatID})
	if len(chat.MasterPrompt) == 0 {
		chat.MasterPrompt = masterPrompt
		chat.ModelName = "gpt-4-turbo-preview"
		chat.Temperature = 0.8
		chat.Stream = true
		chat.ConversationAge = 1
		s.db.Save(&chat)
	}

	if len(username) > 0 && chat.UserID == 0 {
		user := s.getUser(username)
		chat.UserID = user.ID
		s.db.Save(&chat)
	}

	if chat.ConversationAge == 0 {
		chat.ConversationAge = 1
		s.db.Save(&chat)
	}

	s.db.Find(&chat.History, "chat_id = ?", chat.ID)
	log.Printf("History %d, chatid %d\n", len(chat.History), chat.ID)

	return chat
}

// getUsers returns all users from db
func (s *Server) getUsers() []User {
	var users []User
	s.db.Model(&User{}).Preload("Threads").Find(&users)

	return users
}

// getUser returns user from db
func (s *Server) getUser(username string) User {
	var user User
	s.db.First(&user, User{Username: username})

	return user
}

func (s *Server) addUser(username string) {
	s.db.Create(&User{Username: username})
}

func (s *Server) delUser(userNane string) {
	s.db.Where("username = ?", userNane).Delete(&User{})
}

func (s *Server) deleteHistory(chatID uint) {
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

func (s *Server) handlePhoto(c tele.Context) {
	if c.Message().Photo.FileSize == 0 {
		return
	}
	photo := c.Message().Photo.File
	log.Println("Photo file: ", photo.FilePath, photo.FileSize, photo.FileID, photo.FileURL, c.Message().Photo.Caption)

	reader, err := c.Bot().File(&photo)
	if err != nil {
		log.Println("Error getting file content:", err)
		return
	}
	defer reader.Close()

	bytes, err := ioutil.ReadAll(reader)
	if err != nil {
		fmt.Println("Error reading file content:", err)
		return
	}

	var base64Encoding string

	// Determine the content type of the image file
	mimeType := http.DetectContentType(bytes)

	// Prepend the appropriate URI scheme header depending
	// on the MIME type
	switch mimeType {
	case "image/jpeg":
		base64Encoding += "data:image/jpeg;base64,"
	case "image/png":
		base64Encoding += "data:image/png;base64,"
	}

	// Append the base64 encoded output
	encoded := base64Encoding + toBase64(bytes)

	s.complete(c, c.Message().Caption, true, &encoded)
}
