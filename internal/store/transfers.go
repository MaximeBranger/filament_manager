package store

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/MaximeBranger/filament_manager/internal/catalog"
)

const (
	KindLoan = "loan"
	KindGift = "gift"

	InitiatedByOwner = "owner"
	InitiatedByOther = "other"

	StatusPending   = "pending"
	StatusAccepted  = "accepted"
	StatusRefused   = "refused"
	StatusCancelled = "cancelled"

	MaxTransferSpools = 50
)

var ErrSpoolUnavailable = errors.New("une des bobines n'est pas disponible")

type TransferItem struct {
	SpoolID  int64
	Decision string
	Label    string
	Hex      string
	Level    string
}

type Transfer struct {
	ID          int64
	Kind        string
	OwnerID     int64
	OwnerName   string
	OtherID     int64
	OtherName   string
	InitiatedBy string
	Status      string
	Message     string
	GramsNeeded int64
	CreatedAt   time.Time
	DecidedAt   time.Time
	Items       []TransferItem
}

// InitiatorID renvoie l'utilisateur à l'origine de la transaction.
func (t Transfer) InitiatorID() int64 {
	if t.InitiatedBy == InitiatedByOwner {
		return t.OwnerID
	}
	return t.OtherID
}

// DeciderID renvoie l'utilisateur qui doit accepter ou refuser.
func (t Transfer) DeciderID() int64 {
	if t.InitiatedBy == InitiatedByOwner {
		return t.OtherID
	}
	return t.OwnerID
}

// Hex renvoie la couleur du produit, ou celle de sa famille à défaut.
func (p ProductFields) Hex() string {
	if p.ColorHex != "" {
		return p.ColorHex
	}
	if f, ok := catalog.FamilyByCode(p.ColorFamily); ok {
		return f.Hex
	}
	return "#9e9e9e"
}

// NewTransfer décrit une demande (bobines d'un ami) ou une proposition (ses propres bobines).
type NewTransfer struct {
	Kind          string
	InitiatorID   int64
	CounterpartID int64
	SpoolIDs      []int64
	Message       string
	GramsNeeded   int64
	Levels        map[int64]string // niveaux déclarés par le propriétaire (dons)
}

type spoolState struct {
	ownerID int64
	level   string
	hidden  bool
	lent    bool
	label   string
}

func spoolStates(ctx context.Context, q querier, ids []int64) (map[int64]spoolState, error) {
	out := map[int64]spoolState{}
	for _, id := range ids {
		var st spoolState
		var p Product
		err := q.QueryRowContext(ctx, `SELECT s.user_id, s.level, s.hidden_from_friends,
			EXISTS (SELECT 1 FROM loans WHERE spool_id = s.id AND ended_at IS NULL), `+productCols+`
			FROM spools s JOIN products p ON p.id = s.product_id WHERE s.id = ?`, id).
			Scan(append([]any{&st.ownerID, &st.level, &st.hidden, &st.lent}, productDest(&p)...)...)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrSpoolUnavailable
		}
		if err != nil {
			return nil, err
		}
		st.label = p.Label()
		out[id] = st
	}
	return out, nil
}

func displayName(ctx context.Context, q querier, userID int64) (string, error) {
	var name string
	err := q.QueryRowContext(ctx, `SELECT display_name FROM users WHERE id = ?`, userID).Scan(&name)
	return name, notFound(err)
}

func uniqueIDs(ids []int64) []int64 {
	out := slices.Clone(ids)
	slices.Sort(out)
	return slices.Compact(out)
}

// CreateTransfer enregistre une demande ou une proposition. Un prêt proposé par le propriétaire
// est un prêt direct : il prend effet immédiatement.
func (s *Store) CreateTransfer(ctx context.Context, in NewTransfer) (int64, []Notification, error) {
	ids := uniqueIDs(in.SpoolIDs)
	if len(ids) == 0 || len(ids) > MaxTransferSpools || (in.Kind != KindLoan && in.Kind != KindGift) {
		return 0, nil, ErrSpoolUnavailable
	}
	var transferID int64
	var notifs []Notification
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		ok, err := areFriends(ctx, tx, in.InitiatorID, in.CounterpartID)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFriends
		}
		states, err := spoolStates(ctx, tx, ids)
		if err != nil {
			return err
		}

		initiatedBy := InitiatedByOther
		ownerID, otherID := in.CounterpartID, in.InitiatorID
		if states[ids[0]].ownerID == in.InitiatorID {
			initiatedBy = InitiatedByOwner
			ownerID, otherID = in.InitiatorID, in.CounterpartID
		}
		for _, id := range ids {
			st := states[id]
			if st.ownerID != ownerID || st.lent {
				return ErrSpoolUnavailable
			}
			// Un ami ne peut demander que ce qu'il voit.
			if initiatedBy == InitiatedByOther && (st.hidden || st.level == "empty") {
				return ErrSpoolUnavailable
			}
		}

		if initiatedBy == InitiatedByOwner && in.Kind == KindGift {
			for _, id := range ids {
				if lvl, ok := in.Levels[id]; ok && catalog.ValidLevel(lvl) {
					if _, err := tx.ExecContext(ctx, `UPDATE spools SET level = ?, updated_at = ? WHERE id = ?`, lvl, s.now(), id); err != nil {
						return err
					}
				}
			}
		}

		directLoan := initiatedBy == InitiatedByOwner && in.Kind == KindLoan
		status, decidedAt, decision := StatusPending, any(nil), any(nil)
		if directLoan {
			status, decidedAt, decision = StatusAccepted, s.now(), StatusAccepted
		}
		var grams any
		if in.GramsNeeded > 0 {
			grams = in.GramsNeeded
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO transfers (kind, owner_id, other_id, initiated_by, status, message,
			grams_needed, created_at, decided_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			in.Kind, ownerID, otherID, initiatedBy, status, nullable(strings.TrimSpace(in.Message)), grams, s.now(), decidedAt)
		if err != nil {
			return err
		}
		transferID, _ = res.LastInsertId()
		for _, id := range ids {
			if _, err := tx.ExecContext(ctx, `INSERT INTO transfer_items (transfer_id, spool_id, decision) VALUES (?, ?, ?)`,
				transferID, id, decision); err != nil {
				return err
			}
			if directLoan {
				if err := s.startLoan(ctx, tx, id, ownerID, otherID, transferID); err != nil {
					return err
				}
			}
		}

		actor, err := displayName(ctx, tx, in.InitiatorID)
		if err != nil {
			return err
		}
		payload := NotificationPayload{Actor: actor, TransferKind: in.Kind, Count: len(ids)}
		if len(ids) == 1 {
			payload.Label = states[ids[0]].label
		}
		var kind string
		switch {
		case directLoan:
			kind = NotifLoanRecorded
		case initiatedBy == InitiatedByOwner:
			kind = NotifGiftOffered
		case in.Kind == KindLoan:
			kind = NotifLoanRequested
		default:
			kind = NotifGiftRequested
		}
		n, err := s.notify(ctx, tx, in.CounterpartID, kind, payload)
		notifs = append(notifs, n)
		return err
	})
	return transferID, notifs, err
}

func (s *Store) startLoan(ctx context.Context, tx *sql.Tx, spoolID, lenderID, borrowerID, transferID int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO loans (spool_id, lender_id, borrower_id, transfer_id, started_at) VALUES (?, ?, ?, ?, ?)`,
		spoolID, lenderID, borrowerID, transferID, s.now())
	if err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return ErrSpoolUnavailable
	}
	return err
}

// transferOwnership donne une bobine : elle rejoint un produit du receveur (même EAN) ou une copie
// du produit, et perd les informations privées du donneur.
func (s *Store) transferOwnership(ctx context.Context, tx *sql.Tx, spoolID, giverID, receiverID int64, level string) (string, error) {
	var p Product
	var current string
	err := tx.QueryRowContext(ctx, `SELECT s.level, `+productCols+` FROM spools s JOIN products p ON p.id = s.product_id
		WHERE s.id = ? AND s.user_id = ?`, spoolID, giverID).Scan(append([]any{&current}, productDest(&p)...)...)
	if err != nil {
		return "", notFound(err)
	}
	if !catalog.ValidLevel(level) {
		level = current
	}

	var target int64
	if p.EAN != "" {
		err := tx.QueryRowContext(ctx, `SELECT id FROM products WHERE user_id = ? AND ean = ?`, receiverID, p.EAN).Scan(&target)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
	}
	if target == 0 {
		if target, err = s.insertProduct(ctx, tx, receiverID, p.ProductFields); err != nil {
			return "", err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE spools SET user_id = ?, product_id = ?, level = ?, purchase_date = NULL,
		price_cents = NULL, supplier = NULL, notes = NULL, hidden_from_friends = 0, updated_at = ? WHERE id = ?`,
		receiverID, target, level, s.now(), spoolID); err != nil {
		return "", err
	}
	label := p.Label()
	_, err = tx.ExecContext(ctx, `INSERT INTO gifts (spool_id, giver_id, receiver_id, label, level, given_at) VALUES (?, ?, ?, ?, ?, ?)`,
		spoolID, giverID, receiverID, label, level, s.now())
	return label, err
}

func (s *Store) transferByID(ctx context.Context, q querier, id int64) (*Transfer, error) {
	t := &Transfer{}
	var grams sql.NullInt64
	err := q.QueryRowContext(ctx, `SELECT t.id, t.kind, t.owner_id, ou.display_name, t.other_id, xu.display_name,
		t.initiated_by, t.status, t.message, t.grams_needed, t.created_at, t.decided_at
		FROM transfers t JOIN users ou ON ou.id = t.owner_id JOIN users xu ON xu.id = t.other_id
		WHERE t.id = ?`, id).Scan(&t.ID, &t.Kind, &t.OwnerID, &t.OwnerName, &t.OtherID, &t.OtherName,
		&t.InitiatedBy, &t.Status, str{&t.Message}, &grams, ts{&t.CreatedAt}, ts{&t.DecidedAt})
	if err != nil {
		return nil, notFound(err)
	}
	t.GramsNeeded = grams.Int64
	return t, nil
}

// Decide accepte (tout ou partie) ou refuse une transaction en attente. accepted liste les
// bobines acceptées ; levels porte les niveaux déclarés par le propriétaire pour un don.
func (s *Store) Decide(ctx context.Context, transferID, deciderID int64, accepted []int64, levels map[int64]string) ([]Notification, error) {
	var notifs []Notification
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		t, err := s.transferByID(ctx, tx, transferID)
		if err != nil {
			return err
		}
		if t.Status != StatusPending || t.DeciderID() != deciderID {
			return ErrNotFound
		}
		rows, err := tx.QueryContext(ctx, `SELECT spool_id FROM transfer_items WHERE transfer_id = ?`, transferID)
		if err != nil {
			return err
		}
		var ids []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		rows.Close()

		count := 0
		label := ""
		for _, id := range ids {
			decision := StatusRefused
			if slices.Contains(accepted, id) {
				states, err := spoolStates(ctx, tx, []int64{id})
				if err == nil && states[id].ownerID == t.OwnerID && !states[id].lent {
					label = states[id].label
					switch t.Kind {
					case KindLoan:
						err = s.startLoan(ctx, tx, id, t.OwnerID, t.OtherID, t.ID)
					case KindGift:
						level := ""
						if deciderID == t.OwnerID {
							level = levels[id]
						}
						_, err = s.transferOwnership(ctx, tx, id, t.OwnerID, t.OtherID, level)
					}
					if err != nil {
						return err
					}
					decision = StatusAccepted
					count++
				}
			}
			if _, err := tx.ExecContext(ctx, `UPDATE transfer_items SET decision = ? WHERE transfer_id = ? AND spool_id = ?`,
				decision, transferID, id); err != nil {
				return err
			}
		}

		status, kind := StatusRefused, NotifTransferRefused
		if count > 0 {
			status, kind = StatusAccepted, NotifTransferAccepted
		}
		if _, err := tx.ExecContext(ctx, `UPDATE transfers SET status = ?, decided_at = ? WHERE id = ?`, status, s.now(), transferID); err != nil {
			return err
		}
		actor, err := displayName(ctx, tx, deciderID)
		if err != nil {
			return err
		}
		payload := NotificationPayload{Actor: actor, TransferKind: t.Kind, Count: count, Offer: t.InitiatedBy == InitiatedByOwner}
		if count == 1 {
			payload.Label = label
		}
		n, err := s.notify(ctx, tx, t.InitiatorID(), kind, payload)
		notifs = append(notifs, n)
		return err
	})
	return notifs, err
}

// CancelTransfer annule une transaction en attente, à l'initiative de son auteur.
func (s *Store) CancelTransfer(ctx context.Context, transferID, userID int64) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE transfers SET status = 'cancelled', decided_at = ?
		WHERE id = ? AND status = 'pending'
		AND ((initiated_by = 'owner' AND owner_id = ?) OR (initiated_by = 'other' AND other_id = ?))`,
		s.now(), transferID, userID, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// endLoan clôt un prêt actif du prêteur et renvoie la bobine et l'emprunteur.
func (s *Store) endLoan(ctx context.Context, tx *sql.Tx, loanID, lenderID int64, reason string) (spoolID, borrowerID int64, err error) {
	err = tx.QueryRowContext(ctx, `SELECT spool_id, borrower_id FROM loans WHERE id = ? AND lender_id = ? AND ended_at IS NULL`,
		loanID, lenderID).Scan(&spoolID, &borrowerID)
	if err != nil {
		return 0, 0, notFound(err)
	}
	_, err = tx.ExecContext(ctx, `UPDATE loans SET ended_at = ?, end_reason = ? WHERE id = ?`, s.now(), reason, loanID)
	return spoolID, borrowerID, err
}

// ReturnLoan enregistre le retour d'une bobine ; le prêteur indique son nouveau niveau.
func (s *Store) ReturnLoan(ctx context.Context, loanID, lenderID int64, level string) ([]Notification, error) {
	if !catalog.ValidLevel(level) {
		return nil, ErrNotFound
	}
	var notifs []Notification
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		spoolID, borrowerID, err := s.endLoan(ctx, tx, loanID, lenderID, "returned")
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE spools SET level = ?, updated_at = ? WHERE id = ?`, level, s.now(), spoolID); err != nil {
			return err
		}
		states, err := spoolStates(ctx, tx, []int64{spoolID})
		if err != nil {
			return err
		}
		actor, err := displayName(ctx, tx, lenderID)
		if err != nil {
			return err
		}
		n, err := s.notify(ctx, tx, borrowerID, NotifLoanReturned, NotificationPayload{Actor: actor, Count: 1, Label: states[spoolID].label})
		notifs = append(notifs, n)
		return err
	})
	return notifs, err
}

// ConvertLoanToGift transforme un prêt en don (« garde-la ») : effet immédiat.
func (s *Store) ConvertLoanToGift(ctx context.Context, loanID, lenderID int64, level string) ([]Notification, error) {
	var notifs []Notification
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		spoolID, borrowerID, err := s.endLoan(ctx, tx, loanID, lenderID, "gifted")
		if err != nil {
			return err
		}
		label, err := s.transferOwnership(ctx, tx, spoolID, lenderID, borrowerID, level)
		if err != nil {
			return err
		}
		actor, err := displayName(ctx, tx, lenderID)
		if err != nil {
			return err
		}
		n, err := s.notify(ctx, tx, borrowerID, NotifLoanConvertedGift, NotificationPayload{Actor: actor, Count: 1, Label: label})
		notifs = append(notifs, n)
		return err
	})
	return notifs, err
}

// Listes -----------------------------------------------------------------------------------

// ListTransfers renvoie les transactions de l'utilisateur, les plus récentes d'abord.
func (s *Store) ListTransfers(ctx context.Context, userID int64, limit int) ([]Transfer, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM transfers WHERE owner_id = ? OR other_id = ? ORDER BY id DESC LIMIT ?`,
		userID, userID, limit)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()

	out := make([]Transfer, 0, len(ids))
	for _, id := range ids {
		t, err := s.transferByID(ctx, s.DB, id)
		if err != nil {
			return nil, err
		}
		items, err := s.DB.QueryContext(ctx, `SELECT ti.spool_id, ti.decision, s.level, `+productCols+`
			FROM transfer_items ti JOIN spools s ON s.id = ti.spool_id JOIN products p ON p.id = s.product_id
			WHERE ti.transfer_id = ? ORDER BY ti.spool_id`, id)
		if err != nil {
			return nil, err
		}
		for items.Next() {
			var it TransferItem
			var p Product
			if err := items.Scan(append([]any{&it.SpoolID, str{&it.Decision}, &it.Level}, productDest(&p)...)...); err != nil {
				items.Close()
				return nil, err
			}
			it.Label, it.Hex = p.Label(), p.Hex()
			t.Items = append(t.Items, it)
		}
		items.Close()
		out = append(out, *t)
	}
	return out, nil
}

type Loan struct {
	ID           int64
	SpoolID      int64
	LenderID     int64
	LenderName   string
	BorrowerID   int64
	BorrowerName string
	StartedAt    time.Time
	EndedAt      time.Time
	EndReason    string
	Label        string
	Hex          string
	Level        string
}

func (l Loan) Active() bool { return l.EndedAt.IsZero() }

// ListLoans renvoie les prêts où l'utilisateur est prêteur ou emprunteur.
func (s *Store) ListLoans(ctx context.Context, userID int64, limit int) ([]Loan, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT l.id, l.spool_id, l.lender_id, lu.display_name, l.borrower_id, bu.display_name,
		l.started_at, l.ended_at, l.end_reason, s.level, `+productCols+`
		FROM loans l JOIN users lu ON lu.id = l.lender_id JOIN users bu ON bu.id = l.borrower_id
		JOIN spools s ON s.id = l.spool_id JOIN products p ON p.id = s.product_id
		WHERE l.lender_id = ? OR l.borrower_id = ?
		ORDER BY l.ended_at IS NOT NULL, l.id DESC LIMIT ?`, userID, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Loan
	for rows.Next() {
		var l Loan
		var p Product
		if err := rows.Scan(append([]any{&l.ID, &l.SpoolID, &l.LenderID, &l.LenderName, &l.BorrowerID, &l.BorrowerName,
			ts{&l.StartedAt}, ts{&l.EndedAt}, str{&l.EndReason}, &l.Level}, productDest(&p)...)...); err != nil {
			return nil, err
		}
		l.Label, l.Hex = p.Label(), p.Hex()
		out = append(out, l)
	}
	return out, rows.Err()
}

type Gift struct {
	ID           int64
	GiverID      int64
	GiverName    string
	ReceiverID   int64
	ReceiverName string
	Label        string
	Level        string
	GivenAt      time.Time
}

// ListGifts renvoie les dons faits ou reçus par l'utilisateur.
func (s *Store) ListGifts(ctx context.Context, userID int64, limit int) ([]Gift, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT g.id, COALESCE(g.giver_id, 0), COALESCE(gu.display_name, 'Compte supprimé'),
		COALESCE(g.receiver_id, 0), COALESCE(ru.display_name, 'Compte supprimé'), g.label, g.level, g.given_at
		FROM gifts g LEFT JOIN users gu ON gu.id = g.giver_id LEFT JOIN users ru ON ru.id = g.receiver_id
		WHERE g.giver_id = ? OR g.receiver_id = ? ORDER BY g.id DESC LIMIT ?`, userID, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Gift
	for rows.Next() {
		var g Gift
		if err := rows.Scan(&g.ID, &g.GiverID, &g.GiverName, &g.ReceiverID, &g.ReceiverName, &g.Label, &g.Level, ts{&g.GivenAt}); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
