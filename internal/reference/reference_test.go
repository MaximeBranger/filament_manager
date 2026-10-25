package reference

import (
	"encoding/json"
	"strings"
	"testing"
)

const dataset = `{
  "version": "2026.10.24", "generated_at": "2026-10-24T00:00:00Z",
  "brands": [{"id": "b1", "name": "eSun"}, {"id": "b2", "name": "Polymaker"}],
  "materials": [
    {"id": "m1", "material": "PLA", "brand_id": "b1"},
    {"id": "m2", "material": "PA12", "brand_id": "b2"},
    {"id": "m3", "material": "PEEK", "brand_id": "b2"}
  ],
  "filaments": [
    {"id": "f1", "name": "PLA+", "material_id": "m1"},
    {"id": "f2", "name": "Fiberon PA12-CF", "material_id": "m2"},
    {"id": "f3", "name": "PolyMax PEEK", "material_id": "m3"}
  ],
  "variants": [
    {"id": "v1", "name": "Galaxy Black", "color_hex": "#1A1A1A", "traits": {"silk": true}, "filament_id": "f1"},
    {"id": "v2", "name": "Black", "color_hex": "#000000", "traits": {"contains_carbon_fiber": true, "matte": true}, "filament_id": "f2"},
    {"id": "v3", "name": "Natural", "color_hex": "#d9c9a3", "traits": {"transparent": true}, "filament_id": "f3"},
    {"id": "v4", "name": "Sunset", "color_hex": "#ff8800", "color_hexes": ["#ff8800", "#aa00ff"], "filament_id": "f1"}
  ],
  "sizes": [
    {"gtin": "6938936716785", "filament_weight": 1000, "diameter": 1.75, "spool_refill": true, "variant_id": "v1"},
    {"gtin": "6938936716785", "filament_weight": 1000, "diameter": 1.75, "variant_id": "v1"},
    {"ean": "012345678905", "filament_weight": 500, "diameter": 2.85, "variant_id": "v2"},
    {"gtin": "4006381333931", "filament_weight": 250, "diameter": 1.75, "variant_id": "v3"},
    {"gtin": "5901234123457", "filament_weight": 1000, "diameter": 1.75, "variant_id": "v4"},
    {"gtin": "9780201379624", "filament_weight": 1000, "diameter": 2.2, "variant_id": "v1"},
    {"filament_weight": 1000, "diameter": 1.75, "variant_id": "v1"}
  ]
}`

func convertTest(t *testing.T) map[string]Entry {
	t.Helper()
	data, count, err := Convert(strings.NewReader(dataset))
	if err != nil {
		t.Fatal(err)
	}
	var idx index
	if err := json.Unmarshal(data, &idx); err != nil {
		t.Fatalf("index produit illisible : %v\n%s", err, data)
	}
	if count != 4 || idx.Count != 4 || idx.Version != "2026.10.24" {
		t.Fatalf("%d codes (%d annoncés), version %q", count, idx.Count, idx.Version)
	}
	// Une entrée par ligne : les PR de mise à jour restent lisibles.
	if lines := strings.Count(string(data), "\n"); lines < 6 {
		t.Errorf("format attendu : une entrée par ligne (%d lignes)", lines)
	}
	return idx.Entries
}

func TestConvertMapping(t *testing.T) {
	e := convertTest(t)

	plus := e["6938936716785"]
	if plus.Brand != "eSun" || plus.Material != "PLA+" || plus.Finish != "silk" || plus.ColorHex != "#1a1a1a" ||
		plus.ColorFamily != "black" || plus.WeightG != 1000 || plus.Line != "PLA+" {
		t.Errorf("PLA+ silk : %+v", plus)
	}

	// UPC-A (12 chiffres) ramené à 13 ; PA12 → PA ; fibre de carbone prioritaire sur mat ; 2,85 mm.
	cf := e["0012345678905"]
	if cf.Material != "PA" || cf.Finish != "carbon" || cf.DiameterMM != 2.85 || cf.WeightG != 500 {
		t.Errorf("PA12-CF : %+v", cf)
	}

	// Matériau hors référentiel : « Autre » avec son nom ; trait transparent → famille transparente.
	peek := e["4006381333931"]
	if peek.Material != "OTHER" || peek.MaterialOther != "PEEK" || peek.ColorFamily != "transparent" {
		t.Errorf("PEEK : %+v", peek)
	}

	// Plusieurs teintes → multicolore.
	if multi := e["5901234123457"]; multi.Finish != "multicolor" || multi.ColorFamily != "multicolor" ||
		multi.ColorHex != "#ff8800" || multi.ColorHex2 != "#aa00ff" {
		t.Errorf("multicolore : %+v", multi)
	}

	// Diamètre exotique ignoré.
	if _, ok := e["9780201379624"]; ok {
		t.Error("un diamètre de 2,2 mm ne doit pas être importé")
	}
}

func TestConvertRejectsGarbage(t *testing.T) {
	for _, in := range []string{"", "{}", `{"version":"x","sizes":[]}`, "pas du json"} {
		if _, _, err := Convert(strings.NewReader(in)); err == nil {
			t.Errorf("export invalide accepté : %q", in)
		}
	}
}

func TestNormalizeGTIN(t *testing.T) {
	for in, want := range map[string]string{"012345678905": "0012345678905", "6938936716785": "6938936716785", "06938936716785": "6938936716785", " 6938936716785 ": "6938936716785"} {
		if got, ok := NormalizeGTIN(in); !ok || got != want {
			t.Errorf("NormalizeGTIN(%q) = %q, %v", in, got, ok)
		}
	}
	for _, bad := range []string{"", "12345", "69389367167X5", "16938936716785"} {
		if _, ok := NormalizeGTIN(bad); ok {
			t.Errorf("NormalizeGTIN(%q) accepté", bad)
		}
	}
}

func TestEmbeddedIndex(t *testing.T) {
	version, count := Info()
	if version == "" || count < 1000 {
		t.Fatalf("index embarqué : version %q, %d codes", version, count)
	}
	if !strings.Contains(License, "Permission is hereby granted") {
		t.Error("la licence de l'OFD doit être embarquée")
	}
	// Chaque entrée embarquée doit être exploitable par nos formulaires.
	for code, e := range load().Entries {
		if e.Brand == "" || e.ColorName == "" || e.Material == "" || e.ColorFamily == "" || e.WeightG <= 0 {
			t.Fatalf("entrée incomplète %s : %+v", code, e)
		}
		if _, ok := Lookup(code); !ok {
			t.Fatalf("Lookup(%s) introuvable", code)
		}
	}
}

func TestCompareAndFields(t *testing.T) {
	e := Entry{Brand: "eSun", Material: "PLA+", ColorName: "Galaxy Black", ColorFamily: "black", ColorHex: "#1b1b1f",
		Finish: "silk", DiameterMM: 1.75, WeightG: 1000}
	same := e.Fields("6938936716785")
	if same.EAN != "6938936716785" || same.Brand != "eSun" {
		t.Fatalf("Fields : %+v", same)
	}
	if d := Compare(same, e); len(d) != 0 {
		t.Errorf("aucun écart attendu : %+v", d)
	}

	// Teinte très proche : pas un écart.
	near := same
	near.ColorHex = "#1c1c20"
	if d := Compare(near, e); len(d) != 0 {
		t.Errorf("nuance infime signalée : %+v", d)
	}

	wrong := same
	wrong.Brand, wrong.Material, wrong.ColorName, wrong.ColorHex, wrong.Finish, wrong.NominalWeightG = "ESUN", "PLA", "Noir", "#ff0000", "standard", 750
	got := map[string]Difference{}
	for _, d := range Compare(wrong, e) {
		got[d.Field] = d
	}
	for _, f := range []string{"Marque", "Matériau", "Couleur", "Teinte", "Finition", "Poids"} {
		if _, ok := got[f]; !ok {
			t.Errorf("écart %q non signalé (%v)", f, got)
		}
	}
	if got["Finition"].Mine != "Standard" || got["Finition"].Ref != "Silk" || got["Poids"].Ref != "1000 g" {
		t.Errorf("libellés : %+v", got)
	}

	// Teinte égale à celle de la famille : non figée, comme à la saisie.
	if f := (Entry{ColorFamily: "black", ColorHex: "#1a1a1a"}).Fields(""); f.ColorHex != "" {
		t.Errorf("teinte de famille figée : %q", f.ColorHex)
	}
}
