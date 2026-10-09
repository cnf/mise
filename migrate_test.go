package main

import (
	"path/filepath"
	"testing"
)

// TestDocumentMigrationPreservesSteps is the migration-risk test: 004 rebuilds
// manual_step to widen its CHECK constraint for the new 'document' type, and a
// rebuild is exactly where phase-3 data (steps and their images) can be lost.
// This applies migrations 001..003 by hand, writes a manual with an image, then
// runs the real migration and checks nothing was dropped and the new type works.
func TestDocumentMigrationPreservesSteps(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "upgrade.db")
	db, err := openDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Roll the migration record back to the phase-3 state so 004 runs, then
	// simulate having been at phase 3 with real rows in place.
	if _, err := db.Exec(`DELETE FROM migrations WHERE name='004_documents'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TABLE document`); err != nil {
		t.Fatal(err)
	}
	// Put manual_step back to its phase-3 shape (no 'document' in the CHECK).
	if _, err := db.Exec(`
		PRAGMA foreign_keys=OFF;
		CREATE TABLE manual_step_old (
		  id                INTEGER PRIMARY KEY,
		  assembly_id       INTEGER NOT NULL REFERENCES assembly(id) ON DELETE CASCADE,
		  sort              INTEGER NOT NULL DEFAULT 0,
		  item_type         TEXT NOT NULL CHECK (item_type IN ('note','assembly','slot')),
		  title             TEXT NOT NULL DEFAULT '',
		  body              TEXT NOT NULL DEFAULT '',
		  child_assembly_id INTEGER REFERENCES assembly(id) ON DELETE RESTRICT,
		  slot_id           INTEGER REFERENCES variant_slot(id) ON DELETE RESTRICT,
		  created_at        TEXT NOT NULL DEFAULT (datetime('now')),
		  updated_at        TEXT NOT NULL DEFAULT (datetime('now'))
		);
		DROP TABLE manual_step;
		ALTER TABLE manual_step_old RENAME TO manual_step;
		PRAGMA foreign_keys=ON;`); err != nil {
		t.Fatal(err)
	}

	if _, err := db.Exec(`INSERT INTO assembly (name) VALUES ('Frame')`); err != nil {
		t.Fatal(err)
	}
	stepID, err := addStep(db, &Step{AssemblyID: 1, ItemType: "note", Title: "keep me", Body: "precious"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := addImage(db, stepID, "p.png", "image/png", tick(), "cap"); err != nil {
		t.Fatal(err)
	}

	// Now run the real migration list.
	if err := migrate(db); err != nil {
		t.Fatalf("migration from phase 3: %v", err)
	}

	got, err := getStep(db, stepID)
	if err != nil || got == nil {
		t.Fatalf("step lost in migration: %v", err)
	}
	if got.Title != "keep me" || got.Body != "precious" {
		t.Errorf("step body changed: %+v", got)
	}
	if len(got.Images) != 1 || got.Images[0].Caption != "cap" {
		t.Errorf("step image lost in migration: %+v", got.Images)
	}

	// And the new type must be accepted after the rebuild.
	if _, err := addStep(db, &Step{AssemblyID: 1, ItemType: "document", Title: "board"}); err != nil {
		t.Errorf("document step rejected after migration: %v", err)
	}
	// The old CHECK must still reject a bogus type.
	if _, err := db.Exec(`INSERT INTO manual_step (assembly_id,item_type) VALUES (1,'nonsense')`); err == nil {
		t.Error("the widened CHECK no longer rejects an unknown item_type")
	}
}
