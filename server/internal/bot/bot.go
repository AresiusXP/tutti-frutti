// Package bot supplies independent, curated answers for the six classic
// categories in Spanish, English and Dutch. It is a pure data source: callers
// supply variants; no clock, randomness, human answers or external service is
// consulted. Each language covers common letters broadly, usually with two
// choices. Coverage is deliberately incomplete, especially for country/color
// and hard letters: an absent entry means a blank answer, never an invented word.
// Spanish Ñ is separate from N; Dutch IJ words belong to I, as in game.Normalize.
package bot

import (
	"strings"

	"github.com/AresiusXP/tutti-frutti/server/internal/catalog"
	"github.com/AresiusXP/tutti-frutti/server/internal/game"
)

// Supported reports whether valid game settings select three to six distinct
// classic categories and a positive round time limit. Hard letters are allowed;
// a supported game need not have an answer for every category/letter pair.
func Supported(settings game.Settings) bool {
	if settings.TimeLimitSeconds <= 0 || len(settings.Categories) > 6 || settings.Validate() != nil {
		return false
	}
	for _, ref := range settings.Categories {
		c, ok := catalog.ByID(ref.ID)
		if !ok || !c.Default || ref.Custom != "" {
			return false
		}
	}
	return true
}

// lexicon keys are uppercase round letters; alternatives are display spellings.
type lexicon map[string][]string

var corpus = map[game.Lang]map[string]lexicon{
	game.LangES: {"nombre": namesES, "animal": animalsES, "pais": countriesES, "color": colorsES, "comida": foodsES, "marca": brands},
	game.LangEN: {"nombre": namesEN, "animal": animalsEN, "pais": countriesEN, "color": colorsEN, "comida": foodsEN, "marca": brands},
	game.LangNL: {"nombre": namesNL, "animal": animalsNL, "pais": countriesNL, "color": colorsNL, "comida": foodsNL, "marca": brands},
}

// Answer returns a deterministic alternative, or blank for an unknown language,
// category, letter or missing entry. Variants cycle through the curated choices.
// Letter normalization is the engine's: accented/lowercase letters are accepted.
func Answer(lang game.Lang, categoryID string, letter string, variant uint64) string {
	choices := corpus[lang][categoryID][strings.ToUpper(game.Normalize(letter, lang))]
	if len(choices) == 0 {
		return ""
	}
	return choices[variant%uint64(len(choices))]
}

// Identity returns an ordinary nickname and a supported web avatar. Consecutive
// variants have distinct names for a full 24-name cycle (more than one lobby).
// Unknown languages use the English name pool.
func Identity(lang game.Lang, variant uint64) (name string, avatar game.Avatar) {
	names := identityNames[lang]
	if len(names) == 0 {
		names = identityNames[game.LangEN]
	}
	return names[variant%uint64(len(names))], game.Avatar{
		Emoji: avatarEmojis[variant%uint64(len(avatarEmojis))],
		Color: avatarColors[(variant/uint64(len(avatarEmojis))+variant%uint64(len(avatarColors)))%uint64(len(avatarColors))],
	}
}

// Keep these in sync with web/src/lib/avatar.ts.
var avatarEmojis = [...]string{"🍌", "🍊", "🍓", "🍇", "🍉", "🍍"}
var avatarColors = [...]string{"banana", "orange", "strawberry", "lime", "grape", "blueberry"}

var identityNames = map[game.Lang][]string{
	game.LangES: {"Ana", "Bruno", "Carla", "Diego", "Elena", "Fede", "Gabriela", "Hugo", "Inés", "Julián", "Lara", "Mateo", "Nora", "Olivia", "Pablo", "Rocío", "Sofía", "Tomás", "Valeria", "Lucas", "Martina", "Nicolás", "Camila", "Andrés"},
	game.LangEN: {"Alice", "Ben", "Chloe", "Daniel", "Emma", "Finn", "Grace", "Henry", "Isabel", "Jack", "Kate", "Liam", "Mia", "Noah", "Olivia", "Peter", "Ruby", "Sam", "Theo", "Violet", "Will", "Zoe", "Lucy", "Oscar"},
	game.LangNL: {"Anna", "Bram", "Charlotte", "Daan", "Eva", "Finn", "Guus", "Hanne", "Isa", "Joris", "Koen", "Lotte", "Milan", "Noor", "Olivia", "Pieter", "Roos", "Sanne", "Thijs", "Vera", "Willem", "Yara", "Sofie", "Lars"},
}

// International brand names retain their spelling in every lobby language.
var brands = lexicon{
	"A": {"Adidas", "Apple"},
	"B": {"Bosch", "BMW"},
	"C": {"Canon", "Coca-Cola"},
	"D": {"Dell", "Disney"},
	"E": {"Epson", "Electrolux"},
	"F": {"Ford", "Fila"},
	"G": {"Google", "Gucci"},
	"H": {"Honda", "Hyundai"},
	"I": {"IKEA", "Intel"},
	"J": {"Jeep", "JBL"},
	"K": {"Kia", "Kodak"},
	"L": {"Lego", "Lenovo"},
	"M": {"Motorola", "Microsoft"},
	"N": {"Nike", "Nintendo"},
	"O": {"Oreo", "Opel"},
	"P": {"Puma", "Panasonic"},
	"Q": {"Quaker", "Quiksilver"},
	"R": {"Renault", "Reebok"},
	"S": {"Samsung", "Sony"},
	"T": {"Toyota", "Toshiba"},
	"U": {"Uniqlo", "Umbro"},
	"V": {"Volvo", "Vans"},
	"W": {"Whirlpool", "Wilson"},
	"X": {"Xiaomi", "Xerox"},
	"Y": {"Yamaha", "Yonex"},
	"Z": {"Zara", "Zippo"},
}
