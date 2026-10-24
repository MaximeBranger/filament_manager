// Package share prépare les modèles 3D partagés : lecture et validation des STL,
// conversion au format FMM1, mots de passe et fichiers sur disque.
package share

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strconv"
)

// MaxTriangles est le plafond de triangles accepté pour un modèle.
const MaxTriangles = 1_000_000

var (
	ErrNotSTL       = errors.New("ce fichier n'est pas un STL valide")
	ErrEmptyMesh    = errors.New("le modèle ne contient aucun triangle")
	ErrTooManyTris  = fmt.Errorf("le modèle dépasse %d triangles", MaxTriangles)
	ErrInvalidCoord = errors.New("le modèle contient des coordonnées invalides")
)

// Mesh est un maillage non indexé : 9 float32 (3 sommets x, y, z) par triangle, en mm.
type Mesh struct {
	Positions []float32
	Size      [3]float64 // dimensions de la boîte englobante (X, Y, Z)
}

func (m *Mesh) Triangles() int { return len(m.Positions) / 9 }

// ParseSTL lit un STL binaire ou ASCII, le valide et le normalise : recentré en X/Y, Z minimum à 0.
func ParseSTL(data []byte) (*Mesh, error) {
	var m *Mesh
	var err error
	if isBinarySTL(data) {
		m, err = parseBinary(data)
	} else if bytes.HasPrefix(bytes.TrimLeft(data, " \t\r\n"), []byte("solid")) {
		m, err = parseASCII(data)
	} else {
		return nil, ErrNotSTL
	}
	if err != nil {
		return nil, err
	}
	if m.Triangles() == 0 {
		return nil, ErrEmptyMesh
	}
	m.normalize()
	return m, nil
}

// isBinarySTL se fie à la taille : beaucoup de STL binaires commencent aussi par "solid".
func isBinarySTL(data []byte) bool {
	if len(data) < 84 {
		return false
	}
	n := binary.LittleEndian.Uint32(data[80:84])
	return uint64(len(data)) == 84+50*uint64(n)
}

func finite(v float32) bool {
	f := float64(v)
	return !math.IsNaN(f) && !math.IsInf(f, 0)
}

func parseBinary(data []byte) (*Mesh, error) {
	n := int(binary.LittleEndian.Uint32(data[80:84]))
	if n > MaxTriangles {
		return nil, ErrTooManyTris
	}
	pos := make([]float32, 0, n*9)
	for i := range n {
		rec := data[84+i*50:]
		for v := range 9 {
			// 12 premiers octets : normale (ignorée, recalculée côté navigateur).
			f := math.Float32frombits(binary.LittleEndian.Uint32(rec[12+v*4:]))
			if !finite(f) {
				return nil, ErrInvalidCoord
			}
			pos = append(pos, f)
		}
	}
	return &Mesh{Positions: pos}, nil
}

func parseASCII(data []byte) (*Mesh, error) {
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	sc.Split(bufio.ScanWords)
	var pos []float32
	ended := false
	for sc.Scan() {
		switch sc.Text() {
		case "vertex":
			if len(pos) >= MaxTriangles*9 {
				return nil, ErrTooManyTris
			}
			for range 3 {
				if !sc.Scan() {
					return nil, ErrNotSTL
				}
				f, err := strconv.ParseFloat(sc.Text(), 32)
				if err != nil {
					return nil, ErrNotSTL
				}
				if !finite(float32(f)) {
					return nil, ErrInvalidCoord
				}
				pos = append(pos, float32(f))
			}
		case "endsolid":
			ended = true
		}
	}
	if err := sc.Err(); err != nil {
		return nil, ErrNotSTL
	}
	if !ended || len(pos)%9 != 0 {
		return nil, ErrNotSTL
	}
	return &Mesh{Positions: pos}, nil
}

func (m *Mesh) normalize() {
	lo := [3]float32{math.MaxFloat32, math.MaxFloat32, math.MaxFloat32}
	hi := [3]float32{-math.MaxFloat32, -math.MaxFloat32, -math.MaxFloat32}
	for i, v := range m.Positions {
		a := i % 3
		lo[a] = min(lo[a], v)
		hi[a] = max(hi[a], v)
	}
	shift := [3]float32{-(lo[0] + hi[0]) / 2, -(lo[1] + hi[1]) / 2, -lo[2]}
	for i := range m.Positions {
		m.Positions[i] += shift[i%3]
	}
	for a := range 3 {
		m.Size[a] = float64(hi[a] - lo[a])
	}
}

var fmmMagic = []byte("FMM1")

// EncodeFMM produit le format compact servi au navigateur :
// "FMM1", nombre de triangles (uint32 LE), puis les positions float32 LE.
func EncodeFMM(m *Mesh) []byte {
	out := make([]byte, 8+len(m.Positions)*4)
	copy(out, fmmMagic)
	binary.LittleEndian.PutUint32(out[4:], uint32(m.Triangles()))
	for i, v := range m.Positions {
		binary.LittleEndian.PutUint32(out[8+i*4:], math.Float32bits(v))
	}
	return out
}

// DecodeFMM relit un fichier FMM1.
func DecodeFMM(data []byte) (*Mesh, error) {
	if len(data) < 8 || !bytes.Equal(data[:4], fmmMagic) {
		return nil, ErrNotSTL
	}
	n := int(binary.LittleEndian.Uint32(data[4:8]))
	if len(data) != 8+n*36 {
		return nil, ErrNotSTL
	}
	pos := make([]float32, n*9)
	for i := range pos {
		pos[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[8+i*4:]))
	}
	return &Mesh{Positions: pos}, nil
}
