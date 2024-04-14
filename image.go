package main

import (
	"fmt"
	"github.com/meinside/openai-go"
	tele "gopkg.in/telebot.v3"
	"os"
)

func (s *Server) handleImage(c tele.Context) {
	photo := c.Message().Photo.File

	var fileName string
	//var err error
	//var reader io.ReadCloser

	if s.conf.TelegramServerURL != "" {
		f, err := c.Bot().FileByID(photo.FileID)
		if err != nil {
			Log.Warn("Error getting file ID", "error=", err)
			return
		}
		// start reader from f.FilePath
		//reader, err = os.Open(f.FilePath)
		//if err != nil {
		//	Log.Warn("Error opening file", "error=", err)
		//	return
		//}
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

	//defer reader.Close()
	//
	//bytes, err := io.ReadAll(reader)
	//if err != nil {
	//	Log.Warn("Error reading file content", "error=", err)
	//	return
	//}
	//
	//var base64Encoding string
	//
	//// Determine the content type of the image file
	//mimeType := http.DetectContentType(bytes)
	//
	//// Prepend the appropriate URI scheme header depending
	//// on the MIME type
	//switch mimeType {
	//case "image/jpeg":
	//	base64Encoding += "data:image/jpeg;base64,"
	//case "image/png":
	//	base64Encoding += "data:image/png;base64,"
	//}
	//
	//// Append the base64 encoded output
	//encoded := base64Encoding + toBase64(bytes)

	chat := s.getChat(c.Chat(), c.Sender())
	chat.addImageToDialog(c.Message().Caption, fileName)
	s.db.Save(&chat)

	s.complete(c, "", true)
}

func (s *Server) textToImage(c tele.Context, text, model string, hd bool, n int) error {
	Log.WithField("user", c.Sender().Username).Info("generating image")
	options := openai.ImageOptions{}.SetResponseFormat(openai.IamgeResponseFormatURL)
	switch model {
	case "dall-e-3":
		if hd {
			options.SetQuality("hd")
		}
		options.SetSize(openai.ImageSize1024x1024_DallE3).SetN(1)
		break
	case "dall-e-2":
		if n < 1 {
			n = 1
		}
		if n > 10 {
			n = 10
		}
		options.SetN(n).SetSize(openai.ImageSize1024x1024_DallE2)
		break
	default:
		return fmt.Errorf("unsupported model")
	}
	created, err := s.ai.CreateImage(text, options.SetModel(model))
	if err != nil {
		return fmt.Errorf("failed to create image: %s", err)
	}

	if len(created.Data) <= 0 {
		return fmt.Errorf("no items returned")
	}

	Log.WithField("user", c.Sender().Username).WithField("results", len(created.Data)).Info("image generation complete")

	for _, item := range created.Data {
		m := &tele.Photo{File: tele.FromURL(*item.URL)}
		_ = c.Send(m, "text", &tele.SendOptions{ReplyTo: c.Message()})
	}

	return nil
}
