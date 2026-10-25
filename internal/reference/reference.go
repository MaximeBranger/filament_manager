// Package reference fournit le catalogue de référence des filaments par code-barre, issu de
// l'Open Filament Database (licence MIT). L'index est embarqué dans le binaire et rafraîchi
// par une GitHub Action hebdomadaire (cmd/ofdimport) : aucun appel réseau à l'exécution.
package reference

import (
	_ "embed"
	"encoding/json"
	"strings"
	"sync"
)

//go:embed ofd.json
var raw []byte

//go:embed LICENSE-OFD
var License string

// Entry décrit un produit connu de la référence, déjà transposé dans notre référentiel.
type Entry struct {
	Brand         string  `json:"brand"`
	Material      string  `json:"material"`
	MaterialOther string  `json:"material_other,omitempty"`
	Line          string  `json:"line,omitempty"` // gamme du fabricant, ex. « PolyTerra PLA »
	ColorName     string  `json:"color"`
	ColorFamily   string  `json:"family"`
	ColorHex      string  `json:"hex,omitempty"`
	ColorHex2     string  `json:"hex2,omitempty"` // seconde teinte des multicolores
	Finish        string  `json:"finish"`
	DiameterMM    float64 `json:"diameter"`
	WeightG       int     `json:"weight"`
}

type index struct {
	Version     string           `json:"version"`
	GeneratedAt string           `json:"generated_at"`
	Count       int              `json:"count"`
	Entries     map[string]Entry `json:"entries"`
}

var load = sync.OnceValue(func() *index {
	var idx index
	if err := json.Unmarshal(raw, &idx); err != nil {
		panic("index de référence embarqué illisible : " + err.Error())
	}
	return &idx
})

// NormalizeGTIN ramène un code-barre (UPC-A 12, EAN-13, GTIN-14) à sa forme à 13 chiffres.
func NormalizeGTIN(code string) (string, bool) {
	code = strings.TrimSpace(code)
	for _, r := range code {
		if r < '0' || r > '9' {
			return "", false
		}
	}
	switch len(code) {
	case 12:
		return "0" + code, true
	case 13:
		return code, true
	case 14:
		if code[0] == '0' {
			return code[1:], true
		}
	}
	return "", false
}

// Lookup cherche un code-barre dans la référence.
func Lookup(code string) (Entry, bool) {
	c, ok := NormalizeGTIN(code)
	if !ok {
		return Entry{}, false
	}
	e, ok := load().Entries[c]
	return e, ok
}

// Info renvoie la version de la base source et le nombre de codes connus.
func Info() (version string, count int) {
	idx := load()
	return idx.Version, len(idx.Entries)
}
