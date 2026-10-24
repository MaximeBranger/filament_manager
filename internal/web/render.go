package web

import (
	"bytes"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/MaximeBranger/filament_manager/internal/catalog"
	"github.com/MaximeBranger/filament_manager/internal/stock"
	"github.com/MaximeBranger/filament_manager/internal/store"
)

type pageTemplate struct{ t *template.Template }

var funcs = template.FuncMap{
	"levels":     func() []catalog.Option { return catalog.Levels },
	"materials":  func() []catalog.Option { return catalog.Materials },
	"finishes":   func() []catalog.Option { return catalog.Finishes },
	"families":   func() []catalog.Family { return catalog.Families },
	"diameters":  func() []float64 { return catalog.Diameters },
	"weights":    func() []int { return catalog.Weights },
	"tolerances": func() any { return stock.Tolerances },
	"sorts":      func() []catalog.Option { return stock.Sorts },
	"levelLabel": catalog.LevelLabel,
	"familyLabel": func(code string) string {
		f, _ := catalog.FamilyByCode(code)
		return f.Label
	},
	"familyHex": func(code string) string {
		f, _ := catalog.FamilyByCode(code)
		return f.Hex
	},
	"materialText": func(p store.Product) string {
		if p.Material == catalog.Other && p.MaterialOther != "" {
			return p.MaterialOther
		}
		return catalog.MaterialLabel(p.Material)
	},
	"finishText": func(p store.Product) string {
		if p.Finish == catalog.Other && p.FinishOther != "" {
			return p.FinishOther
		}
		return catalog.FinishLabel(p.Finish)
	},
	"hex":      stock.EffectiveHex,
	"diameter": formatDiameter,
	"weight":   formatWeight,
	"price": func(p *int64) string {
		if p == nil {
			return ""
		}
		return stock.FormatPrice(*p) + " €"
	},
	"dateFR": func(d string) string {
		t, err := time.Parse("2006-01-02", d)
		if err != nil {
			return d
		}
		return t.Format("02/01/2006")
	},
	"val":  func(v url.Values, key string) string { return v.Get(key) },
	"ferr": func(e stock.Errors, key string) string { return e[key] },
	"dict": func(kv ...any) map[string]any {
		m := map[string]any{}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i].(string)] = kv[i+1]
		}
		return m
	},
	"has":  func(list []string, v string) bool { return slices.Contains(list, v) },
	"fold": catalog.Fold,
	"ftoa": func(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) },
	"itoa": strconv.Itoa,
	"plural": func(n int, one, many string) string {
		if n > 1 {
			return many
		}
		return one
	},
}

func formatDiameter(d float64) string {
	return strings.Replace(strconv.FormatFloat(d, 'f', 2, 64), ".", ",", 1) + " mm"
}

func formatWeight(g int) string {
	if g >= 1000 && g%100 == 0 {
		return strings.Replace(strconv.FormatFloat(float64(g)/1000, 'f', -1, 64), ".", ",", 1) + " kg"
	}
	return strconv.Itoa(g) + " g"
}

// parseTemplates associe à chaque page le gabarit commun (layout + partiels).
func parseTemplates() (map[string]*pageTemplate, error) {
	shared := []string{"templates/layout.html", "templates/partials.html"}
	files, err := fs.Glob(assets, "templates/*.html")
	if err != nil {
		return nil, err
	}
	pages := map[string]*pageTemplate{}
	for _, f := range files {
		if slices.Contains(shared, f) {
			continue
		}
		name := strings.TrimSuffix(path.Base(f), ".html")
		t, err := template.New(name).Funcs(funcs).ParseFS(assets, append(shared, f)...)
		if err != nil {
			return nil, fmt.Errorf("gabarit %s : %w", name, err)
		}
		pages[name] = &pageTemplate{t}
	}
	return pages, nil
}

// render affiche une page complète. data peut être nil.
func (s *Server) render(w http.ResponseWriter, r *http.Request, status int, page string, data map[string]any) {
	s.renderBlock(w, r, status, page, "layout", data)
}

// renderBlock exécute un bloc précis d'une page (réponses partielles htmx).
func (s *Server) renderBlock(w http.ResponseWriter, r *http.Request, status int, page, block string, data map[string]any) {
	pt, ok := s.pages[page]
	if !ok {
		s.serverError(w, r, fmt.Errorf("page inconnue %q", page))
		return
	}
	if data == nil {
		data = map[string]any{}
	}
	data["User"] = currentUser(r)
	data["Version"] = s.version
	data["Path"] = r.URL.Path
	if block == "layout" {
		data["Flash"] = s.takeFlash(w, r)
	}
	if s.cfg.PlausibleEnabled() {
		data["PlausibleURL"] = s.cfg.PlausibleURL
		data["PlausibleDomain"] = s.cfg.PlausibleDomain
	}
	var buf bytes.Buffer
	if err := pt.t.ExecuteTemplate(&buf, block, data); err != nil {
		s.serverError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	buf.WriteTo(w)
}

func (s *Server) serverError(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Error("erreur interne", "path", r.URL.Path, "err", err)
	http.Error(w, "Erreur interne.", http.StatusInternalServerError)
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusNotFound, "error", map[string]any{
		"Title":   "Introuvable",
		"Message": "Cette page n'existe pas, ou elle ne vous appartient pas.",
	})
}

func (s *Server) errorPage(w http.ResponseWriter, r *http.Request, status int, title, msg string) {
	s.render(w, r, status, "error", map[string]any{"Title": title, "Message": msg})
}

func isHTMX(r *http.Request) bool { return r.Header.Get("HX-Request") == "true" }
