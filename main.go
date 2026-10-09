package main

import (
	"database/sql"
	"embed"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"
)

//go:embed templates/*.html
var templateFiles embed.FS

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
	mux.HandleFunc("/builds", s.builds)
	mux.HandleFunc("/builds/", s.buildByID)
	mux.HandleFunc("/gather/", s.gatherPage)
	mux.HandleFunc("/export/gather/", s.exportCSV)
	mux.HandleFunc("/partdb/sync", s.partdbSync)
	mux.HandleFunc("/api/partdb/search", s.partdbSearchAPI)
	mux.HandleFunc("/ibom/import", s.ibomImport)

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
	if r.Method == http.MethodPost {
		if r.FormValue("_action") == "delete" {
			if err := deletePart(s.db, id); err != nil {
				http.Error(w, err.Error(), 409)
				return
			}
			http.Redirect(w, r, "/parts", http.StatusSeeOther)
			return
		}
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
	// count direct lines for display
	type row struct {
		Assembly
		LineCount int
	}
	rows := make([]row, 0, len(asms))
	for _, a := range asms {
		var n int
		_ = s.db.QueryRow(`SELECT COUNT(*) FROM assembly_line WHERE assembly_id=?`, a.ID).Scan(&n)
		rows = append(rows, row{a, n})
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
		var err error
		switch r.FormValue("_action") {
		case "add_part":
			pid := parseOptInt(r.FormValue("part_id"))
			if pid == nil {
				http.Error(w, "part required", 400)
				return
			}
			_, err = addLine(s.db, &Line{AssemblyID: id, ItemType: "part", PartID: pid, Qty: orFloat(r.FormValue("qty"), 1)})
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
			if err = wouldCycle(s.db, id, *cid); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			_, err = addLine(s.db, &Line{AssemblyID: id, ItemType: "assembly", ChildAssemblyID: cid, Qty: orFloat(r.FormValue("qty"), 1)})
		case "delete":
			if err = deleteAssembly(s.db, id); err != nil {
				http.Error(w, err.Error(), 409)
				return
			}
			http.Redirect(w, r, "/assemblies", http.StatusSeeOther)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		http.Redirect(w, r, "/assemblies/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
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
	})
}

func (s *server) lineByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	var id int64
	fmt.Sscanf(strings.Trim(strings.TrimPrefix(r.URL.Path, "/lines/"), "/"), "%d", &id)
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
		if r.FormValue("_action") == "freeze" {
			if err := freezeBuild(s.db, id); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
		}
		http.Redirect(w, r, "/builds/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
		return
	}
	g, err := gather(s.db, b.AssemblyID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	a, _ := getAssembly(s.db, b.AssemblyID)
	var snap *GatherResult
	if b.Snapshot != nil {
		var sr GatherResult
		if json.Unmarshal([]byte(*b.Snapshot), &sr) == nil {
			snap = &sr
		}
	}
	s.render(w, "build_view.html", map[string]any{
		"Build": b, "Gather": g, "Assembly": a, "Snapshot": snap,
		"Frozen": snap,
	})
}

func (s *server) gatherPage(w http.ResponseWriter, r *http.Request) {
	id, err := idFromPath(r.URL.Path, "/gather/")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	g, err := gather(s.db, id)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.render(w, "gather.html", map[string]any{"Gather": g})
}

func (s *server) exportCSV(w http.ResponseWriter, r *http.Request) {
	id, err := idFromPath(r.URL.Path, "/export/gather/")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	g, err := gather(s.db, id)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", "bom-"+sanitizeFile(g.RootName)+".csv"))
	_, _ = w.Write([]byte{0xEF, 0xBB, 0xBF}) // BOM so Excel opens it cleanly

	// A part can be declared in several sub-assemblies; list them all so the
	// Customer Reference column tells the builder which bag/tray it belongs to.
	bags := map[int64][]string{}
	for _, grp := range g.Groups {
		for _, l := range grp.Lines {
			bags[l.PartID] = append(bags[l.PartID], grp.AssemblyName)
		}
	}

	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"Manufacturer Part Number", "Manufacturer", "Description", "Quantity", "Customer Reference", "Unit Price", "Currency", "Vendor URL"})
	for _, p := range g.Total {
		price := ""
		if p.UnitPrice != nil {
			price = strconv.FormatFloat(*p.UnitPrice, 'f', -1, 64)
		}
		_ = cw.Write([]string{"", "", p.Name, trimNum(p.Qty), strings.Join(bags[p.PartID], ", "), price, p.Currency, p.VendorURL})
	}
	cw.Flush()
}

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
		name := orDefault(r.FormValue("assembly_name"), "PCB")
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

// ---------- helpers ----------

func idFromPath(path, prefix string) (int64, error) {
	rest := strings.Trim(strings.TrimPrefix(path, prefix), "/")
	if i := strings.Index(rest, "/"); i >= 0 {
		rest = rest[:i]
	}
	return strconv.ParseInt(rest, 10, 64)
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

func wouldCycle(db *sql.DB, parent, child int64) error {
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
		rows, err := db.Query(`SELECT child_assembly_id FROM assembly_line WHERE assembly_id=? AND item_type='assembly' AND child_assembly_id IS NOT NULL`, id)
		if err != nil {
			return false
		}
		defer rows.Close()
		for rows.Next() {
			var c int64
			if rows.Scan(&c) == nil && walk(c, depth+1) {
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
