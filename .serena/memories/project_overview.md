# ChatGPT Bot Project Overview

## Purpose
A sophisticated Telegram chatbot that integrates with multiple AI providers (OpenAI, Anthropic, AWS Nova, Gemini) to provide conversational AI capabilities through Telegram.

## Key Features
- Multi-provider AI integration with unified interface
- Voice processing (transcription and TTS)
- Image generation and analysis
- Web search capabilities
- Vector database support (ChromaDB)
- Document processing
- Function/tool calling system
- User authentication and rate limiting
- Conversation context management with automatic summarization

## Tech Stack
- **Language**: Go 1.24.3
- **Database**: SQLite with GORM ORM
- **Telegram Integration**: gopkg.in/telebot.v3
- **AI Providers**:
  - OpenAI (github.com/meinside/openai-go)
  - Anthropic (github.com/tectiv3/anthropic-go)
  - AWS Nova (github.com/tectiv3/awsnova-go)
  - Google Gemini (via OpenAI client)
- **Vector DB**: ChromaDB (github.com/amikos-tech/chroma-go)
- **Audio Processing**: libmp3lame, libopusfile

## Project Structure
- `main.go` - Entry point and initialization
- `bot.go` - Telegram bot handling and message routing
- `llm.go` - AI provider abstraction and management
- `function_calls.go` - Tool/function calling implementation
- `voice.go` - Audio processing pipeline
- `models.go` - Database models and Server struct
- `db.go` - Database operations
- `state.go` - State machine for multi-step interactions
- `tele_handlers.go` - Telegram command handlers
- `vectordb/` - Vector database integration
- `i18n/` - Internationalization support
- `config.json` - Configuration file (created from config.json.sample)