package main

import (
	"database/sql/driver"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/meinside/openai-go"
	tele "gopkg.in/telebot.v3"
	"gorm.io/gorm"
	"io"
	"sync"
	"time"
)

// config struct for loading a configuration file
type config struct {
	// telegram bot api
	TelegramBotToken  string `json:"telegram_bot_token"`
	TelegramServerURL string `json:"telegram_server_url"`

	// openai api
	OpenAIAPIKey         string `json:"openai_api_key"`
	OpenAIOrganizationID string `json:"openai_org_id"`
	OllamaURL            string `json:"ollama_url"`
	OllamaModel          string `json:"ollama_model"`

	// other configurations
	AllowedTelegramUsers []string `json:"allowed_telegram_users"`
	Verbose              bool     `json:"verbose,omitempty"`
	Model                string   `json:"openai_model"`
}

type Server struct {
	sync.RWMutex
	conf  config
	users []string
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
	ChatID          int64 `sql:"chat_id" json:"chat_id"`
	UserID          uint  `json:"user_id" gorm:"nullable:true"`
	History         []ChatMessage
	Temperature     float64
	ModelName       string
	MasterPrompt    string
	Stream          bool
	Voice           bool
	ConversationAge int64
	TotalTokens     int        `json:"total_tokens"`
	mutex           sync.Mutex `gorm:"-"`
	MessageID       *string    `json:"last_message_id"`
}

type ChatMessage struct {
	ID        uint `gorm:"primarykey"`
	CreatedAt time.Time
	UpdatedAt time.Time
	ChatID    int64 `sql:"chat_id" json:"chat_id"`

	Role       openai.ChatMessageRole `json:"role"`
	ToolCallID *string                `json:"tool_call_id,omitempty"`
	Content    *string                `json:"content,omitempty"`

	// for function call
	ToolCalls ToolCalls `json:"tool_calls,omitempty" gorm:"type:text"` // when role == 'assistant'
}

// ToolCalls is a custom type that will allow us to implement
// the driver.Valuer and sql.Scanner interfaces on a slice of ToolCall.
type ToolCalls []ToolCall

type ToolCall struct {
	ID       string                  `json:"id"`
	Type     string                  `json:"type"` // == 'function'
	Function openai.ToolCallFunction `json:"function"`
}

// Value implements the driver.Valuer interface, allowing
// for converting the ToolCalls to a JSON string for database storage.
func (tc ToolCalls) Value() (driver.Value, error) {
	if tc == nil {
		return nil, nil
	}
	return json.Marshal(tc)
}

// Scan implements the sql.Scanner interface, allowing for
// converting a JSON string from the database back into the ToolCalls slice.
func (tc *ToolCalls) Scan(value interface{}) error {
	if value == nil {
		*tc = nil
		return nil
	}

	b, ok := value.([]byte)
	if !ok {
		return fmt.Errorf("type assertion to []byte failed")
	}

	return json.Unmarshal(b, &tc)
}

type GPTResponse interface {
	Type() string       // direct, array, image, audio, async
	Value() interface{} // string, []string
	CanReply() bool     // if true replyMenu need to be shown
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
