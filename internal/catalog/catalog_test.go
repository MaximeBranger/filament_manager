package catalog

import (
	"testing"

	"github.com/MaximeBranger/filament_manager/internal/color"
)

func TestFold(t *testing.T) {
	cases := map[string]string{
		"  eSun  ":       "esun",
		"ESUN":           "esun",
		"Das   Filament": "das filament",
		"Noir Ébène":     "noir ebene",
		"Bambu\tLab":     "bambu lab",
		"Cœur de Pêche":  "coeur de peche",
	}
	for in, want := range cases {
		if got := Fold(in); got != want {
			t.Errorf("Fold(%q) = %q, attendu %q", in, got, want)
		}
	}
}

func TestCleanBrand(t *testing.T) {
	if got := CleanBrand("  Bambu    Lab "); got != "Bambu Lab" {
		t.Errorf("CleanBrand = %q", got)
	}
}

func TestFamilyFromHSL(t *testing.T) {
	// Les teintes par défaut de chaque famille doivent retomber dans leur famille.
	for _, f := range Families {
		if f.Code == "transparent" || f.Code == "multicolor" {
			continue // familles déduites des traits, pas de la teinte
		}
		c, _ := color.ParseHex(f.Hex)
		if got := FamilyFromHSL(c.HSL()); got != f.Code {
			t.Errorf("%s (%s) classé %s", f.Code, f.Hex, got)
		}
	}
	for hex, want := range map[string]string{
		"#000000": "black", "#ffffff": "white", "#f2f3ee": "white", "#808080": "grey", "#4c5975": "grey",
		"#00a86b": "green", "#152a6b": "blue", "#fa6831": "orange", "#d4af37": "yellow",
		"#8b4513": "brown", "#f5deb3": "beige", "#ff69b4": "pink",
	} {
		c, _ := color.ParseHex(hex)
		if got := FamilyFromHSL(c.HSL()); got != want {
			t.Errorf("%s classé %s, attendu %s", hex, got, want)
		}
	}
}

func TestReferentials(t *testing.T) {
	for _, f := range Families {
		if _, err := color.ParseHex(f.Hex); err != nil {
			t.Errorf("famille %s : hexa invalide %q", f.Code, f.Hex)
		}
	}
	if !ValidMaterial("PETG") || ValidMaterial("petg") || !ValidMaterial(Other) {
		t.Error("ValidMaterial incohérent")
	}
	if LevelRank("new") != 0 || LevelRank("empty") != 4 || LevelRank("x") != 5 {
		t.Error("LevelRank incohérent")
	}
	if MaterialLabel("PA") != "PA (nylon)" || FinishLabel("glow") != "Phosphorescent" {
		t.Error("libellés inattendus")
	}
}

func TestFamiliesSorted(t *testing.T) {
	for i := 1; i < len(Families); i++ {
		if Families[i-1].Label > Families[i].Label {
			t.Errorf("familles non triées : %q avant %q", Families[i-1].Label, Families[i].Label)
		}
	}
}
