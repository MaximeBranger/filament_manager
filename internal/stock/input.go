// Package stock contient la logique métier pure du stock : validation des saisies,
// recherche, filtres et tri.
package stock

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/MaximeBranger/filament_manager/internal/catalog"
	"github.com/MaximeBranger/filament_manager/internal/color"
	"github.com/MaximeBranger/filament_manager/internal/store"
)

// Errors associe un nom de champ de formulaire à un message d'erreur.
type Errors map[string]string

func (e Errors) Any() bool { return len(e) > 0 }

const (
	maxText     = 200
	maxNotes    = 2000
	MaxQuantity = 50
)

var eanPattern = regexp.MustCompile(`^[0-9A-Za-z-]{4,32}$`)

func text(v url.Values, key string, max int, errs Errors) string {
	s := strings.TrimSpace(v.Get(key))
	if len([]rune(s)) > max {
		errs[key] = "Texte trop long."
	}
	return s
}

// ParseProduct lit et valide les champs produit d'un formulaire.
// La marque est seulement nettoyée : sa graphie de référence est résolue par le store.
func ParseProduct(v url.Values) (store.ProductFields, Errors) {
	errs := Errors{}
	p := store.ProductFields{
		Brand:       catalog.CleanBrand(text(v, "brand", maxText, errs)),
		Material:    v.Get("material"),
		ColorName:   text(v, "color_name", maxText, errs),
		ColorFamily: v.Get("color_family"),
		Finish:      v.Get("finish"),
		EAN:         strings.ReplaceAll(strings.TrimSpace(v.Get("ean")), " ", ""),
	}
	if p.Brand == "" {
		errs["brand"] = "La marque est obligatoire."
	}
	if !catalog.ValidMaterial(p.Material) {
		errs["material"] = "Choisis un matériau."
	}
	if p.Material == catalog.Other {
		if p.MaterialOther = text(v, "material_other", maxText, errs); p.MaterialOther == "" {
			errs["material_other"] = "Précise le matériau."
		}
	}
	if p.ColorName == "" {
		errs["color_name"] = "Le nom de la couleur est obligatoire."
	}
	family, ok := catalog.FamilyByCode(p.ColorFamily)
	if !ok {
		errs["color_family"] = "Choisis une famille de couleur."
	}
	if raw := strings.TrimSpace(v.Get("color_hex")); raw != "" {
		hex, err := color.NormalizeHex(raw)
		switch {
		case err != nil:
			errs["color_hex"] = "Couleur invalide."
		case ok && hex == family.Hex:
			// Identique à la couleur par défaut de la famille : on ne la fige pas.
		default:
			p.ColorHex = hex
		}
	}
	// Seconde teinte : multicolores seulement ; la valeur par défaut du sélecteur (celle de la famille) vaut absence.
	if raw := strings.TrimSpace(v.Get("color_hex2")); raw != "" && p.ColorFamily == "multicolor" {
		hex, err := color.NormalizeHex(raw)
		switch {
		case err != nil:
			errs["color_hex2"] = "Couleur invalide."
		case hex != family.Hex:
			p.ColorHex2 = hex
		}
	}
	if p.Finish == "" {
		p.Finish = catalog.DefaultFinish
	}
	if !catalog.ValidFinish(p.Finish) {
		errs["finish"] = "Finition inconnue."
	}
	if p.Finish == catalog.Other {
		if p.FinishOther = text(v, "finish_other", maxText, errs); p.FinishOther == "" {
			errs["finish_other"] = "Précise la finition."
		}
	}

	p.DiameterMM = catalog.DefaultDiameter
	if raw := v.Get("diameter"); raw != "" {
		d, err := strconv.ParseFloat(raw, 64)
		if err != nil || !catalog.ValidDiameter(d) {
			errs["diameter"] = "Diamètre invalide."
		} else {
			p.DiameterMM = d
		}
	}
	p.NominalWeightG = catalog.DefaultWeight
	if raw := strings.TrimSpace(v.Get("weight")); raw != "" {
		w, err := strconv.Atoi(raw)
		if err != nil || w < 1 || w > 20000 {
			errs["weight"] = "Poids invalide (en grammes)."
		} else {
			p.NominalWeightG = w
		}
	}
	if p.EAN != "" && !eanPattern.MatchString(p.EAN) {
		errs["ean"] = "Code-barre invalide."
	}
	return p, errs
}

// ParseSpool lit et valide les champs bobine d'un formulaire.
func ParseSpool(v url.Values) (store.SpoolFields, Errors) {
	errs := Errors{}
	s := store.SpoolFields{
		Level:    v.Get("level"),
		Supplier: text(v, "supplier", maxText, errs),
		Notes:    text(v, "notes", maxNotes, errs),
		Hidden:   v.Get("hidden") != "",
	}
	if s.Level == "" {
		s.Level = catalog.DefaultLevel
	}
	if !catalog.ValidLevel(s.Level) {
		errs["level"] = "Niveau inconnu."
	}
	if raw := strings.TrimSpace(v.Get("purchase_date")); raw != "" {
		if _, err := time.Parse("2006-01-02", raw); err != nil {
			errs["purchase_date"] = "Date invalide."
		} else {
			s.PurchaseDate = raw
		}
	}
	if raw := strings.TrimSpace(v.Get("price")); raw != "" {
		cents, err := ParsePrice(raw)
		if err != nil {
			errs["price"] = "Prix invalide."
		} else {
			s.PriceCents = &cents
		}
	}
	return s, errs
}

// ParseQuantity lit le nombre de bobines à créer (1 par défaut).
func ParseQuantity(v url.Values, errs Errors) int {
	raw := strings.TrimSpace(v.Get("quantity"))
	if raw == "" {
		return 1
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > MaxQuantity {
		errs["quantity"] = "Quantité entre 1 et " + strconv.Itoa(MaxQuantity) + "."
		return 1
	}
	return n
}

// ParsePrice convertit "12,99" ou "12.99" (euros) en centimes.
func ParsePrice(raw string) (int64, error) {
	raw = strings.ReplaceAll(strings.TrimSuffix(strings.TrimSpace(raw), "€"), ",", ".")
	f, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || f < 0 || f > 100000 {
		return 0, strconv.ErrSyntax
	}
	return int64(f*100 + 0.5), nil
}

// FormatPrice affiche des centimes en euros, à la française.
func FormatPrice(cents int64) string {
	return strings.Replace(strconv.FormatFloat(float64(cents)/100, 'f', 2, 64), ".", ",", 1)
}
