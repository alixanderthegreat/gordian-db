package graphui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	gordian "github.com/alixanderthegreat/gordian-db"
)

func newTestServer(t *testing.T) *apiServer {
	t.Helper()
	store, err := gordian.Open(filepath.Join(t.TempDir(), "graph-ui-test.db"))
	if err != nil {
		t.Fatalf("gordian.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return &apiServer{g: gordian.NewGraph(store)}
}

func doJSON(t *testing.T, mux *http.ServeMux, method, path string, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	var decoded map[string]any
	if w.Body.Len() > 0 {
		if err := json.Unmarshal(w.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("decode response body %q: %v", w.Body.String(), err)
		}
	}
	return w, decoded
}

// TestListNodesCursor_ExactShape proves GET /api/nodes/cursor returns real data in the exact JSON
// shape the real frontend's NodeCursorPage type expects.
func TestListNodesCursor_ExactShape(t *testing.T) {
	s := newTestServer(t)
	for i := 0; i < 3; i++ {
		if _, err := s.g.AddNode("Thing", map[string]any{"name": "n"}); err != nil {
			t.Fatalf("AddNode: %v", err)
		}
	}
	mux := NewMux(s.g, nil)

	w, body := doJSON(t, mux, "GET", "/api/nodes/cursor?limit=10", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	nodes, ok := body["nodes"].([]any)
	if !ok || len(nodes) != 3 {
		t.Fatalf("nodes = %+v, want exactly 3", body["nodes"])
	}
	first := nodes[0].(map[string]any)
	if first["id"] == nil || first["props"] == nil {
		t.Fatalf("first node = %+v, want id and props fields", first)
	}
	labels, ok := first["labels"].([]any)
	if !ok || len(labels) != 1 || labels[0] != "Thing" {
		t.Fatalf("first node labels = %+v, want [Thing]", first["labels"])
	}
	if body["has_more"] != false {
		t.Fatalf("has_more = %v, want false (only 3 nodes, limit 10)", body["has_more"])
	}
}

// TestNeighborhood_MultipleLabelsBothDirections proves GET /api/nodes/{id}/neighborhood is the
// real point of this tool: edges of different labels, in both directions, all show up in one
// call - the exact real gap OutEdges/InEdges (kata cycle 36) were built to fill.
func TestNeighborhood_MultipleLabelsBothDirections(t *testing.T) {
	s := newTestServer(t)
	a, _ := s.g.AddNode("Thing", map[string]any{"name": "a"})
	b, _ := s.g.AddNode("Thing", map[string]any{"name": "b"})
	c, _ := s.g.AddNode("Thing", map[string]any{"name": "c"})
	if err := s.g.AddEdge(a, b, "LIKES"); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if err := s.g.AddEdge(c, a, "MENTIONS"); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	mux := NewMux(s.g, nil)

	w, body := doJSON(t, mux, "GET", "/api/nodes/"+strconv.FormatInt(a, 10)+"/neighborhood", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	center := body["center"].(map[string]any)
	if int64(center["id"].(float64)) != a {
		t.Fatalf("center.id = %v, want %d", center["id"], a)
	}
	neighbors := body["neighbors"].([]any)
	if len(neighbors) != 2 {
		t.Fatalf("neighbors = %+v, want exactly 2 (b via LIKES, c via MENTIONS)", neighbors)
	}
	edges := body["edges"].([]any)
	if len(edges) != 2 {
		t.Fatalf("edges = %+v, want exactly 2", edges)
	}
	labels := map[string]bool{}
	for _, e := range edges {
		labels[e.(map[string]any)["label"].(string)] = true
	}
	if !labels["LIKES"] || !labels["MENTIONS"] {
		t.Fatalf("edge labels = %v, want both LIKES and MENTIONS", labels)
	}
}

// TestGetNode_NotFound proves a missing node returns a real 404, not a 200 with empty data.
func TestGetNode_NotFound(t *testing.T) {
	s := newTestServer(t)
	mux := NewMux(s.g, nil)
	w, _ := doJSON(t, mux, "GET", "/api/nodes/999999", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

// TestCreateEdge_RealWrite proves POST /api/edges actually writes a real edge, verified via a
// direct Graph read-back, not just a 200 response.
func TestCreateEdge_RealWrite(t *testing.T) {
	s := newTestServer(t)
	a, _ := s.g.AddNode("Thing", map[string]any{})
	b, _ := s.g.AddNode("Thing", map[string]any{})
	mux := NewMux(s.g, nil)

	w, _ := doJSON(t, mux, "POST", "/api/edges", `{"from":`+strconv.FormatInt(a, 10)+`,"to":`+strconv.FormatInt(b, 10)+`,"label":"LIKES"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}

	out, err := s.g.OutEdges(a)
	if err != nil {
		t.Fatalf("OutEdges: %v", err)
	}
	if len(out) != 1 || out[0].To != b || out[0].Label != "LIKES" {
		t.Fatalf("OutEdges after POST /api/edges = %+v, want exactly [to=%d label=LIKES]", out, b)
	}
}

// TestDeleteNode_RealDelete proves DELETE /api/nodes/{id} actually removes the node, verified
// via a direct GetNode read-back.
func TestDeleteNode_RealDelete(t *testing.T) {
	s := newTestServer(t)
	id, _ := s.g.AddNode("Thing", map[string]any{})
	mux := NewMux(s.g, nil)

	w, _ := doJSON(t, mux, "DELETE", "/api/nodes/"+strconv.FormatInt(id, 10), "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}

	_, ok, err := s.g.GetNode(id)
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if ok {
		t.Fatal("node still exists after DELETE /api/nodes/{id}")
	}
}

// TestStats_RealNodeCount proves GET /api/stats reports the real, correct node count.
func TestStats_RealNodeCount(t *testing.T) {
	s := newTestServer(t)
	for i := 0; i < 7; i++ {
		if _, err := s.g.AddNode("Thing", map[string]any{}); err != nil {
			t.Fatalf("AddNode: %v", err)
		}
	}
	mux := NewMux(s.g, nil)

	w, body := doJSON(t, mux, "GET", "/api/stats", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if int64(body["node_count"].(float64)) != 7 {
		t.Fatalf("node_count = %v, want 7", body["node_count"])
	}
}

// TestSearch_ByExactID proves GET /api/nodes/search?q=<id> does a real, exact GetNode lookup when
// the query parses as an integer.
func TestSearch_ByExactID(t *testing.T) {
	s := newTestServer(t)
	id, _ := s.g.AddNode("Entity", map[string]any{"name": "work"})
	mux := NewMux(s.g, nil)

	w, body := doJSON(t, mux, "GET", "/api/nodes/search?q="+strconv.FormatInt(id, 10), "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	nodes := body["nodes"].([]any)
	if len(nodes) != 1 {
		t.Fatalf("search by id = %+v, want exactly 1 result", nodes)
	}
	if int64(nodes[0].(map[string]any)["id"].(float64)) != id {
		t.Fatalf("search by id returned wrong node: %+v", nodes[0])
	}
}

// TestSearch_BySubstring proves a real, case-insensitive substring search across name/title/text
// finds the right real nodes and excludes non-matching ones.
func TestSearch_BySubstring(t *testing.T) {
	s := newTestServer(t)
	match, _ := s.g.AddNode("Fact", map[string]any{"text": "The speaker offloads 90% of their WORK onto AI."})
	s.g.AddNode("Fact", map[string]any{"text": "an unrelated sentence about cooking"})
	mux := NewMux(s.g, nil)

	w, body := doJSON(t, mux, "GET", "/api/nodes/search?q=work", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	nodes := body["nodes"].([]any)
	if len(nodes) != 1 {
		t.Fatalf("search by substring = %+v, want exactly 1 match", nodes)
	}
	if int64(nodes[0].(map[string]any)["id"].(float64)) != match {
		t.Fatalf("search by substring returned wrong node: %+v", nodes[0])
	}
}

// TestSearch_NoMatchReturnsEmpty proves a query matching nothing is a real empty result, not an
// error.
func TestSearch_NoMatchReturnsEmpty(t *testing.T) {
	s := newTestServer(t)
	s.g.AddNode("Entity", map[string]any{"name": "something"})
	mux := NewMux(s.g, nil)

	w, body := doJSON(t, mux, "GET", "/api/nodes/search?q=nonexistent-term-xyz", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	nodes := body["nodes"].([]any)
	if len(nodes) != 0 {
		t.Fatalf("search with no matches = %+v, want empty", nodes)
	}
}

// TestSearch_CapsAtLimit proves more real matches than the requested limit are correctly capped,
// not silently returned in full.
func TestSearch_CapsAtLimit(t *testing.T) {
	s := newTestServer(t)
	for i := 0; i < 10; i++ {
		if _, err := s.g.AddNode("Entity", map[string]any{"name": "shared-term"}); err != nil {
			t.Fatalf("AddNode: %v", err)
		}
	}
	mux := NewMux(s.g, nil)

	w, body := doJSON(t, mux, "GET", "/api/nodes/search?q=shared-term&limit=3", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	nodes := body["nodes"].([]any)
	if len(nodes) != 3 {
		t.Fatalf("search capped at limit=3 = %d results, want exactly 3", len(nodes))
	}
}

// TestSearch_PaginatesAcrossMultiplePages proves kata cycle 42's own real fix: search results
// beyond `limit` are reachable via the returned next_cursor, not silently unreachable - the exact
// real bug found live (486 real "wife" matches, only 30 reachable, the one node actually being
// searched for buried past the cap).
func TestSearch_PaginatesAcrossMultiplePages(t *testing.T) {
	s := newTestServer(t)
	var ids []int64
	for i := 0; i < 7; i++ {
		id, err := s.g.AddNode("Entity", map[string]any{"name": "shared-term"})
		if err != nil {
			t.Fatalf("AddNode %d: %v", i, err)
		}
		ids = append(ids, id)
	}
	mux := NewMux(s.g, nil)

	var seen []int64
	cursor := int64(-1)
	for {
		w, body := doJSON(t, mux, "GET", fmt.Sprintf("/api/nodes/search?q=shared-term&limit=3&cursor=%d", cursor), "")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
		}
		nodes := body["nodes"].([]any)
		for _, n := range nodes {
			seen = append(seen, int64(n.(map[string]any)["id"].(float64)))
		}
		if body["has_more"] != true {
			break
		}
		cursor = int64(body["next_cursor"].(float64))
	}

	if len(seen) != len(ids) {
		t.Fatalf("paginated search found %d nodes, want %d", len(seen), len(ids))
	}
	for i, id := range ids {
		if seen[i] != id {
			t.Fatalf("paginated search order[%d] = %d, want %d", i, seen[i], id)
		}
	}
}

// TestSearch_IDLookupSecondPageIsEmpty proves an exact id match (a real, single result) doesn't
// pretend to have a second page - paginating a single exact result makes no sense.
func TestSearch_IDLookupSecondPageIsEmpty(t *testing.T) {
	s := newTestServer(t)
	id, _ := s.g.AddNode("Entity", map[string]any{"name": "solo"})
	mux := NewMux(s.g, nil)

	w, body := doJSON(t, mux, "GET", "/api/nodes/search?q="+strconv.FormatInt(id, 10)+"&cursor="+strconv.FormatInt(id, 10), "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	nodes := body["nodes"].([]any)
	if len(nodes) != 0 {
		t.Fatalf("id search second page = %+v, want empty", nodes)
	}
}

// rawEdgeOutKey mirrors gordian's own private edgeOutKey encoding exactly (tagEdgeOut=0x02 +
// 8-byte big-endian from + 2-byte big-endian label length + label + 8-byte big-endian to) - used
// ONLY to simulate a pre-existing dangling edge for TestNeighborhood_SkipsDanglingEdge below.
// With kata cycle 43's own DeleteNode fix, a dangling edge can no longer be created through the
// real public API at all (which is the whole point of that fix) - this raw construction is the
// only way left to reproduce "a store with a dangling edge from before that fix shipped."
func rawEdgeOutKey(from int64, label string, to int64) []byte {
	key := make([]byte, 0, 1+8+2+len(label)+8)
	key = append(key, 0x02)
	fromBuf := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		fromBuf[i] = byte(from)
		from >>= 8
	}
	key = append(key, fromBuf...)
	labelLen := make([]byte, 2)
	labelLen[0] = byte(len(label) >> 8)
	labelLen[1] = byte(len(label))
	key = append(key, labelLen...)
	key = append(key, label...)
	toBuf := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		toBuf[i] = byte(to)
		to >>= 8
	}
	key = append(key, toBuf...)
	return key
}

// TestNeighborhood_SkipsDanglingEdge proves kata cycle 43's own defensive fix: a real dangling
// edge (its other endpoint doesn't resolve) is never included in the response - the exact real
// bug found live, where a stale edge crashed the frontend trying to build a cytoscape edge
// referencing a node that was never in its own node set.
func TestNeighborhood_SkipsDanglingEdge(t *testing.T) {
	// Built inline (not via newTestServer) so this test can keep its own reference to the real
	// Store - needed to write a raw, simulated dangling edge key directly, something apiServer
	// itself has no reason to ever expose (it only ever touches the store through Graph).
	store, err := gordian.Open(filepath.Join(t.TempDir(), "graph-ui-dangling-test.db"))
	if err != nil {
		t.Fatalf("gordian.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	g := gordian.NewGraph(store)
	s := &apiServer{g: g}

	a, err := g.AddNode("Entity", map[string]any{"name": "a"})
	if err != nil {
		t.Fatalf("AddNode a: %v", err)
	}
	real, err := g.AddNode("Fact", map[string]any{"text": "a real, surviving neighbor"})
	if err != nil {
		t.Fatalf("AddNode real: %v", err)
	}
	if err := g.AddEdge(a, real, "MENTIONS"); err != nil {
		t.Fatalf("AddEdge a->real: %v", err)
	}

	// Simulate a pre-existing dangling edge: write the raw OUT key directly, pointing at an id
	// that was never created - store-level, bypassing AddEdge's own existence check entirely
	// (which is the only way this could happen for real, from before kata cycle 43's fix).
	if err := store.Put(rawEdgeOutKey(a, "MENTIONS", 999999), []byte{}); err != nil {
		t.Fatalf("Put raw dangling edge: %v", err)
	}

	mux := NewMux(s.g, nil)
	w, body := doJSON(t, mux, "GET", "/api/nodes/"+strconv.FormatInt(a, 10)+"/neighborhood", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	edges := body["edges"].([]any)
	if len(edges) != 1 {
		t.Fatalf("edges = %+v, want exactly 1 (the real edge only, dangling one excluded)", edges)
	}
	if int64(edges[0].(map[string]any)["to"].(float64)) != real {
		t.Fatalf("edges[0] = %+v, want the real surviving edge to %d", edges[0], real)
	}
	neighbors := body["neighbors"].([]any)
	if len(neighbors) != 1 {
		t.Fatalf("neighbors = %+v, want exactly 1 (the dangling target must not appear)", neighbors)
	}
}

// TestNodeDegrees_RealCombinedDegree proves GET /api/nodes/degrees reports the real, combined
// OutEdges+InEdges count for each requested id.
func TestNodeDegrees_RealCombinedDegree(t *testing.T) {
	s := newTestServer(t)
	a, _ := s.g.AddNode("Entity", map[string]any{"name": "a"})
	b, _ := s.g.AddNode("Fact", map[string]any{"text": "b"})
	c, _ := s.g.AddNode("Fact", map[string]any{"text": "c"})
	if err := s.g.AddEdge(b, a, "MENTIONS"); err != nil {
		t.Fatalf("AddEdge b->a: %v", err)
	}
	if err := s.g.AddEdge(c, a, "MENTIONS"); err != nil {
		t.Fatalf("AddEdge c->a: %v", err)
	}
	mux := NewMux(s.g, nil)

	w, body := doJSON(t, mux, "GET", fmt.Sprintf("/api/nodes/degrees?ids=%d,%d", a, b), "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	degrees := body["degrees"].(map[string]any)
	if int(degrees[strconv.FormatInt(a, 10)].(float64)) != 2 {
		t.Fatalf("degree(a) = %v, want 2 (two real MENTIONS in-edges)", degrees[strconv.FormatInt(a, 10)])
	}
	if int(degrees[strconv.FormatInt(b, 10)].(float64)) != 1 {
		t.Fatalf("degree(b) = %v, want 1 (one real MENTIONS out-edge)", degrees[strconv.FormatInt(b, 10)])
	}
}

// TestNodeDegrees_NoEdgesIsZeroNotError proves a real node with no edges reports degree 0, not an
// error.
func TestNodeDegrees_NoEdgesIsZeroNotError(t *testing.T) {
	s := newTestServer(t)
	id, _ := s.g.AddNode("Entity", map[string]any{"name": "lonely"})
	mux := NewMux(s.g, nil)

	w, body := doJSON(t, mux, "GET", "/api/nodes/degrees?ids="+strconv.FormatInt(id, 10), "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	degrees := body["degrees"].(map[string]any)
	if int(degrees[strconv.FormatInt(id, 10)].(float64)) != 0 {
		t.Fatalf("degree(lonely) = %v, want 0", degrees[strconv.FormatInt(id, 10)])
	}
}

// TestNodeDegrees_EmptyParamReturnsEmptyMap proves a missing/empty ids param is a real empty
// result, not a 400 - matching the existing endpoints' own permissive-on-empty convention.
func TestNodeDegrees_EmptyParamReturnsEmptyMap(t *testing.T) {
	s := newTestServer(t)
	mux := NewMux(s.g, nil)

	w, body := doJSON(t, mux, "GET", "/api/nodes/degrees", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	degrees := body["degrees"].(map[string]any)
	if len(degrees) != 0 {
		t.Fatalf("degrees = %+v, want empty", degrees)
	}
}

// TestNodesByLabel_ReturnsOnlyThatLabel proves the real, generic point (kata cycle 54): only
// nodes of the requested label come back, real nodes of a DIFFERENT label are excluded, using the
// same AllNodes(label) real primitive the rest of gordian-db already relies on.
func TestNodesByLabel_ReturnsOnlyThatLabel(t *testing.T) {
	s := newTestServer(t)
	bookA, _ := s.g.AddNode("Book", map[string]any{"title": "Book A"})
	bookB, _ := s.g.AddNode("Book", map[string]any{"title": "Book B"})
	s.g.AddNode("Entity", map[string]any{"name": "not a book"})
	mux := NewMux(s.g, nil)

	w, body := doJSON(t, mux, "GET", "/api/nodes/by-label?label=Book", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	nodes := body["nodes"].([]any)
	if len(nodes) != 2 {
		t.Fatalf("nodes = %+v, want exactly 2 real Book nodes", nodes)
	}
	got := map[int64]bool{}
	for _, n := range nodes {
		m := n.(map[string]any)
		got[int64(m["id"].(float64))] = true
	}
	if !got[bookA] || !got[bookB] {
		t.Fatalf("nodes ids = %v, want exactly [%d,%d]", got, bookA, bookB)
	}
}

// TestNodesByLabel_MissingLabelIsBadRequest proves a real, required param - an empty/missing
// label is a genuine 400, not a silently-empty result that could be mistaken for "no real nodes
// of that label."
func TestNodesByLabel_MissingLabelIsBadRequest(t *testing.T) {
	s := newTestServer(t)
	mux := NewMux(s.g, nil)

	w, _ := doJSON(t, mux, "GET", "/api/nodes/by-label", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

// TestNodeEdgesByLabel_FiltersToRequestedLabelOnly proves the real point (kata cycle 54): a node
// with edges in MULTIPLE real labels only gets back the ones matching the requested label, in
// EITHER direction - the exact real need a Book node's own thousands of PART_OF-linked chunks
// motivated (never resolving/serializing them just to find its far sparser RELATED_TO edges).
func TestNodeEdgesByLabel_FiltersToRequestedLabelOnly(t *testing.T) {
	s := newTestServer(t)
	bookA, _ := s.g.AddNode("Book", map[string]any{"title": "A"})
	bookB, _ := s.g.AddNode("Book", map[string]any{"title": "B"})
	chunk, _ := s.g.AddNode("BookChunk", map[string]any{"text": "a chunk"})
	if err := s.g.AddEdge(bookA, bookB, "RELATED_TO"); err != nil {
		t.Fatalf("AddEdge RELATED_TO: %v", err)
	}
	if err := s.g.AddEdge(chunk, bookA, "PART_OF"); err != nil {
		t.Fatalf("AddEdge PART_OF: %v", err)
	}
	mux := NewMux(s.g, nil)

	w, body := doJSON(t, mux, "GET", "/api/nodes/"+strconv.FormatInt(bookA, 10)+"/edges?label=RELATED_TO", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	edges := body["edges"].([]any)
	if len(edges) != 1 {
		t.Fatalf("edges = %+v, want exactly 1 real RELATED_TO edge (PART_OF must be excluded)", edges)
	}
	e := edges[0].(map[string]any)
	if e["label"] != "RELATED_TO" {
		t.Fatalf("edges[0].label = %v, want RELATED_TO", e["label"])
	}
}

// TestNodeEdgesByLabel_IncludesBothDirections proves an edge is found whether bookA is the real
// "from" or the real "to" side - a caller asking "what is bookA related to" shouldn't have to know
// or care which direction AddEdge originally happened to be called in.
func TestNodeEdgesByLabel_IncludesBothDirections(t *testing.T) {
	s := newTestServer(t)
	bookA, _ := s.g.AddNode("Book", map[string]any{"title": "A"})
	bookB, _ := s.g.AddNode("Book", map[string]any{"title": "B"})
	// Real edge created FROM bookB TO bookA - bookA is the "in" side.
	if err := s.g.AddEdge(bookB, bookA, "RELATED_TO"); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	mux := NewMux(s.g, nil)

	w, body := doJSON(t, mux, "GET", "/api/nodes/"+strconv.FormatInt(bookA, 10)+"/edges?label=RELATED_TO", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	edges := body["edges"].([]any)
	if len(edges) != 1 {
		t.Fatalf("edges = %+v, want exactly 1 (found via the IN direction)", edges)
	}
}

// TestNodeEdgeCounts_GroupsByLabelWithoutResolvingNeighbors proves kata cycle 55's own real
// point: a node's own real edges are grouped by label into real counts, both directions, without
// needing to resolve a single neighbor - the exact real primitive a high-degree node (a real book
// with 1000+ real chunks) needs before a caller decides whether fetching anything more is safe.
func TestNodeEdgeCounts_GroupsByLabelWithoutResolvingNeighbors(t *testing.T) {
	s := newTestServer(t)
	book, _ := s.g.AddNode("Book", map[string]any{"title": "A"})
	otherBook, _ := s.g.AddNode("Book", map[string]any{"title": "B"})
	chunk1, _ := s.g.AddNode("BookChunk", map[string]any{})
	chunk2, _ := s.g.AddNode("BookChunk", map[string]any{})
	if err := s.g.AddEdge(chunk1, book, "PART_OF"); err != nil {
		t.Fatalf("AddEdge chunk1: %v", err)
	}
	if err := s.g.AddEdge(chunk2, book, "PART_OF"); err != nil {
		t.Fatalf("AddEdge chunk2: %v", err)
	}
	if err := s.g.AddEdge(book, otherBook, "RELATED_TO"); err != nil {
		t.Fatalf("AddEdge RELATED_TO: %v", err)
	}
	mux := NewMux(s.g, nil)

	w, body := doJSON(t, mux, "GET", "/api/nodes/"+strconv.FormatInt(book, 10)+"/edge-counts", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	counts := body["counts"].(map[string]any)
	if int(counts["PART_OF"].(float64)) != 2 {
		t.Fatalf("counts[PART_OF] = %v, want 2", counts["PART_OF"])
	}
	if int(counts["RELATED_TO"].(float64)) != 1 {
		t.Fatalf("counts[RELATED_TO] = %v, want 1", counts["RELATED_TO"])
	}
}

// TestNodeEdgeCounts_NoEdgesIsEmptyMap proves a real node with no edges at all gets a real,
// empty counts map, not an error.
func TestNodeEdgeCounts_NoEdgesIsEmptyMap(t *testing.T) {
	s := newTestServer(t)
	id, _ := s.g.AddNode("Entity", map[string]any{"name": "lonely"})
	mux := NewMux(s.g, nil)

	w, body := doJSON(t, mux, "GET", "/api/nodes/"+strconv.FormatInt(id, 10)+"/edge-counts", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	counts := body["counts"].(map[string]any)
	if len(counts) != 0 {
		t.Fatalf("counts = %+v, want empty", counts)
	}
}

// TestNodesBatch_ResolvesExactlyTheRequestedRealIds proves the real batch resolution: every real
// requested id comes back, with its own real label and props - the exact shape GraphViewer needs
// to render a filtered, bounded set of neighbors without an N+1 per-neighbor fetch.
func TestNodesBatch_ResolvesExactlyTheRequestedRealIds(t *testing.T) {
	s := newTestServer(t)
	a, _ := s.g.AddNode("Book", map[string]any{"title": "A"})
	b, _ := s.g.AddNode("Entity", map[string]any{"name": "B"})
	mux := NewMux(s.g, nil)

	w, body := doJSON(t, mux, "GET", "/api/nodes/batch?ids="+strconv.FormatInt(a, 10)+","+strconv.FormatInt(b, 10), "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	nodes := body["nodes"].([]any)
	if len(nodes) != 2 {
		t.Fatalf("nodes = %+v, want exactly 2", nodes)
	}
	got := map[int64]string{}
	for _, n := range nodes {
		m := n.(map[string]any)
		got[int64(m["id"].(float64))] = m["label"].(string)
	}
	if got[a] != "Book" || got[b] != "Entity" {
		t.Fatalf("got = %v, want id=%d label=Book and id=%d label=Entity", got, a, b)
	}
}

// TestNodesBatch_SkipsNonexistentIdsWithoutFailing proves a real, made-up id among otherwise-real
// ones doesn't fail the whole batch - the same permissive-on-bad-input convention every other
// endpoint in this file already uses.
func TestNodesBatch_SkipsNonexistentIdsWithoutFailing(t *testing.T) {
	s := newTestServer(t)
	real, _ := s.g.AddNode("Entity", map[string]any{"name": "real"})
	mux := NewMux(s.g, nil)

	w, body := doJSON(t, mux, "GET", "/api/nodes/batch?ids="+strconv.FormatInt(real, 10)+",999999", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	nodes := body["nodes"].([]any)
	if len(nodes) != 1 {
		t.Fatalf("nodes = %+v, want exactly 1 (the nonexistent id skipped, not errored)", nodes)
	}
}

// TestNodesBatch_MaxPropCapsLongValuesKeepsKeys proves kata cycle 58's own real payload fix: a
// long string prop is capped, a short one is untouched, and EVERY key survives - the last part
// matters most, since the frontend's own display-label priority (name -> title -> label -> text)
// walks those keys and would silently break if the cap dropped any.
func TestNodesBatch_MaxPropCapsLongValuesKeepsKeys(t *testing.T) {
	s := newTestServer(t)
	long := strings.Repeat("x", 5000)
	id, _ := s.g.AddNode("JobListing", map[string]any{
		"title":       "Short Title",
		"description": long,
		"directApply": true,
	})
	mux := NewMux(s.g, nil)

	w, body := doJSON(t, mux, "GET", "/api/nodes/batch?ids="+strconv.FormatInt(id, 10)+"&maxprop=120", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	props := body["nodes"].([]any)[0].(map[string]any)["props"].(map[string]any)

	if len(props) != 3 {
		t.Fatalf("props = %+v, want all 3 real keys preserved", props)
	}
	desc := props["description"].(string)
	if len([]rune(desc)) != 121 { // 120 capped runes + the ellipsis marker
		t.Fatalf("description len = %d runes, want 120 capped + 1 ellipsis", len([]rune(desc)))
	}
	if props["title"].(string) != "Short Title" {
		t.Fatalf("title = %q, want the short value untouched", props["title"])
	}
	if props["directApply"] != true {
		t.Fatalf("directApply = %v, want the non-string value passed through untouched", props["directApply"])
	}
}

// TestNodesBatch_NoMaxPropReturnsFullProps proves the cap is strictly opt-in - the Explorer's own
// detail view genuinely needs full text, so omitting maxprop must behave exactly as before.
func TestNodesBatch_NoMaxPropReturnsFullProps(t *testing.T) {
	s := newTestServer(t)
	long := strings.Repeat("y", 5000)
	id, _ := s.g.AddNode("JobListing", map[string]any{"description": long})
	mux := NewMux(s.g, nil)

	w, body := doJSON(t, mux, "GET", "/api/nodes/batch?ids="+strconv.FormatInt(id, 10), "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	props := body["nodes"].([]any)[0].(map[string]any)["props"].(map[string]any)
	if props["description"].(string) != long {
		t.Fatalf("description was altered without maxprop - got %d chars, want the full %d", len(props["description"].(string)), len(long))
	}
}

// TestTruncateProps_DoesNotSplitMultibyteRunes proves the cap is rune-based, not byte-based - this
// corpus contains real non-ASCII text, and a byte-based cut would emit invalid UTF-8.
func TestTruncateProps_DoesNotSplitMultibyteRunes(t *testing.T) {
	in := map[string]any{"text": strings.Repeat("é", 50)}
	out := truncateProps(in, 10)
	got := out["text"].(string)
	if !utf8.ValidString(got) {
		t.Fatalf("truncated value is not valid UTF-8: %q", got)
	}
	if len([]rune(got)) != 11 {
		t.Fatalf("got %d runes, want 10 + ellipsis", len([]rune(got)))
	}
}

// TestEdgeCounts_GlobalPerLabel proves kata cycle 57's own global counts endpoint - the "price
// the work first" call a whole-graph map makes before fetching any payload. Counts are exact, so
// a regression that double-counted (by also scanning tagEdgeIn) would fail here loudly.
func TestEdgeCounts_GlobalPerLabel(t *testing.T) {
	s := newTestServer(t)
	job, _ := s.g.AddNode("JobListing", nil)
	emp, _ := s.g.AddNode("Employer", nil)
	kw1, _ := s.g.AddNode("Keyword", nil)
	kw2, _ := s.g.AddNode("Keyword", nil)
	for _, e := range []struct {
		f, t2 int64
		l     string
	}{{job, emp, "POSTED_BY"}, {job, kw1, "HAS_KEYWORD"}, {job, kw2, "HAS_KEYWORD"}} {
		if err := s.g.AddEdge(e.f, e.t2, e.l); err != nil {
			t.Fatalf("AddEdge %s: %v", e.l, err)
		}
	}
	mux := NewMux(s.g, nil)

	w, body := doJSON(t, mux, "GET", "/api/edges/counts", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	counts := body["counts"].(map[string]any)
	if int(counts["POSTED_BY"].(float64)) != 1 || int(counts["HAS_KEYWORD"].(float64)) != 2 {
		t.Fatalf("counts = %+v, want POSTED_BY=1 HAS_KEYWORD=2", counts)
	}
}

// TestEdgesByLabel_GlobalReturnsOnlyThatLabel proves the global edge query filters to one real
// label and carries both real endpoints - the whole basis for an edge-label-driven map view.
func TestEdgesByLabel_GlobalReturnsOnlyThatLabel(t *testing.T) {
	s := newTestServer(t)
	job, _ := s.g.AddNode("JobListing", nil)
	emp, _ := s.g.AddNode("Employer", nil)
	kw, _ := s.g.AddNode("Keyword", nil)
	if err := s.g.AddEdge(job, emp, "POSTED_BY"); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if err := s.g.AddEdge(job, kw, "HAS_KEYWORD"); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	mux := NewMux(s.g, nil)

	w, body := doJSON(t, mux, "GET", "/api/edges?label=POSTED_BY", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	edges := body["edges"].([]any)
	if len(edges) != 1 {
		t.Fatalf("edges = %+v, want exactly 1 real POSTED_BY edge", edges)
	}
	e := edges[0].(map[string]any)
	if int64(e["from"].(float64)) != job || int64(e["to"].(float64)) != emp {
		t.Fatalf("edges[0] = %+v, want from=%d to=%d", e, job, emp)
	}
	if body["truncated"].(bool) {
		t.Fatalf("truncated = true, want false")
	}
}

// TestEdgesByLabel_GlobalReportsTruncation proves a real limit is honestly reported - a map view
// must be able to tell "this is everything" from "this is the first N of more".
func TestEdgesByLabel_GlobalReportsTruncation(t *testing.T) {
	s := newTestServer(t)
	job, _ := s.g.AddNode("JobListing", nil)
	for i := 0; i < 4; i++ {
		kw, _ := s.g.AddNode("Keyword", map[string]any{"i": i})
		if err := s.g.AddEdge(job, kw, "HAS_KEYWORD"); err != nil {
			t.Fatalf("AddEdge %d: %v", i, err)
		}
	}
	mux := NewMux(s.g, nil)

	w, body := doJSON(t, mux, "GET", "/api/edges?label=HAS_KEYWORD&limit=2", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if len(body["edges"].([]any)) != 2 || !body["truncated"].(bool) {
		t.Fatalf("edges=%v truncated=%v, want 2 edges and truncated=true", len(body["edges"].([]any)), body["truncated"])
	}
}

// TestEdgesByLabel_GlobalMissingLabelIsBadRequest proves the label really is required - an empty
// result could otherwise be misread as "no real edges of that label exist".
func TestEdgesByLabel_GlobalMissingLabelIsBadRequest(t *testing.T) {
	s := newTestServer(t)
	mux := NewMux(s.g, nil)

	w, _ := doJSON(t, mux, "GET", "/api/edges", "")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}
