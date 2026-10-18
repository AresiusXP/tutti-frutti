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
// "ES / EN" labels and ★ for the defaults), in display order. The ids are the
// stable keys chosen for the catalog. Keep it in sync with the design doc.
var golden = []struct {
	id, group, es, en string
	def               bool
}{
	// personas (6)
	{"nombre", "personas", "Nombre", "Name", true},
	{"apellido", "personas", "Apellido", "Last name", false},
	{"famoso", "personas", "Famoso/a", "Celebrity", false},
	{"personaje_historico", "personas", "Personaje histórico", "Historical figure", false},
	{"futbolista", "personas", "Futbolista", "Football player", false},
	{"cantante_banda", "personas", "Cantante o banda", "Singer or band", false},
	// lugares (8)
	{"pais", "lugares", "País", "Country", true},
	{"ciudad", "lugares", "Ciudad", "City", false},
	{"capital", "lugares", "Capital", "Capital city", false},
	{"provincia_estado", "lugares", "Provincia o estado", "Province or state", false},
	{"rio_lago_mar", "lugares", "Río, lago o mar", "River, lake or sea", false},
	{"lugar_turistico", "lugares", "Lugar turístico", "Tourist attraction", false},
	{"nacionalidad", "lugares", "Nacionalidad", "Nationality", false},
	{"club_futbol", "lugares", "Club de fútbol", "Football club", false},
	// naturaleza (4)
	{"animal", "naturaleza", "Animal", "Animal", true},
	{"ave", "naturaleza", "Ave", "Bird", false},
	{"animal_marino", "naturaleza", "Animal marino", "Sea creature", false},
	{"flor_planta", "naturaleza", "Flor o planta", "Flower or plant", false},
	// comida (5)
	{"comida", "comida", "Comida", "Food", true},
	{"fruta_verdura", "comida", "Fruta o verdura", "Fruit or vegetable", false},
	{"postre", "comida", "Postre", "Dessert", false},
	{"bebida", "comida", "Bebida", "Drink", false},
	{"algo_heladera", "comida", "Algo que hay en la heladera", "Something in the fridge", false},
	// cosas (7): the star is on Color, not on Cosa
	{"cosa", "cosas", "Cosa", "Thing", false},
	{"color", "cosas", "Color", "Color", true},
	{"ropa_accesorio", "cosas", "Ropa o accesorio", "Clothing or accessory", false},
	{"mueble", "cosas", "Mueble", "Furniture", false},
	{"util_escolar", "cosas", "Útil escolar", "School supply", false},
	{"juguete", "cosas", "Juguete", "Toy", false},
	{"transporte", "cosas", "Medio de transporte", "Vehicle", false},
	// marcas (2)
	{"marca", "marcas", "Marca", "Brand", true},
	{"marca_auto", "marcas", "Marca de auto", "Car brand", false},
	// cultura (6)
	{"pelicula", "cultura", "Película", "Movie", false},
	{"serie", "cultura", "Serie", "TV show", false},
	{"dibujo_animado", "cultura", "Dibujo animado", "Cartoon", false},
	{"personaje_ficcion", "cultura", "Personaje de ficción", "Fictional character", false},
	{"superheroe_villano", "cultura", "Superhéroe o villano", "Superhero or villain", false},
	{"videojuego", "cultura", "Videojuego", "Video game", false},
	// trabajo (4)
	{"profesion", "trabajo", "Profesión u oficio", "Job", false},
	{"deporte", "trabajo", "Deporte", "Sport", false},
	{"instrumento", "trabajo", "Instrumento musical", "Musical instrument", false},
	{"parte_cuerpo", "trabajo", "Parte del cuerpo", "Body part", false},
	// divertidas (8)
	{"adjetivo", "divertidas", "Adjetivo", "Adjective", false},
	{"verbo", "divertidas", "Verbo", "Verb", false},
	{"idioma", "divertidas", "Idioma", "Language", false},
	{"emocion", "divertidas", "Emoción", "Emotion", false},
	{"mascota", "divertidas", "Nombre de mascota", "Pet name", false},
	{"excusa_tarde", "divertidas", "Excusa para llegar tarde", "Excuse for being late", false},
	{"algo_playa", "divertidas", "Algo que llevás a la playa", "Something you take to the beach", false},
	{"algo_miedo", "divertidas", "Algo que da miedo", "Something scary", false},
}

func TestCatalogMatchesDesignTable(t *testing.T) {
	all := All()
	if len(golden) != 50 || len(all) != 50 {
		t.Fatalf("golden has %d entries, catalog has %d, want 50 each", len(golden), len(all))
	}
	for i, want := range golden {
		got := all[i]
		if got.ID != want.id || got.Group != want.group || got.ES != want.es || got.EN != want.en || got.Default != want.def {
			t.Errorf("entry %d:\n got  {%q %q %q %q default=%v}\n want {%q %q %q %q default=%v}",
				i, got.ID, got.Group, got.ES, got.EN, got.Default, want.id, want.group, want.es, want.en, want.def)
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
		if c.ID == "" || c.ES == "" || c.EN == "" || c.Group == "" {
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
		for _, label := range []string{c.ES, c.EN} {
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
		for lang, label := range map[string]string{"es": want.es, "en": want.en, "fr": want.es, "": want.es} {
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
		label    func(es, en string) string
	}{
		{"es", "es", func(es, _ string) string { return es }},
		{"en", "en", func(_, en string) string { return en }},
		{"zz", "es", func(es, _ string) string { return es }},
		{"", "es", func(es, _ string) string { return es }},
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
			wantEntry := LocalizedCategory{ID: want.id, Group: want.group, Label: tt.label(want.es, want.en), Default: want.def}
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
}

func TestMustLoadPanicsOnBadJSON(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("mustLoad did not panic on invalid JSON")
		}
	}()
	mustLoad([]byte("{"))
}
