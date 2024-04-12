package ollama

import (
	"context"
	"fmt"
	"github.com/ollama/ollama/api"
	log "github.com/sirupsen/logrus"
	"github.com/tmc/langchaingo/llms/ollama"
	"os"
)

var EmbeddingsModel = "nomic-embed-text:v1.5"

func NewOllamaEmbeddingLLM() (*ollama.LLM, error) {
	return NewOllama(EmbeddingsModel, os.Getenv("OLLAMA_HOST"))
}

func NewOllama(modelName, URL string) (*ollama.LLM, error) {
	return ollama.New(ollama.WithModel(modelName), ollama.WithServerURL(URL), ollama.WithRunnerNumCtx(16000))
}

func GetOllamaModelList() ([]string, error) {
	client, err := api.ClientFromEnvironment()
	if err != nil {
		return nil, err
	}
	models, err := client.List(context.Background())
	if err != nil {
		return nil, err
	}
	modelNames := make([]string, 0)
	for _, model := range models.Models {
		modelNames = append(modelNames, model.Name)
	}
	return modelNames, nil
}

func CheckIfModelExistsOrPull(modelName string) error {
	if err := CheckIfModelExists(modelName); err != nil {
		log.WithField("model", modelName).Info("Model does not exist, pulling it")
		if err := OllamaPullModel(modelName); err != nil {
			return err
		}
	}
	return nil
}

func CheckIfModelExists(requestName string) error {
	modelNames, err := GetOllamaModelList()
	if err != nil {
		return err
	}
	for _, mn := range modelNames {
		if requestName == mn {
			return nil
		}
	}
	return fmt.Errorf("model %s does not exist", requestName)
}

func OllamaPullModel(modelName string) error {
	pullReq := api.PullRequest{
		Model:    modelName,
		Insecure: false,
		Name:     modelName,
	}
	client, err := api.ClientFromEnvironment()
	if err != nil {
		return err
	}
	return client.Pull(context.Background(), &pullReq, pullProgressHandler)
}

var lastProgress string

func pullProgressHandler(progress api.ProgressResponse) error {
	percentage := progressPercentage(progress)
	if percentage != lastProgress {
		log.Info("Pulling model: ", percentage)
		lastProgress = percentage
	}
	return nil
}

func progressPercentage(progress api.ProgressResponse) string {
	return fmt.Sprintf("%d", (progress.Completed*100)/(progress.Total+1))
}

func ParsingErrorPrompt() string {
	return "Parsing Error: Check your output and make sure it conforms to the format."
}

func FormatTextAsMarkdownPrompt(text string) string {
	return fmt.Sprintf("Format the following text in fancy markdown: '%s'. Only write the formatted text, do not write something like 'Here is the fancy text:' just write the text. Do not surround your answer with a codeblock. Use all information provided.", text)
}
