package vectordb

import (
	"context"
	"errors"
	"fmt"
	"github.com/amikos-tech/chroma-go"
	chromatypes "github.com/amikos-tech/chroma-go/types"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"golang.org/x/exp/maps"
)

var (
	ErrInvalidScoreThreshold    = errors.New("score threshold must be between 0 and 1")
	ErrUnexpectedResponseLength = errors.New("unexpected length of response")
	ErrNewClient                = errors.New("error creating collection")
	ErrAddDocument              = errors.New("error adding document")
	ErrRemoveCollection         = errors.New("error resetting collection")
	ErrUnsupportedOptions       = errors.New("unsupported options")
)

// Option is a function that configures an Options.
type Option func(*Options)

// Options is a set of options for similarity search and add documents.
type Options struct {
	NameSpace      string
	ScoreThreshold float32
	Filters        any
	Embedder       Embedder
	Deduplicater   func(context.Context, Document) bool
}

// WithNameSpace returns an Option for setting the name space.
func WithNameSpace(nameSpace string) Option {
	return func(o *Options) {
		o.NameSpace = nameSpace
	}
}

func WithScoreThreshold(scoreThreshold float32) Option {
	return func(o *Options) {
		o.ScoreThreshold = scoreThreshold
	}
}

// WithFilters searches can be limited based on metadata filters. Searches with  metadata
// filters retrieve exactly the number of nearest-neighbors results that match the filters. In
// most cases the search latency will be lower than unfiltered searches
// See https://docs.pinecone.io/docs/metadata-filtering
func WithFilters(filters any) Option {
	return func(o *Options) {
		o.Filters = filters
	}
}

// WithEmbedder returns an Option for setting the embedder that could be used when
// adding documents or doing similarity search (instead the embedder from the Store context)
// this is useful when we are using multiple LLMs with single vectorstore.
func WithEmbedder(embedder Embedder) Option {
	return func(o *Options) {
		o.Embedder = embedder
	}
}

// WithDeduplicater returns an Option for setting the deduplicater that could be used
// when adding documents. This is useful to prevent wasting time on creating an embedding
// when one already exists.
func WithDeduplicater(fn func(ctx context.Context, doc Document) bool) Option {
	return func(o *Options) {
		o.Deduplicater = fn
	}
}

// chromaStore is a wrapper around the chromaGo API and client.
type chromaStore struct {
	client             *chromago.Client
	collection         *chromago.Collection
	distanceFunction   chromatypes.DistanceFunction
	chromaURL          string
	openaiAPIKey       string
	openaiOrganization string

	nameSpace    string
	nameSpaceKey string
	embedder     Embedder
	includes     []chromatypes.QueryEnum
}

// New creates an active client connection to the collection in the Chroma server
// and returns the `chromaStore` object needed by the other accessors.
func newChroma(key, url, namespace string) (chromaStore, error) {
	s := chromaStore{}
	chromaClient, err := chromago.NewClient(url)
	if err != nil {
		return s, err
	}
	if _, errHb := chromaClient.Heartbeat(context.Background()); errHb != nil {
		return s, errHb
	}
	s.client = chromaClient

	// var embeddingFunction chromatypes.EmbeddingFunction
	// embeddingFunction, err = openai.NewOpenAIEmbeddingFunction(key)
	// if err != nil {
	// 	return s, err
	// }
	embedder := NewEmbedder(NewOpenAIClient(key))
	embeddingFunction := chromaGoEmbedder{Embedder: embedder}

	col, errCc := s.client.CreateCollection(context.Background(), namespace, map[string]any{}, true,
		embeddingFunction, "cosine")
	if errCc != nil {
		return s, fmt.Errorf("%w: %w", ErrNewClient, errCc)
	}

	s.collection = col

	return s, nil
}

// AddDocuments adds the text and metadata from the documents to the Chroma collection associated with 'chromaStore'.
// and returns the ids of the added documents.
func (s chromaStore) AddDocuments(ctx context.Context,
	docs []Document,
	options ...Option,
) ([]string, error) {
	opts := s.getOptions(options...)
	if opts.Embedder != nil || opts.ScoreThreshold != 0 || opts.Filters != nil {
		return nil, ErrUnsupportedOptions
	}

	nameSpace := s.getNameSpace(opts)
	if nameSpace != "" && s.nameSpaceKey == "" {
		return nil, fmt.Errorf("%w: nameSpace without nameSpaceKey", ErrUnsupportedOptions)
	}

	ids := make([]string, len(docs))
	texts := make([]string, len(docs))
	metadatas := make([]map[string]any, len(docs))
	for docIdx, doc := range docs {
		ids[docIdx] = uuid.New().String() // TODO (noodnik2): find & use something more meaningful
		texts[docIdx] = doc.PageContent
		mc := make(map[string]any, 0)
		maps.Copy(mc, doc.Metadata)
		metadatas[docIdx] = mc
		if nameSpace != "" {
			metadatas[docIdx][s.nameSpaceKey] = nameSpace
		}
	}

	if _, addErr := s.collection.Add(ctx, nil, metadatas, texts, ids); addErr != nil {
		log.WithField("metadatas", metadatas).WithField("ids", ids).Warn("Collection add failed:", addErr)
		return nil, fmt.Errorf("%w: %w", ErrAddDocument, addErr)
	}

	return ids, nil
}

func (s chromaStore) SimilaritySearch(ctx context.Context, query string, numDocuments int,
	options ...Option,
) ([]Document, error) {
	opts := s.getOptions(options...)

	if opts.Embedder != nil {
		// embedder is not used by this method, so shouldn't ever be specified
		return nil, fmt.Errorf("%w: Embedder", ErrUnsupportedOptions)
	}

	scoreThreshold, stErr := s.getScoreThreshold(opts)
	if stErr != nil {
		return nil, stErr
	}

	filter := s.getNamespacedFilter(opts)
	qr, queryErr := s.collection.Query(ctx, []string{query}, int32(numDocuments), filter, nil, s.includes)
	if queryErr != nil {
		return nil, queryErr
	}

	if len(qr.Documents) != len(qr.Metadatas) || len(qr.Metadatas) != len(qr.Distances) {
		return nil, fmt.Errorf("%w: qr.Documents[%d], qr.Metadatas[%d], qr.Distances[%d]",
			ErrUnexpectedResponseLength, len(qr.Documents), len(qr.Metadatas), len(qr.Distances))
	}
	var sDocs []Document
	for docsI := range qr.Documents {
		for docI := range qr.Documents[docsI] {
			if score := 1.0 - qr.Distances[docsI][docI]; score >= scoreThreshold {
				sDocs = append(sDocs, Document{
					Metadata:    qr.Metadatas[docsI][docI],
					PageContent: qr.Documents[docsI][docI],
					Score:       score,
				})
			}
		}
	}

	return sDocs, nil
}

func (s chromaStore) RemoveCollection() error {
	if s.client == nil || s.collection == nil {
		return fmt.Errorf("%w: no collection", ErrRemoveCollection)
	}
	_, errDc := s.client.DeleteCollection(context.Background(), s.collection.Name)
	if errDc != nil {
		return fmt.Errorf("%w(%s): %w", ErrRemoveCollection, s.collection.Name, errDc)
	}
	return nil
}

func (s chromaStore) getOptions(options ...Option) Options {
	opts := Options{}
	for _, opt := range options {
		opt(&opts)
	}
	return opts
}

func (s chromaStore) getScoreThreshold(opts Options) (float32, error) {
	if opts.ScoreThreshold < 0 || opts.ScoreThreshold > 1 {
		return 0, ErrInvalidScoreThreshold
	}
	return opts.ScoreThreshold, nil
}

func (s chromaStore) getNameSpace(opts Options) string {
	if opts.NameSpace != "" {
		return opts.NameSpace
	}
	return s.nameSpace
}

func (s chromaStore) getNamespacedFilter(opts Options) map[string]any {
	filter, _ := opts.Filters.(map[string]any)

	nameSpace := s.getNameSpace(opts)
	if nameSpace == "" || s.nameSpaceKey == "" {
		return filter
	}

	nameSpaceFilter := map[string]any{s.nameSpaceKey: nameSpace}
	if filter == nil {
		return nameSpaceFilter
	}

	return map[string]any{"$and": []map[string]any{nameSpaceFilter, filter}}
}
