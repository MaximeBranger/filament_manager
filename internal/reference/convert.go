package reference

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/MaximeBranger/filament_manager/internal/catalog"
	"github.com/MaximeBranger/filament_manager/internal/color"
)

// Structure (partielle) de l'export all.json de l'Open Filament Database.
type ofdDataset struct {
	Version     string `json:"version"`
	GeneratedAt string `json:"generated_at"`
	Brands      []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"brands"`
	Materials []struct {
		ID       string `json:"id"`
		Material string `json:"material"`
		BrandID  string `json:"brand_id"`
	} `json:"materials"`
	Filaments []struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		MaterialID string `json:"material_id"`
	} `json:"filaments"`
	Variants []struct {
		ID         string          `json:"id"`
		Name       string          `json:"name"`
		ColorHex   string          `json:"color_hex"`
		ColorHexes json.RawMessage `json:"color_hexes"`
		Traits     map[string]bool `json:"traits"`
		FilamentID string          `json:"filament_id"`
	} `json:"variants"`
	Sizes []struct {
		GTIN           string  `json:"gtin"`
		EAN            string  `json:"ean"`
		FilamentWeight float64 `json:"filament_weight"`
		Diameter       float64 `json:"diameter"`
		SpoolRefill    bool    `json:"spool_refill"`
		Container      string  `json:"container_material"`
		VariantID      string  `json:"variant_id"`
	} `json:"sizes"`
}

// mapMaterial rattache un matériau de l'OFD à notre référentiel.
func mapMaterial(material, filamentName string) (code, other string) {
	m := strings.ToUpper(strings.TrimSpace(material))
	switch m {
	case "PLA":
		name := strings.ToUpper(filamentName)
		if strings.Contains(name, "PLA+") || strings.Contains(name, "PLA PLUS") {
			return "PLA+", ""
		}
		return "PLA", ""
	case "PETG", "ABS", "ASA", "TPU", "PC", "PVA", "HIPS":
		return m, ""
	case "PA", "PA6", "PA11", "PA12", "PA66", "PPA":
		return "PA", ""
	}
	return catalog.Other, strings.TrimSpace(material)
}

// mapFinish choisit la finition la plus marquante parmi les traits.
func mapFinish(traits map[string]bool, multiColor bool) string {
	switch {
	case traits["glow"]:
		return "glow"
	case traits["contains_carbon_fiber"] || traits["contains_carbon"]:
		return "carbon"
	case traits["imitates_marble"]:
		return "marble"
	case traits["imitates_wood"] || traits["contains_wood"]:
		return "wood"
	case multiColor || traits["coextruded"] || traits["gradual_color_change"]:
		return "multicolor"
	case traits["glitter"]:
		return "glitter"
	case traits["silk"]:
		return "silk"
	case traits["matte"]:
		return "matte"
	}
	return catalog.DefaultFinish
}

// twoHexes retient les deux premières teintes distinctes d'un multicolore : la teinte principale,
// complétée par la liste « color_hexes » (illisible ou absente : ignorée).
func twoHexes(hex string, raw json.RawMessage) (string, string) {
	var list []string
	json.Unmarshal(raw, &list)
	for _, h := range list {
		n, err := color.NormalizeHex(h)
		switch {
		case err != nil:
		case hex == "":
			hex = n
		case n != hex:
			return hex, n
		}
	}
	return hex, ""
}

func mapDiameter(d float64) (float64, bool) {
	switch {
	case d == 0 || (d > 1.7 && d < 1.8):
		return 1.75, true
	case d > 2.8 && d < 3.05:
		return 2.85, true
	}
	return 0, false
}

// Convert transforme l'export all.json de l'OFD en index de référence (JSON, une entrée par
// ligne, triée) : seules les références portant un code-barre sont conservées.
func Convert(r io.Reader) ([]byte, int, error) {
	var ds ofdDataset
	if err := json.NewDecoder(r).Decode(&ds); err != nil {
		return nil, 0, fmt.Errorf("lecture de l'export OFD : %w", err)
	}
	if ds.Version == "" || len(ds.Sizes) == 0 {
		return nil, 0, fmt.Errorf("export OFD vide ou inattendu")
	}

	brands := map[string]string{}
	for _, b := range ds.Brands {
		brands[b.ID] = b.Name
	}
	type mat struct{ material, brandID string }
	materials := map[string]mat{}
	for _, m := range ds.Materials {
		materials[m.ID] = mat{m.Material, m.BrandID}
	}
	type fil struct{ name, materialID string }
	filaments := map[string]fil{}
	for _, f := range ds.Filaments {
		filaments[f.ID] = fil{f.Name, f.MaterialID}
	}
	byID := map[string]int{}
	for i, v := range ds.Variants {
		byID[v.ID] = i
	}

	entries := map[string]Entry{}
	refill := map[string]bool{}
	for _, s := range ds.Sizes {
		raw := s.GTIN
		if raw == "" {
			raw = s.EAN
		}
		code, ok := NormalizeGTIN(raw)
		if !ok {
			continue
		}
		vi, ok := byID[s.VariantID]
		if !ok {
			continue
		}
		v := ds.Variants[vi]
		f, ok := filaments[v.FilamentID]
		if !ok {
			continue
		}
		m, ok := materials[f.materialID]
		if !ok {
			continue
		}
		brand := brands[m.brandID]
		diameter, ok := mapDiameter(s.Diameter)
		if brand == "" || !ok {
			continue
		}
		isRefill := s.SpoolRefill || s.Container == "refill"
		if _, exists := entries[code]; exists && (isRefill || !refill[code]) {
			continue // un même code peut couvrir bobine et recharge : on garde la bobine
		}

		material, other := mapMaterial(m.material, f.name)
		multi := len(v.ColorHexes) > 0 && string(v.ColorHexes) != "null" && string(v.ColorHexes) != "[]"
		finish := mapFinish(v.Traits, multi)
		hex, err := color.NormalizeHex(v.ColorHex)
		family := ""
		switch {
		case v.Traits["transparent"]:
			family = "transparent"
		case finish == "multicolor":
			family = "multicolor"
		case err == nil:
			c, _ := color.ParseHex(hex)
			family = catalog.FamilyFromHSL(c.HSL())
		default:
			family = "grey"
		}
		if err != nil {
			hex = ""
		}
		hex2 := ""
		if family == "multicolor" {
			hex, hex2 = twoHexes(hex, v.ColorHexes)
		}
		weight := int(s.FilamentWeight + 0.5)
		if weight <= 0 {
			weight = catalog.DefaultWeight
		}
		entries[code] = Entry{
			Brand: brand, Material: material, MaterialOther: other, Line: f.name,
			ColorName: strings.TrimSpace(v.Name), ColorFamily: family, ColorHex: hex, ColorHex2: hex2, Finish: finish,
			DiameterMM: diameter, WeightG: weight,
		}
		refill[code] = isRefill
	}

	codes := make([]string, 0, len(entries))
	for c := range entries {
		codes = append(codes, c)
	}
	sort.Strings(codes)

	var buf bytes.Buffer
	header, _ := json.Marshal(map[string]any{
		"source":  "Open Filament Database (https://github.com/OpenFilamentCollective/open-filament-database)",
		"license": "MIT", "version": ds.Version, "generated_at": ds.GeneratedAt, "count": len(codes),
	})
	buf.Write(header[:len(header)-1]) // on rouvre l'objet pour y ajouter les entrées
	buf.WriteString(",\n\"entries\":{\n")
	for i, c := range codes {
		line, _ := json.Marshal(entries[c])
		fmt.Fprintf(&buf, "%q:%s", c, line)
		if i < len(codes)-1 {
			buf.WriteByte(',')
		}
		buf.WriteByte('\n')
	}
	buf.WriteString("}}\n")
	return buf.Bytes(), len(codes), nil
}
