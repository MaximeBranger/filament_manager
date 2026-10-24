package db

import (
	"context"
	"path/filepath"
	"testing"
)

func TestOpenAppliesMigrationsOnce(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.db")

	conn, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&n); err != nil || n == 0 {
		t.Fatalf("migrations non appliquées : n=%d err=%v", n, err)
	}
	conn.Close()

	conn, err = Open(ctx, path)
	if err != nil {
		t.Fatalf("réouverture : %v", err)
	}
	defer conn.Close()
	var again int
	conn.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&again)
	if again != n {
		t.Errorf("migrations rejouées : %d puis %d", n, again)
	}

	var fk int
	conn.QueryRow(`PRAGMA foreign_keys`).Scan(&fk)
	if fk != 1 {
		t.Error("foreign_keys devrait être actif")
	}
}
