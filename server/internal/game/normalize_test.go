package game

import (
	"testing"

	"golang.org/x/text/unicode/norm"
)

func TestNormalize(t *testing.T) {
	tests := []struct {
		name string
		in   string
		lang Lang
		want string
	}{
		{"accent", "México", LangES, "mexico"},
		{"already plain", "mexico", LangES, "mexico"},
		{"accent en", "México", LangEN, "mexico"},
		{"trim and lower", "  Árbol  ", LangES, "arbol"},
		{"collapse spaces", "  la   casa\t\nblanca ", LangES, "la casa blanca"},
		{"diaeresis", "pingüino", LangES, "pinguino"},
		{"upper diaeresis", "ÜBER", LangEN, "uber"},
		{"enye kept in es", "Ñandú", LangES, "ñandu"},
		{"enye upper in es", "ÑANDÚ", LangES, "ñandu"},
		{"enye folds in en", "Ñandú", LangEN, "nandu"},
		{"enye folds in unknown lang", "Ñandú", "fr", "nandu"},
		{"decomposed enye in es", "n\u0303andu", LangES, "ñandu"},
		{"decomposed enye in en", "N\u0303andu", LangEN, "nandu"},
		{"tilde not after n is dropped", "a\u0303", LangES, "a"},
		{"tilde after space is dropped", "n \u0303", LangES, "n"},
		{"tilde at start is dropped", "\u0303n", LangES, "n"},
		{"decomposed accent", "cafe\u0301", LangES, "cafe"},
		{"ligatures", "Straße Æon Œuvre", LangEN, "strasse aeon oeuvre"},
		{"latin extended", "Łódź Çağrı Škoda", LangEN, "lodz cagri skoda"},
		{"non-breaking space", "a\u00a0b", LangEN, "a b"},
		{"empty", "", LangES, ""},
		{"blank", " \t ", LangES, ""},
		{"digits and punctuation kept", "R2-D2!", LangEN, "r2-d2!"},
		{"leading zero width space", "\u200bMéxico", LangES, "mexico"},
		{"zero width space inside", "Mé\u200bxico", LangEN, "mexico"},
		{"soft hyphen inside", "Ma\u00adrte", LangES, "marte"},
		{"zero width non-joiner and joiner", "a\u200cb\u200dc", LangEN, "abc"},
		{"word joiner and BOM", "\u2060Perro\ufeff", LangES, "perro"},
		{"bidi marks", "\u200ePerro\u200f", LangEN, "perro"},
		{"format characters around spaces collapse like spaces", "\u200b  la \u200b\u200b casa \u200b", LangES, "la casa"},
		{"format characters do not create spaces", "la\u200bcasa", LangES, "lacasa"},
		{"only format characters", "\u200b\u200c\u00ad\ufeff", LangES, ""},
		{"format characters between n and the combining tilde", "n\u200b\u0303u", LangES, "ñu"},
		{"stacked marks in one letter", "Ắng", LangES, "ang"},
		{"stacked marks decomposed", "A\u0306\u0301ng", LangES, "ang"},
		{"vietnamese", "Việt Nam", LangEN, "viet nam"},
		{"ring and caron", "Åke Čapek", LangES, "ake capek"},
		{"stroked o", "Ø", LangEN, "o"},
		{"stroked o in a word", "Øresund", LangES, "oresund"},
		{"stroked l", "Ł", LangEN, "l"},
		{"stroked l in a word", "Łódź", LangES, "lodz"},
		{"stroked d", "Đà Nẵng", LangEN, "da nang"},
		{"eth and stroked d", "Ðorđe", LangES, "dorde"},
		{"dotless i", "Iıİ", LangEN, "iii"},
		{"capital sharp s", "STRAẞE", LangEN, "strasse"},
		{"NFC enye in es", "\u00d1and\u00fa", LangES, "ñandu"},
		{"NFD enye in es", "N\u0303and\u0075\u0301", LangES, "ñandu"},
		{"NFC enye in en", "\u00d1and\u00fa", LangEN, "nandu"},
		{"NFD enye in en", "N\u0303and\u0075\u0301", LangEN, "nandu"},
		{"lower NFD enye in es", "n\u0303u", LangES, "ñu"},
		{"enye after an accented n mark", "n\u0301\u0303", LangES, "ñ"},
		{"space between n and the tilde", "n \u0303", LangES, "n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Normalize(tt.in, tt.lang); got != tt.want {
				t.Errorf("Normalize(%q, %q) = %q, want %q", tt.in, tt.lang, got, tt.want)
			}
		})
	}
}

// NFC and NFD spellings of the same text must normalize identically.
func TestNormalizeNFCAndNFDAgree(t *testing.T) {
	words := []struct {
		word string
		es   string
		en   string
	}{
		{"México", "mexico", "mexico"},
		{"árbol", "arbol", "arbol"},
		{"Ñandú", "ñandu", "nandu"},
		{"ÑANDÚ", "ñandu", "nandu"},
		{"pingüino", "pinguino", "pinguino"},
		{"Ắng  Việt", "ang viet", "ang viet"},
		{"  Cañón del Colorado ", "cañon del colorado", "canon del colorado"},
	}
	for _, w := range words {
		nfc, nfd := norm.NFC.String(w.word), norm.NFD.String(w.word)
		if nfc == nfd {
			t.Fatalf("setup: %q has no decomposition", w.word)
		}
		for _, tt := range []struct {
			lang Lang
			want string
		}{{LangES, w.es}, {LangEN, w.en}} {
			if got := Normalize(nfc, tt.lang); got != tt.want {
				t.Errorf("Normalize(NFC %q, %s) = %q, want %q", w.word, tt.lang, got, tt.want)
			}
			if got := Normalize(nfd, tt.lang); got != tt.want {
				t.Errorf("Normalize(NFD %q, %s) = %q, want %q", w.word, tt.lang, got, tt.want)
			}
		}
	}
}

func TestStripFormatAndCleanText(t *testing.T) {
	// Every format character (category Cf) goes; ordinary text does not.
	for _, r := range []rune{'\u00ad', '\u200b', '\u200c', '\u200d', '\u200e', '\u200f', '\u2060', '\u2066', '\ufeff', '\U000e0041'} {
		if got := stripFormat("a" + string(r) + "b"); got != "ab" {
			t.Errorf("stripFormat kept U+%04X: %q", r, got)
		}
	}
	if got := stripFormat("Ñandú 🦊 a-b"); got != "Ñandú 🦊 a-b" {
		t.Errorf("stripFormat changed ordinary text: %q", got)
	}
	tests := map[string]string{
		"  \u200bMa\u00adrte \t del\n sur ": "Marte del sur",
		"\u200b\u200b":                      "",
		"   ":                               "",
		"Ñandú":                             "Ñandú",
	}
	for in, want := range tests {
		if got := cleanText(in); got != want {
			t.Errorf("cleanText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeIdempotent(t *testing.T) {
	for _, s := range []string{"México", "Ñandú", "  Árbol  frondoso ", "Straße", "Ắng", "Øl Łódź", "N\u0303u", "\u200bMa\u00adrte \u200b x"} {
		for _, l := range []Lang{LangES, LangEN} {
			once := Normalize(s, l)
			if twice := Normalize(once, l); twice != once {
				t.Errorf("Normalize not idempotent for %q (%s): %q -> %q", s, l, once, twice)
			}
		}
	}
}

func TestStartsWithLetter(t *testing.T) {
	tests := []struct {
		name           string
		answer, letter string
		lang           Lang
		want           bool
	}{
		{"plain", "Argentina", "A", LangES, true},
		{"lowercase answer", "argentina", "A", LangES, true},
		{"accent answer", "Árbol", "A", LangES, true},
		{"accented letter input", "Arbol", "Á", LangES, true},
		{"México for M", "México", "M", LangES, true},
		{"leading spaces", "   ana", "A", LangES, true},
		{"wrong letter", "Brasil", "A", LangES, false},
		{"enye is its own letter in es", "Ñandú", "N", LangES, false},
		{"enye matches enye in es", "Ñandú", "Ñ", LangES, true},
		{"n is not enye in es", "Nube", "Ñ", LangES, false},
		{"enye folds to n in en", "Ñandú", "N", LangEN, true},
		{"umlaut", "Ürsula", "U", LangEN, true},
		{"empty answer", "", "A", LangES, false},
		{"blank answer", "   ", "A", LangES, false},
		{"leading punctuation is not skipped", "¿Ana?", "A", LangES, false},
		{"empty letter", "Ana", "", LangES, false},
		{"NFD accent on the first letter", "A\u0301frica", "A", LangES, true},
		{"NFD accent on the first letter, accented letter", "A\u0301frica", "\u00c1", LangEN, true},
		{"NFC accent on the first letter", "\u00c1frica", "A", LangES, true},
		{"NFD enye is its own letter in es", "N\u0303u", "\u00d1", LangES, true},
		{"NFD enye, NFD letter in es", "N\u0303u", "N\u0303", LangES, true},
		{"NFD enye is not N in es", "N\u0303u", "N", LangES, false},
		{"NFD enye folds to N in en", "N\u0303u", "N", LangEN, true},
		{"NFD enye matches the enye letter in en (both fold to n)", "N\u0303u", "\u00d1", LangEN, true},
		{"NFC enye is not N in es", "\u00d1u", "N", LangES, false},
		{"NFC enye folds to N in en", "\u00d1u", "N", LangEN, true},
		{"stacked marks", "\u1eaeng", "A", LangES, true},
		{"stroked o", "\u00d8l", "O", LangEN, true},
		{"stroked l", "\u0141\u00f3d\u017a", "L", LangEN, true},
		{"leading zero width space", "\u200bPerro", "P", LangES, true},
		{"leading BOM and soft hyphen", "\ufeff\u00adPerro", "P", LangEN, true},
		{"format character in the letter", "Perro", "\u200bP", LangES, true},
		{"only format characters is no answer", "\u200b", "P", LangES, false},
		{"format characters before an enye in es", "\u200bN\u0303u", "\u00d1", LangES, true},
		{"a plain N is not an NFD enye letter in es", "Nube", "N\u0303", LangES, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := StartsWithLetter(tt.answer, tt.letter, tt.lang); got != tt.want {
				t.Errorf("StartsWithLetter(%q, %q, %q) = %v, want %v", tt.answer, tt.letter, tt.lang, got, tt.want)
			}
		})
	}
}
