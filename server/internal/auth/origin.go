package auth

import (
	"net/http"
	"net/url"
	"strings"
)

// NormalizeOrigin returns the canonical scheme://host[:port] form of an
// http(s) origin (lower-case, default port dropped), or "" when o is not a
// plain origin (e.g. "null", a URL with a path, userinfo, another scheme).
func NormalizeOrigin(o string) string {
	u, err := url.Parse(o)
	if err != nil || u.Host == "" || u.User != nil || u.Opaque != "" ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return ""
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return ""
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	port := u.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	if port != "" {
		host += ":" + port
	}
	return scheme + "://" + host
}

// CheckOrigin reports whether the Origin header of r is acceptable: it must
// match (scheme, host and port; host case-insensitively) one of allowed.
//
// When allowed is empty (development, nothing configured) an Origin whose
// host equals the request's Host is accepted instead, like the WebSocket
// library's default. A request without an Origin header is accepted only
// when allowMissing is set; browsers always send Origin on unsafe methods and
// on WebSocket upgrades, so allowMissing exists for non-browser clients in
// development only. The "null" origin is never accepted.
func CheckOrigin(r *http.Request, allowed []string, allowMissing bool) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return allowMissing
	}
	norm := NormalizeOrigin(origin)
	if norm == "" {
		return false
	}
	if len(allowed) == 0 {
		u, err := url.Parse(origin)
		return err == nil && strings.EqualFold(u.Host, r.Host)
	}
	for _, a := range allowed {
		if NormalizeOrigin(a) == norm {
			return true
		}
	}
	return false
}
