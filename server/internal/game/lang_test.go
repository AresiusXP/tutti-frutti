package game

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestAlphabet(t *testing.T) {
	es := Alphabet(LangES)
	if len(es) != 27 {
		t.Fatalf("es alphabet has %d letters, want 27", len(es))
	}
	if i := slices.Index(es, "Ñ"); i < 0 || es[i-1] != "N" || es[i+1] != "O" {
		t.Errorf("Ñ must sit between N and O: %v", es)
	}
	en := Alphabet(LangEN)
	if len(en) != 26 || slices.Contains(en, "Ñ") {
		t.Errorf("en alphabet = %v, want 26 letters without Ñ", en)
	}
	if got := strings.Join(en, ""); got != "ABCDEFGHIJKLMNOPQRSTUVWXYZ" {
		t.Errorf("en alphabet = %q", got)
	}
	if Alphabet("fr") != nil {
		t.Error("unknown language must have no alphabet")
	}
	es[0] = "mutated"
	if Alphabet(LangES)[0] != "A" {
		t.Error("Alphabet exposes internal state")
	}
}

func TestHardLetters(t *testing.T) {
	if got, want := HardLetters(LangES), []string{"K", "Ñ", "Q", "W", "X", "Y", "Z"}; !slices.Equal(got, want) {
		t.Errorf("es hard letters = %v, want %v", got, want)
	}
	if got, want := HardLetters(LangEN), []string{"Q", "U", "V", "X", "Y", "Z"}; !slices.Equal(got, want) {
		t.Errorf("en hard letters = %v, want %v", got, want)
	}
}

func TestAvailableLetters(t *testing.T) {
	tests := []struct {
		name        string
		lang        Lang
		excludeHard bool
		used        []string
		wantLen     int
		wantFirst   string
		absent      []string
	}{
		{"es all", LangES, false, nil, 27, "A", nil},
		{"es no hard", LangES, true, nil, 20, "A", []string{"K", "Ñ", "Q", "W", "X", "Y", "Z"}},
		{"en all", LangEN, false, nil, 26, "A", nil},
		{"en no hard", LangEN, true, nil, 20, "A", []string{"Q", "U", "V", "X", "Y", "Z"}},
		{"used skipped", LangES, true, []string{"A", "B"}, 18, "C", []string{"A", "B"}},
		{"used hard letter is harmless", LangES, true, []string{"K"}, 20, "A", nil},
		{"used with hard allowed", LangES, false, []string{"Ñ"}, 26, "A", []string{"Ñ"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AvailableLetters(tt.lang, tt.excludeHard, tt.used)
			if len(got) != tt.wantLen || got[0] != tt.wantFirst {
				t.Fatalf("got %d letters starting with %q: %v", len(got), got[0], got)
			}
			for _, l := range tt.absent {
				if slices.Contains(got, l) {
					t.Errorf("%q must not be available", l)
				}
			}
			if !slices.IsSorted(got) && tt.lang == LangEN {
				t.Errorf("letters must stay in alphabet order: %v", got)
			}
		})
	}
}

func TestLetterAt(t *testing.T) {
	letters := []string{"A", "B", "C"}
	tests := []struct {
		name      string
		available []string
		elapsed   time.Duration
		want      string
	}{
		{"start", letters, 0, "A"},
		{"119ms", letters, 119 * time.Millisecond, "A"},
		{"120ms", letters, 120 * time.Millisecond, "B"},
		{"239ms", letters, 239 * time.Millisecond, "B"},
		{"240ms", letters, 240 * time.Millisecond, "C"},
		{"wraps at 360ms", letters, 360 * time.Millisecond, "A"},
		{"ten seconds", letters, ChooserTimeout, "C"}, // 83 % 3 = 2
		{"negative", letters, -time.Second, "A"},
		{"single letter", []string{"Z"}, 5 * time.Second, "Z"},
		{"nothing available", nil, time.Second, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LetterAt(tt.available, tt.elapsed); got != tt.want {
				t.Errorf("LetterAt(%v, %v) = %q, want %q", tt.available, tt.elapsed, got, tt.want)
			}
		})
	}
}

func TestLangValid(t *testing.T) {
	for l, want := range map[Lang]bool{LangES: true, LangEN: true, "fr": false, "": false} {
		if l.Valid() != want {
			t.Errorf("Lang(%q).Valid() = %v", l, !want)
		}
	}
}
