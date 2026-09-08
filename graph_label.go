// This file is gordian-db's answer to kata cycle 32: AllNodes(label) (graph.go) originally scanned
// the ENTIRE tagNode keyspace and filtered by label only after decoding every node's JSON -
// cycle 26 benchmarked that cost as real and linear with TOTAL store size (~1.5s at 1M nodes) and
// declared it harmless, but only because its sole real caller at the time was a background job.
// Cycle 31 added synchronous, user-facing callers of AllNodes("Book") in simple-bot
// (booksMatchingSubject, SurroundingChunks) that now share a store with book_chunks - up to
// ~436,538 nodes by cycle 31's own real estimate - once the deferred data migration happens. A
// label index makes AllNodes(label) cost scale with that label's OWN node count, not the total
// store size.
//
// Architecturally different from every other secondary structure in this project
// (property/range/vector indexes, all opt-in via AddIndexedNode/IndexNodeRange/IndexNodeVector):
// every node always has exactly one label (Node.Label is not optional), so the label index can be
// fully automatic - written inside AddNode, deleted inside DeleteNode (which already has n.Label
// from its own getNodeLocked call) - with zero new exported parameters and zero cooperation
// required from any existing caller.
package gordian

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
)

const tagLabelIndex byte = 0x08

func labelIndexPrefix(label string) []byte {
	key := []byte{tagLabelIndex}
	return lengthPrefixed(key, label)
}

func labelIndexKey(label string, id int64) []byte {
	key := labelIndexPrefix(label)
	idBuf := make([]byte, 8)
	binary.BigEndian.PutUint64(idBuf, uint64(id))
	return append(key, idBuf...)
}

// BackfillLabelIndex writes a label-index entry for every existing node that doesn't already have
// one - mandatory before AllNodes can safely rely on the label index alone, since any node written
// before this change shipped has no entry yet. Idempotent (a node whose entry already exists is
// harmlessly overwritten with the same value, not duplicated or corrupted) - safe to run more than
// once, mirroring kata cycles 13-16's own real migrate-command precedent. Scans the full tagNode
// keyspace exactly once (the same cost AllNodes used to pay on every call) - a real, one-time,
// explicit migration step, not a hidden per-call cost.
func (g *Graph) BackfillLabelIndex() (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	var n int
	var putErr error
	err := g.store.Scan([]byte{tagNode}, func(key, value []byte) bool {
		var node Node
		if err := json.Unmarshal(value, &node); err != nil {
			putErr = fmt.Errorf("decode node at key %x: %w", key, err)
			return false
		}
		if err := g.store.Put(labelIndexKey(node.Label, node.ID), []byte{}); err != nil {
			putErr = fmt.Errorf("put label index entry for node %d: %w", node.ID, err)
			return false
		}
		n++
		return true
	})
	if err != nil {
		return n, fmt.Errorf("backfill label index: %w", err)
	}
	if putErr != nil {
		return n, putErr
	}
	return n, nil
}
