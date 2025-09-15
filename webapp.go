package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/meinside/openai-go"
	initdata "github.com/telegram-mini-apps/init-data-golang"
	"gorm.io/gorm"
)

// Thread settings structure
type ThreadSettings struct {
	ModelName    string  `json:"model_name"`
	Temperature  float64 `json:"temperature"`
	RoleID       *uint   `json:"role_id"`
	Lang         string  `json:"lang"`
	MasterPrompt string  `json:"master_prompt"`
	ContextLimit int     `json:"context_limit"`
}

// API request/response structures
type CreateThreadRequest struct {
	InitialMessage string          `json:"initial_message"`
	Settings       *ThreadSettings `json:"settings,omitempty"`
	Images         []ImageUpload   `json:"images,omitempty"`
}

type ChatRequest struct {
	Message string       `json:"message"`
	Image   *ImageUpload `json:"image,omitempty"`
}

// Image upload structure
type ImageUpload struct {
	ID       string `json:"id"`
	URL      string `json:"url,omitempty"`
	Data     string `json:"data,omitempty"` // Base64 image data
	Filename string `json:"filename"`
	Size     int64  `json:"size,omitempty"`
	MimeType string `json:"mime_type"`
}

type ImageUploadResponse struct {
	ID       string `json:"id"`
	URL      string `json:"url"`
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
	MimeType string `json:"mime_type"`
}

type ThreadResponse struct {
	ID                string         `json:"id"`
	Title             string         `json:"title"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
	ArchivedAt        *time.Time     `json:"archived_at"`
	Settings          ThreadSettings `json:"settings"`
	MessageCount      int            `json:"message_count"`
	TotalInputTokens  int            `json:"total_input_tokens"`
	TotalOutputTokens int            `json:"total_output_tokens"`
}

type MessageResponse struct {
	ID          uint      `json:"id"`
	Role        string    `json:"role"`
	Content     *string   `json:"content"`
	CreatedAt   time.Time `json:"created_at"`
	IsLive      bool      `json:"is_live"`
	MessageType string    `json:"message_type"`
	ImageData   *string   `json:"image_data,omitempty"` // URL to the image
	ImageName   *string   `json:"image_name,omitempty"` // Original filename

	InputTokens    *int    `json:"input_tokens,omitempty"`
	OutputTokens   *int    `json:"output_tokens,omitempty"`
	TotalTokens    *int    `json:"total_tokens,omitempty"`
	ModelUsed      *string `json:"model_used,omitempty"`
	ResponseTimeMs *int64  `json:"response_time_ms,omitempty"`
	FinishReason   *string `json:"finish_reason,omitempty"`

	Annotations Annotations `json:"annotations,omitempty"`
}

type ChatWithThreadResponse struct {
	Message *MessageResponse `json:"message,omitempty"`
	Thread  *ThreadResponse  `json:"thread,omitempty"`
}

type ModelResponse struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
}

type RoleResponse struct {
	ID     uint   `json:"id"`
	Name   string `json:"name"`
	Prompt string `json:"prompt"`
}

// ChatWithCount struct for optimized database queries
type ChatWithCount struct {
	Chat
	MessageCount int64
}

func (s *Server) setupWebServer() *http.ServeMux {
	if !s.conf.MiniAppEnabled {
		return nil
	}

	mux := http.NewServeMux()

	// Serve static files
	mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir("webapp/assets"))))
	// Serve uploaded images
	mux.Handle("/uploads/", http.StripPrefix("/uploads/", http.FileServer(http.Dir("uploads"))))

	// Mini app route
	mux.HandleFunc("/miniapp", s.corsMiddleware(s.serveMiniApp))

	mux.HandleFunc("/api/threads", s.apiMiddleware(s.handleThreads))
	mux.HandleFunc("/api/threads/archived", s.apiMiddleware(s.getArchivedThreads))
	mux.HandleFunc("/api/messages", s.apiMiddleware(s.handleDraftMessages)) // Direct messages endpoint for draft threads
	mux.HandleFunc("/api/threads/", s.apiMiddleware(s.handleThreadsWithID))
	mux.HandleFunc("/api/models", s.apiMiddleware(s.getAvailableModels))
	mux.HandleFunc("/api/roles", s.apiMiddleware(s.handleRoles))
	mux.HandleFunc("/api/roles/", s.apiMiddleware(s.handleRolesWithID))
	mux.HandleFunc("/api/messages/", s.apiMiddleware(s.handleMessagesWithID)) // Message operations (delete, etc.)
	mux.HandleFunc("/api/user", s.apiMiddleware(s.getUserInfo))
	mux.HandleFunc("/api/upload-image", s.apiMiddleware(s.handleImageUpload))

	return mux
}

// corsMiddleware for non-API routes
func (s *Server) corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Telegram-Init-Data")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next(w, r)
	}
}

// apiMiddleware consolidates CORS, auth
func (s *Server) apiMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// CORS headers
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Telegram-Init-Data")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		// Authentication
		initDataString := r.Header.Get("Telegram-Init-Data")
		if initDataString == "" {
			s.writeJSONError(w, http.StatusUnauthorized, "Missing Telegram init data")
			return
		}

		if err := initdata.Validate(initDataString, s.conf.TelegramBotToken, 24*time.Hour); err != nil {
			s.writeJSONError(w, http.StatusUnauthorized, "Invalid Telegram authentication")
			return
		}

		parsed, err := initdata.Parse(initDataString)
		if err != nil {
			s.writeJSONError(w, http.StatusUnauthorized, "Failed to parse init data")
			return
		}

		user, err := s.getOrCreateUserFromInitData(parsed)
		if err != nil {
			s.writeJSONError(w, http.StatusInternalServerError, "Failed to get user")
			return
		}

		// Add user to context and proceed
		ctx := context.WithValue(r.Context(), userContextKey, user)
		next(w, r.WithContext(ctx))
	}
}

// Auth context
type contextKey string

const userContextKey contextKey = "user"

// Helper functions for JSON responses
func (s *Server) writeJSONError(w http.ResponseWriter, status int, message string) {
	s.writeJSON(w, status, map[string]string{"error": message})
}

func (s *Server) writeJSONSuccess(w http.ResponseWriter, message string, data interface{}) {
	response := map[string]interface{}{"message": message}
	if data != nil {
		response["data"] = data
	}
	s.writeJSON(w, http.StatusOK, response)
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// Extract path parameter from URL (e.g., /api/threads/123 -> 123)
func extractPathParam(path, prefix string) string {
	if strings.HasPrefix(path, prefix) {
		param := strings.TrimPrefix(path, prefix)
		param = strings.TrimPrefix(param, "/")
		// Remove any trailing path segments
		if idx := strings.Index(param, "/"); idx != -1 {
			param = param[:idx]
		}
		return param
	}
	return ""
}

// Get user from request context
func getUserFromContext(r *http.Request) *User {
	user, ok := r.Context().Value(userContextKey).(*User)
	if !ok {
		return nil
	}
	return user
}

func (s *Server) getOrCreateUserFromInitData(initData initdata.InitData) (*User, error) {
	var user User

	if initData.User.ID != 0 {
		err := s.db.Preload("Roles").Where("username = ?", initData.User.Username).First(&user).Error
		if err != nil && err != gorm.ErrRecordNotFound {
			return nil, err
		}
		if err == nil {
			return &user, nil
		}

		telegramID := int64(initData.User.ID)
		// Create new user
		user = User{
			TelegramID: &telegramID,
			Username:   initData.User.Username,
		}

		if err := s.db.Create(&user).Error; err != nil {
			return nil, err
		}
	}

	return &user, nil
}

func (s *Server) serveMiniApp(w http.ResponseWriter, r *http.Request) {
	tmpl, err := template.ParseFiles("webapp/templates/miniapp.html")
	if err != nil {
		http.Error(w, "Template error", http.StatusInternalServerError)
		return
	}

	botName := "ChatBot"
	if s.bot != nil && s.bot.Me != nil {
		botName = "@" + s.bot.Me.Username
	}

	data := map[string]string{
		"BotName": botName,
	}

	w.Header().Set("Content-Type", "text/html")
	if err := tmpl.Execute(w, data); err != nil {
		http.Error(w, "Template execution error", http.StatusInternalServerError)
	}
}

// Handle /api/threads (GET and POST)
func (s *Server) handleThreads(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.listThreads(w, r)
	case http.MethodPost:
		s.createThread(w, r)
	default:
		s.writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (s *Server) listThreads(w http.ResponseWriter, r *http.Request) {
	user := getUserFromContext(r)
	if user == nil {
		s.writeJSONError(w, http.StatusUnauthorized, "User not found")
		return
	}

	// Optimized query to get chats with message counts in a single query
	var chatCounts []ChatWithCount
	err := s.db.Table("chats").
		Select("chats.*, COALESCE(msg_count.count, 0) as message_count").
		Joins("LEFT JOIN (SELECT chat_id, COUNT(*) as count FROM chat_messages GROUP BY chat_id) msg_count ON chats.chat_id = msg_count.chat_id").
		Where("chats.user_id = ? AND chats.thread_id IS NOT NULL AND chats.archived_at IS NULL", user.ID).
		Preload("Role").
		Order("chats.updated_at DESC").
		Find(&chatCounts).Error
	if err != nil {
		s.writeJSONError(w, http.StatusInternalServerError, "Failed to fetch threads")
		return
	}

	threads := make([]ThreadResponse, len(chatCounts))
	for i, chatCount := range chatCounts {
		settings := ThreadSettings{
			ModelName:    chatCount.Chat.ModelName,
			Temperature:  chatCount.Chat.Temperature,
			RoleID:       chatCount.Chat.RoleID,
			Lang:         chatCount.Chat.Lang,
			MasterPrompt: chatCount.Chat.MasterPrompt,
			ContextLimit: chatCount.Chat.ContextLimit,
		}

		threads[i] = ThreadResponse{
			ID:                *chatCount.Chat.ThreadID,
			Title:             *chatCount.Chat.ThreadTitle,
			CreatedAt:         chatCount.Chat.CreatedAt,
			UpdatedAt:         chatCount.Chat.UpdatedAt,
			ArchivedAt:        chatCount.Chat.ArchivedAt,
			Settings:          settings,
			MessageCount:      int(chatCount.MessageCount),
			TotalInputTokens:  chatCount.Chat.TotalInputTokens,
			TotalOutputTokens: chatCount.Chat.TotalOutputTokens,
		}
	}

	s.writeJSON(w, http.StatusOK, map[string][]ThreadResponse{"threads": threads})
}

func (s *Server) getArchivedThreads(w http.ResponseWriter, r *http.Request) {
	user := getUserFromContext(r)
	if user == nil {
		s.writeJSONError(w, http.StatusUnauthorized, "User not found")
		return
	}

	// Optimized query to get archived chats with message counts in a single query
	var chatCounts []ChatWithCount
	err := s.db.Table("chats").
		Select("chats.*, COALESCE(msg_count.count, 0) as message_count").
		Joins("LEFT JOIN (SELECT chat_id, COUNT(*) as count FROM chat_messages GROUP BY chat_id) msg_count ON chats.chat_id = msg_count.chat_id").
		Where("chats.user_id = ? AND chats.thread_id IS NOT NULL AND chats.archived_at IS NOT NULL", user.ID).
		Preload("Role").
		Order("chats.archived_at DESC").
		Find(&chatCounts).Error
	if err != nil {
		s.writeJSONError(w, http.StatusInternalServerError, "Failed to fetch archived threads")
		return
	}

	threads := make([]ThreadResponse, len(chatCounts))
	for i, chatCount := range chatCounts {
		settings := ThreadSettings{
			ModelName:    chatCount.Chat.ModelName,
			Temperature:  chatCount.Chat.Temperature,
			RoleID:       chatCount.Chat.RoleID,
			Lang:         chatCount.Chat.Lang,
			MasterPrompt: chatCount.Chat.MasterPrompt,
			ContextLimit: chatCount.Chat.ContextLimit,
		}

		threads[i] = ThreadResponse{
			ID:                *chatCount.Chat.ThreadID,
			Title:             *chatCount.Chat.ThreadTitle,
			CreatedAt:         chatCount.Chat.CreatedAt,
			UpdatedAt:         chatCount.Chat.UpdatedAt,
			ArchivedAt:        chatCount.Chat.ArchivedAt,
			Settings:          settings,
			MessageCount:      int(chatCount.MessageCount),
			TotalInputTokens:  chatCount.Chat.TotalInputTokens,
			TotalOutputTokens: chatCount.Chat.TotalOutputTokens,
		}
	}

	s.writeJSON(w, http.StatusOK, map[string][]ThreadResponse{"threads": threads})
}

func (s *Server) createThread(w http.ResponseWriter, r *http.Request) {
	user := getUserFromContext(r)
	if user == nil {
		s.writeJSONError(w, http.StatusUnauthorized, "User not found")
		return
	}

	// Rate limiting check for thread creation
	if !s.rateLimiter.Allow(user.ID) {
		s.writeJSONError(w, http.StatusTooManyRequests, "Rate limit exceeded. Please wait before creating another thread")
		return
	}

	var req CreateThreadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeJSONError(w, http.StatusBadRequest, "Invalid request format")
		return
	}

	var sanitizedMessage string
	if req.InitialMessage == "" {
		// Allow empty message for thread creation without immediate message
		sanitizedMessage = ""
	} else {
		// Validate and sanitize non-empty initial message
		var err error
		sanitizedMessage, err = validateChatMessage(req.InitialMessage)
		if err != nil {
			s.writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("Invalid initial message: %v", err))
			return
		}
	}
	req.InitialMessage = sanitizedMessage

	// Validate thread settings if provided
	if req.Settings != nil {
		if err := validateThreadSettings(req.Settings); err != nil {
			s.writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("Invalid settings: %v", err))
			return
		}
	}

	// Generate thread ID and title
	threadID := uuid.New().String()
	var title string
	if req.InitialMessage == "" {
		// No message provided - just use simple title
		title = "New Thread"
	} else {
		// Generate title from actual message
		var err error
		title, err = s.generateThreadTitle(req.InitialMessage)
		if err != nil {
			title = "New Thread" // Fallback
		}
	}

	// Create new chat with thread
	chat := Chat{
		UserID:       user.ID,
		ChatID:       int64(user.ID)*1000 + time.Now().Unix()%1000, // Unique chat ID
		ThreadID:     &threadID,
		ThreadTitle:  &title,
		Temperature:  1.0, // Default values
		ModelName:    "gpt-4o",
		Stream:       true,
		ContextLimit: 40000,
	}

	// Apply custom settings if provided
	if req.Settings != nil {
		if req.Settings.ModelName != "" {
			chat.ModelName = req.Settings.ModelName
		}
		if req.Settings.Temperature >= 0 {
			chat.Temperature = req.Settings.Temperature
		}
		if req.Settings.RoleID != nil {
			chat.RoleID = req.Settings.RoleID
		}
		// Deprecated settings fields removed - maintain default values
		// Stream, QA, and Voice settings are no longer configurable via API
		if req.Settings.Lang != "" {
			chat.Lang = req.Settings.Lang
		}
		if req.Settings.MasterPrompt != "" {
			chat.MasterPrompt = req.Settings.MasterPrompt
		}
		if req.Settings.ContextLimit > 0 {
			chat.ContextLimit = req.Settings.ContextLimit
		}
	}

	if err := s.db.Create(&chat).Error; err != nil {
		s.writeJSONError(w, http.StatusInternalServerError, "Failed to create thread")
		return
	}

	// Don't save initial message here - let the subsequent chat request handle it
	// This prevents duplicate messages when images are involved

	s.writeJSONSuccess(w, "Thread created successfully", map[string]interface{}{
		"thread_id": threadID,
		"title":     title,
	})
}

// Handle /api/threads/{id}/... routes
func (s *Server) handleThreadsWithID(w http.ResponseWriter, r *http.Request) {
	threadID := extractPathParam(r.URL.Path, "/api/threads")

	// Allow empty thread ID for draft threads (will be handled in individual endpoints)
	if threadID != "" {
		// Validate thread ID format (UUID) only if provided
		if !strings.HasPrefix(threadID, "temp_") { // Allow temp thread IDs
			if err := validateUUID(threadID); err != nil {
				s.writeJSONError(w, http.StatusBadRequest, "Invalid thread ID format")
				return
			}
		}
	}

	// Check if it's a sub-resource or main thread operation
	var subPath string
	if threadID == "" {
		// Handle case where URL is /api/threads//messages (empty thread ID)
		subPath = strings.TrimPrefix(r.URL.Path, "/api/threads/")
	} else {
		subPath = strings.TrimPrefix(r.URL.Path, "/api/threads/"+threadID)
	}

	switch {
	case subPath == "/messages":
		switch r.Method {
		case http.MethodGet:
			if threadID == "" {
				s.writeJSONError(w, http.StatusBadRequest, "Thread ID required for GET messages")
				return
			}
			s.getThreadMessages(w, r, threadID)
		case http.MethodPost:
			// POST to /messages supports empty thread ID for draft threads
			s.chatInThread(w, r, threadID)
		default:
			s.writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
	// Remove polling endpoint as it's no longer needed
	case subPath == "/settings":
		if threadID == "" {
			s.writeJSONError(w, http.StatusBadRequest, "Thread ID required for settings")
			return
		}
		switch r.Method {
		case http.MethodPut:
			s.updateThreadSettings(w, r, threadID)
		default:
			s.writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
	case subPath == "/generate-title":
		if threadID == "" {
			s.writeJSONError(w, http.StatusBadRequest, "Thread ID required for title generation")
			return
		}
		switch r.Method {
		case http.MethodPost:
			s.generateAndUpdateThreadTitle(w, r, threadID)
		default:
			s.writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
	case subPath == "/title":
		if threadID == "" {
			s.writeJSONError(w, http.StatusBadRequest, "Thread ID required for title update")
			return
		}
		switch r.Method {
		case http.MethodPut:
			s.updateThreadTitle(w, r, threadID)
		default:
			s.writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
	case subPath == "/archive":
		if threadID == "" {
			s.writeJSONError(w, http.StatusBadRequest, "Thread ID required for archive")
			return
		}
		switch r.Method {
		case http.MethodPut:
			s.archiveThread(w, r, threadID)
		default:
			s.writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
	case subPath == "" || subPath == "/":
		if threadID == "" {
			s.writeJSONError(w, http.StatusBadRequest, "Thread ID required for thread operations")
			return
		}
		// Main thread operations
		switch r.Method {
		case http.MethodDelete:
			s.deleteThread(w, r, threadID)
		case http.MethodPatch:
			s.updateThread(w, r, threadID)
		default:
			s.writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
	default:
		s.writeJSONError(w, http.StatusNotFound, "Not found")
	}
}

func (s *Server) getThreadMessages(w http.ResponseWriter, r *http.Request, threadID string) {
	user := getUserFromContext(r)
	if user == nil {
		s.writeJSONError(w, http.StatusUnauthorized, "User not found")
		return
	}

	// Find chat by thread ID
	var chat Chat
	err := s.db.Where("user_id = ? AND thread_id = ?", user.ID, threadID).First(&chat).Error
	if err != nil {
		s.writeJSONError(w, http.StatusNotFound, "Thread not found")
		return
	}

	// Get messages
	var messages []ChatMessage
	query := s.db.Where("chat_id = ?", chat.ChatID)

	err = query.Order("created_at ASC").Find(&messages).Error
	if err != nil {
		s.writeJSONError(w, http.StatusInternalServerError, "Failed to fetch messages")
		return
	}

	response := make([]MessageResponse, len(messages))
	for i, msg := range messages {
		var imageData *string
		if msg.ImagePath != nil && *msg.ImagePath != "" {
			// Convert file path to URL path for frontend display
			imageURL := *msg.ImagePath
			// Ensure it starts with /uploads/ for proper serving
			if !strings.HasPrefix(imageURL, "/uploads/") && strings.HasPrefix(imageURL, "uploads/") {
				imageURL = "/" + imageURL
			}
			imageData = &imageURL
		}

		processedAnnotations := make(Annotations, 0, len(msg.Annotations))
		for _, ann := range msg.Annotations {
			processedAnn := ann
			if ann.LocalFilePath != nil && *ann.LocalFilePath != "" {
				url := s.filePathToURL(*ann.LocalFilePath)
				processedAnn.LocalFilePath = &url
			}
			processedAnnotations = append(processedAnnotations, processedAnn)
		}

		response[i] = MessageResponse{
			ID:          msg.ID,
			Role:        string(msg.Role),
			Content:     msg.Content,
			CreatedAt:   msg.CreatedAt,
			IsLive:      msg.IsLive,
			MessageType: msg.MessageType,
			ImageData:   imageData,
			ImageName:   msg.Filename,

			InputTokens:    msg.InputTokens,
			OutputTokens:   msg.OutputTokens,
			TotalTokens:    msg.TotalTokens,
			ModelUsed:      msg.ModelUsed,
			ResponseTimeMs: msg.ResponseTimeMs,
			FinishReason:   msg.FinishReason,

			Annotations: processedAnnotations,
		}
	}

	s.writeJSON(w, http.StatusOK, map[string][]MessageResponse{"messages": response})
}

func (s *Server) chatInThread(w http.ResponseWriter, r *http.Request, threadID string) {
	user := getUserFromContext(r)
	if user == nil {
		s.writeJSONError(w, http.StatusUnauthorized, "User not found")
		return
	}

	// Rate limiting check for chat messages
	if !s.rateLimiter.Allow(user.ID) {
		s.writeJSONError(w, http.StatusTooManyRequests, "Rate limit exceeded. Please wait before sending another message")
		return
	}

	// Enhanced request structure to support thread settings
	var req struct {
		ChatRequest
		Settings *ThreadSettings `json:"settings,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeJSONError(w, http.StatusBadRequest, "Invalid request format")
		return
	}

	// Validate and sanitize message content
	sanitizedMessage, err := validateChatMessage(req.Message)
	if err != nil {
		s.writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("Invalid message: %v", err))
		return
	}
	req.Message = sanitizedMessage

	var chat Chat
	var isNewThread bool

	if threadID == "" || threadID == "null" {
		// Create new thread for draft message
		isNewThread = true

		// Validate thread settings if provided
		if req.Settings != nil {
			if err := validateThreadSettings(req.Settings); err != nil {
				s.writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("Invalid settings: %v", err))
				return
			}
		}

		// Generate thread ID and title using first message
		newThreadID := uuid.New().String()
		title, err := s.generateThreadTitle(req.Message)
		if err != nil {
			title = "New Conversation" // Fallback
		}

		// Create new chat with thread
		chat = Chat{
			UserID:       user.ID,
			ChatID:       int64(user.ID)*1000 + time.Now().Unix()%1000, // Unique chat ID
			ThreadID:     &newThreadID,
			ThreadTitle:  &title,
			Temperature:  1.0, // Default values
			ModelName:    "gpt-4o",
			Stream:       true,
			ContextLimit: 4000,
		}

		// Apply custom settings if provided
		if req.Settings != nil {
			if req.Settings.ModelName != "" {
				chat.ModelName = req.Settings.ModelName
			}
			if req.Settings.Temperature >= 0 {
				chat.Temperature = req.Settings.Temperature
			}
			if req.Settings.RoleID != nil {
				chat.RoleID = req.Settings.RoleID
			}
			if req.Settings.Lang != "" {
				chat.Lang = req.Settings.Lang
			}
			if req.Settings.MasterPrompt != "" {
				chat.MasterPrompt = req.Settings.MasterPrompt
			}
			if req.Settings.ContextLimit > 0 {
				chat.ContextLimit = req.Settings.ContextLimit
			}
		}

		if err := s.db.Create(&chat).Error; err != nil {
			s.writeJSONError(w, http.StatusInternalServerError, "Failed to create thread")
			return
		}

		// Load the created chat with relationships
		s.db.Preload("Role").First(&chat, chat.ID)

	} else {
		// Find existing chat by thread ID
		err = s.db.Where("user_id = ? AND thread_id = ?", user.ID, threadID).
			Preload("Role").
			First(&chat).Error
		if err != nil {
			s.writeJSONError(w, http.StatusNotFound, "Thread not found")
			return
		}
	}

	// Process image if present
	var imageURL string
	if req.Image != nil && req.Image.Data != "" {
		// Save base64 image data to file
		url, err := s.saveBase64Image(req.Image.Data, req.Image.Filename, req.Image.MimeType)
		if err != nil {
			Log.WithError(err).Error("Failed to save image")
			s.writeJSONError(w, http.StatusInternalServerError, "Failed to save image")
			return
		}
		imageURL = url
	}

	messageType := "normal"
	messageContent := req.Message
	var imagePath, filename *string

	if imageURL != "" {
		messageType = "image"
		imagePath = &imageURL
		if req.Image != nil {
			filename = &req.Image.Filename
		}
	}

	// Create and save user message to database (like Telegram bot)
	userMessage := ChatMessage{
		ChatID:      chat.ChatID,
		Role:        "user",
		Content:     &messageContent,
		ImagePath:   imagePath,
		Filename:    filename,
		IsLive:      true,
		MessageType: messageType,
		CreatedAt:   time.Now(),
		ModelUsed:   &chat.ModelName,
	}

	// Estimate input tokens for user message (rough approximation)
	inputTokenEstimate := s.estimateTokenCount(messageContent)
	userMessage.InputTokens = &inputTokenEstimate

	// Save user message to database immediately
	if err := s.db.Create(&userMessage).Error; err != nil {
		Log.WithField("error", err).Error("Failed to save user message")
		s.writeJSONError(w, http.StatusInternalServerError, "Failed to save message")
		return
	}

	// Update thread token counts
	if inputTokenEstimate > 0 {
		if err := s.updateThreadTokens(chat.ChatID, inputTokenEstimate, 0); err != nil {
			Log.WithField("error", err).Warn("Failed to update thread tokens for user message")
		}
	}

	// Check if context limit is exceeded and summarize if needed
	if err := s.checkAndSummarizeContext(&chat); err != nil {
		Log.WithField("error", err).Warn("Failed to summarize context")
	}

	// Always use streaming response for OpenAI models in miniapp
	s.handleStreamingResponse(w, r, &chat, &userMessage, isNewThread)
}

func (s *Server) updateThreadSettings(w http.ResponseWriter, r *http.Request, threadID string) {
	user := getUserFromContext(r)
	if user == nil {
		s.writeJSONError(w, http.StatusUnauthorized, "User not found")
		return
	}

	var settings ThreadSettings
	if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
		s.writeJSONError(w, http.StatusBadRequest, "Invalid request format")
		return
	}

	// Validate thread settings
	if err := validateThreadSettings(&settings); err != nil {
		s.writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("Invalid settings: %v", err))
		return
	}

	// Find and update chat
	var chat Chat
	err := s.db.Where("user_id = ? AND thread_id = ?", user.ID, threadID).First(&chat).Error
	if err != nil {
		s.writeJSONError(w, http.StatusNotFound, "Thread not found")
		return
	}

	// Update settings (deprecated fields removed for security/simplification)
	updates := map[string]interface{}{
		"model_name":    settings.ModelName,
		"temperature":   settings.Temperature,
		"role_id":       settings.RoleID,
		"lang":          settings.Lang,
		"master_prompt": settings.MasterPrompt,
		"context_limit": settings.ContextLimit,
	}

	if err := s.db.Model(&chat).Updates(updates).Error; err != nil {
		s.writeJSONError(w, http.StatusInternalServerError, "Failed to update settings")
		return
	}

	s.writeJSONSuccess(w, "Settings updated successfully", nil)
}

func (s *Server) archiveThread(w http.ResponseWriter, r *http.Request, threadID string) {
	user := getUserFromContext(r)
	if user == nil {
		s.writeJSONError(w, http.StatusUnauthorized, "User not found")
		return
	}

	// Find chat and verify ownership
	var chat Chat
	err := s.db.Where("user_id = ? AND thread_id = ?", user.ID, threadID).First(&chat).Error
	if err != nil {
		s.writeJSONError(w, http.StatusNotFound, "Thread not found")
		return
	}

	// Check if thread is currently archived
	var updates map[string]interface{}
	if chat.ArchivedAt != nil {
		// Unarchive: set archived_at to NULL
		updates = map[string]interface{}{
			"archived_at": nil,
		}
	} else {
		// Archive: set archived_at to current time
		now := time.Now()
		updates = map[string]interface{}{
			"archived_at": &now,
		}
	}

	if err := s.db.Model(&chat).Updates(updates).Error; err != nil {
		s.writeJSONError(w, http.StatusInternalServerError, "Failed to update archive status")
		return
	}

	action := "archived"
	if chat.ArchivedAt != nil {
		action = "unarchived"
	}

	s.writeJSONSuccess(w, fmt.Sprintf("Thread %s successfully", action), nil)
}

func (s *Server) deleteThread(w http.ResponseWriter, r *http.Request, threadID string) {
	user := getUserFromContext(r)
	if user == nil {
		s.writeJSONError(w, http.StatusUnauthorized, "User not found")
		return
	}

	// Find chat
	var chat Chat
	err := s.db.Where("user_id = ? AND thread_id = ?", user.ID, threadID).First(&chat).Error
	if err != nil {
		s.writeJSONError(w, http.StatusNotFound, "Thread not found")
		return
	}

	// Delete messages first
	s.db.Where("chat_id = ?", chat.ChatID).Delete(&ChatMessage{})

	// Delete chat
	if err := s.db.Delete(&chat).Error; err != nil {
		s.writeJSONError(w, http.StatusInternalServerError, "Failed to delete thread")
		return
	}

	s.writeJSONSuccess(w, "Thread deleted successfully", nil)
}

func (s *Server) updateThread(w http.ResponseWriter, r *http.Request, threadID string) {
	user := getUserFromContext(r)
	if user == nil {
		s.writeJSONError(w, http.StatusUnauthorized, "User not found")
		return
	}

	// Find chat
	var chat Chat
	err := s.db.Where("user_id = ? AND thread_id = ?", user.ID, threadID).First(&chat).Error
	if err != nil {
		s.writeJSONError(w, http.StatusNotFound, "Thread not found")
		return
	}

	// Parse request body
	var updateReq struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&updateReq); err != nil {
		s.writeJSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Update thread title
	if updateReq.Title != "" {
		chat.ThreadTitle = &updateReq.Title
		if err := s.db.Save(&chat).Error; err != nil {
			s.writeJSONError(w, http.StatusInternalServerError, "Failed to update thread")
			return
		}
	}

	s.writeJSONSuccess(w, "Thread updated successfully", map[string]interface{}{
		"thread_id": threadID,
		"title":     updateReq.Title,
	})
}

func (s *Server) generateAndUpdateThreadTitle(w http.ResponseWriter, r *http.Request, threadID string) {
	user := getUserFromContext(r)
	if user == nil {
		s.writeJSONError(w, http.StatusUnauthorized, "User not found")
		return
	}

	// Find chat
	var chat Chat
	err := s.db.Where("user_id = ? AND thread_id = ?", user.ID, threadID).First(&chat).Error
	if err != nil {
		s.writeJSONError(w, http.StatusNotFound, "Thread not found")
		return
	}

	// Parse request body
	var titleReq struct {
		Question string `json:"question"`
		Response string `json:"response"`
	}
	if err := json.NewDecoder(r.Body).Decode(&titleReq); err != nil {
		s.writeJSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Generate title from conversation
	title, err := s.generateThreadTitleFromConversation(titleReq.Question, titleReq.Response)
	if err != nil {
		s.writeJSONError(w, http.StatusInternalServerError, "Failed to generate title")
		return
	}

	// Update thread title
	chat.ThreadTitle = &title
	if err := s.db.Save(&chat).Error; err != nil {
		s.writeJSONError(w, http.StatusInternalServerError, "Failed to update thread title")
		return
	}

	s.writeJSONSuccess(w, "Thread title updated successfully", map[string]interface{}{
		"thread_id": threadID,
		"title":     title,
	})
}

func (s *Server) updateThreadTitle(w http.ResponseWriter, r *http.Request, threadID string) {
	user := getUserFromContext(r)
	if user == nil {
		s.writeJSONError(w, http.StatusUnauthorized, "User not found")
		return
	}

	// Parse request body
	var titleReq struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&titleReq); err != nil {
		s.writeJSONError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Validate title
	title := strings.TrimSpace(titleReq.Title)
	if title == "" {
		s.writeJSONError(w, http.StatusBadRequest, "Title cannot be empty")
		return
	}
	if len(title) > 100 {
		s.writeJSONError(w, http.StatusBadRequest, "Title too long (max 100 characters)")
		return
	}

	// Find chat
	var chat Chat
	err := s.db.Where("user_id = ? AND thread_id = ?", user.ID, threadID).First(&chat).Error
	if err != nil {
		s.writeJSONError(w, http.StatusNotFound, "Thread not found")
		return
	}

	// Update thread title
	chat.ThreadTitle = &title
	if err := s.db.Save(&chat).Error; err != nil {
		s.writeJSONError(w, http.StatusInternalServerError, "Failed to update thread title")
		return
	}

	s.writeJSONSuccess(w, "Thread title updated successfully", map[string]interface{}{
		"thread_id": threadID,
		"title":     title,
	})
}

func (s *Server) getAvailableModels(w http.ResponseWriter, r *http.Request) {
	// Filter to only return OpenAI models
	var openaiModels []ModelResponse
	for _, model := range s.conf.Models {
		if model.Provider == "openai" {
			openaiModels = append(openaiModels, ModelResponse{
				ID:       model.ModelID,
				Name:     model.Name,
				Provider: model.Provider,
			})
		}
	}

	s.writeJSON(w, http.StatusOK, map[string][]ModelResponse{"models": openaiModels})
}

// Handle /api/roles (GET and POST)
func (s *Server) handleRoles(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.getUserRoles(w, r)
	case http.MethodPost:
		s.createRole(w, r)
	default:
		s.writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// Handle /api/roles/{id} (PUT and DELETE)
func (s *Server) handleRolesWithID(w http.ResponseWriter, r *http.Request) {
	roleIDStr := extractPathParam(r.URL.Path, "/api/roles")
	if roleIDStr == "" {
		s.writeJSONError(w, http.StatusBadRequest, "Role ID required")
		return
	}

	roleID, err := validateNumericID(roleIDStr, "role ID")
	if err != nil {
		s.writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	switch r.Method {
	case http.MethodPut:
		s.updateRole(w, r, roleID)
	case http.MethodDelete:
		s.deleteRole(w, r, roleID)
	default:
		s.writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (s *Server) getUserRoles(w http.ResponseWriter, r *http.Request) {
	user := getUserFromContext(r)
	if user == nil {
		s.writeJSONError(w, http.StatusUnauthorized, "User not found")
		return
	}
	roles := user.Roles
	response := make([]RoleResponse, len(roles))
	for i, role := range roles {
		response[i] = RoleResponse{
			ID:     role.ID,
			Name:   role.Name,
			Prompt: role.Prompt,
		}
	}

	s.writeJSON(w, http.StatusOK, map[string][]RoleResponse{"roles": response})
}

func (s *Server) createRole(w http.ResponseWriter, r *http.Request) {
	user := getUserFromContext(r)
	if user == nil {
		s.writeJSONError(w, http.StatusUnauthorized, "User not found")
		return
	}

	var req RoleResponse
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeJSONError(w, http.StatusBadRequest, "Invalid request format")
		return
	}

	// Validate and sanitize role data
	sanitizedName, sanitizedPrompt, err := validateRoleData(req.Name, req.Prompt)
	if err != nil {
		s.writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	role := Role{
		UserID: user.ID,
		Name:   sanitizedName,
		Prompt: sanitizedPrompt,
	}

	if err := s.db.Create(&role).Error; err != nil {
		s.writeJSONError(w, http.StatusInternalServerError, "Failed to create role")
		return
	}

	s.writeJSONSuccess(w, "Role created successfully", map[string]interface{}{"id": role.ID})
}

func (s *Server) updateRole(w http.ResponseWriter, r *http.Request, roleID uint) {
	user := getUserFromContext(r)
	if user == nil {
		s.writeJSONError(w, http.StatusUnauthorized, "User not found")
		return
	}

	var req RoleResponse
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeJSONError(w, http.StatusBadRequest, "Invalid request format")
		return
	}

	// Validate and sanitize role data
	sanitizedName, sanitizedPrompt, err := validateRoleData(req.Name, req.Prompt)
	if err != nil {
		s.writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	var role Role
	err = s.db.Where("id = ? AND user_id = ?", roleID, user.ID).First(&role).Error
	if err != nil {
		s.writeJSONError(w, http.StatusNotFound, "Role not found")
		return
	}

	role.Name = sanitizedName
	role.Prompt = sanitizedPrompt

	if err := s.db.Save(&role).Error; err != nil {
		s.writeJSONError(w, http.StatusInternalServerError, "Failed to update role")
		return
	}

	s.writeJSONSuccess(w, "Role updated successfully", nil)
}

func (s *Server) deleteRole(w http.ResponseWriter, r *http.Request, roleID uint) {
	user := getUserFromContext(r)
	if user == nil {
		s.writeJSONError(w, http.StatusUnauthorized, "User not found")
		return
	}

	var role Role
	err := s.db.Where("id = ? AND user_id = ?", roleID, user.ID).First(&role).Error
	if err != nil {
		s.writeJSONError(w, http.StatusNotFound, "Role not found")
		return
	}

	if err := s.db.Delete(&role).Error; err != nil {
		s.writeJSONError(w, http.StatusInternalServerError, "Failed to delete role")
		return
	}

	s.writeJSONSuccess(w, "Role deleted successfully", nil)
}

func (s *Server) getUserInfo(w http.ResponseWriter, r *http.Request) {
	user := getUserFromContext(r)
	if user == nil {
		s.writeJSONError(w, http.StatusUnauthorized, "User not found")
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":       user.ID,
		"username": user.Username,
	})
}

// Handle /api/messages (POST only) - for draft threads without thread ID
func (s *Server) handleDraftMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Call chatInThread with empty thread ID to trigger new thread creation
	s.chatInThread(w, r, "")
}

// Handle /api/messages/{id} (DELETE) - for message operations
func (s *Server) handleMessagesWithID(w http.ResponseWriter, r *http.Request) {
	messageIDStr := extractPathParam(r.URL.Path, "/api/messages")
	if messageIDStr == "" {
		s.writeJSONError(w, http.StatusBadRequest, "Message ID required")
		return
	}

	messageID, err := validateNumericID(messageIDStr, "message ID")
	if err != nil {
		s.writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	switch r.Method {
	case http.MethodDelete:
		s.deleteMessage(w, r, uint(messageID))
	default:
		s.writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// Permanently delete a message
func (s *Server) deleteMessage(w http.ResponseWriter, r *http.Request, messageID uint) {
	user := getUserFromContext(r)
	if user == nil {
		s.writeJSONError(w, http.StatusUnauthorized, "User not found")
		return
	}

	// Start transaction for consistent updates
	tx := s.db.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// Find the message and verify ownership through chat
	var message ChatMessage
	err := tx.Joins("JOIN chats ON chat_messages.chat_id = chats.chat_id").
		Where("chat_messages.id = ? AND chats.user_id = ?", messageID, user.ID).
		First(&message).Error
	if err != nil {
		tx.Rollback()
		if err == gorm.ErrRecordNotFound {
			s.writeJSONError(w, http.StatusNotFound, "Message not found")
		} else {
			s.writeJSONError(w, http.StatusInternalServerError, "Failed to find message")
		}
		return
	}

	// Get the chat to update token counts
	var chat Chat
	err = tx.Where("chat_id = ?", message.ChatID).First(&chat).Error
	if err != nil {
		tx.Rollback()
		s.writeJSONError(w, http.StatusInternalServerError, "Failed to find chat")
		return
	}

	// Update thread token counts by subtracting this message's tokens
	if message.InputTokens != nil {
		chat.TotalInputTokens = max(0, chat.TotalInputTokens-*message.InputTokens)
	}
	if message.OutputTokens != nil {
		chat.TotalOutputTokens = max(0, chat.TotalOutputTokens-*message.OutputTokens)
	}

	// Save updated chat
	if err := tx.Save(&chat).Error; err != nil {
		tx.Rollback()
		s.writeJSONError(w, http.StatusInternalServerError, "Failed to update thread tokens")
		return
	}

	// Permanently delete the message from database
	if err := tx.Delete(&message).Error; err != nil {
		tx.Rollback()
		s.writeJSONError(w, http.StatusInternalServerError, "Failed to delete message")
		return
	}

	// Commit transaction
	if err := tx.Commit().Error; err != nil {
		s.writeJSONError(w, http.StatusInternalServerError, "Failed to commit deletion")
		return
	}

	s.writeJSONSuccess(w, "Message deleted successfully", nil)
}

// Helper functions

// max returns the larger of two integers
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// updateThreadTokens updates the thread's total token count when a message is added
func (s *Server) updateThreadTokens(chatID int64, inputTokens, outputTokens int) error {
	if inputTokens == 0 && outputTokens == 0 {
		return nil
	}

	return s.db.Model(&Chat{}).
		Where("chat_id = ?", chatID).
		UpdateColumns(map[string]interface{}{
			"total_input_tokens":  gorm.Expr("total_input_tokens + ?", inputTokens),
			"total_output_tokens": gorm.Expr("total_output_tokens + ?", outputTokens),
		}).Error
}

func (s *Server) generateThreadTitle(initialMessage string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	messages := []openai.ChatMessage{
		{Role: "system", Content: "Generate a short, descriptive title (max 50 chars) for this conversation based on the user's first message. Reply with just the title, no quotes or extra text."},
		{Role: "user", Content: initialMessage},
	}

	response, err := s.openAI.CreateChatCompletionWithContext(ctx, miniModel, messages, openai.ChatCompletionOptions{}.SetTemperature(0.3))
	if err != nil {
		return "", err
	}

	if len(response.Choices) > 0 {
		if content, ok := response.Choices[0].Message.Content.(string); ok && content != "" {
			title := strings.TrimSpace(content)
			if len(title) > 50 {
				title = title[:47] + "..."
			}
			return title, nil
		}
	}

	return "", fmt.Errorf("no title generated")
}

func (s *Server) generateThreadTitleFromConversation(question, response string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	messages := []openai.ChatMessage{
		{Role: "system", Content: "Generate a short, descriptive title (max 50 chars) for this conversation based on the user's question and AI response. Reply with just the title, no quotes or extra text."},
		{Role: "user", Content: question},
		{Role: "assistant", Content: response},
		{Role: "user", Content: "Generate a title for this conversation"},
	}

	apiResponse, err := s.openAI.CreateChatCompletionWithContext(ctx, miniModel, messages, openai.ChatCompletionOptions{}.SetTemperature(0.3))
	if err != nil {
		return "", err
	}

	if len(apiResponse.Choices) > 0 {
		if content, ok := apiResponse.Choices[0].Message.Content.(string); ok && content != "" {
			title := strings.TrimSpace(content)
			if len(title) > 50 {
				title = title[:47] + "..."
			}
			return title, nil
		}
	}

	return "", fmt.Errorf("no title generated")
}

// TokenUsage holds token usage information
type TokenUsage struct {
	InputTokens  int
	OutputTokens int
	TotalTokens  int
	FinishReason string
}

// Generate response with streaming updates to SSE
func (s *Server) generateResponseWithStreamingUpdates(ctx context.Context, chat *Chat, history []openai.ChatMessage, assistantMsg *ChatMessage, w http.ResponseWriter, flusher http.Flusher) (string, *TokenUsage, error) {
	model := s.getModel(chat.ModelName)
	if model == nil {
		return "", nil, fmt.Errorf("model %s not found", chat.ModelName)
	}

	if model.Provider != "openai" {
		return "", nil, fmt.Errorf("only OpenAI models are supported in miniapp")
	}

	type completion struct {
		event openai.ResponseStreamEvent
		done  bool
		err   error
	}
	ch := make(chan completion, 1)

	var result strings.Builder
	var usage *TokenUsage
	var functionCalls []openai.ResponseOutput

	messages := s.convertDialogToResponseMessages(history)

	instructions := chat.MasterPrompt
	if chat.RoleID != nil {
		instructions = chat.Role.Prompt
	}
	instructions += fmt.Sprintf("\n\nCurrent date and time: %s", time.Now().Format(time.RFC3339))

	options := openai.ResponseOptions{}
	options.SetInstructions(instructions)
	options.SetMaxOutputTokens(10000)
	options.SetTemperature(chat.Temperature)
	options.SetUser(fmt.Sprintf("webapp_user_%d", chat.UserID))
	options.SetStore(false)

	tools := s.getResponseTools()

	if len(model.SearchTool) > 0 {
		tools = append(tools, openai.NewBuiltinTool(model.SearchTool))
	}
	if model.CodeInterpreter {
		tools = append(tools, openai.NewBuiltinToolWithContainer("code_interpreter", "auto"))
	}

	if len(tools) > 0 {
		options.SetTools(tools)
	}
	aiClient := s.openAI

	if err := aiClient.CreateResponseStreamWithContext(ctx, model.ModelID, messages, options,
		func(event openai.ResponseStreamEvent, d bool, e error) {
			ch <- completion{event: event, done: d, err: e}
			if d {
				close(ch)
			}
		}); err != nil {
		return "", nil, fmt.Errorf("failed to start OpenAI stream: %v", err)
	}

	for comp := range ch {
		if comp.err != nil {
			return result.String(), usage, comp.err
		}
		event := comp.event

		if comp.done {
			if len(functionCalls) > 0 {
				// First, add the assistant's message with tool calls to conversation history
				assistantMsgContent := result.String()
				var toolCalls ToolCalls
				for _, responseOutput := range functionCalls {
					if responseOutput.Type == "function_call" {
						toolCall := ToolCall{
							ID:   responseOutput.CallID,
							Type: "function",
							Function: openai.ToolCallFunction{
								Name:      responseOutput.Name,
								Arguments: responseOutput.Arguments,
							},
						}
						toolCalls = append(toolCalls, toolCall)
					}
				}

				if len(toolCalls) > 0 {
					assistantMsg.ToolCalls = toolCalls
					assistantMsg.Content = &assistantMsgContent
					s.db.Save(assistantMsg)
				}

				toolResults, err := s.handleResponseFunctionCallsForWebapp(chat, functionCalls)
				if err != nil {
					Log.WithField("error", err).Error("Failed to handle function calls")
					return result.String(), usage, err
				}

				if toolResults != "" {
					result.WriteString("\n\n")
					result.WriteString(toolResults)

					if w != nil && flusher != nil {
						finalContent := result.String()
						updatedResponse := MessageResponse{
							ID:          assistantMsg.ID,
							Role:        "assistant",
							Content:     &finalContent,
							CreatedAt:   assistantMsg.CreatedAt,
							IsLive:      false,
							MessageType: "normal",
						}

						jsonData, _ := json.Marshal(updatedResponse)
						fmt.Fprintf(w, "data: %s\n\n", jsonData)
						flusher.Flush()
					}
				}
			}

			if event.Response != nil && event.Response.Usage != nil {
				usage = &TokenUsage{
					InputTokens:  event.Response.Usage.InputTokens,
					OutputTokens: event.Response.Usage.OutputTokens,
					TotalTokens:  event.Response.Usage.TotalTokens,
				}
				Log.WithFields(map[string]interface{}{
					"input_tokens":  usage.InputTokens,
					"output_tokens": usage.OutputTokens,
					"total_tokens":  usage.TotalTokens,
				}).Debug("Done")
			} else {
				Log.Debug("No usage data found in response.done event")
			}

			return result.String(), usage, nil
		}

		switch event.Type {
		case "response.output_text.delta":
			if event.Delta != nil {
				result.WriteString(*event.Delta)

				// Send SSE update immediately for every token
				if w != nil && flusher != nil {
					currentContent := result.String()
					updatedResponse := MessageResponse{
						ID:          assistantMsg.ID,
						Role:        "assistant",
						Content:     &currentContent,
						CreatedAt:   assistantMsg.CreatedAt,
						IsLive:      true,
						MessageType: "normal",
					}

					jsonData, _ := json.Marshal(updatedResponse)
					fmt.Fprintf(w, "data: %s\n\n", jsonData)
					flusher.Flush()
				}
			}

		case "response.output_item.added":
			if event.Item != nil {
				switch event.Item.Type {
				case "function_call":
					Log.WithField("function", event.Item.Name).Info("Function call started")
				case "web_search_call":
					currentContent := chat.t("Web search started, please wait...")
					updatedResponse := MessageResponse{
						ID:          assistantMsg.ID,
						Role:        "assistant",
						Content:     &currentContent,
						CreatedAt:   assistantMsg.CreatedAt,
						IsLive:      true,
						MessageType: "normal",
					}

					jsonData, _ := json.Marshal(updatedResponse)
					fmt.Fprintf(w, "data: %s\n\n", jsonData)
					flusher.Flush()
				case "code_interpreter_call":
					currentContent := chat.t("Crafting and executing code, please wait...")
					updatedResponse := MessageResponse{
						ID:          assistantMsg.ID,
						Role:        "assistant",
						Content:     &currentContent,
						CreatedAt:   assistantMsg.CreatedAt,
						IsLive:      true,
						MessageType: "normal",
					}

					jsonData, _ := json.Marshal(updatedResponse)
					fmt.Fprintf(w, "data: %s\n\n", jsonData)
					flusher.Flush()
				}
			}

		case "response.output_item.done":
			if event.Item != nil {
				switch event.Item.Type {
				case "message":
					if event.Item.Status != "" {
						if usage == nil {
							usage = &TokenUsage{}
						}
						usage.FinishReason = event.Item.Status
					}
					if event.Item.Content != nil && len(event.Item.Content) > 0 {
						s.checkAnnotationsForWebapp(assistantMsg, event.Item.Content)
					}
				case "function_call":
					functionCalls = append(functionCalls, *event.Item)
					Log.WithField("function", event.Item.Name).Info("Function call completed")
				}
			}
		}
	}

	return result.String(), usage, nil
}

// handleResponseFunctionCallsForWebapp processes function calls for the web app
func (s *Server) handleResponseFunctionCallsForWebapp(chat *Chat, functions []openai.ResponseOutput) (string, error) {
	// Convert ResponseOutput function calls to ToolCall format
	var toolCalls []openai.ToolCall

	for _, responseOutput := range functions {
		// Only process function calls
		if responseOutput.Type != "function_call" {
			continue
		}

		toolCall := openai.ToolCall{
			ID:   responseOutput.CallID,
			Type: "function",
			Function: openai.ToolCallFunction{
				Name:      responseOutput.Name,
				Arguments: responseOutput.Arguments,
			},
		}
		toolCalls = append(toolCalls, toolCall)
	}

	// Create webapp notifier
	notifier := &WebappToolCallNotifier{}

	// Call the refactored core function
	result, toolMessages, err := s.handleToolCallsCore(chat, toolCalls, notifier)

	// Add tool messages to dialog
	for _, msg := range toolMessages {
		chat.addMessageToDialog(msg)
	}

	// Save history with tool results
	if len(result) > 0 {
		s.saveHistory(chat)
	}

	return result, err
}

// Handle image upload with enhanced security and validation
func (s *Server) handleImageUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	user := getUserFromContext(r)
	if user == nil {
		s.writeJSONError(w, http.StatusUnauthorized, "User not found")
		return
	}

	if !s.rateLimiter.Allow(user.ID) {
		s.writeJSONError(w, http.StatusTooManyRequests, "Rate limit exceeded")
		return
	}

	const maxUploadSize = 10 << 20 // 10MB
	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		s.writeJSONError(w, http.StatusBadRequest, "Failed to parse form data or file too large")
		return
	}

	file, header, err := r.FormFile("image")
	if err != nil {
		s.writeJSONError(w, http.StatusBadRequest, "No image file found in request")
		return
	}
	defer file.Close()

	// Enhanced file validation
	if header.Filename == "" {
		s.writeJSONError(w, http.StatusBadRequest, "Invalid filename")
		return
	}

	// Validate file size (double-check after form parsing)
	if header.Size > maxUploadSize {
		s.writeJSONError(w, http.StatusBadRequest, "File size too large. Maximum 10MB allowed")
		return
	}

	if header.Size == 0 {
		s.writeJSONError(w, http.StatusBadRequest, "Empty file not allowed")
		return
	}

	// Basic file type validation
	contentType := header.Header.Get("Content-Type")
	if contentType == "" {
		s.writeJSONError(w, http.StatusBadRequest, "Missing content type")
		return
	}

	// Validate content type
	validTypes := map[string]bool{
		"image/jpeg": true,
		"image/png":  true,
		"image/gif":  true,
		"image/webp": true,
	}

	if !validTypes[contentType] {
		s.writeJSONError(w, http.StatusBadRequest, "Invalid file type. Only JPEG, PNG, GIF, and WebP images are allowed")
		return
	}

	// Generate unique filename
	uniqueID := uuid.New().String()
	timestamp := time.Now().Unix()

	// Determine file extension based on content type
	var fileExt string
	switch contentType {
	case "image/jpeg":
		fileExt = ".jpg"
	case "image/png":
		fileExt = ".png"
	case "image/gif":
		fileExt = ".gif"
	case "image/webp":
		fileExt = ".webp"
	}

	filename := fmt.Sprintf("%d_%s%s", timestamp, uniqueID, fileExt)
	filePath := fmt.Sprintf("uploads/%s", filename)

	// Create uploads directory with proper permissions
	if err := os.MkdirAll("uploads", 0755); err != nil {
		Log.WithField("error", err).Error("Failed to create uploads directory")
		s.writeJSONError(w, http.StatusInternalServerError, "Failed to create upload directory")
		return
	}

	// Create file with secure permissions
	outFile, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		s.writeJSONError(w, http.StatusInternalServerError, "Failed to create file")
		return
	}
	defer outFile.Close()

	// Copy file content
	written, err := io.Copy(outFile, io.LimitReader(file, maxUploadSize))
	if err != nil {
		os.Remove(filePath)
		s.writeJSONError(w, http.StatusInternalServerError, "Failed to save file")
		return
	}

	// Verify written size matches expected size
	if written != header.Size {
		os.Remove(filePath)
		s.writeJSONError(w, http.StatusBadRequest, "File size mismatch")
		return
	}

	// Create successful response
	response := ImageUploadResponse{
		ID:       uniqueID,
		URL:      "/uploads/" + filename,
		Filename: header.Filename,
		Size:     written,
		MimeType: contentType,
	}

	s.writeJSON(w, http.StatusOK, response)
}

func (s *Server) checkAndSummarizeContext(chat *Chat) error {
	// Count live messages
	var liveCount int64
	s.db.Model(&ChatMessage{}).Where("chat_id = ? AND is_live = ?", chat.ChatID, true).Count(&liveCount)

	// If we're approaching the context limit, summarize old messages
	if liveCount > int64(float64(chat.ContextLimit)*0.8) { // 80% of limit
		return s.summarizeOldMessages(chat)
	}

	return nil
}

func (s *Server) summarizeOldMessages(chat *Chat) error {
	// Get oldest live messages (first half)
	var messages []ChatMessage
	err := s.db.Where("chat_id = ? AND is_live = ? AND message_type = ?",
		chat.ChatID, true, "normal").
		Order("created_at ASC").
		Limit(chat.ContextLimit / 2).
		Find(&messages).Error

	if err != nil || len(messages) == 0 {
		return err
	}

	// Create summary using miniModel
	summary, err := s.createMessagesSummary(messages)
	if err != nil {
		return err
	}

	// Mark old messages as not live
	messageIDs := make([]uint, len(messages))
	for i, msg := range messages {
		messageIDs[i] = msg.ID
	}

	s.db.Model(&ChatMessage{}).
		Where("id IN ?", messageIDs).
		Update("is_live", false)

	// Create summary message
	summaryMessage := ChatMessage{
		ChatID:      chat.ChatID,
		Role:        "system",
		Content:     &summary,
		IsLive:      true,
		MessageType: "summary",
	}

	return s.db.Create(&summaryMessage).Error
}

func (s *Server) createMessagesSummary(messages []ChatMessage) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Format messages for summarization
	var conversation strings.Builder
	for _, msg := range messages {
		if msg.Content != nil {
			conversation.WriteString(fmt.Sprintf("%s: %s\n", string(msg.Role), *msg.Content))
		}
	}

	prompt := []openai.ChatMessage{
		{Role: "system", Content: "Summarize this conversation concisely, preserving key information and context. Focus on important decisions, facts, and ongoing topics."},
		{Role: "user", Content: conversation.String()},
	}

	response, err := s.openAI.CreateChatCompletionWithContext(ctx, miniModel, prompt,
		openai.ChatCompletionOptions{}.SetTemperature(0.2))
	if err != nil {
		return "", err
	}

	if len(response.Choices) > 0 {
		if content, ok := response.Choices[0].Message.Content.(string); ok {
			return content, nil
		}
	}

	return "", fmt.Errorf("no summary generated")
}

// Helper functions for thread message processing

func (s *Server) convertMessagesToOpenAI(messages []ChatMessage, chat *Chat) []openai.ChatMessage {
	var history []openai.ChatMessage

	// Add system prompt/role if exists
	if chat.RoleID != nil && chat.Role.Prompt != "" {
		history = append(history, openai.ChatMessage{
			Role:    "system",
			Content: chat.Role.Prompt,
		})
	} else if chat.MasterPrompt != "" {
		history = append(history, openai.ChatMessage{
			Role:    "system",
			Content: chat.MasterPrompt,
		})
	}

	// Convert chat messages
	for _, msg := range messages {
		if msg.Content == nil {
			continue
		}

		var message openai.ChatMessage

		// Handle image attachments like Telegram bot
		if msg.ImagePath != nil {
			reader, err := os.Open(*msg.ImagePath)
			if err != nil {
				Log.Warn("Error opening image file", "error=", err)
				// Still add text content if available
				if *msg.Content != "" {
					history = append(history, openai.ChatMessage{
						Role:    openai.ChatMessageRole(msg.Role),
						Content: *msg.Content,
					})
				}
				continue
			}
			defer reader.Close()

			bytes, err := io.ReadAll(reader)
			if err != nil {
				Log.Warn("Error reading image file", "error=", err)
				// Still add text content if available
				if *msg.Content != "" {
					history = append(history, openai.ChatMessage{
						Role:    openai.ChatMessageRole(msg.Role),
						Content: *msg.Content,
					})
				}
				continue
			}

			// Add text content first (without the [IMAGE: ...] reference)
			textContent := strings.TrimSpace(*msg.Content)
			// Remove any [IMAGE: ...] references from the text content
			textContent = regexp.MustCompile(`\n*\[IMAGE: [^\]]+\]\s*`).ReplaceAllString(textContent, "")
			textContent = strings.TrimSpace(textContent)

			// Create content array starting with text
			content := []openai.ChatMessageContent{{Type: "text", Text: &textContent}}

			// Add image content
			filename := "image.jpg"
			if msg.Filename != nil {
				filename = *msg.Filename
			}
			content = append(content, openai.NewChatMessageContentWithBytes(bytes))

			Log.Info("Adding image message to history", "filename=", filename)
			message = openai.ChatMessage{Role: openai.ChatMessageRole(msg.Role), Content: content}
		} else {
			// Regular text message
			message = openai.ChatMessage{
				Role:    openai.ChatMessageRole(msg.Role),
				Content: *msg.Content,
			}
		}

		history = append(history, message)
	}

	return history
}

// Handle streaming response with Server-Sent Events
func (s *Server) handleStreamingResponse(w http.ResponseWriter, r *http.Request, chat *Chat, userMessage *ChatMessage, isNewThread bool) {
	// Set up Server-Sent Events headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, ok := w.(http.Flusher)
	if !ok {
		s.writeJSONError(w, http.StatusInternalServerError, "Streaming unsupported")
		return
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()

	// Load chat with full relationships
	s.db.Preload("User").Preload("Role").First(chat, chat.ID)

	// Get live messages for context (now includes the saved user message)
	var messages []ChatMessage
	s.db.Where("chat_id = ? AND is_live = ?", chat.ChatID, true).
		Order("created_at ASC").
		Find(&messages)

	// Convert to OpenAI format (user message is now in DB, no need to append)
	history := s.convertMessagesToOpenAI(messages, chat)

	// Create assistant message in database
	assistantMsg := ChatMessage{
		ChatID:      chat.ChatID,
		Role:        "assistant",
		Content:     new(string), // Start with empty content
		IsLive:      true,
		MessageType: "normal",
	}

	if err := s.db.Create(&assistantMsg).Error; err != nil {
		Log.WithField("error", err).Error("Failed to create assistant message")
		return
	}

	// Send initial empty assistant message to frontend
	initialResponse := MessageResponse{
		ID:          assistantMsg.ID,
		Role:        "assistant",
		Content:     assistantMsg.Content, // Empty string initially
		CreatedAt:   assistantMsg.CreatedAt,
		IsLive:      true,
		MessageType: "normal",
	}
	jsonData, _ := json.Marshal(initialResponse)
	fmt.Fprintf(w, "data: %s\n\n", jsonData)
	flusher.Flush()

	startTime := time.Now()
	response, usage, err := s.generateResponseWithStreamingUpdates(ctx, chat, history, &assistantMsg, w, flusher)
	responseTime := time.Since(startTime).Milliseconds()

	if err != nil {
		Log.WithField("error", err).Error("Failed to generate response")
		errorContent := fmt.Sprintf("Sorry, I encountered an error: %s", err.Error())
		assistantMsg.Content = &errorContent
		s.db.Save(&assistantMsg)

		// Send error response
		errorResponse := MessageResponse{
			ID:          assistantMsg.ID,
			Role:        "assistant",
			Content:     &errorContent,
			CreatedAt:   assistantMsg.CreatedAt,
			IsLive:      true,
			MessageType: "normal",
		}
		jsonData, _ := json.Marshal(errorResponse)
		fmt.Fprintf(w, "data: %s\n\n", jsonData)

		flusher.Flush()
		return
	}

	// Final update with complete response and meta information
	assistantMsg.Content = &response
	assistantMsg.ResponseTimeMs = &responseTime
	assistantMsg.ModelUsed = &chat.ModelName

	// Add token usage information if available
	if usage != nil {
		assistantMsg.InputTokens = &usage.InputTokens
		assistantMsg.OutputTokens = &usage.OutputTokens
		assistantMsg.TotalTokens = &usage.TotalTokens
		if usage.FinishReason != "" {
			assistantMsg.FinishReason = &usage.FinishReason
		}
	}

	if err := s.db.Save(&assistantMsg).Error; err != nil {
		Log.WithField("error", err).Error("Failed to save final response to database")
	}

	// Update thread token counts for assistant message
	if usage != nil {
		if err := s.updateThreadTokens(chat.ChatID, usage.InputTokens, usage.OutputTokens); err != nil {
			Log.WithField("error", err).Warn("Failed to update thread tokens for assistant message")
		}
	}

	// Send thread data if this was a new thread
	if isNewThread {
		settings := ThreadSettings{
			ModelName:    chat.ModelName,
			Temperature:  chat.Temperature,
			RoleID:       chat.RoleID,
			Lang:         chat.Lang,
			MasterPrompt: chat.MasterPrompt,
			ContextLimit: chat.ContextLimit,
		}

		threadResponse := ThreadResponse{
			ID:                *chat.ThreadID,
			Title:             *chat.ThreadTitle,
			CreatedAt:         chat.CreatedAt,
			UpdatedAt:         chat.UpdatedAt,
			ArchivedAt:        chat.ArchivedAt,
			Settings:          settings,
			MessageCount:      1, // Will have one user message + one assistant message
			TotalInputTokens:  chat.TotalInputTokens,
			TotalOutputTokens: chat.TotalOutputTokens,
		}

		threadData, _ := json.Marshal(threadResponse)
		fmt.Fprintf(w, "data: {\"type\": \"thread\", \"thread\": %s}\n\n", threadData)
		flusher.Flush()
	}

	// IMPORTANT: Reload the message from database to ensure we have all annotations
	// that were processed during the streaming response
	if err := s.db.First(&assistantMsg, assistantMsg.ID).Error; err != nil {
		Log.WithField("error", err).Error("Failed to reload message with annotations")
	}

	processedAnnotations := make(Annotations, 0, len(assistantMsg.Annotations))
	for _, ann := range assistantMsg.Annotations {
		processedAnn := ann
		if ann.LocalFilePath != nil && *ann.LocalFilePath != "" {
			url := s.filePathToURL(*ann.LocalFilePath)
			processedAnn.LocalFilePath = &url
		}
		processedAnnotations = append(processedAnnotations, processedAnn)
	}

	// Send final message with complete metadata
	finalResponse := MessageResponse{
		ID:             assistantMsg.ID,
		Role:           "assistant",
		Content:        assistantMsg.Content,
		CreatedAt:      assistantMsg.CreatedAt,
		IsLive:         true,
		MessageType:    "normal",
		InputTokens:    assistantMsg.InputTokens,
		OutputTokens:   assistantMsg.OutputTokens,
		TotalTokens:    assistantMsg.TotalTokens,
		ModelUsed:      assistantMsg.ModelUsed,
		ResponseTimeMs: assistantMsg.ResponseTimeMs,
		FinishReason:   assistantMsg.FinishReason,
		Annotations:    processedAnnotations,
	}
	jsonData, _ = json.Marshal(finalResponse)
	fmt.Fprintf(w, "data: %s\n\n", jsonData)
	flusher.Flush()

	// Send completion signal
	fmt.Fprintf(w, "data: {\"type\": \"complete\"}\n\n")
	flusher.Flush()
}

// Annotation processing for webapp

// WebappAnnotationProcessor implements AnnotationProcessor for webapp
type WebappAnnotationProcessor struct {
	server *Server
}

// ProcessFile saves a file locally and returns the path for webapp
func (p *WebappAnnotationProcessor) ProcessFile(filename string, data []byte, annotation openai.Annotation, text string) (string, error) {
	// Log the file processing for webapp
	Log.WithField("filename", filename).WithField("size", len(data)).Info("Annotation file processed for webapp")

	// Create annotations directory if it doesn't exist
	annotationsDir := "uploads/annotations"
	if err := os.MkdirAll(annotationsDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create annotations directory: %w", err)
	}

	// Generate unique filename to avoid conflicts
	timestamp := time.Now().Unix()
	localFilename := fmt.Sprintf("%d_%s", timestamp, filename)
	localPath := filepath.Join(annotationsDir, localFilename)

	// Write file to disk
	if err := os.WriteFile(localPath, data, 0644); err != nil {
		return "", fmt.Errorf("failed to write annotation file: %w", err)
	}

	return localPath, nil
}

// checkAnnotationsForWebapp processes annotations for webapp streaming responses
func (s *Server) checkAnnotationsForWebapp(assistantMsg *ChatMessage, content []openai.OutputContent) {
	// Create webapp-specific annotation processor
	processor := &WebappAnnotationProcessor{
		server: s,
	}

	// Process annotations using the unified function
	annotations, err := ProcessAnnotations(s.openAI, content, processor)
	if err != nil {
		Log.WithField("error", err).Error("Failed to process annotations")
		return
	}

	// Store annotations in the message
	if len(annotations) > 0 {
		if err := StoreAnnotationsInMessage(s.db, assistantMsg, annotations); err != nil {
			Log.WithField("error", err).Error("Failed to store annotations")
		}
	}
}

func validateString(input string, minLen, maxLen int) error {
	if len(input) < minLen {
		return fmt.Errorf("input too short (minimum %d characters)", minLen)
	}
	if len(input) > maxLen {
		return fmt.Errorf("input too long (maximum %d characters)", maxLen)
	}
	return nil
}

// validateThreadSettings validates thread settings
func validateThreadSettings(settings *ThreadSettings) error {
	if settings == nil {
		return fmt.Errorf("settings cannot be nil")
	}

	// Validate model name
	if settings.ModelName != "" {
		if err := validateString(settings.ModelName, 1, 100); err != nil {
			return fmt.Errorf("invalid model name: %v", err)
		}
	}

	// Validate temperature
	if settings.Temperature < 0 || settings.Temperature > 1 {
		return fmt.Errorf("temperature must be between 0 and 1")
	}

	// Validate master prompt
	if settings.MasterPrompt != "" {
		if err := validateString(settings.MasterPrompt, 0, 4000); err != nil {
			return fmt.Errorf("invalid master prompt: %v", err)
		}
	}

	// Validate context limit
	if settings.ContextLimit < 100 || settings.ContextLimit > 100000 {
		return fmt.Errorf("context limit must be between 100 and 100000")
	}

	return nil
}

// validateChatMessage validates chat message input
func validateChatMessage(message string) (string, error) {
	if message == "" {
		return "", fmt.Errorf("message cannot be empty")
	}

	if err := validateString(message, 1, 4000); err != nil {
		return "", fmt.Errorf("invalid message: %v", err)
	}

	return strings.TrimSpace(message), nil
}

// validateRoleData validates role creation/update data
func validateRoleData(name, prompt string) (string, string, error) {
	if err := validateString(name, 1, 100); err != nil {
		return "", "", fmt.Errorf("invalid role name: %v", err)
	}

	if err := validateString(prompt, 1, 2000); err != nil {
		return "", "", fmt.Errorf("invalid role prompt: %v", err)
	}

	return strings.TrimSpace(name), strings.TrimSpace(prompt), nil
}

// validateNumericID validates numeric IDs from URL parameters
func validateNumericID(idStr string, fieldName string) (uint, error) {
	if idStr == "" {
		return 0, fmt.Errorf("%s cannot be empty", fieldName)
	}

	// Check for valid numeric characters only
	numericRegex := regexp.MustCompile(`^\d+$`)
	if !numericRegex.MatchString(idStr) {
		return 0, fmt.Errorf("invalid %s format", fieldName)
	}

	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %v", fieldName, err)
	}

	if id == 0 {
		return 0, fmt.Errorf("%s must be greater than 0", fieldName)
	}

	return uint(id), nil
}

// validateUUID validates UUID format for thread IDs
func validateUUID(uuidStr string) error {
	if uuidStr == "" {
		return fmt.Errorf("UUID cannot be empty")
	}

	_, err := uuid.Parse(uuidStr)
	if err != nil {
		return fmt.Errorf("invalid UUID format")
	}

	return nil
}

// filePathToURL converts a file path to a URL path for serving
func (s *Server) filePathToURL(filePath string) string {
	// Extract just the filename from the full path
	parts := strings.Split(filePath, "/")
	if len(parts) > 0 {
		filename := parts[len(parts)-1]
		return "/uploads/annotations/" + filename
	}
	return filePath
}

// estimateTokenCount provides a rough estimate of token count for text
// This is an approximation - actual token count can vary significantly
func (s *Server) estimateTokenCount(text string) int {
	// Rough estimation: 1 token ≈ 4 characters or 0.75 words
	// This is a simplified estimate - real tokenization is more complex
	words := len(strings.Fields(text))
	chars := len(text)

	// Use the higher estimate between word-based and character-based
	wordEstimate := int(float64(words) / 0.75)
	charEstimate := chars / 4

	if wordEstimate > charEstimate {
		return wordEstimate
	}
	return charEstimate
}

// saveBase64Image saves base64 image data to a file and returns the URL
func (s *Server) saveBase64Image(base64Data, originalFilename, mimeType string) (string, error) {
	// Decode base64 data
	data, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		return "", fmt.Errorf("failed to decode base64 data: %w", err)
	}

	// Generate unique filename
	timestamp := time.Now().Format("20060102-150405")
	randomID := uuid.New().String()[:8]
	ext := ".jpg"

	switch mimeType {
	case "image/png":
		ext = ".png"
	case "image/gif":
		ext = ".gif"
	case "image/webp":
		ext = ".webp"
	}

	filename := fmt.Sprintf("image_%s_%s%s", timestamp, randomID, ext)

	// Ensure uploads directory exists
	uploadsDir := "uploads"
	if err := os.MkdirAll(uploadsDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create uploads directory: %w", err)
	}

	// Write file
	filePath := filepath.Join(uploadsDir, filename)
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return "", fmt.Errorf("failed to write image file: %w", err)
	}

	// Return file system path for database storage
	// The frontend will get the URL path from the API response
	return filePath, nil
}
