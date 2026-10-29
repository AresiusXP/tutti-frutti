package bot

import (
	"slices"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/AresiusXP/tutti-frutti/server/internal/catalog"
	"github.com/AresiusXP/tutti-frutti/server/internal/game"
)

func TestSupported(t *testing.T) {
	for _, lang := range game.Langs() {
		for _, hard := range []bool{false, true} {
			// Every subset of three to six classics is supported, regardless of
			// order, hard letters, stop voting or finite/unlimited round count.
			for mask := 0; mask < 1<<6; mask++ {
				s := game.DefaultSettings()
				s.Lang, s.ExcludeHardLetters, s.TimeLimitSeconds = lang, hard, 60
				s.Rounds, s.StopVote = 0, false
				s.Categories = nil
				for i, category := range catalog.Defaults() {
					if mask&(1<<i) != 0 {
						s.Categories = append(s.Categories, game.CategoryRef{ID: category.ID})
					}
				}
				slices.Reverse(s.Categories)
				want := len(s.Categories) >= 3
				if got := Supported(s); got != want {
					t.Errorf("Supported(%+v) = %v, want %v", s, got, want)
				}
			}
		}
	}
	for _, seconds := range []int{60, 90, 120, 180} {
		s := game.DefaultSettings()
		s.TimeLimitSeconds = seconds
		if !Supported(s) {
			t.Errorf("valid limit %d unsupported", seconds)
		}
	}
	for _, tt := range []struct {
		name   string
		change func(*game.Settings)
	}{
		{"no limit", func(s *game.Settings) { s.TimeLimitSeconds = 0 }},
		{"negative limit", func(s *game.Settings) { s.TimeLimitSeconds = -1 }},
		{"invalid limit", func(s *game.Settings) { s.TimeLimitSeconds = 61 }},
		{"unknown language", func(s *game.Settings) { s.Lang = "fr" }},
		{"empty language", func(s *game.Settings) { s.Lang = "" }},
		{"negative rounds", func(s *game.Settings) { s.Rounds = -1 }},
		{"too many rounds", func(s *game.Settings) { s.Rounds = 100 }},
		{"no categories", func(s *game.Settings) { s.Categories = nil }},
		{"two categories", func(s *game.Settings) { s.Categories = s.Categories[:2] }},
		{"seven categories", func(s *game.Settings) {
			s.Categories = append(s.Categories, game.CategoryRef{ID: "ciudad"})
		}},
		{"nonclassic", func(s *game.Settings) { s.Categories[0] = game.CategoryRef{ID: "ciudad"} }},
		{"unknown id", func(s *game.Settings) { s.Categories[0] = game.CategoryRef{ID: "name"} }},
		{"duplicate", func(s *game.Settings) { s.Categories[0] = s.Categories[1] }},
		{"custom", func(s *game.Settings) { s.Categories[0] = game.CategoryRef{Custom: "Planets"} }},
		{"id and custom", func(s *game.Settings) { s.Categories[0].Custom = "Planets" }},
		{"empty category", func(s *game.Settings) { s.Categories[0] = game.CategoryRef{} }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := game.DefaultSettings()
			s.TimeLimitSeconds = 60
			tt.change(&s)
			if Supported(s) {
				t.Errorf("unsupported settings accepted: %+v", s)
			}
		})
	}
}

// These are intentional gaps, not promises that every round can be completed.
// All other alphabet/category pairs have answers. The test makes coverage
// changes explicit, while corpus validation examines every alternative.
func TestCorpusCoverageAndValidity(t *testing.T) {
	gaps := map[game.Lang]map[string]string{
		game.LangES: {"nombre": "Ñ", "animal": "", "pais": "ÑQWX", "color": "KÑQWXY", "comida": "X", "marca": "Ñ"},
		game.LangEN: {"nombre": "", "animal": "", "pais": "WX", "color": "QXZ", "comida": "X", "marca": ""},
		game.LangNL: {"nombre": "", "animal": "X", "pais": "WXY", "color": "QXY", "comida": "X", "marca": ""},
	}
	if len(corpus) != len(game.Langs()) {
		t.Fatalf("corpus has %d languages", len(corpus))
	}
	defaults := catalog.Defaults()
	if len(defaults) != 6 {
		t.Fatalf("expected six classics, got %d", len(defaults))
	}
	for lang, categories := range corpus {
		if !lang.Valid() || len(categories) != len(defaults) {
			t.Fatalf("invalid corpus language/categories: %s (%d)", lang, len(categories))
		}
		for _, category := range defaults {
			if _, ok := categories[category.ID]; !ok {
				t.Fatalf("missing %s/%s", lang, category.ID)
			}
		}
		for category, letters := range categories {
			t.Run(string(lang)+"/"+category, func(t *testing.T) {
				c, ok := catalog.ByID(category)
				if !ok || !c.Default {
					t.Fatalf("not a classic: %s", category)
				}
				var missing strings.Builder
				for _, letter := range game.Alphabet(lang) {
					if len(letters[letter]) == 0 {
						missing.WriteString(letter)
						if Answer(lang, category, letter, ^uint64(0)) != "" {
							t.Errorf("missing letter %s should return blank", letter)
						}
					}
				}
				if got, want := missing.String(), gaps[lang][category]; got != want {
					t.Errorf("missing letters = %q, want %q", got, want)
				}
				multiple := 0
				for letter, choices := range letters {
					if !slices.Contains(game.Alphabet(lang), letter) || len(choices) == 0 {
						t.Fatalf("invalid letter/empty alternatives: %q", letter)
					}
					if len(choices) >= 2 {
						multiple++
					}
					seen := map[string]bool{}
					for variant, answer := range choices {
						checkText(t, answer, game.MaxAnswerLen)
						normalized := game.Normalize(answer, lang)
						if normalized == "" || seen[normalized] {
							t.Errorf("empty or duplicate normalized answer: %q", answer)
						}
						seen[normalized] = true
						if !game.StartsWithLetter(answer, letter, lang) {
							t.Errorf("%q does not start with %s", answer, letter)
						}
						for _, v := range []uint64{uint64(variant), uint64(variant + len(choices))} {
							if got := Answer(lang, category, letter, v); got != answer {
								t.Errorf("variant %d = %q, want %q", v, got, answer)
							}
						}
					}
					if got := Answer(lang, category, letter, ^uint64(0)); !slices.Contains(choices, got) {
						t.Errorf("maximum variant returned uncurated answer %q", got)
					}
				}
				if multiple < 12 {
					t.Errorf("only %d letters have multiple choices; want at least 12", multiple)
				}
				t.Logf("%d/%d letters; %d with multiple choices", len(letters), len(game.Alphabet(lang)), multiple)
			})
		}
	}
}

func TestAnswerKnownCases(t *testing.T) {
	for _, tt := range []struct {
		lang                   game.Lang
		category, letter, want string
	}{
		{game.LangES, "pais", "M", "México"},
		{game.LangES, "animal", "N", "Nutria"},
		{game.LangES, "animal", "ñ", "Ñandú"},
		{game.LangES, "animal", "N\u0303", "Ñandú"},
		{game.LangES, "comida", "Ñ", "Ñoquis"},
		{game.LangEN, "animal", "n", "Newt"},
		{game.LangEN, "nombre", "Á", "Alice"},
		{game.LangNL, "pais", "I", "IJsland"},
		{game.LangNL, "animal", "I", "IJsbeer"},
		{game.LangNL, "comida", "I", "IJs"},
		{game.LangNL, "pais", "Y", ""},
		{game.LangES, "pais", "Ñ", ""},
		{game.LangEN, "color", "X", ""},
		{"fr", "nombre", "A", ""},
		{game.LangES, "name", "A", ""},
		{game.LangES, "ciudad", "A", ""},
		{game.LangES, "nombre", "", ""},
		{game.LangES, "nombre", "AB", ""},
		{game.LangNL, "pais", "IJ", ""}, // IJ is not a round letter.
	} {
		if got := Answer(tt.lang, tt.category, tt.letter, 0); got != tt.want {
			t.Errorf("Answer(%q, %q, %q, 0) = %q, want %q", tt.lang, tt.category, tt.letter, got, tt.want)
		}
	}
	if game.Normalize(Answer(game.LangNL, "pais", "I", 0), game.LangNL) != game.Normalize("Ĳsland", game.LangNL) {
		t.Fatal("Dutch IJ and ligature normalization disagree")
	}
}

func TestIdentity(t *testing.T) {
	// Palette contract from web/src/lib/avatar.ts, checked independently of
	// the arrays used by Identity so an accidental unsupported value fails.
	emojis := []string{"🍌", "🍊", "🍓", "🍇", "🍉", "🍍"}
	colors := []string{"banana", "orange", "strawberry", "lime", "grape", "blueberry"}
	for _, lang := range game.Langs() {
		seen := map[string]bool{}
		for v := uint64(0); v < 24; v++ {
			name, avatar := Identity(lang, v)
			checkText(t, name, game.MaxNameLen)
			if normalized := game.Normalize(name, lang); seen[normalized] {
				t.Errorf("duplicate identity %s/%d: %s", lang, v, name)
			} else {
				seen[normalized] = true
			}
			if !slices.Contains(emojis, avatar.Emoji) || !slices.Contains(colors, avatar.Color) {
				t.Errorf("unsupported avatar: %+v", avatar)
			}
			if name2, avatar2 := Identity(lang, v); name != name2 || avatar != avatar2 {
				t.Fatal("identity is not deterministic")
			}
		}
		name, avatar := Identity(lang, ^uint64(0))
		checkText(t, name, game.MaxNameLen)
		if !slices.Contains(emojis, avatar.Emoji) || !slices.Contains(colors, avatar.Color) {
			t.Errorf("unsupported maximum-variant avatar: %+v", avatar)
		}
	}
	if name, avatar := Identity("unknown", 3); name != "Daniel" || avatar.Emoji == "" {
		t.Fatalf("invalid fallback: %q %+v", name, avatar)
	}
}

func checkText(t *testing.T, text string, maxLen int) {
	t.Helper()
	if !utf8.ValidString(text) || text == "" || utf8.RuneCountInString(text) > maxLen || strings.Join(strings.Fields(text), " ") != text {
		t.Errorf("invalid text/length/whitespace: %q", text)
	}
	for _, r := range text {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			t.Errorf("invisible/control character in %q", text)
		}
	}
}
