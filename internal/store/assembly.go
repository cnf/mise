package store

import (
	"database/sql"
	"fmt"
	"sort"
)

type Assembly struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	ProjectID   *int64 `json:"project_id"`
}

type Line struct {
	ID              int64   `json:"id"`
	AssemblyID      int64   `json:"assembly_id"`
	Sort            int     `json:"sort"`
	ItemType        string  `json:"item_type"`
	PartID          *int64  `json:"part_id"`
	ChildAssemblyID *int64  `json:"child_assembly_id"`
	SlotID          *int64  `json:"slot_id"`
	Qty             float64 `json:"qty"`
	DNP             bool    `json:"dnp"`
	Notes           string  `json:"notes"`

	// joined for display
	PartName  string   `json:"part_name"`
	ChildName string   `json:"child_name"`
	SlotName  string   `json:"slot_name"`
	UnitPrice *float64 `json:"unit_price"`
	Currency  string   `json:"currency"`
	PartKind  string   `json:"part_kind"`
	PartDBID  *int64   `json:"partdb_id"`
	VendorURL string   `json:"vendor_url"`

	Options []Option `json:"options"`
}

// VariantSlot is a named point in an assembly where the builder picks one of
// several alternatives. The same physical hardware often fits several variants
// ("this frame takes PCB rev A, B or C"), so the slot is the model's way of
// saying "choose exactly one of these here".
type VariantSlot struct {
	ID         int64  `json:"id"`
	AssemblyID int64  `json:"assembly_id"`
	Name       string `json:"name"`
	Notes      string `json:"notes"`
	Sort       int    `json:"sort"`
}

type Option struct {
	ID              int64   `json:"id"`
	SlotID          int64   `json:"slot_id"`
	Label           string  `json:"label"`
	TargetType      string  `json:"target_type"`
	PartID          *int64  `json:"part_id"`
	ChildAssemblyID *int64  `json:"child_assembly_id"`
	Qty             float64 `json:"qty"`
	Notes           string  `json:"notes"`
	IsDefault       bool    `json:"is_default"`
	Sort            int     `json:"sort"`

	PartName  string `json:"part_name"`
	ChildName string `json:"child_name"`
}

func (o Option) TargetName() string {
	if o.TargetType == "part" {
		return o.PartName
	}
	return o.ChildName
}

// ---------- assemblies ----------

func ListAssemblies(db *sql.DB) ([]Assembly, error) {
	rows, err := db.Query(`SELECT id,name,description,project_id FROM assembly ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Assembly
	for rows.Next() {
		var a Assembly
		if err := rows.Scan(&a.ID, &a.Name, &a.Description, &a.ProjectID); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func GetAssembly(db *sql.DB, id int64) (*Assembly, error) {
	row := db.QueryRow(`SELECT id,name,description,project_id FROM assembly WHERE id=?`, id)
	var a Assembly
	err := row.Scan(&a.ID, &a.Name, &a.Description, &a.ProjectID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func UpsertAssembly(db *sql.DB, a *Assembly) (int64, error) {
	if a.ID == 0 {
		res, err := db.Exec(`INSERT INTO assembly (name,description,project_id) VALUES (?,?,?)`, a.Name, a.Description, a.ProjectID)
		if err != nil {
			return 0, err
		}
		return res.LastInsertId()
	}
	_, err := db.Exec(`UPDATE assembly SET name=?,description=?,project_id=?,updated_at=datetime('now') WHERE id=?`, a.Name, a.Description, a.ProjectID, a.ID)
	return a.ID, err
}

func DeleteAssembly(db *sql.DB, id int64) error {
	uses := []struct {
		q    string
		what string
	}{
		{`SELECT COUNT(*) FROM assembly_line WHERE child_assembly_id=?`, "parent line(s)"},
		{`SELECT COUNT(*) FROM variant_option WHERE child_assembly_id=?`, "variant option(s)"},
		{`SELECT COUNT(*) FROM manual_step WHERE child_assembly_id=?`, "manual step(s)"},
		{`SELECT COUNT(*) FROM build WHERE assembly_id=?`, "build(s)"},
	}
	for _, u := range uses {
		var n int
		if err := db.QueryRow(u.q, id).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return fmt.Errorf("assembly is used by %d %s", n, u.what)
		}
	}
	_, err := db.Exec(`DELETE FROM assembly WHERE id=?`, id)
	return err
}

// Lines returns the Lines of one assembly, joined with part/slot data for
// display. Slot Lines carry their options so the view can render the choice.
func Lines(db *sql.DB, assemblyID int64) ([]Line, error) {
	rows, err := db.Query(`
		SELECT l.id,l.assembly_id,l.sort,l.item_type,l.part_id,l.child_assembly_id,l.slot_id,l.qty,l.dnp,l.notes,
		       COALESCE(p.name,''), COALESCE(c.name,''), COALESCE(s.name,''),
		       p.unit_price, COALESCE(p.currency,'EUR'), COALESCE(p.kind,''), p.partdb_id, COALESCE(p.vendor_url,'')
		FROM assembly_line l
		LEFT JOIN part p         ON p.id = l.part_id
		LEFT JOIN assembly c     ON c.id = l.child_assembly_id
		LEFT JOIN variant_slot s ON s.id = l.slot_id
		WHERE l.assembly_id = ?
		ORDER BY l.sort, l.id`, assemblyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Line
	for rows.Next() {
		var l Line
		if err := rows.Scan(&l.ID, &l.AssemblyID, &l.Sort, &l.ItemType, &l.PartID, &l.ChildAssemblyID,
			&l.SlotID, &l.Qty, &l.DNP, &l.Notes, &l.PartName, &l.ChildName, &l.SlotName,
			&l.UnitPrice, &l.Currency, &l.PartKind, &l.PartDBID, &l.VendorURL); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].SlotID != nil {
			opts, err := OptionsForSlot(db, *out[i].SlotID)
			if err != nil {
				return nil, err
			}
			out[i].Options = opts
		}
	}
	return out, nil
}

func AddLine(db *sql.DB, l *Line) (int64, error) {
	var maxSort sql.NullInt64
	if err := db.QueryRow(`SELECT MAX(sort) FROM assembly_line WHERE assembly_id=?`, l.AssemblyID).Scan(&maxSort); err != nil {
		return 0, err
	}
	l.Sort = int(maxSort.Int64)
	if maxSort.Valid {
		l.Sort++
	}
	res, err := db.Exec(`INSERT INTO assembly_line (assembly_id,sort,item_type,part_id,child_assembly_id,slot_id,qty,dnp,notes)
		VALUES (?,?,?,?,?,?,?,?,?)`, l.AssemblyID, l.Sort, l.ItemType, l.PartID, l.ChildAssemblyID, l.SlotID, l.Qty, l.DNP, l.Notes)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func DeleteLine(db *sql.DB, id int64) error {
	_, err := db.Exec(`DELETE FROM assembly_line WHERE id=?`, id)
	return err
}

func UpdateLineQty(db *sql.DB, id int64, qty float64, dnp bool) error {
	_, err := db.Exec(`UPDATE assembly_line SET qty=?, dnp=? WHERE id=?`, qty, dnp, id)
	return err
}

// ---------- variant slots ----------

func ListSlots(db *sql.DB, assemblyID int64) ([]VariantSlot, error) {
	rows, err := db.Query(`SELECT id,assembly_id,name,notes,sort FROM variant_slot WHERE assembly_id=? ORDER BY sort, id`, assemblyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []VariantSlot
	for rows.Next() {
		var s VariantSlot
		if err := rows.Scan(&s.ID, &s.AssemblyID, &s.Name, &s.Notes, &s.Sort); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func GetSlot(db *sql.DB, id int64) (*VariantSlot, error) {
	row := db.QueryRow(`SELECT id,assembly_id,name,notes,sort FROM variant_slot WHERE id=?`, id)
	var s VariantSlot
	err := row.Scan(&s.ID, &s.AssemblyID, &s.Name, &s.Notes, &s.Sort)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func AddSlot(db *sql.DB, s *VariantSlot) (int64, error) {
	var maxSort sql.NullInt64
	if err := db.QueryRow(`SELECT MAX(sort) FROM variant_slot WHERE assembly_id=?`, s.AssemblyID).Scan(&maxSort); err != nil {
		return 0, err
	}
	s.Sort = int(maxSort.Int64)
	if maxSort.Valid {
		s.Sort++
	}
	res, err := db.Exec(`INSERT INTO variant_slot (assembly_id,name,notes,sort) VALUES (?,?,?,?)`, s.AssemblyID, s.Name, s.Notes, s.Sort)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func DeleteSlot(db *sql.DB, id int64) error {
	// manual_step.slot_id is RESTRICT, so a referenced slot cannot simply be
	// deleted; report it rather than surfacing a raw foreign-key error.
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM manual_step WHERE slot_id=?`, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("slot is used by %d manual step(s)", n)
	}
	_, err := db.Exec(`DELETE FROM variant_slot WHERE id=?`, id)
	return err
}

func OptionsForSlot(db *sql.DB, slotID int64) ([]Option, error) {
	rows, err := db.Query(`
		SELECT o.id,o.slot_id,o.label,o.target_type,o.part_id,o.child_assembly_id,o.qty,o.notes,o.is_default,o.sort,
		       COALESCE(p.name,''), COALESCE(c.name,'')
		FROM variant_option o
		LEFT JOIN part p     ON p.id = o.part_id
		LEFT JOIN assembly c ON c.id = o.child_assembly_id
		WHERE o.slot_id = ?
		ORDER BY o.sort, o.id`, slotID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Option
	for rows.Next() {
		var o Option
		if err := rows.Scan(&o.ID, &o.SlotID, &o.Label, &o.TargetType, &o.PartID, &o.ChildAssemblyID,
			&o.Qty, &o.Notes, &o.IsDefault, &o.Sort, &o.PartName, &o.ChildName); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func getOption(db *sql.DB, id int64) (*Option, error) {
	row := db.QueryRow(`
		SELECT o.id,o.slot_id,o.label,o.target_type,o.part_id,o.child_assembly_id,o.qty,o.notes,o.is_default,o.sort,
		       COALESCE(p.name,''), COALESCE(c.name,'')
		FROM variant_option o
		LEFT JOIN part p     ON p.id = o.part_id
		LEFT JOIN assembly c ON c.id = o.child_assembly_id
		WHERE o.id = ?`, id)
	var o Option
	err := row.Scan(&o.ID, &o.SlotID, &o.Label, &o.TargetType, &o.PartID, &o.ChildAssemblyID,
		&o.Qty, &o.Notes, &o.IsDefault, &o.Sort, &o.PartName, &o.ChildName)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &o, nil
}

func AddOption(db *sql.DB, o *Option) (int64, error) {
	var maxSort sql.NullInt64
	if err := db.QueryRow(`SELECT MAX(sort) FROM variant_option WHERE slot_id=?`, o.SlotID).Scan(&maxSort); err != nil {
		return 0, err
	}
	o.Sort = int(maxSort.Int64)
	if maxSort.Valid {
		o.Sort++
	}
	// first option added becomes the default automatically
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM variant_option WHERE slot_id=?`, o.SlotID).Scan(&count); err != nil {
		return 0, err
	}
	if count == 0 {
		o.IsDefault = true
	}
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if o.IsDefault {
		if _, err := tx.Exec(`UPDATE variant_option SET is_default=0 WHERE slot_id=?`, o.SlotID); err != nil {
			return 0, err
		}
	}
	res, err := tx.Exec(`INSERT INTO variant_option (slot_id,label,target_type,part_id,child_assembly_id,qty,notes,is_default,sort)
		VALUES (?,?,?,?,?,?,?,?,?)`, o.SlotID, o.Label, o.TargetType, o.PartID, o.ChildAssemblyID, o.Qty, o.Notes, o.IsDefault, o.Sort)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func DeleteOption(db *sql.DB, id int64) error {
	_, err := db.Exec(`DELETE FROM variant_option WHERE id=?`, id)
	return err
}

// ChooseOption returns the option a slot resolves to, and whether that was a
// default (i.e. the build did not choose explicitly).
func ChooseOption(db *sql.DB, slotID int64, sel Selection) (*Option, bool) {
	if oid, ok := sel[slotID]; ok {
		o, err := getOption(db, oid)
		if err == nil && o != nil {
			return o, false
		}
	}
	opts, err := OptionsForSlot(db, slotID)
	if err != nil || len(opts) == 0 {
		return nil, false
	}
	for i := range opts {
		if opts[i].IsDefault {
			return &opts[i], true
		}
	}
	return &opts[0], true
}

// SlotsUnder returns every slot reachable from an assembly by walking both
// sub-assembly Lines and slot options, deduplicated, in tree order. The build
// view needs the whole reachable set, not just the root's own slots.
func SlotsUnder(db *sql.DB, rootID int64) ([]VariantSlot, error) {
	var out []VariantSlot
	seenAsm := map[int64]bool{}
	seenSlot := map[int64]bool{}

	var walk func(id int64, depth int)
	walk = func(id int64, depth int) {
		if depth > 32 || seenAsm[id] {
			return
		}
		seenAsm[id] = true

		own, err := ListSlots(db, id)
		if err == nil {
			for _, s := range own {
				if !seenSlot[s.ID] {
					seenSlot[s.ID] = true
					out = append(out, s)
				}
			}
		}
		ls, err := Lines(db, id)
		if err != nil {
			return
		}
		for _, l := range ls {
			switch l.ItemType {
			case "assembly":
				if l.ChildAssemblyID != nil {
					walk(*l.ChildAssemblyID, depth+1)
				}
			case "slot":
				for _, o := range l.Options {
					if o.TargetType == "assembly" && o.ChildAssemblyID != nil {
						walk(*o.ChildAssemblyID, depth+1)
					}
				}
			}
		}
	}
	walk(rootID, 0)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// WouldCycle reports whether adding child under parent would create a cycle.
// Slot options that point at assemblies count as edges too, so a cycle can
// never be introduced through a variant.
//
// Children are collected and the cursor closed before recursing: holding a
// result set open across a nested query on the same connection fails on some
// drivers, and the failure would look like "no children", silently disabling
// the guard for anything below the first level.
func WouldCycle(db *sql.DB, parent, child int64) error {
	seen := map[int64]bool{}

	var childrenOf func(id int64) []int64
	childrenOf = func(id int64) []int64 {
		var out []int64
		rows, err := db.Query(`SELECT child_assembly_id FROM assembly_line
			WHERE assembly_id=? AND item_type='assembly' AND child_assembly_id IS NOT NULL`, id)
		if err != nil {
			return out
		}
		for rows.Next() {
			var c int64
			if rows.Scan(&c) == nil {
				out = append(out, c)
			}
		}
		rows.Close()

		orows, err := db.Query(`SELECT o.child_assembly_id FROM variant_option o
			JOIN variant_slot s ON s.id = o.slot_id
			WHERE s.assembly_id=? AND o.target_type='assembly' AND o.child_assembly_id IS NOT NULL`, id)
		if err != nil {
			return out
		}
		for orows.Next() {
			var c int64
			if orows.Scan(&c) == nil {
				out = append(out, c)
			}
		}
		orows.Close()
		return out
	}

	var walk func(id int64, depth int) bool
	walk = func(id int64, depth int) bool {
		if id == parent {
			return true
		}
		if depth > 64 || seen[id] {
			return false
		}
		seen[id] = true
		for _, c := range childrenOf(id) {
			if walk(c, depth+1) {
				return true
			}
		}
		return false
	}
	if walk(child, 0) {
		return fmt.Errorf("adding this would create a circular sub-assembly reference")
	}
	return nil
}
