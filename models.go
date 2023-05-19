package main

import (
	"github.com/meinside/openai-go"
	tele "gopkg.in/telebot.v3"
	"gorm.io/gorm"
	"time"
)

// DB contains chat history
type DB struct {
	chats map[int64]Chat
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
	openai.ChatMessage
}
