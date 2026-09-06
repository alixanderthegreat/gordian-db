package gordian

import (
	"errors"
	"testing"
)

func openTestGraph(t *testing.T) *Graph {
	t.Helper()
	return NewGraph(openTestStore(t))
}

func TestGraphAddNodeGetNode(t *testing.T) {
	g := openTestGraph(t)

	id, err := g.AddNode("Entity", map[string]any{"name": "alice"})
	if err != nil {
		t.Fatalf("AddNode: %v", err)
	}

	n, ok, err := g.GetNode(id)
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if !ok {
		t.Fatal("GetNode: not found")
	}
	if n.ID != id || n.Label != "Entity" || n.Props["name"] != "alice" {
		t.Fatalf("GetNode = %+v, want id=%d label=Entity name=alice", n, id)
	}
}

func TestGraphGetNodeMissing(t *testing.T) {
	g := openTestGraph(t)
	_, ok, err := g.GetNode(999)
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if ok {
		t.Fatal("expected ok=false for a nonexistent node")
	}
}

func TestGraphAddNodeIDsAreSequential(t *testing.T) {
	g := openTestGraph(t)
	var ids []int64
	for i := 0; i < 3; i++ {
		id, err := g.AddNode("X", nil)
		if err != nil {
			t.Fatalf("AddNode %d: %v", i, err)
		}
		ids = append(ids, id)
	}
	for i := 1; i < len(ids); i++ {
		if ids[i] != ids[i-1]+1 {
			t.Fatalf("node ids not sequential: %v", ids)
		}
	}
}

func TestGraphAddEdgeRequiresRealEndpoints(t *testing.T) {
	g := openTestGraph(t)
	a, err := g.AddNode("A", nil)
	if err != nil {
		t.Fatalf("AddNode: %v", err)
	}

	if err := g.AddEdge(a, 12345, "REL"); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("AddEdge to missing node: got %v, want ErrNodeNotFound", err)
	}
	if err := g.AddEdge(12345, a, "REL"); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("AddEdge from missing node: got %v, want ErrNodeNotFound", err)
	}
}

// TestGraphMentionsShape mirrors simple-bot's real usage shape exactly (journal.go's
// mentionEntities/RelatedFacts, per kata cycle 17's item 0): a Fact node MENTIONS one or more
// Entity nodes, and the traversal must work correctly in both directions - Neighbors from the
// fact's side, InNeighbors from the entity's side, including when multiple facts share the same
// entity (a real, common case RelatedFacts must aggregate correctly).
func TestGraphMentionsShape(t *testing.T) {
	g := openTestGraph(t)

	fact1, err := g.AddNode("Fact", map[string]any{"text": "alice likes go"})
	if err != nil {
		t.Fatalf("AddNode fact1: %v", err)
	}
	fact2, err := g.AddNode("Fact", map[string]any{"text": "alice met bob"})
	if err != nil {
		t.Fatalf("AddNode fact2: %v", err)
	}
	alice, err := g.AddNode("Entity", map[string]any{"name": "alice"})
	if err != nil {
		t.Fatalf("AddNode alice: %v", err)
	}
	bob, err := g.AddNode("Entity", map[string]any{"name": "bob"})
	if err != nil {
		t.Fatalf("AddNode bob: %v", err)
	}

	for _, e := range []struct{ from, to int64 }{
		{fact1, alice},
		{fact2, alice},
		{fact2, bob},
	} {
		if err := g.AddEdge(e.from, e.to, "MENTIONS"); err != nil {
			t.Fatalf("AddEdge %d->%d: %v", e.from, e.to, err)
		}
	}

	// Outgoing: fact2 mentions both alice and bob.
	fact2Mentions, err := g.Neighbors(fact2, "MENTIONS")
	if err != nil {
		t.Fatalf("Neighbors(fact2): %v", err)
	}
	if len(fact2Mentions) != 2 {
		t.Fatalf("Neighbors(fact2, MENTIONS) = %+v, want 2 entities", fact2Mentions)
	}

	// Incoming: alice is mentioned by both fact1 and fact2 - the real RelatedFacts shape
	// (multiple facts sharing one entity) must aggregate both, not just the most recent.
	aliceMentionedBy, err := g.InNeighbors(alice, "MENTIONS")
	if err != nil {
		t.Fatalf("InNeighbors(alice): %v", err)
	}
	if len(aliceMentionedBy) != 2 {
		t.Fatalf("InNeighbors(alice, MENTIONS) = %+v, want 2 facts", aliceMentionedBy)
	}
	gotIDs := map[int64]bool{}
	for _, n := range aliceMentionedBy {
		gotIDs[n.ID] = true
	}
	if !gotIDs[fact1] || !gotIDs[fact2] {
		t.Fatalf("InNeighbors(alice) = %+v, want both fact1=%d and fact2=%d", aliceMentionedBy, fact1, fact2)
	}

	// bob is mentioned only by fact2.
	bobMentionedBy, err := g.InNeighbors(bob, "MENTIONS")
	if err != nil {
		t.Fatalf("InNeighbors(bob): %v", err)
	}
	if len(bobMentionedBy) != 1 || bobMentionedBy[0].ID != fact2 {
		t.Fatalf("InNeighbors(bob, MENTIONS) = %+v, want exactly [fact2]", bobMentionedBy)
	}
}

// TestGraphNeighborsFiltersByLabel proves a differently-labeled edge is correctly excluded, not
// just that matching edges are found - the same "prove exclusion, not just inclusion" discipline
// applied throughout this project's own prefix-scan tests.
func TestGraphNeighborsFiltersByLabel(t *testing.T) {
	g := openTestGraph(t)
	a, err := g.AddNode("A", nil)
	if err != nil {
		t.Fatalf("AddNode a: %v", err)
	}
	b, err := g.AddNode("B", nil)
	if err != nil {
		t.Fatalf("AddNode b: %v", err)
	}
	if err := g.AddEdge(a, b, "LIKES"); err != nil {
		t.Fatalf("AddEdge: %v", err)
	}

	likes, err := g.Neighbors(a, "LIKES")
	if err != nil {
		t.Fatalf("Neighbors LIKES: %v", err)
	}
	if len(likes) != 1 || likes[0].ID != b {
		t.Fatalf("Neighbors(a, LIKES) = %+v, want [b]", likes)
	}

	knows, err := g.Neighbors(a, "KNOWS")
	if err != nil {
		t.Fatalf("Neighbors KNOWS: %v", err)
	}
	if len(knows) != 0 {
		t.Fatalf("Neighbors(a, KNOWS) = %+v, want empty - no such edge exists", knows)
	}
}
