package vectordb

import (
	"context"
	"github.com/go-shiori/go-readability"
	log "github.com/sirupsen/logrus"
	"net/http"
	"os"
	"strings"
	"time"
)

// withBrowserUserAgent creates a RequestWith modifier that sets a realistic browser user agent
func withBrowserUserAgent() readability.RequestWith {
	return func(r *http.Request) {
		r.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36")
	}
}

// VectorStore is the interface for saving and querying documents in the
// form of vector embeddings.
type VectorStore interface {
	AddDocuments(ctx context.Context, docs []Document, options ...Option) ([]string, error)
	SimilaritySearch(ctx context.Context, query string, numDocuments int, options ...Option) ([]Document, error) //nolint:lll
}

type Document struct {
	PageContent string
	Metadata    map[string]any
	Score       float32
}

// Retriever is a retriever for vector stores.
type Retriever struct {
	CallbacksHandler interface{}
	v                VectorStore
	numDocs          int
	options          []Option
}

var _ Retriever = Retriever{}

// GetRelevantDocuments returns documents using the vector store.
func (r Retriever) GetRelevantDocuments(ctx context.Context, query string) ([]Document, error) {
	docs, err := r.v.SimilaritySearch(ctx, query, r.numDocs, r.options...)
	if err != nil {
		return nil, err
	}

	return docs, nil
}

// ToRetriever takes a vector store and returns a retriever using the
// vector store to retrieve documents.
func ToRetriever(vectorStore VectorStore, numDocuments int, options ...Option) Retriever {
	return Retriever{
		v:       vectorStore,
		numDocs: numDocuments,
		options: options,
	}
}

func newStore(ctx context.Context, sessionString string) (*chromaStore, error) {
	store, err := newChroma(os.Getenv("OPENAI_API_KEY"), os.Getenv("CHROMA_DB_URL"), sessionString+"nodeps")

	return &store, err
}

func saveToVectorDb(timeoutCtx context.Context, docs []Document, sessionString string) error {
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
		log.Warn(err)
		return err
	}
	//log.Info("Added documents, count=", len(docs))

	return nil
}

func DownloadWebsiteToVectorDB(ctx context.Context, url string, sessionString string) error {
	article, err := readability.FromURL(url, 10*time.Second, withBrowserUserAgent())
	if err != nil {
		return err
	}

	vectorLoader := NewText(strings.NewReader(article.TextContent))
	splitter := NewTokenSplitter(WithSeparators([]string{"\n\n", "\n"}))
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

func SearchVectorDB(ctx context.Context, input string, sessionString string) ([]Document, error) {
	amountOfResults := 3
	scoreThreshold := 0.4
	store, err := newStore(ctx, sessionString)
	if err != nil {
		return []Document{}, err
	}

	options := []Option{WithScoreThreshold(float32(scoreThreshold))}
	retriever := ToRetriever(store, amountOfResults, options...)

	return retriever.GetRelevantDocuments(context.Background(), input)
}
