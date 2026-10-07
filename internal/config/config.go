package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"

	"omniroute-api-wa/internal"
)

// Config represents all application configuration parameters.
type Config struct {
	// Server
	Port    string
	GinMode string

	// Database (PostgreSQL) - Required in .env
	DBHost     string
	DBPort     string
	DBUser     string
	DBPassword string
	DBName     string
	DBSSLMode  string

	// Omniroute AI API - Required in .env
	OmnirouteAPIBaseURL string
	OmnirouteAPIKey     string
	OmnirouteModel      string

	// Conversation Context
	MaxContextMessages int

	// WhatsApp Session Storage
	WhatsAppSessionStore string // "postgres" or "sqlite"
	WhatsAppSQLitePath   string // file path if session store is sqlite

	// Worker Pool Concurrency
	WorkerPoolSize  int
	WorkerQueueSize int
}

// Load reads configuration from .env file and validates mandatory environment variables.
func Load() (*Config, error) {
	// Attempt to load .env file if it exists, ignore if not found (e.g. injected in production/container)
	_ = godotenv.Load()

	cfg := &Config{
		Port:                 getEnv("APP_PORT", getEnv("PORT", "8080")),
		GinMode:              getEnv("GIN_MODE", "release"),
		DBHost:               getEnv("DB_HOST", ""),
		DBPort:               getEnv("DB_PORT", ""),
		DBUser:               getEnv("DB_USER", ""),
		DBPassword:           getEnv("DB_PASSWORD", ""),
		DBName:               getEnv("DB_NAME", ""),
		DBSSLMode:            getEnv("DB_SSLMODE", "disable"),
		OmnirouteAPIBaseURL:  strings.TrimRight(getEnv("OMNIROUTE_API_BASE_URL", ""), "/"),
		OmnirouteAPIKey:      getEnv("OMNIROUTE_API_KEY", ""),
		OmnirouteModel:       getEnv("OMNIROUTE_MODEL", "auto"),
		MaxContextMessages:   getEnvAsInt("MAX_CONTEXT_MESSAGES", internal.DefaultMaxContextMessages),
		WhatsAppSessionStore: strings.ToLower(getEnv("WHATSAPP_SESSION_STORE", "postgres")),
		WhatsAppSQLitePath:   getEnv("WHATSAPP_SQLITE_PATH", "whatsapp_session.db"),
		WorkerPoolSize:       getEnvAsInt("WORKER_POOL_SIZE", internal.DefaultWorkerPoolSize),
		WorkerQueueSize:      getEnvAsInt("WORKER_QUEUE_SIZE", internal.DefaultWorkerQueueSize),
	}

	return cfg, cfg.Validate()
}

// PostgresDSN returns standard PostgreSQL connection string URL.
func (c *Config) PostgresDSN() string {
	encodedPassword := url.QueryEscape(c.DBPassword)
	return fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=%s",
		c.DBUser,
		encodedPassword,
		c.DBHost,
		c.DBPort,
		c.DBName,
		c.DBSSLMode,
	)
}

// Validate checks essential configurations and throws clear error messages for missing env values.
func (c *Config) Validate() error {
	var missingVars []string

	if c.DBHost == "" {
		missingVars = append(missingVars, "DB_HOST")
	}
	if c.DBPort == "" {
		missingVars = append(missingVars, "DB_PORT")
	}
	if c.DBUser == "" {
		missingVars = append(missingVars, "DB_USER")
	}
	if c.DBPassword == "" {
		missingVars = append(missingVars, "DB_PASSWORD")
	}
	if c.DBName == "" {
		missingVars = append(missingVars, "DB_NAME")
	}
	if c.OmnirouteAPIBaseURL == "" {
		missingVars = append(missingVars, "OMNIROUTE_API_BASE_URL")
	}

	if len(missingVars) > 0 {
		return fmt.Errorf("variabel lingkungan wajib berikut belum diisi di file .env: %s", strings.Join(missingVars, ", "))
	}

	if c.MaxContextMessages <= 0 {
		c.MaxContextMessages = internal.DefaultMaxContextMessages
	}
	if c.WorkerPoolSize <= 0 {
		c.WorkerPoolSize = internal.DefaultWorkerPoolSize
	}
	if c.WorkerQueueSize <= 0 {
		c.WorkerQueueSize = internal.DefaultWorkerQueueSize
	}

	return nil
}

func getEnv(key, defaultVal string) string {
	if val, exists := os.LookupEnv(key); exists && strings.TrimSpace(val) != "" {
		return strings.TrimSpace(val)
	}
	return defaultVal
}

func getEnvAsInt(key string, defaultVal int) int {
	valStr := getEnv(key, "")
	if valStr == "" {
		return defaultVal
	}
	val, err := strconv.Atoi(valStr)
	if err != nil {
		// Warn instead of silently swallowing the parse error
		fmt.Fprintf(os.Stderr, "WARN: env var %s=%q is not a valid integer, using default %d\n", key, valStr, defaultVal)
		return defaultVal
	}
	return val
}
