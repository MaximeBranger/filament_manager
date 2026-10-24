package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// ageSessions fait comme si toutes les connexions dataient d'une heure.
func ageSessions(t *testing.T, app *testApp) {
	t.Helper()
	old := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	if _, err := app.store.DB.Exec(`UPDATE sessions SET created_at = ?`, old); err != nil {
		t.Fatal(err)
	}
}

// Une session ancienne (volée, par exemple) ne suffit pas pour les actions sensibles.
func TestSensitiveActionsRequireRecentLogin(t *testing.T) {
	app := newTestApp(t)
	alice := app.signup("alice@example.fr", "Alice")
	ageSessions(t, app)

	r, body := alice.postJSONMap("/passkeys/register/begin", map[string]string{"name": "Pirate"})
	if r.Status != http.StatusForbidden || body["redirect"] != reauthURL {
		t.Errorf("ajout de passkey avec une session ancienne : %d %s", r.Status, r.Body)
	}
	r = alice.post("/account/delete", url.Values{"confirm_email": {"alice@example.fr"}})
	if r.Status != http.StatusSeeOther || r.Location != reauthURL {
		t.Errorf("suppression du compte avec une session ancienne : %d %s", r.Status, r.Location)
	}
	if _, err := app.store.UserByEmail(context.Background(), "alice@example.fr"); err != nil {
		t.Fatal("compte supprimé sans connexion récente")
	}
	if r := alice.post("/passkeys/1/delete", nil); r.Location != reauthURL {
		t.Errorf("suppression de passkey avec une session ancienne : %d %s", r.Status, r.Location)
	}

	// La page de connexion reste accessible pour confirmer son identité, email prérempli.
	if r := alice.get("/login"); r.Location != "/stock" {
		t.Errorf("connecté, /login doit renvoyer vers le stock : %s", r.Location)
	}
	if r := alice.get(reauthURL); r.Status != http.StatusOK || !strings.Contains(r.Body, `value="alice@example.fr"`) {
		t.Errorf("page de confirmation d'identité : %d", r.Status)
	}
	_, code := alice.loginEmail("alice@example.fr")
	if r := alice.post("/login/code", url.Values{"code": {code}}); r.Status != http.StatusSeeOther {
		t.Fatalf("reconnexion : %d", r.Status)
	}
	if n, _ := app.store.CountSessions(context.Background(), 1); n != 1 {
		t.Errorf("la nouvelle session doit remplacer l'ancienne : %d sessions", n)
	}
	r = alice.post("/account/delete", url.Values{"confirm_email": {"alice@example.fr"}})
	if r.Status != http.StatusSeeOther || r.Location != "/" {
		t.Errorf("suppression après reconnexion : %d %s", r.Status, r.Location)
	}
}

// L'ajout d'une passkey est signalé par email, même notifications désactivées.
func TestPasskeyAddedSendsAlert(t *testing.T) {
	app := newTestApp(t)
	bob := app.signup("bob@example.fr", "Bob")
	bob.post("/account/notifications", url.Values{})
	registerPasskey(t, bob, newAuthenticator(t), "Téléphone")
	m, ok := app.mail.Last("bob@example.fr")
	if !ok || !strings.Contains(m.Subject, "passkey") || !strings.Contains(m.Text, "« Téléphone »") {
		t.Errorf("email d'alerte attendu : %+v", m)
	}
}

func TestRevokeOtherSessions(t *testing.T) {
	app := newTestApp(t)
	laptop := app.signup("carol@example.fr", "Carol")
	phone := app.newClient()
	_, code := phone.loginEmail("carol@example.fr")
	phone.post("/login/code", url.Values{"code": {code}})

	if r := laptop.get("/account"); !strings.Contains(r.Body, "1 autre navigateur") {
		t.Error("la page Compte doit signaler l'autre session")
	}
	r := laptop.post("/account/sessions/revoke", nil)
	if r.Status != http.StatusSeeOther || r.Location != "/account" {
		t.Fatalf("fermeture des autres sessions : %d %s", r.Status, r.Location)
	}
	if r := phone.get("/stock"); r.Status != http.StatusSeeOther || !strings.HasPrefix(r.Location, "/login") {
		t.Errorf("la session fermée reste valide : %d %s", r.Status, r.Location)
	}
	if r := laptop.get("/stock"); r.Status != http.StatusOK {
		t.Errorf("la session courante doit rester ouverte : %d", r.Status)
	}
}
