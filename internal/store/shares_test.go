package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func shareFile(key string) ShareFile {
	return ShareFile{OriginalName: "benchy.stl", StorageKey: key, Triangles: 12, SizeX: 60, SizeY: 31, SizeZ: 48, Bytes: 440}
}

func TestShareLifecycle(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	alice := newUser(t, s, "alice@example.fr")
	bob := newUser(t, s, "bob@example.fr")

	id, err := s.CreateShare(ctx, alice.ID, "h1", "Benchy", "", shareFile("k1"), 2, 7*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	s.CreateShare(ctx, alice.ID, "h2", "Vase", "pw", shareFile("k2"), 2, 7*24*time.Hour)
	if _, err := s.CreateShare(ctx, alice.ID, "h3", "Trop", "", shareFile("k3"), 2, 7*24*time.Hour); !errors.Is(err, ErrTooManyShares) {
		t.Errorf("limite de partages actifs : %v", err)
	}

	sh, err := s.ShareByToken(ctx, "h1")
	if err != nil || sh.ID != id || sh.OwnerName != alice.DisplayName || sh.File.SizeX != 60 || sh.HasPassword() || sh.Choice != nil {
		t.Fatalf("partage : %+v %v", sh, err)
	}
	if _, err := s.ShareByID(ctx, bob.ID, id); !errors.Is(err, ErrNotFound) {
		t.Error("Bob accède au partage d'Alice")
	}
	if err := s.SetSharePassword(ctx, bob.ID, id, "x"); !errors.Is(err, ErrNotFound) {
		t.Error("Bob change le mot de passe d'Alice")
	}
	if _, err := s.RevokeShare(ctx, bob.ID, id); !errors.Is(err, ErrNotFound) {
		t.Error("Bob révoque le partage d'Alice")
	}

	// Changer le mot de passe incrémente la version (invalide les cookies).
	s.SetSharePassword(ctx, alice.ID, id, "hash")
	sh, _ = s.ShareByID(ctx, alice.ID, id)
	if !sh.HasPassword() || sh.PasswordVersion != 1 {
		t.Errorf("mot de passe : %+v", sh)
	}

	// Choix du destinataire : le dernier remplace le précédent, le créateur est notifié.
	notifs, err := s.SaveShareChoice(ctx, id, ShareChoice{ProductID: 0, Label: "eSun PLA Noir", ColorHex: "#000000"})
	if err != nil || notifs[0].UserID != alice.ID || notifs[0].Kind != NotifShareChoice || notifs[0].Payload.Actor != "Votre destinataire" {
		t.Fatalf("choix : %v %+v", err, notifs)
	}
	s.SaveShareChoice(ctx, id, ShareChoice{Label: "Sunlu PETG Bleu", ColorHex: "#1565c0", RecipientName: "Léa", Message: "Merci !"})
	sh, _ = s.ShareByID(ctx, alice.ID, id)
	if sh.Choice == nil || sh.Choice.Label != "Sunlu PETG Bleu" || sh.Choice.RecipientName != "Léa" {
		t.Errorf("choix remplacé : %+v", sh.Choice)
	}

	keys, err := s.RevokeShare(ctx, alice.ID, id)
	if err != nil || len(keys) != 1 || keys[0] != "k1" {
		t.Errorf("révocation : %v %v", keys, err)
	}
	if _, err := s.ShareByToken(ctx, "h1"); !errors.Is(err, ErrNotFound) {
		t.Error("partage révoqué encore accessible")
	}
	if list, _ := s.ListShares(ctx, alice.ID); len(list) != 1 {
		t.Errorf("%d partages actifs", len(list))
	}
}

func TestShareLockout(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	alice := newUser(t, s, "alice@example.fr")
	id, _ := s.CreateShare(ctx, alice.ID, "h", "Benchy", "pw", shareFile("k"), 5, time.Hour)

	var notified int
	for i := 1; i <= ShareLockThreshold+2; i++ {
		locked, notifs, err := s.RecordShareFailure(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		notified += len(notifs)
		if locked != (i >= ShareLockThreshold) {
			t.Fatalf("échec %d : verrouillé = %v", i, locked)
		}
	}
	if notified != 1 {
		t.Errorf("%d notifications de verrouillage, 1 attendue", notified)
	}
	s.SetSharePassword(ctx, alice.ID, id, "nouveau")
	sh, _ := s.ShareByID(ctx, alice.ID, id)
	if sh.Locked() || sh.FailedAttempts != 0 {
		t.Errorf("un nouveau mot de passe doit déverrouiller : %+v", sh)
	}
}

func TestShareExpiryAndUserDeletion(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	alice := newUser(t, s, "alice@example.fr")
	s.CreateShare(ctx, alice.ID, "old", "Ancien", "", shareFile("old"), 5, time.Hour)
	s.CreateShare(ctx, alice.ID, "new", "Récent", "", shareFile("new"), 5, 7*24*time.Hour)

	s.Now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	ids, keys, err := s.ExpiredShares(ctx)
	if err != nil || len(ids) != 1 || keys[0] != "old" {
		t.Fatalf("partages expirés : %v %v %v", ids, keys, err)
	}
	if _, err := s.ShareByToken(ctx, "old"); !errors.Is(err, ErrNotFound) {
		t.Error("partage expiré encore accessible")
	}
	s.DeleteShares(ctx, ids)
	known, _ := s.AllShareStorageKeys(ctx)
	if len(known) != 1 || !known["new"] {
		t.Errorf("fichiers connus : %v", known)
	}

	keys, _ = s.ShareStorageKeys(ctx, alice.ID)
	if err := s.DeleteUser(ctx, alice.ID); err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 {
		t.Errorf("clés à effacer : %v", keys)
	}
	if known, _ := s.AllShareStorageKeys(ctx); len(known) != 0 {
		t.Error("les partages doivent disparaître avec le compte")
	}
}

func TestShareColors(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	alice := newUser(t, s, "alice@example.fr")
	bob := newUser(t, s, "bob@example.fr")
	befriend(t, s, alice, bob)

	red := pla("eSun", "")
	red.ColorName, red.ColorFamily = "Rouge", "red"
	addSpools(t, s, alice, red, 3, SpoolFields{Level: "new"}) // 3 bobines identiques = 1 pastille
	blue := pla("Sunlu", "")
	blue.ColorName, blue.ColorFamily = "Bleu", "blue"
	addSpools(t, s, alice, blue, 1, SpoolFields{Level: "half"})
	empty := pla("Jayo", "")
	empty.ColorName = "Vide"
	addSpools(t, s, alice, empty, 1, SpoolFields{Level: "empty"})
	hidden := pla("Jayo", "")
	hidden.ColorName = "Masquée"
	addSpools(t, s, alice, hidden, 1, SpoolFields{Level: "new", Hidden: true})
	lent := pla("Elegoo", "")
	lent.ColorName = "Prêtée"
	lentSpools := addSpools(t, s, alice, lent, 1, SpoolFields{Level: "new"})
	s.CreateTransfer(ctx, NewTransfer{Kind: KindLoan, InitiatorID: alice.ID, CounterpartID: bob.ID, SpoolIDs: []int64{lentSpools[0].ID}})

	colors, err := s.ShareColors(ctx, alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range colors {
		names = append(names, p.ColorName)
	}
	if len(names) != 2 || names[0] != "Rouge" || names[1] != "Bleu" {
		t.Errorf("couleurs proposées : %v (attendu Rouge puis Bleu, triées par teinte)", names)
	}

	silk := pla("Bambu Lab", "")
	silk.Finish, silk.ColorName = "silk", "Or"
	if got := silk.ColorLabel(); got != "Bambu Lab PLA Or (Silk)" {
		t.Errorf("ColorLabel = %q", got)
	}
}
