package main

// bot.go

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	openai "github.com/meinside/openai-go"
	tg "github.com/meinside/telegram-bot-go"
)

const (
	intervalSeconds = 1

	cmdStart           = "/start"
	cmdReset           = "/reset"
	msgStart           = "This bot will answer your messages with ChatGPT API"
	msgReset           = "This bots memory erased"
	msgCmdNotSupported = "Unknown command: %s"
)

// config struct for loading a configuration file
type config struct {
	// telegram bot api
	TelegramBotToken string `json:"telegram_bot_token"`

	// openai api
	OpenAIAPIKey         string `json:"openai_api_key"`
	OpenAIOrganizationID string `json:"openai_org_id"`

	// other configurations
	AllowedTelegramUsers []string `json:"allowed_telegram_users"`
	Verbose              bool     `json:"verbose,omitempty"`
	Model                string   `json:"openai_model"`
}

// DB contains chat history
type DB struct {
	chats map[int64]Chat
}

// Chat is chat history by chatid
type Chat struct {
	history []openai.ChatMessage
}

var db DB

func init() {
	db = DB{chats: make(map[int64]Chat)}
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

// launch bot with given parameters
func runBot(conf config) {
	token := conf.TelegramBotToken
	apiKey := conf.OpenAIAPIKey
	orgID := conf.OpenAIOrganizationID

	allowedUsers := map[string]bool{}
	for _, user := range conf.AllowedTelegramUsers {
		allowedUsers[user] = true
	}

	bot := tg.NewClient(token)
	client := openai.NewClient(apiKey, orgID)

	if b := bot.GetMe(); b.Ok {
		log.Printf("launching bot: %s", userName(b.Result))

		bot.StartMonitoringUpdates(0, intervalSeconds, func(b *tg.Bot, update tg.Update, err error) {
			if isAllowed(update, allowedUsers) {
				var message *tg.Message

				if update.HasMessage() && update.Message.HasText() {
					message = update.Message
				} else if update.HasEditedMessage() && update.EditedMessage.HasText() {
					message = update.EditedMessage
				}

				chatID := message.Chat.ID
				userID := message.From.ID
				txt := *message.Text

				if !strings.HasPrefix(txt, "/") {
					// classify message
					// if reason, flagged := isFlagged(client, txt); flagged {
					// send(bot, conf, fmt.Sprintf("Could not handle message: %s.", reason), chatID)
					// } else {
					messageID := message.MessageID
					answer(bot, client, conf, txt, chatID, userID, messageID)
					// }
				} else {
					switch txt {
					case cmdStart:
						send(bot, conf, msgStart, chatID)
					case cmdReset:
						db.chats[chatID] = Chat{history: []openai.ChatMessage{}}
						send(bot, conf, msgReset, chatID)
					// TODO: process more bot commands here
					default:
						send(bot, conf, fmt.Sprintf(msgCmdNotSupported, txt), chatID)
					}
				}
			} else {
				log.Printf("not allowed: %s", userNameFromUpdate(&update))
			}
		})
	} else {
		log.Printf("failed to get bot info: %s", *b.Description)
	}
}

// checks if given update is allowed or not
func isAllowed(update tg.Update, allowedUsers map[string]bool) bool {
	var username string
	if update.HasMessage() && update.Message.From.Username != nil {
		username = *update.Message.From.Username
	} else if update.HasEditedMessage() && update.EditedMessage.From.Username != nil {
		username = *update.EditedMessage.From.Username
	}

	if _, exists := allowedUsers[username]; exists {
		return true
	}

	return false
}

// send given message to the chat
func send(bot *tg.Bot, conf config, message string, chatID int64) {
	bot.SendChatAction(chatID, tg.ChatActionTyping, nil)

	if conf.Verbose {
		log.Printf("[verbose] sending message to chat(%d): '%s'", chatID, message)
	}

	if res := bot.SendMessage(chatID, message, tg.OptionsSendMessage{}); !res.Ok {
		log.Printf("failed to send message: %s", *res.Description)
	}
}

// generate an answer to given message and send it to the chat
func answer(bot *tg.Bot, client *openai.Client, conf config, message string, chatID, userID, messageID int64) {
	bot.SendChatAction(chatID, tg.ChatActionTyping, nil)
	msg := openai.NewChatUserMessage(message)

	var chat Chat
	chat, ok := db.chats[chatID]
	if !ok {
		chat = Chat{history: []openai.ChatMessage{}}
	}

	chat.history = append(chat.history, msg)

	response, err := client.CreateChatCompletion(conf.Model, chat.history, openai.ChatCompletionOptions{}.SetUser(userAgent(userID)))
	if err != nil {
		log.Printf("failed to create chat completion: %s", err)
		return
	}
	if conf.Verbose {
		log.Printf("[verbose] %s ===> %+v", message, response.Choices)
	}

	// bot.SendChatAction(chatID, tg.ChatActionTyping, nil)

	var answer string
	if len(response.Choices) > 0 {
		answer = response.Choices[0].Message.Content
	} else {
		answer = "No response from API."
	}

	if conf.Verbose {
		log.Printf("[verbose] sending answer to chat(%d): '%s'", chatID, answer)
	}

	chat.history = append(chat.history, openai.NewChatAssistantMessage(answer))
	db.chats[chatID] = chat

	if res := bot.SendMessage(
		chatID,
		answer,
		tg.OptionsSendMessage{}.
			SetReplyToMessageID(messageID)); !res.Ok {
		log.Printf("failed to answer message '%s' with '%s': %s", message, answer, err)
	}

}

// check if given message is flagged or not
func isFlagged(client *openai.Client, message string) (output string, flagged bool) {
	if response, err := client.CreateModeration(message, openai.ModerationOptions{}); err == nil {
		for _, classification := range response.Results {
			if classification.Flagged {
				categories := []string{}

				for k, v := range classification.Categories {
					if v {
						categories = append(categories, k)
					}
				}

				return fmt.Sprintf("'%s' was flagged due to following reason(s): %s", message, strings.Join(categories, ", ")), true
			}
		}

		return "", false
	} else {
		return fmt.Sprintf("failed to classify message: '%s' with error: %s", message, err), true
	}
}

// generate a user-agent value
func userAgent(userID int64) string {
	return fmt.Sprintf("telegram-chatgpt-bot:%d", userID)
}

// generate user's name
func userName(user *tg.User) string {
	if user.Username != nil {
		return fmt.Sprintf("@%s (%s)", *user.Username, user.FirstName)
	} else {
		return user.FirstName
	}
}

// generate user's name from update
func userNameFromUpdate(update *tg.Update) string {
	var user *tg.User
	if update.HasMessage() {
		user = update.Message.From
	} else if update.HasEditedMessage() {
		user = update.EditedMessage.From
	}

	return userName(user)
}
