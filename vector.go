// This file is gordian-db's real production vector-similarity machinery, promoted from test-only
// infrastructure (kata cycle 20's own encodeVector/decodeVector/cosineSimilarity/scoredMinHeap,
// built for a synthetic benchmark in vector_fixture_test.go/vector_bruteforce_bench_test.go) into
// real code - kata cycle 27's own job, closing the gap cycle 24 found: this machinery was never
// reachable from Graph/Node at all before this file existed. graph_vector.go builds on top of
// this to wire it into Graph as AddNodeWithVector/VectorTopK. IVF (cycle 20's own hybrid decision
// for large, unfiltered corpora) is deliberately NOT promoted here - it needs its own separate
// design (centroid persistence was never solved even in cycle 20's synthetic benchmark) and stays
// test-only until that real design work happens.
package gordian

import (
	"encoding/binary"
	"math"
)

// encodeVector serializes a float32 vector to little-endian bytes for storage as a Store value -
// the same raw-bytes contract every other gordian-db value already follows (Store has no type
// system of its own).
func encodeVector(v []float32) []byte {
	buf := make([]byte, len(v)*4)
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

// decodeVector is encodeVector's inverse.
func decodeVector(b []byte) []float32 {
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return v
}

// cosineSimilarity is the same metric DuckDB's real array_cosine_similarity computes (confirmed
// against simple-bot/src/journal.go's own queries) - dot product over the product of norms, not
// assuming pre-normalized input.
func cosineSimilarity(a, b []float32) float64 {
	var dot, normA, normB float64
	for i := range a {
		da, db := float64(a[i]), float64(b[i])
		dot += da * db
		normA += da * da
		normB += db * db
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// scoredID pairs a vector's id with its similarity to the query - the unit scoredMinHeap orders.
type scoredID struct {
	id    uint64
	score float64
}

// scoredMinHeap is a min-heap on score, so the smallest of the current top-k sits at the root and
// can be evicted in O(log k) the moment a better candidate is found - the standard bounded
// top-k-via-min-heap approach, O(n log k) instead of an O(n log n) full sort of every candidate.
type scoredMinHeap []scoredID

func (h scoredMinHeap) Len() int            { return len(h) }
func (h scoredMinHeap) Less(i, j int) bool  { return h[i].score < h[j].score }
func (h scoredMinHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *scoredMinHeap) Push(x interface{}) { *h = append(*h, x.(scoredID)) }
func (h *scoredMinHeap) Pop() interface{} {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}
