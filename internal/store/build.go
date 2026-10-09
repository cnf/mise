package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// ---------- build selection ----------

// Selection maps slot id -> chosen option id. Stored on the build as a JSON
// object keyed by the decimal slot id, because JSON object keys are strings.
type Selection map[int64]int64

func ParseSelection(raw string) Selection {
	sel := Selection{}
	if strings.TrimSpace(raw) == "" {
		return sel
	}
	var m map[string]int64
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return sel
	}
	for k, v := range m {
		if id, err := strconv.ParseInt(k, 10, 64); err == nil {
			sel[id] = v
		}
	}
	return sel
}

func (s Selection) JSON() string {
	m := map[string]int64{}
	for k, v := range s {
		m[strconv.FormatInt(k, 10)] = v
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// Set validates that the option belongs to the slot before recording it, so a
// tampered form cannot silently point a slot at an unrelated option.
func (s Selection) Set(db *sql.DB, slotID, optionID int64) error {
	o, err := getOption(db, optionID)
	if err != nil {
		return err
	}
	if o == nil {
		return fmt.Errorf("option %d not found", optionID)
	}
	if o.SlotID != slotID {
		return fmt.Errorf("option %d does not belong to slot %d", optionID, slotID)
	}
	s[slotID] = optionID
	return nil
}

// ---------- build (release) ----------

type Build struct {
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	AssemblyID int64   `json:"assembly_id"`
	Selections string  `json:"selections"`
	Snapshot   *string `json:"snapshot"`
	FrozenAt   *string `json:"frozen_at"`
}

func UpsertBuild(db *sql.DB, b *Build) (int64, error) {
	if b.Selections == "" {
		b.Selections = "{}"
	}
	if b.ID == 0 {
		res, err := db.Exec(`INSERT INTO build (name,assembly_id,selections) VALUES (?,?,?)`, b.Name, b.AssemblyID, b.Selections)
		if err != nil {
			return 0, err
		}
		return res.LastInsertId()
	}
	_, err := db.Exec(`UPDATE build SET name=?,assembly_id=?,selections=? WHERE id=?`, b.Name, b.AssemblyID, b.Selections, b.ID)
	return b.ID, err
}

func ListBuilds(db *sql.DB) ([]Build, error) {
	rows, err := db.Query(`SELECT id,name,assembly_id,selections,snapshot,frozen_at FROM build ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Build
	for rows.Next() {
		var b Build
		if err := rows.Scan(&b.ID, &b.Name, &b.AssemblyID, &b.Selections, &b.Snapshot, &b.FrozenAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func GetBuild(db *sql.DB, id int64) (*Build, error) {
	row := db.QueryRow(`SELECT id,name,assembly_id,selections,snapshot,frozen_at FROM build WHERE id=?`, id)
	var b Build
	err := row.Scan(&b.ID, &b.Name, &b.AssemblyID, &b.Selections, &b.Snapshot, &b.FrozenAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func DeleteBuild(db *sql.DB, id int64) error {
	_, err := db.Exec(`DELETE FROM build WHERE id=?`, id)
	return err
}
