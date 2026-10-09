package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

const schema = `
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS project (
  id         INTEGER PRIMARY KEY,
  name       TEXT NOT NULL,
  owner_id   INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_project_name ON project(name);

CREATE TABLE IF NOT EXISTS part (
  id            INTEGER PRIMARY KEY,
  name          TEXT NOT NULL,
  kind          TEXT NOT NULL DEFAULT 'other',
  description   TEXT NOT NULL DEFAULT '',
  unit_price    REAL,
  currency      TEXT NOT NULL DEFAULT 'EUR',
  vendor_url    TEXT NOT NULL DEFAULT '',
  datasheet_url TEXT NOT NULL DEFAULT '',
  partdb_id     INTEGER,
  attrs         TEXT NOT NULL DEFAULT '{}',
  notes         TEXT NOT NULL DEFAULT '',
  owner_id      INTEGER NOT NULL DEFAULT 1,
  created_at    TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at    TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_part_name ON part(name);

CREATE TABLE IF NOT EXISTS assembly (
  id          INTEGER PRIMARY KEY,
  name        TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  project_id  INTEGER REFERENCES project(id) ON DELETE SET NULL,
  owner_id    INTEGER NOT NULL DEFAULT 1,
  created_at  TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at  TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_assembly_name ON assembly(name);

CREATE TABLE IF NOT EXISTS assembly_line (
  id                INTEGER PRIMARY KEY,
  assembly_id       INTEGER NOT NULL REFERENCES assembly(id) ON DELETE CASCADE,
  sort              INTEGER NOT NULL DEFAULT 0,
  item_type         TEXT NOT NULL CHECK (item_type IN ('part','assembly')),
  part_id           INTEGER REFERENCES part(id) ON DELETE RESTRICT,
  child_assembly_id INTEGER REFERENCES assembly(id) ON DELETE RESTRICT,
  qty               REAL NOT NULL DEFAULT 1,
  dnp               INTEGER NOT NULL DEFAULT 0,
  notes             TEXT NOT NULL DEFAULT '',
  CHECK (
    (item_type = 'part'     AND part_id IS NOT NULL AND child_assembly_id IS NULL) OR
    (item_type = 'assembly' AND child_assembly_id IS NOT NULL AND part_id IS NULL)
  ),
  CHECK (child_assembly_id IS NULL OR child_assembly_id <> assembly_id)
);
CREATE INDEX IF NOT EXISTS idx_line_assembly ON assembly_line(assembly_id);

CREATE TABLE IF NOT EXISTS build (
  id          INTEGER PRIMARY KEY,
  name        TEXT,
  assembly_id INTEGER NOT NULL REFERENCES assembly(id) ON DELETE RESTRICT,
  selections  TEXT NOT NULL DEFAULT '{}',
  snapshot    TEXT,
  frozen_at   TEXT,
  owner_id    INTEGER NOT NULL DEFAULT 1,
  created_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS partdb_cache (
  partdb_id     INTEGER PRIMARY KEY,
  name          TEXT NOT NULL DEFAULT '',
  mpn           TEXT NOT NULL DEFAULT '',
  manufacturer  TEXT NOT NULL DEFAULT '',
  description   TEXT NOT NULL DEFAULT '',
  datasheet_url TEXT NOT NULL DEFAULT '',
  fetched_at    TEXT NOT NULL DEFAULT (datetime('now'))
);

INSERT OR IGNORE INTO project (id, name) VALUES (1, 'Default');
`

// openDB opens (creating if needed) the SQLite database at path and applies the
// schema. Foreign keys must be enabled per-connection.
func openDB(path string) (*sql.DB, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create db dir: %w", err)
		}
	}
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return db, nil
}
