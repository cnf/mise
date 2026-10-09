// Command mise serves the BOM & assembly-manual app.
package main

import (
	"flag"
	"log"
	"net/http"

	"example.com/cnf/mise/internal/store"
	"example.com/cnf/mise/internal/web"
)

func main() {
	var (
		addr   = flag.String("addr", ":8080", "listen address")
		dbPath = flag.String("db", "mise.db", "path to sqlite database")
	)
	flag.Parse()

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
