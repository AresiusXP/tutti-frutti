// Package catalog exposes the built-in Tutti Frutti category catalog.
//
// The catalog is an embedded JSON file (categories.json): exactly 80 entries,
// 6 of them flagged as defaults ("Clásicas"). The file order is the display
// order. Custom per-game categories are never part of the catalog.
package catalog

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"slices"
)

//go:embed categories.json
var raw []byte

// Category is one built-in category.
type Category struct {
	// ID is a short, stable, lower-case ASCII key.
	ID string `json:"id"`
	// Group is the picker group (personas, lugares, ...).
	Group string `json:"group"`
	// ES, EN and NL are the labels in each lobby language.
	ES string `json:"es"`
	EN string `json:"en"`
	NL string `json:"nl"`
	// Default marks the six "Clásicas" categories.
	Default bool `json:"default"`
}

// data is parsed once at package initialisation and never mutated afterwards.
var data = mustLoad(raw)

type loaded struct {
	all    []Category
	byID   map[string]Category
	groups []string
}

// mustLoad parses and validates the embedded catalog. It is strict on
// purpose: an unknown field (a label for a language Category does not know)
// or an empty id, group or label (a language added to the file but missing in
// one entry) must stop the server at start-up, not show up as a blank button.
func mustLoad(b []byte) loaded {
	var all []Category
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&all); err != nil {
		panic(fmt.Sprintf("catalog: invalid categories.json: %v", err))
	}
	if _, err := dec.Token(); err != io.EOF {
		panic("catalog: invalid categories.json: data after the top-level array")
	}
	l := loaded{all: all, byID: make(map[string]Category, len(all))}
	for _, c := range all {
		if c.ID == "" || c.Group == "" || c.ES == "" || c.EN == "" || c.NL == "" {
			panic(fmt.Sprintf("catalog: entry %+v has an empty id, group or label", c))
		}
		l.byID[c.ID] = c
		if !slices.Contains(l.groups, c.Group) {
			l.groups = append(l.groups, c.Group)
		}
	}
	return l
}

// All returns a copy of every category in display order.
func All() []Category { return slices.Clone(data.all) }

// Defaults returns the default ("Clásicas") categories in display order.
func Defaults() []Category {
	var out []Category
	for _, c := range data.all {
		if c.Default {
			out = append(out, c)
		}
	}
	return out
}

// ByID looks a category up by its stable id.
func ByID(id string) (Category, bool) {
	c, ok := data.byID[id]
	return c, ok
}

// Groups returns the group names in display order.
func Groups() []string { return slices.Clone(data.groups) }

// Label returns the category label for lang: "en" gives English, "nl" gives
// Dutch, anything else gives Spanish.
func Label(c Category, lang string) string {
	switch lang {
	case "en":
		return c.EN
	case "nl":
		return c.NL
	}
	return c.ES
}

// LocalizedCategory is a category rendered in a single language.
type LocalizedCategory struct {
	ID      string `json:"id"`
	Group   string `json:"group"`
	Label   string `json:"label"`
	Default bool   `json:"default"`
}

// Listing is the JSON document served by GET /api/categories?lang=.
type Listing struct {
	Lang       string              `json:"lang"`
	Groups     []string            `json:"groups"`
	Categories []LocalizedCategory `json:"categories"`
}

// ForLang renders the whole catalog in lang. Any value other than "en" and
// "nl" is treated as "es"; Listing.Lang is the language actually used.
func ForLang(lang string) Listing {
	if lang != "en" && lang != "nl" {
		lang = "es"
	}
	out := Listing{Lang: lang, Groups: Groups(), Categories: make([]LocalizedCategory, 0, len(data.all))}
	for _, c := range data.all {
		out.Categories = append(out.Categories, LocalizedCategory{
			ID: c.ID, Group: c.Group, Label: Label(c, lang), Default: c.Default,
		})
	}
	return out
}
