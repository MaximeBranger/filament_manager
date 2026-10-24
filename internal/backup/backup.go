// Package backup écrit périodiquement une copie cohérente de la base (VACUUM INTO)
// et ne conserve que les N plus récentes.
package backup

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const prefix = "filamentmanager-"

type Snapshotter interface {
	Backup(ctx context.Context, path string) error
}

type Runner struct {
	Dir       string
	Interval  time.Duration
	Retention int
	DB        Snapshotter
	Log       *slog.Logger
	Now       func() time.Time
}

// Run fait une sauvegarde au démarrage puis à chaque intervalle, jusqu'à l'annulation du contexte.
func (r *Runner) Run(ctx context.Context) {
	ticker := time.NewTicker(r.Interval)
	defer ticker.Stop()
	for {
		if path, err := r.Once(ctx); err != nil {
			r.Log.Error("sauvegarde échouée", "err", err)
		} else {
			r.Log.Info("sauvegarde écrite", "path", path)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Once écrit une sauvegarde et applique la rétention.
func (r *Runner) Once(ctx context.Context) (string, error) {
	if err := os.MkdirAll(r.Dir, 0o750); err != nil {
		return "", err
	}
	now := time.Now
	if r.Now != nil {
		now = r.Now
	}
	name := prefix + now().UTC().Format("20060102T150405Z") + ".db"
	path := filepath.Join(r.Dir, name)
	tmp := path + ".tmp"
	os.Remove(tmp)
	if err := r.DB.Backup(ctx, tmp); err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("VACUUM INTO : %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", err
	}
	return path, r.prune()
}

func (r *Runner) prune() error {
	entries, err := os.ReadDir(r.Dir)
	if err != nil {
		return err
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), prefix) && strings.HasSuffix(e.Name(), ".db") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files) // l'horodatage dans le nom donne l'ordre chronologique
	for len(files) > r.Retention {
		if err := os.Remove(filepath.Join(r.Dir, files[0])); err != nil {
			return err
		}
		files = files[1:]
	}
	return nil
}
