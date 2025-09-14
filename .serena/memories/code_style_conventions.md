# Code Style Conventions for ChatGPT Bot

## Go Code Style

### General Conventions
- Standard Go formatting (use `go fmt` or `gofmt`)
- CamelCase for exported identifiers, camelCase for unexported
- Meaningful variable and function names
- Struct tags for JSON and GORM annotations

### Naming Patterns
- **Structs**: PascalCase (e.g., `Server`, `Message`, `Chat`)
- **Interfaces**: Usually end with `-er` suffix (e.g., `Reader`, `Writer`)
- **Functions**: CamelCase, verb-first (e.g., `loadConfig`, `handleMessage`)
- **Constants**: PascalCase or SCREAMING_SNAKE_CASE for groups
- **Package names**: Lowercase, single word preferred

### Code Organization
- Group imports: standard library, external packages, internal packages
- Embed sync.RWMutex directly in structs for thread safety
- Use context.Context for cancellation and timeouts
- Error handling: return errors, don't panic in libraries

### Common Patterns
```go
// Struct definition with embedded mutex
type Server struct {
    sync.RWMutex
    conf      config
    users     []string
    // ...
}

// Error handling
if err != nil {
    return fmt.Errorf("failed to do X: %w", err)
}

// Context with timeout
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

// Interface checks
var _ Interface = (*Implementation)(nil)
```

### Database Models (GORM)
- Use appropriate struct tags for GORM
- Primary keys, foreign keys, and indexes defined via tags
- Timestamps: CreatedAt, UpdatedAt fields
- Soft deletes where appropriate

### Logging
- Use logrus for structured logging
- Log levels: Debug, Info, Warn, Error, Fatal
- Include context in log messages

### Comments and Documentation
- Package comments for godoc
- Function comments for exported functions
- Inline comments for complex logic
- No unnecessary comments for obvious code

### Testing (when implemented)
- Test files named `*_test.go`
- Table-driven tests preferred
- Use testify for assertions (if used)
- Mock external dependencies