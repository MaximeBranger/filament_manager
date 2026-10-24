package web

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/fxamacker/cbor/v2"
)

var b64 = base64.RawURLEncoding

// authenticator simule un authentificateur WebAuthn (attestation « none », clé ES256).
type authenticator struct {
	key        *ecdsa.PrivateKey
	credID     []byte
	userHandle []byte
	counter    uint32
	rpID       string
	origin     string
}

func newAuthenticator(t *testing.T) *authenticator {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id := make([]byte, 16)
	rand.Read(id)
	return &authenticator{key: key, credID: id, rpID: "filaments.test", origin: "http://filaments.test"}
}

func (a *authenticator) authData(flags byte, attested []byte) []byte {
	rp := sha256.Sum256([]byte(a.rpID))
	out := append(rp[:], flags)
	out = binary.BigEndian.AppendUint32(out, a.counter)
	return append(out, attested...)
}

func (a *authenticator) clientData(kind, challenge string) []byte {
	cd, _ := json.Marshal(map[string]any{"type": kind, "challenge": challenge, "origin": a.origin, "crossOrigin": false})
	return cd
}

// create répond à navigator.credentials.create().
func (a *authenticator) create(options map[string]any) map[string]any {
	pk := options["publicKey"].(map[string]any)
	a.userHandle, _ = b64.DecodeString(pk["user"].(map[string]any)["id"].(string))

	pub := a.key.PublicKey
	x, y := make([]byte, 32), make([]byte, 32)
	pub.X.FillBytes(x)
	pub.Y.FillBytes(y)
	coseKey, _ := cbor.Marshal(map[int]any{1: 2, 3: -7, -1: 1, -2: x, -3: y})
	attested := make([]byte, 16) // AAGUID nul
	attested = binary.BigEndian.AppendUint16(attested, uint16(len(a.credID)))
	attested = append(append(attested, a.credID...), coseKey...)
	attObj, _ := cbor.Marshal(map[string]any{"fmt": "none", "attStmt": map[string]any{}, "authData": a.authData(0x45, attested)})

	return map[string]any{
		"id": b64.EncodeToString(a.credID), "rawId": b64.EncodeToString(a.credID), "type": "public-key",
		"response": map[string]any{
			"clientDataJSON":    b64.EncodeToString(a.clientData("webauthn.create", pk["challenge"].(string))),
			"attestationObject": b64.EncodeToString(attObj),
		},
	}
}

// get répond à navigator.credentials.get().
func (a *authenticator) get(options map[string]any) map[string]any {
	pk := options["publicKey"].(map[string]any)
	a.counter++
	authData := a.authData(0x05, nil)
	clientData := a.clientData("webauthn.get", pk["challenge"].(string))
	cdHash := sha256.Sum256(clientData)
	digest := sha256.Sum256(append(append([]byte{}, authData...), cdHash[:]...))
	sig, _ := ecdsa.SignASN1(rand.Reader, a.key, digest[:])
	return map[string]any{
		"id": b64.EncodeToString(a.credID), "rawId": b64.EncodeToString(a.credID), "type": "public-key",
		"response": map[string]any{
			"clientDataJSON":    b64.EncodeToString(clientData),
			"authenticatorData": b64.EncodeToString(authData),
			"signature":         b64.EncodeToString(sig),
			"userHandle":        b64.EncodeToString(a.userHandle),
		},
	}
}

func (c *client) postJSONMap(path string, v any) (response, map[string]any) {
	body, _ := json.Marshal(v)
	req, _ := http.NewRequest(http.MethodPost, c.app.srv.URL+path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r := c.do(req)
	var out map[string]any
	json.Unmarshal([]byte(r.Body), &out)
	return r, out
}

func registerPasskey(t *testing.T, c *client, a *authenticator, name string) {
	t.Helper()
	r, options := c.postJSONMap("/passkeys/register/begin", map[string]string{"name": name})
	if r.Status != http.StatusOK {
		t.Fatalf("début d'enregistrement : %d %s", r.Status, r.Body)
	}
	sel := options["publicKey"].(map[string]any)["authenticatorSelection"].(map[string]any)
	if sel["residentKey"] != "required" {
		t.Errorf("la passkey doit être découvrable : %v", sel)
	}
	if r, _ := c.postJSONMap("/passkeys/register/finish", a.create(options)); r.Status != http.StatusOK {
		t.Fatalf("fin d'enregistrement : %d %s", r.Status, r.Body)
	}
}

func TestPasskeyRegisterAndLogin(t *testing.T) {
	app := newTestApp(t)
	alice := app.signup("alice@example.fr", "Alice")
	a := newAuthenticator(t)
	registerPasskey(t, alice, a, "iPhone d'Alice")
	if r := alice.get("/account"); !strings.Contains(r.Body, "iPhone d&#39;Alice") {
		t.Error("la passkey n'apparaît pas dans le compte")
	}

	// Connexion depuis un autre navigateur, sans email.
	browser := app.newClient()
	before := app.mail.Count()
	r, options := browser.postJSONMap("/auth/passkey/begin", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("début de connexion : %d", r.Status)
	}
	r, out := browser.postJSONMap("/auth/passkey/finish?next="+url.QueryEscape("/transfers"), a.get(options))
	if r.Status != http.StatusOK || out["redirect"] != "/transfers" {
		t.Fatalf("fin de connexion : %d %s", r.Status, r.Body)
	}
	if r := browser.get("/stock"); r.Status != http.StatusOK {
		t.Errorf("session non ouverte : %d", r.Status)
	}
	if app.mail.Count() != before {
		t.Error("la connexion par passkey ne doit envoyer aucun email")
	}
	passkeys, _ := app.store.ListPasskeys(context.Background(), userID(t, app, "alice@example.fr"))
	if passkeys[0].LastUsedAt.IsZero() || !strings.Contains(passkeys[0].Data, `"signCount":1`) {
		t.Errorf("compteur de signatures non mis à jour : %s", passkeys[0].Data)
	}

	// Un défi ne sert qu'une fois : rejouer la même réponse échoue.
	replay := a.get(options)
	if r, _ := app.newClient().postJSONMap("/auth/passkey/finish", replay); r.Status != http.StatusBadRequest {
		t.Errorf("réponse rejouée sans défi : %d", r.Status)
	}

	// Compteur qui recule : passkey possiblement clonée, refusée.
	clone := app.newClient()
	_, options = clone.postJSONMap("/auth/passkey/begin", nil)
	a.counter = 0
	if r, _ := clone.postJSONMap("/auth/passkey/finish", a.get(options)); r.Status != http.StatusUnauthorized {
		t.Errorf("passkey clonée acceptée : %d", r.Status)
	}
}

func TestPasskeyRejections(t *testing.T) {
	app := newTestApp(t)
	alice := app.signup("alice@example.fr", "Alice")
	bob := app.signup("bob@example.fr", "Bob")
	a := newAuthenticator(t)
	registerPasskey(t, alice, a, "Clé")

	// Mauvaise origine (site de phishing).
	phishing := app.newClient()
	_, options := phishing.postJSONMap("/auth/passkey/begin", nil)
	a.origin = "https://filaments-test.evil"
	if r, _ := phishing.postJSONMap("/auth/passkey/finish", a.get(options)); r.Status != http.StatusUnauthorized {
		t.Errorf("origine étrangère acceptée : %d", r.Status)
	}
	a.origin = "http://filaments.test"

	// Signature d'une autre clé.
	forged := app.newClient()
	_, options = forged.postJSONMap("/auth/passkey/begin", nil)
	other := newAuthenticator(t)
	other.credID, other.userHandle = a.credID, a.userHandle
	if r, _ := forged.postJSONMap("/auth/passkey/finish", other.get(options)); r.Status != http.StatusUnauthorized {
		t.Errorf("signature d'une autre clé acceptée : %d", r.Status)
	}

	// Bob ne peut pas supprimer la passkey d'Alice ; Alice le peut, et elle ne sert plus.
	passkeys, _ := app.store.ListPasskeys(context.Background(), userID(t, app, "alice@example.fr"))
	id := strconv.FormatInt(passkeys[0].ID, 10)
	if r := bob.post("/passkeys/"+id+"/delete", nil); r.Status != http.StatusNotFound {
		t.Errorf("Bob supprime la passkey d'Alice : %d", r.Status)
	}
	alice.post("/passkeys/"+id+"/delete", nil)
	c := app.newClient()
	_, options = c.postJSONMap("/auth/passkey/begin", nil)
	if r, _ := c.postJSONMap("/auth/passkey/finish", a.get(options)); r.Status != http.StatusUnauthorized {
		t.Errorf("passkey supprimée encore acceptée : %d", r.Status)
	}

	// Un enregistrement ne peut pas être terminé par un autre compte.
	_, options = alice.postJSONMap("/passkeys/register/begin", nil)
	if r, _ := bob.postJSONMap("/passkeys/register/finish", newAuthenticator(t).create(options)); r.Status != http.StatusBadRequest {
		t.Errorf("enregistrement terminé par un autre compte : %d", r.Status)
	}
}
