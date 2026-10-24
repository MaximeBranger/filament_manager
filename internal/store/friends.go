package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var (
	ErrInvitationInvalid = errors.New("invitation expirée, révoquée ou déjà utilisée")
	ErrSelfInvite        = errors.New("impossible d'accepter sa propre invitation")
	ErrAlreadyFriends    = errors.New("déjà amis")
	ErrNotFriends        = errors.New("pas amis")
	ErrActiveLoan        = errors.New("un prêt est en cours")
)

type Invitation struct {
	ID          int64
	InviterID   int64
	InviterName string
	CreatedAt   time.Time
	ExpiresAt   time.Time
}

type Friend struct {
	ID          int64
	DisplayName string
	Since       time.Time
}

// friendIDs est une sous-requête renvoyant les amis de l'utilisateur (3 paramètres : son id).
const friendIDs = `SELECT CASE WHEN user_a_id = ? THEN user_b_id ELSE user_a_id END
	FROM friendships WHERE user_a_id = ? OR user_b_id = ?`

func pair(a, b int64) (int64, int64) {
	if a < b {
		return a, b
	}
	return b, a
}

func (s *Store) CreateInvitation(ctx context.Context, inviterID int64, tokenHash string, expiresAt time.Time) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO friend_invitations (inviter_id, token_hash, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		inviterID, tokenHash, s.now(), expiresAt.UTC().Format(timeFormat))
	return err
}

const validInvitation = `i.used_at IS NULL AND i.revoked_at IS NULL AND i.expires_at > ?`

// InvitationByToken renvoie une invitation encore utilisable.
func (s *Store) InvitationByToken(ctx context.Context, tokenHash string) (*Invitation, error) {
	inv := &Invitation{}
	err := s.DB.QueryRowContext(ctx, `SELECT i.id, i.inviter_id, u.display_name, i.created_at, i.expires_at
		FROM friend_invitations i JOIN users u ON u.id = i.inviter_id
		WHERE i.token_hash = ? AND `+validInvitation, tokenHash, s.now()).
		Scan(&inv.ID, &inv.InviterID, &inv.InviterName, ts{&inv.CreatedAt}, ts{&inv.ExpiresAt})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInvitationInvalid
	}
	return inv, err
}

// ListInvitations renvoie les invitations encore utilisables créées par l'utilisateur.
func (s *Store) ListInvitations(ctx context.Context, userID int64) ([]Invitation, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT i.id, i.inviter_id, '', i.created_at, i.expires_at
		FROM friend_invitations i WHERE i.inviter_id = ? AND `+validInvitation+` ORDER BY i.id DESC`, userID, s.now())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Invitation
	for rows.Next() {
		var inv Invitation
		if err := rows.Scan(&inv.ID, &inv.InviterID, &inv.InviterName, ts{&inv.CreatedAt}, ts{&inv.ExpiresAt}); err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

func (s *Store) RevokeInvitation(ctx context.Context, userID, id int64) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE friend_invitations SET revoked_at = ?
		WHERE id = ? AND inviter_id = ? AND used_at IS NULL AND revoked_at IS NULL`, s.now(), id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// AcceptInvitation crée l'amitié et consomme l'invitation. Renvoie la notification destinée
// à l'invitant.
func (s *Store) AcceptInvitation(ctx context.Context, tokenHash string, userID int64) (*Invitation, []Notification, error) {
	var inv *Invitation
	var notifs []Notification
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		inv = &Invitation{}
		err := tx.QueryRowContext(ctx, `SELECT i.id, i.inviter_id, u.display_name FROM friend_invitations i
			JOIN users u ON u.id = i.inviter_id WHERE i.token_hash = ? AND `+validInvitation, tokenHash, s.now()).
			Scan(&inv.ID, &inv.InviterID, &inv.InviterName)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrInvitationInvalid
		}
		if err != nil {
			return err
		}
		if inv.InviterID == userID {
			return ErrSelfInvite
		}
		a, b := pair(inv.InviterID, userID)
		res, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO friendships (user_a_id, user_b_id, created_at) VALUES (?, ?, ?)`, a, b, s.now())
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrAlreadyFriends
		}
		if _, err := tx.ExecContext(ctx, `UPDATE friend_invitations SET used_at = ?, used_by = ? WHERE id = ?`, s.now(), userID, inv.ID); err != nil {
			return err
		}
		var name string
		if err := tx.QueryRowContext(ctx, `SELECT display_name FROM users WHERE id = ?`, userID).Scan(&name); err != nil {
			return err
		}
		n, err := s.notify(ctx, tx, inv.InviterID, NotifInviteAccepted, NotificationPayload{Actor: name})
		notifs = append(notifs, n)
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	return inv, notifs, nil
}

func areFriends(ctx context.Context, q querier, a, b int64) (bool, error) {
	x, y := pair(a, b)
	var one int
	err := q.QueryRowContext(ctx, `SELECT 1 FROM friendships WHERE user_a_id = ? AND user_b_id = ?`, x, y).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (s *Store) AreFriends(ctx context.Context, a, b int64) (bool, error) {
	return areFriends(ctx, s.DB, a, b)
}

func (s *Store) ListFriends(ctx context.Context, userID int64) ([]Friend, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT u.id, u.display_name, f.created_at FROM friendships f
		JOIN users u ON u.id = CASE WHEN f.user_a_id = ? THEN f.user_b_id ELSE f.user_a_id END
		WHERE f.user_a_id = ? OR f.user_b_id = ?
		ORDER BY u.display_name COLLATE NOCASE`, userID, userID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Friend
	for rows.Next() {
		var f Friend
		if err := rows.Scan(&f.ID, &f.DisplayName, ts{&f.Since}); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// RemoveFriend coupe l'amitié dans les deux sens. Refusé si un prêt est en cours entre eux ;
// les demandes en attente sont annulées.
func (s *Store) RemoveFriend(ctx context.Context, userID, friendID int64) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		ok, err := areFriends(ctx, tx, userID, friendID)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM loans WHERE ended_at IS NULL
			AND ((lender_id = ? AND borrower_id = ?) OR (lender_id = ? AND borrower_id = ?))`,
			userID, friendID, friendID, userID).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return ErrActiveLoan
		}
		if _, err := tx.ExecContext(ctx, `UPDATE transfers SET status = 'cancelled', decided_at = ?
			WHERE status = 'pending' AND ((owner_id = ? AND other_id = ?) OR (owner_id = ? AND other_id = ?))`,
			s.now(), userID, friendID, friendID, userID); err != nil {
			return err
		}
		a, b := pair(userID, friendID)
		_, err = tx.ExecContext(ctx, `DELETE FROM friendships WHERE user_a_id = ? AND user_b_id = ?`, a, b)
		return err
	})
}

// ListFriendSpools renvoie les bobines visibles des amis du lecteur (ownerID = 0 : tous ses amis).
// Les bobines vides ou masquées sont exclues et les champs privés effacés.
func (s *Store) ListFriendSpools(ctx context.Context, viewerID, ownerID int64) ([]Spool, error) {
	where := `s.user_id IN (` + friendIDs + `) AND s.level != 'empty' AND s.hidden_from_friends = 0`
	args := []any{viewerID, viewerID, viewerID}
	if ownerID != 0 {
		where += ` AND s.user_id = ?`
		args = append(args, ownerID)
	}
	spools, err := s.listSpools(ctx, where, args...)
	for i := range spools {
		spools[i].StripPrivate()
	}
	return spools, err
}

// ListBorrowedSpools renvoie les bobines que l'utilisateur emprunte actuellement.
func (s *Store) ListBorrowedSpools(ctx context.Context, borrowerID int64) ([]Spool, error) {
	spools, err := s.listSpools(ctx, `l.borrower_id = ?`, borrowerID)
	for i := range spools {
		spools[i].StripPrivate()
	}
	return spools, err
}
