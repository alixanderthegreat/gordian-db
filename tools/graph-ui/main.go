// Command graph-ui is kata cycle 36's own real graph explorer for gordian-db - browse nodes,
// inspect a node's full neighborhood (every edge, every label, both directions), create an edge,
// delete a node. See graphui/server.go's own package doc for why this is a small, purpose-built
// tool, not a port of goraphdb's own graphdb-ui.
//
// A thin CLI wrapper as of kata cycle 52 - the real server/API/UI-serving logic lives in the
// importable graphui package now, so a caller that already has a *gordian.Graph open (simple-bot's
// own live process, most concretely) can serve the exact same thing directly, as a goroutine in
// its own process, with no second OS process or gordian.Open lock contention. This binary remains
// for the standalone case: inspecting a store while nothing else has it open.
//
//	go run ./tools/graph-ui -db ./mydata.db
//	go run ./tools/graph-ui -db ./mydata.db -addr :7474 -ui ./tools/graph-ui/ui/dist
package main

import (
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"

	gordian "github.com/alixanderthegreat/gordian-db"
	"github.com/alixanderthegreat/gordian-db/tools/graph-ui/graphui"
)

func main() {
	dbPath := flag.String("db", "", "path to a gordian-db store directory (required)")
	addr := flag.String("addr", ":7474", "HTTP listen address")
	uiDir := flag.String("ui", "", "override the built-in embedded UI with a real directory (e.g. for frontend dev against fresh, unbuilt files) - omit to serve the embedded build")
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

	var uiFS fs.FS = graphui.EmbeddedUI()
	if *uiDir != "" {
		uiFS = os.DirFS(*uiDir)
	}

	mux := graphui.NewMux(g, uiFS)

	fmt.Printf("graph-ui: db=%s api=http://localhost%s/api/\n", *dbPath, *addr)
	fmt.Printf("graph-ui: ui=http://localhost%s/\n", *addr)

	log.Fatal(http.ListenAndServe(*addr, mux))
}
