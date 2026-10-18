package auth

import (
	"strings"
	"testing"
)

func TestSafeReturnTo(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"root", "/", "/"},
		{"a room", "/KT7P", "/KT7P"},
		{"lower case is normalised", "/kt7p", "/KT7P"},
		{"mixed case is normalised", "/Kt7p", "/KT7P"},
		{"every alphabet character", "/BCDF", "/BCDF"},
		{"digits 2 to 9", "/2345", "/2345"},
		{"digits 6 to 9", "/6789", "/6789"},
		{"the rest of the alphabet", "/GHJK", "/GHJK"},
		{"and the rest", "/LMNP", "/LMNP"},
		{"Q R S T", "/QRST", "/QRST"},
		{"V W X Z", "/VWXZ", "/VWXZ"},

		{"empty", "", "/"},
		{"no leading slash", "KT7P", "/"},
		{"a character instead of the slash", "xKT7P", "/"},
		{"a backslash instead of the slash", `\KT7P`, "/"},
		{"a dot instead of the slash", ".KT7P", "/"},
		{"a percent sign instead of the slash", "%KT7P", "/"},
		{"protocol-relative", "//evil.com", "/"},
		{"protocol-relative with a code", "//KT7P", "/"},
		{"backslash", `/\evil.com`, "/"},
		{"backslash after a slash", `/\\evil.com`, "/"},
		{"slash backslash", `/\KT7`, "/"},
		{"absolute URL", "https://evil.com", "/"},
		{"absolute URL to ourselves", "https://tuttifrutti.aresius.xyz/KT7P", "/"},
		{"javascript scheme", "javascript:alert(1)", "/"},
		{"data scheme", "data:text/html,<script>alert(1)</script>", "/"},
		{"dot segments", "/KT7P/../x", "/"},
		{"dot segments only", "/../", "/"},
		{"parent of a room", "/KT7P/..", "/"},
		{"trailing slash", "/KT7P/", "/"},
		{"a sub path", "/KT7P/lobby", "/"},
		{"a query", "/KT7P?x=1", "/"},
		{"a query on root", "/?login=failed", "/"},
		{"a fragment", "/KT7P#x", "/"},
		{"another page", "/stats", "/"},
		{"an API path", "/api/me", "/"},
		{"encoded slash after the first", "/%2fevil.com", "/"},
		{"encoded leading slash", "%2f%2fevil.com", "/"},
		{"encoded code", "/%4bT7P", "/"},
		{"double encoded", "%252FKT7P", "/"},
		{"CRLF", "/KT7P\r\nSet-Cookie: x=1", "/"},
		{"CRLF only", "\r\n", "/"},
		{"LF after a code", "/KT7P\n", "/"},
		{"tab", "/KT\t7", "/"},
		{"NUL", "/KT7\x00", "/"},
		{"space before", " /KT7P", "/"},
		{"space after", "/KT7P ", "/"},
		{"space inside", "/KT 7", "/"},
		{"too short", "/KT7", "/"},
		{"too long by one", "/KT7PX", "/"},
		{"very long", "/" + strings.Repeat("A", 100_000), "/"},
		{"very long path", strings.Repeat("/", 100_000), "/"},
		{"vowel", "/KA7P", "/"},
		{"look-alike zero", "/K07P", "/"},
		{"look-alike one", "/K17P", "/"},
		{"look-alike letter O", "/KO7P", "/"},
		{"non-ASCII letter", "/KT7ṖP", "/"},
		{"full-width slash", "／KT7P", "/"},
		{"unicode that lower-cases to ASCII", "/K\u212a7P", "/"}, // KELVIN SIGN
		{"unicode that upper-cases to ASCII", "/kt7\u0131", "/"}, // dotless i
		{"null byte room", "/\x00\x00\x00\x00", "/"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SafeReturnTo(tc.in)
			if got != tc.want {
				t.Errorf("SafeReturnTo(%q) = %q, want %q", truncate(tc.in, 40), got, tc.want)
			}
			// Whatever the input, the answer is something we would redirect to.
			if again := SafeReturnTo(got); again != got {
				t.Errorf("not idempotent: %q -> %q", got, again)
			}
		})
	}
}

func FuzzSafeReturnTo(f *testing.F) {
	for _, s := range []string{"/", "/KT7P", "//evil", `/\x`, "", "/kt7p", "/KT7P/../"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		got := SafeReturnTo(in)
		if got != "/" && !(len(got) == 5 && got[0] == '/' && strings.IndexFunc(got[1:], func(r rune) bool {
			return !strings.ContainsRune(roomCodeAlphabet, r)
		}) < 0) {
			t.Errorf("SafeReturnTo(%q) = %q: neither / nor /<CODE>", in, got)
		}
	})
}
