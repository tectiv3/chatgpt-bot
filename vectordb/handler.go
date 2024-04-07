package vectordb

import (
	"context"
	"fmt"
	"github.com/go-shiori/go-readability"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/tectiv3/chatgpt-bot/ollama"
	"github.com/tmc/langchaingo/documentloaders"
	"github.com/tmc/langchaingo/embeddings"
	"github.com/tmc/langchaingo/schema"
	"github.com/tmc/langchaingo/textsplitter"
	"github.com/tmc/langchaingo/vectorstores/chroma"
)

func saveToVectorDb(timeoutCtx context.Context, docs []schema.Document, sessionString string) error {
	llm, err := ollama.NewOllamaEmbeddingLLM()
	if err != nil {
		return err
	}

	embedder, err := embeddings.NewEmbedder(llm)
	if err != nil {
		return err
	}

	store, errNs := chroma.New(
		chroma.WithChromaURL(os.Getenv("CHROMA_DB_URL")),
		chroma.WithEmbedder(embedder),
		chroma.WithDistanceFunction("cosine"),
		chroma.WithNameSpace(sessionString),
	)

	if errNs != nil {
		return errNs
	}

	for i := range docs {
		if len(docs[i].PageContent) == 0 {
			// remove the document from the list
			docs = append(docs[:i], docs[i+1:]...)
		}
	}

	_, errAd := store.AddDocuments(timeoutCtx, docs)

	if errAd != nil {
		slog.Warn("Error adding document", "error", errAd)
		return fmt.Errorf("Error adding document: %v\n", errAd)
	}

	// log.Printf("Added %d documents\n", len(res))
	return nil
}

func DownloadWebsiteToVectorDB(ctx context.Context, url string, sessionString string) error {
	// log.Printf("downloading: %s", url)
	article, err := readability.FromURL(url, 30*time.Second)
	if err != nil {
		return err
	}

	vectorLoader := documentloaders.NewText(strings.NewReader(article.TextContent))
	splitter := textsplitter.NewTokenSplitter(textsplitter.WithSeparators([]string{"\n\n", "\n"}))
	splitter.ChunkOverlap = 100
	splitter.ChunkSize = 300
	docs, err := vectorLoader.LoadAndSplit(ctx, splitter)

	for i := range docs {
		docs[i].Metadata = map[string]interface{}{"url": url}
	}

	timeoutCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	return saveToVectorDb(timeoutCtx, docs, sessionString)
}
