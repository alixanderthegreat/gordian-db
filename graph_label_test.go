package gordian

import (
	"encoding/json"
	"testing"
)

// TestAllNodes_LabelIndex_IncludesMatchingLabel proves the real point of kata cycle 32's rewrite:
// AllNodes still finds every node of the requested label, now via the label index instead of a
// full tagNode scan.
func TestAllNodes_LabelIndex_IncludesMatchingLabel(t *testing.T) {
	g := openTestGraph(t)
	id1, err := g.AddNode("Book", map[string]any{"title": "one"})
	if err != nil {
		t.Fatalf("AddNode: %v", err)
	}
	id2, err := g.AddNode("Book", map[string]any{"title": "two"})
	if err != nil {
		t.Fatalf("AddNode: %v", err)
	}

	got, err := g.AllNodes("Book")
	if err != nil {
		t.Fatalf("AllNodes: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("AllNodes(Book) = %+v, want exactly 2 nodes", got)
	}
	seen := map[int64]bool{}
	for _, n := range got {
		seen[n.ID] = true
	}
	if !seen[id1] || !seen[id2] {
		t.Fatalf("AllNodes(Book) = %+v, want ids %d and %d", got, id1, id2)
	}
}

// TestAllNodes_LabelIndex_ExcludesOtherLabels proves the label index is genuinely scoped - a
// node of a DIFFERENT label must never appear, the same "prove exclusion, not just inclusion"
// discipline used throughout this project. This is the exact real risk this cycle exists to fix:
// a store containing MANY BookChunk nodes must not slow down or pollute AllNodes("Book").
func TestAllNodes_LabelIndex_ExcludesOtherLabels(t *testing.T) {
	g := openTestGraph(t)
	bookID, err := g.AddNode("Book", map[string]any{"title": "the one real book"})
	if err != nil {
		t.Fatalf("AddNode(Book): %v", err)
	}
	for i := 0; i < 50; i++ {
		if _, err := g.AddNode("BookChunk", map[string]any{"chunk_index": i}); err != nil {
			t.Fatalf("AddNode(BookChunk) %d: %v", i, err)
		}
	}

	got, err := g.AllNodes("Book")
	if err != nil {
		t.Fatalf("AllNodes: %v", err)
	}
	if len(got) != 1 || got[0].ID != bookID {
		t.Fatalf("AllNodes(Book) = %+v, want exactly [id=%d], not polluted by BookChunk nodes", got, bookID)
	}
}

// TestAllNodes_LabelIndex_DeleteNodeLeavesNoDanglingEntry proves DeleteNode's own new cleanup:
// after deleting a node, AllNodes must not return a dangling id (which would make resolveNodes
// hit ErrNodeNotFound) and a sibling node of the same label must survive untouched.
func TestAllNodes_LabelIndex_DeleteNodeLeavesNoDanglingEntry(t *testing.T) {
	g := openTestGraph(t)
	deleted, err := g.AddNode("Book", map[string]any{"title": "deleted"})
	if err != nil {
		t.Fatalf("AddNode: %v", err)
	}
	survivor, err := g.AddNode("Book", map[string]any{"title": "survivor"})
	if err != nil {
		t.Fatalf("AddNode: %v", err)
	}

	if err := g.DeleteNode(deleted); err != nil {
		t.Fatalf("DeleteNode: %v", err)
	}

	got, err := g.AllNodes("Book")
	if err != nil {
		t.Fatalf("AllNodes: %v (a dangling label-index entry would error here via resolveNodes)", err)
	}
	if len(got) != 1 || got[0].ID != survivor {
		t.Fatalf("AllNodes(Book) after delete = %+v, want exactly [survivor=%d]", got, survivor)
	}
}

// TestBackfillLabelIndex_IndexesPreExistingNodes proves the real safety net this cycle requires:
// a node written directly at the Store layer (simulating a node that predates the label index -
// the exact real shape of any node already in a production store before this change ships) is
// invisible to AllNodes until BackfillLabelIndex runs, then found correctly afterward.
func TestBackfillLabelIndex_IndexesPreExistingNodes(t *testing.T) {
	g := openTestGraph(t)

	// Simulate a pre-label-index node: write directly via Store, bypassing AddNode's own
	// automatic label-index write entirely.
	id, err := g.AddNode("Book", map[string]any{"title": "normal"})
	if err != nil {
		t.Fatalf("AddNode: %v", err)
	}
	preExisting := id + 1000 // a node id guaranteed not to collide with the real counter
	n := Node{ID: preExisting, Label: "Book", Props: map[string]any{"title": "pre-existing"}}
	data, err := json.Marshal(n)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := g.store.Put(nodeKey(preExisting), data); err != nil {
		t.Fatalf("store.Put node directly: %v", err)
	}

	// Before backfill: the pre-existing node has no label-index entry, so AllNodes must not find it.
	before, err := g.AllNodes("Book")
	if err != nil {
		t.Fatalf("AllNodes before backfill: %v", err)
	}
	for _, got := range before {
		if got.ID == preExisting {
			t.Fatalf("AllNodes found the pre-existing node BEFORE backfill - test setup is wrong")
		}
	}

	n2, err := g.BackfillLabelIndex()
	if err != nil {
		t.Fatalf("BackfillLabelIndex: %v", err)
	}
	if n2 != 2 {
		t.Fatalf("BackfillLabelIndex indexed %d nodes, want 2 (the normal one + the pre-existing one)", n2)
	}

	after, err := g.AllNodes("Book")
	if err != nil {
		t.Fatalf("AllNodes after backfill: %v", err)
	}
	found := false
	for _, got := range after {
		if got.ID == preExisting {
			found = true
		}
	}
	if !found {
		t.Fatalf("AllNodes after backfill = %+v, want to find the pre-existing node id=%d", after, preExisting)
	}
}

// TestBackfillLabelIndex_IsIdempotent proves running the backfill twice is safe - a real
// requirement given this is meant to be run against a real production store, where re-running it
// accidentally (or deliberately, out of caution) must not corrupt or duplicate anything.
func TestBackfillLabelIndex_IsIdempotent(t *testing.T) {
	g := openTestGraph(t)
	if _, err := g.AddNode("Book", map[string]any{"title": "one"}); err != nil {
		t.Fatalf("AddNode: %v", err)
	}

	if _, err := g.BackfillLabelIndex(); err != nil {
		t.Fatalf("first BackfillLabelIndex: %v", err)
	}
	if _, err := g.BackfillLabelIndex(); err != nil {
		t.Fatalf("second BackfillLabelIndex: %v", err)
	}

	got, err := g.AllNodes("Book")
	if err != nil {
		t.Fatalf("AllNodes: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("AllNodes(Book) after double backfill = %+v, want exactly 1 (no duplication)", got)
	}
}
