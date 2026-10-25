package share

import (
	"archive/zip"
	"bytes"
	"errors"
	"strings"
	"testing"
)

// zip3MF fabrique une archive 3MF à partir de fichiers {chemin: contenu}.
func zip3MF(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(content))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

const rels3MF = `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Target="/3D/3dmodel.model" Id="rel0" Type="http://schemas.microsoft.com/3dmanufacturing/2013/01/3dmodel"/></Relationships>`

// Triangle rectangle de 10 × 20 dans le plan Z = 0.
const triangleMesh = `<mesh><vertices><vertex x="0" y="0" z="0"/><vertex x="10" y="0" z="0"/><vertex x="0" y="20" z="0"/></vertices>
<triangles><triangle v1="0" v2="1" v3="2"/></triangles></mesh>`

func TestParse3MF(t *testing.T) {
	data := zip3MF(t, map[string]string{
		"_rels/.rels": rels3MF,
		"3D/3dmodel.model": `<?xml version="1.0"?><model unit="centimeter" xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02">
<resources>
  <object id="1" type="model">` + triangleMesh + `</object>
  <object id="2" type="support">` + triangleMesh + `</object>
</resources>
<build>
  <item objectid="1"/>
  <item objectid="1" transform="1 0 0 0 1 0 0 0 1 0 0 3"/>
  <item objectid="2"/>
</build></model>`,
	})
	m, err := Parse(data, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	// Deux instances de l'objet 1 (le support est ignoré), en cm convertis en mm.
	if m.Triangles() != 2 || m.Size != [3]float64{100, 200, 30} {
		t.Errorf("%d triangles, taille %v", m.Triangles(), m.Size)
	}
}

func TestParse3MFComponentsInOtherFiles(t *testing.T) {
	// Structure des 3MF de Bambu Studio : le modèle principal référence des objets d'autres fichiers.
	data := zip3MF(t, map[string]string{
		"_rels/.rels": rels3MF,
		"3D/3dmodel.model": `<model unit="millimeter" xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02"
  xmlns:p="http://schemas.microsoft.com/3dmanufacturing/production/2015/06">
<resources><object id="5"><components>
  <component p:path="/3D/Objects/object_1.model" objectid="1" transform="2 0 0 0 2 0 0 0 2 0 0 0"/>
</components></object></resources>
<build><item objectid="5" transform="1 0 0 0 1 0 0 0 1 100 100 0"/></build></model>`,
		"3D/Objects/object_1.model": `<model unit="millimeter"><resources><object id="1">` + triangleMesh + `</object></resources></model>`,
	})
	m, err := Parse3MF(data, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if m.Triangles() != 1 || m.Size != [3]float64{20, 40, 0} {
		t.Errorf("%d triangles, taille %v", m.Triangles(), m.Size)
	}
}

func TestParse3MFRejects(t *testing.T) {
	model := func(body string) []byte {
		return zip3MF(t, map[string]string{"3D/3dmodel.model": `<model unit="millimeter">` + body + `</model>`})
	}
	cases := map[string]struct {
		data []byte
		want error
	}{
		"pas une archive": {[]byte("PK\x03\x04 tronqué"), ErrNot3MF},
		"sans modèle":     {zip3MF(t, map[string]string{"autre.txt": "x"}), ErrNot3MF},
		"vide":            {model(`<resources/><build/>`), ErrEmptyMesh},
		"indice hors maillage": {model(`<resources><object id="1"><mesh><vertices><vertex x="0" y="0" z="0"/></vertices>
<triangles><triangle v1="0" v2="1" v3="2"/></triangles></mesh></object></resources><build><item objectid="1"/></build>`), ErrNot3MF},
		"coordonnée invalide": {model(`<resources><object id="1"><mesh><vertices><vertex x="NaN" y="0" z="0"/></vertices></mesh></object></resources>`), ErrInvalidCoord},
		"composants circulaires": {model(`<resources><object id="1"><components><component objectid="1"/></components></object></resources>
<build><item objectid="1"/></build>`), ErrNot3MF},
	}
	for name, c := range cases {
		if _, err := Parse(c.data, 1<<20); !errors.Is(err, c.want) {
			t.Errorf("%s : %v, attendu %v", name, err, c.want)
		}
	}

	// Archive piégée : le volume décompressé est plafonné.
	bomb := zip3MF(t, map[string]string{"3D/3dmodel.model": `<model>` + strings.Repeat(" ", 1<<20) + `</model>`})
	if _, err := Parse(bomb, 1<<16); err == nil || !strings.Contains(err.Error(), "trop volumineux") {
		t.Errorf("archive piégée : %v", err)
	}
}

func TestParseFallsBackToSTL(t *testing.T) {
	if m, err := Parse(binarySTL("", cubeCorner), 1<<20); err != nil || m.Triangles() != 1 {
		t.Errorf("STL via Parse : %v", err)
	}
}
