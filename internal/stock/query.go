package stock

import (
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/MaximeBranger/filament_manager/internal/catalog"
	"github.com/MaximeBranger/filament_manager/internal/color"
	"github.com/MaximeBranger/filament_manager/internal/store"
)

// Tolérances de la recherche par couleur proche (ΔE2000).
var Tolerances = []struct {
	Value float64
	Label string
}{
	{5, "Très proche"},
	{10, "Proche"},
	{20, "Large"},
}

const DefaultTolerance = 10

var Sorts = []catalog.Option{
	{Code: "color", Label: "Couleur"},
	{Code: "brand", Label: "Marque"},
	{Code: "date", Label: "Date d'achat"},
	{Code: "level", Label: "Niveau"},
}

// Query décrit une recherche dans le stock. Elle se lit et s'écrit dans l'URL.
type Query struct {
	Scope     string // "" : mon stock, "friends" : tous mes amis, sinon id d'un ami
	Avail     string // "" : toutes, "available" : non prêtées, "lent" : prêtées
	Text      string
	Families  []string
	Materials []string
	Brands    []string // valeurs normalisées (catalog.Fold)
	Finishes  []string
	Levels    []string
	Diameter  string
	ShowEmpty bool
	Near      string // hexa normalisé, vide si pas de recherche par proximité
	Tolerance float64
	Sort      string
}

// ParseQuery lit une recherche depuis les paramètres d'URL.
func ParseQuery(v url.Values) Query {
	q := Query{
		Scope:     v.Get("scope"),
		Avail:     v.Get("avail"),
		Text:      strings.TrimSpace(v.Get("q")),
		Families:  v["fam"],
		Materials: v["mat"],
		Finishes:  v["fin"],
		Levels:    v["lvl"],
		Diameter:  v.Get("dia"),
		ShowEmpty: v.Get("empty") == "1",
		Tolerance: DefaultTolerance,
		Sort:      v.Get("sort"),
	}
	for _, b := range v["brand"] {
		if b = catalog.Fold(b); b != "" {
			q.Brands = append(q.Brands, b)
		}
	}
	if v.Get("near_on") == "1" {
		if hex, err := color.NormalizeHex(v.Get("near")); err == nil {
			q.Near = hex
		}
	}
	if tol, err := strconv.ParseFloat(v.Get("tol"), 64); err == nil {
		for _, t := range Tolerances {
			if t.Value == tol {
				q.Tolerance = tol
			}
		}
	}
	if !slices.ContainsFunc(Sorts, func(o catalog.Option) bool { return o.Code == q.Sort }) {
		q.Sort = "color"
	}
	if q.Avail != "available" && q.Avail != "lent" {
		q.Avail = ""
	}
	return q
}

// Values réécrit la recherche en paramètres d'URL (pour les liens et hx-push-url).
func (q Query) Values() url.Values {
	v := url.Values{}
	if q.Scope != "" {
		v.Set("scope", q.Scope)
	}
	if q.Avail != "" {
		v.Set("avail", q.Avail)
	}
	if q.Text != "" {
		v.Set("q", q.Text)
	}
	v["fam"] = q.Families
	v["mat"] = q.Materials
	v["brand"] = q.Brands
	v["fin"] = q.Finishes
	v["lvl"] = q.Levels
	if q.Diameter != "" {
		v.Set("dia", q.Diameter)
	}
	if q.ShowEmpty {
		v.Set("empty", "1")
	}
	if q.Near != "" {
		v.Set("near_on", "1")
		v.Set("near", q.Near)
		v.Set("tol", strconv.FormatFloat(q.Tolerance, 'f', -1, 64))
	}
	if q.Sort != "color" {
		v.Set("sort", q.Sort)
	}
	for k, vals := range v {
		if len(vals) == 0 {
			delete(v, k)
		}
	}
	return v
}

// Active indique si au moins un filtre restreint la liste.
func (q Query) Active() bool {
	return q.Text != "" || len(q.Families) > 0 || len(q.Materials) > 0 || len(q.Brands) > 0 ||
		len(q.Finishes) > 0 || len(q.Levels) > 0 || q.Diameter != "" || q.Near != "" || q.Avail != ""
}

// Item est une bobine dans un résultat de recherche.
type Item struct {
	store.Spool
	Hex      string  // couleur effective (hexa du produit ou de sa famille)
	Distance float64 // ΔE à la couleur recherchée, -1 sans recherche par proximité
}

// EffectiveHex renvoie l'hexa du produit, ou celui de sa famille à défaut.
func EffectiveHex(p store.Product) string { return p.Hex() }

func haystack(sp store.Spool) string {
	p := sp.Product
	family, _ := catalog.FamilyByCode(p.ColorFamily)
	return catalog.Fold(strings.Join([]string{
		p.Brand, p.ColorName, family.Label, catalog.MaterialLabel(p.Material), p.MaterialOther,
		catalog.FinishLabel(p.Finish), p.FinishOther, p.EAN, sp.Notes, sp.Supplier,
	}, " "))
}

func in(list []string, v string) bool { return len(list) == 0 || slices.Contains(list, v) }

func containsAll(h string, words []string) bool {
	for _, w := range words {
		if !strings.Contains(h, w) {
			return false
		}
	}
	return true
}

// Apply filtre et trie les bobines.
func (q Query) Apply(spools []store.Spool) []Item {
	words := strings.Fields(catalog.Fold(q.Text))
	var target color.RGB
	if q.Near != "" {
		target, _ = color.ParseHex(q.Near)
	}

	out := make([]Item, 0, len(spools))
	for _, sp := range spools {
		p := sp.Product
		if sp.Level == "empty" && !q.ShowEmpty && !slices.Contains(q.Levels, "empty") {
			continue
		}
		if !in(q.Families, p.ColorFamily) || !in(q.Materials, p.Material) || !in(q.Finishes, p.Finish) ||
			!in(q.Levels, sp.Level) || !in(q.Brands, catalog.Fold(p.Brand)) {
			continue
		}
		if q.Diameter != "" && strconv.FormatFloat(p.DiameterMM, 'f', -1, 64) != q.Diameter {
			continue
		}
		if (q.Avail == "available" && sp.Loan != nil) || (q.Avail == "lent" && sp.Loan == nil) {
			continue
		}
		if len(words) > 0 && !containsAll(haystack(sp), words) {
			continue
		}
		it := Item{Spool: sp, Hex: EffectiveHex(p), Distance: -1}
		if q.Near != "" {
			c, err := color.ParseHex(it.Hex)
			if err != nil {
				continue
			}
			if it.Distance = color.Distance(target, c); it.Distance > q.Tolerance {
				continue
			}
		}
		out = append(out, it)
	}
	q.sort(out)
	return out
}

func (q Query) sort(items []Item) {
	colorKey := func(it Item) float64 {
		c, _ := color.ParseHex(it.Hex)
		return c.SortKey()
	}
	var less func(a, b Item) bool
	switch {
	case q.Near != "":
		less = func(a, b Item) bool { return a.Distance < b.Distance }
	case q.Sort == "brand":
		less = func(a, b Item) bool {
			ba, bb := catalog.Fold(a.Product.Brand), catalog.Fold(b.Product.Brand)
			if ba != bb {
				return ba < bb
			}
			return catalog.Fold(a.Product.ColorName) < catalog.Fold(b.Product.ColorName)
		}
	case q.Sort == "date":
		// Plus récentes d'abord, sans date à la fin.
		less = func(a, b Item) bool {
			if (a.PurchaseDate == "") != (b.PurchaseDate == "") {
				return b.PurchaseDate == ""
			}
			return a.PurchaseDate > b.PurchaseDate
		}
	case q.Sort == "level":
		less = func(a, b Item) bool { return catalog.LevelRank(a.Level) < catalog.LevelRank(b.Level) }
	default:
		less = func(a, b Item) bool { return colorKey(a) < colorKey(b) }
	}
	sort.SliceStable(items, func(i, j int) bool { return less(items[i], items[j]) })
}

// Facets liste les valeurs de filtre présentes dans un stock, dans l'ordre du catalogue.
// Les valeurs cochées restent proposées même si plus aucune bobine ne les porte,
// pour pouvoir les décocher.
type Facets struct {
	Families  []catalog.Family
	Materials []catalog.Option
	Brands    []catalog.Option // Code : marque normalisée (catalog.Fold)
	Finishes  []catalog.Option
	Diameters []float64
}

// Facets calcule les filtres utiles pour ces bobines. Les bobines vides ne comptent
// que si la recherche les affiche.
func (q Query) Facets(spools []store.Spool) Facets {
	fams, mats, fins, dias := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	brands := map[string]string{}
	showEmpty := q.ShowEmpty || slices.Contains(q.Levels, "empty")
	for _, sp := range spools {
		if sp.Level == "empty" && !showEmpty {
			continue
		}
		p := sp.Product
		fams[p.ColorFamily] = true
		mats[p.Material] = true
		fins[p.Finish] = true
		dias[strconv.FormatFloat(p.DiameterMM, 'f', -1, 64)] = true
		brands[catalog.Fold(p.Brand)] = p.Brand
	}
	for _, c := range q.Families {
		fams[c] = true
	}
	for _, c := range q.Materials {
		mats[c] = true
	}
	for _, c := range q.Finishes {
		fins[c] = true
	}
	if q.Diameter != "" {
		dias[q.Diameter] = true
	}

	var f Facets
	for _, fam := range catalog.Families {
		if fams[fam.Code] {
			f.Families = append(f.Families, fam)
		}
	}
	keep := func(opts []catalog.Option, present map[string]bool) []catalog.Option {
		var out []catalog.Option
		for _, o := range opts {
			if present[o.Code] {
				out = append(out, o)
			}
		}
		return out
	}
	f.Materials = keep(catalog.Materials, mats)
	f.Finishes = keep(catalog.Finishes, fins)
	for _, d := range catalog.Diameters {
		if dias[strconv.FormatFloat(d, 'f', -1, 64)] {
			f.Diameters = append(f.Diameters, d)
		}
	}
	for _, b := range q.Brands {
		if _, ok := brands[b]; !ok {
			brands[b] = b
		}
	}
	for norm, label := range brands {
		f.Brands = append(f.Brands, catalog.Option{Code: norm, Label: label})
	}
	sort.Slice(f.Brands, func(i, j int) bool { return f.Brands[i].Code < f.Brands[j].Code })
	return f
}
