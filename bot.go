package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tectiv3/chatgpt-bot/i18n"

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
	cmdLang       = "/lang"
	cmdImage      = "/image"
	cmdToJapanese = "/ja"
	cmdToEnglish  = "/en"
	cmdToRussian  = "/ru"
	cmdToItalian  = "/it"
	cmdToSpanish  = "/es"
	cmdToChinese  = "/cn"
	cmdRoles      = "/roles"
	cmdRole       = "/role"
	cmdQA         = "/qa"
	cmdUsers      = "/users"
	cmdAddUser    = "/add"
	cmdDelUser    = "/del"
	msgStart      = "This bot will answer your messages with ChatGPT API"
	masterPrompt  = "You are a helpful assistant. You always try to answer truthfully. If you don't know the answer, just say that you don't know, don't try to make up an answer. Don't explain yourself. Do not introduce yourself, just answer the user concisely."
	pOllama       = "ollama"
	pGroq         = "groq"
	pOpenAI       = "openai"
	miniModel     = "gpt-4o-mini"
	pAWS          = "aws"
	pAnthropic    = "anthropic"
	openAILatest  = "openAILatest"
)

var (
	menu       = &tele.ReplyMarkup{ResizeKeyboard: true}
	replyMenu  = &tele.ReplyMarkup{ResizeKeyboard: true, OneTimeKeyboard: true}
	removeMenu = &tele.ReplyMarkup{RemoveKeyboard: true}
	btnModel   = tele.Btn{Text: "Select Model", Unique: "btnModel", Data: ""}
	btnT0      = tele.Btn{Text: "0.0", Unique: "btntemp", Data: "0.0"}
	btnT2      = tele.Btn{Text: "0.2", Unique: "btntemp", Data: "0.2"}
	btnT4      = tele.Btn{Text: "0.4", Unique: "btntemp", Data: "0.4"}
	btnT6      = tele.Btn{Text: "0.6", Unique: "btntemp", Data: "0.6"}
	btnT8      = tele.Btn{Text: "0.8", Unique: "btntemp", Data: "0.8"}
	btnT10     = tele.Btn{Text: "1.0", Unique: "btntemp", Data: "1.0"}
	btnCreate  = tele.Btn{Text: "New Role", Unique: "btnRole", Data: "create"}
	btnUpdate  = tele.Btn{Text: "Update", Unique: "btnUpdate", Data: "update"}
	btnDelete  = tele.Btn{Text: "Delete", Unique: "btnDelete", Data: "delete"}
	btnCancel  = tele.Btn{Text: "Cancel", Unique: "btnRole", Data: "cancel"}

	btnReset = tele.Btn{Text: "New Conversation", Unique: "btnreset", Data: "r"}
	btnEmpty = tele.Btn{Text: "", Data: "no_data"}
)

func init() {
	replyMenu.Inline(menu.Row(btnReset))
	removeMenu.Inline(menu.Row(btnEmpty))
}

// run will launch bot with given parameters
func (s *Server) run() {
	b, err := tele.NewBot(tele.Settings{
		Token: s.conf.TelegramBotToken,
		URL:   s.conf.TelegramServerURL,
		Poller: &tele.LongPoller{
			Timeout: 1 * time.Second,
			AllowedUpdates: []string{
				"message",
				"edited_message",
				"inline_query",
				"callback_query",
				// "message_reaction",
				// "message_reaction_count",
			},
		},
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

	// b.Use(middleware.Logger())
	s.loadUsers()

	s.Lock()
	b.Use(s.whitelist())
	s.bot = b
	s.Unlock()

	b.Handle(cmdStart, func(c tele.Context) error {
		return c.Send(
			l.GetWithLocale(c.Sender().LanguageCode, msgStart),
			"text",
			&tele.SendOptions{ReplyTo: c.Message()},
		)
	})

	b.Handle(cmdModel, func(c tele.Context) error {
		chat := s.getChat(c.Chat(), c.Sender())
		model := strings.TrimSpace(c.Message().Payload)
		if model == "" {
			rows := []tele.Row{}
			row := []tele.Btn{}

			for _, m := range s.conf.Models {
				if len(row) == 3 {
					rows = append(rows, menu.Row(row...))
					row = []tele.Btn{}
				}
				if m.Provider == pOpenAI || (m.Provider == pAnthropic && s.conf.AnthropicEnabled) || (m.Provider == pAWS && s.conf.AWSEnabled) {
					row = append(row, tele.Btn{Text: m.Name, Unique: "btnModel", Data: m.Name})
				}
			}
			rows = append(rows, menu.Row(row...))

			menu.Inline(rows...)

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

		return c.Send(
			fmt.Sprintf(
				chat.t(
					"Set temperature from less random (0.0) to more random (1.0).\nCurrent: %0.2f (default: 0.8)",
				),
				chat.Temperature,
			),
			menu,
		)
	})

	b.Handle(cmdRole, func(c tele.Context) error {
		chat := s.getChat(c.Chat(), c.Sender())
		name := strings.TrimSpace(c.Message().Payload)
		if err := ValidateRoleName(name); err != nil {
			return c.Send(
				chat.t("Invalid role name: {{.error}}", &i18n.Replacements{"error": err.Error()}),
				"text",
				&tele.SendOptions{ReplyTo: c.Message()},
			)
		}
		role := s.findRole(chat.UserID, name)
		if role == nil {
			return c.Send(chat.t("Role not found"))
		}
		s.setChatRole(&role.ID, chat.ChatID)

		return c.Send(chat.t("Role set to {{.role}}", &i18n.Replacements{"role": role.Name}))
	})

	b.Handle(cmdRoles, func(c tele.Context) error {
		chat := s.getChat(c.Chat(), c.Sender())
		roles := chat.User.Roles
		rows := []tele.Row{}
		// iterate over roles, add menu button with role name 3 buttons in a row
		row := []tele.Btn{
			{
				Text:   chat.t("default"),
				Unique: "btnRole",
				Data:   "___default___",
			},
		}
		for _, role := range roles {
			if len(row) == 3 {
				rows = append(rows, menu.Row(row...))
				row = []tele.Btn{}
			}
			row = append(
				row,
				tele.Btn{Text: role.Name, Unique: "btnRole", Data: strconv.Itoa(int(role.ID))},
			)
		}
		if len(row) != 0 {
			rows = append(rows, menu.Row(row...))
			row = []tele.Btn{}
		}
		row = append(row, btnCreate, btnUpdate, btnDelete)
		rows = append(rows, menu.Row(row...))
		// Log.Info(rows)
		menu.Inline(rows...)

		return c.Send(chat.t("Select role"), menu)
	})

	b.Handle(&btnCreate, func(c tele.Context) error {
		Log.WithField("user", c.Sender().Username).Info("Selected role ", c.Data())
		chat := s.getChat(c.Chat(), c.Sender())

		user := chat.User
		if c.Data() == "cancel" {
			s.db.Model(&user).Update("State", nil)

			return c.Edit(chat.t("Canceled"), removeMenu)
		}

		if c.Data() == "___default___" {
			chat.MasterPrompt = masterPrompt
			s.db.Save(&chat)
			s.setChatRole(nil, chat.ChatID)

			return c.Edit(chat.t("Default prompt set"))
		}

		if c.Data() != "create" {
			roleID := asUint(c.Data())
			role := s.getRole(roleID)
			if role == nil {
				return c.Send(chat.t("Role not found"))
			}
			// s.db.Model(&chat).Update("RoleID", role.ID) // gorm is weird
			s.setChatRole(&role.ID, chat.ChatID)
			s.setChatLastMessageID(nil, chat.ChatID)

			return c.Edit(chat.t("Role set to {{.role}}", &i18n.Replacements{"role": role.Name}))
		}

		state := State{
			Name: "RoleCreate",
			FirstStep: Step{
				Field:  "Name",
				Prompt: "Enter role name",
				Next: &Step{
					Prompt: "Enter system prompt",
					Field:  "Prompt",
				},
			},
		}
		s.db.Model(&user).Update("State", state)

		menu.Inline(menu.Row(btnCancel))

		id := &([]string{strconv.Itoa(c.Message().ID)}[0])
		s.setChatLastMessageID(id, chat.ChatID)

		return c.Edit(chat.t("Enter role name"), menu)
	})

	b.Handle(&btnUpdate, func(c tele.Context) error {
		Log.WithField("user", c.Sender().Username).Info("Selected option ", c.Data())
		chat := s.getChat(c.Chat(), c.Sender())
		user := chat.User

		if c.Data() != "update" {
			roleID := asUint(c.Data())
			role := s.getRole(roleID)
			if role == nil {
				return c.Edit(chat.t("Role not found"))
			}

			state := State{
				Name: "RoleUpdate",
				ID:   &roleID,
				FirstStep: Step{
					Field:  "Name",
					Prompt: "Enter role name",
					Next: &Step{
						Prompt: "Enter system prompt",
						Field:  "Prompt",
					},
				},
			}
			user.State = &state
			s.db.Save(&user)

			menu.Inline(menu.Row(btnCancel))

			return c.Edit(chat.t(state.FirstStep.Prompt), menu)
		}

		roles := chat.User.Roles
		rows := []tele.Row{}
		// iterate over roles, add menu button with role name 3 buttons in a row
		row := []tele.Btn{}
		for _, role := range roles {
			if len(row) == 3 {
				rows = append(rows, menu.Row(row...))
				row = []tele.Btn{}
			}
			row = append(
				row,
				tele.Btn{Text: role.Name, Unique: "btnUpdate", Data: strconv.Itoa(int(role.ID))},
			)
		}
		rows = append(rows, menu.Row(row...), menu.Row(btnCancel))
		menu.Inline(rows...)

		return c.Edit(chat.t("Select Role"), menu)
	})

	b.Handle(&btnDelete, func(c tele.Context) error {
		Log.WithField("user", c.Sender().Username).Info("Selected option ", c.Data())
		chat := s.getChat(c.Chat(), c.Sender())

		if c.Data() != "delete" {
			roleID := asUint(c.Data())
			role := s.getRole(roleID)
			if role == nil {
				return c.Send(chat.t("Role not found"))
			}
			// Log.WithField("roleID", roleID).WithField("chat", *chat.RoleID).Info("Role deleted")
			if chat.RoleID != nil {
				Log.WithField("roleID", roleID).WithField("chat", *chat.RoleID).Info("Role deleted")
				if *chat.RoleID == roleID {
					// s.db.Model(&chat).Update("RoleID", nil) // stupid gorm insert chat, roles, users and duplicates roleID
					s.setChatRole(nil, chat.ChatID)
				}
			}
			s.db.Unscoped().Delete(&Role{}, roleID)

			return c.Edit(chat.t("Role deleted"))
		}

		roles := chat.User.Roles
		rows := []tele.Row{}
		// iterate over roles, add menu button with role name, 3 buttons in a row
		// TODO: refactor to use native menu.Split(3, btns)
		row := []tele.Btn{}
		for _, role := range roles {
			if len(row) == 3 {
				rows = append(rows, menu.Row(row...))
				row = []tele.Btn{}
			}
			row = append(
				row,
				tele.Btn{Text: role.Name, Unique: "btnDelete", Data: strconv.Itoa(int(role.ID))},
			)
		}
		rows = append(rows, menu.Row(row...), menu.Row(btnCancel))
		menu.Inline(rows...)

		return c.Edit(chat.t("Select Role"), menu)
	})

	b.Handle(cmdAge, func(c tele.Context) error {
		chat := s.getChat(c.Chat(), c.Sender())
		ageStr := strings.TrimSpace(c.Message().Payload)
		age, err := ValidateAge(ageStr)
		if err != nil {
			return c.Send(
				chat.t("Invalid age: {{.error}}", &i18n.Replacements{"error": err.Error()}),
				"text",
				&tele.SendOptions{ReplyTo: c.Message()},
			)
		}
		chat.ConversationAge = int64(age)
		s.db.Save(&chat)

		return c.Send(
			fmt.Sprintf(chat.t("Conversation age set to %d days"), age),
			"text",
			&tele.SendOptions{ReplyTo: c.Message()},
		)
	})

	b.Handle(cmdPrompt, func(c tele.Context) error {
		chat := s.getChat(c.Chat(), c.Sender())
		query := strings.TrimSpace(c.Message().Payload)
		if err := ValidatePrompt(query); err != nil {
			return c.Send(
				chat.t("Invalid prompt: {{.error}}", &i18n.Replacements{"error": err.Error()}),
				"text",
				&tele.SendOptions{ReplyTo: c.Message()},
			)
		}

		chat.MasterPrompt = query
		s.db.Save(&chat)

		return c.Send(chat.t("Prompt set"), "text", &tele.SendOptions{ReplyTo: c.Message()})
	})

	b.Handle(cmdPromptCL, func(c tele.Context) error {
		chat := s.getChat(c.Chat(), c.Sender())
		chat.MasterPrompt = masterPrompt
		chat.RoleID = nil
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

	b.Handle(cmdQA, func(c tele.Context) error {
		chat := s.getChat(c.Chat(), c.Sender())
		chat.QA = !chat.QA
		s.db.Save(&chat)
		status := "disabled"
		if chat.QA {
			status = "enabled"
		}
		text := chat.t("Questions List is {{.status}}", &i18n.Replacements{"status": chat.t(status)})

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

		prompt := chat.MasterPrompt
		role := chat.t("default")
		if chat.RoleID != nil {
			prompt = chat.Role.Prompt
			role = chat.Role.Name
		}

		return c.Send(
			fmt.Sprintf(
				"Version: %s\nModel: %s\nTemperature: %0.2f\nPrompt: %s\nStreaming: %s\nConvesation Age (days): %d\nRole: %s",
				Version,
				s.getModel(chat.ModelName),
				chat.Temperature,
				prompt,
				status,
				chat.ConversationAge,
				role,
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

	b.Handle(cmdImage, func(c tele.Context) error {
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

	b.Handle(cmdLang, func(c tele.Context) error {
		chat := s.getChat(c.Chat(), c.Sender())
		langCode := strings.TrimSpace(c.Message().Payload)
		if err := ValidateLanguageCode(langCode); err != nil {
			return c.Send(
				chat.t("Invalid language code: {{.error}}", &i18n.Replacements{"error": err.Error()}),
				"text",
				&tele.SendOptions{ReplyTo: c.Message()},
			)
		}
		chat.Lang = langCode
		s.db.Save(&chat)
		return c.Send(
			fmt.Sprintf("Language set to %s", chat.Lang),
			"text",
			&tele.SendOptions{ReplyTo: c.Message()},
		)
	})

	b.Handle(&btnModel, func(c tele.Context) error {
		Log.WithField("user", c.Sender().Username).Info("Selected model ", c.Data())
		chat := s.getChat(c.Chat(), c.Sender())
		chat.ModelName = c.Data()
		s.db.Save(&chat)

		return c.Edit(chat.t("Model set to {{.model}}", &i18n.Replacements{"model": c.Data()}))
	})

	b.Handle(&btnT0, func(c tele.Context) error {
		Log.WithField("user", c.Sender().Username).Info("Selected temperature ", c.Data())
		chat := s.getChat(c.Chat(), c.Sender())
		temp, err := ValidateTemperature(c.Data())
		if err != nil {
			Log.WithField("error", err).Warn("Invalid temperature value")
			return c.Edit(chat.t("Invalid temperature value"))
		}
		chat.Temperature = temp
		s.db.Save(&chat)

		return c.Edit(chat.t("Temperature set to {{.temp}}", &i18n.Replacements{"temp": c.Data()}))
	})

	b.Handle(&btnReset, func(c tele.Context) error {
		chat := s.getChat(c.Chat(), c.Sender())

		s.deleteHistory(chat.ID)
		s.setChatLastMessageID(nil, chat.ChatID)

		return c.Edit(removeMenu)
	})

	b.Handle(cmdReset, func(c tele.Context) error {
		chat := s.getChat(c.Chat(), c.Sender())
		// Log.Info("Resetting chat")
		s.deleteHistory(chat.ID)
		if chat.MessageID != nil {
			id, _ := strconv.Atoi(*chat.MessageID)
			sentMessage := &tele.Message{ID: id, Chat: &tele.Chat{ID: chat.ChatID}}

			// Log.Infof("Resetting chat menu, sentMessage: %v", sentMessage)
			c.Bot().Edit(sentMessage, removeMenu)
			s.setChatLastMessageID(nil, chat.ChatID)

			return nil
		}
		s.setChatLastMessageID(nil, chat.ChatID)

		return nil
	})

	b.Handle(tele.OnText, func(c tele.Context) error {
		chat := s.getChat(c.Chat(), c.Sender())

		// not handling  user input through stepper/state machine
		if chat.User.State == nil {
			// if e := b.React(c.Sender(), c.Message(), react.React(react.Eyes)); e != nil {
			// 	Log.Warn(e)
			// }

			go s.onText(c)
		} else {
			chat.removeMenu(c)
			// in the middle of stepper input
			go s.onState(c)
		}

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

		// b.React(c.Recipient(), c.Message(), react.React(react.Eyes))

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
		go s.onGetUsers(c)

		return nil
	})

	b.Handle(cmdAddUser, func(c tele.Context) error {
		if !in_array(c.Sender().Username, s.conf.AllowedTelegramUsers) {
			return nil
		}
		name := strings.TrimSpace(c.Message().Payload)
		if err := ValidateUsername(name); err != nil {
			return c.Send(
				fmt.Sprintf("Invalid username: %s", err.Error()),
				"text",
				&tele.SendOptions{ReplyTo: c.Message()},
			)
		}
		s.addUser(name)
		s.loadUsers()

		go s.onGetUsers(c)

		return nil
	})

	b.Handle(cmdDelUser, func(c tele.Context) error {
		if !in_array(c.Sender().Username, s.conf.AllowedTelegramUsers) {
			return nil
		}
		name := strings.TrimSpace(c.Message().Payload)
		if err := ValidateUsername(name); err != nil {
			return c.Send(
				fmt.Sprintf("Invalid username: %s", err.Error()),
				"text",
				&tele.SendOptions{ReplyTo: c.Message()},
			)
		}
		s.delUser(name)
		s.loadUsers()

		go s.onGetUsers(c)

		return nil
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
				return c.Send(
					fmt.Sprintf("not allowed: %s", c.Sender().Username),
					"text",
					&tele.SendOptions{ReplyTo: c.Message()},
				)
			},
		})(next)
	}
}
