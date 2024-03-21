package main

import (
	"encoding/json"
	"fmt"
	"github.com/go-shiori/go-readability"
	"github.com/meinside/openai-go"
	tele "gopkg.in/telebot.v3"
	"log"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"
)

func (s Server) handleFunctionCall(c tele.Context, result openai.ChatMessage) (string, error) {
	// refactor to handle multiple function calls not just the first one
	for _, toolCall := range result.ToolCalls {
		function := toolCall.Function

		if function.Name == "" {
			err := fmt.Sprint("there was no returned function call name")
			log.Println(err)

			return err, fmt.Errorf(err)
		}
		//if function.Arguments == nil {
		//	err := fmt.Sprint("there were no returned function call arguments")
		//	log.Println(err)
		//
		//	return err, fmt.Errorf(err)
		//}
		//arguments, _ := toolCall.ArgumentsParsed()

		switch function.Name {
		case "set_reminder":
			type parsed struct {
				Reminder string `json:"reminder"`
				Minutes  int64  `json:"time"`
			}
			var arguments parsed
			if err := toolCall.ArgumentsInto(&arguments); err != nil {
				err := fmt.Errorf("failed to parse arguments into struct: %s", err)
				return "", err
			} else {
				log.Printf("Will call %s(\"%s\", %d)", function.Name, arguments.Reminder, arguments.Minutes)

				if err := s.setReminder(c.Chat().ID, arguments.Reminder, arguments.Minutes); err != nil {
					return "", err
				}
			}

			return "Reminder set", nil
		case "make_summary":
			type parsed struct {
				URL string `json:"url"`
			}
			var arguments parsed
			if err := toolCall.ArgumentsInto(&arguments); err != nil {
				err := fmt.Errorf("failed to parse arguments into struct: %s", err)
				return "", err
			} else {
				log.Printf("Will call %s(\"%s\")", function.Name, arguments.URL)

				go s.getPageSummary(c.Chat().ID, arguments.URL)

				return "Downloading summary. Please wait.", nil
			}
		case "get_crypto_rate":
			type parsed struct {
				Asset string `json:"asset"`
			}
			var arguments parsed
			if err := toolCall.ArgumentsInto(&arguments); err != nil {
				err := fmt.Errorf("failed to parse arguments into struct: %s", err)
				return "", err
			} else {
				log.Printf("Will call %s(\"%s\")", function.Name, arguments.Asset)

				return s.getCryptoRate(arguments.Asset)
			}
		}
		arguments, _ := toolCall.ArgumentsParsed()
		log.Printf("Got a function call %s(%v)", function.Name, arguments)

		return fmt.Sprintf("Function call in response (%s)", function.Name), nil
	}

	return "", nil
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
	// You are acting as a summarization AI, and for the input text please summarize it to the most important 3 to 5 bullet points for brevity:
	system := openai.NewChatSystemMessage("Make a summary of the article. Try to be as brief as possible and highlight key points. Use markdown to annotate the summary.")

	history := []openai.ChatMessage{system, msg}

	response, err := s.ai.CreateChatCompletion("gpt-3.5-turbo-16k", history, openai.ChatCompletionOptions{}.SetUser(userAgent(31337)).SetTemperature(0.2))

	if err != nil {
		log.Printf("failed to create chat completion: %s", err)
		return
	}
	chat := s.getChat(chatID, "")
	chat.TotalTokens += response.Usage.TotalTokens
	s.db.Save(&chat)
	str, _ := response.Choices[0].Message.ContentString()
	if _, err := s.bot.Send(tele.ChatID(chatID),
		str,
		"text",
		&tele.SendOptions{ParseMode: tele.ModeMarkdown},
	); err != nil {
		log.Println(err)
	}
}

func (s Server) getCryptoRate(asset string) (string, error) {
	asset = strings.ToLower(asset)
	format := "$%0.0f"
	switch asset {
	case "btc":
		asset = "bitcoin"
	case "eth":
		asset = "ethereum"
	case "ltc":
		asset = "litecoin"
	case "xrp":
		asset = "ripple"
		format = "$%0.3f"
	case "xlm":
		asset = "stellar"
		format = "$%0.3f"
	case "ada":
		asset = "cardano"
		format = "$%0.3f"
	}
	client := &http.Client{}
	client.Timeout = 10 * time.Second
	req, err := http.NewRequest("GET", fmt.Sprintf("https://api.coincap.io/v2/assets/%s", asset), nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var symbol CoinCap
	err = json.NewDecoder(resp.Body).Decode(&symbol)
	if err != nil {
		return "", err
	}
	price, _ := strconv.ParseFloat(symbol.Data.PriceUsd, 64)

	return fmt.Sprintf(format, price), nil
}
