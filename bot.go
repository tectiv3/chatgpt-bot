package main

// bot.go

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/meinside/openai-go"
	"github.com/sunicy/go-lame"
	"github.com/tectiv3/chatgpt-bot/opus"
	tele "gopkg.in/telebot.v3"
)

const (
	cmdStart      = "/start"
	cmdReset      = "/reset"
	cmdModel      = "/model"
	cmdTemp       = "/temperature"
	cmdPrompt     = "/prompt"
	cmdPromptCL   = "/defaultprompt"
	cmdStream     = "/stream"
	cmdInfo       = "/info"
	cmdToJapanese = "/jp"
	cmdToEnglish  = "/en"
	msgStart      = "This bot will answer your messages with ChatGPT API"
	msgReset      = "This bots memory erased"
	masterPrompt  = "You are a helpful assistant. You always try to answer truthfully. If you don't know the answer, just say that you don't know, don't try to make up an answer."
)

var (
	menu   = &tele.ReplyMarkup{ResizeKeyboard: true}
	btn3   = tele.Btn{Text: "GPT3", Unique: "btnModel", Data: "gpt-3.5-turbo"}
	btn4   = tele.Btn{Text: "GPT4", Unique: "btnModel", Data: "gpt-4"}
	btn316 = tele.Btn{Text: "GPT3-16k", Unique: "btnModel", Data: "gpt-3.5-turbo-16k"}
	btnT0  = tele.Btn{Text: "0.0", Unique: "btntemp", Data: "0.0"}
	btnT2  = tele.Btn{Text: "0.2", Unique: "btntemp", Data: "0.2"}
	btnT4  = tele.Btn{Text: "0.4", Unique: "btntemp", Data: "0.4"}
	btnT6  = tele.Btn{Text: "0.6", Unique: "btntemp", Data: "0.6"}
	btnT8  = tele.Btn{Text: "0.8", Unique: "btntemp", Data: "0.8"}
	btnT10 = tele.Btn{Text: "1.0", Unique: "btntemp", Data: "1.0"}
)

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

	usage, err := s.getUsageMonth()
	if err != nil {
		log.Println(err)
	}
	log.Printf("Current usage: %0.2f", usage)

	b.Handle(cmdStart, func(c tele.Context) error {
		return c.Send(msgStart, "text", &tele.SendOptions{
			ReplyTo: c.Message(),
		})
	})

	b.Handle(cmdModel, func(c tele.Context) error {
		menu.Inline(menu.Row(btn3, btn4, btn316))

		return c.Send("Select model", menu)
	})

	b.Handle(cmdTemp, func(c tele.Context) error {
		menu.Inline(menu.Row(btnT0, btnT2, btnT4, btnT6, btnT8, btnT10))
		chat := s.getChat(c.Chat().ID)

		return c.Send(fmt.Sprintf("Set temperature from less random (0.0) to more random (1.0.\nCurrent: %0.2f (default: 0.8)", chat.Temperature), menu)
	})

	b.Handle(cmdPrompt, func(c tele.Context) error {
		query := c.Message().Payload
		if len(query) < 3 {
			return c.Send("Please provide a longer prompt", "text", &tele.SendOptions{
				ReplyTo: c.Message(),
			})
			//return c.Send("Please provide a longer query", "text", &tele.SendOptions{
			//	ReplyTo:     c.Message(),
			//	ReplyMarkup: &tele.ReplyMarkup{ForceReply: true},
			//})
		}

		chat := s.getChat(c.Chat().ID)
		chat.MasterPrompt = query
		s.db.Save(&chat)

		return nil
	})

	b.Handle(cmdPromptCL, func(c tele.Context) error {
		chat := s.getChat(c.Chat().ID)
		chat.MasterPrompt = masterPrompt
		s.db.Save(&chat)

		return c.Send("Default prompt set", "text", &tele.SendOptions{ReplyTo: c.Message()})
	})

	b.Handle(cmdStream, func(c tele.Context) error {
		chat := s.getChat(c.Chat().ID)
		chat.Stream = !chat.Stream
		s.db.Save(&chat)
		status := "disabled"
		if chat.Stream {
			status = "enabled"
		}

		return c.Send("Stream is "+status, "text", &tele.SendOptions{ReplyTo: c.Message()})
	})

	b.Handle(cmdInfo, func(c tele.Context) error {
		chat := s.getChat(c.Chat().ID)
		status := "disabled"
		if chat.Stream {
			status = "enabled"
		}

		usage, err := s.getUsageMonth()
		if err != nil {
			log.Println(err)
		}
		log.Printf("Current usage: %0.2f", usage)

		return c.Send(fmt.Sprintf("Model: %s\nTemperature: %0.2f\nPrompt: %s\nStreaming: %s\nUsage: $%0.2f",
			chat.ModelName, chat.Temperature, chat.MasterPrompt, status, usage,
		),
			"text",
			&tele.SendOptions{ReplyTo: c.Message()},
		)
	})

	b.Handle(cmdToJapanese, func(c tele.Context) error {
		query := c.Message().Payload
		if len(query) < 3 {
			return c.Send("Please provide a longer prompt", "text", &tele.SendOptions{
				ReplyTo: c.Message(),
			})
		}
		response, err := s.answer("To Japanese: "+query, c)
		if err != nil {
			log.Println(err)
			return c.Send(err.Error(), "text", &tele.SendOptions{
				ReplyTo: c.Message(),
			})
		}
		return c.Send(response, "text", &tele.SendOptions{
			ReplyTo:   c.Message(),
			ParseMode: tele.ModeMarkdown,
		})
	})

	b.Handle(cmdToEnglish, func(c tele.Context) error {
		query := c.Message().Payload
		if len(query) < 1 {
			return c.Send("Please provide a longer prompt", "text", &tele.SendOptions{
				ReplyTo: c.Message(),
			})
		}
		response, err := s.answer("To English: "+query, c)
		if err != nil {
			log.Println(err)
			return c.Send(err.Error(), "text", &tele.SendOptions{
				ReplyTo: c.Message(),
			})
		}
		return c.Send(response, "text", &tele.SendOptions{
			ReplyTo:   c.Message(),
			ParseMode: tele.ModeMarkdown,
		})
	})

	// On inline button pressed (callback)
	b.Handle(&btn3, func(c tele.Context) error {
		log.Printf("%s selected", c.Data())
		chat := s.getChat(c.Chat().ID)
		chat.ModelName = c.Data()
		s.db.Save(&chat)

		return c.Edit("Model set to " + c.Data())
	})

	// On inline button pressed (callback)
	b.Handle(&btn316, func(c tele.Context) error {
		log.Printf("%s selected", c.Data())
		chat := s.getChat(c.Chat().ID)
		chat.ModelName = c.Data()
		s.db.Save(&chat)

		return c.Edit("Model set to " + c.Data())
	})

	// On inline button pressed (callback)
	b.Handle(&btnT0, func(c tele.Context) error {
		log.Printf("Temp: %s\n", c.Data())
		chat := s.getChat(c.Chat().ID)
		chat.Temperature, _ = strconv.ParseFloat(c.Data(), 64)
		s.db.Save(&chat)

		return c.Edit("Temperature set to " + c.Data())
	})

	b.Handle(cmdReset, func(c tele.Context) error {
		chat := s.getChat(c.Chat().ID)
		s.deleteHistory(chat.ID)

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
			if transcript, err := s.ai.CreateTranscription(audio, "whisper-1", nil); err != nil {
				log.Printf("failed to create transcription: %s\n", err)
				return c.Send("Failed to create transcription")
			} else {
				if transcript.JSON == nil &&
					transcript.Text == nil &&
					transcript.SRT == nil &&
					transcript.VerboseJSON == nil &&
					transcript.VTT == nil {
					return fmt.Errorf("there was no returned data")
				}

				s.complete(c, *transcript.Text, false)
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
	chat := s.getChat(c.Chat().ID)
	if strings.HasPrefix(strings.ToLower(message), "reset") {
		s.deleteHistory(chat.ID)
		_ = c.Send(msgReset, "text", &tele.SendOptions{
			ReplyTo: c.Message(),
		})
		return
	}

	text := "..."
	if !reply {
		text = fmt.Sprintf("_Transcript:_\n%s\n\n_Answer:_\n...", message)
		chat.SentMessage, _ = c.Bot().Send(c.Recipient(), text, "text", &tele.SendOptions{
			ReplyTo:   c.Message(),
			ParseMode: tele.ModeMarkdown,
		})
	}

	response, err := s.answer(message, c)
	if err != nil {
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
	if !reply {
		text = text[:len(text)-3] + response
		if _, err := c.Bot().Edit(chat.SentMessage, text, "text", &tele.SendOptions{
			ReplyTo:   c.Message(),
			ParseMode: tele.ModeMarkdown,
		}); err != nil {
			c.Bot().Edit(chat.SentMessage, text)
		}
		return
	}

	_ = c.Send(response, "text", &tele.SendOptions{
		ReplyTo:   c.Message(),
		ParseMode: tele.ModeMarkdown,
	})
}

// generate an answer to given message and send it to the chat
func (s Server) answer(message string, c tele.Context) (string, error) {
	_ = c.Notify(tele.Typing)
	chat := s.getChat(c.Chat().ID)
	msg := openai.NewChatUserMessage(message)
	system := openai.NewChatSystemMessage(chat.MasterPrompt)

	chat.History = append(chat.History, ChatMessage{Role: msg.Role, Content: msg.Content, ChatID: chat.ChatID})
	history := []openai.ChatMessage{system}
	for _, h := range chat.History {
		history = append(history, openai.ChatMessage{Role: h.Role, Content: h.Content})
	}
	log.Printf("Chat history %d\n", len(history))

	if chat.Stream {
		return s.launchStream(chat, c, history)
	}
	options := openai.ChatCompletionOptions{}
	if chat.ModelName == "gpt-3.5-turbo-16k" {
		options.
			SetFunctions([]openai.ChatCompletionFunction{
				openai.NewChatCompletionFunction(
					"set_reminder",
					"Set a reminder to do something at a specific time.",
					map[string]any{
						"type": "object",
						"properties": map[string]any{
							"reminder": map[string]any{
								"type":        "string",
								"description": "A reminder of what to do, e.g. 'buy groceries'.",
							},
							"time": map[string]any{
								"type":        "number",
								"description": "A time at which to be reminded in minutes from now, e.g. 1440.",
							},
						},
						"required": []string{"reminder", "time"},
					},
				),
				openai.NewChatCompletionFunction(
					"make_summary",
					"Make a summary of a web page.",
					map[string]any{
						"type": "object",
						"properties": map[string]any{
							"url": map[string]any{
								"type":        "string",
								"description": "A valid URL to a web page.",
							},
						},
						"required": []string{"url"},
					},
				),
			}).
			SetFunctionCall(openai.ChatCompletionFunctionCallAuto)
	}
	response, err := s.ai.CreateChatCompletion(chat.ModelName, history,
		options.
			SetUser(userAgent(c.Sender().ID)).
			SetTemperature(chat.Temperature))

	if err != nil {
		log.Printf("failed to create chat completion: %s", err)
		return err.Error(), err
	}
	if s.conf.Verbose {
		log.Printf("[verbose] %s ===> %+v", message, response.Choices)
	}

	result := response.Choices[0].Message
	if result.FunctionCall != nil {
		return s.handleFunctionCall(c, result)
	}

	_ = c.Notify(tele.Typing)

	var answer string
	if len(response.Choices) > 0 {
		answer = response.Choices[0].Message.Content
		s.saveHistory(chat, answer)
	} else {
		answer = "No response from API."
	}

	if s.conf.Verbose {
		log.Printf("[verbose] sending answer: '%s'", answer)
	}

	return answer, nil
}

// checks if given update is allowed or not
func (s Server) isAllowed(username string) bool {
	_, exists := s.users[username]

	return exists
}

// getChat returns chat from db or creates a new one
func (s Server) getChat(chatID int64) Chat {
	var chat Chat
	s.db.FirstOrCreate(&chat, Chat{ChatID: chatID})
	if len(chat.MasterPrompt) == 0 {
		chat.MasterPrompt = masterPrompt
		chat.ModelName = "gpt-3.5-turbo"
		chat.Temperature = 0.8
		s.db.Save(&chat)
	}
	s.db.Find(&chat.History, "chat_id = ?", chat.ID)
	log.Printf("History %d, chatid %d\n", len(chat.History), chat.ID)

	return chat
}

func (s Server) deleteHistory(chatID uint) {
	s.db.Where("chat_id = ?", chatID).Delete(&ChatMessage{})
}

func (s Server) saveHistory(chat Chat, answer string) {
	msg := openai.NewChatAssistantMessage(answer)
	chat.History = append(chat.History, ChatMessage{Role: msg.Role, Content: msg.Content, ChatID: chat.ChatID})
	log.Printf("chat history len: %d", len(chat.History))

	if len(chat.History) > 8 {
		log.Printf("Chat history for chat ID %d is too long. Summarising...\n", chat.ID)
		summary, err := s.summarize(chat.History)
		if err != nil {
			log.Println("Failed to summarise chat history: ", err)
			return
		}

		if s.conf.Verbose {
			log.Println("Summary: ", summary)
		}
		maxID := chat.History[len(chat.History)-1].ID
		s.db.Where("chat_id = ?", chat.ID).Where("id <= ?", maxID).Delete(&ChatMessage{})
		msg = openai.NewChatUserMessage(summary)
		chat.History = []ChatMessage{{Role: msg.Role, Content: msg.Content, ChatID: chat.ChatID}}
	}
	s.db.Save(&chat)
}

func (s Server) summarize(chatHistory []ChatMessage) (string, error) {
	msg := openai.NewChatUserMessage("Make a compressed summary of the conversation with the AI. Try to be as brief as possible.")
	system := openai.NewChatSystemMessage("Be as brief as possible")

	history := []openai.ChatMessage{system}
	for _, h := range chatHistory {
		history = append(history, openai.ChatMessage{Role: h.Role, Content: h.Content})
	}
	history = append(history, msg)

	log.Printf("Chat history %d\n", len(history))

	response, err := s.ai.CreateChatCompletion("gpt-3.5-turbo", history, openai.ChatCompletionOptions{}.SetUser(userAgent(31337)).SetTemperature(0.2))

	if err != nil {
		log.Printf("failed to create chat completion: %s", err)
		return "", err
	}
	return response.Choices[0].Message.Content, nil
}

// get billing usage
func (s Server) getUsageMonth() (float64, error) {
	now := time.Now()
	firstDay := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	lastDay := firstDay.AddDate(0, 1, -1)

	client := &http.Client{}
	req, err := http.NewRequest("GET", "https://api.openai.com/dashboard/billing/usage", nil)
	if err != nil {
		return 0, err
	}

	query := req.URL.Query()
	query.Add("start_date", firstDay.Format("2006-01-02"))
	query.Add("end_date", lastDay.Format("2006-01-02"))
	req.URL.RawQuery = query.Encode()

	req.Header.Add("Authorization", "Bearer "+s.conf.OpenAIAPIKey)

	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	var billingData BillingData
	err = json.NewDecoder(resp.Body).Decode(&billingData)
	if err != nil {
		return 0, err
	}

	return billingData.TotalUsage / 100, nil
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

func (s Server) setReminder(chatID int64, reminder string, minutes int64) error {
	timer := time.NewTimer(time.Minute * time.Duration(minutes))
	go func() {
		<-timer.C
		fmt.Println("Timer fired")

		if _, err := s.bot.Send(tele.ChatID(chatID), reminder); err != nil {
			log.Println(err)
		}
	}()

	return nil
}
