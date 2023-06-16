package main

import (
	"github.com/meinside/openai-go"
	tele "gopkg.in/telebot.v3"
	"gorm.io/gorm"
	"io"
	"time"
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

type BillingData struct {
	TotalUsage float64 `json:"total_usage"`
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
	TelegramID int64
	ApiKey     string
	OrgID      string
	Threads    []Chat
}

type Chat struct {
	gorm.Model
	ChatID       int64
	UserID       uint `json:"user_id" gorm:"nullable:true"`
	History      []ChatMessage
	Temperature  float64
	ModelName    string
	MasterPrompt string
	Stream       bool
	SentMessage  *tele.Message `gorm:"-"`
}

type ChatMessage struct {
	ID        uint `gorm:"primarykey"`
	CreatedAt time.Time
	UpdatedAt time.Time
	ChatID    int64
	Role      openai.ChatMessageRole `json:"role"`
	Content   string                 `json:"content,omitempty"`
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
