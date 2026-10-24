package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/MaximeBranger/filament_manager/internal/auth"
	"github.com/MaximeBranger/filament_manager/internal/store"
)

const (
	ceremonyCookie = "webauthn"
	ceremonyTTL    = 5 * time.Minute
	maxPasskeyName = 50
)

// ceremony est un échange WebAuthn en cours (défi à usage unique), gardé en mémoire :
// l'application tourne en une seule instance.
type ceremony struct {
	data    webauthn.SessionData
	userID  int64 // 0 pour une connexion
	name    string
	expires time.Time
}

type ceremonies struct {
	mu sync.Mutex
	m  map[string]ceremony
}

func (c *ceremonies) put(cer ceremony) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for k, v := range c.m {
		if now.After(v.expires) {
			delete(c.m, k)
		}
	}
	key := auth.NewToken()
	cer.expires = now.Add(ceremonyTTL)
	c.m[key] = cer
	return key
}

// take renvoie la cérémonie et la supprime : un défi ne sert qu'une fois.
func (c *ceremonies) take(key string) (ceremony, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cer, ok := c.m[key]
	delete(c.m, key)
	return cer, ok && time.Now().Before(cer.expires)
}

func (s *Server) startCeremony(w http.ResponseWriter, cer ceremony) {
	key := s.ceremonies.put(cer)
	s.setCookie(w, ceremonyCookie, key, time.Now().Add(ceremonyTTL))
}

func (s *Server) takeCeremony(w http.ResponseWriter, r *http.Request) (ceremony, bool) {
	c, err := r.Cookie(ceremonyCookie)
	if err != nil {
		return ceremony{}, false
	}
	s.clearCookie(w, ceremonyCookie)
	return s.ceremonies.take(c.Value)
}

// waUser adapte un utilisateur à l'interface attendue par la bibliothèque WebAuthn.
type waUser struct {
	user  *store.User
	id    []byte
	creds []webauthn.Credential
}

func (u *waUser) WebAuthnID() []byte                         { return u.id }
func (u *waUser) WebAuthnName() string                       { return u.user.Email }
func (u *waUser) WebAuthnDisplayName() string                { return u.user.DisplayName }
func (u *waUser) WebAuthnCredentials() []webauthn.Credential { return u.creds }

func (s *Server) loadWAUser(ctx context.Context, user *store.User) (*waUser, error) {
	id, err := s.store.WebAuthnID(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	passkeys, err := s.store.ListPasskeys(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	wu := &waUser{user: user, id: id}
	for _, p := range passkeys {
		var c webauthn.Credential
		if err := json.Unmarshal([]byte(p.Data), &c); err != nil {
			return nil, err
		}
		wu.creds = append(wu.creds, c)
	}
	return wu, nil
}

func jsonError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// Enregistrement (utilisateur connecté) -------------------------------------------------

func (s *Server) passkeyRegisterBegin(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	if !s.recentLogin(r) {
		s.setFlash(w, reauthMessage)
		writeJSON(w, http.StatusForbidden, map[string]string{"error": reauthMessage, "redirect": reauthURL})
		return
	}
	var body struct{ Name string }
	json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body)
	name := strings.Join(strings.Fields(body.Name), " ")
	if name == "" {
		name = "Passkey du " + time.Now().In(paris).Format("02/01/2006")
	}
	if len([]rune(name)) > maxPasskeyName {
		name = string([]rune(name)[:maxPasskeyName])
	}

	wu, err := s.loadWAUser(r.Context(), user)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	exclude := make([]protocol.CredentialDescriptor, 0, len(wu.creds))
	for _, c := range wu.creds {
		exclude = append(exclude, c.Descriptor())
	}
	creation, session, err := s.webauthn.BeginRegistration(wu,
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired),
		webauthn.WithExclusions(exclude),
	)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.startCeremony(w, ceremony{data: *session, userID: user.ID, name: name})
	writeJSON(w, http.StatusOK, creation)
}

func (s *Server) passkeyRegisterFinish(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	cer, ok := s.takeCeremony(w, r)
	if !ok || cer.userID != user.ID {
		jsonError(w, http.StatusBadRequest, "La demande a expiré, recommencez.")
		return
	}
	wu, err := s.loadWAUser(r.Context(), user)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	cred, err := s.webauthn.FinishRegistration(wu, cer.data, r)
	if err != nil {
		s.log.Warn("enregistrement de passkey refusé", "user_id", user.ID, "err", err)
		jsonError(w, http.StatusBadRequest, "La passkey n'a pas pu être vérifiée.")
		return
	}
	data, _ := json.Marshal(cred)
	if err := s.store.AddPasskey(r.Context(), user.ID, cred.ID, cer.name, string(data)); err != nil {
		if errors.Is(err, store.ErrDuplicatePasskey) {
			jsonError(w, http.StatusConflict, "Cette passkey est déjà enregistrée.")
			return
		}
		s.serverError(w, r, err)
		return
	}
	s.log.Info("passkey ajoutée", "user_id", user.ID)
	s.sendSecurityEmail(user.Email, "FilamentManager : nouvelle passkey ajoutée à votre compte", fmt.Sprintf(`Bonjour %s,

Une passkey « %s » vient d'être ajoutée à votre compte FilamentManager (%s).
Elle permet de se connecter sans email.

Si ce n'est pas vous : connectez-vous avec le lien reçu par email, supprimez cette passkey
et déconnectez les autres sessions depuis la page Mon compte :

%s/account

--
FilamentManager
`, user.DisplayName, cer.name, time.Now().In(paris).Format("02/01/2006 à 15h04"), s.cfg.BaseURL.String()))
	s.setFlash(w, "Passkey « "+cer.name+" » ajoutée : vous pourrez vous connecter sans email.")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) passkeyDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	if !s.requireRecentLogin(w, r) {
		return
	}
	if err := s.store.DeletePasskey(r.Context(), currentUser(r).ID, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.notFound(w, r)
			return
		}
		s.serverError(w, r, err)
		return
	}
	s.setFlash(w, "Passkey supprimée. Pensez à la retirer aussi de votre appareil.")
	http.Redirect(w, r, "/account", http.StatusSeeOther)
}

// Connexion (public) -----------------------------------------------------------------------

func (s *Server) passkeyLoginBegin(w http.ResponseWriter, r *http.Request) {
	if !s.passkeyRate.Allow(auth.ClientIP(r, s.cfg.TrustedProxies)) {
		jsonError(w, http.StatusTooManyRequests, "Trop de tentatives, réessayez dans quelques minutes.")
		return
	}
	assertion, session, err := s.webauthn.BeginDiscoverableLogin()
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.startCeremony(w, ceremony{data: *session})
	writeJSON(w, http.StatusOK, assertion)
}

func (s *Server) passkeyLoginFinish(w http.ResponseWriter, r *http.Request) {
	cer, ok := s.takeCeremony(w, r)
	if !ok || cer.userID != 0 {
		jsonError(w, http.StatusBadRequest, "La demande a expiré, recommencez.")
		return
	}
	var found *waUser
	handler := func(rawID, userHandle []byte) (webauthn.User, error) {
		u, err := s.store.UserByWebAuthnID(r.Context(), userHandle)
		if err != nil {
			return nil, err
		}
		if found, err = s.loadWAUser(r.Context(), u); err != nil {
			return nil, err
		}
		return found, nil
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	_, cred, err := s.webauthn.FinishPasskeyLogin(handler, cer.data, r)
	if err != nil || found == nil {
		s.log.Warn("connexion par passkey refusée", "err", err)
		jsonError(w, http.StatusUnauthorized, "Passkey inconnue ou invalide. Si vous l'avez supprimée de votre compte, connectez-vous par email.")
		return
	}
	if cred.Authenticator.CloneWarning {
		s.log.Warn("passkey possiblement clonée : compteur de signatures incohérent", "user_id", found.user.ID)
		jsonError(w, http.StatusUnauthorized, "Cette passkey a été refusée par précaution. Connectez-vous par email.")
		return
	}
	data, _ := json.Marshal(cred)
	if err := s.store.TouchPasskey(r.Context(), cred.ID, string(data)); err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := s.createSession(w, r, found.user.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	next := safeNext(r.URL.Query().Get("next"))
	if next == "" {
		next = "/stock"
	}
	writeJSON(w, http.StatusOK, map[string]string{"redirect": next})
}
