package store

import (
	"context"
	"crypto/rand"
	"errors"
	"strings"
	"time"
)

var ErrDuplicatePasskey = errors.New("cette passkey est déjà enregistrée")

// Passkey est une clé d'accès WebAuthn. Data contient l'enregistrement complet de la
// bibliothèque WebAuthn (JSON), opaque pour le store.
type Passkey struct {
	ID           int64
	UserID       int64
	CredentialID []byte
	Name         string
	Data         string
	CreatedAt    time.Time
	LastUsedAt   time.Time
}

// WebAuthnID renvoie l'identifiant WebAuthn de l'utilisateur, créé au premier appel.
func (s *Store) WebAuthnID(ctx context.Context, userID int64) ([]byte, error) {
	id := make([]byte, 32)
	rand.Read(id)
	if _, err := s.DB.ExecContext(ctx, `UPDATE users SET webauthn_id = ? WHERE id = ? AND webauthn_id IS NULL`, id, userID); err != nil {
		return nil, err
	}
	var current []byte
	err := s.DB.QueryRowContext(ctx, `SELECT webauthn_id FROM users WHERE id = ?`, userID).Scan(&current)
	return current, notFound(err)
}

// UserByWebAuthnID retrouve l'utilisateur à partir de l'identifiant renvoyé par l'authentificateur.
func (s *Store) UserByWebAuthnID(ctx context.Context, webauthnID []byte) (*User, error) {
	return scanUser(s.DB.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE webauthn_id = ?`, webauthnID))
}

func (s *Store) ListPasskeys(ctx context.Context, userID int64) ([]Passkey, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, user_id, credential_id, name, data, created_at, last_used_at
		FROM webauthn_credentials WHERE user_id = ? ORDER BY id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Passkey
	for rows.Next() {
		var p Passkey
		if err := rows.Scan(&p.ID, &p.UserID, &p.CredentialID, &p.Name, &p.Data, ts{&p.CreatedAt}, ts{&p.LastUsedAt}); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) AddPasskey(ctx context.Context, userID int64, credentialID []byte, name, data string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO webauthn_credentials (user_id, credential_id, name, data, created_at)
		VALUES (?, ?, ?, ?, ?)`, userID, credentialID, name, data, s.now())
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return ErrDuplicatePasskey
	}
	return err
}

// TouchPasskey enregistre l'état mis à jour (compteur de signatures) après une connexion.
func (s *Store) TouchPasskey(ctx context.Context, credentialID []byte, data string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE webauthn_credentials SET data = ?, last_used_at = ? WHERE credential_id = ?`,
		data, s.now(), credentialID)
	return err
}

func (s *Store) DeletePasskey(ctx context.Context, userID, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM webauthn_credentials WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
