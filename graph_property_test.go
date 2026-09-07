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
