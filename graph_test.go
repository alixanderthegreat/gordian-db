package gordian

import (
	"errors"
	"sync"
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

// TestGraphUpdateNode proves the real contract UpdateNode needs to satisfy: props change in
// place, id and label do not - grounded in kata cycle 21's item 0 (a plain overwrite, mirroring
// AddNode's own encode-and-Put, just against an existing key).
func TestGraphUpdateNode(t *testing.T) {
	g := openTestGraph(t)

	id, err := g.AddNode("Note", map[string]any{"status": "open", "label": "buy milk"})
	if err != nil {
		t.Fatalf("AddNode: %v", err)
	}

	if err := g.UpdateNode(id, map[string]any{"status": "done", "label": "buy milk"}); err != nil {
		t.Fatalf("UpdateNode: %v", err)
	}

	n, ok, err := g.GetNode(id)
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if !ok {
		t.Fatal("GetNode: not found after UpdateNode")
	}
	if n.ID != id {
		t.Fatalf("GetNode.ID = %d, want %d (UpdateNode must not change id)", n.ID, id)
	}
	if n.Label != "Note" {
		t.Fatalf("GetNode.Label = %q, want %q (UpdateNode must not change label)", n.Label, "Note")
	}
	if n.Props["status"] != "done" {
		t.Fatalf("GetNode.Props[status] = %v, want %q", n.Props["status"], "done")
	}
}

// TestGraphUpdateNodeMissing proves UpdateNode reports ErrNodeNotFound rather than silently
// creating a node at an id that was never allocated by AddNode/AddIndexedNode.
func TestGraphUpdateNodeMissing(t *testing.T) {
	g := openTestGraph(t)
	if err := g.UpdateNode(999, map[string]any{"x": 1}); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("UpdateNode(999, ...): got %v, want ErrNodeNotFound", err)
	}
}

// TestGraphUpdateNodeIf_AppliesWhenPredicateTrue and its sibling below prove the exact real shape
// DeleteNote needs (kata cycle 21 item 1): applied only when the predicate matches the node's
// CURRENT state, honest applied=false (no error) otherwise - not a blind overwrite.
func TestGraphUpdateNodeIf_AppliesWhenPredicateTrue(t *testing.T) {
	g := openTestGraph(t)
	id, err := g.AddNode("Note", map[string]any{"status": "open"})
	if err != nil {
		t.Fatalf("AddNode: %v", err)
	}

	isOpen := func(props map[string]any) bool { return props["status"] == "open" }
	applied, err := g.UpdateNodeIf(id, isOpen, func(map[string]any) map[string]any {
		return map[string]any{"status": "done"}
	})
	if err != nil {
		t.Fatalf("UpdateNodeIf: %v", err)
	}
	if !applied {
		t.Fatal("UpdateNodeIf: applied = false, want true (status was open)")
	}

	n, _, err := g.GetNode(id)
	if err != nil {
		t.Fatalf("GetNode: %v", err)
	}
	if n.Props["status"] != "done" {
		t.Fatalf("GetNode.Props[status] = %v, want %q", n.Props["status"], "done")
	}
}

// TestGraphUpdateNodeIf_SkipsWhenPredicateFalse mirrors DeleteNote called a second time on an
// already-resolved note: it must NOT re-apply, and must report applied=false honestly rather than
// pretending to have done something.
func TestGraphUpdateNodeIf_SkipsWhenPredicateFalse(t *testing.T) {
	g := openTestGraph(t)
	id, err := g.AddNode("Note", map[string]any{"status": "done"})
	if err != nil {
		t.Fatalf("AddNode: %v", err)
	}

	isOpen := func(props map[string]any) bool { return props["status"] == "open" }
	applied, err := g.UpdateNodeIf(id, isOpen, func(map[string]any) map[string]any {
		return map[string]any{"status": "done"}
	})
	if err != nil {
		t.Fatalf("UpdateNodeIf: %v", err)
	}
	if applied {
		t.Fatal("UpdateNodeIf: applied = true, want false (status was already done, not open)")
	}
}

// TestGraphUpdateNodeIf_MissingNode proves a nonexistent id is applied=false with a nil error,
// not ErrNodeNotFound - matching DeleteNote's own real "ok=false" contract for a made-up id
// (see UpdateNodeIf's own doc comment).
func TestGraphUpdateNodeIf_MissingNode(t *testing.T) {
	g := openTestGraph(t)
	applied, err := g.UpdateNodeIf(999, func(map[string]any) bool { return true }, func(map[string]any) map[string]any { return map[string]any{} })
	if err != nil {
		t.Fatalf("UpdateNodeIf: got error %v, want nil", err)
	}
	if applied {
		t.Fatal("UpdateNodeIf: applied = true for a nonexistent id, want false")
	}
}

// TestGraphUpdateNodeIf_ConcurrentOnlyOneApplies is the real point of UpdateNodeIf over a
// caller-composed GetNode+UpdateNode: many goroutines racing the exact same check-then-flip
// against the same node must yield EXACTLY one success, proven under -race, not just asserted by
// inspecting the locking code.
func TestGraphUpdateNodeIf_ConcurrentOnlyOneApplies(t *testing.T) {
	g := openTestGraph(t)
	id, err := g.AddNode("Note", map[string]any{"status": "open"})
	if err != nil {
		t.Fatalf("AddNode: %v", err)
	}

	isOpen := func(props map[string]any) bool { return props["status"] == "open" }

	const n = 50
	results := make([]bool, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			applied, err := g.UpdateNodeIf(id, isOpen, func(map[string]any) map[string]any {
				return map[string]any{"status": "done"}
			})
			if err != nil {
				t.Errorf("UpdateNodeIf goroutine %d: %v", i, err)
				return
			}
			results[i] = applied
		}(i)
	}
	wg.Wait()

	successes := 0
	for _, applied := range results {
		if applied {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("got %d successful UpdateNodeIf calls racing the same predicate, want exactly 1", successes)
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
