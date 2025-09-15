# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build and Development Commands

### Build the application
```bash
go build
```

### Run the application
```bash
./chatgpt-bot
```

### Install required dependencies (macOS)
```bash
brew install lame opusfile pkg-config
```

### Install required dependencies (Ubuntu/Debian)
```bash
sudo apt-get install libmp3lame0 libopus0 libopusfile0 libogg0 libmp3lame-dev libopusfile-dev
```

## Architecture Overview

This is a sophisticated Telegram chatbot written in Go that integrates with multiple AI providers (OpenAI, Anthropic, AWS Nova, Gemini) and provides advanced features including voice processing, image generation and web search capabilities.

### Core Components

**Multi-Provider AI Integration**
- The application abstracts AI providers through a unified interface in `llm.go`
- Provider selection is based on model configuration in `config.json`
- Supports both streaming and non-streaming responses
- Implements OpenAI Responses API for advanced features

**Message Processing Pipeline**
1. **Entry Point**: `bot.go` - Handles Telegram webhook/polling and routes messages
2. **Command Processing**: Commands are handled through telebot handlers with middleware for authentication
3. **AI Processing**: `llm.go` - Manages conversation context, calls appropriate AI provider, handles streaming
4. **Response Delivery**: Formats and sends responses back to Telegram with proper markdown handling

**Function/Tool Calling System**
- Implemented in `function_calls.go`
- Supports multiple providers (OpenAI, Anthropic, AWS Nova)
- Built-in tools: web search, reminders, image generation
- Tool results are processed and conversation continues automatically

**Voice Processing Pipeline**
- `voice.go` handles the complete audio pipeline
- Opus → WAV → MP3 conversion using platform-specific libraries
- Transcription via OpenAI Whisper
- TTS response generation using OpenAI or Piper

**Database Layer**
- SQLite with GORM ORM (`models.go`, `db.go`)
- Stores users, chats, messages, and roles
- Automatic conversation summarization for context management
- Session state management for multi-step interactions

### Key Integration Points

**Telegram Bot API** (`bot.go`, `tele_handlers.go`)
- Uses gopkg.in/telebot.v3 framework
- Middleware for user authentication and rate limiting
- Interactive menus and inline keyboards
- File handling for documents, images, and voice messages

**AI Provider Clients**
- OpenAI: `github.com/meinside/openai-go`
- Anthropic: `github.com/tectiv3/anthropic-go`
- AWS Nova: `github.com/tectiv3/awsnova-go`
- All clients are initialized in `main.go` and stored in the Server struct

## Configuration

The application uses `config.json` for all configuration. Key fields:
- `telegram_bot_token`: Bot authentication
- API keys for each AI provider (`openai_api_key`, `anthropic_api_key`, etc.)
- `models`: Array of model configurations with provider mapping
- `allowed_telegram_users`: User whitelist for access control
- Feature flags for optional capabilities

## Important Patterns

### Error Handling
- All AI provider calls include timeout contexts
- Streaming responses handle partial failures gracefully
- Database operations use transactions where appropriate

### Message Context Management
- Conversation history is maintained per chat
- Automatic summarization when context exceeds limits
- System prompts and roles are injected based on user configuration

### State Management
- `state.go` implements a state machine for multi-step interactions
- States are stored in database and expire after timeout
- Used for complex workflows like role creation and user management