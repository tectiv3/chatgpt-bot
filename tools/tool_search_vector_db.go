package tools

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"

	"github.com/tectiv3/chatgpt-bot/ollama"
	"github.com/tmc/langchaingo/callbacks"
	"github.com/tmc/langchaingo/embeddings"
	"github.com/tmc/langchaingo/tools"
	"github.com/tmc/langchaingo/vectorstores"
	"github.com/tmc/langchaingo/vectorstores/chroma"
)

// SearchVectorDB is a tool that finds the most relevant documents in the vector db.
type SearchVectorDB struct {
	CallbacksHandler callbacks.Handler
	SessionString    string
}

var _ tools.Tool = SearchVectorDB{}

type DocResult struct {
	Text   string
	Source string
}

var usedResults = make(map[string][]string)

func (c SearchVectorDB) Description() string {
	return `Useful for searching through added files and websites. Search for keywords in the text not whole questions, avoid relative words like "yesterday" think about what could be in the text. 
    The input to this tool will be run against a vector db. The top results will be returned as json.`
}

func (c SearchVectorDB) Name() string {
	return "SearchVectorDB"
}

func (c SearchVectorDB) Call(ctx context.Context, input string) (string, error) {
	amountOfResults := 3
	scoreThreshold := 0.4
	if c.CallbacksHandler != nil {
		c.CallbacksHandler.HandleToolStart(ctx, input)
	}

	llm, err := ollama.NewOllamaEmbeddingLLM()
	if err != nil {
		return "", err
	}

	ollamaEmbedder, err := embeddings.NewEmbedder(llm)
	if err != nil {
		return "", err
	}

	store, errNs := chroma.New(
		chroma.WithChromaURL(os.Getenv("CHROMA_DB_URL")),
		chroma.WithEmbedder(ollamaEmbedder),
		chroma.WithDistanceFunction("cosine"),
		chroma.WithNameSpace(c.SessionString),
	)

	if errNs != nil {
		return "", errNs
	}

	options := []vectorstores.Option{
		vectorstores.WithScoreThreshold(float32(scoreThreshold)),
	}

	retriever := vectorstores.ToRetriever(store, amountOfResults, options...)
	docs, err := retriever.GetRelevantDocuments(context.Background(), input)
	if err != nil {
		return "", err
	}

	var results []DocResult

	for _, r := range docs {
		newResult := DocResult{
			Text: r.PageContent,
		}

		source, ok := r.Metadata["url"].(string)
		if ok {
			newResult.Source = source
		}

		for _, usedLink := range usedResults[c.SessionString] {
			if usedLink == newResult.Text {
				continue
			}
		}
		//ch, ok := c.CallbacksHandler.(CustomHandler)
		//if ok {
		//	ch.HandleVectorFound(ctx, fmt.Sprintf("%s with a score of %f", newResult.Source, r.Score))
		//}
		results = append(results, newResult)
		usedResults[c.SessionString] = append(usedResults[c.SessionString], newResult.Text)
	}

	if len(docs) == 0 {
		response := "no results found. Try other db search keywords or download more websites."
		slog.Warn("no results found", "input", input)
		results = append(results, DocResult{Text: response})

	} else if len(results) == 0 {
		response := "No new results found, all returned results have been used already. Try other db search keywords or download more websites."
		results = append(results, DocResult{Text: response})
	}

	if c.CallbacksHandler != nil {
		c.CallbacksHandler.HandleToolEnd(ctx, input)
	}

	resultJson, err := json.Marshal(results)
	if err != nil {
		return "", err
	}

	return string(resultJson), nil
}
