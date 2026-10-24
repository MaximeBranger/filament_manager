package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/MaximeBranger/filament_manager/internal/auth"
	mailer "github.com/MaximeBranger/filament_manager/internal/mail"
	"github.com/MaximeBranger/filament_manager/internal/store"
)

const (
	maxCodeAttempts = 5
	signupTTL       = 30 * time.Minute
	maxNameLength   = 50
)

func (s *Server) loginForm(w http.ResponseWriter, r *http.Request) {
	if currentUser(r) != nil {
		http.Redirect(w, r, "/stock", http.StatusSeeOther)
		return
	}
	s.render(w, r, http.StatusOK, "login", map[string]any{"Next": safeNext(r.URL.Query().Get("next"))})
}

func normalizeEmail(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	addr, err := mail.ParseAddress(raw)
	if err != nil || addr.Address != raw || len(raw) > 254 {
		return "", false
	}
	return strings.ToLower(raw), true
}

func (s *Server) loginSubmit(w http.ResponseWriter, r *http.Request) {
	next := safeNext(r.FormValue("next"))
	email, ok := normalizeEmail(r.FormValue("email"))
	if !ok {
		s.render(w, r, http.StatusUnprocessableEntity, "login", map[string]any{
			"Next": next, "Email": r.FormValue("email"), "Error": "Adresse email invalide.",
		})
		return
	}

	browser := ""
	if c, err := r.Cookie(browserCookie); err == nil && len(c.Value) >= 32 {
		browser = c.Value
	} else {
		browser = auth.NewToken()
	}
	s.setCookie(w, browserCookie, browser, time.Now().Add(24*time.Hour))

	ip := auth.ClientIP(r, s.cfg.TrustedProxies)
	if !s.ipRate.Allow(ip) || !s.emailRate.Allow(email) {
		// Même réponse qu'en cas de succès : on ne révèle rien.
		s.log.Warn("limite d'envoi de liens atteinte", "ip", ip)
	} else if err := s.sendLoginEmail(r.Context(), email, browser, next); err != nil {
		s.log.Error("envoi du lien de connexion", "err", err)
	}
	http.Redirect(w, r, "/login/code", http.StatusSeeOther)
}

func (s *Server) sendLoginEmail(ctx context.Context, email, browser, next string) error {
	token, code := auth.NewToken(), auth.NewCode()
	tokenHash := auth.HashToken(token)
	_, err := s.store.CreateChallenge(ctx, &store.Challenge{
		Email:       email,
		TokenHash:   tokenHash,
		CodeHash:    auth.HashCode(s.cfg.SessionSecret, tokenHash, code),
		BrowserHash: auth.HashToken(browser),
		NextPath:    next,
		ExpiresAt:   time.Now().Add(s.cfg.MagicLinkTTL),
	})
	if err != nil {
		return err
	}
	link := s.cfg.BaseURL.String() + "/auth/verify?t=" + token
	minutes := int(s.cfg.MagicLinkTTL.Minutes())
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	return s.mailer.Send(ctx, mailer.Message{
		To:      email,
		Subject: "Votre connexion à FilamentManager",
		Text: fmt.Sprintf(`Bonjour,

Pour vous connecter à FilamentManager, ouvrez ce lien (valable %d minutes) :

%s

Ou saisissez ce code dans la page restée ouverte sur votre navigateur :

    %s

Si vous n'êtes pas à l'origine de cette demande, ignorez simplement cet email.

--
FilamentManager
`, minutes, link, code),
	})
}

func (s *Server) codeForm(w http.ResponseWriter, r *http.Request) {
	if _, err := r.Cookie(browserCookie); err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	s.render(w, r, http.StatusOK, "login_code", nil)
}

func (s *Server) codeSubmit(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie(browserCookie)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	expired := func() {
		s.render(w, r, http.StatusUnprocessableEntity, "login_code", map[string]any{
			"Error": "Ce code a expiré ou a été trop souvent mal saisi. Demandez un nouvel email.", "Expired": true,
		})
	}
	ch, err := s.store.LatestChallengeForBrowser(r.Context(), auth.HashToken(c.Value))
	if errors.Is(err, store.ErrNotFound) || (err == nil && ch.Attempts >= maxCodeAttempts) {
		expired()
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := s.store.IncrementChallengeAttempts(r.Context(), ch.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	code := strings.ReplaceAll(strings.TrimSpace(r.FormValue("code")), " ", "")
	if !auth.Equal(auth.HashCode(s.cfg.SessionSecret, ch.TokenHash, code), ch.CodeHash) {
		left := maxCodeAttempts - ch.Attempts - 1
		if left <= 0 {
			expired()
			return
		}
		s.render(w, r, http.StatusUnprocessableEntity, "login_code", map[string]any{
			"Error": "Code incorrect. Encore " + strconv.Itoa(left) + " essai(s).",
		})
		return
	}
	s.completeLogin(w, r, ch)
}

// verifyForm demande une confirmation avant de consommer le lien : les antivirus de
// messagerie qui pré-ouvrent les liens ne doivent pas le griller.
func (s *Server) verifyForm(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("t")
	if _, err := s.store.ChallengeByToken(r.Context(), auth.HashToken(token)); err != nil {
		s.errorPage(w, r, http.StatusGone, "Lien expiré", "Ce lien de connexion a expiré ou a déjà été utilisé. Demandez-en un nouveau.")
		return
	}
	s.render(w, r, http.StatusOK, "verify", map[string]any{"Token": token})
}

func (s *Server) verifySubmit(w http.ResponseWriter, r *http.Request) {
	ch, err := s.store.ChallengeByToken(r.Context(), auth.HashToken(r.FormValue("t")))
	if err != nil {
		s.errorPage(w, r, http.StatusGone, "Lien expiré", "Ce lien de connexion a expiré ou a déjà été utilisé. Demandez-en un nouveau.")
		return
	}
	s.completeLogin(w, r, ch)
}

func (s *Server) completeLogin(w http.ResponseWriter, r *http.Request, ch *store.Challenge) {
	if err := s.store.ConsumeChallenge(r.Context(), ch.ID); err != nil {
		s.errorPage(w, r, http.StatusGone, "Lien expiré", "Ce lien de connexion a déjà été utilisé. Demandez-en un nouveau.")
		return
	}
	user, err := s.store.UserByEmail(r.Context(), ch.Email)
	if errors.Is(err, store.ErrNotFound) {
		// Première connexion : on demande un nom d'affichage avant de créer le compte.
		value := url.Values{
			"e": {ch.Email},
			"x": {strconv.FormatInt(time.Now().Add(signupTTL).Unix(), 10)},
			"n": {ch.NextPath},
		}.Encode()
		s.setCookie(w, signupCookie, auth.Sign(s.cfg.SessionSecret, value), time.Now().Add(signupTTL))
		http.Redirect(w, r, "/signup", http.StatusSeeOther)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.startSession(w, r, user.ID, ch.NextPath)
}

// createSession ouvre une session et pose son cookie.
func (s *Server) createSession(w http.ResponseWriter, r *http.Request, userID int64) error {
	token := auth.NewToken()
	expires := time.Now().Add(s.cfg.SessionTTL)
	if err := s.store.CreateSession(r.Context(), auth.HashToken(token), userID, expires); err != nil {
		return err
	}
	s.setCookie(w, sessionCookie, token, expires)
	s.clearCookie(w, browserCookie)
	return nil
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, userID int64, next string) {
	if err := s.createSession(w, r, userID); err != nil {
		s.serverError(w, r, err)
		return
	}
	if next = safeNext(next); next == "" {
		next = "/stock"
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// pendingSignup lit le cookie signé posé après validation de l'email.
func (s *Server) pendingSignup(r *http.Request) (email, next string, ok bool) {
	c, err := r.Cookie(signupCookie)
	if err != nil {
		return "", "", false
	}
	value, ok := auth.Verify(s.cfg.SessionSecret, c.Value)
	if !ok {
		return "", "", false
	}
	v, err := url.ParseQuery(value)
	if err != nil || v.Get("e") == "" {
		return "", "", false
	}
	exp, err := strconv.ParseInt(v.Get("x"), 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return "", "", false
	}
	return v.Get("e"), v.Get("n"), true
}

func (s *Server) signupForm(w http.ResponseWriter, r *http.Request) {
	email, _, ok := s.pendingSignup(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	s.render(w, r, http.StatusOK, "signup", map[string]any{"Email": email})
}

func (s *Server) signupSubmit(w http.ResponseWriter, r *http.Request) {
	email, next, ok := s.pendingSignup(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	name := strings.Join(strings.Fields(r.FormValue("display_name")), " ")
	if name == "" || len([]rune(name)) > maxNameLength {
		s.render(w, r, http.StatusUnprocessableEntity, "signup", map[string]any{
			"Email": email, "Name": name, "Error": "Le nom doit faire entre 1 et 50 caractères.",
		})
		return
	}
	user, err := s.store.CreateUser(r.Context(), email, name)
	if err != nil {
		// Double soumission : le compte existe déjà.
		if user, err = s.store.UserByEmail(r.Context(), email); err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	s.clearCookie(w, signupCookie)
	s.log.Info("compte créé", "user_id", user.ID)
	s.startSession(w, r, user.ID, next)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if hash, ok := r.Context().Value(sessionKey).(string); ok {
		if err := s.store.DeleteSession(r.Context(), hash); err != nil {
			s.log.Error("suppression de session", "err", err)
		}
	}
	s.clearCookie(w, sessionCookie)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
