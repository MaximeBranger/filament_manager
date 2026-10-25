package web

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/MaximeBranger/filament_manager/internal/auth"
	"github.com/MaximeBranger/filament_manager/internal/catalog"
	"github.com/MaximeBranger/filament_manager/internal/share"
	"github.com/MaximeBranger/filament_manager/internal/store"
)

const (
	shareTTL          = 7 * 24 * time.Hour
	shareCookie       = "share_access"
	maxShareTitle     = 100
	maxRecipientName  = 50
	maxChoiceMessage  = 500
	maxPasswordLength = 200
	sharePurgeEvery   = 15 * time.Minute

	maxConcurrentUploads = 2
)

// Créateur -------------------------------------------------------------------------------

func (s *Server) sharesPage(w http.ResponseWriter, r *http.Request) {
	s.renderShares(w, r, 0, false)
}

// renderShares affiche la liste ; newID désigne le partage qui vient d'être créé (mis en avant).
func (s *Server) renderShares(w http.ResponseWriter, r *http.Request, newID int64, withPassword bool) {
	shares, err := s.store.ListShares(r.Context(), currentUser(r).ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	links := make(map[int64]string, len(shares))
	for _, sh := range shares {
		if link, ok := s.shareLink(sh); ok {
			links[sh.ID] = link
		}
	}
	w.Header().Set("Cache-Control", "private, no-store") // la page contient les liens
	s.render(w, r, http.StatusOK, "shares", map[string]any{
		"Title": "Partages", "Shares": shares, "Links": links, "NewID": newID,
		"NewWithPassword": withPassword, "MaxActive": s.cfg.ShareMaxActive,
	})
}

// shareLink recalcule le lien public d'un partage. Impossible pour les partages antérieurs à
// l'aléa, ou si SESSION_SECRET a changé depuis la création (le jeton dérivé ne correspond plus).
func (s *Server) shareLink(sh store.Share) (string, bool) {
	if sh.LinkNonce == "" {
		return "", false
	}
	token := auth.DeriveToken(s.cfg.SessionSecret, sh.LinkNonce)
	if !auth.Equal(auth.HashToken(token), sh.TokenHash) {
		return "", false
	}
	return s.cfg.BaseURL.String() + "/share/" + token, true
}

// shareThumb sert la miniature d'un partage (créateur uniquement). Les partages créés avant
// l'ajout des miniatures en reçoivent une à la première demande.
func (s *Server) shareThumb(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	sh, err := s.store.ShareByID(r.Context(), currentUser(r).ID, id)
	if err != nil {
		s.notFound(w, r)
		return
	}
	thumbKey := share.ThumbKey(sh.File.StorageKey)
	f, err := share.Open(s.cfg.ShareDir, thumbKey)
	if err != nil {
		data, err := s.renderStoredThumbnail(sh.File.StorageKey)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "private, max-age=3600")
		w.Write(data)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	io.Copy(w, f)
}

func (s *Server) renderStoredThumbnail(meshKey string) ([]byte, error) {
	f, err := share.Open(s.cfg.ShareDir, meshKey)
	if err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(f)
	f.Close()
	if err != nil {
		return nil, err
	}
	mesh, err := share.DecodeFMM(raw)
	if err != nil {
		return nil, err
	}
	data, err := share.RenderThumbnail(mesh)
	if err != nil {
		return nil, err
	}
	if err := share.Write(s.cfg.ShareDir, share.ThumbKey(meshKey), data); err != nil {
		s.log.Error("écriture de la miniature", "err", err)
	}
	return data, nil
}

func (s *Server) shareNew(w http.ResponseWriter, r *http.Request) {
	s.renderShareForm(w, r, http.StatusOK, "", "")
}

func (s *Server) renderShareForm(w http.ResponseWriter, r *http.Request, status int, title, errMsg string) {
	s.render(w, r, status, "share_new", map[string]any{
		"Title": "Nouveau partage", "ShareTitle": title, "Error": errMsg,
		"MaxSizeMB": s.cfg.ShareMaxFileSize >> 20, "MaxActive": s.cfg.ShareMaxActive,
	})
}

// shareCreate lit le formulaire multipart en mémoire : le STL n'est jamais écrit tel quel.
func (s *Server) shareCreate(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	select {
	case s.uploadSlots <- struct{}{}:
		defer func() { <-s.uploadSlots }()
	default:
		s.renderShareForm(w, r, http.StatusServiceUnavailable, "", "Le serveur traite déjà d'autres modèles : réessayez dans un instant.")
		return
	}
	http.NewResponseController(w).SetReadDeadline(time.Now().Add(10 * time.Minute))
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.ShareMaxFileSize+1<<20)

	if active, err := s.store.ListShares(r.Context(), user.ID); err != nil {
		s.serverError(w, r, err)
		return
	} else if len(active) >= s.cfg.ShareMaxActive {
		s.renderShareForm(w, r, http.StatusConflict, "", "Vous avez déjà "+strconv.Itoa(len(active))+
			" partages actifs : révoquez-en un avant d'en créer un nouveau.")
		return
	}

	mr, err := r.MultipartReader()
	if err != nil {
		s.renderShareForm(w, r, http.StatusBadRequest, "", "Formulaire invalide.")
		return
	}
	var title, password, filename string
	var data []byte
	tooBig := false
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				tooBig = true
				break
			}
			s.renderShareForm(w, r, http.StatusBadRequest, "", "Envoi interrompu, réessayez.")
			return
		}
		switch part.FormName() {
		case "title":
			b, _ := io.ReadAll(io.LimitReader(part, 4096))
			title = strings.TrimSpace(string(b))
		case "password":
			b, _ := io.ReadAll(io.LimitReader(part, 4096))
			password = string(b)
		case "file":
			filename = filepath.Base(part.FileName())
			data, err = io.ReadAll(io.LimitReader(part, s.cfg.ShareMaxFileSize+1))
			if err != nil || int64(len(data)) > s.cfg.ShareMaxFileSize {
				tooBig = true
			}
		}
		part.Close()
		if tooBig {
			break
		}
	}
	if tooBig {
		s.renderShareForm(w, r, http.StatusRequestEntityTooLarge, title,
			"Fichier trop volumineux (maximum "+strconv.FormatInt(s.cfg.ShareMaxFileSize>>20, 10)+" Mo).")
		return
	}
	if len(data) == 0 {
		s.renderShareForm(w, r, http.StatusUnprocessableEntity, title, "Choisissez un fichier STL.")
		return
	}
	if title == "" {
		title = strings.TrimSuffix(filename, filepath.Ext(filename))
	}
	title = strings.Join(strings.Fields(title), " ")
	if title == "" || len([]rune(title)) > maxShareTitle {
		s.renderShareForm(w, r, http.StatusUnprocessableEntity, title, "Le titre doit faire entre 1 et 100 caractères.")
		return
	}
	if password != "" && (len([]rune(password)) < share.MinPasswordLength || len(password) > maxPasswordLength) {
		s.renderShareForm(w, r, http.StatusUnprocessableEntity, title, "Le mot de passe doit faire au moins 6 caractères.")
		return
	}

	mesh, err := share.ParseSTL(data)
	if err != nil {
		s.renderShareForm(w, r, http.StatusUnprocessableEntity, title, "Fichier refusé : "+err.Error()+".")
		return
	}
	data = nil // le STL d'origine n'est plus utile
	encoded := share.EncodeFMM(mesh)

	var passwordHash string
	if password != "" {
		if passwordHash, err = share.HashPassword(password); err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	key := share.NewStorageKey()
	if err := share.Write(s.cfg.ShareDir, key, encoded); err != nil {
		s.serverError(w, r, err)
		return
	}
	if thumb, err := share.RenderThumbnail(mesh); err != nil {
		s.log.Error("miniature du partage", "err", err) // non bloquant : régénérée à la demande
	} else if err := share.Write(s.cfg.ShareDir, share.ThumbKey(key), thumb); err != nil {
		s.log.Error("écriture de la miniature", "err", err)
	}
	nonce := auth.NewToken()
	token := auth.DeriveToken(s.cfg.SessionSecret, nonce)
	id, err := s.store.CreateShare(r.Context(), user.ID, auth.HashToken(token), nonce, title, passwordHash, store.ShareFile{
		OriginalName: filename, StorageKey: key, Triangles: mesh.Triangles(),
		SizeX: mesh.Size[0], SizeY: mesh.Size[1], SizeZ: mesh.Size[2], Bytes: int64(len(encoded)),
	}, s.cfg.ShareMaxActive, shareTTL)
	if err != nil {
		s.removeShareFiles([]string{key})
		if errors.Is(err, store.ErrTooManyShares) {
			s.renderShareForm(w, r, http.StatusConflict, title, "Nombre maximal de partages actifs atteint.")
			return
		}
		s.serverError(w, r, err)
		return
	}
	s.log.Info("partage créé", "user_id", user.ID, "triangles", mesh.Triangles())
	s.renderShares(w, r, id, passwordHash != "")
}

func (s *Server) shareRevoke(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	keys, err := s.store.RevokeShare(r.Context(), currentUser(r).ID, id)
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.removeShareFiles(keys)
	s.setFlash(w, "Partage révoqué : le lien ne fonctionne plus et le modèle est supprimé.")
	http.Redirect(w, r, "/shares", http.StatusSeeOther)
}

func (s *Server) sharePreview(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	sh, err := s.store.ShareByID(r.Context(), currentUser(r).ID, id)
	if err != nil {
		s.notFound(w, r)
		return
	}
	s.renderViewer(w, r, sh, true, "/shares/"+strconv.FormatInt(id, 10)+"/file", "")
}

func (s *Server) shareOwnerFile(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	sh, err := s.store.ShareByID(r.Context(), currentUser(r).ID, id)
	if err != nil {
		s.notFound(w, r)
		return
	}
	s.serveMesh(w, r, sh)
}

// Destinataire (public) ------------------------------------------------------------------

// publicShare ajoute les en-têtes propres aux pages de partage.
func publicShare(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Robots-Tag", "noindex, nofollow")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		next(w, r)
	})
}

func (s *Server) shareUnavailable(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusNotFound, "error", map[string]any{
		"Title": "Partage indisponible", "Message": "Ce partage n'est plus disponible.",
	})
}

// loadShare retrouve le partage du lien ; renvoie nil (et répond) s'il n'existe plus.
func (s *Server) loadShare(w http.ResponseWriter, r *http.Request) *store.Share {
	sh, err := s.store.ShareByToken(r.Context(), auth.HashToken(r.PathValue("token")))
	if errors.Is(err, store.ErrNotFound) {
		s.shareUnavailable(w, r)
		return nil
	}
	if err != nil {
		s.serverError(w, r, err)
		return nil
	}
	return sh
}

func accessValue(sh *store.Share) string {
	return "share:" + strconv.FormatInt(sh.ID, 10) + ":" + strconv.FormatInt(sh.PasswordVersion, 10)
}

// hasAccess vérifie le cookie d'accès d'un partage protégé par mot de passe.
func (s *Server) hasAccess(r *http.Request, sh *store.Share) bool {
	if !sh.HasPassword() {
		return true
	}
	c, err := r.Cookie(shareCookie)
	if err != nil {
		return false
	}
	value, ok := auth.Verify(s.cfg.SessionSecret, c.Value)
	return ok && value == accessValue(sh)
}

func (s *Server) sharePage(w http.ResponseWriter, r *http.Request) {
	sh := s.loadShare(w, r)
	if sh == nil {
		return
	}
	if !s.hasAccess(r, sh) {
		s.renderSharePassword(w, r, http.StatusOK, sh, "")
		return
	}
	token := r.PathValue("token")
	s.renderViewer(w, r, sh, false, "/share/"+token+"/files/"+strconv.FormatInt(sh.File.ID, 10), "/share/"+token+"/choice")
}

func (s *Server) renderSharePassword(w http.ResponseWriter, r *http.Request, status int, sh *store.Share, errMsg string) {
	s.render(w, r, status, "share_password", map[string]any{
		"Title": "Partage protégé", "Token": r.PathValue("token"), "Locked": sh.Locked(), "Error": errMsg,
	})
}

func (s *Server) shareUnlock(w http.ResponseWriter, r *http.Request) {
	sh := s.loadShare(w, r)
	if sh == nil {
		return
	}
	token := r.PathValue("token")
	if !sh.HasPassword() {
		http.Redirect(w, r, "/share/"+token, http.StatusSeeOther)
		return
	}
	if sh.Locked() {
		s.renderSharePassword(w, r, http.StatusLocked, sh, "")
		return
	}
	key := auth.ClientIP(r, s.cfg.TrustedProxies) + "|" + strconv.FormatInt(sh.ID, 10)
	if !s.unlockRate.Allow(key) {
		s.renderSharePassword(w, r, http.StatusTooManyRequests, sh, "Trop d'essais. Réessayez dans quelques minutes.")
		return
	}
	if !share.VerifyPassword(r.FormValue("password"), sh.PasswordHash) {
		locked, notifs, err := s.store.RecordShareFailure(r.Context(), sh.ID)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		s.dispatch(notifs)
		sh.LockedAt = time.Time{}
		if locked {
			sh.LockedAt = time.Now()
		}
		s.renderSharePassword(w, r, http.StatusUnauthorized, sh, "Mot de passe incorrect.")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     shareCookie,
		Value:    auth.Sign(s.cfg.SessionSecret, accessValue(sh)),
		Path:     "/share/" + token,
		Expires:  sh.ExpiresAt,
		HttpOnly: true,
		Secure:   s.cfg.SecureCookies(),
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, "/share/"+token, http.StatusSeeOther)
}

func (s *Server) shareFile(w http.ResponseWriter, r *http.Request) {
	sh := s.loadShare(w, r)
	if sh == nil {
		return
	}
	if !s.hasAccess(r, sh) || r.PathValue("fileID") != strconv.FormatInt(sh.File.ID, 10) {
		http.Error(w, "Accès refusé.", http.StatusForbidden)
		return
	}
	s.serveMesh(w, r, sh)
}

func (s *Server) serveMesh(w http.ResponseWriter, r *http.Request, sh *store.Share) {
	f, err := share.Open(s.cfg.ShareDir, sh.File.StorageKey)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	defer f.Close()
	http.NewResponseController(w).SetWriteDeadline(time.Now().Add(10 * time.Minute))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(sh.File.Bytes, 10))
	w.Header().Set("Cache-Control", "private, no-store")
	io.Copy(w, f)
}

func (s *Server) shareChoice(w http.ResponseWriter, r *http.Request) {
	sh := s.loadShare(w, r)
	if sh == nil {
		return
	}
	token := r.PathValue("token")
	if !s.hasAccess(r, sh) {
		http.Redirect(w, r, "/share/"+token, http.StatusSeeOther)
		return
	}
	key := auth.ClientIP(r, s.cfg.TrustedProxies) + "|" + strconv.FormatInt(sh.ID, 10)
	if !s.choiceRate.Allow(key) {
		s.setFlash(w, "Trop d'envois rapprochés. Réessayez dans quelques minutes.")
		http.Redirect(w, r, "/share/"+token, http.StatusSeeOther)
		return
	}

	productID, _ := strconv.ParseInt(r.FormValue("product_id"), 10, 64)
	colors, err := s.store.ShareColors(r.Context(), sh.UserID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	var chosen *store.Product
	for i := range colors {
		if colors[i].ID == productID {
			chosen = &colors[i]
		}
	}
	name := strings.Join(strings.Fields(r.FormValue("recipient_name")), " ")
	message := strings.TrimSpace(r.FormValue("message"))
	switch {
	case chosen == nil:
		s.setFlash(w, "Cette couleur n'est plus disponible : choisissez-en une autre.")
	case len([]rune(name)) > maxRecipientName || len([]rune(message)) > maxChoiceMessage:
		s.setFlash(w, "Prénom (50 caractères) ou message (500 caractères) trop long.")
	default:
		notifs, err := s.store.SaveShareChoice(r.Context(), sh.ID, store.ShareChoice{
			ProductID: chosen.ID, Label: chosen.ColorLabel(), ColorHex: chosen.Hex(),
			RecipientName: name, Message: message,
		})
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		s.dispatch(notifs)
		s.setFlash(w, "Merci ! Votre choix a été transmis à "+sh.OwnerName+". Vous pouvez encore le modifier.")
	}
	http.Redirect(w, r, "/share/"+token, http.StatusSeeOther)
}

// Rendu de la visionneuse ------------------------------------------------------------------

type swatch struct {
	ID          int64
	ColorName   string
	Brand       string
	Material    string
	Finish      string
	FinishCode  string
	Family      string
	Hex         string
	Hex2        string
	Unrealistic bool
}

func toSwatch(p store.Product) swatch {
	material := catalog.MaterialLabel(p.Material)
	if p.Material == catalog.Other && p.MaterialOther != "" {
		material = p.MaterialOther
	}
	finish := catalog.FinishLabel(p.Finish)
	if p.Finish == catalog.Other && p.FinishOther != "" {
		finish = p.FinishOther
	}
	unrealistic := p.ColorFamily == "multicolor"
	switch p.Finish {
	case "marble", "wood", "carbon", "multicolor", catalog.Other:
		unrealistic = true
	}
	return swatch{p.ID, p.ColorName, p.Brand, material, finish, p.Finish, p.ColorFamily, p.Hex(), p.ColorHex2, unrealistic}
}

func (s *Server) renderViewer(w http.ResponseWriter, r *http.Request, sh *store.Share, preview bool, meshURL, choiceAction string) {
	colors, err := s.store.ShareColors(r.Context(), sh.UserID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	swatches := make([]swatch, 0, len(colors))
	selected := int64(0)
	for _, p := range colors {
		swatches = append(swatches, toSwatch(p))
		if sh.Choice != nil && sh.Choice.ProductID == p.ID {
			selected = p.ID
		}
	}
	if selected == 0 && len(swatches) > 0 {
		selected = swatches[0].ID
	}
	s.render(w, r, http.StatusOK, "share_view", map[string]any{
		"Title": sh.Title, "Share": sh, "Swatches": swatches, "Selected": selected, "Preview": preview,
		"MeshURL": meshURL, "ChoiceAction": choiceAction,
	})
}

// Purge ----------------------------------------------------------------------------------

// removeShareFiles efface les maillages et leurs miniatures.
func (s *Server) removeShareFiles(keys []string) {
	for _, k := range keys {
		for _, name := range []string{k, share.ThumbKey(k)} {
			if err := share.Remove(s.cfg.ShareDir, name); err != nil {
				s.log.Error("suppression d'un modèle partagé", "err", err)
			}
		}
	}
}

// PurgeShares supprime les partages expirés : fichiers d'abord, lignes ensuite.
func (s *Server) PurgeShares(ctx context.Context) error {
	ids, keys, err := s.store.ExpiredShares(ctx)
	if err != nil || len(ids) == 0 {
		return err
	}
	s.removeShareFiles(keys)
	if err := s.store.DeleteShares(ctx, ids); err != nil {
		return err
	}
	s.log.Info("partages expirés supprimés", "count", len(ids))
	return nil
}

// CleanShareOrphans supprime les fichiers sans partage en base (après un arrêt brutal).
func (s *Server) CleanShareOrphans(ctx context.Context) error {
	known, err := s.store.AllShareStorageKeys(ctx)
	if err != nil {
		return err
	}
	for k := range known {
		known[share.ThumbKey(k)] = true
	}
	n, err := share.CleanOrphans(s.cfg.ShareDir, known)
	if n > 0 {
		s.log.Info("fichiers de partage orphelins supprimés", "count", n)
	}
	return err
}

// SharePurgeLoop supprime les partages expirés toutes les 15 min. Les orphelins sont nettoyés
// au démarrage par CleanShareOrphans, avant l'ouverture du port (aucun envoi en cours).
func (s *Server) SharePurgeLoop(ctx context.Context) {
	ticker := time.NewTicker(sharePurgeEvery)
	defer ticker.Stop()
	for {
		if err := s.PurgeShares(ctx); err != nil && ctx.Err() == nil {
			s.log.Error("purge des partages expirés", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
