module github.com/tectiv3/chatgpt-bot

go 1.20

require (
	github.com/meinside/openai-go v0.0.5
	github.com/sunicy/go-lame v0.0.0-20200422031049-1c192eaafa39
	gopkg.in/telebot.v3 v3.1.3
	gorm.io/driver/sqlite v1.5.0
	gorm.io/gorm v1.25.0
)

require (
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/jinzhu/now v1.1.5 // indirect
	github.com/kisielk/sqlstruct v0.0.0-20210630145711-dae28ed37023 // indirect
	github.com/mattn/go-sqlite3 v1.14.16 // indirect
)

replace github.com/meinside/openai-go => github.com/tectiv3/openai-go v0.0.6
