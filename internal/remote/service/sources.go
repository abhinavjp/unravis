package service

import (
	"context"
	"encoding/json"
	"io"
	"strings"

	"api-audit-explorer/internal/remote/registry"
)

const sourcesLimit = 256 << 10
const sourcesNote = "This machine's Blackbox did not publish a list of its folders. Use the usual folders or add them yourself."

// Source is a location published by the recorder, annotated with its allowlist status.
type Source struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Label      string `json:"label"`
	Dir        string `json:"dir"`
	Pattern    string `json:"pattern"`
	Origin     string `json:"origin"`
	Err        string `json:"err"`
	Exists     bool   `json:"exists"`
	Readable   bool   `json:"readable"`
	Truncated  bool   `json:"truncated"`
	Files      int    `json:"files"`
	Bytes      int64  `json:"bytes"`
	NewestUnix int64  `json:"newestUnix"`
	Folder     string `json:"folder"`
	Allowed    bool   `json:"allowed"`
}

// Discovery describes the recorder's published locations.
type Discovery struct {
	Machine   string   `json:"machine"`
	Found     bool     `json:"found"`
	From      string   `json:"from"`
	Host      string   `json:"host"`
	Version   string   `json:"version"`
	Generated string   `json:"generated"`
	Sources   []Source `json:"sources"`
	Note      string   `json:"note"`
}

func parseSources(b []byte, e registry.Entry) (Discovery, error) {
	missing := Discovery{Note: sourcesNote, Sources: []Source{}}
	var doc struct {
		Schema    int      `json:"schema"`
		Host      string   `json:"host"`
		Version   string   `json:"version"`
		Generated string   `json:"generated"`
		Sources   []Source `json:"sources"`
	}
	if len(b) > sourcesLimit || json.Unmarshal(b, &doc) != nil { //nolint:nilerr // a manifest we cannot read is "not published", not a failure
		return missing, nil
	}
	if doc.Schema != 1 {
		missing.Note = "This Blackbox is newer than Unravis understands."
		return missing, nil
	}
	out := Discovery{Found: true, Host: strings.TrimSpace(doc.Host), Version: strings.TrimSpace(doc.Version), Generated: strings.TrimSpace(doc.Generated), Sources: []Source{}}
	seen := make(map[string]bool)
	for _, src := range doc.Sources {
		if len(out.Sources) == 200 {
			break
		}
		src.ID = strings.TrimSpace(src.ID)
		src.Kind = strings.TrimSpace(src.Kind)
		src.Label = strings.TrimSpace(src.Label)
		src.Dir = strings.TrimSpace(src.Dir)
		src.Pattern = strings.TrimSpace(src.Pattern)
		src.Origin = strings.TrimSpace(src.Origin)
		src.Err = strings.TrimSpace(src.Err)
		key := src.Dir + "|" + src.Pattern
		if src.Dir == "" || seen[key] {
			continue
		}
		seen[key] = true
		src.Folder = registry.FolderOther
		switch src.Kind {
		case "blackbox":
			src.Folder = registry.FolderBlackbox
		case "iis":
			src.Folder = registry.FolderIIS
		case "sqlserver":
			src.Folder = registry.FolderSQL
		case "redis":
			src.Folder = registry.FolderRedis
		}
		src.Allowed = false
		for _, dir := range e.Folders[src.Folder] {
			if dir == src.Dir {
				src.Allowed = true
				break
			}
		}
		out.Sources = append(out.Sources, src)
	}
	return out, nil
}

// Discover reads the first published source list in the allowed Blackbox folders.
func (s *Service) Discover(ctx context.Context, id, machine string) (Discovery, error) {
	t, err := s.open(ctx, id, machine)
	if err != nil {
		return Discovery{}, err
	}
	defer t.close()
	out := Discovery{Machine: t.machine, Note: sourcesNote, Sources: []Source{}}
	for _, dir := range t.e.Folders[registry.FolderBlackbox] {
		sep := "/"
		if strings.Contains(dir, `\`) || (len(dir) >= 2 && dir[1] == ':') {
			sep = `\`
		}
		path := strings.TrimRight(dir, `\/`) + sep + "sources.json"
		f, err := t.tr.Open(ctx, path, 0)
		if err != nil {
			continue
		}
		b, readErr := io.ReadAll(io.LimitReader(f, sourcesLimit+1))
		_ = f.Close()
		if readErr != nil {
			out.From = path
			return out, nil
		}
		out, err = parseSources(b, t.e)
		out.Machine, out.From = t.machine, path
		return out, err
	}
	return out, nil
}
