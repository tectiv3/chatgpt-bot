package tools

import (
	"context"
	"fmt"
	"github.com/tmc/langchaingo/callbacks"
	"github.com/tmc/langchaingo/llms"

	"github.com/tmc/langchaingo/tools"
)

// Feedback is a tool that can do math.
type Feedback struct {
	CallbacksHandler callbacks.Handler
	sessionString    string
	Query            ClientQuery
	Llm              llms.Model
}

type ClientQuery struct {
	Prompt        string `json:"prompt"`
	MaxIterations int    `json:"maxIterations"`
	ModelName     string `json:"modelName"`
	Session       string `json:"session"`
}

var _ tools.Tool = Feedback{}

func (dw Feedback) Description() string {
	return `Useful for self critique. You have to use this function before submitting a final answer. You have to provide your current attempt at answering the question.`
}

func (dw Feedback) Name() string {
	return "Feedback"
}

func (dw Feedback) Call(ctx context.Context, input string) (string, error) {
	if dw.CallbacksHandler != nil {
		dw.CallbacksHandler.HandleToolStart(ctx, input)
	}

	newPrompt := fmt.Sprintf("Critique if the quesion: `%s` is answered with: `%s`", dw.Query.Prompt, input)
	feedback, err := dw.Llm.Call(ctx, newPrompt) // llms.WithTemperature(0.8),

	if err != nil {
		return "", err
	}

	if dw.CallbacksHandler != nil {
		dw.CallbacksHandler.HandleToolEnd(ctx, feedback)
	}

	return feedback, nil
}
