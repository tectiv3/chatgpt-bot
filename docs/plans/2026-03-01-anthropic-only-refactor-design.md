# Anthropic-Only Refactoring Design

## Goal

Remove all non-Anthropic AI providers (OpenAI, AWS Nova, Gemini) from the codebase. Clean up duplicate code, strip obsolete functionality, and simplify the architecture around a single provider using `github.com/tectiv3/anthropic-go`.

## Scope

### Keep
- Streaming text responses (Anthropic)
- Web search (Anthropic built-in WebSearchTool)
- File/image uploads for Claude analysis (vision + documents)
- `make_summary` custom tool (refactored to use Anthropic)
- Voice transcription (local whisper via configurable HTTP endpoint)
- Webapp (refactored for Anthropic)
- State machine for multi-step interactions
- Database layer, auth, rate limiting

### Remove
- OpenAI provider (all API calls, Responses API, chat completions)
- AWS Nova provider
- Gemini provider (OpenAI-compatible wrapper)
- Ollama / Groq (already dead code)
- DALL-E image generation
- All TTS (OpenAI TTS + Piper)
- OpenAI Whisper transcription (replaced by local STT)
- Code interpreter tool
- MCP server support
- `generate_image`, `text_to_speech`, `web_to_speech` tools

## Approach

Clean rewrite of core files (llm.go, chat.go, function_calls.go), adapt peripheral files (bot.go, webapp.go, models.go, main.go, voice.go, image.go).

## Design

### 1. Data Model Changes

**`config` struct** - Remove all provider fields except Anthropic:
```go
type config struct {
    TelegramBotToken  string   `json:"telegram_bot_token"`
    TelegramServerURL string   `json:"telegram_server_url"`
    Models            []AiModel `json:"models"`
    AnthropicAPIKey   string   `json:"anthropic_api_key"`
    AllowedTelegramUsers []string `json:"allowed_telegram_users"`
    Verbose           bool     `json:"verbose,omitempty"`
    WhisperEndpoint   string   `json:"whisper_endpoint"` // e.g. http://localhost:8765/transcribe
    MiniAppEnabled    bool     `json:"mini_app_enabled"`
    WebServerPort     string   `json:"web_server_port"`
    MiniAppURL        string   `json:"mini_app_url"`
}
```

**`AiModel` struct** - Remove Provider (always Anthropic):
```go
type AiModel struct {
    ModelID   string `json:"model_id"`
    Name      string `json:"name"`
    WebSearch bool   `json:"web_search,omitempty"`
    Reasoning bool   `json:"reasoning,omitempty"`
}
```

**`Server` struct** - Single AI client:
```go
type Server struct {
    sync.RWMutex
    conf              config
    users             []string
    ai                *anthropic.Client
    bot               *tele.Bot
    db                *gorm.DB
    webServer         *http.Server
    rateLimiter       *RateLimiter
    connectionManager *ConnectionManager
}
```

**`ChatMessage`** - Remove OpenAI type dependencies:
- `Role` field: `openai.ChatMessageRole` -> plain `string`
- `Annotations` field: `[]AnnotationData` (embeds `openai.Annotation`) -> `Citations` field with local struct
- `ToolCalls` field: `ToolCall` (uses `openai.ToolCallFunction`) -> local struct

**New Citations type:**
```go
type Citation struct {
    URL       string `json:"url"`
    Title     string `json:"title"`
    CitedText string `json:"cited_text,omitempty"`
}
type Citations []Citation
```

### 2. Core Rewrite

**`llm.go`** (~1278 -> ~400 lines):
- Single streaming path via `s.ai.Stream()`
- Single non-streaming path via `s.ai.Generate()` for internal use (summaries, titles)
- `complete()` calls Anthropic directly, no provider routing
- Citation extraction from `TextContent.Citations` during streaming
- Remove: `getResponseStream()`, `getResponse()`, `getStreamAnswer()`, `getAnswer()`, `getNovaAnswer()`, `ProcessAnnotations()`, `processFileCitation()`

**`chat.go`** (~355 -> ~120 lines):
- Single `getDialog()` producing `anthropic.Messages`
- Remove: `getAnthropicDialog()`, `getNovaDialog()` (merge their logic into single path)
- One code path for history filtering, image loading, document attachment

**`function_calls.go`** (~377 -> ~150 lines):
- Keep: `make_summary` tool (rewrite to use Anthropic for summarization)
- Web search: handled by Anthropic's built-in `WebSearchTool`, not through function calling
- Tool call loop: when Anthropic returns `ToolUseContent`, execute it, continue with `ToolResultContent`
- Remove: `generate_image`, `text_to_speech`, `web_to_speech` tools
- Remove: OpenAI Responses API tool handling (`handleResponseFunctionCalls`)

### 3. Voice Pipeline

**Current:** Opus -> WAV -> MP3 -> OpenAI Whisper -> text -> response -> TTS
**New:** Opus -> WAV -> HTTP POST to whisper_endpoint -> text -> response (text only)

- Keep opus->WAV conversion
- Remove MP3 encoding (go-lame dependency)
- Remove all TTS code
- Call configurable `whisper_endpoint` via HTTP POST with WAV data
- Add `WhisperEndpoint` to config

### 4. Webapp Changes

- Replace `s.openAI.CreateChatCompletionWithContext()` calls with `s.ai.Generate()`
- Replace OpenAI Responses API streaming with Anthropic streaming
- Replace annotation rendering with citation rendering
- Remove provider availability checks
- Keep: SSE streaming, thread management, roles, mini app, rate limiting

### 5. Other File Changes

**`image.go`**: Keep `handleImage()` (inbound), remove `textToImage()` (DALL-E)
**`bot.go`**: Remove provider routing, dead tool menus, simplify model selection
**`main.go`**: Remove OpenAI/Nova/Gemini client initialization
**`go.mod`**: Remove `openai-go`, `awsnova-go`, `go-lame` dependencies

### 6. Dependencies

**Remove:**
- `github.com/meinside/openai-go` (and its replace directive for `tectiv3/openai-go`)
- `github.com/tectiv3/awsnova-go`
- `github.com/tectiv3/go-lame`

**Keep:**
- `github.com/tectiv3/anthropic-go` (sole AI provider)
- `gopkg.in/telebot.v3` (Telegram)
- `gorm.io/*` (database)
- All other existing deps

## SDK Decision

Use `github.com/tectiv3/anthropic-go` (the existing wrapper), NOT the official `anthropics/anthropic-sdk-go`. Rationale:
- Already integrated and tested in the codebase
- Supports all needed features: streaming, tool use, web search, images, documents, citations, thinking blocks
- Cleaner API for this use case vs the Stainless-generated official SDK
- You control the wrapper and can update it as needed

## Migration Notes

- Database schema change: `ChatMessage.Annotations` column becomes `ChatMessage.Citations`. Requires migration.
- `ChatMessage.Role` type change: `openai.ChatMessageRole` -> `string`. Values are identical ("user", "assistant", "system") so data is compatible.
- `config.json` format changes: old provider keys become unused. New fields: `whisper_endpoint`.
