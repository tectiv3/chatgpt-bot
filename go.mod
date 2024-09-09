module github.com/tectiv3/chatgpt-bot

go 1.22.5

toolchain go1.22.6

require (
	github.com/PuerkitoBio/goquery v1.9.2
	github.com/amikos-tech/chroma-go v0.1.4
	github.com/eminarican/safetypes v0.0.8
	github.com/go-shiori/go-readability v0.0.0-20240701094332-1070de7e32ef
	github.com/google/uuid v1.6.0
	github.com/joho/godotenv v1.5.1
	github.com/meinside/openai-go v0.4.7
	github.com/pkoukk/tiktoken-go v0.1.7
	github.com/sirupsen/logrus v1.9.3
	github.com/tectiv3/go-lame v0.0.0-20240321153525-da7c3c48f794
	golang.org/x/crypto v0.26.0
	golang.org/x/exp v0.0.0-20240823005443-9b4947da3948
	golang.org/x/net v0.28.0
	gopkg.in/telebot.v3 v3.3.8
	gorm.io/driver/sqlite v1.5.6
	gorm.io/gorm v1.25.11
)

require (
	github.com/Masterminds/semver v1.5.0 // indirect
	github.com/andybalholm/cascadia v1.3.2 // indirect
	github.com/araddon/dateparse v0.0.0-20210429162001-6b43995a97de // indirect
	github.com/dlclark/regexp2 v1.11.4 // indirect
	github.com/go-shiori/dom v0.0.0-20230515143342-73569d674e1c // indirect
	github.com/gogs/chardet v0.0.0-20211120154057-b7413eaefb8f // indirect
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/jinzhu/now v1.1.5 // indirect
	github.com/mattn/go-sqlite3 v1.14.22 // indirect
	github.com/oklog/ulid v1.3.1 // indirect
	golang.org/x/sys v0.24.0 // indirect
	golang.org/x/term v0.23.0 // indirect
	golang.org/x/text v0.17.0 // indirect
)

replace github.com/meinside/openai-go => github.com/tectiv3/openai-go v0.4.8-0.20240413013544-cb9b1058c0f9
