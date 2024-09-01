package vectordb

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

const (
	defaultBaseURL = "https://api.openai.com/v1"
)

// ErrEmptyResponse is returned when the OpenAI API returns an empty response.
var ErrEmptyResponse = errors.New("empty response")

// OpenAIClient is a client for the OpenAI API.
type OpenAIClient struct {
	token        string
	baseURL      string
	organization string
	httpClient   Doer

	EmbeddingModel string
}

// Doer performs a HTTP request.
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// New returns a new OpenAI client.
func NewOpenAIClient(token string) *OpenAIClient {
	return &OpenAIClient{
		token:          token,
		EmbeddingModel: defaultEmbeddingModel,
		baseURL:        strings.TrimSuffix(defaultBaseURL, "/"),
		httpClient:     http.DefaultClient,
	}
}

// Completion is a completion.
type Completion struct {
	Text string `json:"text"`
}

// EmbeddingRequest is a request to create an embedding.
type EmbeddingRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

// CreateEmbedding creates embeddings.
func (c *OpenAIClient) CreateEmbedding(ctx context.Context, inputTexts []string) ([][]float32, error) {
	resp, err := c.createEmbedding(ctx, &embeddingPayload{
		Input: inputTexts,
		Model: defaultEmbeddingModel,
	})
	if err != nil {
		return nil, err
	}

	if len(resp.Data) == 0 {
		return nil, ErrEmptyResponse
	}

	embeddings := make([][]float32, 0)
	for i := 0; i < len(resp.Data); i++ {
		embeddings = append(embeddings, resp.Data[i].Embedding)
	}

	return embeddings, nil
}

func (c *OpenAIClient) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	if c.organization != "" {
		req.Header.Set("OpenAI-Organization", c.organization)
	}
}

func (c *OpenAIClient) buildURL(suffix string, model string) string {
	// open ai implement:
	return fmt.Sprintf("%s%s", c.baseURL, suffix)
}
