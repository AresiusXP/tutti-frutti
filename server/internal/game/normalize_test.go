package game

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

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

		// Dutch: the ij ligature (U+0133, capital U+0132) is "ij" in every language.
		{"nl ij ligature", "\u0133sland", LangNL, "ijsland"},
		{"nl capital ij ligature", "\u0132sland", LangNL, "ijsland"},
		{"nl IJ typed as two letters", "IJsland", LangNL, "ijsland"},
		{"nl Ij and iJ typed as two letters", "Ijsland iJsland", LangNL, "ijsland ijsland"},
		{"nl ij ligature inside a word", "W\u0133k aan Zee, Zw\u0133ndrecht", LangNL, "wijk aan zee, zwijndrecht"},
		{"nl capital ij ligature inside a word", "ZW\u0132NDRECHT", LangNL, "zwijndrecht"},
		{"nl ij ligature alone", "\u0133", LangNL, "ij"},
		{"nl capital ij ligature alone", "\u0132", LangNL, "ij"},
		{"nl ij ligatures in a row", "\u0132\u0133", LangNL, "ijij"},
		{"nl ij ligature after a zero width space", "\u200b\u0132sland", LangNL, "ijsland"},
		{"nl ij ligature next to format characters", "\u0132\u200b\u00adsland", LangNL, "ijsland"},
		{"nl ij ligature with a combining mark", "\u0133\u0301sland", LangNL, "ijsland"},
		{"nl ij ligature between spaces", "  \u0132   \u0133  ", LangNL, "ij ij"},
		{"es ij ligature", "\u0132sland", LangES, "ijsland"},
		{"es lower ij ligature", "\u0133sland", LangES, "ijsland"},
		{"en ij ligature", "\u0132sland", LangEN, "ijsland"},
		{"en lower ij ligature", "\u0133sland", LangEN, "ijsland"},
		{"unknown language ij ligature", "\u0132sland", "fr", "ijsland"},
		{"ij ligature next to an enye in es", "\u0133\u00d1", LangES, "ijñ"},
		{"nl y stays y", "Ysland", LangNL, "ysland"},
		{"nl enye folds to n", "\u00d1andú", LangNL, "nandu"},
		{"nl decomposed enye folds to n", "N\u0303andu", LangNL, "nandu"},
		{"nl decomposed enye with an accent folds to n", "n\u0303u\u0301", LangNL, "nu"},
		{"nl acute", "café", LangNL, "cafe"},
		{"nl acute, decomposed", "cafe\u0301", LangNL, "cafe"},
		{"nl grave", "Crème brûlée", LangNL, "creme brulee"},
		{"nl diaeresis on e", "Zoë, Citroën, geërfd", LangNL, "zoe, citroen, geerfd"},
		{"nl diaeresis on e, decomposed", "Zoe\u0308, Citroe\u0308n", LangNL, "zoe, citroen"},
		{"nl diaeresis on i", "Ruïne naïef Oost-Indië", LangNL, "ruine naief oost-indie"},
		{"nl diaeresis on i, decomposed", "Ruı\u0308ne", LangNL, "ruine"},
		{"nl diaeresis on i, decomposed from i", "Rui\u0308ne", LangNL, "ruine"},
		{"nl diaeresis on o and u", "coördinatie Müller", LangNL, "coordinatie muller"},
		{"nl cedilla", "Curaçao Façade", LangNL, "curacao facade"},
		{"nl cedilla, decomposed", "Cura\u0063\u0327ao", LangNL, "curacao"},
		{"nl circumflex", "Fêtes", LangNL, "fetes"},
		{"nl één", "Één", LangNL, "een"},
		{"nl apostrophe is kept", "'s-Hertogenbosch", LangNL, "'s-hertogenbosch"},
		{"nl typographic apostrophe is kept", "\u2019s-Hertogenbosch", LangNL, "\u2019s-hertogenbosch"},
		{"nl spaces and capitals", "  Naam   VAN een  Huisdier ", LangNL, "naam van een huisdier"},
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
		{"Citroën Curaçao Oost-Indië", "citroen curacao oost-indie", "citroen curacao oost-indie"},
		{"coördinatie  Zoë", "coordinatie zoe", "coordinatie zoe"},
	}
	for _, w := range words {
		nfc, nfd := norm.NFC.String(w.word), norm.NFD.String(w.word)
		if nfc == nfd {
			t.Fatalf("setup: %q has no decomposition", w.word)
		}
		for _, tt := range []struct {
			lang Lang
			want string
		}{{LangES, w.es}, {LangEN, w.en}, {LangNL, w.en}} { // nl folds like en: no enye
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
	for _, s := range []string{"México", "Ñandú", "  Árbol  frondoso ", "Straße", "Ắng", "Øl Łódź", "N\u0303u", "\u200bMa\u00adrte \u200b x", "\u0132sland \u0133s", "Zoë Curaçao"} {
		for _, l := range Langs() {
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

		{"nl: ij ligature starts with I", "\u0133sland", "I", LangNL, true},
		{"nl: capital ij ligature starts with I", "\u0132sland", "I", LangNL, true},
		{"nl: IJ typed as two letters starts with I", "IJsland", "I", LangNL, true},
		{"nl: lower-case ij starts with I", "ijsland", "I", LangNL, true},
		{"nl: Ij starts with I", "Ijsvogel", "I", LangNL, true},
		{"nl: ij ligature in lower-case letter", "\u0132sland", "i", LangNL, true},
		{"nl: ij ligature after a zero width space", "\u200b\u0132sland", "I", LangNL, true},
		{"nl: ij ligature with a combining mark", "\u0132\u0301sland", "I", LangNL, true},
		{"nl: ij ligature is not a Y", "\u0132sland", "Y", LangNL, false},
		{"nl: IJ typed as two letters is not a Y", "IJsland", "Y", LangNL, false},
		{"nl: Ysland is not an I", "Ysland", "I", LangNL, false},
		{"nl: Ysland starts with Y", "Ysland", "Y", LangNL, true},
		{"nl: ij ligature is not a J", "\u0132sland", "J", LangNL, false},
		{"es: ij ligature starts with I", "\u0132sland", "I", LangES, true},
		{"en: ij ligature starts with I", "\u0133sland", "I", LangEN, true},
		{"unknown language: ij ligature starts with I", "\u0133sland", "I", "fr", true},
		{"nl: Iets is an I", "Iets", "I", LangNL, true},
		{"nl: enye folds to n", "\u00d1andú", "N", LangNL, true},
		{"nl: NFD enye folds to n", "N\u0303andu", "N", LangNL, true},
		{"nl: acute on the first letter", "\u00c9\u00e9n", "E", LangNL, true},
		{"nl: NFD acute on the first letter", "E\u0301\u00e9n", "E", LangNL, true},
		{"nl: diaeresis on the first letter", "\u00cfsland", "I", LangNL, true},
		{"nl: NFD diaeresis on the first letter", "I\u0308sland", "I", LangNL, true},
		{"nl: cedilla on the first letter", "\u00c7orlu", "C", LangNL, true},
		{"nl: umlaut on the first letter", "\u00dcber", "U", LangNL, true},
		{"nl: o with diaeresis on the first letter", "\u00d6stersund", "O", LangNL, true},
		{"nl: leading apostrophe is not skipped", "'s-Hertogenbosch", "H", LangNL, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := StartsWithLetter(tt.answer, tt.letter, tt.lang); got != tt.want {
				t.Errorf("StartsWithLetter(%q, %q, %q) = %v, want %v", tt.answer, tt.letter, tt.lang, got, tt.want)
			}
		})
	}
}

// A NUL (or any other non-whitespace control character) in a nickname, answer
// or custom category made the room's snapshot unsaveable: PostgreSQL's jsonb
// cannot hold U+0000, so the room was never persisted again.
func TestControlCharactersAreStripped(t *testing.T) {
	for _, r := range []rune{0x00, 0x01, 0x07, 0x08, 0x1b, 0x7f, 0x80, 0x9f} {
		in := "a" + string(r) + "b"
		if got := stripFormat(in); got != "ab" {
			t.Errorf("stripFormat kept U+%04X: %q", r, got)
		}
		if got := cleanText(in); got != "ab" {
			t.Errorf("cleanText kept U+%04X: %q", r, got)
		}
		if got := Normalize(in, LangES); got != "ab" {
			t.Errorf("Normalize kept U+%04X: %q", r, got)
		}
	}
	// whitespace controls still separate words instead of gluing them
	for in, want := range map[string]string{"a\tb": "a b", "a\nb": "a b", "a\r\nb": "a b", "a\x00 b": "a b", "a\u0085b": "a b"} {
		if got := cleanText(in); got != want {
			t.Errorf("cleanText(%q) = %q, want %q", in, got, want)
		}
	}
	if _, err := CleanName("\x00\x01"); !errors.Is(err, ErrInvalidName) {
		t.Errorf("a name of control characters only was accepted: %v", err)
	}
	if got, err := CleanName("Ma\x00ría"); err != nil || got != "María" {
		t.Errorf("CleanName = %q, %v", got, err)
	}
}

func TestPlainField(t *testing.T) {
	for _, ok := range []string{"🍎", "#ff4f81", "banana", "👨\u200d👩\u200d👧", "🏳\ufe0f\u200d🌈", "ñ"} {
		if !plainField(ok) {
			t.Errorf("plainField(%q) = false", ok)
		}
	}
	for _, bad := range []string{"a\x00", "\x00", "\n", "\x7f", "\xff", "a\xc3"} {
		if plainField(bad) {
			t.Errorf("plainField(%q) = true", bad)
		}
	}
}

// Whatever control characters the players type, the marshalled state (what the
// room persists) never holds a NUL.
func TestNoNULEverReachesTheSnapshot(t *testing.T) {
	g := newLobby(t, 2)
	apply(t, g, t0.Add(10*time.Second), "p3", Join{Name: "Ev\x00il\x01", Avatar: Avatar{Emoji: "🍐", Color: "#111"}})
	if got := playerByID(t, g, "p3").Name; got != "Evil" {
		t.Errorf("name = %q", got)
	}
	expectErr(t, g, t0.Add(11*time.Second), "p4", Join{Name: "Bad", Avatar: Avatar{Emoji: "\x00", Color: "#111"}}, ErrInvalidAvatar)
	expectErr(t, g, t0.Add(11*time.Second), "p4", Join{Name: "Bad", Avatar: Avatar{Emoji: "🍐", Color: "x\x00y"}}, ErrInvalidAvatar)
	expectErr(t, g, t0.Add(11*time.Second), "p4", Join{Name: "\x00", Avatar: Avatar{Emoji: "🍐", Color: "#111"}}, ErrInvalidName)

	configure(t, g, func(s *Settings) {
		s.Categories = append(s.Categories[:2], CategoryRef{Custom: "Cosas\x00 raras"})
	})
	apply(t, g, tStart, pid(1), StartGame{})
	if got := g.Settings.Categories[2].Custom; got != "Cosas raras" {
		t.Errorf("custom label = %q", got)
	}
	b, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `\u0000`) || strings.ContainsRune(string(b), 0) {
		t.Errorf("the marshalled game holds a NUL: %s", b)
	}
}

func TestAnswersDropControlCharacters(t *testing.T) {
	g := newPlaying(t, 2, nil)
	letter := string(g.Round.Letter)
	apply(t, g, tStart.Add(time.Second), pid(1), SetAnswer{Category: 0, Text: letter + "a\x00b\x07"})
	if got := g.Round.Answers[pid(1)][0]; got != letter+"ab" {
		t.Errorf("stored answer = %q", got)
	}
	b, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `\u0000`) {
		t.Errorf("the marshalled game holds a NUL")
	}
}

// A nickname like "Juan 👨‍💻" must keep its emoji sequence in one piece, while
// a joiner anywhere else is as invisible as the other format characters.
func TestCleanTextKeepsEmojiJoiners(t *testing.T) {
	keep := []string{
		"Juan 👨\u200d💻",          // technologist
		"👩\u200d🚀",               // astronaut
		"👨\u200d👩\u200d👧\u200d👦", // family
		"🏳\ufe0f\u200d🌈",         // rainbow flag: variation selector before the joiner
		"👩🏽\u200d⚕\ufe0f",        // skin tone modifier before the joiner, medical sign after
		"❤\ufe0f\u200d🔥",         // heart on fire
	}
	for _, in := range keep {
		if got := cleanText(in); got != in {
			t.Errorf("cleanText(%q) = %q, want it untouched", in, got)
		}
		if got, err := CleanName(in); err != nil || got != in {
			t.Errorf("CleanName(%q) = %q, %v", in, got, err)
		}
	}
	drop := map[string]string{
		"a\u200db":             "ab",  // between letters
		"\u200d👨":              "👨",   // leading
		"👨\u200d":              "👨",   // trailing
		"👨\u200d\u200d💻":       "👨💻",  // doubled: neither joins
		"👨 \u200d 💻":           "👨 💻", // separated by spaces
		"\u200b👨\u200d💻\u200b": "👨\u200d💻",
		"1\u200d2":             "12",
	}
	for in, want := range drop {
		if got := cleanText(in); got != want {
			t.Errorf("cleanText(%q) = %q, want %q", in, got, want)
		}
	}
	// Answers and comparison keep treating every format character as invisible.
	if got := stripFormat("👨\u200d💻"); got != "👨💻" {
		t.Errorf("stripFormat = %q", got)
	}
	if Normalize("👨\u200d💻", LangES) != Normalize("👨💻", LangES) {
		t.Error("Normalize must ignore the joiner")
	}
}
