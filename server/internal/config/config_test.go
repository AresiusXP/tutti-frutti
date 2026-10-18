package config

import (
	"log/slog"
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
				"DATABASE_URL": "postgres://x", "SESSION_SECRET": "s",
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
