package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/MaximeBranger/filament_manager/internal/catalog"
	"github.com/MaximeBranger/filament_manager/internal/store"
)

const (
	historyLimit    = 20
	maxMessageRunes = 1000
)

func formIDs(values []string) []int64 {
	var ids []int64
	for _, v := range values {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil && id > 0 {
			ids = append(ids, id)
		}
	}
	return ids
}

// formLevels lit les champs level_<id> d'un formulaire.
func formLevels(form url.Values) map[int64]string {
	levels := map[int64]string{}
	for k, v := range form {
		raw, ok := strings.CutPrefix(k, "level_")
		if !ok || len(v) == 0 || !catalog.ValidLevel(v[0]) {
			continue
		}
		if id, err := strconv.ParseInt(raw, 10, 64); err == nil {
			levels[id] = v[0]
		}
	}
	return levels
}

func (s *Server) transfersPage(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	ctx := r.Context()
	transfers, err := s.store.ListTransfers(ctx, user.ID, 100)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	loans, err := s.store.ListLoans(ctx, user.ID, 200)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	gifts, err := s.store.ListGifts(ctx, user.ID, historyLimit)
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	var received, sent, past []store.Transfer
	for _, t := range transfers {
		switch {
		case t.Status == store.StatusPending && t.DeciderID() == user.ID:
			received = append(received, t)
		case t.Status == store.StatusPending:
			sent = append(sent, t)
		case len(past) < historyLimit:
			past = append(past, t)
		}
	}
	var lent, borrowed, ended []store.Loan
	for _, l := range loans {
		switch {
		case l.Active() && l.LenderID == user.ID:
			lent = append(lent, l)
		case l.Active():
			borrowed = append(borrowed, l)
		case len(ended) < historyLimit:
			ended = append(ended, l)
		}
	}
	s.render(w, r, http.StatusOK, "transfers", map[string]any{
		"Title": "Prêts & dons", "Received": received, "Sent": sent, "Past": past,
		"Lent": lent, "Borrowed": borrowed, "Ended": ended, "Gifts": gifts,
	})
}

// transferNew affiche le formulaire de demande pour des bobines choisies chez un ami.
func (s *Server) transferNew(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	q := r.URL.Query()
	kind := q.Get("kind")
	if kind != store.KindGift {
		kind = store.KindLoan
	}
	ownerID, _ := strconv.ParseInt(q.Get("to"), 10, 64)
	wanted := formIDs(q["spool"])
	visible, err := s.store.ListFriendSpools(r.Context(), user.ID, ownerID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	var spools []store.Spool
	for _, sp := range visible {
		if slices.Contains(wanted, sp.ID) && sp.Loan == nil {
			spools = append(spools, sp)
		}
	}
	if ownerID == 0 || len(spools) == 0 {
		s.setFlash(w, "Sélectionnez au moins une bobine disponible chez un ami.")
		http.Redirect(w, r, "/stock?scope="+strconv.FormatInt(ownerID, 10), http.StatusSeeOther)
		return
	}
	title := "Demander un prêt"
	if kind == store.KindGift {
		title = "Demander un don"
	}
	s.render(w, r, http.StatusOK, "transfer_new", map[string]any{
		"Title": title, "Kind": kind, "OwnerID": ownerID, "OwnerName": spools[0].OwnerName, "Spools": spools,
	})
}

// transferCreate enregistre une demande (bobines d'un ami) ou une proposition (ses bobines).
func (s *Server) transferCreate(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Requête invalide.", http.StatusBadRequest)
		return
	}
	f := r.PostForm
	counterpart, _ := strconv.ParseInt(f.Get("to"), 10, 64)
	message := strings.TrimSpace(f.Get("message"))
	if len([]rune(message)) > maxMessageRunes {
		message = string([]rune(message)[:maxMessageRunes])
	}
	grams, _ := strconv.ParseInt(strings.TrimSpace(f.Get("grams")), 10, 64)
	if grams < 0 || grams > 100000 {
		grams = 0
	}
	in := store.NewTransfer{
		Kind:          f.Get("kind"),
		InitiatorID:   user.ID,
		CounterpartID: counterpart,
		SpoolIDs:      formIDs(f["spool"]),
		Message:       message,
		GramsNeeded:   grams,
		Levels:        formLevels(f),
	}
	_, notifs, err := s.store.CreateTransfer(r.Context(), in)
	switch {
	case errors.Is(err, store.ErrNotFriends), errors.Is(err, store.ErrSpoolUnavailable):
		s.setFlash(w, "Impossible d'enregistrer : une des bobines n'est plus disponible, ou vous n'êtes pas amis.")
		http.Redirect(w, r, "/transfers", http.StatusSeeOther)
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	}
	s.dispatch(notifs)
	switch notifs[0].Kind {
	case store.NotifLoanRecorded:
		s.setFlash(w, "Prêt enregistré.")
	case store.NotifGiftOffered:
		s.setFlash(w, "Proposition de don envoyée.")
	default:
		s.setFlash(w, "Demande envoyée.")
	}
	http.Redirect(w, r, "/transfers", http.StatusSeeOther)
}

func (s *Server) transferDecide(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	r.ParseForm()
	var accepted []int64
	if r.PostForm.Get("action") != "refuse" {
		accepted = formIDs(r.PostForm["accept"])
	}
	notifs, err := s.store.Decide(r.Context(), id, currentUser(r).ID, accepted, formLevels(r.PostForm))
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.dispatch(notifs)
	if notifs[0].Kind == store.NotifTransferAccepted {
		s.setFlash(w, "C'est enregistré.")
	} else {
		s.setFlash(w, "Demande refusée.")
	}
	http.Redirect(w, r, "/transfers", http.StatusSeeOther)
}

func (s *Server) transferCancel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	if err := s.store.CancelTransfer(r.Context(), id, currentUser(r).ID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.notFound(w, r)
			return
		}
		s.serverError(w, r, err)
		return
	}
	s.setFlash(w, "Demande annulée.")
	http.Redirect(w, r, "/transfers", http.StatusSeeOther)
}

func (s *Server) loanReturn(w http.ResponseWriter, r *http.Request) {
	s.endLoan(w, r, s.store.ReturnLoan, "Retour enregistré.")
}

func (s *Server) loanGift(w http.ResponseWriter, r *http.Request) {
	s.endLoan(w, r, s.store.ConvertLoanToGift, "La bobine a été donnée.")
}

func (s *Server) endLoan(w http.ResponseWriter, r *http.Request,
	op func(ctx context.Context, loanID, lenderID int64, level string) ([]store.Notification, error), flash string) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	notifs, err := op(r.Context(), id, currentUser(r).ID, r.FormValue("level"))
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.dispatch(notifs)
	s.setFlash(w, flash)
	http.Redirect(w, r, "/transfers", http.StatusSeeOther)
}

func (s *Server) notificationsPage(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	notifs, err := s.store.ListNotifications(r.Context(), user.ID, 50)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	type entry struct {
		store.Notification
		Text, Link string
	}
	entries := make([]entry, 0, len(notifs))
	for _, n := range notifs {
		text, link := notificationText(n)
		entries = append(entries, entry{n, text, link})
	}
	if err := s.store.MarkNotificationsRead(r.Context(), user.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "notifications", map[string]any{"Title": "Notifications", "Entries": entries})
}

func (s *Server) accountNotifications(w http.ResponseWriter, r *http.Request) {
	enabled := r.FormValue("email_notifications") != ""
	if err := s.store.SetEmailNotifications(r.Context(), currentUser(r).ID, enabled); err != nil {
		s.serverError(w, r, err)
		return
	}
	if enabled {
		s.setFlash(w, "Vous recevrez les notifications par email.")
	} else {
		s.setFlash(w, "Vous ne recevrez plus de notifications par email.")
	}
	http.Redirect(w, r, "/account", http.StatusSeeOther)
}
