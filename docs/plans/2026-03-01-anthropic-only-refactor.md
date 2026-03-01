# Anthropic-Only Refactoring Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Strip all non-Anthropic AI providers, remove dead features (TTS, DALL-E, code interpreter), unify duplicate code, and simplify the architecture around `github.com/tectiv3/anthropic-go`.

**Architecture:** Single AI provider (`anthropic.Client` as `s.ai`), single dialog converter producing `anthropic.Messages`, single streaming path via `s.ai.Stream()`, local whisper HTTP endpoint for voice transcription. Bot and webapp both use the same unified core.

**Tech Stack:** Go, `github.com/tectiv3/anthropic-go`, `gopkg.in/telebot.v3`, GORM/SQLite, configurable whisper HTTP endpoint for STT.

---

### Task 1: Create feature branch and update data model types

**Files:**
- Modify: `models.go`
- Modify: `go.mod`

**Step 1: Create the feature branch**

```bash
git checkout -b refactor/anthropic-only
```

**Step 2: Replace OpenAI type dependencies in models.go**

Replace the `ChatMessage.Role` type from `openai.ChatMessageRole` to `string`:

```go
// In ChatMessage struct (models.go:233)
Role       string  `json:"role"`
```

Replace `AnnotationData` and `Annotations` with `Citation`/`Citations`:

```go
type Citation struct {
    URL       string `json:"url"`
    Title     string `json:"title"`
    CitedText string `json:"cited_text,omitempty"`
}

type Citations []Citation

// Value implements driver.Valuer for database storage
func (c Citations) Value() (driver.Value, error) {
    if c == nil {
        return nil, nil
    }
    return json.Marshal(c)
}

// Scan implements sql.Scanner for database retrieval
func (c *Citations) Scan(value interface{}) error {
    if value == nil {
        *c = nil
        return nil
    }
    b, ok := value.([]byte)
    if !ok {
        return fmt.Errorf("type assertion to []byte failed")
    }
    return json.Unmarshal(b, &c)
}
```

Update `ChatMessage` to use `Citations` instead of `Annotations`:

```go
// Replace:   Annotations Annotations `json:"annotations,omitempty" gorm:"type:json"`
// With:
Citations   Citations   `json:"citations,omitempty" gorm:"type:json"`
```

Replace `ToolCall` to use local types instead of `openai.ToolCallFunction`:

```go
type ToolCallFunction struct {
    Name      string `json:"name"`
    Arguments string `json:"arguments"`
}

type ToolCall struct {
    ID       string           `json:"id"`
    Type     string           `json:"type"`
    Function ToolCallFunction `json:"function"`
}
```

Remove `AnnotationData`, `AnnotationProcessor`, `GPTResponse` interface (unused after refactor), `wavWriter`/`wavHeader` (move to voice.go if still needed).

**Step 3: Simplify config struct**

```go
type config struct {
    TelegramBotToken     string    `json:"telegram_bot_token"`
    TelegramServerURL    string    `json:"telegram_server_url"`
    Models               []AiModel `json:"models"`
    AnthropicAPIKey      string    `json:"anthropic_api_key"`
    AllowedTelegramUsers []string  `json:"allowed_telegram_users"`
    Verbose              bool      `json:"verbose,omitempty"`
    WhisperEndpoint      string    `json:"whisper_endpoint"`
    MiniAppEnabled       bool      `json:"mini_app_enabled"`
    WebServerPort        string    `json:"web_server_port"`
    MiniAppURL           string    `json:"mini_app_url"`
}
```

**Step 4: Simplify AiModel struct**

```go
type AiModel struct {
    ModelID   string `json:"model_id"`
    Name      string `json:"name"`
    WebSearch bool   `json:"web_search,omitempty"`
    Reasoning bool   `json:"reasoning,omitempty"`
}
```

**Step 5: Simplify Server struct**

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

Remove imports for `openai`, `awsnova`. Remove `RestrictConfig` only if unused (check `whitelist()` in `bot.go` first — it IS used, keep it). Keep `RateLimiter`, `ConnectionManager`, `User`, `Chat`, `Role` as-is. Remove `GPTResponse` interface. Keep `wavWriter`/`wavHeader` in `voice.go`.

**Step 6: Update getModel in db.go:109-124**

```go
func (s *Server) getModel(model string) *AiModel {
    for _, m := range s.conf.Models {
        if m.Name == model || m.ModelID == model {
            return &m
        }
    }
    // Default to first configured model
    if len(s.conf.Models) > 0 {
        return &s.conf.Models[0]
    }
    return &AiModel{ModelID: model, Name: model}
}
```

**Step 7: Commit**

```bash
git add models.go db.go
git commit -m "refactor: replace OpenAI types with local types in data model

- ChatMessage.Role: openai.ChatMessageRole -> string
- Annotations -> Citations (for Anthropic web search)
- ToolCall.Function: openai.ToolCallFunction -> local ToolCallFunction
- Simplify config, AiModel, Server structs for single provider
- Remove Provider field from AiModel (always Anthropic)"
```

---

### Task 2: Rewrite chat.go — single dialog converter

**Files:**
- Rewrite: `chat.go`

**Step 1: Rewrite chat.go**

Remove all three dialog converters (`getDialog`, `getAnthropicDialog`, `getNovaDialog`) and all `openai.NewChat*` usage. Replace with single Anthropic-native dialog converter and local helper methods.

```go
package main

import (
    "io"
    "net/http"
    "os"
    "strings"
    "time"

    "github.com/tectiv3/anthropic-go"
    "github.com/tectiv3/chatgpt-bot/i18n"
    tele "gopkg.in/telebot.v3"
)

func (c *Chat) getSentMessage(context tele.Context) *tele.Message {
    // Keep existing implementation unchanged (lines 18-37)
    c.mutex.Lock()
    defer c.mutex.Unlock()
    if c.MessageID != nil {
        id, _ := strconv.Atoi(*c.MessageID)
        return &tele.Message{ID: id, Chat: &tele.Chat{ID: c.ChatID}}
    }
    if context.Get("reply") != nil {
        sentMessage := context.Get("reply").(tele.Message)
        c.MessageID = &([]string{strconv.Itoa(sentMessage.ID)}[0])
        return &sentMessage
    }
    msgPointer, _ := context.Bot().Reply(context.Message(), "...")
    c.MessageID = &([]string{strconv.Itoa(msgPointer.ID)}[0])
    return msgPointer
}

func (c *Chat) addToolResultToDialog(id, content string) {
    c.mutex.Lock()
    defer c.mutex.Unlock()
    role := "user"
    c.History = append(c.History, ChatMessage{
        Role:       role,
        Content:    &content,
        ChatID:     c.ChatID,
        ToolCallID: &id,
        CreatedAt:  time.Now(),
    })
}

func (c *Chat) addImageToDialog(text, path string) {
    c.mutex.Lock()
    defer c.mutex.Unlock()
    role := "user"
    c.History = append(c.History, ChatMessage{
        Role:      role,
        Content:   &text,
        ImagePath: &path,
        ChatID:    c.ChatID,
        CreatedAt: time.Now(),
    })
}

func (c *Chat) addFileToDialog(text, path, filename string) {
    c.mutex.Lock()
    defer c.mutex.Unlock()
    role := "user"
    c.History = append(c.History, ChatMessage{
        Role:      role,
        Content:   &text,
        ImagePath: &path,
        Filename:  &filename,
        ChatID:    c.ChatID,
        CreatedAt: time.Now(),
    })
}

func (c *Chat) addUserMessage(text string) {
    c.mutex.Lock()
    defer c.mutex.Unlock()
    role := "user"
    c.History = append(c.History, ChatMessage{
        Role:      role,
        Content:   &text,
        ChatID:    c.ChatID,
        CreatedAt: time.Now(),
    })
}

func (c *Chat) addAssistantMessage(text string) {
    c.mutex.Lock()
    defer c.mutex.Unlock()
    role := "assistant"
    c.History = append(c.History, ChatMessage{
        Role:      role,
        Content:   &text,
        ChatID:    c.ChatID,
        CreatedAt: time.Now(),
    })
}

// addMessageToDialog adds a raw ChatMessage. Used for tool call results.
func (c *Chat) addMessageToDialog(msg ChatMessage) {
    c.mutex.Lock()
    defer c.mutex.Unlock()
    msg.ChatID = c.ChatID
    msg.CreatedAt = time.Now()
    c.History = append(c.History, msg)
}

// getDialog builds Anthropic message history from chat history
func (c *Chat) getDialog(request *string) []*anthropic.Message {
    if request != nil {
        c.addUserMessage(*request)
    }

    var history []*anthropic.Message
    for _, h := range c.History {
        if h.CreatedAt.Before(time.Now().AddDate(0, 0, -int(c.ConversationAge))) {
            continue
        }
        if h.Content == nil || *h.Content == "" {
            continue
        }

        role := anthropic.Role(h.Role)

        // Tool result messages become user messages with tool_result content
        if h.ToolCallID != nil {
            history = append(history, anthropic.NewToolResultMessage(
                &anthropic.ToolResultContent{
                    ToolUseID: *h.ToolCallID,
                    Content:   *h.Content,
                },
            ))
            continue
        }

        var content []anthropic.Content

        if h.Filename != nil && h.ImagePath != nil {
            // File attachment (PDF, etc.)
            fileData, err := os.ReadFile(*h.ImagePath)
            if err != nil {
                Log.Warn("Error reading file", "error=", err)
                continue
            }
            content = append(content, anthropic.NewTextContent(*h.Content))
            content = append(content, &anthropic.DocumentContent{
                Source: anthropic.RawData(http.DetectContentType(fileData), fileData),
            })
        } else if h.ImagePath != nil {
            // Image attachment
            imageData, err := os.ReadFile(*h.ImagePath)
            if err != nil {
                Log.Warn("Error reading image", "error=", err)
                continue
            }
            content = append(content, anthropic.NewTextContent(*h.Content))
            content = append(content, &anthropic.ImageContent{
                Source: anthropic.RawData(http.DetectContentType(imageData), imageData),
            })
        } else {
            content = append(content, anthropic.NewTextContent(*h.Content))
        }

        // Handle tool calls in assistant messages
        if role == "assistant" && len(h.ToolCalls) > 0 {
            for _, tc := range h.ToolCalls {
                content = append(content, &anthropic.ToolUseContent{
                    ID:    tc.ID,
                    Name:  tc.Function.Name,
                    Input: []byte(tc.Function.Arguments),
                })
            }
        }

        history = append(history, anthropic.NewMessage(role, content))
    }

    return history
}

func (c *Chat) t(key string, replacements ...*i18n.Replacements) string {
    return l.GetWithLocale(c.Lang, key, replacements...)
}

// Keep all remaining utility methods unchanged:
// updateTotalTokens, setMessageID, getMessageID, removeMenu,
// GetEnabledToolsArray, SetEnabledToolsFromArray
```

**Step 2: Verify it compiles (won't fully compile yet — other files still reference old types)**

```bash
# Just check chat.go syntax
go vet ./... 2>&1 | head -20
```

Expected: Errors from other files referencing removed types. chat.go itself should be clean.

**Step 3: Commit**

```bash
git add chat.go
git commit -m "refactor: unify 3 dialog converters into single Anthropic-native path

- Remove getDialog (OpenAI), getAnthropicDialog, getNovaDialog
- Single getDialog() returns []*anthropic.Message
- addMessageToDialog takes ChatMessage directly (no OpenAI types)
- Add addUserMessage/addAssistantMessage helpers"
```

---

### Task 3: Rewrite llm.go — single provider streaming

**Files:**
- Rewrite: `llm.go`

**Step 1: Rewrite llm.go**

This is the largest change. Remove all 6 response methods and replace with 2:
- `getStreamingAnswer()` — Anthropic streaming for interactive use
- `generateSimple()` — Anthropic non-streaming for internal use (summaries, titles)

The key structure:

```go
package main

import (
    "context"
    "fmt"
    "strconv"
    "strings"
    "time"

    "github.com/tectiv3/anthropic-go"
    tele "gopkg.in/telebot.v3"
)

func userAgent(userID int64) string {
    return fmt.Sprintf("telegram-chatgpt-bot:%d", userID)
}

// complete is the main entry point for generating AI responses
func (s *Server) complete(c tele.Context, message string, reply bool) {
    chat := s.getChat(c.Chat(), c.Sender())

    text := "..."
    sentMessage := c.Message()
    var err error

    if !reply {
        text = fmt.Sprintf(chat.t("_Transcript:_\n\n%s\n\n_Answer:_ \n\n"), message)
        sentMessage, err = c.Bot().Send(c.Recipient(), text, "text", &tele.SendOptions{
            ReplyTo:   c.Message(),
            ParseMode: tele.ModeMarkdown,
        })
        if err != nil {
            Log.WithField("user", c.Sender().Username).Error(err)
            sentMessage, _ = c.Bot().Send(c.Recipient(), err.Error())
        }
        chat.MessageID = &([]string{strconv.Itoa(sentMessage.ID)}[0])
        c.Set("reply", *sentMessage)
    }

    msgPtr := &message
    if len(message) == 0 {
        msgPtr = nil
    }

    s.getStreamingAnswer(chat, c, msgPtr)
}

// getStreamingAnswer handles streaming responses from Anthropic
func (s *Server) getStreamingAnswer(chat *Chat, c tele.Context, question *string) {
    model := s.getModel(chat.ModelName)
    maxTokens := 4096

    system := chat.MasterPrompt
    if chat.RoleID != nil {
        system = chat.Role.Prompt
    }

    chat.removeMenu(c)
    sentMessage := chat.getSentMessage(c)
    ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
    defer cancel()

    s.ai.Apply(anthropic.WithModel(model.ModelID))
    s.ai.Apply(anthropic.WithSystemPrompt(system))
    s.ai.Apply(anthropic.WithMaxTokens(maxTokens))

    if !model.Reasoning {
        s.ai.Apply(anthropic.WithTemperature(chat.Temperature))
    }

    // Add tools based on model config and chat settings
    var tools []anthropic.ToolInterface
    if model.WebSearch {
        tools = append(tools, anthropic.NewWebSearchTool(anthropic.WebSearchToolOptions{MaxUses: 5}))
    }
    // Add custom tools (make_summary)
    tools = append(tools, s.getTools()...)

    if len(tools) > 0 {
        s.ai.Apply(anthropic.WithTools(tools...))
    }

    dialog := chat.getDialog(question)
    stream, err := s.ai.Stream(ctx, dialog)
    if err != nil {
        Log.WithField("user", c.Sender().Username).Error(err)
        _, _ = c.Bot().Edit(sentMessage, err.Error())
        return
    }
    defer stream.Close()

    var result strings.Builder
    _ = c.Notify(tele.Typing)
    tokens := 0
    accumulator := anthropic.NewResponseAccumulator()
    var citations []Citation

    for stream.Next() {
        select {
        case <-ctx.Done():
            _, _ = c.Bot().Edit(sentMessage, "Timeout")
            return
        default:
        }
        event := stream.Event()
        accumulator.AddEvent(event)

        switch event.Type {
        case anthropic.EventTypeContentBlockStart:
            if event.ContentBlock != nil {
                if event.ContentBlock.Type == anthropic.ContentTypeServerToolUse &&
                    event.ContentBlock.Name == "web_search" {
                    _, _ = c.Bot().Edit(sentMessage, chat.t("Web search started, please wait..."))
                }
            }
        case anthropic.EventTypeContentBlockDelta:
            if event.Delta == nil {
                continue
            }
            if event.Delta.Type == anthropic.EventDeltaTypeText {
                result.WriteString(event.Delta.Text)
                tokens++
                if tokens%10 == 0 {
                    _, _ = c.Bot().Edit(sentMessage, result.String())
                }
            }
        case anthropic.EventTypeMessageStop:
            Log.WithField("user", c.Sender().Username).
                WithField("tokens", tokens).Info("Response stream finished")
        }
    }

    if err := stream.Err(); err != nil {
        if ctx.Err() == context.DeadlineExceeded {
            _, _ = c.Bot().Edit(sentMessage, "Timeout. Partial: "+result.String())
        } else {
            _, _ = c.Bot().Edit(sentMessage, "Error: "+err.Error())
        }
        return
    }

    if !accumulator.IsComplete() {
        return
    }

    // Extract citations from accumulated response
    response := accumulator.Response()
    for _, content := range response.Content {
        if tc, ok := content.(*anthropic.TextContent); ok {
            for _, cit := range tc.Citations {
                citations = append(citations, extractCitation(cit))
            }
        }
    }

    // Check for tool use in response and handle tool calls
    var toolUses []anthropic.Content
    for _, content := range response.Content {
        if content.Type() == anthropic.ContentTypeToolUse {
            toolUses = append(toolUses, content)
        }
    }

    if len(toolUses) > 0 {
        s.handleAnthropicToolCalls(chat, c, response, toolUses)
        return
    }

    // Finalize response
    usage := accumulator.Usage()
    reply := result.String()
    s.updateReply(chat, reply, c)

    totalTokens := usage.InputTokens + usage.OutputTokens
    if totalTokens > 0 {
        chat.updateTotalTokens(totalTokens)
    }

    if reply != "" {
        chat.addAssistantMessage(reply)
        // Store citations if any
        if len(citations) > 0 {
            s.storeCitations(chat, citations)
        }
        s.saveHistory(chat)
    }
}

// extractCitation converts an Anthropic Citation to our local Citation type
func extractCitation(cit anthropic.Citation) Citation {
    // The Citation interface varies by type; extract URL-based ones
    // This will need adaptation based on actual citation interface
    return Citation{}
}

// generateSimple performs a non-streaming Anthropic call for internal use
// (summaries, title generation, etc.)
func (s *Server) generateSimple(system, prompt, model string) (string, error) {
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    client := anthropic.New(
        anthropic.WithAPIKey(s.conf.AnthropicAPIKey),
        anthropic.WithModel(model),
        anthropic.WithSystemPrompt(system),
        anthropic.WithMaxTokens(1024),
    )

    messages := []*anthropic.Message{
        anthropic.NewUserTextMessage(prompt),
    }

    response, err := client.Generate(ctx, messages)
    if err != nil {
        return "", err
    }

    var result string
    for _, content := range response.Content {
        if tc, ok := content.(*anthropic.TextContent); ok {
            result += tc.Text
        }
    }

    return result, nil
}

// simpleAnswer answers a one-off question using the chat's current model
func (s *Server) simpleAnswer(c tele.Context, request string) (string, error) {
    _ = c.Notify(tele.Typing)
    chat := s.getChat(c.Chat(), c.Sender())

    prompt := chat.MasterPrompt
    if chat.RoleID != nil {
        prompt = chat.Role.Prompt
    }

    model := s.getModel(chat.ModelName)
    return s.generateSimple(prompt, request, model.ModelID)
}

// anonymousAnswer answers without chat context
func (s *Server) anonymousAnswer(c tele.Context, request string) (string, error) {
    _ = c.Notify(tele.Typing)
    model := s.conf.Models[0] // Use first configured model
    return s.generateSimple(masterPrompt, request, model.ModelID)
}

// summarize summarizes chat history
func (s *Server) summarize(chatHistory []ChatMessage) (string, error) {
    var historyText strings.Builder
    for _, h := range chatHistory {
        if h.Role == "tool" {
            continue
        }
        if h.Content != nil {
            historyText.WriteString(fmt.Sprintf("%s: %s\n", h.Role, *h.Content))
        }
    }

    prompt := historyText.String() + "\n\nMake a compressed summary of the conversation. Be brief, highlight key points. Use same language as the user."
    model := s.conf.Models[0].ModelID
    return s.generateSimple("Be as brief as possible", prompt, model)
}

// storeCitations saves citations to the last assistant message
func (s *Server) storeCitations(chat *Chat, citations []Citation) {
    var lastMsg *ChatMessage
    for i := len(chat.History) - 1; i >= 0; i-- {
        if chat.History[i].Role == "assistant" {
            lastMsg = &chat.History[i]
            break
        }
    }
    if lastMsg != nil {
        lastMsg.Citations = Citations(citations)
        s.db.Save(lastMsg)
    }
}

// processPDF handles uploaded PDF files
func (s *Server) processPDF(c tele.Context) {
    // Keep existing implementation (saves file, adds to dialog, calls complete)
    pdf := c.Message().Document.File
    var fileName string

    if s.conf.TelegramServerURL != "" {
        f, err := c.Bot().FileByID(pdf.FileID)
        if err != nil {
            Log.Warn("Error getting file ID", "error=", err)
            return
        }
        fileName = f.FilePath
    } else {
        out, err := os.Create("uploads/" + pdf.FileID + ".pdf")
        if err != nil {
            Log.Warn("Error creating file", "error=", err)
            return
        }
        if err := c.Bot().Download(&pdf, out.Name()); err != nil {
            Log.Warn("Error getting file content", "error=", err)
            return
        }
        fileName = out.Name()
    }

    chat := s.getChat(c.Chat(), c.Sender())
    chat.addFileToDialog(c.Message().Caption, fileName, c.Message().Document.FileName)
    s.db.Save(&chat)

    s.complete(c, "", true)
}

// Keep: updateReply, saveHistory, summariseHistory
// (these are called from getStreamingAnswer and don't depend on OpenAI types)
```

Note: `updateReply` and `saveHistory` from the existing llm.go should be preserved. They handle Telegram message editing and database persistence — review them and adapt any OpenAI type references.

**Step 2: Commit**

```bash
git add llm.go
git commit -m "refactor: rewrite llm.go for Anthropic-only streaming

- Remove 6 provider-specific methods (getResponseStream, getResponse,
  getStreamAnswer, getAnswer, getAnthropicAnswer, getNovaAnswer)
- Single getStreamingAnswer() using anthropic.Stream()
- Single generateSimple() for internal non-streaming calls
- Simplified complete() with no provider routing
- summarize() uses Anthropic instead of OpenAI gpt-4o-mini
- Remove all ProcessAnnotations/processFileCitation code"
```

---

### Task 4: Rewrite function_calls.go — single tool

**Files:**
- Rewrite: `function_calls.go`

**Step 1: Rewrite function_calls.go**

Strip all OpenAI types. Keep only `make_summary` tool. Handle Anthropic tool use/result flow.

```go
package main

import (
    "encoding/json"
    "fmt"
    "runtime/debug"
    "time"

    "github.com/go-shiori/go-readability"
    "github.com/tectiv3/anthropic-go"
    "github.com/tectiv3/chatgpt-bot/i18n"
    tele "gopkg.in/telebot.v3"
)

// ToolCallNotifier interface for platform-specific notifications
type ToolCallNotifier interface {
    OnFunctionCall(functionName string, arguments string)
    OnFunctionResult(functionName string, result string)
    SendMessage(message string) error
}

type TelegramToolCallNotifier struct {
    chat *Chat
    c    tele.Context
    bot  *tele.Bot
}

func (t *TelegramToolCallNotifier) OnFunctionCall(functionName string, arguments string) {
    sentMessage := t.chat.getSentMessage(t.c)
    message := fmt.Sprintf(t.chat.t("Action: {{.tool}}\nAction input: %s",
        &i18n.Replacements{"tool": t.chat.t(functionName)}), arguments)
    _, _ = t.bot.Edit(sentMessage, message)
}

func (t *TelegramToolCallNotifier) OnFunctionResult(functionName string, result string) {}

func (t *TelegramToolCallNotifier) SendMessage(message string) error {
    _, err := t.bot.Send(t.c.Recipient(), message)
    return err
}

type WebappToolCallNotifier struct{}

func (w *WebappToolCallNotifier) OnFunctionCall(functionName string, arguments string) {}
func (w *WebappToolCallNotifier) OnFunctionResult(functionName string, result string)  {}
func (w *WebappToolCallNotifier) SendMessage(message string) error                     { return nil }

// MakeSummaryTool implements anthropic.ToolInterface
type MakeSummaryTool struct{}

func (t *MakeSummaryTool) Name() string        { return "make_summary" }
func (t *MakeSummaryTool) Description() string {
    return "Make a summary of a web page from an explicit summarization request."
}
func (t *MakeSummaryTool) Schema() *anthropic.Schema {
    return &anthropic.Schema{
        Type: "object",
        Properties: map[string]*anthropic.Property{
            "url": {Type: "string", Description: "A valid URL to a web page"},
        },
        Required: []string{"url"},
    }
}

// getTools returns the custom tools available for Anthropic
func (s *Server) getTools() []anthropic.ToolInterface {
    return []anthropic.ToolInterface{&MakeSummaryTool{}}
}

// handleAnthropicToolCalls processes tool_use content blocks from Anthropic response
func (s *Server) handleAnthropicToolCalls(
    chat *Chat, c tele.Context,
    response *anthropic.Response, toolUses []anthropic.Content,
) {
    notifier := &TelegramToolCallNotifier{chat: chat, c: c, bot: s.bot}

    // Add assistant message with tool use to history
    var assistantContent []anthropic.Content
    for _, content := range response.Content {
        assistantContent = append(assistantContent, content)
    }
    // Store the assistant's response (including tool_use blocks) in history
    var textParts []string
    for _, content := range response.Content {
        if tc, ok := content.(*anthropic.TextContent); ok {
            textParts = append(textParts, tc.Text)
        }
    }

    // Build tool call records for history
    var toolCalls []ToolCall
    for _, tu := range toolUses {
        if tuc, ok := tu.(*anthropic.ToolUseContent); ok {
            toolCalls = append(toolCalls, ToolCall{
                ID:   tuc.ID,
                Type: "function",
                Function: ToolCallFunction{
                    Name:      tuc.Name,
                    Arguments: string(tuc.Input),
                },
            })
        }
    }

    assistantText := strings.Join(textParts, "")
    chat.addMessageToDialog(ChatMessage{
        Role:      "assistant",
        Content:   &assistantText,
        ToolCalls: toolCalls,
    })

    // Execute each tool and collect results
    var toolResults []*anthropic.ToolResultContent
    for _, tu := range toolUses {
        tuc, ok := tu.(*anthropic.ToolUseContent)
        if !ok {
            continue
        }

        result, err := s.executeToolCall(tuc, notifier)
        if err != nil {
            result = fmt.Sprintf("Error: %v", err)
        }

        toolResults = append(toolResults, &anthropic.ToolResultContent{
            ToolUseID: tuc.ID,
            Content:   result,
        })

        // Add tool result to chat history
        chat.addToolResultToDialog(tuc.ID, result)
    }

    // Save history so far
    s.saveHistory(chat)

    // Continue the conversation with tool results
    s.getStreamingAnswer(chat, c, nil)
}

// executeToolCall executes a single tool
func (s *Server) executeToolCall(
    toolUse *anthropic.ToolUseContent, notifier ToolCallNotifier,
) (string, error) {
    if notifier != nil {
        notifier.OnFunctionCall(toolUse.Name, string(toolUse.Input))
    }

    switch toolUse.Name {
    case "make_summary":
        type parsed struct {
            URL string `json:"url"`
        }
        var args parsed
        if err := json.Unmarshal(toolUse.Input, &args); err != nil {
            return "", fmt.Errorf("failed to parse arguments: %w", err)
        }
        Log.Info("Making summary for URL: ", args.URL)
        return s.getPageSummary(args.URL)

    default:
        return "", fmt.Errorf("unknown function: %s", toolUse.Name)
    }
}

// getPageSummary fetches and summarizes a web page
func (s *Server) getPageSummary(url string) (string, error) {
    defer func() {
        if err := recover(); err != nil {
            Log.WithField("error", err).Error("panic: ", string(debug.Stack()))
        }
    }()

    article, err := readability.FromURL(url, 30*time.Second, withBrowserUserAgent())
    if err != nil {
        return "", fmt.Errorf("failed to parse %s: %v", url, err)
    }

    return s.generateSimple(
        "Make a summary of the article. Be brief but thorough and highlight key points. Use markdown.",
        article.TextContent,
        s.conf.Models[0].ModelID,
    )
}

func withBrowserUserAgent() readability.RequestWith {
    return func(r *http.Request) {
        r.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")
    }
}
```

**Step 2: Commit**

```bash
git add function_calls.go
git commit -m "refactor: rewrite function_calls.go for Anthropic tool use

- Remove generate_image, text_to_speech, web_to_speech tools
- Keep make_summary using Anthropic instead of OpenAI
- MakeSummaryTool implements anthropic.ToolInterface
- handleAnthropicToolCalls processes ToolUseContent blocks
- Tool result continuation via getStreamingAnswer recursive call"
```

---

### Task 5: Rewrite voice.go — local whisper endpoint

**Files:**
- Rewrite: `voice.go`

**Step 1: Rewrite voice.go**

Keep opus→WAV conversion. Remove MP3 encoding and all TTS code. Add HTTP call to configurable whisper endpoint.

```go
package main

import (
    "bytes"
    "encoding/binary"
    "encoding/json"
    "fmt"
    "io"
    "mime/multipart"
    "net/http"
    "os"
    "strings"

    "github.com/tectiv3/chatgpt-bot/opus"
    tele "gopkg.in/telebot.v3"
)

func convertToWav(r io.Reader) ([]byte, error) {
    // Keep existing implementation unchanged (lines 19-49)
    output := new(bytes.Buffer)
    wavWriter, err := newWavWriter(output, 48000, 1, 16)
    if err != nil {
        return nil, err
    }
    s, err := opus.NewStream(r)
    if err != nil {
        return nil, err
    }
    defer s.Close()

    pcmbuf := make([]float32, 16384)
    for {
        n, err := s.ReadFloat32(pcmbuf)
        if err == io.EOF {
            break
        } else if err != nil {
            return nil, err
        }
        pcm := pcmbuf[:n*1]
        if err := wavWriter.WriteSamples(pcm); err != nil {
            return nil, err
        }
    }
    return output.Bytes(), nil
}

// wavWriter and newWavWriter — keep existing implementation from voice.go:52-91

func (s *Server) handleVoice(c tele.Context) {
    if c.Message().Voice.FileSize == 0 {
        return
    }
    audioFile := c.Message().Voice.File
    var reader io.ReadCloser
    var err error

    if s.conf.TelegramServerURL != "" {
        f, err := c.Bot().FileByID(audioFile.FileID)
        if err != nil {
            Log.Warn("Error getting file ID", "error=", err)
            return
        }
        reader, err = os.Open(f.FilePath)
        if err != nil {
            Log.Warn("Error opening file", "error=", err)
            return
        }
    } else {
        reader, err = c.Bot().File(&audioFile)
        if err != nil {
            Log.Warn("Error getting file content", "error=", err)
            return
        }
    }
    defer reader.Close()

    wav, err := convertToWav(reader)
    if err != nil {
        Log.Warn("failed to convert to wav", "error=", err)
        return
    }

    transcript, err := s.transcribe(wav)
    if err != nil {
        Log.Warn("failed to transcribe", "error=", err)
        return
    }

    if strings.HasPrefix(strings.ToLower(transcript), "reset") {
        chat := s.getChat(c.Chat(), c.Sender())
        s.deleteHistory(chat.ID)
        return
    }

    s.complete(c, transcript, false)
}

// transcribe sends WAV audio to the configured whisper endpoint
func (s *Server) transcribe(wav []byte) (string, error) {
    if s.conf.WhisperEndpoint == "" {
        return "", fmt.Errorf("whisper_endpoint not configured")
    }

    var body bytes.Buffer
    writer := multipart.NewWriter(&body)
    part, err := writer.CreateFormFile("file", "audio.wav")
    if err != nil {
        return "", fmt.Errorf("failed to create form file: %w", err)
    }
    if _, err := part.Write(wav); err != nil {
        return "", fmt.Errorf("failed to write audio: %w", err)
    }
    writer.Close()

    resp, err := http.Post(s.conf.WhisperEndpoint, writer.FormDataContentType(), &body)
    if err != nil {
        return "", fmt.Errorf("whisper request failed: %w", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        respBody, _ := io.ReadAll(resp.Body)
        return "", fmt.Errorf("whisper returned %d: %s", resp.StatusCode, string(respBody))
    }

    var result struct {
        Text string `json:"text"`
    }
    if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
        // Try plain text response
        respBody, _ := io.ReadAll(resp.Body)
        return string(respBody), nil
    }

    return result.Text, nil
}
```

**Step 2: Commit**

```bash
git add voice.go
git commit -m "refactor: replace OpenAI Whisper with configurable local STT endpoint

- Remove MP3 encoding (go-lame dependency)
- Remove all TTS code (sendAudio, textToSpeech, pageToSpeech)
- Add transcribe() calling whisper_endpoint via HTTP POST
- Keep opus->WAV conversion for Telegram voice messages"
```

---

### Task 6: Clean up image.go

**Files:**
- Modify: `image.go`

**Step 1: Remove textToImage, keep handleImage**

```go
package main

import (
    tele "gopkg.in/telebot.v3"
)

func (s *Server) handleImage(c tele.Context) {
    // Keep existing implementation unchanged — saves photo and calls complete
    photo := c.Message().Photo.File
    var fileName string

    if s.conf.TelegramServerURL != "" {
        f, err := c.Bot().FileByID(photo.FileID)
        if err != nil {
            Log.Warn("Error getting file ID", "error=", err)
            return
        }
        fileName = f.FilePath
    } else {
        out, err := os.Create("uploads/" + photo.FileID + ".jpg")
        if err != nil {
            Log.Warn("Error creating file", "error=", err)
            return
        }
        if err := c.Bot().Download(&photo, out.Name()); err != nil {
            Log.Warn("Error getting file content", "error=", err)
            return
        }
        fileName = out.Name()
    }

    chat := s.getChat(c.Chat(), c.Sender())
    chat.addImageToDialog(c.Message().Caption, fileName)
    s.db.Save(&chat)

    s.complete(c, "", true)
}
```

Remove `textToImage()` entirely. Remove the `openai` import.

**Step 2: Commit**

```bash
git add image.go
git commit -m "refactor: remove DALL-E image generation, keep inbound image handling"
```

---

### Task 7: Clean up bot.go — remove provider routing

**Files:**
- Modify: `bot.go`

**Step 1: Remove provider constants and simplify model selection**

- Remove constants: `pOllama`, `pGroq`, `pOpenAI`, `pAWS`, `pGemini`, `miniModel`, `openAILatest`
- Remove `/image` command (no DALL-E)
- Remove `/voice` command reference to TTS in help text
- Simplify model selection menu — remove provider availability checks (lines 210-215):

```go
// Replace the provider check block with:
for _, m := range s.conf.Models {
    if len(row) == 3 {
        rows = append(rows, menu.Row(row...))
        row = []tele.Btn{}
    }
    row = append(row, tele.Btn{Text: m.Name, Unique: "btnModel", Data: m.Name})
}
```

- Update help text to remove image generation and voice/TTS references
- Remove `openai` import

**Step 2: Commit**

```bash
git add bot.go
git commit -m "refactor: remove provider routing and dead commands from bot.go

- Remove provider constants (pOpenAI, pAWS, etc.)
- Simplify model selection (no provider checks)
- Remove /image command (no DALL-E)
- Update help text"
```

---

### Task 8: Update main.go — single provider init

**Files:**
- Modify: `main.go`

**Step 1: Simplify client initialization**

```go
// Replace server initialization (lines 100-118) with:
server := &Server{
    conf: conf,
    db:   db,
    ai:   anthropic.New(anthropic.WithAPIKey(conf.AnthropicAPIKey)),
    rateLimiter:       NewRateLimiter(20, time.Minute),
    connectionManager: NewConnectionManager(3),
}
```

Remove imports: `openai`, `awsnova`. Remove the `apiKey`/`orgID` variables. Remove conditional Gemini/Anthropic initialization.

**Step 2: Commit**

```bash
git add main.go
git commit -m "refactor: simplify main.go to single Anthropic client init

- Remove OpenAI, Nova, Gemini client creation
- Server.ai = anthropic.New() directly
- Remove unused config field references"
```

---

### Task 9: Update go.mod — remove dead dependencies

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`

**Step 1: Remove dependencies and tidy**

```bash
# Remove the replace directive for openai-go
# Then tidy
go mod tidy
```

This should automatically remove:
- `github.com/meinside/openai-go` (and the replace directive for `tectiv3/openai-go`)
- `github.com/tectiv3/awsnova-go`
- `github.com/tectiv3/go-lame`
- Any transitive dependencies only used by those

**Step 2: Build and verify**

```bash
go build
```

Expected: Clean compilation. If there are errors, fix them before committing.

**Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "chore: remove OpenAI, Nova, go-lame dependencies from go.mod"
```

---

### Task 10: Adapt webapp.go for Anthropic

**Files:**
- Modify: `webapp.go` (2557 lines — largest file, modify in place)

**Step 1: Audit all OpenAI references in webapp.go**

Search for and replace all occurrences of:
- `s.openAI` → use `s.generateSimple()` or `s.ai`
- `openai.*` types → local types or Anthropic types
- Provider checks (`model.Provider == "openai"`) → remove
- Annotation processing → citation processing
- Streaming path → adapt to Anthropic streaming

Key areas to modify:
- **Title/summary generation** (lines using `CreateChatCompletionWithContext`): Replace with `s.generateSimple()`
- **Streaming endpoint**: Replace OpenAI Responses API streaming with Anthropic streaming
- **Annotation rendering** in message responses: Replace with Citations
- **Model availability checks**: Remove provider filtering

This is the largest single task. The webapp streaming needs to produce SSE events from Anthropic's stream events instead of OpenAI's.

**Step 2: Verify webapp compiles and key endpoints work**

```bash
go build
```

**Step 3: Commit**

```bash
git add webapp.go
git commit -m "refactor: adapt webapp.go for Anthropic-only provider

- Replace OpenAI chat completion calls with generateSimple()
- Replace Responses API streaming with Anthropic streaming
- Replace annotations with citations in message responses
- Remove provider availability checks"
```

---

### Task 11: Update config.json.sample

**Files:**
- Modify: `config.json.sample`

**Step 1: Update sample config**

```json
{
  "telegram_bot_token": "YOUR_TELEGRAM_BOT_TOKEN",
  "telegram_server_url": "",
  "anthropic_api_key": "YOUR_ANTHROPIC_API_KEY",
  "whisper_endpoint": "http://localhost:8765/transcribe",
  "allowed_telegram_users": ["username1", "username2"],
  "verbose": false,
  "mini_app_enabled": false,
  "web_server_port": ":8080",
  "mini_app_url": "",
  "models": [
    {
      "model_id": "claude-sonnet-4-20250514",
      "name": "Sonnet",
      "web_search": true,
      "reasoning": false
    },
    {
      "model_id": "claude-opus-4-20250514",
      "name": "Opus",
      "web_search": true,
      "reasoning": false
    }
  ]
}
```

**Step 2: Commit**

```bash
git add config.json.sample
git commit -m "chore: update config.json.sample for Anthropic-only setup"
```

---

### Task 12: Database migration for Annotations → Citations

**Files:**
- Modify: `main.go` (add migration)

**Step 1: Add column migration**

After `AutoMigrate` calls in main.go, add:

```go
// Rename annotations column to citations
if db.Migrator().HasColumn(&ChatMessage{}, "annotations") {
    db.Migrator().RenameColumn(&ChatMessage{}, "annotations", "citations")
}
```

**Step 2: Commit**

```bash
git add main.go
git commit -m "chore: add database migration for annotations -> citations column"
```

---

### Task 13: Build, test, verify

**Step 1: Full build**

```bash
go build
```

**Step 2: Check for any remaining OpenAI references**

```bash
grep -r "openai" --include="*.go" . | grep -v "_test.go" | grep -v vendor
```

Expected: No results.

**Step 3: Check for remaining awsnova references**

```bash
grep -r "awsnova\|nova\." --include="*.go" . | grep -v "_test.go" | grep -v vendor
```

Expected: No results.

**Step 4: Run the bot (smoke test)**

```bash
./chatgpt-bot
```

Verify it starts without errors and can respond to a simple Telegram message.

**Step 5: Final commit if any fixes needed**

```bash
git add -A
git commit -m "fix: resolve remaining compilation issues from refactoring"
```

---

### Task 14: Update summariseHistory to use Anthropic

**Files:**
- Modify: `llm.go` (the `summariseHistory` function)

The existing `summariseHistory` calls `s.summarize()` which we already rewrote. But verify the full flow works: it should call `generateSimple()` internally and produce a summary that gets stored as a ChatMessage.

Review the function, ensure it doesn't reference any OpenAI types, and that the summarization model ID is valid.

**Step 1: Verify and fix**

Read `summariseHistory`, ensure it uses string roles and calls the new `summarize()`.

**Step 2: Commit if changes needed**

```bash
git add llm.go
git commit -m "fix: ensure summariseHistory works with Anthropic-only types"
```
