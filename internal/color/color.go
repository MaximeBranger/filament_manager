// Package color convertit les couleurs hexadécimales et mesure leur proximité perçue (ΔE2000).
package color

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// RGB est une couleur sRGB 8 bits.
type RGB struct{ R, G, B uint8 }

// Lab est une couleur dans l'espace CIELAB (illuminant D65).
type Lab struct{ L, A, B float64 }

var ErrInvalidHex = errors.New("couleur hexadécimale invalide")

// ParseHex accepte "#rrggbb", "rrggbb", "#rgb" ou "rgb".
func ParseHex(s string) (RGB, error) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) == 3 {
		s = string([]byte{s[0], s[0], s[1], s[1], s[2], s[2]})
	}
	if len(s) != 6 {
		return RGB{}, ErrInvalidHex
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return RGB{}, ErrInvalidHex
	}
	return RGB{uint8(v >> 16), uint8(v >> 8), uint8(v)}, nil
}

// NormalizeHex renvoie la forme canonique "#rrggbb" en minuscules.
func NormalizeHex(s string) (string, error) {
	c, err := ParseHex(s)
	if err != nil {
		return "", err
	}
	return c.Hex(), nil
}

func (c RGB) Hex() string { return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B) }

func linearize(v uint8) float64 {
	c := float64(v) / 255
	if c <= 0.04045 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

// Lab convertit sRGB → XYZ (D65) → CIELAB.
func (c RGB) Lab() Lab {
	r, g, b := linearize(c.R), linearize(c.G), linearize(c.B)
	x := (0.4124564*r + 0.3575761*g + 0.1804375*b) / 0.95047
	y := (0.2126729*r + 0.7151522*g + 0.0721750*b) / 1.0
	z := (0.0193339*r + 0.1191920*g + 0.9503041*b) / 1.08883

	const eps = 216.0 / 24389.0
	const kappa = 24389.0 / 27.0
	f := func(t float64) float64 {
		if t > eps {
			return math.Cbrt(t)
		}
		return (kappa*t + 16) / 116
	}
	fx, fy, fz := f(x), f(y), f(z)
	return Lab{L: 116*fy - 16, A: 500 * (fx - fy), B: 200 * (fy - fz)}
}

// HSL renvoie teinte (0–360), saturation et luminosité (0–1).
func (c RGB) HSL() (h, s, l float64) {
	r, g, b := float64(c.R)/255, float64(c.G)/255, float64(c.B)/255
	maxV := math.Max(r, math.Max(g, b))
	minV := math.Min(r, math.Min(g, b))
	l = (maxV + minV) / 2
	d := maxV - minV
	if d == 0 {
		return 0, 0, l
	}
	s = d / (1 - math.Abs(2*l-1))
	switch maxV {
	case r:
		h = math.Mod((g-b)/d, 6)
	case g:
		h = (b-r)/d + 2
	default:
		h = (r-g)/d + 4
	}
	h *= 60
	if h < 0 {
		h += 360
	}
	return h, s, l
}

// SortKey ordonne les couleurs comme un nuancier : les couleurs saturées par teinte,
// puis les neutres (noir → blanc).
func (c RGB) SortKey() float64 {
	h, s, l := c.HSL()
	if s < 0.15 || l < 0.08 || l > 0.95 {
		return 1000 + l
	}
	return h
}

func deg2rad(d float64) float64 { return d * math.Pi / 180 }

func hueAngle(b, a float64) float64 {
	if a == 0 && b == 0 {
		return 0
	}
	h := math.Atan2(b, a) * 180 / math.Pi
	if h < 0 {
		h += 360
	}
	return h
}

// DeltaE2000 calcule l'écart de couleur CIEDE2000 (kL = kC = kH = 1).
func DeltaE2000(c1, c2 Lab) float64 {
	const pow25_7 = 6103515625.0 // 25^7

	cab1 := math.Hypot(c1.A, c1.B)
	cab2 := math.Hypot(c2.A, c2.B)
	cabMean7 := math.Pow((cab1+cab2)/2, 7)
	g := 0.5 * (1 - math.Sqrt(cabMean7/(cabMean7+pow25_7)))

	a1p := (1 + g) * c1.A
	a2p := (1 + g) * c2.A
	c1p := math.Hypot(a1p, c1.B)
	c2p := math.Hypot(a2p, c2.B)
	h1p := hueAngle(c1.B, a1p)
	h2p := hueAngle(c2.B, a2p)

	dLp := c2.L - c1.L
	dCp := c2p - c1p

	var dhp float64
	if c1p*c2p != 0 {
		dhp = h2p - h1p
		if dhp > 180 {
			dhp -= 360
		} else if dhp < -180 {
			dhp += 360
		}
	}
	dHp := 2 * math.Sqrt(c1p*c2p) * math.Sin(deg2rad(dhp/2))

	lMean := (c1.L + c2.L) / 2
	cMean := (c1p + c2p) / 2

	var hMean float64
	switch {
	case c1p*c2p == 0:
		hMean = h1p + h2p
	case math.Abs(h1p-h2p) <= 180:
		hMean = (h1p + h2p) / 2
	case h1p+h2p < 360:
		hMean = (h1p + h2p + 360) / 2
	default:
		hMean = (h1p + h2p - 360) / 2
	}

	t := 1 - 0.17*math.Cos(deg2rad(hMean-30)) +
		0.24*math.Cos(deg2rad(2*hMean)) +
		0.32*math.Cos(deg2rad(3*hMean+6)) -
		0.20*math.Cos(deg2rad(4*hMean-63))
	dTheta := 30 * math.Exp(-math.Pow((hMean-275)/25, 2))
	cMean7 := math.Pow(cMean, 7)
	rc := 2 * math.Sqrt(cMean7/(cMean7+pow25_7))
	lm50 := (lMean - 50) * (lMean - 50)
	sl := 1 + 0.015*lm50/math.Sqrt(20+lm50)
	sc := 1 + 0.045*cMean
	sh := 1 + 0.015*cMean*t
	rt := -math.Sin(deg2rad(2*dTheta)) * rc

	l := dLp / sl
	c := dCp / sc
	h := dHp / sh
	return math.Sqrt(l*l + c*c + h*h + rt*c*h)
}

// Distance renvoie le ΔE2000 entre deux couleurs sRGB.
func Distance(a, b RGB) float64 { return DeltaE2000(a.Lab(), b.Lab()) }
