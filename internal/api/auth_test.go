package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ashutoshm97/curly-engine/internal/blob"
	"github.com/ashutoshm97/curly-engine/internal/jobs"
	"github.com/ashutoshm97/curly-engine/internal/store"
	"github.com/ashutoshm97/curly-engine/internal/store/storetest"
)

func newTestServer(t *testing.T, signup bool) (*httptest.Server, *store.Store) {
	dir := t.TempDir()
	st := storetest.Open(t)
	blobs, _ := blob.New(filepath.Join(dir, "blobs"))
	srv := &Server{Store: st, Blobs: blobs, Runner: jobs.NewRunner(st, 1), Signup: signup}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, st
}

func newClient() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar}
}

func call(t *testing.T, c *http.Client, method, url, body string, hdr ...string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestAuthAndIsolation(t *testing.T) {
	ts, st := newTestServer(t, true)
	ctx := context.Background()
	a, b, anon := newClient(), newClient(), newClient()

	if code, _ := call(t, anon, "GET", ts.URL+"/api/profiles", ""); code != 401 {
		t.Fatalf("anon profiles = %d", code)
	}
	if code, _ := call(t, anon, "GET", ts.URL+"/", ""); code != 200 {
		t.Fatalf("UI should be public, got %d", code)
	}
	if code, _ := call(t, a, "POST", ts.URL+"/api/auth/signup", `{"email":"a@x.io","password":"short"}`); code != 400 {
		t.Fatalf("short password = %d", code)
	}
	code, body := call(t, a, "POST", ts.URL+"/api/auth/signup", `{"email":"A@x.io","password":"password-a"}`)
	if code != 201 {
		t.Fatalf("signup a = %d %s", code, body)
	}
	var me struct{ User store.User }
	json.Unmarshal([]byte(body), &me)
	if code, _ := call(t, b, "POST", ts.URL+"/api/auth/signup", `{"email":"a@x.io","password":"password-b"}`); code != 409 {
		t.Fatalf("duplicate email = %d", code)
	}
	if code, _ := call(t, b, "POST", ts.URL+"/api/auth/signup", `{"email":"b@x.io","password":"password-b"}`); code != 201 {
		t.Fatalf("signup b = %d", code)
	}

	// Data owned by a.
	if err := st.CreateProfile(ctx, &store.Profile{ID: "pf_a", UserID: me.User.ID, Name: "A"}, []byte("<profile/>")); err != nil {
		t.Fatal(err)
	}
	st.SaveDeck(ctx, &store.Deck{ID: "dk_a", UserID: me.User.ID, ProfileID: "pf_a", ProfileVersion: 1, Status: "done"})
	j, _, _ := st.Enqueue(store.WithUser(ctx, me.User.ID), "noop", "{}", "")

	for _, path := range []string{"/api/profiles/pf_a/components.xml", "/api/decks/dk_a", "/api/jobs/" + j.ID} {
		if code, _ := call(t, a, "GET", ts.URL+path, ""); code != 200 {
			t.Errorf("owner GET %s = %d", path, code)
		}
		if code, _ := call(t, b, "GET", ts.URL+path, ""); code != 404 {
			t.Errorf("other user GET %s = %d, want 404", path, code)
		}
	}
	if code, _ := call(t, b, "PUT", ts.URL+"/api/profiles/pf_a/components.xml", "<x/>"); code != 404 {
		t.Errorf("other user PUT xml = %d", code)
	}
	if code, _ := call(t, b, "POST", ts.URL+"/api/decks", `{"profileId":"pf_a","prompt":"x"}`); code != 404 {
		t.Errorf("other user deck from foreign profile = %d", code)
	}
	if _, body := call(t, b, "GET", ts.URL+"/api/profiles", ""); strings.Contains(body, "pf_a") {
		t.Errorf("b sees a's profile: %s", body)
	}

	// CSRF: cookie-authenticated cross-origin write is rejected.
	if code, _ := call(t, a, "POST", ts.URL+"/api/tokens", `{"name":"x"}`, "Origin", "https://evil.example"); code != 403 {
		t.Errorf("cross-origin write = %d", code)
	}
	// API token.
	_, body = call(t, a, "POST", ts.URL+"/api/tokens", `{"name":"ci"}`)
	var tk struct{ Token string }
	json.Unmarshal([]byte(body), &tk)
	if code, body := call(t, anon, "GET", ts.URL+"/api/profiles", "", "Authorization", "Bearer "+tk.Token); code != 200 || !strings.Contains(body, "pf_a") {
		t.Errorf("token auth = %d %s", code, body)
	}
	if code, _ := call(t, anon, "GET", ts.URL+"/api/profiles", "", "Authorization", "Bearer se_bogus"); code != 401 {
		t.Errorf("bogus token = %d", code)
	}

	// Logout invalidates the session; login restores access.
	call(t, a, "POST", ts.URL+"/api/auth/logout", "")
	if code, _ := call(t, a, "GET", ts.URL+"/api/profiles", ""); code != 401 {
		t.Errorf("after logout = %d", code)
	}
	if code, _ := call(t, a, "POST", ts.URL+"/api/auth/login", `{"email":"a@x.io","password":"wrong-password"}`); code != 401 {
		t.Errorf("wrong password = %d", code)
	}
	if code, _ := call(t, a, "POST", ts.URL+"/api/auth/login", `{"email":"a@x.io","password":"password-a"}`); code != 200 {
		t.Errorf("login = %d", code)
	}
	if code, _ := call(t, a, "GET", ts.URL+"/api/profiles", ""); code != 200 {
		t.Errorf("after login = %d", code)
	}
}

func TestSignupDisabled(t *testing.T) {
	ts, _ := newTestServer(t, false)
	if code, _ := call(t, newClient(), "POST", ts.URL+"/api/auth/signup", `{"email":"a@x.io","password":"password-a"}`); code != 403 {
		t.Fatalf("signup disabled = %d", code)
	}
}
