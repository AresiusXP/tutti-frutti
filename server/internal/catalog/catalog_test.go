package catalog

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

var wantGroups = []struct {
	name  string
	count int
}{
	{"personas", 6},
	{"lugares", 8},
	{"naturaleza", 4},
	{"comida", 5},
	{"cosas", 7},
	{"marcas", 2},
	{"cultura", 6},
	{"trabajo", 4},
	{"divertidas", 8},
}

// golden is the catalog as written in DESIGN.md section 4 (the table with
// "ES / EN" labels and ★ for the defaults, plus the Dutch label of each entry),
// in display order. The ids are the stable keys chosen for the catalog. Keep it
// in sync with the design doc.
var golden = []struct {
	id, group, es, en, nl string
	def                   bool
}{
	// personas (6)
	{"nombre", "personas", "Nombre", "Name", "Naam", true},
	{"apellido", "personas", "Apellido", "Last name", "Achternaam", false},
	{"famoso", "personas", "Famoso/a", "Celebrity", "Bekende persoon", false},
	{"personaje_historico", "personas", "Personaje histórico", "Historical figure", "Historisch figuur", false},
	{"futbolista", "personas", "Futbolista", "Football player", "Voetballer", false},
	{"cantante_banda", "personas", "Cantante o banda", "Singer or band", "Zanger of band", false},
	// lugares (8)
	{"pais", "lugares", "País", "Country", "Land", true},
	{"ciudad", "lugares", "Ciudad", "City", "Stad", false},
	{"capital", "lugares", "Capital", "Capital city", "Hoofdstad", false},
	{"provincia_estado", "lugares", "Provincia o estado", "Province or state", "Provincie of staat", false},
	{"rio_lago_mar", "lugares", "Río, lago o mar", "River, lake or sea", "Rivier, meer of zee", false},
	{"lugar_turistico", "lugares", "Lugar turístico", "Tourist attraction", "Toeristische attractie", false},
	{"nacionalidad", "lugares", "Nacionalidad", "Nationality", "Nationaliteit", false},
	{"club_futbol", "lugares", "Club de fútbol", "Football club", "Voetbalclub", false},
	// naturaleza (4)
	{"animal", "naturaleza", "Animal", "Animal", "Dier", true},
	{"ave", "naturaleza", "Ave", "Bird", "Vogel", false},
	{"animal_marino", "naturaleza", "Animal marino", "Sea creature", "Zeedier", false},
	{"flor_planta", "naturaleza", "Flor o planta", "Flower or plant", "Bloem of plant", false},
	// comida (5)
	{"comida", "comida", "Comida", "Food", "Eten", true},
	{"fruta_verdura", "comida", "Fruta o verdura", "Fruit or vegetable", "Fruit of groente", false},
	{"postre", "comida", "Postre", "Dessert", "Nagerecht", false},
	{"bebida", "comida", "Bebida", "Drink", "Drank", false},
	{"algo_heladera", "comida", "Algo que hay en la heladera", "Something in the fridge", "Iets in de koelkast", false},
	// cosas (7): the star is on Color, not on Cosa
	{"cosa", "cosas", "Cosa", "Thing", "Ding", false},
	{"color", "cosas", "Color", "Color", "Kleur", true},
	{"ropa_accesorio", "cosas", "Ropa o accesorio", "Clothing or accessory", "Kleding of accessoire", false},
	{"mueble", "cosas", "Mueble", "Furniture", "Meubelstuk", false},
	{"util_escolar", "cosas", "Útil escolar", "School supply", "Schoolbenodigdheid", false},
	{"juguete", "cosas", "Juguete", "Toy", "Speelgoed", false},
	{"transporte", "cosas", "Medio de transporte", "Vehicle", "Vervoermiddel", false},
	// marcas (2)
	{"marca", "marcas", "Marca", "Brand", "Merk", true},
	{"marca_auto", "marcas", "Marca de auto", "Car brand", "Automerk", false},
	// cultura (6)
	{"pelicula", "cultura", "Película", "Movie", "Film", false},
	{"serie", "cultura", "Serie", "TV show", "Tv-serie", false},
	{"dibujo_animado", "cultura", "Dibujo animado", "Cartoon", "Tekenfilm", false},
	{"personaje_ficcion", "cultura", "Personaje de ficción", "Fictional character", "Fictief personage", false},
	{"superheroe_villano", "cultura", "Superhéroe o villano", "Superhero or villain", "Superheld of schurk", false},
	{"videojuego", "cultura", "Videojuego", "Video game", "Videogame", false},
	// trabajo (4)
	{"profesion", "trabajo", "Profesión u oficio", "Job", "Beroep", false},
	{"deporte", "trabajo", "Deporte", "Sport", "Sport", false},
	{"instrumento", "trabajo", "Instrumento musical", "Musical instrument", "Muziekinstrument", false},
	{"parte_cuerpo", "trabajo", "Parte del cuerpo", "Body part", "Lichaamsdeel", false},
	// divertidas (8)
	{"adjetivo", "divertidas", "Adjetivo", "Adjective", "Bijvoeglijk naamwoord", false},
	{"verbo", "divertidas", "Verbo", "Verb", "Werkwoord", false},
	{"idioma", "divertidas", "Idioma", "Language", "Taal", false},
	{"emocion", "divertidas", "Emoción", "Emotion", "Emotie", false},
	{"mascota", "divertidas", "Nombre de mascota", "Pet name", "Naam van een huisdier", false},
	{"excusa_tarde", "divertidas", "Excusa para llegar tarde", "Excuse for being late", "Smoes om te laat te komen", false},
	{"algo_playa", "divertidas", "Algo que llevás a la playa", "Something you take to the beach", "Iets wat je meeneemt naar het strand", false},
	{"algo_miedo", "divertidas", "Algo que da miedo", "Something scary", "Iets engs", false},
}

func TestCatalogMatchesDesignTable(t *testing.T) {
	all := All()
	if len(golden) != 50 || len(all) != 50 {
		t.Fatalf("golden has %d entries, catalog has %d, want 50 each", len(golden), len(all))
	}
	for i, want := range golden {
		got := all[i]
		if got.ID != want.id || got.Group != want.group || got.ES != want.es || got.EN != want.en || got.NL != want.nl || got.Default != want.def {
			t.Errorf("entry %d:\n got  {%q %q %q %q %q default=%v}\n want {%q %q %q %q %q default=%v}",
				i, got.ID, got.Group, got.ES, got.EN, got.NL, got.Default, want.id, want.group, want.es, want.en, want.nl, want.def)
		}
	}
}

func TestCatalogShape(t *testing.T) {
	all := All()
	if len(all) != 50 {
		t.Fatalf("len(All()) = %d, want 50", len(all))
	}

	ids := map[string]bool{}
	for _, c := range all {
		if c.ID == "" || c.ES == "" || c.EN == "" || c.NL == "" || c.Group == "" {
			t.Errorf("entry %+v has an empty field", c)
		}
		if ids[c.ID] {
			t.Errorf("duplicate id %q", c.ID)
		}
		ids[c.ID] = true
		for _, r := range c.ID {
			if !(r >= 'a' && r <= 'z') && r != '_' {
				t.Errorf("id %q must be lowercase ASCII (a-z and _)", c.ID)
				break
			}
		}
		for _, label := range []string{c.ES, c.EN, c.NL} {
			if n := utf8.RuneCountInString(label); n > 40 {
				t.Errorf("label %q is %d runes, want <= 40", label, n)
			}
		}
	}
}

func TestDefaults(t *testing.T) {
	want := []string{"nombre", "animal", "pais", "color", "comida", "marca"}
	var got []string
	for _, c := range Defaults() {
		got = append(got, c.ID)
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("defaults = %v, want %v", got, want)
	}
	n := 0
	for _, c := range All() {
		if c.Default {
			n++
		}
	}
	if n != 6 {
		t.Fatalf("%d entries flagged default, want 6", n)
	}
}

func TestGroups(t *testing.T) {
	groups := Groups()
	if len(groups) != len(wantGroups) {
		t.Fatalf("Groups() = %v, want %d groups", groups, len(wantGroups))
	}
	counts := map[string]int{}
	for _, c := range All() {
		counts[c.Group]++
	}
	for i, w := range wantGroups {
		if groups[i] != w.name {
			t.Errorf("Groups()[%d] = %q, want %q", i, groups[i], w.name)
		}
		if counts[w.name] != w.count {
			t.Errorf("group %q has %d entries, want %d", w.name, counts[w.name], w.count)
		}
	}
}

func TestGroupsAreContiguousInFileOrder(t *testing.T) {
	var seen []string
	for _, c := range All() {
		if len(seen) == 0 || seen[len(seen)-1] != c.Group {
			if slices.Contains(seen, c.Group) {
				t.Fatalf("group %q appears in two separate blocks", c.Group)
			}
			seen = append(seen, c.Group)
		}
	}
}

func TestByIDAndLabel(t *testing.T) {
	for _, want := range golden {
		c, ok := ByID(want.id)
		if !ok {
			t.Errorf("ByID(%q) not found", want.id)
			continue
		}
		if c.ID != want.id || c.Group != want.group || c.Default != want.def {
			t.Errorf("ByID(%q) = %+v", want.id, c)
		}
		for lang, label := range map[string]string{"es": want.es, "en": want.en, "nl": want.nl, "fr": want.es, "": want.es, "NL": want.es, "nl ": want.es} {
			if got := Label(c, lang); got != label {
				t.Errorf("Label(%q, %q) = %q, want %q", want.id, lang, got, label)
			}
		}
	}
	if c, ok := ByID("nope"); ok || c != (Category{}) {
		t.Errorf("ByID(nope) = %+v, %v", c, ok)
	}
	if _, ok := ByID(""); ok {
		t.Error("ByID(\"\") must not match")
	}
}

func TestAllReturnsCopy(t *testing.T) {
	a := All()
	a[0].ES = "mutated"
	if All()[0].ES == "mutated" {
		t.Fatal("All() exposes internal state")
	}
	g := Groups()
	g[0] = "mutated"
	if Groups()[0] == "mutated" {
		t.Fatal("Groups() exposes internal state")
	}
}

func TestForLang(t *testing.T) {
	for _, tt := range []struct {
		in, lang string
		label    func(es, en, nl string) string
	}{
		{"es", "es", func(es, _, _ string) string { return es }},
		{"en", "en", func(_, en, _ string) string { return en }},
		{"nl", "nl", func(_, _, nl string) string { return nl }},
		{"zz", "es", func(es, _, _ string) string { return es }},
		{"", "es", func(es, _, _ string) string { return es }},
		{"NL", "es", func(es, _, _ string) string { return es }},
	} {
		l := ForLang(tt.in)
		if l.Lang != tt.lang {
			t.Errorf("ForLang(%q).Lang = %q, want %q", tt.in, l.Lang, tt.lang)
		}
		if len(l.Categories) != 50 || len(l.Groups) != 9 {
			t.Fatalf("ForLang(%q): %d categories / %d groups", tt.in, len(l.Categories), len(l.Groups))
		}
		for i, want := range golden {
			got := l.Categories[i]
			wantEntry := LocalizedCategory{ID: want.id, Group: want.group, Label: tt.label(want.es, want.en, want.nl), Default: want.def}
			if got != wantEntry {
				t.Errorf("ForLang(%q) entry %d = %+v, want %+v", tt.in, i, got, wantEntry)
			}
		}
	}

	b, err := json.Marshal(ForLang("en"))
	if err != nil {
		t.Fatal(err)
	}
	var back Listing
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(back.Groups, Groups()) || len(back.Categories) != 50 || back.Lang != "en" {
		t.Fatalf("JSON round trip lost data: %+v", back)
	}
	if !strings.Contains(string(b), `{"id":"nombre","group":"personas","label":"Name","default":true}`) {
		t.Errorf("unexpected JSON shape: %.200s", b)
	}

	// The Dutch listing as the web client receives it.
	b, err = json.Marshal(ForLang("nl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(b), `{"lang":"nl","groups":["personas","lugares",`) ||
		!strings.Contains(string(b), `{"id":"pais","group":"lugares","label":"Land","default":true}`) {
		t.Errorf("unexpected Dutch JSON: %.300s", b)
	}
}

// Every built-in label, in every language, must name exactly one entry once
// normalized the way the duplicate check does: otherwise a custom category
// could be refused with the label of the wrong built-in.
func TestLabelsAreUnambiguousAcrossLanguages(t *testing.T) {
	seen := map[string]string{}
	for _, c := range All() {
		for _, label := range []string{c.ES, c.EN, c.NL} {
			key := strings.ToLower(label)
			if owner, ok := seen[key]; ok && owner != c.ID {
				t.Errorf("label %q is used by %q and %q", label, owner, c.ID)
			}
			seen[key] = c.ID
		}
	}
}

func TestMustLoadRejectsBadCatalogs(t *testing.T) {
	const ok = `{"id":"a","group":"g","es":"A","en":"B","nl":"C","default":false}`
	if got := mustLoad([]byte("[" + ok + "]")); len(got.all) != 1 || got.all[0].NL != "C" {
		t.Fatalf("a valid catalog was not loaded: %+v", got.all)
	}
	for name, doc := range map[string]string{
		"invalid JSON":        "{",
		"not an array":        ok,
		"unknown field":       `[{"id":"a","group":"g","es":"A","en":"B","nl":"C","fr":"D","default":false}]`,
		"data after the list": "[" + ok + "] []",
		"missing id":          `[{"group":"g","es":"A","en":"B","nl":"C"}]`,
		"missing group":       `[{"id":"a","es":"A","en":"B","nl":"C"}]`,
		"missing es":          `[{"id":"a","group":"g","en":"B","nl":"C"}]`,
		"missing en":          `[{"id":"a","group":"g","es":"A","nl":"C"}]`,
		"missing nl":          `[{"id":"a","group":"g","es":"A","en":"B"}]`,
		"empty nl":            `[{"id":"a","group":"g","es":"A","en":"B","nl":""}]`,
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("mustLoad did not panic")
				}
			}()
			mustLoad([]byte(doc))
		})
	}
}
