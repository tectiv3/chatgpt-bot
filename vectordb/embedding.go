package vectordb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

const (
	defaultEmbeddingModel = "text-embedding-3-large"
)

type embeddingPayload struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embeddingResponsePayload struct {
	Object string `json:"object"`
	Data   []struct {
		Object    string    `json:"object"`
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
	Model string `json:"model"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

type errorMessage struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// nolint:lll
func (c *OpenAIClient) createEmbedding(ctx context.Context, payload *embeddingPayload) (*embeddingResponsePayload, error) {
	if c.baseURL == "" {
		c.baseURL = defaultBaseURL
	}
	payload.Model = c.EmbeddingModel

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.buildURL("/embeddings", c.EmbeddingModel), bytes.NewReader(payloadBytes))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	c.setHeaders(req)

	r, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer r.Body.Close()

	if r.StatusCode != http.StatusOK {
		msg := fmt.Sprintf("API returned unexpected status code: %d", r.StatusCode)

		// No need to check the error here: if it fails, we'll just return the
		// status code.
		var errResp errorMessage
		if err := json.NewDecoder(r.Body).Decode(&errResp); err != nil {
			return nil, errors.New(msg) // nolint:goerr113
		}

		return nil, fmt.Errorf("%s: %s", msg, errResp.Error.Message) // nolint:goerr113
	}

	var response embeddingResponsePayload

	if err := json.NewDecoder(r.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &response, nil
}

// NewEmbedder creates a new Embedder from the given EmbedderClient, with
// some options that affect how embedding will be done.
func NewEmbedder(client EmbedderClient, opts ...EOption) *EmbedderImpl {
	e := &EmbedderImpl{
		client:        client,
		StripNewLines: defaultStripNewLines,
		BatchSize:     defaultBatchSize,
	}

	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Embedder is the interface for creating vector embeddings from texts.
type Embedder interface {
	// EmbedDocuments returns a vector for each text.
	EmbedDocuments(ctx context.Context, texts []string) ([][]float32, error)
	// EmbedQuery embeds a single text.
	EmbedQuery(ctx context.Context, text string) ([]float32, error)
}

// EmbedderClient is the interface LLM clients implement for embeddings.
type EmbedderClient interface {
	CreateEmbedding(ctx context.Context, texts []string) ([][]float32, error)
}

// EmbedderClientFunc is an adapter to allow the use of ordinary functions as Embedder Clients. If
// `f` is a function with the appropriate signature, `EmbedderClientFunc(f)` is an `EmbedderClient`
// that calls `f`.
type EmbedderClientFunc func(ctx context.Context, texts []string) ([][]float32, error)

func (e EmbedderClientFunc) CreateEmbedding(ctx context.Context, texts []string) ([][]float32, error) {
	return e(ctx, texts)
}

type EmbedderImpl struct {
	client EmbedderClient

	StripNewLines bool
	BatchSize     int
}

const (
	defaultBatchSize     = 512
	defaultStripNewLines = true
)

type EOption func(p *EmbedderImpl)

// WithStripNewLines is an option for specifying the should it strip new lines.
func WithStripNewLines(stripNewLines bool) EOption {
	return func(p *EmbedderImpl) {
		p.StripNewLines = stripNewLines
	}
}

// WithBatchSize is an option for specifying the batch size.
func WithBatchSize(batchSize int) EOption {
	return func(p *EmbedderImpl) {
		p.BatchSize = batchSize
	}
}

// EmbedQuery embeds a single text.
func (ei *EmbedderImpl) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	if ei.StripNewLines {
		text = strings.ReplaceAll(text, "\n", " ")
	}

	emb, err := ei.client.CreateEmbedding(ctx, []string{text})
	if err != nil {
		return nil, fmt.Errorf("error embedding query: %w", err)
	}

	return emb[0], nil
}

// EmbedDocuments creates one vector embedding for each of the texts.
func (ei *EmbedderImpl) EmbedDocuments(ctx context.Context, texts []string) ([][]float32, error) {
	texts = MaybeRemoveNewLines(texts, ei.StripNewLines)
	return BatchedEmbed(ctx, ei.client, texts, ei.BatchSize)
}

func MaybeRemoveNewLines(texts []string, removeNewLines bool) []string {
	if !removeNewLines {
		return texts
	}

	for i := 0; i < len(texts); i++ {
		texts[i] = strings.ReplaceAll(texts[i], "\n", " ")
	}

	return texts
}

// BatchTexts splits strings by the length batchSize.
func BatchTexts(texts []string, batchSize int) [][]string {
	batchedTexts := make([][]string, 0, len(texts)/batchSize+1)

	for i := 0; i < len(texts); i += batchSize {
		batchedTexts = append(batchedTexts, texts[i:minInt([]int{i + batchSize, len(texts)})])
	}

	return batchedTexts
}

// BatchedEmbed creates embeddings for the given input texts, batching them
// into batches of batchSize if needed.
func BatchedEmbed(ctx context.Context, embedder EmbedderClient, texts []string, batchSize int) ([][]float32, error) {
	batchedTexts := BatchTexts(texts, batchSize)

	emb := make([][]float32, 0, len(texts))
	for _, batch := range batchedTexts {
		curBatchEmbeddings, err := embedder.CreateEmbedding(ctx, batch)
		if err != nil {
			return nil, fmt.Errorf("error embedding batch: %w", err)
		}
		emb = append(emb, curBatchEmbeddings...)
	}

	return emb, nil
}

// MinInt returns the minimum value in nums.
// If nums is empty, it returns 0.
func minInt(nums []int) int {
	var min int
	for idx := 0; idx < len(nums); idx++ {
		item := nums[idx]
		if idx == 0 {
			min = item
			continue
		}
		if item < min {
			min = item
		}
	}
	return min
}
