package web

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/MaximeBranger/filament_manager/internal/modelimport"
)

// fakeImporter remplace les sites de modèles : un seul modèle connu, à deux fichiers.
type fakeImporter struct{ files map[string][]byte }

func (f fakeImporter) Resolve(_ context.Context, raw string) (*modelimport.Model, error) {
	if raw != "https://www.printables.com/model/3161" {
		return nil, modelimport.ErrUnsupported
	}
	return &modelimport.Model{
		Source: "Printables", URL: raw, Title: "3D BENCHY", Author: "Prusa Research", License: "CC0",
		Files: []modelimport.File{{ID: "1", Name: "benchy.stl", Size: 1000}, {ID: "2", Name: "benchy.3mf", Size: 2000}},
	}, nil
}

func (f fakeImporter) Download(_ context.Context, m *modelimport.Model, id string) (string, []byte, error) {
	file, ok := m.File(id)
	if !ok {
		return "", nil, modelimport.ErrUnknownFile
	}
	return file.Name, f.files[id], nil
}

// test3MF fabrique un 3MF minimal (un triangle).
func test3MF(t *testing.T) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("3D/3dmodel.model")
	w.Write([]byte(`<model unit="millimeter"><resources><object id="1"><mesh><vertices>
<vertex x="0" y="0" z="0"/><vertex x="10" y="0" z="0"/><vertex x="0" y="10" z="5"/></vertices>
<triangles><triangle v1="0" v2="1" v3="2"/></triangles></mesh></object></resources><build><item objectid="1"/></build></model>`))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestShareImportFromLink(t *testing.T) {
	app := newTestApp(t)
	app.server.importer = fakeImporter{files: map[string][]byte{"1": testSTL(2), "2": test3MF(t)}}
	alice := app.signup("alice@example.fr", "Alice")
	stockForSharing(alice)
	link := "https://www.printables.com/model/3161"

	r := alice.get("/shares/import?url=" + url.QueryEscape(link))
	if r.Status != http.StatusOK || !strings.Contains(r.Body, "benchy.3mf") || !strings.Contains(r.Body, `value="3D BENCHY"`) {
		t.Fatalf("choix du fichier : %d\n%s", r.Status, r.Body)
	}

	r = alice.post("/shares/import", url.Values{"url": {link}, "file": {"2"}, "title": {"Mon Benchy"}})
	m := shareLinkRe.FindStringSubmatch(r.Body)
	if r.Status != http.StatusOK || m == nil || !strings.Contains(r.Body, "Importé de Printables") {
		t.Fatalf("import : %d\n%s", r.Status, r.Body)
	}
	// Le destinataire voit l'attribution du modèle.
	if r := app.newClient().get(m[1]); !strings.Contains(r.Body, "Mon Benchy") || !strings.Contains(r.Body, "par Prusa Research") ||
		!strings.Contains(r.Body, "licence CC0") {
		t.Errorf("attribution absente de la page du destinataire :\n%s", r.Body)
	}

	// Lien non pris en charge : message sur le formulaire, lien conservé.
	r = alice.get("/shares/import?url=" + url.QueryEscape("https://exemple.fr/page"))
	if r.Status != http.StatusUnprocessableEntity || !strings.Contains(r.Body, "Lien non reconnu") || !strings.Contains(r.Body, "https://exemple.fr/page") {
		t.Errorf("lien non pris en charge : %d", r.Status)
	}
	// Fichier hors du modèle.
	if r := alice.post("/shares/import", url.Values{"url": {link}, "file": {"9"}}); !strings.Contains(r.Body, "ne fait pas partie du modèle") {
		t.Errorf("fichier inconnu : %d", r.Status)
	}
}

func TestShareUpload3MF(t *testing.T) {
	app := newTestApp(t)
	alice := app.signup("alice@example.fr", "Alice")
	r := alice.upload("/shares", nil, "piece.3mf", test3MF(t))
	if r.Status != http.StatusOK || !shareLinkRe.MatchString(r.Body) {
		t.Fatalf("envoi d'un 3MF : %d\n%s", r.Status, r.Body)
	}
}
