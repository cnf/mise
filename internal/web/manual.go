package web

import (
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"example.com/cnf/mise/internal/bom"
	"example.com/cnf/mise/internal/store"
)

// ---------- manual ----------

// manualByID renders the authored manual for one assembly. With no query string
// it edits the assembly's own steps; ?build=N or ?sel=slot:option,... renders it
// as a build would follow it, resolving variant references.
func (s *Server) manualByID(w http.ResponseWriter, r *http.Request) {
	id, err := idFromPath(r.URL.Path, "/manuals/")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	a, err := store.GetAssembly(s.db, id)
	if err != nil || a == nil {
		http.NotFound(w, r)
		return
	}
	sel := store.Selection{}
	label := ""
	if bs := r.URL.Query().Get("build"); bs != "" {
		if bid, err := strconv.ParseInt(bs, 10, 64); err == nil {
			if b, err := store.GetBuild(s.db, bid); err == nil && b != nil {
				sel = store.ParseSelection(b.Selections)
				label = "build: " + orDefault(b.Name, fmt.Sprintf("#%d", b.ID))
			}
		}
	}
	m, err := bom.Manual(s.db, id, sel)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	// Read is the default; ?edit=1 is the editor. Back-links carry the mode so
	// an edit action returns you to editing rather than dumping you into read.
	edit := r.URL.Query().Get("edit") != ""
	back := "/manuals/" + strconv.FormatInt(id, 10)
	if edit {
		back += "?edit=1"
	}
	if b := r.URL.Query().Get("build"); b != "" {
		sep := "?"
		if edit {
			sep = "&"
		}
		back += sep + "build=" + url.QueryEscape(b)
	}
	m.Edit = edit
	m.Back = back
	m.PropagateMode(edit, back)
	parts, _ := store.ListParts(s.db, "")
	all, _ := store.ListAssemblies(s.db)
	slots, _ := store.ListSlots(s.db, id)
	s.render(w, "manual_view.html", map[string]any{
		"Assembly": a, "Manual": m, "Parts": parts, "All": all,
		"Slots": slots, "Label": label, "BuildQuery": r.URL.Query().Get("build"),
		"Back":        back,
		"Edit":        edit,
		"NeedsViewer": m.HasModel(),
	})
}

func (s *Server) stepByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	id, err := idFromPath(r.URL.Path, "/steps/")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	back := orDefault(r.FormValue("_back"), "/")
	st, err := store.GetStep(s.db, id)
	if err != nil || st == nil {
		http.NotFound(w, r)
		return
	}
	switch r.FormValue("_action") {
	case "delete":
		if err := store.DeleteStep(s.db, id); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	case "update":
		st.Title = r.FormValue("title")
		st.Body = r.FormValue("body")
		if err := store.UpdateStep(s.db, st); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	case "up":
		if err := store.MoveStep(s.db, id, -1); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	case "down":
		if err := store.MoveStep(s.db, id, 1); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// addStepHandler is called from the manual view with the owning assembly.
func (s *Server) stepNew(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	assemblyID := parseOptInt(r.FormValue("assembly_id"))
	if assemblyID == nil {
		http.Error(w, "assembly_id required", 400)
		return
	}
	back := orDefault(r.FormValue("_back"), "/manuals/"+strconv.FormatInt(*assemblyID, 10))
	st := &store.Step{
		AssemblyID: *assemblyID,
		ItemType:   orDefault(r.FormValue("item_type"), "note"),
		Title:      r.FormValue("title"),
		Body:       r.FormValue("body"),
	}
	switch st.ItemType {
	case "note", "document":
		// document steps carry no reference; the file is attached after creation
	case "assembly":
		st.ChildAssemblyID = parseOptInt(r.FormValue("child_assembly_id"))
		if st.ChildAssemblyID == nil {
			http.Error(w, "pick a sub-assembly", 400)
			return
		}
		if *st.ChildAssemblyID == *assemblyID {
			http.Error(w, "a step cannot follow its own assembly", 400)
			return
		}
		if err := wouldCycleManual(s.db, *assemblyID, *st.ChildAssemblyID); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
	case "slot":
		st.SlotID = parseOptInt(r.FormValue("slot_id"))
		if st.SlotID == nil {
			http.Error(w, "pick a slot", 400)
			return
		}
		sl, err := store.GetSlot(s.db, *st.SlotID)
		if err != nil || sl == nil || sl.AssemblyID != *assemblyID {
			http.Error(w, "slot does not belong to this assembly", 400)
			return
		}
	default:
		http.Error(w, "unknown step type", 400)
		return
	}
	if _, err := store.AddStep(s.db, st); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// documentUpload stores an iBOM against a document step, shown embedded in the
// manual. Re-uploading replaces it, so a re-exported board is updated in place.
func (s *Server) documentUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseMultipartForm(store.MaxDocumentBytes + (1 << 20)); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	stepID := parseOptInt(r.FormValue("step_id"))
	if stepID == nil {
		http.Error(w, "step_id required", 400)
		return
	}
	// Only a document step renders an embedded board; attaching one to any other
	// step would store bytes no page ever shows.
	st, err := store.GetStep(s.db, *stepID)
	if err != nil || st == nil {
		http.Error(w, "no such step", 404)
		return
	}
	if st.ItemType != "document" {
		http.Error(w, "only a document step can carry an embedded board", 400)
		return
	}
	back := orDefault(r.FormValue("_back"), "/")
	fh, hdr, err := r.FormFile("document")
	if err != nil {
		http.Error(w, "file required", 400)
		return
	}
	defer fh.Close()
	data, err := io.ReadAll(io.LimitReader(fh, store.MaxDocumentBytes+1))
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if _, err := store.AddDocument(s.db, *stepID, hdr.Filename, data); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// documentByID serves an embedded asset. How it is served depends on what it
// is. An iBOM is HTML the browser must run to render the board, so it is served
// as text/html with `Content-Security-Policy: sandbox allow-scripts`, which
// puts it in an opaque origin: its scripts run but it cannot reach the parent
// page or the network. A glTF binary is inert data, and must NOT be served as
// HTML — it goes out with its own type and nosniff so the browser loads it as a
// model and never as markup. This is the only place mise serves user-supplied
// bytes, so both restrictions live on this one response.
func (s *Server) documentByID(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/documents/")
	id, err := strconv.ParseInt(strings.Split(strings.Trim(path, "/"), "/")[0], 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodPost {
		_ = r.ParseForm()
		back := orDefault(r.FormValue("_back"), "/")
		if r.FormValue("_action") == "delete" {
			if err := store.DeleteDocument(s.db, id); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
		}
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	data, mime, err := store.DocumentBytes(s.db, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if mime == "text/html; charset=utf-8" {
		w.Header().Set("Content-Security-Policy", "sandbox allow-scripts")
	}
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(data)
}

// modelViewerJS serves the vendored <model-viewer> bundle. It is embedded in the
// binary (assets/model-viewer.min.js) and served from our own origin, so the
// viewer needs no CDN at runtime.
func (s *Server) modelViewerJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=604800")
	_, _ = w.Write(modelViewerJSBytes)
}

func (s *Server) imageByID(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/images/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodPost {
		_ = r.ParseForm()
		back := orDefault(r.FormValue("_back"), "/")
		if r.FormValue("_action") == "delete" {
			if err := store.DeleteImage(s.db, id); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
		}
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	data, mime, err := store.ImageBytes(s.db, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(data)
}

// imageUpload stores one or more images against a step.
func (s *Server) imageUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseMultipartForm(store.MaxImageBytes + (1 << 20)); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	stepID := parseOptInt(r.FormValue("step_id"))
	if stepID == nil {
		http.Error(w, "step_id required", 400)
		return
	}
	back := orDefault(r.FormValue("_back"), "/")
	fhs := r.MultipartForm.File["image"]
	if len(fhs) == 0 {
		http.Error(w, "no file", 400)
		return
	}
	var failures []string
	for _, fh := range fhs {
		f, err := fh.Open()
		if err != nil {
			failures = append(failures, fh.Filename+": "+err.Error())
			continue
		}
		data, err := io.ReadAll(io.LimitReader(f, store.MaxImageBytes+1))
		f.Close()
		if err != nil {
			failures = append(failures, fh.Filename+": "+err.Error())
			continue
		}
		mime := fh.Header.Get("Content-Type")
		if mime == "" || !store.ImageMimes[mime] {
			mime = http.DetectContentType(data)
		}
		if _, err := store.AddImage(s.db, *stepID, fh.Filename, mime, data, r.FormValue("caption")); err != nil {
			failures = append(failures, fh.Filename+": "+err.Error())
		}
	}
	if len(failures) > 0 {
		http.Error(w, strings.Join(failures, "; "), 400)
		return
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// wouldCycleManual mirrors wouldCycle for manual step references.
func wouldCycleManual(db *sql.DB, parent, child int64) error {
	seen := map[int64]bool{}
	var walk func(id int64, depth int) bool
	walk = func(id int64, depth int) bool {
		if id == parent {
			return true
		}
		if depth > 64 || seen[id] {
			return false
		}
		seen[id] = true
		rows, err := db.Query(`SELECT child_assembly_id FROM manual_step WHERE assembly_id=? AND item_type='assembly' AND child_assembly_id IS NOT NULL`, id)
		if err != nil {
			return false
		}
		var kids []int64
		for rows.Next() {
			var c int64
			if rows.Scan(&c) == nil {
				kids = append(kids, c)
			}
		}
		rows.Close()
		for _, c := range kids {
			if walk(c, depth+1) {
				return true
			}
		}
		return false
	}
	if walk(child, 0) {
		return fmt.Errorf("adding this would create a circular manual reference")
	}
	return nil
}
