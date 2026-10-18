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
	nl := Alphabet(LangNL)
	if len(nl) != 26 || slices.Contains(nl, "Ñ") || slices.Contains(nl, "IJ") {
		t.Errorf("nl alphabet = %v, want 26 letters without Ñ and without a digraph", nl)
	}
	if got := strings.Join(nl, ""); got != "ABCDEFGHIJKLMNOPQRSTUVWXYZ" {
		t.Errorf("nl alphabet = %q", got)
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
	if got, want := HardLetters(LangNL), []string{"Q", "X", "Y"}; !slices.Equal(got, want) {
		t.Errorf("nl hard letters = %v, want %v", got, want)
	}
	if HardLetters("fr") != nil {
		t.Error("unknown language must have no hard letters")
	}
	nl := HardLetters(LangNL)
	nl[0] = "mutated"
	if HardLetters(LangNL)[0] != "Q" {
		t.Error("HardLetters exposes internal state")
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
		{"nl all", LangNL, false, nil, 26, "A", nil},
		{"nl no hard", LangNL, true, nil, 23, "A", []string{"Q", "X", "Y"}},
		{"nl used skipped", LangNL, true, []string{"A", "B"}, 21, "C", []string{"A", "B", "Q", "X", "Y"}},
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
			if !slices.IsSorted(got) && tt.lang != LangES {
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

func TestNLAvailableLettersKeepsTheEasyOnes(t *testing.T) {
	got := AvailableLetters(LangNL, true, nil)
	want := "ABCDEFGHIJKLMNOPRSTUVWZ"
	if strings.Join(got, "") != want {
		t.Errorf("nl letters without hard letters = %q, want %q", strings.Join(got, ""), want)
	}
}

func TestLangValid(t *testing.T) {
	for l, want := range map[Lang]bool{LangES: true, LangEN: true, LangNL: true, "fr": false, "": false, "NL": false, "nl ": false} {
		if l.Valid() != want {
			t.Errorf("Lang(%q).Valid() = %v", l, !want)
		}
	}
}

// Langs is what the language-agnostic code iterates over: it must list every
// language the engine knows, each of them valid and fully configured.
func TestLangsListsEveryLanguage(t *testing.T) {
	got := Langs()
	if !slices.Equal(got, []Lang{LangES, LangEN, LangNL}) {
		t.Fatalf("Langs() = %v", got)
	}
	if len(alphabets) != len(got) || len(hardLetters) != len(got) {
		t.Errorf("%d alphabets and %d hard-letter tables for %d languages", len(alphabets), len(hardLetters), len(got))
	}
	for _, l := range got {
		if !l.Valid() {
			t.Errorf("%q is listed but not valid", l)
		}
		alphabet := Alphabet(l)
		if len(alphabet) == 0 {
			t.Errorf("%q has no alphabet", l)
		}
		for _, h := range HardLetters(l) {
			if !slices.Contains(alphabet, h) {
				t.Errorf("hard letter %q is not in the %s alphabet", h, l)
			}
		}
	}
	got[0] = "mutated"
	if Langs()[0] != LangES {
		t.Error("Langs exposes internal state")
	}
}

// The number of rounds a lobby may ask for is the number of playable letters.
func TestMaxRoundsPerLanguage(t *testing.T) {
	for _, tt := range []struct {
		lang        Lang
		excludeHard bool
		want        int
	}{
		{LangES, false, 27}, {LangES, true, 20},
		{LangEN, false, 26}, {LangEN, true, 20},
		{LangNL, false, 26}, {LangNL, true, 23},
	} {
		if got := len(AvailableLetters(tt.lang, tt.excludeHard, nil)); got != tt.want {
			t.Errorf("%s excludeHard=%v: %d rounds, want %d", tt.lang, tt.excludeHard, got, tt.want)
		}
	}
}
