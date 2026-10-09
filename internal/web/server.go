package web

import (
	"database/sql"
	"embed"
	"html/template"
	"log"
	"net/http"
)

//go:embed templates/*.html
var templateFiles embed.FS

//go:embed assets/model-viewer.min.js
var modelViewerJSBytes []byte

// Server holds the shared dependencies every page handler needs: the database
// and the parsed template set.
type Server struct {
	db   *sql.DB
	tmpl *template.Template
}

// New parses the embedded templates and returns a Server over db. Every
// template helper is registered here and nowhere else, so the tests that parse
// the same files exercise the same FuncMap the running server does.
func New(db *sql.DB) (*Server, error) {
	tmpl, err := template.New("").Funcs(template.FuncMap{
		"trimNum": TrimNum,
		"price":   func(p *float64) string { return StrFmt(p) },
		"mul":     func(p *float64, q float64) string { return MulFmt(p, q) },
		"inc":     func(i int) int { return i + 1 },
	}).ParseFS(templateFiles, "templates/*.html")
	if err != nil {
		return nil, err
	}
	return &Server{db: db, tmpl: tmpl}, nil
}

// Handler returns the router with every route wired to its method.
func (s *Server) Handler() http.Handler {
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
	return mux
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("template %s: %v", name, err)
	}
}
