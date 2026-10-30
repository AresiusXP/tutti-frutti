package config

import (
	"log/slog"
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(env(nil))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Port != "8080" {
		t.Errorf("Port = %q, want 8080", cfg.Port)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, want info", cfg.LogLevel)
	}
	if cfg.PublicURL != "" || cfg.GoogleEnabled() {
		t.Errorf("unexpected optional config: %+v", cfg)
	}
}

func TestLoad(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr bool
		check   func(t *testing.T, c Config)
	}{
		{
			name: "all values",
			env: map[string]string{
				"PORT": "9000", "LOG_LEVEL": "DEBUG", "PUBLIC_URL": "https://tuttifrutti.aresius.xyz/",
				"DATABASE_URL": "postgres://x", "SESSION_SECRET": strings.Repeat("s", 32),
				"GOOGLE_CLIENT_ID": "id", "GOOGLE_CLIENT_SECRET": "secret",
			},
			check: func(t *testing.T, c Config) {
				if c.Port != "9000" || c.LogLevel != slog.LevelDebug {
					t.Errorf("port/level = %s/%v", c.Port, c.LogLevel)
				}
				if c.PublicURL != "https://tuttifrutti.aresius.xyz" {
					t.Errorf("PublicURL = %q (trailing slash must be trimmed)", c.PublicURL)
				}
				if !c.GoogleEnabled() {
					t.Error("GoogleEnabled() = false")
				}
			},
		},
		{name: "bad port", env: map[string]string{"PORT": "abc"}, wantErr: true},
		{name: "port out of range", env: map[string]string{"PORT": "70000"}, wantErr: true},
		{name: "bad level", env: map[string]string{"LOG_LEVEL": "loud"}, wantErr: true},
		{name: "bad public url", env: map[string]string{"PUBLIC_URL": "ftp://x"}, wantErr: true},
		{name: "public url without host", env: map[string]string{"PUBLIC_URL": "https://"}, wantErr: true},
		{name: "google id without secret", env: map[string]string{"GOOGLE_CLIENT_ID": "id"}, wantErr: true},
		{name: "google secret without id", env: map[string]string{"GOOGLE_CLIENT_SECRET": "secret", "PUBLIC_URL": "https://x.example"}, wantErr: true},
		{name: "google without public url", env: map[string]string{"GOOGLE_CLIENT_ID": "id", "GOOGLE_CLIENT_SECRET": "secret"}, wantErr: true},
		{name: "google with a public url made of slashes", env: map[string]string{"GOOGLE_CLIENT_ID": "id", "GOOGLE_CLIENT_SECRET": "secret", "PUBLIC_URL": "///"}, wantErr: true},
		{
			name: "google with a public url",
			env:  map[string]string{"GOOGLE_CLIENT_ID": "id", "GOOGLE_CLIENT_SECRET": "secret", "PUBLIC_URL": "http://localhost:8000/"},
			check: func(t *testing.T, c Config) {
				if !c.GoogleEnabled() || c.GoogleRedirectURL() != "http://localhost:8000/api/auth/google/callback" {
					t.Errorf("GoogleEnabled=%v redirect=%q", c.GoogleEnabled(), c.GoogleRedirectURL())
				}
			},
		},
		{
			name: "no google needs no public url",
			env:  map[string]string{},
			check: func(t *testing.T, c Config) {
				if c.GoogleEnabled() || c.GoogleRedirectURL() != "" {
					t.Errorf("GoogleEnabled=%v redirect=%q", c.GoogleEnabled(), c.GoogleRedirectURL())
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Load(env(tt.env))
			if (err != nil) != tt.wantErr {
				t.Fatalf("Load() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && tt.check != nil {
				tt.check(t, cfg)
			}
		})
	}
}

func TestLoadTransportDefaults(t *testing.T) {
	cfg, err := Load(env(nil))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.AllowedOrigins) != 0 {
		t.Errorf("AllowedOrigins = %v, want empty", cfg.AllowedOrigins)
	}
	if cfg.TrustForwardedFor {
		t.Error("TrustForwardedFor must default to false")
	}
	if cfg.MaxRooms != 1000 {
		t.Errorf("MaxRooms = %d, want 1000", cfg.MaxRooms)
	}
}

func TestLoadAllowedOrigins(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    []string
		wantErr bool
	}{
		{name: "unset", raw: "", want: nil},
		{name: "blank", raw: "  ,, ", want: nil},
		{name: "single", raw: "http://localhost:5173", want: []string{"http://localhost:5173"}},
		{name: "several with spaces", raw: " http://localhost:5173 , https://tuttifrutti.aresius.xyz ", want: []string{"http://localhost:5173", "https://tuttifrutti.aresius.xyz"}},
		{name: "trailing slash trimmed", raw: "http://localhost:8000/,https://a.example/", want: []string{"http://localhost:8000", "https://a.example"}},
		{name: "duplicates dropped", raw: "http://a.example,http://a.example/", want: []string{"http://a.example"}},
		{name: "trailing comma", raw: "http://a.example,", want: []string{"http://a.example"}},
		{name: "ipv6 host", raw: "http://[::1]:5173", want: []string{"http://[::1]:5173"}},
		{name: "no scheme", raw: "localhost:5173", wantErr: true},
		{name: "ftp scheme", raw: "ftp://a.example", wantErr: true},
		{name: "wildcard", raw: "*", wantErr: true},
		{name: "no host", raw: "https://", wantErr: true},
		{name: "path", raw: "http://a.example/app", wantErr: true},
		{name: "query", raw: "http://a.example?x=1", wantErr: true},
		{name: "userinfo", raw: "http://user@a.example", wantErr: true},
		{name: "one bad entry poisons the list", raw: "http://a.example,nope", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Load(env(map[string]string{"ALLOWED_ORIGINS": tt.raw}))
			if (err != nil) != tt.wantErr {
				t.Fatalf("Load() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if len(cfg.AllowedOrigins) != len(tt.want) {
				t.Fatalf("AllowedOrigins = %v, want %v", cfg.AllowedOrigins, tt.want)
			}
			for i := range tt.want {
				if cfg.AllowedOrigins[i] != tt.want[i] {
					t.Errorf("AllowedOrigins[%d] = %q, want %q", i, cfg.AllowedOrigins[i], tt.want[i])
				}
			}
		})
	}
}

func TestLoadTrustForwardedFor(t *testing.T) {
	tests := []struct {
		raw     string
		want    bool
		wantErr bool
	}{
		{raw: "", want: false},
		{raw: "true", want: true},
		{raw: "TRUE", want: true},
		{raw: "1", want: true},
		{raw: " true ", want: true},
		{raw: "false", want: false},
		{raw: "0", want: false},
		{raw: "yes", wantErr: true},
		{raw: "maybe", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			cfg, err := Load(env(map[string]string{"TRUST_FORWARDED_FOR": tt.raw}))
			if (err != nil) != tt.wantErr {
				t.Fatalf("Load() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && cfg.TrustForwardedFor != tt.want {
				t.Errorf("TrustForwardedFor = %v, want %v", cfg.TrustForwardedFor, tt.want)
			}
		})
	}
}

func TestLoadMaxRooms(t *testing.T) {
	tests := []struct {
		raw     string
		want    int
		wantErr bool
	}{
		{raw: "", want: 1000},
		{raw: "1", want: 1},
		{raw: "250", want: 250},
		{raw: " 42 ", want: 42},
		{raw: "100000", want: 100000},
		{raw: "0", wantErr: true},
		{raw: "-5", wantErr: true},
		{raw: "100001", wantErr: true},
		{raw: "lots", wantErr: true},
		{raw: "1.5", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			cfg, err := Load(env(map[string]string{"MAX_ROOMS": tt.raw}))
			if (err != nil) != tt.wantErr {
				t.Fatalf("Load() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && cfg.MaxRooms != tt.want {
				t.Errorf("MaxRooms = %d, want %d", cfg.MaxRooms, tt.want)
			}
		})
	}
}

func TestOriginsAndSecureCookies(t *testing.T) {
	tests := []struct {
		name       string
		cfg        Config
		wantOrigin []string
		wantSecure bool
	}{
		{name: "empty", cfg: Config{}, wantOrigin: nil, wantSecure: false},
		{
			name:       "public url only",
			cfg:        Config{PublicURL: "https://tuttifrutti.aresius.xyz"},
			wantOrigin: []string{"https://tuttifrutti.aresius.xyz"},
			wantSecure: true,
		},
		{
			name:       "public url with path is reduced to its origin",
			cfg:        Config{PublicURL: "http://localhost:8080/app"},
			wantOrigin: []string{"http://localhost:8080"},
			wantSecure: false,
		},
		{
			name:       "merged without duplicates",
			cfg:        Config{PublicURL: "http://localhost:5173", AllowedOrigins: []string{"http://localhost:5173", "http://localhost:8000"}},
			wantOrigin: []string{"http://localhost:5173", "http://localhost:8000"},
			wantSecure: false,
		},
		{
			name:       "allowed origins only",
			cfg:        Config{AllowedOrigins: []string{"http://localhost:8000"}},
			wantOrigin: []string{"http://localhost:8000"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.cfg.Origins()
			if len(got) != len(tt.wantOrigin) {
				t.Fatalf("Origins() = %v, want %v", got, tt.wantOrigin)
			}
			for i := range got {
				if got[i] != tt.wantOrigin[i] {
					t.Errorf("Origins()[%d] = %q, want %q", i, got[i], tt.wantOrigin[i])
				}
			}
			if tt.cfg.SecureCookies() != tt.wantSecure {
				t.Errorf("SecureCookies() = %v, want %v", tt.cfg.SecureCookies(), tt.wantSecure)
			}
		})
	}
}

func TestLoadSessionSecret(t *testing.T) {
	tests := []struct {
		name    string
		secret  string
		wantErr bool
	}{
		{name: "unset keeps the ephemeral-secret behaviour", secret: ""},
		{name: "exactly 32 bytes", secret: strings.Repeat("k", 32)},
		{name: "longer", secret: strings.Repeat("k", 100)},
		{name: "openssl rand -base64 32", secret: "q9v0Yc3m2x1lq0b8mWmE0m5s1y2n8Z4a0m7H6pQ3rXk="},
		{name: "31 bytes", secret: strings.Repeat("k", 31), wantErr: true},
		{name: "one byte", secret: "~", wantErr: true},
		{name: "whitespace only is not empty", secret: "   ", wantErr: true},
		{name: "dev placeholder of docker-compose", secret: "dev-only-session-secret-do-not-use-in-production"},
		{name: "32 bytes counted as bytes, not runes", secret: strings.Repeat("é", 16)},
		{name: "16 runes of 1 byte", secret: strings.Repeat("e", 16), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Load(env(map[string]string{"SESSION_SECRET": tt.secret}))
			if (err != nil) != tt.wantErr {
				t.Fatalf("Load() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				if !strings.Contains(err.Error(), "SESSION_SECRET must be at least 32 bytes (e.g. openssl rand -base64 32)") {
					t.Errorf("error %q lacks the guidance", err)
				}
				return
			}
			if cfg.SessionSecret != tt.secret {
				t.Errorf("SessionSecret = %q, want %q", cfg.SessionSecret, tt.secret)
			}
		})
	}
}

func TestLoadSessionSecretErrorNeverEchoesTheValue(t *testing.T) {
	_, err := Load(env(map[string]string{"SESSION_SECRET": "hunter2-weak"}))
	if err == nil {
		t.Fatal("a 12 byte secret was accepted")
	}
	if strings.Contains(err.Error(), "hunter2") {
		t.Errorf("error %q echoes the secret", err)
	}
}

func TestLoadJudge(t *testing.T) {
	cfg, err := Load(env(nil))
	if err != nil || cfg.JudgeEnabled() || cfg.JudgeDailyNeurons != 9000 {
		t.Fatalf("defaults: %+v, %v", cfg, err)
	}
	cfg, err = Load(env(map[string]string{"CLOUDFLARE_ACCOUNT_ID": " acc ", "CLOUDFLARE_API_TOKEN": "tok\n", "JUDGE_DAILY_NEURONS": "5000"}))
	if err != nil || !cfg.JudgeEnabled() || cfg.CloudflareAccountID != "acc" || cfg.CloudflareAPIToken != "tok" || cfg.JudgeDailyNeurons != 5000 {
		t.Fatalf("configured: %+v, %v", cfg, err)
	}

	for name, vars := range map[string]map[string]string{
		"only the account":   {"CLOUDFLARE_ACCOUNT_ID": "acc"},
		"only the token":     {"CLOUDFLARE_API_TOKEN": "tok-secret"},
		"zero budget":        {"JUDGE_DAILY_NEURONS": "0"},
		"over the allowance": {"JUDGE_DAILY_NEURONS": "10001"},
		"not a number":       {"JUDGE_DAILY_NEURONS": "lots"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Load(env(vars))
			if err == nil {
				t.Fatal("Load must fail")
			}
			if strings.Contains(err.Error(), "tok-secret") {
				t.Errorf("the error echoes the token: %v", err)
			}
		})
	}
}

func TestLoadModerationSocket(t *testing.T) {
	cfg, err := Load(env(nil))
	if err != nil || cfg.ModerationSocket != "" {
		t.Fatalf("default = %q, %v: the tool is off unless it is configured", cfg.ModerationSocket, err)
	}
	cfg, err = Load(env(map[string]string{"MODERATION_SOCKET": " /run/tutti/moderation.sock\n"}))
	if err != nil || cfg.ModerationSocket != "/run/tutti/moderation.sock" {
		t.Errorf("socket = %q, %v", cfg.ModerationSocket, err)
	}
	if _, err := Load(env(map[string]string{"MODERATION_SOCKET": "moderation.sock"})); err == nil {
		t.Error("a relative socket path was accepted")
	}
}
