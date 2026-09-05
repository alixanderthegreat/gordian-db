package gordian

import (
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	bolt "go.etcd.io/bbolt"
)

// buildBboltFixture creates a small bbolt file exercising nested buckets at varying depths -
// NOT kata-journal's specific 2-level shape, per cycle fourteen's obstacle 1: WalkBbolt must not
// only be proven against the one real schema it was built to migrate.
//
// Shape:
//
//	a/
//	  direct = "d"          (a plain key directly in a top-level bucket)
//	  nested1/
//	    x = "1"
//	    y = "2"
//	b/
//	  k = "v"                (a plain key alongside a nested bucket in the same bucket)
//	  level1/
//	    level2/
//	      deep = "z"         (three levels deep, proving arbitrary depth, not just two)
func buildBboltFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.db")
	db, err := bolt.Open(path, 0600, nil)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer db.Close()

	err = db.Update(func(tx *bolt.Tx) error {
		a, err := tx.CreateBucket([]byte("a"))
		if err != nil {
			return err
		}
		if err := a.Put([]byte("direct"), []byte("d")); err != nil {
			return err
		}
		nested1, err := a.CreateBucket([]byte("nested1"))
		if err != nil {
			return err
		}
		if err := nested1.Put([]byte("x"), []byte("1")); err != nil {
			return err
		}
		if err := nested1.Put([]byte("y"), []byte("2")); err != nil {
			return err
		}

		b, err := tx.CreateBucket([]byte("b"))
		if err != nil {
			return err
		}
		if err := b.Put([]byte("k"), []byte("v")); err != nil {
			return err
		}
		level1, err := b.CreateBucket([]byte("level1"))
		if err != nil {
			return err
		}
		level2, err := level1.CreateBucket([]byte("level2"))
		if err != nil {
			return err
		}
		return level2.Put([]byte("deep"), []byte("z"))
	})
	if err != nil {
		t.Fatalf("populate fixture: %v", err)
	}
	return path
}

type walked struct {
	path  string // joined bucketPath, for easy comparison
	key   string
	value string
}

func TestWalkBbolt(t *testing.T) {
	path := buildBboltFixture(t)

	var got []walked
	err := WalkBbolt(path, func(bucketPath [][]byte, key, value []byte) error {
		var joined string
		for i, p := range bucketPath {
			if i > 0 {
				joined += "/"
			}
			joined += string(p)
		}
		got = append(got, walked{path: joined, key: string(key), value: string(value)})
		return nil
	})
	if err != nil {
		t.Fatalf("WalkBbolt: %v", err)
	}

	want := []walked{
		{path: "a", key: "direct", value: "d"},
		{path: "a/nested1", key: "x", value: "1"},
		{path: "a/nested1", key: "y", value: "2"},
		{path: "b", key: "k", value: "v"},
		{path: "b/level1/level2", key: "deep", value: "z"},
	}

	sortWalked := func(w []walked) {
		sort.Slice(w, func(i, j int) bool {
			if w[i].path != w[j].path {
				return w[i].path < w[j].path
			}
			return w[i].key < w[j].key
		})
	}
	sortWalked(got)
	sortWalked(want)

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("WalkBbolt entries mismatch:\n got:  %+v\n want: %+v", got, want)
	}
}

// TestWalkBboltStopsOnError proves a real error from fn aborts the walk and is returned, not
// swallowed - a partial migration must be reported as a failure, not silently completed short.
func TestWalkBboltStopsOnError(t *testing.T) {
	path := buildBboltFixture(t)

	sentinel := errTestWalkAbort{}
	visited := 0
	err := WalkBbolt(path, func(bucketPath [][]byte, key, value []byte) error {
		visited++
		return sentinel
	})
	if err != sentinel {
		t.Fatalf("expected WalkBbolt to return fn's own error, got %v", err)
	}
	if visited != 1 {
		t.Fatalf("expected the walk to stop after the first entry, visited %d", visited)
	}
}

type errTestWalkAbort struct{}

func (errTestWalkAbort) Error() string { return "test walk abort" }
