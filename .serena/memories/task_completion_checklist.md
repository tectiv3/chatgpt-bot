# Task Completion Checklist

When completing a coding task in the ChatGPT Bot project, ensure the following:

## Before Marking Task Complete

### 1. Code Quality
- [ ] Code follows Go fmt standards (`go fmt ./...`)
- [ ] No syntax errors (`go build`)
- [ ] Passes go vet (`go vet ./...`)
- [ ] Meaningful variable and function names used
- [ ] Error handling implemented properly

### 2. Functionality
- [ ] Feature works as specified
- [ ] Edge cases handled
- [ ] Proper context and timeout handling for external calls
- [ ] Database operations use transactions where appropriate

### 3. Integration
- [ ] New code integrates with existing architecture
- [ ] Follows existing patterns (e.g., Server struct methods)
- [ ] Configuration properly handled via config.json
- [ ] Logging added for important operations

### 4. Dependencies
- [ ] go.mod updated if new packages added (`go mod tidy`)
- [ ] No unnecessary dependencies introduced
- [ ] Version compatibility verified

### 5. Documentation
- [ ] Functions have appropriate comments
- [ ] Complex logic explained with inline comments
- [ ] README.md updated if adding major features
- [ ] CLAUDE.md updated if architecture changes

## Commands to Run

```bash
# Always run before considering task complete:
go fmt ./...
go build
go vet ./...

# If tests exist:
go test ./...

# Update dependencies:
go mod tidy

# Verify no issues:
go run .  # Quick test if configuration allows
```

## Common Issues to Check
- Goroutine leaks (proper cleanup)
- Resource leaks (close files, connections)
- Concurrent access (proper mutex usage)
- SQL injection (use parameterized queries)
- API key exposure (use config, not hardcoded)