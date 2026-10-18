package store

import (
	"errors"
	"testing"
)

func TestValidateSnapshot(t *testing.T) {
	tests := []struct {
		name    string
		snap    Snapshot
		wantErr bool
	}{
		{"ok object", snap("ABCD", `{"a":1}`, baseTime), false},
		{"ok null literal", snap("ABCD", `null`, baseTime), false},
		{"ok escaped backslash then u0000", snap("ABCD", `{"a":"\\u0000"}`, baseTime), false},
		{"ok non-NUL escape", snap("ABCD", `{"a":"\u0001\u001f"}`, baseTime), false},
		{"ok large number", snap("ABCD", `{"n":1e999}`, baseTime), false},
		{"ok NUL text elsewhere", snap("ABCD", `{"u0000":1}`, baseTime), false},
		{"empty code", snap("", `{}`, baseTime), true},
		{"NUL in code", snap("A\x00", `{}`, baseTime), true},
		{"invalid UTF-8 code", snap("\xff", `{}`, baseTime), true},
		{"not JSON", snap("ABCD", `nope`, baseTime), true},
		{"invalid UTF-8 state", snap("ABCD", "\"\xc3\x28\"", baseTime), true},
		{"NUL escape", snap("ABCD", `"\u0000"`, baseTime), true},
		{"NUL escape after escaped backslash", snap("ABCD", `["\\","\u0000"]`, baseTime), true},
		{"NUL escape with huge number first", snap("ABCD", `[1e999,"\u0000"]`, baseTime), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateSnapshot(tc.snap)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateSnapshot() = %v, wantErr %v", err, tc.wantErr)
			}
			if err != nil && !errors.Is(err, ErrInvalidSnapshot) {
				t.Errorf("error %v does not wrap ErrInvalidSnapshot", err)
			}
		})
	}
}
