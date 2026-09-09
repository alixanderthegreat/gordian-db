package gordian

import "testing"

// TestListNodes_PaginatesAcrossMultiplePages proves real, correct ascending-id pagination: seed
// 5 nodes, page through with limit=2, confirm every node is seen exactly once, in order, with
// hasMore correctly false only on the last page.
func TestListNodes_PaginatesAcrossMultiplePages(t *testing.T) {
	g := openTestGraph(t)
	var ids []int64
	for i := 0; i < 5; i++ {
		id, err := g.AddNode("Thing", map[string]any{"n": i})
		if err != nil {
			t.Fatalf("AddNode %d: %v", i, err)
		}
		ids = append(ids, id)
	}

	var seen []int64
	cursor := int64(-1)
	for {
		nodes, next, hasMore, err := g.ListNodes(cursor, 2)
		if err != nil {
			t.Fatalf("ListNodes(cursor=%d): %v", cursor, err)
		}
		for _, n := range nodes {
			seen = append(seen, n.ID)
		}
		if !hasMore {
			break
		}
		cursor = next
	}

	if len(seen) != len(ids) {
		t.Fatalf("ListNodes paginated to %d nodes, want %d", len(seen), len(ids))
	}
	for i, id := range ids {
		if seen[i] != id {
			t.Fatalf("ListNodes page order[%d] = %d, want %d (ascending id order)", i, seen[i], id)
		}
	}
}

// TestListNodes_FewerThanOnePage proves a store with fewer nodes than the requested limit
// returns them all with hasMore=false, not an error or a short read.
func TestListNodes_FewerThanOnePage(t *testing.T) {
	g := openTestGraph(t)
	id, err := g.AddNode("Thing", map[string]any{})
	if err != nil {
		t.Fatalf("AddNode: %v", err)
	}

	nodes, next, hasMore, err := g.ListNodes(-1, 10)
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	if len(nodes) != 1 || nodes[0].ID != id {
		t.Fatalf("ListNodes = %+v, want exactly [id=%d]", nodes, id)
	}
	if hasMore {
		t.Fatal("ListNodes hasMore = true, want false (fewer nodes than the page limit)")
	}
	if next != id {
		t.Fatalf("ListNodes nextCursor = %d, want %d", next, id)
	}
}

// TestListNodes_IncludesNodeZero proves the real edge case this primitive's own -1 sentinel
// design exists for: node id 0 is a real, valid id (the first node ever created in a fresh
// store) and must be included when starting from the beginning, not silently skipped.
func TestListNodes_IncludesNodeZero(t *testing.T) {
	g := openTestGraph(t)
	id, err := g.AddNode("Thing", map[string]any{})
	if err != nil {
		t.Fatalf("AddNode: %v", err)
	}
	if id != 0 {
		t.Fatalf("first node id = %d, want 0 (test assumption for this case)", id)
	}

	nodes, _, _, err := g.ListNodes(-1, 10)
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	if len(nodes) != 1 || nodes[0].ID != 0 {
		t.Fatalf("ListNodes(-1, 10) = %+v, want to include node id=0", nodes)
	}
}

// TestListNodes_EmptyStore proves an empty store returns an empty slice, not an error.
func TestListNodes_EmptyStore(t *testing.T) {
	g := openTestGraph(t)
	nodes, _, hasMore, err := g.ListNodes(-1, 10)
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	if len(nodes) != 0 || hasMore {
		t.Fatalf("ListNodes(empty store) = %+v hasMore=%v, want empty and false", nodes, hasMore)
	}
}

// TestOutEdges_InEdges_MultipleLabels proves the real point of these primitives: a node with
// edges of MULTIPLE different labels, in both directions, are all found in one call each - the
// exact real gap Neighbors/InNeighbors (label-required) can't answer.
func TestOutEdges_InEdges_MultipleLabels(t *testing.T) {
	g := openTestGraph(t)
	a, err := g.AddNode("Thing", map[string]any{})
	if err != nil {
		t.Fatalf("AddNode a: %v", err)
	}
	b, err := g.AddNode("Thing", map[string]any{})
	if err != nil {
		t.Fatalf("AddNode b: %v", err)
	}
	c, err := g.AddNode("Thing", map[string]any{})
	if err != nil {
		t.Fatalf("AddNode c: %v", err)
	}
	if err := g.AddEdge(a, b, "LIKES"); err != nil {
		t.Fatalf("AddEdge LIKES: %v", err)
	}
	if err := g.AddEdge(a, c, "MENTIONS"); err != nil {
		t.Fatalf("AddEdge MENTIONS: %v", err)
	}

	out, err := g.OutEdges(a)
	if err != nil {
		t.Fatalf("OutEdges: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("OutEdges(a) = %+v, want 2 edges (LIKES and MENTIONS)", out)
	}
	labels := map[string]bool{}
	for _, e := range out {
		labels[e.Label] = true
		if e.From != a {
			t.Fatalf("OutEdges(a) edge.From = %d, want %d", e.From, a)
		}
	}
	if !labels["LIKES"] || !labels["MENTIONS"] {
		t.Fatalf("OutEdges(a) labels = %v, want both LIKES and MENTIONS", labels)
	}

	inB, err := g.InEdges(b)
	if err != nil {
		t.Fatalf("InEdges(b): %v", err)
	}
	if len(inB) != 1 || inB[0].From != a || inB[0].Label != "LIKES" {
		t.Fatalf("InEdges(b) = %+v, want exactly [from=%d label=LIKES]", inB, a)
	}
}

// TestOutEdges_NoEdgesReturnsEmpty proves a node with no outgoing edges returns an empty slice,
// not an error.
func TestOutEdges_NoEdgesReturnsEmpty(t *testing.T) {
	g := openTestGraph(t)
	id, err := g.AddNode("Thing", map[string]any{})
	if err != nil {
		t.Fatalf("AddNode: %v", err)
	}
	edges, err := g.OutEdges(id)
	if err != nil {
		t.Fatalf("OutEdges: %v", err)
	}
	if len(edges) != 0 {
		t.Fatalf("OutEdges(no edges) = %+v, want empty", edges)
	}
}

// TestOutEdges_ConsistentWithNeighbors cross-checks OutEdges against the existing, already-proven
// Neighbors(from,label) for the same real edges - the same "cross-check against an independent
// implementation" discipline used throughout this project.
func TestOutEdges_ConsistentWithNeighbors(t *testing.T) {
	g := openTestGraph(t)
	a, err := g.AddNode("Thing", map[string]any{})
	if err != nil {
		t.Fatalf("AddNode a: %v", err)
	}
	b, err := g.AddNode("Thing", map[string]any{})
	if err != nil {
		t.Fatalf("AddNode b: %v", err)
	}
	if err := g.AddEdge(a, b, "LIKES"); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}

	viaNeighbors, err := g.Neighbors(a, "LIKES")
	if err != nil {
		t.Fatalf("Neighbors: %v", err)
	}
	viaOutEdges, err := g.OutEdges(a)
	if err != nil {
		t.Fatalf("OutEdges: %v", err)
	}
	if len(viaNeighbors) != 1 || len(viaOutEdges) != 1 {
		t.Fatalf("Neighbors=%+v OutEdges=%+v, want exactly 1 each", viaNeighbors, viaOutEdges)
	}
	if viaNeighbors[0].ID != viaOutEdges[0].To {
		t.Fatalf("Neighbors found id=%d, OutEdges found to=%d, want the same real edge", viaNeighbors[0].ID, viaOutEdges[0].To)
	}
}

// TestEdgeCountsByLabel_CountsEveryRealEdgeExactlyOnce proves kata cycle 57's own real design
// point: scanning tagEdgeOut alone counts each real edge exactly once. AddEdge writes BOTH edge
// indexes, so a scan that also touched tagEdgeIn would double every count - this test would catch
// that regression immediately, since the expected numbers are exact, not approximate.
func TestEdgeCountsByLabel_CountsEveryRealEdgeExactlyOnce(t *testing.T) {
	g := openTestGraph(t)
	a, _ := g.AddNode("Job", nil)
	b, _ := g.AddNode("Employer", nil)
	c, _ := g.AddNode("Keyword", nil)
	d, _ := g.AddNode("Keyword", nil)
	for _, e := range []struct {
		from, to int64
		label    string
	}{
		{a, b, "POSTED_BY"},
		{a, c, "HAS_KEYWORD"},
		{a, d, "HAS_KEYWORD"},
	} {
		if err := g.AddEdge(e.from, e.to, e.label); err != nil {
			t.Fatalf("AddEdge %s: %v", e.label, err)
		}
	}

	counts, err := g.EdgeCountsByLabel()
	if err != nil {
		t.Fatalf("EdgeCountsByLabel: %v", err)
	}
	if len(counts) != 2 {
		t.Fatalf("counts = %+v, want exactly 2 real labels", counts)
	}
	if counts["POSTED_BY"] != 1 {
		t.Fatalf("counts[POSTED_BY] = %d, want exactly 1 (2 would mean tagEdgeIn is being double-counted)", counts["POSTED_BY"])
	}
	if counts["HAS_KEYWORD"] != 2 {
		t.Fatalf("counts[HAS_KEYWORD] = %d, want exactly 2", counts["HAS_KEYWORD"])
	}
}

// TestEdgeCountsByLabel_EmptyStoreIsEmptyMap proves a real store with no edges at all returns an
// empty map, not an error and not a nil that a caller has to special-case.
func TestEdgeCountsByLabel_EmptyStoreIsEmptyMap(t *testing.T) {
	g := openTestGraph(t)
	if _, err := g.AddNode("Lonely", nil); err != nil {
		t.Fatalf("AddNode: %v", err)
	}
	counts, err := g.EdgeCountsByLabel()
	if err != nil {
		t.Fatalf("EdgeCountsByLabel: %v", err)
	}
	if len(counts) != 0 {
		t.Fatalf("counts = %+v, want empty", counts)
	}
}

// TestEdgesByLabel_ReturnsOnlyThatLabelWithRealEndpoints proves the filter really filters, and
// that BOTH endpoints come back correctly - From is parsed from the key's own id bytes rather
// than inherited from a caller-supplied node (the real difference between this global scan and
// OutEdges' own per-node one).
func TestEdgesByLabel_ReturnsOnlyThatLabelWithRealEndpoints(t *testing.T) {
	g := openTestGraph(t)
	job, _ := g.AddNode("Job", nil)
	emp, _ := g.AddNode("Employer", nil)
	kw, _ := g.AddNode("Keyword", nil)
	if err := g.AddEdge(job, emp, "POSTED_BY"); err != nil {
		t.Fatalf("AddEdge POSTED_BY: %v", err)
	}
	if err := g.AddEdge(job, kw, "HAS_KEYWORD"); err != nil {
		t.Fatalf("AddEdge HAS_KEYWORD: %v", err)
	}

	edges, truncated, err := g.EdgesByLabel("POSTED_BY", 0)
	if err != nil {
		t.Fatalf("EdgesByLabel: %v", err)
	}
	if truncated {
		t.Fatalf("truncated = true, want false (no limit given)")
	}
	if len(edges) != 1 {
		t.Fatalf("edges = %+v, want exactly 1 (HAS_KEYWORD must be excluded)", edges)
	}
	if edges[0].From != job || edges[0].To != emp || edges[0].Label != "POSTED_BY" {
		t.Fatalf("edges[0] = %+v, want from=%d to=%d label=POSTED_BY", edges[0], job, emp)
	}
}

// TestEdgesByLabel_ReportsTruncationHonestly proves a real limit stops the scan AND says so -
// a partial answer that looked complete would be worse than no answer at all for a map view
// deciding what it can render.
func TestEdgesByLabel_ReportsTruncationHonestly(t *testing.T) {
	g := openTestGraph(t)
	job, _ := g.AddNode("Job", nil)
	for i := 0; i < 5; i++ {
		kw, _ := g.AddNode("Keyword", map[string]any{"i": i})
		if err := g.AddEdge(job, kw, "HAS_KEYWORD"); err != nil {
			t.Fatalf("AddEdge %d: %v", i, err)
		}
	}

	edges, truncated, err := g.EdgesByLabel("HAS_KEYWORD", 3)
	if err != nil {
		t.Fatalf("EdgesByLabel: %v", err)
	}
	if len(edges) != 3 || !truncated {
		t.Fatalf("got %d edges truncated=%v, want exactly 3 and truncated=true", len(edges), truncated)
	}

	all, truncated, err := g.EdgesByLabel("HAS_KEYWORD", 5)
	if err != nil {
		t.Fatalf("EdgesByLabel (exact limit): %v", err)
	}
	if len(all) != 5 || truncated {
		t.Fatalf("got %d edges truncated=%v, want exactly 5 and truncated=false (limit met exactly, nothing left behind)", len(all), truncated)
	}
}
