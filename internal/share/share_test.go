package share

import (
	"encoding/binary"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// binarySTL fabrique un STL binaire à partir de triangles (9 coordonnées chacun).
func binarySTL(header string, tris ...[9]float32) []byte {
	data := make([]byte, 84+50*len(tris))
	copy(data, header)
	binary.LittleEndian.PutUint32(data[80:], uint32(len(tris)))
	for i, t := range tris {
		for v, f := range t {
			binary.LittleEndian.PutUint32(data[84+i*50+12+v*4:], math.Float32bits(f))
		}
	}
	return data
}

var cubeCorner = [9]float32{10, 20, 5, 30, 20, 5, 10, 60, 15}

func TestParseBinary(t *testing.T) {
	// L'en-tête commence par "solid" : la taille doit primer sur ce préfixe trompeur.
	m, err := ParseSTL(binarySTL("solid trompeur", cubeCorner, [9]float32{30, 60, 15, 10, 60, 15, 30, 20, 5}))
	if err != nil {
		t.Fatal(err)
	}
	if m.Triangles() != 2 {
		t.Fatalf("%d triangles", m.Triangles())
	}
	if m.Size != [3]float64{20, 40, 10} {
		t.Errorf("dimensions = %v", m.Size)
	}
	// Recentré en X/Y, Z min à 0.
	if m.Positions[0] != -10 || m.Positions[1] != -20 || m.Positions[2] != 0 {
		t.Errorf("premier sommet = %v", m.Positions[:3])
	}
}

func TestParseASCII(t *testing.T) {
	src := `solid cube
  facet normal 0 0 1
    outer loop
      vertex 0 0 0
      vertex 10 0 0
      vertex 0 10 2.5e0
    endloop
  endfacet
endsolid cube`
	m, err := ParseSTL([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if m.Triangles() != 1 || m.Size != [3]float64{10, 10, 2.5} {
		t.Errorf("maillage : %d triangles, %v", m.Triangles(), m.Size)
	}
}

func TestParseRejects(t *testing.T) {
	nan := float32(math.NaN())
	inf := float32(math.Inf(1))
	truncated := binarySTL("", cubeCorner)
	truncated = truncated[:len(truncated)-1]
	tooMany := make([]byte, 84)
	binary.LittleEndian.PutUint32(tooMany[80:], MaxTriangles+1)

	cases := []struct {
		name string
		data []byte
		want error
	}{
		{"vide", nil, ErrNotSTL},
		{"texte quelconque", []byte("bonjour"), ErrNotSTL},
		{"binaire tronqué", truncated, ErrNotSTL},
		{"zéro triangle", binarySTL(""), ErrEmptyMesh},
		{"NaN", binarySTL("", [9]float32{nan, 0, 0, 1, 0, 0, 0, 1, 0}), ErrInvalidCoord},
		{"Inf", binarySTL("", [9]float32{0, 0, 0, inf, 0, 0, 0, 1, 0}), ErrInvalidCoord},
		{"ASCII sans endsolid", []byte("solid x\nvertex 0 0 0\nvertex 1 0 0\nvertex 0 1 0\n"), ErrNotSTL},
		{"ASCII incomplet", []byte("solid x\nvertex 0 0 0\nvertex 1 0 0\nendsolid"), ErrNotSTL},
		{"ASCII nan", []byte("solid x\nvertex nan 0 0\nvertex 1 0 0\nvertex 0 1 0\nendsolid"), ErrInvalidCoord},
		{"ASCII vide", []byte("solid x\nendsolid x"), ErrEmptyMesh},
	}
	for _, c := range cases {
		if _, err := ParseSTL(c.data); !errors.Is(err, c.want) {
			t.Errorf("%s : %v, attendu %v", c.name, err, c.want)
		}
	}
	// Le plafond est vérifié avant toute lecture (fichier de la bonne taille simulée).
	big := make([]byte, 84+50*(MaxTriangles+1))
	binary.LittleEndian.PutUint32(big[80:], MaxTriangles+1)
	if _, err := ParseSTL(big); !errors.Is(err, ErrTooManyTris) {
		t.Errorf("plafond de triangles : %v", err)
	}
}

func TestFMMRoundTrip(t *testing.T) {
	m, _ := ParseSTL(binarySTL("", cubeCorner))
	data := EncodeFMM(m)
	if string(data[:4]) != "FMM1" || len(data) != 8+36 {
		t.Fatalf("format FMM1 inattendu : %d octets", len(data))
	}
	back, err := DecodeFMM(data)
	if err != nil || back.Triangles() != 1 || back.Positions[3] != m.Positions[3] {
		t.Errorf("aller-retour : %v", err)
	}
	if _, err := DecodeFMM(data[:len(data)-4]); err == nil {
		t.Error("FMM tronqué accepté")
	}
}

func TestPassword(t *testing.T) {
	Iterations = 1000
	hash, err := HashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "pbkdf2-sha256$1000$") {
		t.Errorf("format : %s", hash)
	}
	if !VerifyPassword("correct horse", hash) || VerifyPassword("Correct horse", hash) || VerifyPassword("x", "n'importe quoi") {
		t.Error("vérification incorrecte")
	}
	other, _ := HashPassword("correct horse")
	if other == hash {
		t.Error("le sel doit rendre chaque empreinte unique")
	}
}

func TestFiles(t *testing.T) {
	dir := t.TempDir()
	key := NewStorageKey()
	if err := Write(dir, key, []byte("data")); err != nil {
		t.Fatal(err)
	}
	if err := Write(dir, "../evil.fmm", nil); err == nil {
		t.Error("clé de stockage hors format acceptée")
	}
	os.WriteFile(filepath.Join(dir, "orphelin.fmm"), nil, 0o600)
	n, err := CleanOrphans(dir, map[string]bool{key: true})
	if err != nil || n != 1 {
		t.Errorf("orphelins : %d, %v", n, err)
	}
	if _, err := os.Stat(filepath.Join(dir, key)); err != nil {
		t.Error("fichier connu supprimé")
	}
	if err := Remove(dir, key); err != nil {
		t.Error(err)
	}
	if err := Remove(dir, key); err != nil {
		t.Error("supprimer un fichier absent ne doit pas échouer")
	}
}
