package gordian

import (
	"encoding/json"
	"fmt"
	"testing"
)

// realBookCount and bookChunkScales ground this benchmark in the exact real numbers kata cycle 31
// established: 2,614 real books, up to ~436,538 real BookChunk nodes by its own ceiling estimate
// (2,614 books x up to 167 chunks each, the "chunk 11 of 167" comment). This is the precise real
// risk kata cycle 32 exists to fix: booksMatchingSubject/SurroundingChunks call AllNodes("Book")
// in a store that ALSO holds every one of these BookChunk nodes.
const realBookCount = 2614

var bookChunkScalesForAllNodes = []int{0, 10_000, 100_000, 436_538}

// allNodesOldFullScan is kata cycle 23's ORIGINAL AllNodes implementation, kept here verbatim
// (not in graph.go, which now has the real cycle-32 label-indexed version) purely so this
// benchmark can measure old-vs-new in the SAME run, same machine state, same moment - a real,
// direct comparison, not a cross-session/cross-machine number lookup.
func allNodesOldFullScan(g *Graph, label string) ([]Node, error) {
	var out []Node
	var decodeErr error
	err := g.store.Scan([]byte{tagNode}, func(key, value []byte) bool {
		var n Node
		if err := json.Unmarshal(value, &n); err != nil {
			decodeErr = err
			return false
		}
		if n.Label == label {
			out = append(out, n)
		}
		return true
	})
	if err != nil {
		return nil, err
	}
	if decodeErr != nil {
		return nil, decodeErr
	}
	return out, nil
}

func seedBookAndChunksFixture(b *testing.B, g *Graph, chunkCount int) {
	b.Helper()
	for i := 0; i < realBookCount; i++ {
		if _, err := g.AddNode("Book", map[string]any{"title": fmt.Sprintf("book %d", i)}); err != nil {
			b.Fatalf("AddNode Book %d: %v", i, err)
		}
	}
	for i := 0; i < chunkCount; i++ {
		if _, err := g.AddNode("BookChunk", map[string]any{"chunk_index": i}); err != nil {
			b.Fatalf("AddNode BookChunk %d: %v", i, err)
		}
	}
}

// BenchmarkAllNodes_Book_OldVsNew is kata cycle 32's own real payoff measurement: AllNodes("Book")
// cost, old full-scan implementation vs the new label-indexed one, at realistic book_chunks scale
// (up to the real 436,538 ceiling estimate) sharing ONE store with the real 2,614 Book nodes -
// exactly the shape booksMatchingSubject/SurroundingChunks hit once the deferred book_chunks data
// migration happens.
func BenchmarkAllNodes_Book_OldVsNew(b *testing.B) {
	for _, chunks := range bookChunkScalesForAllNodes {
		store := openBenchStore(b)
		g := NewGraph(store)
		seedBookAndChunksFixture(b, g, chunks)

		b.Run(fmt.Sprintf("chunks=%d/old", chunks), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				got, err := allNodesOldFullScan(g, "Book")
				if err != nil {
					b.Fatalf("allNodesOldFullScan: %v", err)
				}
				if len(got) != realBookCount {
					b.Fatalf("allNodesOldFullScan(Book) = %d, want %d", len(got), realBookCount)
				}
			}
		})
		b.Run(fmt.Sprintf("chunks=%d/new", chunks), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				got, err := g.AllNodes("Book")
				if err != nil {
					b.Fatalf("AllNodes: %v", err)
				}
				if len(got) != realBookCount {
					b.Fatalf("AllNodes(Book) = %d, want %d", len(got), realBookCount)
				}
			}
		})
	}
}
