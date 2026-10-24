package web

import (
	"context"
	"fmt"
	"strconv"
	"time"

	mailer "github.com/MaximeBranger/filament_manager/internal/mail"
	"github.com/MaximeBranger/filament_manager/internal/store"
)

// what décrit l'objet d'une notification : le libellé de la bobine, ou un nombre de bobines.
func what(p store.NotificationPayload) string {
	if p.Count == 1 && p.Label != "" {
		return p.Label
	}
	if p.Count <= 1 {
		return "1 bobine"
	}
	return strconv.Itoa(p.Count) + " bobines"
}

// notificationText rédige le message d'une notification et le lien associé.
func notificationText(n store.Notification) (text, link string) {
	p := n.Payload
	switch n.Kind {
	case store.NotifInviteAccepted:
		return p.Actor + " a accepté votre invitation : vous partagez désormais vos stocks.", "/friends"
	case store.NotifLoanRequested:
		return p.Actor + " demande à vous emprunter " + what(p) + ".", "/transfers"
	case store.NotifGiftRequested:
		return p.Actor + " aimerait que vous lui donniez " + what(p) + ".", "/transfers"
	case store.NotifGiftOffered:
		return p.Actor + " vous propose de vous donner " + what(p) + ".", "/transfers"
	case store.NotifTransferAccepted:
		switch {
		case p.Offer:
			return p.Actor + " a accepté votre don : " + what(p) + ".", "/transfers"
		case p.TransferKind == store.KindLoan:
			return p.Actor + " a accepté de vous prêter " + what(p) + ".", "/transfers"
		default:
			return p.Actor + " a accepté de vous donner " + what(p) + ".", "/transfers"
		}
	case store.NotifTransferRefused:
		switch {
		case p.Offer:
			return p.Actor + " a décliné votre proposition de don.", "/transfers"
		case p.TransferKind == store.KindLoan:
			return p.Actor + " ne peut pas vous prêter les bobines demandées.", "/transfers"
		default:
			return p.Actor + " a décliné votre demande de don.", "/transfers"
		}
	case store.NotifLoanRecorded:
		return p.Actor + " vous a prêté " + what(p) + ".", "/transfers"
	case store.NotifLoanReturned:
		return p.Actor + " a enregistré le retour de " + what(p) + ".", "/transfers"
	case store.NotifLoanConvertedGift:
		return p.Actor + " vous donne définitivement " + what(p) + ".", "/stock"
	case store.NotifShareChoice:
		return p.Actor + " a choisi « " + p.Label + " » pour « " + p.Title + " ».", "/shares"
	case store.NotifShareLocked:
		return "Le partage « " + p.Title + " » est verrouillé après trop de mots de passe erronés : définissez-en un nouveau pour le rouvrir.", "/shares"
	}
	return "Nouvelle activité.", "/notifications"
}

// emailable indique si la notification mérite un email (le retour d'un prêt, non).
func emailable(kind string) bool { return kind != store.NotifLoanReturned }

// dispatch envoie les emails des notifications, en arrière-plan sauf en test.
func (s *Server) dispatch(notifs []store.Notification) {
	for _, n := range notifs {
		if !emailable(n.Kind) {
			continue
		}
		if s.syncMail {
			s.emailNotification(n)
		} else {
			go s.emailNotification(n)
		}
	}
}

// sendSecurityEmail prévient d'un changement sensible du compte, même si l'utilisateur a
// désactivé les notifications. En arrière-plan sauf en test.
func (s *Server) sendSecurityEmail(to, subject, text string) {
	send := func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := s.mailer.Send(ctx, mailer.Message{To: to, Subject: subject, Text: text}); err != nil {
			s.log.Error("email de sécurité non envoyé", "err", err)
		}
	}
	if s.syncMail {
		send()
	} else {
		go send()
	}
}

func (s *Server) emailNotification(n store.Notification) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	user, err := s.store.UserByID(ctx, n.UserID)
	if err != nil || !user.EmailNotifications {
		return
	}
	text, link := notificationText(n)
	msg := mailer.Message{
		To:      user.Email,
		Subject: "FilamentManager : " + text,
		Text: fmt.Sprintf(`Bonjour %s,

%s

%s%s

--
FilamentManager
Pour ne plus recevoir ces emails : %s/account
`, user.DisplayName, text, s.cfg.BaseURL.String(), link, s.cfg.BaseURL.String()),
	}
	for attempt := 1; ; attempt++ {
		sendCtx, cancelSend := context.WithTimeout(ctx, 30*time.Second)
		err = s.mailer.Send(sendCtx, msg)
		cancelSend()
		if err == nil {
			return
		}
		if attempt == 3 {
			s.log.Error("email de notification non envoyé", "kind", n.Kind, "user_id", n.UserID, "err", err)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Duration(attempt*attempt) * 5 * time.Second):
		}
	}
}
