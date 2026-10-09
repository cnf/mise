package store

import (
	"database/sql"
	"fmt"
	"regexp"
)

// ibomBlobRe matches the base64 LZString payload KiCad's InteractiveHtmlBom
// embeds. The pattern lives here, not in the ibom package, so that the board
// format stays the ibom package's concern while the upload guard stays in
// store with the rest of the document handling — ibom imports store, so store
// cannot import ibom without a cycle.
var ibomBlobRe = regexp.MustCompile(`pcbdata\s*=\s*JSON\.parse\(LZString\.decompressFromBase64\(\"([^\"]+)\"\)`)

// ---------- manual ----------

type Step struct {
	ID              int64  `json:"id"`
	AssemblyID      int64  `json:"assembly_id"`
	Sort            int    `json:"sort"`
	ItemType        string `json:"item_type"`
	Title           string `json:"title"`
	Body            string `json:"body"`
	ChildAssemblyID *int64 `json:"child_assembly_id"`
	SlotID          *int64 `json:"slot_id"`

	ChildName string    `json:"child_name"`
	SlotName  string    `json:"slot_name"`
	Images    []Image   `json:"images"`
	Document  *Document `json:"document"` // document Steps: an embedded iBOM
}

// Document is an embedded asset attached to a step and shown on the manual
// page. The mime decides how it renders: an InteractiveHtmlBom file is shown in
// a sandboxed iframe, a glTF binary in a model-viewer canvas. Both are stored
// whole and served from /documents/{id}; the step is a placeholder that gives
// the asset a place in the sequence.
type Document struct {
	ID       int64
	StepID   int64
	Filename string
	Mime     string
	Size     int64
}

// IsModel reports whether the embedded asset is a 3D model (rendered with
// model-viewer) rather than a markup document (rendered in an iframe).
func (d *Document) IsModel() bool {
	return d != nil && d.Mime == "model/gltf-binary"
}

type Image struct {
	ID       int64  `json:"id"`
	StepID   int64  `json:"step_id"`
	Sort     int    `json:"sort"`
	Filename string `json:"filename"`
	Mime     string `json:"mime"`
	Caption  string `json:"caption"`
	Size     int64  `json:"size"`
}

func Steps(db *sql.DB, assemblyID int64) ([]Step, error) {
	rows, err := db.Query(`
		SELECT s.id,s.assembly_id,s.sort,s.item_type,s.title,s.body,s.child_assembly_id,s.slot_id,
		       COALESCE(c.name,''), COALESCE(v.name,'')
		FROM manual_step s
		LEFT JOIN assembly c     ON c.id = s.child_assembly_id
		LEFT JOIN variant_slot v ON v.id = s.slot_id
		WHERE s.assembly_id = ?
		ORDER BY s.sort, s.id`, assemblyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Step
	for rows.Next() {
		var st Step
		if err := rows.Scan(&st.ID, &st.AssemblyID, &st.Sort, &st.ItemType, &st.Title, &st.Body,
			&st.ChildAssemblyID, &st.SlotID, &st.ChildName, &st.SlotName); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// images are fetched after the cursor is closed, never while it is open
	for i := range out {
		imgs, err := stepImages(db, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Images = imgs
		if out[i].ItemType == "document" {
			doc, err := StepDocument(db, out[i].ID)
			if err != nil {
				return nil, err
			}
			out[i].Document = doc
		}
	}
	return out, nil
}

func GetStep(db *sql.DB, id int64) (*Step, error) {
	row := db.QueryRow(`
		SELECT s.id,s.assembly_id,s.sort,s.item_type,s.title,s.body,s.child_assembly_id,s.slot_id,
		       COALESCE(c.name,''), COALESCE(v.name,'')
		FROM manual_step s
		LEFT JOIN assembly c     ON c.id = s.child_assembly_id
		LEFT JOIN variant_slot v ON v.id = s.slot_id
		WHERE s.id = ?`, id)
	var st Step
	err := row.Scan(&st.ID, &st.AssemblyID, &st.Sort, &st.ItemType, &st.Title, &st.Body,
		&st.ChildAssemblyID, &st.SlotID, &st.ChildName, &st.SlotName)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	st.Images, err = stepImages(db, id)
	if err != nil {
		return nil, err
	}
	if st.ItemType == "document" {
		st.Document, err = StepDocument(db, id)
	}
	return &st, err
}

func AddStep(db *sql.DB, st *Step) (int64, error) {
	var maxSort sql.NullInt64
	if err := db.QueryRow(`SELECT MAX(sort) FROM manual_step WHERE assembly_id=?`, st.AssemblyID).Scan(&maxSort); err != nil {
		return 0, err
	}
	st.Sort = int(maxSort.Int64)
	if maxSort.Valid {
		st.Sort++
	}
	res, err := db.Exec(`INSERT INTO manual_step (assembly_id,sort,item_type,title,body,child_assembly_id,slot_id)
		VALUES (?,?,?,?,?,?,?)`, st.AssemblyID, st.Sort, st.ItemType, st.Title, st.Body, st.ChildAssemblyID, st.SlotID)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func UpdateStep(db *sql.DB, st *Step) error {
	_, err := db.Exec(`UPDATE manual_step SET title=?,body=?,updated_at=datetime('now') WHERE id=?`, st.Title, st.Body, st.ID)
	return err
}

func DeleteStep(db *sql.DB, id int64) error {
	_, err := db.Exec(`DELETE FROM manual_step WHERE id=?`, id)
	return err
}

// MoveStep swaps sort with the neighbouring step in the given direction, so
// ordering can be adjusted without renumbering the whole manual.
func MoveStep(db *sql.DB, id int64, dir int) error {
	st, err := GetStep(db, id)
	if err != nil || st == nil {
		return fmt.Errorf("step %d not found", id)
	}
	var neighbourID int64
	var neighbourSort int
	q := `SELECT id,sort FROM manual_step WHERE assembly_id=? AND sort < ? ORDER BY sort DESC LIMIT 1`
	if dir > 0 {
		q = `SELECT id,sort FROM manual_step WHERE assembly_id=? AND sort > ? ORDER BY sort ASC LIMIT 1`
	}
	err = db.QueryRow(q, st.AssemblyID, st.Sort).Scan(&neighbourID, &neighbourSort)
	if err == sql.ErrNoRows {
		return nil // already at the end; not an error
	}
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE manual_step SET sort=? WHERE id=?`, neighbourSort, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE manual_step SET sort=? WHERE id=?`, st.Sort, neighbourID); err != nil {
		return err
	}
	return tx.Commit()
}

// ---------- step images ----------

const MaxImageBytes = 10 << 20 // 10 MiB

var ImageMimes = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true,
}

func AddImage(db *sql.DB, stepID int64, filename, mime string, data []byte, caption string) (int64, error) {
	if !ImageMimes[mime] {
		return 0, fmt.Errorf("unsupported image type %q (png, jpeg, gif or webp)", mime)
	}
	if len(data) == 0 {
		return 0, fmt.Errorf("empty file")
	}
	if len(data) > MaxImageBytes {
		return 0, fmt.Errorf("image is %d bytes, limit is %d", len(data), MaxImageBytes)
	}
	var maxSort sql.NullInt64
	if err := db.QueryRow(`SELECT MAX(sort) FROM step_image WHERE step_id=?`, stepID).Scan(&maxSort); err != nil {
		return 0, err
	}
	sort := 0
	if maxSort.Valid {
		sort = int(maxSort.Int64) + 1
	}
	res, err := db.Exec(`INSERT INTO step_image (step_id,sort,filename,mime,caption,size,bytes)
		VALUES (?,?,?,?,?,?,?)`, stepID, sort, filename, mime, caption, len(data), data)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func stepImages(db *sql.DB, stepID int64) ([]Image, error) {
	rows, err := db.Query(`SELECT id,step_id,sort,filename,mime,caption,size FROM step_image WHERE step_id=? ORDER BY sort, id`, stepID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Image
	for rows.Next() {
		var im Image
		if err := rows.Scan(&im.ID, &im.StepID, &im.Sort, &im.Filename, &im.Mime, &im.Caption, &im.Size); err != nil {
			return nil, err
		}
		out = append(out, im)
	}
	return out, rows.Err()
}

func ImageBytes(db *sql.DB, id int64) ([]byte, string, error) {
	var b []byte
	var mime string
	err := db.QueryRow(`SELECT bytes,mime FROM step_image WHERE id=?`, id).Scan(&b, &mime)
	return b, mime, err
}

func DeleteImage(db *sql.DB, id int64) error {
	_, err := db.Exec(`DELETE FROM step_image WHERE id=?`, id)
	return err
}

// ---------- step documents (embedded iBOM / 3D model) ----------

// Embedded assets are self-contained files: an iBOM is ~300 KiB of HTML, a glTF
// binary model is a few MiB. A step carries at most one.
const MaxDocumentBytes = 64 << 20 // 64 MiB

// SniffDocument identifies an embedded asset by content and returns the mime it
// must be served with. We only store files we can actually render, so a file
// that is neither gets refused on the way in rather than failing later in the
// browser.
func SniffDocument(data []byte) (mime string, ok bool) {
	// InteractiveHtmlBom: identified by the pcbdata blob the importer decodes.
	if ibomBlobRe.Match(data) {
		return "text/html; charset=utf-8", true
	}
	// glTF binary: magic "glTF" (0x46546C67), then a uint32 version that must be
	// 2, then the total length. Checking all three rejects a coincidental match.
	if len(data) >= 12 && string(data[0:4]) == "glTF" {
		ver := uint32(data[4]) | uint32(data[5])<<8 | uint32(data[6])<<16 | uint32(data[7])<<24
		if ver == 2 {
			return "model/gltf-binary", true
		}
	}
	return "", false
}

// AddDocument attaches an asset to a step. A step holds one; re-adding replaces
// it, whichever kind it was, which is how a re-exported board or model is
// updated in place.
func AddDocument(db *sql.DB, stepID int64, filename string, data []byte) (int64, error) {
	if len(data) == 0 {
		return 0, fmt.Errorf("empty file")
	}
	if len(data) > MaxDocumentBytes {
		return 0, fmt.Errorf("file is %d bytes, limit is %d", len(data), MaxDocumentBytes)
	}
	mime, ok := SniffDocument(data)
	if !ok {
		return 0, fmt.Errorf("unrecognised file: expected an InteractiveHtmlBom export or a glTF binary model")
	}
	if _, err := db.Exec(`DELETE FROM document WHERE step_id=?`, stepID); err != nil {
		return 0, err
	}
	res, err := db.Exec(`INSERT INTO document (step_id,filename,mime,size,bytes) VALUES (?,?,?,?,?)`,
		stepID, filename, mime, len(data), data)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// StepDocument returns the document attached to a step, or nil. The bytes are
// not loaded here — only metadata — so a manual page with many document Steps
// does not pull every board into memory.
func StepDocument(db *sql.DB, stepID int64) (*Document, error) {
	var d Document
	err := db.QueryRow(`SELECT id,step_id,filename,mime,size FROM document WHERE step_id=?`, stepID).
		Scan(&d.ID, &d.StepID, &d.Filename, &d.Mime, &d.Size)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

func DocumentBytes(db *sql.DB, id int64) ([]byte, string, error) {
	var b []byte
	var mime string
	err := db.QueryRow(`SELECT bytes,mime FROM document WHERE id=?`, id).Scan(&b, &mime)
	return b, mime, err
}

func DeleteDocument(db *sql.DB, id int64) error {
	_, err := db.Exec(`DELETE FROM document WHERE id=?`, id)
	return err
}
