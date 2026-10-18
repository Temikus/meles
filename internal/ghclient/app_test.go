package ghclient

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func testKey(t *testing.T) []byte {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)})
}

// appServer answers the App endpoints like GitHub: JWT for /app, an
// installation token for everything else.
type appServer struct {
	mu       sync.Mutex
	installs string // JSON array for GET /app/installations
	auth     map[string]string
}

func (s *appServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.auth[r.Method+" "+r.URL.Path] = r.Header.Get("Authorization")
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.URL.Path == "/app/installations":
		fmt.Fprint(w, s.installs)
	case r.Method == http.MethodPost && r.URL.Path == "/app/installations/7/access_tokens":
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"token":"ghs_test","expires_at":"2099-01-01T00:00:00Z"}`)
	case r.URL.Path == "/repos/acme/widget":
		fmt.Fprint(w, `{"name":"widget"}`)
	default:
		http.NotFound(w, r)
	}
}

func newAppServer(t *testing.T, installs string) (*appServer, string) {
	s := &appServer{installs: installs, auth: map[string]string{}}
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	return s, srv.URL + "/"
}

func TestAppFromEnv(t *testing.T) {
	for _, tc := range []struct {
		name, id, key, err string
		want               bool
	}{
		{name: "unset"},
		{name: "both", id: "42", key: "pem", want: true},
		{name: "id only", id: "42", err: "set both"},
		{name: "key only", key: "pem", err: "set both"},
		{name: "bad id", id: "abc", key: "pem", err: "MELES_APP_ID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MELES_APP_ID", tc.id)
			t.Setenv("MELES_APP_PRIVATE_KEY", tc.key)
			app, err := AppFromEnv()
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("err = %v, want it to mention %q", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if (app != nil) != tc.want {
				t.Fatalf("app = %+v, want set: %v", app, tc.want)
			}
			if app != nil && (app.ID != 42 || string(app.Key) != "pem") {
				t.Errorf("app = %+v", app)
			}
		})
	}
}

const twoInstalls = `[
  {"id": 7, "account": {"login": "Temikus"}, "permissions": {"contents": "write", "workflows": "write"}},
  {"id": 8, "account": {"login": "acme"}, "permissions": {"contents": "read", "administration": "read"}}
]`

func TestFindInstallation(t *testing.T) {
	key := testKey(t)
	for _, tc := range []struct {
		name, installs, owner, err string
		wantID                     int64
	}{
		{name: "owner picks one", installs: twoInstalls, owner: "temikus", wantID: 7},
		{name: "org", installs: twoInstalls, owner: "acme", wantID: 8},
		{name: "only one", installs: `[{"id": 7, "account": {"login": "Temikus"}}]`, wantID: 7},
		{name: "several, no owner", installs: twoInstalls, err: "pass --owner"},
		{name: "not installed", installs: twoInstalls, owner: "other", err: "not installed on other"},
		{name: "none", installs: `[]`, err: "no installations"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, url := newAppServer(t, tc.installs)
			app := &App{ID: 42, Key: key}
			in, err := app.FindInstallation(context.Background(), tc.owner, url)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("err = %v, want it to mention %q", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if in.ID != tc.wantID {
				t.Errorf("installation = %d, want %d", in.ID, tc.wantID)
			}
			if a := s.auth["GET /app/installations"]; !strings.HasPrefix(a, "Bearer ") {
				t.Errorf("installations must be listed with the App JWT, got %q", a)
			}
		})
	}
}

func TestInstallationPermissions(t *testing.T) {
	_, url := newAppServer(t, twoInstalls)
	app := &App{ID: 42, Key: testKey(t)}
	in, err := app.FindInstallation(context.Background(), "acme", url)
	if err != nil {
		t.Fatal(err)
	}
	if in.Account != "acme" || in.Administration != "read" || in.Workflows != "" {
		t.Errorf("installation = %+v", in)
	}
}

func TestNewAppClient(t *testing.T) {
	s, url := newAppServer(t, twoInstalls)
	app := &App{ID: 42, Key: testKey(t)}
	gh, tr, err := NewApp(app, Installation{ID: 7, Account: "Temikus"}, Options{CacheDir: t.TempDir(), BaseURL: url})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := gh.Repositories.Get(context.Background(), "acme", "widget"); err != nil {
		t.Fatal(err)
	}
	if a := s.auth["GET /repos/acme/widget"]; a != "token ghs_test" {
		t.Errorf("Authorization = %q, want the installation token", a)
	}
	if tr.Identity != "app:7" {
		t.Errorf("cache identity = %q, want app:7", tr.Identity)
	}
}
