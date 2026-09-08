// This file wires vector.go's promoted vector-similarity machinery into Graph/Node - kata cycle
// 27's own real point, closing the gap cycle 24 found (cycle 20's vector work was never reachable
// from Graph at all). Label-scoped, not cycle 20's own flat vecKey layout: a single Scan over one
// label's vectors must return them all with values already inline, the same "denormalize into the
// scanned value, no per-candidate Get" fix cycle 25 found necessary for FindByPropertyIndex-based
// search - the identical principle applies here.
package gordian

import (
	"container/heap"
	"encoding/binary"
	"fmt"
)

const tagVector byte = 0x06

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
	if err := g.store.Put(vectorKey(label, id), encodeVector(vec)); err != nil {
		return 0, fmt.Errorf("put vector: %w", err)
	}
	return id, nil
}

// VectorTopK returns the k nodes of label whose vectors are closest to query by cosine
// similarity, in descending-score order - a brute-force scan of just that label's vectors (one
// Scan, values already carry the vector, an O(n log k) min-heap for the top-k itself). This is
// the real, production home of what cycles 20/25 proved synthetically: brute force, not IVF,
// resolves categories C and E for real use (see project_gordian-db-compound-filtered-vector-search
// and project_gordian-db-vector-search-benchmark memory) - IVF's own production integration is a
// deliberately separate, later addition for whenever a large, UNFILTERED corpus (category D at
// real scale) actually needs it.
func (g *Graph) VectorTopK(label string, query []float32, k int) ([]Node, error) {
	h := &scoredMinHeap{}
	err := g.store.Scan(vectorPrefix(label), func(key, value []byte) bool {
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
