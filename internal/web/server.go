// Package web expose l'application en HTTP : routes, middlewares, rendu des pages.
package web

import (
	"context"
	"embed"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/MaximeBranger/filament_manager/internal/auth"
	"github.com/MaximeBranger/filament_manager/internal/config"
	"github.com/MaximeBranger/filament_manager/internal/mail"
	"github.com/MaximeBranger/filament_manager/internal/reference"
	"github.com/MaximeBranger/filament_manager/internal/store"
)

//go:embed templates static
var assets embed.FS

type Server struct {
	cfg       *config.Config
	store     *store.Store
	mailer    mail.Mailer
	log       *slog.Logger
	version   string
	pages     map[string]*pageTemplate
	emailRate *auth.RateLimiter
	ipRate    *auth.RateLimiter
	codeRate  *auth.RateLimiter // essais de code par adresse email
	// Limites des pages publiques de partage, par IP et par partage.
	unlockRate *auth.RateLimiter
	choiceRate *auth.RateLimiter
	// Passkeys : configuration WebAuthn, défis en cours, limite des tentatives par IP.
	webauthn    *webauthn.WebAuthn
	ceremonies  *ceremonies
	passkeyRate *auth.RateLimiter
	// uploadSlots borne les conversions de STL simultanées (chacune lit le fichier en mémoire).
	uploadSlots chan struct{}
	// reference cherche un code-barre dans le catalogue de référence (remplaçable en test).
	reference func(code string) (reference.Entry, bool)
	csp       string
	syncMail  bool // envoi synchrone des emails de notification (tests)
}

func New(cfg *config.Config, st *store.Store, mailer mail.Mailer, log *slog.Logger, version string) (*Server, error) {
	s := &Server{
		cfg:       cfg,
		store:     st,
		mailer:    mailer,
		log:       log,
		version:   version,
		emailRate: auth.NewRateLimiter(cfg.RateLimitEmail.Count, cfg.RateLimitEmail.Window),
		ipRate:    auth.NewRateLimiter(cfg.RateLimitIP.Count, cfg.RateLimitIP.Window),
		codeRate:  auth.NewRateLimiter(maxCodeAttemptsPerEmail, time.Hour),

		unlockRate: auth.NewRateLimiter(5, 15*time.Minute),
		choiceRate: auth.NewRateLimiter(10, 15*time.Minute),

		ceremonies:  &ceremonies{m: map[string]ceremony{}},
		passkeyRate: auth.NewRateLimiter(30, 15*time.Minute),
		uploadSlots: make(chan struct{}, maxConcurrentUploads),
		reference:   reference.Lookup,
	}
	wa, err := webauthn.New(&webauthn.Config{
		RPID:          cfg.WebAuthnRPID,
		RPDisplayName: cfg.WebAuthnRPName,
		RPOrigins:     []string{cfg.Origin()},
	})
	if err != nil {
		return nil, err
	}
	s.webauthn = wa
	pages, err := parseTemplates()
	if err != nil {
		return nil, err
	}
	s.pages = pages
	s.csp = buildCSP(cfg)
	return s, nil
}

func buildCSP(cfg *config.Config) string {
	script, connect := "'self'", "'self'"
	if cfg.PlausibleEnabled() {
		script += " " + cfg.PlausibleURL
		connect += " " + cfg.PlausibleURL
	}
	return strings.Join([]string{
		"default-src 'self'",
		"script-src " + script,
		"connect-src " + connect,
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data:",
		"media-src 'self' blob: mediastream:",
		"object-src 'none'",
		"base-uri 'self'",
		"form-action 'self'",
		"frame-ancestors 'none'",
	}, "; ")
}

// Handler renvoie l'application complète, middlewares compris.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	static, _ := fs.Sub(assets, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", cacheForever(http.FileServerFS(static))))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("GET /readyz", s.readyz)

	mux.HandleFunc("GET /manifest.webmanifest", s.manifest)
	mux.HandleFunc("GET /sw.js", s.serviceWorker)
	mux.HandleFunc("GET /offline", s.offline)
	mux.HandleFunc("GET /{$}", s.home)
	mux.HandleFunc("GET /confidentialite", s.privacy)
	mux.HandleFunc("GET /credits", s.credits)

	mux.HandleFunc("GET /login", s.loginForm)
	mux.HandleFunc("POST /login", s.loginSubmit)
	mux.HandleFunc("GET /login/code", s.codeForm)
	mux.HandleFunc("POST /login/code", s.codeSubmit)
	mux.HandleFunc("GET /auth/verify", s.verifyForm)
	mux.HandleFunc("POST /auth/verify", s.verifySubmit)
	mux.HandleFunc("GET /signup", s.signupForm)
	mux.HandleFunc("POST /signup", s.signupSubmit)
	mux.HandleFunc("POST /logout", s.logout)
	mux.HandleFunc("POST /auth/passkey/begin", s.passkeyLoginBegin)
	mux.HandleFunc("POST /auth/passkey/finish", s.passkeyLoginFinish)

	user := func(h http.HandlerFunc) http.Handler { return s.requireUser(h) }
	mux.Handle("GET /stock", user(s.stockList))
	mux.Handle("GET /spools/new", user(s.spoolNew))
	mux.Handle("POST /spools", user(s.spoolCreate))
	mux.Handle("GET /spools/{id}", user(s.spoolEdit))
	mux.Handle("POST /spools/{id}", user(s.spoolUpdate))
	mux.Handle("POST /spools/{id}/level", user(s.spoolLevel))
	mux.Handle("POST /spools/{id}/delete", user(s.spoolDelete))
	mux.Handle("GET /products", user(s.productList))
	mux.Handle("POST /products/resync", user(s.productResyncAll))
	mux.Handle("GET /products/{id}", user(s.productEdit))
	mux.Handle("POST /products/{id}/resync", user(s.productResync))
	mux.Handle("POST /products/{id}", user(s.productUpdate))
	mux.Handle("POST /products/{id}/delete", user(s.productDelete))
	mux.Handle("GET /scan", user(s.scanPage))
	mux.Handle("GET /scan/lookup", user(s.scanLookup))
	mux.Handle("GET /reception", user(s.receptionPage))
	mux.Handle("POST /reception", user(s.receptionSubmit))
	mux.Handle("GET /api/products/lookup", user(s.productLookup))
	mux.Handle("GET /friends", user(s.friendsPage))
	mux.Handle("POST /friends/invitations", user(s.invitationCreate))
	mux.Handle("POST /friends/invitations/{id}/revoke", user(s.invitationRevoke))
	mux.Handle("POST /friends/{id}/remove", user(s.friendRemove))
	mux.Handle("GET /invite/{token}", user(s.invitePage))
	mux.Handle("POST /invite/{token}", user(s.inviteAccept))
	mux.Handle("GET /transfers", user(s.transfersPage))
	mux.Handle("GET /transfers/new", user(s.transferNew))
	mux.Handle("POST /transfers", user(s.transferCreate))
	mux.Handle("POST /transfers/{id}/decide", user(s.transferDecide))
	mux.Handle("POST /transfers/{id}/cancel", user(s.transferCancel))
	mux.Handle("POST /loans/{id}/return", user(s.loanReturn))
	mux.Handle("POST /loans/{id}/gift", user(s.loanGift))
	mux.Handle("GET /shares", user(s.sharesPage))
	mux.Handle("GET /shares/new", user(s.shareNew))
	mux.Handle("POST /shares", user(s.shareCreate))
	mux.Handle("POST /shares/{id}/revoke", user(s.shareRevoke))
	mux.Handle("GET /shares/{id}/preview", user(s.sharePreview))
	mux.Handle("GET /shares/{id}/file", user(s.shareOwnerFile))
	mux.Handle("GET /shares/{id}/thumb", user(s.shareThumb))
	mux.Handle("GET /share/{token}", publicShare(s.sharePage))
	mux.Handle("POST /share/{token}/unlock", publicShare(s.shareUnlock))
	mux.Handle("GET /share/{token}/files/{fileID}", publicShare(s.shareFile))
	mux.Handle("POST /share/{token}/choice", publicShare(s.shareChoice))
	mux.Handle("GET /notifications", user(s.notificationsPage))
	mux.Handle("POST /account/notifications", user(s.accountNotifications))
	mux.Handle("POST /passkeys/register/begin", user(s.passkeyRegisterBegin))
	mux.Handle("POST /passkeys/register/finish", user(s.passkeyRegisterFinish))
	mux.Handle("POST /passkeys/{id}/delete", user(s.passkeyDelete))
	mux.Handle("GET /account", user(s.accountPage))
	mux.Handle("POST /account/name", user(s.accountName))
	mux.Handle("GET /account/export.json", user(s.exportJSON))
	mux.Handle("GET /account/export.csv", user(s.exportCSV))
	mux.Handle("POST /account/sessions/revoke", user(s.accountSessionsRevoke))
	mux.Handle("POST /account/delete", user(s.accountDelete))

	cop := http.NewCrossOriginProtection()
	cop.AddTrustedOrigin(s.cfg.BaseURL.Scheme + "://" + s.cfg.BaseURL.Host)
	cop.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.log.Warn("requête cross-origin refusée", "path", r.URL.Path, "origin", r.Header.Get("Origin"))
		http.Error(w, "Requête refusée.", http.StatusForbidden)
	}))

	var h http.Handler = mux
	h = s.loadSession(h)
	h = cop.Handler(h)
	h = s.securityHeaders(h)
	h = s.recoverer(h)
	h = s.logRequests(h)
	return h
}

func cacheForever(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("v") != "" {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=3600")
		}
		h.ServeHTTP(w, r)
	})
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.store.Ping(ctx); err != nil {
		s.log.Error("readyz", "err", err)
		http.Error(w, "base indisponible", http.StatusServiceUnavailable)
		return
	}
	w.Write([]byte("ok"))
}

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	if currentUser(r) != nil {
		http.Redirect(w, r, "/stock", http.StatusSeeOther)
		return
	}
	s.render(w, r, http.StatusOK, "home", nil)
}

func (s *Server) credits(w http.ResponseWriter, r *http.Request) {
	version, count := reference.Info()
	s.render(w, r, http.StatusOK, "credits", map[string]any{
		"Title": "Crédits", "RefVersion": version, "RefCount": count, "RefLicense": reference.License,
	})
}

func (s *Server) privacy(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "privacy", nil)
}

// PurgeLoop supprime régulièrement les sessions et défis de connexion expirés.
func (s *Server) PurgeLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		if err := s.store.PurgeExpired(ctx); err != nil && ctx.Err() == nil {
			s.log.Error("purge des sessions expirées", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
