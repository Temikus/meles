// Package housekeeping holds what meles writes into its PRs and commits so a
// later run can read them back: the PR marker and the commit trailer.
package housekeeping

import (
	"crypto/sha1" //nolint:gosec // git object ids are SHA-1; not used for security
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/temikus/meles/internal/config"
)

// Trailer marks a commit meles made. A branch head without it has a human
// commit on top, and meles must not force-push over it.
const Trailer = "Meles-Housekeeping: v1"

// ClosedByMeles marks a PR meles closed itself, so the close is not read as a
// human accepting the drift.
const ClosedByMeles = "meles"

// NoteMarker starts the comment meles leaves on a PR a human closed, so it
// is posted once.
const NoteMarker = "<!-- meles:closed-note -->"

const (
	markerOpen  = "<!-- meles:v1 "
	markerClose = " -->"
)

// Item is one finding a PR carried.
type Item struct {
	Key     string         `json:"key"`
	Shape   string         `json:"shape"`
	OnClose config.OnClose `json:"on_close"`
}

// Marker is the hidden record at the end of a meles PR body. GitHub keeps it,
// so meles needs no state of its own.
type Marker struct {
	Items    []Item `json:"items,omitempty"`
	ClosedBy string `json:"closed_by,omitempty"`
}

// Encode renders the marker as one HTML comment line.
func (m Marker) Encode() string {
	b, _ := json.Marshal(m) // cannot fail: strings and slices only
	return markerOpen + string(b) + markerClose
}

// Decode finds the marker in a PR body. A body without a valid v1 marker is
// not a meles PR.
func Decode(body string) (Marker, bool) {
	_, rest, ok := strings.Cut(body, markerOpen)
	if !ok {
		return Marker{}, false
	}
	payload, _, ok := strings.Cut(rest, markerClose)
	if !ok {
		return Marker{}, false
	}
	var m Marker
	if err := json.Unmarshal([]byte(payload), &m); err != nil {
		return Marker{}, false
	}
	return m, true
}

// BlobSHA is the git object id GitHub reports for a file with this content.
func BlobSHA(content []byte) string {
	h := sha1.New() //nolint:gosec // see import
	fmt.Fprintf(h, "blob %d\x00", len(content))
	h.Write(content)
	return hex.EncodeToString(h.Sum(nil))
}

// HasTrailer reports whether a commit message carries Trailer on its own line.
func HasTrailer(msg string) bool {
	for _, l := range strings.Split(msg, "\n") {
		if strings.TrimSpace(l) == Trailer {
			return true
		}
	}
	return false
}
