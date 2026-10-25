package web

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/MaximeBranger/filament_manager/internal/modelimport"
	"github.com/MaximeBranger/filament_manager/internal/store"
)

// modelSource récupère un modèle depuis un lien (remplaçable en test).
type modelSource interface {
	Resolve(ctx context.Context, raw string) (*modelimport.Model, error)
	Download(ctx context.Context, m *modelimport.Model, fileID string) (string, []byte, error)
}

const (
	importTimeout = 3 * time.Minute
	maxImportURL  = 2048
)

// resolveModel applique la limite d'imports puis interroge le site du modèle. Le message renvoyé
// s'affiche tel quel à l'utilisateur.
func (s *Server) resolveModel(r *http.Request, raw string) (*modelimport.Model, string) {
	if len(raw) > maxImportURL {
		return nil, "Lien trop long."
	}
	if !s.importRate.Allow(strconv.FormatInt(currentUser(r).ID, 10)) {
		return nil, "Trop d'imports en peu de temps : réessayez dans quelques minutes."
	}
	ctx, cancel := context.WithTimeout(r.Context(), importTimeout)
	defer cancel()
	m, err := s.importer.Resolve(ctx, raw)
	if err != nil {
		return nil, importMessage(err)
	}
	return m, ""
}

func importMessage(err error) string {
	msg := err.Error()
	if errors.Is(err, modelimport.ErrUnavailable) || errors.Is(err, modelimport.ErrTooLarge) {
		msg = strings.SplitN(msg, " (", 2)[0] // pas de détail technique (code HTTP)
	}
	return strings.ToUpper(msg[:1]) + msg[1:] + "."
}

// shareImportForm affiche les fichiers du modèle désigné par ?url=. C'est aussi le point d'entrée
// d'une future extension de navigateur (« Ajouter dans FilamentManager »).
func (s *Server) shareImportForm(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimSpace(r.URL.Query().Get("url"))
	if raw == "" {
		http.Redirect(w, r, "/shares/new", http.StatusSeeOther)
		return
	}
	m, msg := s.resolveModel(r, raw)
	if m == nil {
		s.renderImportError(w, r, raw, msg)
		return
	}
	fileID := ""
	for _, f := range m.Files {
		if f.Size <= s.cfg.ShareMaxFileSize {
			fileID = f.ID // premier fichier téléchargeable, présélectionné
			break
		}
	}
	s.renderImportForm(w, r, http.StatusOK, raw, m, "", fileID, "")
}

func (s *Server) renderImportError(w http.ResponseWriter, r *http.Request, raw, msg string) {
	s.render(w, r, http.StatusUnprocessableEntity, "share_new", map[string]any{
		"Title": "Nouveau partage", "ImportURL": raw, "ImportError": msg,
		"MaxSizeMB": s.cfg.ShareMaxFileSize >> 20, "MaxActive": s.cfg.ShareMaxActive,
	})
}

func (s *Server) renderImportForm(w http.ResponseWriter, r *http.Request, status int, raw string, m *modelimport.Model, title, fileID, errMsg string) {
	if title == "" {
		title = m.Title
	}
	if len([]rune(title)) > maxShareTitle {
		title = string([]rune(title)[:maxShareTitle])
	}
	s.render(w, r, status, "share_import", map[string]any{
		"Title": "Importer un modèle", "URL": raw, "Model": m, "ShareTitle": title, "FileID": fileID, "Error": errMsg,
		"MaxSize": s.cfg.ShareMaxFileSize, "MaxSizeMB": s.cfg.ShareMaxFileSize >> 20,
	})
}

// shareImport télécharge le fichier choisi puis crée le partage comme pour un envoi direct.
// Le lien est résolu de nouveau : rien n'est conservé entre l'affichage et la validation.
func (s *Server) shareImport(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	raw := strings.TrimSpace(r.FormValue("url"))
	fileID, title, password := r.FormValue("file"), strings.TrimSpace(r.FormValue("title")), r.FormValue("password")

	select {
	case s.uploadSlots <- struct{}{}:
		defer func() { <-s.uploadSlots }()
	default:
		s.renderImportError(w, r, raw, "Le serveur traite déjà d'autres modèles : réessayez dans un instant.")
		return
	}
	if active, err := s.store.ListShares(r.Context(), currentUser(r).ID); err != nil {
		s.serverError(w, r, err)
		return
	} else if len(active) >= s.cfg.ShareMaxActive {
		s.renderImportError(w, r, raw, "Vous avez déjà "+strconv.Itoa(len(active))+
			" partages actifs : révoquez-en un avant d'en créer un nouveau.")
		return
	}

	m, msg := s.resolveModel(r, raw)
	if m == nil {
		s.renderImportError(w, r, raw, msg)
		return
	}
	fail := func(status int, msg string) { s.renderImportForm(w, r, status, raw, m, title, fileID, msg) }
	ctx, cancel := context.WithTimeout(r.Context(), importTimeout)
	defer cancel()
	filename, data, err := s.importer.Download(ctx, m, fileID)
	if err != nil {
		if errors.Is(err, modelimport.ErrTooLarge) {
			fail(http.StatusRequestEntityTooLarge, "Fichier trop volumineux (maximum "+strconv.FormatInt(s.cfg.ShareMaxFileSize>>20, 10)+" Mo).")
			return
		}
		fail(http.StatusBadGateway, importMessage(err))
		return
	}
	s.publishShare(w, r, shareInput{
		title: title, password: password, filename: filename, data: data,
		source: store.ShareSource{Site: m.Source, URL: m.URL, Title: m.Title, Author: m.Author, License: m.License},
	}, fail)
}
