package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"runtime/debug"
	"strings"
	"time"

	"github.com/MaximeBranger/filament_manager/internal/auth"
	"github.com/MaximeBranger/filament_manager/internal/store"
)

type ctxKey int

const (
	userKey ctxKey = iota
	sessionKey
)

const (
	sessionCookie = "session"
	browserCookie = "login_browser"
	signupCookie  = "signup"
	flashCookie   = "flash"
)

func currentUser(r *http.Request) *store.User {
	u, _ := r.Context().Value(userKey).(*store.User)
	return u
}

func currentSession(r *http.Request) *store.Session {
	sess, _ := r.Context().Value(sessionKey).(*store.Session)
	return sess
}

// reauthWindow est l'ancienneté maximale de la connexion pour les actions sensibles (passkeys,
// suppression du compte) : une session volée ne suffit pas à s'installer durablement.
const reauthWindow = 15 * time.Minute

const reauthURL = "/login?reauth=1&next=%2Faccount"

const reauthMessage = "Par sécurité, confirmez votre identité pour effectuer cette action."

// recentLogin indique si la session courante a été ouverte il y a moins de reauthWindow.
func (s *Server) recentLogin(r *http.Request) bool {
	sess := currentSession(r)
	return sess != nil && s.store.Now().Sub(sess.CreatedAt) < reauthWindow
}

// requireRecentLogin renvoie vers la connexion si la session est trop ancienne ; false si la
// réponse a été envoyée.
func (s *Server) requireRecentLogin(w http.ResponseWriter, r *http.Request) bool {
	if s.recentLogin(r) {
		return true
	}
	s.setFlash(w, reauthMessage)
	http.Redirect(w, r, reauthURL, http.StatusSeeOther)
	return false
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// logRequests journalise chaque requête. Seul le chemin est loggé : la query string peut
// contenir un jeton de connexion.
func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			return
		}
		// Les chemins d'invitation et de partage contiennent un jeton secret.
		path := r.URL.Path
		if strings.HasPrefix(path, "/invite/") {
			path = "/invite/…"
		} else if strings.HasPrefix(path, "/share/") {
			path = "/share/…"
		}
		s.log.Info("http", "method", r.Method, "path", path, "status", sw.status,
			"duration_ms", time.Since(start).Milliseconds())
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				s.log.Error("panic", "err", v, "stack", string(debug.Stack()))
				http.Error(w, "Erreur interne.", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", s.csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(self), microphone=(), geolocation=()")
		h.Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

// loadSession place l'utilisateur connecté dans le contexte et prolonge la session
// au plus une fois par jour.
func (s *Server) loadSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil || c.Value == "" || r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			next.ServeHTTP(w, r)
			return
		}
		hash := auth.HashToken(c.Value)
		sess, err := s.store.SessionByHash(r.Context(), hash)
		if err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				s.log.Error("lecture de session", "err", err)
			}
			s.clearCookie(w, sessionCookie)
			next.ServeHTTP(w, r)
			return
		}
		user, err := s.store.UserByID(r.Context(), sess.UserID)
		if err != nil {
			s.clearCookie(w, sessionCookie)
			next.ServeHTTP(w, r)
			return
		}
		if time.Since(sess.LastSeenAt) > 24*time.Hour {
			expires := time.Now().Add(s.cfg.SessionTTL)
			if err := s.store.RenewSession(r.Context(), hash, expires); err == nil {
				s.setCookie(w, sessionCookie, c.Value, expires)
			}
		}
		ctx := context.WithValue(r.Context(), userKey, user)
		ctx = context.WithValue(ctx, sessionKey, sess)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) requireUser(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if currentUser(r) == nil {
			target := "/login"
			if r.Method == http.MethodGet {
				target += "?next=" + url.QueryEscape(r.URL.RequestURI())
			}
			if r.Header.Get("HX-Request") != "" {
				w.Header().Set("HX-Redirect", target)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			http.Redirect(w, r, target, http.StatusSeeOther)
			return
		}
		next(w, r)
	})
}

func (s *Server) setCookie(w http.ResponseWriter, name, value string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   s.cfg.SecureCookies(),
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.cfg.SecureCookies(),
		SameSite: http.SameSiteLaxMode,
	})
}

// setFlash mémorise un message affiché sur la page suivante.
func (s *Server) setFlash(w http.ResponseWriter, msg string) {
	s.setCookie(w, flashCookie, url.QueryEscape(msg), time.Now().Add(time.Minute))
}

func (s *Server) takeFlash(w http.ResponseWriter, r *http.Request) string {
	c, err := r.Cookie(flashCookie)
	if err != nil {
		return ""
	}
	s.clearCookie(w, flashCookie)
	msg, _ := url.QueryUnescape(c.Value)
	return msg
}

// safeNext n'accepte que des chemins locaux pour éviter les redirections ouvertes.
func safeNext(next string) string {
	if len(next) < 1 || next[0] != '/' || (len(next) > 1 && (next[1] == '/' || next[1] == '\\')) {
		return ""
	}
	u, err := url.Parse(next)
	if err != nil || u.Host != "" || u.Scheme != "" {
		return ""
	}
	return next
}
