package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadPublicBots(t *testing.T) {
	for _, tt := range []struct {
		name    string
		vars    map[string]string
		enabled bool
		max     int
		delays  [4]time.Duration
	}{
		{"defaults", nil, true, 2, [4]time.Duration{20, 30, 40, 55}},
		{"disabled", map[string]string{"PUBLIC_BOTS_ENABLED": " false "}, false, 2, [4]time.Duration{20, 30, 40, 55}},
		{"one bot", map[string]string{"PUBLIC_BOTS_MAX": " 1 "}, true, 1, [4]time.Duration{20, 30, 40, 55}},
		{"explicit enabled", map[string]string{"PUBLIC_BOTS_ENABLED": "true", "PUBLIC_BOTS_MAX": "2"}, true, 2, [4]time.Duration{20, 30, 40, 55}},
		{"custom ranges", map[string]string{
			"PUBLIC_BOTS_FIRST_MIN_SECONDS": " 1 ", "PUBLIC_BOTS_FIRST_MAX_SECONDS": "2",
			"PUBLIC_BOTS_SECOND_MIN_SECONDS": "3", "PUBLIC_BOTS_SECOND_MAX_SECONDS": "300",
		}, true, 2, [4]time.Duration{1, 2, 3, 300}},
		{"fixed delays", map[string]string{
			"PUBLIC_BOTS_FIRST_MIN_SECONDS": "1", "PUBLIC_BOTS_FIRST_MAX_SECONDS": "1",
			"PUBLIC_BOTS_SECOND_MIN_SECONDS": "300", "PUBLIC_BOTS_SECOND_MAX_SECONDS": "300",
		}, true, 2, [4]time.Duration{1, 1, 300, 300}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Load(env(tt.vars))
			if err != nil {
				t.Fatal(err)
			}
			if cfg.BotsEnabled != tt.enabled || cfg.MaxBots != tt.max {
				t.Errorf("enabled/max = %v/%d, want %v/%d", cfg.BotsEnabled, cfg.MaxBots, tt.enabled, tt.max)
			}
			got := [4]time.Duration{cfg.BotFirstDelayMin, cfg.BotFirstDelayMax, cfg.BotSecondDelayMin, cfg.BotSecondDelayMax}
			for i, seconds := range tt.delays {
				if got[i] != seconds*time.Second {
					t.Errorf("delay[%d] = %v, want %v", i, got[i], seconds*time.Second)
				}
			}
		})
	}
}

func TestLoadPublicRooms(t *testing.T) {
	for _, tt := range []struct {
		value string
		want  bool
	}{{"", true}, {" true ", true}, {"1", true}, {"false", false}, {" 0 ", false}, {"FALSE", false}} {
		cfg, err := Load(env(map[string]string{"PUBLIC_ROOMS_ENABLED": tt.value}))
		if err != nil {
			t.Fatalf("PUBLIC_ROOMS_ENABLED=%q: %v", tt.value, err)
		}
		if cfg.PublicRoomsEnabled != tt.want {
			t.Errorf("PUBLIC_ROOMS_ENABLED=%q: enabled = %v, want %v", tt.value, cfg.PublicRoomsEnabled, tt.want)
		}
		// The bots have their own switch: turning public games off leaves it alone.
		if !cfg.BotsEnabled {
			t.Errorf("PUBLIC_ROOMS_ENABLED=%q changed BotsEnabled", tt.value)
		}
	}
}

func TestLoadPublicBotsInvalid(t *testing.T) {
	for key, values := range map[string][]string{
		"PUBLIC_ROOMS_ENABLED":           {"yes", "off", "maybe"},
		"PUBLIC_BOTS_ENABLED":            {"yes", "maybe"},
		"PUBLIC_BOTS_MAX":                {"0", "-1", "3", "1.5", "lots"},
		"PUBLIC_BOTS_FIRST_MIN_SECONDS":  {"0", "-1", "301", "1.5", "20s", "lots", "999999999999999999999"},
		"PUBLIC_BOTS_FIRST_MAX_SECONDS":  {"0", "-1", "301", "1.5", "30s", "lots", "999999999999999999999"},
		"PUBLIC_BOTS_SECOND_MIN_SECONDS": {"0", "-1", "301", "1.5", "40s", "lots", "999999999999999999999"},
		"PUBLIC_BOTS_SECOND_MAX_SECONDS": {"0", "-1", "301", "1.5", "55s", "lots", "999999999999999999999"},
	} {
		for _, value := range values {
			t.Run(key+"/"+value, func(t *testing.T) {
				_, err := Load(env(map[string]string{key: value}))
				if err == nil || !strings.Contains(err.Error(), key) {
					t.Fatalf("error = %v, want error naming %s", err, key)
				}
			})
		}
	}
	for _, tt := range []struct {
		name string
		vars map[string]string
		key  string
	}{
		{"first range reversed", map[string]string{"PUBLIC_BOTS_FIRST_MIN_SECONDS": "31"}, "PUBLIC_BOTS_FIRST_MIN_SECONDS"},
		{"second range reversed", map[string]string{"PUBLIC_BOTS_SECOND_MAX_SECONDS": "39"}, "PUBLIC_BOTS_SECOND_MIN_SECONDS"},
		{"ranges touch", map[string]string{"PUBLIC_BOTS_SECOND_MIN_SECONDS": "30"}, "PUBLIC_BOTS_SECOND_MIN_SECONDS"},
		{"ranges overlap", map[string]string{"PUBLIC_BOTS_FIRST_MAX_SECONDS": "41"}, "PUBLIC_BOTS_SECOND_MIN_SECONDS"},
		{"disabled still validates", map[string]string{"PUBLIC_BOTS_ENABLED": "false", "PUBLIC_BOTS_MAX": "0"}, "PUBLIC_BOTS_MAX"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(env(tt.vars))
			if err == nil || !strings.Contains(err.Error(), tt.key) {
				t.Fatalf("error = %v, want error naming %s", err, tt.key)
			}
		})
	}
}
