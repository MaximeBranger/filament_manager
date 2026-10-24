package share

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
)

// Maillages (.fmm) et miniatures (.png) partagent le même nom aléatoire.
var keyPattern = regexp.MustCompile(`^[0-9a-f]{32}\.(fmm|png)$`)

// tempPattern reconnaît les fichiers temporaires de Write (os.CreateTemp).
var tempPattern = regexp.MustCompile(`^\.upload-[0-9]+$`)

// NewStorageKey renvoie un nom de fichier aléatoire.
func NewStorageKey() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b) + ".fmm"
}

func path(dir, key string) (string, error) {
	if !keyPattern.MatchString(key) {
		return "", errors.New("clé de stockage invalide")
	}
	return filepath.Join(dir, key), nil
}

// Write écrit le fichier de façon atomique (fichier temporaire puis renommage).
func Write(dir, key string, data []byte) error {
	p, err := path(dir, key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}

// Open ouvre un maillage stocké.
func Open(dir, key string) (*os.File, error) {
	p, err := path(dir, key)
	if err != nil {
		return nil, err
	}
	return os.Open(p)
}

// Remove supprime un maillage ; un fichier déjà absent n'est pas une erreur.
func Remove(dir, key string) error {
	p, err := path(dir, key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// CleanOrphans supprime les fichiers de l'application (maillages, miniatures, envois temporaires)
// qui ne correspondent à aucune clé connue (restes d'un arrêt brutal). Tout autre fichier est
// ignoré : un SHARE_DIR mal configuré (ex. le dossier de la base) ne doit rien perdre.
// À appeler sans envoi en cours. Renvoie le nombre de fichiers supprimés.
func CleanOrphans(dir string, known map[string]bool) (int, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, e := range entries {
		name := e.Name()
		if !e.Type().IsRegular() || known[name] || !(keyPattern.MatchString(name) || tempPattern.MatchString(name)) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}
