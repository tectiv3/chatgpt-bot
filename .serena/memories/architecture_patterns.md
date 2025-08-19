# Architecture Patterns and Design Guidelines

## Core Architecture

### Server Pattern
The application uses a central `Server` struct that holds all dependencies:
- Embedded `sync.RWMutex` for thread-safe operations
- All services (AI clients, bot, database) as fields
- Methods on Server struct handle business logic

### Message Processing Flow
1. **Telegram → Bot Handler** (`bot.go`, `tele_handlers.go`)
   - Webhook or polling receives message
   - Middleware checks authentication
   - Routes to appropriate handler

2. **Handler → LLM Processing** (`llm.go`)
   - Builds conversation context from database
   - Selects AI provider based on model config
   - Handles streaming or non-streaming response

3. **Function Calling** (`function_calls.go`)
   - Tools requested by AI are executed
   - Results fed back to continue conversation
   - Supports multiple rounds of tool use

4. **Response → Telegram**
   - Markdown formatting applied
   - Large messages split if needed
   - Updates stored in database

### Database Patterns
- Repository pattern via GORM
- Models defined with proper relationships
- Transactions for multi-step operations
- Soft deletes for audit trail

### State Management
- State machine pattern for complex workflows
- States persisted in database
- Timeout-based expiration
- Used for multi-step user interactions

### Error Handling Strategy
- Errors bubbled up, not swallowed
- Context-aware error messages
- Graceful degradation for non-critical failures
- User-friendly error messages via Telegram

### Configuration Management
- Single config.json file
- Struct-based configuration
- Environment variable support via godotenv
- Hot-reload not implemented (requires restart)

### Provider Abstraction
- Unified interface for AI providers
- Provider selection at runtime
- Fallback mechanisms for failures
- Provider-specific features exposed via config

### Audio Processing Pipeline
```
Telegram Voice → Opus → WAV → MP3 → OpenAI Whisper
                                ↓
                          Transcription
                                ↓
                          LLM Processing
                                ↓
                          TTS (Optional)
                                ↓
                          MP3 → Telegram
```

### Security Patterns
- User whitelist enforcement
- API key management via config
- Rate limiting per user
- No credential logging

### Concurrency Patterns
- Goroutines for async operations
- Context for cancellation
- Mutexes for shared state
- Channel-based communication where appropriate