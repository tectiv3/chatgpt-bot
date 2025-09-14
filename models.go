package main

import (
	"context"
	"database/sql/driver"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/meinside/openai-go"
	"github.com/tectiv3/anthropic-go"
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

	OllamaURL     string `json:"ollama_url"`
	OllamaEnabled bool   `json:"ollama_enabled"`

	GroqAPIKey string `json:"groq_api_key"`

	AnthropicAPIKey  string `json:"anthropic_api_key"`
	AnthropicEnabled bool   `json:"anthropic_enabled"`

	AWSAccessKeyID     string `json:"aws_access_key_id"`
	AWSSecretAccessKey string `json:"aws_secret_access_key"`
	AWSModelID         string `json:"aws_model_id"`
	AWSRegion          string `json:"aws_region"`
	AWSEnabled         bool   `json:"aws_enabled"`

	GeminiEnabled bool   `json:"gemini_enabled"`
	GeminiAPIKey  string `json:"gemini_api_key"`

	// other configurations
	AllowedTelegramUsers []string `json:"allowed_telegram_users"`
	Verbose              bool     `json:"verbose,omitempty"`
	PiperDir             string   `json:"piper_dir"`

	// Mini app configuration
	MiniAppEnabled bool   `json:"mini_app_enabled"`
	WebServerPort  string `json:"web_server_port"`
	MiniAppURL     string `json:"mini_app_url"`
}

type AiModel struct {
	ModelID         string `json:"model_id"`
	Name            string `json:"name"`
	Provider        string `json:"provider"` // openai, ollama, groq, nova
	SearchTool      string `json:"search_tool,omitempty"`
	Reasoning       bool   `json:"reasoning,omitempty"`
	CodeInterpreter bool   `json:"code_interpreter,omitempty"`
}

type Server struct {
	sync.RWMutex
	conf      config
	users     []string
	openAI    *openai.Client
	anthropic *anthropic.Client
	gemini    *openai.Client
	nova      *awsnova.Client
	bot       *tele.Bot
	db        *gorm.DB
	webServer *http.Server

	// Rate limiting and connection management for webapp
	rateLimiter       *RateLimiter
	connectionManager *ConnectionManager
}

// Rate limiting and connection management
type RateLimiter struct {
	mu          sync.RWMutex
	requests    map[uint][]time.Time // userID -> request timestamps
	maxRequests int
	window      time.Duration
}

func NewRateLimiter(maxRequests int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		requests:    make(map[uint][]time.Time),
		maxRequests: maxRequests,
		window:      window,
	}
}

func (rl *RateLimiter) Allow(userID uint) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	windowStart := now.Add(-rl.window)

	// Get user's request history
	requests := rl.requests[userID]

	// Filter out old requests outside the window
	var validRequests []time.Time
	for _, req := range requests {
		if req.After(windowStart) {
			validRequests = append(validRequests, req)
		}
	}

	// Check if under limit
	if len(validRequests) >= rl.maxRequests {
		rl.requests[userID] = validRequests
		return false
	}

	// Add current request
	validRequests = append(validRequests, now)
	rl.requests[userID] = validRequests
	return true
}

// Connection manager for polling
type ConnectionManager struct {
	mu             sync.RWMutex
	connections    map[uint]int // userID -> active connection count
	maxConnections int
}

func NewConnectionManager(maxConnections int) *ConnectionManager {
	return &ConnectionManager{
		connections:    make(map[uint]int),
		maxConnections: maxConnections,
	}
}

func (cm *ConnectionManager) CanConnect(userID uint) bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.connections[userID] < cm.maxConnections
}

func (cm *ConnectionManager) AddConnection(userID uint) bool {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if cm.connections[userID] >= cm.maxConnections {
		return false
	}

	cm.connections[userID]++
	return true
}

func (cm *ConnectionManager) RemoveConnection(userID uint) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if cm.connections[userID] > 0 {
		cm.connections[userID]--
	}
	if cm.connections[userID] == 0 {
		delete(cm.connections, userID)
	}
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
	ThreadID        *string    `json:"thread_id" gorm:"index;nullable:true"` // NULL for default chat, UUID for threads
	ThreadTitle     *string    `json:"thread_title" gorm:"nullable:true"`    // Generated topic for thread
	RoleID          *uint      `json:"role_id" gorm:"nullable:true"`
	MessageID       *string    `json:"last_message_id" gorm:"nullable:true"`
	ArchivedAt      *time.Time `json:"archived_at" gorm:"nullable:true"` // NULL for active threads, timestamp when archived
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
	ContextLimit    int `json:"context_limit" gorm:"default:4000"` // Context limit for this thread

	// Thread-level token tracking
	TotalInputTokens  int `json:"total_input_tokens" gorm:"default:0"`
	TotalOutputTokens int `json:"total_output_tokens" gorm:"default:0"`
}

type ChatMessage struct {
	ID        uint      `gorm:"primarykey"`
	CreatedAt time.Time `gorm:"index"`
	UpdatedAt time.Time
	ChatID    int64 `sql:"chat_id" json:"chat_id" gorm:"index"`

	Role       openai.ChatMessageRole `json:"role"`
	ToolCallID *string                `json:"tool_call_id,omitempty"`
	Content    *string                `json:"content,omitempty"`
	ImagePath  *string                `json:"image_path,omitempty"`
	Filename   *string                `json:"filename,omitempty"`

	// Context management
	IsLive      bool   `json:"is_live" gorm:"default:true;index"`  // If false, not sent to model
	MessageType string `json:"message_type" gorm:"default:normal"` // normal, summary, system

	// Meta information (nullable for backwards compatibility)
	InputTokens    *int    `json:"input_tokens,omitempty" gorm:"nullable"`
	OutputTokens   *int    `json:"output_tokens,omitempty" gorm:"nullable"`
	TotalTokens    *int    `json:"total_tokens,omitempty" gorm:"nullable"`
	ModelUsed      *string `json:"model_used,omitempty" gorm:"size:100;nullable"`
	ResponseTimeMs *int64  `json:"response_time_ms,omitempty" gorm:"nullable"`
	FinishReason   *string `json:"finish_reason,omitempty" gorm:"size:50;nullable"`

	// Annotation data for code interpreter outputs (nullable for backwards compatibility)
	AnnotationContainerID *string `json:"annotation_container_id,omitempty" gorm:"size:100;nullable"` // OpenAI container ID
	AnnotationFileID      *string `json:"annotation_file_id,omitempty" gorm:"size:100;nullable"`
	AnnotationFilename    *string `json:"annotation_filename,omitempty" gorm:"size:255;nullable"`
	AnnotationFileType    *string `json:"annotation_file_type,omitempty" gorm:"size:50;nullable"` // image, document, etc.

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
