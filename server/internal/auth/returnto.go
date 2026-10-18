package auth

import "strings"

// roomCodeAlphabet is the alphabet of room codes (see internal/room).
const roomCodeAlphabet = "BCDFGHJKLMNPQRSTVWXZ23456789"

const roomCodeLen = 4

// SafeReturnTo maps the returnTo query parameter of the login endpoint to the
// path the browser is sent back to: exactly "/" or "/<ROOMCODE>" (4 characters
// of the room code alphabet, any case, normalised to upper case). Anything
// else, whatever it is (another host, a scheme, "//host", a backslash, dot
// segments, an encoded slash, control characters, a long string), becomes "/".
// There is no trimming and no decoding: the value is compared as it is.
func SafeReturnTo(raw string) string {
	if len(raw) != 1+roomCodeLen || raw[0] != '/' {
		return "/"
	}
	code := []byte(raw[1:])
	for i, c := range code {
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
			code[i] = c
		}
		if strings.IndexByte(roomCodeAlphabet, c) < 0 {
			return "/"
		}
	}
	return "/" + string(code)
}
