package gordian

import (
	"bytes"
	"math"
	"testing"
)

// seedChunk adds a Chunk node with a given book_id/chunk_index, mirroring simple-bot's real
// book_chunks shape (kata cycle 24, category F).
func seedChunk(t *testing.T, g *Graph, bookID int64, chunkIndex int64, text string) int64 {
	t.Helper()
	id, err := g.AddRangeIndexedNode("Chunk", map[string]any{
		"book_id": bookID, "chunk_index": chunkIndex, "text": text,
	}, "book_id", "chunk_index")
	if err != nil {
		t.Fatalf("AddRangeIndexedNode(book_id=%d, chunk_index=%d): %v", bookID, chunkIndex, err)
	}
	return id
}

// TestRangeScan_BoundedRange mirrors SurroundingChunks' real shape exactly: chunk_index BETWEEN
// two bounds, scoped to one book, in ascending order.
func TestRangeScan_BoundedRange(t *testing.T) {
	g := openTestGraph(t)
	var ids [10]int64
	for i := int64(0); i < 10; i++ {
		ids[i] = seedChunk(t, g, 1, i, "")
	}

	got, err := g.RangeScan("Chunk", "book_id", 1, "chunk_index", 3, 6)
	if err != nil {
		t.Fatalf("RangeScan: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("RangeScan(3,6) = %d nodes, want 4", len(got))
	}
	for i, n := range got {
		wantID := ids[3+i]
		if n.ID != wantID {
			t.Fatalf("RangeScan(3,6)[%d].ID = %d, want %d (ascending order)", i, n.ID, wantID)
		}
	}
}

// TestRangeScan_OpenEndedWithCallerTruncation mirrors ChunkRange's real shape: chunk_index >= a
// bound, ascending, with the caller applying its own LIMIT by truncating the result.
func TestRangeScan_OpenEndedWithCallerTruncation(t *testing.T) {
	g := openTestGraph(t)
	var ids [10]int64
	for i := int64(0); i < 10; i++ {
		ids[i] = seedChunk(t, g, 1, i, "")
	}

	got, err := g.RangeScan("Chunk", "book_id", 1, "chunk_index", 5, math.MaxInt64)
	if err != nil {
		t.Fatalf("RangeScan: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("RangeScan(5, MaxInt64) = %d nodes, want 5 (indices 5-9)", len(got))
	}
	const limit = 2
	truncated := got[:limit]
	if len(truncated) != limit || truncated[0].ID != ids[5] || truncated[1].ID != ids[6] {
		t.Fatalf("caller-truncated result = %+v, want first %d ascending from chunk_index=5", truncated, limit)
	}
}

// TestRangeScan_ExcludesOtherParents proves a chunk belonging to a DIFFERENT book never appears -
// the same "prove exclusion, not just inclusion" discipline used throughout this project.
func TestRangeScan_ExcludesOtherParents(t *testing.T) {
	g := openTestGraph(t)
	book1Chunk := seedChunk(t, g, 1, 0, "")
	seedChunk(t, g, 2, 0, "") // same chunk_index, different book - must never appear in book 1's scan

	got, err := g.RangeScan("Chunk", "book_id", 1, "chunk_index", 0, 100)
	if err != nil {
		t.Fatalf("RangeScan: %v", err)
	}
	if len(got) != 1 || got[0].ID != book1Chunk {
		t.Fatalf("RangeScan(book_id=1) = %+v, want exactly [book1Chunk=%d]", got, book1Chunk)
	}
}

// TestEncodeOrderedInt64_SortsCorrectly proves the sign-bit-flip encoding actually produces the
// real property this whole primitive depends on: byte-comparison order matches numeric order,
// including across the negative/positive boundary - not just asserted by inspection.
func TestEncodeOrderedInt64_SortsCorrectly(t *testing.T) {
	values := []int64{math.MinInt64, -1000, -1, 0, 1, 1000, math.MaxInt64}
	for i := 0; i < len(values)-1; i++ {
		a, b := encodeOrderedInt64(values[i]), encodeOrderedInt64(values[i+1])
		if bytes.Compare(a, b) >= 0 {
			t.Fatalf("encodeOrderedInt64(%d) >= encodeOrderedInt64(%d) as bytes, want strictly less (numeric order not preserved)", values[i], values[i+1])
		}
	}
}

// TestEncodeOrderedInt64_RoundTrip proves decodeOrderedInt64 is a real inverse, not just a
// same-order encoding.
func TestEncodeOrderedInt64_RoundTrip(t *testing.T) {
	for _, v := range []int64{math.MinInt64, -1, 0, 1, math.MaxInt64} {
		got := decodeOrderedInt64(encodeOrderedInt64(v))
		if got != v {
			t.Fatalf("decodeOrderedInt64(encodeOrderedInt64(%d)) = %d, want %d", v, got, v)
		}
	}
}

// TestAddRangeIndexedNode_RequiresInt64Props proves a missing/wrong-typed parent or range prop is
// a real error, not a silent skip (unlike AddIndexedNode's own permissive behavior) - documented
// in AddRangeIndexedNode's own doc comment as a deliberate difference.
func TestAddRangeIndexedNode_RequiresInt64Props(t *testing.T) {
	g := openTestGraph(t)
	if _, err := g.AddRangeIndexedNode("Chunk", map[string]any{"book_id": "not-a-number", "chunk_index": int64(0)}, "book_id", "chunk_index"); err == nil {
		t.Fatal("AddRangeIndexedNode with a non-int64 book_id: want an error, got nil")
	}
	if _, err := g.AddRangeIndexedNode("Chunk", map[string]any{"book_id": int64(1)}, "book_id", "chunk_index"); err == nil {
		t.Fatal("AddRangeIndexedNode with a missing chunk_index: want an error, got nil")
	}
}
