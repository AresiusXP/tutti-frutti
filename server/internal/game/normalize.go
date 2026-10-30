package game

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// combiningTilde is U+0303, the mark that turns "n" into "ñ" when decomposed.
const combiningTilde = '\u0303'

// folded maps lower-case Latin letters that have no canonical decomposition,
// so stripping combining marks leaves them alone, to their plain spelling.
// The Dutch ligature ĳ (U+0133, its capital U+0132 lower-cases to it) becomes
// "ij" in every language: it only exists in Dutch text, where "ĳsland" and
// "ijsland" are the same word.
var folded = map[rune]string{
	'ø': "o", 'đ': "d", 'ð': "d", 'ł': "l", 'ħ': "h", 'ŧ': "t", 'ı': "i",
	'ß': "ss", 'æ': "ae", 'œ': "oe", 'ĳ': "ij",
}

// stripFormat removes every Unicode format character (category Cf): zero
// width space, joiners, soft hyphen, BOM, bidi marks and the like. They are
// invisible, so text that differs only by them must compare equal.
//
// It also removes the control characters (category Cc) that are not
// whitespace, NUL above all: PostgreSQL's jsonb cannot store U+0000, so one
// in a nickname, answer or custom category would make the room's snapshot
// unsaveable. Tab, newline and the other whitespace controls stay (cleanText
// collapses them to a space).
func stripFormat(s string) string {
	return strings.Map(func(r rune) rune {
		if invisible(r) {
			return -1
		}
		return r
	}, s)
}

// invisible reports the runes stripFormat removes: format characters and the
// non-whitespace controls.
func invisible(r rune) bool {
	return unicode.Is(unicode.Cf, r) || (unicode.IsControl(r) && !unicode.IsSpace(r))
}

const zwj = '\u200d' // zero width joiner

// stripKeepingEmojiJoiners is stripFormat for text that is shown (nicknames,
// custom category labels): a zero width joiner that glues two emoji together
// stays, or "👨‍💻" would fall apart into "👨💻". Anywhere else a joiner is as
// invisible as the other format characters and goes.
func stripKeepingEmojiJoiners(s string) string {
	rs := []rune(s)
	var b strings.Builder
	b.Grow(len(s))
	for i, r := range rs {
		if invisible(r) && !(r == zwj && joinsEmoji(rs, i)) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// joinsEmoji reports whether the joiner at rs[i] sits between two emoji: a
// symbol (category So) before it, ignoring the variation selector and skin
// tone modifier that may follow that symbol, and a symbol after it.
func joinsEmoji(rs []rune, i int) bool {
	j := i - 1
	for j >= 0 && (unicode.Is(unicode.Mn, rs[j]) || unicode.Is(unicode.Sk, rs[j])) {
		j--
	}
	return j >= 0 && unicode.Is(unicode.So, rs[j]) &&
		i+1 < len(rs) && unicode.Is(unicode.So, rs[i+1])
}

// plainField reports whether s is valid UTF-8 without control characters
// (NUL included). Format characters stay allowed: emoji sequences need the
// zero width joiner.
func plainField(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// cleanText strips format characters (keeping the joiners inside emoji
// sequences), trims and collapses inner whitespace to single spaces. It is
// used for the display text a player types (nicknames, custom category
// labels).
func cleanText(s string) string {
	return strings.Join(strings.Fields(stripKeepingEmojiJoiners(s)), " ")
}

// Normalize returns the canonical form used to compare answers: invisible
// format characters (category Cf, e.g. U+200B or the soft hyphen) removed,
// trimmed, inner whitespace collapsed to single spaces, lower-cased,
// decomposed (NFD) and stripped of every combining mark, so any diacritic goes
// and NFC and NFD spellings agree ("México" == "mexico", "Ắng" == "ang",
// "ü" -> "u"). A few letters that do not decompose are folded too (ø, đ, ð,
// ł, ...), among them the Dutch ligature ĳ / Ĳ, which becomes "ij" in every
// language ("Ĳsland" == "IJsland" == "ijsland"; "ij" is two letters, so an
// answer that starts with it starts with "i", never with "y").
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

// CleanText is cleanText for the room and REST layers: the same cleaner as
// nicknames and custom labels (invisible format and control characters removed,
// emoji joiners kept, whitespace trimmed and collapsed). It never truncates.
func CleanText(s string) string { return cleanText(s) }
