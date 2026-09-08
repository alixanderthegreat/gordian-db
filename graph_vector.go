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

// vectorTopKScan is VectorTopK/FilteredVectorTopK's shared heap-scan core: scan prefix (values
// already carrying the vector - no per-candidate Get), score by cosine similarity against query,
// keep the top k via an O(n log k) min-heap, resolve to full Nodes in descending-score order.
func (g *Graph) vectorTopKScan(prefix []byte, query []float32, k int) ([]Node, error) {
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

	ids := make([]int64, h.Len())
	for i := len(ids) - 1; i >= 0; i-- {
		ids[i] = int64(heap.Pop(h).(scoredID).id)
	}
	return g.resolveNodes(ids)
}

// VectorTopK returns the k nodes of label whose vectors are closest to query by cosine
// similarity, in descending-score order - a brute-force scan of just that label's vectors. This
// is the real, production home of what cycles 20/25 proved synthetically: brute force, not IVF,
// resolves categories C and E for real use (see project_gordian-db-compound-filtered-vector-search
// and project_gordian-db-vector-search-benchmark memory) - IVF's own production integration is a
// deliberately separate, later addition for whenever a large, UNFILTERED corpus (category D at
// real scale) actually needs it.
func (g *Graph) VectorTopK(label string, query []float32, k int) ([]Node, error) {
	return g.vectorTopKScan(vectorPrefix(label), query, k)
}

// FilteredVectorTopK returns the k nodes of label, restricted to those indexed under
// (filterKey, filterValue) via IndexNodeFilteredVector, whose vectors are closest to query by
// cosine similarity - the real, production home of cycle 25's own proven design (a compound
// equality filter, e.g. simple-bot's real room+sender, narrows the candidate set enough that
// brute force stays fast regardless of overall corpus size; see
// project_gordian-db-compound-filtered-vector-search memory for the real numbers). Resolves
// categories C for real production use, the same way VectorTopK resolves E.
func (g *Graph) FilteredVectorTopK(label, filterKey, filterValue string, query []float32, k int) ([]Node, error) {
	return g.vectorTopKScan(filteredVectorPrefix(label, filterKey, filterValue), query, k)
}
