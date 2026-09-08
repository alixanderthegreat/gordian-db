package gordian

import (
	"fmt"
	"testing"
)

// allNodesTotalSizes are the TOTAL store sizes (across every label) this cycle benchmarks
// AllNodes against, holding the target label's own count fixed - see
// allNodesTargetCount's own doc comment for why 0 is included as a real baseline.
var allNodesTotalSizes = []int{0, 10_000, 100_000, 1_000_000}

// allNodesTargetCount is the target label's own real node count, held fixed across every
// allNodesTotalSizes step - grounded in kata cycle 9's real entries=580 measurement, not a round
// number. The question this benchmark asks is whether OTHER labels' node count (which keeps
// growing forever per this project's own "don't sanitize the record" philosophy) makes finding
// these 500 real Entry nodes slower over time, even though their own count never changes.
const allNodesTargetCount = 500

// seedAllNodesFixture populates g with allNodesTargetCount "Entry" nodes and otherCount nodes of
// an unrelated label ("Fact") - mirroring the real mix of a growing graph store where the target
// label is a small, stable slice of an ever-larger whole.
func seedAllNodesFixture(b *testing.B, g *Graph, otherCount int) {
	b.Helper()
	for i := 0; i < allNodesTargetCount; i++ {
		if _, err := g.AddNode("Entry", map[string]any{"processed": false}); err != nil {
			b.Fatalf("AddNode Entry %d: %v", i, err)
		}
	}
	for i := 0; i < otherCount; i++ {
		if _, err := g.AddNode("Fact", map[string]any{"text": fmt.Sprintf("fact %d", i)}); err != nil {
			b.Fatalf("AddNode Fact %d: %v", i, err)
		}
	}
}

// BenchmarkAllNodes_ByTotalStoreSize measures AllNodes("Entry") cost as a function of TOTAL store
// size, with the target label's own count held fixed at allNodesTargetCount throughout - kata
// cycle 26's own real question: does scanning past ever-more irrelevant (Fact-labeled) nodes cost
// real, measurable time as the store grows over the bot's real lifetime, not just a theoretical
// inefficiency accepted in cycles 21/23 without measurement.
func BenchmarkAllNodes_ByTotalStoreSize(b *testing.B) {
	for _, other := range allNodesTotalSizes {
		b.Run(fmt.Sprintf("total=%d", allNodesTargetCount+other), func(b *testing.B) {
			store := openBenchStore(b)
			g := NewGraph(store)
			seedAllNodesFixture(b, g, other)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				got, err := g.AllNodes("Entry")
				if err != nil {
					b.Fatalf("AllNodes: %v", err)
				}
				if len(got) != allNodesTargetCount {
					b.Fatalf("AllNodes(Entry) = %d nodes, want %d", len(got), allNodesTargetCount)
				}
			}
		})
	}
}
