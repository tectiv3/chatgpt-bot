package main

import (
	"context"
	"fmt"
	"github.com/tectiv3/chatgpt-bot/types"
	tele "gopkg.in/telebot.v3"
	"io"
	"os"
	"runtime/debug"
	"strings"
	"time"
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

	if c.Message().Document.MIME != "text/plain" {
		chat := s.getChat(c.Chat(), c.Sender())
		_ = c.Send(chat.t("Please provide a text file"), "text", &tele.SendOptions{ReplyTo: c.Message()})
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

	message := c.Message().Payload
	if len(message) == 0 {
		message = c.Message().Text
	}

	s.complete(c, message, true)
}

func (s *Server) onVoice(c tele.Context) {
	defer func() {
		if err := recover(); err != nil {
			Log.WithField("error", err).Error("panic: ", string(debug.Stack()))
		}
	}()

	Log.WithField("user", c.Sender().Username).Info("Got a voice, filesize=", c.Message().Voice.FileSize)

	s.handleVoice(c)
}

func (s *Server) onPhoto(c tele.Context) {
	defer func() {
		if err := recover(); err != nil {
			Log.WithField("error", err).Error("panic: ", string(debug.Stack()))
		}
	}()

	Log.WithField("user", c.Sender().Username).Info("Got a photo, filesize=", c.Message().Photo.FileSize)

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
		_ = c.Send("Please provide a longer prompt", "text", &tele.SendOptions{ReplyTo: c.Message()})

		return
	}
	// get the text after the command
	if len(c.Message().Entities) > 0 {
		command := c.Message().EntityText(c.Message().Entities[0])
		query = query[len(command):]
	}

	s.complete(c, fmt.Sprintf("%s\n%s", prefix, query), true)
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
			Log.WithField("error", err).Error("panic: ", string(debug.Stack()))
		}
	}()
	clientQuery := types.ClientQuery{}

	prompt := c.Message().Payload
	clientQuery.Prompt = prompt
	clientQuery.Session = c.Sender().Username
	clientQuery.ModelName = chat.ModelName
	if chat.ModelName == mOllama && s.conf.OllamaEnabled {
		clientQuery.ModelName = s.conf.OllamaModel
	} else {
		clientQuery.ModelName = mGPT4
	}
	clientQuery.MaxIterations = 10

	outputChan := make(chan types.HttpJsonStreamElement)
	defer close(outputChan)

	// Start the agent chain function in a goroutine
	ctx := context.Background()
	sentMessage := chat.getSentMessage(c)

	go s.startAgent(ctx, outputChan, clientQuery)

	result := ""
	tokens := 0
	for {
		select {
		case output, ok := <-outputChan:
			if !ok {
				break
			}
			//Log.Info("Got output", "output", output)
			if output.Stream {
				tokens++
				result += output.Message
				result = strings.TrimSuffix(result, "<|im_end|>") // strip ollama end token
				if tokens%10 == 0 {
					_, _ = c.Bot().Edit(sentMessage, result)
				}
			} else if output.Close {
				Log.WithField("user", c.Sender().Username).WithField("tokens", tokens).Info("Stream finished")
				_, _ = c.Bot().Edit(sentMessage, result, "text", &tele.SendOptions{
					ReplyTo:   c.Message(),
					ParseMode: tele.ModeMarkdown,
				})
			} else if output.StepType == types.StepHandleChainEnd {
				result += "\n"
				_, _ = c.Bot().Edit(sentMessage, result, "text", &tele.SendOptions{
					ReplyTo:   c.Message(),
					ParseMode: tele.ModeMarkdown,
				})
			}
		case <-ctx.Done():
			Log.Info("Done. Client disconnected")
			break
		}
	}
}
