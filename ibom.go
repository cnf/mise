package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// iBOM import. KiCad's InteractiveHtmlBom embeds an LZString-compressed,
// base64-encoded JSON blob named `pcbdata` that holds the metadata, the BOM
// (refdes -> value/footprint groups) and per-footprint on-board geometry. We
// decode that directly rather than parsing the rendered HTML: it is the
// authoritative machine-readable form and needs no headless browser.
//
// Verified field shapes (iBOM v2.11.2):
//   pcbdata.metadata    {title, revision, company, date, variant}
//   pcbdata.bom.fields  {"20": ["AP2112K-3.3","SOT-23-5"], ...}
//   pcbdata.bom.both    [[[refdes, fieldIndex], ...], ...]   (groups of equal lines)
//   pcbdata.bom.skipped [fieldIndex, ...]                    (DNP / not-populated)
//   pcbdata.footprints  [{ref, bbox:{pos:[x,y],angle}, layer, pads, ...}, ...]

var ibomRe = regexp.MustCompile(`pcbdata\s*=\s*JSON\.parse\(LZString\.decompressFromBase64\("([^"]+)"\)`)

type ibomData struct {
	Metadata struct {
		Title    string `json:"title"`
		Revision string `json:"revision"`
		Company  string `json:"company"`
		Date     string `json:"date"`
	} `json:"metadata"`
	Bom struct {
		Fields  map[string][]string `json:"fields"`
		Both    [][][]any           `json:"both"`
		Skipped []int               `json:"skipped"`
	} `json:"bom"`
	Footprints []struct {
		Ref   string `json:"ref"`
		Layer string `json:"layer"`
	} `json:"footprints"`
}

// importIBOM decodes an ibom.html and creates (or reuses) an assembly for the
// board, returning the assembly id plus the number of components imported.
// Components that share a value and footprint become ONE part and ONE line with
// an aggregated count and the designators as its note (e.g. "C3, C4, C19"),
// rather than a line per reference designator.
//
// The caller also keeps the file itself: the board layout is the reason you
// imported it, so it belongs on the manual too (see the import handler).
func importIBOM(db *sql.DB, raw []byte, name string) (int64, int, error) {
	m := ibomRe.FindSubmatch(raw)
	if m == nil {
		return 0, 0, fmt.Errorf("no pcbdata blob found — is this an InteractiveHtmlBom file?")
	}
	js, err := decompressFromBase64(string(m[1]))
	if err != nil {
		return 0, 0, fmt.Errorf("decompress pcbdata: %w", err)
	}
	var data ibomData
	if err := json.Unmarshal([]byte(js), &data); err != nil {
		return 0, 0, fmt.Errorf("parse pcbdata: %w", err)
	}

	title := name
	if t := strings.TrimSpace(data.Metadata.Title); t != "" && name == "" {
		title = t
	}
	if title == "" {
		title = "PCB"
	}
	rev := strings.TrimSpace(data.Metadata.Revision)
	if rev != "" {
		title = title + " rev " + rev
	}

	// reuse an existing assembly of the same name, else create it
	var asmID int64
	err = db.QueryRow(`SELECT id FROM assembly WHERE name=?`, title).Scan(&asmID)
	if err == sql.ErrNoRows {
		res, e := db.Exec(`INSERT INTO assembly (name, description) VALUES (?,?)`, title,
			strings.TrimSpace(data.Metadata.Company+" "+data.Metadata.Date))
		if e != nil {
			return 0, 0, e
		}
		asmID, _ = res.LastInsertId()
	} else if err != nil {
		return 0, 0, err
	} else {
		// refresh: clear previous lines so re-import is idempotent
		if _, err := db.Exec(`DELETE FROM assembly_line WHERE assembly_id=?`, asmID); err != nil {
			return 0, 0, err
		}
	}

	skipped := map[int]bool{}
	for _, s := range data.Bom.Skipped {
		skipped[s] = true
	}

	// bom.both already groups components that share a value and footprint: each
	// group is a slice of [refdes, fieldIndex] pairs (e.g. [[C3,1],[C4,50],
	// [C19,22]] is one 1µF part placed three times). Walk the groups and emit
	// one line per group — do NOT key by field index, because several field
	// indices share identical fields and that splits one part into many lines
	// with qty=1 each.
	tx, err := db.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()

	imported := 0
	sort := 0
	for _, group := range data.Bom.Both {
		// the group's field index is the same for every member; a group whose
		// members are all skipped (DNP) is dropped.
		fi := -1
		var refs []string
		allSkipped := true
		for _, entry := range group {
			if len(entry) != 2 {
				continue
			}
			ref, ok := entry[0].(string)
			fiF, ok2 := entry[1].(float64)
			if !ok || !ok2 {
				continue
			}
			if fi < 0 {
				fi = int(fiF)
			}
			refs = append(refs, ref)
			if !skipped[int(fiF)] {
				allSkipped = false
			}
		}
		if len(refs) == 0 || allSkipped || (fi >= 0 && skipped[fi]) {
			continue
		}
		fields := data.Bom.Fields[fmt.Sprintf("%d", fi)]
		value, footprint := "", ""
		if len(fields) > 0 {
			value = fields[0]
		}
		if len(fields) > 1 {
			footprint = fields[1]
		}
		pname := value
		if footprint != "" {
			pname = value + " (" + footprint + ")"
		}
		if pname == "" {
			pname = "board part " + fmt.Sprintf("%d", fi)
		}
		pid, err := upsertPartTx(tx, &Part{
			Name:        pname,
			Kind:        "electronic",
			Description: footprint,
			Attrs:       mustJSON(map[string]any{"refs": refs, "value": value, "footprint": footprint}),
		})
		if err != nil {
			return 0, 0, err
		}
		if _, err := tx.Exec(`INSERT INTO assembly_line (assembly_id,sort,item_type,part_id,qty,notes)
			VALUES (?,?,'part',?,?,?)`, asmID, sort, pid, float64(len(refs)), strings.Join(refs, ", ")); err != nil {
			return 0, 0, err
		}
		sort++
		imported += len(refs)
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	return asmID, imported, nil
}

func upsertPartTx(tx *sql.Tx, p *Part) (int64, error) {
	var id int64
	err := tx.QueryRow(`SELECT id FROM part WHERE name=?`, p.Name).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return 0, err
	}
	res, err := tx.Exec(`INSERT INTO part (name,kind,description,attrs) VALUES (?,?,?,?)`,
		p.Name, p.Kind, p.Description, orDefault(p.Attrs, "{}"))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}
