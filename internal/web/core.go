package web

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"example.com/cnf/mise/internal/store"
)

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	parts, _ := store.ListParts(s.db, "")
	asms, _ := store.ListAssemblies(s.db)
	builds, _ := store.ListBuilds(s.db)
	s.render(w, "home.html", map[string]any{
		"Parts": parts, "Assemblies": asms, "Builds": builds,
		"PartCount": len(parts), "AsmCount": len(asms), "BuildCount": len(builds),
	})
}

// ---------- parts ----------

func (s *Server) parts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	parts, err := store.ListParts(s.db, q)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.render(w, "parts.html", map[string]any{"Parts": parts, "Q": q, "Kinds": partKinds})
}

func (s *Server) partNew(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.render(w, "part_edit.html", map[string]any{"Part": &store.Part{Currency: "EUR", Kind: "other", Attrs: "{}"}, "Kinds": partKinds})
		return
	}
	s.savePart(w, r, &store.Part{})
}

func (s *Server) savePart(w http.ResponseWriter, r *http.Request, p *store.Part) {
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
	id, err := store.UpsertPart(s.db, p)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	http.Redirect(w, r, "/parts/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func (s *Server) partByID(w http.ResponseWriter, r *http.Request) {
	pth := strings.TrimPrefix(r.URL.Path, "/parts/")
	idStr := strings.TrimSuffix(strings.Trim(pth, "/"), "/edit")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	p, err := store.GetPart(s.db, id)
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
		if err := store.DeletePart(s.db, id); err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		http.Redirect(w, r, "/parts", http.StatusSeeOther)
		return
	}
	s.render(w, "part_view.html", map[string]any{"Part": p, "Kinds": partKinds})
}
