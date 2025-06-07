package main

import (
	"context"
	"database/sql/driver"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/meinside/openai-go"
	"github.com/tectiv3/awsnova-go"
	tele "gopkg.in/telebot.v3"
	"gorm.io/gorm"
)

// config struct for loading a configuration file
type config struct {
	// telegram bot api
	TelegramBotToken  string `json:"telegram_bot_token"`
	TelegramServerURL string `json:"telegram_server_url"`

	Models []AiModel `json:"models"`

	// openai api
	OpenAIAPIKey         string `json:"openai_api_key"`
	OpenAIOrganizationID string `json:"openai_org_id"`
	OpenAILatestModel    string `json:"openai_latest_model"`
	OllamaURL            string `json:"ollama_url"`
	OllamaModel          string `json:"ollama_model"`
	OllamaEnabled        bool   `json:"ollama_enabled"`
	GroqModel            string `json:"groq_model"`
	GroqAPIKey           string `json:"groq_api_key"`

	AnthropicAPIKey  string `json:"anthropic_api_key"`
	AnthropicEnabled bool   `json:"anthropic_enabled"`

	AWSAccessKeyID     string `json:"aws_access_key_id"`
	AWSSecretAccessKey string `json:"aws_secret_access_key"`
	AWSModelID         string `json:"aws_model_id"`
	AWSRegion          string `json:"aws_region"`
	AWSEnabled         bool   `json:"aws_enabled"`

	// other configurations
	AllowedTelegramUsers []string `json:"allowed_telegram_users"`
	Verbose              bool     `json:"verbose,omitempty"`
	PiperDir             string   `json:"piper_dir"`
}

type AiModel struct {
	ModelID  string `json:"model_id"`
	Name     string `json:"name"`
	Provider string `json:"provider"` // openai, ollama, groq, nova
}

type Server struct {
	sync.RWMutex
	conf      config
	users     []string
	openAI    *openai.Client
	anthropic *openai.Client
	nova      *awsnova.Client
	bot       *tele.Bot
	db        *gorm.DB
}

type User struct {
	gorm.Model
	TelegramID *int64 `gorm:"nullable:true"`
	Username   string
	ApiKey     *string `gorm:"nullable:true"`
	OrgID      *string `gorm:"nullable:true"`
	Threads    []Chat
	Roles      []Role
	State      *State `json:"state,omitempty" gorm:"type:text"`
}

type Role struct {
	gorm.Model
	UserID uint `json:"user_id"`
	Name   string
	Prompt string
}

type Chat struct {
	gorm.Model
	mutex           sync.Mutex `gorm:"-"`
	ChatID          int64      `sql:"chat_id" json:"chat_id"`
	UserID          uint       `json:"user_id" gorm:"nullable:false"`
	RoleID          *uint      `json:"role_id" gorm:"nullable:true"`
	MessageID       *string    `json:"last_message_id" gorm:"nullable:true"`
	Lang            string
	History         []ChatMessage
	User            User `gorm:"foreignKey:UserID;references:ID;fetch:join"`
	Role            Role `gorm:"foreignKey:RoleID;references:ID;fetch:join"`
	Temperature     float64
	ModelName       string
	MasterPrompt    string
	Stream          bool
	QA              bool
	Voice           bool
	ConversationAge int64
	TotalTokens     int `json:"total_tokens"`
}

type ChatMessage struct {
	ID        uint `gorm:"primarykey"`
	CreatedAt time.Time
	UpdatedAt time.Time
	ChatID    int64 `sql:"chat_id" json:"chat_id"`

	Role       openai.ChatMessageRole `json:"role"`
	ToolCallID *string                `json:"tool_call_id,omitempty"`
	Content    *string                `json:"content,omitempty"`
	ImagePath  *string                `json:"image_path,omitempty"`

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

// Context handling utilities

// WithTimeout creates a context with timeout for operations
func WithTimeout(duration time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), duration)
}

// WithCancel creates a cancellable context
func WithCancel() (context.Context, context.CancelFunc) {
	return context.WithCancel(context.Background())
}

// DefaultTimeout for operations
const (
	DefaultTimeout = 30 * time.Second
	LongTimeout    = 5 * time.Minute
)
