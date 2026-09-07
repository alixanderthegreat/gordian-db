package gordian

import "testing"

func TestFindByPropertyIndex(t *testing.T) {
	g := openTestGraph(t)
	alice, err := g.AddIndexedNode("Entity", map[string]any{"name": "alice"}, "name")
	if err != nil {
		t.Fatalf("AddIndexedNode alice: %v", err)
	}
	if _, err := g.AddIndexedNode("Entity", map[string]any{"name": "bob"}, "name"); err != nil {
		t.Fatalf("AddIndexedNode bob: %v", err)
	}

	got, err := g.FindByPropertyIndex("Entity", "name", "alice")
	if err != nil {
		t.Fatalf("FindByPropertyIndex: %v", err)
	}
	if len(got) != 1 || got[0].ID != alice {
		t.Fatalf("FindByPropertyIndex(Entity,name,alice) = %+v, want exactly [alice]", got)
	}

	none, err := g.FindByPropertyIndex("Entity", "name", "carol")
	if err != nil {
		t.Fatalf("FindByPropertyIndex: %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("FindByPropertyIndex(Entity,name,carol) = %+v, want empty", none)
	}
}

// TestFindByPropertyIndex_Int64 proves int64-valued properties are indexed and looked up
// correctly - the real requirement kata cycle 18 found in simple-bot's own Fact.fact_id (an
// int64), not just string properties like Entity.name.
func TestFindByPropertyIndex_Int64(t *testing.T) {
	g := openTestGraph(t)
	var factID int64 = 733
	id, err := g.AddIndexedNode("Fact", map[string]any{"fact_id": factID, "text": "hi"}, "fact_id")
	if err != nil {
		t.Fatalf("AddIndexedNode: %v", err)
	}

	got, err := g.FindByPropertyIndex("Fact", "fact_id", factID)
	if err != nil {
		t.Fatalf("FindByPropertyIndex: %v", err)
	}
	if len(got) != 1 || got[0].ID != id {
		t.Fatalf("FindByPropertyIndex(Fact,fact_id,733) = %+v, want exactly [id=%d]", got, id)
	}

	none, err := g.FindByPropertyIndex("Fact", "fact_id", int64(999))
	if err != nil {
		t.Fatalf("FindByPropertyIndex: %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("FindByPropertyIndex(Fact,fact_id,999) = %+v, want empty", none)
	}
}

// TestFindByPropertyIndex_IntKindsInterchangeable proves int, int32, and int64 canonicalize to
// the same index entry - a caller shouldn't have to track which concrete Go integer type was
// used to add a node versus look it up.
func TestFindByPropertyIndex_IntKindsInterchangeable(t *testing.T) {
	g := openTestGraph(t)
	id, err := g.AddIndexedNode("Fact", map[string]any{"fact_id": int(42)}, "fact_id")
	if err != nil {
		t.Fatalf("AddIndexedNode: %v", err)
	}
	got, err := g.FindByPropertyIndex("Fact", "fact_id", int64(42))
	if err != nil {
		t.Fatalf("FindByPropertyIndex: %v", err)
	}
	if len(got) != 1 || got[0].ID != id {
		t.Fatalf("FindByPropertyIndex(fact_id, int64(42)) = %+v, want exactly [id=%d] (added as int(42))", got, id)
	}
}

// TestUpdateAllIndexed_ClearNotesShape mirrors simple-bot's real ClearNotes exactly (kata cycle
// 21's item 2): every OPEN note in one room flips to done in a single call, an already-done note
// in that same room is untouched (predicate false -> not counted as changed), and an open note in
// a DIFFERENT room is untouched too - proving both inclusion and exclusion, not just that matching
// rows change.
func TestUpdateAllIndexed_ClearNotesShape(t *testing.T) {
	g := openTestGraph(t)

	openA, err := g.AddIndexedNode("Note", map[string]any{"room": "room1", "status": "open", "label": "a"}, "room")
	if err != nil {
		t.Fatalf("AddIndexedNode openA: %v", err)
	}
	openB, err := g.AddIndexedNode("Note", map[string]any{"room": "room1", "status": "open", "label": "b"}, "room")
	if err != nil {
		t.Fatalf("AddIndexedNode openB: %v", err)
	}
	alreadyDone, err := g.AddIndexedNode("Note", map[string]any{"room": "room1", "status": "done", "label": "c"}, "room")
	if err != nil {
		t.Fatalf("AddIndexedNode alreadyDone: %v", err)
	}
	otherRoomOpen, err := g.AddIndexedNode("Note", map[string]any{"room": "room2", "status": "open", "label": "d"}, "room")
	if err != nil {
		t.Fatalf("AddIndexedNode otherRoomOpen: %v", err)
	}

	isOpen := func(props map[string]any) bool { return props["status"] == "open" }
	setDone := func(current map[string]any) map[string]any {
		current["status"] = "done"
		return current
	}

	changed, err := g.UpdateAllIndexed("Note", "room", "room1", isOpen, setDone)
	if err != nil {
		t.Fatalf("UpdateAllIndexed: %v", err)
	}
	if changed != 2 {
		t.Fatalf("UpdateAllIndexed changed = %d, want 2 (openA and openB only)", changed)
	}

	for _, id := range []int64{openA, openB} {
		n, _, err := g.GetNode(id)
		if err != nil {
			t.Fatalf("GetNode(%d): %v", id, err)
		}
		if n.Props["status"] != "done" {
			t.Fatalf("GetNode(%d).Props[status] = %v, want done", id, n.Props["status"])
		}
	}

	// alreadyDone was never open, so it must not be counted as changed and must keep its own
	// label - proving setDone's transform never even ran against it.
	n, _, err := g.GetNode(alreadyDone)
	if err != nil {
		t.Fatalf("GetNode(alreadyDone): %v", err)
	}
	if n.Props["label"] != "c" {
		t.Fatalf("GetNode(alreadyDone).Props[label] = %v, want unchanged %q", n.Props["label"], "c")
	}

	// otherRoomOpen is open but in room2, not room1 - must be excluded entirely by the index
	// scope, not just skipped by the predicate.
	n, _, err = g.GetNode(otherRoomOpen)
	if err != nil {
		t.Fatalf("GetNode(otherRoomOpen): %v", err)
	}
	if n.Props["status"] != "open" {
		t.Fatalf("GetNode(otherRoomOpen).Props[status] = %v, want still open (different room)", n.Props["status"])
	}
}

// TestFindByPropertyIndexFiltered_OpenNotesShape mirrors simple-bot's real OpenNotes exactly
// (kata cycle 21's item 3): room AND status='open', ORDER BY created_at ASC. Notes are inserted
// in a deliberately NON-sorted created_at order, so a correct result proves real sorting
// happened, not an accidental match with insertion order.
func TestFindByPropertyIndexFiltered_OpenNotesShape(t *testing.T) {
	g := openTestGraph(t)

	type seed struct {
		room, status string
		createdAt    int64
	}
	seeds := []seed{
		{"room1", "open", 300}, // inserted first, but should sort LAST among room1/open
		{"room1", "done", 100}, // wrong status - must be excluded
		{"room1", "open", 100}, // should sort FIRST
		{"room2", "open", 50},  // wrong room - must be excluded
		{"room1", "open", 200}, // should sort SECOND
	}
	ids := make([]int64, len(seeds))
	for i, s := range seeds {
		id, err := g.AddIndexedNode("Note", map[string]any{
			"room": s.room, "status": s.status, "created_at": s.createdAt,
		}, "room")
		if err != nil {
			t.Fatalf("AddIndexedNode %d: %v", i, err)
		}
		ids[i] = id
	}

	isOpen := func(props map[string]any) bool { return props["status"] == "open" }
	byCreatedAtAsc := func(a, b Node) bool {
		return a.Props["created_at"].(float64) < b.Props["created_at"].(float64)
	}

	got, err := g.FindByPropertyIndexFiltered("Note", "room", "room1", isOpen, byCreatedAtAsc)
	if err != nil {
		t.Fatalf("FindByPropertyIndexFiltered: %v", err)
	}

	want := []int64{ids[2], ids[4], ids[0]} // created_at 100, 200, 300 - ascending, not insertion order
	if len(got) != len(want) {
		t.Fatalf("FindByPropertyIndexFiltered returned %d nodes, want %d: %+v", len(got), len(want), got)
	}
	for i, n := range got {
		if n.ID != want[i] {
			t.Fatalf("FindByPropertyIndexFiltered[%d].ID = %d, want %d (order: %v)", i, n.ID, want[i], got)
		}
	}
}

// TestAddIndexedNode_UnsupportedTypeSkipped proves a property value of an unsupported type
// (e.g. bool) is silently skipped rather than erroring - the node itself is unaffected.
func TestAddIndexedNode_UnsupportedTypeSkipped(t *testing.T) {
	g := openTestGraph(t)
	id, err := g.AddIndexedNode("Entity", map[string]any{"verified": true}, "verified")
	if err != nil {
		t.Fatalf("AddIndexedNode: %v", err)
	}
	if _, err := g.FindByPropertyIndex("Entity", "verified", true); err == nil {
		t.Fatal("FindByPropertyIndex(verified, true): expected an error for an unsupported value type")
	}
	if n, ok, err := g.GetNode(id); err != nil || !ok || n.Props["verified"] != true {
		t.Fatalf("GetNode(id) = %+v,%v,%v, want the node itself unaffected", n, ok, err)
	}
}
