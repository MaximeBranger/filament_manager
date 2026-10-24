package web

import (
	"encoding/json"
	"net/http"
	"strings"
)

type manifestIcon struct {
	Src     string `json:"src"`
	Sizes   string `json:"sizes"`
	Type    string `json:"type"`
	Purpose string `json:"purpose,omitempty"`
}

type manifestShortcut struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// manifest décrit l'application installable (PWA).
func (s *Server) manifest(w http.ResponseWriter, r *http.Request) {
	m := map[string]any{
		"name":             "FilamentManager",
		"short_name":       "Filaments",
		"description":      "Votre stock de filament 3D : bobines, couleurs, scan des codes-barres, prêts entre amis.",
		"lang":             "fr",
		"start_url":        "/stock",
		"scope":            "/",
		"display":          "standalone",
		"background_color": "#f6f5f3",
		"theme_color":      "#e8590c",
		"icons": []manifestIcon{
			{"/static/icon-192.png", "192x192", "image/png", "any"},
			{"/static/icon-512.png", "512x512", "image/png", "any"},
			{"/static/icon-maskable-512.png", "512x512", "image/png", "maskable"},
		},
		"shortcuts": []manifestShortcut{
			{"Scanner une boîte", "/scan"},
			{"Réception", "/reception"},
			{"Prêts & dons", "/transfers"},
		},
	}
	w.Header().Set("Content-Type", "application/manifest+json")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	json.NewEncoder(w).Encode(m)
}

// serviceWorker sert sw.js depuis la racine (portée « / ») avec la version de l'application.
func (s *Server) serviceWorker(w http.ResponseWriter, r *http.Request) {
	src, err := assets.ReadFile("static/sw.js")
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write([]byte(strings.ReplaceAll(string(src), "__VERSION__", s.version)))
}

// offline est la page affichée par le service worker sans réseau. Elle ne contient aucune
// donnée personnelle : elle est mise en cache.
func (s *Server) offline(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "offline", map[string]any{"Title": "Hors ligne", "Bare": true})
}
