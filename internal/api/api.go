// Package api exposes the HTTP/JSON + SSE interface.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/ashutoshm97/curly-engine/internal/blob"
	"github.com/ashutoshm97/curly-engine/internal/gen"
	"github.com/ashutoshm97/curly-engine/internal/ingest"
	"github.com/ashutoshm97/curly-engine/internal/jobs"
	"github.com/ashutoshm97/curly-engine/internal/profile"
	"github.com/ashutoshm97/curly-engine/internal/store"
)

const MaxUpload = 50 << 20

type Server struct {
	Store  *store.Store
	Blobs  *blob.Store
	Runner *jobs.Runner
	Ingest *ingest.Service
	Gen    *gen.Service
	Signup bool // allow new accounts via POST /api/auth/signup
	mux    *http.ServeMux
}

func (s *Server) Handler() http.Handler {
	s.mux = http.NewServeMux()
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	s.mux.HandleFunc("POST /api/uploads", s.upload)
	s.mux.HandleFunc("GET /api/jobs/{id}", s.getJob)
	s.mux.HandleFunc("GET /api/jobs/{id}/events", s.jobEvents)
	s.mux.HandleFunc("GET /api/profiles", s.listProfiles)
	s.mux.HandleFunc("GET /api/profiles/{id}", s.getProfile)
	s.mux.HandleFunc("GET /api/profiles/{id}/components.xml", s.getXML)
	s.mux.HandleFunc("PUT /api/profiles/{id}/components.xml", s.putXML)
	s.mux.HandleFunc("GET /api/assets/{ref}", s.asset)
	s.authRoutes()
	s.deckRoutes()
	s.uiRoutes()
	return logging(s.authenticate(s.mux))
}

// Mux allows other packages to register routes (generation, UI).
func (s *Server) Mux() *http.ServeMux { return s.mux }

func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, MaxUpload+1<<20)
	f, hdr, err := r.FormFile("file")
	if err != nil {
		Error(w, http.StatusBadRequest, "multipart field 'file' required")
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxUpload+1))
	if err != nil || len(data) > MaxUpload {
		Error(w, http.StatusRequestEntityTooLarge, "file too large")
		return
	}
	kind, err := ingest.DetectKind(hdr.Filename, data)
	if err != nil {
		Error(w, http.StatusUnsupportedMediaType, err.Error())
		return
	}
	sha, err := s.Blobs.Put(data)
	if err != nil {
		Error(w, http.StatusInternalServerError, "store failed")
		return
	}
	up := &store.Upload{UserID: userID(r), SHA256: sha, Filename: hdr.Filename, Kind: kind, Size: int64(len(data))}
	if err := s.Store.CreateUpload(r.Context(), up); err != nil {
		Error(w, http.StatusInternalServerError, "store failed")
		return
	}
	job, err := s.Runner.Submit(r.Context(), ingest.JobType, ingest.Payload{UploadID: up.ID, Name: r.FormValue("name")}, "ingest:"+userID(r)+":"+sha)
	if err != nil {
		Error(w, http.StatusInternalServerError, "enqueue failed")
		return
	}
	JSON(w, http.StatusAccepted, map[string]any{"uploadId": up.ID, "kind": kind, "job": job})
}

func (s *Server) getJob(w http.ResponseWriter, r *http.Request) {
	j, err := s.Store.GetJob(r.Context(), r.PathValue("id"))
	if err == nil && j.UserID != userID(r) {
		err = store.ErrNotFound
	}
	if err != nil {
		storeErr(w, err)
		return
	}
	JSON(w, http.StatusOK, j)
}

// jobEvents streams job progress as Server-Sent Events until the job finishes.
func (s *Server) jobEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	events, unsub := s.Runner.Subscribe(id)
	defer unsub()
	j, err := s.Store.GetJob(r.Context(), id)
	if err == nil && j.UserID != userID(r) {
		err = store.ErrNotFound
	}
	if err != nil {
		storeErr(w, err)
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		Error(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	send := func(e jobs.Event) {
		b, _ := json.Marshal(e)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Type, b)
		fl.Flush()
	}
	switch j.Status {
	case store.JobSucceeded:
		send(jobs.Event{JobID: id, Type: "done", Data: json.RawMessage(j.Result)})
		return
	case store.JobFailed:
		send(jobs.Event{JobID: id, Type: "error", Error: j.Error})
		return
	}
	send(jobs.Event{JobID: id, Type: "stage", Stage: string(j.Status) + ":" + j.Stage})
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	poll := time.NewTicker(2 * time.Second) // covers events published before Subscribe
	defer poll.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case e := <-events:
			send(e)
			if e.Type == "done" || e.Type == "error" {
				return
			}
		case <-poll.C:
			if j, err := s.Store.GetJob(r.Context(), id); err == nil && (j.Status == store.JobSucceeded || j.Status == store.JobFailed) {
				if j.Status == store.JobSucceeded {
					send(jobs.Event{JobID: id, Type: "done", Data: json.RawMessage(j.Result)})
				} else {
					send(jobs.Event{JobID: id, Type: "error", Error: j.Error})
				}
				return
			}
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		}
	}
}

func (s *Server) listProfiles(w http.ResponseWriter, r *http.Request) {
	ps, err := s.Store.ListProfiles(r.Context(), userID(r))
	if err != nil {
		storeErr(w, err)
		return
	}
	if ps == nil {
		ps = []*store.Profile{}
	}
	JSON(w, http.StatusOK, ps)
}

// ownProfile returns the profile if it belongs to the requesting user, else ErrNotFound.
func (s *Server) ownProfile(r *http.Request, id string) (*store.Profile, error) {
	p, err := s.Store.GetProfile(r.Context(), id)
	if err == nil && p.UserID != userID(r) {
		return nil, store.ErrNotFound
	}
	return p, err
}

// LoadProfile returns metadata and the parsed components.xml for a version (0 = current).
func (s *Server) LoadProfile(r *http.Request, id string, version int) (*store.Profile, *profile.StyleProfile, int, error) {
	meta, err := s.ownProfile(r, id)
	if err != nil {
		return nil, nil, 0, err
	}
	x, v, err := s.Store.ProfileXML(r.Context(), id, version)
	if err != nil {
		return nil, nil, 0, err
	}
	p, err := profile.Parse(x)
	return meta, p, v, err
}

func (s *Server) getProfile(w http.ResponseWriter, r *http.Request) {
	v, _ := strconv.Atoi(r.URL.Query().Get("version"))
	meta, p, ver, err := s.LoadProfile(r, r.PathValue("id"), v)
	if err != nil {
		storeErr(w, err)
		return
	}
	JSON(w, http.StatusOK, map[string]any{"meta": meta, "version": ver, "profile": p})
}

func (s *Server) getXML(w http.ResponseWriter, r *http.Request) {
	v, _ := strconv.Atoi(r.URL.Query().Get("version"))
	if _, err := s.ownProfile(r, r.PathValue("id")); err != nil {
		storeErr(w, err)
		return
	}
	x, _, err := s.Store.ProfileXML(r.Context(), r.PathValue("id"), v)
	if err != nil {
		storeErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Content-Disposition", `inline; filename="components.xml"`)
	w.Write(x)
}

// putXML stores a user-edited components.xml as a new version after validation.
func (s *Server) putXML(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.ownProfile(r, id); err != nil {
		storeErr(w, err)
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		Error(w, http.StatusBadRequest, "read body")
		return
	}
	p, err := profile.Parse(data)
	if err == nil {
		err = p.Validate()
	}
	if err != nil {
		Error(w, http.StatusUnprocessableEntity, "invalid components.xml: "+err.Error())
		return
	}
	p.ID = id
	x, _ := p.MarshalIndent()
	v, err := s.Store.AddProfileVersion(r.Context(), id, x, r.URL.Query().Get("note"))
	if err != nil {
		storeErr(w, err)
		return
	}
	JSON(w, http.StatusOK, map[string]any{"id": id, "version": v})
}

func (s *Server) asset(w http.ResponseWriter, r *http.Request) {
	data, err := s.Blobs.Get(r.PathValue("ref"))
	if err != nil {
		Error(w, http.StatusNotFound, "not found")
		return
	}
	w.Header().Set("Content-Type", http.DetectContentType(data))
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Write(data)
}

func JSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func Error(w http.ResponseWriter, code int, msg string) {
	JSON(w, code, map[string]string{"error": msg})
}

func storeErr(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		Error(w, http.StatusNotFound, "not found")
		return
	}
	slog.Error("store", "err", err)
	Error(w, http.StatusInternalServerError, "internal error")
}

type statusWriter struct {
	http.ResponseWriter
	code int
}

func (s *statusWriter) WriteHeader(c int) { s.code = c; s.ResponseWriter.WriteHeader(c) }
func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func logging(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, code: 200}
		defer func() {
			if p := recover(); p != nil {
				slog.Error("panic", "path", r.URL.Path, "panic", p)
				Error(sw, http.StatusInternalServerError, "internal error")
			}
			slog.Info("http", "method", r.Method, "path", r.URL.Path, "status", sw.code, "ms", time.Since(start).Milliseconds())
		}()
		h.ServeHTTP(sw, r)
	})
}
