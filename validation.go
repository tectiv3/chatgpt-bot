package main

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Input validation errors
var (
	ErrInvalidInput  = errors.New("invalid input")
	ErrInputTooShort = errors.New("input too short")
	ErrInputTooLong  = errors.New("input too long")
	ErrInvalidFormat = errors.New("invalid format")
	ErrInvalidRange  = errors.New("value out of range")
)

// Validation constraints
const (
	MinUsernameLength = 3
	MaxUsernameLength = 32
	MinPromptLength   = 3
	MaxPromptLength   = 4000
	MinRoleNameLength = 1
	MaxRoleNameLength = 50
	MaxFileSize       = 10 * 1024 * 1024 // 10MB
	MinAge            = 1
	MaxAge            = 365
)

// Input validation functions

// ValidateUsername validates telegram username
func ValidateUsername(username string) error {
	if username == "" {
		return fmt.Errorf("%w: username cannot be empty", ErrInvalidInput)
	}

	if len(username) < MinUsernameLength {
		return fmt.Errorf("%w: username must be at least %d characters", ErrInputTooShort, MinUsernameLength)
	}

	if len(username) > MaxUsernameLength {
		return fmt.Errorf("%w: username must be at most %d characters", ErrInputTooLong, MaxUsernameLength)
	}

	// Username should contain only alphanumeric characters and underscores
	matched, _ := regexp.MatchString(`^[a-zA-Z0-9_]+$`, username)
	if !matched {
		return fmt.Errorf("%w: username can only contain letters, numbers, and underscores", ErrInvalidFormat)
	}

	return nil
}

// ValidatePrompt validates user prompt input
func ValidatePrompt(prompt string) error {
	if strings.TrimSpace(prompt) == "" {
		return fmt.Errorf("%w: prompt cannot be empty", ErrInvalidInput)
	}

	if len(prompt) < MinPromptLength {
		return fmt.Errorf("%w: prompt must be at least %d characters", ErrInputTooShort, MinPromptLength)
	}

	if len(prompt) > MaxPromptLength {
		return fmt.Errorf("%w: prompt must be at most %d characters", ErrInputTooLong, MaxPromptLength)
	}

	return nil
}

// ValidateRoleName validates role name input
func ValidateRoleName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("%w: role name cannot be empty", ErrInvalidInput)
	}

	if len(name) < MinRoleNameLength {
		return fmt.Errorf("%w: role name must be at least %d character", ErrInputTooShort, MinRoleNameLength)
	}

	if len(name) > MaxRoleNameLength {
		return fmt.Errorf("%w: role name must be at most %d characters", ErrInputTooLong, MaxRoleNameLength)
	}

	return nil
}

// ValidateAge validates conversation age input
func ValidateAge(ageStr string) (int, error) {
	if ageStr == "" {
		return 0, fmt.Errorf("%w: age cannot be empty", ErrInvalidInput)
	}

	age, err := strconv.Atoi(ageStr)
	if err != nil {
		return 0, fmt.Errorf("%w: age must be a number", ErrInvalidFormat)
	}

	if age < MinAge || age > MaxAge {
		return 0, fmt.Errorf("%w: age must be between %d and %d days", ErrInvalidRange, MinAge, MaxAge)
	}

	return age, nil
}

// ValidateTemperature validates temperature input
func ValidateTemperature(tempStr string) (float64, error) {
	if tempStr == "" {
		return 0, fmt.Errorf("%w: temperature cannot be empty", ErrInvalidInput)
	}

	temp, err := strconv.ParseFloat(tempStr, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: temperature must be a number", ErrInvalidFormat)
	}

	if temp < 0.0 || temp > 1.0 {
		return 0, fmt.Errorf("%w: temperature must be between 0.0 and 1.0", ErrInvalidRange)
	}

	return temp, nil
}

// ValidateLanguageCode validates language code input
func ValidateLanguageCode(lang string) error {
	if lang == "" {
		return fmt.Errorf("%w: language code cannot be empty", ErrInvalidInput)
	}

	// Language codes should be 2-5 characters (e.g., "en", "en-US")
	if len(lang) < 2 || len(lang) > 5 {
		return fmt.Errorf("%w: language code must be 2-5 characters", ErrInvalidFormat)
	}

	// Basic format validation for language codes
	matched, _ := regexp.MatchString(`^[a-z]{2}(-[A-Z]{2})?$`, lang)
	if !matched {
		return fmt.Errorf("%w: invalid language code format", ErrInvalidFormat)
	}

	return nil
}

// ValidateFileSize validates uploaded file size
func ValidateFileSize(size int64) error {
	if size <= 0 {
		return fmt.Errorf("%w: file size must be greater than 0", ErrInvalidInput)
	}

	if size > MaxFileSize {
		return fmt.Errorf("%w: file size must be less than %d bytes", ErrInputTooLong, MaxFileSize)
	}

	return nil
}
