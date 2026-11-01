package inventory

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/temikus/meles/internal/ghclient"
)

func TestListInstallation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/installation/repositories" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") == "2" {
			fmt.Fprint(w, `{"total_count": 2, "repositories": [{"name": "b", "owner": {"login": "acme"}, "archived": true}]}`)
			return
		}
		w.Header().Set("Link", fmt.Sprintf(`<http://%s/installation/repositories?page=2>; rel="next"`, r.Host))
		fmt.Fprint(w, `{"total_count": 2, "repositories": [{"name": "a", "owner": {"login": "acme"}, "visibility": "private", "default_branch": "main"}]}`)
	}))
	defer srv.Close()
	gh, _, err := ghclient.New("ghs_test", ghclient.Options{BaseURL: srv.URL + "/"})
	if err != nil {
		t.Fatal(err)
	}

	metas, err := ListInstallation(context.Background(), gh, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 2 {
		t.Fatalf("got %d repos, want 2 across both pages: %+v", len(metas), metas)
	}
	if m := metas[0]; m.FullName() != "acme/a" || m.Visibility != "private" || m.DefaultBranch != "main" || !m.Admin {
		t.Errorf("first = %+v", m)
	}
	if !metas[1].Archived {
		t.Errorf("second = %+v, want archived", metas[1])
	}

	metas, err = ListInstallation(context.Background(), gh, false)
	if err != nil {
		t.Fatal(err)
	}
	if metas[0].Admin {
		t.Error("Admin must follow the App's Administration permission")
	}
}
