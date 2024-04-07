package main

import (
	"context"
	"fmt"
	"github.com/tectiv3/chatgpt-bot/ollama"
	llm_tools "github.com/tectiv3/chatgpt-bot/tools"
	"github.com/tmc/langchaingo/agents"
	"github.com/tmc/langchaingo/chains"
	"github.com/tmc/langchaingo/memory"
	"github.com/tmc/langchaingo/tools"
	"log/slog"
	"time"
)

type Sessions map[string]*memory.ConversationBuffer

var sessions Sessions = make(Sessions)

func (s *Server) startAgent(ctx context.Context, outputChan chan<- HttpJsonStreamElement, userQuery ClientQuery) {
	neededModels := []string{ollama.EmbeddingsModel, userQuery.ModelName}
	s.RLock()
	for _, modelName := range neededModels {
		if err := ollama.CheckIfModelExistsOrPull(modelName); err != nil {
			slog.Error("Model does not exist and could not be pulled", "model", modelName, "error", err)
			//outputChan <- HttpJsonStreamElement{
			//	Message:  fmt.Sprintf("Model %s does not exist and could not be pulled: %s", modelName, err.Error()),
			//	StepType: StepHandleLlmError,
			//	Stream:   false,
			//}
			return
		}
	}
	s.RUnlock()

	startTime := time.Now()
	session := userQuery.Session

	s.Lock()
	if sessions[session] == nil {
		slog.Info("Creating new session", "session", session)
		sessions[session] = memory.NewConversationBuffer()
		memory.NewChatMessageHistory()
		//outputChan <- HttpJsonStreamElement{
		//	StepType: StepHandleNewSession,
		//	Session:  session,
		//	Stream:   false,
		//}
	}
	mem := sessions[session]
	s.Unlock()

	slog.Info("Starting agent chain", "session", session, "userQuery", userQuery, "startTime", startTime)

	llm, err := ollama.NewOllama(userQuery.ModelName, s.conf.OllamaURL)
	if err != nil {
		slog.Error("Error creating new LLM", "error", err)
		return
	}

	agentTools := []tools.Tool{
		tools.Calculator{},
		llm_tools.WebSearch{SessionString: session},
		llm_tools.SearchVectorDB{SessionString: session},
	}

	executor, err := agents.Initialize(
		llm,
		agentTools,
		agents.ConversationalReactDescription,
		agents.WithParserErrorHandler(agents.NewParserErrorHandler(func(s string) string {
			slog.Error("Parsing Error", "error", s)
			return ollama.ParsingErrorPrompt()
		})),

		agents.WithMaxIterations(userQuery.MaxIterations),
		agents.WithCallbacksHandler(CustomHandler{OutputChan: outputChan}),
		agents.WithMemory(mem),
	)

	if err != nil {
		slog.Error("Error initializing agent", "error", err)
		return
	}

	outputChan <- HttpJsonStreamElement{StepType: StepHandleOllamaStart}

	temp := 0.0
	prompt := fmt.Sprintf(`
    1. Format your answer (after AI:) in markdown. 
    2. You have to use your tools to answer questions. 
    3. You have to provide the sources / links you've used to answer the quesion.
    4. You may use tools more than once.
    5. Create your reply in the same language as the search string.
    Question: %s`, userQuery.Prompt)
	_, err = chains.Run(ctx, executor, prompt, chains.WithTemperature(temp))
	if err != nil {
		slog.Error("Error running agent", "error", err)
		return
	}

	outputChan <- HttpJsonStreamElement{Close: true}
}
