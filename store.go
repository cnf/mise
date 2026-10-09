package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type Part struct {
	ID           int64    `json:"id"`
	Name         string   `json:"name"`
	Kind         string   `json:"kind"`
	Description  string   `json:"description"`
	UnitPrice    *float64 `json:"unit_price"`
	Currency     string   `json:"currency"`
	VendorURL    string   `json:"vendor_url"`
	DatasheetURL string   `json:"datasheet_url"`
	PartDBID     *int64   `json:"partdb_id"`
	Attrs        string   `json:"attrs"`
	Notes        string   `json:"notes"`
}

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

// ---------- parts ----------

func listParts(db *sql.DB, q string) ([]Part, error) {
	sqlStr := `SELECT id,name,kind,description,unit_price,currency,vendor_url,datasheet_url,partdb_id,attrs,notes FROM part`
	var args []any
	if s := strings.TrimSpace(q); s != "" {
		sqlStr += ` WHERE name LIKE ? OR description LIKE ? OR attrs LIKE ?`
		like := "%" + s + "%"
		args = append(args, like, like, like)
	}
	sqlStr += ` ORDER BY kind, name COLLATE NOCASE`
	rows, err := db.Query(sqlStr, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanParts(rows)
}

func getPart(db *sql.DB, id int64) (*Part, error) {
	row := db.QueryRow(`SELECT id,name,kind,description,unit_price,currency,vendor_url,datasheet_url,partdb_id,attrs,notes FROM part WHERE id=?`, id)
	var p Part
	err := row.Scan(&p.ID, &p.Name, &p.Kind, &p.Description, &p.UnitPrice, &p.Currency, &p.VendorURL, &p.DatasheetURL, &p.PartDBID, &p.Attrs, &p.Notes)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func upsertPart(db *sql.DB, p *Part) (int64, error) {
	if p.Attrs == "" {
		p.Attrs = "{}"
	}
	if p.Currency == "" {
		p.Currency = "EUR"
	}
	if p.ID == 0 {
		res, err := db.Exec(`INSERT INTO part (name,kind,description,unit_price,currency,vendor_url,datasheet_url,partdb_id,attrs,notes)
			VALUES (?,?,?,?,?,?,?,?,?,?)`,
			p.Name, p.Kind, p.Description, p.UnitPrice, p.Currency, p.VendorURL, p.DatasheetURL, p.PartDBID, p.Attrs, p.Notes)
		if err != nil {
			return 0, err
		}
		return res.LastInsertId()
	}
	_, err := db.Exec(`UPDATE part SET name=?,kind=?,description=?,unit_price=?,currency=?,vendor_url=?,datasheet_url=?,partdb_id=?,attrs=?,notes=?,updated_at=datetime('now') WHERE id=?`,
		p.Name, p.Kind, p.Description, p.UnitPrice, p.Currency, p.VendorURL, p.DatasheetURL, p.PartDBID, p.Attrs, p.Notes, p.ID)
	return p.ID, err
}

func deletePart(db *sql.DB, id int64) error {
	uses := []struct {
		q    string
		what string
	}{
		{`SELECT COUNT(*) FROM assembly_line WHERE part_id=?`, "assembly line(s)"},
		{`SELECT COUNT(*) FROM variant_option WHERE part_id=?`, "variant option(s)"},
	}
	for _, u := range uses {
		var n int
		if err := db.QueryRow(u.q, id).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return fmt.Errorf("part is used by %d %s", n, u.what)
		}
	}
	_, err := db.Exec(`DELETE FROM part WHERE id=?`, id)
	return err
}

func scanParts(rows *sql.Rows) ([]Part, error) {
	var out []Part
	for rows.Next() {
		var p Part
		if err := rows.Scan(&p.ID, &p.Name, &p.Kind, &p.Description, &p.UnitPrice, &p.Currency, &p.VendorURL, &p.DatasheetURL, &p.PartDBID, &p.Attrs, &p.Notes); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ---------- assemblies ----------

func listAssemblies(db *sql.DB) ([]Assembly, error) {
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

func getAssembly(db *sql.DB, id int64) (*Assembly, error) {
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

func upsertAssembly(db *sql.DB, a *Assembly) (int64, error) {
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

func deleteAssembly(db *sql.DB, id int64) error {
	uses := []struct {
		q    string
		what string
	}{
		{`SELECT COUNT(*) FROM assembly_line WHERE child_assembly_id=?`, "parent line(s)"},
		{`SELECT COUNT(*) FROM variant_option WHERE child_assembly_id=?`, "variant option(s)"},
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

// lines returns the lines of one assembly, joined with part/slot data for
// display. Slot lines carry their options so the view can render the choice.
func lines(db *sql.DB, assemblyID int64) ([]Line, error) {
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
			opts, err := optionsForSlot(db, *out[i].SlotID)
			if err != nil {
				return nil, err
			}
			out[i].Options = opts
		}
	}
	return out, nil
}

func addLine(db *sql.DB, l *Line) (int64, error) {
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

func deleteLine(db *sql.DB, id int64) error {
	_, err := db.Exec(`DELETE FROM assembly_line WHERE id=?`, id)
	return err
}

func updateLineQty(db *sql.DB, id int64, qty float64, dnp bool) error {
	_, err := db.Exec(`UPDATE assembly_line SET qty=?, dnp=? WHERE id=?`, qty, dnp, id)
	return err
}

// ---------- variant slots ----------

func listSlots(db *sql.DB, assemblyID int64) ([]VariantSlot, error) {
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

func getSlot(db *sql.DB, id int64) (*VariantSlot, error) {
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

func addSlot(db *sql.DB, s *VariantSlot) (int64, error) {
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

func deleteSlot(db *sql.DB, id int64) error {
	_, err := db.Exec(`DELETE FROM variant_slot WHERE id=?`, id)
	return err
}

func optionsForSlot(db *sql.DB, slotID int64) ([]Option, error) {
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

func addOption(db *sql.DB, o *Option) (int64, error) {
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

func deleteOption(db *sql.DB, id int64) error {
	_, err := db.Exec(`DELETE FROM variant_option WHERE id=?`, id)
	return err
}

// ---------- build selection ----------

// Selection maps slot id -> chosen option id. Stored on the build as a JSON
// object keyed by the decimal slot id, because JSON object keys are strings.
type Selection map[int64]int64

func parseSelection(raw string) Selection {
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

func upsertBuild(db *sql.DB, b *Build) (int64, error) {
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

func listBuilds(db *sql.DB) ([]Build, error) {
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

func getBuild(db *sql.DB, id int64) (*Build, error) {
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

func deleteBuild(db *sql.DB, id int64) error {
	_, err := db.Exec(`DELETE FROM build WHERE id=?`, id)
	return err
}

// freezeBuild computes the gather list for the build's current selections and
// stores it, with the selections, as the immutable snapshot.
func freezeBuild(db *sql.DB, id int64) error {
	b, err := getBuild(db, id)
	if err != nil || b == nil {
		return fmt.Errorf("build %d not found", id)
	}
	sel := parseSelection(b.Selections)
	g, err := gather(db, b.AssemblyID, sel)
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

// ---------- gather (the flatten) ----------

type GatheredPart struct {
	PartID    int64    `json:"part_id"`
	Name      string   `json:"name"`
	Kind      string   `json:"kind"`
	Qty       float64  `json:"qty"`
	UnitPrice *float64 `json:"unit_price"`
	Currency  string   `json:"currency"`
	VendorURL string   `json:"vendor_url"`
	PartDBID  *int64   `json:"partdb_id"`
}

type GatherGroup struct {
	AssemblyID   int64          `json:"assembly_id"`
	AssemblyName string         `json:"assembly_name"`
	Lines        []GatheredPart `json:"lines"`
}

// ResolvedSlot records which option a slot contributed to the list, so the
// gather view can show the build's choices and any compatibility note.
type ResolvedSlot struct {
	SlotID     int64  `json:"slot_id"`
	SlotName   string `json:"slot_name"`
	Assembly   string `json:"assembly"`
	Label      string `json:"label"`
	Target     string `json:"target"`
	Note       string `json:"note"`
	IsDefault  bool   `json:"is_default"`
	Unresolved bool   `json:"unresolved"`
}

type GatherResult struct {
	RootID    int64              `json:"root_id"`
	RootName  string             `json:"root_name"`
	Groups    []GatherGroup      `json:"groups"`
	Total     []GatheredPart     `json:"total"`
	TotalCost map[string]float64 `json:"total_cost"`
	Slots     []ResolvedSlot     `json:"slots"`
	Selection Selection          `json:"-"`
	Errors    []string           `json:"errors,omitempty"`
}

// gather walks the assembly tree from rootID, summing part quantities and
// grouping them by the sub-assembly that declares them. Slot lines resolve to
// one option each: the build's selection if it has one, otherwise the option
// marked default, otherwise the first by sort order. DNP lines are excluded.
// Rollups are computed on read, never stored.
func gather(db *sql.DB, rootID int64, sel Selection) (*GatherResult, error) {
	root, err := getAssembly(db, rootID)
	if err != nil || root == nil {
		return nil, fmt.Errorf("assembly %d not found", rootID)
	}
	if sel == nil {
		sel = Selection{}
	}
	res := &GatherResult{RootID: rootID, RootName: root.Name, TotalCost: map[string]float64{}}

	type acc struct {
		name  string
		order int
		parts map[int64]*GatheredPart
	}
	groups := map[int64]*acc{}
	var groupOrder []int64
	totals := map[int64]*GatheredPart{}
	seen := map[int64]bool{}
	slotSeen := map[int64]bool{}
	order := 0

	addPart := func(g *acc, partID int64, name, kind string, price *float64, currency, vendor string, pdbid *int64, q float64) {
		if g.parts[partID] == nil {
			g.parts[partID] = &GatheredPart{PartID: partID, Name: name, Kind: kind, UnitPrice: price, Currency: currency, VendorURL: vendor, PartDBID: pdbid}
		}
		g.parts[partID].Qty += q
		if totals[partID] == nil {
			totals[partID] = &GatheredPart{PartID: partID, Name: name, Kind: kind, UnitPrice: price, Currency: currency, VendorURL: vendor, PartDBID: pdbid}
		}
		totals[partID].Qty += q
	}

	var walk func(id int64, mult float64, depth int)
	walk = func(id int64, mult float64, depth int) {
		if depth > 32 {
			res.Errors = append(res.Errors, fmt.Sprintf("depth limit reached at assembly %d (cycle?)", id))
			return
		}
		if seen[id] {
			res.Errors = append(res.Errors, fmt.Sprintf("assembly %d appears twice on the same path (cycle?)", id))
			return
		}
		seen[id] = true
		defer delete(seen, id)

		ls, err := lines(db, id)
		if err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("assembly %d: %v", id, err))
			return
		}
		if _, ok := groups[id]; !ok {
			a, _ := getAssembly(db, id)
			name := fmt.Sprintf("assembly %d", id)
			if a != nil {
				name = a.Name
			}
			groups[id] = &acc{name: name, order: order, parts: map[int64]*GatheredPart{}}
			groupOrder = append(groupOrder, id)
			order++
		}
		g := groups[id]

		for _, l := range ls {
			if l.DNP {
				continue
			}
			switch l.ItemType {
			case "part":
				if l.PartID == nil {
					continue
				}
				addPart(g, *l.PartID, l.PartName, l.PartKind, l.UnitPrice, l.Currency, l.VendorURL, l.PartDBID, l.Qty*mult)
			case "assembly":
				if l.ChildAssemblyID == nil {
					continue
				}
				walk(*l.ChildAssemblyID, mult*l.Qty, depth+1)
			case "slot":
				if l.SlotID == nil {
					continue
				}
				opt, isDefault := chooseOption(db, *l.SlotID, sel)
				slotRow := ResolvedSlot{
					SlotID: *l.SlotID, SlotName: l.SlotName, Assembly: g.name,
					Note: l.Notes, IsDefault: isDefault,
				}
				if opt == nil {
					slotRow.Unresolved = true
					slotRow.Label = "(no options)"
					if !slotSeen[*l.SlotID] {
						slotSeen[*l.SlotID] = true
						res.Slots = append(res.Slots, slotRow)
					}
					res.Errors = append(res.Errors, fmt.Sprintf("slot %q in %s has no options", l.SlotName, g.name))
					continue
				}
				q := l.Qty * opt.Qty * mult
				switch opt.TargetType {
				case "part":
					if opt.PartID != nil {
						if p, err := getPart(db, *opt.PartID); err == nil && p != nil {
							addPart(g, p.ID, p.Name, p.Kind, p.UnitPrice, p.Currency, p.VendorURL, p.PartDBID, q)
						}
					}
				case "assembly":
					if opt.ChildAssemblyID == nil {
						continue
					}
					walk(*opt.ChildAssemblyID, q, depth+1)
				}
				if !slotSeen[*l.SlotID] {
					slotSeen[*l.SlotID] = true
					slotRow.Label = opt.Label
					slotRow.Target = opt.TargetName()
					res.Slots = append(res.Slots, slotRow)
				}
			}
		}
	}
	walk(rootID, 1, 0)

	for _, id := range groupOrder {
		g := groups[id]
		gg := GatherGroup{AssemblyID: id, AssemblyName: g.name}
		for _, p := range g.parts {
			gg.Lines = append(gg.Lines, *p)
		}
		sort.Slice(gg.Lines, func(i, j int) bool {
			if gg.Lines[i].Kind != gg.Lines[j].Kind {
				return gg.Lines[i].Kind < gg.Lines[j].Kind
			}
			return strings.ToLower(gg.Lines[i].Name) < strings.ToLower(gg.Lines[j].Name)
		})
		res.Groups = append(res.Groups, gg)
	}

	for _, p := range totals {
		res.Total = append(res.Total, *p)
		if p.UnitPrice != nil {
			res.TotalCost[p.Currency] += *p.UnitPrice * p.Qty
		}
	}
	sort.Slice(res.Total, func(i, j int) bool {
		if res.Total[i].Kind != res.Total[j].Kind {
			return res.Total[i].Kind < res.Total[j].Kind
		}
		return strings.ToLower(res.Total[i].Name) < strings.ToLower(res.Total[j].Name)
	})
	sort.Slice(res.Slots, func(i, j int) bool { return res.Slots[i].SlotID < res.Slots[j].SlotID })
	return res, nil
}

// chooseOption returns the option a slot resolves to, and whether that was a
// default (i.e. the build did not choose explicitly).
func chooseOption(db *sql.DB, slotID int64, sel Selection) (*Option, bool) {
	if oid, ok := sel[slotID]; ok {
		o, err := getOption(db, oid)
		if err == nil && o != nil {
			return o, false
		}
	}
	opts, err := optionsForSlot(db, slotID)
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
