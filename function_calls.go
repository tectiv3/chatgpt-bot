package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/go-shiori/go-readability"
	"github.com/tectiv3/anthropic-go"
	"github.com/tectiv3/chatgpt-bot/i18n"
	tele "gopkg.in/telebot.v3"
)

// ToolCallNotifier is an interface for notifying about tool call events
type ToolCallNotifier interface {
	OnFunctionCall(functionName string, arguments string)
	OnFunctionResult(functionName string, result string)
	SendMessage(message string) error
}

// TelegramToolCallNotifier implements ToolCallNotifier for Telegram
type TelegramToolCallNotifier struct {
	chat *Chat
	c    tele.Context
	bot  *tele.Bot
}

func (t *TelegramToolCallNotifier) OnFunctionCall(functionName string, arguments string) {
	sentMessage := t.chat.getSentMessage(t.c)
	message := fmt.Sprintf(t.chat.t("Action: {{.tool}}\nAction input: %s", &i18n.Replacements{"tool": t.chat.t(functionName)}), arguments)
	_, _ = t.bot.Edit(sentMessage, message)
}

func (t *TelegramToolCallNotifier) OnFunctionResult(functionName string, result string) {
	// Can be used to notify about function results if needed
}

func (t *TelegramToolCallNotifier) SendMessage(message string) error {
	_, err := t.bot.Send(t.c.Recipient(), message)
	return err
}

// WebappToolCallNotifier implements ToolCallNotifier for web app (no-op for now)
type WebappToolCallNotifier struct{}

func (w *WebappToolCallNotifier) OnFunctionCall(functionName string, arguments string) {
	// Web app handles notifications through SSE stream
}

func (w *WebappToolCallNotifier) OnFunctionResult(functionName string, result string) {
	// Web app handles notifications through SSE stream
}

func (w *WebappToolCallNotifier) SendMessage(message string) error {
	// Web app doesn't send messages directly
	return nil
}

func withBrowserUserAgent() readability.RequestWith {
	return func(r *http.Request) {
		r.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36")
	}
}

// MakeSummaryTool implements anthropic.ToolInterface for web page summarization
type MakeSummaryTool struct{}

func (t *MakeSummaryTool) Name() string { return "make_summary" }
func (t *MakeSummaryTool) Description() string {
	return "Make a summary of a web page from an explicit summarization request."
}
func (t *MakeSummaryTool) Schema() *anthropic.Schema {
	return &anthropic.Schema{
		Type: anthropic.Object,
		Properties: map[string]*anthropic.Property{
			"url": {Type: anthropic.String, Description: "A valid URL to a web page"},
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

	// Collect text parts from the assistant response
	var textParts []string
	for _, content := range response.Content {
		if tc, ok := content.(*anthropic.TextContent); ok {
			textParts = append(textParts, tc.Text)
		}
	}

	// Build tool call records for chat history
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

	// Store the assistant message (text + tool_use blocks) in dialog history
	assistantText := strings.Join(textParts, "")
	chat.addMessageToDialog(ChatMessage{
		Role:      "assistant",
		Content:   &assistantText,
		ToolCalls: toolCalls,
	})

	// Execute each tool and add results to dialog
	for _, tu := range toolUses {
		tuc, ok := tu.(*anthropic.ToolUseContent)
		if !ok {
			continue
		}

		result, err := s.executeToolCall(tuc, notifier)
		if err != nil {
			result = fmt.Sprintf("Error: %v", err)
		}

		chat.addToolResultToDialog(tuc.ID, result)
	}

	s.saveHistory(chat)

	// Continue the conversation so the model can incorporate tool results
	s.getStreamingAnswer(chat, c, nil)
}

// executeToolCall executes a single tool call
func (s *Server) executeToolCall(toolUse *anthropic.ToolUseContent, notifier ToolCallNotifier) (string, error) {
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

// getPageSummary fetches and summarizes a web page using Anthropic
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

	if s.conf.Verbose {
		Log.Info("Page title=", article.Title, ", content=", len(article.TextContent))
	}

	return s.generateSimple(
		"Make a summary of the article. Be brief but thorough and highlight key points. Use markdown.",
		article.TextContent,
		s.conf.Models[0].ModelID,
	)
}
