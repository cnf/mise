package bom

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"example.com/cnf/mise/internal/store"
)

// FreezeBuild computes the gather list for the build's current selections and
// stores it immutably. It lives here rather than in store because freezing is
// the one place the two concerns meet: it reads the build (store) and resolves
// the tree (bom). Keeping it in store would make store import bom, and bom
// already imports store.
// stores it, with the selections, as the immutable snapshot.
func FreezeBuild(db *sql.DB, id int64) error {
	b, err := store.GetBuild(db, id)
	if err != nil || b == nil {
		return fmt.Errorf("build %d not found", id)
	}
	sel := store.ParseSelection(b.Selections)
	g, err := Gather(db, b.AssemblyID, sel)
	if err != nil {
		return err
	}
	if len(g.Errors) > 0 {
		return fmt.Errorf("cannot freeze, %d problem(s): %s", len(g.Errors), strings.Join(g.Errors, "; "))
	}
	g.Selection = sel
	raw, err := json.Marshal(g)
	if err != nil {
		return err
	}
	_, err = db.Exec(`UPDATE build SET snapshot=?, frozen_at=datetime('now') WHERE id=?`, string(raw), id)
	return err
}
