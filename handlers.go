package main

import (
	"fmt"
	tele "gopkg.in/telebot.v3"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"runtime/debug"
	"strings"
	"time"
)

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

	response, err := s.simpleAnswer(c, string(bytes))
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

	response, err := s.answer(c, fmt.Sprintf("%s\n%s", prefix, query), nil)
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
