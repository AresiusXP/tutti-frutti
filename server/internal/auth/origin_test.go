package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNormalizeOrigin(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"https://tuttifrutti.aresius.xyz", "https://tuttifrutti.aresius.xyz"},
		{"HTTPS://TuttiFrutti.Aresius.XYZ", "https://tuttifrutti.aresius.xyz"},
		{"https://example.com:443", "https://example.com"},
		{"http://example.com:80", "http://example.com"},
		{"http://example.com:443", "http://example.com:443"},
		{"https://example.com:80", "https://example.com:80"},
		{"http://localhost:5173", "http://localhost:5173"},
		{"http://localhost:5173/", "http://localhost:5173"},
		{"http://[::1]:5173", "http://[::1]:5173"},
		{"http://[::1]", "http://[::1]"},
		{"null", ""},
		{"", ""},
		{"ftp://example.com", ""},
		{"example.com", ""},
		{"https://", ""},
		{"https://example.com/path", ""},
		{"https://example.com?x=1", ""},
		{"https://example.com#frag", ""},
		{"https://user@example.com", ""},
		{"https://example.com:99999999999999999999", "https://example.com:99999999999999999999"},
		{"://bad", ""},
		{"mailto:someone@example.com", ""},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := NormalizeOrigin(tt.in); got != tt.want {
				t.Errorf("NormalizeOrigin(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestCheckOrigin(t *testing.T) {
	prod := []string{"https://tuttifrutti.aresius.xyz", "http://localhost:5173"}
	tests := []struct {
		name         string
		origin       string // "-" means header absent
		host         string
		allowed      []string
		allowMissing bool
		want         bool
	}{
		{name: "exact match", origin: "https://tuttifrutti.aresius.xyz", allowed: prod, want: true},
		{name: "host case insensitive", origin: "https://TUTTIFRUTTI.aresius.xyz", allowed: prod, want: true},
		{name: "allowed list case insensitive", origin: "https://tuttifrutti.aresius.xyz", allowed: []string{"HTTPS://TuttiFrutti.Aresius.xyz"}, want: true},
		{name: "default port is the same origin", origin: "https://tuttifrutti.aresius.xyz:443", allowed: prod, want: true},
		{name: "second entry", origin: "http://localhost:5173", allowed: prod, want: true},
		{name: "wrong scheme", origin: "http://tuttifrutti.aresius.xyz", allowed: prod, want: false},
		{name: "wrong port", origin: "http://localhost:5174", allowed: prod, want: false},
		{name: "implicit vs explicit other port", origin: "http://localhost", allowed: prod, want: false},
		{name: "subdomain", origin: "https://evil.tuttifrutti.aresius.xyz", allowed: prod, want: false},
		{name: "suffix attack", origin: "https://tuttifrutti.aresius.xyz.evil.com", allowed: prod, want: false},
		{name: "prefix attack", origin: "https://eviltuttifrutti.aresius.xyz", allowed: prod, want: false},
		{name: "other site", origin: "https://evil.example", allowed: prod, want: false},
		{name: "null origin", origin: "null", allowed: prod, want: false},
		{name: "null origin in dev", origin: "null", allowed: nil, allowMissing: true, want: false},
		{name: "garbage", origin: "%%%", allowed: prod, want: false},
		{name: "origin with path", origin: "https://tuttifrutti.aresius.xyz/x", allowed: prod, want: false},
		{name: "missing, not allowed", origin: "-", allowed: prod, want: false},
		{name: "missing, allowed", origin: "-", allowed: nil, allowMissing: true, want: true},
		{name: "empty list, same host", origin: "http://example.com:8080", host: "example.com:8080", allowed: nil, want: true},
		{name: "empty list, same host other case", origin: "http://EXAMPLE.com:8080", host: "example.com:8080", allowed: nil, want: true},
		{name: "empty list, other host", origin: "http://evil.example", host: "example.com:8080", allowed: nil, want: false},
		{name: "empty list, other port", origin: "http://example.com:9", host: "example.com:8080", allowed: nil, want: false},
		{name: "invalid allowed entry never matches", origin: "https://tuttifrutti.aresius.xyz", allowed: []string{"*", "tuttifrutti.aresius.xyz"}, want: false},
		{name: "list set so same host does not apply", origin: "http://example.com:8080", host: "example.com:8080", allowed: prod, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/rooms", nil)
			if tt.host != "" {
				r.Host = tt.host
			}
			if tt.origin != "-" {
				r.Header.Set("Origin", tt.origin)
			}
			if got := CheckOrigin(r, tt.allowed, tt.allowMissing); got != tt.want {
				t.Errorf("CheckOrigin(origin=%q) = %v, want %v", tt.origin, got, tt.want)
			}
		})
	}
}
