package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MaximeBranger/filament_manager/internal/db"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	conn, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return New(conn)
}

func newUser(t *testing.T, s *Store, email string) *User {
	t.Helper()
	u, err := s.CreateUser(context.Background(), email, email)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func pla(brand, ean string) ProductFields {
	return ProductFields{Brand: brand, Material: "PLA", ColorName: "Noir", ColorFamily: "black",
		Finish: "standard", DiameterMM: 1.75, NominalWeightG: 1000, EAN: ean}
}

func TestAddProductWithSpoolsAndIsolation(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	alice := newUser(t, s, "alice@example.fr")
	bob := newUser(t, s, "bob@example.fr")

	pid, err := s.AddProductWithSpools(ctx, alice.ID, pla("eSun", "1234567890123"), 3, SpoolFields{Level: "new"})
	if err != nil {
		t.Fatal(err)
	}
	spools, _ := s.ListSpools(ctx, alice.ID)
	if len(spools) != 3 || spools[0].Product.ID != pid || spools[0].Product.Brand != "eSun" {
		t.Fatalf("bobines inattendues : %+v", spools)
	}

	// Bob ne voit ni ne modifie rien d'Alice.
	if _, err := s.SpoolByID(ctx, bob.ID, spools[0].ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("SpoolByID par un tiers : %v", err)
	}
	if err := s.SetSpoolLevel(ctx, bob.ID, spools[0].ID, "empty"); !errors.Is(err, ErrNotFound) {
		t.Errorf("SetSpoolLevel par un tiers : %v", err)
	}
	if err := s.DeleteSpool(ctx, bob.ID, spools[0].ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("DeleteSpool par un tiers : %v", err)
	}
	if err := s.AddSpools(ctx, bob.ID, pid, 1, SpoolFields{Level: "new"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("AddSpools sur le produit d'un tiers : %v", err)
	}
	if err := s.UpdateProduct(ctx, bob.ID, pid, pla("x", "")); !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateProduct par un tiers : %v", err)
	}

	// Le même EAN est possible chez un autre utilisateur, pas deux fois chez le même.
	if _, err := s.CreateProduct(ctx, bob.ID, pla("eSun", "1234567890123")); err != nil {
		t.Errorf("EAN identique chez un autre utilisateur : %v", err)
	}
	if _, err := s.CreateProduct(ctx, alice.ID, pla("eSun", "1234567890123")); !errors.Is(err, ErrDuplicateEAN) {
		t.Errorf("EAN en double : %v", err)
	}

	if err := s.DeleteProduct(ctx, alice.ID, pid); !errors.Is(err, ErrProductInUse) {
		t.Errorf("suppression d'un produit utilisé : %v", err)
	}
}

func TestReceiveIsAtomic(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	u := newUser(t, s, "u@example.fr")
	pid, _ := s.CreateProduct(ctx, u.ID, pla("Sunlu", "111"))

	err := s.Receive(ctx, u.ID, []ReceptionItem{
		{ProductID: pid, Quantity: 2},
		{NewProduct: ptr(pla("Sunlu", "111")), Quantity: 1}, // EAN en double : tout est annulé
	}, SpoolFields{Level: "new"})
	if !errors.Is(err, ErrDuplicateEAN) {
		t.Fatalf("erreur attendue, obtenu %v", err)
	}
	if spools, _ := s.ListSpools(ctx, u.ID); len(spools) != 0 {
		t.Errorf("la réception aurait dû être annulée, %d bobines créées", len(spools))
	}

	err = s.Receive(ctx, u.ID, []ReceptionItem{
		{ProductID: pid, Quantity: 2},
		{NewProduct: ptr(pla("Polymaker", "222")), Quantity: 3},
	}, SpoolFields{Level: "new", PurchaseDate: "2026-10-24"})
	if err != nil {
		t.Fatal(err)
	}
	spools, _ := s.ListSpools(ctx, u.ID)
	if len(spools) != 5 || spools[4].PurchaseDate != "2026-10-24" {
		t.Errorf("réception : %d bobines", len(spools))
	}
}

func ptr[T any](v T) *T { return &v }

func TestBrandSuggestionsAndResolve(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	me := newUser(t, s, "me@example.fr")

	// "Obscure3D" est utilisée par 3 autres comptes : elle devient publique.
	// "Typo" n'est utilisée que par 2 comptes : elle reste privée.
	for i := range 3 {
		u := newUser(t, s, fmt.Sprintf("u%d@example.fr", i))
		s.CreateProduct(ctx, u.ID, pla("Obscure3D", ""))
		if i < 2 {
			s.CreateProduct(ctx, u.ID, pla("Typo", ""))
		}
	}
	s.CreateProduct(ctx, me.ID, pla("Ma Marque", ""))

	got, err := s.BrandSuggestions(ctx, me.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Obscure3D", "Ma Marque", "eSun", "Bambu Lab"} {
		if !slices.Contains(got, want) {
			t.Errorf("suggestion manquante %q", want)
		}
	}
	if slices.Contains(got, "Typo") {
		t.Error("une marque utilisée par moins de 3 comptes ne doit pas être proposée")
	}

	for in, want := range map[string]string{"ESUN": "eSun", "  ma   marque": "Ma Marque", "obscure3d": "Obscure3D", "Nouvelle  Marque": "Nouvelle Marque"} {
		if got, _ := s.ResolveBrand(ctx, me.ID, in); got != want {
			t.Errorf("ResolveBrand(%q) = %q, attendu %q", in, got, want)
		}
	}
}

func TestDeleteUserCascades(t *testing.T) {
	ctx := context.Background()
	s := newStore(t)
	u := newUser(t, s, "bye@example.fr")
	s.AddProductWithSpools(ctx, u.ID, pla("eSun", ""), 2, SpoolFields{Level: "new"})
	if err := s.DeleteUser(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	s.DB.QueryRow(`SELECT (SELECT COUNT(*) FROM spools) + (SELECT COUNT(*) FROM products) + (SELECT COUNT(*) FROM users)`).Scan(&n)
	if n != 0 {
		t.Errorf("%d lignes restantes", n)
	}
}
