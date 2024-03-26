package main

import "log"

// getChat returns chat from db or creates a new one
func (s *Server) getChat(chatID int64, username string) Chat {
	var chat Chat

	s.db.FirstOrCreate(&chat, Chat{ChatID: chatID})
	if len(chat.MasterPrompt) == 0 {
		chat.MasterPrompt = masterPrompt
		chat.ModelName = "gpt-4-turbo-preview"
		chat.Temperature = 0.8
		chat.Stream = true
		chat.ConversationAge = 1
		s.db.Save(&chat)
	}

	if len(username) > 0 && chat.UserID == 0 {
		user := s.getUser(username)
		chat.UserID = user.ID
		s.db.Save(&chat)
	}

	if chat.ConversationAge == 0 {
		chat.ConversationAge = 1
		s.db.Save(&chat)
	}

	s.db.Find(&chat.History, "chat_id = ?", chat.ID)
	log.Printf("History %d, chatid %d\n", len(chat.History), chat.ID)

	return chat
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
