package game

import (
	"slices"
	"testing"
)

// cellWant is the expected outcome of one answer.
type cellWant struct {
	status Status
	points int
	shared bool
}

var (
	unique   = cellWant{StatusValid, 10, false}
	shared   = cellWant{StatusValid, 5, true}
	empty    = cellWant{StatusEmpty, 0, false}
	wrong    = cellWant{StatusWrongLetter, 0, false}
	rejected = cellWant{StatusRejected, 0, false}
)

func TestScoreRound(t *testing.T) {
	// Every case scores a single category (index 0) unless noted.
	tests := []struct {
		name     string
		lang     Lang
		letter   string
		answers  map[string]string
		rejected []string // players whose answer in category 0 was rejected
		merges   [][]string
		want     map[string]cellWant
	}{
		{
			name:    "all unique",
			lang:    LangES,
			letter:  "A",
			answers: map[string]string{"p1": "Argentina", "p2": "Alemania", "p3": "Angola"},
			want:    map[string]cellWant{"p1": unique, "p2": unique, "p3": unique},
		},
		{
			name:    "shared by two",
			lang:    LangES,
			letter:  "A",
			answers: map[string]string{"p1": "Argentina", "p2": "argentina", "p3": "Alemania"},
			want:    map[string]cellWant{"p1": shared, "p2": shared, "p3": unique},
		},
		{
			name:    "shared by three",
			lang:    LangES,
			letter:  "A",
			answers: map[string]string{"p1": "Ana", "p2": "ANA", "p3": " ana ", "p4": "Alba"},
			want:    map[string]cellWant{"p1": shared, "p2": shared, "p3": shared, "p4": unique},
		},
		{
			name:     "rejecting one makes the other unique",
			lang:     LangES,
			letter:   "A",
			answers:  map[string]string{"p1": "Ana", "p2": "Ana"},
			rejected: []string{"p2"},
			want:     map[string]cellWant{"p1": unique, "p2": rejected},
		},
		{
			name:     "rejecting one of three leaves a pair",
			lang:     LangES,
			letter:   "A",
			answers:  map[string]string{"p1": "Ana", "p2": "Ana", "p3": "Ana"},
			rejected: []string{"p3"},
			want:     map[string]cellWant{"p1": shared, "p2": shared, "p3": rejected},
		},
		{
			name:    "merge turns two tens into two fives",
			lang:    LangES,
			letter:  "A",
			answers: map[string]string{"p1": "Ana", "p2": "Anna", "p3": "Alba"},
			merges:  [][]string{{"p1", "p2"}},
			want:    map[string]cellWant{"p1": shared, "p2": shared, "p3": unique},
		},
		{
			name:    "merge group of three",
			lang:    LangES,
			letter:  "A",
			answers: map[string]string{"p1": "Ana", "p2": "Anna", "p3": "Anita"},
			merges:  [][]string{{"p1", "p2", "p3"}},
			want:    map[string]cellWant{"p1": shared, "p2": shared, "p3": shared},
		},
		{
			name:    "merges are transitive",
			lang:    LangES,
			letter:  "A",
			answers: map[string]string{"p1": "Ana", "p2": "Anna", "p3": "Anita", "p4": "Alba"},
			merges:  [][]string{{"p1", "p2"}, {"p2", "p3"}},
			want:    map[string]cellWant{"p1": shared, "p2": shared, "p3": shared, "p4": unique},
		},
		{
			name:    "merge joins two existing duplicate groups",
			lang:    LangES,
			letter:  "A",
			answers: map[string]string{"p1": "Ana", "p2": "Ana", "p3": "Alba", "p4": "Alba", "p5": "Aldo"},
			merges:  [][]string{{"p2", "p3"}},
			want:    map[string]cellWant{"p1": shared, "p2": shared, "p3": shared, "p4": shared, "p5": unique},
		},
		{
			name:     "merge only links valid answers",
			lang:     LangES,
			letter:   "A",
			answers:  map[string]string{"p1": "Ana", "p2": "Anna", "p3": "Anita"},
			rejected: []string{"p2"},
			merges:   [][]string{{"p1", "p2"}, {"p2", "p3"}},
			want:     map[string]cellWant{"p1": unique, "p2": rejected, "p3": unique},
		},
		{
			name:    "merge ignores empty and wrong-letter members",
			lang:    LangES,
			letter:  "A",
			answers: map[string]string{"p1": "Ana", "p2": "", "p3": "Beto"},
			merges:  [][]string{{"p1", "p2", "p3"}},
			want:    map[string]cellWant{"p1": unique, "p2": empty, "p3": wrong},
		},
		{
			name:    "merge group with a single valid member changes nothing",
			lang:    LangES,
			letter:  "A",
			answers: map[string]string{"p1": "Ana", "p2": "Alba"},
			merges:  [][]string{{"p1"}, {"p2", "ghost"}},
			want:    map[string]cellWant{"p1": unique, "p2": unique},
		},
		{
			name:    "empty answers",
			lang:    LangES,
			letter:  "A",
			answers: map[string]string{"p1": "", "p2": "   ", "p3": "Ana"},
			want:    map[string]cellWant{"p1": empty, "p2": empty, "p3": unique},
		},
		{
			name:    "wrong letter scores zero and does not make duplicates",
			lang:    LangES,
			letter:  "A",
			answers: map[string]string{"p1": "Brasil", "p2": "Brasil", "p3": "Ana", "p4": "Ana"},
			want:    map[string]cellWant{"p1": wrong, "p2": wrong, "p3": shared, "p4": shared},
		},
		{
			name:     "wrong letter wins over rejected",
			lang:     LangES,
			letter:   "A",
			answers:  map[string]string{"p1": "Brasil"},
			rejected: []string{"p1"},
			want:     map[string]cellWant{"p1": wrong},
		},
		{
			name:    "México and mexico are the same",
			lang:    LangES,
			letter:  "M",
			answers: map[string]string{"p1": "México", "p2": "mexico", "p3": "Marruecos"},
			want:    map[string]cellWant{"p1": shared, "p2": shared, "p3": unique},
		},
		{
			name:    "invisible format characters do not make answers differ",
			lang:    LangES,
			letter:  "M",
			answers: map[string]string{"p1": "M\u00e9xico", "p2": "\u200bmexico", "p3": "Me\u00adxi\u200bco\ufeff", "p4": "Marruecos"},
			want:    map[string]cellWant{"p1": shared, "p2": shared, "p3": shared, "p4": unique},
		},
		{
			name:    "a leading zero width space does not make the letter wrong",
			lang:    LangEN,
			letter:  "P",
			answers: map[string]string{"p1": "\u200bPerro", "p2": "Pato"},
			want:    map[string]cellWant{"p1": unique, "p2": unique},
		},
		{
			name:    "an answer of only invisible characters is empty",
			lang:    LangES,
			letter:  "A",
			answers: map[string]string{"p1": "\u200b\u200d", "p2": "Ana"},
			want:    map[string]cellWant{"p1": empty, "p2": unique},
		},
		{
			name:    "NFC and NFD spellings are the same answer",
			lang:    LangES,
			letter:  "M",
			answers: map[string]string{"p1": "M\u00e9xico", "p2": "Me\u0301xico", "p3": "MEXICO", "p4": "Marruecos"},
			want:    map[string]cellWant{"p1": shared, "p2": shared, "p3": shared, "p4": unique},
		},
		{
			name:    "NFD letter and NFD answers",
			lang:    LangES,
			letter:  "A",
			answers: map[string]string{"p1": "A\u0301frica", "p2": "\u00c1frica", "p3": "Asia"},
			want:    map[string]cellWant{"p1": shared, "p2": shared, "p3": unique},
		},
		{
			name:    "es: NFC and NFD enye match each other and the enye letter",
			lang:    LangES,
			letter:  "\u00d1",
			answers: map[string]string{"p1": "\u00d1and\u00fa", "p2": "N\u0303andu\u0301", "p3": "Nandu"},
			want:    map[string]cellWant{"p1": shared, "p2": shared, "p3": wrong},
		},
		{
			name:    "en: NFC and NFD enye fold into n",
			lang:    LangEN,
			letter:  "N",
			answers: map[string]string{"p1": "\u00d1and\u00fa", "p2": "N\u0303andu\u0301", "p3": "Nandu"},
			want:    map[string]cellWant{"p1": shared, "p2": shared, "p3": shared},
		},
		{
			name:    "es: enye is its own letter",
			lang:    LangES,
			letter:  "Ñ",
			answers: map[string]string{"p1": "Ñandú", "p2": "ñandu", "p3": "Nandu"},
			want:    map[string]cellWant{"p1": shared, "p2": shared, "p3": wrong},
		},
		{
			name:    "es: enye answers do not count for N",
			lang:    LangES,
			letter:  "N",
			answers: map[string]string{"p1": "Ñandú", "p2": "Nandu", "p3": "Nube"},
			want:    map[string]cellWant{"p1": wrong, "p2": unique, "p3": unique},
		},
		{
			name:    "en: enye folds into n",
			lang:    LangEN,
			letter:  "N",
			answers: map[string]string{"p1": "Ñandú", "p2": "Nandu", "p3": "Nube"},
			want:    map[string]cellWant{"p1": shared, "p2": shared, "p3": unique},
		},
		{
			name:    "nl: ij ligature, IJ and Ij are the same answer",
			lang:    LangNL,
			letter:  "I",
			answers: map[string]string{"p1": "IJsland", "p2": "\u0132sland", "p3": "\u0133sland", "p4": "ijsland", "p5": "Ierland"},
			want:    map[string]cellWant{"p1": shared, "p2": shared, "p3": shared, "p4": shared, "p5": unique},
		},
		{
			name:    "nl: ysland is not an I, nor is IJsland a Y",
			lang:    LangNL,
			letter:  "I",
			answers: map[string]string{"p1": "IJsland", "p2": "Ysland"},
			want:    map[string]cellWant{"p1": unique, "p2": wrong},
		},
		{
			name:    "nl: IJsland does not count for Y",
			lang:    LangNL,
			letter:  "Y",
			answers: map[string]string{"p1": "IJsland", "p2": "\u0132sland", "p3": "Yoghurt"},
			want:    map[string]cellWant{"p1": wrong, "p2": wrong, "p3": unique},
		},
		{
			name:    "nl: ij is two letters, so IJs and Is are different answers",
			lang:    LangNL,
			letter:  "I",
			answers: map[string]string{"p1": "IJs", "p2": "\u0132s", "p3": "Is"},
			want:    map[string]cellWant{"p1": shared, "p2": shared, "p3": unique},
		},
		{
			name:    "nl: accents of any kind match",
			lang:    LangNL,
			letter:  "C",
			answers: map[string]string{"p1": "Citro\u00ebn", "p2": "citroe\u0308n", "p3": "CITROEN", "p4": "Curaçao"},
			want:    map[string]cellWant{"p1": shared, "p2": shared, "p3": shared, "p4": unique},
		},
		{
			name:    "nl: diaeresis and acute on a Dutch word",
			lang:    LangNL,
			letter:  "E",
			answers: map[string]string{"p1": "\u00c9\u00e9n", "p2": "Ee\u0301n", "p3": "een", "p4": "Elf"},
			want:    map[string]cellWant{"p1": shared, "p2": shared, "p3": shared, "p4": unique},
		},
		{
			name:    "nl: enye folds into n",
			lang:    LangNL,
			letter:  "N",
			answers: map[string]string{"p1": "\u00d1andú", "p2": "N\u0303andu\u0301", "p3": "Nandu", "p4": "Nijmegen"},
			want:    map[string]cellWant{"p1": shared, "p2": shared, "p3": shared, "p4": unique},
		},
		{
			name:    "es: ij ligature matches IJ",
			lang:    LangES,
			letter:  "I",
			answers: map[string]string{"p1": "IJsland", "p2": "\u0132sland", "p3": "Italia"},
			want:    map[string]cellWant{"p1": shared, "p2": shared, "p3": unique},
		},
		{
			name:    "en: ij ligature matches IJ",
			lang:    LangEN,
			letter:  "I",
			answers: map[string]string{"p1": "IJsland", "p2": "\u0133sland", "p3": "Italy"},
			want:    map[string]cellWant{"p1": shared, "p2": shared, "p3": unique},
		},
		{
			name:     "nl: a rejected ij answer no longer counts as a duplicate",
			lang:     LangNL,
			letter:   "I",
			answers:  map[string]string{"p1": "IJsland", "p2": "\u0132sland", "p3": "\u0133sland"},
			rejected: []string{"p3"},
			want:     map[string]cellWant{"p1": shared, "p2": shared, "p3": rejected},
		},
		{
			name:    "player with an empty category among others",
			lang:    LangES,
			letter:  "A",
			answers: map[string]string{"p1": "Ana", "p2": ""},
			want:    map[string]cellWant{"p1": unique, "p2": empty},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := RoundInput{Lang: tt.lang, Letter: tt.letter, Categories: 1, Answers: map[string][]string{}}
			for _, p := range sortedKeys(tt.answers) {
				in.Participants = append(in.Participants, p)
				in.Answers[p] = []string{tt.answers[p]}
			}
			for _, p := range tt.rejected {
				in.Rejected = append(in.Rejected, AnswerKey{Player: p, Category: 0})
			}
			for _, g := range tt.merges {
				in.Merges = append(in.Merges, MergeGroup{Category: 0, Players: g})
			}

			res := ScoreRound(in)
			if res.Letter != tt.letter {
				t.Errorf("Letter = %q, want %q", res.Letter, tt.letter)
			}
			if len(res.Players) != len(tt.want) {
				t.Fatalf("scored %d players, want %d", len(res.Players), len(tt.want))
			}
			for p, w := range tt.want {
				got := res.Players[p].Cells[0]
				if got.Status != w.status || got.Points != w.points || got.Shared != w.shared {
					t.Errorf("%s: got {%s %d shared=%v}, want {%s %d shared=%v}",
						p, got.Status, got.Points, got.Shared, w.status, w.points, w.shared)
				}
				if res.Players[p].Total != w.points {
					t.Errorf("%s: total = %d, want %d", p, res.Players[p].Total, w.points)
				}
			}
		})
	}
}

func TestScoreRoundMultipleCategories(t *testing.T) {
	in := RoundInput{
		Lang:         LangES,
		Letter:       "A",
		Categories:   3,
		Participants: []string{"p1", "p2", "p3"},
		Answers: map[string][]string{
			"p1": {"Ana", "Argentina", "Azul"},
			"p2": {"Ana", "Alemania", ""},
			"p3": {"", "", ""}, // a player with every category empty
		},
		Merges: []MergeGroup{{Category: 1, Players: []string{"p1", "p2"}}},
	}
	res := ScoreRound(in)

	wantTotals := map[string]int{"p1": 5 + 5 + 10, "p2": 5 + 5 + 0, "p3": 0}
	for p, want := range wantTotals {
		if got := res.Players[p].Total; got != want {
			t.Errorf("%s total = %d, want %d", p, got, want)
		}
		if got := len(res.Players[p].Cells); got != 3 {
			t.Errorf("%s has %d cells, want 3", p, got)
		}
	}
	for i, c := range res.Players["p3"].Cells {
		if c.Status != StatusEmpty || c.Points != 0 || c.Answer != "" {
			t.Errorf("p3 cell %d = %+v, want empty", i, c)
		}
	}
	// A merge in category 1 must not leak into other categories.
	if c := res.Players["p1"].Cells[2]; c.Shared || c.Points != 10 {
		t.Errorf("p1 category 2 = %+v, want unique", c)
	}
	if c := res.Players["p1"].Cells[0]; c.Answer != "ana" || c.Raw != "Ana" {
		t.Errorf("cell keeps raw and normalized answers, got %+v", c)
	}
}

func TestScoreRoundEdgeCases(t *testing.T) {
	t.Run("short or missing answers count as empty", func(t *testing.T) {
		res := ScoreRound(RoundInput{
			Lang: LangES, Letter: "A", Categories: 2,
			Participants: []string{"p1", "p2"},
			Answers:      map[string][]string{"p1": {"Ana"}},
		})
		if c := res.Players["p1"].Cells[1]; c.Status != StatusEmpty {
			t.Errorf("missing category = %+v", c)
		}
		if c := res.Players["p2"].Cells[0]; c.Status != StatusEmpty || res.Players["p2"].Total != 0 {
			t.Errorf("missing player answers = %+v", c)
		}
	})
	t.Run("duplicate participants and strangers are ignored", func(t *testing.T) {
		res := ScoreRound(RoundInput{
			Lang: LangES, Letter: "A", Categories: 1,
			Participants: []string{"p1", "p1", "p2"},
			Answers:      map[string][]string{"p1": {"Ana"}, "p2": {"Ana"}, "stranger": {"Ana"}},
		})
		if len(res.Players) != 2 {
			t.Fatalf("scored %v, want only p1 and p2", res.Players)
		}
		if c := res.Players["p1"].Cells[0]; c.Points != 5 {
			t.Errorf("p1 = %+v, want shared 5 (stranger must not count)", c)
		}
	})
	t.Run("rejection is per category", func(t *testing.T) {
		res := ScoreRound(RoundInput{
			Lang: LangES, Letter: "A", Categories: 2,
			Participants: []string{"p1"},
			Answers:      map[string][]string{"p1": {"Ana", "Ana"}},
			Rejected:     []AnswerKey{{Player: "p1", Category: 1}},
		})
		cells := res.Players["p1"].Cells
		if cells[0].Status != StatusValid || cells[1].Status != StatusRejected {
			t.Errorf("cells = %+v", cells)
		}
	})
	t.Run("no categories", func(t *testing.T) {
		res := ScoreRound(RoundInput{Lang: LangES, Letter: "A", Participants: []string{"p1"}})
		if got := res.Players["p1"]; got.Total != 0 || len(got.Cells) != 0 {
			t.Errorf("got %+v", got)
		}
	})
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
