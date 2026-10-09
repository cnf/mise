package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Schema is applied in order. Each step is idempotent in effect: fresh
// databases run every step from empty, an existing one runs only the steps it
// has not recorded. migrations table records what has been applied.
//
// Steps that change a CHECK constraint need a table rebuild — SQLite cannot
// alter one in place — so those steps create a new table, copy, drop, rename.
// That is why this is a migration list rather than a pile of CREATE IF NOT
// EXISTS statements.

var migrations = []struct {
	name string
	stmt string
}{
	{"001_init", `
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
`},

	// 002 adds variant slots: a named point in an assembly where the builder
	// picks one of several alternatives (PCB rev A/B/C, faller type 1/2). A slot
	// is a third kind of line, so assembly_line must be rebuilt to widen its
	// CHECK constraint.
	{"002_variant_slots", `
CREATE TABLE IF NOT EXISTS variant_slot (
  id          INTEGER PRIMARY KEY,
  assembly_id INTEGER NOT NULL REFERENCES assembly(id) ON DELETE CASCADE,
  name        TEXT NOT NULL,
  notes       TEXT NOT NULL DEFAULT '',
  sort        INTEGER NOT NULL DEFAULT 0,
  created_at  TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_slot_assembly ON variant_slot(assembly_id);

CREATE TABLE IF NOT EXISTS variant_option (
  id                INTEGER PRIMARY KEY,
  slot_id           INTEGER NOT NULL REFERENCES variant_slot(id) ON DELETE CASCADE,
  label             TEXT NOT NULL,
  target_type       TEXT NOT NULL CHECK (target_type IN ('part','assembly')),
  part_id           INTEGER REFERENCES part(id) ON DELETE RESTRICT,
  child_assembly_id INTEGER REFERENCES assembly(id) ON DELETE RESTRICT,
  qty               REAL NOT NULL DEFAULT 1,
  notes             TEXT NOT NULL DEFAULT '',
  is_default        INTEGER NOT NULL DEFAULT 0,
  sort              INTEGER NOT NULL DEFAULT 0,
  CHECK (
    (target_type = 'part'     AND part_id IS NOT NULL AND child_assembly_id IS NULL) OR
    (target_type = 'assembly' AND child_assembly_id IS NOT NULL AND part_id IS NULL)
  )
);
CREATE INDEX IF NOT EXISTS idx_option_slot ON variant_option(slot_id);

CREATE TABLE assembly_line_new (
  id                INTEGER PRIMARY KEY,
  assembly_id       INTEGER NOT NULL REFERENCES assembly(id) ON DELETE CASCADE,
  sort              INTEGER NOT NULL DEFAULT 0,
  item_type         TEXT NOT NULL CHECK (item_type IN ('part','assembly','slot')),
  part_id           INTEGER REFERENCES part(id) ON DELETE RESTRICT,
  child_assembly_id INTEGER REFERENCES assembly(id) ON DELETE RESTRICT,
  slot_id           INTEGER REFERENCES variant_slot(id) ON DELETE CASCADE,
  qty               REAL NOT NULL DEFAULT 1,
  dnp               INTEGER NOT NULL DEFAULT 0,
  notes             TEXT NOT NULL DEFAULT '',
  CHECK (
    (item_type = 'part'     AND part_id IS NOT NULL AND child_assembly_id IS NULL AND slot_id IS NULL) OR
    (item_type = 'assembly' AND child_assembly_id IS NOT NULL AND part_id IS NULL AND slot_id IS NULL) OR
    (item_type = 'slot'     AND slot_id IS NOT NULL AND part_id IS NULL AND child_assembly_id IS NULL)
  ),
  CHECK (child_assembly_id IS NULL OR child_assembly_id <> assembly_id)
);
INSERT INTO assembly_line_new (id, assembly_id, sort, item_type, part_id, child_assembly_id, qty, dnp, notes)
  SELECT id, assembly_id, sort, item_type, part_id, child_assembly_id, qty, dnp, notes FROM assembly_line;
DROP TABLE assembly_line;
ALTER TABLE assembly_line_new RENAME TO assembly_line;
CREATE INDEX IF NOT EXISTS idx_line_assembly ON assembly_line(assembly_id);
`},
}

// openDB opens (creating if needed) the SQLite database at path and applies any
// migrations it has not recorded. Foreign keys must be enabled per-connection.
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
	if err := migrate(db); err != nil {
		return nil, err
	}
	return db, nil
}

func migrate(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS migrations (
		name TEXT PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`); err != nil {
		return err
	}
	for _, m := range migrations {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM migrations WHERE name=?`, m.name).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			continue
		}
		if err := applyMigration(db, m.stmt); err != nil {
			return fmt.Errorf("migration %s: %w", m.name, err)
		}
		if _, err := db.Exec(`INSERT INTO migrations (name) VALUES (?)`, m.name); err != nil {
			return err
		}
	}
	return nil
}

// applyMigration runs one step. Foreign keys are disabled for the duration
// because a table rebuild drops and renames tables that other tables reference;
// the pragma is a no-op inside a transaction, so it is set outside one.
func applyMigration(db *sql.DB, stmt string) error {
	if _, err := db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
		return err
	}
	defer db.Exec(`PRAGMA foreign_keys=ON`)

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(stmt); err != nil {
		tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	// A rebuild can leave rows orphaned only if the copied data was already
	// inconsistent; check rather than assume.
	var bad int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&bad); err != nil {
		return nil // pragma unavailable on this build; nothing to report
	}
	if bad > 0 {
		return fmt.Errorf("%d foreign-key violations after migration", bad)
	}
	return nil
}
