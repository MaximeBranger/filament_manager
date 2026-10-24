package web

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/MaximeBranger/filament_manager/internal/store"
)

var inviteRe = regexp.MustCompile(`/invite/([A-Za-z0-9_-]+)`)

func userID(t *testing.T, app *testApp, email string) int64 {
	t.Helper()
	u, err := app.store.UserByEmail(context.Background(), email)
	if err != nil {
		t.Fatal(err)
	}
	return u.ID
}

// makeFriends fait inviter b par a et renvoie le chemin d'invitation utilisé.
func makeFriends(t *testing.T, a, b *client) string {
	t.Helper()
	r := a.post("/friends/invitations", nil)
	m := inviteRe.FindStringSubmatch(r.Body)
	if r.Status != http.StatusOK || m == nil {
		t.Fatalf("création d'invitation : %d", r.Status)
	}
	path := "/invite/" + m[1]
	if r := b.post(path, nil); r.Status != http.StatusSeeOther || !strings.HasPrefix(r.Location, "/stock?scope=") {
		t.Fatalf("acceptation : %d %s", r.Status, r.Location)
	}
	return path
}

func TestInvitationFlow(t *testing.T) {
	app := newTestApp(t)
	alice := app.signup("alice@example.fr", "Alice")

	r := alice.post("/friends/invitations", nil)
	m := inviteRe.FindStringSubmatch(r.Body)
	if m == nil || !strings.Contains(r.Body, "http://filaments.test/invite/") {
		t.Fatalf("lien d'invitation absent :\n%s", r.Body)
	}
	path := "/invite/" + m[1]

	// Un visiteur non connecté est envoyé vers la connexion, puis revient sur l'invitation.
	anon := app.newClient()
	if r := anon.get(path); r.Status != http.StatusSeeOther || r.Location != "/login?next="+url.QueryEscape(path) {
		t.Errorf("invitation anonyme : %d %s", r.Status, r.Location)
	}
	if r := alice.get(path); !strings.Contains(r.Body, "votre propre lien") {
		t.Error("l'invitant doit être prévenu qu'il s'agit de son lien")
	}

	bob := app.signup("bob@example.fr", "Bob")
	if r := bob.get(path); r.Status != http.StatusOK || !strings.Contains(r.Body, "<strong>Alice</strong> vous invite") {
		t.Errorf("page d'invitation : %d", r.Status)
	}
	r = bob.post(path, nil)
	if r.Location != "/stock?scope="+strconv.FormatInt(userID(t, app, "alice@example.fr"), 10) {
		t.Errorf("acceptation : %s", r.Location)
	}
	if m, ok := app.mail.Last("alice@example.fr"); !ok || !strings.Contains(m.Subject, "Bob a accepté votre invitation") {
		t.Errorf("email à l'invitant : %+v", m)
	}
	if r := bob.post(path, nil); r.Status != http.StatusGone {
		t.Errorf("invitation réutilisée : %d", r.Status)
	}
	if r := alice.get("/friends"); !strings.Contains(r.Body, "<strong>Bob</strong>") {
		t.Error("Bob absent de la liste d'amis d'Alice")
	}
	if r := alice.get("/stock"); !strings.Contains(r.Body, `class="badge-count">1<`) {
		t.Error("badge de notification absent")
	}
	if r := alice.get("/notifications"); !strings.Contains(r.Body, "Bob a accepté votre invitation") {
		t.Error("notification absente")
	}
	if r := alice.get("/stock"); strings.Contains(r.Body, "badge-count") {
		t.Error("la notification devrait être marquée lue")
	}
}

func TestInvitationRevoke(t *testing.T) {
	app := newTestApp(t)
	alice := app.signup("alice@example.fr", "Alice")
	bob := app.signup("bob@example.fr", "Bob")
	m := inviteRe.FindStringSubmatch(alice.post("/friends/invitations", nil).Body)
	invs, _ := app.store.ListInvitations(context.Background(), userID(t, app, "alice@example.fr"))

	if r := bob.post("/friends/invitations/"+strconv.FormatInt(invs[0].ID, 10)+"/revoke", nil); r.Status != http.StatusNotFound {
		t.Errorf("révocation par un tiers : %d", r.Status)
	}
	alice.post("/friends/invitations/"+strconv.FormatInt(invs[0].ID, 10)+"/revoke", nil)
	if r := bob.get("/invite/" + m[1]); r.Status != http.StatusGone {
		t.Errorf("invitation révoquée : %d", r.Status)
	}
}

func TestFriendStockVisibility(t *testing.T) {
	app := newTestApp(t)
	alice := app.signup("alice@example.fr", "Alice")
	bob := app.signup("bob@example.fr", "Bob")
	carol := app.signup("carol@example.fr", "Carol")
	makeFriends(t, alice, bob)
	aliceID := strconv.FormatInt(userID(t, app, "alice@example.fr"), 10)

	alice.post("/spools", newProductForm("color_name", "Visible", "notes", "note très privée", "price", "42", "supplier", "Fournisseur secret"))
	alice.post("/spools", newProductForm("color_name", "CouleurMasquee", "hidden", "on"))
	alice.post("/spools", newProductForm("color_name", "CouleurVide", "level", "empty"))

	r := bob.get("/stock?scope=" + aliceID)
	if r.Status != http.StatusOK || !strings.Contains(r.Body, "Stock de Alice") || !strings.Contains(r.Body, "Visible") {
		t.Fatalf("stock d'Alice vu par Bob : %d", r.Status)
	}
	for _, leak := range []string{"CouleurMasquee", "CouleurVide", "note très privée", "42", "Fournisseur secret", `hx-post="/spools/`} {
		if strings.Contains(r.Body, leak) {
			t.Errorf("Bob voit %q", leak)
		}
	}
	if r := bob.get("/stock?scope=friends"); !strings.Contains(r.Body, ">Alice</a>") {
		t.Error("la vue « tous mes amis » doit nommer le propriétaire")
	}
	if r := carol.get("/stock?scope=" + aliceID); r.Status != http.StatusNotFound {
		t.Errorf("Carol, non amie, voit le stock d'Alice : %d", r.Status)
	}
	if r := carol.get("/stock?scope=friends"); strings.Contains(r.Body, "Visible") {
		t.Error("Carol voit le stock d'Alice via « tous mes amis »")
	}
	spoolID := spoolIDs(alice.get("/stock").Body)[0]
	if r := carol.get("/transfers/new?kind=loan&to=" + aliceID + "&spool=" + spoolID); r.Status != http.StatusSeeOther {
		t.Errorf("Carol peut préparer une demande chez Alice : %d", r.Status)
	}
	if r := carol.post("/transfers", url.Values{"kind": {"loan"}, "to": {aliceID}, "spool": {spoolID}}); !strings.HasPrefix(r.Location, "/transfers") {
		t.Errorf("demande de Carol : %d", r.Status)
	}
	if n, _ := app.store.UnreadCount(context.Background(), userID(t, app, "alice@example.fr")); n != 1 {
		t.Errorf("Alice ne devrait avoir que la notification d'amitié, %d", n)
	}
}

func TestLoanFlow(t *testing.T) {
	app := newTestApp(t)
	alice := app.signup("alice@example.fr", "Alice")
	bob := app.signup("bob@example.fr", "Bob")
	makeFriends(t, alice, bob)
	aliceID := strconv.FormatInt(userID(t, app, "alice@example.fr"), 10)
	alice.post("/spools", newProductForm("quantity", "2"))
	ids := spoolIDs(alice.get("/stock").Body)

	// Bob sélectionne une bobine chez Alice et demande un prêt.
	r := bob.get("/transfers/new?kind=loan&to=" + aliceID + "&spool=" + ids[0])
	if r.Status != http.StatusOK || !strings.Contains(r.Body, "Demander un prêt") {
		t.Fatalf("formulaire de demande : %d", r.Status)
	}
	r = bob.post("/transfers", url.Values{"kind": {"loan"}, "to": {aliceID}, "spool": {ids[0]}, "message": {"Pour un casque"}, "grams": {"300"}})
	if r.Location != "/transfers" {
		t.Fatalf("demande : %d %s", r.Status, r.Location)
	}
	if m, _ := app.mail.Last("alice@example.fr"); !strings.Contains(m.Subject, "Bob demande à vous emprunter") || !strings.Contains(m.Text, "http://filaments.test/transfers") {
		t.Errorf("email de demande : %+v", m)
	}

	r = alice.get("/transfers")
	if !strings.Contains(r.Body, "Demandes à traiter") || !strings.Contains(r.Body, "Pour un casque") || !strings.Contains(r.Body, "300 g") {
		t.Fatal("la demande n'apparaît pas chez Alice")
	}
	transfers, _ := app.store.ListTransfers(context.Background(), userID(t, app, "alice@example.fr"), 10)
	tid := strconv.FormatInt(transfers[0].ID, 10)
	if r := bob.post("/transfers/"+tid+"/decide", url.Values{"accept": {ids[0]}}); r.Status != http.StatusNotFound {
		t.Errorf("Bob accepte sa propre demande : %d", r.Status)
	}
	alice.post("/transfers/"+tid+"/decide", url.Values{"action": {"accept"}, "accept": {ids[0]}})
	if m, _ := app.mail.Last("bob@example.fr"); !strings.Contains(m.Subject, "Alice a accepté de vous prêter") {
		t.Errorf("email d'acceptation : %+v", m)
	}

	// Pendant le prêt.
	if r := alice.get("/stock"); !strings.Contains(r.Body, "Prêtée à Bob") {
		t.Error("la bobine prêtée n'est pas signalée")
	}
	if r := alice.get("/stock?avail=lent"); len(spoolIDs(r.Body)) != 1 {
		t.Error("filtre « prêtées » incorrect")
	}
	if r := alice.post("/spools/"+ids[0]+"/level", url.Values{"level": {"empty"}}, "HX-Request", "true"); strings.Contains(r.Body, "level-empty") {
		t.Error("le niveau d'une bobine prêtée a changé")
	}
	if r := alice.post("/spools/"+ids[0]+"/delete", nil); r.Location != "/spools/"+ids[0] {
		t.Errorf("suppression d'une bobine prêtée : %s", r.Location)
	}
	if r := bob.get("/stock?scope=" + aliceID); !strings.Contains(r.Body, "Empruntée par vous") {
		t.Error("Bob ne voit pas qu'il emprunte la bobine")
	}
	if r := bob.get("/transfers"); !strings.Contains(r.Body, "Prêtée par Alice") {
		t.Error("la bobine n'apparaît pas dans les emprunts de Bob")
	}
	bobID := strconv.FormatInt(userID(t, app, "bob@example.fr"), 10)
	alice.post("/friends/"+bobID+"/remove", nil)
	if ok, _ := app.store.AreFriends(context.Background(), userID(t, app, "alice@example.fr"), userID(t, app, "bob@example.fr")); !ok {
		t.Error("amitié retirée malgré un prêt en cours")
	}
	if r := bob.post("/account/delete", url.Values{"confirm_email": {"bob@example.fr"}}); r.Status != http.StatusConflict {
		t.Errorf("suppression du compte de Bob pendant un emprunt : %d", r.Status)
	}

	// Retour : pas d'email, mais une notification.
	before := app.mail.Count()
	loans, _ := app.store.ListLoans(context.Background(), userID(t, app, "alice@example.fr"), 10)
	lid := strconv.FormatInt(loans[0].ID, 10)
	if r := bob.post("/loans/"+lid+"/return", url.Values{"level": {"low"}}); r.Status != http.StatusNotFound {
		t.Errorf("retour déclaré par l'emprunteur : %d", r.Status)
	}
	alice.post("/loans/"+lid+"/return", url.Values{"level": {"low"}})
	if app.mail.Count() != before {
		t.Error("le retour ne doit pas envoyer d'email")
	}
	if r := alice.get("/spools/" + ids[0]); !strings.Contains(r.Body, `value="low" selected`) || strings.Contains(r.Body, "Prêtée à") {
		t.Error("le niveau saisi au retour n'est pas appliqué")
	}
}

func TestGiftFlows(t *testing.T) {
	app := newTestApp(t)
	alice := app.signup("alice@example.fr", "Alice")
	bob := app.signup("bob@example.fr", "Bob")
	makeFriends(t, alice, bob)
	bobID := strconv.FormatInt(userID(t, app, "bob@example.fr"), 10)
	alice.post("/spools", newProductForm("quantity", "2", "notes", "secret", "ean", "7777777777777"))
	ids := spoolIDs(alice.get("/stock").Body)

	if r := alice.get("/spools/" + ids[0]); !strings.Contains(r.Body, "Prêter ou donner") {
		t.Fatal("section prêt/don absente")
	}

	// Proposition de don, acceptée par Bob.
	alice.post("/transfers", url.Values{"kind": {"gift"}, "to": {bobID}, "spool": {ids[0]}, "level_" + ids[0]: {"half"}})
	if m, _ := app.mail.Last("bob@example.fr"); !strings.Contains(m.Subject, "Alice vous propose de vous donner") {
		t.Errorf("email de proposition : %+v", m)
	}
	transfers, _ := app.store.ListTransfers(context.Background(), userID(t, app, "bob@example.fr"), 10)
	bob.post("/transfers/"+strconv.FormatInt(transfers[0].ID, 10)+"/decide", url.Values{"accept": {ids[0]}})
	r := bob.get("/stock")
	if len(spoolIDs(r.Body)) != 1 || strings.Contains(r.Body, "secret") {
		t.Fatalf("bobine reçue par Bob : %d", len(spoolIDs(r.Body)))
	}
	if r := bob.get("/spools/" + ids[0]); !strings.Contains(r.Body, `value="half" selected`) || strings.Contains(r.Body, "secret") {
		t.Error("niveau non transmis ou notes divulguées")
	}
	if r := alice.get("/transfers"); !strings.Contains(r.Body, "Donnée à Bob") {
		t.Error("historique du don absent chez Alice")
	}

	// Prêt direct puis « garde-la ». Bob ne veut plus d'emails.
	bob.post("/account/notifications", url.Values{})
	before := app.mail.Count()
	alice.post("/transfers", url.Values{"kind": {"loan"}, "to": {bobID}, "spool": {ids[1]}})
	loans, _ := app.store.ListLoans(context.Background(), userID(t, app, "alice@example.fr"), 10)
	if len(loans) != 1 || !loans[0].Active() {
		t.Fatalf("prêt direct : %+v", loans)
	}
	alice.post("/loans/"+strconv.FormatInt(loans[0].ID, 10)+"/gift", url.Values{"level": {"low"}})
	if n := len(spoolIDs(bob.get("/stock").Body)); n != 2 {
		t.Errorf("Bob devrait avoir 2 bobines, %d", n)
	}
	if n := len(spoolIDs(alice.get("/stock").Body)); n != 0 {
		t.Errorf("Alice devrait n'avoir plus rien, %d", n)
	}
	if app.mail.Count() != before {
		t.Error("Bob a désactivé les emails mais en a reçu")
	}
	products, _ := app.store.ListProducts(context.Background(), userID(t, app, "bob@example.fr"))
	if len(products) != 1 {
		t.Errorf("les deux bobines (même EAN) doivent partager un produit chez Bob : %d produits", len(products))
	}
}

func TestNotificationTexts(t *testing.T) {
	cases := map[string]store.NotificationPayload{
		"Alice a accepté votre don : eSun PLA Noir · 1 kg.":    {Actor: "Alice", TransferKind: "gift", Count: 1, Label: "eSun PLA Noir · 1 kg", Offer: true},
		"Alice ne peut pas vous prêter les bobines demandées.": {Actor: "Alice", TransferKind: "loan"},
		"Alice demande à vous emprunter 3 bobines.":            {Actor: "Alice", Count: 3},
	}
	kinds := map[string]string{
		"Alice a accepté votre don : eSun PLA Noir · 1 kg.":    store.NotifTransferAccepted,
		"Alice ne peut pas vous prêter les bobines demandées.": store.NotifTransferRefused,
		"Alice demande à vous emprunter 3 bobines.":            store.NotifLoanRequested,
	}
	for want, p := range cases {
		if got, _ := notificationText(store.Notification{Kind: kinds[want], Payload: p}); got != want {
			t.Errorf("%q, attendu %q", got, want)
		}
	}
}
