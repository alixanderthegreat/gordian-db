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

// ErrNodeNotFound is returned by AddEdge when either endpoint doesn't exist.
var ErrNodeNotFound = errors.New("gordian: node not found")

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
