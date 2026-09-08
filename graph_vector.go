// This file wires vector.go's promoted vector-similarity machinery into Graph/Node - kata cycle
// 27's own real point, closing the gap cycle 24 found (cycle 20's vector work was never reachable
// from Graph at all). Label-scoped, not cycle 20's own flat vecKey layout: a single Scan over one
// label's vectors must return them all with values already inline, the same "denormalize into the
// scanned value, no per-candidate Get" fix cycle 25 found necessary for FindByPropertyIndex-based
// search - the identical principle applies here. Kata cycle 28 added the compound-filtered half
// (IndexNodeFilteredVector/FilteredVectorTopK), promoting cycle 25's own proven design the same
// way this file's unfiltered half was promoted in cycle 27.
package gordian

import (
	"container/heap"
	"encoding/binary"
	"fmt"
)

const (
	tagVector         byte = 0x06
	tagFilteredVector byte = 0x07
)

func vectorPrefix(label string) []byte {
	key := []byte{tagVector}
	return lengthPrefixed(key, label)
}

func vectorKey(label string, id int64) []byte {
	key := vectorPrefix(label)
	idBuf := make([]byte, 8)
	binary.BigEndian.PutUint64(idBuf, uint64(id))
	return append(key, idBuf...)
}

// filteredVectorPrefix/filteredVectorKey mirror vectorPrefix/vectorKey exactly, with an extra
// (filterKey, filterValue) component - the same compound-equality-filter shape
// propIndexPrefix/propIndexKey already use for FindByPropertyIndex, just carrying a vector as the
// value instead of an empty one. filterValue is an opaque, caller-composed string (e.g.
// simple-bot's own room+"\x00"+sender trick, per cycle 25) - gordian-db stays agnostic to what
// the filter actually means, the same design stance propIndexKey already takes.
func filteredVectorPrefix(label, filterKey, filterValue string) []byte {
	key := []byte{tagFilteredVector}
	key = lengthPrefixed(key, label)
	key = lengthPrefixed(key, filterKey)
	return lengthPrefixed(key, filterValue)
}

func filteredVectorKey(label, filterKey, filterValue string, id int64) []byte {
	key := filteredVectorPrefix(label, filterKey, filterValue)
	idBuf := make([]byte, 8)
	binary.BigEndian.PutUint64(idBuf, uint64(id))
	return append(key, idBuf...)
}

// IndexNodeVector writes vec under a label-scoped vector key for an EXISTING node id - extracted
// (kata cycle 28) from AddNodeWithVector's own body so a node needing more than one kind of
// secondary structure can compose them explicitly. See AddNodeWithVector's own doc comment for
// why the vector lives in its own key, never in Node.Props.
func (g *Graph) IndexNodeVector(label string, id int64, vec []float32) error {
	if err := g.store.Put(vectorKey(label, id), encodeVector(vec)); err != nil {
		return fmt.Errorf("put vector: %w", err)
	}
	return nil
}

// GetNodeVector reads back the real, already-stored vector IndexNodeVector/AddNodeWithVector
// wrote for id - a real, generically useful primitive found necessary live (kata cycle 48): a
// real backfill pass over already-embedded nodes needs each one's own real vector to compute
// similarity against, and re-embedding real text just to get back a vector that already exists
// would be pure waste (real GPU/embedder time, per this project's own ~250ms-per-embed finding
// earlier this session). Mirrors GetNode's own (Node, bool, error) shape exactly. ok=false, not
// an error, if no vector was ever indexed for this (label, id).
func (g *Graph) GetNodeVector(label string, id int64) ([]float32, bool, error) {
	raw, ok, err := g.store.Get(vectorKey(label, id))
	if err != nil {
		return nil, false, fmt.Errorf("get vector: %w", err)
	}
	if !ok {
		return nil, false, nil
	}
	return decodeVector(raw), true, nil
}

// IndexNodeFilteredVector mirrors IndexNodeVector, scoped additionally by (filterKey,
// filterValue) - the write-side half of FilteredVectorTopK, and the real primitive entries/facts'
// port needs alongside IndexNode's own event_id exact-match index on the SAME node (cycle 28's
// own found composability gap).
func (g *Graph) IndexNodeFilteredVector(label, filterKey, filterValue string, id int64, vec []float32) error {
	if err := g.store.Put(filteredVectorKey(label, filterKey, filterValue, id), encodeVector(vec)); err != nil {
		return fmt.Errorf("put filtered vector index entry: %w", err)
	}
	return nil
}

// DeindexNodeVector removes the label-scoped vector entry IndexNodeVector/AddNodeWithVector wrote
// for id - kata cycle 29's own real find: DeleteNode only ever cleaned up propIndexKey entries
// (graph_property.go), never a node's vector entry, so deleting a vector-carrying node (e.g.
// simple-bot's real DeletePartialResource, cleaning up ResourceChunk nodes after a failed
// ingestion) without this would leave a real dangling vector - VectorTopK would keep scoring and
// trying to resolve it, hitting ErrNodeNotFound (dangling reference) on every future call, not a
// cosmetic leftover. Safe to call even if no vector entry exists (Store.Delete is a no-op for a
// missing key).
func (g *Graph) DeindexNodeVector(label string, id int64) error {
	if err := g.store.Delete(vectorKey(label, id)); err != nil {
		return fmt.Errorf("delete vector: %w", err)
	}
	return nil
}

// DeindexNodeFilteredVector mirrors DeindexNodeVector for the compound-filtered case -
// IndexNodeFilteredVector's own real deletion counterpart.
func (g *Graph) DeindexNodeFilteredVector(label, filterKey, filterValue string, id int64) error {
	if err := g.store.Delete(filteredVectorKey(label, filterKey, filterValue, id)); err != nil {
		return fmt.Errorf("delete filtered vector index entry: %w", err)
	}
	return nil
}

// AddNodeWithVector creates a node (exactly like AddNode) and stores vec under a label-scoped
// vector key, so VectorTopK can scan just this label's vectors in one pass. vec is stored
// separately from Props deliberately - Props round-trips through JSON (Node's own encoding), and
// a several-hundred-float JSON array is far more verbose than vec's compact binary encoding (see
// vector.go's encodeVector) - storing it in Props would silently regress the real disk-footprint
// numbers cycle 20 already measured, exactly the integration mistake cycle 24 flagged and this
// cycle exists to avoid repeating.
func (g *Graph) AddNodeWithVector(label string, props map[string]any, vec []float32) (int64, error) {
	id, err := g.AddNode(label, props)
	if err != nil {
		return 0, err
	}
	if err := g.IndexNodeVector(label, id, vec); err != nil {
		return 0, err
	}
	return id, nil
}

// ScoredNode pairs a Node with its cosine-similarity score against the query that found it - kata
// cycle 29's own real fix to VectorTopK/FilteredVectorTopK's original ([]Node, error) signature.
// Found necessary before any real caller existed: AppendFact's real dedup check compares a
// similarity score against a threshold, and Entry/ResourceChunk/BookChunk's own real Similarity
// fields need the actual number, not just rank order - rank alone was never enough for the real
// callers this primitive exists for.
type ScoredNode struct {
	Node
	Score float64
}

// vectorTopKScan is VectorTopK/FilteredVectorTopK's shared heap-scan core: scan prefix (values
// already carrying the vector - no per-candidate Get), score by cosine similarity against query,
// keep the top k via an O(n log k) min-heap, resolve to full ScoredNodes in descending-score
// order.
func (g *Graph) vectorTopKScan(prefix []byte, query []float32, k int) ([]ScoredNode, error) {
	h := &scoredMinHeap{}
	err := g.store.Scan(prefix, func(key, value []byte) bool {
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

	ordered := make([]scoredID, h.Len())
	for i := len(ordered) - 1; i >= 0; i-- {
		ordered[i] = heap.Pop(h).(scoredID)
	}

	ids := make([]int64, len(ordered))
	for i, s := range ordered {
		ids[i] = int64(s.id)
	}
	nodes, err := g.resolveNodes(ids)
	if err != nil {
		return nil, err
	}

	out := make([]ScoredNode, len(nodes))
	for i, n := range nodes {
		out[i] = ScoredNode{Node: n, Score: ordered[i].score}
	}
	return out, nil
}

// VectorTopK returns the k nodes of label whose vectors are closest to query by cosine
// similarity, each paired with its real similarity score, in descending-score order - a
// brute-force scan of just that label's vectors. This is the real, production home of what
// cycles 20/25 proved synthetically: brute force, not IVF, resolves categories C and E for real
// use (see project_gordian-db-compound-filtered-vector-search and
// project_gordian-db-vector-search-benchmark memory) - IVF's own production integration is a
// deliberately separate, later addition for whenever a large, UNFILTERED corpus (category D at
// real scale) actually needs it.
func (g *Graph) VectorTopK(label string, query []float32, k int) ([]ScoredNode, error) {
	return g.vectorTopKScan(vectorPrefix(label), query, k)
}

// FilteredVectorTopK returns the k nodes of label, restricted to those indexed under
// (filterKey, filterValue) via IndexNodeFilteredVector, whose vectors are closest to query by
// cosine similarity, each paired with its real similarity score - the real, production home of
// cycle 25's own proven design (a compound equality filter, e.g. simple-bot's real room+sender,
// narrows the candidate set enough that brute force stays fast regardless of overall corpus size;
// see project_gordian-db-compound-filtered-vector-search memory for the real numbers). Resolves
// category C for real production use, the same way VectorTopK resolves E.
func (g *Graph) FilteredVectorTopK(label, filterKey, filterValue string, query []float32, k int) ([]ScoredNode, error) {
	return g.vectorTopKScan(filteredVectorPrefix(label, filterKey, filterValue), query, k)
}
