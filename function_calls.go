package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/go-shiori/go-readability"
	"github.com/meinside/openai-go"
	"github.com/tectiv3/chatgpt-bot/i18n"
	"github.com/tectiv3/chatgpt-bot/tools"
	"github.com/tectiv3/chatgpt-bot/vectordb"
	tele "gopkg.in/telebot.v3"
	"log"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"
)

func (s *Server) getFunctionTools() []openai.ChatCompletionTool {
	return []openai.ChatCompletionTool{
		openai.NewChatCompletionTool(
			"search_images",
			"Search image or GIFs for a given query",
			openai.NewToolFunctionParameters().
				AddPropertyWithDescription("query", "string", "The query to search for").
				AddPropertyWithEnums("type", "string", "The type of image to search for. Default to `photo` if not specified", []string{"photo", "gif"}).
				AddPropertyWithEnums("region", "string",
					"The region to use for the search. Infer this from the language used for the query. Default to `wt-wt` if not specified or can not be inferred. Do not leave it empty.",
					[]string{"xa-ar", "xa-en", "ar-es", "au-en", "at-de", "be-fr", "be-nl", "br-pt", "bg-bg",
						"ca-en", "ca-fr", "ct-ca", "cl-es", "cn-zh", "co-es", "hr-hr", "cz-cs", "dk-da",
						"ee-et", "fi-fi", "fr-fr", "de-de", "gr-el", "hk-tzh", "hu-hu", "in-en", "id-id",
						"id-en", "ie-en", "il-he", "it-it", "jp-jp", "kr-kr", "lv-lv", "lt-lt", "xl-es",
						"my-ms", "my-en", "mx-es", "nl-nl", "nz-en", "no-no", "pe-es", "ph-en", "ph-tl",
						"pl-pl", "pt-pt", "ro-ro", "ru-ru", "sg-en", "sk-sk", "sl-sl", "za-en", "es-es",
						"se-sv", "ch-de", "ch-fr", "ch-it", "tw-tzh", "th-th", "tr-tr", "ua-uk", "uk-en",
						"us-en", "ue-es", "ve-es", "vn-vi", "wt-wt"}).
				SetRequiredParameters([]string{"query", "type", "region"}),
		),
		openai.NewChatCompletionTool(
			"web_search",
			"This is web search. Use this tool to search the internet. Use it when you need access to real time information. The top 10 results will be added to the vector db. The top 3 results are also getting returned to you directly. For more search queries through the same websites, use the vector_search tool. Input should be a string. Append sources to the response.",
			openai.NewToolFunctionParameters().
				AddPropertyWithDescription("query", "string", "A query to search the web for").
				AddPropertyWithEnums("region", "string",
					"The region to use for the search. Infer this from the language used for the query. Default to `wt-wt` if not specified or can not be inferred. Do not leave it empty.",
					[]string{"xa-ar", "xa-en", "ar-es", "au-en", "at-de", "be-fr", "be-nl", "br-pt", "bg-bg",
						"ca-en", "ca-fr", "ct-ca", "cl-es", "cn-zh", "co-es", "hr-hr", "cz-cs", "dk-da",
						"ee-et", "fi-fi", "fr-fr", "de-de", "gr-el", "hk-tzh", "hu-hu", "in-en", "id-id",
						"id-en", "ie-en", "il-he", "it-it", "jp-jp", "kr-kr", "lv-lv", "lt-lt", "xl-es",
						"my-ms", "my-en", "mx-es", "nl-nl", "nz-en", "no-no", "pe-es", "ph-en", "ph-tl",
						"pl-pl", "pt-pt", "ro-ro", "ru-ru", "sg-en", "sk-sk", "sl-sl", "za-en", "es-es",
						"se-sv", "ch-de", "ch-fr", "ch-it", "tw-tzh", "th-th", "tr-tr", "ua-uk", "uk-en",
						"us-en", "ue-es", "ve-es", "vn-vi", "wt-wt"}).
				SetRequiredParameters([]string{"query", "region"}),
		),
		openai.NewChatCompletionTool(
			"text_to_speech",
			"Convert text to speech.",
			openai.NewToolFunctionParameters().
				AddPropertyWithDescription("query", "string", "A text to convert to speech.").
				AddPropertyWithEnums("language", "string",
					"The language to use for the speech synthesis. Default to `en` if could not be detected.",
					[]string{"fr", "ru", "en", "ja"}).
				SetRequiredParameters([]string{"query", "language"}),
		),
		openai.NewChatCompletionTool(
			"vector_search",
			`Useful for searching through added files and websites. Search for keywords in the text not whole questions, avoid relative words like "yesterday" think about what could be in the text. The input to this tool will be run against a vector db. The top results will be returned as json.`,
			openai.NewToolFunctionParameters().
				AddPropertyWithDescription("query", "string", "A query to search the vector db").
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

func (s *Server) handleFunctionCall(chat *Chat, c tele.Context, response openai.ChatMessage) (string, error) {
	// refactor to handle multiple function calls not just the first one
	result := ""
	var resultErr error
	var toolID string
	sentMessage := chat.getSentMessage(c)
	for _, toolCall := range response.ToolCalls {
		function := toolCall.Function
		if function.Name == "" {
			err := fmt.Sprint("there was no returned function call name")
			resultErr = fmt.Errorf(err)
			continue
		}
		if !s.conf.Verbose {
			slog.Info("Function call", "name", function.Name, "user", c.Sender().Username)
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
			}
			if s.conf.Verbose {
				slog.Info("Will call", "name", function.Name, "query", arguments.Query, "type", arguments.Type, "region", arguments.Region)
			}
			_, _ = c.Bot().Edit(&sentMessage,
				fmt.Sprintf(chat.t("Action: {{.tool}}\nAction input: %s", &i18n.Replacements{"tool": chat.t(function.Name)}), arguments.Query),
			)
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
				return "", fmt.Errorf("no results found")
			}
			img := tele.FromURL(res[0].Image)
			return "Got it", c.Send(&tele.Photo{
				File:    img,
				Caption: fmt.Sprintf("%s\n%s", res[0].Title, res[0].Link),
			})

		case "web_search":
			type parsed struct {
				Query  string `json:"query"`
				Region string `json:"region"`
			}
			var arguments parsed
			if err := toolCall.ArgumentsInto(&arguments); err != nil {
				err := fmt.Errorf("failed to parse arguments into struct: %s", err)
				return "", err
			}

			_, _ = c.Bot().Edit(&sentMessage,
				fmt.Sprintf(chat.t("Action: {{.tool}}\nAction input: %s", &i18n.Replacements{"tool": chat.t(function.Name)}), arguments.Query),
			)

			if s.conf.Verbose {
				slog.Info("Will call", "function", function.Name, "query", arguments.Query, "region", arguments.Region)
			}
			var err error
			result, err = s.webSearchSearX(arguments.Query, arguments.Region, c.Sender().Username)
			if err != nil {
				slog.Warn("Failed to search web", "error", err)
				continue
			}
			resultErr = nil
			toolID = toolCall.ID
			response.Role = openai.ChatMessageRoleAssistant
			chat.addMessageToHistory(response)

		case "vector_search":
			type parsed struct {
				Query string `json:"query"`
			}
			var arguments parsed
			if err := toolCall.ArgumentsInto(&arguments); err != nil {
				err := fmt.Errorf("failed to parse arguments into struct: %s", err)
				return "", err
			}
			if s.conf.Verbose {
				slog.Info("Will call", "function", function.Name, "query", arguments.Query)
			}
			_, _ = c.Bot().Edit(&sentMessage,
				fmt.Sprintf(chat.t("Action: {{.tool}}\nAction input: %s", &i18n.Replacements{"tool": chat.t(function.Name)}), arguments.Query),
			)
			var err error
			result, err = s.vectorSearch(arguments.Query, c.Sender().Username)
			if err != nil {
				slog.Warn("Failed to search vector", "error", err)
				continue
			}
			resultErr = nil
			toolID = toolCall.ID
			response.Role = openai.ChatMessageRoleAssistant
			chat.addMessageToHistory(response)

		case "text_to_speech":
			type parsed struct {
				Query    string `json:"query"`
				Language string `json:"language"`
			}
			var arguments parsed
			if err := toolCall.ArgumentsInto(&arguments); err != nil {
				err := fmt.Errorf("failed to parse arguments into struct: %s", err)
				return "", err
			}
			if s.conf.Verbose {
				slog.Info("Will call", "function", function.Name, "query", arguments.Query, "language", arguments.Language)
			}
			_, _ = c.Bot().Edit(&sentMessage, fmt.Sprintf(chat.t("Action: {{.tool}}\nAction input: %s", &i18n.Replacements{"tool": chat.t(function.Name)}), arguments.Query))

			return "", s.textToSpeech(c, arguments.Query, arguments.Language)

		case "set_reminder":
			type parsed struct {
				Reminder string `json:"reminder"`
				Minutes  int64  `json:"time"`
			}
			var arguments parsed
			if err := toolCall.ArgumentsInto(&arguments); err != nil {
				err := fmt.Errorf("failed to parse arguments into struct: %s", err)
				return "", err
			}
			if s.conf.Verbose {
				slog.Info("Will call", "function", function.Name, "reminder", arguments.Reminder, "minutes", arguments.Minutes)
			}
			_, _ = c.Bot().Edit(&sentMessage,
				fmt.Sprintf(chat.t("Action: {{.tool}}\nAction input: %s", &i18n.Replacements{"tool": chat.t(function.Name)}), arguments.Reminder+","+strconv.Itoa(int(arguments.Minutes))),
			)

			if err := s.setReminder(c.Chat().ID, arguments.Reminder, arguments.Minutes); err != nil {
				return "", err
			}

			return fmt.Sprintf("Reminder set for %d minutes from now", arguments.Minutes), nil

		case "make_summary":
			type parsed struct {
				URL string `json:"url"`
			}
			var arguments parsed
			if err := toolCall.ArgumentsInto(&arguments); err != nil {
				err := fmt.Errorf("failed to parse arguments into struct: %s", err)
				return "", err
			}
			if s.conf.Verbose {
				log.Printf("Will call %s(\"%s\")", function.Name, arguments.URL)
			}
			go s.getPageSummary(c.Chat().ID, arguments.URL)

			return "Downloading summary. Please wait.", nil

		case "get_crypto_rate":
			type parsed struct {
				Asset string `json:"asset"`
			}
			var arguments parsed
			if err := toolCall.ArgumentsInto(&arguments); err != nil {
				err := fmt.Errorf("failed to parse arguments into struct: %s", err)
				return "", err
			}
			if s.conf.Verbose {
				slog.Info("Will call", "function", function.Name, "asset", arguments.Asset)
			}
			_, _ = c.Bot().Edit(&sentMessage,
				fmt.Sprintf(chat.t("Action: {{.tool}}\nAction input: %s", &i18n.Replacements{"tool": chat.t(function.Name)}), arguments.Asset))

			return s.getCryptoRate(arguments.Asset)
		}
	}

	if len(result) == 0 {
		return "", resultErr
	}
	chat.addToolResultToHistory(toolID, result)

	if chat.Stream {
		return s.getStreamAnswer(chat, c, chat.getConversationContext(nil, nil))
	}

	return s.getAnswer(chat, c, chat.getConversationContext(nil, nil), false)
}

func (s *Server) setReminder(chatID int64, reminder string, minutes int64) error {
	timer := time.NewTimer(time.Minute * time.Duration(minutes))
	go func() {
		<-timer.C
		_, _ = s.bot.Send(tele.ChatID(chatID), reminder)
	}()

	return nil
}

func (s *Server) getPageSummary(chatID int64, url string) {
	defer func() {
		if err := recover(); err != nil {
			slog.Error("Panic", "stack", string(debug.Stack()), "error", err)
		}
	}()
	article, err := readability.FromURL(url, 30*time.Second)
	if err != nil {
		log.Fatalf("failed to parse %s, %v\n", url, err)
	}

	if s.conf.Verbose {
		slog.Info("Page", "title", article.Title, "content", len(article.TextContent))
	}

	msg := openai.NewChatUserMessage(article.TextContent)
	// You are acting as a summarization AI, and for the input text please summarize it to the most important 3 to 5 bullet points for brevity:
	system := openai.NewChatSystemMessage("Make a summary of the article. Try to be as brief as possible and highlight key points. Use markdown to annotate the summary.")

	history := []openai.ChatMessage{system, msg}

	response, err := s.ai.CreateChatCompletion("gpt-3.5-turbo-16k", history, openai.ChatCompletionOptions{}.SetUser(userAgent(31337)).SetTemperature(0.2))

	if err != nil {
		slog.Warn("failed to create chat completion", "error", err)
		return
	}
	chat := s.getChatByID(chatID)
	chat.TotalTokens += response.Usage.TotalTokens
	str, _ := response.Choices[0].Message.ContentString()
	chat.addMessageToHistory(openai.NewChatAssistantMessage(str))
	s.db.Save(&chat)

	if _, err := s.bot.Send(tele.ChatID(chatID),
		str,
		"text",
		&tele.SendOptions{ParseMode: tele.ModeMarkdown},
	); err != nil {
		slog.Error("Sending", "error", err)
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

func (s *Server) webSearchDDG(input, region, username string) (string, error) {
	param, err := tools.NewSearchParam(input, region)
	if err != nil {
		return "", err
	}
	result := tools.Search(param)
	if result.IsErr() {
		return "", result.Error()
	}
	res := *result.Unwrap()
	if len(res) == 0 {
		return "", fmt.Errorf("no results found")
	}
	slog.Info("Search found", "results", len(res))
	ctx := context.WithValue(context.Background(), "ollama", s.conf.OllamaEnabled)
	limit := 10

	wg := sync.WaitGroup{}
	counter := 0
	for i := range res {
		if counter > limit {
			break
		}
		// if result link ends in .pdf, skip
		if strings.HasSuffix(res[i].Link, ".pdf") {
			continue
		}

		counter += 1
		wg.Add(1)
		go func(i int) {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("Panic", "stack", string(debug.Stack()), "error", err)
				}
			}()
			err := vectordb.DownloadWebsiteToVectorDB(ctx, res[i].Link, username)
			if err != nil {
				slog.Warn("Error downloading website", "error", err)
				wg.Done()
				return
			}
			wg.Done()
		}(i)
	}
	wg.Wait()

	return s.vectorSearch(input, username)
}

func (s *Server) webSearchSearX(input, region, username string) (string, error) {
	input = strings.TrimSuffix(strings.TrimSuffix(strings.TrimPrefix(input, "\""), "\""), "?")
	//if region != "" && region != "wt-wt" {
	//	input += ":" + region
	//}
	res, err := tools.SearchSearX(input)
	if err != nil {
		return "", err
	}

	slog.Info("Search found", "results", len(res))
	ctx := context.WithValue(context.Background(), "ollama", s.conf.OllamaEnabled)
	limit := 10

	wg := sync.WaitGroup{}
	counter := 0
	for i := range res {
		if counter > limit {
			break
		}
		// if result link ends in .pdf, skip
		if strings.HasSuffix(res[i].URL, ".pdf") {
			continue
		}

		counter += 1
		wg.Add(1)
		go func(i int) {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("Panic", "stack", string(debug.Stack()), "error", err)
				}
			}()
			err := vectordb.DownloadWebsiteToVectorDB(ctx, res[i].URL, username)
			if err != nil {
				slog.Warn("Error downloading website", "error", err)
				wg.Done()
				return
			}
			wg.Done()
		}(i)
	}
	wg.Wait()

	return s.vectorSearch(input, username)
}

func (s *Server) vectorSearch(input string, username string) (string, error) {
	ctx := context.Background()
	ctx = context.WithValue(ctx, "ollama", s.conf.OllamaEnabled)
	docs, err := vectordb.SearchVectorDB(ctx, input, username)
	type DocResult struct {
		Text   string
		Source string
	}
	var results []DocResult

	for _, r := range docs {
		newResult := DocResult{Text: r.PageContent}
		source, ok := r.Metadata["url"].(string)
		if ok {
			newResult.Source = source
		}

		results = append(results, newResult)
	}

	if len(docs) == 0 {
		response := "no results found. Try other db search keywords or download more websites."
		slog.Warn("no results found", "input", input)
		results = append(results, DocResult{Text: response})
	} else if len(results) == 0 {
		response := "No new results found, all returned results have been used already. Try other db search keywords or download more websites."
		results = append(results, DocResult{Text: response})
	}

	resultJson, err := json.Marshal(results)
	if err != nil {
		return "", err
	}

	return string(resultJson), nil
}
