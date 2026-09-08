// This file is gordian-db's answer to category F in project_gordian-db-duckdb-inventory: a
// parent-scoped ORDERED RANGE query (simple-bot's real SurroundingChunks - chunk_index BETWEEN
// two bounds - and ChunkRange - chunk_index >= a bound, ascending, LIMIT - both scoped to one
// parent book/resource). graph_property.go's existing secondary index (canonicalPropValue/
// propIndexKey) cannot answer this: it encodes every value as a STRING for exact-match equality,
// and a string encoding of integers does not sort correctly as raw bytes ("10" < "9"
// lexicographically) - a genuinely different, big-endian binary encoding is needed here, the same
// technique graph.go's own nodeKey/edgeKey already use for int64 IDs, just generalized to an
// arbitrary caller-chosen "range" property instead of only node/edge IDs.
package gordian

import (
	"encoding/binary"
	"fmt"
)

const tagRangeIndex byte = 0x05

// encodeOrderedInt64 encodes v as 8 big-endian bytes that sort in the same order as v's own
// numeric value, including negative numbers - flipping the sign bit before encoding is the
// standard fix (a plain binary.BigEndian.PutUint64(uint64(v)) would sort all negative int64
// values AFTER all positive ones, since their top bit is set). Real chunk_index/book_id values
// are always non-negative, but the primitive itself should be correct for the general int64
// range, not narrowly scoped to today's real values only.
func encodeOrderedInt64(v int64) []byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, uint64(v)^0x8000000000000000)
	return buf
}

func decodeOrderedInt64(b []byte) int64 {
	return int64(binary.BigEndian.Uint64(b) ^ 0x8000000000000000)
}

// toInt64 extracts an int64 from a WRITE-path prop value (a real Go value the caller just
// constructed - int/int32/int64 - never the float64 a value round-trips to via GetNode/AllNodes'
// JSON decoding, since this is only ever called from AddRangeIndexedNode's own insert path).
func toInt64(v any) (int64, bool) {
	switch t := v.(type) {
	case int64:
		return t, true
	case int:
		return int64(t), true
	case int32:
		return int64(t), true
	default:
		return 0, false
	}
}

// rangeIndexPrefix includes rangeKey, not just (label, parentKey, parentValue) - a real scoping
// requirement, not a redundant field: without it, a scan would mix together entries for every
// range-indexed property a parent might have, not just the one the caller asked for. Today's real
// usage only ever has one (chunk_index), but the key layout is correct for the general case
// regardless, same reasoning as encodeOrderedInt64's own sign-bit handling.
func rangeIndexPrefix(label, parentKey string, parentValue int64, rangeKey string) []byte {
	key := []byte{tagRangeIndex}
	key = lengthPrefixed(key, label)
	key = lengthPrefixed(key, parentKey)
	key = append(key, encodeOrderedInt64(parentValue)...)
	key = lengthPrefixed(key, rangeKey)
	return key
}

func rangeIndexKey(label, parentKey string, parentValue int64, rangeKey string, rangeValue, nodeID int64) []byte {
	key := rangeIndexPrefix(label, parentKey, parentValue, rangeKey)
	key = append(key, encodeOrderedInt64(rangeValue)...)
	key = append(key, encodeOrderedInt64(nodeID)...)
	return key
}

// AddRangeIndexedNode behaves like AddNode, additionally writing a durable range-index entry
// keyed by (label, parentKey, parentValue, rangeKey, rangeValue) - kata cycle 24's own real
// primitive, grounded in SurroundingChunks/ChunkRange's real shape (a chunk belongs to one parent
// book, ordered by chunk_index). props[parentKey] and props[rangeKey] must both be int64-shaped
// (int, int32, or int64) - unlike AddIndexedNode's own permissive "skip unsupported types"
// behavior, a missing or wrong-typed parent/range value here is an error, not a silent skip,
// since a range-indexed node with no range entry at all would be silently unfindable via
// RangeScan with no indication why.
func (g *Graph) AddRangeIndexedNode(label string, props map[string]any, parentKey, rangeKey string) (int64, error) {
	parentValue, ok := toInt64(props[parentKey])
	if !ok {
		return 0, fmt.Errorf("gordian: AddRangeIndexedNode: props[%q] is not an int64-shaped value", parentKey)
	}
	rangeValue, ok := toInt64(props[rangeKey])
	if !ok {
		return 0, fmt.Errorf("gordian: AddRangeIndexedNode: props[%q] is not an int64-shaped value", rangeKey)
	}

	id, err := g.AddNode(label, props)
	if err != nil {
		return 0, err
	}
	if err := g.store.Put(rangeIndexKey(label, parentKey, parentValue, rangeKey, rangeValue, id), []byte{}); err != nil {
		return 0, fmt.Errorf("put range index entry: %w", err)
	}
	return id, nil
}

// RangeScan returns every node indexed under (label, parentKey, parentValue, rangeKey) whose
// range value falls within [from, to] inclusive, in ascending range-value order - the order
// falls out of the key encoding itself (encodeOrderedInt64 sorts correctly, and Store.Scan visits
// keys in ascending order), not a separate Go-side sort call. Scans every child of the given
// parent (a real, already-bounded set per real usage - see this file's own package doc) and
// filters/early-exits in Go rather than requiring Store to support true bounded range scans -
// the same "make it go" sequencing as every other primitive in this project. Callers wanting a
// LIMIT (ChunkRange's own shape) just truncate the returned slice themselves.
func (g *Graph) RangeScan(label, parentKey string, parentValue int64, rangeKey string, from, to int64) ([]Node, error) {
	var ids []int64
	err := g.store.Scan(rangeIndexPrefix(label, parentKey, parentValue, rangeKey), func(key, value []byte) bool {
		// key = rangeIndexPrefix(...) + 8 bytes rangeValue + 8 bytes nodeID
		nodeID := decodeOrderedInt64(key[len(key)-8:])
		rangeValue := decodeOrderedInt64(key[len(key)-16 : len(key)-8])
		if rangeValue < from || rangeValue > to {
			return true
		}
		ids = append(ids, nodeID)
		return true
	})
	if err != nil {
		return nil, err
	}
	return g.resolveNodes(ids)
}
