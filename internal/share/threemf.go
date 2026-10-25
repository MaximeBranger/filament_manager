package share

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"math"
	pathpkg "path"
	"strconv"
	"strings"
)

var ErrNot3MF = errors.New("ce fichier n'est pas un 3MF valide")

// maxComponentDepth borne l'imbrication des composants (et coupe les références circulaires).
const maxComponentDepth = 8

// Parse lit un modèle STL ou 3MF (reconnu à sa signature zip). maxUnzipped plafonne le volume
// décompressé d'un 3MF : une archive piégée ne peut pas saturer la mémoire.
func Parse(data []byte, maxUnzipped int64) (*Mesh, error) {
	if bytes.HasPrefix(data, []byte("PK\x03\x04")) {
		return Parse3MF(data, maxUnzipped)
	}
	return ParseSTL(data)
}

// Parse3MF extrait la géométrie d'un 3MF (couleurs et matériaux ignorés) : objets de la
// section build, composants et transformations appliqués, unités converties en mm.
func Parse3MF(data []byte, maxUnzipped int64) (*Mesh, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, ErrNot3MF
	}
	r := &reader3mf{files: map[string]*zip.File{}, models: map[string]*model3mf{}, budget: maxUnzipped}
	for _, f := range zr.File {
		r.files[strings.TrimPrefix(f.Name, "/")] = f
	}
	root, err := r.rootPath()
	if err != nil {
		return nil, err
	}
	m, err := r.model(root)
	if err != nil {
		return nil, err
	}
	items := m.build
	if len(items) == 0 {
		// Section build absente : on retient tous les objets du modèle principal.
		for id := range m.objects {
			items = append(items, component{objectID: id, transform: identity})
		}
	}
	for _, it := range items {
		file := root
		if it.path != "" {
			file = it.path
		}
		t := it.transform
		for k := 9; k < 12; k++ {
			t[k] *= m.scale // translations dans l'unité du modèle principal
		}
		if err := r.emit(file, it.objectID, t, 0); err != nil {
			return nil, err
		}
	}
	if len(r.out) == 0 {
		return nil, ErrEmptyMesh
	}
	mesh := &Mesh{Positions: r.out}
	mesh.normalize()
	return mesh, nil
}

// transform est une matrice 3MF 4×3 : x' = x·m0 + y·m3 + z·m6 + m9, etc.
type transform [12]float64

var identity = transform{1, 0, 0, 0, 1, 0, 0, 0, 1, 0, 0, 0}

func (t transform) apply(x, y, z float64) (float64, float64, float64) {
	return x*t[0] + y*t[3] + z*t[6] + t[9], x*t[1] + y*t[4] + z*t[7] + t[10], x*t[2] + y*t[5] + z*t[8] + t[11]
}

// then compose : appliquer t puis u.
func (t transform) then(u transform) transform {
	var o transform
	for row := range 4 {
		for col := range 3 {
			v := 0.0
			for k := range 3 {
				v += t[row*3+k] * u[k*3+col]
			}
			if row == 3 {
				v += u[9+col]
			}
			o[row*3+col] = v
		}
	}
	return o
}

func parseTransform(s string) (transform, error) {
	if s == "" {
		return identity, nil
	}
	fields := strings.Fields(s)
	if len(fields) != 12 {
		return identity, ErrNot3MF
	}
	var t transform
	for i, f := range fields {
		v, err := strconv.ParseFloat(f, 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			return identity, ErrNot3MF
		}
		t[i] = v
	}
	return t, nil
}

type component struct {
	objectID  string
	path      string // fichier modèle d'un autre objet (extension Production), vide = même fichier
	transform transform
}

type object3mf struct {
	vertices   []float64
	triangles  []int
	components []component
}

type model3mf struct {
	scale   float64 // facteur vers le millimètre
	objects map[string]*object3mf
	build   []component
}

type reader3mf struct {
	files  map[string]*zip.File
	models map[string]*model3mf
	budget int64 // octets décompressibles restants
	out    []float32
}

// rootPath lit la relation « 3dmodel » de l'archive, à défaut l'emplacement conventionnel.
func (r *reader3mf) rootPath() (string, error) {
	if f, ok := r.files["_rels/.rels"]; ok {
		data, err := r.read(f)
		if err != nil {
			return "", err
		}
		var rels struct {
			Items []struct {
				Target string `xml:"Target,attr"`
				Type   string `xml:"Type,attr"`
			} `xml:"Relationship"`
		}
		if xml.Unmarshal(data, &rels) == nil {
			for _, it := range rels.Items {
				if strings.HasSuffix(it.Type, "/3dmodel") {
					return cleanPath(it.Target), nil
				}
			}
		}
	}
	if _, ok := r.files["3D/3dmodel.model"]; ok {
		return "3D/3dmodel.model", nil
	}
	return "", ErrNot3MF
}

func cleanPath(p string) string { return strings.TrimPrefix(pathpkg.Clean("/"+p), "/") }

func (r *reader3mf) read(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, ErrNot3MF
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, r.budget+1))
	if err != nil {
		return nil, ErrNot3MF
	}
	if int64(len(data)) > r.budget {
		return nil, errors.New("le 3MF décompressé est trop volumineux")
	}
	r.budget -= int64(len(data))
	return data, nil
}

func (r *reader3mf) model(name string) (*model3mf, error) {
	if m, ok := r.models[name]; ok {
		return m, nil
	}
	f, ok := r.files[name]
	if !ok {
		return nil, ErrNot3MF
	}
	data, err := r.read(f)
	if err != nil {
		return nil, err
	}
	m, err := decodeModel(data)
	if err != nil {
		return nil, err
	}
	r.models[name] = m
	return m, nil
}

var unitScale = map[string]float64{
	"": 1, "millimeter": 1, "micron": 0.001, "centimeter": 10, "inch": 25.4, "foot": 304.8, "meter": 1000,
}

// decodeModel lit un fichier .model en flux : les maillages peuvent compter des millions de sommets.
func decodeModel(data []byte) (*model3mf, error) {
	m := &model3mf{scale: 1, objects: map[string]*object3mf{}}
	dec := xml.NewDecoder(bytes.NewReader(data))
	var cur *object3mf
	seenModel := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, ErrNot3MF
		}
		el, ok := tok.(xml.StartElement)
		if !ok {
			if end, ok := tok.(xml.EndElement); ok && end.Name.Local == "object" {
				cur = nil
			}
			continue
		}
		switch el.Name.Local {
		case "model":
			seenModel = true
			s, ok := unitScale[attr(el, "unit")]
			if !ok {
				return nil, ErrNot3MF
			}
			m.scale = s
		case "object":
			switch attr(el, "type") {
			case "", "model", "solidsupport":
				cur = &object3mf{}
				m.objects[attr(el, "id")] = cur
			default:
				cur = nil // supports et surfaces : non imprimés comme pièce
			}
		case "vertex":
			if cur == nil {
				continue
			}
			if len(cur.vertices) >= 3*3*MaxTriangles {
				return nil, ErrTooManyTris
			}
			for _, a := range [3]string{"x", "y", "z"} {
				v, err := strconv.ParseFloat(attr(el, a), 64)
				if err != nil {
					return nil, ErrNot3MF
				}
				if math.IsNaN(v) || math.IsInf(v, 0) {
					return nil, ErrInvalidCoord
				}
				cur.vertices = append(cur.vertices, v)
			}
		case "triangle":
			if cur == nil {
				continue
			}
			if len(cur.triangles) >= 3*MaxTriangles {
				return nil, ErrTooManyTris
			}
			for _, a := range [3]string{"v1", "v2", "v3"} {
				i, err := strconv.Atoi(attr(el, a))
				if err != nil || i < 0 {
					return nil, ErrNot3MF
				}
				cur.triangles = append(cur.triangles, i)
			}
		case "component", "item":
			t, err := parseTransform(attr(el, "transform"))
			if err != nil {
				return nil, err
			}
			c := component{objectID: attr(el, "objectid"), transform: t}
			if p := attr(el, "path"); p != "" {
				c.path = cleanPath(p)
			}
			if el.Name.Local == "item" {
				m.build = append(m.build, c)
			} else if cur != nil {
				cur.components = append(cur.components, c)
			}
		}
	}
	if !seenModel {
		return nil, ErrNot3MF
	}
	return m, nil
}

// attr renvoie un attribut par son nom local (les espaces de noms des extensions varient).
func attr(el xml.StartElement, name string) string {
	for _, a := range el.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

// emit ajoute à la sortie les triangles d'un objet et de ses composants, transformés.
func (r *reader3mf) emit(file, objectID string, t transform, depth int) error {
	if depth > maxComponentDepth {
		return ErrNot3MF
	}
	m, err := r.model(file)
	if err != nil {
		return err
	}
	obj, ok := m.objects[objectID]
	if !ok {
		return nil // objet de support ou de type ignoré
	}
	nv := len(obj.vertices) / 3
	for i := 0; i+2 < len(obj.triangles); i += 3 {
		if len(r.out) >= MaxTriangles*9 {
			return ErrTooManyTris
		}
		for _, vi := range obj.triangles[i : i+3] {
			if vi >= nv {
				return ErrNot3MF
			}
			v := obj.vertices[vi*3 : vi*3+3]
			x, y, z := t.apply(v[0]*m.scale, v[1]*m.scale, v[2]*m.scale)
			for _, c := range [3]float64{x, y, z} {
				if f := float32(c); !finite(f) {
					return ErrInvalidCoord
				}
				r.out = append(r.out, float32(c))
			}
		}
	}
	for _, c := range obj.components {
		next := file
		if c.path != "" {
			next = c.path
		}
		// Les translations des composants sont dans l'unité du fichier courant.
		ct := c.transform
		for k := 9; k < 12; k++ {
			ct[k] *= m.scale
		}
		if err := r.emit(next, c.objectID, ct.then(t), depth+1); err != nil {
			return err
		}
	}
	return nil
}
