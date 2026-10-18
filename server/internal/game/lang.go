package game

import (
	"slices"
	"strings"
	"time"
)

// Lang is a lobby language. It selects the alphabet, the hard-letter list,
// the category labels and the normalization rules.
type Lang string

// Supported lobby languages.
const (
	LangES Lang = "es"
	LangEN Lang = "en"
	LangNL Lang = "nl"
)

// Valid reports whether l is a supported language.
func (l Lang) Valid() bool { return l == LangES || l == LangEN || l == LangNL }

// Langs returns every supported lobby language. Code that has to treat all of
// them alike (the duplicate check of custom categories) iterates over this
// list instead of naming the languages, so a new one cannot be forgotten.
func Langs() []Lang { return []Lang{LangES, LangEN, LangNL} }

// Timing constants (DESIGN.md sections 3 and 10).
const (
	// LetterTickDuration is how long each letter of the silent alphabet lasts.
	LetterTickDuration = 120 * time.Millisecond
	// ChooserTimeout is how long the chooser has before the server stops the
	// alphabet for them.
	ChooserTimeout = 10 * time.Second
	// GraceDuration is the countdown between a Basta call and the input lock.
	GraceDuration = 3 * time.Second
	// LateUpdateAllowance is how long after the lock answer updates are still
	// accepted (network lag allowance).
	LateUpdateAllowance = 500 * time.Millisecond
)

// Limits.
const (
	// MaxPlayers is the lobby capacity (players that have not left).
	MaxPlayers = 10
	// MinPlayers is the minimum number of seated players to start a game.
	MinPlayers = 2
	// MaxAnswerLen is the maximum answer length in runes.
	MaxAnswerLen = 40
	// MaxNameLen is the maximum nickname length in runes.
	MaxNameLen = 16
	// MaxCustomLen is the maximum custom category label length in runes.
	MaxCustomLen = 24
	// MinCategories and MaxCategories bound the number of categories per game.
	MinCategories = 3
	MaxCategories = 10
)

var alphabets = map[Lang][]string{
	LangES: strings.Split("A B C D E F G H I J K L M N Ñ O P Q R S T U V W X Y Z", " "),
	LangEN: strings.Split("A B C D E F G H I J K L M N O P Q R S T U V W X Y Z", " "),
	LangNL: strings.Split("A B C D E F G H I J K L M N O P Q R S T U V W X Y Z", " "),
}

// hardLetters is the editable table of hard letters per language.
var hardLetters = map[Lang][]string{
	LangES: {"K", "Ñ", "Q", "W", "X", "Y", "Z"},
	LangEN: {"Q", "U", "V", "X", "Y", "Z"},
	LangNL: {"Q", "X", "Y"},
}

// Alphabet returns the letters of lang in alphabetical order: A–Z with Ñ
// after N for Spanish (27 letters), A–Z for English and Dutch (26). It
// returns nil for an unknown language.
func Alphabet(lang Lang) []string { return slices.Clone(alphabets[lang]) }

// HardLetters returns the hard letters of lang.
func HardLetters(lang Lang) []string { return slices.Clone(hardLetters[lang]) }

// AvailableLetters returns the alphabet of lang, in order, without the used
// letters and, if excludeHard is set, without the hard letters.
func AvailableLetters(lang Lang, excludeHard bool, used []string) []string {
	var hard []string
	if excludeHard {
		hard = hardLetters[lang]
	}
	out := make([]string, 0, len(alphabets[lang]))
	for _, l := range alphabets[lang] {
		if slices.Contains(used, l) || slices.Contains(hard, l) {
			continue
		}
		out = append(out, l)
	}
	return out
}

// LetterAt returns the letter the silent alphabet shows after elapsed time:
// available[floor(elapsed/LetterTickDuration) mod len(available)]. A negative
// elapsed gives the first letter; it returns "" when nothing is available.
func LetterAt(available []string, elapsed time.Duration) string {
	if len(available) == 0 {
		return ""
	}
	if elapsed < 0 {
		elapsed = 0
	}
	return available[int(elapsed/LetterTickDuration)%len(available)]
}
