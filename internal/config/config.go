package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HTTPAddr        string
	DatabaseURL     string
	JWTSecret       string
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration
	AllowedOrigins  []string
	ShutdownTimeout time.Duration
	MaxRequestBytes int64
	SyncBatchSize   int
	Environment     string
}

func Load() (Config, error) {
	accessTTL, err := durationEnv("ACCESS_TOKEN_TTL", 15*time.Minute)
	if err != nil {
		return Config{}, err
	}
	refreshTTL, err := durationEnv("REFRESH_TOKEN_TTL", 30*24*time.Hour)
	if err != nil {
		return Config{}, err
	}
	shutdownTimeout, err := durationEnv("SHUTDOWN_TIMEOUT", 10*time.Second)
	if err != nil {
		return Config{}, err
	}
	maxRequestBytes, err := int64Env("MAX_REQUEST_BYTES", 2<<20)
	if err != nil {
		return Config{}, err
	}
	syncBatchSize, err := intEnv("SYNC_BATCH_SIZE", 200)
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		HTTPAddr:        env("HTTP_ADDR", ":8080"),
		DatabaseURL:     strings.TrimSpace(os.Getenv("DATABASE_URL")),
		JWTSecret:       os.Getenv("JWT_SECRET"),
		AccessTokenTTL:  accessTTL,
		RefreshTokenTTL: refreshTTL,
		AllowedOrigins:  csvEnv("CORS_ALLOWED_ORIGINS"),
		ShutdownTimeout: shutdownTimeout,
		MaxRequestBytes: maxRequestBytes,
		SyncBatchSize:   syncBatchSize,
		Environment:     env("APP_ENV", "development"),
	}

	var problems []error
	if cfg.DatabaseURL == "" {
		problems = append(problems, errors.New("DATABASE_URL is required"))
	}
	if len(cfg.JWTSecret) < 32 {
		problems = append(problems, errors.New("JWT_SECRET must contain at least 32 characters"))
	}
	if cfg.SyncBatchSize < 1 || cfg.SyncBatchSize > 1000 {
		problems = append(problems, errors.New("SYNC_BATCH_SIZE must be between 1 and 1000"))
	}
	if cfg.MaxRequestBytes < 1024 {
		problems = append(problems, errors.New("MAX_REQUEST_BYTES must be at least 1024"))
	}
	return cfg, errors.Join(problems...)
}

func env(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func csvEnv(name string) []string {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func durationEnv(name string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", name)
	}
	return value, nil
}

func intEnv(name string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", name)
	}
	return value, nil
}

func int64Env(name string, fallback int64) (int64, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", name)
	}
	return value, nil
}
