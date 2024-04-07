package main

import (
	"context"
	"fmt"
	tele "gopkg.in/telebot.v3"
	"io"
	"log/slog"
	"net/http"
	"os"
	"runtime/debug"
	"strings"
	"time"
)

func (s *Server) onDocument(c tele.Context) {
	defer func() {
		if err := recover(); err != nil {
			slog.Error("Panic", "stack", string(debug.Stack()), "error", err)
		}
	}()
	slog.Info("Got a file",
		"name", c.Message().Document.FileName,
		"mime", c.Message().Document.MIME,
		"size", c.Message().Document.FileSize)

	if c.Message().Document.MIME != "text/plain" {
		_ = c.Send("Please provide a text file", "text", &tele.SendOptions{ReplyTo: c.Message()})
		return
	}
	var reader io.ReadCloser
	var err error
	if s.conf.TelegramServerURL != "" {
		f, err := c.Bot().FileByID(c.Message().Document.FileID)
		if err != nil {
			slog.Warn("Error getting file ID", "error", err)
			return
		}
		// start reader from f.FilePath
		reader, err = os.Open(f.FilePath)
		if err != nil {
			slog.Warn("Error opening file", "error", err)
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
	slog.Info("Response", "user", c.Sender().Username, "length", len(response))

	if len(response) == 0 {
		return
	}

	file := tele.FromReader(strings.NewReader(response))
	_ = c.Send(&tele.Document{File: file, FileName: "answer.txt", MIME: "text/plain"})
}

func (s *Server) onText(c tele.Context) {
	defer func() {
		if err := recover(); err != nil {
			slog.Error("Panic", "stack", string(debug.Stack()), "error", err)
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
			slog.Error("Panic", "stack", string(debug.Stack()), "error", err)
		}
	}()

	slog.Info("Got a voice", "size", c.Message().Voice.FileSize, "caption", c.Message().Voice.Caption)

	s.handleVoice(c)
}

func (s *Server) onPhoto(c tele.Context) {
	defer func() {
		if err := recover(); err != nil {
			slog.Error("Panic", "stack", string(debug.Stack()), "error", err)
		}
	}()

	slog.Info("Got a photo", "size", c.Message().Photo.FileSize, "caption", c.Message().Photo.Caption)

	if c.Message().Photo.FileSize == 0 {
		return
	}
	photo := c.Message().Photo.File

	var reader io.ReadCloser
	var err error

	if s.conf.TelegramServerURL != "" {
		f, err := c.Bot().FileByID(photo.FileID)
		if err != nil {
			slog.Warn("Error getting file ID", "error", err)
			return
		}
		// start reader from f.FilePath
		reader, err = os.Open(f.FilePath)
		if err != nil {
			slog.Warn("Error opening file", "error", err)
			return
		}
	} else {
		reader, err = c.Bot().File(&photo)
		if err != nil {
			slog.Warn("Error getting file content", "error", err)
			return
		}
	}

	defer reader.Close()

	bytes, err := io.ReadAll(reader)
	if err != nil {
		slog.Warn("Error reading file content", "error", err)
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
			slog.Error("Panic", "stack", string(debug.Stack()), "error", err)
		}
	}()

	query := c.Message().Text
	if len(query) < 1 {
		_ = c.Send("Please provide a longer prompt", "text", &tele.SendOptions{ReplyTo: c.Message()})

		return
	}
	// get the text after the command
	if len(c.Message().Entities) > 0 {
		command := c.Message().EntityText(c.Message().Entities[0])
		query = query[len(command):]
	}

	_, err := s.answer(c, fmt.Sprintf("%s\n%s", prefix, query), nil)
	if err != nil {
		slog.Warn("Translate error", "error", err)
		_ = c.Send(err.Error(), "text", &tele.SendOptions{ReplyTo: c.Message()})

		return
	}

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

func (s *Server) onChain(c tele.Context, chat *Chat) {
	defer func() {
		if err := recover(); err != nil {
			slog.Error("Panic", "stack", string(debug.Stack()), "error", err)
		}
	}()
	clientQuery := ClientQuery{}

	// get request params
	prompt := c.Message().Payload
	clientQuery.Prompt = prompt
	clientQuery.Session = c.Sender().Username
	clientQuery.ModelName = "knoopx/hermes-2-pro-mistral:7b-q8_0"
	clientQuery.MaxIterations = 30

	// Create a channel for communication with the llm agent chain
	outputChan := make(chan HttpJsonStreamElement)
	defer close(outputChan)

	// Start the agent chain function in a goroutine
	ctx := context.Background()
	sentMessage := chat.getSentMessage(c)

	go s.startAgent(ctx, outputChan, clientQuery)

	result := ""
	// Stream the output back to the client as it arrives
	for {
		select {
		case output, ok := <-outputChan:
			if !ok {
				// Channel was closed, end the response
				break
			}
			result += output.Message
			_, _ = c.Bot().Edit(&sentMessage, output.Message)
		case <-ctx.Done():
			_, _ = c.Bot().Edit(&sentMessage, result, "text", &tele.SendOptions{
				ReplyTo:   c.Message(),
				ParseMode: tele.ModeMarkdown,
			}, replyMenu)

			slog.Info("Done. Client disconnected")
			break
		}
	}

}
