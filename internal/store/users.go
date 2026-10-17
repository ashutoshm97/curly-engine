package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

var ErrExists = errors.New("already exists")

type User struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	PwHash    string `json:"-"`
	CreatedAt int64  `json:"createdAt"`
}

type userKey struct{}

// WithUser attaches the authenticated user ID to ctx; Enqueue records it as the job owner.
func WithUser(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, userKey{}, id)
}

func UserFrom(ctx context.Context) string {
	id, _ := ctx.Value(userKey{}).(string)
	return id
}

func (s *Store) CreateUser(ctx context.Context, email, pwHash string) (*User, error) {
	u := &User{ID: NewID("usr"), Email: strings.ToLower(strings.TrimSpace(email)), PwHash: pwHash, CreatedAt: now()}
	_, err := s.db.ExecContext(ctx, `INSERT INTO users (id,email,pw_hash,created_at) VALUES (?,?,?,?)`, u.ID, u.Email, u.PwHash, u.CreatedAt)
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "unique") {
		return nil, ErrExists
	}
	return u, err
}

func (s *Store) UserByEmail(ctx context.Context, email string) (*User, error) {
	u := &User{}
	err := s.db.QueryRowContext(ctx, `SELECT id, email, pw_hash, created_at FROM users WHERE email=?`, strings.ToLower(strings.TrimSpace(email))).
		Scan(&u.ID, &u.Email, &u.PwHash, &u.CreatedAt)
	return u, notFound(err)
}

// Session kinds: browser cookie sessions expire; API tokens do not.
const (
	KindSession = "session"
	KindToken   = "token"
)

func hashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

// CreateSession returns a new random bearer secret; only its SHA-256 is stored.
func (s *Store) CreateSession(ctx context.Context, userID, kind, name string, ttl time.Duration) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	tok := "se_" + hex.EncodeToString(b)
	exp := int64(1 << 62)
	if ttl > 0 {
		exp = now() + ttl.Milliseconds()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions (token_hash,user_id,kind,name,expires_at,created_at) VALUES (?,?,?,?,?,?)`,
		hashToken(tok), userID, kind, name, exp, now())
	return tok, err
}

// SessionUser resolves a bearer secret to its user if it has not expired.
func (s *Store) SessionUser(ctx context.Context, token string) (*User, string, error) {
	u := &User{}
	var kind string
	err := s.db.QueryRowContext(ctx, `SELECT u.id, u.email, u.created_at, s.kind FROM sessions s JOIN users u ON u.id=s.user_id
		WHERE s.token_hash=? AND s.expires_at>?`, hashToken(token), now()).Scan(&u.ID, &u.Email, &u.CreatedAt, &kind)
	return u, kind, notFound(err)
}

func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash=?`, hashToken(token))
	return err
}
