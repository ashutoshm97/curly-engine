package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/ashutoshm97/curly-engine/internal/gen"
	"github.com/ashutoshm97/curly-engine/internal/render"
	"github.com/ashutoshm97/curly-engine/internal/scene"
	"github.com/ashutoshm97/curly-engine/internal/store"
)

func assetURL(ref string) string { return "/api/assets/" + ref }

func (s *Server) deckRoutes() {
	s.mux.HandleFunc("POST /api/decks", s.createDeck)
	s.mux.HandleFunc("GET /api/decks/{id}", s.getDeck)
	s.mux.HandleFunc("GET /api/decks/{id}/slides/{n}", s.slideSVG)
	s.mux.HandleFunc("GET /api/decks/{id}/export.pptx", s.exportPPTX)
	s.mux.HandleFunc("GET /api/profiles/{id}/layouts/{lid}/preview", s.layoutPreview)
}

func (s *Server) createDeck(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ProfileID string `json:"profileId"`
		Prompt    string `json:"prompt"`
		Slides    int    `json:"slides"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil || req.ProfileID == "" || strings.TrimSpace(req.Prompt) == "" {
		Error(w, http.StatusBadRequest, "profileId and prompt are required")
		return
	}
	if req.Slides <= 0 {
		req.Slides = 8
	}
	req.Slides = min(req.Slides, 30)
	meta, err := s.Store.GetProfile(r.Context(), req.ProfileID)
	if err != nil {
		storeErr(w, err)
		return
	}
	d := &store.Deck{ProfileID: meta.ID, ProfileVersion: meta.CurrentVersion, Prompt: req.Prompt, Status: "queued"}
	if err := s.Store.SaveDeck(r.Context(), d); err != nil {
		storeErr(w, err)
		return
	}
	job, err := s.Runner.Submit(r.Context(), gen.JobType, gen.Payload{DeckID: d.ID, Slides: req.Slides}, "")
	if err != nil {
		Error(w, http.StatusInternalServerError, "enqueue failed")
		return
	}
	JSON(w, http.StatusAccepted, map[string]any{"deck": d, "job": job})
}

func (s *Server) loadScene(r *http.Request) (*store.Deck, *scene.Deck, error) {
	d, err := s.Store.GetDeck(r.Context(), r.PathValue("id"))
	if err != nil {
		return nil, nil, err
	}
	sc := &scene.Deck{}
	if len(d.Scene) > 0 {
		if err := json.Unmarshal(d.Scene, sc); err != nil {
			return nil, nil, err
		}
	}
	return d, sc, nil
}

func (s *Server) getDeck(w http.ResponseWriter, r *http.Request) {
	d, sc, err := s.loadScene(r)
	if err != nil {
		storeErr(w, err)
		return
	}
	JSON(w, http.StatusOK, map[string]any{"deck": d, "scene": sc})
}

func (s *Server) slideSVG(w http.ResponseWriter, r *http.Request) {
	_, sc, err := s.loadScene(r)
	if err != nil {
		storeErr(w, err)
		return
	}
	n, _ := strconv.Atoi(r.PathValue("n"))
	if n < 1 || n > len(sc.Slides) {
		Error(w, http.StatusNotFound, "no such slide")
		return
	}
	writeSVG(w, render.SVG(sc.Slides[n-1], sc.WidthPt, sc.HeightPt, assetURL))
}

func (s *Server) exportPPTX(w http.ResponseWriter, r *http.Request) {
	d, sc, err := s.loadScene(r)
	if err != nil {
		storeErr(w, err)
		return
	}
	if len(sc.Slides) == 0 {
		Error(w, http.StatusConflict, "deck is not ready")
		return
	}
	_, p, _, err := s.LoadProfile(r, d.ProfileID, d.ProfileVersion)
	if err != nil {
		storeErr(w, err)
		return
	}
	out, err := render.PPTX(sc, p, s.Blobs.Get)
	if err != nil {
		Error(w, http.StatusInternalServerError, "export failed")
		return
	}
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.presentationml.presentation")
	w.Header().Set("Content-Disposition", `attachment; filename="`+d.ID+`.pptx"`)
	w.Write(out)
}

func (s *Server) layoutPreview(w http.ResponseWriter, r *http.Request) {
	_, p, _, err := s.LoadProfile(r, r.PathValue("id"), 0)
	if err != nil {
		storeErr(w, err)
		return
	}
	l := p.Layout(r.PathValue("lid"))
	if l == nil {
		Error(w, http.StatusNotFound, "no such layout")
		return
	}
	writeSVG(w, render.SVG(gen.Preview(p, l), p.WidthPt, p.HeightPt, assetURL))
}

func writeSVG(w http.ResponseWriter, svg string) {
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write([]byte(svg))
}
