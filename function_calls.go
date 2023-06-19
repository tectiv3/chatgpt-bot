package main

import (
	"fmt"
	"github.com/go-shiori/go-readability"
	"github.com/meinside/openai-go"
	tele "gopkg.in/telebot.v3"
	"log"
	"runtime/debug"
	"time"
)

func (s Server) handleFunctionCall(c tele.Context, result openai.ChatMessage) (string, error) {
	functionName := result.FunctionCall.Name
	if functionName == "" {
		err := fmt.Sprint("there was no returned function call name")
		log.Println(err)

		return err, fmt.Errorf(err)
	}
	if result.FunctionCall.Arguments == nil {
		err := fmt.Sprint("there were no returned function call arguments")
		log.Println(err)

		return err, fmt.Errorf(err)
	}
	arguments, _ := result.FunctionCall.ArgumentsParsed()

	if functionName == "set_reminder" {
		var reminder string
		var minutes int64
		if l, exists := arguments["reminder"]; exists {
			reminder = l.(string)
		} else {
			err := fmt.Sprint("there was no returned parameter 'reminder' from function call")
			log.Println(err)

			return err, fmt.Errorf(err)
		}
		if u, exists := arguments["time"]; exists {
			minutes = int64(u.(float64))
		} else {
			err := fmt.Sprint("there was no returned parameter 'time' from function call")
			log.Println(err)

			return err, fmt.Errorf(err)
		}
		log.Printf("Will call %s(\"%s\", %d)", functionName, reminder, minutes)

		if err := s.setReminder(c.Chat().ID, reminder, minutes); err != nil {
			return "", err
		}

		return "Reminder set", nil
	} else if functionName == "make_summary" {
		var url string
		if l, exists := arguments["url"]; exists {
			url = l.(string)
		} else {
			err := fmt.Sprint("there was no returned parameter 'url' from function call")
			log.Println(err)

			return err, fmt.Errorf(err)
		}
		log.Printf("Will call %s(\"%s\")", functionName, url)

		go s.getPageSummary(c.Chat().ID, url)

		return "Downloading summary. Please wait.", nil
	}
	log.Printf("Got a function call %s(%v)", functionName, arguments)

	return fmt.Sprintf("Function call in response (%s)", functionName), nil
}

func (s Server) setReminder(chatID int64, reminder string, minutes int64) error {
	timer := time.NewTimer(time.Minute * time.Duration(minutes))
	go func() {
		<-timer.C
		fmt.Println("Timer fired")

		if _, err := s.bot.Send(tele.ChatID(chatID), reminder); err != nil {
			log.Println(err)
		}
	}()

	return nil
}

func (s Server) getPageSummary(chatID int64, url string) {
	defer func() {
		if err := recover(); err != nil {
			log.Println(string(debug.Stack()), err)
		}
	}()
	article, err := readability.FromURL(url, 30*time.Second)
	if err != nil {
		log.Fatalf("failed to parse %s, %v\n", url, err)
	}

	log.Printf("Page title	: %s\n", article.Title)
	log.Printf("Page content	: %d\n", len(article.TextContent))

	msg := openai.NewChatUserMessage(article.TextContent)
	system := openai.NewChatSystemMessage("Make a summary of the article. Try to be as brief as possible and highlight key points. Use markdown to annotate the summary.")

	history := []openai.ChatMessage{system, msg}

	response, err := s.ai.CreateChatCompletion("gpt-3.5-turbo-16k", history, openai.ChatCompletionOptions{}.SetUser(userAgent(31337)).SetTemperature(0.2))

	if err != nil {
		log.Printf("failed to create chat completion: %s", err)
		return
	}

	if _, err := s.bot.Send(tele.ChatID(chatID),
		response.Choices[0].Message.Content,
		"text",
		&tele.SendOptions{
			ParseMode: tele.ModeMarkdown,
		},
	); err != nil {
		log.Println(err)
	}
}
