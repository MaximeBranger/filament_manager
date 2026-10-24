package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/MaximeBranger/filament_manager/internal/catalog"
	"github.com/MaximeBranger/filament_manager/internal/stock"
	"github.com/MaximeBranger/filament_manager/internal/store"
)

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

func today() string { return time.Now().Format("2006-01-02") }

// Liste ----------------------------------------------------------------------------

func (s *Server) stockList(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	spools, err := s.store.ListSpools(r.Context(), user.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	q := stock.ParseQuery(r.URL.Query())
	items := q.Apply(spools)

	// Facettes : seules les marques présentes dans le stock sont proposées au filtre.
	brands := map[string]string{}
	for _, sp := range spools {
		brands[catalog.Fold(sp.Product.Brand)] = sp.Product.Brand
	}
	brandOpts := make([]catalog.Option, 0, len(brands))
	for norm, label := range brands {
		brandOpts = append(brandOpts, catalog.Option{Code: norm, Label: label})
	}
	sort.Slice(brandOpts, func(i, j int) bool { return brandOpts[i].Code < brandOpts[j].Code })

	near := q.Near
	if near == "" {
		near = "#1565c0"
	}
	data := map[string]any{
		"Title":  "Mon stock",
		"Query":  q,
		"Items":  items,
		"Total":  len(spools),
		"Brands": brandOpts,
		"Near":   near,
	}
	if isHTMX(r) && r.Header.Get("HX-Target") == "results" {
		s.renderBlock(w, r, http.StatusOK, "stock", "results", data)
		return
	}
	s.render(w, r, http.StatusOK, "stock", data)
}

func (s *Server) spoolLevel(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	id, ok := pathID(r)
	level := r.FormValue("level")
	if !ok || !catalog.ValidLevel(level) {
		http.Error(w, "Requête invalide.", http.StatusBadRequest)
		return
	}
	if err := s.store.SetSpoolLevel(r.Context(), user.ID, id, level); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.notFound(w, r)
			return
		}
		s.serverError(w, r, err)
		return
	}
	if !isHTMX(r) {
		http.Redirect(w, r, "/stock", http.StatusSeeOther)
		return
	}
	sp, err := s.store.SpoolByID(r.Context(), user.ID, id)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.renderBlock(w, r, http.StatusOK, "stock", "spool_row", map[string]any{
		"Item": stock.Item{Spool: *sp, Hex: stock.EffectiveHex(sp.Product), Distance: -1},
	})
}

// Ajout ----------------------------------------------------------------------------

func productValues(p store.ProductFields) url.Values {
	v := url.Values{}
	v.Set("brand", p.Brand)
	v.Set("material", p.Material)
	v.Set("material_other", p.MaterialOther)
	v.Set("color_name", p.ColorName)
	v.Set("color_family", p.ColorFamily)
	hex := p.ColorHex
	if hex == "" {
		if f, ok := catalog.FamilyByCode(p.ColorFamily); ok {
			hex = f.Hex
		}
	}
	v.Set("color_hex", hex)
	v.Set("finish", p.Finish)
	v.Set("finish_other", p.FinishOther)
	v.Set("diameter", strconv.FormatFloat(p.DiameterMM, 'f', -1, 64))
	v.Set("weight", strconv.Itoa(p.NominalWeightG))
	v.Set("ean", p.EAN)
	return v
}

func spoolValues(sp store.SpoolFields) url.Values {
	v := url.Values{}
	v.Set("level", sp.Level)
	v.Set("purchase_date", sp.PurchaseDate)
	if sp.PriceCents != nil {
		v.Set("price", stock.FormatPrice(*sp.PriceCents))
	}
	v.Set("supplier", sp.Supplier)
	v.Set("notes", sp.Notes)
	if sp.Hidden {
		v.Set("hidden", "on")
	}
	return v
}

func defaultFormValues() url.Values {
	v := productValues(store.ProductFields{
		Material: "PLA", ColorFamily: "black", Finish: catalog.DefaultFinish,
		DiameterMM: catalog.DefaultDiameter, NominalWeightG: catalog.DefaultWeight,
	})
	v.Set("level", catalog.DefaultLevel)
	v.Set("purchase_date", today())
	v.Set("quantity", "1")
	return v
}

func (s *Server) spoolNew(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	form := defaultFormValues()
	var product *store.Product
	if raw := r.URL.Query().Get("product_id"); raw != "" {
		id, _ := strconv.ParseInt(raw, 10, 64)
		p, err := s.store.ProductByID(r.Context(), user.ID, id)
		if err != nil {
			s.notFound(w, r)
			return
		}
		product = p
	}
	if ean := r.URL.Query().Get("ean"); ean != "" {
		form.Set("ean", ean)
	}
	s.renderSpoolForm(w, r, http.StatusOK, product, form, nil)
}

func (s *Server) renderSpoolForm(w http.ResponseWriter, r *http.Request, status int, product *store.Product, form url.Values, errs stock.Errors) {
	user := currentUser(r)
	data := map[string]any{"Title": "Ajouter des bobines", "Form": form, "Errors": errs, "Product": product}
	if product == nil {
		products, err := s.store.ListProducts(r.Context(), user.ID)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		brands, err := s.store.BrandSuggestions(r.Context(), user.ID)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		data["Products"] = products
		data["BrandSuggestions"] = brands
	}
	s.render(w, r, status, "spool_new", data)
}

func (s *Server) spoolCreate(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Requête invalide.", http.StatusBadRequest)
		return
	}
	spoolFields, errs := stock.ParseSpool(r.PostForm)
	n := stock.ParseQuantity(r.PostForm, errs)

	if raw := r.PostForm.Get("product_id"); raw != "" {
		id, _ := strconv.ParseInt(raw, 10, 64)
		product, err := s.store.ProductByID(r.Context(), user.ID, id)
		if err != nil {
			s.notFound(w, r)
			return
		}
		if errs.Any() {
			s.renderSpoolForm(w, r, http.StatusUnprocessableEntity, product, r.PostForm, errs)
			return
		}
		if err := s.store.AddSpools(r.Context(), user.ID, id, n, spoolFields); err != nil {
			s.serverError(w, r, err)
			return
		}
	} else {
		productFields, perrs := stock.ParseProduct(r.PostForm)
		for k, v := range perrs {
			errs[k] = v
		}
		if errs.Any() {
			s.renderSpoolForm(w, r, http.StatusUnprocessableEntity, nil, r.PostForm, errs)
			return
		}
		brand, err := s.store.ResolveBrand(r.Context(), user.ID, productFields.Brand)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		productFields.Brand = brand
		if _, err := s.store.AddProductWithSpools(r.Context(), user.ID, productFields, n, spoolFields); err != nil {
			if errors.Is(err, store.ErrDuplicateEAN) {
				errs["ean"] = "Vous avez déjà un produit avec ce code-barre : choisissez-le dans la liste."
				s.renderSpoolForm(w, r, http.StatusUnprocessableEntity, nil, r.PostForm, errs)
				return
			}
			s.serverError(w, r, err)
			return
		}
	}
	s.setFlash(w, strconv.Itoa(n)+" "+pluralize(n, "bobine ajoutée", "bobines ajoutées")+".")
	http.Redirect(w, r, "/stock", http.StatusSeeOther)
}

func pluralize(n int, one, many string) string {
	if n > 1 {
		return many
	}
	return one
}

// Modification d'une bobine --------------------------------------------------------------

func (s *Server) spoolEdit(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	sp, err := s.store.SpoolByID(r.Context(), user.ID, id)
	if err != nil {
		s.notFound(w, r)
		return
	}
	s.render(w, r, http.StatusOK, "spool_edit", map[string]any{
		"Title": "Bobine", "Spool": sp, "Form": spoolValues(sp.SpoolFields),
	})
}

func (s *Server) spoolUpdate(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	sp, err := s.store.SpoolByID(r.Context(), user.ID, id)
	if err != nil {
		s.notFound(w, r)
		return
	}
	r.ParseForm()
	fields, errs := stock.ParseSpool(r.PostForm)
	if errs.Any() {
		s.render(w, r, http.StatusUnprocessableEntity, "spool_edit", map[string]any{
			"Title": "Bobine", "Spool": sp, "Form": r.PostForm, "Errors": errs,
		})
		return
	}
	if err := s.store.UpdateSpool(r.Context(), user.ID, id, fields); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.setFlash(w, "Bobine enregistrée.")
	http.Redirect(w, r, "/stock", http.StatusSeeOther)
}

func (s *Server) spoolDelete(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	if err := s.store.DeleteSpool(r.Context(), user.ID, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.notFound(w, r)
			return
		}
		s.serverError(w, r, err)
		return
	}
	s.setFlash(w, "Bobine supprimée.")
	http.Redirect(w, r, "/stock", http.StatusSeeOther)
}

// Produits ---------------------------------------------------------------------------

func (s *Server) productList(w http.ResponseWriter, r *http.Request) {
	products, err := s.store.ListProducts(r.Context(), currentUser(r).ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "products", map[string]any{"Title": "Mes produits", "Products": products})
}

func (s *Server) productEdit(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	p, err := s.store.ProductByID(r.Context(), user.ID, id)
	if err != nil {
		s.notFound(w, r)
		return
	}
	s.renderProductForm(w, r, http.StatusOK, p, productValues(p.ProductFields), nil)
}

func (s *Server) renderProductForm(w http.ResponseWriter, r *http.Request, status int, p *store.Product, form url.Values, errs stock.Errors) {
	brands, err := s.store.BrandSuggestions(r.Context(), currentUser(r).ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, status, "product_edit", map[string]any{
		"Title": "Produit", "Product": p, "Form": form, "Errors": errs, "BrandSuggestions": brands,
	})
}

func (s *Server) productUpdate(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	p, err := s.store.ProductByID(r.Context(), user.ID, id)
	if err != nil {
		s.notFound(w, r)
		return
	}
	r.ParseForm()
	fields, errs := stock.ParseProduct(r.PostForm)
	if errs.Any() {
		s.renderProductForm(w, r, http.StatusUnprocessableEntity, p, r.PostForm, errs)
		return
	}
	if fields.Brand, err = s.store.ResolveBrand(r.Context(), user.ID, fields.Brand); err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := s.store.UpdateProduct(r.Context(), user.ID, id, fields); err != nil {
		if errors.Is(err, store.ErrDuplicateEAN) {
			errs["ean"] = "Un autre de vos produits a déjà ce code-barre."
			s.renderProductForm(w, r, http.StatusUnprocessableEntity, p, r.PostForm, errs)
			return
		}
		s.serverError(w, r, err)
		return
	}
	s.setFlash(w, "Produit enregistré.")
	http.Redirect(w, r, "/products", http.StatusSeeOther)
}

func (s *Server) productDelete(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	switch err := s.store.DeleteProduct(r.Context(), user.ID, id); {
	case errors.Is(err, store.ErrNotFound):
		s.notFound(w, r)
		return
	case errors.Is(err, store.ErrProductInUse):
		s.setFlash(w, "Ce produit a encore des bobines : supprimez-les d'abord.")
	case err != nil:
		s.serverError(w, r, err)
		return
	default:
		s.setFlash(w, "Produit supprimé.")
	}
	http.Redirect(w, r, "/products", http.StatusSeeOther)
}

// Scan & réception ----------------------------------------------------------------------

func cleanEAN(raw string) string { return strings.ReplaceAll(strings.TrimSpace(raw), " ", "") }

func (s *Server) scanPage(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "scan", map[string]any{"Title": "Scanner"})
}

func (s *Server) scanLookup(w http.ResponseWriter, r *http.Request) {
	ean := cleanEAN(r.URL.Query().Get("ean"))
	if ean == "" {
		http.Redirect(w, r, "/scan", http.StatusSeeOther)
		return
	}
	p, err := s.store.ProductByEAN(r.Context(), currentUser(r).ID, ean)
	switch {
	case err == nil:
		http.Redirect(w, r, "/spools/new?product_id="+strconv.FormatInt(p.ID, 10), http.StatusSeeOther)
	case errors.Is(err, store.ErrNotFound):
		s.setFlash(w, "Code-barre inconnu : renseignez le produit une fois, il sera reconnu ensuite.")
		http.Redirect(w, r, "/spools/new?ean="+url.QueryEscape(ean), http.StatusSeeOther)
	default:
		s.serverError(w, r, err)
	}
}

type productJSON struct {
	ID    int64  `json:"id"`
	Label string `json:"label"`
	Hex   string `json:"hex"`
}

func productLabel(p store.ProductFields) string {
	material := catalog.MaterialLabel(p.Material)
	if p.Material == catalog.Other && p.MaterialOther != "" {
		material = p.MaterialOther
	}
	return p.Brand + " " + material + " " + p.ColorName + " · " + formatWeight(p.NominalWeightG)
}

func (s *Server) productLookup(w http.ResponseWriter, r *http.Request) {
	ean := cleanEAN(r.URL.Query().Get("ean"))
	resp := map[string]any{"found": false}
	if ean != "" {
		p, err := s.store.ProductByEAN(r.Context(), currentUser(r).ID, ean)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			s.serverError(w, r, err)
			return
		}
		if p != nil {
			resp = map[string]any{"found": true, "product": productJSON{p.ID, productLabel(p.ProductFields), stock.EffectiveHex(*p)}}
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) receptionPage(w http.ResponseWriter, r *http.Request) {
	brands, err := s.store.BrandSuggestions(r.Context(), currentUser(r).ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "reception", map[string]any{
		"Title": "Réception", "Form": defaultFormValues(), "BrandSuggestions": brands, "Today": today(),
	})
}

type receptionRequest struct {
	PurchaseDate string `json:"purchase_date"`
	Supplier     string `json:"supplier"`
	Items        []struct {
		ProductID int64             `json:"product_id"`
		Quantity  int               `json:"quantity"`
		Product   map[string]string `json:"product"`
	} `json:"items"`
}

func (s *Server) receptionSubmit(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	var req receptionRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "Requête invalide."})
		return
	}
	if len(req.Items) == 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "Aucune bobine à enregistrer."})
		return
	}

	common, errs := stock.ParseSpool(url.Values{
		"purchase_date": {req.PurchaseDate}, "supplier": {req.Supplier}, "level": {catalog.DefaultLevel},
	})
	if errs.Any() {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "Date ou fournisseur invalide.", "fields": errs})
		return
	}

	items := make([]store.ReceptionItem, 0, len(req.Items))
	itemErrors := map[int]stock.Errors{}
	total := 0
	for i, it := range req.Items {
		item := store.ReceptionItem{ProductID: it.ProductID, Quantity: it.Quantity}
		if it.Quantity < 1 || it.Quantity > stock.MaxQuantity {
			itemErrors[i] = stock.Errors{"quantity": "Quantité entre 1 et " + strconv.Itoa(stock.MaxQuantity) + "."}
			continue
		}
		if it.ProductID == 0 {
			v := url.Values{}
			for k, val := range it.Product {
				v.Set(k, val)
			}
			fields, perrs := stock.ParseProduct(v)
			if perrs.Any() {
				itemErrors[i] = perrs
				continue
			}
			brand, err := s.store.ResolveBrand(r.Context(), user.ID, fields.Brand)
			if err != nil {
				s.serverError(w, r, err)
				return
			}
			fields.Brand = brand
			item.NewProduct = &fields
		}
		total += it.Quantity
		items = append(items, item)
	}
	if len(itemErrors) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "Certaines lignes sont invalides.", "items": itemErrors})
		return
	}

	switch err := s.store.Receive(r.Context(), user.ID, items, common); {
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "Un des produits n'existe plus. Rechargez la page."})
		return
	case errors.Is(err, store.ErrDuplicateEAN):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "Un code-barre correspond déjà à l'un de vos produits. Rechargez la page et rescannez."})
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	}
	s.setFlash(w, "Réception enregistrée : "+strconv.Itoa(total)+" "+pluralize(total, "bobine ajoutée", "bobines ajoutées")+".")
	writeJSON(w, http.StatusOK, map[string]any{"redirect": "/stock", "created": total})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
