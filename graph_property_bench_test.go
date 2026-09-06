package gordian

import (
	"fmt"
	"path/filepath"
	"testing"
)

func openBenchStore(b *testing.B) *Store {
	b.Helper()
	s, err := Open(filepath.Join(b.TempDir(), "gordian-bench-graph.db"))
	if err != nil {
		b.Fatalf("Open: %v", err)
	}
	b.Cleanup(func() { s.Close() })
	return s
}

// propBenchEntityCount and propBenchFactCount are simple-bot's own real DuckDB row counts,
// measured firsthand in kata cycle 9 (entries=580, facts=1091) - not a round number picked for
// convenience. This grounds FindByPropertyIndex's benchmark at simple-bot's real scale rather
// than an arbitrary one. (Two other candidates - a naive scan and an in-memory cache - were
// benchmarked here too during kata cycle 17 and are now decided against; see
// project_gordian-db-property-lookup-benchmark memory for those numbers - the code was removed
// once the decision was recorded, per the project's own discipline against keeping unneeded code
// "just in case".)
const (
	propBenchEntityCount = 580
	propBenchFactCount   = 1091
)

// seedPropBenchGraph populates g with propBenchEntityCount Entity nodes (each with a unique
// indexed "name" property) and propBenchFactCount Fact nodes, matching simple-bot's real node mix
// at real scale.
func seedPropBenchGraph(b *testing.B, g *Graph) {
	b.Helper()
	for i := 0; i < propBenchEntityCount; i++ {
		name := fmt.Sprintf("entity-%d", i)
		if _, err := g.AddIndexedNode("Entity", map[string]any{"name": name}, "name"); err != nil {
			b.Fatalf("seed entity %d: %v", i, err)
		}
	}
	for i := 0; i < propBenchFactCount; i++ {
		if _, err := g.AddNode("Fact", map[string]any{"text": fmt.Sprintf("fact %d", i)}); err != nil {
			b.Fatalf("seed fact %d: %v", i, err)
		}
	}
}

// benchLookupName is the real property value being looked up - roughly in the middle of the
// entity range, so the benchmark isn't an artificially cheap first-match.
var benchLookupName = fmt.Sprintf("entity-%d", propBenchEntityCount/2)

func BenchmarkFindByProperty_Index(b *testing.B) {
	store := openBenchStore(b)
	g := NewGraph(store)
	seedPropBenchGraph(b, g)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got, err := g.FindByPropertyIndex("Entity", "name", benchLookupName)
		if err != nil {
			b.Fatalf("FindByPropertyIndex: %v", err)
		}
		if len(got) != 1 {
			b.Fatalf("FindByPropertyIndex = %d results, want 1", len(got))
		}
	}
}

// BenchmarkAddNode_Plain and BenchmarkAddNode_Indexed isolate the real write-path cost
// AddIndexedNode pays over plain AddNode: one extra Put per indexed node.
func BenchmarkAddNode_Plain(b *testing.B) {
	store := openBenchStore(b)
	g := NewGraph(store)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := g.AddNode("Entity", map[string]any{"name": fmt.Sprintf("e-%d", i)}); err != nil {
			b.Fatalf("AddNode: %v", err)
		}
	}
}

func BenchmarkAddNode_Indexed(b *testing.B) {
	store := openBenchStore(b)
	g := NewGraph(store)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := g.AddIndexedNode("Entity", map[string]any{"name": fmt.Sprintf("e-%d", i)}, "name"); err != nil {
			b.Fatalf("AddIndexedNode: %v", err)
		}
	}
}
