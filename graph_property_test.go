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

// TestAddIndexedNode_NonStringPropertySkipped proves a non-string property named in indexedKeys
// is silently skipped rather than erroring - AddIndexedNode only ever indexes string values.
func TestAddIndexedNode_NonStringPropertySkipped(t *testing.T) {
	g := openTestGraph(t)
	id, err := g.AddIndexedNode("Entity", map[string]any{"count": 42}, "count")
	if err != nil {
		t.Fatalf("AddIndexedNode: %v", err)
	}
	got, err := g.FindByPropertyIndex("Entity", "count", "42")
	if err != nil {
		t.Fatalf("FindByPropertyIndex: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("FindByPropertyIndex(count,42) = %+v, want empty - non-string props are never indexed", got)
	}
	if n, ok, err := g.GetNode(id); err != nil || !ok || n.Props["count"] != float64(42) {
		t.Fatalf("GetNode(id) = %+v,%v,%v, want the node itself unaffected", n, ok, err)
	}
}
