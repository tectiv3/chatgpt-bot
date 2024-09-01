package vectordb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	log "github.com/sirupsen/logrus"
	"io"
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

// CreateEmbedding creates embeddings.
func (c *OpenAIClient) CreateEmbedding(ctx context.Context, inputTexts []string) ([][]float32, error) {
	resp, err := c.createEmbedding(ctx, &EmbeddingRequest{
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

// nolint:lll
func (c *OpenAIClient) createEmbedding(ctx context.Context, payload *EmbeddingRequest) (*embeddingResponsePayload, error) {
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

		// read all from r.Body
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, fmt.Errorf("read response body: %w", err)
		}

		if err := json.Unmarshal(body, &errResp); err != nil {
			log.Warn(string(body))
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
