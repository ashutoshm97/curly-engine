// Package ingest turns an uploaded PPTX/HTML file into a stored style profile.
package ingest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/ashutoshm97/curly-engine/internal/blob"
	"github.com/ashutoshm97/curly-engine/internal/htmlx"
	"github.com/ashutoshm97/curly-engine/internal/jobs"
	"github.com/ashutoshm97/curly-engine/internal/normalize"
	"github.com/ashutoshm97/curly-engine/internal/ooxml"
	"github.com/ashutoshm97/curly-engine/internal/profile"
	"github.com/ashutoshm97/curly-engine/internal/rawdeck"
	"github.com/ashutoshm97/curly-engine/internal/store"
)

const JobType = "ingest"

type Payload struct {
	UploadID string `json:"uploadId"`
	Name     string `json:"name,omitempty"`
}

type Result struct {
	ProfileID string `json:"profileId"`
	Version   int    `json:"version"`
	Reused    bool   `json:"reused,omitempty"`
	Layouts   int    `json:"layouts"`
	Slides    int    `json:"slides"`
}

type Service struct {
	Store *store.Store
	Blobs *blob.Store
	HTML  *htmlx.Extractor
}

// DetectKind classifies an upload by magic bytes, falling back to the extension.
func DetectKind(filename string, data []byte) (string, error) {
	head := bytes.TrimSpace(data[:min(len(data), 512)])
	switch {
	case bytes.HasPrefix(data, []byte("PK\x03\x04")):
		return "pptx", nil
	case bytes.HasPrefix(bytes.ToLower(head), []byte("<!doctype html")), bytes.HasPrefix(bytes.ToLower(head), []byte("<html")):
		return "html", nil
	}
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".html", ".htm":
		return "html", nil
	}
	return "", errors.New("unsupported file: expected .pptx or .html")
}

// Extract parses a source file into a raw deck.
func (s *Service) Extract(ctx context.Context, kind string, data []byte) (*rawdeck.Deck, error) {
	switch kind {
	case "pptx":
		return ooxml.Extract(data, ooxml.DefaultLimits)
	case "html":
		if s.HTML == nil {
			return nil, errors.New("html extraction disabled")
		}
		return s.HTML.Extract(ctx, data)
	}
	return nil, fmt.Errorf("unknown kind %q", kind)
}

// Handle is the jobs.Handler for ingest jobs.
func (s *Service) Handle(ctx context.Context, j *store.Job, rep jobs.Reporter) (any, error) {
	var p Payload
	if err := json.Unmarshal([]byte(j.Payload), &p); err != nil {
		return nil, jobs.Permanent{Err: err}
	}
	up, err := s.Store.GetUpload(ctx, p.UploadID)
	if err != nil {
		return nil, jobs.Permanent{Err: fmt.Errorf("upload %s: %w", p.UploadID, err)}
	}
	if existing, err := s.Store.FindProfileBySource(ctx, up.SHA256); err == nil {
		return Result{ProfileID: existing.ID, Version: existing.CurrentVersion, Reused: true}, nil
	}
	data, err := s.Blobs.Get(up.SHA256)
	if err != nil {
		return nil, err
	}
	rep.Stage("parse")
	deck, err := s.Extract(ctx, up.Kind, data)
	if err != nil {
		return nil, jobs.Permanent{Err: err}
	}
	rep.Stage("normalize")
	name := p.Name
	if name == "" {
		name = strings.TrimSuffix(up.Filename, filepath.Ext(up.Filename))
	}
	id := store.NewID("pf")
	prof, err := normalize.Build(deck, normalize.Options{ID: id, Name: name})
	if err != nil {
		return nil, jobs.Permanent{Err: err}
	}
	rep.Stage("save")
	if err := s.saveAssets(prof, deck); err != nil {
		return nil, err
	}
	x, err := prof.MarshalIndent()
	if err != nil {
		return nil, jobs.Permanent{Err: err}
	}
	rec := &store.Profile{ID: id, Name: name, UploadID: up.ID, SourceSHA256: up.SHA256, SourceType: up.Kind}
	if err := s.Store.CreateProfile(ctx, rec, x); err != nil {
		return nil, err
	}
	return Result{ProfileID: id, Version: 1, Layouts: len(prof.Layouts), Slides: len(deck.Slides)}, nil
}

func (s *Service) saveAssets(p *profile.StyleProfile, d *rawdeck.Deck) error {
	for _, a := range p.Assets {
		src, ok := d.Assets[a.Ref]
		if !ok {
			continue
		}
		if _, err := s.Blobs.Put(src.Data); err != nil {
			return err
		}
	}
	return nil
}
