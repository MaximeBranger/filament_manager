package web

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/MaximeBranger/filament_manager/internal/catalog"
	"github.com/MaximeBranger/filament_manager/internal/stock"
	"github.com/MaximeBranger/filament_manager/internal/store"
)

func (s *Server) accountPage(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "account", map[string]any{"Title": "Mon compte"})
}

func (s *Server) accountName(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	name := strings.Join(strings.Fields(r.FormValue("display_name")), " ")
	if name == "" || len([]rune(name)) > maxNameLength {
		s.render(w, r, http.StatusUnprocessableEntity, "account", map[string]any{
			"Title": "Mon compte", "NameError": "Le nom doit faire entre 1 et 50 caractères.",
		})
		return
	}
	if err := s.store.UpdateDisplayName(r.Context(), user.ID, name); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.setFlash(w, "Nom mis à jour.")
	http.Redirect(w, r, "/account", http.StatusSeeOther)
}

type exportProduct struct {
	ID             int64   `json:"id"`
	Brand          string  `json:"brand"`
	Material       string  `json:"material"`
	MaterialOther  string  `json:"material_other,omitempty"`
	ColorName      string  `json:"color_name"`
	ColorFamily    string  `json:"color_family"`
	ColorHex       string  `json:"color_hex,omitempty"`
	Finish         string  `json:"finish"`
	FinishOther    string  `json:"finish_other,omitempty"`
	DiameterMM     float64 `json:"diameter_mm"`
	NominalWeightG int     `json:"nominal_weight_g"`
	EAN            string  `json:"ean,omitempty"`
}

type exportSpool struct {
	ID           int64  `json:"id"`
	ProductID    int64  `json:"product_id"`
	Level        string `json:"level"`
	PurchaseDate string `json:"purchase_date,omitempty"`
	PriceCents   *int64 `json:"price_cents,omitempty"`
	Supplier     string `json:"supplier,omitempty"`
	Notes        string `json:"notes,omitempty"`
	Hidden       bool   `json:"hidden_from_friends"`
}

func exportFilename(ext string) string {
	return "filamentmanager-" + time.Now().Format("2006-01-02") + "." + ext
}

func (s *Server) exportJSON(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	products, err := s.store.ListProducts(r.Context(), user.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	spools, err := s.store.ListSpools(r.Context(), user.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	out := map[string]any{
		"exported_at": time.Now().UTC().Format(time.RFC3339),
		"account": map[string]any{
			"email": user.Email, "display_name": user.DisplayName, "created_at": user.CreatedAt.Format(time.RFC3339),
		},
		"products": []exportProduct{},
		"spools":   []exportSpool{},
	}
	ps := make([]exportProduct, 0, len(products))
	for _, p := range products {
		ps = append(ps, exportProduct{p.ID, p.Brand, p.Material, p.MaterialOther, p.ColorName, p.ColorFamily,
			p.ColorHex, p.Finish, p.FinishOther, p.DiameterMM, p.NominalWeightG, p.EAN})
	}
	ss := make([]exportSpool, 0, len(spools))
	for _, sp := range spools {
		ss = append(ss, exportSpool{sp.ID, sp.ProductID, sp.Level, sp.PurchaseDate, sp.PriceCents, sp.Supplier, sp.Notes, sp.Hidden})
	}
	out["products"], out["spools"] = ps, ss

	// Partages actifs : métadonnées et choix reçus, jamais les modèles 3D.
	shares, err := s.store.ListShares(r.Context(), user.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	shs := make([]map[string]any, 0, len(shares))
	for _, sh := range shares {
		e := map[string]any{
			"title": sh.Title, "file_name": sh.File.OriginalName, "triangles": sh.File.Triangles,
			"size_mm":    []float64{sh.File.SizeX, sh.File.SizeY, sh.File.SizeZ},
			"created_at": sh.CreatedAt.Format(time.RFC3339), "expires_at": sh.ExpiresAt.Format(time.RFC3339),
			"has_password": sh.HasPassword(),
		}
		if c := sh.Choice; c != nil {
			e["choice"] = map[string]any{"color": c.Label, "hex": c.ColorHex, "recipient_name": c.RecipientName,
				"message": c.Message, "updated_at": c.UpdatedAt.Format(time.RFC3339)}
		}
		shs = append(shs, e)
	}
	out["shares"] = shs

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+exportFilename("json")+`"`)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(out)
}

func (s *Server) exportCSV(w http.ResponseWriter, r *http.Request) {
	spools, err := s.store.ListSpools(r.Context(), currentUser(r).ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+exportFilename("csv")+`"`)
	w.Write([]byte("\xef\xbb\xbf")) // BOM : ouverture correcte des accents dans Excel
	cw := csv.NewWriter(w)
	cw.Comma = ';'
	cw.Write([]string{"id", "marque", "matériau", "couleur", "famille", "hexa", "finition", "diamètre (mm)",
		"poids (g)", "code-barre", "niveau", "date d'achat", "prix (€)", "fournisseur", "notes", "masquée aux amis"})
	for _, sp := range spools {
		p := sp.Product
		material := catalog.MaterialLabel(p.Material)
		if p.MaterialOther != "" {
			material = p.MaterialOther
		}
		finish := catalog.FinishLabel(p.Finish)
		if p.FinishOther != "" {
			finish = p.FinishOther
		}
		family, _ := catalog.FamilyByCode(p.ColorFamily)
		price := ""
		if sp.PriceCents != nil {
			price = stock.FormatPrice(*sp.PriceCents)
		}
		hidden := "non"
		if sp.Hidden {
			hidden = "oui"
		}
		cw.Write([]string{strconv.FormatInt(sp.ID, 10), p.Brand, material, p.ColorName, family.Label,
			stock.EffectiveHex(p), finish, strings.Replace(strconv.FormatFloat(p.DiameterMM, 'f', 2, 64), ".", ",", 1),
			strconv.Itoa(p.NominalWeightG), p.EAN, catalog.LevelLabel(sp.Level), sp.PurchaseDate, price,
			sp.Supplier, sp.Notes, hidden})
	}
	cw.Flush()
}

func (s *Server) accountDelete(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	if !strings.EqualFold(strings.TrimSpace(r.FormValue("confirm_email")), user.Email) {
		s.render(w, r, http.StatusUnprocessableEntity, "account", map[string]any{
			"Title": "Mon compte", "DeleteError": "L'adresse saisie ne correspond pas à votre compte.",
		})
		return
	}
	shareKeys, err := s.store.ShareStorageKeys(r.Context(), user.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	switch err := s.store.DeleteUser(r.Context(), user.ID); {
	case errors.Is(err, store.ErrActiveLoan):
		s.render(w, r, http.StatusConflict, "account", map[string]any{
			"Title": "Mon compte", "DeleteError": "Un prêt est en cours (prêté ou emprunté) : clôturez-le avant de supprimer votre compte.",
		})
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	}
	s.removeShareFiles(shareKeys)
	s.log.Info("compte supprimé", "user_id", user.ID)
	s.clearCookie(w, sessionCookie)
	s.setFlash(w, "Votre compte et toutes vos données ont été supprimés.")
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
