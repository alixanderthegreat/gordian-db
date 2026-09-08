package gordian

import (
	"container/heap"
	"encoding/binary"
	"strconv"
	"testing"
)

// roomSenderKey composes room+sender into a single indexed property value - kata cycle 25's own
// real finding: compound equality filtering needs no new gordian-db primitive, just a composite
// key through the EXISTING FindByPropertyIndex (the same "not every query needs new capability"
// conclusion cycles 21/23 already reached for other shapes). The NUL separator can't appear in
// either half of a real room/sender string, so concatenation is unambiguous.
func roomSenderKey(room, sender string) string {
	return room + "\x00" + sender
}

// seedFilteredEntry adds an Entry node indexed on the composite room_sender key, and its vector
// under vecKey(id) in the SAME store (mirroring cycle 20's own standalone vector-storage
// approach, not Node.Props - see this cycle's own obstacle about cycle 24's found integration
// gap).
func seedFilteredEntry(t testing.TB, g *Graph, store *Store, room, sender string, vec []float32) int64 {
	t.Helper()
	id, err := g.AddIndexedNode("Entry", map[string]any{
		"room": room, "sender": sender, "room_sender": roomSenderKey(room, sender),
	}, "room_sender")
	if err != nil {
		t.Fatalf("AddIndexedNode: %v", err)
	}
	if err := store.Put(vecKey(uint64(id)), encodeVector(vec)); err != nil {
		t.Fatalf("put vector for entry %d: %v", id, err)
	}
	return id
}

// filteredCosineTopK is the real shape searchTable/AppendFact both need: narrow to the
// room+sender subset via the EXISTING FindByPropertyIndex, then brute-force-rank just that
// subset - not a new gordian-db primitive, a composition of two already-proven ones (cycle 18's
// FindByPropertyIndex, cycle 20's cosineSimilarity/scoredMinHeap top-k pattern).
func filteredCosineTopK(g *Graph, store *Store, room, sender string, query []float32, k int) ([]int64, error) {
	nodes, err := g.FindByPropertyIndex("Entry", "room_sender", roomSenderKey(room, sender))
	if err != nil {
		return nil, err
	}
	type scored struct {
		id    int64
		score float64
	}
	scoredNodes := make([]scored, 0, len(nodes))
	for _, n := range nodes {
		v, ok, err := store.Get(vecKey(uint64(n.ID)))
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		scoredNodes = append(scoredNodes, scored{n.ID, cosineSimilarity(query, decodeVector(v))})
	}
	// A plain sort is fine here (not scoredMinHeap's O(n log k) trick) - n is already the
	// room+sender-narrowed subset, not the whole corpus, and this cycle's own point is that n
	// stays small regardless of corpus size.
	for i := 0; i < len(scoredNodes); i++ {
		for j := i + 1; j < len(scoredNodes); j++ {
			if scoredNodes[j].score > scoredNodes[i].score {
				scoredNodes[i], scoredNodes[j] = scoredNodes[j], scoredNodes[i]
			}
		}
	}
	if len(scoredNodes) > k {
		scoredNodes = scoredNodes[:k]
	}
	out := make([]int64, len(scoredNodes))
	for i, s := range scoredNodes {
		out[i] = s.id
	}
	return out, nil
}

// seedFilteredEntryFast mirrors seedFilteredEntry but denormalizes the vector directly into the
// property-index entry's VALUE (propIndexKey, reused directly since this file lives in package
// gordian) instead of a separate vecKey Put - a real fix for a real finding this cycle's own
// first benchmark run surfaced: resolving each candidate via an individual Store.Get (one round
// trip per node) is much slower than decoding the vector straight out of a single Scan's already-
// returned values, the same "one scan beats N gets" principle cycle 20's own bruteForceCosineTopK
// already relied on for its own (unfiltered) shape.
func seedFilteredEntryFast(t testing.TB, g *Graph, store *Store, room, sender string, vec []float32) int64 {
	t.Helper()
	id, err := g.AddNode("Entry", map[string]any{"room": room, "sender": sender})
	if err != nil {
		t.Fatalf("AddNode: %v", err)
	}
	key := propIndexKey("Entry", "room_sender", roomSenderKey(room, sender), id)
	if err := store.Put(key, encodeVector(vec)); err != nil {
		t.Fatalf("put denormalized index entry for entry %d: %v", id, err)
	}
	return id
}

// filteredCosineTopKFast is filteredCosineTopK's fixed-the-real-way sibling: one Scan over the
// room+sender-scoped index prefix, decoding each entry's vector directly from its value (no
// GetNode, no per-candidate Store.Get) - see seedFilteredEntryFast's own doc comment for why.
func filteredCosineTopKFast(store *Store, room, sender string, query []float32, k int) ([]int64, error) {
	prefix := propIndexPrefix("Entry", "room_sender", roomSenderKey(room, sender))
	// Reuses scoredID/scoredMinHeap (vector_bruteforce_bench_test.go) for the same O(n log k)
	// bounded top-k this cycle's first draft skipped in favor of an O(n^2) selection sort - a
	// second real inefficiency this cycle's own benchmarking surfaced, fixed the same way cycle
	// 20 already established rather than left in what's about to be recommended as the real
	// design.
	h := &scoredMinHeap{}
	err := store.Scan(prefix, func(key, value []byte) bool {
		id := binary.BigEndian.Uint64(key[len(key)-8:])
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
	out := make([]int64, h.Len())
	for i := len(out) - 1; i >= 0; i-- {
		out[i] = int64(heap.Pop(h).(scoredID).id)
	}
	return out, nil
}

// BenchmarkFilteredCosineTopKFast_LargeCorpus proves item 1's own numbers weren't an artifact of
// a tiny, isolated store: seeds a real 1M-vector TOTAL corpus spread across many DIFFERENT
// room+sender groups (mirroring vectorScales' own "1M" anchor, cycle ten's large-scale reference),
// then queries just ONE 200-sized group - the same size already measured in isolation by
// BenchmarkFilteredCosineTopKFast_Search/200 (~213us). A result close to that number, not
// meaningfully worse, is the real confirmation this cycle's whole hypothesis rests on: total
// corpus size doesn't matter to this query's cost, only the matching group's own subset size
// does - not just assumed from FindByPropertyIndex's prefix-scan design, actually measured at
// real scale.
func BenchmarkFilteredCosineTopKFast_LargeCorpus(b *testing.B) {
	const totalCorpus = 1_000_000
	const targetGroupSize = 200
	const otherGroups = 5_000 // totalCorpus-targetGroupSize spread across this many other groups

	store := openVecBenchStore(b)
	g := NewGraph(store)

	targetVecs := genVectors(targetGroupSize)
	for _, v := range targetVecs {
		seedFilteredEntryFast(b, g, store, "target-room", "target-sender", v)
	}

	remaining := totalCorpus - targetGroupSize
	perGroup := remaining / otherGroups
	otherVecs := genVectors(perGroup) // reused across groups - fixture content doesn't matter here, only real key/store volume does
	for i := 0; i < otherGroups; i++ {
		room := "room-" + strconv.Itoa(i)
		for _, v := range otherVecs {
			seedFilteredEntryFast(b, g, store, room, "sender", v)
		}
	}

	query := genVectors(1)[0]
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got, err := filteredCosineTopKFast(store, "target-room", "target-sender", query, 10)
		if err != nil {
			b.Fatalf("filteredCosineTopKFast: %v", err)
		}
		if len(got) != 10 {
			b.Fatalf("filteredCosineTopKFast returned %d results, want 10", len(got))
		}
	}
}

// TestFilteredCosineTopKFast_NarrowsToExactRoomAndSender mirrors
// TestFilteredCosineTopK_NarrowsToExactRoomAndSender exactly, proving the fast variant has the
// same real compound-AND correctness, not just better numbers.
func TestFilteredCosineTopKFast_NarrowsToExactRoomAndSender(t *testing.T) {
	store := openVecBenchStore(t)
	g := NewGraph(store)
	vecs := genVectors(4)

	match := seedFilteredEntryFast(t, g, store, "room1", "alice", vecs[0])
	seedFilteredEntryFast(t, g, store, "room1", "bob", vecs[1])
	seedFilteredEntryFast(t, g, store, "room2", "alice", vecs[2])
	seedFilteredEntryFast(t, g, store, "room2", "bob", vecs[3])

	got, err := filteredCosineTopKFast(store, "room1", "alice", vecs[0], 10)
	if err != nil {
		t.Fatalf("filteredCosineTopKFast: %v", err)
	}
	if len(got) != 1 || got[0] != match {
		t.Fatalf("filteredCosineTopKFast(room1,alice) = %v, want exactly [%d]", got, match)
	}
}

// BenchmarkFilteredCosineTopKFast_Search mirrors BenchmarkFilteredCosineTopK_Search exactly
// (same sizes, same k=10) against the fast variant, so the two are directly comparable.
func BenchmarkFilteredCosineTopKFast_Search(b *testing.B) {
	for _, n := range filteredSubsetSizes {
		b.Run(itoa(n), func(b *testing.B) {
			store := openVecBenchStore(b)
			g := NewGraph(store)
			vecs := genVectors(n)
			for _, v := range vecs {
				seedFilteredEntryFast(b, g, store, "room1", "alice", v)
			}
			query := genVectors(1)[0]

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := filteredCosineTopKFast(store, "room1", "alice", query, 10); err != nil {
					b.Fatalf("filteredCosineTopKFast: %v", err)
				}
			}
		})
	}
}

// filteredSubsetSizes are the per-filter (room+sender) candidate-set sizes this cycle benchmarks,
// since no real per-room/per-sender cardinality data exists (only kata cycle 9's aggregate
// entries=580/facts=1091 corpus totals) - a range standing in for "no real number to ground one
// specific size on", from small to a deliberately generous upper bound.
var filteredSubsetSizes = []int{10, 50, 200, 1000, 5000}

// seedFilteredSubset seeds n vectors all under the SAME room+sender - the point of this
// benchmark is per-filter subset size, not corpus size, so every seeded vector must actually be
// in the one filter being queried.
func seedFilteredSubset(b *testing.B, g *Graph, store *Store, n int) {
	b.Helper()
	vecs := genVectors(n)
	for _, v := range vecs {
		seedFilteredEntry(b, g, store, "room1", "alice", v)
	}
}

// BenchmarkFilteredCosineTopK_Search mirrors searchTable's real shape (top-10 within one
// room+sender) across filteredSubsetSizes.
func BenchmarkFilteredCosineTopK_Search(b *testing.B) {
	for _, n := range filteredSubsetSizes {
		b.Run(itoa(n), func(b *testing.B) {
			store := openVecBenchStore(b)
			g := NewGraph(store)
			seedFilteredSubset(b, g, store, n)
			query := genVectors(1)[0]

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := filteredCosineTopK(g, store, "room1", "alice", query, 10); err != nil {
					b.Fatalf("filteredCosineTopK: %v", err)
				}
			}
		})
	}
}

// BenchmarkFilteredCosineTopK_Dedup mirrors AppendFact's own real shape (top-1 nearest neighbor
// within one room+sender, the write-time dedup check) across filteredSubsetSizes.
func BenchmarkFilteredCosineTopK_Dedup(b *testing.B) {
	for _, n := range filteredSubsetSizes {
		b.Run(itoa(n), func(b *testing.B) {
			store := openVecBenchStore(b)
			g := NewGraph(store)
			seedFilteredSubset(b, g, store, n)
			query := genVectors(1)[0]

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := filteredCosineTopK(g, store, "room1", "alice", query, 1); err != nil {
					b.Fatalf("filteredCosineTopK: %v", err)
				}
			}
		})
	}
}

func itoa(n int) string {
	if n >= 1000 {
		return strconv.Itoa(n/1000) + "k"
	}
	return strconv.Itoa(n)
}

// TestFilteredCosineTopK_NarrowsToExactRoomAndSender proves the composite key correctly excludes
// BOTH a same-room-different-sender node AND a same-sender-different-room node - not just that
// the matching one is included. The real compound-AND semantics searchTable/AppendFact need.
func TestFilteredCosineTopK_NarrowsToExactRoomAndSender(t *testing.T) {
	store := openVecBenchStore(t)
	g := NewGraph(store)
	vecs := genVectors(4)

	match := seedFilteredEntry(t, g, store, "room1", "alice", vecs[0])
	seedFilteredEntry(t, g, store, "room1", "bob", vecs[1])   // same room, different sender
	seedFilteredEntry(t, g, store, "room2", "alice", vecs[2]) // same sender, different room
	seedFilteredEntry(t, g, store, "room2", "bob", vecs[3])   // neither matches

	got, err := filteredCosineTopK(g, store, "room1", "alice", vecs[0], 10)
	if err != nil {
		t.Fatalf("filteredCosineTopK: %v", err)
	}
	if len(got) != 1 || got[0] != match {
		t.Fatalf("filteredCosineTopK(room1,alice) = %v, want exactly [%d]", got, match)
	}
}
