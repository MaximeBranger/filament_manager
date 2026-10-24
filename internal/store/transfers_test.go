package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// befriend rend a et b amis via une invitation.
func befriend(t *testing.T, s *Store, a, b *User) {
	t.Helper()
	ctx := context.Background()
	token := "tok-" + a.Email + "-" + b.Email
	if err := s.CreateInvitation(ctx, a.ID, token, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.AcceptInvitation(ctx, token, b.ID); err != nil {
		t.Fatal(err)
	}
}

func addSpools(t *testing.T, s *Store, u *User, f ProductFields, n int, sf SpoolFields) []Spool {
	t.Helper()
	ctx := context.Background()
	before, _ := s.ListSpools(ctx, u.ID)
	if _, err := s.AddProductWithSpools(ctx, u.ID, f, n, sf); err != nil {
		t.Fatal(err)
	}
	after, _ := s.ListSpools(ctx, u.ID)
	return after[len(before):]
}

func TestInvitations(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	alice := newUser(t, s, "alice@example.fr")
	bob := newUser(t, s, "bob@example.fr")

	s.CreateInvitation(ctx, alice.ID, "t1", time.Now().Add(time.Hour))
	if _, _, err := s.AcceptInvitation(ctx, "t1", alice.ID); !errors.Is(err, ErrSelfInvite) {
		t.Errorf("auto-invitation : %v", err)
	}
	inv, notifs, err := s.AcceptInvitation(ctx, "t1", bob.ID)
	if err != nil || inv.InviterID != alice.ID {
		t.Fatalf("acceptation : %v", err)
	}
	if len(notifs) != 1 || notifs[0].UserID != alice.ID || notifs[0].Kind != NotifInviteAccepted || notifs[0].Payload.Actor != bob.DisplayName {
		t.Errorf("notification : %+v", notifs)
	}
	if ok, _ := s.AreFriends(ctx, bob.ID, alice.ID); !ok {
		t.Error("amitié non réciproque")
	}
	if _, _, err := s.AcceptInvitation(ctx, "t1", bob.ID); !errors.Is(err, ErrInvitationInvalid) {
		t.Errorf("invitation réutilisée : %v", err)
	}

	s.CreateInvitation(ctx, alice.ID, "t2", time.Now().Add(time.Hour))
	if _, _, err := s.AcceptInvitation(ctx, "t2", bob.ID); !errors.Is(err, ErrAlreadyFriends) {
		t.Errorf("déjà amis : %v", err)
	}
	invs, _ := s.ListInvitations(ctx, alice.ID)
	if len(invs) != 1 {
		t.Fatalf("%d invitations en cours", len(invs))
	}
	if err := s.RevokeInvitation(ctx, bob.ID, invs[0].ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("révocation par un tiers : %v", err)
	}
	s.RevokeInvitation(ctx, alice.ID, invs[0].ID)
	if _, err := s.InvitationByToken(ctx, "t2"); !errors.Is(err, ErrInvitationInvalid) {
		t.Errorf("invitation révoquée : %v", err)
	}

	s.CreateInvitation(ctx, alice.ID, "t3", time.Now().Add(-time.Minute))
	if _, err := s.InvitationByToken(ctx, "t3"); !errors.Is(err, ErrInvitationInvalid) {
		t.Errorf("invitation expirée : %v", err)
	}
}

func TestFriendVisibility(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	alice := newUser(t, s, "alice@example.fr")
	bob := newUser(t, s, "bob@example.fr")
	stranger := newUser(t, s, "x@example.fr")
	befriend(t, s, alice, bob)

	price := int64(1999)
	addSpools(t, s, alice, pla("eSun", ""), 1, SpoolFields{Level: "new", Notes: "secret", PriceCents: &price, Supplier: "Amazon", PurchaseDate: "2026-01-01"})
	addSpools(t, s, alice, pla("Sunlu", ""), 1, SpoolFields{Level: "empty"})
	addSpools(t, s, alice, pla("Jayo", ""), 1, SpoolFields{Level: "new", Hidden: true})

	got, err := s.ListFriendSpools(ctx, bob.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Product.Brand != "eSun" || got[0].OwnerName != alice.DisplayName {
		t.Fatalf("bobines visibles : %+v", got)
	}
	if got[0].Notes != "" || got[0].PriceCents != nil || got[0].Supplier != "" || got[0].PurchaseDate != "" {
		t.Error("champs privés exposés")
	}
	if got, _ := s.ListFriendSpools(ctx, stranger.ID, alice.ID); len(got) != 0 {
		t.Error("un non-ami voit le stock")
	}
	if got, _ := s.ListFriendSpools(ctx, alice.ID, 0); len(got) != 0 {
		t.Error("Bob n'a rien de visible")
	}
}

func TestLoanRequestLifecycle(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	alice := newUser(t, s, "alice@example.fr")
	bob := newUser(t, s, "bob@example.fr")
	stranger := newUser(t, s, "x@example.fr")
	befriend(t, s, alice, bob)
	spools := addSpools(t, s, alice, pla("eSun", ""), 3, SpoolFields{Level: "new"})
	hidden := addSpools(t, s, alice, pla("Jayo", ""), 1, SpoolFields{Level: "new", Hidden: true})

	if _, _, err := s.CreateTransfer(ctx, NewTransfer{Kind: KindLoan, InitiatorID: stranger.ID, CounterpartID: alice.ID, SpoolIDs: []int64{spools[0].ID}}); !errors.Is(err, ErrNotFriends) {
		t.Errorf("demande d'un non-ami : %v", err)
	}
	if _, _, err := s.CreateTransfer(ctx, NewTransfer{Kind: KindLoan, InitiatorID: bob.ID, CounterpartID: alice.ID, SpoolIDs: []int64{hidden[0].ID}}); !errors.Is(err, ErrSpoolUnavailable) {
		t.Errorf("demande d'une bobine masquée : %v", err)
	}

	id, notifs, err := s.CreateTransfer(ctx, NewTransfer{Kind: KindLoan, InitiatorID: bob.ID, CounterpartID: alice.ID,
		SpoolIDs: []int64{spools[0].ID, spools[1].ID}, Message: "Pour un cosplay", GramsNeeded: 300})
	if err != nil {
		t.Fatal(err)
	}
	if len(notifs) != 1 || notifs[0].UserID != alice.ID || notifs[0].Kind != NotifLoanRequested || notifs[0].Payload.Count != 2 {
		t.Errorf("notification de demande : %+v", notifs)
	}
	if _, err := s.Decide(ctx, id, bob.ID, []int64{spools[0].ID}, nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("décision par le demandeur : %v", err)
	}

	// Acceptation partielle.
	notifs, err = s.Decide(ctx, id, alice.ID, []int64{spools[0].ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if notifs[0].UserID != bob.ID || notifs[0].Kind != NotifTransferAccepted || notifs[0].Payload.Count != 1 {
		t.Errorf("notification de décision : %+v", notifs)
	}
	transfers, _ := s.ListTransfers(ctx, bob.ID, 10)
	if transfers[0].Status != StatusAccepted || transfers[0].GramsNeeded != 300 || transfers[0].Items[1].Decision != StatusRefused {
		t.Errorf("transaction : %+v", transfers[0])
	}

	// Pendant le prêt : niveau figé, suppression refusée, plus de nouvelle demande.
	if err := s.SetSpoolLevel(ctx, alice.ID, spools[0].ID, "low"); !errors.Is(err, ErrSpoolLent) {
		t.Errorf("niveau modifié pendant le prêt : %v", err)
	}
	s.UpdateSpool(ctx, alice.ID, spools[0].ID, SpoolFields{Level: "empty", Notes: "prêtée"})
	sp, _ := s.SpoolByID(ctx, alice.ID, spools[0].ID)
	if sp.Level != "new" || sp.Notes != "prêtée" || sp.Loan == nil || sp.Loan.BorrowerID != bob.ID {
		t.Errorf("bobine prêtée : %+v", sp)
	}
	if err := s.DeleteSpool(ctx, alice.ID, spools[0].ID); !errors.Is(err, ErrSpoolLent) {
		t.Errorf("suppression pendant le prêt : %v", err)
	}
	if _, _, err := s.CreateTransfer(ctx, NewTransfer{Kind: KindGift, InitiatorID: bob.ID, CounterpartID: alice.ID, SpoolIDs: []int64{spools[0].ID}}); !errors.Is(err, ErrSpoolUnavailable) {
		t.Errorf("demande sur une bobine prêtée : %v", err)
	}
	borrowed, _ := s.ListBorrowedSpools(ctx, bob.ID)
	if len(borrowed) != 1 || borrowed[0].ID != spools[0].ID {
		t.Errorf("emprunts de Bob : %+v", borrowed)
	}
	if err := s.RemoveFriend(ctx, bob.ID, alice.ID); !errors.Is(err, ErrActiveLoan) {
		t.Errorf("retrait d'ami pendant un prêt : %v", err)
	}
	if err := s.DeleteUser(ctx, bob.ID); !errors.Is(err, ErrActiveLoan) {
		t.Errorf("suppression de compte pendant un prêt : %v", err)
	}

	// Retour.
	loans, _ := s.ListLoans(ctx, alice.ID, 10)
	if len(loans) != 1 || !loans[0].Active() {
		t.Fatalf("prêts : %+v", loans)
	}
	if _, err := s.ReturnLoan(ctx, loans[0].ID, bob.ID, "half"); !errors.Is(err, ErrNotFound) {
		t.Errorf("retour enregistré par l'emprunteur : %v", err)
	}
	notifs, err = s.ReturnLoan(ctx, loans[0].ID, alice.ID, "half")
	if err != nil || notifs[0].Kind != NotifLoanReturned || notifs[0].UserID != bob.ID {
		t.Fatalf("retour : %v %+v", err, notifs)
	}
	sp, _ = s.SpoolByID(ctx, alice.ID, spools[0].ID)
	if sp.Level != "half" || sp.Loan != nil {
		t.Errorf("après retour : %+v", sp)
	}
}

func TestDirectLoanConvertedToGift(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	alice := newUser(t, s, "alice@example.fr")
	bob := newUser(t, s, "bob@example.fr")
	befriend(t, s, alice, bob)
	price := int64(2500)
	spools := addSpools(t, s, alice, pla("eSun", "6938936716785"), 1, SpoolFields{Level: "new", Notes: "secret", PriceCents: &price})

	_, notifs, err := s.CreateTransfer(ctx, NewTransfer{Kind: KindLoan, InitiatorID: alice.ID, CounterpartID: bob.ID, SpoolIDs: []int64{spools[0].ID}})
	if err != nil {
		t.Fatal(err)
	}
	if notifs[0].Kind != NotifLoanRecorded || notifs[0].UserID != bob.ID {
		t.Errorf("notification de prêt direct : %+v", notifs)
	}
	loans, _ := s.ListLoans(ctx, bob.ID, 10)
	if len(loans) != 1 || !loans[0].Active() {
		t.Fatalf("le prêt direct doit être actif : %+v", loans)
	}

	notifs, err = s.ConvertLoanToGift(ctx, loans[0].ID, alice.ID, "half")
	if err != nil || notifs[0].Kind != NotifLoanConvertedGift {
		t.Fatalf("conversion : %v", err)
	}
	if _, err := s.SpoolByID(ctx, alice.ID, spools[0].ID); !errors.Is(err, ErrNotFound) {
		t.Error("la bobine donnée est encore chez Alice")
	}
	sp, err := s.SpoolByID(ctx, bob.ID, spools[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if sp.Level != "half" || sp.Notes != "" || sp.PriceCents != nil || sp.Product.UserID != bob.ID || sp.Product.EAN != "6938936716785" {
		t.Errorf("bobine reçue : %+v", sp)
	}
	gifts, _ := s.ListGifts(ctx, alice.ID, 10)
	if len(gifts) != 1 || gifts[0].ReceiverName != bob.DisplayName || gifts[0].Level != "half" {
		t.Errorf("historique des dons : %+v", gifts)
	}
}

func TestGiftOfferReusesReceiverProduct(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	alice := newUser(t, s, "alice@example.fr")
	bob := newUser(t, s, "bob@example.fr")
	befriend(t, s, alice, bob)
	spools := addSpools(t, s, alice, pla("eSun", "111"), 2, SpoolFields{Level: "new"})
	bobProduct, _ := s.CreateProduct(ctx, bob.ID, pla("eSun", "111"))

	id, notifs, err := s.CreateTransfer(ctx, NewTransfer{Kind: KindGift, InitiatorID: alice.ID, CounterpartID: bob.ID,
		SpoolIDs: []int64{spools[0].ID, spools[1].ID}, Levels: map[int64]string{spools[0].ID: "low"}})
	if err != nil || notifs[0].Kind != NotifGiftOffered {
		t.Fatalf("proposition de don : %v", err)
	}
	if _, err := s.Decide(ctx, id, alice.ID, nil, nil); !errors.Is(err, ErrNotFound) {
		t.Error("le donneur ne doit pas décider à la place du receveur")
	}
	if _, err := s.Decide(ctx, id, bob.ID, []int64{spools[0].ID, spools[1].ID}, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := s.ListSpools(ctx, bob.ID)
	if len(got) != 2 || got[0].ProductID != bobProduct || got[0].Level != "low" || got[1].Level != "new" {
		t.Errorf("bobines reçues : %+v", got)
	}
}

func TestCancelAndRemoveFriend(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	alice := newUser(t, s, "alice@example.fr")
	bob := newUser(t, s, "bob@example.fr")
	befriend(t, s, alice, bob)
	spools := addSpools(t, s, alice, pla("eSun", ""), 1, SpoolFields{Level: "new"})

	id, _, _ := s.CreateTransfer(ctx, NewTransfer{Kind: KindGift, InitiatorID: bob.ID, CounterpartID: alice.ID, SpoolIDs: []int64{spools[0].ID}})
	if err := s.CancelTransfer(ctx, id, alice.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("annulation par le destinataire : %v", err)
	}
	if err := s.CancelTransfer(ctx, id, bob.ID); err != nil {
		t.Errorf("annulation : %v", err)
	}

	id, _, _ = s.CreateTransfer(ctx, NewTransfer{Kind: KindLoan, InitiatorID: bob.ID, CounterpartID: alice.ID, SpoolIDs: []int64{spools[0].ID}})
	if err := s.RemoveFriend(ctx, alice.ID, bob.ID); err != nil {
		t.Fatal(err)
	}
	transfers, _ := s.ListTransfers(ctx, alice.ID, 10)
	if transfers[0].ID != id || transfers[0].Status != StatusCancelled {
		t.Errorf("demande en attente non annulée : %+v", transfers[0])
	}
	if _, err := s.Decide(ctx, id, alice.ID, []int64{spools[0].ID}, nil); !errors.Is(err, ErrNotFound) {
		t.Error("une demande annulée ne peut plus être acceptée")
	}
	if ok, _ := s.AreFriends(ctx, alice.ID, bob.ID); ok {
		t.Error("toujours amis")
	}
}

func TestNotifications(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	alice := newUser(t, s, "alice@example.fr")
	bob := newUser(t, s, "bob@example.fr")
	befriend(t, s, alice, bob)

	if n, _ := s.UnreadCount(ctx, alice.ID); n != 1 {
		t.Errorf("%d notifications non lues", n)
	}
	list, _ := s.ListNotifications(ctx, alice.ID, 10)
	if len(list) != 1 || list[0].Payload.Actor != bob.DisplayName {
		t.Errorf("notifications : %+v", list)
	}
	s.MarkNotificationsRead(ctx, alice.ID)
	if n, _ := s.UnreadCount(ctx, alice.ID); n != 0 {
		t.Error("notifications non marquées lues")
	}
}
