package modelimport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

// fakeSites simule l'API Printables, l'API Thingiverse et un hébergeur de fichiers.
func fakeSites(t *testing.T) (*httptest.Server, *Importer) {
	t.Helper()
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("POST /graphql/", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query     string            `json:"query"`
			Variables map[string]string `json:"variables"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		switch {
		case strings.Contains(req.Query, "getDownloadLink"):
			io.WriteString(w, `{"data":{"getDownloadLink":{"ok":true,"output":{"link":"`+srv.URL+`/files/`+req.Variables["id"]+`"}}}}`)
		case req.Variables["id"] == "3161":
			io.WriteString(w, `{"data":{"model":{"id":"3161","name":"3D BENCHY","premium":false,"price":null,
				"license":{"name":"CC0"},"user":{"publicUsername":"Prusa Research"},
				"stls":[{"id":"1","name":"benchy.stl","fileSize":5},{"id":"2","name":"benchy.step","fileSize":9},{"id":"3","name":"benchy.3mf","fileSize":9999999}]}}}`)
		case req.Variables["id"] == "7":
			io.WriteString(w, `{"data":{"model":{"id":"7","name":"Payant","premium":true,"stls":[{"id":"1","name":"a.stl"}]}}}`)
		default:
			io.WriteString(w, `{"data":{"model":null}}`)
		}
	})
	mux.HandleFunc("GET /files/{id}", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "solid "+r.PathValue("id")) })
	mux.HandleFunc("GET /things/42", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer jeton" {
			http.Error(w, "", http.StatusUnauthorized)
			return
		}
		io.WriteString(w, `{"id":42,"name":"Vase","license":"Creative Commons - Attribution","creator":{"name":"alice"}}`)
	})
	mux.HandleFunc("GET /things/42/files", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `[{"id":100,"name":"vase.STL","size":12},{"id":101,"name":"notice.pdf","size":3}]`)
	})
	mux.HandleFunc("GET /files/100/download", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, srv.URL+"/files/vase", http.StatusFound)
	})
	mux.HandleFunc("GET /dl/piece.stl", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "solid direct") })
	srv = httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)

	im := New("jeton", 1<<20, "test")
	im.client = newClient(true)
	im.client.Transport.(*http.Transport).TLSClientConfig = srv.Client().Transport.(*http.Transport).TLSClientConfig
	im.printablesAPI = srv.URL + "/graphql/"
	im.thingiverseAPI = srv.URL
	return srv, im
}

func TestPrintables(t *testing.T) {
	_, im := fakeSites(t)
	ctx := context.Background()
	m, err := im.Resolve(ctx, "https://www.printables.com/fr/model/3161-3d-benchy/files")
	if err != nil {
		t.Fatal(err)
	}
	if m.Title != "3D BENCHY" || m.Author != "Prusa Research" || m.License != "CC0" || len(m.Files) != 2 {
		t.Fatalf("modèle = %+v", m)
	}
	name, data, err := im.Download(ctx, m, "1")
	if err != nil || name != "benchy.stl" || string(data) != "solid 1" {
		t.Errorf("téléchargement : %q %q %v", name, data, err)
	}
	if _, _, err := im.Download(ctx, m, "3"); !errors.Is(err, ErrTooLarge) {
		t.Errorf("fichier annoncé trop gros : %v", err)
	}
	if _, _, err := im.Download(ctx, m, "2"); !errors.Is(err, ErrUnknownFile) {
		t.Errorf("fichier hors liste : %v", err)
	}
	if _, err := im.Resolve(ctx, "https://www.printables.com/model/7-payant"); !errors.Is(err, ErrPaid) {
		t.Errorf("modèle payant : %v", err)
	}
	if _, err := im.Resolve(ctx, "https://printables.com/model/8"); !errors.Is(err, ErrNotFound) {
		t.Errorf("modèle absent : %v", err)
	}
}

func TestThingiverse(t *testing.T) {
	_, im := fakeSites(t)
	ctx := context.Background()
	m, err := im.Resolve(ctx, "https://www.thingiverse.com/thing:42/files")
	if err != nil {
		t.Fatal(err)
	}
	if m.Title != "Vase" || m.Author != "alice" || len(m.Files) != 1 || m.Files[0].ID != "100" {
		t.Fatalf("modèle = %+v", m)
	}
	if _, data, err := im.Download(ctx, m, "100"); err != nil || string(data) != "solid vase" {
		t.Errorf("téléchargement après redirection : %q %v", data, err)
	}
	im.thingiverseToken = ""
	if _, err := im.Resolve(ctx, "https://www.thingiverse.com/thing:42"); !errors.Is(err, ErrNoThingiverse) {
		t.Errorf("sans jeton : %v", err)
	}
}

func TestDirectLink(t *testing.T) {
	srv, im := fakeSites(t)
	ctx := context.Background()
	m, err := im.Resolve(ctx, srv.URL+"/dl/piece.stl")
	if err != nil || m.Title != "piece" {
		t.Fatalf("lien direct : %+v %v", m, err)
	}
	if _, data, err := im.Download(ctx, m, "0"); err != nil || string(data) != "solid direct" {
		t.Errorf("téléchargement direct : %q %v", data, err)
	}
	im.maxSize = 5
	if _, _, err := im.Download(ctx, m, "0"); !errors.Is(err, ErrTooLarge) {
		t.Errorf("plafond de taille : %v", err)
	}
	for _, raw := range []string{"http://exemple.fr/piece.stl", "https://exemple.fr/notice.pdf", "ftp://x/y.stl", "pas un lien", "https://www.printables.com/fr/collections/1"} {
		if _, err := im.Resolve(ctx, raw); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s : %v", raw, err)
		}
	}
}

func TestPrivateAddressesRefused(t *testing.T) {
	for addr, public := range map[string]bool{
		"93.184.216.34": true, "2606:4700::1111": true,
		"127.0.0.1": false, "10.1.2.3": false, "192.168.1.10": false, "172.16.0.1": false, "169.254.169.254": false,
		"100.64.0.1": false, "0.0.0.0": false, "::1": false, "fd00::1": false, "fe80::1": false, "::ffff:127.0.0.1": false,
	} {
		if got := publicAddr(netip.MustParseAddr(addr)); got != public {
			t.Errorf("%s : public = %v", addr, got)
		}
	}
	// Le vrai client refuse de se connecter au serveur local.
	srv, _ := fakeSites(t)
	im := New("", 1<<20, "test")
	if _, err := im.Resolve(context.Background(), srv.URL+"/dl/piece.stl"); err != nil {
		t.Fatal(err)
	}
	m, _ := im.Resolve(context.Background(), srv.URL+"/dl/piece.stl")
	if _, _, err := im.Download(context.Background(), m, "0"); !errors.Is(err, errForbiddenAddress) {
		t.Errorf("connexion à 127.0.0.1 : %v", err)
	}
}
