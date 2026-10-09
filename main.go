package main

import (
	"database/sql"
	"embed"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

//go:embed templates/*.html
var templateFiles embed.FS

//go:embed assets/model-viewer.min.js
var modelViewerJSBytes []byte

type server struct {
	db   *sql.DB
	tmpl *template.Template
}

func main() {
	var (
		addr   = flag.String("addr", ":8080", "listen address")
		dbPath = flag.String("db", "mise.db", "path to sqlite database")
	)
	flag.Parse()

	db, err := openDB(*dbPath)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer db.Close()

	tmpl, err := template.New("").Funcs(template.FuncMap{
		"trimNum": trimNum,
		"price":   func(p *float64) string { return strFmt(p) },
		"mul":     func(p *float64, q float64) string { return mulFmt(p, q) },
		"inc":     func(i int) int { return i + 1 },
	}).ParseFS(templateFiles, "templates/*.html")
	if err != nil {
		log.Fatalf("templates: %v", err)
	}

	s := &server{db: db, tmpl: tmpl}
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.home)
	mux.HandleFunc("/parts", s.parts)
	mux.HandleFunc("/parts/new", s.partNew)
	mux.HandleFunc("/parts/", s.partByID)
	mux.HandleFunc("/assemblies", s.assemblies)
	mux.HandleFunc("/assemblies/new", s.assemblyNew)
	mux.HandleFunc("/assemblies/", s.assemblyByID)
	mux.HandleFunc("/lines/", s.lineByID)
	mux.HandleFunc("/slots/", s.slotByID)
	mux.HandleFunc("/options/", s.optionByID)
	mux.HandleFunc("/builds", s.builds)
	mux.HandleFunc("/builds/", s.buildByID)
	mux.HandleFunc("/builds/select/", s.buildSelect)
	mux.HandleFunc("/gather/", s.gatherPage)
	mux.HandleFunc("/export/gather/", s.exportCSV)
	mux.HandleFunc("/export/snapshot/", s.exportSnapshotCSV)
	mux.HandleFunc("/partdb/sync", s.partdbSync)
	mux.HandleFunc("/api/partdb/search", s.partdbSearchAPI)
	mux.HandleFunc("/ibom/import", s.ibomImport)
	mux.HandleFunc("/manuals/", s.manualByID)
	mux.HandleFunc("/steps/new", s.stepNew)
	mux.HandleFunc("/steps/upload", s.imageUpload)
	mux.HandleFunc("/steps/document", s.documentUpload)
	mux.HandleFunc("/steps/", s.stepByID)
	mux.HandleFunc("/images/", s.imageByID)
	mux.HandleFunc("/documents/", s.documentByID)
	mux.HandleFunc("/assets/model-viewer.min.js", s.modelViewerJS)

	log.Printf("mise listening on %s (db=%s)", *addr, *dbPath)
	log.Fatal(http.ListenAndServe(*addr, mux))
}

func (s *server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("template %s: %v", name, err)
	}
}

func (s *server) home(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	parts, _ := listParts(s.db, "")
	asms, _ := listAssemblies(s.db)
	builds, _ := listBuilds(s.db)
	s.render(w, "home.html", map[string]any{
		"Parts": parts, "Assemblies": asms, "Builds": builds,
		"PartCount": len(parts), "AsmCount": len(asms), "BuildCount": len(builds),
	})
}

// ---------- parts ----------

func (s *server) parts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	parts, err := listParts(s.db, q)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.render(w, "parts.html", map[string]any{"Parts": parts, "Q": q, "Kinds": partKinds})
}

func (s *server) partNew(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.render(w, "part_edit.html", map[string]any{"Part": &Part{Currency: "EUR", Kind: "other", Attrs: "{}"}, "Kinds": partKinds})
		return
	}
	s.savePart(w, r, &Part{})
}

func (s *server) savePart(w http.ResponseWriter, r *http.Request, p *Part) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	p.Name = strings.TrimSpace(r.FormValue("name"))
	p.Kind = orDefault(r.FormValue("kind"), "other")
	p.Description = r.FormValue("description")
	p.Currency = orDefault(r.FormValue("currency"), "EUR")
	p.VendorURL = r.FormValue("vendor_url")
	p.DatasheetURL = r.FormValue("datasheet_url")
	p.Attrs = orDefault(r.FormValue("attrs"), "{}")
	p.Notes = r.FormValue("notes")
	p.UnitPrice = parseOptFloat(r.FormValue("unit_price"))
	p.PartDBID = parseOptInt(r.FormValue("partdb_id"))
	if p.Name == "" {
		http.Error(w, "name required", 400)
		return
	}
	if p.Attrs != "{}" && !json.Valid([]byte(p.Attrs)) {
		http.Error(w, "attrs must be valid JSON", 400)
		return
	}
	id, err := upsertPart(s.db, p)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, "/parts/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func (s *server) partByID(w http.ResponseWriter, r *http.Request) {
	pth := strings.TrimPrefix(r.URL.Path, "/parts/")
	idStr := strings.TrimSuffix(strings.Trim(pth, "/"), "/edit")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	p, err := getPart(s.db, id)
	if err != nil || p == nil {
		http.NotFound(w, r)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/edit") {
		if r.Method == http.MethodPost {
			s.savePart(w, r, p)
			return
		}
		s.render(w, "part_edit.html", map[string]any{"Part": p, "Kinds": partKinds})
		return
	}
	if r.Method == http.MethodPost && r.FormValue("_action") == "delete" {
		if err := deletePart(s.db, id); err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		http.Redirect(w, r, "/parts", http.StatusSeeOther)
		return
	}
	s.render(w, "part_view.html", map[string]any{"Part": p, "Kinds": partKinds})
}

// ---------- assemblies ----------

func (s *server) assemblies(w http.ResponseWriter, r *http.Request) {
	asms, err := listAssemblies(s.db)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	type row struct {
		Assembly
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

func (s *server) assemblyNew(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.render(w, "assembly_edit.html", map[string]any{"Assembly": &Assembly{}})
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	a := &Assembly{Name: strings.TrimSpace(r.FormValue("name")), Description: r.FormValue("description")}
	if a.Name == "" {
		http.Error(w, "name required", 400)
		return
	}
	id, err := upsertAssembly(s.db, a)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, "/assemblies/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func (s *server) assemblyByID(w http.ResponseWriter, r *http.Request) {
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
		a, err := getAssembly(s.db, id)
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
			if _, err := upsertAssembly(s.db, a); err != nil {
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
	a, err := getAssembly(s.db, id)
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
			_, opErr = addLine(s.db, &Line{AssemblyID: id, ItemType: "part", PartID: pid, Qty: orFloat(r.FormValue("qty"), 1)})
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
			if opErr = wouldCycle(s.db, id, *cid); opErr != nil {
				http.Error(w, opErr.Error(), 400)
				return
			}
			_, opErr = addLine(s.db, &Line{AssemblyID: id, ItemType: "assembly", ChildAssemblyID: cid, Qty: orFloat(r.FormValue("qty"), 1)})
		case "add_slot":
			name := strings.TrimSpace(r.FormValue("slot_name"))
			if name == "" {
				http.Error(w, "slot name required", 400)
				return
			}
			sid, e := addSlot(s.db, &VariantSlot{AssemblyID: id, Name: name, Notes: r.FormValue("slot_notes")})
			if e != nil {
				http.Error(w, e.Error(), 500)
				return
			}
			_, opErr = addLine(s.db, &Line{AssemblyID: id, ItemType: "slot", SlotID: &sid, Qty: 1, Notes: r.FormValue("slot_notes")})
		case "delete":
			if opErr = deleteAssembly(s.db, id); opErr != nil {
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
	ls, err := lines(s.db, id)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	parts, _ := listParts(s.db, "")
	all, _ := listAssemblies(s.db)
	s.render(w, "assembly_view.html", map[string]any{
		"Assembly": a, "Lines": ls, "Parts": parts, "All": all,
		"Back": "/assemblies/" + strconv.FormatInt(id, 10),
		"Edit": edit,
	})
}

func (s *server) lineByID(w http.ResponseWriter, r *http.Request) {
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
		if err := deleteLine(s.db, id); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	case "update":
		if err := updateLineQty(s.db, id, orFloat(r.FormValue("qty"), 1), r.FormValue("dnp") != ""); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

func (s *server) slotByID(w http.ResponseWriter, r *http.Request) {
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
		o := &Option{
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
		sl, err := getSlot(s.db, id)
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
			if err := wouldCycle(s.db, sl.AssemblyID, *o.ChildAssemblyID); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
		default:
			http.Error(w, "target_type must be part or assembly", 400)
			return
		}
		if _, err := addOption(s.db, o); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	case "delete":
		if err := deleteSlot(s.db, id); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

func (s *server) optionByID(w http.ResponseWriter, r *http.Request) {
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
		if err := deleteOption(s.db, id); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// ---------- builds ----------

func (s *server) builds(w http.ResponseWriter, r *http.Request) {
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
		id, err := upsertBuild(s.db, &Build{Name: r.FormValue("name"), AssemblyID: *aid})
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		http.Redirect(w, r, "/builds/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
		return
	}
	bs, _ := listBuilds(s.db)
	asms, _ := listAssemblies(s.db)
	names := map[int64]string{}
	for _, a := range asms {
		names[a.ID] = a.Name
	}
	s.render(w, "builds.html", map[string]any{"Builds": bs, "Assemblies": asms, "AsmNames": names})
}

func (s *server) buildByID(w http.ResponseWriter, r *http.Request) {
	id, err := idFromPath(r.URL.Path, "/builds/")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	b, err := getBuild(s.db, id)
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
			opErr = freezeBuild(s.db, id)
		case "rename":
			b.Name = r.FormValue("name")
			_, opErr = upsertBuild(s.db, b)
		case "delete":
			if opErr = deleteBuild(s.db, id); opErr == nil {
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
	sel := parseSelection(b.Selections)
	g, err := gather(s.db, b.AssemblyID, sel)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	g.Selection = sel
	a, _ := getAssembly(s.db, b.AssemblyID)
	var snap *GatherResult
	if b.Snapshot != nil {
		var sr GatherResult
		if json.Unmarshal([]byte(*b.Snapshot), &sr) == nil {
			snap = &sr
		}
	}
	// slots reachable from this build's root, with their options and the
	// build's current choice, ready for the picker
	slots, err := slotsUnder(s.db, b.AssemblyID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	type slotView struct {
		Slot     VariantSlot
		Options  []Option
		Selected int64 // chosen option id, 0 = use the slot default
	}
	views := make([]slotView, 0, len(slots))
	for _, sl := range slots {
		opts, err := optionsForSlot(s.db, sl.ID)
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
func (s *server) buildSelect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	bid, err := idFromPath(r.URL.Path, "/builds/select/")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	b, err := getBuild(s.db, bid)
	if err != nil || b == nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	sel := parseSelection(b.Selections)
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
	if _, err := upsertBuild(s.db, b); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, "/builds/"+strconv.FormatInt(bid, 10), http.StatusSeeOther)
}

func (s *server) gatherPage(w http.ResponseWriter, r *http.Request) {
	id, err := idFromPath(r.URL.Path, "/gather/")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	g, err := gather(s.db, id, nil)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.render(w, "gather.html", map[string]any{"Gather": g})
}

// ---------- export ----------

// csvRows is the shared shape of the distributor CSV: one row per part with the
// sub-assemblies it belongs to in the Customer Reference column.
func csvRows(g *GatherResult) [][]string {
	bags := map[int64][]string{}
	for _, grp := range g.Groups {
		for _, l := range grp.Lines {
			bags[l.PartID] = append(bags[l.PartID], grp.AssemblyName)
		}
	}
	rows := [][]string{{"Manufacturer Part Number", "Manufacturer", "Description", "Quantity", "Customer Reference", "Unit Price", "Currency", "Vendor URL"}}
	for _, p := range g.Total {
		price := ""
		if p.UnitPrice != nil {
			price = strconv.FormatFloat(*p.UnitPrice, 'f', -1, 64)
		}
		rows = append(rows, []string{"", "", p.Name, trimNum(p.Qty), strings.Join(bags[p.PartID], ", "), price, p.Currency, p.VendorURL})
	}
	return rows
}

func writeCSV(w http.ResponseWriter, filename string, rows [][]string) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF}) // BOM so Excel opens it cleanly
	cw := csv.NewWriter(w)
	for _, r := range rows {
		_ = cw.Write(r)
	}
	cw.Flush()
}

func (s *server) exportCSV(w http.ResponseWriter, r *http.Request) {
	id, err := idFromPath(r.URL.Path, "/export/gather/")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	g, err := gather(s.db, id, nil)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeCSV(w, "bom-"+sanitizeFile(g.RootName)+".csv", csvRows(g))
}

// exportSnapshotCSV exports exactly what the build was frozen with, which is
// the point of freezing: a built unit can be reproduced from this file even
// after the assemblies have moved on.
func (s *server) exportSnapshotCSV(w http.ResponseWriter, r *http.Request) {
	id, err := idFromPath(r.URL.Path, "/export/snapshot/")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	b, err := getBuild(s.db, id)
	if err != nil || b == nil {
		http.NotFound(w, r)
		return
	}
	if b.Snapshot == nil {
		http.Error(w, "build has not been frozen yet", 409)
		return
	}
	var g GatherResult
	if err := json.Unmarshal([]byte(*b.Snapshot), &g); err != nil {
		http.Error(w, "corrupt snapshot: "+err.Error(), 500)
		return
	}
	name := b.Name
	if name == "" {
		name = fmt.Sprintf("build-%d", b.ID)
	}
	writeCSV(w, "bom-frozen-"+sanitizeFile(name)+".csv", csvRows(&g))
}

// ---------- part-db / ibom ----------

func (s *server) partdbSync(w http.ResponseWriter, r *http.Request) {
	n, err := syncPartDB(s.db)
	if err != nil {
		http.Error(w, "Part-DB sync failed: "+err.Error(), 502)
		return
	}
	http.Redirect(w, r, "/parts?synced="+strconv.Itoa(n), http.StatusSeeOther)
}

func (s *server) partdbSearchAPI(w http.ResponseWriter, r *http.Request) {
	res, err := searchCache(s.db, r.URL.Query().Get("q"))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}

func (s *server) ibomImport(w http.ResponseWriter, r *http.Request) {
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
		id, n, err := importIBOM(s.db, f, name)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		http.Redirect(w, r, "/assemblies/"+strconv.FormatInt(id, 10)+"?imported="+strconv.Itoa(n), http.StatusSeeOther)
		return
	}
	s.render(w, "ibom_import.html", map[string]any{})
}

// ---------- manual ----------

// manualByID renders the authored manual for one assembly. With no query string
// it edits the assembly's own steps; ?build=N or ?sel=slot:option,... renders it
// as a build would follow it, resolving variant references.
func (s *server) manualByID(w http.ResponseWriter, r *http.Request) {
	id, err := idFromPath(r.URL.Path, "/manuals/")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	a, err := getAssembly(s.db, id)
	if err != nil || a == nil {
		http.NotFound(w, r)
		return
	}
	sel := Selection{}
	label := ""
	if bs := r.URL.Query().Get("build"); bs != "" {
		if bid, err := strconv.ParseInt(bs, 10, 64); err == nil {
			if b, err := getBuild(s.db, bid); err == nil && b != nil {
				sel = parseSelection(b.Selections)
				label = "build: " + orDefault(b.Name, fmt.Sprintf("#%d", b.ID))
			}
		}
	}
	m, err := manual(s.db, id, sel)
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
	m.propagateMode(edit, back)
	parts, _ := listParts(s.db, "")
	all, _ := listAssemblies(s.db)
	slots, _ := listSlots(s.db, id)
	s.render(w, "manual_view.html", map[string]any{
		"Assembly": a, "Manual": m, "Parts": parts, "All": all,
		"Slots": slots, "Label": label, "BuildQuery": r.URL.Query().Get("build"),
		"Back":        back,
		"Edit":        edit,
		"NeedsViewer": m.HasModel(),
	})
}

func (s *server) stepByID(w http.ResponseWriter, r *http.Request) {
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
	st, err := getStep(s.db, id)
	if err != nil || st == nil {
		http.NotFound(w, r)
		return
	}
	switch r.FormValue("_action") {
	case "delete":
		if err := deleteStep(s.db, id); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	case "update":
		st.Title = r.FormValue("title")
		st.Body = r.FormValue("body")
		if err := updateStep(s.db, st); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	case "up":
		if err := moveStep(s.db, id, -1); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	case "down":
		if err := moveStep(s.db, id, 1); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// addStepHandler is called from the manual view with the owning assembly.
func (s *server) stepNew(w http.ResponseWriter, r *http.Request) {
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
	st := &Step{
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
		sl, err := getSlot(s.db, *st.SlotID)
		if err != nil || sl == nil || sl.AssemblyID != *assemblyID {
			http.Error(w, "slot does not belong to this assembly", 400)
			return
		}
	default:
		http.Error(w, "unknown step type", 400)
		return
	}
	if _, err := addStep(s.db, st); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// documentUpload stores an iBOM against a document step, shown embedded in the
// manual. Re-uploading replaces it, so a re-exported board is updated in place.
func (s *server) documentUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseMultipartForm(maxDocumentBytes + (1 << 20)); err != nil {
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
	st, err := getStep(s.db, *stepID)
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
	data, err := io.ReadAll(io.LimitReader(fh, maxDocumentBytes+1))
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if _, err := addDocument(s.db, *stepID, hdr.Filename, data); err != nil {
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
func (s *server) documentByID(w http.ResponseWriter, r *http.Request) {
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
			if err := deleteDocument(s.db, id); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
		}
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	data, mime, err := documentBytes(s.db, id)
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
func (s *server) modelViewerJS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=604800")
	_, _ = w.Write(modelViewerJSBytes)
}

func (s *server) imageByID(w http.ResponseWriter, r *http.Request) {
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
			if err := deleteImage(s.db, id); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
		}
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	data, mime, err := imageBytes(s.db, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(data)
}

// imageUpload stores one or more images against a step.
func (s *server) imageUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseMultipartForm(maxImageBytes + (1 << 20)); err != nil {
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
		data, err := io.ReadAll(io.LimitReader(f, maxImageBytes+1))
		f.Close()
		if err != nil {
			failures = append(failures, fh.Filename+": "+err.Error())
			continue
		}
		mime := fh.Header.Get("Content-Type")
		if mime == "" || !imageMimes[mime] {
			mime = http.DetectContentType(data)
		}
		if _, err := addImage(s.db, *stepID, fh.Filename, mime, data, r.FormValue("caption")); err != nil {
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

// slotsUnder returns every slot reachable from an assembly by walking both
// sub-assembly lines and slot options, deduplicated, in tree order. The build
// view needs the whole reachable set, not just the root's own slots.
func slotsUnder(db *sql.DB, rootID int64) ([]VariantSlot, error) {
	var out []VariantSlot
	seenAsm := map[int64]bool{}
	seenSlot := map[int64]bool{}

	var walk func(id int64, depth int)
	walk = func(id int64, depth int) {
		if depth > 32 || seenAsm[id] {
			return
		}
		seenAsm[id] = true

		own, err := listSlots(db, id)
		if err == nil {
			for _, s := range own {
				if !seenSlot[s.ID] {
					seenSlot[s.ID] = true
					out = append(out, s)
				}
			}
		}
		ls, err := lines(db, id)
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

// sameSnapshot reports whether the live gather matches the frozen one for the
// parts that matter. A build whose assembly has been edited since freezing
// should say so rather than silently showing two different lists.
func sameSnapshot(live, frozen *GatherResult) bool {
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

func idFromPath(path, prefix string) (int64, error) {
	rest := strings.Trim(strings.TrimPrefix(path, prefix), "/")
	if i := strings.Index(rest, "/"); i >= 0 {
		rest = rest[:i]
	}
	return strconv.ParseInt(rest, 10, 64)
}

// idTail parses the trailing integer of a path, tolerating a trailing slash.
func idTail(path string) (int64, error) {
	path = strings.Trim(path, "/")
	i := strings.LastIndex(path, "/")
	return strconv.ParseInt(path[i+1:], 10, 64)
}

func orDefault(s, d string) string {
	if strings.TrimSpace(s) == "" {
		return d
	}
	return s
}

func orFloat(s string, d float64) float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return d
	}
	return f
}

func parseOptFloat(s string) *float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	return &f
}

func parseOptInt(s string) *int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	i, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return nil
	}
	return &i
}

func trimNum(f float64) string {
	if f == float64(int64(f)) {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func strFmt(p *float64) string {
	if p == nil {
		return ""
	}
	return strconv.FormatFloat(*p, 'f', 2, 64)
}

func mulFmt(p *float64, q float64) string {
	if p == nil {
		return ""
	}
	return strconv.FormatFloat(*p*q, 'f', 2, 64)
}

func sanitizeFile(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, s)
}

// wouldCycle reports whether adding child under parent would create a cycle.
// Slot options that point at assemblies count as edges too, so a cycle can
// never be introduced through a variant.
//
// Children are collected and the cursor closed before recursing: holding a
// result set open across a nested query on the same connection fails on some
// drivers, and the failure would look like "no children", silently disabling
// the guard for anything below the first level.
func wouldCycle(db *sql.DB, parent, child int64) error {
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

var partKinds = []string{"fastener", "extrusion", "printed", "sheetmetal", "electronic", "belt", "wire", "switch", "other"}
