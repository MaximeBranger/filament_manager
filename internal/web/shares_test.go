package web

import (
	"bytes"
	"context"
	"encoding/binary"
	"math"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MaximeBranger/filament_manager/internal/share"
)

func init() { share.Iterations = 1000 } // PBKDF2 rapide en test

var shareLinkRe = regexp.MustCompile(`http://filaments\.test(/share/[A-Za-z0-9_-]+)`)

// testSTL fabrique un petit STL binaire de n triangles.
func testSTL(n int) []byte {
	data := make([]byte, 84+50*n)
	binary.LittleEndian.PutUint32(data[80:], uint32(n))
	for i := range n {
		tri := [9]float32{0, 0, 0, 20, 0, 0, 0, 10, 5 + float32(i)}
		for v, f := range tri {
			binary.LittleEndian.PutUint32(data[84+i*50+12+v*4:], math.Float32bits(f))
		}
	}
	return data
}

func (c *client) upload(path string, fields map[string]string, filename string, data []byte) response {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		mw.WriteField(k, v)
	}
	if filename != "" {
		fw, _ := mw.CreateFormFile("file", filename)
		fw.Write(data)
	}
	mw.Close()
	req, _ := http.NewRequest(http.MethodPost, c.app.srv.URL+path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return c.do(req)
}

// createShare envoie un STL et renvoie le chemin public du partage.
func (c *client) createShare(title, password string) string {
	t := c.app.t
	t.Helper()
	r := c.upload("/shares", map[string]string{"title": title, "password": password}, "benchy.stl", testSTL(3))
	m := shareLinkRe.FindStringSubmatch(r.Body)
	if r.Status != http.StatusOK || m == nil {
		t.Fatalf("création du partage : %d\n%s", r.Status, r.Body)
	}
	return m[1]
}

func shareFiles(t *testing.T, app *testApp) []string {
	t.Helper()
	entries, _ := os.ReadDir(app.cfg.ShareDir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// stockForSharing crée chez Alice : rouge (silk, 2 bobines), bleu, une vide et une masquée.
func stockForSharing(c *client) {
	c.post("/spools", newProductForm("color_name", "Rouge Feu", "color_family", "red", "color_hex", "#c62828", "finish", "silk", "quantity", "2", "price", "19,99", "notes", "note privée"))
	c.post("/spools", newProductForm("color_name", "Bleu Nuit", "color_family", "blue", "color_hex", "#152a6b"))
	c.post("/spools", newProductForm("color_name", "CouleurVide", "level", "empty"))
	c.post("/spools", newProductForm("color_name", "CouleurMasquee", "hidden", "on"))
}

func TestShareCreateViewAndChoose(t *testing.T) {
	app := newTestApp(t)
	alice := app.signup("alice@example.fr", "Alice")
	stockForSharing(alice)
	path := alice.createShare("Benchy", "")

	// Un maillage converti et sa miniature, jamais le STL d'origine.
	files := shareFiles(t, app)
	if len(files) != 2 || !strings.HasSuffix(files[0], ".fmm") || files[1] != strings.TrimSuffix(files[0], ".fmm")+".png" {
		t.Fatalf("fichiers stockés : %v", files)
	}
	stored, _ := os.ReadFile(filepath.Join(app.cfg.ShareDir, files[0]))
	if !bytes.HasPrefix(stored, []byte("FMM1")) {
		t.Error("le STL d'origine ne doit pas être conservé tel quel")
	}

	visitor := app.newClient()
	r := visitor.get(path)
	if r.Status != http.StatusOK || !strings.Contains(r.Body, "Alice vous propose « Benchy »") {
		t.Fatalf("page publique : %d\n%s", r.Status, r.Body)
	}
	for header, want := range map[string]string{"X-Robots-Tag": "noindex, nofollow", "Referrer-Policy": "no-referrer", "Cache-Control": "no-store"} {
		if got := r.Header.Get(header); got != want {
			t.Errorf("%s = %q, attendu %q", header, got, want)
		}
	}
	if strings.Count(r.Body, `name="product_id"`) != 2 || !strings.Contains(r.Body, "Rouge Feu") || !strings.Contains(r.Body, "Bleu Nuit") {
		t.Error("les deux couleurs disponibles doivent être proposées, une pastille par produit")
	}
	for _, leak := range []string{"CouleurVide", "CouleurMasquee", "19,99", "note privée", "Neuf", "2 bobines"} {
		if strings.Contains(r.Body, leak) {
			t.Errorf("la page publique expose %q", leak)
		}
	}

	fileURL := regexp.MustCompile(`data-mesh="([^"]+)"`).FindStringSubmatch(r.Body)[1]
	if r := visitor.get(fileURL); r.Status != http.StatusOK || !strings.HasPrefix(r.Body, "FMM1") {
		t.Errorf("maillage : %d", r.Status)
	}

	// Choix du destinataire, puis remplacement.
	products, _ := app.store.ListProducts(context.Background(), userID(t, app, "alice@example.fr"))
	ids := map[string]string{}
	for _, p := range products {
		ids[p.ColorName] = strconv.FormatInt(p.ID, 10)
	}
	r = visitor.post(path+"/choice", url.Values{"product_id": {ids["Rouge Feu"]}, "recipient_name": {"Léa"}, "message": {"Pour mon bureau"}})
	if r.Status != http.StatusSeeOther || r.Location != path {
		t.Fatalf("choix : %d %s", r.Status, r.Location)
	}
	if m, _ := app.mail.Last("alice@example.fr"); !strings.Contains(m.Subject, "Léa a choisi « eSun PLA+ Rouge Feu (Silk) » pour « Benchy »") {
		t.Errorf("email au créateur : %q", m.Subject)
	}
	if r := visitor.get(path); !strings.Contains(r.Body, "Votre choix a été transmis") || !strings.Contains(r.Body, "Votre choix actuel") {
		t.Error("confirmation du choix absente")
	}
	visitor.post(path+"/choice", url.Values{"product_id": {ids["Bleu Nuit"]}})
	if r := alice.get("/shares"); !strings.Contains(r.Body, "Bleu Nuit") || strings.Contains(r.Body, "Rouge Feu") {
		t.Error("le dernier choix doit remplacer le précédent")
	}
	before := app.mail.Count()
	visitor.post(path+"/choice", url.Values{"product_id": {ids["CouleurMasquee"]}})
	if app.mail.Count() != before {
		t.Error("une couleur masquée ne doit pas pouvoir être choisie")
	}

	// Limitation des envois de choix (10 / 15 min par IP et par partage).
	for range 10 {
		visitor.post(path+"/choice", url.Values{"product_id": {ids["Bleu Nuit"]}})
	}
	before = app.mail.Count()
	visitor.post(path+"/choice", url.Values{"product_id": {ids["Bleu Nuit"]}})
	if app.mail.Count() != before {
		t.Error("les envois de choix doivent être limités")
	}
}

func TestSharePassword(t *testing.T) {
	app := newTestApp(t)
	alice := app.signup("alice@example.fr", "Alice")
	stockForSharing(alice)
	path := alice.createShare("Vase secret", "sesame123")

	visitor := app.newClient()
	r := visitor.get(path)
	if !strings.Contains(r.Body, "protégé par un mot de passe") || strings.Contains(r.Body, "Vase secret") || strings.Contains(r.Body, "Alice") {
		t.Fatal("sans mot de passe, la page doit rester neutre")
	}
	shares, _ := app.store.ListShares(context.Background(), userID(t, app, "alice@example.fr"))
	fileURL := path + "/files/" + strconv.FormatInt(shares[0].File.ID, 10)
	if r := visitor.get(fileURL); r.Status != http.StatusForbidden {
		t.Errorf("maillage sans mot de passe : %d", r.Status)
	}
	if r := visitor.post(path+"/unlock", url.Values{"password": {"faux"}}); r.Status != http.StatusUnauthorized {
		t.Errorf("mauvais mot de passe : %d", r.Status)
	}
	if r := visitor.post(path+"/unlock", url.Values{"password": {"sesame123"}}); r.Status != http.StatusSeeOther {
		t.Fatalf("bon mot de passe : %d", r.Status)
	}
	if r := visitor.get(path); !strings.Contains(r.Body, "Vase secret") {
		t.Fatal("le cookie d'accès n'ouvre pas le partage")
	}
	if r := visitor.get(fileURL); r.Status != http.StatusOK {
		t.Errorf("maillage avec cookie : %d", r.Status)
	}

	// Changer le mot de passe invalide les cookies existants.
	alice.post("/shares/"+strconv.FormatInt(shares[0].ID, 10)+"/password", url.Values{"action": {"set"}, "password": {"nouveau-mdp"}})
	if r := visitor.get(path); !strings.Contains(r.Body, "protégé par un mot de passe") {
		t.Error("l'ancien cookie ouvre encore le partage")
	}

	// 5 essais / 15 min par IP.
	other := app.newClient()
	for range 5 {
		other.post(path+"/unlock", url.Values{"password": {"x"}})
	}
	if r := other.post(path+"/unlock", url.Values{"password": {"nouveau-mdp"}}); r.Status != http.StatusTooManyRequests {
		t.Errorf("6e essai depuis la même IP : %d", r.Status)
	}

	// Retirer le mot de passe : le lien suffit.
	alice.post("/shares/"+strconv.FormatInt(shares[0].ID, 10)+"/password", url.Values{"action": {"remove"}})
	if r := app.newClient().get(path); !strings.Contains(r.Body, "Vase secret") {
		t.Error("après retrait du mot de passe, le lien doit suffire")
	}
}

func TestShareLockout(t *testing.T) {
	app := newTestApp(t)
	alice := app.signup("alice@example.fr", "Alice")
	path := alice.createShare("Benchy", "sesame123")
	visitor := app.newClient()

	// 20 échecs cumulés depuis plusieurs IP verrouillent le partage.
	for i := range 20 {
		ip := "203.0.113." + strconv.Itoa(i/4+1)
		visitor.post(path+"/unlock", url.Values{"password": {"faux"}}, "X-Forwarded-For", ip)
	}
	if m, _ := app.mail.Last("alice@example.fr"); !strings.Contains(m.Subject, "verrouillé") {
		t.Errorf("email de verrouillage : %q", m.Subject)
	}
	r := visitor.post(path+"/unlock", url.Values{"password": {"sesame123"}}, "X-Forwarded-For", "198.51.100.1")
	if r.Status != http.StatusLocked || !strings.Contains(r.Body, "momentanément indisponible") {
		t.Errorf("partage verrouillé : %d", r.Status)
	}

	shares, _ := app.store.ListShares(context.Background(), userID(t, app, "alice@example.fr"))
	alice.post("/shares/"+strconv.FormatInt(shares[0].ID, 10)+"/password", url.Values{"action": {"set"}, "password": {"nouveau-mdp"}})
	if r := visitor.post(path+"/unlock", url.Values{"password": {"nouveau-mdp"}}, "X-Forwarded-For", "198.51.100.2"); r.Status != http.StatusSeeOther {
		t.Errorf("après un nouveau mot de passe : %d", r.Status)
	}
}

func TestShareUploadErrorsAndLimits(t *testing.T) {
	app := newTestApp(t)
	alice := app.signup("alice@example.fr", "Alice")

	cases := []struct {
		name   string
		file   []byte
		fields map[string]string
		status int
		msg    string
	}{
		{"pas un STL", []byte("bonjour"), nil, http.StatusUnprocessableEntity, "pas un STL valide"},
		{"trop gros", testSTL(100), nil, http.StatusRequestEntityTooLarge, "trop volumineux"},
		{"mot de passe court", testSTL(1), map[string]string{"password": "abc"}, http.StatusUnprocessableEntity, "au moins 6 caractères"},
		{"sans fichier", nil, nil, http.StatusUnprocessableEntity, "Choisissez un fichier"},
	}
	for _, c := range cases {
		name := "modele.stl"
		if c.file == nil {
			name = ""
		}
		r := alice.upload("/shares", c.fields, name, c.file)
		if r.Status != c.status || !strings.Contains(r.Body, c.msg) {
			t.Errorf("%s : %d (attendu %d, %q)", c.name, r.Status, c.status, c.msg)
		}
	}
	if files := shareFiles(t, app); len(files) != 0 {
		t.Errorf("aucun fichier ne doit être écrit en cas de rejet : %v", files)
	}

	// Titre par défaut = nom du fichier ; plafond de partages actifs.
	r := alice.upload("/shares", nil, "Vase-spirale.stl", testSTL(1))
	if !strings.Contains(r.Body, "Vase-spirale") {
		t.Error("le titre doit reprendre le nom du fichier")
	}
	for range app.cfg.ShareMaxActive - 1 {
		alice.createShare("x", "")
	}
	if r := alice.upload("/shares", nil, "trop.stl", testSTL(1)); r.Status != http.StatusConflict {
		t.Errorf("partage au-delà du plafond : %d", r.Status)
	}
}

func TestShareIsolationRevokeAndPurge(t *testing.T) {
	app := newTestApp(t)
	alice := app.signup("alice@example.fr", "Alice")
	bob := app.signup("bob@example.fr", "Bob")
	path := alice.createShare("Benchy", "")
	shares, _ := app.store.ListShares(context.Background(), userID(t, app, "alice@example.fr"))
	id := strconv.FormatInt(shares[0].ID, 10)

	for _, r := range []response{
		bob.get("/shares/" + id + "/preview"),
		bob.get("/shares/" + id + "/file"),
		bob.post("/shares/"+id+"/password", url.Values{"action": {"remove"}}),
		bob.post("/shares/"+id+"/revoke", nil),
	} {
		if r.Status != http.StatusNotFound {
			t.Errorf("Bob agit sur le partage d'Alice : %d", r.Status)
		}
	}
	if r := alice.get("/shares/" + id + "/preview"); r.Status != http.StatusOK || !strings.Contains(r.Body, "Aperçu") {
		t.Errorf("aperçu : %d", r.Status)
	}
	if r := alice.get("/shares/" + id + "/file"); !strings.HasPrefix(r.Body, "FMM1") {
		t.Error("maillage de l'aperçu")
	}

	alice.post("/shares/"+id+"/revoke", nil)
	if files := shareFiles(t, app); len(files) != 0 {
		t.Errorf("fichier conservé après révocation : %v", files)
	}
	if r := app.newClient().get(path); r.Status != http.StatusNotFound || !strings.Contains(r.Body, "plus disponible") || strings.Contains(r.Body, "Alice") {
		t.Errorf("partage révoqué : %d", r.Status)
	}

	// Expiration : la purge supprime fichier puis lignes.
	path = alice.createShare("Éphémère", "")
	app.store.Now = func() time.Time { return time.Now().Add(8 * 24 * time.Hour) }
	if r := app.newClient().get(path); r.Status != http.StatusNotFound {
		t.Errorf("partage expiré encore visible : %d", r.Status)
	}
	if err := app.server.PurgeShares(context.Background()); err != nil {
		t.Fatal(err)
	}
	app.store.Now = time.Now
	if files := shareFiles(t, app); len(files) != 0 {
		t.Errorf("fichier conservé après expiration : %v", files)
	}

	// Orphelins au démarrage.
	os.WriteFile(filepath.Join(app.cfg.ShareDir, share.NewStorageKey()), []byte("x"), 0o600)
	app.server.CleanShareOrphans(context.Background())
	if files := shareFiles(t, app); len(files) != 0 {
		t.Errorf("orphelin conservé : %v", files)
	}
}

func TestAccountDeletionRemovesShareFiles(t *testing.T) {
	app := newTestApp(t)
	alice := app.signup("alice@example.fr", "Alice")
	alice.createShare("Benchy", "")
	if r := alice.get("/account/export.json"); !strings.Contains(r.Body, `"title": "Benchy"`) || strings.Contains(r.Body, "FMM1") {
		t.Error("l'export doit lister les partages, sans les fichiers")
	}
	alice.post("/account/delete", url.Values{"confirm_email": {"alice@example.fr"}})
	if files := shareFiles(t, app); len(files) != 0 {
		t.Errorf("fichiers conservés après suppression du compte : %v", files)
	}
}

func TestShareUploadConcurrencyLimit(t *testing.T) {
	app := newTestApp(t)
	alice := app.signup("alice@example.fr", "Alice")
	// Simule des conversions en cours sur tous les créneaux.
	for range cap(app.server.uploadSlots) {
		app.server.uploadSlots <- struct{}{}
	}
	if r := alice.upload("/shares", nil, "x.stl", testSTL(1)); r.Status != http.StatusServiceUnavailable {
		t.Errorf("envoi au-delà de la limite : %d", r.Status)
	}
	<-app.server.uploadSlots
	if r := alice.upload("/shares", nil, "x.stl", testSTL(1)); r.Status != http.StatusOK {
		t.Errorf("envoi après libération d'un créneau : %d", r.Status)
	}
}

func TestShareThumbnail(t *testing.T) {
	app := newTestApp(t)
	alice := app.signup("alice@example.fr", "Alice")
	bob := app.signup("bob@example.fr", "Bob")
	alice.createShare("Benchy", "")
	shares, _ := app.store.ListShares(context.Background(), userID(t, app, "alice@example.fr"))
	thumb := "/shares/" + strconv.FormatInt(shares[0].ID, 10) + "/thumb"

	r := alice.get(thumb)
	if r.Status != http.StatusOK || r.Header.Get("Content-Type") != "image/png" || !strings.HasPrefix(r.Body, "\x89PNG") {
		t.Fatalf("miniature : %d %s", r.Status, r.Header.Get("Content-Type"))
	}
	if r := bob.get(thumb); r.Status != http.StatusNotFound {
		t.Errorf("Bob voit la miniature d'Alice : %d", r.Status)
	}
	if r := alice.get("/shares"); !strings.Contains(r.Body, `src="`+thumb+`"`) || !strings.Contains(r.Body, `<span class="share-name">Benchy</span>`) {
		t.Error("la liste doit afficher miniature et titre")
	}

	// Miniature absente (partage antérieur) : régénérée à la demande.
	os.Remove(filepath.Join(app.cfg.ShareDir, strings.TrimSuffix(shares[0].File.StorageKey, ".fmm")+".png"))
	if r := alice.get(thumb); r.Status != http.StatusOK || !strings.HasPrefix(r.Body, "\x89PNG") {
		t.Errorf("régénération : %d", r.Status)
	}
	if len(shareFiles(t, app)) != 2 {
		t.Error("la miniature régénérée doit être enregistrée")
	}

	// La révocation efface aussi la miniature.
	alice.post("/shares/"+strconv.FormatInt(shares[0].ID, 10)+"/revoke", nil)
	if files := shareFiles(t, app); len(files) != 0 {
		t.Errorf("fichiers restants : %v", files)
	}
}
