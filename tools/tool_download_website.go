package tools

import (
	"context"
	"fmt"
	"github.com/tectiv3/chatgpt-bot/vectordb"
	"github.com/tmc/langchaingo/callbacks"
	"github.com/tmc/langchaingo/tools"
	"strings"
)

// DownloadWebsite is a tool that can do math.
type DownloadWebsite struct {
	CallbacksHandler callbacks.Handler
	SessionString    string
}

var _ tools.Tool = DownloadWebsite{}

func (t DownloadWebsite) Description() string {
	return `Useful for getting the downloading a website into your vector db. The websites content will be saved into your vector database.
    The input to this tool must be a valid http(s) link. You only get a status from this tool, no real information. Use the database tool to query the information after downloading.`
}

func (t DownloadWebsite) Name() string {
	return "DownloadWebsite"
}

func (t DownloadWebsite) Call(ctx context.Context, input string) (string, error) {
	if t.CallbacksHandler != nil {
		t.CallbacksHandler.HandleToolStart(ctx, input)
	}
	input = strings.TrimPrefix(strings.TrimSuffix(input, "\""), "\"")

	err := vectordb.DownloadWebsiteToVectorDB(ctx, input, t.SessionString)
	if err != nil {
		return fmt.Sprintf("error from evaluator: %s", err.Error()), nil //nolint:nilerr
	}

	if t.CallbacksHandler != nil {
		t.CallbacksHandler.HandleToolEnd(ctx, "success")
	}

	return "success", nil
}
