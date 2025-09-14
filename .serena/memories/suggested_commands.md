# Suggested Commands for ChatGPT Bot Development

## Build and Run Commands
```bash
# Build the application
go build

# Build with version and timestamp
make build

# Run the application
./chatgpt-bot

# Install Go dependencies
go mod download
go mod tidy

# Update dependencies
go get -u ./...
```

## Development Commands
```bash
# Format Go code
go fmt ./...
gofmt -w .

# Check for issues
go vet ./...

# Run staticcheck (if installed)
staticcheck ./...

# Generate mocks or test coverage (when tests are added)
go test ./...
go test -cover ./...
```

## System Commands (Darwin/macOS)
```bash
# Install system dependencies
brew install lame opusfile pkg-config

# File operations
ls -la           # List files with details
find . -name "*.go"  # Find Go files
grep -r "pattern" .  # Search in files
rg "pattern"     # Faster search with ripgrep (if installed)

# Git commands
git status
git diff
git add .
git commit -m "message"
git push origin branch-name
git pull origin branch-name

# Process management
ps aux | grep chatgpt-bot  # Find running process
kill -9 PID              # Kill process
lsof -i :PORT           # Check port usage
```

## Configuration Setup
```bash
# Create config from sample
cp config.json.sample config.json

# Edit configuration
nano config.json  # or vim, code, etc.
```

## Deployment
```bash
# Deploy script
./deploy.sh

# Systemd service (on Linux servers)
systemctl start chatgpt-bot
systemctl stop chatgpt-bot
systemctl restart chatgpt-bot
systemctl status chatgpt-bot
```

## Database Operations
```bash
# SQLite commands (if needed)
sqlite3 database.db
.tables
.schema
.quit
```