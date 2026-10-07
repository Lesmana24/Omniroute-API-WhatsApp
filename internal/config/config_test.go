package config

import (
	"os"
	"strings"
	"testing"
)

func TestConfigMissingRequiredEnvReturnsError(t *testing.T) {
	// Clear mandatory env variables
	os.Unsetenv("DB_HOST")
	os.Unsetenv("DB_PORT")
	os.Unsetenv("DB_USER")
	os.Unsetenv("DB_PASSWORD")
	os.Unsetenv("DB_NAME")
	os.Unsetenv("OMNIROUTE_API_BASE_URL")

	_, err := Load()
	if err == nil {
		t.Fatalf("expected error due to missing required env vars, got nil")
	}

	errMsg := err.Error()
	if !strings.Contains(errMsg, "DB_HOST") || !strings.Contains(errMsg, "DB_PASSWORD") || !strings.Contains(errMsg, "OMNIROUTE_API_BASE_URL") {
		t.Errorf("expected error message to mention missing variables, got: %s", errMsg)
	}
}

func TestConfigValidEnv(t *testing.T) {
	os.Setenv("DB_HOST", "localhost")
	os.Setenv("DB_PORT", "5432")
	os.Setenv("DB_USER", "postgres")
	os.Setenv("DB_PASSWORD", "secret123")
	os.Setenv("DB_NAME", "my_chat_db")
	os.Setenv("OMNIROUTE_API_BASE_URL", "http://localhost:20128/v1")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error with valid env: %v", err)
	}

	if cfg.DBPassword != "secret123" {
		t.Errorf("expected DBPassword 'secret123', got '%s'", cfg.DBPassword)
	}
	if cfg.OmnirouteAPIBaseURL != "http://localhost:20128/v1" {
		t.Errorf("expected OmnirouteAPIBaseURL 'http://localhost:20128/v1', got '%s'", cfg.OmnirouteAPIBaseURL)
	}
	if cfg.Port != "8080" {
		t.Errorf("expected default Port '8080', got '%s'", cfg.Port)
	}
}

func TestConfigPostgresDSN(t *testing.T) {
	cfg := &Config{
		DBHost:     "127.0.0.1",
		DBPort:     "5433",
		DBUser:     "myuser",
		DBPassword: "mypassword",
		DBName:     "mydb",
		DBSSLMode:  "disable",
	}

	expectedDSN := "postgres://myuser:mypassword@127.0.0.1:5433/mydb?sslmode=disable"
	if dsn := cfg.PostgresDSN(); dsn != expectedDSN {
		t.Errorf("expected DSN '%s', got '%s'", expectedDSN, dsn)
	}
}
