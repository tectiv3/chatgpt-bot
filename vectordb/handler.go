package vectordb

import (
	"context"
	"github.com/go-shiori/go-readability"
	"github.com/tmc/langchaingo/llms/openai"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/tmc/langchaingo/documentloaders"
	"github.com/tmc/langchaingo/embeddings"
	"github.com/tmc/langchaingo/schema"
	"github.com/tmc/langchaingo/textsplitter"
	"github.com/tmc/langchaingo/vectorstores/chroma"
)

const mEmbedding = "text-embedding-3-large"

func saveToVectorDb(timeoutCtx context.Context, docs []schema.Document, sessionString string) error {
	//llm, err := ollama.NewOllamaEmbeddingLLM()
	llm, err := openai.New(openai.WithEmbeddingModel(mEmbedding))
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

	if _, err := store.AddDocuments(timeoutCtx, docs); err != nil {
		slog.Warn("Error adding document", "error", err)
		return err
	}

	slog.Info("Added documents", "count", len(docs))

	return nil
}

func DownloadWebsiteToVectorDB(ctx context.Context, url string, sessionString string) error {
	// log.Printf("downloading: %s", url)
	article, err := readability.FromURL(url, 10*time.Second)
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

	timeoutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	return saveToVectorDb(timeoutCtx, docs, sessionString)
}
