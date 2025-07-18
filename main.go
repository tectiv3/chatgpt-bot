package main

// main.go

import (
	"encoding/json"
	"fmt"
	stdlog "log"
	"os"
	"path"
	"runtime"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/meinside/openai-go"
	log "github.com/sirupsen/logrus"
	"github.com/tectiv3/anthropic-go"
	"github.com/tectiv3/awsnova-go"
	"github.com/tectiv3/chatgpt-bot/i18n"
	"golang.org/x/crypto/ssh/terminal"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var (
	Log *log.Entry
	l   *i18n.Localizer
	// Version string will be set by linker
	Version   = "dev"
	BuildTime = "unknown"
)

func main() {
	logrus := log.New()
	// redirect Go standard log library calls to logrus writer
	stdlog.SetFlags(0)
	stdlog.SetFlags(stdlog.LstdFlags | stdlog.Lshortfile)
	logrus.Formatter = &log.TextFormatter{
		FullTimestamp:    true,
		DisableTimestamp: !terminal.IsTerminal(int(os.Stdout.Fd())),
		TimestampFormat:  "Jan 2 15:04:05.000",
		CallerPrettyfier: func(f *runtime.Frame) (string, string) {
			return strings.Replace(f.Function, "(*Server).", "", -1),
				fmt.Sprintf("%s:%d", path.Base(f.File), f.Line)
		},
	}
	logrus.SetFormatter(logrus.Formatter)
	logrus.SetReportCaller(true)
	stdlog.SetOutput(logrus.Writer())
	logrus.SetOutput(os.Stdout)
	Log = logrus.WithFields(log.Fields{"ver": Version})

	confFilepath := "config.json"
	if len(os.Args) == 2 {
		confFilepath = os.Args[1]
	}

	if conf, err := loadConfig(confFilepath); err == nil {
		apiKey := conf.OpenAIAPIKey
		orgID := conf.OpenAIOrganizationID
		level := logger.Error
		if conf.Verbose {
			//	level = logger.Info
			logrus.SetLevel(log.DebugLevel)
		}
		newLogger := logger.New(
			stdlog.New(os.Stdout, "\r\n", stdlog.LstdFlags), // io writer
			logger.Config{
				SlowThreshold:             time.Second, // Slow SQL threshold
				LogLevel:                  level,       // Log level
				IgnoreRecordNotFoundError: true,        // Ignore ErrRecordNotFound error for logger
				ParameterizedQueries:      false,       // Don't include params in the SQL log
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
		if err := db.AutoMigrate(&Role{}); err != nil {
			panic("failed to migrate role")
		}

		Log.WithField("allowed_users", len(conf.AllowedTelegramUsers)).Info("Started")
		server := &Server{
			conf:   conf,
			db:     db,
			openAI: openai.NewClient(apiKey, orgID),
			nova: awsnova.NewClient(conf.AWSRegion, conf.AWSModelID, awsnova.AWSCredentials{
				AccessKeyID:     conf.AWSAccessKeyID,
				SecretAccessKey: conf.AWSSecretAccessKey,
			}),
		}
		if conf.AnthropicEnabled {
			server.anthropic = anthropic.New(anthropic.WithAPIKey(conf.AnthropicAPIKey))
		}
		if conf.GeminiEnabled {
			server.gemini = openai.NewClient(conf.GeminiAPIKey, "").SetBaseURL("https://generativelanguage.googleapis.com/v1beta/openai")
		}
		l = i18n.New("ru", "en")

		server.run()
	} else {
		Log.Warn("failed to load config", "error=", err)
	}
}

// load config at given path
func loadConfig(fpath string) (conf config, err error) {
	if err := godotenv.Load(); err != nil {
		log.WithField("error", err).Warn("Error loading .env file")
	}

	var bytes []byte
	if bytes, err = os.ReadFile(fpath); err == nil {
		if err = json.Unmarshal(bytes, &conf); err == nil {
			return conf, nil
		}
	}

	return config{}, err
}
