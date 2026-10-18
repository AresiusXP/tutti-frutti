package game

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// combiningTilde is U+0303, the mark that turns "n" into "ñ" when decomposed.
const combiningTilde = '\u0303'

// folded maps lower-case Latin letters that have no canonical decomposition,
// so stripping combining marks leaves them alone, to their plain spelling.
var folded = map[rune]string{
	'ø': "o", 'đ': "d", 'ð': "d", 'ł': "l", 'ħ': "h", 'ŧ': "t", 'ı': "i",
	'ß': "ss", 'æ': "ae", 'œ': "oe",
}

// stripFormat removes every Unicode format character (category Cf): zero
// width space, joiners, soft hyphen, BOM, bidi marks and the like. They are
// invisible, so text that differs only by them must compare equal.
func stripFormat(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, s)
}

// cleanText strips format characters, trims and collapses inner whitespace to
// single spaces. It is used for the display text a player types (nicknames,
// custom category labels, answers).
func cleanText(s string) string {
	return strings.Join(strings.Fields(stripFormat(s)), " ")
}

// Normalize returns the canonical form used to compare answers: invisible
// format characters (category Cf, e.g. U+200B or the soft hyphen) removed,
// trimmed, inner whitespace collapsed to single spaces, lower-cased,
// decomposed (NFD) and stripped of every combining mark, so any diacritic goes
// and NFC and NFD spellings agree ("México" == "mexico", "Ắng" == "ang",
// "ü" -> "u"). A few letters that do not decompose are folded too (ø, đ, ð,
// ł, ...).
//
// In Spanish lobbies "ñ" is a letter of its own and is kept; in every other
// language it folds to "n".
func Normalize(s string, lang Lang) string {
	out := make([]rune, 0, len(s))
	pendingSpace := false
	for _, r := range norm.NFD.String(stripFormat(s)) {
		switch {
		case unicode.IsSpace(r):
			pendingSpace = len(out) > 0
			continue
		case unicode.Is(unicode.Mn, r):
			if r == combiningTilde && lang == LangES && !pendingSpace && len(out) > 0 && out[len(out)-1] == 'n' {
				out[len(out)-1] = 'ñ'
			}
			continue
		}
		if pendingSpace {
			out = append(out, ' ')
			pendingSpace = false
		}
		r = unicode.ToLower(r)
		if plain, ok := folded[r]; ok {
			out = append(out, []rune(plain)...)
			continue
		}
		out = append(out, r)
	}
	return string(out)
}

// StartsWithLetter reports whether the normalized answer starts with letter
// (compared case- and diacritic-insensitively, with the same ñ rule as
// Normalize). Only the first rune counts: leading punctuation is not skipped.
func StartsWithLetter(answer, letter string, lang Lang) bool {
	l := Normalize(letter, lang)
	return l != "" && strings.HasPrefix(Normalize(answer, lang), l)
}
