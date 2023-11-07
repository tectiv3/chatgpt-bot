package main

import (
	"encoding/base64"
	"github.com/meinside/openai-go"
	tele "gopkg.in/telebot.v3"
	"gorm.io/gorm"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"time"
)

var modelCosts map[string]ModelCosts

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

type UsageResponseBody struct {
	Object string `json:"object"`
	Data   []struct {
		AggregationTimestamp  int    `json:"aggregation_timestamp"`
		NRequests             int    `json:"n_requests"`
		Operation             string `json:"operation"`
		SnapshotID            string `json:"snapshot_id"`
		NContext              int    `json:"n_context"`
		NContextTokensTotal   int    `json:"n_context_tokens_total"`
		NGenerated            int    `json:"n_generated"`
		NGeneratedTokensTotal int    `json:"n_generated_tokens_total"`
	} `json:"data"`
	FtData          []interface{} `json:"ft_data"`
	DalleAPIData    []interface{} `json:"dalle_api_data"`
	CurrentUsageUsd float64       `json:"current_usage_usd"`
}

type Server struct {
	conf  config
	users map[string]bool
	ai    *openai.Client
	bot   *tele.Bot
	db    *gorm.DB
}

type User struct {
	gorm.Model
	TelegramID *int64 `gorm:"nullable:true"`
	Username   string
	ApiKey     *string `gorm:"nullable:true"`
	OrgID      *string `gorm:"nullable:true"`
	Threads    []Chat
}

type Chat struct {
	gorm.Model
	ChatID          int64
	UserID          uint `json:"user_id" gorm:"nullable:true"`
	History         []ChatMessage
	Temperature     float64
	ModelName       string
	MasterPrompt    string
	Stream          bool
	ConversationAge int64
	TotalTokens     int `json:"total_tokens"`
}

type ChatMessage struct {
	ID        uint `gorm:"primarykey"`
	CreatedAt time.Time
	UpdatedAt time.Time
	ChatID    int64
	Role      openai.ChatMessageRole `json:"role"`
	Content   *string                `json:"content,omitempty"`
}

// WAV writer struct
type wavWriter struct {
	w io.Writer
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

// RestrictConfig defines config for Restrict middleware.
type RestrictConfig struct {
	// Chats is a list of chats that are going to be affected
	// by either In or Out function.
	Usernames []string

	// In defines a function that will be called if the chat
	// of an update will be found in the Chats list.
	In tele.HandlerFunc

	// Out defines a function that will be called if the chat
	// of an update will NOT be found in the Chats list.
	Out tele.HandlerFunc
}

func in_array(needle string, haystack []string) bool {
	for _, v := range haystack {
		if needle == v {
			return true
		}
	}

	return false
}

type ModelCosts struct {
	Context   float64
	Generated float64
}

func init() {
	modelCosts = map[string]ModelCosts{
		"gpt-3.5-turbo-0301":     {Context: 0.0015, Generated: 0.002},
		"gpt-3.5-turbo-0613":     {Context: 0.0015, Generated: 0.002},
		"gpt-3.5-turbo-16k":      {Context: 0.003, Generated: 0.004},
		"gpt-3.5-turbo-16k-0613": {Context: 0.003, Generated: 0.004},
		"gpt-4-0314":             {Context: 0.03, Generated: 0.06},
		"gpt-4-0613":             {Context: 0.03, Generated: 0.06},
		"gpt-4-32k":              {Context: 0.06, Generated: 0.12},
		"gpt-4-32k-0314":         {Context: 0.06, Generated: 0.12},
		"gpt-4-32k-0613":         {Context: 0.06, Generated: 0.12},
		"whisper-1":              {Context: 0.006 / 60, Generated: 0}, // Cost is per second, so convert to minutes
	}
}

type CoinCap struct {
	Data struct {
		Symbol   string `json:"symbol"`
		PriceUsd string `json:"priceUsd"`
	} `json:"data"`
	Timestamp int64 `json:"timestamp"`
}

func toBase64(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}

func getBase64(filename string) string {
	// Read the entire file into a byte slice
	bytes, err := ioutil.ReadFile(filename)
	if err != nil {
		log.Fatal(err)
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
	return toBase64(bytes)
}
