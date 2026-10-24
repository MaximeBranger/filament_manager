package stock

import (
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/MaximeBranger/filament_manager/internal/store"
)

func form(kv ...string) url.Values {
	v := url.Values{}
	for i := 0; i < len(kv); i += 2 {
		v.Add(kv[i], kv[i+1])
	}
	return v
}

func TestParseProduct(t *testing.T) {
	p, errs := ParseProduct(form(
		"brand", "  Bambu   Lab ", "material", "PLA", "color_name", "Galaxy Black",
		"color_family", "black", "color_hex", "#1A1A1A", "finish", "silk", "diameter", "2.85",
		"weight", "250", "ean", "6 975337 03",
	))
	if errs.Any() {
		t.Fatalf("erreurs inattendues : %v", errs)
	}
	if p.Brand != "Bambu Lab" || p.EAN != "697533703" || p.DiameterMM != 2.85 || p.NominalWeightG != 250 {
		t.Errorf("produit = %+v", p)
	}
	if p.ColorHex != "" {
		t.Errorf("l'hexa par défaut de la famille ne doit pas être figé, obtenu %q", p.ColorHex)
	}

	p, _ = ParseProduct(form("brand", "x", "material", "PLA", "color_name", "x", "color_family", "black", "color_hex", "#123456"))
	if p.ColorHex != "#123456" || p.Finish != "standard" || p.DiameterMM != 1.75 || p.NominalWeightG != 1000 {
		t.Errorf("valeurs par défaut : %+v", p)
	}
}

func TestParseProductErrors(t *testing.T) {
	_, errs := ParseProduct(form("material", "OTHER", "color_family", "fuchsia", "finish", "OTHER",
		"color_hex", "zz", "diameter", "3", "weight", "-1", "ean", "!!"))
	for _, k := range []string{"brand", "material_other", "color_name", "color_family", "finish_other",
		"color_hex", "diameter", "weight", "ean"} {
		if _, ok := errs[k]; !ok {
			t.Errorf("erreur attendue sur %s (%v)", k, errs)
		}
	}
}

func TestParseSpool(t *testing.T) {
	s, errs := ParseSpool(form("level", "half", "purchase_date", "2026-10-01", "price", "19,99 €", "hidden", "on"))
	if errs.Any() {
		t.Fatal(errs)
	}
	if s.Level != "half" || s.PurchaseDate != "2026-10-01" || s.PriceCents == nil || *s.PriceCents != 1999 || !s.Hidden {
		t.Errorf("bobine = %+v", s)
	}
	_, errs = ParseSpool(form("level", "plein", "purchase_date", "01/10/2026", "price", "gratuit"))
	if len(errs) != 3 {
		t.Errorf("3 erreurs attendues : %v", errs)
	}
	errs = Errors{}
	if n := ParseQuantity(form("quantity", "51"), errs); n != 1 || errs["quantity"] == "" {
		t.Error("quantité hors limite acceptée")
	}
}

func TestPrice(t *testing.T) {
	if c, _ := ParsePrice("24.9"); c != 2490 || FormatPrice(c) != "24,90" {
		t.Errorf("prix = %d", c)
	}
}

func spool(id int64, brand, mat, colorName, family, hex, level, date string) store.Spool {
	return store.Spool{ID: id, SpoolFields: store.SpoolFields{Level: level, PurchaseDate: date},
		Product: store.Product{ProductFields: store.ProductFields{Brand: brand, Material: mat, ColorName: colorName,
			ColorFamily: family, ColorHex: hex, Finish: "standard", DiameterMM: 1.75}}}
}

func ids(items []Item) []int64 {
	out := []int64{}
	for _, it := range items {
		out = append(out, it.ID)
	}
	return out
}

func eq(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

var sample = []store.Spool{
	spool(1, "eSun", "PLA", "Galaxy Black", "black", "", "new", "2026-01-10"),
	spool(2, "Sunlu", "PETG", "Bleu Ciel", "blue", "#1e88e5", "half", "2026-03-01"),
	spool(3, "Polymaker", "PLA", "Navy", "blue", "#1a237e", "low", ""),
	spool(4, "eSun", "PLA", "Rouge Feu", "red", "", "empty", "2025-12-01"),
	spool(5, "Bambu Lab", "PETG", "Orange Électrique", "orange", "", "started", "2026-05-05"),
}

func TestApplyFilters(t *testing.T) {
	cases := []struct {
		name  string
		query url.Values
		want  []int64
	}{
		{"vides masquées par défaut", form(), []int64{5, 2, 3, 1}},
		{"afficher les vides", form("empty", "1"), []int64{4, 5, 2, 3, 1}},
		{"filtre niveau vide", form("lvl", "empty"), []int64{4}},
		{"texte sans accent", form("q", "electrique"), []int64{5}},
		{"texte multi-mots", form("q", "esun noir"), []int64{1}},
		{"famille", form("fam", "blue"), []int64{2, 3}},
		{"matériau + marque", form("mat", "PLA", "brand", "ESUN"), []int64{1}},
		{"tri marque", form("sort", "brand"), []int64{5, 1, 3, 2}},
		{"tri date", form("sort", "date"), []int64{5, 2, 1, 3}},
		{"tri niveau", form("sort", "level"), []int64{1, 5, 2, 3}},
	}
	for _, c := range cases {
		got := ids(ParseQuery(c.query).Apply(sample))
		if !eq(got, c.want) {
			t.Errorf("%s : %v, attendu %v", c.name, got, c.want)
		}
	}
}

func TestApplyAvailability(t *testing.T) {
	spools := []store.Spool{sample[0], sample[1]}
	spools[1].Loan = &store.ActiveLoan{ID: 1, BorrowerID: 2}
	if got := ids(ParseQuery(form("avail", "lent")).Apply(spools)); !eq(got, []int64{2}) {
		t.Errorf("prêtées : %v", got)
	}
	if got := ids(ParseQuery(form("avail", "available")).Apply(spools)); !eq(got, []int64{1}) {
		t.Errorf("disponibles : %v", got)
	}
	if q := ParseQuery(form("avail", "n'importe")); q.Avail != "" {
		t.Error("valeur de disponibilité inconnue acceptée")
	}
}

func TestApplyNearColor(t *testing.T) {
	q := ParseQuery(form("near_on", "1", "near", "#1976d2", "tol", "20"))
	items := q.Apply(sample)
	if len(items) == 0 || items[0].ID != 2 {
		t.Fatalf("le bleu ciel devrait sortir en premier : %v", ids(items))
	}
	for i, it := range items {
		if it.Product.ColorFamily != "blue" {
			t.Errorf("couleur trop éloignée retenue : %+v", it.Product)
		}
		if i > 0 && it.Distance < items[i-1].Distance {
			t.Error("résultats non triés par distance")
		}
	}
	// Sans case cochée, la couleur est ignorée.
	if q := ParseQuery(form("near", "#1976d2")); q.Near != "" {
		t.Error("near ne doit être actif qu'avec near_on=1")
	}
}

func TestQueryRoundTrip(t *testing.T) {
	in := form("q", "noir", "fam", "black", "fam", "grey", "near_on", "1", "near", "#000", "tol", "5", "sort", "brand")
	q := ParseQuery(in)
	again := ParseQuery(q.Values())
	if again.Text != "noir" || len(again.Families) != 2 || again.Near != "#000000" || again.Tolerance != 5 || again.Sort != "brand" {
		t.Errorf("aller-retour incorrect : %+v", again)
	}
}

func TestFacets(t *testing.T) {
	codes := func(f Facets) (fams, mats, brands []string) {
		for _, o := range f.Families {
			fams = append(fams, o.Code)
		}
		for _, o := range f.Materials {
			mats = append(mats, o.Code)
		}
		for _, o := range f.Brands {
			brands = append(brands, o.Code)
		}
		return
	}
	fams, mats, brands := codes(Query{}.Facets(sample))
	// Ordre du catalogue ; la bobine rouge, vide, est ignorée.
	if strings.Join(fams, ",") != "black,orange,blue" || strings.Join(mats, ",") != "PLA,PETG" ||
		strings.Join(brands, ",") != "bambu lab,esun,polymaker,sunlu" {
		t.Errorf("facettes = %v %v %v", fams, mats, brands)
	}

	f := Query{ShowEmpty: true, Families: []string{"green"}, Brands: []string{"prusament"}}.Facets(sample)
	fams, _, brands = codes(f)
	if strings.Join(fams, ",") != "black,red,orange,green,blue" || !slices.Contains(brands, "prusament") {
		t.Errorf("vides et valeurs cochées : %v %v", fams, brands)
	}
	if len(f.Diameters) != 1 || len(f.Finishes) != 1 {
		t.Errorf("diamètres %v, finitions %v", f.Diameters, f.Finishes)
	}
}
