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
		s, ok := canonicalPropValue(props[k])
		if !ok {
			continue
		}
		if err := g.store.Put(propIndexKey(label, k, s, id), []byte{}); err != nil {
			return 0, fmt.Errorf("put property index entry: %w", err)
		}
	}
	return id, nil
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
