package game

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/AresiusXP/tutti-frutti/server/internal/catalog"
)

func ids(refs ...string) []CategoryRef {
	out := make([]CategoryRef, len(refs))
	for i, r := range refs {
		out[i] = CategoryRef{ID: r}
	}
	return out
}

func customs(labels ...string) []CategoryRef {
	out := make([]CategoryRef, len(labels))
	for i, l := range labels {
		out[i] = CategoryRef{Custom: l}
	}
	return out
}

func TestDefaultSettings(t *testing.T) {
	s := DefaultSettings()
	if s.Lang != LangES || !s.ExcludeHardLetters || s.Rounds != 5 || s.TimeLimitSeconds != 0 {
		t.Errorf("unexpected defaults: %+v", s)
	}
	var want []CategoryRef
	for _, c := range catalog.Defaults() {
		want = append(want, CategoryRef{ID: c.ID})
	}
	if len(want) != 6 || !reflect.DeepEqual(s.Categories, want) {
		t.Errorf("categories = %v, want the 6 catalog defaults %v", s.Categories, want)
	}
	if err := s.Validate(); err != nil {
		t.Errorf("default settings are invalid: %v", err)
	}
	if got := s.Labels(); !slices.Equal(got, []string{"Nombre", "País", "Animal", "Comida", "Color", "Marca"}) {
		t.Errorf("Labels() = %v", got)
	}
}

func TestSettingsValidate(t *testing.T) {
	with := func(mod func(*Settings)) Settings {
		s := DefaultSettings()
		mod(&s)
		return s
	}
	tests := []struct {
		name string
		s    Settings
		ok   bool
	}{
		{"defaults", DefaultSettings(), true},
		{"english lobby", with(func(s *Settings) { s.Lang = LangEN }), true},
		{"unknown language", with(func(s *Settings) { s.Lang = "fr" }), false},
		{"empty language", with(func(s *Settings) { s.Lang = "" }), false},
		{"time limit 60", with(func(s *Settings) { s.TimeLimitSeconds = 60 }), true},
		{"time limit 90", with(func(s *Settings) { s.TimeLimitSeconds = 90 }), true},
		{"time limit 120", with(func(s *Settings) { s.TimeLimitSeconds = 120 }), true},
		{"time limit 180", with(func(s *Settings) { s.TimeLimitSeconds = 180 }), true},
		{"time limit 45", with(func(s *Settings) { s.TimeLimitSeconds = 45 }), false},
		{"negative time limit", with(func(s *Settings) { s.TimeLimitSeconds = -60 }), false},
		{"unlimited rounds", with(func(s *Settings) { s.Rounds = 0 }), true},
		{"one round", with(func(s *Settings) { s.Rounds = 1 }), true},
		{"max rounds es without hard letters", with(func(s *Settings) { s.Rounds = 20 }), true},
		{"too many rounds es without hard letters", with(func(s *Settings) { s.Rounds = 21 }), false},
		{"max rounds es with hard letters", with(func(s *Settings) { s.ExcludeHardLetters = false; s.Rounds = 27 }), true},
		{"too many rounds es with hard letters", with(func(s *Settings) { s.ExcludeHardLetters = false; s.Rounds = 28 }), false},
		{"max rounds en with hard letters", with(func(s *Settings) { s.Lang = LangEN; s.ExcludeHardLetters = false; s.Rounds = 26 }), true},
		{"too many rounds en with hard letters", with(func(s *Settings) { s.Lang = LangEN; s.ExcludeHardLetters = false; s.Rounds = 27 }), false},
		{"too many rounds en without hard letters", with(func(s *Settings) { s.Lang = LangEN; s.Rounds = 21 }), false},
		{"negative rounds", with(func(s *Settings) { s.Rounds = -1 }), false},
		{"3 categories", with(func(s *Settings) { s.Categories = ids("nombre", "pais", "animal") }), true},
		{"2 categories", with(func(s *Settings) { s.Categories = ids("nombre", "pais") }), false},
		{"no categories", with(func(s *Settings) { s.Categories = nil }), false},
		{"10 categories", with(func(s *Settings) {
			s.Categories = ids("nombre", "pais", "animal", "comida", "color", "marca", "ciudad", "ave", "postre", "bebida")
		}), true},
		{"11 categories", with(func(s *Settings) {
			s.Categories = ids("nombre", "pais", "animal", "comida", "color", "marca", "ciudad", "ave", "postre", "bebida", "verbo")
		}), false},
		{"unknown id", with(func(s *Settings) { s.Categories = ids("nombre", "pais", "nope") }), false},
		{"id and custom both set", with(func(s *Settings) { s.Categories[0] = CategoryRef{ID: "nombre", Custom: "Algo"} }), false},
		{"neither id nor custom", with(func(s *Settings) { s.Categories[0] = CategoryRef{} }), false},
		{"valid custom", with(func(s *Settings) { s.Categories[0] = CategoryRef{Custom: "Cosas de la cocina"} }), true},
		{"blank custom", with(func(s *Settings) { s.Categories[0] = CategoryRef{Custom: "   "} }), false},
		{"custom of only invisible characters", with(func(s *Settings) { s.Categories[0] = CategoryRef{Custom: "\u200b\u200c \ufeff"} }), false},
		{"custom padded with invisible characters fits after cleaning", with(func(s *Settings) {
			s.Categories[0] = CategoryRef{Custom: strings.Repeat("a\u200b", 24)}
		}), true},
		{"custom of 24 runes", with(func(s *Settings) { s.Categories[0] = CategoryRef{Custom: strings.Repeat("ñ", 24)} }), true},
		{"custom of 25 runes", with(func(s *Settings) { s.Categories[0] = CategoryRef{Custom: strings.Repeat("ñ", 25)} }), false},
		{"custom padded with spaces fits after trimming", with(func(s *Settings) {
			s.Categories[0] = CategoryRef{Custom: "  " + strings.Repeat("a", 24) + "  "}
		}), true},
		{"duplicate customs", with(func(s *Settings) {
			s.Categories = append(customs("Cosas rojas", "cosas   ROJAS"), s.Categories[:1]...)
		}), false},
		{"customs that differ", with(func(s *Settings) {
			s.Categories = append(customs("Cosas rojas", "Cosas azules"), s.Categories[:1]...)
		}), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.s.Validate()
			if tt.ok && err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
			if !tt.ok {
				if err == nil {
					t.Fatal("Validate() = nil, want an error")
				}
				if !errors.Is(err, ErrInvalidSettings) {
					t.Errorf("error %v does not wrap ErrInvalidSettings", err)
				}
			}
		})
	}
}

func TestSettingsDuplicateCategoryError(t *testing.T) {
	tests := []struct {
		name      string
		lang      Lang
		cats      []CategoryRef
		wantLabel string
	}{
		{"duplicate id", LangES, ids("nombre", "pais", "pais"), "País"},
		{"duplicate id in english", LangEN, ids("nombre", "pais", "pais"), "Country"},
		{"custom equals built-in es label", LangES, append(ids("nombre", "animal"), CategoryRef{Custom: "país"}), "País"},
		{"custom equals built-in without accent", LangES, append(ids("nombre", "animal"), CategoryRef{Custom: "PAIS"}), "País"},
		{"custom equals built-in with invisible characters", LangES, append(ids("nombre", "animal"), CategoryRef{Custom: "Pa\u00ed\u00ads\u200b"}), "País"},
		{"custom equals other custom with invisible characters", LangES, append(ids("nombre", "animal"), CategoryRef{Custom: "Mi cosa"}, CategoryRef{Custom: "Mi\u200b cosa\u200d"}), "Mi cosa"},
		{"custom equals built-in in NFD", LangES, append(ids("nombre", "animal"), CategoryRef{Custom: "Pai\u0301s"}), "País"},
		{"custom equals built-in en label in es lobby", LangES, append(ids("nombre", "animal"), CategoryRef{Custom: "Country"}), "País"},
		{"custom equals built-in es label in en lobby", LangEN, append(ids("nombre", "animal"), CategoryRef{Custom: "Pais"}), "Country"},
		{"custom equals built-in that is not selected", LangES, append(ids("nombre", "animal"), CategoryRef{Custom: "algo que da miedo"}), "Algo que da miedo"},
		{"custom equals other custom", LangES, append(ids("nombre", "animal"), CategoryRef{Custom: "Mi cosa"}, CategoryRef{Custom: "  mi   COSA "}), "Mi cosa"},
		{"custom equals other custom ignoring accents", LangEN, append(ids("nombre", "animal"), CategoryRef{Custom: "Café"}, CategoryRef{Custom: "cafe"}), "Café"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := DefaultSettings()
			s.Lang, s.Categories = tt.lang, tt.cats
			err := s.Validate()
			var dup *DuplicateCategoryError
			if !errors.As(err, &dup) {
				t.Fatalf("Validate() = %v, want *DuplicateCategoryError", err)
			}
			if dup.Label != tt.wantLabel {
				t.Errorf("conflicting label = %q, want %q", dup.Label, tt.wantLabel)
			}
			if !errors.Is(err, ErrInvalidSettings) {
				t.Error("DuplicateCategoryError must wrap ErrInvalidSettings")
			}
			if !strings.Contains(err.Error(), tt.wantLabel) {
				t.Errorf("message %q must mention %q", err.Error(), tt.wantLabel)
			}
		})
	}
}

func TestSettingsCleanAndLabels(t *testing.T) {
	s := DefaultSettings()
	s.Lang = LangEN
	s.Categories = []CategoryRef{{ID: "pais"}, {Custom: "  Cosas   rojas "}, {ID: "ghost"}, {Custom: "\u200bCo\u00adsas\u200d azules"}}
	c := s.Clean()
	if c.Categories[1].Custom != "Cosas rojas" {
		t.Errorf("Clean did not collapse spaces: %q", c.Categories[1].Custom)
	}
	if c.Categories[3].Custom != "Cosas azules" {
		t.Errorf("Clean did not strip invisible characters: %q", c.Categories[3].Custom)
	}
	if s.Categories[1].Custom != "  Cosas   rojas " {
		t.Error("Clean must not modify the receiver")
	}
	c.Categories[0].ID = "mutated"
	if s.Categories[0].ID != "pais" {
		t.Error("Clean must deep-copy the category slice")
	}
	if got, want := s.Labels(), []string{"Country", "  Cosas   rojas ", "ghost", "\u200bCo\u00adsas\u200d azules"}; !slices.Equal(got, want) {
		t.Errorf("Labels() = %q, want %q", got, want)
	}
}

func TestSettingsJSONRoundTrip(t *testing.T) {
	s := DefaultSettings()
	s.Lang = LangEN
	s.Rounds = 0
	s.TimeLimitSeconds = 90
	s.Categories = append(s.Categories[:2], CategoryRef{Custom: "Cosas rojas"})

	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"lang":"en"`, `"excludeHardLetters":true`, `"rounds":0`, `"timeLimitSeconds":90`, `{"id":"nombre"}`, `{"custom":"Cosas rojas"}`} {
		if !strings.Contains(string(b), key) {
			t.Errorf("JSON %s is missing %s", b, key)
		}
	}
	var back Settings
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s, back) {
		t.Errorf("round trip changed settings:\n got %+v\nwant %+v", back, s)
	}
}

func TestErrorCode(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{ErrNotHost, "not_host"},
		{ErrWrongPhase, "wrong_phase"},
		{ErrLobbyFull, "lobby_full"},
		{ErrBastaNotAllowed, "basta_not_allowed"},
		{&DuplicateCategoryError{Label: "País"}, "invalid_settings"},
		{errors.Join(errors.New("ctx"), ErrNotYourTurn), "not_your_turn"},
		{errors.New("boom"), "internal"},
		{nil, "internal"},
	}
	for _, tt := range tests {
		if got := ErrorCode(tt.err); got != tt.want {
			t.Errorf("ErrorCode(%v) = %q, want %q", tt.err, got, tt.want)
		}
	}
	seen := map[string]bool{}
	for _, e := range errorCodes {
		if seen[e.code] {
			t.Errorf("duplicate error code %q", e.code)
		}
		seen[e.code] = true
	}
}
