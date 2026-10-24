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
