// Command graph-ui is kata cycle 36's own real graph explorer for gordian-db - browse nodes,
// inspect a node's full neighborhood (every edge, every label, both directions), create an edge,
// delete a node. See server.go's own package doc for why this is a small, purpose-built tool, not
// a port of goraphdb's own graphdb-ui.
//
//	go run ./tools/graph-ui -db ./mydata.db
//	go run ./tools/graph-ui -db ./mydata.db -addr :7474 -ui ./tools/graph-ui/ui/dist
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"

	gordian "github.com/alixanderthegreat/gordian-db"
)

func main() {
	dbPath := flag.String("db", "", "path to a gordian-db store directory (required)")
	addr := flag.String("addr", ":7474", "HTTP listen address")
	uiDir := flag.String("ui", "", "path to built UI static files (ui/dist) - omit to serve API only")
	flag.Parse()

	if *dbPath == "" {
		log.Fatal("-db is required")
	}

	store, err := gordian.Open(*dbPath)
	if err != nil {
		log.Fatalf("open store %s: %v", *dbPath, err)
	}
	defer store.Close()
	g := gordian.NewGraph(store)

	mux := newMux(&apiServer{g: g}, *uiDir)

	fmt.Printf("graph-ui: db=%s api=http://localhost%s/api/\n", *dbPath, *addr)
	if *uiDir != "" {
		fmt.Printf("graph-ui: ui=http://localhost%s/\n", *addr)
	} else {
		fmt.Println("graph-ui: no -ui given, serving API only")
	}

	log.Fatal(http.ListenAndServe(*addr, mux))
}
