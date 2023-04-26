package main

// bot.go

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"github.com/sunicy/go-lame"
	"github.com/tectiv3/chatgpt-bot/opus"
	"io"
	"log"
	"runtime/debug"
	"strings"
	"time"

	"github.com/meinside/openai-go"
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

			wav, err := convertToWav(reader)
			if err != nil {
				return err
			}
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
	if strings.HasPrefix(strings.ToLower(message), "reset") {
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
		response = fmt.Sprintf("%s\n%s", message, response)

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

func convertToWav(r io.Reader) ([]byte, error) {
	output := new(bytes.Buffer)
	wavWriter, err := newWavWriter(output, 48000, 1, 16)
	if err != nil {
		return nil, err
	}

	s, err := opus.NewStream(r)
	if err != nil {
		return nil, err
	}
	defer s.Close()

	pcmbuf := make([]float32, 16384)
	for {
		n, err := s.ReadFloat32(pcmbuf)
		if err == io.EOF {
			break
		} else if err != nil {
			log.Fatal(err)
		}
		pcm := pcmbuf[:n*1]

		err = wavWriter.WriteSamples(pcm)
		if err != nil {
			return nil, err
		}
	}

	return output.Bytes(), err
}

// Helper function to create a new WAV writer
func newWavWriter(w io.Writer, sampleRate int, numChannels int, bitsPerSample int) (*wavWriter, error) {
	var header wavHeader

	// Set header values
	header.RIFFID = [4]byte{'R', 'I', 'F', 'F'}
	header.WAVEID = [4]byte{'W', 'A', 'V', 'E'}
	header.FMTID = [4]byte{'f', 'm', 't', ' '}
	header.Subchunk1Size = 16
	header.AudioFormat = 1
	header.NumChannels = uint16(numChannels)
	header.SampleRate = uint32(sampleRate)
	header.BitsPerSample = uint16(bitsPerSample)
	header.ByteRate = uint32(sampleRate * numChannels * bitsPerSample / 8)
	header.BlockAlign = uint16(numChannels * bitsPerSample / 8)
	header.DataID = [4]byte{'d', 'a', 't', 'a'}

	// Write header
	err := binary.Write(w, binary.LittleEndian, &header)
	if err != nil {
		return nil, err
	}

	return &wavWriter{w: w}, nil
}

// WAV writer struct
type wavWriter struct {
	w io.Writer
}

// WriteSamples Write samples to the WAV file
func (ww *wavWriter) WriteSamples(samples []float32) error {
	// Convert float32 samples to int16 samples
	int16Samples := make([]int16, len(samples))
	for i, s := range samples {
		if s > 1.0 {
			s = 1.0
		} else if s < -1.0 {
			s = -1.0
		}
		int16Samples[i] = int16(s * 32767)
	}
	// Write int16 samples to the WAV file
	return binary.Write(ww.w, binary.LittleEndian, &int16Samples)
}

// WAV file header struct
type wavHeader struct {
	RIFFID        [4]byte // RIFF header
	FileSize      uint32  // file size - 8
	WAVEID        [4]byte // WAVE header
	FMTID         [4]byte // fmt header
	Subchunk1Size uint32  // size of the fmt chunk
	AudioFormat   uint16  // audio format code
	NumChannels   uint16  // number of channels
	SampleRate    uint32  // sample rate
	ByteRate      uint32  // bytes per second
	BlockAlign    uint16  // block align
	BitsPerSample uint16  // bits per sample
	DataID        [4]byte // data header
	Subchunk2Size uint32  // size of the data chunk
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
