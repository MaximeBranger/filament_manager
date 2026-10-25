package modelimport

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
)

// Printables -------------------------------------------------------------------------
// API GraphQL publique (non documentée) du site : modèles gratuits accessibles sans compte.

const printablesModelQuery = `query ModelFiles($id: ID!) { model: print(id: $id) {
	id name premium price license { name } user { publicUsername } stls { id name fileSize } } }`

const printablesLinkMutation = `mutation GetDownloadLink($id: ID!, $modelId: ID!, $fileType: DownloadFileTypeEnum!, $source: DownloadSourceEnum!) {
	getDownloadLink(id: $id, printId: $modelId, fileType: $fileType, source: $source) { ok output { link } } }`

func (im *Importer) graphql(ctx context.Context, query string, vars map[string]string, out any) error {
	body, _ := json.Marshal(map[string]any{"query": query, "variables": vars})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, im.printablesAPI, bytes.NewReader(body))
	if err != nil {
		return ErrUnavailable
	}
	data, err := im.do(req, http.Header{"Content-Type": {"application/json"}}, maxAPIResponse)
	if err != nil {
		return err
	}
	if json.Unmarshal(data, out) != nil {
		return ErrUnavailable
	}
	return nil
}

func (im *Importer) printables(ctx context.Context, id string) (*Model, error) {
	var resp struct {
		Data struct {
			Model *struct {
				ID      string   `json:"id"`
				Name    string   `json:"name"`
				Premium bool     `json:"premium"`
				Price   *float64 `json:"price"`
				License *struct {
					Name string `json:"name"`
				} `json:"license"`
				User *struct {
					PublicUsername string `json:"publicUsername"`
				} `json:"user"`
				STLs []struct {
					ID       string `json:"id"`
					Name     string `json:"name"`
					FileSize int64  `json:"fileSize"`
				} `json:"stls"`
			} `json:"model"`
		} `json:"data"`
	}
	if err := im.graphql(ctx, printablesModelQuery, map[string]string{"id": id}, &resp); err != nil {
		return nil, err
	}
	p := resp.Data.Model
	if p == nil {
		return nil, ErrNotFound
	}
	if p.Premium || (p.Price != nil && *p.Price > 0) {
		return nil, ErrPaid
	}
	m := &Model{Source: "Printables", URL: "https://www.printables.com/model/" + p.ID, Title: p.Name, kind: "printables", id: p.ID}
	if p.License != nil {
		m.License = p.License.Name
	}
	if p.User != nil {
		m.Author = p.User.PublicUsername
	}
	for _, f := range p.STLs {
		if isModelFile(f.Name) {
			m.Files = append(m.Files, File{ID: f.ID, Name: f.Name, Size: f.FileSize})
		}
	}
	if len(m.Files) == 0 {
		return nil, ErrNoModelFile
	}
	return m, nil
}

// printablesLink demande un lien de téléchargement (les 3MF sont rangés avec les STL).
func (im *Importer) printablesLink(ctx context.Context, modelID, fileID string) (string, error) {
	var resp struct {
		Data struct {
			GetDownloadLink struct {
				OK     bool `json:"ok"`
				Output *struct {
					Link string `json:"link"`
				} `json:"output"`
			} `json:"getDownloadLink"`
		} `json:"data"`
	}
	vars := map[string]string{"id": fileID, "modelId": modelID, "fileType": "stl", "source": "model_detail"}
	if err := im.graphql(ctx, printablesLinkMutation, vars, &resp); err != nil {
		return "", err
	}
	dl := resp.Data.GetDownloadLink
	if !dl.OK || dl.Output == nil || dl.Output.Link == "" {
		return "", ErrUnavailable
	}
	return dl.Output.Link, nil
}

// Thingiverse ------------------------------------------------------------------------
// API REST officielle, avec le jeton d'application de l'instance.

func (im *Importer) thingiverse(ctx context.Context, id string) (*Model, error) {
	if im.thingiverseToken == "" {
		return nil, ErrNoThingiverse
	}
	auth := http.Header{"Authorization": {"Bearer " + im.thingiverseToken}}
	var thing struct {
		ID      int64  `json:"id"`
		Name    string `json:"name"`
		License string `json:"license"`
		Creator *struct {
			Name string `json:"name"`
		} `json:"creator"`
	}
	if err := im.getJSON(ctx, im.thingiverseAPI+"/things/"+url.PathEscape(id), auth, &thing); err != nil {
		return nil, err
	}
	var files []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
		Size int64  `json:"size"`
	}
	if err := im.getJSON(ctx, im.thingiverseAPI+"/things/"+url.PathEscape(id)+"/files", auth, &files); err != nil {
		return nil, err
	}
	m := &Model{
		Source: "Thingiverse", URL: "https://www.thingiverse.com/thing:" + id, Title: thing.Name,
		License: thing.License, kind: "thingiverse", id: id,
	}
	if thing.Creator != nil {
		m.Author = thing.Creator.Name
	}
	for _, f := range files {
		if isModelFile(f.Name) {
			m.Files = append(m.Files, File{ID: strconv.FormatInt(f.ID, 10), Name: f.Name, Size: f.Size})
		}
	}
	if len(m.Files) == 0 {
		return nil, ErrNoModelFile
	}
	return m, nil
}
