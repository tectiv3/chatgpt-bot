package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/go-shiori/go-readability"
	"github.com/meinside/openai-go"
	"github.com/tectiv3/chatgpt-bot/i18n"
	tele "gopkg.in/telebot.v3"
)

// withBrowserUserAgent creates a RequestWith modifier that sets a realistic browser user agent
func withBrowserUserAgent() readability.RequestWith {
	return func(r *http.Request) {
		r.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36")
	}
}

// getResponseTools converts function tools to format compatible with Responses API
func (s *Server) getResponseTools() []any {
	traditionalTools := s.getFunctionTools()
	responseTools := make([]any, 0, len(traditionalTools))

	for _, tool := range traditionalTools {
		// Convert ChatCompletionTool to ResponseTool format
		responseTool := openai.ResponseTool{
			Type:       "function",
			Name:       tool.Function.Name,
			Parameters: tool.Function.Parameters,
		}
		if tool.Function.Description != nil {
			responseTool.Description = *tool.Function.Description
		}
		responseTools = append(responseTools, responseTool)
	}

	return responseTools
}

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

// handleResponseFunctionCalls converts ResponseOutput to ToolCalls and delegates to unified handler
func (s *Server) handleResponseFunctionCalls(chat *Chat, c tele.Context, functions []openai.ResponseOutput) (string, error) {
	// Convert ResponseOutput function calls to ToolCall format
	var toolCalls []openai.ToolCall

	for _, responseOutput := range functions {
		// Only process function calls
		if responseOutput.Type != "function_call" {
			continue
		}

		toolCall := openai.ToolCall{
			ID:   responseOutput.CallID,
			Type: "function",
			Function: openai.ToolCallFunction{
				Name:      responseOutput.Name,
				Arguments: responseOutput.Arguments,
			},
		}
		toolCalls = append(toolCalls, toolCall)
	}

	// Use the unified handler with converted tool calls
	return s.handleToolCalls(chat, c, toolCalls)
}

func (s *Server) handleFunctionCall(chat *Chat, c tele.Context, response openai.ChatMessage) (string, error) {
	return s.handleToolCalls(chat, c, response.ToolCalls)
}

// handleToolCalls processes a slice of tool calls (unified logic for both APIs)
func (s *Server) handleToolCalls(chat *Chat, c tele.Context, toolCalls []openai.ToolCall) (string, error) {
	result := ""
	var resultErr error
	var toolID string
	sentMessage := chat.getSentMessage(c)
	toolCallsCount := len(toolCalls)
	reply := ""
	for i, toolCall := range toolCalls {
		function := toolCall.Function
		if function.Name == "" {
			err := fmt.Sprint("there was no returned function call name")
			resultErr = fmt.Errorf(err)
			continue
		}
		Log.WithField("tools", toolCallsCount).WithField("tool", i).WithField("function", function.Name).WithField("user", c.Sender().Username).Info("Function call")

		switch function.Name {
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

			reminderResult := fmt.Sprintf("Reminder set for %d minutes from now", arguments.Minutes)
			result = reminderResult
			toolID = toolCall.ID

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

			summary, err := s.getPageSummary(arguments.URL)
			if err != nil {
				resultErr = fmt.Errorf("failed to get page summary: %s", err)
				continue
			}

			// Store result for continuing conversation
			result = summary
			toolID = toolCall.ID

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

			rate, err := s.getCryptoRate(arguments.Asset)
			if err != nil {
				resultErr = fmt.Errorf("failed to get crypto rate: %s", err)
				continue
			}

			// Store result for continuing conversation
			result = rate
			toolID = toolCall.ID
		}
	}

	if len(result) == 0 {
		s.saveHistory(chat)
		return "", resultErr
	}
	chat.addToolResultToDialog(toolID, result)

	// For OpenAI Responses API, don't automatically continue the conversation
	// Let the tool result be the final response, user can ask follow-up questions
	model := s.getModel(chat.ModelName)
	if model.Provider == pOpenAI {
		// Save history and return - conversation ends with tool result
		s.saveHistory(chat)
		return result, nil
	}

	// For other providers, continue with Chat Completions API
	if chat.Stream {
		_ = s.getStreamAnswer(chat, c, nil)
		return "", nil
	}

	// Non-streaming mode
	if model.Provider == pOpenAI {
		// For OpenAI models, use non-streaming Responses API
		err := s.getResponse(chat, c, nil)
		return "", err
	} else {
		err := s.getAnswer(chat, c, nil)
		return "", err
	}
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

	article, err := readability.FromURL(url, 30*time.Second, withBrowserUserAgent())
	if err != nil {
		Log.Fatalf("failed to parse %s, %v\n", url, err)
	}

	if s.conf.Verbose {
		Log.Info("Page title=", article.Title, ", content=", len(article.TextContent))
	}
	_ = c.Notify(tele.Typing)

	s.sendAudio(c, article.TextContent)
}

// getPageSummary gets a page summary synchronously and returns the result
func (s *Server) getPageSummary(url string) (string, error) {
	defer func() {
		if err := recover(); err != nil {
			Log.WithField("error", err).Error("panic: ", string(debug.Stack()))
		}
	}()

	article, err := readability.FromURL(url, 30*time.Second, withBrowserUserAgent())
	if err != nil {
		return "", fmt.Errorf("failed to parse %s: %v", url, err)
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
		return "", fmt.Errorf("failed to create chat completion: %v", err)
	}

	str, _ := response.Choices[0].Message.ContentString()

	return str, nil
}

func (s *Server) getCryptoRate(asset string) (string, error) {
	asset = strings.ToLower(asset)
	format := "$%0.0f"
	switch asset {
	case "btc":
		asset = "bitcoin"
	case "eth":
		asset = "ethereum"
	case "sol":
		asset = "solana"
	case "xrp":
		asset = "ripple"
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
