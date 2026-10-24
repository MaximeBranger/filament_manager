package store

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/MaximeBranger/filament_manager/internal/catalog"
)

var (
	ErrDuplicateEAN = errors.New("un produit avec ce code-barre existe déjà")
	ErrProductInUse = errors.New("le produit a encore des bobines")
)

// ProductFields sont les champs saisissables d'un produit.
type ProductFields struct {
	Brand          string
	Material       string
	MaterialOther  string
	ColorName      string
	ColorFamily    string
	ColorHex       string
	Finish         string
	FinishOther    string
	DiameterMM     float64
	NominalWeightG int
	EAN            string
}

type Product struct {
	ID     int64
	UserID int64
	ProductFields
	SpoolCount int
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// SpoolFields sont les champs saisissables d'une bobine.
type SpoolFields struct {
	Level        string
	PurchaseDate string // YYYY-MM-DD ou vide
	PriceCents   *int64
	Supplier     string
	Notes        string
	Hidden       bool
}

type Spool struct {
	ID        int64
	UserID    int64
	ProductID int64
	SpoolFields
	CreatedAt time.Time
	UpdatedAt time.Time
	Product   Product
}

const productCols = `p.id, p.user_id, p.brand, p.material, p.material_other, p.color_name, p.color_family,
	p.color_hex, p.finish, p.finish_other, p.diameter_mm, p.nominal_weight_g, p.ean, p.created_at, p.updated_at`

func productDest(p *Product) []any {
	return []any{&p.ID, &p.UserID, &p.Brand, &p.Material, str{&p.MaterialOther}, &p.ColorName, &p.ColorFamily,
		str{&p.ColorHex}, &p.Finish, str{&p.FinishOther}, &p.DiameterMM, &p.NominalWeightG, str{&p.EAN},
		ts{&p.CreatedAt}, ts{&p.UpdatedAt}}
}

const spoolCols = `s.id, s.user_id, s.product_id, s.level, s.purchase_date, s.price_cents, s.supplier, s.notes,
	s.hidden_from_friends, s.created_at, s.updated_at`

func spoolDest(sp *Spool) []any {
	return []any{&sp.ID, &sp.UserID, &sp.ProductID, &sp.Level, str{&sp.PurchaseDate}, &sp.PriceCents,
		str{&sp.Supplier}, str{&sp.Notes}, &sp.Hidden, ts{&sp.CreatedAt}, ts{&sp.UpdatedAt}}
}

func isDuplicateEAN(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed: products.user_id, products.ean")
}

// Produits ---------------------------------------------------------------------------

func (s *Store) ProductByID(ctx context.Context, userID, id int64) (*Product, error) {
	p := &Product{}
	err := s.DB.QueryRowContext(ctx, `SELECT `+productCols+`,
		(SELECT COUNT(*) FROM spools WHERE product_id = p.id)
		FROM products p WHERE p.id = ? AND p.user_id = ?`, id, userID).
		Scan(append(productDest(p), &p.SpoolCount)...)
	if err != nil {
		return nil, notFound(err)
	}
	return p, nil
}

func (s *Store) ProductByEAN(ctx context.Context, userID int64, ean string) (*Product, error) {
	var id int64
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM products WHERE user_id = ? AND ean = ?`, userID, ean).Scan(&id)
	if err != nil {
		return nil, notFound(err)
	}
	return s.ProductByID(ctx, userID, id)
}

// ListProducts renvoie les produits de l'utilisateur avec leur nombre de bobines.
func (s *Store) ListProducts(ctx context.Context, userID int64) ([]Product, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+productCols+`,
		(SELECT COUNT(*) FROM spools WHERE product_id = p.id)
		FROM products p WHERE p.user_id = ?
		ORDER BY p.brand_norm, p.material, p.color_name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Product
	for rows.Next() {
		var p Product
		if err := rows.Scan(append(productDest(&p), &p.SpoolCount)...); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) insertProduct(ctx context.Context, q querier, userID int64, f ProductFields) (int64, error) {
	now := s.now()
	res, err := q.ExecContext(ctx, `INSERT INTO products (user_id, brand, brand_norm, material, material_other,
		color_name, color_family, color_hex, finish, finish_other, diameter_mm, nominal_weight_g, ean, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		userID, f.Brand, catalog.Fold(f.Brand), f.Material, nullable(f.MaterialOther), f.ColorName, f.ColorFamily,
		nullable(f.ColorHex), f.Finish, nullable(f.FinishOther), f.DiameterMM, f.NominalWeightG, nullable(f.EAN), now, now)
	if isDuplicateEAN(err) {
		return 0, ErrDuplicateEAN
	}
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) CreateProduct(ctx context.Context, userID int64, f ProductFields) (int64, error) {
	return s.insertProduct(ctx, s.DB, userID, f)
}

func (s *Store) UpdateProduct(ctx context.Context, userID, id int64, f ProductFields) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE products SET brand = ?, brand_norm = ?, material = ?, material_other = ?,
		color_name = ?, color_family = ?, color_hex = ?, finish = ?, finish_other = ?, diameter_mm = ?,
		nominal_weight_g = ?, ean = ?, updated_at = ?
		WHERE id = ? AND user_id = ?`,
		f.Brand, catalog.Fold(f.Brand), f.Material, nullable(f.MaterialOther), f.ColorName, f.ColorFamily,
		nullable(f.ColorHex), f.Finish, nullable(f.FinishOther), f.DiameterMM, f.NominalWeightG, nullable(f.EAN),
		s.now(), id, userID)
	if isDuplicateEAN(err) {
		return ErrDuplicateEAN
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteProduct supprime un produit sans bobine.
func (s *Store) DeleteProduct(ctx context.Context, userID, id int64) error {
	p, err := s.ProductByID(ctx, userID, id)
	if err != nil {
		return err
	}
	if p.SpoolCount > 0 {
		return ErrProductInUse
	}
	_, err = s.DB.ExecContext(ctx, `DELETE FROM products WHERE id = ? AND user_id = ?
		AND NOT EXISTS (SELECT 1 FROM spools WHERE product_id = products.id)`, id, userID)
	return err
}

// Marques ----------------------------------------------------------------------------

// popularBrandThreshold est le nombre de comptes distincts au-delà duquel la marque
// d'un autre utilisateur est proposée à tous.
const popularBrandThreshold = 3

// BrandSuggestions renvoie la liste triée des marques proposées à l'utilisateur.
func (s *Store) BrandSuggestions(ctx context.Context, userID int64) ([]string, error) {
	byNorm := map[string]string{}
	for _, b := range catalog.SeedBrands {
		byNorm[catalog.Fold(b)] = b
	}
	rows, err := s.DB.QueryContext(ctx, `
		SELECT brand_norm, brand FROM (
			SELECT brand_norm, brand, COUNT(*) AS n,
				ROW_NUMBER() OVER (PARTITION BY brand_norm ORDER BY COUNT(*) DESC, brand) AS rk
			FROM products
			WHERE brand_norm IN (SELECT brand_norm FROM products GROUP BY brand_norm HAVING COUNT(DISTINCT user_id) >= ?)
			GROUP BY brand_norm, brand
		) WHERE rk = 1
		UNION ALL
		SELECT brand_norm, MIN(brand) FROM products WHERE user_id = ? GROUP BY brand_norm`,
		popularBrandThreshold, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var norm, brand string
		if err := rows.Scan(&norm, &brand); err != nil {
			return nil, err
		}
		byNorm[norm] = brand // les marques de l'utilisateur (en dernier) priment
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(byNorm))
	for _, b := range byNorm {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return catalog.Fold(out[i]) < catalog.Fold(out[j]) })
	return out, nil
}

// ResolveBrand renvoie la graphie de référence d'une marque saisie : celle déjà utilisée par
// l'utilisateur, sinon celle des suggestions, sinon la saisie nettoyée.
func (s *Store) ResolveBrand(ctx context.Context, userID int64, input string) (string, error) {
	clean := catalog.CleanBrand(input)
	norm := catalog.Fold(clean)
	var own string
	err := s.DB.QueryRowContext(ctx, `SELECT brand FROM products WHERE user_id = ? AND brand_norm = ? ORDER BY id LIMIT 1`,
		userID, norm).Scan(&own)
	if err == nil {
		return own, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	suggestions, err := s.BrandSuggestions(ctx, userID)
	if err != nil {
		return "", err
	}
	for _, b := range suggestions {
		if catalog.Fold(b) == norm {
			return b, nil
		}
	}
	return clean, nil
}

// Bobines ----------------------------------------------------------------------------

func (s *Store) insertSpools(ctx context.Context, q querier, userID, productID int64, n int, f SpoolFields) error {
	now := s.now()
	for range n {
		_, err := q.ExecContext(ctx, `INSERT INTO spools (user_id, product_id, level, purchase_date, price_cents,
			supplier, notes, hidden_from_friends, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			userID, productID, f.Level, nullable(f.PurchaseDate), f.PriceCents, nullable(f.Supplier),
			nullable(f.Notes), f.Hidden, now, now)
		if err != nil {
			return err
		}
	}
	return nil
}

func ownsProduct(ctx context.Context, q querier, userID, productID int64) error {
	var one int
	err := q.QueryRowContext(ctx, `SELECT 1 FROM products WHERE id = ? AND user_id = ?`, productID, userID).Scan(&one)
	return notFound(err)
}

// AddSpools crée n bobines d'un produit existant de l'utilisateur.
func (s *Store) AddSpools(ctx context.Context, userID, productID int64, n int, f SpoolFields) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if err := ownsProduct(ctx, tx, userID, productID); err != nil {
			return err
		}
		return s.insertSpools(ctx, tx, userID, productID, n, f)
	})
}

// AddProductWithSpools crée un produit et n bobines en une transaction. Renvoie l'id du produit.
func (s *Store) AddProductWithSpools(ctx context.Context, userID int64, p ProductFields, n int, f SpoolFields) (int64, error) {
	var productID int64
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var err error
		if productID, err = s.insertProduct(ctx, tx, userID, p); err != nil {
			return err
		}
		return s.insertSpools(ctx, tx, userID, productID, n, f)
	})
	return productID, err
}

// ReceptionItem est une ligne de réception : un produit existant ou nouveau, en quantité.
type ReceptionItem struct {
	ProductID  int64
	NewProduct *ProductFields
	Quantity   int
}

// Receive crée toutes les bobines d'une réception en une seule transaction.
func (s *Store) Receive(ctx context.Context, userID int64, items []ReceptionItem, f SpoolFields) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		for _, it := range items {
			productID := it.ProductID
			if it.NewProduct != nil {
				var err error
				if productID, err = s.insertProduct(ctx, tx, userID, *it.NewProduct); err != nil {
					return err
				}
			} else if err := ownsProduct(ctx, tx, userID, productID); err != nil {
				return err
			}
			if err := s.insertSpools(ctx, tx, userID, productID, it.Quantity, f); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) SpoolByID(ctx context.Context, userID, id int64) (*Spool, error) {
	sp := &Spool{}
	err := s.DB.QueryRowContext(ctx, `SELECT `+spoolCols+`, `+productCols+`
		FROM spools s JOIN products p ON p.id = s.product_id
		WHERE s.id = ? AND s.user_id = ?`, id, userID).
		Scan(append(spoolDest(sp), productDest(&sp.Product)...)...)
	if err != nil {
		return nil, notFound(err)
	}
	return sp, nil
}

// ListSpools renvoie toutes les bobines de l'utilisateur avec leur produit. Le filtrage et le
// tri sont faits en mémoire (package stock) : les volumes restent faibles.
func (s *Store) ListSpools(ctx context.Context, userID int64) ([]Spool, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+spoolCols+`, `+productCols+`
		FROM spools s JOIN products p ON p.id = s.product_id
		WHERE s.user_id = ? ORDER BY s.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Spool
	for rows.Next() {
		var sp Spool
		if err := rows.Scan(append(spoolDest(&sp), productDest(&sp.Product)...)...); err != nil {
			return nil, err
		}
		out = append(out, sp)
	}
	return out, rows.Err()
}

func (s *Store) UpdateSpool(ctx context.Context, userID, id int64, f SpoolFields) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE spools SET level = ?, purchase_date = ?, price_cents = ?, supplier = ?,
		notes = ?, hidden_from_friends = ?, updated_at = ? WHERE id = ? AND user_id = ?`,
		f.Level, nullable(f.PurchaseDate), f.PriceCents, nullable(f.Supplier), nullable(f.Notes), f.Hidden,
		s.now(), id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetSpoolLevel(ctx context.Context, userID, id int64, level string) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE spools SET level = ?, updated_at = ? WHERE id = ? AND user_id = ?`,
		level, s.now(), id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteSpool(ctx context.Context, userID, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM spools WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
