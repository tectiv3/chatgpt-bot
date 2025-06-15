package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-shiori/go-readability"
	"github.com/meinside/openai-go"
	"github.com/tectiv3/chatgpt-bot/i18n"
	"github.com/tectiv3/chatgpt-bot/tools"
	"github.com/tectiv3/chatgpt-bot/vectordb"
	tele "gopkg.in/telebot.v3"
)

func (s *Server) getFunctionTools() []openai.ChatCompletionTool {
	availableTools := []openai.ChatCompletionTool{
		/*
			openai.NewChatCompletionTool(
				"search_images",
				"Search image or GIFs for a given query",
				openai.NewToolFunctionParameters().
					AddPropertyWithDescription("query", "string", "The query to search for").
					AddPropertyWithEnums("type", "string", "The type of image to search for. Default to `photo` if not specified", []string{"photo", "gif"}).
					AddPropertyWithEnums("region", "string",
						"The region to use for the search. Infer this from the language used for the query. Default to `wt-wt` if not specified or can not be inferred. Do not leave it empty.",
						[]string{
							"xa-ar", "xa-en", "ar-es", "au-en", "at-de", "be-fr", "be-nl", "br-pt", "bg-bg",
							"ca-en", "ca-fr", "ct-ca", "cl-es", "cn-zh", "co-es", "hr-hr", "cz-cs", "dk-da",
							"ee-et", "fi-fi", "fr-fr", "de-de", "gr-el", "hk-tzh", "hu-hu", "in-en", "id-id",
							"id-en", "ie-en", "il-he", "it-it", "jp-jp", "kr-kr", "lv-lv", "lt-lt", "xl-es",
							"my-ms", "my-en", "mx-es", "nl-nl", "nz-en", "no-no", "pe-es", "ph-en", "ph-tl",
							"pl-pl", "pt-pt", "ro-ro", "ru-ru", "sg-en", "sk-sk", "sl-sl", "za-en", "es-es",
							"se-sv", "ch-de", "ch-fr", "ch-it", "tw-tzh", "th-th", "tr-tr", "ua-uk", "uk-en",
							"us-en", "ue-es", "ve-es", "vn-vi", "wt-wt",
						}).
					SetRequiredParameters([]string{"query", "type", "region"}),
			),
			openai.NewChatCompletionTool(
				"web_search",
				"This is web search. Use this tool to search the internet. Use it when you need access to real time information. The top 10 results will be added to the vector db. The top 3 results are also getting returned to you directly. For more search queries through the same websites, use the vector_search tool. Input should be a string. Append sources to the response.",
				openai.NewToolFunctionParameters().
					AddPropertyWithDescription("query", "string", "A query to search the web for").
					// AddPropertyWithEnums("region", "string",
					//	"The region to use for the search. Infer this from the language used for the query. Default to `wt-wt` if not specified or can not be inferred. Do not leave it empty.",
					//	[]string{"xa-ar", "xa-en", "ar-es", "au-en", "at-de", "be-fr", "be-nl", "br-pt", "bg-bg",
					//		"ca-en", "ca-fr", "ct-ca", "cl-es", "cn-zh", "co-es", "hr-hr", "cz-cs", "dk-da",
					//		"ee-et", "fi-fi", "fr-fr", "de-de", "gr-el", "hk-tzh", "hu-hu", "in-en", "id-id",
					//		"id-en", "ie-en", "il-he", "it-it", "jp-jp", "kr-kr", "lv-lv", "lt-lt", "xl-es",
					//		"my-ms", "my-en", "mx-es", "nl-nl", "nz-en", "no-no", "pe-es", "ph-en", "ph-tl",
					//		"pl-pl", "pt-pt", "ro-ro", "ru-ru", "sg-en", "sk-sk", "sl-sl", "za-en", "es-es",
					//		"se-sv", "ch-de", "ch-fr", "ch-it", "tw-tzh", "th-th", "tr-tr", "ua-uk", "uk-en",
					//		"us-en", "ue-es", "ve-es", "vn-vi", "wt-wt"}).
					SetRequiredParameters([]string{"query"}),
			),
		*/
		// openai.NewChatCompletionTool(
		// 	"text_to_speech",
		// 	"Convert provided text to speech.",
		// 	openai.NewToolFunctionParameters().
		// 		AddPropertyWithDescription("text", "string", "A text to use.").
		// 		AddPropertyWithEnums("language", "string",
		// 			"The language to use for the speech synthesis. Default to `en` if could not be detected.",
		// 			[]string{"fr", "ru", "en", "ja", "ua", "de", "es", "it", "tw"}).
		// 		SetRequiredParameters([]string{"text", "language"}),
		// ),
		/*
			openai.NewChatCompletionTool(
				"full_webpage_to_speech",
				"Download full web page and convert it to speech. Use ONLY when you need to pass the full content of a web page to the speech synthesiser.",
				openai.NewToolFunctionParameters().
					AddPropertyWithDescription("url", "string", "A valid URL to a web page, should not end in PDF.").
					SetRequiredParameters([]string{"url"}),
			),
			openai.NewChatCompletionTool(
				"vector_search",
				`Useful for searching through added files and websites. Search for keywords in the text not whole questions, avoid relative words like "yesterday" think about what could be in the text. The input to this tool will be run against a vector db. The top results will be returned as json.`,
				openai.NewToolFunctionParameters().
					AddPropertyWithDescription("query", "string", "A query to search the vector db").
					SetRequiredParameters([]string{"query"}),
			),
		*/
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
				SetRequiredParameters([]string{"asset"}),
		),
	}

	// availableTools = append(availableTools,
	// 	openai.NewChatCompletionTool(
	// 		"generate_image",
	// 		"Generate an image based on the input text",
	// 		openai.NewToolFunctionParameters().
	// 			AddPropertyWithDescription("text", "string", "The text to generate an image from").
	// 			AddPropertyWithDescription("hd", "boolean", "Whether to generate an HD image. Default to false.").
	// 			SetRequiredParameters([]string{"text", "hd"}),
	// 	),
	// )

	return availableTools
}

func (s *Server) handleResponseFunctionCalls(chat *Chat, c tele.Context, functions []openai.ResponseOutput) (string, error) {
	return "", nil
}

func (s *Server) handleFunctionCall(chat *Chat, c tele.Context, response openai.ChatMessage) (string, error) {
	// refactor to handle multiple function calls not just the first one
	result := ""
	var resultErr error
	var toolID string
	sentMessage := chat.getSentMessage(c)
	toolCallsCount := len(response.ToolCalls)
	reply := ""
	for i, toolCall := range response.ToolCalls {
		function := toolCall.Function
		if function.Name == "" {
			err := fmt.Sprint("there was no returned function call name")
			resultErr = fmt.Errorf(err)
			continue
		}
		Log.WithField("tools", toolCallsCount).WithField("tool", i).WithField("function", function.Name).WithField("user", c.Sender().Username).Info("Function call")

		switch function.Name {
		case "search_images":
			type parsed struct {
				Query  string `json:"query"`
				Type   string `json:"type"`
				Region string `json:"region"`
			}
			var arguments parsed
			if err := toolCall.ArgumentsInto(&arguments); err != nil {
				resultErr = fmt.Errorf("failed to parse arguments into struct: %s", err)
				continue
			}
			if s.conf.Verbose {
				Log.Info("Will call ", function.Name, "(", arguments.Query, ", ", arguments.Type, ", ", arguments.Region, ")")
			}
			_, _ = c.Bot().Edit(sentMessage,
				fmt.Sprintf(chat.t("Action: {{.tool}}\nAction input: %s", &i18n.Replacements{"tool": chat.t(function.Name)}), arguments.Query),
			)
			param, err := tools.NewSearchImageParam(arguments.Query, arguments.Region, arguments.Type)
			if err != nil {
				resultErr = err
				continue
			}
			result := tools.SearchImages(param)
			if result.IsErr() {
				resultErr = result.Error()
				continue
			}
			res := *result.Unwrap()
			if len(res) == 0 {
				resultErr = fmt.Errorf("no results found")
				continue
			}
			img := tele.FromURL(res[0].Image)
			return "", c.Send(&tele.Photo{
				File:    img,
				Caption: fmt.Sprintf("%s\n%s", res[0].Title, res[0].Link),
			})

		case "web_search":
			type parsed struct {
				Query string `json:"query"`
				// Region string `json:"region"`
			}
			var arguments parsed
			if err := toolCall.ArgumentsInto(&arguments); err != nil {
				resultErr = fmt.Errorf("failed to parse arguments into struct: %s", err)
				continue
			}
			if len(reply) > 0 {
				reply += "\n"
			}
			reply += fmt.Sprintf(chat.t("Action: {{.tool}}\nAction input: %s", &i18n.Replacements{"tool": chat.t(function.Name)}), arguments.Query)
			_, _ = c.Bot().Edit(sentMessage, reply)

			if s.conf.Verbose {
				Log.Info("Will call ", function.Name, "(", arguments.Query, ")")
			}
			var err error
			result, err = s.webSearchSearX(arguments.Query, "wt-wt", c.Sender().Username)
			if err != nil {
				Log.Warn("Failed to search web", "error=", err)
				continue
			}
			resultErr = nil
			toolID = toolCall.ID
			response.Role = openai.ChatMessageRoleAssistant
			chat.addMessageToDialog(response)

		case "vector_search":
			type parsed struct {
				Query string `json:"query"`
			}
			var arguments parsed
			if err := toolCall.ArgumentsInto(&arguments); err != nil {
				resultErr = fmt.Errorf("failed to parse arguments into struct: %s", err)
				continue
			}
			if s.conf.Verbose {
				Log.Info("Will call ", function.Name, "(", arguments.Query, ")")
			}
			if len(reply) > 0 {
				reply += "\n"
			}
			reply += fmt.Sprintf(chat.t("Action: {{.tool}}\nAction input: %s", &i18n.Replacements{"tool": chat.t(function.Name)}), arguments.Query)
			_, _ = c.Bot().Edit(sentMessage, reply)
			var err error
			result, err = s.vectorSearch(arguments.Query, c.Sender().Username)
			if err != nil {
				Log.Warn("Failed to search vector", "error=", err)
				continue
			}
			resultErr = nil
			toolID = toolCall.ID
			response.Role = openai.ChatMessageRoleAssistant
			chat.addMessageToDialog(response)

		case "text_to_speech":
			type parsed struct {
				Text     string `json:"text"`
				Language string `json:"language"`
			}
			var arguments parsed
			if err := toolCall.ArgumentsInto(&arguments); err != nil {
				resultErr = fmt.Errorf("failed to parse arguments into struct: %s", err)
				continue
			}
			if s.conf.Verbose {
				Log.Info("Will call ", function.Name, "(", arguments.Text, ", ", arguments.Language, ")")
			}
			if len(reply) > 0 {
				reply += "\n"
			}
			reply += fmt.Sprintf(chat.t("Action: {{.tool}}\nAction input: %s", &i18n.Replacements{"tool": chat.t(function.Name)}), arguments.Text)
			_, _ = c.Bot().Edit(sentMessage, reply)

			go s.textToSpeech(c, arguments.Text, arguments.Language)

		case "web_to_speech":
			type parsed struct {
				URL string `json:"url"`
			}
			var arguments parsed
			if err := toolCall.ArgumentsInto(&arguments); err != nil {
				resultErr = fmt.Errorf("failed to parse arguments into struct: %s", err)
				continue
			}
			if s.conf.Verbose {
				Log.Info("Will call ", function.Name, "(", arguments.URL, ")")
			}
			_, _ = c.Bot().Edit(sentMessage,
				fmt.Sprintf(chat.t("Action: {{.tool}}\nAction input: %s", &i18n.Replacements{"tool": chat.t(function.Name)}), arguments.URL),
			)

			go s.pageToSpeech(c, arguments.URL)

			return "", nil

		case "generate_image":
			type parsed struct {
				Text string `json:"text"`
				HD   bool   `json:"hd"`
			}
			var arguments parsed
			if err := toolCall.ArgumentsInto(&arguments); err != nil {
				resultErr = fmt.Errorf("failed to parse arguments into struct: %s", err)
				continue
			}
			if s.conf.Verbose {
				Log.WithField("user", c.Sender().Username).Info("Will call ", function.Name, "(", arguments.Text, ", ", arguments.HD, ")")
			}
			_, _ = c.Bot().Edit(sentMessage,
				fmt.Sprintf(chat.t("Action: {{.tool}}\nAction input: %s", &i18n.Replacements{"tool": chat.t(function.Name)}), arguments.Text),
			)

			if err := s.textToImage(c, arguments.Text, arguments.HD); err != nil {
				Log.WithField("user", c.Sender().Username).Warn(err)
			} else {
				continue
			}

		case "set_reminder":
			type parsed struct {
				Reminder string `json:"reminder"`
				Minutes  int64  `json:"time"`
			}
			var arguments parsed
			if err := toolCall.ArgumentsInto(&arguments); err != nil {
				resultErr = fmt.Errorf("failed to parse arguments into struct: %s", err)
				continue
			}
			if s.conf.Verbose {
				Log.Info("Will call ", function.Name, "(", arguments.Reminder, ", ", arguments.Minutes, ")")
			}
			_, _ = c.Bot().Edit(sentMessage,
				fmt.Sprintf(chat.t("Action: {{.tool}}\nAction input: %s", &i18n.Replacements{"tool": chat.t(function.Name)}), arguments.Reminder+","+strconv.Itoa(int(arguments.Minutes))),
			)

			if err := s.setReminder(c.Chat().ID, arguments.Reminder, arguments.Minutes); err != nil {
				resultErr = fmt.Errorf("failed to set reminder: %s", err)
				continue
			}

			return fmt.Sprintf("Reminder set for %d minutes from now", arguments.Minutes), nil

		case "make_summary":
			type parsed struct {
				URL string `json:"url"`
			}
			var arguments parsed
			if err := toolCall.ArgumentsInto(&arguments); err != nil {
				resultErr = fmt.Errorf("failed to parse arguments into struct: %s", err)
				continue
			}
			if s.conf.Verbose {
				Log.Info("Will call ", function.Name, "(", arguments.URL, ")")
			}
			_, _ = c.Bot().Edit(sentMessage,
				fmt.Sprintf(chat.t("Action: {{.tool}}\nAction input: %s", &i18n.Replacements{"tool": chat.t(function.Name)}), arguments.URL),
			)
			go s.getPageSummary(chat, arguments.URL)
			continue

		case "get_crypto_rate":
			type parsed struct {
				Asset string `json:"asset"`
			}
			var arguments parsed
			if err := toolCall.ArgumentsInto(&arguments); err != nil {
				resultErr = fmt.Errorf("failed to parse arguments into struct: %s", err)
				continue
			}
			if s.conf.Verbose {
				Log.Info("Will call ", function.Name, "(", arguments.Asset, ")")
			}
			_, _ = c.Bot().Edit(sentMessage,
				fmt.Sprintf(chat.t("Action: {{.tool}}\nAction input: %s", &i18n.Replacements{"tool": chat.t(function.Name)}), arguments.Asset))

			return s.getCryptoRate(arguments.Asset)
		}
	}

	if len(result) == 0 {
		s.saveHistory(chat)
		return "", resultErr
	}
	chat.addToolResultToDialog(toolID, result)

	if chat.Stream {
		_ = s.getStreamAnswer(chat, c, nil)
		return "", nil
	}

	err := s.getAnswer(chat, c, nil)
	return "", err
}

func (s *Server) setReminder(chatID int64, reminder string, minutes int64) error {
	timer := time.NewTimer(time.Minute * time.Duration(minutes))
	go func() {
		<-timer.C
		_, _ = s.bot.Send(tele.ChatID(chatID), reminder)
	}()

	return nil
}

func (s *Server) pageToSpeech(c tele.Context, url string) {
	defer func() {
		if err := recover(); err != nil {
			Log.WithField("error", err).Error("panic: ", string(debug.Stack()))
		}
	}()

	article, err := readability.FromURL(url, 30*time.Second)
	if err != nil {
		Log.Fatalf("failed to parse %s, %v\n", url, err)
	}

	if s.conf.Verbose {
		Log.Info("Page title=", article.Title, ", content=", len(article.TextContent))
	}
	_ = c.Notify(tele.Typing)

	s.sendAudio(c, article.TextContent)
}

func (s *Server) getPageSummary(chat *Chat, url string) {
	defer func() {
		if err := recover(); err != nil {
			Log.WithField("error", err).Error("panic: ", string(debug.Stack()))
		}
	}()
	article, err := readability.FromURL(url, 30*time.Second)
	if err != nil {
		Log.Fatalf("failed to parse %s, %v\n", url, err)
	}

	if s.conf.Verbose {
		Log.Info("Page title=", article.Title, ", content=", len(article.TextContent))
	}

	msg := openai.NewChatUserMessage(article.TextContent)
	// You are acting as a summarization AI, and for the input text please summarize it to the most important 3 to 5 bullet points for brevity:
	system := openai.NewChatSystemMessage("Make a summary of the article. Try to be as brief as possible and highlight key points. Use markdown to annotate the summary.")

	history := []openai.ChatMessage{system, msg}

	response, err := s.openAI.CreateChatCompletion(miniModel, history, openai.ChatCompletionOptions{}.SetUser(userAgent(31337)).SetTemperature(0.2))
	if err != nil {
		Log.Warn("failed to create chat completion", "error=", err)
		s.bot.Send(tele.ChatID(chat.ChatID), err.Error(), "text", replyMenu)
		return
	}

	chat.TotalTokens += response.Usage.TotalTokens
	str, _ := response.Choices[0].Message.ContentString()
	chat.addMessageToDialog(openai.NewChatAssistantMessage(str))
	s.db.Save(&chat)

	if _, err := s.bot.Send(tele.ChatID(chat.ChatID),
		str,
		"text",
		&tele.SendOptions{ParseMode: tele.ModeMarkdown},
		replyMenu,
	); err != nil {
		Log.Error("Sending", "error=", err)
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
	case "sol":
		asset = "solana"
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
	Log.Info("Search results found=", len(res))
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
					Log.WithField("error", err).Error("panic: ", string(debug.Stack()))
				}
			}()
			err := vectordb.DownloadWebsiteToVectorDB(ctx, res[i].Link, username)
			if err != nil {
				Log.Warn("Error downloading website", "error=", err)
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

	Log.Info("Search results found=", len(res))
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
					Log.WithField("error", err).Error("panic: ", string(debug.Stack()))
				}
			}()
			err := vectordb.DownloadWebsiteToVectorDB(ctx, res[i].URL, username)
			if err != nil {
				Log.Warn("Error downloading website", "error=", err)
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
		Log.Warn("no results found", "input", input)
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
