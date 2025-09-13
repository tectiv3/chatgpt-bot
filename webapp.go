package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
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
	"sync"
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
	ID           string         `json:"id"`
	Title        string         `json:"title"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	ArchivedAt   *time.Time     `json:"archived_at"`
	Settings     ThreadSettings `json:"settings"`
	MessageCount int            `json:"message_count"`
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

// CSRF token store for session-based CSRF protection
type CSRFTokenStore struct {
	tokens map[uint]string // userId -> token
	mutex  sync.RWMutex
}

var csrfStore = &CSRFTokenStore{
	tokens: make(map[uint]string),
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

	mux.HandleFunc("/api/csrf-token", s.apiMiddleware(s.getCSRFToken, false))
	mux.HandleFunc("/api/threads", s.apiMiddleware(s.handleThreads, false))
	mux.HandleFunc("/api/threads/archived", s.apiMiddleware(s.getArchivedThreads, false))
	mux.HandleFunc("/api/messages", s.apiMiddleware(s.handleDraftMessages, false)) // Direct messages endpoint for draft threads
	mux.HandleFunc("/api/threads/", s.apiMiddleware(s.handleThreadsWithID, false))
	mux.HandleFunc("/api/models", s.apiMiddleware(s.getAvailableModels, false))
	mux.HandleFunc("/api/roles", s.apiMiddleware(s.handleRoles, false))
	mux.HandleFunc("/api/roles/", s.apiMiddleware(s.handleRolesWithID, false))
	mux.HandleFunc("/api/user", s.apiMiddleware(s.getUserInfo, false))
	mux.HandleFunc("/api/upload-image", s.apiMiddleware(s.handleImageUpload, false))

	return mux
}

// corsMiddleware for non-API routes
func (s *Server) corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Telegram-Init-Data, X-CSRF-Token")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next(w, r)
	}
}

// apiMiddleware consolidates CORS, auth, and optional CSRF protection
func (s *Server) apiMiddleware(next http.HandlerFunc, requireCSRF bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// CORS headers
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Telegram-Init-Data, X-CSRF-Token")

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

		// CSRF protection for write operations
		if requireCSRF && r.Method != http.MethodGet {
			providedToken := r.Header.Get("X-CSRF-Token")
			if providedToken == "" {
				s.writeJSONError(w, http.StatusForbidden, "CSRF token required")
				return
			}

			if !csrfStore.validateCSRFToken(user.ID, providedToken) {
				s.writeJSONError(w, http.StatusForbidden, "Invalid CSRF token")
				return
			}
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

// Get client IP with proxy headers support
func (s *Server) getClientIP(r *http.Request) string {
	// Check X-Forwarded-For header (for proxies)
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Take the first IP in the chain
		ips := strings.Split(xff, ",")
		if len(ips) > 0 {
			return strings.TrimSpace(ips[0])
		}
	}

	// Check X-Real-IP header
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}

	// Fallback to remote address
	ip := r.RemoteAddr
	// Remove port if present
	if colon := strings.LastIndex(ip, ":"); colon != -1 {
		ip = ip[:colon]
	}
	return ip
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
			ID:           *chatCount.Chat.ThreadID,
			Title:        *chatCount.Chat.ThreadTitle,
			CreatedAt:    chatCount.Chat.CreatedAt,
			UpdatedAt:    chatCount.Chat.UpdatedAt,
			ArchivedAt:   chatCount.Chat.ArchivedAt,
			Settings:     settings,
			MessageCount: int(chatCount.MessageCount),
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
			ID:           *chatCount.Chat.ThreadID,
			Title:        *chatCount.Chat.ThreadTitle,
			CreatedAt:    chatCount.Chat.CreatedAt,
			UpdatedAt:    chatCount.Chat.UpdatedAt,
			ArchivedAt:   chatCount.Chat.ArchivedAt,
			Settings:     settings,
			MessageCount: int(chatCount.MessageCount),
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
	err = s.db.Where("chat_id = ?", chat.ChatID).
		Order("created_at ASC").
		Find(&messages).Error
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

		response[i] = MessageResponse{
			ID:          msg.ID,
			Role:        string(msg.Role),
			Content:     msg.Content,
			CreatedAt:   msg.CreatedAt,
			IsLive:      msg.IsLive,
			MessageType: msg.MessageType,
			ImageData:   imageData,
			ImageName:   msg.Filename,
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

	// Create temporary user message for AI context (not saved to DB - frontend shows it)
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
	}

	// Save user message to database immediately
	if err := s.db.Create(&userMessage).Error; err != nil {
		Log.WithField("error", err).Error("Failed to save user message")
		s.writeJSONError(w, http.StatusInternalServerError, "Failed to save message")
		return
	}

	// Check if context limit is exceeded and summarize if needed
	if err := s.checkAndSummarizeContext(&chat); err != nil {
		Log.WithField("error", err).Warn("Failed to summarize context")
	}

	// Process message synchronously with streaming response

	if chat.Stream {
		// Use Server-Sent Events for streaming response
		s.handleStreamingResponse(w, r, &chat, &userMessage, isNewThread)
	} else {
		// Process synchronously and return complete response
		s.handleSynchronousResponse(w, &chat, &userMessage, isNewThread)
	}
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
	models := make([]ModelResponse, len(s.conf.Models))
	for i, model := range s.conf.Models {
		models[i] = ModelResponse{
			ID:       model.ModelID,
			Name:     model.Name,
			Provider: model.Provider,
		}
	}

	s.writeJSON(w, http.StatusOK, map[string][]ModelResponse{"models": models})
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

func (s *Server) getCSRFToken(w http.ResponseWriter, r *http.Request) {
	user := getUserFromContext(r)
	if user == nil {
		s.writeJSONError(w, http.StatusUnauthorized, "User not found")
		return
	}

	token, err := csrfStore.getOrCreateCSRFToken(user.ID)
	if err != nil {
		Log.WithField("error", err).Error("Failed to generate CSRF token")
		s.writeJSONError(w, http.StatusInternalServerError, "Failed to generate CSRF token")
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]string{
		"csrf_token": token,
	})
}

// Helper functions

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

// Generate response with streaming updates to SSE
// Generate response with streaming updates to SSE (following Telegram bot pattern)
func (s *Server) generateResponseWithStreamingUpdates(ctx context.Context, chat *Chat, history []openai.ChatMessage, assistantMsg *ChatMessage, w http.ResponseWriter, flusher http.Flusher) (string, error) {
	model := s.getModel(chat.ModelName)
	if model == nil {
		return "", fmt.Errorf("model %s not found", chat.ModelName)
	}

	switch model.Provider {
	case "openai":
		// Use the exact same pattern as getStreamAnswer in llm.go
		type completion struct {
			response openai.ChatCompletion
			done     bool
			err      error
		}
		ch := make(chan completion, 1)

		var result strings.Builder
		tokenCount := 0

		options := openai.ChatCompletionOptions{}.
			SetTemperature(chat.Temperature).
			SetStream(func(r openai.ChatCompletion, done bool, err error) {
				ch <- completion{response: r, done: done, err: err}
				if done {
					close(ch)
				}
			})

		// Start the streaming request
		if _, err := s.openAI.CreateChatCompletionWithContext(ctx, model.ModelID, history, options); err != nil {
			return "", fmt.Errorf("failed to start OpenAI stream: %v", err)
		}

		// Process streaming responses (exact pattern from Telegram bot)
		for comp := range ch {
			if comp.err != nil {
				return result.String(), nil
			}

			if !comp.done {
				// Extract token from delta (same as Telegram bot)
				if len(comp.response.Choices) > 0 && comp.response.Choices[0].Delta.Content != nil {
					if deltaStr, err := comp.response.Choices[0].Delta.ContentString(); err == nil && deltaStr != "" {
						result.WriteString(deltaStr)
						tokenCount++

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
				}
			}
		}

		return result.String(), nil

	case "anthropic":
		// For Anthropic, fall back to non-streaming for now
		response, err := s.generateResponseForThread(chat, history)
		if err != nil {
			return "", err
		}

		// Send complete response via SSE
		if w != nil && flusher != nil {
			updatedResponse := MessageResponse{
				ID:          assistantMsg.ID,
				Role:        "assistant",
				Content:     &response,
				CreatedAt:   assistantMsg.CreatedAt,
				IsLive:      true,
				MessageType: "normal",
			}
			jsonData, _ := json.Marshal(updatedResponse)
			fmt.Fprintf(w, "data: %s\n\n", jsonData)
			flusher.Flush()
		}

		return response, nil

	case "gemini":
		// For Gemini, fall back to non-streaming for now
		response, err := s.generateResponseForThread(chat, history)
		if err != nil {
			return "", err
		}

		// Send complete response via SSE
		if w != nil && flusher != nil {
			updatedResponse := MessageResponse{
				ID:          assistantMsg.ID,
				Role:        "assistant",
				Content:     &response,
				CreatedAt:   assistantMsg.CreatedAt,
				IsLive:      true,
				MessageType: "normal",
			}
			jsonData, _ := json.Marshal(updatedResponse)
			fmt.Fprintf(w, "data: %s\n\n", jsonData)
			flusher.Flush()
		}

		return response, nil

	default:
		return "", fmt.Errorf("unsupported provider: %s", model.Provider)
	}
}

// Handle image upload with enhanced security and validation
func (s *Server) handleImageUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// Get user from context (authentication already validated by middleware)
	user := getUserFromContext(r)
	if user == nil {
		s.writeJSONError(w, http.StatusUnauthorized, "User not found")
		return
	}

	// Rate limiting check
	if !s.rateLimiter.Allow(user.ID) {
		s.writeJSONError(w, http.StatusTooManyRequests, "Rate limit exceeded")
		return
	}

	// Parse multipart form data with size limit (10MB per image)
	const maxUploadSize = 10 << 20 // 10MB
	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		s.writeJSONError(w, http.StatusBadRequest, "Failed to parse form data or file too large")
		return
	}

	// Get the uploaded file
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

	response, err := s.generateResponseWithStreamingUpdates(ctx, chat, history, &assistantMsg, w, flusher)
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

	// Final update with complete response - ONLY database update during streaming
	assistantMsg.Content = &response
	if err := s.db.Save(&assistantMsg).Error; err != nil {
		Log.WithField("error", err).Error("Failed to save final response to database")
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
			ID:           *chat.ThreadID,
			Title:        *chat.ThreadTitle,
			CreatedAt:    chat.CreatedAt,
			UpdatedAt:    chat.UpdatedAt,
			ArchivedAt:   chat.ArchivedAt,
			Settings:     settings,
			MessageCount: 1, // Will have one user message + one assistant message
		}

		threadData, _ := json.Marshal(threadResponse)
		fmt.Fprintf(w, "data: {\"type\": \"thread\", \"thread\": %s}\n\n", threadData)
		flusher.Flush()
	}

	// Send completion signal
	fmt.Fprintf(w, "data: {\"type\": \"complete\"}\n\n")
	flusher.Flush()
}

// Handle synchronous response without streaming
func (s *Server) handleSynchronousResponse(w http.ResponseWriter, chat *Chat, userMessage *ChatMessage, isNewThread bool) {
	// Load chat with full relationships
	s.db.Preload("User").Preload("Role").First(chat, chat.ID)

	// Get live messages for context (now includes the saved user message)
	var messages []ChatMessage
	s.db.Where("chat_id = ? AND is_live = ?", chat.ChatID, true).
		Order("created_at ASC").
		Find(&messages)

	// Convert to OpenAI format (user message is now in DB, no need to append)
	history := s.convertMessagesToOpenAI(messages, chat)

	// Generate AI response
	response, err := s.generateResponseForThread(chat, history)
	if err != nil {
		Log.WithFields(map[string]interface{}{"thread_id": *chat.ThreadID, "error": err}).Error("Failed to generate response for thread")
		s.writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to generate response: %v", err))
		return
	}

	// Save AI response to database
	assistantMsg := ChatMessage{
		ChatID:      chat.ChatID,
		Role:        "assistant",
		Content:     &response,
		IsLive:      true,
		MessageType: "normal",
	}

	if err := s.db.Create(&assistantMsg).Error; err != nil {
		Log.WithField("error", err).Error("Failed to save assistant message")
		s.writeJSONError(w, http.StatusInternalServerError, "Failed to save response")
		return
	}

	// Return the complete response
	msgResponse := MessageResponse{
		ID:          assistantMsg.ID,
		Role:        "assistant",
		Content:     &response,
		CreatedAt:   assistantMsg.CreatedAt,
		IsLive:      true,
		MessageType: "normal",
	}

	if isNewThread {
		// Include thread data in response for new threads
		settings := ThreadSettings{
			ModelName:    chat.ModelName,
			Temperature:  chat.Temperature,
			RoleID:       chat.RoleID,
			Lang:         chat.Lang,
			MasterPrompt: chat.MasterPrompt,
			ContextLimit: chat.ContextLimit,
		}

		threadResponse := ThreadResponse{
			ID:           *chat.ThreadID,
			Title:        *chat.ThreadTitle,
			CreatedAt:    chat.CreatedAt,
			UpdatedAt:    chat.UpdatedAt,
			ArchivedAt:   chat.ArchivedAt,
			Settings:     settings,
			MessageCount: 2, // User message + assistant message
		}

		combinedResponse := ChatWithThreadResponse{
			Message: &msgResponse,
			Thread:  &threadResponse,
		}

		s.writeJSONSuccess(w, "Response generated successfully", combinedResponse)
	} else {
		s.writeJSONSuccess(w, "Response generated successfully", msgResponse)
	}
}

// Helper methods for AI providers
func (s *Server) callAnthropic(ctx context.Context, modelID string, history []openai.ChatMessage, temperature float64) (string, error) {
	// For webapp integration, use fallback to OpenAI for now
	return "", fmt.Errorf("anthropic integration not implemented for webapp - use OpenAI models")
}

func (s *Server) callGemini(ctx context.Context, modelID string, history []openai.ChatMessage, temperature float64) (string, error) {
	// For webapp integration, use fallback to OpenAI for now
	return "", fmt.Errorf("gemini integration not implemented for webapp - use OpenAI models")
}

func (s *Server) generateResponseForThread(chat *Chat, history []openai.ChatMessage) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	model := s.getModel(chat.ModelName)
	if model == nil {
		return "", fmt.Errorf("model %s not found", chat.ModelName)
	}

	options := openai.ChatCompletionOptions{}.SetTemperature(chat.Temperature)

	switch model.Provider {
	case "openai":
		response, err := s.openAI.CreateChatCompletionWithContext(ctx, model.ModelID, history, options)
		if err != nil {
			return "", err
		}
		if len(response.Choices) > 0 {
			if content, ok := response.Choices[0].Message.Content.(string); ok {
				return content, nil
			}
		}
		return "", fmt.Errorf("no response generated")

	case "anthropic":
		if s.anthropic == nil {
			return "", fmt.Errorf("anthropic client not initialized")
		}
		// Convert OpenAI messages to Anthropic format and make the call
		response, err := s.callAnthropic(ctx, model.ModelID, history, chat.Temperature)
		if err != nil {
			return "", err
		}
		return response, nil

	case "gemini":
		if s.gemini == nil {
			return "", fmt.Errorf("gemini client not initialized")
		}
		// Convert OpenAI messages to Gemini format and make the call
		response, err := s.callGemini(ctx, model.ModelID, history, chat.Temperature)
		if err != nil {
			return "", err
		}
		return response, nil

	default:
		return "", fmt.Errorf("unsupported provider: %s", model.Provider)
	}
}

// Input validation and sanitization helpers

// validateString validates basic string input with length limits
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
	if settings.Temperature < 0 || settings.Temperature > 2 {
		return fmt.Errorf("temperature must be between 0 and 2")
	}

	// Validate master prompt
	if settings.MasterPrompt != "" {
		if err := validateString(settings.MasterPrompt, 0, 2000); err != nil {
			return fmt.Errorf("invalid master prompt: %v", err)
		}
	}

	// Validate context limit
	if settings.ContextLimit < 100 || settings.ContextLimit > 10000 {
		return fmt.Errorf("context limit must be between 100 and 10000")
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

// CSRF token management functions

// generateCSRFToken generates a cryptographically secure random token
func generateCSRFToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(bytes), nil
}

// getOrCreateCSRFToken gets or creates a CSRF token for the user
func (store *CSRFTokenStore) getOrCreateCSRFToken(userID uint) (string, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()

	if token, exists := store.tokens[userID]; exists {
		return token, nil
	}

	token, err := generateCSRFToken()
	if err != nil {
		return "", err
	}

	store.tokens[userID] = token
	return token, nil
}

// validateCSRFToken validates a CSRF token for the user
func (store *CSRFTokenStore) validateCSRFToken(userID uint, token string) bool {
	store.mutex.RLock()
	defer store.mutex.RUnlock()

	expectedToken, exists := store.tokens[userID]
	if !exists {
		return false
	}

	// Use constant-time comparison to prevent timing attacks
	return subtle.ConstantTimeCompare([]byte(expectedToken), []byte(token)) == 1
}

// removeCSRFToken removes a CSRF token (useful for cleanup)
func (store *CSRFTokenStore) removeCSRFToken(userID uint) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	delete(store.tokens, userID)
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
