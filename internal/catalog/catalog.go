// Package catalog définit les référentiels fixes (matériaux, finitions, niveaux, familles de couleur)
// et la normalisation des marques.
package catalog

import (
	"strings"
	"unicode"
)

// Other est la valeur « Autre » des listes à champ libre.
const Other = "OTHER"

type Option struct {
	Code  string
	Label string
}

type Family struct {
	Code  string
	Label string
	Hex   string
}

var Materials = []Option{
	{"PLA", "PLA"},
	{"PLA+", "PLA+"},
	{"PETG", "PETG"},
	{"ABS", "ABS"},
	{"ASA", "ASA"},
	{"TPU", "TPU"},
	{"PA", "PA (nylon)"},
	{"PC", "PC"},
	{"PVA", "PVA"},
	{"HIPS", "HIPS"},
	{Other, "Autre"},
}

var Finishes = []Option{
	{"standard", "Standard"},
	{"matte", "Mat"},
	{"silk", "Silk"},
	{"glossy", "Brillant"},
	{"glitter", "Pailleté"},
	{"marble", "Marbre"},
	{"wood", "Bois"},
	{"carbon", "Fibre de carbone"},
	{"glow", "Phosphorescent"},
	{"multicolor", "Multicolore / dégradé"},
	{Other, "Autre"},
}

// Levels est ordonné du plus plein au plus vide.
var Levels = []Option{
	{"new", "Neuf"},
	{"started", "Entamée"},
	{"half", "Moitié"},
	{"low", "Faible"},
	{"empty", "Vide"},
}

var Families = []Family{
	{"black", "Noir", "#1a1a1a"},
	{"white", "Blanc", "#f5f5f5"},
	{"grey", "Gris", "#8a8a8a"},
	{"red", "Rouge", "#c62828"},
	{"orange", "Orange", "#ef6c00"},
	{"yellow", "Jaune", "#fbc02d"},
	{"green", "Vert", "#2e7d32"},
	{"blue", "Bleu", "#1565c0"},
	{"purple", "Violet", "#6a1b9a"},
	{"pink", "Rose", "#ec407a"},
	{"brown", "Marron", "#6d4c41"},
	{"beige", "Beige", "#d7c4a3"},
	{"transparent", "Transparent", "#e0f2f1"},
	{"multicolor", "Multicolore", "#9e9e9e"},
}

var Diameters = []float64{1.75, 2.85}

var Weights = []int{250, 500, 750, 1000, 2000, 3000}

const (
	DefaultDiameter = 1.75
	DefaultWeight   = 1000
	DefaultLevel    = "new"
	DefaultFinish   = "standard"
)

// SeedBrands est la liste de marques proposée d'office à l'autocomplétion.
var SeedBrands = []string{
	"3DJake", "Amolen", "Anycubic", "Atomic", "Azurefilm", "Bambu Lab", "ColorFabb", "Creality",
	"Das Filament", "Elegoo", "Eryone", "eSun", "Extrudr", "Fiberlogy", "Fillamentum", "Formfutura",
	"Geeetech", "Hatchbox", "Inland", "Jayo", "Kexcelled", "Kingroon", "Overture", "Polymaker",
	"Prusament", "Rosa3D", "Sakata", "Spectrum", "Sunlu", "Ziro",
}

func label(opts []Option, code string) string {
	for _, o := range opts {
		if o.Code == code {
			return o.Label
		}
	}
	return code
}

func valid(opts []Option, code string) bool {
	for _, o := range opts {
		if o.Code == code {
			return true
		}
	}
	return false
}

func MaterialLabel(code string) string { return label(Materials, code) }
func FinishLabel(code string) string   { return label(Finishes, code) }
func LevelLabel(code string) string    { return label(Levels, code) }
func ValidMaterial(code string) bool   { return valid(Materials, code) }
func ValidFinish(code string) bool     { return valid(Finishes, code) }
func ValidLevel(code string) bool      { return valid(Levels, code) }

// LevelRank renvoie la position du niveau (0 = neuf), ou len(Levels) si inconnu.
func LevelRank(code string) int {
	for i, o := range Levels {
		if o.Code == code {
			return i
		}
	}
	return len(Levels)
}

func FamilyByCode(code string) (Family, bool) {
	for _, f := range Families {
		if f.Code == code {
			return f, true
		}
	}
	return Family{}, false
}

func ValidDiameter(d float64) bool {
	for _, v := range Diameters {
		if v == d {
			return true
		}
	}
	return false
}

var foldMap = map[rune]string{
	'à': "a", 'â': "a", 'ä': "a", 'á': "a", 'ã': "a", 'å': "a",
	'ç': "c",
	'é': "e", 'è': "e", 'ê': "e", 'ë': "e",
	'î': "i", 'ï': "i", 'í': "i", 'ì': "i",
	'ô': "o", 'ö': "o", 'ó': "o", 'ò': "o", 'õ': "o", 'ø': "o",
	'ù': "u", 'û': "u", 'ü': "u", 'ú': "u",
	'ÿ': "y", 'ý': "y",
	'ñ': "n",
	'œ': "oe", 'æ': "ae", 'ß': "ss",
}

// Fold met en minuscules, retire les accents et réduit les espaces : sert aux comparaisons
// et à la recherche texte.
func Fold(s string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(s) {
		if unicode.IsSpace(r) {
			space = true
			continue
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		if rep, ok := foldMap[r]; ok {
			b.WriteString(rep)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// CleanBrand nettoie la saisie d'une marque (trim et espaces multiples) sans changer sa casse.
func CleanBrand(s string) string { return strings.Join(strings.Fields(s), " ") }
