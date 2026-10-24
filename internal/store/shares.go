package store

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"time"

	"github.com/MaximeBranger/filament_manager/internal/catalog"
	"github.com/MaximeBranger/filament_manager/internal/color"
)

const (
	NotifShareChoice = "share_choice"
	NotifShareLocked = "share_locked"

	// ShareLockThreshold est le nombre d'échecs cumulés de mot de passe qui verrouille un partage.
	ShareLockThreshold = 20
)

var ErrTooManyShares = errors.New("nombre maximal de partages actifs atteint")

type ShareFile struct {
	ID           int64
	OriginalName string
	StorageKey   string
	Triangles    int
	SizeX        float64
	SizeY        float64
	SizeZ        float64
	Bytes        int64
}

type ShareChoice struct {
	ProductID     int64
	Label         string
	ColorHex      string
	RecipientName string
	Message       string
	UpdatedAt     time.Time
}

type Share struct {
	ID              int64
	UserID          int64
	OwnerName       string
	Title           string
	PasswordHash    string
	PasswordVersion int64
	FailedAttempts  int
	LockedAt        time.Time
	CreatedAt       time.Time
	ExpiresAt       time.Time
	File            ShareFile
	Choice          *ShareChoice
}

func (sh Share) HasPassword() bool { return sh.PasswordHash != "" }
func (sh Share) Locked() bool      { return !sh.LockedAt.IsZero() }

const shareSelect = `SELECT sh.id, sh.user_id, u.display_name, sh.title, sh.password_hash, sh.password_version,
	sh.failed_attempts, sh.locked_at, sh.created_at, sh.expires_at,
	f.id, f.original_name, f.storage_key, f.triangles, f.size_x_mm, f.size_y_mm, f.size_z_mm, f.bytes,
	c.product_id, c.label, c.color_hex, c.recipient_name, c.message, c.updated_at
	FROM shares sh
	JOIN users u ON u.id = sh.user_id
	JOIN share_files f ON f.share_id = sh.id
	LEFT JOIN share_choices c ON c.share_id = sh.id`

func scanShare(row interface{ Scan(...any) error }) (*Share, error) {
	sh := &Share{}
	f := &sh.File
	var choiceProduct sql.NullInt64
	var choiceLabel, choiceHex sql.NullString
	var c ShareChoice
	err := row.Scan(&sh.ID, &sh.UserID, &sh.OwnerName, &sh.Title, str{&sh.PasswordHash}, &sh.PasswordVersion,
		&sh.FailedAttempts, ts{&sh.LockedAt}, ts{&sh.CreatedAt}, ts{&sh.ExpiresAt},
		&f.ID, &f.OriginalName, &f.StorageKey, &f.Triangles, &f.SizeX, &f.SizeY, &f.SizeZ, &f.Bytes,
		&choiceProduct, &choiceLabel, &choiceHex, str{&c.RecipientName}, str{&c.Message}, ts{&c.UpdatedAt})
	if err != nil {
		return nil, notFound(err)
	}
	if choiceLabel.Valid {
		c.ProductID, c.Label, c.ColorHex = choiceProduct.Int64, choiceLabel.String, choiceHex.String
		sh.Choice = &c
	}
	return sh, nil
}

// CreateShare enregistre un partage et son fichier, dans la limite de max partages actifs.
func (s *Store) CreateShare(ctx context.Context, userID int64, tokenHash, title, passwordHash string,
	f ShareFile, max int, ttl time.Duration) (int64, error) {
	var id int64
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var active int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM shares WHERE user_id = ? AND expires_at > ?`,
			userID, s.now()).Scan(&active); err != nil {
			return err
		}
		if active >= max {
			return ErrTooManyShares
		}
		now := s.Now()
		res, err := tx.ExecContext(ctx, `INSERT INTO shares (user_id, token_hash, title, password_hash, created_at, expires_at)
			VALUES (?, ?, ?, ?, ?, ?)`, userID, tokenHash, title, nullable(passwordHash),
			now.UTC().Format(timeFormat), now.Add(ttl).UTC().Format(timeFormat))
		if err != nil {
			return err
		}
		id, _ = res.LastInsertId()
		_, err = tx.ExecContext(ctx, `INSERT INTO share_files (share_id, original_name, storage_key, triangles,
			size_x_mm, size_y_mm, size_z_mm, bytes, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, f.OriginalName, f.StorageKey, f.Triangles, f.SizeX, f.SizeY, f.SizeZ, f.Bytes, s.now())
		return err
	})
	return id, err
}

// ListShares renvoie les partages actifs de l'utilisateur.
func (s *Store) ListShares(ctx context.Context, userID int64) ([]Share, error) {
	rows, err := s.DB.QueryContext(ctx, shareSelect+` WHERE sh.user_id = ? AND sh.expires_at > ? ORDER BY sh.id DESC`,
		userID, s.now())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Share
	for rows.Next() {
		sh, err := scanShare(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sh)
	}
	return out, rows.Err()
}

// ShareByID renvoie un partage actif de l'utilisateur.
func (s *Store) ShareByID(ctx context.Context, userID, id int64) (*Share, error) {
	return scanShare(s.DB.QueryRowContext(ctx, shareSelect+` WHERE sh.id = ? AND sh.user_id = ? AND sh.expires_at > ?`,
		id, userID, s.now()))
}

// ShareByToken renvoie un partage actif à partir de l'empreinte de son jeton.
func (s *Store) ShareByToken(ctx context.Context, tokenHash string) (*Share, error) {
	return scanShare(s.DB.QueryRowContext(ctx, shareSelect+` WHERE sh.token_hash = ? AND sh.expires_at > ?`,
		tokenHash, s.now()))
}

// SetSharePassword change ou retire (hash vide) le mot de passe : les cookies d'accès existants
// sont invalidés et un éventuel verrouillage est levé.
func (s *Store) SetSharePassword(ctx context.Context, userID, id int64, passwordHash string) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE shares SET password_hash = ?, password_version = password_version + 1,
		failed_attempts = 0, locked_at = NULL WHERE id = ? AND user_id = ? AND expires_at > ?`,
		nullable(passwordHash), id, userID, s.now())
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// RecordShareFailure compte un mot de passe erroné et verrouille le partage au seuil.
func (s *Store) RecordShareFailure(ctx context.Context, shareID int64) (bool, []Notification, error) {
	var locked bool
	var notifs []Notification
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var failed int
		var ownerID int64
		var title string
		var lockedAt sql.NullString
		if err := tx.QueryRowContext(ctx, `UPDATE shares SET failed_attempts = failed_attempts + 1 WHERE id = ?
			RETURNING failed_attempts, user_id, title, locked_at`, shareID).Scan(&failed, &ownerID, &title, &lockedAt); err != nil {
			return notFound(err)
		}
		if failed < ShareLockThreshold || lockedAt.Valid {
			locked = lockedAt.Valid
			return nil
		}
		locked = true
		if _, err := tx.ExecContext(ctx, `UPDATE shares SET locked_at = ? WHERE id = ?`, s.now(), shareID); err != nil {
			return err
		}
		n, err := s.notify(ctx, tx, ownerID, NotifShareLocked, NotificationPayload{Title: title})
		notifs = append(notifs, n)
		return err
	})
	return locked, notifs, err
}

// SaveShareChoice enregistre (ou remplace) le choix du destinataire et prévient le créateur.
func (s *Store) SaveShareChoice(ctx context.Context, shareID int64, c ShareChoice) ([]Notification, error) {
	var notifs []Notification
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var ownerID int64
		var title string
		if err := tx.QueryRowContext(ctx, `SELECT user_id, title FROM shares WHERE id = ? AND expires_at > ?`,
			shareID, s.now()).Scan(&ownerID, &title); err != nil {
			return notFound(err)
		}
		now := s.now()
		var productID any
		if c.ProductID != 0 {
			productID = c.ProductID
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO share_choices (share_id, product_id, label, color_hex, recipient_name,
			message, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (share_id) DO UPDATE SET product_id = excluded.product_id, label = excluded.label,
			color_hex = excluded.color_hex, recipient_name = excluded.recipient_name, message = excluded.message,
			updated_at = excluded.updated_at`,
			shareID, productID, c.Label, c.ColorHex, nullable(c.RecipientName), nullable(c.Message), now, now); err != nil {
			return err
		}
		actor := c.RecipientName
		if actor == "" {
			actor = "Votre destinataire"
		}
		n, err := s.notify(ctx, tx, ownerID, NotifShareChoice, NotificationPayload{Actor: actor, Label: c.Label, Title: title})
		notifs = append(notifs, n)
		return err
	})
	return notifs, err
}

// RevokeShare supprime le partage et renvoie les clés des fichiers à effacer.
func (s *Store) RevokeShare(ctx context.Context, userID, id int64) ([]string, error) {
	var keys []string
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var err error
		if keys, err = storageKeys(ctx, tx, `SELECT f.storage_key FROM share_files f JOIN shares sh ON sh.id = f.share_id
			WHERE sh.id = ? AND sh.user_id = ?`, id, userID); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM shares WHERE id = ? AND user_id = ?`, id, userID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return nil
	})
	return keys, err
}

func storageKeys(ctx context.Context, q querier, query string, args ...any) ([]string, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

// ExpiredShares renvoie les partages expirés et les clés de leurs fichiers.
func (s *Store) ExpiredShares(ctx context.Context) ([]int64, []string, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT sh.id, f.storage_key FROM shares sh
		LEFT JOIN share_files f ON f.share_id = sh.id WHERE sh.expires_at <= ?`, s.now())
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var ids []int64
	var keys []string
	for rows.Next() {
		var id int64
		var key sql.NullString
		if err := rows.Scan(&id, &key); err != nil {
			return nil, nil, err
		}
		ids = append(ids, id)
		if key.Valid {
			keys = append(keys, key.String)
		}
	}
	return ids, keys, rows.Err()
}

func (s *Store) DeleteShares(ctx context.Context, ids []int64) error {
	for _, id := range ids {
		if _, err := s.DB.ExecContext(ctx, `DELETE FROM shares WHERE id = ?`, id); err != nil {
			return err
		}
	}
	return nil
}

// ShareStorageKeys renvoie les fichiers de tous les partages de l'utilisateur.
func (s *Store) ShareStorageKeys(ctx context.Context, userID int64) ([]string, error) {
	return storageKeys(ctx, s.DB, `SELECT f.storage_key FROM share_files f JOIN shares sh ON sh.id = f.share_id
		WHERE sh.user_id = ?`, userID)
}

// AllShareStorageKeys renvoie l'ensemble des fichiers référencés en base.
func (s *Store) AllShareStorageKeys(ctx context.Context) (map[string]bool, error) {
	keys, err := storageKeys(ctx, s.DB, `SELECT storage_key FROM share_files`)
	known := make(map[string]bool, len(keys))
	for _, k := range keys {
		known[k] = true
	}
	return known, err
}

// ShareColors renvoie les produits proposables au destinataire d'un partage : au moins une
// bobine non vide, non masquée et non prêtée. Un produit = une pastille, triées par teinte.
func (s *Store) ShareColors(ctx context.Context, userID int64) ([]Product, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+productCols+` FROM products p
		WHERE p.user_id = ? AND EXISTS (
			SELECT 1 FROM spools s WHERE s.product_id = p.id AND s.level != 'empty' AND s.hidden_from_friends = 0
			AND NOT EXISTS (SELECT 1 FROM loans l WHERE l.spool_id = s.id AND l.ended_at IS NULL))`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Product
	for rows.Next() {
		var p Product
		if err := rows.Scan(productDest(&p)...); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, _ := color.ParseHex(out[i].Hex())
		b, _ := color.ParseHex(out[j].Hex())
		return a.SortKey() < b.SortKey()
	})
	return out, nil
}

// ColorLabel décrit une couleur pour le destinataire : "eSun PLA+ Galaxy Black (Silk)".
func (p ProductFields) ColorLabel() string {
	material := catalog.MaterialLabel(p.Material)
	if p.Material == catalog.Other && p.MaterialOther != "" {
		material = p.MaterialOther
	}
	label := p.Brand + " " + material + " " + p.ColorName
	if p.Finish != catalog.DefaultFinish {
		finish := catalog.FinishLabel(p.Finish)
		if p.Finish == catalog.Other && p.FinishOther != "" {
			finish = p.FinishOther
		}
		label += " (" + finish + ")"
	}
	return label
}
