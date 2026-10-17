// Package store persists uploads, profiles, decks, jobs and cache entries in
// SQLite (WAL mode). The exported methods are the seam for a Postgres backend.
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("not found")

const schema = `
CREATE TABLE IF NOT EXISTS uploads (
  id TEXT PRIMARY KEY, sha256 TEXT NOT NULL, filename TEXT NOT NULL, kind TEXT NOT NULL,
  size INTEGER NOT NULL, created_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS profiles (
  id TEXT PRIMARY KEY, name TEXT NOT NULL, upload_id TEXT, source_sha256 TEXT, source_type TEXT,
  current_version INTEGER NOT NULL DEFAULT 1, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS profiles_source ON profiles(source_sha256);
CREATE TABLE IF NOT EXISTS profile_versions (
  profile_id TEXT NOT NULL REFERENCES profiles(id) ON DELETE CASCADE, version INTEGER NOT NULL,
  xml TEXT NOT NULL, note TEXT, created_at INTEGER NOT NULL, PRIMARY KEY (profile_id, version));
CREATE TABLE IF NOT EXISTS decks (
  id TEXT PRIMARY KEY, profile_id TEXT NOT NULL, profile_version INTEGER NOT NULL, title TEXT,
  prompt TEXT, status TEXT NOT NULL, scene TEXT, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS jobs (
  id TEXT PRIMARY KEY, type TEXT NOT NULL, payload TEXT NOT NULL, status TEXT NOT NULL, stage TEXT,
  result TEXT, error TEXT, attempts INTEGER NOT NULL DEFAULT 0, max_attempts INTEGER NOT NULL DEFAULT 3,
  idem_key TEXT UNIQUE, run_after INTEGER NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS jobs_ready ON jobs(status, run_after);
CREATE TABLE IF NOT EXISTS cache (key TEXT PRIMARY KEY, value BLOB NOT NULL, expires_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS users (
  id TEXT PRIMARY KEY, email TEXT NOT NULL UNIQUE, pw_hash TEXT NOT NULL, created_at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS sessions (
  token_hash TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind TEXT NOT NULL, name TEXT, expires_at INTEGER NOT NULL, created_at INTEGER NOT NULL);
`

// ownedTables get a user_id column; added by ALTER so pre-auth databases migrate in place.
var ownedTables = []string{"uploads", "profiles", "decks", "jobs"}

type Store struct{ db dbx }

// Open connects to Postgres when dsn is a postgres:// URL, otherwise opens (or
// creates) the SQLite file at dsn, and applies the schema.
func Open(dsn string) (*Store, error) {
	if IsPostgres(dsn) {
		db, err := sql.Open("pgx", dsn)
		if err != nil {
			return nil, err
		}
		db.SetMaxOpenConns(20)
		s := &Store{db: dbx{db, true}}
		if err := s.migrate(); err != nil {
			db.Close()
			return nil, fmt.Errorf("migrate: %w", err)
		}
		return s, nil
	}
	path := dsn
	dsn = fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite allows one writer; a single connection avoids SQLITE_BUSY churn and is fast with WAL.
	db.SetMaxOpenConns(1)
	s := &Store{db: dbx{db, false}}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *Store) migrate() error {
	db := s.db.DB
	if s.db.pg {
		if _, err := db.Exec(pgSchema()); err != nil {
			return err
		}
		for _, t := range ownedTables {
			if _, err := db.Exec(`ALTER TABLE ` + t + ` ADD COLUMN IF NOT EXISTS user_id TEXT`); err != nil {
				return err
			}
		}
		_, err := db.Exec(`CREATE INDEX IF NOT EXISTS profiles_user ON profiles(user_id, updated_at)`)
		return err
	}
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	for _, t := range ownedTables {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name='user_id'`, t).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			if _, err := db.Exec(`ALTER TABLE ` + t + ` ADD COLUMN user_id TEXT`); err != nil {
				return err
			}
		}
	}
	_, err := db.Exec(`CREATE INDEX IF NOT EXISTS profiles_user ON profiles(user_id, updated_at)`)
	return err
}

func (s *Store) Close() error { return s.db.Close() }

func NewID(prefix string) string {
	b := make([]byte, 8)
	rand.Read(b)
	return prefix + "_" + hex.EncodeToString(b)
}

func now() int64 { return time.Now().UnixMilli() }

type Upload struct {
	ID, UserID, SHA256, Filename, Kind string
	Size                               int64
	CreatedAt                          int64
}

func (s *Store) CreateUpload(ctx context.Context, u *Upload) error {
	if u.ID == "" {
		u.ID = NewID("up")
	}
	u.CreatedAt = now()
	_, err := s.db.ExecContext(ctx, `INSERT INTO uploads (id,sha256,filename,kind,size,created_at,user_id) VALUES (?,?,?,?,?,?,?)`,
		u.ID, u.SHA256, u.Filename, u.Kind, u.Size, u.CreatedAt, u.UserID)
	return err
}

func (s *Store) GetUpload(ctx context.Context, id string) (*Upload, error) {
	u := &Upload{}
	err := s.db.QueryRowContext(ctx, `SELECT id, sha256, filename, kind, size, created_at, COALESCE(user_id,'') FROM uploads WHERE id=?`, id).
		Scan(&u.ID, &u.SHA256, &u.Filename, &u.Kind, &u.Size, &u.CreatedAt, &u.UserID)
	return u, notFound(err)
}

type Profile struct {
	ID             string `json:"id"`
	UserID         string `json:"-"`
	Name           string `json:"name"`
	UploadID       string `json:"uploadId,omitempty"`
	SourceSHA256   string `json:"sourceSha256,omitempty"`
	SourceType     string `json:"sourceType,omitempty"`
	CurrentVersion int    `json:"version"`
	CreatedAt      int64  `json:"createdAt"`
	UpdatedAt      int64  `json:"updatedAt"`
}

// CreateProfile inserts a profile with its first components.xml version.
func (s *Store) CreateProfile(ctx context.Context, p *Profile, xml []byte) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	p.CreatedAt, p.UpdatedAt, p.CurrentVersion = now(), now(), 1
	if _, err := tx.ExecContext(ctx, `INSERT INTO profiles (id,name,upload_id,source_sha256,source_type,current_version,created_at,updated_at,user_id) VALUES (?,?,?,?,?,?,?,?,?)`,
		p.ID, p.Name, p.UploadID, p.SourceSHA256, p.SourceType, 1, p.CreatedAt, p.UpdatedAt, p.UserID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO profile_versions VALUES (?,?,?,?,?)`, p.ID, 1, string(xml), "extracted", p.CreatedAt); err != nil {
		return err
	}
	return tx.Commit()
}

// AddProfileVersion stores an edited components.xml and makes it current.
func (s *Store) AddProfileVersion(ctx context.Context, id string, xml []byte, note string) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var v int
	if err := tx.QueryRowContext(ctx, `SELECT current_version FROM profiles WHERE id=?`, id).Scan(&v); err != nil {
		return 0, notFound(err)
	}
	var top int
	tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM profile_versions WHERE profile_id=?`, id).Scan(&top)
	v = top + 1
	if _, err := tx.ExecContext(ctx, `INSERT INTO profile_versions VALUES (?,?,?,?,?)`, id, v, string(xml), note, now()); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE profiles SET current_version=?, updated_at=? WHERE id=?`, v, now(), id); err != nil {
		return 0, err
	}
	return v, tx.Commit()
}

const profileCols = `id, name, COALESCE(upload_id,''), COALESCE(source_sha256,''), COALESCE(source_type,''), current_version, created_at, updated_at, COALESCE(user_id,'')`

func scanProfile(r interface{ Scan(...any) error }) (*Profile, error) {
	p := &Profile{}
	err := r.Scan(&p.ID, &p.Name, &p.UploadID, &p.SourceSHA256, &p.SourceType, &p.CurrentVersion, &p.CreatedAt, &p.UpdatedAt, &p.UserID)
	return p, err
}

func (s *Store) GetProfile(ctx context.Context, id string) (*Profile, error) {
	p, err := scanProfile(s.db.QueryRowContext(ctx, `SELECT `+profileCols+` FROM profiles WHERE id=?`, id))
	return p, notFound(err)
}

// FindProfileBySource returns the user's profile already extracted from identical bytes (dedupe).
func (s *Store) FindProfileBySource(ctx context.Context, userID, sha string) (*Profile, error) {
	p, err := scanProfile(s.db.QueryRowContext(ctx, `SELECT `+profileCols+` FROM profiles WHERE source_sha256=? AND COALESCE(user_id,'')=? ORDER BY created_at LIMIT 1`, sha, userID))
	return p, notFound(err)
}

func (s *Store) ListProfiles(ctx context.Context, userID string) ([]*Profile, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+profileCols+` FROM profiles WHERE user_id=? ORDER BY updated_at DESC LIMIT 200`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Profile
	for rows.Next() {
		p, err := scanProfile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ProfileXML returns components.xml for a version (0 = current).
func (s *Store) ProfileXML(ctx context.Context, id string, version int) ([]byte, int, error) {
	if version == 0 {
		if err := s.db.QueryRowContext(ctx, `SELECT current_version FROM profiles WHERE id=?`, id).Scan(&version); err != nil {
			return nil, 0, notFound(err)
		}
	}
	var x string
	err := s.db.QueryRowContext(ctx, `SELECT xml FROM profile_versions WHERE profile_id=? AND version=?`, id, version).Scan(&x)
	return []byte(x), version, notFound(err)
}

type Deck struct {
	ID             string `json:"id"`
	UserID         string `json:"-"`
	ProfileID      string `json:"profileId"`
	ProfileVersion int    `json:"profileVersion"`
	Title          string `json:"title"`
	Prompt         string `json:"prompt"`
	Status         string `json:"status"`
	Scene          []byte `json:"-"`
	CreatedAt      int64  `json:"createdAt"`
	UpdatedAt      int64  `json:"updatedAt"`
}

func (s *Store) SaveDeck(ctx context.Context, d *Deck) error {
	if d.ID == "" {
		d.ID = NewID("dk")
	}
	if d.CreatedAt == 0 {
		d.CreatedAt = now()
	}
	d.UpdatedAt = now()
	_, err := s.db.ExecContext(ctx, `INSERT INTO decks (id,profile_id,profile_version,title,prompt,status,scene,created_at,updated_at,user_id) VALUES (?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET title=excluded.title, status=excluded.status, scene=excluded.scene, updated_at=excluded.updated_at`,
		d.ID, d.ProfileID, d.ProfileVersion, d.Title, d.Prompt, d.Status, string(d.Scene), d.CreatedAt, d.UpdatedAt, d.UserID)
	return err
}

func (s *Store) GetDeck(ctx context.Context, id string) (*Deck, error) {
	d := &Deck{}
	var scene sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT id, profile_id, profile_version, COALESCE(title,''), COALESCE(prompt,''), status, scene, created_at, updated_at, COALESCE(user_id,'') FROM decks WHERE id=?`, id).
		Scan(&d.ID, &d.ProfileID, &d.ProfileVersion, &d.Title, &d.Prompt, &d.Status, &scene, &d.CreatedAt, &d.UpdatedAt, &d.UserID)
	d.Scene = []byte(scene.String)
	return d, notFound(err)
}

// CacheGet returns a non-expired cache value.
func (s *Store) CacheGet(ctx context.Context, key string) ([]byte, bool) {
	var v []byte
	err := s.db.QueryRowContext(ctx, `SELECT value FROM cache WHERE key=? AND expires_at>?`, key, now()).Scan(&v)
	return v, err == nil
}

func (s *Store) CachePut(ctx context.Context, key string, v []byte, ttl time.Duration) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO cache VALUES (?,?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value, expires_at=excluded.expires_at`,
		key, v, now()+ttl.Milliseconds())
	return err
}

func (s *Store) CachePrune(ctx context.Context) (int64, error) {
	r, err := s.db.ExecContext(ctx, `DELETE FROM cache WHERE expires_at<=?`, now())
	if err != nil {
		return 0, err
	}
	return r.RowsAffected()
}

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
