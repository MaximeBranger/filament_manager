// Package modelimport récupère un modèle 3D à partir d'un lien : page Printables ou Thingiverse,
// ou lien direct vers un .stl / .3mf. Les requêtes sortantes ne visent que des adresses publiques
// en HTTPS, avec une taille et un délai bornés.
package modelimport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"
)

var (
	ErrUnsupported   = errors.New("lien non reconnu : collez l'adresse d'un modèle Printables ou Thingiverse, ou un lien direct vers un fichier .stl ou .3mf")
	ErrNotFound      = errors.New("modèle introuvable : vérifiez le lien")
	ErrPaid          = errors.New("ce modèle est payant : téléchargez-le vous-même puis envoyez le fichier")
	ErrNoModelFile   = errors.New("ce modèle ne contient aucun fichier .stl ou .3mf")
	ErrUnknownFile   = errors.New("ce fichier ne fait pas partie du modèle")
	ErrTooLarge      = errors.New("fichier trop volumineux")
	ErrUnavailable   = errors.New("le site du modèle ne répond pas, réessayez plus tard")
	ErrNoThingiverse = errors.New("l'import depuis Thingiverse n'est pas configuré sur ce serveur (THINGIVERSE_TOKEN)")
)

// File est un fichier téléchargeable d'un modèle.
type File struct {
	ID   string
	Name string
	Size int64 // 0 si inconnue
}

// Model décrit un modèle trouvé à partir d'un lien.
type Model struct {
	Source  string // nom du site, ex. « Printables »
	URL     string // page du modèle
	Title   string
	Author  string
	License string
	Files   []File // .stl et .3mf uniquement

	kind string // printables, thingiverse, direct
	id   string
}

// File renvoie le fichier d'identifiant donné.
func (m *Model) File(id string) (File, bool) {
	for _, f := range m.Files {
		if f.ID == id {
			return f, true
		}
	}
	return File{}, false
}

// Importer interroge les sites de modèles.
type Importer struct {
	client           *http.Client
	maxSize          int64
	userAgent        string
	thingiverseToken string
	printablesAPI    string
	thingiverseAPI   string
}

// New crée un importeur ; maxSize plafonne le fichier téléchargé.
func New(thingiverseToken string, maxSize int64, userAgent string) *Importer {
	return &Importer{
		client:           newClient(false),
		maxSize:          maxSize,
		userAgent:        userAgent,
		thingiverseToken: thingiverseToken,
		printablesAPI:    "https://api.printables.com/graphql/",
		thingiverseAPI:   "https://api.thingiverse.com",
	}
}

var (
	printablesPath  = regexp.MustCompile(`^/(?:[a-z]{2}/)?model/(\d+)`)
	thingiversePath = regexp.MustCompile(`^/thing:(\d+)`)
)

// Resolve identifie le modèle désigné par un lien et liste ses fichiers.
func (im *Importer) Resolve(ctx context.Context, raw string) (*Model, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, ErrUnsupported
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	switch {
	case host == "printables.com":
		if m := printablesPath.FindStringSubmatch(u.Path); m != nil {
			return tidy(im.printables(ctx, m[1]))
		}
	case host == "thingiverse.com":
		if m := thingiversePath.FindStringSubmatch(u.Path); m != nil {
			return tidy(im.thingiverse(ctx, m[1]))
		}
	case u.Scheme == "https" && isModelFile(u.Path):
		name := path.Base(u.Path)
		if unescaped, err := url.PathUnescape(name); err == nil {
			name = unescaped
		}
		return &Model{
			Source: u.Hostname(), URL: u.String(), Title: strings.TrimSuffix(name, path.Ext(name)),
			Files: []File{{ID: "0", Name: name}}, kind: "direct", id: u.String(),
		}, nil
	}
	return nil, ErrUnsupported
}

// Download télécharge un fichier du modèle et renvoie son nom et son contenu.
func (im *Importer) Download(ctx context.Context, m *Model, fileID string) (string, []byte, error) {
	f, ok := m.File(fileID)
	if !ok {
		return "", nil, ErrUnknownFile
	}
	if f.Size > im.maxSize {
		return "", nil, ErrTooLarge
	}
	var link string
	var header http.Header
	switch m.kind {
	case "printables":
		var err error
		if link, err = im.printablesLink(ctx, m.id, f.ID); err != nil {
			return "", nil, err
		}
	case "thingiverse":
		link = im.thingiverseAPI + "/files/" + url.PathEscape(f.ID) + "/download"
		header = http.Header{"Authorization": {"Bearer " + im.thingiverseToken}}
	default:
		link = m.id
	}
	data, err := im.get(ctx, link, header, im.maxSize)
	if err != nil {
		return "", nil, err
	}
	return f.Name, data, nil
}

// tidy normalise les espaces des textes renvoyés par les sites.
func tidy(m *Model, err error) (*Model, error) {
	if m != nil {
		for _, s := range []*string{&m.Title, &m.Author, &m.License} {
			*s = strings.Join(strings.Fields(*s), " ")
		}
	}
	return m, err
}

func isModelFile(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".stl", ".3mf":
		return true
	}
	return false
}

// get lit une réponse en refusant ce qui dépasse limit octets.
func (im *Importer) get(ctx context.Context, link string, header http.Header, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return nil, ErrUnsupported
	}
	return im.do(req, header, limit)
}

func (im *Importer) do(req *http.Request, header http.Header, limit int64) ([]byte, error) {
	for k, v := range header {
		req.Header[k] = v
	}
	req.Header.Set("User-Agent", im.userAgent)
	resp, err := im.client.Do(req)
	if err != nil {
		if errors.Is(err, errForbiddenAddress) {
			return nil, errForbiddenAddress
		}
		return nil, ErrUnavailable
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return nil, ErrNotFound
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("%w (HTTP %d)", ErrUnavailable, resp.StatusCode)
	case resp.ContentLength > limit:
		return nil, ErrTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, ErrUnavailable
	}
	if int64(len(data)) > limit {
		return nil, ErrTooLarge
	}
	return data, nil
}

// maxAPIResponse plafonne les réponses JSON des API (métadonnées, liens).
const maxAPIResponse = 2 << 20

func (im *Importer) getJSON(ctx context.Context, link string, header http.Header, out any) error {
	data, err := im.get(ctx, link, header, maxAPIResponse)
	if err != nil {
		return err
	}
	if json.Unmarshal(data, out) != nil {
		return ErrUnavailable
	}
	return nil
}

// requestTimeout borne chaque échange, téléchargement compris.
const requestTimeout = 2 * time.Minute
