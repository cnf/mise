// Package xdg resolves where mise keeps its files, following the XDG Base
// Directory specification: data under $XDG_DATA_HOME, configuration (the
// Part-DB key) under $XDG_CONFIG_HOME. Per the spec, a variable that is unset,
// empty or not an absolute path is ignored in favour of the default.
package xdg

import (
	"os"
	"path/filepath"
)

const app = "mise"

func home(env string, fallback ...string) string {
	if v := os.Getenv(env); filepath.IsAbs(v) {
		return v
	}
	h, err := os.UserHomeDir()
	if err != nil || h == "" {
		h = "."
	}
	return filepath.Join(append([]string{h}, fallback...)...)
}

// DataHome is $XDG_DATA_HOME, default ~/.local/share.
func DataHome() string { return home("XDG_DATA_HOME", ".local", "share") }

// ConfigHome is $XDG_CONFIG_HOME, default ~/.config.
func ConfigHome() string { return home("XDG_CONFIG_HOME", ".config") }

// DBPath is the default database: $XDG_DATA_HOME/mise/mise.db.
func DBPath() string { return filepath.Join(DataHome(), app, "mise.db") }

// APIKeyPath is the default Part-DB key file: $XDG_CONFIG_HOME/mise/partdb_apikey.
func APIKeyPath() string { return filepath.Join(ConfigHome(), app, "partdb_apikey") }
