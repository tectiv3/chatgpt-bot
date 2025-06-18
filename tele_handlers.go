package main

import (
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/tectiv3/chatgpt-bot/i18n"
	tele "gopkg.in/telebot.v3"
)

func (s *Server) onDocument(c tele.Context) {
	defer func() {
		if err := recover(); err != nil {
			Log.WithField("error", err).Error("panic: ", string(debug.Stack()))
		}
	}()
	Log.WithField("user", c.Sender().Username).
		WithField("name", c.Message().Document.FileName).
		WithField("mime", c.Message().Document.MIME).
		WithField("size", c.Message().Document.FileSize).
		Info("Got a file")

	// Validate file size
	if err := ValidateFileSize(c.Message().Document.FileSize); err != nil {
		chat := s.getChat(c.Chat(), c.Sender())
		_ = c.Send(
			chat.t("File too large: {{.error}}", &i18n.Replacements{"error": err.Error()}),
			"text",
			&tele.SendOptions{ReplyTo: c.Message()},
		)
		return
	}

	// Validate file type
	if c.Message().Document.MIME == "application/pdf" {
		s.processPDF(c)
		return
	}

	if c.Message().Document.MIME != "text/plain" {
		chat := s.getChat(c.Chat(), c.Sender())
		_ = c.Send(
			chat.t("Please provide a text file"),
			"text",
			&tele.SendOptions{ReplyTo: c.Message()},
		)
		return
	}
	var reader io.ReadCloser
	var err error
	if s.conf.TelegramServerURL != "" {
		f, err := c.Bot().FileByID(c.Message().Document.FileID)
		if err != nil {
			Log.Warn("Error getting file ID", "error=", err)
			return
		}
		// start reader from f.FilePath
		reader, err = os.Open(f.FilePath)
		if err != nil {
			Log.Warn("Error opening file", "error=", err)
			return
		}
	} else {
		reader, err = s.bot.File(&c.Message().Document.File)
		if err != nil {
			_ = c.Send(err.Error(), "text", &tele.SendOptions{ReplyTo: c.Message()})
			return
		}
	}
	defer reader.Close()
	bytes, err := io.ReadAll(reader)
	if err != nil {
		_ = c.Send(err.Error(), "text", &tele.SendOptions{ReplyTo: c.Message()})
		return
	}

	response, err := s.simpleAnswer(c, string(bytes))
	if err != nil {
		_ = c.Send(response)
		return
	}
	Log.WithField("user", c.Sender().Username).Info("Response length=", len(response))

	if len(response) == 0 {
		return
	}

	file := tele.FromReader(strings.NewReader(response))
	_ = c.Send(&tele.Document{File: file, FileName: "answer.txt", MIME: "text/plain"})
}

func (s *Server) onText(c tele.Context) {
	defer func() {
		if err := recover(); err != nil {
			Log.WithField("error", err).Error("panic: ", string(debug.Stack()))
		}
	}()

	message := strings.TrimSpace(c.Message().Payload)
	if len(message) == 0 {
		message = strings.TrimSpace(c.Message().Text)
	}

	// Basic validation for message length
	if len(message) == 0 {
		chat := s.getChat(c.Chat(), c.Sender())
		_ = c.Send(chat.t("Please provide a message"), "text", &tele.SendOptions{ReplyTo: c.Message()})
		return
	}

	if len(message) > MaxPromptLength {
		chat := s.getChat(c.Chat(), c.Sender())
		_ = c.Send(
			chat.t("Message too long. Maximum length is {{.max}} characters", &i18n.Replacements{"max": fmt.Sprintf("%d", MaxPromptLength)}),
			"text",
			&tele.SendOptions{ReplyTo: c.Message()},
		)
		return
	}

	s.complete(c, message, true)
}

func (s *Server) onVoice(c tele.Context) {
	defer func() {
		if err := recover(); err != nil {
			Log.WithField("error", err).Error("panic: ", string(debug.Stack()))
		}
	}()

	Log.WithField("user", c.Sender().Username).
		Info("Got a voice, filesize=", c.Message().Voice.FileSize)

	s.handleVoice(c)
}

func (s *Server) onPhoto(c tele.Context) {
	defer func() {
		if err := recover(); err != nil {
			Log.WithField("error", err).Error("panic: ", string(debug.Stack()))
		}
	}()

	Log.WithField("user", c.Sender().Username).
		Info("Got a photo, filesize=", c.Message().Photo.FileSize)

	if c.Message().Photo.FileSize == 0 {
		return
	}

	s.handleImage(c)
}

func (s *Server) onTranslate(c tele.Context, prefix string) {
	defer func() {
		if err := recover(); err != nil {
			Log.WithField("error", err).Error("panic: ", string(debug.Stack()))
		}
	}()

	query := c.Message().Text
	if len(query) < 1 {
		_ = c.Send(
			"Please provide a longer prompt",
			"text",
			&tele.SendOptions{ReplyTo: c.Message()},
		)

		return
	}
	// get the text after the command
	if len(c.Message().Entities) > 0 {
		command := c.Message().EntityText(c.Message().Entities[0])
		query = query[len(command):]
	}

	s.complete(c, fmt.Sprintf("%s\n%s", prefix, query), true)
}

func (s *Server) onGetUsers(c tele.Context) {
	defer func() {
		if err := recover(); err != nil {
			Log.WithField("error", err).Error("panic: ", string(debug.Stack()))
		}
	}()

	users := s.getUsers()
	text := "Users:\n"
	for _, user := range users {
		threads := user.Threads
		var historyLen int64
		var updatedAt time.Time
		var totalTokens int
		var model string
		role := "default"

		if len(threads) > 0 {
			s.db.Model(&ChatMessage{}).Where("chat_id = ?", threads[0].ID).Count(&historyLen)
			updatedAt = threads[0].UpdatedAt
			totalTokens = threads[0].TotalTokens
			model = threads[0].ModelName
			if threads[0].RoleID != nil {
				role = threads[0].Role.Name
			}
		}

		text += fmt.Sprintf(
			"*%s*, last used: *%s*, history: *%d*, usage: *%d*, model: *%s*, role: *%s*\n",
			user.Username,
			updatedAt.Format("2006/01/02 15:04"),
			historyLen,
			totalTokens,
			model,
			role,
		)
	}

	_ = c.Send(text, "text", &tele.SendOptions{ReplyTo: c.Message(), ParseMode: tele.ModeMarkdown})
}

func (s *Server) onState(c tele.Context) {
	defer func() {
		if err := recover(); err != nil {
			Log.WithField("error", err).Error("panic: ", string(debug.Stack()))
		}
	}()

	chat := s.getChat(c.Chat(), c.Sender())
	user := chat.User
	state := user.State
	step := findEmptyStep(&state.FirstStep)

	if step == nil {
		s.resetUserState(user)
		return
	}

	step.Input = &c.Message().Text
	s.db.Model(&user).Update("State", state)

	chat.removeMenu(c)

	next := findEmptyStep(step)
	if next != nil {
		menu.Inline(menu.Row(btnCancel))
		sentMessage, err := c.Bot().Send(c.Recipient(), chat.t(next.Prompt), menu)
		if err != nil {
			Log.WithField("err", err).Error("Error sending message")
			return
		}
		id := &([]string{strconv.Itoa(sentMessage.ID)}[0])
		s.setChatLastMessageID(id, chat.ChatID)

		return
	}

	s.setChatLastMessageID(nil, chat.ChatID)
	Log.WithField("State", state.Name).Info("State: Done!")
	switch state.Name {
	case "RoleCreate":
		role := Role{
			UserID: user.ID,
			Name:   *state.FirstStep.Input,
			Prompt: *state.FirstStep.Next.Input,
		}

		chat.mutex.Lock()
		defer chat.mutex.Unlock()
		user.Roles = append(user.Roles, role)
		s.db.Save(&user)

		if err := c.Send(chat.t("Role created")); err != nil {
			Log.WithField("err", err).Error("Error sending message")
		}
	case "RoleUpdate":
		role := s.getRole(*state.ID)
		if role == nil {
			Log.Warn("Role not found")
			return
		}
		role.Name = *state.FirstStep.Input
		role.Prompt = *state.FirstStep.Next.Input
		s.db.Save(role)

		if err := c.Send(chat.t("Role updated")); err != nil {
			Log.WithField("err", err).Error("Error sending message")
		}
	default:
		Log.Warn("Unknown state: ", state.Name)
	}
	s.resetUserState(user)
}
