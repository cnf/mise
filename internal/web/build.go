package web

import (
	"encoding/json"
	"net/http"
	"strconv"

	"example.com/cnf/mise/internal/bom"
	"example.com/cnf/mise/internal/store"
)

// ---------- builds ----------

func (s *Server) builds(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		aid := parseOptInt(r.FormValue("assembly_id"))
		if aid == nil {
			http.Error(w, "assembly required", 400)
			return
		}
		id, err := store.UpsertBuild(s.db, &store.Build{Name: r.FormValue("name"), AssemblyID: *aid})
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		http.Redirect(w, r, "/builds/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
		return
	}
	bs, _ := store.ListBuilds(s.db)
	asms, _ := store.ListAssemblies(s.db)
	names := map[int64]string{}
	for _, a := range asms {
		names[a.ID] = a.Name
	}
	s.render(w, "builds.html", map[string]any{"Builds": bs, "Assemblies": asms, "AsmNames": names})
}

func (s *Server) buildByID(w http.ResponseWriter, r *http.Request) {
	id, err := idFromPath(r.URL.Path, "/builds/")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	b, err := store.GetBuild(s.db, id)
	if err != nil || b == nil {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		var opErr error
		switch r.FormValue("_action") {
		case "freeze":
			opErr = bom.FreezeBuild(s.db, id)
		case "rename":
			b.Name = r.FormValue("name")
			_, opErr = store.UpsertBuild(s.db, b)
		case "delete":
			if opErr = store.DeleteBuild(s.db, id); opErr == nil {
				http.Redirect(w, r, "/builds", http.StatusSeeOther)
				return
			}
		}
		if opErr != nil {
			http.Error(w, opErr.Error(), 400)
			return
		}
		http.Redirect(w, r, "/builds/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
		return
	}
	sel := store.ParseSelection(b.Selections)
	g, err := bom.Gather(s.db, b.AssemblyID, sel)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	g.Selection = sel
	a, _ := store.GetAssembly(s.db, b.AssemblyID)
	var snap *bom.GatherResult
	if b.Snapshot != nil {
		var sr bom.GatherResult
		if json.Unmarshal([]byte(*b.Snapshot), &sr) == nil {
			snap = &sr
		}
	}
	// slots reachable from this build's root, with their options and the
	// build's current choice, ready for the picker
	slots, err := store.SlotsUnder(s.db, b.AssemblyID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	type slotView struct {
		Slot     store.VariantSlot
		Options  []store.Option
		Selected int64 // chosen option id, 0 = use the slot default
	}
	views := make([]slotView, 0, len(slots))
	for _, sl := range slots {
		opts, err := store.OptionsForSlot(s.db, sl.ID)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		v := slotView{Slot: sl, Options: opts}
		if oid, ok := sel[sl.ID]; ok {
			v.Selected = oid
		}
		views = append(views, v)
	}
	s.render(w, "build_view.html", map[string]any{
		"Build": b, "Gather": g, "Assembly": a, "Snapshot": snap,
		"Frozen": snap, "Sel": sel, "SlotViews": views,
		"Stale": snap != nil && !sameSnapshot(g, snap),
	})
}

// buildSelect records a slot choice on a build. Choices are per-build, which is
// what makes "the same hardware, PCB rev B" a named, reproducible thing.
func (s *Server) buildSelect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	bid, err := idFromPath(r.URL.Path, "/builds/select/")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	b, err := store.GetBuild(s.db, bid)
	if err != nil || b == nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	sel := store.ParseSelection(b.Selections)
	slotID := parseOptInt(r.FormValue("slot_id"))
	optID := parseOptInt(r.FormValue("option_id"))
	if slotID == nil {
		http.Error(w, "slot required", 400)
		return
	}
	if optID == nil || r.FormValue("option_id") == "" {
		delete(sel, *slotID) // back to the slot's default
	} else if err := sel.Set(s.db, *slotID, *optID); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	b.Selections = sel.JSON()
	if _, err := store.UpsertBuild(s.db, b); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, "/builds/"+strconv.FormatInt(bid, 10), http.StatusSeeOther)
}

// sameSnapshot reports whether the live gather matches the frozen one for the
// parts that matter. A build whose assembly has been edited since freezing
// should say so rather than silently showing two different lists.
func sameSnapshot(live, frozen *bom.GatherResult) bool {
	if live == nil || frozen == nil {
		return true
	}
	q := map[int64]float64{}
	for _, p := range frozen.Total {
		q[p.PartID] = p.Qty
	}
	if len(q) != len(live.Total) {
		return false
	}
	for _, p := range live.Total {
		if got, ok := q[p.PartID]; !ok || got != p.Qty {
			return false
		}
	}
	return true
}
