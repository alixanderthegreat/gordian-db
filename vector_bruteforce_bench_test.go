package gordian

import (
	"container/heap"
	"encoding/binary"
	"path/filepath"
	"testing"
)

// vecBenchTag is this benchmark's own key tag - deliberately 0x10, clear of every real
// production tag range used so far (0x00-0x05: graph.go's tagCounter/tagNode/tagEdgeOut/
// tagEdgeIn, graph_property.go's tagPropIndex, graph_range.go's tagRangeIndex added in kata
// cycle 24 after this constant's original value of 0x05 was picked - a real, if harmless,
// numeric collision found and fixed in cycle 25 rather than left as a landmine for whichever
// future benchmark first combines a real Graph with this test-only vector scheme in one Store).
const vecBenchTag byte = 0x10

// vecKey encodes a vector's id as a fixed 8-byte big-endian key under vecBenchTag - the same
// fixed-width-id key shape as graph.go's nodeKey, chosen for the same reason: fixed width means
// no length-prefix ambiguity is even possible.
func vecKey(id uint64) []byte {
	key := make([]byte, 9)
	key[0] = vecBenchTag
	binary.BigEndian.PutUint64(key[1:], id)
	return key
}

var vecPrefix = []byte{vecBenchTag}

// seedVectors durably writes vecs into store under vecKey(0), vecKey(1), ... - the Store-backed
// baseline's entire "index": there is no other structure, by design, since this benchmark exists
// to measure exactly how expensive that absence is.
func seedVectors(tb testing.TB, store *Store, vecs [][]float32) {
	tb.Helper()
	for i, v := range vecs {
		if err := store.Put(vecKey(uint64(i)), encodeVector(v)); err != nil {
			tb.Fatalf("seed vector %d: %v", i, err)
		}
	}
}

// cosineSimilarity/scoredID/scoredMinHeap moved to the real production vector.go in kata cycle
// 27 - this file keeps the flat, non-label-scoped candidate (vecKey/bruteForceCosineTopK) as a
// reference/ground-truth implementation, per cycle 20's own decision to keep it as such.

// bruteForceCosineTopK scans every vector in store (under vecPrefix), scores it against query by
// cosine similarity, and returns the k highest-scoring ids in descending-score order - the
// simplest possibly-correct approach to "find similar vectors" over a plain KV store: no index
// maintained on write, full-corpus work on every query.
func bruteForceCosineTopK(store *Store, query []float32, k int) ([]uint64, error) {
	h := &scoredMinHeap{}
	err := store.Scan(vecPrefix, func(key, value []byte) bool {
		id := binary.BigEndian.Uint64(key[1:])
		score := cosineSimilarity(query, decodeVector(value))
		if h.Len() < k {
			heap.Push(h, scoredID{id, score})
		} else if score > (*h)[0].score {
			heap.Pop(h)
			heap.Push(h, scoredID{id, score})
		}
		return true
	})
	if err != nil {
		return nil, err
	}

	// h currently holds the top-k in ascending score order (min-heap) - pop it out to get
	// descending order, the order a real caller (and array_cosine_similarity's own ORDER BY
	// similarity DESC usage in journal.go) actually wants.
	out := make([]uint64, h.Len())
	for i := len(out) - 1; i >= 0; i-- {
		out[i] = heap.Pop(h).(scoredID).id
	}
	return out, nil
}

func openVecBenchStore(tb testing.TB) *Store {
	tb.Helper()
	s, err := Open(filepath.Join(tb.TempDir(), "gordian-vecbench.db"))
	if err != nil {
		tb.Fatalf("Open: %v", err)
	}
	tb.Cleanup(func() { s.Close() })
	return s
}

// TestBruteForceCosineTopK_MatchesNaiveFullSort cross-checks bruteForceCosineTopK's min-heap
// bookkeeping against an independent, deliberately naive full-sort implementation over the same
// data - the same cross-check discipline kata cycle 19 used for goraphdb's own NodeCount/
// EdgeCount (an implementation that agrees with itself isn't proof; an independent second
// implementation reaching the same answer is).
func TestBruteForceCosineTopK_MatchesNaiveFullSort(t *testing.T) {
	const n, k = 500, 10
	vecs := genVectors(n)
	store := openVecBenchStore(t)
	seedVectors(t, store, vecs)

	query := genVectors(1)[0]

	got, err := bruteForceCosineTopK(store, query, k)
	if err != nil {
		t.Fatalf("bruteForceCosineTopK: %v", err)
	}
	if len(got) != k {
		t.Fatalf("len(got) = %d, want %d", len(got), k)
	}

	type scored struct {
		id    uint64
		score float64
	}
	all := make([]scored, n)
	for i, v := range vecs {
		all[i] = scored{uint64(i), cosineSimilarity(query, v)}
	}
	for i := 0; i < len(all); i++ {
		for j := i + 1; j < len(all); j++ {
			if all[j].score > all[i].score {
				all[i], all[j] = all[j], all[i]
			}
		}
	}

	for i, id := range got {
		if id != all[i].id {
			t.Fatalf("got[%d] = id %d, naive full sort says id %d (score %v) at rank %d",
				i, id, all[i].id, all[i].score, i)
		}
	}
}

// TestBruteForceCosineTopK_QueryEqualsStoredVectorIsTopMatch: a vector queried against itself
// must be its own closest match (cosine similarity 1, or within float rounding of it) - a basic
// sanity check independent of the naive-sort cross-check above.
func TestBruteForceCosineTopK_QueryEqualsStoredVectorIsTopMatch(t *testing.T) {
	vecs := genVectors(200)
	store := openVecBenchStore(t)
	seedVectors(t, store, vecs)

	const targetID = 42
	got, err := bruteForceCosineTopK(store, vecs[targetID], 5)
	if err != nil {
		t.Fatalf("bruteForceCosineTopK: %v", err)
	}
	if got[0] != targetID {
		t.Fatalf("top match = id %d, want id %d (query was that vector itself)", got[0], targetID)
	}
}

// BenchmarkBruteForceCosineTopK_Query measures per-query latency of bruteForceCosineTopK across
// vectorScales - the real number this cycle needs: does a full-corpus scan-and-score stay cheap
// enough as n grows toward simple-bot's real scale, or does it become the bottleneck the target
// condition predicted.
func BenchmarkBruteForceCosineTopK_Query(b *testing.B) {
	for _, scale := range vectorScales {
		b.Run(scale.name, func(b *testing.B) {
			vecs := genVectors(scale.count)
			store := openVecBenchStore(b)
			seedVectors(b, store, vecs)
			query := genVectors(1)[0]

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := bruteForceCosineTopK(store, query, 10); err != nil {
					b.Fatalf("bruteForceCosineTopK: %v", err)
				}
			}
		})
	}
}

// BenchmarkBruteForceCosineTopK_Populate measures the write/insert-side cost of this baseline
// across vectorScales - deliberately cheap by construction (one Put per vector, no index to
// maintain), the mirror image of BenchmarkAddNode_Plain vs. BenchmarkAddNode_Indexed in
// graph_property_bench_test.go: this baseline pays nothing extra on write, all its cost is on
// read.
func BenchmarkBruteForceCosineTopK_Populate(b *testing.B) {
	for _, scale := range vectorScales {
		b.Run(scale.name, func(b *testing.B) {
			vecs := genVectors(scale.count)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				store := openVecBenchStore(b)
				b.StartTimer()
				seedVectors(b, store, vecs)
			}
		})
	}
}
