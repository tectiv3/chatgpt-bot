package tools

import (
	"context"
	"encoding/json"
	log "github.com/sirupsen/logrus"
	"github.com/tectiv3/chatgpt-bot/vectordb"
	"github.com/tmc/langchaingo/callbacks"
	"github.com/tmc/langchaingo/tools"
)

// SearchVectorDB is a tool that finds the most relevant documents in the vector db.
type SearchVectorDB struct {
	CallbacksHandler callbacks.Handler
	SessionString    string
	Ollama           bool
}

var _ tools.Tool = SearchVectorDB{}

type DocResult struct {
	Text   string
	Source string
}

// usedResults is a map of used results for all users throughout the session. Potentially a memory leak.
var usedResults = make(map[string][]string)

func (t SearchVectorDB) Description() string {
	return `Useful for searching through added files and websites. Search for keywords in the text not whole questions, avoid relative words like "yesterday" think about what could be in the text. 
    The input to this tool will be run against a vector db. The top results will be returned as json.`
}

func (t SearchVectorDB) Name() string {
	return "SearchVectorDB"
}

func (t SearchVectorDB) Call(ctx context.Context, input string) (string, error) {
	if t.CallbacksHandler != nil {
		t.CallbacksHandler.HandleToolStart(ctx, input)
	}
	ctx = context.WithValue(ctx, "ollama", t.Ollama)
	docs, err := vectordb.SearchVectorDB(ctx, input, t.SessionString)

	var results []DocResult

	for _, r := range docs {
		newResult := DocResult{Text: r.PageContent}

		source, ok := r.Metadata["url"].(string)
		if ok {
			newResult.Source = source
		}

		for _, usedLink := range usedResults[t.SessionString] {
			if usedLink == newResult.Text {
				continue
			}
		}
		//ch, ok := c.CallbacksHandler.(chain.CustomHandler)
		//if ok {
		//	ch.HandleVectorFound(ctx, fmt.Sprintf("%s with a score of %f", newResult.Source, r.Score))
		//}
		results = append(results, newResult)
		usedResults[t.SessionString] = append(usedResults[t.SessionString], newResult.Text)
	}

	if len(docs) == 0 {
		response := "no results found. Try other db search keywords or download more websites."
		log.Warn("no results found", "input", input)
		results = append(results, DocResult{Text: response})
	} else if len(results) == 0 {
		response := "No new results found, all returned results have been used already. Try other db search keywords or download more websites."
		results = append(results, DocResult{Text: response})
	}

	if t.CallbacksHandler != nil {
		t.CallbacksHandler.HandleToolEnd(ctx, input)
	}

	resultJson, err := json.Marshal(results)
	if err != nil {
		return "", err
	}

	return string(resultJson), nil
}
