// Package config loads the server configuration from environment variables.
package config

import (
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
)

// Config is the runtime configuration of the server.
type Config struct {
	// Port is the TCP port the HTTP server listens on (PORT, default 8080).
	Port string
	// LogLevel is the minimum slog level (LOG_LEVEL, default info).
	LogLevel slog.Level
	// PublicURL is the externally visible base URL (PUBLIC_URL), e.g.
	// https://tuttifrutti.aresius.xyz. It is used for the WebSocket Origin
	// check and OAuth redirects. Empty means "not configured" (dev mode).
	PublicURL string
	// DatabaseURL is the Postgres connection string (DATABASE_URL). Optional
	// until persistence lands; the server runs without it.
	DatabaseURL string
	// SessionSecret signs guest/session cookies (SESSION_SECRET).
	SessionSecret string
	// GoogleClientID and GoogleClientSecret enable Google login when both set.
	GoogleClientID     string
	GoogleClientSecret string
}

// Load reads the configuration using getenv (typically os.Getenv).
func Load(getenv func(string) string) (Config, error) {
	cfg := Config{
		Port:               valueOr(getenv("PORT"), "8080"),
		DatabaseURL:        getenv("DATABASE_URL"),
		SessionSecret:      getenv("SESSION_SECRET"),
		GoogleClientID:     getenv("GOOGLE_CLIENT_ID"),
		GoogleClientSecret: getenv("GOOGLE_CLIENT_SECRET"),
	}

	port, err := strconv.Atoi(cfg.Port)
	if err != nil || port < 1 || port > 65535 {
		return Config{}, fmt.Errorf("config: invalid PORT %q", cfg.Port)
	}

	level := valueOr(getenv("LOG_LEVEL"), "info")
	if err := cfg.LogLevel.UnmarshalText([]byte(strings.ToLower(level))); err != nil {
		return Config{}, fmt.Errorf("config: invalid LOG_LEVEL %q: %w", level, err)
	}

	if raw := strings.TrimRight(getenv("PUBLIC_URL"), "/"); raw != "" {
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return Config{}, fmt.Errorf("config: invalid PUBLIC_URL %q", raw)
		}
		cfg.PublicURL = raw
	}

	if (cfg.GoogleClientID == "") != (cfg.GoogleClientSecret == "") {
		return Config{}, fmt.Errorf("config: GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET must be set together")
	}

	return cfg, nil
}

// GoogleEnabled reports whether Google login is configured.
func (c Config) GoogleEnabled() bool {
	return c.GoogleClientID != "" && c.GoogleClientSecret != ""
}

func valueOr(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return strings.TrimSpace(v)
}
