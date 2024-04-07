package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/tectiv3/chatgpt-bot/types"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/tectiv3/chatgpt-bot/chain"
	"github.com/tectiv3/chatgpt-bot/vectordb"
	"github.com/tmc/langchaingo/callbacks"
	"github.com/tmc/langchaingo/tools"
)

type WebSearch struct {
	CallbacksHandler callbacks.Handler
	SessionString    string
	Ollama           bool
}

type SeaXngResult struct {
	Query           string `json:"query"`
	NumberOfResults int    `json:"number_of_results"`
	Results         []struct {
		URL           string   `json:"url"`
		Title         string   `json:"title"`
		Content       string   `json:"content"`
		PublishedDate any      `json:"publishedDate,omitempty"`
		ImgSrc        any      `json:"img_src,omitempty"`
		Engine        string   `json:"engine"`
		ParsedURL     []string `json:"parsed_url"`
		Template      string   `json:"template"`
		Engines       []string `json:"engines"`
		Positions     []int    `json:"positions"`
		Score         float64  `json:"score"`
		Category      string   `json:"category"`
	} `json:"results"`
	Answers             []any    `json:"answers"`
	Corrections         []any    `json:"corrections"`
	Infoboxes           []any    `json:"infoboxes"`
	Suggestions         []string `json:"suggestions"`
	UnresponsiveEngines []any    `json:"unresponsive_engines"`
}

var usedLinks = make(map[string][]string)

var _ tools.Tool = WebSearch{}

func (t WebSearch) Description() string {
	return `Useful for searching the internet. You have to use this tool if you're not 100% certain. The top 10 results will be added to the vector db. The top 3 results are also getting returned to you directly. For more search queries through the same websites, use the VectorDB tool.`
}

func (t WebSearch) Name() string {
	return "WebSearch"
}

func (t WebSearch) Call(ctx context.Context, input string) (string, error) {
	if t.CallbacksHandler != nil {
		t.CallbacksHandler.HandleToolStart(ctx, input)
	}

	input = strings.TrimPrefix(input, "\"")
	input = strings.TrimSuffix(input, "\"")
	inputQuery := url.QueryEscape(input)
	searXNGDomain := os.Getenv("SEARXNG_DOMAIN")
	//slog.Info("Searching", "query", inputQuery)
	resp, err := http.Get(fmt.Sprintf("%s/?q=%s&format=json", searXNGDomain, inputQuery))

	if err != nil {
		slog.Warn("Error making the request", "error", err)
		return "", err
	}
	defer resp.Body.Close()

	var apiResponse SeaXngResult
	if err := json.NewDecoder(resp.Body).Decode(&apiResponse); err != nil {
		slog.Warn("Error decoding the response", "error", err)
		return "", err
	}

	slog.Info("Search found", "results", len(apiResponse.Results))

	wg := sync.WaitGroup{}
	counter := 0
	for i := range apiResponse.Results {
		for _, usedLink := range usedLinks[t.SessionString] {
			if usedLink == apiResponse.Results[i].URL {
				continue
			}
		}
		if apiResponse.Results[i].Score <= 0.5 {
			continue
		}

		if counter > 10 {
			break
		}

		// if result link ends in .pdf, skip
		if strings.HasSuffix(apiResponse.Results[i].URL, ".pdf") {
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
			ctx = context.WithValue(ctx, "ollama", t.Ollama)
			err := vectordb.DownloadWebsiteToVectorDB(ctx, apiResponse.Results[i].URL, t.SessionString)
			if err != nil {
				slog.Warn("Error downloading website", "error", err)
				wg.Done()
				return
			}
			ch, ok := t.CallbacksHandler.(chain.CustomHandler)
			if ok {
				newSource := types.Source{Name: "WebSearch", Link: apiResponse.Results[i].URL}

				ch.HandleSourceAdded(ctx, newSource)
				usedLinks[t.SessionString] = append(usedLinks[t.SessionString], apiResponse.Results[i].URL)
			}
			wg.Done()
		}(i)
	}
	wg.Wait()
	result, err := SearchVectorDB.Call(
		SearchVectorDB{CallbacksHandler: nil, SessionString: t.SessionString, Ollama: t.Ollama},
		context.Background(),
		input,
	)

	if err != nil {
		return fmt.Sprintf("error from vector db search: %s", err.Error()), nil //nolint:nilerr
	}

	if t.CallbacksHandler != nil {
		t.CallbacksHandler.HandleToolEnd(ctx, result)
	}

	if len(apiResponse.Results) == 0 {
		return "No results found", fmt.Errorf("no results, we might be rate limited")
	}

	return result, nil
}
