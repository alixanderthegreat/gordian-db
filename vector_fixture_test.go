package gordian

import "math/rand/v2"

// embeddingDim mirrors simple-bot's real embedDim (simple-bot/src/main.go: embedDim = 384) - the
// actual dimension DuckDB's VSS/HNSW index stores today, confirmed by reading that file directly,
// not picked as a round number.
const embeddingDim = 384

// vectorScale names one point in kata cycle 20's benchmark scale sweep, growing toward
// simple-bot's real order of magnitude without claiming to reach it. Even the largest step here
// is still a deliberate practical compromise, the same honesty bench_test.go's own
// largeScaleKeyCount (cycle ten) already applied: simple-bot's real journal.db is ~71GB across
// three embedded tables (entries, book_chunks, resource_chunks), which at ~2048 bytes/row
// (float32[384] embedding = 1536 bytes, plus text/id overhead - bench_test.go's own estimate)
// works out to on the order of tens of millions of rows, far beyond what a single benchmark run
// can populate in a reasonable amount of time. "1M" deliberately reuses cycle ten's own
// largeScaleKeyCount anchor rather than inventing a new number.
type vectorScale struct {
	name  string
	count int
}

var vectorScales = []vectorScale{
	{"1k", 1_000},
	{"50k", 50_000},
	{"1M", largeScaleKeyCount},
}

// genVectors deterministically generates n random float32 vectors of embeddingDim each, seeded on
// n itself so every call for a given scale reproduces the exact same fixture across benchmark
// runs without needing to persist anything to disk between runs. Values are uniform in [-1, 1),
// not normalized to unit length - cosine similarity is scale-invariant regardless.
//
// Real, documented limitation (not hidden): uniformly random high-dimensional vectors have no
// cluster structure, unlike real sentence embeddings, which is a known weakness of synthetic ANN
// benchmarks (near-uniform pairwise distances can make recall look artificially easy or hard).
// Acceptable for this cycle's "make it go" pass; a future cycle could generate clustered synthetic
// vectors if the measured numbers here look suspicious.
func genVectors(n int) [][]float32 {
	rng := rand.New(rand.NewPCG(uint64(n), uint64(n)))
	vecs := make([][]float32, n)
	for i := range vecs {
		v := make([]float32, embeddingDim)
		for j := range v {
			v[j] = rng.Float32()*2 - 1
		}
		vecs[i] = v
	}
	return vecs
}

// encodeVector/decodeVector moved to the real production vector.go in kata cycle 27 - this file
// keeps only the benchmark-fixture concepts (embeddingDim, vectorScales, genVectors) that have no
// place in the real Graph API.
