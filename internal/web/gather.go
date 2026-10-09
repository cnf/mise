package web

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"example.com/cnf/mise/internal/bom"
	"example.com/cnf/mise/internal/store"
)

func (s *Server) gatherPage(w http.ResponseWriter, r *http.Request) {
	id, err := idFromPath(r.URL.Path, "/gather/")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	g, err := bom.Gather(s.db, id, nil)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.render(w, "gather.html", map[string]any{"Gather": g})
}

// ---------- export ----------

// csvRows is the shared shape of the distributor CSV: one row per part with the
// sub-assemblies it belongs to in the Customer Reference column.
func csvRows(g *bom.GatherResult) [][]string {
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
		rows = append(rows, []string{"", "", p.Name, TrimNum(p.Qty), strings.Join(bags[p.PartID], ", "), price, p.Currency, p.VendorURL})
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

func (s *Server) exportCSV(w http.ResponseWriter, r *http.Request) {
	id, err := idFromPath(r.URL.Path, "/export/gather/")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	g, err := bom.Gather(s.db, id, nil)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	writeCSV(w, "bom-"+sanitizeFile(g.RootName)+".csv", csvRows(g))
}

// exportSnapshotCSV exports exactly what the build was frozen with, which is
// the point of freezing: a built unit can be reproduced from this file even
// after the assemblies have moved on.
func (s *Server) exportSnapshotCSV(w http.ResponseWriter, r *http.Request) {
	id, err := idFromPath(r.URL.Path, "/export/snapshot/")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	b, err := store.GetBuild(s.db, id)
	if err != nil || b == nil {
		http.NotFound(w, r)
		return
	}
	if b.Snapshot == nil {
		http.Error(w, "build has not been frozen yet", 409)
		return
	}
	var g bom.GatherResult
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
