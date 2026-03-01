package main

import (
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/tectiv3/anthropic-go"
	"github.com/tectiv3/chatgpt-bot/i18n"
	tele "gopkg.in/telebot.v3"
)

func (c *Chat) getSentMessage(context tele.Context) *tele.Message {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if c.MessageID != nil {
		id, _ := strconv.Atoi(*c.MessageID)

		return &tele.Message{ID: id, Chat: &tele.Chat{ID: c.ChatID}}
	}
	// if we already have a message ID, use it, otherwise create a new message
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
	c.History = append(c.History,
		ChatMessage{
			Role:       "user",
			Content:    &content,
			ChatID:     c.ChatID,
			ToolCallID: &id,
			CreatedAt:  time.Now(),
		})
}

func (c *Chat) addImageToDialog(text, path string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.History = append(c.History,
		ChatMessage{
			Role:      "user",
			Content:   &text,
			ImagePath: &path,
			ChatID:    c.ChatID,
			CreatedAt: time.Now(),
		})
}

func (c *Chat) addFileToDialog(text, path, filename string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.History = append(c.History,
		ChatMessage{
			Role:      "user",
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
	c.History = append(c.History,
		ChatMessage{
			Role:      "user",
			Content:   &text,
			ChatID:    c.ChatID,
			CreatedAt: time.Now(),
		})
}

func (c *Chat) addAssistantMessage(text string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.History = append(c.History,
		ChatMessage{
			Role:      "assistant",
			Content:   &text,
			ChatID:    c.ChatID,
			CreatedAt: time.Now(),
		})
}

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
		if (h.Content == nil || *h.Content == "") && len(h.ToolCalls) == 0 && h.ToolCallID == nil {
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
			imageData, err := os.ReadFile(*h.ImagePath)
			if err != nil {
				Log.Warn("Error reading image", "error=", err)
				continue
			}
			content = append(content, anthropic.NewTextContent(*h.Content))
			content = append(content, &anthropic.ImageContent{
				Source: anthropic.RawData(http.DetectContentType(imageData), imageData),
			})
		} else if h.Content != nil && *h.Content != "" {
			content = append(content, anthropic.NewTextContent(*h.Content))
		}

		// Handle tool calls in assistant messages
		if role == "assistant" && len(h.ToolCalls) > 0 {
			for _, tc := range h.ToolCalls {
				content = append(content, &anthropic.ToolUseContent{
					ID:    tc.ID,
					Name:  tc.Function.Name,
					Input: json.RawMessage(tc.Function.Arguments),
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

// Safe methods for updating chat properties
func (c *Chat) updateTotalTokens(tokens int) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.TotalTokens += tokens
}

func (c *Chat) setMessageID(id *string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.MessageID = id
}

func (c *Chat) getMessageID() *string {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	return c.MessageID
}

func (c *Chat) removeMenu(context tele.Context) {
	c.mutex.Lock()
	if c.MessageID != nil {
		_, _ = context.Bot().EditReplyMarkup(tele.StoredMessage{MessageID: *c.MessageID, ChatID: c.ChatID}, removeMenu)
		c.MessageID = nil
	}
	c.mutex.Unlock()
}

// GetEnabledToolsArray returns the enabled tools as an array
func (c *Chat) GetEnabledToolsArray() []string {
	if c.EnabledTools == "" {
		return []string{}
	}

	return strings.Split(c.EnabledTools, ",")
}

// SetEnabledToolsFromArray sets the enabled tools from an array
func (c *Chat) SetEnabledToolsFromArray(tools []string) {
	if len(tools) == 0 {
		c.EnabledTools = ""
		return
	}
	c.EnabledTools = strings.Join(tools, ",")
}
