package types

import (
	"database/sql/driver"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/meinside/openai-go"
	tele "gopkg.in/telebot.v3"
	"io"
)

// ToolCalls is a custom type that will allow us to implement
// the driver.Valuer and sql.Scanner interfaces on a slice of ToolCall.
type ToolCalls []ToolCall

type ToolCall struct {
	ID       string                  `json:"id"`
	Type     string                  `json:"type"` // == 'function'
	Function openai.ToolCallFunction `json:"function"`
}

// Value implements the driver.Valuer interface, allowing
// for converting the ToolCalls to a JSON string for database storage.
func (tc ToolCalls) Value() (driver.Value, error) {
	if tc == nil {
		return nil, nil
	}
	return json.Marshal(tc)
}

// Scan implements the sql.Scanner interface, allowing for
// converting a JSON string from the database back into the ToolCalls slice.
func (tc *ToolCalls) Scan(value interface{}) error {
	if value == nil {
		*tc = nil
		return nil
	}

	b, ok := value.([]byte)
	if !ok {
		return fmt.Errorf("type assertion to []byte failed")
	}

	return json.Unmarshal(b, &tc)
}

type GPTResponse interface {
	Type() string       // direct, array, image, audio, async
	Value() interface{} // string, []string
	CanReply() bool     // if true replyMenu need to be shown
}

// WAV writer struct
type wavWriter struct {
	w io.Writer
}

// WAV file header struct
type wavHeader struct {
	RIFFID        [4]byte // RIFF header
	FileSize      uint32  // file size - 8
	WAVEID        [4]byte // WAVE header
	FMTID         [4]byte // fmt header
	Subchunk1Size uint32  // size of the fmt chunk
	AudioFormat   uint16  // audio format code
	NumChannels   uint16  // number of channels
	SampleRate    uint32  // sample rate
	ByteRate      uint32  // bytes per second
	BlockAlign    uint16  // block align
	BitsPerSample uint16  // bits per sample
	DataID        [4]byte // data header
	Subchunk2Size uint32  // size of the data chunk
}

// RestrictConfig defines config for Restrict middleware.
type RestrictConfig struct {
	// Chats is a list of chats that are going to be affected
	// by either In or Out function.
	Usernames []string

	// In defines a function that will be called if the chat
	// of an update will be found in the Chats list.
	In tele.HandlerFunc

	// Out defines a function that will be called if the chat
	// of an update will NOT be found in the Chats list.
	Out tele.HandlerFunc
}

func in_array(needle string, haystack []string) bool {
	for _, v := range haystack {
		if needle == v {
			return true
		}
	}

	return false
}

type CoinCap struct {
	Data struct {
		Symbol   string `json:"symbol"`
		PriceUsd string `json:"priceUsd"`
	} `json:"data"`
	Timestamp int64 `json:"timestamp"`
}

type StepType string

const (
	StepHandleAgentAction             StepType = "HandleAgentAction"
	StepHandleAgentFinish             StepType = "HandleAgentFinish"
	StepHandleChainEnd                StepType = "HandleChainEnd"
	StepHandleChainError              StepType = "HandleChainError"
	StepHandleChainStart              StepType = "HandleChainStart"
	StepHandleFinalAnswer             StepType = "HandleFinalAnswer"
	StepHandleLLMGenerateContentEnd   StepType = "HandleLLMGenerateContentEnd"
	StepHandleLLMGenerateContentStart StepType = "HandleLLMGenerateContentStart"
	StepHandleLlmEnd                  StepType = "HandleLlmEnd"
	StepHandleLlmError                StepType = "HandleLlmError"
	StepHandleLlmStart                StepType = "HandleLlmStart"
	StepHandleNewSession              StepType = "HandleNewSession"
	StepHandleOllamaStart             StepType = "HandleOllamaStart"
	StepHandleParseError              StepType = "HandleParseError"
	StepHandleRetrieverEnd            StepType = "HandleRetrieverEnd"
	StepHandleRetrieverStart          StepType = "HandleRetrieverStart"
	StepHandleSourceAdded             StepType = "HandleSourceAdded"
	StepHandleToolEnd                 StepType = "HandleToolEnd"
	StepHandleToolError               StepType = "HandleToolError"
	StepHandleToolStart               StepType = "HandleToolStart"
	StepHandleVectorFound             StepType = "HandleVectorFound"
)

type ClientQuery struct {
	Prompt        string `json:"prompt"`
	MaxIterations int    `json:"maxIterations"`
	ModelName     string `json:"modelName"`
	Session       string `json:"session"`
}

type Source struct {
	Name    string `json:"name"`
	Link    string `json:"link"`
	Summary string `json:"summary"`
}

type HttpJsonStreamElement struct {
	Close    bool     `json:"close"`
	Message  string   `json:"message"`
	Stream   bool     `json:"stream"`
	StepType StepType `json:"stepType"`
	Source   Source   `json:"source"`
	Session  string   `json:"session"`
}

func toBase64(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}
