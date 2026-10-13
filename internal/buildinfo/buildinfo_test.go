package buildinfo

import "testing"

func TestGetLdflagsWin(t *testing.T) {
	old := [3]string{Version, Commit, Date}
	t.Cleanup(func() { Version, Commit, Date = old[0], old[1], old[2] })
	Version, Commit, Date = "v1.0.0", "abc", "2026-10-14"

	if got, want := Get(), (Info{"v1.0.0", "abc", "2026-10-14"}); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestGetDefaults(t *testing.T) {
	// Test binaries carry no ldflags and report "(devel)" as the main module version.
	if got := Get(); got.Version != "dev" || got.Commit == "" || got.Date == "" {
		t.Errorf("got %+v, want dev version and non-empty commit and date", got)
	}
}
