package gordian

import (
	"math"
	"testing"
)

// TestGenVectors_ShapeAndDeterminism verifies genVectors's real contract before anything is
// benchmarked against it: right dimension, right count, and - critically for reproducible
// benchmark comparisons across runs - the exact same output for the exact same n.
func TestGenVectors_ShapeAndDeterminism(t *testing.T) {
	const n = 200
	a := genVectors(n)
	if len(a) != n {
		t.Fatalf("genVectors(%d) len = %d, want %d", n, len(a), n)
	}
	for i, v := range a {
		if len(v) != embeddingDim {
			t.Fatalf("genVectors(%d)[%d] len = %d, want %d", n, i, len(v), embeddingDim)
		}
	}

	b := genVectors(n)
	for i := range a {
		for j := range a[i] {
			if a[i][j] != b[i][j] {
				t.Fatalf("genVectors(%d) not deterministic: [%d][%d] = %v then %v", n, i, j, a[i][j], b[i][j])
			}
		}
	}
}

// TestGenVectors_DifferentScalesDiffer guards against a degenerate seeding bug (e.g. seeding on
// something constant instead of n) that TestGenVectors_ShapeAndDeterminism's same-n comparison
// alone couldn't catch.
func TestGenVectors_DifferentScalesDiffer(t *testing.T) {
	a := genVectors(100)
	b := genVectors(101)
	same := true
	for i := range a {
		for j := range a[i] {
			if a[i][j] != b[i][j] {
				same = false
			}
		}
	}
	if same {
		t.Fatal("genVectors(100) and genVectors(101) produced identical vectors - seeding is not varying with n")
	}
}

// TestVectorEncodeRoundTrip verifies encodeVector/decodeVector preserve the exact bit pattern of
// every float32, not just an approximately-equal value - real Store keys/values are compared
// byte-for-byte elsewhere in this project (e.g. propIndexKey), so silent precision loss here
// would be a real, not hypothetical, bug.
func TestVectorEncodeRoundTrip(t *testing.T) {
	want := genVectors(1)[0]
	got := decodeVector(encodeVector(want))
	if len(got) != len(want) {
		t.Fatalf("decodeVector len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("decodeVector(encodeVector(v))[%d] = %v, want %v (bit-exact)", i, got[i], want[i])
		}
	}
}

// TestVectorEncodeLength confirms the on-disk size assumption largeScaleValueSize/bench_test.go's
// own comments already rely on: a real 384-dim float32 embedding is exactly 1536 bytes, not an
// estimate.
func TestVectorEncodeLength(t *testing.T) {
	v := genVectors(1)[0]
	got := len(encodeVector(v))
	want := embeddingDim * 4
	if got != want {
		t.Fatalf("encodeVector length = %d, want %d", got, want)
	}
}
