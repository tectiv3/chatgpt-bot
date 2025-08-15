# Agent Instructions for ChatGPT Bot

## Build/Test Commands
- **Build**: `go build` or `make build` (builds with version info)
- **Run**: `./chatgpt-bot [config.json]` (config file optional, defaults to config.json)
- **Test**: No automated tests configured
- **Format**: `go fmt ./...`
- **Mod tidy**: `go mod tidy`

## Code Style Guidelines
- Use **camelCase** for variables and functions, **PascalCase** for types
- Package imports grouped: stdlib, third-party, local (separated by blank lines)
- Error handling: Always check and handle errors explicitly, use `log.WithField()` for structured logging
- Naming: Use descriptive names, constants in ALL_CAPS, unexported fields start with lowercase
- Types: Use custom types for domain concepts (e.g., `ToolCalls`, `RestrictConfig`)
- Database: Use GORM with custom JSON marshaling for complex types via `Value()` and `Scan()` methods
- Comments: Use Go doc comments for exported functions/types, brief and descriptive
- String operations: Use `strings.TrimSpace()` for user input validation
- Configuration: Load from JSON file with environment variable fallbacks via godotenv
- Telegram handlers: Use `tele.Context` pattern with proper error returns
- Logging: Use logrus with structured fields, set appropriate log levels
- File organization: Separate concerns into logical files (bot.go, models.go, handlers.go, etc.)
- Dependencies: External packages in go.mod, prefer established libraries