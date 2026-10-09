package web

import (
	"net/http"
	"strconv"
	"strings"

	"example.com/cnf/mise/internal/store"
)

// ---------- assemblies ----------

func (s *Server) assemblies(w http.ResponseWriter, r *http.Request) {
	asms, err := store.ListAssemblies(s.db)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	type row struct {
		store.Assembly
		LineCount int
		SlotCount int
	}
	rows := make([]row, 0, len(asms))
	for _, a := range asms {
		var n, sl int
		_ = s.db.QueryRow(`SELECT COUNT(*) FROM assembly_line WHERE assembly_id=?`, a.ID).Scan(&n)
		_ = s.db.QueryRow(`SELECT COUNT(*) FROM variant_slot WHERE assembly_id=?`, a.ID).Scan(&sl)
		rows = append(rows, row{a, n, sl})
	}
	s.render(w, "assemblies.html", map[string]any{"Rows": rows})
}

func (s *Server) assemblyNew(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.render(w, "assembly_edit.html", map[string]any{"Assembly": &store.Assembly{}})
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	a := &store.Assembly{Name: strings.TrimSpace(r.FormValue("name")), Description: r.FormValue("description")}
	if a.Name == "" {
		http.Error(w, "name required", 400)
		return
	}
	id, err := store.UpsertAssembly(s.db, a)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, "/assemblies/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func (s *Server) assemblyByID(w http.ResponseWriter, r *http.Request) {
	// Edit is opt-in. A GET is the read surface; ?edit=1 turns the same page
	// into its editor. Without this the page was 30 form controls and nothing
	// said which mode you were in, so there was no way to just look at it.
	edit := r.URL.Query().Get("edit") != ""
	pth := strings.TrimPrefix(r.URL.Path, "/assemblies/")
	if strings.HasSuffix(pth, "/edit") {
		id, err := strconv.ParseInt(strings.TrimSuffix(pth, "/edit"), 10, 64)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		a, err := store.GetAssembly(s.db, id)
		if err != nil || a == nil {
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodPost {
			_ = r.ParseForm()
			a.Name = strings.TrimSpace(r.FormValue("name"))
			a.Description = r.FormValue("description")
			if a.Name == "" {
				http.Error(w, "name required", 400)
				return
			}
			if _, err := store.UpsertAssembly(s.db, a); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			http.Redirect(w, r, "/assemblies/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
			return
		}
		s.render(w, "assembly_edit.html", map[string]any{"Assembly": a})
		return
	}
	id, err := strconv.ParseInt(strings.Trim(pth, "/"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	a, err := store.GetAssembly(s.db, id)
	if err != nil || a == nil {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		// A read-mode action only changes which view you see.
		if act := r.FormValue("_action"); act == "read" || act == "edit" {
			target := "/assemblies/" + strconv.FormatInt(id, 10)
			if act == "edit" {
				target += "?edit=1"
			}
			http.Redirect(w, r, target, http.StatusSeeOther)
			return
		}
		var opErr error
		switch r.FormValue("_action") {
		case "add_part":
			pid := parseOptInt(r.FormValue("part_id"))
			if pid == nil {
				http.Error(w, "part required", 400)
				return
			}
			_, opErr = store.AddLine(s.db, &store.Line{AssemblyID: id, ItemType: "part", PartID: pid, Qty: orFloat(r.FormValue("qty"), 1)})
		case "add_sub":
			cid := parseOptInt(r.FormValue("child_assembly_id"))
			if cid == nil {
				http.Error(w, "child required", 400)
				return
			}
			if *cid == id {
				http.Error(w, "cannot add an assembly to itself", 400)
				return
			}
			if opErr = store.WouldCycle(s.db, id, *cid); opErr != nil {
				http.Error(w, opErr.Error(), 400)
				return
			}
			_, opErr = store.AddLine(s.db, &store.Line{AssemblyID: id, ItemType: "assembly", ChildAssemblyID: cid, Qty: orFloat(r.FormValue("qty"), 1)})
		case "add_slot":
			name := strings.TrimSpace(r.FormValue("slot_name"))
			if name == "" {
				http.Error(w, "slot name required", 400)
				return
			}
			sid, e := store.AddSlot(s.db, &store.VariantSlot{AssemblyID: id, Name: name, Notes: r.FormValue("slot_notes")})
			if e != nil {
				http.Error(w, e.Error(), 500)
				return
			}
			_, opErr = store.AddLine(s.db, &store.Line{AssemblyID: id, ItemType: "slot", SlotID: &sid, Qty: 1, Notes: r.FormValue("slot_notes")})
		case "delete":
			if opErr = store.DeleteAssembly(s.db, id); opErr != nil {
				http.Error(w, opErr.Error(), 409)
				return
			}
			http.Redirect(w, r, "/assemblies", http.StatusSeeOther)
			return
		}
		if opErr != nil {
			http.Error(w, opErr.Error(), 500)
			return
		}
		// Mutations return to the mode you were in, so editing does not bounce
		// you out into read mode mid-task.
		target := "/assemblies/" + strconv.FormatInt(id, 10)
		if edit {
			target += "?edit=1"
		}
		http.Redirect(w, r, target, http.StatusSeeOther)
		return
	}
	ls, err := store.Lines(s.db, id)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	parts, _ := store.ListParts(s.db, "")
	all, _ := store.ListAssemblies(s.db)
	hasDoc := false
	if ss, err := store.Steps(s.db, id); err == nil {
		for i := range ss {
			if ss[i].Document != nil {
				hasDoc = true
				break
			}
		}
	}
	s.render(w, "assembly_view.html", map[string]any{
		"Assembly": a, "Lines": ls, "Parts": parts, "All": all,
		"Back": "/assemblies/" + strconv.FormatInt(id, 10),
		"Edit": edit,
		// the import redirect reports what happened, so the page says it rather
		// than leaving you to infer it
		"Imported":    r.URL.Query().Get("imported"),
		"ImportErr":   r.URL.Query().Get("ibom_step_error"),
		"HasDocument": hasDoc,
	})
}

func (s *Server) lineByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	id, err := idTail(r.URL.Path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	back := orDefault(r.FormValue("_back"), "/")
	switch r.FormValue("_action") {
	case "delete":
		if err := store.DeleteLine(s.db, id); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	case "update":
		if err := store.UpdateLineQty(s.db, id, orFloat(r.FormValue("qty"), 1), r.FormValue("dnp") != ""); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

func (s *Server) slotByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	id, err := idFromPath(r.URL.Path, "/slots/")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	_ = r.ParseForm()
	back := orDefault(r.FormValue("_back"), "/")
	switch r.FormValue("_action") {
	case "add_option":
		o := &store.Option{
			SlotID:     id,
			Label:      strings.TrimSpace(r.FormValue("label")),
			TargetType: r.FormValue("target_type"),
			Qty:        orFloat(r.FormValue("qty"), 1),
			Notes:      r.FormValue("notes"),
			IsDefault:  r.FormValue("is_default") != "",
		}
		if o.Label == "" {
			http.Error(w, "label required", 400)
			return
		}
		sl, err := store.GetSlot(s.db, id)
		if err != nil || sl == nil {
			http.NotFound(w, r)
			return
		}
		switch o.TargetType {
		case "part":
			o.PartID = parseOptInt(r.FormValue("part_id"))
			if o.PartID == nil {
				http.Error(w, "pick a part for the option", 400)
				return
			}
		case "assembly":
			o.ChildAssemblyID = parseOptInt(r.FormValue("child_assembly_id"))
			if o.ChildAssemblyID == nil {
				http.Error(w, "pick an assembly for the option", 400)
				return
			}
			if *o.ChildAssemblyID == sl.AssemblyID {
				http.Error(w, "an option cannot be its own assembly", 400)
				return
			}
			if err := store.WouldCycle(s.db, sl.AssemblyID, *o.ChildAssemblyID); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
		default:
			http.Error(w, "target_type must be part or assembly", 400)
			return
		}
		if _, err := store.AddOption(s.db, o); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	case "delete":
		if err := store.DeleteSlot(s.db, id); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

func (s *Server) optionByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	id, err := idFromPath(r.URL.Path, "/options/")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	_ = r.ParseForm()
	back := orDefault(r.FormValue("_back"), "/")
	if r.FormValue("_action") == "delete" {
		if err := store.DeleteOption(s.db, id); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}
