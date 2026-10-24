package color

import (
	"math"
	"sort"
	"testing"
)

// Paires de référence publiées par Sharma, Wu & Dalal (2005).
func TestDeltaE2000Reference(t *testing.T) {
	cases := []struct {
		a, b Lab
		want float64
	}{
		{Lab{50, 2.6772, -79.7751}, Lab{50, 0, -82.7485}, 2.0425},
		{Lab{50, 0, 0}, Lab{50, -1, 2}, 2.3669},
		{Lab{50, 2.49, -0.001}, Lab{50, -2.49, 0.0009}, 7.1792},
		{Lab{50, 2.5, 0}, Lab{73, 25, -18}, 27.1492},
		{Lab{60.2574, -34.0099, 36.2677}, Lab{60.4626, -34.1751, 39.4387}, 1.2644},
		{Lab{2.0776, 0.0795, -1.1350}, Lab{0.9033, -0.0636, -0.5514}, 0.9082},
	}
	for i, c := range cases {
		got := DeltaE2000(c.a, c.b)
		if math.Abs(got-c.want) > 1e-4 {
			t.Errorf("cas %d : ΔE = %.4f, attendu %.4f", i, got, c.want)
		}
		if sym := DeltaE2000(c.b, c.a); math.Abs(sym-got) > 1e-9 {
			t.Errorf("cas %d : non symétrique (%.6f / %.6f)", i, got, sym)
		}
	}
}

func TestLabKnownValues(t *testing.T) {
	white := RGB{255, 255, 255}.Lab()
	if math.Abs(white.L-100) > 0.01 || math.Abs(white.A) > 0.01 || math.Abs(white.B) > 0.01 {
		t.Errorf("blanc = %+v", white)
	}
	red := RGB{255, 0, 0}.Lab()
	if math.Abs(red.L-53.24) > 0.05 || math.Abs(red.A-80.09) > 0.05 || math.Abs(red.B-67.20) > 0.05 {
		t.Errorf("rouge = %+v", red)
	}
}

func TestParseHex(t *testing.T) {
	for in, want := range map[string]string{"#FF8800": "#ff8800", "ff8800": "#ff8800", "#f80": "#ff8800", " #00aaBB ": "#00aabb"} {
		got, err := NormalizeHex(in)
		if err != nil || got != want {
			t.Errorf("NormalizeHex(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "#12", "#gggggg", "#1234567"} {
		if _, err := ParseHex(bad); err == nil {
			t.Errorf("ParseHex(%q) devrait échouer", bad)
		}
	}
}

func TestDistanceOrdering(t *testing.T) {
	navy, _ := ParseHex("#1565c0")
	sky, _ := ParseHex("#1e88e5")
	orange, _ := ParseHex("#ef6c00")
	if Distance(navy, sky) >= Distance(navy, orange) {
		t.Error("deux bleus devraient être plus proches qu'un bleu et un orange")
	}
}

func TestSortKey(t *testing.T) {
	hexes := []string{"#ffffff", "#1565c0", "#c62828", "#1a1a1a", "#2e7d32"}
	sort.Slice(hexes, func(i, j int) bool {
		a, _ := ParseHex(hexes[i])
		b, _ := ParseHex(hexes[j])
		return a.SortKey() < b.SortKey()
	})
	want := []string{"#c62828", "#2e7d32", "#1565c0", "#1a1a1a", "#ffffff"}
	for i := range want {
		if hexes[i] != want[i] {
			t.Fatalf("ordre = %v, attendu %v", hexes, want)
		}
	}
}
