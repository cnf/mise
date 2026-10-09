package xdg

import (
	"path/filepath"
	"testing"
)

func TestXDGVariablesAreHonoured(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/srv/data")
	t.Setenv("XDG_CONFIG_HOME", "/srv/conf")
	if got, want := DBPath(), "/srv/data/mise/mise.db"; got != want {
		t.Errorf("DBPath = %q, want %q", got, want)
	}
	if got, want := APIKeyPath(), "/srv/conf/mise/partdb_apikey"; got != want {
		t.Errorf("APIKeyPath = %q, want %q", got, want)
	}
}

// The spec says a relative or empty value must be ignored, not used.
func TestXDGIgnoresRelativeAndEmpty(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	for _, v := range []string{"", "relative/path"} {
		t.Setenv("XDG_DATA_HOME", v)
		t.Setenv("XDG_CONFIG_HOME", v)
		if got, want := DBPath(), filepath.FromSlash("/home/u/.local/share/mise/mise.db"); got != want {
			t.Errorf("XDG_DATA_HOME=%q: DBPath = %q, want %q", v, got, want)
		}
		if got, want := APIKeyPath(), filepath.FromSlash("/home/u/.config/mise/partdb_apikey"); got != want {
			t.Errorf("XDG_CONFIG_HOME=%q: APIKeyPath = %q, want %q", v, got, want)
		}
	}
}
