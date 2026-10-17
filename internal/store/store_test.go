package store

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// open uses SQLite, or a fresh schema in TEST_POSTGRES_DSN when set.
// (storetest.Open can't be used here: it imports this package.)
func open(t *testing.T) *Store {
	dsn := filepath.Join(t.TempDir(), "t.db")
	if pg := os.Getenv("TEST_POSTGRES_DSN"); pg != "" {
		schema := NewID("t")
		admin, err := sql.Open("pgx", pg)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`); admin.Close() })
		u, _ := url.Parse(pg)
		q := u.Query()
		q.Set("search_path", schema)
		u.RawQuery = q.Encode()
		dsn = u.String()
	}
	s, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestProfileVersions(t *testing.T) {
	s, ctx := open(t), context.Background()
	p := &Profile{ID: "p1", Name: "Acme", SourceSHA256: "abc"}
	if err := s.CreateProfile(ctx, p, []byte("<v1/>")); err != nil {
		t.Fatal(err)
	}
	v, err := s.AddProfileVersion(ctx, "p1", []byte("<v2/>"), "edit")
	if err != nil || v != 2 {
		t.Fatalf("v=%d err=%v", v, err)
	}
	x, ver, _ := s.ProfileXML(ctx, "p1", 0)
	if string(x) != "<v2/>" || ver != 2 {
		t.Errorf("current = %s v%d", x, ver)
	}
	x, _, _ = s.ProfileXML(ctx, "p1", 1)
	if string(x) != "<v1/>" {
		t.Errorf("v1 = %s", x)
	}
	if got, err := s.FindProfileBySource(ctx, "", "abc"); err != nil || got.ID != "p1" {
		t.Errorf("by source: %v %v", got, err)
	}
	if _, err := s.GetProfile(ctx, "nope"); err != ErrNotFound {
		t.Errorf("err = %v", err)
	}
}

func TestJobLifecycle(t *testing.T) {
	s, ctx := open(t), context.Background()
	j, created, err := s.Enqueue(ctx, "ingest", `{"a":1}`, "k1")
	if err != nil || !created {
		t.Fatal(err, created)
	}
	j2, created, _ := s.Enqueue(ctx, "ingest", `{"a":1}`, "k1")
	if created || j2.ID != j.ID {
		t.Fatal("idempotency key not honored")
	}
	c, err := s.Claim(ctx)
	if err != nil || c == nil || c.ID != j.ID || c.Attempts != 1 {
		t.Fatalf("claim: %+v %v", c, err)
	}
	if c2, _ := s.Claim(ctx); c2 != nil {
		t.Fatal("claimed twice")
	}
	retry, _ := s.Fail(ctx, c, "boom", true)
	if !retry {
		t.Fatal("expected retry")
	}
	if c2, _ := s.Claim(ctx); c2 != nil {
		t.Fatal("backoff ignored")
	}
	s.db.Exec(`UPDATE jobs SET run_after=0`)
	c, _ = s.Claim(ctx)
	if err := s.Complete(ctx, c.ID, `{"ok":true}`); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetJob(ctx, j.ID)
	if got.Status != JobSucceeded || got.Attempts != 2 {
		t.Errorf("job = %+v", got)
	}
}

func TestCache(t *testing.T) {
	s, ctx := open(t), context.Background()
	s.CachePut(ctx, "k", []byte("v"), time.Hour)
	if v, ok := s.CacheGet(ctx, "k"); !ok || string(v) != "v" {
		t.Fatal("miss")
	}
	s.CachePut(ctx, "old", []byte("v"), -time.Second)
	if _, ok := s.CacheGet(ctx, "old"); ok {
		t.Fatal("expired hit")
	}
	if n, _ := s.CachePrune(ctx); n != 1 {
		t.Errorf("pruned %d", n)
	}
}

func TestConcurrentClaimNoDuplicates(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	const n = 40
	for i := 0; i < n; i++ {
		if _, _, err := s.Enqueue(ctx, "noop", "{}", ""); err != nil {
			t.Fatal(err)
		}
	}
	var mu sync.Mutex
	seen := map[string]bool{}
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				j, err := s.Claim(ctx)
				if err != nil {
					t.Error(err)
					return
				}
				if j == nil {
					return
				}
				mu.Lock()
				if seen[j.ID] {
					t.Errorf("job %s claimed twice", j.ID)
				}
				seen[j.ID] = true
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(seen) != n {
		t.Fatalf("claimed %d of %d jobs", len(seen), n)
	}
}
