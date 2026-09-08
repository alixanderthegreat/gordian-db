package gordian

import "testing"

func TestVectorTopK_QueryEqualsStoredVectorIsTopMatch(t *testing.T) {
	g := openTestGraph(t)
	vecs := genVectors(50)

	var targetID int64
	for i, v := range vecs {
		id, err := g.AddNodeWithVector("Fact", map[string]any{"i": i}, v)
		if err != nil {
			t.Fatalf("AddNodeWithVector %d: %v", i, err)
		}
		if i == 17 {
			targetID = id
		}
	}

	got, err := g.VectorTopK("Fact", vecs[17], 5)
	if err != nil {
		t.Fatalf("VectorTopK: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("VectorTopK len = %d, want 5", len(got))
	}
	if got[0].ID != targetID {
		t.Fatalf("VectorTopK top match = id %d, want id %d (query was that vector itself)", got[0].ID, targetID)
	}
}

// TestVectorTopK_ExcludesOtherLabels proves a vector stored under a DIFFERENT label never
// appears, even if it would otherwise score well - the real point of a label-scoped key layout,
// not just an incidental property.
func TestVectorTopK_ExcludesOtherLabels(t *testing.T) {
	g := openTestGraph(t)
	vecs := genVectors(2)

	factID, err := g.AddNodeWithVector("Fact", nil, vecs[0])
	if err != nil {
		t.Fatalf("AddNodeWithVector Fact: %v", err)
	}
	if _, err := g.AddNodeWithVector("Entity", nil, vecs[0]); err != nil {
		t.Fatalf("AddNodeWithVector Entity: %v", err)
	}

	got, err := g.VectorTopK("Fact", vecs[0], 10)
	if err != nil {
		t.Fatalf("VectorTopK: %v", err)
	}
	if len(got) != 1 || got[0].ID != factID {
		t.Fatalf("VectorTopK(Fact) = %+v, want exactly [id=%d] (Entity node must be excluded)", got, factID)
	}
}

// TestVectorTopK_MatchesNaiveFullSort cross-checks VectorTopK's min-heap bookkeeping against an
// independent, deliberately naive full-sort implementation over the same data - the same
// cross-check discipline cycle 20's own bruteForceCosineTopK test used (an implementation that
// agrees with itself isn't proof; an independent second implementation reaching the same answer
// is).
func TestVectorTopK_MatchesNaiveFullSort(t *testing.T) {
	g := openTestGraph(t)
	const n, k = 300, 10
	vecs := genVectors(n)

	ids := make([]int64, n)
	for i, v := range vecs {
		id, err := g.AddNodeWithVector("Fact", map[string]any{"i": i}, v)
		if err != nil {
			t.Fatalf("AddNodeWithVector %d: %v", i, err)
		}
		ids[i] = id
	}

	query := genVectors(1)[0]
	got, err := g.VectorTopK("Fact", query, k)
	if err != nil {
		t.Fatalf("VectorTopK: %v", err)
	}
	if len(got) != k {
		t.Fatalf("len(got) = %d, want %d", len(got), k)
	}

	type scored struct {
		id    int64
		score float64
	}
	all := make([]scored, n)
	for i, v := range vecs {
		all[i] = scored{ids[i], cosineSimilarity(query, v)}
	}
	for i := 0; i < len(all); i++ {
		for j := i + 1; j < len(all); j++ {
			if all[j].score > all[i].score {
				all[i], all[j] = all[j], all[i]
			}
		}
	}

	for i, n := range got {
		if n.ID != all[i].id {
			t.Fatalf("got[%d].ID = %d, naive full sort says id %d at rank %d", i, n.ID, all[i].id, i)
		}
	}
}

// BenchmarkVectorTopK is a quick sanity check, not a full new benchmark suite (cycle 20/25
// already answered the real performance question for this exact algorithm) - confirming the
// real, production, Node-integrated path performs in the same ballpark as their own numbers.
func BenchmarkVectorTopK(b *testing.B) {
	for _, scale := range vectorScales {
		b.Run(scale.name, func(b *testing.B) {
			g := NewGraph(openBenchStore(b))
			vecs := genVectors(scale.count)
			for _, v := range vecs {
				if _, err := g.AddNodeWithVector("Fact", nil, v); err != nil {
					b.Fatalf("AddNodeWithVector: %v", err)
				}
			}
			query := genVectors(1)[0]

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := g.VectorTopK("Fact", query, 10); err != nil {
					b.Fatalf("VectorTopK: %v", err)
				}
			}
		})
	}
}
