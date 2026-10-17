package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/ashutoshm97/curly-engine/internal/store"
)

const (
	sessionCookie = "se_session"
	sessionTTL    = 30 * 24 * time.Hour
)

// dummyHash keeps login timing similar for unknown emails.
var dummyHash = sync.OnceValue(func() []byte {
	h, _ := bcrypt.GenerateFromPassword([]byte("not-a-real-password"), bcrypt.DefaultCost)
	return h
})

func (s *Server) authRoutes() {
	s.mux.HandleFunc("POST /api/auth/signup", s.signup)
	s.mux.HandleFunc("POST /api/auth/login", s.login)
	s.mux.HandleFunc("POST /api/auth/logout", s.logout)
	s.mux.HandleFunc("GET /api/auth/me", s.me)
	s.mux.HandleFunc("POST /api/tokens", s.createToken)
}

func userID(r *http.Request) string { return store.UserFrom(r.Context()) }

func isPublic(path string) bool {
	return !strings.HasPrefix(path, "/api/") || path == "/api/auth/signup" || path == "/api/auth/login"
}

// credential returns the bearer secret from the Authorization header or the session cookie.
func credential(r *http.Request) (tok string, viaCookie bool) {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(h[len("Bearer "):]), false
	}
	if c, err := r.Cookie(sessionCookie); err == nil {
		return c.Value, true
	}
	return "", false
}

func sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	u, err := url.Parse(o)
	return err == nil && u.Host == r.Host
}

// authenticate requires a valid session or API token for /api/* (except signup/login)
// and puts the user ID in the request context. Cookie-authenticated writes must be
// same-origin (CSRF defence on top of SameSite=Lax).
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isPublic(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		tok, viaCookie := credential(r)
		if tok == "" {
			Error(w, http.StatusUnauthorized, "login required")
			return
		}
		u, _, err := s.Store.SessionUser(r.Context(), tok)
		if err != nil {
			Error(w, http.StatusUnauthorized, "login required")
			return
		}
		if viaCookie && r.Method != http.MethodGet && r.Method != http.MethodHead && !sameOrigin(r) {
			Error(w, http.StatusForbidden, "cross-origin request rejected")
			return
		}
		next.ServeHTTP(w, r.WithContext(store.WithUser(r.Context(), u.ID)))
	})
}

type credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func readCredentials(w http.ResponseWriter, r *http.Request) (credentials, bool) {
	var c credentials
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&c); err != nil {
		Error(w, http.StatusBadRequest, "invalid JSON")
		return c, false
	}
	c.Email = strings.TrimSpace(c.Email)
	if !strings.Contains(c.Email, "@") || len(c.Email) > 254 {
		Error(w, http.StatusBadRequest, "valid email required")
		return c, false
	}
	if len(c.Password) < 8 || len(c.Password) > 72 {
		Error(w, http.StatusBadRequest, "password must be 8-72 characters")
		return c, false
	}
	return c, true
}

func (s *Server) signup(w http.ResponseWriter, r *http.Request) {
	if !s.Signup {
		Error(w, http.StatusForbidden, "signup is disabled")
		return
	}
	c, ok := readCredentials(w, r)
	if !ok {
		return
	}
	h, err := bcrypt.GenerateFromPassword([]byte(c.Password), bcrypt.DefaultCost)
	if err != nil {
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	u, err := s.Store.CreateUser(r.Context(), c.Email, string(h))
	if errors.Is(err, store.ErrExists) {
		Error(w, http.StatusConflict, "email already registered")
		return
	}
	if err != nil {
		storeErr(w, err)
		return
	}
	s.startSession(w, r, u, http.StatusCreated)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	c, ok := readCredentials(w, r)
	if !ok {
		return
	}
	u, err := s.Store.UserByEmail(r.Context(), c.Email)
	if err != nil {
		bcrypt.CompareHashAndPassword(dummyHash(), []byte(c.Password))
		Error(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PwHash), []byte(c.Password)) != nil {
		Error(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	s.startSession(w, r, u, http.StatusOK)
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, u *store.User, code int) {
	tok, err := s.Store.CreateSession(r.Context(), u.ID, store.KindSession, "", sessionTTL)
	if err != nil {
		storeErr(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: tok, Path: "/", MaxAge: int(sessionTTL.Seconds()),
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
	})
	JSON(w, code, map[string]any{"user": u})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	tok, _ := credential(r)
	if err := s.Store.DeleteSession(r.Context(), tok); err != nil {
		storeErr(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	JSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	tok, _ := credential(r)
	u, kind, err := s.Store.SessionUser(r.Context(), tok)
	if err != nil {
		storeErr(w, err)
		return
	}
	JSON(w, http.StatusOK, map[string]any{"user": u, "auth": kind})
}

// createToken issues a non-expiring API token for scripts (Authorization: Bearer <token>).
// The secret is returned once; only its hash is stored.
func (s *Server) createToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if r.ContentLength > 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
			Error(w, http.StatusBadRequest, "invalid JSON")
			return
		}
	}
	tok, err := s.Store.CreateSession(r.Context(), userID(r), store.KindToken, req.Name, 0)
	if err != nil {
		storeErr(w, err)
		return
	}
	JSON(w, http.StatusCreated, map[string]string{"token": tok, "name": req.Name})
}
