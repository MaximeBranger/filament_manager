package reference

import (
	"strconv"
	"strings"

	"github.com/MaximeBranger/filament_manager/internal/catalog"
	"github.com/MaximeBranger/filament_manager/internal/color"
	"github.com/MaximeBranger/filament_manager/internal/store"
)

// hexTolerance est l'écart ΔE2000 en dessous duquel deux teintes sont considérées identiques.
const hexTolerance = 3

// Fields transpose l'entrée en champs produit, en conservant le code-barre saisi.
func (e Entry) Fields(ean string) store.ProductFields {
	f := store.ProductFields{
		Brand: e.Brand, Material: e.Material, MaterialOther: e.MaterialOther, ColorName: e.ColorName,
		ColorFamily: e.ColorFamily, ColorHex: e.ColorHex, ColorHex2: e.ColorHex2, Finish: e.Finish, DiameterMM: e.DiameterMM,
		NominalWeightG: e.WeightG, EAN: ean,
	}
	// Teinte identique à celle de la famille : on ne la fige pas (comme à la saisie).
	if fam, ok := catalog.FamilyByCode(f.ColorFamily); ok && strings.EqualFold(fam.Hex, f.ColorHex) {
		f.ColorHex = ""
	}
	return f
}

// Difference est un champ dont la valeur diffère de la référence.
type Difference struct {
	Field string
	Mine  string
	Ref   string
}

func materialText(code, other string) string {
	if code == catalog.Other && other != "" {
		return other
	}
	return catalog.MaterialLabel(code)
}

func finishText(code, other string) string {
	if code == catalog.Other && other != "" {
		return other
	}
	return catalog.FinishLabel(code)
}

func familyLabel(code string) string {
	f, _ := catalog.FamilyByCode(code)
	return f.Label
}

func formatNumber(v float64) string {
	return strings.Replace(strconv.FormatFloat(v, 'f', -1, 64), ".", ",", 1)
}

// sameHex indique si deux teintes sont indiscernables (à hexTolerance près).
func sameHex(x, y string) bool {
	a, errA := color.ParseHex(x)
	b, errB := color.ParseHex(y)
	return errA == nil && errB == nil && color.Distance(a, b) <= hexTolerance
}

// Compare liste les écarts entre un produit et sa référence (vide s'ils concordent).
func Compare(p store.ProductFields, e Entry) []Difference {
	ref := e.Fields(p.EAN)
	var out []Difference
	add := func(field, mine, theirs string) {
		out = append(out, Difference{field, mine, theirs})
	}
	if p.Brand != ref.Brand {
		add("Marque", p.Brand, ref.Brand)
	}
	if mine, theirs := materialText(p.Material, p.MaterialOther), materialText(ref.Material, ref.MaterialOther); p.Material != ref.Material || mine != theirs {
		add("Matériau", mine, theirs)
	}
	if p.ColorName != ref.ColorName {
		add("Couleur", p.ColorName, ref.ColorName)
	}
	if p.ColorFamily != ref.ColorFamily {
		add("Famille de couleur", familyLabel(p.ColorFamily), familyLabel(ref.ColorFamily))
	}
	if mineHex, theirHex := p.Hex(), ref.Hex(); !sameHex(mineHex, theirHex) {
		add("Teinte", mineHex, theirHex)
	}
	// Seconde teinte : comparée seulement si la référence en connaît une.
	if ref.ColorHex2 != "" && !sameHex(p.ColorHex2, ref.ColorHex2) {
		add("Seconde teinte", p.ColorHex2, ref.ColorHex2)
	}
	if mine, theirs := finishText(p.Finish, p.FinishOther), finishText(ref.Finish, ref.FinishOther); p.Finish != ref.Finish || mine != theirs {
		add("Finition", mine, theirs)
	}
	if p.DiameterMM != ref.DiameterMM {
		add("Diamètre", formatNumber(p.DiameterMM)+" mm", formatNumber(ref.DiameterMM)+" mm")
	}
	if p.NominalWeightG != ref.NominalWeightG {
		add("Poids", strconv.Itoa(p.NominalWeightG)+" g", strconv.Itoa(ref.NominalWeightG)+" g")
	}
	return out
}
