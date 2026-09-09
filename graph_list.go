// This file is gordian-db's answer to kata cycle 36's own real gap: AllNodes(label) (graph.go)
// requires knowing the label in advance - there is no way to answer "list every node, regardless
// of label" at all, a real requirement for a genuine graph-browsing/introspection tool (the
// graph-ui tool this cycle builds). Cursor-paginated rather than "return everything" (unlike
// AllNodes, which cycle 32 made cheap specifically because it's label-scoped) - a label-agnostic
// listing has no such bound and could span the whole store at real production scale.
package gordian

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
)

// ListNodes returns up to limit nodes with id > afterID, in ascending id order, using
// Store.ScanRange (cycle 36's own new primitive) rather than Store.Scan, since a cursor start
// point is an arbitrary key, not a prefix. afterID=-1 means start from the very beginning (node
// id 0 is a real, valid id - see nextIDLocked in graph.go - so -1, not 0, is the "nothing seen
// yet" sentinel). nextCursor is the last returned node's own id (feed it back as the next call's
// afterID); hasMore reports whether more nodes exist past this page, found via a real +1-peek
// (fetching limit+1 to know without a second round trip).
func (g *Graph) ListNodes(afterID int64, limit int) (nodes []Node, nextCursor int64, hasMore bool, err error) {
	lowerID := afterID + 1
	if lowerID < 0 {
		lowerID = 0
	}
	lowerBuf := make([]byte, 8)
	binary.BigEndian.PutUint64(lowerBuf, uint64(lowerID))
	lower := append([]byte{tagNode}, lowerBuf...)
	upper := prefixUpperBound([]byte{tagNode})

	var decodeErr error
	scanErr := g.store.ScanRange(lower, upper, func(key, value []byte) bool {
		if len(nodes) >= limit+1 {
			return false
		}
		var n Node
		if err := json.Unmarshal(value, &n); err != nil {
			decodeErr = fmt.Errorf("decode node at key %x: %w", key, err)
			return false
		}
		nodes = append(nodes, n)
		return true
	})
	if scanErr != nil {
		return nil, 0, false, scanErr
	}
	if decodeErr != nil {
		return nil, 0, false, decodeErr
	}

	if len(nodes) > limit {
		nodes = nodes[:limit]
		hasMore = true
	}
	if len(nodes) > 0 {
		nextCursor = nodes[len(nodes)-1].ID
	} else {
		nextCursor = afterID
	}
	return nodes, nextCursor, hasMore, nil
}

// Edge is a directed labeled edge, resolved from the real key layout at read time - gordian-db
// itself never stores an Edge struct directly (AddEdge only ever writes empty-valued presence
// keys), so this is purely a query-layer convenience for OutEdges/InEdges below.
type Edge struct {
	From  int64
	To    int64
	Label string
}

// OutEdges returns every real edge from id, across EVERY label - the label-agnostic counterpart
// to Neighbors(from, label), which requires already knowing which label to ask for. No new Store
// primitive needed (unlike ListNodes): tagEdgeOut + from's own 8-byte id, with no label suffix,
// is itself a valid PREFIX that Store.Scan already bounds correctly - the label+to bytes that
// follow for any real edge always sort strictly within that prefix's own range, confirmed in this
// cycle's own obstacle. Returns an empty slice, not an error, if from has no outgoing edges.
func (g *Graph) OutEdges(from int64) ([]Edge, error) {
	prefix := make([]byte, 1, 9)
	prefix[0] = tagEdgeOut
	fromBuf := make([]byte, 8)
	binary.BigEndian.PutUint64(fromBuf, uint64(from))
	prefix = append(prefix, fromBuf...)

	var edges []Edge
	var parseErr error
	err := g.store.Scan(prefix, func(key, value []byte) bool {
		label, to, ok := parseEdgeOutSuffix(key)
		if !ok {
			parseErr = fmt.Errorf("malformed OUT edge key %x", key)
			return false
		}
		edges = append(edges, Edge{From: from, To: to, Label: label})
		return true
	})
	if err != nil {
		return nil, err
	}
	if parseErr != nil {
		return nil, parseErr
	}
	return edges, nil
}

// InEdges returns every real edge into id, across EVERY label - the label-agnostic counterpart to
// InNeighbors(to, label). Same real technique as OutEdges, mirrored for tagEdgeIn.
func (g *Graph) InEdges(to int64) ([]Edge, error) {
	prefix := make([]byte, 1, 9)
	prefix[0] = tagEdgeIn
	toBuf := make([]byte, 8)
	binary.BigEndian.PutUint64(toBuf, uint64(to))
	prefix = append(prefix, toBuf...)

	var edges []Edge
	var parseErr error
	err := g.store.Scan(prefix, func(key, value []byte) bool {
		label, from, ok := parseEdgeOutSuffix(key)
		if !ok {
			parseErr = fmt.Errorf("malformed IN edge key %x", key)
			return false
		}
		edges = append(edges, Edge{From: from, To: to, Label: label})
		return true
	})
	if err != nil {
		return nil, err
	}
	if parseErr != nil {
		return nil, parseErr
	}
	return edges, nil
}

// EdgeCountsByLabel returns how many real edges exist per label across the WHOLE store - kata
// cycle 57's own real primitive, the "counts before payload" step a whole-graph map view needs to
// decide what is even safe to fetch (the same discipline cycle 55 established for a single
// high-degree node, applied globally).
//
// Scans the tagEdgeOut prefix ONLY, deliberately: every real edge is written to both indexes by
// AddEdge, but appears exactly ONCE under tagEdgeOut (from's own perspective), so this needs no
// deduplication - whereas a scan touching tagEdgeIn as well would double-count every edge.
// Aggregate-only, so the result stays tiny (one int per real label) no matter how large the store
// is, which is what makes it safe to call unconditionally.
func (g *Graph) EdgeCountsByLabel() (map[string]int, error) {
	counts := map[string]int{}
	var parseErr error
	err := g.store.Scan([]byte{tagEdgeOut}, func(key, value []byte) bool {
		label, _, ok := parseEdgeOutSuffix(key)
		if !ok {
			parseErr = fmt.Errorf("malformed OUT edge key %x", key)
			return false
		}
		counts[label]++
		return true
	})
	if err != nil {
		return nil, err
	}
	if parseErr != nil {
		return nil, parseErr
	}
	return counts, nil
}

// EdgesByLabel returns up to limit real edges carrying label, from anywhere in the store, and
// reports honestly whether it stopped early (truncated) rather than silently returning a partial
// answer that looks complete - kata cycle 57.
//
// A full tagEdgeOut scan with in-loop filtering is genuinely the only option here, and that is
// worth stating plainly rather than papering over: the real key layout is
// tag + from + labelLen + label + to, so label sits AFTER from and cannot be prefix-sought. Making
// label-scoped edge lookup cheap would require a real new index (a third edge keyspace keyed
// label-first), which is a deliberate non-goal for a browsing/introspection surface that is
// invoked explicitly by a human, not on a hot path. limit <= 0 means no cap.
func (g *Graph) EdgesByLabel(label string, limit int) (edges []Edge, truncated bool, err error) {
	var parseErr error
	scanErr := g.store.Scan([]byte{tagEdgeOut}, func(key, value []byte) bool {
		gotLabel, to, ok := parseEdgeOutSuffix(key)
		if !ok {
			parseErr = fmt.Errorf("malformed OUT edge key %x", key)
			return false
		}
		if gotLabel != label {
			return true
		}
		if limit > 0 && len(edges) >= limit {
			truncated = true
			return false
		}
		from := int64(binary.BigEndian.Uint64(key[1:9]))
		edges = append(edges, Edge{From: from, To: to, Label: gotLabel})
		return true
	})
	if scanErr != nil {
		return nil, false, scanErr
	}
	if parseErr != nil {
		return nil, false, parseErr
	}
	return edges, truncated, nil
}

// parseEdgeOutSuffix parses the (label, otherID) suffix shared by both edgeOutKey and edgeInKey's
// own real layout: 1 tag byte + 8 id bytes + 2 length-prefix bytes + label bytes + 8 id bytes.
// Named for the OUT case (from's own perspective); InEdges reuses it identically since tagEdgeIn
// keys share the exact same suffix shape (only the tag byte and which id is "self" vs "other"
// differ, and the caller already knows which is which).
func parseEdgeOutSuffix(key []byte) (label string, otherID int64, ok bool) {
	if len(key) < 11 {
		return "", 0, false
	}
	labelLen := int(binary.BigEndian.Uint16(key[9:11]))
	if len(key) < 11+labelLen+8 {
		return "", 0, false
	}
	label = string(key[11 : 11+labelLen])
	otherID = int64(binary.BigEndian.Uint64(key[11+labelLen : 11+labelLen+8]))
	return label, otherID, true
}
