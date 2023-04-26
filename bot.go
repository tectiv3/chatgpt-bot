package main

// bot.go

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os/exec"
	"runtime/debug"
	"strings"
	"time"

	"github.com/meinside/openai-go"
	"github.com/sunicy/go-lame"
	tele "gopkg.in/telebot.v3"
)

const (
	cmdStart = "/start"
	cmdReset = "/reset"
	msgStart = "This bot will answer your messages with ChatGPT API"
	msgReset = "This bots memory erased"
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
	Opusdec              string   `json:"opusdec"`
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
	s.bot = b

	b.Handle(cmdStart, func(c tele.Context) error {
		return c.Send(msgStart, "text", &tele.SendOptions{
			ReplyTo: c.Message(),
		})
	})

	b.Handle(cmdReset, func(c tele.Context) error {
		s.db.chats[c.Chat().ID] = Chat{history: []openai.ChatMessage{}}
		return c.Send(msgReset, "text", &tele.SendOptions{
			ReplyTo: c.Message(),
		})
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
		defer func() {
			if err := recover(); err != nil {
				log.Println(string(debug.Stack()), err)
			}
		}()

		if !s.isAllowed(c.Sender().Username) {
			return c.Send(fmt.Sprintf("not allowed: %s", c.Sender().Username), "text", &tele.SendOptions{
				ReplyTo: c.Message(),
			})
		}

		log.Printf("Got a voice, size %d, caption: %s\n", c.Message().Voice.FileSize, c.Message().Voice.Caption)
		if c.Message().Voice.FileSize > 0 {
			audioFile := c.Message().Voice.File
			log.Println("Audio file: ", audioFile.FilePath, audioFile.FileSize, audioFile.FileID, audioFile.FileURL)

			reader, err := b.File(&audioFile)
			if err != nil {
				return err
			}
			defer reader.Close()

			//body, err := ioutil.ReadAll(reader)
			//if err != nil {
			//	fmt.Println("Error reading file content:", err)
			//	return nil
			//}
			//ioutil.WriteFile("./test.ogg", body, 0644)

			wav, err := s.convertToWav(reader)
			if err != nil {
				return err
			}
			log.Printf("wav: %d bytes\n", len(wav))
			// save bytes to file
			//ioutil.WriteFile("./test.wav", wav, 0644)
			mp3 := wavToMp3(wav)
			if mp3 == nil {
				return fmt.Errorf("failed to convert to mp3")
			}
			audio := openai.NewFileParamFromBytes(mp3)
			if translated, err := s.ai.CreateTranscription(audio, "whisper-1", nil); err != nil {
				log.Printf("failed to create transcription: %s\n", err)
			} else {
				if translated.JSON == nil &&
					translated.Text == nil &&
					translated.SRT == nil &&
					translated.VerboseJSON == nil &&
					translated.VTT == nil {
					return fmt.Errorf("there was no returned data")
				}

				s.complete(c, *translated.Text, false)
			}

		}
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

	if !s.isAllowed(c.Sender().Username) {
		_ = c.Send(fmt.Sprintf("not allowed: %s", c.Sender().Username), "text", &tele.SendOptions{
			ReplyTo: c.Message(),
		})
		return
	}

	message := c.Message().Payload
	if len(message) == 0 {
		message = c.Message().Text
	}

	s.complete(c, message, true)
}

func (s Server) complete(c tele.Context, message string, reply bool) {
	if strings.HasPrefix(strings.ToLower(message), "reset") ||
		strings.HasPrefix(strings.ToLower(message), "ресет") {
		s.db.chats[c.Chat().ID] = Chat{history: []openai.ChatMessage{}}
		_ = c.Send(msgReset, "text", &tele.SendOptions{
			ReplyTo: c.Message(),
		})
		return
	}

	response, err := s.answer(message, c)
	if err != nil {
		return
	}
	log.Printf("User: %s. Response length: %d\n", c.Sender().Username, len(response))

	if len(response) > 4096 {
		file := tele.FromReader(strings.NewReader(response))
		_ = c.Send(&tele.Document{File: file, FileName: "answer.txt", MIME: "text/plain"})
		return
	}
	if !reply {
		response = fmt.Sprintf("%s\n\n%s", message, response)

		_ = c.Send(response)
		return
	}

	_ = c.Send(response, "text", &tele.SendOptions{
		ReplyTo: c.Message(),
	})
}

// checks if given update is allowed or not
func (s Server) isAllowed(username string) bool {
	_, exists := s.users[username]

	return exists
}

// generate an answer to given message and send it to the chat
func (s Server) answer(message string, c tele.Context) (string, error) {
	_ = c.Notify(tele.Typing)

	msg := openai.NewChatUserMessage(message)
	system := openai.NewChatSystemMessage("You are a helpful assistant. You always try to answer truthfully. If you don't know the answer you say you don't know.")

	var chat Chat
	chat, ok := s.db.chats[c.Chat().ID]
	if !ok {
		chat = Chat{history: []openai.ChatMessage{}}
	}
	chat.history = append(chat.history, msg)
	history := append([]openai.ChatMessage{system}, chat.history...)

	response, err := s.ai.CreateChatCompletion(s.conf.Model, history, openai.ChatCompletionOptions{}.SetUser(userAgent(c.Sender().ID)))

	if err != nil {
		log.Printf("failed to create chat completion: %s", err)
		return err.Error(), err
	}
	if s.conf.Verbose {
		log.Printf("[verbose] %s ===> %+v", message, response.Choices)
	}

	_ = c.Notify(tele.Typing)

	var answer string
	if len(response.Choices) > 0 {
		answer = response.Choices[0].Message.Content
	} else {
		answer = "No response from API."
	}

	if s.conf.Verbose {
		log.Printf("[verbose] sending answer: '%s'", answer)
	}

	chat.history = append(chat.history, openai.NewChatAssistantMessage(answer))
	s.db.chats[c.Chat().ID] = chat

	if len(chat.history) > 8 {
		chat.history = chat.history[1:]
	}

	return answer, nil
}

// generate a user-agent value
func userAgent(userID int64) string {
	return fmt.Sprintf("telegram-chatgpt-bot:%d", userID)
}

func (s Server) convertToWav(r io.Reader) ([]byte, error) {
	output := new(bytes.Buffer)
	// run command with stdin as the reader and stdout as the writer
	cmd := exec.Command(s.conf.Opusdec, "--force-wav", "-", "-")

	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	// write to stdin
	if _, err := io.Copy(stdin, r); err != nil {
		return nil, err
	}
	stdin.Close()

	// read from stdout
	tmp := make([]byte, 1024)
	for {
		n, err := stdout.Read(tmp)
		if err != nil && err != io.EOF {
			return nil, err
		}
		if n == 0 {
			break
		}
		output.Write(tmp[:n])
	}
	// read from stderr
	tmp = make([]byte, 1024)
	for {
		n, err := stderr.Read(tmp)
		if err != nil && err != io.EOF {
			log.Println(err)
			break
		}
		if n == 0 {
			break
		}
		log.Println(string(tmp[:n]))
	}
	cmd.Wait()

	return output.Bytes(), nil
}

func wavToMp3(wav []byte) []byte {
	reader := bytes.NewReader(wav)
	wavHdr, err := lame.ReadWavHeader(reader)
	if err != nil {
		log.Println("not a wav file, err=" + err.Error())
		return nil
	}
	output := new(bytes.Buffer)
	wr, _ := lame.NewWriter(output)
	defer wr.Close()

	wr.EncodeOptions = wavHdr.ToEncodeOptions()
	if _, err := io.Copy(wr, reader); err != nil {
		return nil
	}

	return output.Bytes()
}
