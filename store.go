package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
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
	Qty             float64 `json:"qty"`
	DNP             bool    `json:"dnp"`
	Notes           string  `json:"notes"`

	// joined for display
	PartName  string   `json:"part_name"`
	ChildName string   `json:"child_name"`
	UnitPrice *float64 `json:"unit_price"`
	Currency  string   `json:"currency"`
	PartKind  string   `json:"part_kind"`
	PartDBID  *int64   `json:"partdb_id"`
	VendorURL string   `json:"vendor_url"`
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
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM assembly_line WHERE part_id=?`, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("part is used by %d assembly line(s)", n)
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
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM assembly_line WHERE child_assembly_id=?`, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("assembly is used by %d parent line(s)", n)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM build WHERE assembly_id=?`, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("assembly is used by %d build(s)", n)
	}
	_, err := db.Exec(`DELETE FROM assembly WHERE id=?`, id)
	return err
}

// lines returns the lines of one assembly, joined with part data for display.
func lines(db *sql.DB, assemblyID int64) ([]Line, error) {
	rows, err := db.Query(`
		SELECT l.id,l.assembly_id,l.sort,l.item_type,l.part_id,l.child_assembly_id,l.qty,l.dnp,l.notes,
		       COALESCE(p.name,''), COALESCE(c.name,''), p.unit_price, COALESCE(p.currency,'EUR'),
		       COALESCE(p.kind,''), p.partdb_id, COALESCE(p.vendor_url,'')
		FROM assembly_line l
		LEFT JOIN part p ON p.id = l.part_id
		LEFT JOIN assembly c ON c.id = l.child_assembly_id
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
			&l.Qty, &l.DNP, &l.Notes, &l.PartName, &l.ChildName, &l.UnitPrice, &l.Currency, &l.PartKind,
			&l.PartDBID, &l.VendorURL); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
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
	res, err := db.Exec(`INSERT INTO assembly_line (assembly_id,sort,item_type,part_id,child_assembly_id,qty,dnp,notes)
		VALUES (?,?,?,?,?,?,?,?)`, l.AssemblyID, l.Sort, l.ItemType, l.PartID, l.ChildAssemblyID, l.Qty, l.DNP, l.Notes)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func deleteLine(db *sql.DB, id int64) error {
	_, err := db.Exec(`DELETE FROM assembly_line WHERE id=?`, id)
	return err
}

func setLineQty(db *sql.DB, id int64, qty float64) error {
	_, err := db.Exec(`UPDATE assembly_line SET qty=? WHERE id=?`, qty, id)
	return err
}

// updateLineQtyByPart updates every part-line for a part in an assembly. X-checked
// in the edit form posts to this.
func updateLineQty(db *sql.DB, id int64, qty float64, dnp bool) error {
	_, err := db.Exec(`UPDATE assembly_line SET qty=?, dnp=? WHERE id=?`, qty, dnp, id)
	return err
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

// freezeBuild computes the gather list and stores it as the immutable snapshot.
func freezeBuild(db *sql.DB, id int64) error {
	b, err := getBuild(db, id)
	if err != nil || b == nil {
		return fmt.Errorf("build %d not found", id)
	}
	g, err := gather(db, b.AssemblyID)
	if err != nil {
		return err
	}
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

type GatherResult struct {
	RootID    int64              `json:"root_id"`
	RootName  string             `json:"root_name"`
	Groups    []GatherGroup      `json:"groups"`
	Total     []GatheredPart     `json:"total"`
	TotalCost map[string]float64 `json:"total_cost"`
	Errors    []string           `json:"errors"`
}

// gather walks the assembly tree from rootID, summing part quantities and
// grouping them by the sub-assembly that declares them ("which bag does this
// belong to"). DNP lines are excluded. Rollups are computed on read, never
// stored — stored rollups drift.
func gather(db *sql.DB, rootID int64) (*GatherResult, error) {
	root, err := getAssembly(db, rootID)
	if err != nil || root == nil {
		return nil, fmt.Errorf("assembly %d not found", rootID)
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
	order := 0

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
				q := l.Qty * mult
				if g.parts[*l.PartID] == nil {
					g.parts[*l.PartID] = &GatheredPart{
						PartID: *l.PartID, Name: l.PartName, Kind: l.PartKind,
						UnitPrice: l.UnitPrice, Currency: l.Currency,
						VendorURL: l.VendorURL, PartDBID: l.PartDBID,
					}
				}
				g.parts[*l.PartID].Qty += q
				if totals[*l.PartID] == nil {
					totals[*l.PartID] = &GatheredPart{
						PartID: *l.PartID, Name: l.PartName, Kind: l.PartKind,
						UnitPrice: l.UnitPrice, Currency: l.Currency,
						VendorURL: l.VendorURL, PartDBID: l.PartDBID,
					}
				}
				totals[*l.PartID].Qty += q
			case "assembly":
				if l.ChildAssemblyID == nil {
					continue
				}
				walk(*l.ChildAssemblyID, mult*l.Qty, depth+1)
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
	if len(res.Errors) == 0 {
		res.Errors = nil
	}
	return res, nil
}
