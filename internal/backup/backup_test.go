package backup

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MaximeBranger/filament_manager/internal/db"
	"github.com/MaximeBranger/filament_manager/internal/store"
)

func TestOnceWritesConsistentCopyAndPrunes(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	conn, err := db.Open(ctx, filepath.Join(dir, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	s := store.New(conn)
	s.CreateUser(ctx, "a@example.fr", "A")

	now := time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)
	r := &Runner{Dir: filepath.Join(dir, "backups"), Retention: 2, DB: s, Now: func() time.Time { return now }}

	var last string
	for range 3 {
		if last, err = r.Once(ctx); err != nil {
			t.Fatal(err)
		}
		now = now.Add(time.Hour)
	}
	entries, _ := os.ReadDir(r.Dir)
	if len(entries) != 2 {
		t.Fatalf("%d sauvegardes conservées, 2 attendues", len(entries))
	}

	copyDB, err := sql.Open("sqlite", last)
	if err != nil {
		t.Fatal(err)
	}
	defer copyDB.Close()
	var n int
	if err := copyDB.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n); err != nil || n != 1 {
		t.Errorf("copie illisible ou incomplète : n=%d err=%v", n, err)
	}
}
