package main

import (
	"github.com/meinside/openai-go"
	"github.com/tectiv3/chatgpt-bot/i18n"
	tele "gopkg.in/telebot.v3"
	"strconv"
	"time"
)

// getChat returns chat from db or creates a new one
func (s *Server) getChat(c *tele.Chat, u *tele.User) *Chat {
	var chat Chat

	s.db.FirstOrCreate(&chat, Chat{ChatID: c.ID})
	if len(chat.MasterPrompt) == 0 {
		chat.MasterPrompt = masterPrompt
		chat.ModelName = "gpt-4-turbo-preview"
		chat.Temperature = 0.8
		chat.Stream = true
		chat.ConversationAge = 1
		s.db.Save(&chat)
	}

	if chat.UserID == 0 {
		user := s.getUser(u.Username)
		chat.UserID = user.ID
		s.db.Save(&chat)
	}

	if chat.ConversationAge == 0 {
		chat.ConversationAge = 1
		s.db.Save(&chat)
	}
	if chat.Lang == "" {
		chat.Lang = u.LanguageCode
		s.db.Save(&chat)
	}

	s.db.Find(&chat.History, "chat_id = ?", chat.ID)
	//log.Printf("History %d, chatid %d\n", len(chat.History), chat.ID)

	return &chat
}

func (s *Server) getChatByID(chatID int64) *Chat {
	var chat Chat
	s.db.First(&chat, Chat{ChatID: chatID})
	s.db.Find(&chat.History, "chat_id = ?", chat.ID)

	return &chat
}

// getUsers returns all users from db
func (s *Server) getUsers() []User {
	var users []User
	s.db.Model(&User{}).Preload("Threads").Find(&users)

	return users
}

// getUser returns user from db
func (s *Server) getUser(username string) User {
	var user User
	s.db.First(&user, User{Username: username})

	return user
}

func (s *Server) addUser(username string) {
	s.db.Create(&User{Username: username})
}

func (s *Server) delUser(userNane string) {
	s.db.Where("username = ?", userNane).Delete(&User{})
}

func (s *Server) deleteHistory(chatID uint) {
	s.db.Where("chat_id = ?", chatID).Delete(&ChatMessage{})
}

func (s *Server) loadUsers() {
	s.Lock()
	defer s.Unlock()
	admins := s.conf.AllowedTelegramUsers
	var usernames []string
	s.db.Model(&User{}).Pluck("username", &usernames)
	for _, username := range admins {
		if !in_array(username, usernames) {
			usernames = append(usernames, username)
		}
	}
	s.users = append(s.users, usernames...)
}

func (c *Chat) getSentMessage(context tele.Context) tele.Message {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if c.MessageID != nil {
		id, _ := strconv.Atoi(*c.MessageID)

		return tele.Message{ID: id, Chat: &tele.Chat{ID: c.ChatID}}
	}
	// if we already have a message ID, use it, otherwise create a new message
	if context.Get("reply") != nil {
		sentMessage := context.Get("reply").(tele.Message)
		c.MessageID = &([]string{strconv.Itoa(sentMessage.ID)}[0])
		return sentMessage
	}

	msgPointer, _ := context.Bot().Send(context.Recipient(), "...", "text", &tele.SendOptions{ReplyTo: context.Message()})
	c.MessageID = &([]string{strconv.Itoa(msgPointer.ID)}[0])

	return *msgPointer
}

func (c *Chat) addToolResultToHistory(id, content string) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	msg := openai.NewChatToolMessage(id, content)
	//log.Printf("Adding tool message to history: %v\n", msg)
	c.History = append(c.History,
		ChatMessage{
			Role:       msg.Role,
			Content:    &content,
			ChatID:     c.ChatID,
			ToolCallID: &id,
			CreatedAt:  time.Now(),
		})
}

func (c *Chat) addMessageToHistory(msg openai.ChatMessage) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	//log.Printf("Adding message to history: %v\n", msg)
	toolCalls := make([]ToolCall, 0)
	for _, tc := range msg.ToolCalls {
		toolCalls = append(toolCalls, ToolCall{
			ID:       tc.ID,
			Type:     tc.Type,
			Function: tc.Function,
		})
	}
	content, _ := msg.ContentString()
	c.History = append(c.History,
		ChatMessage{
			Role:      msg.Role,
			Content:   &content,
			ToolCalls: toolCalls,
			ChatID:    c.ChatID,
			CreatedAt: time.Now(),
		})
}

func (c *Chat) getConversationContext(request *string, image *string) []openai.ChatMessage {
	system := openai.NewChatSystemMessage(c.MasterPrompt)
	if request != nil {
		c.addMessageToHistory(openai.NewChatUserMessage(*request))
	}

	history := []openai.ChatMessage{system}
	for _, h := range c.History {
		if h.CreatedAt.Before(time.Now().
			AddDate(0, 0, -int(c.ConversationAge))) {
			continue
		}

		var message openai.ChatMessage

		if image != nil && request != nil && *h.Content == *request {
			content := []openai.ChatMessageContent{{Type: "text", Text: h.Content}}
			content = append(content, openai.NewChatMessageContentWithImageURL(*image))
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

	//log.Printf("Conversation context: %v\n", history)

	return history
}

func (c *Chat) t(key string, replacements ...*i18n.Replacements) string {
	return l.GetWithLocale(c.Lang, key, replacements...)
}
