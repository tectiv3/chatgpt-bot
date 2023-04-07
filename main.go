package main

// main.go

import (
	"encoding/json"
	"log"
	"os"

	openai "github.com/meinside/openai-go"
)

func main() {
	confFilepath := "config.json"
	if len(os.Args) == 2 {
		confFilepath = os.Args[1]
	}

	if conf, err := loadConfig(confFilepath); err == nil {
		apiKey := conf.OpenAIAPIKey
		orgID := conf.OpenAIOrganizationID
		allowedUsers := map[string]bool{}
		for _, user := range conf.AllowedTelegramUsers {
			allowedUsers[user] = true
		}

		server := &Server{
			conf:  conf,
			ai:    openai.NewClient(apiKey, orgID),
			users: allowedUsers,
			db:    DB{chats: make(map[int64]Chat)},
		}

		server.run()
	} else {
		log.Printf("failed to load config: %s", err)
	}
}

// load config at given path
func loadConfig(fpath string) (conf config, err error) {
	var bytes []byte
	if bytes, err = os.ReadFile(fpath); err == nil {
		if err = json.Unmarshal(bytes, &conf); err == nil {
			return conf, nil
		}
	}

	return config{}, err
}
