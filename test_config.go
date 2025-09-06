//go:build test
// +build test

package main

import (
	"os"
	"testing"
)

// TestMain is the entry point for running tests
func TestMain(m *testing.M) {
	// Setup test environment
	setupTestEnvironment()
	
	// Run tests
	code := m.Run()
	
	// Cleanup test environment
	cleanupTestEnvironment()
	
	// Exit with test result code
	os.Exit(code)
}

// setupTestEnvironment prepares the test environment
func setupTestEnvironment() {
	// Set test mode
	os.Setenv("TEST_MODE", "true")
	
	// Disable verbose logging during tests
	os.Setenv("LOG_LEVEL", "ERROR")
	
	// Set test database configuration
	os.Setenv("DB_TYPE", "sqlite")
	os.Setenv("DB_PATH", ":memory:")
	
	// Disable external services during tests
	os.Setenv("DISABLE_EXTERNAL_CALLS", "true")
}

// cleanupTestEnvironment cleans up after tests
func cleanupTestEnvironment() {
	// Remove test environment variables
	os.Unsetenv("TEST_MODE")
	os.Unsetenv("LOG_LEVEL")
	os.Unsetenv("DB_TYPE")
	os.Unsetenv("DB_PATH")
	os.Unsetenv("DISABLE_EXTERNAL_CALLS")
}

// TestConfig holds configuration for tests
type TestConfig struct {
	DatabaseURL         string
	EnableParallelTests bool
	TestTimeout         int // in seconds
	MockExternalAPIs    bool
}

// GetTestConfig returns the test configuration
func GetTestConfig() TestConfig {
	return TestConfig{
		DatabaseURL:         ":memory:",
		EnableParallelTests: true,
		TestTimeout:         300, // 5 minutes
		MockExternalAPIs:    true,
	}
}

// IsTestMode returns true if running in test mode
func IsTestMode() bool {
	return os.Getenv("TEST_MODE") == "true"
}

// ShouldMockExternalAPIs returns true if external APIs should be mocked
func ShouldMockExternalAPIs() bool {
	return os.Getenv("DISABLE_EXTERNAL_CALLS") == "true"
}