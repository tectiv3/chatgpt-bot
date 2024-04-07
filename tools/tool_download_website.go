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
	sessionString    string
}

var _ tools.Tool = DownloadWebsite{}

func (dw DownloadWebsite) Description() string {
	return `Useful for getting the downloading a website into your vector db. The websites content will be saved into your vector database.
    The input to this tool must be a valid http(s) link. You only get a status from this tool, no real information. Use the database tool to query the information after downloading.`
}

func (dw DownloadWebsite) Name() string {
	return "DownloadWebsite"
}

func (dw DownloadWebsite) Call(ctx context.Context, input string) (string, error) {
	if dw.CallbacksHandler != nil {
		dw.CallbacksHandler.HandleToolStart(ctx, input)
	}

	input = strings.TrimPrefix(strings.TrimSuffix(input, "\""), "\"")

	err := vectordb.DownloadWebsiteToVectorDB(ctx, input, dw.sessionString)
	if err != nil {
		return fmt.Sprintf("error from evaluator: %s", err.Error()), nil //nolint:nilerr
	}

	if dw.CallbacksHandler != nil {
		dw.CallbacksHandler.HandleToolEnd(ctx, "success")
	}

	return "success", nil
}
