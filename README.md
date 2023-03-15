# Telegram ChatGPT Bot

A telegram bot which answers to messages with [ChatGPT API](https://platform.openai.com/docs/api-reference/chat).

## Configurations

Create a configuration file:

```bash
$ cp config.json.sample config.json
```

and set your values:

```json
{
    "telegram_bot_token": "123456:abcdefghijklmnop-QRSTUVWXYZ7890",
    "openai_api_key": "key-ABCDEFGHIJK1234567890",
    "openai_org_id": "org-1234567890abcdefghijk",
    "allowed_telegram_users": ["user1", "user2"],
    "openai_model": "gpt-3.5-turbo",
    "verbose": false
}
```

## Build

```bash
$ go build
```

## Run

Run the built binary with the config file's path:

```bash
$ ./chatgpt-bot
```

## Run as a systemd service

Createa a systemd service file:

```
[Unit]
Description=Telegram ChatGPT Bot
After=syslog.target
After=network.target

[Service]
Type=simple
User=ubuntu
Group=ubuntu
WorkingDirectory=/dir/to/chatgpt-bot
ExecStart=/dir/to/chatgpt-bot/chatgpt-bot /path/to/config.json
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

and `systemctl` enable|start|restart|stop the service.

## License

The MIT License (MIT)

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.

