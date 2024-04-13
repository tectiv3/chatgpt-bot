package vectordb

import (
	"context"
	"github.com/go-shiori/go-readability"
	log "github.com/sirupsen/logrus"
	"github.com/tectiv3/chatgpt-bot/ollama"
	"github.com/tmc/langchaingo/llms/openai"
	"github.com/tmc/langchaingo/vectorstores"
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

func newStore(ctx context.Context, sessionString string) (*chroma.Store, error) {
	var llm embeddings.EmbedderClient
	var err error
	suffix := ""
	var useOllama bool
	if o, ok := ctx.Value("ollama").(bool); ok {
		useOllama = o
	}
	if useOllama {
		//log.Info("Using Ollama")
		llm, err = ollama.NewOllamaEmbeddingLLM()
	} else {
		//log.Info("Using OpenAI")
		llm, err = openai.New(openai.WithEmbeddingModel(mEmbedding))
		suffix = "openai"
	}
	if err != nil {
		return nil, err
	}

	embedder, err := embeddings.NewEmbedder(llm)
	if err != nil {
		return nil, err
	}

	opts := []chroma.Option{
		chroma.WithChromaURL(os.Getenv("CHROMA_DB_URL")),
		chroma.WithEmbedder(embedder),
		chroma.WithDistanceFunction("cosine"),
		chroma.WithNameSpace(sessionString + suffix),
	}
	if !useOllama {
		opts = append(opts, chroma.WithOpenAIAPIKey(os.Getenv("OPENAI_API_KEY")))
	}

	store, err := chroma.New(opts...)

	return &store, err
}

func saveToVectorDb(timeoutCtx context.Context, docs []schema.Document, sessionString string) error {
	store, err := newStore(timeoutCtx, sessionString)
	if err != nil {
		return err
	}

	for i := range docs {
		if len(docs[i].PageContent) == 0 {
			// remove the document from the list
			docs = append(docs[:i], docs[i+1:]...)
		}
	}

	if _, err := store.AddDocuments(timeoutCtx, docs); err != nil {
		log.WithField("docs", docs).Warn(err)
		return err
	}
	//log.Info("Added documents, count=", len(docs))

	return nil
}

func DownloadWebsiteToVectorDB(ctx context.Context, url string, sessionString string) error {
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

	var useOllama bool
	if o, ok := ctx.Value("ollama").(bool); ok {
		useOllama = o
	}
	timeoutCtx = context.WithValue(timeoutCtx, "ollama", useOllama)

	return saveToVectorDb(timeoutCtx, docs, sessionString)
}

func SearchVectorDB(ctx context.Context, input string, sessionString string) ([]schema.Document, error) {
	amountOfResults := 3
	scoreThreshold := 0.4
	store, err := newStore(ctx, sessionString)
	if err != nil {
		return []schema.Document{}, err
	}

	options := []vectorstores.Option{vectorstores.WithScoreThreshold(float32(scoreThreshold))}
	retriever := vectorstores.ToRetriever(store, amountOfResults, options...)

	return retriever.GetRelevantDocuments(context.Background(), input)
}
