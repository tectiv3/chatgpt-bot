package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/tectiv3/anthropic-go"
	tele "gopkg.in/telebot.v3"
)

// friendlyAPIError extracts a human-readable message from Anthropic ClientError,
// falling back to err.Error() for other error types.
func friendlyAPIError(err error) string {
	var clientErr *anthropic.ClientError
	if !errors.As(err, &clientErr) {
		return err.Error()
	}

	// Error() returns "provider api error (status N): {json body}"
	// Extract the JSON portion after the prefix
	raw := clientErr.Error()
	if idx := strings.Index(raw, ": {"); idx != -1 {
		body := raw[idx+2:]
		var parsed struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(body), &parsed) == nil && parsed.Error.Message != "" {
			return fmt.Sprintf("API error (%d): %s", clientErr.StatusCode(), parsed.Error.Message)
		}
	}

	return err.Error()
}

func (s *Server) complete(c tele.Context, message string) {
	chat := s.getChat(c.Chat(), c.Sender())

	var msgPtr *string
	if len(message) > 0 {
		msgPtr = &message
	}

	s.getStreamingAnswer(chat, c, msgPtr)
}

// getStreamingAnswer handles streaming responses from Anthropic.
// Creates a fresh client per request to avoid race conditions.
// Tool call continuation is handled iteratively (max 10 rounds).
func (s *Server) getStreamingAnswer(chat *Chat, c tele.Context, question *string) {
	model := s.getModel(chat.ModelName)
	maxTokens := 4096
	maxToolRounds := 10

	system := chat.MasterPrompt
	if chat.RoleID != nil {
		system = chat.Role.Prompt
	}
	system += fmt.Sprintf("\n\nCurrent date: %s", time.Now().Format("2006-01-02"))
	if model.WebSearch {
		system += "\n\nOnly use web search when the query explicitly requires up-to-date information, factual verification, or references you don't have. Do not search for general knowledge questions you can answer from training data."
	}

	chat.removeMenu(c)
	draftID := int(time.Now().UnixMilli() % 1000000)
	if draftID == 0 {
		draftID = 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	var tools []anthropic.ToolInterface
	if model.WebSearch {
		tools = append(tools, anthropic.NewWebSearchTool(anthropic.WebSearchToolOptions{MaxUses: 3}))
	}
	tools = append(tools, s.getTools()...)

	opts := []anthropic.Option{
		anthropic.WithAPIKey(s.conf.AnthropicAPIKey),
		anthropic.WithModel(model.ModelID),
		anthropic.WithSystemPrompt(system),
		anthropic.WithMaxTokens(maxTokens),
	}
	if len(tools) > 0 {
		opts = append(opts, anthropic.WithTools(tools...))
	}

	dialog := chat.getDialog(question)
	_ = c.Notify(tele.Typing)
	var totalInputTokens, totalOutputTokens int

	caching := true
	for round := 0; round < maxToolRounds; round++ {
		client := anthropic.New(opts...)
		client.Caching = &caching
		if !model.Reasoning {
			temp := chat.Temperature
			client.Temperature = &temp
		}

		stream, err := client.Stream(ctx, dialog)
		if err != nil {
			Log.WithField("user", c.Sender().Username).Error(err)
			_, _ = c.Bot().Send(c.Sender(), friendlyAPIError(err))
			return
		}

		var result strings.Builder
		tokens := 0
		lastDraft := time.Now()
		accumulator := anthropic.NewResponseAccumulator()

		for stream.Next() {
			select {
			case <-ctx.Done():
				stream.Close()
				_, _ = c.Bot().Send(c.Sender(), "Timeout")
				return
			default:
			}
			event := stream.Event()
			if err := accumulator.AddEvent(event); err != nil {
				Log.WithField("user", c.Sender().Username).
					WithField("event_type", event.Type).
					Warn("Accumulator error: ", err)
			}

			switch event.Type {
			case anthropic.EventTypeContentBlockStart:
				if event.ContentBlock != nil {
					if event.ContentBlock.Type == anthropic.ContentTypeServerToolUse &&
						event.ContentBlock.Name == "web_search" {
						_ = c.Bot().SendMessageDraft(c.Sender(), draftID, chat.t("Web search started, please wait..."))
					}
				}
			case anthropic.EventTypeContentBlockDelta:
				if event.Delta == nil {
					continue
				}
				if event.Delta.Type == anthropic.EventDeltaTypeText {
					result.WriteString(event.Delta.Text)
					tokens++
					if time.Since(lastDraft) >= 100*time.Millisecond {
						if err := c.Bot().SendMessageDraft(c.Sender(), draftID, result.String()); err != nil {
							Log.WithField("user", c.Sender().Username).Warn("SendMessageDraft error: ", err)
						}
						lastDraft = time.Now()
					}
				}
			case anthropic.EventTypeMessageStop:
				Log.WithField("user", c.Sender().Username).
					WithField("tokens", tokens).Info("Response stream finished")
			}
		}
		stream.Close()

		if err := stream.Err(); err != nil {
			if ctx.Err() == context.DeadlineExceeded {
				Log.WithField("user", c.Sender().Username).Error("Timeout. Partial: ", result.String())
				_, _ = c.Bot().Send(c.Sender(), "Timeout. Partial: "+result.String())
			} else if ctx.Err() == context.Canceled {
				Log.WithField("user", c.Sender().Username).Error("Request cancelled. Partial: ", result.String())
				_, _ = c.Bot().Send(c.Sender(), "Request cancelled. Partial: "+result.String())
			} else {
				Log.WithField("user", c.Sender().Username).Error("Streaming error: ", err)
				_, _ = c.Bot().Send(c.Sender(), "Error: "+friendlyAPIError(err))
			}
			return
		}

		if !accumulator.IsComplete() {
			Log.WithField("user", c.Sender().Username).Warn("Stream ended with incomplete accumulator")
		}

		response := accumulator.Response()
		if response == nil {
			Log.WithField("user", c.Sender().Username).Error("No response from stream")
			if result.Len() > 0 {
				s.sendFinalReply(chat, result.String(), c)
				chat.addAssistantMessage(result.String())
				s.saveHistory(chat)
			}
			return
		}

		// Extract citations
		var citations []Citation
		for _, content := range response.Content {
			if content == nil {
				continue
			}
			if tc, ok := content.(*anthropic.TextContent); ok {
				for _, cit := range tc.Citations {
					citations = append(citations, extractCitation(cit))
				}
			}
		}

		// Check for tool use — execute and loop
		var toolUses []anthropic.Content
		for _, content := range response.Content {
			if content == nil {
				continue
			}
			if content.Type() == anthropic.ContentTypeToolUse {
				toolUses = append(toolUses, content)
			}
		}

		usage := accumulator.Usage()
		totalInputTokens += usage.InputTokens
		totalOutputTokens += usage.OutputTokens
		if usage.CacheReadInputTokens > 0 || usage.CacheCreationInputTokens > 0 {
			Log.WithField("user", c.Sender().Username).
				Infof("Cache: read=%d, created=%d", usage.CacheReadInputTokens, usage.CacheCreationInputTokens)
		}

		if len(toolUses) > 0 {
			s.processToolCalls(chat, c, response, toolUses, draftID)
			dialog = chat.getDialog(nil)
			continue
		}

		// No tool use — finalize response
		reply := result.String()
		s.sendFinalReply(chat, reply, c)

		totalTokens := totalInputTokens + totalOutputTokens
		if totalTokens > 0 {
			chat.updateTotalTokens(totalTokens)
		}

		if reply != "" {
			chat.addAssistantMessage(reply)
			if len(citations) > 0 {
				s.storeCitations(chat, citations)
			}
			s.saveHistory(chat)
		}
		return
	}

	Log.WithField("user", c.Sender().Username).Warn("Max tool call rounds exceeded")
	_, _ = c.Bot().Send(c.Sender(), "Response incomplete: too many tool calls")
}

// extractCitation converts an Anthropic Citation to our local Citation type
func extractCitation(cit anthropic.Citation) Citation {
	switch c := cit.(type) {
	case *anthropic.WebSearchResultLocation:
		return Citation{
			URL:       c.URL,
			Title:     c.Title,
			CitedText: c.CitedText,
		}
	case *anthropic.CharLocation:
		return Citation{
			Title:     c.DocumentTitle,
			CitedText: c.CitedText,
		}
	default:
		return Citation{}
	}
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
	model := s.conf.Models[0]
	return s.generateSimple(masterPrompt, request, model.ModelID)
}

// summarize summarizes chat history using generateSimple
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

// storeCitations saves citations to the last assistant message in chat history
func (s *Server) storeCitations(chat *Chat, citations []Citation) {
	var lastMsg *ChatMessage
	for i := len(chat.History) - 1; i >= 0; i-- {
		if chat.History[i].Role == "assistant" {
			lastMsg = &chat.History[i]
			break
		}
	}
	if lastMsg != nil {
		s.StoreCitations(lastMsg, citations)
	}
}

func (s *Server) sendFinalReply(chat *Chat, answer string, c tele.Context) {
	if len(answer) == 0 {
		return
	}

	msg, err := c.Bot().Send(
		c.Sender(),
		ConvertMarkdownToTelegramMarkdownV2(answer),
		"text",
		&tele.SendOptions{ParseMode: tele.ModeMarkdownV2},
		replyMenu,
	)
	if err != nil {
		Log.Warn(err)
		msg, err = c.Bot().Send(c.Sender(), answer, replyMenu)
		if err != nil {
			Log.Warn(err)
			_ = c.Send(err.Error())
			return
		}
	}

	if msg != nil {
		id := strconv.Itoa(msg.ID)
		chat.setMessageID(&id)
		s.setChatLastMessageID(&id, chat.ChatID)
	}
}

func (s *Server) saveHistory(chat *Chat) {
	var history []ChatMessage
	chat.mutex.Lock()
	defer chat.mutex.Unlock()
	for _, h := range chat.History {
		if h.ID == 0 {
			history = append(history, h)
			continue
		}
		if chat.ConversationAge > 0 && h.CreatedAt.Before(time.Now().AddDate(0, 0, -int(chat.ConversationAge))) {
			s.db.Where("chat_id = ?", chat.ID).Where("id = ?", h.ID).Delete(&ChatMessage{})
		} else {
			history = append(history, h)
		}
	}
	chat.History = history
	if len(chat.History) < 100 {
		s.db.Save(&chat)
		return
	}

	Log.WithField("user", chat.User.Username).
		Infof("Chat history for chat ID %d is too long. Summarising...", chat.ID)
	summary, err := s.summarize(chat.History)
	if err != nil {
		Log.Warn(err)
		return
	}

	if s.conf.Verbose {
		Log.Info(summary)
	}
	maxID := chat.History[len(chat.History)-3].ID
	Log.WithField("user", chat.User.Username).
		Infof("Deleting chat history for chat ID %d up to message ID %d", chat.ID, maxID)
	s.db.Where("chat_id = ?", chat.ID).Where("id <= ?", maxID).Delete(&ChatMessage{})

	chat.History = []ChatMessage{{
		Role:      "assistant",
		Content:   &summary,
		ChatID:    chat.ChatID,
		CreatedAt: time.Now(),
	}}

	Log.WithField("user", chat.User.Username).
		Info("Chat history length after summarising: ", len(chat.History))

	s.db.Save(&chat)
}

func (s *Server) processPDF(c tele.Context) {
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

	s.complete(c, "")
}
