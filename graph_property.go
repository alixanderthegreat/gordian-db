// This file is gordian-db's answer to the real gap flagged in graph.go: Graph originally only
// supported lookup-by-id (GetNode), but simple-bot's real goraphdb usage needs lookup-by-property
// (e.g. "the Entity node named alice"), via in-memory entityNodeID/factNodeID caches it builds
// itself. Kata cycle 17 benchmarked this Store-backed index against two other candidates - a
// naive full scan and an in-memory cache mirroring goraphdb's own pattern - at simple-bot's real
// scale. The cache measured faster per lookup, but the user accepted the recommendation
// (2026-09-06) to ship this index instead: its correctness is structural (no caller-maintained
// invariant), where the cache's is procedural - the same failure shape as two real incidents
// already hit in this project (see project_gordian-db-kata-journal-adoption memory, cycles 13
// and 16). The rejected candidates' code was removed once the decision was recorded - see
// project_gordian-db-property-lookup-benchmark memory for the full numbers and reasoning.
package gordian

import (
	"encoding/binary"
	"fmt"
	"sort"
	"strconv"
)

const tagPropIndex byte = 0x04

// canonicalPropValue converts a property value into the exact string used as its index key
// component, so AddIndexedNode (write) and FindByPropertyIndex (lookup) always agree on
// encoding regardless of which concrete integer type the caller passes. Needed for real usage
// beyond string properties (e.g. Entity.name): simple-bot's real Fact.fact_id is an int64 (see
// kata cycle 18's own grounding in journal.go). ok is false for any type not explicitly
// supported here - callers must not silently index nothing when they meant to.
func canonicalPropValue(v any) (s string, ok bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case int:
		return strconv.FormatInt(int64(t), 10), true
	case int32:
		return strconv.FormatInt(int64(t), 10), true
	case int64:
		return strconv.FormatInt(t, 10), true
	default:
		return "", false
	}
}

func propIndexPrefix(label, propKey, propValue string) []byte {
	key := []byte{tagPropIndex}
	key = lengthPrefixed(key, label)
	key = lengthPrefixed(key, propKey)
	key = lengthPrefixed(key, propValue)
	return key
}

func propIndexKey(label, propKey, propValue string, id int64) []byte {
	key := propIndexPrefix(label, propKey, propValue)
	idBuf := make([]byte, 8)
	binary.BigEndian.PutUint64(idBuf, uint64(id))
	return append(key, idBuf...)
}

// IndexNode writes one durable secondary-index entry for an EXISTING node id, so it can later be
// found via FindByPropertyIndex(label, propKey, propValue). Silently does nothing if propValue's
// type isn't supported by canonicalPropValue (string or an integer kind) - matching AddNode's own
// permissiveness about property shapes, not an error. Extracted (kata cycle 28) from
// AddIndexedNode's own loop body so a node needing MULTIPLE kinds of secondary structure (an
// exact-match index AND a filtered vector, say - entries' own real shape) can compose them
// explicitly instead of needing a dedicated AddXWithY method for every combination.
func (g *Graph) IndexNode(label, propKey string, propValue any, id int64) error {
	s, ok := canonicalPropValue(propValue)
	if !ok {
		return nil
	}
	if err := g.store.Put(propIndexKey(label, propKey, s, id), []byte{}); err != nil {
		return fmt.Errorf("put property index entry: %w", err)
	}
	return nil
}

// AddIndexedNode behaves exactly like AddNode, additionally writing a durable secondary-index
// entry for each of indexedKeys whose value in props has a supported type (see
// canonicalPropValue) - currently string and integer kinds. An indexed key with an unsupported
// value type (e.g. a bool or float64) is silently skipped, not an error, matching AddNode's own
// permissiveness about property shapes.
func (g *Graph) AddIndexedNode(label string, props map[string]any, indexedKeys ...string) (int64, error) {
	id, err := g.AddNode(label, props)
	if err != nil {
		return 0, err
	}
	for _, k := range indexedKeys {
		if err := g.IndexNode(label, k, props[k], id); err != nil {
			return 0, err
		}
	}
	return id, nil
}

// DeleteNode permanently removes node id, along with any secondary-index entries recorded for
// indexedKeys - kata cycle 23's item 0, needed for simple-bot's real DeletePartialBook/
// DeletePartialResource (a genuine hard delete, unlike notes/books.complete's soft "mark done"
// pattern - AddNode/AddIndexedNode/UpdateNode/UpdateNodeIf had no reason to build this before).
// indexedKeys must name whatever keys were originally passed to AddIndexedNode for this node -
// DeleteNode has no other record of which index entries exist, the same caller-must-know-what-
// was-indexed limitation UpdateNode's own doc comment already established for updates. Returns
// ErrNodeNotFound if id doesn't exist (does not silently no-op).
//
// The label index (kata cycle 32) and every real edge (kata cycle 43) are the two exceptions to
// "caller must know what was indexed" - unlike property/range/vector indexes, both are fully
// discoverable with no caller cooperation at all (label from n.Label directly; edges via
// OutEdges/InEdges, kata cycle 36's own label-agnostic primitives), so DeleteNode cleans both up
// automatically. Without the label cleanup, AllNodes would return a dangling id for every deleted
// node; without the edge cleanup - the real, live bug this cycle fixes - any node that still had
// real edges left a dangling tagEdgeOut/tagEdgeIn key behind, pointing at an id that no longer
// resolves (found live: deleting nodes connected to a real Entity broke tools/graph-ui's own
// neighborhood view, which tried to build an edge referencing a node that no longer existed).
func (g *Graph) DeleteNode(id int64, indexedKeys ...string) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	n, ok, err := g.getNodeLocked(id)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: id=%d", ErrNodeNotFound, id)
	}
	for _, k := range indexedKeys {
		s, ok := canonicalPropValue(n.Props[k])
		if !ok {
			continue
		}
		if err := g.store.Delete(propIndexKey(n.Label, k, s, id)); err != nil {
			return fmt.Errorf("delete property index entry: %w", err)
		}
	}
	if err := g.store.Delete(labelIndexKey(n.Label, id)); err != nil {
		return fmt.Errorf("delete label index entry: %w", err)
	}
	out, err := g.OutEdges(id)
	if err != nil {
		return fmt.Errorf("list outgoing edges: %w", err)
	}
	for _, e := range out {
		if err := g.removeEdgeLocked(e.From, e.To, e.Label); err != nil {
			return fmt.Errorf("remove outgoing edge: %w", err)
		}
	}
	in, err := g.InEdges(id)
	if err != nil {
		return fmt.Errorf("list incoming edges: %w", err)
	}
	for _, e := range in {
		if err := g.removeEdgeLocked(e.From, e.To, e.Label); err != nil {
			return fmt.Errorf("remove incoming edge: %w", err)
		}
	}
	if err := g.store.Delete(nodeKey(id)); err != nil {
		return fmt.Errorf("delete node %d: %w", id, err)
	}
	return nil
}

// FindByPropertyIndex looks up every node id previously indexed under (label, propKey,
// propValue) via AddIndexedNode, and resolves them to full Nodes. propValue must be a type
// canonicalPropValue supports (string or an integer kind) and must match the type given to
// AddIndexedNode in spirit, not necessarily in concrete Go type - int(7), int32(7), and int64(7)
// all canonicalize identically and are interchangeable here.
func (g *Graph) FindByPropertyIndex(label, propKey string, propValue any) ([]Node, error) {
	s, ok := canonicalPropValue(propValue)
	if !ok {
		return nil, fmt.Errorf("gordian: unsupported property value type %T for FindByPropertyIndex", propValue)
	}
	var ids []int64
	err := g.store.Scan(propIndexPrefix(label, propKey, s), func(key, value []byte) bool {
		ids = append(ids, int64(binary.BigEndian.Uint64(key[len(key)-8:])))
		return true
	})
	if err != nil {
		return nil, err
	}
	return g.resolveNodes(ids)
}

// FindByPropertyIndexFiltered finds every node matching (label, propKey, propValue) via
// FindByPropertyIndex, then filters the result by an additional predicate over CURRENT props and
// sorts the survivors via less - kata cycle 21's item 3, grounded in simple-bot's real OpenNotes
// shape ("room AND status='open' ORDER BY created_at ASC").
//
// Real course correction from how this cycle's own target condition originally framed the
// problem ("two indexed properties both matching"): once item 0 established that UpdateNode/
// UpdateNodeIf don't maintain secondary-index entries, indexing a property expected to CHANGE
// (like status) would go stale the moment it's updated - the exact staleness risk item 0's own
// doc comment already flagged. So this filters an already index-narrowed (room-scoped) result set
// in application code instead of adding a second index on status - the same real pattern
// FindBook/SearchNotes already use for their own full-table scans (see cycle 21's obstacle about
// over-indexing), just applied to a smaller, pre-filtered subset rather than the whole table.
// filter and less may be nil (no filtering / insertion order from the underlying index scan,
// respectively) for callers that don't need them.
func (g *Graph) FindByPropertyIndexFiltered(label, propKey string, propValue any, filter func(props map[string]any) bool, less func(a, b Node) bool) ([]Node, error) {
	nodes, err := g.FindByPropertyIndex(label, propKey, propValue)
	if err != nil {
		return nil, err
	}
	if filter != nil {
		kept := nodes[:0]
		for _, n := range nodes {
			if filter(n.Props) {
				kept = append(kept, n)
			}
		}
		nodes = kept
	}
	if less != nil {
		sort.SliceStable(nodes, func(i, j int) bool { return less(nodes[i], nodes[j]) })
	}
	return nodes, nil
}

// UpdateAllIndexed finds every node matching (label, propKey, propValue) via FindByPropertyIndex,
// then applies UpdateNodeIf(predicate, transform) to each independently - kata cycle 21's item 2,
// grounded in simple-bot's real ClearNotes shape ("every open note in a room flips to done in one
// call"). Returns the number of nodes actually changed - predicate was true for that node at its
// own individual atomic check - not the number matched by the index, the same "report what really
// happened" honesty as UpdateNodeIf's own applied bool.
//
// This is a composition of N independently-atomic per-node updates, NOT one whole-batch
// transaction: the index scan and each node's update are separate locked sections, so a node
// that enters or leaves the matching set between the scan and its own update is handled
// according to its own state at that moment, not a single consistent snapshot across the whole
// batch. Simple-bot's real ClearNotes usage has no cross-note atomicity requirement (each note's
// flip is independent, and it's a low-frequency, room-scoped user action) - holding the graph's
// lock across the entire batch would trade real concurrency (blocking every other graph
// operation for the whole scan+update duration) for a guarantee this use case doesn't need.
func (g *Graph) UpdateAllIndexed(label, propKey string, propValue any, predicate func(props map[string]any) bool, transform func(current map[string]any) map[string]any) (changed int, err error) {
	nodes, err := g.FindByPropertyIndex(label, propKey, propValue)
	if err != nil {
		return 0, err
	}
	for _, n := range nodes {
		applied, err := g.UpdateNodeIf(n.ID, predicate, transform)
		if err != nil {
			return changed, err
		}
		if applied {
			changed++
		}
	}
	return changed, nil
}
