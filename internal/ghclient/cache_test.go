package ghclient

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// etagServer serves one resource per path and honours If-None-Match like GitHub.
type etagServer struct {
	mu      sync.Mutex
	version int
	hits    int
	notMod  int
	sawINM  []string
}

func (s *etagServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hits++
	etag := fmt.Sprintf(`W/"v%d"`, s.version)
	s.sawINM = append(s.sawINM, r.Header.Get("If-None-Match"))
	w.Header().Set("X-RateLimit-Remaining", fmt.Sprint(5000-s.hits))
	w.Header().Set("X-RateLimit-Used", fmt.Sprint(s.hits))
	if r.URL.Path == "/noetag" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.URL.Path == "/missing" {
		http.NotFound(w, r)
		return
	}
	if r.Header.Get("If-None-Match") == etag {
		s.notMod++
		w.Header().Set("ETag", etag)
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("ETag", etag)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Link", `<https://api.github.com/x?page=2>; rel="next"`)
	fmt.Fprintf(w, `{"name":"widget","full_name":"acme/widget-v%d"}`, s.version)
}

func newCached(t *testing.T, dir, identity string) (*http.Client, *Transport) {
	t.Helper()
	tr := &Transport{Base: http.DefaultTransport, Dir: dir, Identity: identity}
	return &http.Client{Transport: tr}, tr
}

func get(t *testing.T, c *http.Client, url string) (*http.Response, string) {
	t.Helper()
	resp, err := c.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func TestRevalidates(t *testing.T) {
	srv := &etagServer{version: 1}
	ts := httptest.NewServer(srv)
	defer ts.Close()
	c, tr := newCached(t, t.TempDir(), "user:acme")

	_, first := get(t, c, ts.URL+"/repos/acme/widget")
	resp, second := get(t, c, ts.URL+"/repos/acme/widget")

	if resp.StatusCode != http.StatusOK || second != first {
		t.Fatalf("second GET: status %d body %q, want 200 %q", resp.StatusCode, second, first)
	}
	if srv.notMod != 1 || srv.sawINM[1] != `W/"v1"` {
		t.Errorf("server saw If-None-Match %q and sent %d 304s, want W/\"v1\" and 1", srv.sawINM, srv.notMod)
	}
	if resp.Header.Get("Link") == "" {
		t.Error("cached Link header lost; go-github pagination needs it")
	}
	if resp.Header.Get("X-RateLimit-Remaining") != "4998" {
		t.Errorf("rate headers must come from the live 304, got %q", resp.Header.Get("X-RateLimit-Remaining"))
	}
	if s := tr.Stats(); s.Requests != 2 || s.NotModified != 1 || s.RateUsed != 2 {
		t.Errorf("stats = %+v", s)
	}
}

func TestRefreshesOnChange(t *testing.T) {
	srv := &etagServer{version: 1}
	ts := httptest.NewServer(srv)
	defer ts.Close()
	c, _ := newCached(t, t.TempDir(), "id")

	get(t, c, ts.URL+"/r")
	srv.version = 2
	_, body := get(t, c, ts.URL+"/r")
	_, again := get(t, c, ts.URL+"/r")
	if !strings.Contains(body, "v2") || again != body || srv.notMod != 1 {
		t.Errorf("body %q again %q 304s %d", body, again, srv.notMod)
	}
}

func TestOnlyCachesGETWith200(t *testing.T) {
	srv := &etagServer{version: 1}
	ts := httptest.NewServer(srv)
	defer ts.Close()
	c, _ := newCached(t, t.TempDir(), "id")

	for range 2 {
		resp, err := c.Post(ts.URL+"/r", "application/json", strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		get(t, c, ts.URL+"/missing")
	}
	for i, inm := range srv.sawINM {
		if inm != "" {
			t.Errorf("request %d sent If-None-Match %q; POST and 404 must not be cached", i, inm)
		}
	}
}

func TestIdentityIsolatesEntries(t *testing.T) {
	srv := &etagServer{version: 1}
	ts := httptest.NewServer(srv)
	defer ts.Close()
	dir := t.TempDir()
	a, _ := newCached(t, dir, "installation:1")
	b, _ := newCached(t, dir, "installation:2")

	get(t, a, ts.URL+"/r")
	get(t, b, ts.URL+"/r")
	if srv.sawINM[1] != "" {
		t.Error("a different identity must not reuse another identity's cache entry")
	}
}

// go-github treats 304 as an error; the transport must hide it.
func TestThroughGoGithub(t *testing.T) {
	srv := &etagServer{version: 1}
	ts := httptest.NewServer(srv)
	defer ts.Close()
	gh, tr, err := New("tok", Options{CacheDir: t.TempDir(), Identity: "user:acme", BaseURL: ts.URL + "/"})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		repo, _, err := gh.Repositories.Get(context.Background(), "acme", "widget")
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if repo.GetFullName() != "acme/widget-v1" {
			t.Errorf("call %d: full name %q", i, repo.GetFullName())
		}
	}
	if s := tr.Stats(); s.NotModified != 1 {
		t.Errorf("stats = %+v, want one 304", s)
	}
}

// GitHub answers some GETs (vulnerability-alerts) with 204 and no ETag.
func TestCountsUncacheable(t *testing.T) {
	ts := httptest.NewServer(&etagServer{version: 1})
	defer ts.Close()
	c, tr := newCached(t, t.TempDir(), "id")
	get(t, c, ts.URL+"/noetag")
	get(t, c, ts.URL+"/noetag")
	if s := tr.Stats(); s.Uncacheable != 2 || s.NotModified != 0 {
		t.Errorf("stats = %+v", s)
	}
}

// GraphQL is a POST with its own quota: never cached, counted apart.
func TestCountsGraphQL(t *testing.T) {
	ts := httptest.NewServer(&etagServer{version: 1})
	defer ts.Close()
	c, tr := newCached(t, t.TempDir(), "id")
	for range 2 {
		resp, err := c.Post(ts.URL+"/graphql", "application/json", strings.NewReader(`{"query":"{viewer{login}}"}`))
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	}
	if s := tr.Stats(); s.GraphQL != 2 || s.Requests != 2 || s.NotModified != 0 || s.Uncacheable != 0 {
		t.Errorf("stats = %+v", s)
	}
}

// Writes is how the idempotency test proves a no-drift run changed nothing.
func TestCountsWrites(t *testing.T) {
	ts := httptest.NewServer(&etagServer{version: 1})
	defer ts.Close()
	c, tr := newCached(t, t.TempDir(), "id")
	get(t, c, ts.URL+"/repo")
	for _, m := range []string{http.MethodPost, http.MethodPatch, http.MethodPut, http.MethodDelete} {
		req, _ := http.NewRequest(m, ts.URL+"/repo", strings.NewReader("{}"))
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
	}
	resp, err := c.Post(ts.URL+"/graphql", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if s := tr.Stats(); s.Writes != 4 || s.GraphQL != 1 || s.Requests != 6 {
		t.Errorf("stats = %+v, want 4 writes and 1 GraphQL query", s)
	}
}

func TestScopes(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-OAuth-Scopes", "repo, workflow, read:org")
	}))
	defer ts.Close()
	c, tr := newCached(t, t.TempDir(), "id")
	if _, known := tr.Scopes(); known {
		t.Error("scopes known before any response")
	}
	get(t, c, ts.URL+"/user")
	if s, known := tr.Scopes(); !known || !slices.Equal(s, []string{"repo", "workflow", "read:org"}) {
		t.Errorf("scopes = %v, %v", s, known)
	}
}

func TestBlobStore(t *testing.T) {
	bs := BlobStore{Dir: t.TempDir()}
	if _, ok := bs.Get("abc"); ok {
		t.Fatal("empty store hit")
	}
	if err := bs.Put("abc", []byte("hello")); err != nil {
		t.Fatal(err)
	}
	if b, ok := bs.Get("abc"); !ok || string(b) != "hello" {
		t.Errorf("Get = %q %v", b, ok)
	}
	if err := bs.Put("../escape", []byte("x")); err == nil {
		t.Error("non-hex sha must be rejected")
	}
}

func TestTokenFromEnv(t *testing.T) {
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "from-github-token")
	if tok, err := Token(context.Background()); err != nil || tok != "from-github-token" {
		t.Errorf("Token = %q, %v", tok, err)
	}
	t.Setenv("GH_TOKEN", "from-gh-token")
	if tok, _ := Token(context.Background()); tok != "from-gh-token" {
		t.Errorf("GH_TOKEN must win, got %q", tok)
	}
}

func TestRetriesServerErrors(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls < 3 {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		fmt.Fprint(w, "ok")
	}))
	defer ts.Close()
	tr := &Transport{Base: http.DefaultTransport, RetryWait: time.Millisecond}
	resp, body := get(t, &http.Client{Transport: tr}, ts.URL)
	if resp.StatusCode != http.StatusOK || body != "ok" || calls != 3 {
		t.Errorf("status %d body %q after %d calls, want 200 ok after 3", resp.StatusCode, body, calls)
	}

	calls = -10 // never recovers
	resp, _ = get(t, &http.Client{Transport: tr}, ts.URL)
	if resp.StatusCode != http.StatusInternalServerError || calls != -7 {
		t.Errorf("status %d after %d calls, want 500 after 3 attempts", resp.StatusCode, calls+10)
	}
}
