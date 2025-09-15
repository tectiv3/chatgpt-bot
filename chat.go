package main

import (
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/meinside/openai-go"
	"github.com/tectiv3/anthropic-go"
	"github.com/tectiv3/awsnova-go"
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
	msg := openai.NewChatToolMessage(id, content)
	// log.Printf("Adding tool message to history: %v\n", msg)
	c.History = append(c.History,
		ChatMessage{
			Role:       msg.Role,
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
			Role:      openai.ChatMessageRoleUser,
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
			Role:      openai.ChatMessageRoleUser,
			Content:   &text,
			ImagePath: &path,
			Filename:  &filename,
			ChatID:    c.ChatID,
			CreatedAt: time.Now(),
		})
}

func (c *Chat) addMessageToDialog(msg openai.ChatMessage) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	// log.Printf("Adding message to history: %v\n", msg)
	toolCalls := make([]ToolCall, 0)
	for _, tc := range msg.ToolCalls {
		toolCalls = append(toolCalls, ToolCall{
			ID:       tc.ID,
			Type:     tc.Type,
			Function: tc.Function,
		})
	}
	content, err := msg.ContentString()
	if err != nil {
		if contentArr, err := msg.ContentArray(); err == nil {
			for _, c := range contentArr {
				if c.Type == "text" {
					content = *c.Text
					break
				}
				//if c.Type == "image_url" {
				//
				//}
			}
		}
	}
	c.History = append(c.History,
		ChatMessage{
			Role:      msg.Role,
			Content:   &content,
			ToolCalls: toolCalls,
			ChatID:    c.ChatID,
			CreatedAt: time.Now(),
		})
}

func (c *Chat) getDialog(request *string) []openai.ChatMessage {
	prompt := c.MasterPrompt
	if c.RoleID != nil {
		prompt = c.Role.Prompt
	}

	system := openai.NewChatSystemMessage(prompt)
	if request != nil {
		c.addMessageToDialog(openai.NewChatUserMessage(*request))
	}

	history := []openai.ChatMessage{system}
	for _, h := range c.History {
		if h.CreatedAt.Before(
			time.Now().AddDate(0, 0, -int(c.ConversationAge)),
		) {
			continue
		}

		var message openai.ChatMessage

		if h.Filename != nil {
			reader, err := os.Open(*h.ImagePath)
			if err != nil {
				Log.Warn("Error opening image file", "error=", err)
				continue
			}
			defer reader.Close()

			bytes, err := io.ReadAll(reader)
			if err != nil {
				Log.Warn("Error reading file content", "error=", err)
				continue
			}
			content := []openai.ChatMessageContent{{Type: "text", Text: h.Content}}
			content = append(content, openai.NewChatMessageContentFileWithBytes(bytes, *h.Filename))
			Log.Info("Adding file message to history", "filename=", *h.Filename)
			message = openai.ChatMessage{Role: h.Role, Content: content}
		} else if h.ImagePath != nil {
			reader, err := os.Open(*h.ImagePath)
			if err != nil {
				Log.Warn("Error opening image file", "error=", err)
				continue
			}
			defer reader.Close()

			image, err := io.ReadAll(reader)
			if err != nil {
				Log.Warn("Error reading file content", "error=", err)
				continue
			}
			content := []openai.ChatMessageContent{{Type: "text", Text: h.Content}}
			content = append(content, openai.NewChatMessageContentWithBytes(image))
			message = openai.ChatMessage{Role: h.Role, Content: content}
		} else {
			message = openai.ChatMessage{Role: h.Role, Content: h.Content}
		}
		if h.Role == openai.ChatMessageRoleAssistant && h.ToolCalls != nil {
			message.ToolCalls = make([]openai.ToolCall, 0)
			for _, tc := range h.ToolCalls {
				message.ToolCalls = append(message.ToolCalls, openai.ToolCall{
					ID:       tc.ID,
					Type:     tc.Type,
					Function: tc.Function,
				})
			}
		}
		if h.ToolCallID != nil {
			message.ToolCallID = h.ToolCallID
		}
		history = append(history, message)
	}

	// Log.Infof("Dialog history: %v", history)

	return history
}

func (c *Chat) getAnthropicDialog(request *string) []*anthropic.Message {
	if request != nil {
		c.addMessageToDialog(openai.NewChatUserMessage(*request))
	}

	history := []*anthropic.Message{}
	for _, h := range c.History {
		if h.CreatedAt.Before(
			time.Now().AddDate(0, 0, -int(c.ConversationAge)),
		) {
			continue
		}

		var message *anthropic.Message

		if h.ImagePath != nil {
			reader, err := os.Open(*h.ImagePath)
			if err != nil {
				Log.Warn("Error opening image file", "error=", err)
				continue
			}
			defer reader.Close()

			image, err := io.ReadAll(reader)
			if err != nil {
				Log.Warn("Error reading file content", "error=", err)
				continue
			}
			message = anthropic.NewMessage(anthropic.Role(h.Role),
				[]anthropic.Content{
					anthropic.NewTextContent(*h.Content),
					&anthropic.ImageContent{
						Source: &anthropic.ContentSource{
							Type:      anthropic.ContentSourceTypeBase64,
							MediaType: http.DetectContentType(image),
							Data:      toBase64(image),
						},
					},
				},
			)
		} else {
			message = anthropic.NewMessage(anthropic.Role(h.Role), []anthropic.Content{anthropic.NewTextContent(*h.Content)})
		}
		history = append(history, message)
	}

	return history
}

func (c *Chat) getNovaDialog(request *string) []awsnova.Message {
	if request != nil {
		c.addMessageToDialog(openai.NewChatUserMessage(*request))
	}

	history := []awsnova.Message{}
	for _, h := range c.History {
		if h.CreatedAt.Before(
			time.Now().AddDate(0, 0, -int(c.ConversationAge)),
		) {
			continue
		}

		var message awsnova.Message

		if h.ImagePath != nil {
			reader, err := os.Open(*h.ImagePath)
			if err != nil {
				Log.Warn("Error opening image file", "error=", err)
				continue
			}
			defer reader.Close()

			image, err := io.ReadAll(reader)
			if err != nil {
				Log.Warn("Error reading file content", "error=", err)
				continue
			}
			content := []awsnova.Content{{
				Text: h.Content,
				Image: &awsnova.Image{Format: "png", Source: struct {
					Bytes string `json:"bytes"`
				}{Bytes: toBase64(image)}},
			}}
			message = awsnova.Message{Role: string(h.Role), Content: content}
		} else {
			message = awsnova.Message{Role: string(h.Role), Content: []awsnova.Content{{
				Text: h.Content,
			}}}
		}
		// if h.Role == "assistant" && h.ToolCalls != nil {
		// 	message.ToolCalls = make([]openai.ToolCall, 0)
		// 	for _, tc := range h.ToolCalls {
		// 		message.ToolCalls = append(message.ToolCalls, openai.ToolCall{
		// 			ID:       tc.ID,
		// 			Type:     tc.Type,
		// 			Function: tc.Function,
		// 		})
		// 	}
		// }
		// if h.ToolCallID != nil {
		// 	message.ToolCallID = h.ToolCallID
		// }
		history = append(history, message)
	}

	// Log.Infof("Dialog history: %v", history)

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
