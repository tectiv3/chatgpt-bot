# ChatGPT Bot Architecture

## System Overview
This is a single Go application that runs both:
1. **Telegram Bot** - Handles Telegram messaging via webhook/polling
2. **Web Server** - Serves a webapp interface on port 8989

Both components share:
- Same database (SQLite with GORM)
- Same AI provider clients (OpenAI, Anthropic, AWS Nova, Gemini)
- Same configuration file (config.json)
- Same Server struct instance

## Key Components

### Entry Points
- `main.go` - Initializes both bot and web server in same process
- `bot.go` - Telegram bot handlers and webhook setup
- `webapp.go` - HTTP handlers for web interface

### Shared Core
- `llm.go` - AI provider abstraction and response handling
- `models.go` - Database models (shared between bot and webapp)
- `db.go` - Database operations
- `function_calls.go` - Tool calling system

### Current Issue: Webapp Annotations
- **Problem**: In webapp interface, annotations (like charts) appear only after reload, not during streaming
- **Root Cause**: Annotations are processed and stored after streaming completes, but webapp streaming doesn't send annotation updates during the stream
- **Location**: webapp.go around line 1734 has code to send annotations during streaming, but it may not be working correctly

### Webapp Streaming Flow
1. Client sends request to `/api/chat`
2. Server streams responses using SSE (Server-Sent Events)
3. Annotations are detected and stored during streaming
4. Should send annotation updates immediately via SSE
5. Currently only works after full response completion