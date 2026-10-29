// Package config loads the server configuration from environment variables.
package config

import (
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
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
	// SessionSecret signs guest/session cookies (SESSION_SECRET). Empty means
	// "generate an ephemeral one at startup"; otherwise it must be at least
	// MinSessionSecretBytes long, so that a guessable value cannot be
	// brute-forced offline from a single signed cookie.
	SessionSecret string
	// GoogleClientID and GoogleClientSecret enable Google login when both set;
	// it then requires PUBLIC_URL, because the redirect URI derives from it.
	GoogleClientID     string
	GoogleClientSecret string
	// AllowedOrigins are extra origins (scheme://host[:port], no path) that
	// may call unsafe REST methods and open the WebSocket, in addition to
	// PUBLIC_URL's origin (ALLOWED_ORIGINS, comma-separated; for dev, e.g.
	// http://localhost:5173,http://localhost:8000). Trailing slashes are trimmed.
	AllowedOrigins []string
	// TrustForwardedFor makes the rate limiters take the client IP from the
	// rightmost X-Forwarded-For entry (the one our reverse proxy appended)
	// instead of the TCP peer (TRUST_FORWARDED_FOR, default false). Only
	// enable it when every request comes through a proxy that sets the header.
	TrustForwardedFor bool
	// MaxRooms bounds the number of live rooms (MAX_ROOMS, default 1000,
	// 1..100000).
	MaxRooms int
	// BotsEnabled permits new automatic bot additions to public rooms
	// (PUBLIC_BOTS_ENABLED, default true). Existing bots keep playing when off.
	BotsEnabled bool
	// MaxBots caps automated seats per public room (PUBLIC_BOTS_MAX, default 2,
	// 1..2); disable additions with BotsEnabled rather than a zero cap.
	MaxBots int
	// Bot delays are measured since public waiting started, in whole seconds
	// (PUBLIC_BOTS_FIRST_MIN_SECONDS/MAX_SECONDS and SECOND_MIN_SECONDS/MAX_SECONDS).
	// Each bound is 1..300, min <= max, and the second min exceeds the first max.
	BotFirstDelayMin  time.Duration
	BotFirstDelayMax  time.Duration
	BotSecondDelayMin time.Duration
	BotSecondDelayMax time.Duration
	// CloudflareAccountID and CloudflareAPIToken enable the AI judge of the
	// review when both are set (CLOUDFLARE_ACCOUNT_ID, CLOUDFLARE_API_TOKEN):
	// a Workers AI token on a Cloudflare account on the Free plan.
	CloudflareAccountID string
	CloudflareAPIToken  string
	// JudgeDailyNeurons is the Workers AI budget per UTC day the judge keeps to
	// (JUDGE_DAILY_NEURONS, default 9000, 1..10000: the Free plan allows 10000).
	JudgeDailyNeurons int
}

// googleCallbackPath is the path of the OAuth redirect URI under PUBLIC_URL
// (see auth.GoogleCallbackPath).
const googleCallbackPath = "/api/auth/google/callback"

const (
	defaultJudgeDailyNeurons = 9000
	maxJudgeDailyNeurons     = 10000 // the Workers AI Free plan's daily allowance

	defaultMaxRooms = 1000
	maxMaxRooms     = 100000

	// MinSessionSecretBytes is the shortest accepted non-empty SESSION_SECRET
	// (e.g. the 44 characters of `openssl rand -base64 32`). auth.NewGuests
	// warns below the same length.
	MinSessionSecretBytes = 32
)

// Load reads the configuration using getenv (typically os.Getenv).
func Load(getenv func(string) string) (Config, error) {
	cfg := Config{
		Port:               valueOr(getenv("PORT"), "8080"),
		DatabaseURL:        getenv("DATABASE_URL"),
		SessionSecret:      getenv("SESSION_SECRET"),
		GoogleClientID:     getenv("GOOGLE_CLIENT_ID"),
		GoogleClientSecret: getenv("GOOGLE_CLIENT_SECRET"),

		CloudflareAccountID: strings.TrimSpace(getenv("CLOUDFLARE_ACCOUNT_ID")),
		CloudflareAPIToken:  strings.TrimSpace(getenv("CLOUDFLARE_API_TOKEN")),

		BotsEnabled:       true,
		MaxBots:           2,
		BotFirstDelayMin:  20 * time.Second,
		BotFirstDelayMax:  30 * time.Second,
		BotSecondDelayMin: 40 * time.Second,
		BotSecondDelayMax: 55 * time.Second,
	}

	port, err := strconv.Atoi(cfg.Port)
	if err != nil || port < 1 || port > 65535 {
		return Config{}, fmt.Errorf("config: invalid PORT %q", cfg.Port)
	}

	if n := len(cfg.SessionSecret); n > 0 && n < MinSessionSecretBytes {
		return Config{}, fmt.Errorf("config: SESSION_SECRET must be at least %d bytes (e.g. openssl rand -base64 32)", MinSessionSecretBytes)
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
	if cfg.GoogleEnabled() && cfg.PublicURL == "" {
		return Config{}, fmt.Errorf("config: Google login needs PUBLIC_URL (the OAuth redirect URI is PUBLIC_URL%s)", googleCallbackPath)
	}

	if (cfg.CloudflareAccountID == "") != (cfg.CloudflareAPIToken == "") {
		return Config{}, fmt.Errorf("config: CLOUDFLARE_ACCOUNT_ID and CLOUDFLARE_API_TOKEN must be set together")
	}
	cfg.JudgeDailyNeurons = defaultJudgeDailyNeurons
	if raw := strings.TrimSpace(getenv("JUDGE_DAILY_NEURONS")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxJudgeDailyNeurons {
			return Config{}, fmt.Errorf("config: invalid JUDGE_DAILY_NEURONS %q (want 1..%d)", raw, maxJudgeDailyNeurons)
		}
		cfg.JudgeDailyNeurons = n
	}

	origins, err := parseOrigins(getenv("ALLOWED_ORIGINS"))
	if err != nil {
		return Config{}, err
	}
	cfg.AllowedOrigins = origins

	if raw := strings.TrimSpace(getenv("TRUST_FORWARDED_FOR")); raw != "" {
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return Config{}, fmt.Errorf("config: invalid TRUST_FORWARDED_FOR %q (want true or false)", raw)
		}
		cfg.TrustForwardedFor = b
	}

	cfg.MaxRooms = defaultMaxRooms
	if raw := strings.TrimSpace(getenv("MAX_ROOMS")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxMaxRooms {
			return Config{}, fmt.Errorf("config: invalid MAX_ROOMS %q (want 1..%d)", raw, maxMaxRooms)
		}
		cfg.MaxRooms = n
	}

	if raw := strings.TrimSpace(getenv("PUBLIC_BOTS_ENABLED")); raw != "" {
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return Config{}, fmt.Errorf("config: invalid PUBLIC_BOTS_ENABLED %q (want true or false)", raw)
		}
		cfg.BotsEnabled = b
	}
	if raw := strings.TrimSpace(getenv("PUBLIC_BOTS_MAX")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 2 {
			return Config{}, fmt.Errorf("config: invalid PUBLIC_BOTS_MAX %q (want 1..2; use PUBLIC_BOTS_ENABLED=false to disable additions)", raw)
		}
		cfg.MaxBots = n
	}
	for _, delay := range []struct {
		key string
		dst *time.Duration
	}{
		{"PUBLIC_BOTS_FIRST_MIN_SECONDS", &cfg.BotFirstDelayMin},
		{"PUBLIC_BOTS_FIRST_MAX_SECONDS", &cfg.BotFirstDelayMax},
		{"PUBLIC_BOTS_SECOND_MIN_SECONDS", &cfg.BotSecondDelayMin},
		{"PUBLIC_BOTS_SECOND_MAX_SECONDS", &cfg.BotSecondDelayMax},
	} {
		if raw := strings.TrimSpace(getenv(delay.key)); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 || n > 300 {
				return Config{}, fmt.Errorf("config: invalid %s %q (want 1..300 seconds)", delay.key, raw)
			}
			*delay.dst = time.Duration(n) * time.Second
		}
	}
	if cfg.BotFirstDelayMin > cfg.BotFirstDelayMax {
		return Config{}, fmt.Errorf("config: PUBLIC_BOTS_FIRST_MIN_SECONDS must be <= PUBLIC_BOTS_FIRST_MAX_SECONDS")
	}
	if cfg.BotSecondDelayMin > cfg.BotSecondDelayMax {
		return Config{}, fmt.Errorf("config: PUBLIC_BOTS_SECOND_MIN_SECONDS must be <= PUBLIC_BOTS_SECOND_MAX_SECONDS")
	}
	if cfg.BotSecondDelayMin <= cfg.BotFirstDelayMax {
		return Config{}, fmt.Errorf("config: PUBLIC_BOTS_SECOND_MIN_SECONDS must be > PUBLIC_BOTS_FIRST_MAX_SECONDS")
	}

	return cfg, nil
}

// parseOrigins parses a comma-separated list of http(s) origins. Empty
// entries are ignored, trailing slashes are trimmed and duplicates dropped.
func parseOrigins(raw string) ([]string, error) {
	var out []string
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimRight(strings.TrimSpace(entry), "/")
		if entry == "" {
			continue
		}
		u, err := url.Parse(entry)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
			u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil || u.Opaque != "" {
			return nil, fmt.Errorf("config: invalid ALLOWED_ORIGINS entry %q (want scheme://host[:port])", entry)
		}
		if !slices.Contains(out, entry) {
			out = append(out, entry)
		}
	}
	return out, nil
}

// Origins returns every origin allowed to call unsafe REST methods and to open
// the WebSocket: PUBLIC_URL's origin (if set) followed by ALLOWED_ORIGINS,
// without duplicates.
func (c Config) Origins() []string {
	var out []string
	if c.PublicURL != "" {
		if u, err := url.Parse(c.PublicURL); err == nil && u.Host != "" {
			out = append(out, u.Scheme+"://"+u.Host)
		}
	}
	for _, o := range c.AllowedOrigins {
		if !slices.Contains(out, o) {
			out = append(out, o)
		}
	}
	return out
}

// SecureCookies reports whether cookies must carry the Secure attribute, that
// is whether PUBLIC_URL is an https URL.
func (c Config) SecureCookies() bool {
	return strings.HasPrefix(c.PublicURL, "https://")
}

// GoogleEnabled reports whether Google login is configured.
func (c Config) GoogleEnabled() bool {
	return c.GoogleClientID != "" && c.GoogleClientSecret != ""
}

// JudgeEnabled reports whether the AI judge is configured.
func (c Config) JudgeEnabled() bool {
	return c.CloudflareAccountID != "" && c.CloudflareAPIToken != ""
}

// GoogleRedirectURL is the OAuth redirect URI registered with Google:
// PUBLIC_URL + /api/auth/google/callback ("" without PUBLIC_URL).
func (c Config) GoogleRedirectURL() string {
	if c.PublicURL == "" {
		return ""
	}
	return c.PublicURL + googleCallbackPath
}

func valueOr(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return strings.TrimSpace(v)
}
