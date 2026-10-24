package web

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/MaximeBranger/filament_manager/internal/auth"
	"github.com/MaximeBranger/filament_manager/internal/store"
)

const invitationTTL = 7 * 24 * time.Hour

func (s *Server) friendsPage(w http.ResponseWriter, r *http.Request) {
	s.renderFriends(w, r, "")
}

func (s *Server) renderFriends(w http.ResponseWriter, r *http.Request, newLink string) {
	user := currentUser(r)
	friends, err := s.store.ListFriends(r.Context(), user.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	invitations, err := s.store.ListInvitations(r.Context(), user.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "friends", map[string]any{
		"Title": "Mes amis", "Friends": friends, "Invitations": invitations, "NewLink": newLink,
	})
}

// invitationCreate affiche le lien une seule fois : seul son empreinte est conservée.
func (s *Server) invitationCreate(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	token := auth.NewToken()
	if err := s.store.CreateInvitation(r.Context(), user.ID, auth.HashToken(token), time.Now().Add(invitationTTL)); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.renderFriends(w, r, s.cfg.BaseURL.String()+"/invite/"+token)
}

func (s *Server) invitationRevoke(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	if err := s.store.RevokeInvitation(r.Context(), currentUser(r).ID, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.notFound(w, r)
			return
		}
		s.serverError(w, r, err)
		return
	}
	s.setFlash(w, "Invitation révoquée.")
	http.Redirect(w, r, "/friends", http.StatusSeeOther)
}

func (s *Server) friendRemove(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.notFound(w, r)
		return
	}
	switch err := s.store.RemoveFriend(r.Context(), currentUser(r).ID, id); {
	case errors.Is(err, store.ErrNotFound):
		s.notFound(w, r)
		return
	case errors.Is(err, store.ErrActiveLoan):
		s.setFlash(w, "Un prêt est en cours entre vous : clôturez-le avant de retirer cet ami.")
	case err != nil:
		s.serverError(w, r, err)
		return
	default:
		s.setFlash(w, "Ami retiré. Vous ne voyez plus vos stocks respectifs.")
	}
	http.Redirect(w, r, "/friends", http.StatusSeeOther)
}

func (s *Server) inviteInvalid(w http.ResponseWriter, r *http.Request) {
	s.errorPage(w, r, http.StatusGone, "Invitation invalide",
		"Ce lien d'invitation a expiré, a été révoqué ou a déjà servi. Demandez-en un nouveau à votre ami.")
}

func (s *Server) invitePage(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	inv, err := s.store.InvitationByToken(r.Context(), auth.HashToken(r.PathValue("token")))
	if errors.Is(err, store.ErrInvitationInvalid) {
		s.inviteInvalid(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	already, err := s.store.AreFriends(r.Context(), user.ID, inv.InviterID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, http.StatusOK, "invite", map[string]any{
		"Title": "Invitation", "Invitation": inv, "Token": r.PathValue("token"),
		"Self": inv.InviterID == user.ID, "Already": already,
	})
}

func (s *Server) inviteAccept(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	inv, notifs, err := s.store.AcceptInvitation(r.Context(), auth.HashToken(r.PathValue("token")), user.ID)
	switch {
	case errors.Is(err, store.ErrInvitationInvalid):
		s.inviteInvalid(w, r)
		return
	case errors.Is(err, store.ErrSelfInvite), errors.Is(err, store.ErrAlreadyFriends):
		http.Redirect(w, r, "/friends", http.StatusSeeOther)
		return
	case err != nil:
		s.serverError(w, r, err)
		return
	}
	s.dispatch(notifs)
	s.setFlash(w, "Vous êtes maintenant ami avec "+inv.InviterName+".")
	http.Redirect(w, r, "/stock?scope="+strconv.FormatInt(inv.InviterID, 10), http.StatusSeeOther)
}
