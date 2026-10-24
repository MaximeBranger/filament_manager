package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/MaximeBranger/filament_manager/internal/db"
)

type User struct {
	ID                 int64
	Email              string
	DisplayName        string
	EmailNotifications bool
	CreatedAt          time.Time
}

const userCols = `id, email, display_name, email_notifications, created_at`

func scanUser(row interface{ Scan(...any) error }) (*User, error) {
	u := &User{}
	if err := row.Scan(&u.ID, &u.Email, &u.DisplayName, &u.EmailNotifications, ts{&u.CreatedAt}); err != nil {
		return nil, notFound(err)
	}
	return u, nil
}

func (s *Store) UserByID(ctx context.Context, id int64) (*User, error) {
	return scanUser(s.DB.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id = ?`, id))
}

func (s *Store) UserByEmail(ctx context.Context, email string) (*User, error) {
	return scanUser(s.DB.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE email = ?`, email))
}

func (s *Store) CreateUser(ctx context.Context, email, displayName string) (*User, error) {
	res, err := s.DB.ExecContext(ctx, `INSERT INTO users (email, display_name, created_at) VALUES (?, ?, ?)`,
		email, displayName, s.now())
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.UserByID(ctx, id)
}

func (s *Store) UpdateDisplayName(ctx context.Context, userID int64, name string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE users SET display_name = ? WHERE id = ?`, name, userID)
	return err
}

// DeleteUser supprime le compte et, en cascade, toutes ses données.
func (s *Store) DeleteUser(ctx context.Context, userID int64) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		// Les bobines référencent les produits sans cascade : on les supprime d'abord.
		if _, err := tx.ExecContext(ctx, `DELETE FROM spools WHERE user_id = ?`, userID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM products WHERE user_id = ?`, userID); err != nil {
			return err
		}
		var email string
		if err := tx.QueryRowContext(ctx, `SELECT email FROM users WHERE id = ?`, userID).Scan(&email); err != nil {
			return notFound(err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM login_challenges WHERE email = ?`, email); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, userID)
		return err
	})
}

// Sessions ---------------------------------------------------------------------

type Session struct {
	IDHash     string
	UserID     int64
	ExpiresAt  time.Time
	LastSeenAt time.Time
}

func (s *Store) CreateSession(ctx context.Context, idHash string, userID int64, expiresAt time.Time) error {
	now := s.now()
	_, err := s.DB.ExecContext(ctx, `INSERT INTO sessions (id_hash, user_id, created_at, expires_at, last_seen_at) VALUES (?, ?, ?, ?, ?)`,
		idHash, userID, now, expiresAt.UTC().Format(db.TimeFormat), now)
	return err
}

// SessionByHash renvoie une session non expirée.
func (s *Store) SessionByHash(ctx context.Context, idHash string) (*Session, error) {
	sess := &Session{IDHash: idHash}
	err := s.DB.QueryRowContext(ctx, `SELECT user_id, expires_at, last_seen_at FROM sessions WHERE id_hash = ? AND expires_at > ?`,
		idHash, s.now()).Scan(&sess.UserID, ts{&sess.ExpiresAt}, ts{&sess.LastSeenAt})
	if err != nil {
		return nil, notFound(err)
	}
	return sess, nil
}

func (s *Store) RenewSession(ctx context.Context, idHash string, expiresAt time.Time) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE sessions SET expires_at = ?, last_seen_at = ? WHERE id_hash = ?`,
		expiresAt.UTC().Format(db.TimeFormat), s.now(), idHash)
	return err
}

func (s *Store) DeleteSession(ctx context.Context, idHash string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE id_hash = ?`, idHash)
	return err
}

// Défis de connexion (lien magique + code) ------------------------------------------

type Challenge struct {
	ID          int64
	Email       string
	TokenHash   string
	CodeHash    string
	BrowserHash string
	Attempts    int
	NextPath    string
	ExpiresAt   time.Time
	UsedAt      time.Time
}

const challengeCols = `id, email, token_hash, code_hash, browser_hash, attempts, next_path, expires_at, used_at`

func scanChallenge(row *sql.Row) (*Challenge, error) {
	c := &Challenge{}
	err := row.Scan(&c.ID, &c.Email, &c.TokenHash, &c.CodeHash, &c.BrowserHash, &c.Attempts,
		str{&c.NextPath}, ts{&c.ExpiresAt}, ts{&c.UsedAt})
	if err != nil {
		return nil, notFound(err)
	}
	return c, nil
}

func (s *Store) CreateChallenge(ctx context.Context, c *Challenge) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `INSERT INTO login_challenges
		(email, token_hash, code_hash, browser_hash, next_path, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		c.Email, c.TokenHash, c.CodeHash, c.BrowserHash, nullable(c.NextPath), s.now(),
		c.ExpiresAt.UTC().Format(db.TimeFormat))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ChallengeByToken renvoie un défi non utilisé et non expiré.
func (s *Store) ChallengeByToken(ctx context.Context, tokenHash string) (*Challenge, error) {
	return scanChallenge(s.DB.QueryRowContext(ctx, `SELECT `+challengeCols+` FROM login_challenges
		WHERE token_hash = ? AND used_at IS NULL AND expires_at > ?`, tokenHash, s.now()))
}

// LatestChallengeForBrowser renvoie le dernier défi actif demandé depuis ce navigateur.
func (s *Store) LatestChallengeForBrowser(ctx context.Context, browserHash string) (*Challenge, error) {
	return scanChallenge(s.DB.QueryRowContext(ctx, `SELECT `+challengeCols+` FROM login_challenges
		WHERE browser_hash = ? AND used_at IS NULL AND expires_at > ?
		ORDER BY id DESC LIMIT 1`, browserHash, s.now()))
}

func (s *Store) IncrementChallengeAttempts(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE login_challenges SET attempts = attempts + 1 WHERE id = ?`, id)
	return err
}

// ConsumeChallenge marque le défi comme utilisé. Renvoie ErrNotFound s'il l'était déjà
// (protection contre la double utilisation concurrente).
func (s *Store) ConsumeChallenge(ctx context.Context, id int64) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE login_challenges SET used_at = ? WHERE id = ? AND used_at IS NULL`, s.now(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// PurgeExpired supprime sessions et défis expirés depuis plus d'un jour.
func (s *Store) PurgeExpired(ctx context.Context) error {
	limit := s.Now().Add(-24 * time.Hour).UTC().Format(db.TimeFormat)
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < ?`, limit); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `DELETE FROM login_challenges WHERE expires_at < ?`, limit)
	return err
}
