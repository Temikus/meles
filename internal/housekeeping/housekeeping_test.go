package housekeeping

import (
	"strings"
	"testing"

	"github.com/temikus/meles/internal/config"
)

func TestMarkerRoundTrip(t *testing.T) {
	m := Marker{Items: []Item{
		{Key: "governance.standards/codeowners", Shape: "9e28dd715a37769d", OnClose: config.Accept},
		{Key: "governance.standards/securityPolicy", Shape: "1b2c", OnClose: config.Reopen},
	}}
	body := "## meles housekeeping\n\nHuman text, maybe edited.\n\n" + m.Encode() + "\n"
	got, ok := Decode(body)
	if !ok {
		t.Fatalf("no marker in %q", body)
	}
	if len(got.Items) != 2 || got.Items[1] != m.Items[1] || got.ClosedBy != "" {
		t.Errorf("got %+v", got)
	}
	if !strings.HasPrefix(m.Encode(), "<!-- meles:v1 ") || strings.Contains(m.Encode(), "\n") {
		t.Errorf("marker must be one hidden line: %q", m.Encode())
	}
}

func TestMarkerClosedByMeles(t *testing.T) {
	m := Marker{ClosedBy: ClosedByMeles}
	got, ok := Decode("text " + m.Encode())
	if !ok || got.ClosedBy != ClosedByMeles {
		t.Errorf("got %+v, %v", got, ok)
	}
}

func TestDecodeRejects(t *testing.T) {
	for _, body := range []string{
		"",
		"a PR someone opened by hand from the same branch",
		"<!-- meles:v1 {not json} -->",
		"<!-- meles:v2 {\"items\":[]} -->",
		"<!-- meles:v1 {\"items\":[]}", // truncated by an edit
	} {
		if m, ok := Decode(body); ok {
			t.Errorf("Decode(%q) = %+v, want no marker", body, m)
		}
	}
}

func TestBlobSHAMatchesGit(t *testing.T) {
	// printf 'hello\n' | git hash-object --stdin
	if got := BlobSHA([]byte("hello\n")); got != "ce013625030ba8dba906f756967f9e9ca394464a" {
		t.Errorf("BlobSHA = %s", got)
	}
}

func TestHasTrailer(t *testing.T) {
	msg := "chore: meles housekeeping\n\nAdds CODEOWNERS.\n\n" + Trailer + "\n"
	if !HasTrailer(msg) {
		t.Error("trailer not found")
	}
	if HasTrailer("fix: a human commit mentioning " + Trailer + " in prose") {
		t.Error("trailer must be its own line")
	}
}
