package main

import (
	"encoding/json"
	"fmt"
	"github.com/go-shiori/go-readability"
	"github.com/meinside/openai-go"
	"github.com/tectiv3/chatgpt-bot/tools"
	tele "gopkg.in/telebot.v3"
	"log"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"
)

func (s *Server) getFunctionTools() []openai.ChatCompletionTool {
	return []openai.ChatCompletionTool{
		openai.NewChatCompletionTool(
			"search_images",
			"Search image or GIFs for a given query",
			openai.NewToolFunctionParameters().
				AddPropertyWithDescription("query", "string", "The query to search for").
				AddPropertyWithEnums("type", "string", []string{"photo", "gif"}, "The type of image to search for. Default to `photo` if not specified").
				AddPropertyWithEnums("region", "string",
					[]string{"xa-ar", "xa-en", "ar-es", "au-en", "at-de", "be-fr", "be-nl", "br-pt", "bg-bg",
						"ca-en", "ca-fr", "ct-ca", "cl-es", "cn-zh", "co-es", "hr-hr", "cz-cs", "dk-da",
						"ee-et", "fi-fi", "fr-fr", "de-de", "gr-el", "hk-tzh", "hu-hu", "in-en", "id-id",
						"id-en", "ie-en", "il-he", "it-it", "jp-jp", "kr-kr", "lv-lv", "lt-lt", "xl-es",
						"my-ms", "my-en", "mx-es", "nl-nl", "nz-en", "no-no", "pe-es", "ph-en", "ph-tl",
						"pl-pl", "pt-pt", "ro-ro", "ru-ru", "sg-en", "sk-sk", "sl-sl", "za-en", "es-es",
						"se-sv", "ch-de", "ch-fr", "ch-it", "tw-tzh", "th-th", "tr-tr", "ua-uk", "uk-en",
						"us-en", "ue-es", "ve-es", "vn-vi", "wt-wt"},
					"The region to use for the search. Infer this from the language used for the query. Default to `wt-wt` if not specified").
				SetRequiredParameters([]string{"query", "type", "region"}),
		),
		openai.NewChatCompletionTool(
			"web_search",
			"This is DuckDuckGo. Use this tool to search the internet. Input should be a string",
			openai.NewToolFunctionParameters().
				AddPropertyWithDescription("query", "string", "A query to search the web for").
				SetRequiredParameters([]string{"query"}),
		),
		openai.NewChatCompletionTool(
			"set_reminder",
			"Set a reminder to do something at a specific time.",
			openai.NewToolFunctionParameters().
				AddPropertyWithDescription("reminder", "string", "A reminder of what to do, e.g. 'buy groceries'").
				AddPropertyWithDescription("time", "number", "A time at which to be reminded in minutes from now, e.g. 1440").
				SetRequiredParameters([]string{"reminder", "time"}),
		),
		openai.NewChatCompletionTool(
			"make_summary",
			"Make a summary of a web page.",
			openai.NewToolFunctionParameters().
				AddPropertyWithDescription("url", "string", "A valid URL to a web page").
				SetRequiredParameters([]string{"url"}),
		),
		openai.NewChatCompletionTool(
			"get_crypto_rate",
			"Get the current rate of various crypto currencies",
			openai.NewToolFunctionParameters().
				AddPropertyWithDescription("asset", "string", "Asset of the crypto").
				SetRequiredParameters([]string{"asset"})),
	}
}

func (s *Server) handleFunctionCall(c tele.Context, result openai.ChatMessage) (string, error) {
	// refactor to handle multiple function calls not just the first one
	for _, toolCall := range result.ToolCalls {
		function := toolCall.Function

		if function.Name == "" {
			err := fmt.Sprint("there was no returned function call name")
			log.Println(err)

			return err, fmt.Errorf(err)
		}

		switch function.Name {
		case "search_images":
			type parsed struct {
				Query  string `json:"query"`
				Type   string `json:"type"`
				Region string `json:"region"`
			}
			var arguments parsed
			if err := toolCall.ArgumentsInto(&arguments); err != nil {
				err := fmt.Errorf("failed to parse arguments into struct: %s", err)
				return "", err
			} else {
				log.Printf("Will call %s(\"%s\", \"%s\", \"%s\")", function.Name, arguments.Query, arguments.Type, arguments.Region)
				param, err := tools.NewSearchImageParam(arguments.Query, arguments.Region, arguments.Type)
				if err != nil {
					return "", err
				}
				result := tools.SearchImages(param)
				if result.IsErr() {
					return "", result.Error()
				}
				res := *result.Unwrap()
				if len(res) == 0 {
					return "No results found", nil
				}
				img := tele.FromURL(res[0].Image)
				return "Got it", c.Send(&tele.Photo{
					File:    img,
					Caption: fmt.Sprintf("%s\n%s", res[0].Title, res[0].Link),
				}, "photo", replyMenu)
			}
		case "web_search":
			type parsed struct {
				Query string `json:"query"`
			}
			var arguments parsed
			if err := toolCall.ArgumentsInto(&arguments); err != nil {
				err := fmt.Errorf("failed to parse arguments into struct: %s", err)
				return "", err
			} else {
				log.Printf("Will call %s(\"%s\")", function.Name, arguments.Query)
				param, err := tools.NewSearchParam(arguments.Query)
				if err != nil {
					return "", err
				}
				result := tools.Search(param)
				if result.IsErr() {
					return "", result.Error()
				}
				res := *result.Unwrap()
				if len(res) == 0 {
					return "No results found", nil
				}
				// shuffle the results
				//rand.Shuffle(len(res), func(i, j int) { res[i], res[j] = res[j], res[i] })

				return fmt.Sprintf("%s\n%s\n%s", res[0].Title, res[0].Snippet, res[0].Link), nil
			}
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

func (s *Server) setReminder(chatID int64, reminder string, minutes int64) error {
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

func (s *Server) getPageSummary(chatID int64, url string) {
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

func (s *Server) getCryptoRate(asset string) (string, error) {
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
