package web

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MaximeBranger/filament_manager/internal/config"
	"github.com/MaximeBranger/filament_manager/internal/db"
	"github.com/MaximeBranger/filament_manager/internal/mail"
	"github.com/MaximeBranger/filament_manager/internal/reference"
	"github.com/MaximeBranger/filament_manager/internal/store"
)

type testApp struct {
	t      *testing.T
	srv    *httptest.Server
	mail   *mail.Fake
	store  *store.Store
	server *Server
	cfg    *config.Config
}

func newTestApp(t *testing.T) *testApp { return newTestAppEnv(t, nil) }

// newTestAppEnv permet de compléter ou remplacer la configuration de test.
func newTestAppEnv(t *testing.T, extra map[string]string) *testApp {
	t.Helper()
	conn, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	env := map[string]string{
		"BASE_URL":            "http://filaments.test",
		"SESSION_SECRET":      strings.Repeat("s", 32),
		"SMTP_HOST":           "smtp.test",
		"SMTP_FROM":           "FilamentManager <noreply@filaments.test>",
		"SHARE_DIR":           t.TempDir(),
		"SHARE_MAX_FILE_SIZE": "4KB",
		"TRUSTED_PROXIES":     "127.0.0.1", // permet de simuler plusieurs IP via X-Forwarded-For
	}
	for k, v := range extra {
		env[k] = v
	}
	cfg, err := config.Load(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	fake := &mail.Fake{}
	st := store.New(conn)
	s, err := New(cfg, st, fake, slog.New(slog.NewTextHandler(io.Discard, nil)), "test")
	if err != nil {
		t.Fatal(err)
	}
	s.syncMail = true
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return &testApp{t: t, srv: srv, mail: fake, store: st, server: s, cfg: cfg}
}

type client struct {
	app  *testApp
	http *http.Client
}

// newClient simule un navigateur : cookies conservés, redirections non suivies.
func (a *testApp) newClient() *client {
	jar, _ := cookiejar.New(nil)
	return &client{app: a, http: &http.Client{
		Jar:           jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

type response struct {
	Status   int
	Body     string
	Location string
	Header   http.Header
}

func (c *client) do(req *http.Request) response {
	c.app.t.Helper()
	resp, err := c.http.Do(req)
	if err != nil {
		c.app.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return response{resp.StatusCode, string(body), resp.Header.Get("Location"), resp.Header}
}

func (c *client) get(path string, headers ...string) response {
	req, _ := http.NewRequest(http.MethodGet, c.app.srv.URL+path, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	return c.do(req)
}

func (c *client) post(path string, form url.Values, headers ...string) response {
	req, _ := http.NewRequest(http.MethodPost, c.app.srv.URL+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	return c.do(req)
}

func (c *client) postJSON(path string, v any) response {
	body, _ := json.Marshal(v)
	req, _ := http.NewRequest(http.MethodPost, c.app.srv.URL+path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return c.do(req)
}

var (
	tokenRe = regexp.MustCompile(`/auth/verify\?t=([A-Za-z0-9_-]+)`)
	codeRe  = regexp.MustCompile(`(?m)^\s+(\d{6})\s*$`)
)

// loginEmail demande un email de connexion et renvoie le jeton et le code reçus.
func (c *client) loginEmail(email string) (token, code string) {
	t := c.app.t
	t.Helper()
	before := c.app.mail.Count()
	r := c.post("/login", url.Values{"email": {email}})
	if r.Status != http.StatusSeeOther || r.Location != "/login/code" {
		t.Fatalf("POST /login : %d %s", r.Status, r.Location)
	}
	if c.app.mail.Count() == before {
		t.Fatal("aucun email envoyé")
	}
	m, _ := c.app.mail.Last(strings.ToLower(email))
	tm, cm := tokenRe.FindStringSubmatch(m.Text), codeRe.FindStringSubmatch(m.Text)
	if tm == nil || cm == nil {
		t.Fatalf("email sans lien ni code : %q", m.Text)
	}
	return tm[1], cm[1]
}

// signup crée un compte et renvoie un client connecté.
func (a *testApp) signup(email, name string) *client {
	a.t.Helper()
	c := a.newClient()
	token, _ := c.loginEmail(email)
	r := c.post("/auth/verify", url.Values{"t": {token}})
	if r.Location != "/signup" {
		a.t.Fatalf("première connexion : redirection vers %q", r.Location)
	}
	r = c.post("/signup", url.Values{"display_name": {name}})
	if r.Status != http.StatusSeeOther || r.Location != "/stock" {
		a.t.Fatalf("POST /signup : %d %s", r.Status, r.Location)
	}
	return c
}

func newProductForm(kv ...string) url.Values {
	v := url.Values{
		"brand": {"eSun"}, "material": {"PLA+"}, "color_name": {"Galaxy Black"}, "color_family": {"black"},
		"color_hex": {"#1a1a1a"}, "finish": {"standard"}, "diameter": {"1.75"}, "weight": {"1000"},
		"level": {"new"}, "purchase_date": {"2026-10-20"}, "quantity": {"1"},
	}
	for i := 0; i+1 < len(kv); i += 2 {
		v.Set(kv[i], kv[i+1])
	}
	return v
}

// Authentification ------------------------------------------------------------------------

func TestSignupAndLoginWithMagicLink(t *testing.T) {
	app := newTestApp(t)
	c := app.signup("Alice@Example.fr", "Alice")

	r := c.get("/stock")
	if r.Status != http.StatusOK || !strings.Contains(r.Body, "Votre stock est vide") {
		t.Fatalf("GET /stock : %d", r.Status)
	}

	// Seconde connexion depuis un autre navigateur : pas de nouvelle inscription.
	other := app.newClient()
	token, _ := other.loginEmail("alice@example.fr")
	if r := other.get("/auth/verify?t=" + token); r.Status != http.StatusOK {
		t.Fatalf("GET /auth/verify : %d", r.Status)
	}
	// Le GET (pré-ouverture par un antivirus) ne consomme pas le lien.
	if r := other.get("/auth/verify?t=" + token); r.Status != http.StatusOK {
		t.Fatalf("second GET /auth/verify : %d", r.Status)
	}
	if r := other.post("/auth/verify", url.Values{"t": {token}}); r.Location != "/stock" {
		t.Fatalf("connexion : redirection vers %q", r.Location)
	}
	if r := other.post("/auth/verify", url.Values{"t": {token}}); r.Status != http.StatusGone {
		t.Errorf("lien réutilisé : %d", r.Status)
	}

	// Déconnexion.
	other.post("/logout", nil)
	if r := other.get("/stock"); r.Status != http.StatusSeeOther {
		t.Errorf("après déconnexion : %d", r.Status)
	}
}

func TestLoginWithCode(t *testing.T) {
	app := newTestApp(t)
	app.signup("bob@example.fr", "Bob")

	c := app.newClient()
	_, code := c.loginEmail("bob@example.fr")

	// Le code ne vaut que dans le navigateur qui l'a demandé.
	intruder := app.newClient()
	if r := intruder.post("/login/code", url.Values{"code": {code}}); r.Status != http.StatusSeeOther || r.Location != "/login" {
		t.Errorf("code sans cookie navigateur : %d %s", r.Status, r.Location)
	}
	intruder.loginEmail("intrus@example.fr")
	if r := intruder.post("/login/code", url.Values{"code": {code}}); r.Status != http.StatusUnprocessableEntity {
		t.Errorf("code d'un autre navigateur accepté : %d", r.Status)
	}

	if r := c.post("/login/code", url.Values{"code": {code[:3] + " " + code[3:]}}); r.Location != "/stock" {
		t.Fatalf("connexion par code : %d %s", r.Status, r.Location)
	}
	if r := c.get("/stock"); r.Status != http.StatusOK {
		t.Errorf("GET /stock : %d", r.Status)
	}
}

func TestCodeAttemptsAreLimited(t *testing.T) {
	app := newTestApp(t)
	c := app.newClient()
	_, code := c.loginEmail("carol@example.fr")
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	for range maxCodeAttempts {
		c.post("/login/code", url.Values{"code": {wrong}})
	}
	r := c.post("/login/code", url.Values{"code": {code}})
	if r.Status != http.StatusUnprocessableEntity || !strings.Contains(r.Body, "expiré") {
		t.Errorf("bon code accepté après %d échecs : %d", maxCodeAttempts, r.Status)
	}
}

func TestLoginRateLimit(t *testing.T) {
	app := newTestApp(t)
	c := app.newClient()
	for range 5 {
		r := c.post("/login", url.Values{"email": {"spam@example.fr"}})
		if r.Status != http.StatusSeeOther {
			t.Fatalf("réponse différente une fois limité : %d", r.Status)
		}
	}
	if n := app.mail.Count(); n != 3 {
		t.Errorf("%d emails envoyés, 3 attendus", n)
	}
}

func TestLoginRejectsInvalidEmail(t *testing.T) {
	app := newTestApp(t)
	r := app.newClient().post("/login", url.Values{"email": {"Alice <a@b.fr>"}})
	if r.Status != http.StatusUnprocessableEntity || app.mail.Count() != 0 {
		t.Errorf("email invalide : %d, %d emails", r.Status, app.mail.Count())
	}
}

func TestRequireLoginAndSafeRedirect(t *testing.T) {
	app := newTestApp(t)
	c := app.newClient()
	r := c.get("/stock?fam=blue")
	if r.Status != http.StatusSeeOther || r.Location != "/login?next=%2Fstock%3Ffam%3Dblue" {
		t.Errorf("anonyme : %d %s", r.Status, r.Location)
	}
	if r := c.get("/stock", "HX-Request", "true"); r.Header.Get("HX-Redirect") == "" {
		t.Error("une requête htmx anonyme doit recevoir HX-Redirect")
	}

	// La destination après connexion est conservée, sauf si elle sort du site.
	i := 0
	for next, want := range map[string]string{"/stock?fam=blue": "/stock?fam=blue", "//evil.example/x": "/stock", "https://evil.example": "/stock"} {
		i++
		email := "dave" + strconv.Itoa(i) + "@example.fr"
		app.signup(email, "Dave")
		c := app.newClient()
		c.post("/login", url.Values{"email": {email}, "next": {next}})
		m, _ := app.mail.Last(email)
		token := tokenRe.FindStringSubmatch(m.Text)[1]
		if r := c.post("/auth/verify", url.Values{"t": {token}}); r.Location != want {
			t.Errorf("next=%q : redirection vers %q, attendu %q", next, r.Location, want)
		}
	}
}

func TestCrossOriginPostRejected(t *testing.T) {
	app := newTestApp(t)
	c := app.signup("eve@example.fr", "Eve")
	r := c.post("/spools", newProductForm(), "Origin", "https://evil.example", "Sec-Fetch-Site", "cross-site")
	if r.Status != http.StatusForbidden {
		t.Errorf("POST cross-origin : %d", r.Status)
	}
	if r := c.post("/spools", newProductForm(), "Origin", app.srv.URL, "Sec-Fetch-Site", "same-origin"); r.Status != http.StatusSeeOther {
		t.Errorf("POST same-origin : %d", r.Status)
	}
}

// Stock ---------------------------------------------------------------------------------

var spoolIDRe = regexp.MustCompile(`id="spool-(\d+)"`)

func spoolIDs(body string) []string {
	var ids []string
	for _, m := range spoolIDRe.FindAllStringSubmatch(body, -1) {
		ids = append(ids, m[1])
	}
	return ids
}

func TestSpoolLifecycle(t *testing.T) {
	app := newTestApp(t)
	c := app.signup("frank@example.fr", "Frank")

	r := c.post("/spools", newProductForm("quantity", "3", "brand", "ESUN", "ean", "6938936716785"))
	if r.Status != http.StatusSeeOther || r.Location != "/stock" {
		t.Fatalf("création : %d %s\n%s", r.Status, r.Location, r.Body)
	}
	r = c.get("/stock")
	ids := spoolIDs(r.Body)
	if len(ids) != 3 {
		t.Fatalf("%d bobines affichées, 3 attendues", len(ids))
	}
	if !strings.Contains(r.Body, "3 bobines ajoutées") {
		t.Error("message flash manquant")
	}
	if !strings.Contains(r.Body, "eSun") {
		t.Error("la marque saisie « ESUN » doit être normalisée en « eSun »")
	}
	if strings.Contains(r.Body, "ZgotmplZ") {
		t.Error("une valeur a été neutralisée par html/template (pastille de couleur ?)")
	}

	// Réponse partielle htmx pour les filtres.
	r = c.get("/stock?q=galaxy&fam=black", "HX-Request", "true", "HX-Target", "results")
	if strings.Contains(r.Body, "<html") || len(spoolIDs(r.Body)) != 3 {
		t.Errorf("partiel htmx incorrect (%d bobines)", len(spoolIDs(r.Body)))
	}
	if r := c.get("/stock?fam=blue"); len(spoolIDs(r.Body)) != 0 {
		t.Error("le filtre famille ne filtre pas")
	}

	// Changement de niveau depuis la liste.
	r = c.post("/spools/"+ids[0]+"/level", url.Values{"level": {"half"}}, "HX-Request", "true")
	if r.Status != http.StatusOK || !strings.Contains(r.Body, `value="half" selected`) {
		t.Errorf("changement de niveau : %d", r.Status)
	}
	if r := c.post("/spools/"+ids[0]+"/level", url.Values{"level": {"plein"}}); r.Status != http.StatusBadRequest {
		t.Errorf("niveau invalide : %d", r.Status)
	}

	// Les bobines vides sont masquées par défaut.
	c.post("/spools/"+ids[1]+"/level", url.Values{"level": {"empty"}})
	if n := len(spoolIDs(c.get("/stock").Body)); n != 2 {
		t.Errorf("%d bobines affichées, la vide devrait être masquée", n)
	}
	if n := len(spoolIDs(c.get("/stock?empty=1").Body)); n != 3 {
		t.Errorf("%d bobines avec empty=1", n)
	}

	// Modification d'une bobine.
	r = c.post("/spools/"+ids[2], url.Values{"level": {"low"}, "price": {"21,50"}, "notes": {"Pour le Voron"}})
	if r.Status != http.StatusSeeOther {
		t.Fatalf("modification : %d\n%s", r.Status, r.Body)
	}
	if r := c.get("/spools/" + ids[2]); !strings.Contains(r.Body, "21,50") || !strings.Contains(r.Body, "Pour le Voron") {
		t.Error("modification non enregistrée")
	}
	if r := c.post("/spools/"+ids[2], url.Values{"price": {"cher"}}); r.Status != http.StatusUnprocessableEntity {
		t.Errorf("prix invalide : %d", r.Status)
	}

	// Ajout d'une bobine d'un produit existant puis suppression.
	products, _ := app.store.ListProducts(context.Background(), 1)
	pid := strconv.FormatInt(products[0].ID, 10)
	if r := c.post("/spools", url.Values{"product_id": {pid}, "quantity": {"1"}, "level": {"new"}}); r.Status != http.StatusSeeOther {
		t.Fatalf("ajout sur produit existant : %d", r.Status)
	}
	if r := c.post("/spools/"+ids[0]+"/delete", nil); r.Status != http.StatusSeeOther {
		t.Errorf("suppression : %d", r.Status)
	}
	if r := c.get("/spools/" + ids[0]); r.Status != http.StatusNotFound {
		t.Errorf("bobine supprimée encore visible : %d", r.Status)
	}

	// Même EAN deux fois pour un nouveau produit : refusé proprement.
	r = c.post("/spools", newProductForm("ean", "6938936716785"))
	if r.Status != http.StatusUnprocessableEntity || !strings.Contains(r.Body, "déjà un produit avec ce code-barre") {
		t.Errorf("EAN en double : %d", r.Status)
	}
}

func TestSpoolFormValidation(t *testing.T) {
	app := newTestApp(t)
	c := app.signup("gina@example.fr", "Gina")
	r := c.post("/spools", newProductForm("brand", "", "quantity", "99", "color_family", "fuchsia"))
	if r.Status != http.StatusUnprocessableEntity {
		t.Fatalf("formulaire invalide : %d", r.Status)
	}
	for _, msg := range []string{"La marque est obligatoire", "Quantité entre 1 et 50", "Choisis une famille"} {
		if !strings.Contains(r.Body, msg) {
			t.Errorf("message manquant : %q", msg)
		}
	}
	if !strings.Contains(r.Body, `value="Galaxy Black"`) {
		t.Error("les valeurs saisies doivent être conservées")
	}
}

func TestProductsAndScan(t *testing.T) {
	app := newTestApp(t)
	c := app.signup("hugo@example.fr", "Hugo")
	c.post("/spools", newProductForm("ean", "1111111111116"))
	products, _ := app.store.ListProducts(context.Background(), 1)
	pid := strconv.FormatInt(products[0].ID, 10)

	if r := c.get("/scan/lookup?ean=1111111111116"); r.Location != "/spools/new?product_id="+pid {
		t.Errorf("EAN connu : %s", r.Location)
	}
	if r := c.get("/scan/lookup?ean=2222222222222"); r.Location != "/spools/new?ean=2222222222222" {
		t.Errorf("EAN inconnu : %s", r.Location)
	}
	if r := c.get("/spools/new?ean=2222222222222"); !strings.Contains(r.Body, `value="2222222222222"`) {
		t.Error("l'EAN scanné doit préremplir le formulaire")
	}

	var lookup struct {
		Found   bool
		Product productJSON
	}
	json.Unmarshal([]byte(c.get("/api/products/lookup?ean=1111111111116").Body), &lookup)
	if !lookup.Found || lookup.Product.ID != products[0].ID || lookup.Product.Hex != "#1a1a1a" {
		t.Errorf("lookup = %+v", lookup)
	}

	// Modification du produit.
	form := newProductForm("color_name", "Noir Profond", "color_hex", "#000000", "ean", "1111111111116")
	if r := c.post("/products/"+pid, form); r.Status != http.StatusSeeOther {
		t.Fatalf("modification produit : %d\n%s", r.Status, r.Body)
	}
	if r := c.get("/stock"); !strings.Contains(r.Body, "Noir Profond") || !strings.Contains(r.Body, "#000000") {
		t.Error("la modification du produit doit se refléter sur ses bobines")
	}

	// Un produit avec des bobines ne peut pas être supprimé.
	c.post("/products/"+pid+"/delete", nil)
	if r := c.get("/products/" + pid); r.Status != http.StatusOK {
		t.Error("produit utilisé supprimé")
	}
}

func TestReception(t *testing.T) {
	app := newTestApp(t)
	c := app.signup("ines@example.fr", "Inès")
	c.post("/spools", newProductForm("ean", "3333333333333"))
	products, _ := app.store.ListProducts(context.Background(), 1)

	r := c.postJSON("/reception", map[string]any{
		"purchase_date": "2026-10-24",
		"supplier":      "3DJake",
		"items": []map[string]any{
			{"product_id": products[0].ID, "quantity": 2},
			{"quantity": 3, "product": map[string]string{
				"brand": "polymaker", "material": "PETG", "color_name": "Bleu", "color_family": "blue",
				"color_hex": "#1565c0", "finish": "standard", "diameter": "1.75", "weight": "1000", "ean": "4444444444444",
			}},
		},
	})
	if r.Status != http.StatusOK || !strings.Contains(r.Body, `"created":5`) {
		t.Fatalf("réception : %d %s", r.Status, r.Body)
	}
	if n := len(spoolIDs(c.get("/stock").Body)); n != 6 {
		t.Errorf("%d bobines, 6 attendues", n)
	}
	if r := c.get("/stock?brand=polymaker"); !strings.Contains(r.Body, "Polymaker") {
		t.Error("la marque doit reprendre la graphie de la liste de référence")
	}

	r = c.postJSON("/reception", map[string]any{"items": []map[string]any{
		{"quantity": 1, "product": map[string]string{"material": "PLA"}},
	}})
	if r.Status != http.StatusUnprocessableEntity || !strings.Contains(r.Body, `"items"`) {
		t.Errorf("réception invalide : %d %s", r.Status, r.Body)
	}
	if r := c.postJSON("/reception", map[string]any{"items": []any{}}); r.Status != http.StatusUnprocessableEntity {
		t.Errorf("réception vide : %d", r.Status)
	}
}

// Cloisonnement ------------------------------------------------------------------------------

func TestUsersCannotAccessOthersData(t *testing.T) {
	app := newTestApp(t)
	alice := app.signup("alice@example.fr", "Alice")
	bob := app.signup("bob@example.fr", "Bob")

	alice.post("/spools", newProductForm("ean", "5555555555555", "notes", "secret d'Alice"))
	aliceSpool := spoolIDs(alice.get("/stock").Body)[0]
	products, _ := app.store.ListProducts(context.Background(), 1)
	aliceProduct := strconv.FormatInt(products[0].ID, 10)

	checks := []struct {
		name string
		resp response
		want int
	}{
		{"voir la bobine", bob.get("/spools/" + aliceSpool), http.StatusNotFound},
		{"modifier la bobine", bob.post("/spools/"+aliceSpool, url.Values{"level": {"empty"}}), http.StatusNotFound},
		{"changer le niveau", bob.post("/spools/"+aliceSpool+"/level", url.Values{"level": {"empty"}}), http.StatusNotFound},
		{"supprimer la bobine", bob.post("/spools/"+aliceSpool+"/delete", nil), http.StatusNotFound},
		{"voir le produit", bob.get("/products/" + aliceProduct), http.StatusNotFound},
		{"modifier le produit", bob.post("/products/"+aliceProduct, newProductForm()), http.StatusNotFound},
		{"supprimer le produit", bob.post("/products/"+aliceProduct+"/delete", nil), http.StatusNotFound},
		{"formulaire sur son produit", bob.get("/spools/new?product_id=" + aliceProduct), http.StatusNotFound},
		{"ajouter sur son produit", bob.post("/spools", url.Values{"product_id": {aliceProduct}, "level": {"new"}}), http.StatusNotFound},
		{"réception sur son produit", bob.postJSON("/reception", map[string]any{"items": []map[string]any{{"product_id": products[0].ID, "quantity": 1}}}), http.StatusUnprocessableEntity},
	}
	for _, c := range checks {
		if c.resp.Status != c.want {
			t.Errorf("Bob peut %s : statut %d, attendu %d", c.name, c.resp.Status, c.want)
		}
	}
	if r := bob.get("/api/products/lookup?ean=5555555555555"); strings.Contains(r.Body, `"found":true`) {
		t.Error("Bob voit le produit d'Alice par son EAN")
	}
	if r := bob.get("/stock"); strings.Contains(r.Body, "Galaxy Black") {
		t.Error("le stock d'Alice apparaît chez Bob")
	}
	if r := bob.get("/account/export.json"); strings.Contains(r.Body, "secret d'Alice") {
		t.Error("l'export de Bob contient des données d'Alice")
	}
	// Rien n'a bougé chez Alice.
	if r := alice.get("/spools/" + aliceSpool); r.Status != http.StatusOK || !strings.Contains(r.Body, `value="new" selected`) {
		t.Error("la bobine d'Alice a été modifiée")
	}
}

// Compte -------------------------------------------------------------------------------------

func TestAccountExportAndDelete(t *testing.T) {
	app := newTestApp(t)
	c := app.signup("jules@example.fr", "Jules")
	c.post("/spools", newProductForm("quantity", "2", "price", "19,99"))

	r := c.get("/account/export.json")
	var export struct {
		Account struct{ Email string }
		Spools  []map[string]any
	}
	if err := json.Unmarshal([]byte(r.Body), &export); err != nil || export.Account.Email != "jules@example.fr" || len(export.Spools) != 2 {
		t.Errorf("export JSON : %v %+v", err, export)
	}
	r = c.get("/account/export.csv")
	if !strings.Contains(r.Body, "Galaxy Black") || !strings.Contains(r.Body, "19,99") || strings.Count(r.Body, "\n") != 3 {
		t.Errorf("export CSV :\n%s", r.Body)
	}

	if r := c.post("/account/name", url.Values{"display_name": {"  Jules   V. "}}); r.Status != http.StatusSeeOther {
		t.Errorf("changement de nom : %d", r.Status)
	}
	if r := c.get("/account"); !strings.Contains(r.Body, `value="Jules V."`) {
		t.Error("nom non mis à jour")
	}

	if r := c.post("/account/delete", url.Values{"confirm_email": {"autre@example.fr"}}); r.Status != http.StatusUnprocessableEntity {
		t.Errorf("suppression avec mauvais email : %d", r.Status)
	}
	if r := c.post("/account/delete", url.Values{"confirm_email": {"JULES@example.fr"}}); r.Status != http.StatusSeeOther {
		t.Fatalf("suppression : %d", r.Status)
	}
	if r := c.get("/stock"); r.Status != http.StatusSeeOther {
		t.Errorf("session encore valide après suppression : %d", r.Status)
	}
	if _, err := app.store.UserByEmail(context.Background(), "jules@example.fr"); err == nil {
		t.Error("le compte existe encore")
	}
}

// Divers -------------------------------------------------------------------------------------

func TestPublicPagesAndHeaders(t *testing.T) {
	app := newTestApp(t)
	c := app.newClient()
	for _, path := range []string{"/", "/login", "/confidentialite", "/healthz", "/readyz", "/static/app.css", "/static/vendor/htmx.min.js"} {
		if r := c.get(path); r.Status != http.StatusOK {
			t.Errorf("GET %s : %d", path, r.Status)
		}
	}
	r := c.get("/")
	if csp := r.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") {
		t.Errorf("CSP = %q", csp)
	}
	if r.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("X-Content-Type-Options manquant")
	}
	if strings.Contains(r.Body, "plausible") {
		t.Error("Plausible ne doit pas être injecté sans configuration")
	}
	if r := c.get("/static/app.css?v=1"); !strings.Contains(r.Header.Get("Cache-Control"), "immutable") {
		t.Error("les fichiers versionnés doivent être mis en cache longtemps")
	}
	if r := c.get("/inexistant"); r.Status != http.StatusNotFound {
		t.Errorf("404 attendu : %d", r.Status)
	}
}

func TestSessionExpires(t *testing.T) {
	app := newTestApp(t)
	c := app.signup("kim@example.fr", "Kim")
	app.store.DB.Exec(`UPDATE sessions SET expires_at = ?`, time.Now().Add(-time.Minute).UTC().Format(db.TimeFormat))
	if r := c.get("/stock"); r.Status != http.StatusSeeOther {
		t.Errorf("session expirée acceptée : %d", r.Status)
	}
}

func TestPWA(t *testing.T) {
	app := newTestApp(t)
	c := app.signup("pwa@example.fr", "Pwa")

	r := c.get("/manifest.webmanifest")
	var m struct {
		StartURL string `json:"start_url"`
		Display  string
		Icons    []struct{ Src, Purpose string }
	}
	if err := json.Unmarshal([]byte(r.Body), &m); err != nil || r.Header.Get("Content-Type") != "application/manifest+json" {
		t.Fatalf("manifeste : %v %s", err, r.Header.Get("Content-Type"))
	}
	if m.StartURL != "/stock" || m.Display != "standalone" || len(m.Icons) != 3 {
		t.Errorf("manifeste : %+v", m)
	}
	for _, icon := range m.Icons {
		if r := c.get(icon.Src); r.Status != http.StatusOK || r.Header.Get("Content-Type") != "image/png" {
			t.Errorf("icône %s : %d", icon.Src, r.Status)
		}
	}

	r = c.get("/sw.js")
	if !strings.Contains(r.Body, "const VERSION = 'test'") || r.Header.Get("Cache-Control") != "no-cache" {
		t.Errorf("service worker : %q", r.Header.Get("Cache-Control"))
	}
	if r := c.get("/offline"); r.Status != http.StatusOK || strings.Contains(r.Body, `href="/account"`) || strings.Contains(r.Body, "badge-count") {
		t.Error("la page hors ligne ne doit contenir aucune donnée de compte (elle est mise en cache)")
	}
	if r := c.get("/stock"); !strings.Contains(r.Body, `rel="manifest"`) {
		t.Error("lien vers le manifeste absent")
	}
}

func TestPlausibleNeverSeesTokens(t *testing.T) {
	app := newTestAppEnv(t, map[string]string{"PLAUSIBLE_URL": "https://stats.example.fr", "PLAUSIBLE_DOMAIN": "filaments.test"})
	alice := app.signup("alice@example.fr", "Alice")

	if r := alice.get("/stock"); !strings.Contains(r.Body, "https://stats.example.fr/js/script.js") {
		t.Error("suivi normal attendu sur les pages ordinaires")
	}
	invite := inviteRe.FindString(alice.post("/friends/invitations", nil).Body)
	shareRe := regexp.MustCompile(`/share/[A-Za-z0-9_-]+`)
	share := shareRe.FindString(alice.upload("/shares", map[string]string{"title": "x"}, "x.stl", testSTL(1)).Body)
	token, _ := app.newClient().loginEmail("alice@example.fr")

	for path, generic := range map[string]string{invite: "/invite/*", share: "/share/*", "/auth/verify?t=" + token: "/auth/verify"} {
		body := alice.get(path).Body
		if !strings.Contains(body, "script.manual.js") || !strings.Contains(body, `data-url="`+generic+`"`) {
			t.Errorf("%s : suivi générique %q attendu", generic, generic)
		}
		if strings.Contains(body, "/js/script.js") {
			t.Errorf("%s : le suivi automatique enverrait l'URL avec son jeton", generic)
		}
	}
}

func TestCSVExportNeutralizesFormulas(t *testing.T) {
	app := newTestApp(t)
	c := app.signup("csv@example.fr", "Csv")
	c.post("/spools", newProductForm("brand", "=cmd|' /C calc'!A0", "notes", `=HYPERLINK("http://evil")`))
	body := c.get("/account/export.csv").Body
	if !strings.Contains(body, `'=cmd`) || !strings.Contains(body, `'=HYPERLINK`) {
		t.Errorf("formules non neutralisées :\n%s", body)
	}
}

func TestReferencePrefill(t *testing.T) {
	app := newTestApp(t)
	app.server.reference = func(code string) (reference.Entry, bool) {
		if code != "6938936716785" {
			return reference.Entry{}, false
		}
		return reference.Entry{Brand: "eSun", Material: "PLA+", Line: "PLA+", ColorName: "Galaxy Black", ColorFamily: "black",
			ColorHex: "#1b1b1f", Finish: "silk", DiameterMM: 1.75, WeightG: 1000}, true
	}
	c := app.signup("ref@example.fr", "Ref")

	// Scan unitaire : pas de message « inconnu », formulaire prérempli et signalé.
	r := c.get("/scan/lookup?ean=6938936716785")
	if r.Location != "/spools/new?ean=6938936716785" {
		t.Fatalf("redirection : %s", r.Location)
	}
	r = c.get(r.Location)
	for _, want := range []string{`value="eSun"`, `value="Galaxy Black"`, `value="#1b1b1f"`, `<option value="silk" selected>`, "Open Filament Database", `value="6938936716785"`} {
		if !strings.Contains(r.Body, want) {
			t.Errorf("préremplissage : %q absent", want)
		}
	}
	if strings.Contains(r.Body, "Code-barre inconnu") {
		t.Error("un code connu de la référence ne doit pas être annoncé comme inconnu")
	}

	// Réception : l'API renvoie les valeurs de référence.
	var out struct {
		Found     bool
		Reference map[string]string
		Line      string
	}
	json.Unmarshal([]byte(c.get("/api/products/lookup?ean=6938936716785").Body), &out)
	if out.Found || out.Reference["brand"] != "eSun" || out.Reference["finish"] != "silk" || out.Line != "PLA+" {
		t.Errorf("lookup : %+v", out)
	}

	// Une fois le produit enregistré, le produit de l'utilisateur prime sur la référence.
	c.post("/spools", newProductForm("ean", "6938936716785", "color_name", "Mon noir"))
	if r := c.get("/scan/lookup?ean=6938936716785"); !strings.HasPrefix(r.Location, "/spools/new?product_id=") {
		t.Errorf("le produit de l'utilisateur doit primer : %s", r.Location)
	}
	if r := c.get("/credits"); !strings.Contains(r.Body, "Permission is hereby granted") {
		t.Error("la page Crédits doit reproduire la licence de l'OFD")
	}
}

func TestReferenceResync(t *testing.T) {
	app := newTestApp(t)
	refs := map[string]reference.Entry{
		"6938936716785": {Brand: "eSun", Material: "PLA+", ColorName: "Galaxy Black", ColorFamily: "black", ColorHex: "#1b1b1f", Finish: "silk", DiameterMM: 1.75, WeightG: 1000},
		"5901234123457": {Brand: "Polymaker", Material: "PETG", ColorName: "Teal", ColorFamily: "blue", ColorHex: "#00808a", Finish: "standard", DiameterMM: 1.75, WeightG: 1000},
	}
	app.server.reference = func(code string) (reference.Entry, bool) { e, ok := refs[code]; return e, ok }
	alice := app.signup("alice@example.fr", "Alice")
	bob := app.signup("bob@example.fr", "Bob")

	// Deux produits saisis à la main, faux ; un troisième sans code-barre.
	alice.post("/spools", newProductForm("ean", "6938936716785", "brand", "Inconnue", "color_name", "Noir", "quantity", "2"))
	alice.post("/spools", newProductForm("ean", "5901234123457", "material", "PLA", "color_name", "Bleu canard"))
	alice.post("/spools", newProductForm("color_name", "Sans code"))
	products, _ := app.store.ListProducts(context.Background(), userID(t, app, "alice@example.fr"))
	ids := map[string]string{}
	for _, p := range products {
		ids[p.ColorName] = strconv.FormatInt(p.ID, 10)
	}

	r := alice.get("/products/" + ids["Noir"])
	for _, want := range []string{"Différences avec la référence", "Inconnue", "Galaxy Black", "Silk", "Resynchroniser avec la référence"} {
		if !strings.Contains(r.Body, want) {
			t.Errorf("fiche produit : %q absent", want)
		}
	}
	if r := alice.get("/products/" + ids["Sans code"]); strings.Contains(r.Body, "Différences avec la référence") {
		t.Error("un produit sans code-barre n'a pas d'écart")
	}
	r = alice.get("/products")
	if strings.Count(r.Body, "≠ référence") != 2 || !strings.Contains(r.Body, "Resynchroniser ces produits") {
		t.Error("la liste doit signaler les 2 produits divergents et proposer la resynchronisation groupée")
	}

	// Bob ne peut pas resynchroniser le produit d'Alice.
	if r := bob.post("/products/"+ids["Noir"]+"/resync", nil); r.Status != http.StatusNotFound {
		t.Errorf("resynchronisation par un tiers : %d", r.Status)
	}

	// Resynchronisation unitaire : remplacement intégral, code-barre conservé, bobines suivent.
	alice.post("/products/"+ids["Noir"]+"/resync", nil)
	noirID, _ := strconv.ParseInt(ids["Noir"], 10, 64)
	p, _ := app.store.ProductByID(context.Background(), userID(t, app, "alice@example.fr"), noirID)
	if p.Brand != "eSun" || p.Material != "PLA+" || p.ColorName != "Galaxy Black" || p.Finish != "silk" || p.ColorHex != "#1b1b1f" || p.EAN != "6938936716785" {
		t.Errorf("produit resynchronisé : %+v", p.ProductFields)
	}
	if r := alice.get("/stock"); strings.Count(r.Body, "Galaxy Black") != 2 {
		t.Error("les 2 bobines doivent refléter la référence")
	}
	if r := alice.get("/products/" + ids["Noir"]); strings.Contains(r.Body, "Différences avec la référence") {
		t.Error("plus d'écart après resynchronisation")
	}

	// Resynchronisation groupée : le produit restant.
	alice.post("/products/resync", nil)
	if r := alice.get("/products"); strings.Contains(r.Body, "≠ référence") || !strings.Contains(r.Body, "Teal") {
		t.Error("tous les produits divergents doivent être resynchronisés")
	}
	if r := alice.get("/products"); !strings.Contains(r.Body, "Sans code") {
		t.Error("un produit sans code-barre ne doit pas être touché")
	}
}
