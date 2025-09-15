package main

import (
	"fmt"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/go-shiori/go-readability"
	"github.com/meinside/openai-go"
	"github.com/tectiv3/chatgpt-bot/i18n"
	tele "gopkg.in/telebot.v3"
)

// withBrowserUserAgent creates a RequestWith modifier that sets a realistic browser user agent
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

// getResponseTools converts function tools to format compatible with Responses API
func (s *Server) getResponseTools() []any {
	traditionalTools := s.getFunctionTools()
	responseTools := make([]any, 0, len(traditionalTools))

	for _, tool := range traditionalTools {
		// Convert ChatCompletionTool to ResponseTool format
		responseTool := openai.ResponseTool{
			Type:       "function",
			Name:       tool.Function.Name,
			Parameters: tool.Function.Parameters,
		}
		if tool.Function.Description != nil {
			responseTool.Description = *tool.Function.Description
		}
		responseTools = append(responseTools, responseTool)
	}

	return responseTools
}

func (s *Server) getFunctionTools() []openai.ChatCompletionTool {
	availableTools := []openai.ChatCompletionTool{
		openai.NewChatCompletionTool(
			"set_reminder",
			"Set a reminder to do something at a specific time.",
			openai.NewToolFunctionParameters().
				AddPropertyWithDescription("reminder", "string", "A reminder of what to do, e.g. 'buy groceries'").
				AddPropertyWithDescription("time", "number", "A time at which to be reminded in minutes from now, e.g. 1440").
				SetRequiredParameters([]string{"reminder", "time"}),
		),
		openai.NewChatCompletionTool(
			"make_summary",
			"Make a summary of a web page.",
			openai.NewToolFunctionParameters().
				AddPropertyWithDescription("url", "string", "A valid URL to a web page").
				SetRequiredParameters([]string{"url"}),
		),
	}

	// availableTools = append(availableTools,
	// 	openai.NewChatCompletionTool(
	// 		"generate_image",
	// 		"Generate an image based on the input text",
	// 		openai.NewToolFunctionParameters().
	// 			AddPropertyWithDescription("text", "string", "The text to generate an image from").
	// 			AddPropertyWithDescription("hd", "boolean", "Whether to generate an HD image. Default to false.").
	// 			SetRequiredParameters([]string{"text", "hd"}),
	// 	),
	// )

	return availableTools
}

// handleResponseFunctionCalls converts ResponseOutput to ToolCalls and delegates to unified handler
func (s *Server) handleResponseFunctionCalls(chat *Chat, c tele.Context, functions []openai.ResponseOutput) (string, error) {
	// Convert ResponseOutput function calls to ToolCall format
	var toolCalls []openai.ToolCall

	for _, responseOutput := range functions {
		// Only process function calls
		if responseOutput.Type != "function_call" {
			continue
		}

		toolCall := openai.ToolCall{
			ID:   responseOutput.CallID,
			Type: "function",
			Function: openai.ToolCallFunction{
				Name:      responseOutput.Name,
				Arguments: responseOutput.Arguments,
			},
		}
		toolCalls = append(toolCalls, toolCall)
	}

	// Use the unified handler with converted tool calls
	return s.handleToolCalls(chat, c, toolCalls)
}

func (s *Server) handleFunctionCall(chat *Chat, c tele.Context, response openai.ChatMessage) (string, error) {
	return s.handleToolCalls(chat, c, response.ToolCalls)
}

// handleToolCalls processes a slice of tool calls (unified logic for both APIs)
func (s *Server) handleToolCalls(chat *Chat, c tele.Context, toolCalls []openai.ToolCall) (string, error) {
	// Create a Telegram notifier wrapper
	notifier := &TelegramToolCallNotifier{
		chat: chat,
		c:    c,
		bot:  s.bot,
	}

	// Call the refactored core function
	result, toolMessages, err := s.handleToolCallsCore(chat, toolCalls, notifier)

	// Add tool messages to dialog
	for _, msg := range toolMessages {
		chat.addMessageToDialog(msg)
	}

	// Handle continuation based on provider
	if len(result) > 0 {
		model := s.getModel(chat.ModelName)
		if model.Provider == pOpenAI {
			// Save history and return - conversation ends with tool result
			s.saveHistory(chat)
			return result, nil
		}

		// For other providers, continue conversation
		if chat.Stream {
			_ = s.getStreamAnswer(chat, c, nil)
			return "", nil
		}

		// Non-streaming mode
		if model.Provider == pOpenAI {
			// For OpenAI models, use non-streaming Responses API
			err := s.getResponse(chat, c, nil)
			return "", err
		} else {
			err := s.getAnswer(chat, c, nil)
			return "", err
		}
	}

	s.saveHistory(chat)
	return "", err
}

// handleToolCallsCore is the core implementation without Telegram dependencies
func (s *Server) handleToolCallsCore(chat *Chat, toolCalls []openai.ToolCall, notifier ToolCallNotifier) (string, []openai.ChatMessage, error) {
	var results []string
	var toolMessages []openai.ChatMessage
	var resultErr error

	toolCallsCount := len(toolCalls)

	for i, toolCall := range toolCalls {
		function := toolCall.Function
		if function.Name == "" {
			err := fmt.Errorf("there was no returned function call name")
			resultErr = err
			continue
		}

		Log.WithField("tools", toolCallsCount).WithField("tool", i).WithField("function", function.Name).Info("Function call")

		// Process the function call
		functionResult, err := s.executeToolCall(chat, toolCall, notifier)
		if err != nil {
			Log.WithField("function", function.Name).WithField("error", err).Error("Function call failed")
			functionResult = fmt.Sprintf("Error: %v", err)
			resultErr = err
		}

		// Add result to list
		if functionResult != "" {
			results = append(results, functionResult)

			// Create tool message for history
			toolMessage := openai.NewChatToolMessage(functionResult, toolCall.ID)
			toolMessages = append(toolMessages, toolMessage)
		}
	}

	// Combine all results
	combinedResult := ""
	if len(results) > 0 {
		combinedResult = strings.Join(results, "\n\n")
	}

	return combinedResult, toolMessages, resultErr
}

// executeToolCall executes a single tool call
func (s *Server) executeToolCall(chat *Chat, toolCall openai.ToolCall, notifier ToolCallNotifier) (string, error) {
	function := toolCall.Function

	// Notify about the function being called
	if notifier != nil {
		notifier.OnFunctionCall(function.Name, function.Arguments)
	}

	switch function.Name {
	case "text_to_speech":
		type parsed struct {
			Text     string `json:"text"`
			Language string `json:"language"`
		}
		var arguments parsed
		if err := toolCall.ArgumentsInto(&arguments); err != nil {
			return "", fmt.Errorf("failed to parse arguments: %w", err)
		}

		// For TTS in Telegram context
		if teleNotifier, ok := notifier.(*TelegramToolCallNotifier); ok {
			go s.textToSpeech(teleNotifier.c, arguments.Text, arguments.Language)
			return "Text-to-speech generation started", nil
		}

		// For webapp context - generate and return URL
		// TODO: Implement audio generation and return download URL
		return "Text-to-speech is being generated", nil

	case "web_to_speech":
		type parsed struct {
			URL string `json:"url"`
		}
		var arguments parsed
		if err := toolCall.ArgumentsInto(&arguments); err != nil {
			return "", fmt.Errorf("failed to parse arguments: %w", err)
		}

		// For Telegram context
		if teleNotifier, ok := notifier.(*TelegramToolCallNotifier); ok {
			go s.pageToSpeech(teleNotifier.c, arguments.URL)
			return "Web page to speech conversion started", nil
		}

		// For webapp context
		// TODO: Implement page-to-speech and return download URL
		return "Web page to speech conversion started", nil

	case "generate_image":
		type parsed struct {
			Text string `json:"text"`
			HD   bool   `json:"hd"`
		}
		var arguments parsed
		if err := toolCall.ArgumentsInto(&arguments); err != nil {
			return "", fmt.Errorf("failed to parse arguments: %w", err)
		}

		// For Telegram context
		if teleNotifier, ok := notifier.(*TelegramToolCallNotifier); ok {
			if err := s.textToImage(teleNotifier.c, arguments.Text, arguments.HD); err != nil {
				return "", fmt.Errorf("failed to generate image: %w", err)
			}
			return "Image generated and sent", nil
		}

		// For webapp context - generate and return URL
		// TODO: Implement image generation and return URL for display
		return fmt.Sprintf("Image generation for '%s' started", arguments.Text), nil

	case "set_reminder":
		type parsed struct {
			Reminder string `json:"reminder"`
			Minutes  int64  `json:"time"`
		}
		var arguments parsed
		if err := toolCall.ArgumentsInto(&arguments); err != nil {
			return "", fmt.Errorf("failed to parse arguments: %w", err)
		}

		// Current implementation only works for Telegram and is not persistent
		// TODO: Implement proper reminder storage in database for both Telegram and webapp
		if teleNotifier, ok := notifier.(*TelegramToolCallNotifier); ok {
			if err := s.setReminder(teleNotifier.c.Chat().ID, arguments.Reminder, arguments.Minutes); err != nil {
				return "", fmt.Errorf("failed to set reminder: %w", err)
			}
			return fmt.Sprintf("Reminder set for %d minutes from now (will be lost if bot restarts)", arguments.Minutes), nil
		}

		// For webapp, we need database storage implementation
		return "Reminder feature not yet implemented for web interface", nil

	case "make_summary":
		type parsed struct {
			URL string `json:"url"`
		}
		var arguments parsed
		if err := toolCall.ArgumentsInto(&arguments); err != nil {
			return "", fmt.Errorf("failed to parse arguments: %w", err)
		}

		summary, err := s.getPageSummary(arguments.URL)
		if err != nil {
			return "", fmt.Errorf("failed to get page summary: %w", err)
		}
		return summary, nil

	default:
		return "", fmt.Errorf("unknown function: %s", function.Name)
	}
}

func (s *Server) setReminder(chatID int64, reminder string, minutes int64) error {
	timer := time.NewTimer(time.Minute * time.Duration(minutes))
	go func() {
		<-timer.C
		_, _ = s.bot.Send(tele.ChatID(chatID), reminder)
	}()

	return nil
}

func (s *Server) pageToSpeech(c tele.Context, url string) {
	defer func() {
		if err := recover(); err != nil {
			Log.WithField("error", err).Error("panic: ", string(debug.Stack()))
		}
	}()

	article, err := readability.FromURL(url, 30*time.Second, withBrowserUserAgent())
	if err != nil {
		Log.Fatalf("failed to parse %s, %v\n", url, err)
	}

	if s.conf.Verbose {
		Log.Info("Page title=", article.Title, ", content=", len(article.TextContent))
	}
	_ = c.Notify(tele.Typing)

	s.sendAudio(c, article.TextContent)
}

// getPageSummary gets a page summary synchronously and returns the result
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

	msg := openai.NewChatUserMessage(article.TextContent)
	// You are acting as a summarization AI, and for the input text please summarize it to the most important 3 to 5 bullet points for brevity:
	system := openai.NewChatSystemMessage("Make a summary of the article. Try to be as brief as possible and highlight key points. Use markdown to annotate the summary.")

	history := []openai.ChatMessage{system, msg}

	response, err := s.openAI.CreateChatCompletion(miniModel, history, openai.ChatCompletionOptions{}.SetUser(userAgent(31337)).SetTemperature(0.2))
	if err != nil {
		return "", fmt.Errorf("failed to create chat completion: %v", err)
	}

	str, _ := response.Choices[0].Message.ContentString()

	return str, nil
}
