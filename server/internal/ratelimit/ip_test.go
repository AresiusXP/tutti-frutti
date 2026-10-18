package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientIP(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		xff        []string
		trust      bool
		want       string
	}{
		{name: "untrusted ignores XFF", remoteAddr: "203.0.113.9:4444", xff: []string{"1.2.3.4"}, trust: false, want: "203.0.113.9"},
		{name: "untrusted no header", remoteAddr: "203.0.113.9:4444", trust: false, want: "203.0.113.9"},
		{name: "trusted but absent", remoteAddr: "10.0.0.1:1234", trust: true, want: "10.0.0.1"},
		{name: "trusted single entry", remoteAddr: "10.0.0.1:1234", xff: []string{"198.51.100.7"}, trust: true, want: "198.51.100.7"},
		{name: "rightmost wins over spoofed left", remoteAddr: "10.0.0.1:1234", xff: []string{"6.6.6.6, 198.51.100.7"}, trust: true, want: "198.51.100.7"},
		{name: "several spoofed entries", remoteAddr: "10.0.0.1:1234", xff: []string{"1.1.1.1, 2.2.2.2, 3.3.3.3, 198.51.100.7"}, trust: true, want: "198.51.100.7"},
		{name: "spoofed in a separate header line", remoteAddr: "10.0.0.1:1234", xff: []string{"6.6.6.6", "198.51.100.7"}, trust: true, want: "198.51.100.7"},
		{name: "last line is the proxy's", remoteAddr: "10.0.0.1:1234", xff: []string{"6.6.6.6, 7.7.7.7", "8.8.8.8, 198.51.100.7"}, trust: true, want: "198.51.100.7"},
		{name: "no spaces", remoteAddr: "10.0.0.1:1234", xff: []string{"6.6.6.6,198.51.100.7"}, trust: true, want: "198.51.100.7"},
		{name: "extra spaces", remoteAddr: "10.0.0.1:1234", xff: []string{"6.6.6.6 ,   198.51.100.7   "}, trust: true, want: "198.51.100.7"},
		{name: "trailing empty entries skipped", remoteAddr: "10.0.0.1:1234", xff: []string{"6.6.6.6, 198.51.100.7, , "}, trust: true, want: "198.51.100.7"},
		{name: "empty header", remoteAddr: "10.0.0.1:1234", xff: []string{""}, trust: true, want: "10.0.0.1"},
		{name: "only commas", remoteAddr: "10.0.0.1:1234", xff: []string{" , ,"}, trust: true, want: "10.0.0.1"},
		{name: "garbage rightmost falls back to peer", remoteAddr: "10.0.0.1:1234", xff: []string{"198.51.100.7, not-an-ip"}, trust: true, want: "10.0.0.1"},
		{name: "garbage only", remoteAddr: "10.0.0.1:1234", xff: []string{"unknown"}, trust: true, want: "10.0.0.1"},
		{name: "injection attempt", remoteAddr: "10.0.0.1:1234", xff: []string{"1.2.3.4; DROP TABLE"}, trust: true, want: "10.0.0.1"},
		{name: "forwarded ipv6", remoteAddr: "10.0.0.1:1234", xff: []string{"2001:db8::1"}, trust: true, want: "2001:db8::1"},
		{name: "forwarded ipv6 bracketed", remoteAddr: "10.0.0.1:1234", xff: []string{"[2001:db8::1]"}, trust: true, want: "2001:db8::1"},
		{name: "forwarded ipv6 with port", remoteAddr: "10.0.0.1:1234", xff: []string{"[2001:db8::1]:5555"}, trust: true, want: "2001:db8::1"},
		{name: "forwarded ipv4 with port", remoteAddr: "10.0.0.1:1234", xff: []string{"198.51.100.7:5555"}, trust: true, want: "198.51.100.7"},
		{name: "forwarded ipv6 uppercase is canonicalised", remoteAddr: "10.0.0.1:1234", xff: []string{"2001:DB8:0:0:0:0:0:1"}, trust: true, want: "2001:db8::1"},
		{name: "forwarded v4-mapped", remoteAddr: "10.0.0.1:1234", xff: []string{"::ffff:198.51.100.7"}, trust: true, want: "198.51.100.7"},
		{name: "forwarded zone dropped", remoteAddr: "10.0.0.1:1234", xff: []string{"fe80::1%eth0"}, trust: true, want: "fe80::1"},
		{name: "peer ipv6 with port", remoteAddr: "[2001:db8::2]:443", trust: false, want: "2001:db8::2"},
		{name: "peer ipv6 loopback", remoteAddr: "[::1]:80", trust: false, want: "::1"},
		{name: "peer without port", remoteAddr: "203.0.113.9", trust: false, want: "203.0.113.9"},
		{name: "peer v4-mapped", remoteAddr: "[::ffff:203.0.113.9]:80", trust: false, want: "203.0.113.9"},
		{name: "peer hostname-ish stays as is", remoteAddr: "weird:80", trust: false, want: "weird"},
		{name: "peer unix socket", remoteAddr: "@", trust: false, want: "@"},
		{name: "empty peer", remoteAddr: "", trust: false, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = tt.remoteAddr
			for _, v := range tt.xff {
				r.Header.Add("X-Forwarded-For", v)
			}
			if got := ClientIP(r, tt.trust); got != tt.want {
				t.Errorf("ClientIP = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestKeyForIP(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"203.0.113.9", "203.0.113.9"},
		{"2001:db8:1:2:3:4:5:6", "2001:db8:1:2::/64"},
		{"2001:db8:1:2:ffff:ffff:ffff:ffff", "2001:db8:1:2::/64"},
		{"::1", "::/64"},
		{"::ffff:203.0.113.9", "203.0.113.9"},
		{"not-an-ip", "not-an-ip"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := KeyForIP(tt.in); got != tt.want {
			t.Errorf("KeyForIP(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	if KeyForIP("2001:db8:1:2::1") != KeyForIP("2001:db8:1:2::2") {
		t.Error("addresses of one /64 must share a key")
	}
	if KeyForIP("2001:db8:1:2::1") == KeyForIP("2001:db8:1:3::1") {
		t.Error("different /64s must not share a key")
	}
}
