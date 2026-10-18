package game

import (
	"fmt"
	"slices"
	"unicode/utf8"

	"github.com/AresiusXP/tutti-frutti/server/internal/catalog"
)

// AllowedTimeLimits are the accepted values of Settings.TimeLimitSeconds
// (0 = no limit).
var AllowedTimeLimits = []int{0, 60, 90, 120, 180}

// CategoryRef selects one category of the game: either a built-in catalog
// entry (ID) or a per-game custom label (Custom). Exactly one must be set.
type CategoryRef struct {
	ID     string `json:"id,omitempty"`
	Custom string `json:"custom,omitempty"`
}

// Settings are the lobby settings. Only the host can change them, and only
// in the lobby.
type Settings struct {
	Lang Lang `json:"lang"`
	// ExcludeHardLetters removes the hard letters from the alphabet.
	ExcludeHardLetters bool `json:"excludeHardLetters"`
	// Rounds is the number of rounds; 0 means unlimited (until the letters run
	// out).
	Rounds     int           `json:"rounds"`
	Categories []CategoryRef `json:"categories"`
	// TimeLimitSeconds is the round time limit; 0 means off.
	TimeLimitSeconds int `json:"timeLimitSeconds"`
}

// DefaultSettings returns the settings of a fresh lobby: Spanish, hard
// letters excluded, 5 rounds, the six default catalog categories, no time
// limit.
func DefaultSettings() Settings {
	s := Settings{Lang: LangES, ExcludeHardLetters: true, Rounds: 5}
	for _, c := range catalog.Defaults() {
		s.Categories = append(s.Categories, CategoryRef{ID: c.ID})
	}
	return s
}

// Clean returns a deep copy of s with every custom label cleaned: invisible
// format characters removed, trimmed, inner whitespace collapsed.
func (s Settings) Clean() Settings {
	s.Categories = slices.Clone(s.Categories)
	for i := range s.Categories {
		s.Categories[i].Custom = cleanText(s.Categories[i].Custom)
	}
	return s
}

// Validate checks the settings. Errors wrap ErrInvalidSettings; a category
// clash is a *DuplicateCategoryError that carries the conflicting label.
func (s Settings) Validate() error {
	if !s.Lang.Valid() {
		return fmt.Errorf("%w: unknown language %q", ErrInvalidSettings, s.Lang)
	}
	if !slices.Contains(AllowedTimeLimits, s.TimeLimitSeconds) {
		return fmt.Errorf("%w: time limit %d is not one of %v", ErrInvalidSettings, s.TimeLimitSeconds, AllowedTimeLimits)
	}
	if maxRounds := len(AvailableLetters(s.Lang, s.ExcludeHardLetters, nil)); s.Rounds < 0 || s.Rounds > maxRounds {
		return fmt.Errorf("%w: rounds must be 0 (unlimited) or 1 to %d, got %d", ErrInvalidSettings, maxRounds, s.Rounds)
	}
	if n := len(s.Categories); n < MinCategories || n > MaxCategories {
		return fmt.Errorf("%w: categories must be %d to %d, got %d", ErrInvalidSettings, MinCategories, MaxCategories, n)
	}

	builtin := builtinLabels(s.Lang)
	ids := map[string]bool{}
	customs := map[string]string{}
	for i, ref := range s.Categories {
		if (ref.ID == "") == (ref.Custom == "") {
			return fmt.Errorf("%w: category %d must set exactly one of id and custom", ErrInvalidSettings, i)
		}
		if ref.ID != "" {
			c, ok := catalog.ByID(ref.ID)
			if !ok {
				return fmt.Errorf("%w: unknown category id %q", ErrInvalidSettings, ref.ID)
			}
			if ids[ref.ID] {
				return &DuplicateCategoryError{Label: catalog.Label(c, string(s.Lang))}
			}
			ids[ref.ID] = true
			continue
		}

		label := cleanText(ref.Custom)
		if label == "" {
			return fmt.Errorf("%w: category %d has an empty custom label", ErrInvalidSettings, i)
		}
		if n := utf8.RuneCountInString(label); n > MaxCustomLen {
			return fmt.Errorf("%w: custom label %q is %d characters, max %d", ErrInvalidSettings, label, n, MaxCustomLen)
		}
		norm := Normalize(label, s.Lang)
		if existing, ok := builtin[norm]; ok {
			return &DuplicateCategoryError{Label: existing}
		}
		if existing, ok := customs[norm]; ok {
			return &DuplicateCategoryError{Label: existing}
		}
		customs[norm] = label
	}
	return nil
}

// Labels returns the display label of every selected category in the lobby
// language. Unknown ids yield their id.
func (s Settings) Labels() []string {
	out := make([]string, len(s.Categories))
	for i, ref := range s.Categories {
		c, ok := catalog.ByID(ref.ID)
		switch {
		case ref.ID == "":
			out[i] = ref.Custom
		case ok:
			out[i] = catalog.Label(c, string(s.Lang))
		default:
			out[i] = ref.ID
		}
	}
	return out
}

// builtinLabels maps the normalized form of every built-in label, in both
// languages, to the label shown in lang.
func builtinLabels(lang Lang) map[string]string {
	out := map[string]string{}
	for _, c := range catalog.All() {
		shown := catalog.Label(c, string(lang))
		out[Normalize(c.ES, lang)] = shown
		out[Normalize(c.EN, lang)] = shown
	}
	return out
}
