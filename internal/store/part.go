package store

import (
	"database/sql"
	"fmt"
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

// ---------- parts ----------

func ListParts(db *sql.DB, q string) ([]Part, error) {
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

func GetPart(db *sql.DB, id int64) (*Part, error) {
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

func UpsertPart(db *sql.DB, p *Part) (int64, error) {
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

func DeletePart(db *sql.DB, id int64) error {
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
