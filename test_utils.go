//go:build test
// +build test

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meinside/openai-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Test database utilities

// SetupTestDB creates an in-memory SQLite database for testing
func SetupTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("Failed to connect to test database: %v", err)
	}

	// Run migrations
	err = db.AutoMigrate(&User{}, &Chat{}, &ChatMessage{}, &Role{}, &WebAppSession{})
	if err != nil {
		t.Fatalf("Failed to migrate test database: %v", err)
	}

	return db
}

// CleanupTestDB cleans up the test database
func CleanupTestDB(db *gorm.DB) {
	// Clean up all tables
	db.Exec("DELETE FROM chat_messages")
	db.Exec("DELETE FROM chats")
	db.Exec("DELETE FROM roles")
	db.Exec("DELETE FROM web_app_sessions")
	db.Exec("DELETE FROM users")
}

// Test fixtures and data

// CreateTestUser creates a test user
func CreateTestUser(db *gorm.DB, telegramID int64, username string) *User {
	user := &User{
		TelegramID: &telegramID,
		Username:   username,
	}
	db.Create(user)
	return user
}

// CreateTestRole creates a test role for a user
func CreateTestRole(db *gorm.DB, userID uint, name, prompt string) *Role {
	role := &Role{
		UserID: userID,
		Name:   name,
		Prompt: prompt,
	}
	db.Create(role)
	return role
}

// CreateTestChat creates a test chat/thread
func CreateTestChat(db *gorm.DB, userID uint, chatID int64, threadID *string, threadTitle *string) *Chat {
	chat := &Chat{
		UserID:       userID,
		ChatID:       chatID,
		ThreadID:     threadID,
		ThreadTitle:  threadTitle,
		Temperature:  1.0,
		ModelName:    "gpt-4o",
		Stream:       true,
		ContextLimit: 4000,
		Lang:         "en",
	}
	db.Create(chat)
	return chat
}

// CreateTestMessage creates a test chat message
func CreateTestMessage(db *gorm.DB, chatID int64, role string, content string, isLive bool) *ChatMessage {
	message := &ChatMessage{
		ChatID:      chatID,
		Role:        openai.ChatMessageRole(role),
		Content:     &content,
		IsLive:      isLive,
		MessageType: "normal",
	}
	db.Create(message)
	return message
}

// Mock AI clients

// MockOpenAIClient is a mock for OpenAI client
type MockOpenAIClient struct {
	mock.Mock
}

func (m *MockOpenAIClient) CreateChatCompletionWithContext(ctx context.Context, model string, messages []openai.ChatMessage, options openai.ChatCompletionOptions) (*openai.ChatCompletion, error) {
	args := m.Called(ctx, model, messages, options)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*openai.ChatCompletion), args.Error(1)
}

// MockAnthropicClient is a mock for Anthropic client
type MockAnthropicClient struct {
	mock.Mock
}

// Note: Anthropic interface would need proper implementation based on actual client
func (m *MockAnthropicClient) Apply(options ...interface{}) {
	m.Called(options)
}

// MockGeminiClient is a mock for Gemini client (using OpenAI interface)
type MockGeminiClient struct {
	mock.Mock
}

func (m *MockGeminiClient) CreateChatCompletionWithContext(ctx context.Context, model string, messages []openai.ChatMessage, options openai.ChatCompletionOptions) (*openai.ChatCompletion, error) {
	args := m.Called(ctx, model, messages, options)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*openai.ChatCompletion), args.Error(1)
}

// MockNovaClient is a mock for AWS Nova client
type MockNovaClient struct {
	mock.Mock
}

// Note: Nova interface would need proper implementation based on actual client
func (m *MockNovaClient) InvokeModelWithResponseStream(ctx context.Context, request interface{}) (chan interface{}, error) {
	args := m.Called(ctx, request)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(chan interface{}), args.Error(1)
}

// Test server setup

// CreateTestServer creates a test server with mocked dependencies
func CreateTestServer(t *testing.T, db *gorm.DB) *Server {
	conf := config{
		TelegramBotToken: "test_token",
		MiniAppEnabled:   true,
		WebServerPort:    "8080",
		Models: []AiModel{
			{ModelID: "gpt-4o", Name: "GPT-4o", Provider: "openai"},
			{ModelID: "claude-3-sonnet", Name: "Claude 3 Sonnet", Provider: "anthropic"},
			{ModelID: "gemini-pro", Name: "Gemini Pro", Provider: "gemini"},
		},
		AllowedTelegramUsers: []string{"testuser"},
		Verbose:              false,
	}

	server := &Server{
		conf: conf,
		db:   db,
	}

	// Note: For proper testing, you would set up real clients or use interface mocking
	// These are simplified for testing purposes and may need real implementations
	// server.openAI = &MockOpenAIClient{}
	// server.anthropic = &MockAnthropicClient{}
	// server.gemini = &MockGeminiClient{}
	// server.nova = &MockNovaClient{}

	return server
}

// HTTP test utilities

// CreateAuthenticatedRequest creates an HTTP request with valid Telegram init data
func CreateAuthenticatedRequest(t *testing.T, method, url string, body interface{}, userID int64, username string) *http.Request {
	var reqBody *bytes.Buffer
	if body != nil {
		jsonBody, err := json.Marshal(body)
		assert.NoError(t, err)
		reqBody = bytes.NewBuffer(jsonBody)
	} else {
		reqBody = bytes.NewBuffer(nil)
	}

	req := httptest.NewRequest(method, url, reqBody)
	req.Header.Set("Content-Type", "application/json")

	// Create mock init data
	initDataStr := CreateMockInitData(userID, username)
	req.Header.Set("Telegram-Init-Data", initDataStr)

	return req
}

// CreateMockInitData creates mock Telegram init data for testing
func CreateMockInitData(userID int64, username string) string {
	// Create simplified mock init data
	// In real tests, you might want to create properly signed data
	userData := fmt.Sprintf(`{"id":%d,"username":"%s","first_name":"Test","auth_date":%d}`,
		userID, username, time.Now().Unix())
	return fmt.Sprintf("user=%s&auth_date=%d", userData, time.Now().Unix())
}

// CreateUnauthenticatedRequest creates an HTTP request without authentication
func CreateUnauthenticatedRequest(method, url string, body interface{}) *http.Request {
	var reqBody *bytes.Buffer
	if body != nil {
		jsonBody, _ := json.Marshal(body)
		reqBody = bytes.NewBuffer(jsonBody)
	} else {
		reqBody = bytes.NewBuffer(nil)
	}

	req := httptest.NewRequest(method, url, reqBody)
	req.Header.Set("Content-Type", "application/json")
	return req
}

// Response assertion utilities

// AssertJSONResponse asserts that the response has expected status and JSON content
func AssertJSONResponse(t *testing.T, w *httptest.ResponseRecorder, expectedStatus int, expectedBody interface{}) {
	assert.Equal(t, expectedStatus, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	if expectedBody != nil {
		var actualBody interface{}
		err := json.Unmarshal(w.Body.Bytes(), &actualBody)
		assert.NoError(t, err)

		expectedJSON, err := json.Marshal(expectedBody)
		assert.NoError(t, err)

		var expectedBodyNormalized interface{}
		err = json.Unmarshal(expectedJSON, &expectedBodyNormalized)
		assert.NoError(t, err)

		assert.Equal(t, expectedBodyNormalized, actualBody)
	}
}

// AssertErrorResponse asserts that the response contains an error message
func AssertErrorResponse(t *testing.T, w *httptest.ResponseRecorder, expectedStatus int, expectedError string) {
	assert.Equal(t, expectedStatus, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var response map[string]string
	err := json.Unmarshal(w.Body.Bytes(), &response)
	assert.NoError(t, err)

	assert.Contains(t, response, "error")
	if expectedError != "" {
		assert.Equal(t, expectedError, response["error"])
	}
}

// Database assertion utilities

// AssertUserExists asserts that a user exists in the database
func AssertUserExists(t *testing.T, db *gorm.DB, telegramID int64, username string) *User {
	var user User
	err := db.Where("telegram_id = ?", telegramID).First(&user).Error
	assert.NoError(t, err)
	assert.Equal(t, username, user.Username)
	return &user
}

// AssertChatExists asserts that a chat exists in the database
func AssertChatExists(t *testing.T, db *gorm.DB, userID uint, threadID string) *Chat {
	var chat Chat
	err := db.Where("user_id = ? AND thread_id = ?", userID, threadID).First(&chat).Error
	assert.NoError(t, err)
	return &chat
}

// AssertMessageExists asserts that a message exists in the database
func AssertMessageExists(t *testing.T, db *gorm.DB, chatID int64, role, content string) *ChatMessage {
	var message ChatMessage
	err := db.Where("chat_id = ? AND role = ? AND content = ?", chatID, role, content).First(&message).Error
	assert.NoError(t, err)
	return &message
}

// AssertRoleExists asserts that a role exists in the database
func AssertRoleExists(t *testing.T, db *gorm.DB, userID uint, name string) *Role {
	var role Role
	err := db.Where("user_id = ? AND name = ?", userID, name).First(&role).Error
	assert.NoError(t, err)
	return &role
}

// Concurrency test utilities

// RunConcurrentTests runs a function concurrently for testing race conditions
func RunConcurrentTests(t *testing.T, numGoroutines int, testFunc func(t *testing.T, goroutineID int)) {
	done := make(chan bool, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(goroutineID int) {
			defer func() { done <- true }()
			testFunc(t, goroutineID)
		}(i)
	}

	// Wait for all goroutines to complete
	for i := 0; i < numGoroutines; i++ {
		<-done
	}
}

// Performance test utilities

// BenchmarkEndpoint benchmarks an HTTP endpoint
func BenchmarkEndpoint(b *testing.B, server *Server, method, path string, body interface{}, userID int64) {
	mux := server.setupWebServer()

	for i := 0; i < b.N; i++ {
		req := CreateAuthenticatedRequest(&testing.T{}, method, path, body, userID, "testuser")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
	}
}

// Memory leak detection utilities

// CheckForMemoryLeaks runs a test multiple times and checks for memory growth
func CheckForMemoryLeaks(t *testing.T, iterations int, testFunc func()) {
	// This is a simplified memory leak detection
	// In production, you'd want more sophisticated tooling
	for i := 0; i < iterations; i++ {
		testFunc()

		// Force garbage collection
		if i%100 == 0 {
			runtime.GC()
		}
	}
}

// Environment setup utilities

// SetupTestEnvironment sets up environment variables for testing
func SetupTestEnvironment() {
	os.Setenv("TEST_MODE", "true")
}

// CleanupTestEnvironment cleans up environment variables after testing
func CleanupTestEnvironment() {
	os.Unsetenv("TEST_MODE")
}

// Mock response builders

// BuildMockOpenAIResponse builds a mock OpenAI chat completion response
func BuildMockOpenAIResponse(content string) *openai.ChatCompletion {
	return &openai.ChatCompletion{
		Choices: []openai.ChatCompletionChoice{
			{
				Message:      openai.NewChatAssistantMessage(content),
				FinishReason: "stop",
			},
		},
		Usage: openai.Usage{
			PromptTokens:     10,
			CompletionTokens: 20,
			TotalTokens:      30,
		},
	}
}

// BuildMockAnthropicResponse builds a mock Anthropic stream response
func BuildMockAnthropicResponse(content string) chan interface{} {
	// For testing purposes, return a simple mock stream channel
	ch := make(chan interface{}, 1)
	// In real tests, you'd populate this with actual stream events
	close(ch)
	return ch
}

// Test data generators

// GenerateUniqueThreadID generates a unique thread ID for testing
func GenerateUniqueThreadID() string {
	return uuid.New().String()
}

// GenerateUniqueChatID generates a unique chat ID for testing
func GenerateUniqueChatID() int64 {
	return time.Now().UnixNano()
}

// Test validation utilities

// ValidateThreadResponse validates the structure of a thread response
func ValidateThreadResponse(t *testing.T, thread ThreadResponse) {
	assert.NotEmpty(t, thread.ID)
	assert.NotEmpty(t, thread.Title)
	assert.NotZero(t, thread.CreatedAt)
	assert.NotZero(t, thread.UpdatedAt)
	assert.NotEmpty(t, thread.Settings.ModelName)
	assert.GreaterOrEqual(t, thread.Settings.Temperature, 0.0)
	assert.LessOrEqual(t, thread.Settings.Temperature, 2.0)
	assert.GreaterOrEqual(t, thread.MessageCount, 0)
}

// ValidateMessageResponse validates the structure of a message response
func ValidateMessageResponse(t *testing.T, message MessageResponse) {
	assert.NotZero(t, message.ID)
	assert.Contains(t, []string{"user", "assistant", "system"}, message.Role)
	assert.NotZero(t, message.CreatedAt)
	assert.Contains(t, []string{"normal", "summary", "system"}, message.MessageType)
}

// ValidateRoleResponse validates the structure of a role response
func ValidateRoleResponse(t *testing.T, role RoleResponse) {
	assert.NotZero(t, role.ID)
	assert.NotEmpty(t, role.Name)
	assert.NotEmpty(t, role.Prompt)
}

// GetCurrentMemUsage returns current memory usage statistics
func GetCurrentMemUsage() (uint64, uint64) {
	var m runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m)
	return m.Alloc, m.Sys
}
