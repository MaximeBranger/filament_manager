package store

import (
	"context"
	"encoding/json"
	"time"
)

// Types de notification.
const (
	NotifInviteAccepted    = "invite_accepted"
	NotifLoanRequested     = "loan_requested"
	NotifGiftRequested     = "gift_requested"
	NotifGiftOffered       = "gift_offered"
	NotifTransferAccepted  = "transfer_accepted"
	NotifTransferRefused   = "transfer_refused"
	NotifLoanRecorded      = "loan_recorded"
	NotifLoanReturned      = "loan_returned"
	NotifLoanConvertedGift = "loan_converted_to_gift"
)

// NotificationPayload contient de quoi rédiger le message sans relire la base.
type NotificationPayload struct {
	Actor        string `json:"actor"`
	TransferKind string `json:"transfer_kind,omitempty"`
	Offer        bool   `json:"offer,omitempty"` // la transaction était une proposition du propriétaire
	Count        int    `json:"count,omitempty"`
	Label        string `json:"label,omitempty"`
	Title        string `json:"title,omitempty"` // titre d'un partage de modèle 3D
}

type Notification struct {
	ID        int64
	UserID    int64
	Kind      string
	Payload   NotificationPayload
	CreatedAt time.Time
	ReadAt    time.Time
}

func (s *Store) notify(ctx context.Context, q querier, userID int64, kind string, p NotificationPayload) (Notification, error) {
	raw, _ := json.Marshal(p)
	now := s.Now()
	res, err := q.ExecContext(ctx, `INSERT INTO notifications (user_id, kind, payload, created_at) VALUES (?, ?, ?, ?)`,
		userID, kind, string(raw), now.UTC().Format(timeFormat))
	if err != nil {
		return Notification{}, err
	}
	id, _ := res.LastInsertId()
	return Notification{ID: id, UserID: userID, Kind: kind, Payload: p, CreatedAt: now}, nil
}

// UnreadCount renvoie le nombre de notifications non lues.
func (s *Store) UnreadCount(ctx context.Context, userID int64) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM notifications WHERE user_id = ? AND read_at IS NULL`, userID).Scan(&n)
	return n, err
}

// ListNotifications renvoie les dernières notifications, les plus récentes d'abord.
func (s *Store) ListNotifications(ctx context.Context, userID int64, limit int) ([]Notification, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, kind, payload, created_at, read_at FROM notifications
		WHERE user_id = ? ORDER BY id DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Notification
	for rows.Next() {
		n := Notification{UserID: userID}
		var raw string
		if err := rows.Scan(&n.ID, &n.Kind, &raw, ts{&n.CreatedAt}, ts{&n.ReadAt}); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(raw), &n.Payload)
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Store) MarkNotificationsRead(ctx context.Context, userID int64) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE notifications SET read_at = ? WHERE user_id = ? AND read_at IS NULL`, s.now(), userID)
	return err
}

func (s *Store) SetEmailNotifications(ctx context.Context, userID int64, enabled bool) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE users SET email_notifications = ? WHERE id = ?`, enabled, userID)
	return err
}
