package web

import (
	"database/sql"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"

	"example.com/cnf/mise/internal/ibom"
	"example.com/cnf/mise/internal/partdb"
	"example.com/cnf/mise/internal/store"
)

// ---------- part-db / ibom ----------

func (s *Server) partdbSync(w http.ResponseWriter, r *http.Request) {
	n, err := partdb.SyncPartDB(s.db)
	if err != nil {
		http.Error(w, "Part-DB sync failed: "+err.Error(), 502)
		return
	}
	http.Redirect(w, r, "/parts?synced="+strconv.Itoa(n), http.StatusSeeOther)
}

func (s *Server) partdbSearchAPI(w http.ResponseWriter, r *http.Request) {
	res, err := partdb.SearchCache(s.db, r.URL.Query().Get("q"))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}

func (s *Server) ibomImport(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		f, _, err := r.FormFile("ibom")
		if err != nil {
			http.Error(w, "file required", 400)
			return
		}
		defer f.Close()
		name := r.FormValue("assembly_name")
		raw, err := io.ReadAll(io.LimitReader(f, store.MaxDocumentBytes+1))
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		id, n, err := ibom.ImportIBOM(s.db, raw, name)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		// Keep the board itself and put it on the assembly's manual, so the
		// imported layout is viewable without a second manual upload. This is
		// best-effort: the BOM import already succeeded, so a step that cannot
		// be created is reported, not fatal.
		stepNote := ""
		if err := attachImportedIBOM(s.db, id, raw); err != nil {
			stepNote = "&ibom_step_error=" + url.QueryEscape(err.Error())
			log.Printf("ibom import %d: could not attach the layout to the manual: %v", id, err)
		}
		http.Redirect(w, r, "/assemblies/"+strconv.FormatInt(id, 10)+"?imported="+strconv.Itoa(n)+stepNote, http.StatusSeeOther)
		return
	}
	s.render(w, "ibom_import.html", map[string]any{})
}

// attachImportedIBOM stores an imported board and puts it on the assembly's
// manual as a document step, so "import an iBOM" and "see the board layout" are
// one action instead of two. Re-importing replaces the board and does not stack
// duplicate steps.
func attachImportedIBOM(db *sql.DB, asmID int64, raw []byte) error {
	existing, err := store.Steps(db, asmID)
	if err != nil {
		return err
	}
	stepID := int64(0)
	for i := range existing {
		if existing[i].ItemType == "document" && existing[i].Document != nil {
			stepID = existing[i].ID
			break
		}
	}
	if stepID == 0 {
		stepID, err = store.AddStep(db, &store.Step{
			AssemblyID: asmID,
			ItemType:   "document",
			Title:      "Board layout",
		})
		if err != nil {
			return err
		}
	}
	_, err = store.AddDocument(db, stepID, "board.html", raw)
	return err
}
