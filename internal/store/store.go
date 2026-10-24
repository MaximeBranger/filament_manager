// Package store regroupe l'accès aux données SQLite.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/MaximeBranger/filament_manager/internal/db"
)

// ErrNotFound est renvoyé quand l'objet n'existe pas ou n'appartient pas à l'utilisateur.
var ErrNotFound = errors.New("introuvable")

type Store struct {
	DB  *sql.DB
	Now func() time.Time
}

func New(conn *sql.DB) *Store {
	return &Store{DB: conn, Now: time.Now}
}

type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

const timeFormat = db.TimeFormat

func (s *Store) now() string { return s.Now().UTC().Format(timeFormat) }

func (s *Store) inTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// Ping vérifie que la base répond.
func (s *Store) Ping(ctx context.Context) error {
	var one int
	return s.DB.QueryRowContext(ctx, `SELECT 1`).Scan(&one)
}

// Backup écrit une copie cohérente de la base dans path.
func (s *Store) Backup(ctx context.Context, path string) error {
	_, err := s.DB.ExecContext(ctx, `VACUUM INTO ?`, path)
	return err
}

// nullable convertit "" en NULL.
func nullable(v string) any {
	if v == "" {
		return nil
	}
	return v
}

// str scanne une colonne TEXT éventuellement NULL dans une chaîne.
type str struct{ p *string }

func (n str) Scan(v any) error {
	switch t := v.(type) {
	case nil:
		*n.p = ""
	case string:
		*n.p = t
	case []byte:
		*n.p = string(t)
	default:
		return fmt.Errorf("type inattendu %T", v)
	}
	return nil
}

// ts scanne une date stockée en texte.
type ts struct{ p *time.Time }

func (n ts) Scan(v any) error {
	var raw string
	if err := (str{&raw}).Scan(v); err != nil {
		return err
	}
	if raw == "" {
		*n.p = time.Time{}
		return nil
	}
	t, err := time.Parse(db.TimeFormat, raw)
	if err != nil {
		return err
	}
	*n.p = t
	return nil
}

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
