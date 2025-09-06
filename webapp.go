package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/meinside/openai-go"
	initdata "github.com/telegram-mini-apps/init-data-golang"
	"gorm.io/gorm"
)

// WebApp session for authentication
type WebAppSession struct {
	gorm.Model
	UserID      uint
	SessionData string `gorm:"type:text"`
	ExpiresAt   time.Time
}

// Thread settings structure
type ThreadSettings struct {
	ModelName    string  `json:"model_name"`
	Temperature  float64 `json:"temperature"`
	RoleID       *uint   `json:"role_id"`
	Stream       bool    `json:"stream"`
	QA           bool    `json:"qa"`
	Voice        bool    `json:"voice"`
	Lang         string  `json:"lang"`
	MasterPrompt string  `json:"master_prompt"`
	ContextLimit int     `json:"context_limit"`
}

// API request/response structures
type CreateThreadRequest struct {
	InitialMessage string          `json:"initial_message"`
	Settings       *ThreadSettings `json:"settings,omitempty"`
}

type ChatRequest struct {
	Message string `json:"message"`
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

func (s *Server) setupWebServer() *http.ServeMux {
	if !s.conf.MiniAppEnabled {
		return nil
	}

	mux := http.NewServeMux()

	// Serve static files
	mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir("webapp/assets"))))

	// Mini app route
	mux.HandleFunc("/miniapp", s.corsMiddleware(s.serveMiniApp))

	// API endpoints with CORS and authentication middleware
	mux.HandleFunc("/api/threads", s.corsMiddleware(s.authMiddleware(s.handleThreads)))
	mux.HandleFunc("/api/threads/archived", s.corsMiddleware(s.authMiddleware(s.getArchivedThreads)))
	mux.HandleFunc("/api/threads/", s.corsMiddleware(s.authMiddleware(s.handleThreadsWithID)))
	mux.HandleFunc("/api/models", s.corsMiddleware(s.authMiddleware(s.getAvailableModels)))
	mux.HandleFunc("/api/roles", s.corsMiddleware(s.authMiddleware(s.handleRoles)))
	mux.HandleFunc("/api/roles/", s.corsMiddleware(s.authMiddleware(s.handleRolesWithID)))
	mux.HandleFunc("/api/user", s.corsMiddleware(s.authMiddleware(s.getUserInfo)))

	return mux
}

// CORS middleware
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

// Auth middleware with user context
type contextKey string

const userContextKey contextKey = "user"

func (s *Server) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		initDataString := r.Header.Get("Telegram-Init-Data")
		if initDataString == "" {
			s.writeJSONError(w, http.StatusUnauthorized, "Missing Telegram init data")
			return
		}

		if err := initdata.Validate(initDataString, s.conf.TelegramBotToken, 24*time.Hour); err != nil {
			s.writeJSONError(w, http.StatusUnauthorized, "Invalid Telegram authentication")
			return
		}

		// Parse user info from init data
		parsed, err := initdata.Parse(initDataString)
		if err != nil {
			s.writeJSONError(w, http.StatusUnauthorized, "Failed to parse init data")
			return
		}

		// Get or create user
		user, err := s.getOrCreateUserFromInitData(parsed)
		if err != nil {
			s.writeJSONError(w, http.StatusInternalServerError, "Failed to get user")
			return
		}

		// Add user to context
		ctx := context.WithValue(r.Context(), userContextKey, user)
		next(w, r.WithContext(ctx))
	}
}

// Helper functions for JSON responses
func (s *Server) writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
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

	// Try to find existing user by Telegram ID
	if initData.User.ID != 0 {
		telegramID := int64(initData.User.ID)
		err := s.db.Preload("Roles").Where("telegram_id = ?", telegramID).First(&user).Error
		if err != nil && err != gorm.ErrRecordNotFound {
			return nil, err
		}
		if err == nil {
			return &user, nil
		}

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
		Log.WithField("error", err).Error("Failed to parse template file")
		s.writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Template error: %v", err))
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
		Log.WithField("error", err).Error("Failed to execute template")
		s.writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Template execution error: %v", err))
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

	var chats []Chat
	err := s.db.Where("user_id = ? AND thread_id IS NOT NULL AND archived_at IS NULL", user.ID).
		Order("updated_at DESC").
		Preload("Role").
		Find(&chats).Error
	if err != nil {
		s.writeJSONError(w, http.StatusInternalServerError, "Failed to fetch threads")
		return
	}

	threads := make([]ThreadResponse, len(chats))
	for i, chat := range chats {
		var messageCount int64
		s.db.Model(&ChatMessage{}).Where("chat_id = ?", chat.ChatID).Count(&messageCount)

		settings := ThreadSettings{
			ModelName:    chat.ModelName,
			Temperature:  chat.Temperature,
			RoleID:       chat.RoleID,
			Stream:       chat.Stream,
			QA:           chat.QA,
			Voice:        chat.Voice,
			Lang:         chat.Lang,
			MasterPrompt: chat.MasterPrompt,
			ContextLimit: chat.ContextLimit,
		}

		threads[i] = ThreadResponse{
			ID:           *chat.ThreadID,
			Title:        *chat.ThreadTitle,
			CreatedAt:    chat.CreatedAt,
			UpdatedAt:    chat.UpdatedAt,
			ArchivedAt:   chat.ArchivedAt,
			Settings:     settings,
			MessageCount: int(messageCount),
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

	var chats []Chat
	err := s.db.Where("user_id = ? AND thread_id IS NOT NULL AND archived_at IS NOT NULL", user.ID).
		Order("archived_at DESC").
		Preload("Role").
		Find(&chats).Error
	if err != nil {
		s.writeJSONError(w, http.StatusInternalServerError, "Failed to fetch archived threads")
		return
	}

	threads := make([]ThreadResponse, len(chats))
	for i, chat := range chats {
		var messageCount int64
		s.db.Model(&ChatMessage{}).Where("chat_id = ?", chat.ChatID).Count(&messageCount)

		settings := ThreadSettings{
			ModelName:    chat.ModelName,
			Temperature:  chat.Temperature,
			RoleID:       chat.RoleID,
			Stream:       chat.Stream,
			QA:           chat.QA,
			Voice:        chat.Voice,
			Lang:         chat.Lang,
			MasterPrompt: chat.MasterPrompt,
			ContextLimit: chat.ContextLimit,
		}

		threads[i] = ThreadResponse{
			ID:           *chat.ThreadID,
			Title:        *chat.ThreadTitle,
			CreatedAt:    chat.CreatedAt,
			UpdatedAt:    chat.UpdatedAt,
			ArchivedAt:   chat.ArchivedAt,
			Settings:     settings,
			MessageCount: int(messageCount),
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

	var req CreateThreadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeJSONError(w, http.StatusBadRequest, "Invalid request format")
		return
	}

	// Generate thread ID and title using miniModel
	threadID := uuid.New().String()
	title, err := s.generateThreadTitle(req.InitialMessage)
	if err != nil {
		title = "New Conversation" // Fallback
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
		chat.Stream = req.Settings.Stream
		chat.QA = req.Settings.QA
		chat.Voice = req.Settings.Voice
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

	// Add initial message
	message := ChatMessage{
		ChatID:      chat.ChatID,
		Role:        "user",
		Content:     &req.InitialMessage,
		IsLive:      true,
		MessageType: "normal",
	}

	if err := s.db.Create(&message).Error; err != nil {
		s.writeJSONError(w, http.StatusInternalServerError, "Failed to create initial message")
		return
	}

	// Generate AI response
	go s.processThreadMessage(&chat, req.InitialMessage)

	s.writeJSON(w, http.StatusCreated, map[string]interface{}{
		"thread_id": threadID,
		"title":     title,
		"message":   "Thread created successfully",
	})
}

// Handle /api/threads/{id}/... routes
func (s *Server) handleThreadsWithID(w http.ResponseWriter, r *http.Request) {
	threadID := extractPathParam(r.URL.Path, "/api/threads")
	if threadID == "" {
		s.writeJSONError(w, http.StatusBadRequest, "Thread ID required")
		return
	}

	// Check if it's a sub-resource or main thread operation
	subPath := strings.TrimPrefix(r.URL.Path, "/api/threads/"+threadID)

	switch {
	case subPath == "/messages":
		switch r.Method {
		case http.MethodGet:
			s.getThreadMessages(w, r, threadID)
		case http.MethodPost:
			s.chatInThread(w, r, threadID)
		default:
			s.writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
	// Remove polling endpoint as it's no longer needed
	case subPath == "/settings":
		switch r.Method {
		case http.MethodPut:
			s.updateThreadSettings(w, r, threadID)
		default:
			s.writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
	case subPath == "/archive":
		switch r.Method {
		case http.MethodPut:
			s.archiveThread(w, r, threadID)
		default:
			s.writeJSONError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
	case subPath == "" || subPath == "/":
		// Main thread operations
		switch r.Method {
		case http.MethodDelete:
			s.deleteThread(w, r, threadID)
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
		response[i] = MessageResponse{
			ID:          msg.ID,
			Role:        string(msg.Role),
			Content:     msg.Content,
			CreatedAt:   msg.CreatedAt,
			IsLive:      msg.IsLive,
			MessageType: msg.MessageType,
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

	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeJSONError(w, http.StatusBadRequest, "Invalid request format")
		return
	}

	// Find chat by thread ID
	var chat Chat
	err := s.db.Where("user_id = ? AND thread_id = ?", user.ID, threadID).
		Preload("Role").
		First(&chat).Error
	if err != nil {
		s.writeJSONError(w, http.StatusNotFound, "Thread not found")
		return
	}

	// Add user message
	message := ChatMessage{
		ChatID:      chat.ChatID,
		Role:        "user",
		Content:     &req.Message,
		IsLive:      true,
		MessageType: "normal",
	}

	if err := s.db.Create(&message).Error; err != nil {
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
		s.handleStreamingResponse(w, r, &chat, req.Message)
	} else {
		// Process synchronously and return complete response
		s.handleSynchronousResponse(w, &chat, req.Message)
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

	// Find and update chat
	var chat Chat
	err := s.db.Where("user_id = ? AND thread_id = ?", user.ID, threadID).First(&chat).Error
	if err != nil {
		s.writeJSONError(w, http.StatusNotFound, "Thread not found")
		return
	}

	// Update settings
	updates := map[string]interface{}{
		"model_name":    settings.ModelName,
		"temperature":   settings.Temperature,
		"role_id":       settings.RoleID,
		"stream":        settings.Stream,
		"qa":            settings.QA,
		"voice":         settings.Voice,
		"lang":          settings.Lang,
		"master_prompt": settings.MasterPrompt,
		"context_limit": settings.ContextLimit,
	}

	if err := s.db.Model(&chat).Updates(updates).Error; err != nil {
		s.writeJSONError(w, http.StatusInternalServerError, "Failed to update settings")
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]string{"message": "Settings updated successfully"})
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

	s.writeJSON(w, http.StatusOK, map[string]string{"message": fmt.Sprintf("Thread %s successfully", action)})
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

	s.writeJSON(w, http.StatusOK, map[string]string{"message": "Thread deleted successfully"})
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

	roleID, err := strconv.ParseUint(roleIDStr, 10, 32)
	if err != nil {
		s.writeJSONError(w, http.StatusBadRequest, "Invalid role ID")
		return
	}

	switch r.Method {
	case http.MethodPut:
		s.updateRole(w, r, uint(roleID))
	case http.MethodDelete:
		s.deleteRole(w, r, uint(roleID))
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

	role := Role{
		UserID: user.ID,
		Name:   req.Name,
		Prompt: req.Prompt,
	}

	if err := s.db.Create(&role).Error; err != nil {
		s.writeJSONError(w, http.StatusInternalServerError, "Failed to create role")
		return
	}

	s.writeJSON(w, http.StatusCreated, map[string]interface{}{"id": role.ID, "message": "Role created successfully"})
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

	var role Role
	err := s.db.Where("id = ? AND user_id = ?", roleID, user.ID).First(&role).Error
	if err != nil {
		s.writeJSONError(w, http.StatusNotFound, "Role not found")
		return
	}

	role.Name = req.Name
	role.Prompt = req.Prompt

	if err := s.db.Save(&role).Error; err != nil {
		s.writeJSONError(w, http.StatusInternalServerError, "Failed to update role")
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]string{"message": "Role updated successfully"})
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

	s.writeJSON(w, http.StatusOK, map[string]string{"message": "Role deleted successfully"})
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

func (s *Server) processThreadMessage(chat *Chat, message string) {
	defer func() {
		if r := recover(); r != nil {
			Log.WithField("panic", r).Error("Panic in processThreadMessage")
		}
	}()

	// Load chat with full relationships
	s.db.Preload("User").Preload("Role").First(chat, chat.ID)

	// Get live messages for context
	var messages []ChatMessage
	s.db.Where("chat_id = ? AND is_live = ?", chat.ChatID, true).
		Order("created_at ASC").
		Find(&messages)

	// Convert to OpenAI format - this already includes the user message from DB
	history := s.convertMessagesToOpenAI(messages, chat)

	// Generate AI response
	response, err := s.generateResponseForThread(chat, history)
	if err != nil {
		Log.WithFields(map[string]interface{}{"thread_id": *chat.ThreadID, "error": err}).Error("Failed to generate response for thread")
		// Save error message
		errorContent := fmt.Sprintf("Sorry, I encountered an error: %s", err.Error())
		errorMsg := ChatMessage{
			ChatID:      chat.ChatID,
			Role:        "assistant",
			Content:     &errorContent,
			IsLive:      true,
			MessageType: "normal",
		}
		s.db.Create(&errorMsg)
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
	}
}

// New function that builds response incrementally for polling
func (s *Server) processThreadMessageWithUpdates(chat *Chat, message string) {
	// Create context with timeout for the entire processing
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	defer func() {
		if r := recover(); r != nil {
			Log.WithField("panic", r).Error("Panic in processThreadMessageWithUpdates")
			debug.PrintStack()
		}
	}()

	// Load chat with full relationships
	s.db.Preload("User").Preload("Role").First(chat, chat.ID)

	// Get live messages for context
	var messages []ChatMessage
	s.db.Where("chat_id = ? AND is_live = ?", chat.ChatID, true).
		Order("created_at ASC").
		Find(&messages)

	// Convert to OpenAI format - this should include the new user message from DB
	history := s.convertMessagesToOpenAI(messages, chat)

	// Create placeholder assistant message that we'll update as we build the response
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

	// Generate AI response and update the message as we build it
	response, err := s.generateResponseWithStreamingUpdates(ctx, chat, history, &assistantMsg, nil, nil)
	if err != nil {
		Log.WithFields(map[string]interface{}{"thread_id": *chat.ThreadID, "error": err}).Error("Failed to generate response for thread")
		// Update with error message
		errorContent := fmt.Sprintf("Sorry, I encountered an error: %s", err.Error())
		assistantMsg.Content = &errorContent
		if updateErr := s.db.Save(&assistantMsg).Error; updateErr != nil {
			Log.WithField("error", updateErr).Error("Failed to save error message")
		}
		return
	}

	// Final update with complete response
	assistantMsg.Content = &response
	if err := s.db.Save(&assistantMsg).Error; err != nil {
		Log.WithField("error", err).Error("Failed to save final assistant message")
	}
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
		if msg.Content != nil {
			history = append(history, openai.ChatMessage{
				Role:    openai.ChatMessageRole(msg.Role),
				Content: *msg.Content,
			})
		}
	}

	return history
}

// Handle streaming response with Server-Sent Events
func (s *Server) handleStreamingResponse(w http.ResponseWriter, r *http.Request, chat *Chat, message string) {
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

	// Get live messages for context
	var messages []ChatMessage
	s.db.Where("chat_id = ? AND is_live = ?", chat.ChatID, true).
		Order("created_at ASC").
		Find(&messages)

	// Convert to OpenAI format (this should already include the user message that was just saved)
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

	// Send completion signal
	fmt.Fprintf(w, "data: {\"type\": \"complete\"}\n\n")
	flusher.Flush()
}

// Handle synchronous response without streaming
func (s *Server) handleSynchronousResponse(w http.ResponseWriter, chat *Chat, message string) {
	// Load chat with full relationships
	s.db.Preload("User").Preload("Role").First(chat, chat.ID)

	// Get live messages for context
	var messages []ChatMessage
	s.db.Where("chat_id = ? AND is_live = ?", chat.ChatID, true).
		Order("created_at ASC").
		Find(&messages)

	// Convert to OpenAI format
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

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"message":  "Response generated successfully",
		"response": msgResponse,
	})
}

// Helper functions for different AI providers
func (s *Server) callAnthropic(ctx context.Context, modelID string, history []openai.ChatMessage, temperature float64) (string, error) {
	// For now, return error since we need to integrate with existing anthropic logic
	// TODO: Integrate with existing getAnthropicAnswer function
	return "", fmt.Errorf("anthropic integration not yet implemented for webapp")
}

func (s *Server) callGemini(ctx context.Context, modelID string, history []openai.ChatMessage, temperature float64) (string, error) {
	// For now, return error since we need to integrate with existing gemini logic
	// TODO: Integrate with existing gemini functions
	return "", fmt.Errorf("gemini integration not yet implemented for webapp")
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
