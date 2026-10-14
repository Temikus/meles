package ghclient

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"
)

// Transport caches GET responses by ETag and revalidates with If-None-Match.
// GitHub does not count an authorized 304 against the rate limit.
type Transport struct {
	Base http.RoundTripper
	Dir  string // empty disables caching
	// Identity scopes entries to who is asking. Not the token: installation
	// tokens rotate hourly and would empty the cache every run.
	Identity string
	// RetryWait is the first backoff for retrying a GET that got a 5xx.
	// Recursive trees on larger repos 500 intermittently. Default 1s.
	RetryWait time.Duration

	requests, notModified, uncacheable, rateUsed atomic.Int64
}

// Stats counts what the transport did.
type Stats struct {
	Requests    int64 // requests sent to GitHub
	NotModified int64 // of those, answered 304 and served from cache
	Uncacheable int64 // GETs answered 2xx without an ETag; these always count
	// RateUsed is the highest X-RateLimit-Used seen for the core quota.
	// GET /rate_limit reported 0 used for gh OAuth tokens; the headers are accurate.
	RateUsed int64
}

// Stats returns counts so far.
func (t *Transport) Stats() Stats {
	return Stats{Requests: t.requests.Load(), NotModified: t.notModified.Load(), Uncacheable: t.uncacheable.Load(), RateUsed: t.rateUsed.Load()}
}

type entry struct {
	ETag   string      `json:"etag"`
	Header http.Header `json:"header"`
	Body   []byte      `json:"body"`
}

// RoundTrip implements http.RoundTripper.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	t.requests.Add(1)
	if req.Method != http.MethodGet || t.Dir == "" {
		resp, err := t.send(base, req)
		if err == nil {
			t.noteRate(resp)
		}
		return resp, err
	}
	path := t.path(req)
	cached, haveCached := t.load(path)
	if haveCached {
		req = req.Clone(req.Context())
		req.Header.Set("If-None-Match", cached.ETag)
	}
	resp, err := t.send(base, req)
	if err != nil {
		return nil, err
	}
	t.noteRate(resp)
	switch {
	case resp.StatusCode == http.StatusNotModified && haveCached:
		t.notModified.Add(1)
		_ = resp.Body.Close()
		return replay(resp, cached), nil
	case resp.StatusCode == http.StatusOK && resp.Header.Get("ETag") != "":
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return nil, err
		}
		_ = t.store(path, entry{ETag: resp.Header.Get("ETag"), Header: resp.Header, Body: body})
		resp.Body = io.NopCloser(bytes.NewReader(body))
	case resp.StatusCode/100 == 2 && resp.Header.Get("ETag") == "":
		t.uncacheable.Add(1)
	}
	return resp, nil
}

// send retries a GET twice on 5xx with doubling backoff; other methods are not
// safe to repeat.
func (t *Transport) send(base http.RoundTripper, req *http.Request) (*http.Response, error) {
	wait := t.RetryWait
	if wait == 0 {
		wait = time.Second
	}
	for attempt := 0; ; attempt++ {
		resp, err := base.RoundTrip(req)
		if err != nil || resp.StatusCode < 500 || req.Method != http.MethodGet || attempt == 2 {
			return resp, err
		}
		_ = resp.Body.Close()
		select {
		case <-req.Context().Done():
			return nil, req.Context().Err()
		case <-time.After(wait << attempt):
		}
	}
}

func (t *Transport) noteRate(resp *http.Response) {
	if r := resp.Header.Get("X-RateLimit-Resource"); r != "" && r != "core" {
		return
	}
	used, err := strconv.ParseInt(resp.Header.Get("X-RateLimit-Used"), 10, 64)
	if err != nil {
		return
	}
	for {
		cur := t.rateUsed.Load()
		if used <= cur || t.rateUsed.CompareAndSwap(cur, used) {
			return
		}
	}
}

// replay turns a 304 into the cached 200. go-github's CheckResponse treats
// 304 as an error. Live headers win so rate-limit counts stay current.
func replay(live *http.Response, e entry) *http.Response {
	h := e.Header.Clone()
	for k, v := range live.Header {
		h[k] = v
	}
	return &http.Response{
		Status:        "200 OK",
		StatusCode:    http.StatusOK,
		Proto:         live.Proto,
		ProtoMajor:    live.ProtoMajor,
		ProtoMinor:    live.ProtoMinor,
		Header:        h,
		Body:          io.NopCloser(bytes.NewReader(e.Body)),
		ContentLength: int64(len(e.Body)),
		Request:       live.Request,
	}
}

func (t *Transport) path(req *http.Request) string {
	h := sha256.New()
	for _, s := range []string{t.Identity, req.URL.String(), req.Header.Get("Accept"), req.Header.Get("X-GitHub-Api-Version")} {
		h.Write([]byte(s))
		h.Write([]byte{0})
	}
	sum := hex.EncodeToString(h.Sum(nil))
	return filepath.Join(t.Dir, "http", sum[:2], sum)
}

func (t *Transport) load(path string) (entry, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return entry{}, false
	}
	var e entry
	if json.Unmarshal(b, &e) != nil || e.ETag == "" {
		return entry{}, false
	}
	return e, true
}

func (t *Transport) store(path string, e entry) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	return writeAtomic(path, b)
}

// writeAtomic avoids torn entries when workers write the same key.
func writeAtomic(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return err
	}
	return os.Rename(f.Name(), path)
}
