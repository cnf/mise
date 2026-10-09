// Command mise serves the BOM & assembly-manual app.
package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"example.com/cnf/mise/internal/store"
	"example.com/cnf/mise/internal/web"
	"example.com/cnf/mise/internal/xdg"
)

func main() {
	var (
		addr   = flag.String("addr", ":8080", "listen address")
		dbPath = flag.String("db", xdg.DBPath(), "path to sqlite database")
	)
	flag.Parse()

	// The default lives under $XDG_DATA_HOME; make sure its directory exists.
	if err := os.MkdirAll(filepath.Dir(*dbPath), 0o700); err != nil {
		log.Fatalf("data dir: %v", err)
	}
	db, err := store.OpenDB(*dbPath)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer db.Close()

	srv, err := web.New(db)
	if err != nil {
		log.Fatalf("templates: %v", err)
	}

	log.Printf("mise listening on %s (db=%s)", *addr, *dbPath)
	log.Fatal(http.ListenAndServe(*addr, srv.Handler()))
}
