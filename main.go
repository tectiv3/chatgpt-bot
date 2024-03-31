package main

// main.go

import (
	"encoding/json"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"log"
	"os"
	"time"

	"github.com/meinside/openai-go"
)

func main() {
	confFilepath := "config.json"
	if len(os.Args) == 2 {
		confFilepath = os.Args[1]
	}

	if conf, err := loadConfig(confFilepath); err == nil {
		apiKey := conf.OpenAIAPIKey
		orgID := conf.OpenAIOrganizationID
		level := logger.Error
		//if conf.Verbose {
		//	level = logger.Info
		//}
		newLogger := logger.New(
			log.New(os.Stdout, "\r\n", log.LstdFlags), // io writer
			logger.Config{
				SlowThreshold:             time.Second, // Slow SQL threshold
				LogLevel:                  level,       // Log level
				IgnoreRecordNotFoundError: true,        // Ignore ErrRecordNotFound error for logger
				ParameterizedQueries:      true,        // Don't include params in the SQL log
				Colorful:                  true,        // Disable color
			},
		)

		db, err := gorm.Open(sqlite.Open("bot.db"), &gorm.Config{Logger: newLogger})

		if err != nil {
			panic("failed to connect database")
		}

		// Migrate the schema
		if err := db.AutoMigrate(&User{}); err != nil {
			panic("failed to migrate user")
		}
		if err := db.AutoMigrate(&Chat{}); err != nil {
			panic("failed to migrate chat")
		}
		if err := db.AutoMigrate(&ChatMessage{}); err != nil {
			panic("failed to migrate chat message")
		}

		log.Printf("Allowed users: %d\n", len(conf.AllowedTelegramUsers))
		server := &Server{
			conf: conf,
			ai:   openai.NewClient(apiKey, orgID),
			db:   db,
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
