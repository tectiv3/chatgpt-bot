package main

import (
	"context"
	"fmt"
	"github.com/tectiv3/chatgpt-bot/chain"
	llm_tools "github.com/tectiv3/chatgpt-bot/tools"
	"github.com/tectiv3/chatgpt-bot/types"
	"github.com/tmc/langchaingo/agents"
	"github.com/tmc/langchaingo/chains"
	"github.com/tmc/langchaingo/llms/openai"
	"github.com/tmc/langchaingo/memory"
	"github.com/tmc/langchaingo/tools"
	"log/slog"
)

type Sessions map[string]*memory.ConversationBuffer

var sessions = make(Sessions)

func parsingErrorPrompt() string {
	return "Parsing Error: Check your output and make sure it conforms to the format."
}

func (s *Server) startAgent(ctx context.Context, outputChan chan<- types.HttpJsonStreamElement, userQuery types.ClientQuery) {
	//startTime := time.Now()
	session := userQuery.Session

	s.Lock()
	if sessions[session] == nil {
		slog.Info("Creating new session", "session", session)
		sessions[session] = memory.NewConversationBuffer()
		memory.NewChatMessageHistory()
		outputChan <- types.HttpJsonStreamElement{
			StepType: types.StepHandleNewSession,
			Session:  session,
			Stream:   false,
		}
	}
	mem := sessions[session]
	s.Unlock()

	slog.Info("Starting agent chain", "session", session) //, "userQuery", userQuery, "startTime", startTime)

	llm, _ := openai.New(openai.WithModel(userQuery.ModelName))

	agentTools := []tools.Tool{
		tools.Calculator{},
		llm_tools.WebSearch{
			CallbacksHandler: chain.CustomHandler{
				OutputChan: outputChan,
			},
			SessionString: session,
		},
		llm_tools.SearchVectorDB{
			CallbacksHandler: chain.CustomHandler{
				OutputChan: outputChan,
			},
			SessionString: session,
		},
	}

	executor, err := agents.Initialize(
		llm,
		agentTools,
		agents.ConversationalReactDescription,
		agents.WithParserErrorHandler(agents.NewParserErrorHandler(func(s string) string {
			slog.Error("Parsing Error", "error", s)
			return parsingErrorPrompt()
		})),

		agents.WithMaxIterations(userQuery.MaxIterations),
		agents.WithCallbacksHandler(chain.CustomHandler{OutputChan: outputChan}),
		agents.WithMemory(mem),
	)

	if err != nil {
		slog.Error("Error initializing agent", "error", err)
		return
	}

	outputChan <- types.HttpJsonStreamElement{
		StepType: types.StepHandleOllamaStart,
		Session:  session,
		Stream:   false,
	}

	temp := 0.0
	prompt := fmt.Sprintf(`
    1. Format your answer (after AI:) in valid Telegram MarkDown V1 markup every time. Use STRICTLY ONLY simple telegram markdown v1 markup.
    2. You have to use your tools to answer questions.
    3. You have to provide the sources / links you've used to answer the quesion. 
    4. You may use tools more than once.
    5. Create your reply in the same language as the search string.
	6. Do not confuse your own instructions with users question.
    Question: %s`, userQuery.Prompt)
	_, err = chains.Run(ctx, executor, prompt, chains.WithTemperature(temp))
	if err != nil {
		slog.Error("Error running agent", "error", err)
		return
	}

	outputChan <- types.HttpJsonStreamElement{Close: true}
}
