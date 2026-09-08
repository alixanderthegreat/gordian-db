// Package gordian: this file is gordian-db's "make it go" step for the graph shape - one of the
// two remaining capabilities (graph, SQL+vector) simple-bot's real DuckDB+goraphdb usage
// represents (see kata cycle 17). Deliberately minimal: nodes with a single label and
// properties, directed labeled edges, bidirectional adjacency lookup by label. NOT an attempt
// to replicate goraphdb's full surface (Cypher, secondary/composite indexes beyond label,
// sharding, replication) - grounded specifically against simple-bot's real, current usage
// (journal.go's mentionEntities/RelatedFacts), not goraphdb's full feature set.
package gordian

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

const (
	tagCounter byte = 0x00
	tagNode    byte = 0x01
	tagEdgeOut byte = 0x02
	tagEdgeIn  byte = 0x03
)

var counterKey = []byte{tagCounter}

// Node is a labeled graph node with arbitrary JSON-compatible properties.
type Node struct {
	ID    int64          `json:"id"`
	Label string         `json:"label"`
	Props map[string]any `json:"props,omitempty"`
}

func nodeKey(id int64) []byte {
	key := make([]byte, 1, 9)
	key[0] = tagNode
	idBuf := make([]byte, 8)
	binary.BigEndian.PutUint64(idBuf, uint64(id))
	return append(key, idBuf...)
}

func edgeOutPrefix(from int64, label string) []byte {
	key := make([]byte, 1, 9)
	key[0] = tagEdgeOut
	idBuf := make([]byte, 8)
	binary.BigEndian.PutUint64(idBuf, uint64(from))
	key = append(key, idBuf...)
	return lengthPrefixed(key, label)
}

func edgeOutKey(from int64, label string, to int64) []byte {
	key := edgeOutPrefix(from, label)
	idBuf := make([]byte, 8)
	binary.BigEndian.PutUint64(idBuf, uint64(to))
	return append(key, idBuf...)
}

func edgeInPrefix(to int64, label string) []byte {
	key := make([]byte, 1, 9)
	key[0] = tagEdgeIn
	idBuf := make([]byte, 8)
	binary.BigEndian.PutUint64(idBuf, uint64(to))
	key = append(key, idBuf...)
	return lengthPrefixed(key, label)
}

func edgeInKey(to int64, label string, from int64) []byte {
	key := edgeInPrefix(to, label)
	idBuf := make([]byte, 8)
	binary.BigEndian.PutUint64(idBuf, uint64(from))
	return append(key, idBuf...)
}

// lengthPrefixed appends a 2-byte big-endian length prefix followed by s - the same
// collision-safe composite-key convention proven in kata-journal's own key encoding: two
// different labels can never produce ambiguous overlapping key ranges the way plain
// concatenation could.
func lengthPrefixed(buf []byte, s string) []byte {
	buf = binary.BigEndian.AppendUint16(buf, uint16(len(s)))
	return append(buf, s...)
}

// Graph is a minimal, durable directed labeled property graph on top of Store.
type Graph struct {
	store *Store

	// mu serializes node-id allocation and every read-modify-write against this graph, the
	// same coarse-but-correct posture kata-journal's Store uses for its own cycle counter.
	mu sync.Mutex
}

// NewGraph wraps store with graph operations. store is not owned exclusively - a caller may use
// the same Store for other data too, as long as it doesn't collide with Graph's own tag bytes
// (0x00-0x03).
func NewGraph(store *Store) *Graph {
	return &Graph{store: store}
}

// AddNode creates a new node with the given label and properties, returning its id.
func (g *Graph) AddNode(label string, props map[string]any) (int64, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	id, err := g.nextIDLocked()
	if err != nil {
		return 0, err
	}

	n := Node{ID: id, Label: label, Props: props}
	data, err := json.Marshal(n)
	if err != nil {
		return 0, fmt.Errorf("encode node: %w", err)
	}
	if err := g.store.Put(nodeKey(id), data); err != nil {
		return 0, fmt.Errorf("put node: %w", err)
	}
	// Label index entry (kata cycle 32) - automatic and mandatory, unlike property/range/vector
	// indexes: every node always has exactly one label, so AllNodes(label) can stay fast without
	// requiring any caller to opt in.
	if err := g.store.Put(labelIndexKey(label, id), []byte{}); err != nil {
		return 0, fmt.Errorf("put label index entry: %w", err)
	}
	return id, nil
}

func (g *Graph) nextIDLocked() (int64, error) {
	v, ok, err := g.store.Get(counterKey)
	if err != nil {
		return 0, fmt.Errorf("read node counter: %w", err)
	}
	var next uint64
	if ok {
		if len(v) != 8 {
			return 0, fmt.Errorf("corrupt node counter: %d bytes, want 8", len(v))
		}
		next = binary.BigEndian.Uint64(v) + 1
	}
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, next)
	if err := g.store.Put(counterKey, buf); err != nil {
		return 0, fmt.Errorf("write node counter: %w", err)
	}
	return int64(next), nil
}

// GetNode returns the node with the given id. ok is false if it doesn't exist.
func (g *Graph) GetNode(id int64) (Node, bool, error) {
	v, ok, err := g.store.Get(nodeKey(id))
	if err != nil || !ok {
		return Node{}, ok, err
	}
	var n Node
	if err := json.Unmarshal(v, &n); err != nil {
		return Node{}, false, fmt.Errorf("decode node %d: %w", id, err)
	}
	return n, true, nil
}

// AllNodes returns every node with the given label - kata cycle 23's own real discovery: a
// caller outside package gordian (like simple-bot) has no way to construct a raw tagNode scan
// prefix itself (tagNode is unexported), so "list every Book/Resource node" - the real shape
// FindBook, LibrarySubjects, and IncompleteBooks/IncompleteResources all need (a full scan +
// Go-side filter/comparison, per cycle 21's own "not every query needs an index" finding) - had
// no way to be expressed at all until this existed.
//
// Kata cycle 32: originally a full scan over every label (matching cycle 21 item 4's own
// documented, accepted inefficiency, on the assumption scan cost wouldn't matter at real scale).
// That assumption didn't hold - cycle 31 added synchronous, user-facing AllNodes("Book") callers
// in simple-bot sharing a store with book_chunks (up to ~436,538 nodes by cycle 31's own
// estimate), so a full-store scan to find ~2,614 Book nodes became a real latency concern, not a
// theoretical one. Now backed by graph_label.go's label index - a real, label-scoped prefix scan,
// cost proportional to |label|, not total store size. Any node written before this change shipped
// has no label-index entry yet - see BackfillLabelIndex, mandatory before relying on this for a
// pre-existing store.
func (g *Graph) AllNodes(label string) ([]Node, error) {
	var ids []int64
	err := g.store.Scan(labelIndexPrefix(label), func(key, value []byte) bool {
		// key = labelIndexPrefix(label) + 8 bytes nodeID
		id := int64(binary.BigEndian.Uint64(key[len(key)-8:]))
		ids = append(ids, id)
		return true
	})
	if err != nil {
		return nil, err
	}
	return g.resolveNodes(ids)
}

// ErrNodeNotFound is returned by AddEdge when either endpoint doesn't exist, and by UpdateNode
// when id doesn't exist.
var ErrNodeNotFound = errors.New("gordian: node not found")

// UpdateNode replaces an existing node's properties in place, keeping its id and label unchanged
// - kata cycle 21's own item 0, the plain-overwrite half of the relational-rows primitive
// (AddNode/AddIndexedNode only ever insert; nothing could change an existing node's props before
// this). Returns ErrNodeNotFound if id doesn't exist.
//
// Deliberately does NOT touch any secondary-index entries (see AddIndexedNode/
// FindByPropertyIndex) - it has no record of which of props' keys, if any, were originally passed
// as indexedKeys, so it cannot know which index entries would need to move. Callers must only
// use UpdateNode on properties that are never indexed, or accept that any existing index entry
// for this node becomes stale (pointing at a node whose live prop value no longer matches the
// indexed value). This is a real, deliberate scope boundary for this cycle, not an oversight -
// see cycle 21's own obstacle about over-indexing: some real properties (e.g. journal.go's own
// Note.status) are intended to change over time and are better handled by scanning a small,
// already-narrowed result set in application code than by an index that update would need to
// keep in sync.
func (g *Graph) UpdateNode(id int64, props map[string]any) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	n, ok, err := g.getNodeLocked(id)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: id=%d", ErrNodeNotFound, id)
	}
	n.Props = props
	return g.putNodeLocked(n)
}

// UpdateNodeIf atomically updates node id's properties only if predicate returns true for its
// CURRENT properties, the whole check-transform-write happening under g.mu so no concurrent
// AddNode/AddEdge/UpdateNode/UpdateNodeIf call can interleave - kata cycle 21's item 1, grounded
// directly in simple-bot's real DeleteNote requirement ("only flip a note's status if it's still
// open, report honestly if nothing matched" - a compare-and-swap shape, not a blind overwrite). A
// caller composing plain GetNode + UpdateNode itself would have exactly this gap: another
// goroutine's update could land between the two calls (GetNode takes no lock at all), silently
// losing an update or acting on a stale check - UpdateNodeIf closes that race by construction,
// not by caller discipline (the same reasoning that favored a structural fix over a procedural
// one in the property-index benchmark, see project_gordian-db-property-lookup-benchmark).
//
// transform receives the node's CURRENT properties (not a value the caller captured earlier
// outside the lock) and returns what they should become - deliberately a function, not a static
// map, fixing a real staleness gap an earlier version of this function had: a caller wanting to
// change only ONE field (e.g. status) while preserving the rest (label, text) would otherwise
// have to read those other fields before calling UpdateNodeIf, and a second writer's change to
// one of THOSE fields in between would get silently reverted by this call's own stale copy - even
// though the predicate check itself was correctly atomic. transform running inside the lock, over
// truly-current props, closes that gap the same way the predicate already did. A transform that
// doesn't need current values (a full, fixed replacement) can just ignore its argument.
//
// applied is false, with a nil error, if id doesn't exist or predicate returned false - a real
// node lookup/predicate miss is not itself an error condition, matching DeleteNote's own real
// "ok=false, not an error" contract.
func (g *Graph) UpdateNodeIf(id int64, predicate func(props map[string]any) bool, transform func(props map[string]any) map[string]any) (applied bool, err error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	n, ok, err := g.getNodeLocked(id)
	if err != nil {
		return false, err
	}
	if !ok || !predicate(n.Props) {
		return false, nil
	}
	n.Props = transform(n.Props)
	if err := g.putNodeLocked(n); err != nil {
		return false, err
	}
	return true, nil
}

// getNodeLocked and putNodeLocked factor out UpdateNode/UpdateNodeIf's shared read-decode and
// encode-write steps. Callers must already hold g.mu - these do not lock themselves, unlike the
// public GetNode (a pure read with no compound-operation atomicity to protect).
func (g *Graph) getNodeLocked(id int64) (Node, bool, error) {
	v, ok, err := g.store.Get(nodeKey(id))
	if err != nil {
		return Node{}, false, fmt.Errorf("read node %d: %w", id, err)
	}
	if !ok {
		return Node{}, false, nil
	}
	var n Node
	if err := json.Unmarshal(v, &n); err != nil {
		return Node{}, false, fmt.Errorf("decode node %d: %w", id, err)
	}
	return n, true, nil
}

func (g *Graph) putNodeLocked(n Node) error {
	data, err := json.Marshal(n)
	if err != nil {
		return fmt.Errorf("encode node %d: %w", n.ID, err)
	}
	if err := g.store.Put(nodeKey(n.ID), data); err != nil {
		return fmt.Errorf("put node %d: %w", n.ID, err)
	}
	return nil
}

// AddEdge creates a directed edge from -> to, labeled. Both endpoints must already exist.
// Writes both the forward (Neighbors) and reverse (InNeighbors) index entries - Store's Scan is
// forward-only and prefix-bounded, so answering "which edges point TO this node" requires its
// own denormalized index, not derivable from the forward one alone (see graph.go's package doc
// and kata cycle 17's own item 0 finding).
func (g *Graph) AddEdge(from, to int64, label string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	if _, ok, err := g.store.Get(nodeKey(from)); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("%w: from=%d", ErrNodeNotFound, from)
	}
	if _, ok, err := g.store.Get(nodeKey(to)); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("%w: to=%d", ErrNodeNotFound, to)
	}

	if err := g.store.Put(edgeOutKey(from, label, to), []byte{}); err != nil {
		return fmt.Errorf("put forward edge: %w", err)
	}
	if err := g.store.Put(edgeInKey(to, label, from), []byte{}); err != nil {
		return fmt.Errorf("put reverse edge: %w", err)
	}
	return nil
}

// RemoveEdge removes a real, latent gap found live (kata cycle 38): AddEdge had no inverse at
// all - a graph that can only ever gain edges, never correct a mistaken one, is a real
// operational gap for any real caller, not a hypothetical (found via a genuine accidental write
// against the production store during graph-ui's own real write-path verification). Removes both
// the OUT and IN presence keys AddEdge wrote; safe to call even if the edge doesn't exist
// (Store.Delete is a no-op for a missing key, same convention as DeindexNodeVector/Range/etc).
// Does not require either endpoint to still exist - unlike AddEdge's own existence check, a
// caller cleaning up after a node deletion shouldn't be blocked by the very deletion it's
// following up on.
func (g *Graph) RemoveEdge(from, to int64, label string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.removeEdgeLocked(from, to, label)
}

// removeEdgeLocked is RemoveEdge's own real body, extracted (kata cycle 43) so DeleteNode can
// call it directly while already holding g.mu - the same lock-already-held convention
// getNodeLocked already establishes elsewhere in this file. RemoveEdge itself is NOT reentrant
// (sync.Mutex isn't), so DeleteNode calling the public RemoveEdge from inside its own locked
// section would deadlock; this is the fix.
func (g *Graph) removeEdgeLocked(from, to int64, label string) error {
	if err := g.store.Delete(edgeOutKey(from, label, to)); err != nil {
		return fmt.Errorf("delete forward edge: %w", err)
	}
	if err := g.store.Delete(edgeInKey(to, label, from)); err != nil {
		return fmt.Errorf("delete reverse edge: %w", err)
	}
	return nil
}

// Neighbors returns every node reachable from `from` via an outgoing edge labeled `label`.
func (g *Graph) Neighbors(from int64, label string) ([]Node, error) {
	var ids []int64
	err := g.store.Scan(edgeOutPrefix(from, label), func(key, value []byte) bool {
		ids = append(ids, int64(binary.BigEndian.Uint64(key[len(key)-8:])))
		return true
	})
	if err != nil {
		return nil, err
	}
	return g.resolveNodes(ids)
}

// InNeighbors returns every node with an outgoing edge labeled `label` pointing TO `to`.
func (g *Graph) InNeighbors(to int64, label string) ([]Node, error) {
	var ids []int64
	err := g.store.Scan(edgeInPrefix(to, label), func(key, value []byte) bool {
		ids = append(ids, int64(binary.BigEndian.Uint64(key[len(key)-8:])))
		return true
	})
	if err != nil {
		return nil, err
	}
	return g.resolveNodes(ids)
}

func (g *Graph) resolveNodes(ids []int64) ([]Node, error) {
	nodes := make([]Node, 0, len(ids))
	for _, id := range ids {
		n, ok, err := g.GetNode(id)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, fmt.Errorf("%w: id=%d (dangling edge reference)", ErrNodeNotFound, id)
		}
		nodes = append(nodes, n)
	}
	return nodes, nil
}
