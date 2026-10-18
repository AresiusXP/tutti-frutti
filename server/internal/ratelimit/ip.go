package ratelimit

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// ClientIP returns the IP address of the client of r.
//
// With trustForwardedFor it is the rightmost non-empty entry of the
// X-Forwarded-For header (several header lines are read as one list): that is
// the entry appended by our own reverse proxy, while everything to its left is
// supplied by the client and ignored. If that entry is not a valid IP (the
// header did not come from a trusted proxy) or the header is absent, and
// whenever trustForwardedFor is false, it is the host part of r.RemoteAddr.
// The result is a canonical IP string (IPv4-mapped IPv6 addresses are
// unmapped, zones and ports dropped).
func ClientIP(r *http.Request, trustForwardedFor bool) string {
	if trustForwardedFor {
		if ip, ok := rightmostForwarded(r.Header.Values("X-Forwarded-For")); ok {
			return ip
		}
	}
	return remoteIP(r.RemoteAddr)
}

// KeyForIP maps a client IP to a rate-limit key: IPv4 addresses are used as
// is, IPv6 addresses are reduced to their /64 so a client that owns a whole
// /64 cannot dodge the limit by rotating addresses.
func KeyForIP(ip string) string {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	addr = addr.Unmap()
	if addr.Is6() {
		if p, err := addr.Prefix(64); err == nil {
			return p.String()
		}
	}
	return addr.String()
}

func rightmostForwarded(values []string) (string, bool) {
	for i := len(values) - 1; i >= 0; i-- {
		parts := strings.Split(values[i], ",")
		for j := len(parts) - 1; j >= 0; j-- {
			entry := strings.TrimSpace(parts[j])
			if entry == "" {
				continue
			}
			return parseIP(entry)
		}
	}
	return "", false
}

// parseIP accepts "1.2.3.4", "1.2.3.4:5", "::1", "[::1]" and "[::1]:5".
func parseIP(s string) (string, bool) {
	if a, err := netip.ParseAddr(s); err == nil {
		return canonical(a), true
	}
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return canonical(ap.Addr()), true
	}
	if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
		if a, err := netip.ParseAddr(s[1 : len(s)-1]); err == nil {
			return canonical(a), true
		}
	}
	return "", false
}

func canonical(a netip.Addr) string { return a.Unmap().WithZone("").String() }

func remoteIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	if ip, ok := parseIP(host); ok {
		return ip
	}
	return host
}
