package main

import (
	"os"

	tele "gopkg.in/telebot.v3"
)

func (s *Server) handleImage(c tele.Context) {
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
	caption := c.Message().Caption
	if caption == "" {
		caption = "Briefly describe the image"
	}
	chat.addImageToDialog(caption, fileName)
	s.db.Save(&chat)

	s.complete(c, "")
}
