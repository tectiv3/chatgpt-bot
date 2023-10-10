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
	TelegramID *int64 `gorm:"nullable:true"`
	Username   string
	ApiKey     *string `gorm:"nullable:true"`
	OrgID      *string `gorm:"nullable:true"`
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
	ConversationAge int64
	TotalTokens  int `json:"total_tokens"`
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
