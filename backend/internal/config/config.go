package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Environment      string
	HTTPAddr         string
	DatabaseURL      string
	RedisAddr        string
	TokenPepper      string
	SessionTTL       time.Duration
	FrontendOrigin   string
	AnalyticsEnabled bool
	KAnonymity       int
	AutoCloseDays    int
}

func Load() (Config, error) {
	ttl, err := time.ParseDuration(value("SESSION_TTL", "24h"))
	if err != nil {
		return Config{}, fmt.Errorf("SESSION_TTL: %w", err)
	}
	k, err := strconv.Atoi(value("ANALYTICS_K_ANONYMITY", "5"))
	if err != nil || k < 1 {
		return Config{}, fmt.Errorf("ANALYTICS_K_ANONYMITY must be a positive integer")
	}
	autoCloseDays, err := strconv.Atoi(value("AUTO_CLOSE_DAYS", "14"))
	if err != nil || autoCloseDays < 1 {
		return Config{}, fmt.Errorf("AUTO_CLOSE_DAYS must be a positive integer")
	}
	cfg := Config{
		Environment:      value("APP_ENV", "development"),
		HTTPAddr:         value("HTTP_ADDR", ":8080"),
		DatabaseURL:      value("DATABASE_URL", "postgres://otklik:otklik@localhost:5432/otklik?sslmode=disable"),
		RedisAddr:        value("REDIS_ADDR", "localhost:6379"),
		TokenPepper:      os.Getenv("TOKEN_PEPPER"),
		SessionTTL:       ttl,
		FrontendOrigin:   value("FRONTEND_ORIGIN", "http://localhost:5173"),
		AnalyticsEnabled: boolValue("ANALYTICS_ENABLED", true),
		KAnonymity:       k,
		AutoCloseDays:    autoCloseDays,
	}
	if len(cfg.TokenPepper) < 32 {
		return Config{}, fmt.Errorf("TOKEN_PEPPER must contain at least 32 characters")
	}
	return cfg, nil
}

func value(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func boolValue(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return parsed
}
