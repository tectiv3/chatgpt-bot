module github.com/tectiv3/chatgpt-bot

go 1.25.0

require (
	github.com/go-shiori/go-readability v0.0.0-20251205110129-5db1dc9836f0
	github.com/google/uuid v1.6.0
	github.com/joho/godotenv v1.5.1
	github.com/sirupsen/logrus v1.9.4
	github.com/tectiv3/anthropic-go v0.1.2
	github.com/telegram-mini-apps/init-data-golang v1.5.0
	golang.org/x/crypto v0.48.0
	gopkg.in/telebot.v3 v3.3.8
	gorm.io/driver/sqlite v1.6.0
	gorm.io/gorm v1.31.1
)

require (
	github.com/andybalholm/cascadia v1.3.3 // indirect
	github.com/araddon/dateparse v0.0.0-20210429162001-6b43995a97de // indirect
	github.com/go-shiori/dom v0.0.0-20230515143342-73569d674e1c // indirect
	github.com/gogs/chardet v0.0.0-20211120154057-b7413eaefb8f // indirect
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/jinzhu/now v1.1.5 // indirect
	github.com/mattn/go-sqlite3 v1.14.34 // indirect
	golang.org/x/net v0.51.0 // indirect
	golang.org/x/sys v0.41.0 // indirect
	golang.org/x/term v0.40.0 // indirect
	golang.org/x/text v0.34.0 // indirect
)

replace gopkg.in/telebot.v3 => github.com/tectiv3/telebot v0.0.0-20260301132725-f54b1d46d473
