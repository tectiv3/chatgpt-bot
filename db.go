package main

import tele "gopkg.in/telebot.v3"

// getChat returns chat from db or creates a new one
func (s *Server) getChat(c *tele.Chat, u *tele.User) *Chat {
	var chat Chat

	s.db.Preload("User").Preload("User.Roles").Preload("Role").Preload("History").FirstOrCreate(&chat, Chat{ChatID: c.ID})
	if len(chat.MasterPrompt) == 0 {
		chat.MasterPrompt = masterPrompt
		chat.ModelName = openAILatest
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

	// s.db.Find(&chat.History, "chat_id = ?", chat.ID)
	// log.Printf("History %d, chatid %d\n", len(chat.History), chat.ID)

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
	s.db.Model(&User{}).Preload("Threads").Preload("Threads.Role").Find(&users)

	return users
}

// getUser returns user from db
func (s *Server) getUser(username string) (user User) {
	s.db.First(&user, User{Username: username})

	return user
}

func (s *Server) addUser(username string) {
	s.db.Create(&User{Username: username})
}

func (s *Server) delUser(username string) {
	user := s.getUser(username)
	if user.ID == 0 {
		Log.Info("User not found: ", username)
		return
	}

	var chat Chat
	s.db.First(&chat, Chat{UserID: user.ID})
	if chat.ID != 0 {
		s.deleteHistory(chat.ID)
		s.db.Unscoped().Delete(&Chat{}, chat.ID)
	}
	s.db.Unscoped().Delete(&User{}, user.ID)
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
	s.users = []string{}
	for _, username := range admins {
		if !in_array(username, usernames) {
			usernames = append(usernames, username)
		}
	}
	s.users = append(s.users, usernames...)
}

func (s *Server) findRole(userID uint, name string) *Role {
	var r Role
	s.db.First(&r, Role{UserID: userID, Name: name})

	return &r
}

func (s *Server) getModel(model string) *AiModel {
	for _, m := range s.conf.Models {
		if m.Name == model {
			return &m
		}
		if m.ModelID == model {
			return &m
		}
		if model == openAILatest {
			return &AiModel{s.conf.OpenAILatestModel, "OpenAI Latest", "openai"}
		}
	}

	return &AiModel{model, model, "openai"}
}

func (s *Server) getRole(id uint) *Role {
	var r Role
	s.db.First(&r, id)

	return &r
}

func (s *Server) setChatRole(id *uint, ChatID int64) {
	s.db.Model(&Chat{}).Where("chat_id", ChatID).Update("role_id", id)
}

func (s *Server) setChatLastMessageID(id *string, ChatID int64) {
	s.db.Model(&Chat{}).Where("chat_id", ChatID).Update("message_id", id)
}

// set user.State to null
func (s *Server) resetUserState(user User) {
	s.db.Model(&User{}).Where("id", user.ID).Update("State", nil)
}
