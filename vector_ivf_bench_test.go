package gordian

import (
	"container/heap"
	"encoding/binary"
	"math"
	"math/rand/v2"
	"testing"
)

// vecIVFTag is this benchmark's own key tag - 0x11, clear of every real production tag range
// (see vecBenchTag's own doc comment in vector_bruteforce_bench_test.go for why 0x05/0x06 were
// retired in kata cycle 25).
const vecIVFTag byte = 0x11

// ivfKey encodes (clusterID, vectorID) so Store.Scan(ivfClusterPrefix(clusterID), ...) visits
// only that cluster's vectors - the entire point of IVF (Inverted File index, the classical
// clustering-based ANN approach named directly in this cycle's own test item text) over the flat
// brute-force layout: a query only pays scan cost proportional to the probed clusters, not the
// whole corpus.
func ivfKey(clusterID uint32, vectorID uint64) []byte {
	key := make([]byte, 13)
	key[0] = vecIVFTag
	binary.BigEndian.PutUint32(key[1:5], clusterID)
	binary.BigEndian.PutUint64(key[5:], vectorID)
	return key
}

func ivfClusterPrefix(clusterID uint32) []byte {
	key := make([]byte, 5)
	key[0] = vecIVFTag
	binary.BigEndian.PutUint32(key[1:], clusterID)
	return key
}

// ivfNumClusters is the classical IVF heuristic (used by FAISS and others): roughly sqrt(n)
// clusters balances cluster size against cluster count - too few clusters and each one is nearly
// the whole corpus (no speedup over brute force), too many and per-query centroid-scoring cost
// starts to dominate.
func ivfNumClusters(n int) int {
	c := int(math.Sqrt(float64(n)))
	if c < 1 {
		c = 1
	}
	return c
}

// ivfNProbe is fixed rather than scaled with ivfNumClusters - deliberately, so that as n (and
// thus ivfNumClusters) grows, probing the same fixed number of clusters covers a SHRINKING
// fraction of the corpus, which is exactly the asymptotic property that should make IVF's
// advantage over brute force widen at larger scale, not stay constant.
const ivfNProbe = 8

// kmeansIters is a fixed iteration count, not a convergence-threshold loop - "make it go" over
// "make it converge optimally", matching this project's own established sequencing philosophy.
// 10 is enough for Lloyd's algorithm to stabilize on well-separated data; genVectors's own
// documented lack of cluster structure (uniform random, no natural clusters) means convergence
// quality here is inherently limited regardless of iteration count - a real, already-documented
// benchmark caveat, not something more iterations would fix.
const kmeansIters = 10

// ivfIndex holds an IVF index's in-memory centroids - small by construction (ivfNumClusters(n) x
// embeddingDim floats, e.g. ~1.5MB at n=1M) so keeping them in memory rather than persisting them
// to Store is a deliberate, documented simplification for this synthetic benchmark: a real
// implementation would need to persist and reload them, but that's an orthogonal question to the
// core measurement this cycle needs (does clustering reduce per-query scan cost at all), and
// loading ~1.5MB once at open time would not materially change the numbers below.
type ivfIndex struct {
	centroids [][]float32
}

// ivfTrainSampleSize caps how many vectors k-means actually trains on. Discovered necessary, not
// a premature optimization: an earlier version of this benchmark ran Lloyd's iterations over the
// FULL n vectors, and at n=1M (ivfNumClusters=1000) that was measured LIVE (via `ps` while it ran
// for 37+ minutes without finishing a single sub-benchmark) to be computationally impractical -
// O(n*k*dim) per iteration x kmeansIters iterations. Training on a bounded sample and doing one
// final full-dataset assignment pass with the trained centroids is not a shortcut, it's exactly
// what real IVF implementations (FAISS included) do: train() on a subsample, add() separately to
// place every vector. This is itself a real finding worth keeping: naive O(n*k) iterative
// clustering does not scale to real corpus sizes without this, confirming obstacle 3's concern
// about ANN engineering difficulty in a more concrete way than anticipated.
const ivfTrainSampleSize = 20_000

// vecNorm and cosineSimNorms split cosineSimilarity's work so a norm computed once (a centroid
// across n comparisons, or a vector across k comparisons) isn't redundantly recomputed on every
// single pairwise call - the other half of the fix that makes the final full-dataset assignment
// pass (unavoidable once, unlike the training iterations) complete in reasonable time.
func vecNorm(v []float32) float64 {
	var sumSq float64
	for _, f := range v {
		d := float64(f)
		sumSq += d * d
	}
	return math.Sqrt(sumSq)
}

func cosineSimNorms(a, b []float32, normA, normB float64) float64 {
	if normA == 0 || normB == 0 {
		return 0
	}
	var dot float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
	}
	return dot / (normA * normB)
}

// nearestCentroid returns the index of the centroid closest to v by cosine similarity, given v's
// precomputed norm and every centroid's precomputed norm (both invariant across the many calls
// each is reused across, hence precomputed by the caller rather than inside this function).
func nearestCentroid(v []float32, vNorm float64, centroids [][]float32, centroidNorms []float64) int {
	best, bestScore := 0, cosineSimNorms(v, centroids[0], vNorm, centroidNorms[0])
	for c := 1; c < len(centroids); c++ {
		if s := cosineSimNorms(v, centroids[c], vNorm, centroidNorms[c]); s > bestScore {
			best, bestScore = c, s
		}
	}
	return best
}

// buildIVFIndex runs a fixed-iteration spherical k-means (cosine-similarity assignment, plain
// arithmetic-mean centroid update - re-normalizing centroids would not change cosine-similarity
// ranking, so it's skipped as unneeded complexity) over a bounded training sample of vecs (see
// ivfTrainSampleSize), then does one final assignment pass over every vector in vecs using the
// trained centroids and writes each into store under its assigned cluster's key range. Returns
// the built index (centroids only - the vectors themselves are the entries just written to
// store).
func buildIVFIndex(tb testing.TB, store *Store, vecs [][]float32) *ivfIndex {
	tb.Helper()
	n := len(vecs)
	k := ivfNumClusters(n)

	rng := rand.New(rand.NewPCG(uint64(n), uint64(n)+1))
	sampleSize := min(n, ivfTrainSampleSize)
	perm := rng.Perm(n)
	sample := make([][]float32, sampleSize)
	sampleNorms := make([]float64, sampleSize)
	for i := 0; i < sampleSize; i++ {
		sample[i] = vecs[perm[i]]
		sampleNorms[i] = vecNorm(sample[i])
	}

	// k = ivfNumClusters(n) = sqrt(n) never exceeds ivfTrainSampleSize (20,000) until n reaches
	// 4*10^8, far beyond this project's vectorScales - so sample[:k] always has k distinct
	// vectors (perm guarantees distinctness) to seed initial centroids from.
	centroids := make([][]float32, k)
	for i := 0; i < k; i++ {
		centroids[i] = append([]float32(nil), sample[i]...)
	}

	assign := make([]int, sampleSize)
	for iter := 0; iter < kmeansIters; iter++ {
		centroidNorms := make([]float64, k)
		for c, cv := range centroids {
			centroidNorms[c] = vecNorm(cv)
		}
		for i, v := range sample {
			assign[i] = nearestCentroid(v, sampleNorms[i], centroids, centroidNorms)
		}

		sums := make([][]float64, k)
		counts := make([]int, k)
		for c := range sums {
			sums[c] = make([]float64, embeddingDim)
		}
		for i, v := range sample {
			c := assign[i]
			counts[c]++
			for j, f := range v {
				sums[c][j] += float64(f)
			}
		}
		for c := 0; c < k; c++ {
			if counts[c] == 0 {
				continue // empty cluster: leave its centroid where it was, documented in the type doc
			}
			newCentroid := make([]float32, embeddingDim)
			for j := range newCentroid {
				newCentroid[j] = float32(sums[c][j] / float64(counts[c]))
			}
			centroids[c] = newCentroid
		}
	}

	finalCentroidNorms := make([]float64, k)
	for c, cv := range centroids {
		finalCentroidNorms[c] = vecNorm(cv)
	}
	for i, v := range vecs {
		c := uint32(nearestCentroid(v, vecNorm(v), centroids, finalCentroidNorms))
		if err := store.Put(ivfKey(c, uint64(i)), encodeVector(v)); err != nil {
			tb.Fatalf("put ivf vector %d (cluster %d): %v", i, c, err)
		}
	}

	return &ivfIndex{centroids: centroids}
}

// ivfCosineTopK scores every centroid against query (cheap - centroids are few by construction),
// probes the ivfNProbe closest clusters' full contents from store, and returns the k
// highest-scoring ids across just those probed clusters - approximate, unlike
// bruteForceCosineTopK's exhaustive exact answer, since a vector's true nearest neighbor could in
// principle sit in an unprobed cluster.
func ivfCosineTopK(store *Store, idx *ivfIndex, query []float32, k int) ([]uint64, error) {
	type centroidScore struct {
		id    int
		score float64
	}
	cs := make([]centroidScore, len(idx.centroids))
	for i, c := range idx.centroids {
		cs[i] = centroidScore{i, cosineSimilarity(query, c)}
	}
	for i := 0; i < len(cs); i++ {
		for j := i + 1; j < len(cs); j++ {
			if cs[j].score > cs[i].score {
				cs[i], cs[j] = cs[j], cs[i]
			}
		}
	}
	nProbe := ivfNProbe
	if nProbe > len(cs) {
		nProbe = len(cs)
	}

	h := &scoredMinHeap{}
	for p := 0; p < nProbe; p++ {
		clusterID := uint32(cs[p].id)
		err := store.Scan(ivfClusterPrefix(clusterID), func(key, value []byte) bool {
			id := binary.BigEndian.Uint64(key[5:])
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
	}

	out := make([]uint64, h.Len())
	for i := len(out) - 1; i >= 0; i-- {
		out[i] = heap.Pop(h).(scoredID).id
	}
	return out, nil
}

// TestIVFCosineTopK_RecallAgainstBruteForce measures IVF's real recall@10 against
// bruteForceCosineTopK's exact ground truth over the SAME data and queries - the honest check
// this approximate approach needs before its benchmark numbers mean anything: a fast wrong answer
// is not a result worth having. Uses genVectors's own documented uniform-random (no natural
// cluster structure) fixture, so a low recall number here is expected and informative, not a bug
// - see ivfIndex's own type doc and vector_fixture_test.go's genVectors doc for why.
func TestIVFCosineTopK_RecallAgainstBruteForce(t *testing.T) {
	const n, numQueries, k = 2000, 20, 10
	all := genVectors(n + numQueries)
	vecs, queries := all[:n], all[n:]

	store := openVecBenchStore(t)
	idx := buildIVFIndex(t, store, vecs)

	flatStore := openVecBenchStore(t)
	seedVectors(t, flatStore, vecs)

	var totalRecall float64
	for _, q := range queries {
		got, err := ivfCosineTopK(store, idx, q, k)
		if err != nil {
			t.Fatalf("ivfCosineTopK: %v", err)
		}
		want, err := bruteForceCosineTopK(flatStore, q, k)
		if err != nil {
			t.Fatalf("bruteForceCosineTopK: %v", err)
		}
		wantSet := make(map[uint64]bool, len(want))
		for _, id := range want {
			wantSet[id] = true
		}
		hits := 0
		for _, id := range got {
			if wantSet[id] {
				hits++
			}
		}
		totalRecall += float64(hits) / float64(k)
	}
	avgRecall := totalRecall / float64(numQueries)
	t.Logf("IVF recall@%d over %d queries, n=%d, nProbe=%d/%d clusters: %.3f",
		k, numQueries, n, ivfNProbe, ivfNumClusters(n), avgRecall)
}

// BenchmarkIVFCosineTopK_Query mirrors BenchmarkBruteForceCosineTopK_Query's shape exactly (same
// vectorScales, same k=10, same single-store-then-time-N-queries structure) so the two are
// directly comparable - storage engine and query shape are identical, indexing strategy is the
// only variable.
func BenchmarkIVFCosineTopK_Query(b *testing.B) {
	for _, scale := range vectorScales {
		b.Run(scale.name, func(b *testing.B) {
			vecs := genVectors(scale.count)
			store := openVecBenchStore(b)
			idx := buildIVFIndex(b, store, vecs)
			query := genVectors(1)[0]

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := ivfCosineTopK(store, idx, query, 10); err != nil {
					b.Fatalf("ivfCosineTopK: %v", err)
				}
			}
		})
	}
}

// BenchmarkIVFCosineTopK_Populate measures build cost including the k-means clustering pass
// itself, not just the raw Store writes - the real cost an IVF-backed insert path would pay,
// unlike BenchmarkBruteForceCosineTopK_Populate's plain per-vector Put with no index to build.
func BenchmarkIVFCosineTopK_Populate(b *testing.B) {
	for _, scale := range vectorScales {
		b.Run(scale.name, func(b *testing.B) {
			vecs := genVectors(scale.count)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				store := openVecBenchStore(b)
				b.StartTimer()
				buildIVFIndex(b, store, vecs)
			}
		})
	}
}
