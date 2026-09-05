package gordian

import (
	"bytes"
	"testing"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	return s
}

func TestStorePutGet(t *testing.T) {
	s := openTestStore(t)

	key, want := []byte("key"), []byte("value")
	if err := s.Put(key, want); err != nil {
		t.Fatalf("put: %v", err)
	}

	got, ok, err := s.Get(key)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !ok {
		t.Fatalf("get: key not found")
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("roundtrip mismatch: got %q, want %q", got, want)
	}
}

func TestStoreGetMissing(t *testing.T) {
	s := openTestStore(t)

	value, ok, err := s.Get([]byte("nope"))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if ok {
		t.Fatalf("get: expected ok=false for missing key, got value %q", value)
	}
	if value != nil {
		t.Fatalf("get: expected nil value for missing key, got %q", value)
	}
}

// TestStoreScanPrefix proves Scan returns exactly the keys under a prefix, in ascending order,
// and excludes keys both lexically before and after the prefix range - the real correctness
// question cycle eleven's item 0 flagged, since pebble's iterators aren't automatically
// prefix-limited.
func TestStoreScanPrefix(t *testing.T) {
	s := openTestStore(t)

	entries := map[string]string{
		"a":   "before the prefix entirely",
		"b/1": "in prefix",
		"b/2": "in prefix",
		"b/3": "in prefix",
		"b0":  "lexically after b/ but not under it - must be excluded",
		"c":   "after the prefix entirely",
	}
	for k, v := range entries {
		if err := s.Put([]byte(k), []byte(v)); err != nil {
			t.Fatalf("put %q: %v", k, err)
		}
	}

	var gotKeys []string
	if err := s.Scan([]byte("b/"), func(key, value []byte) bool {
		gotKeys = append(gotKeys, string(key))
		if want := entries[string(key)]; string(value) != want {
			t.Errorf("scan %q: value = %q, want %q", key, value, want)
		}
		return true
	}); err != nil {
		t.Fatalf("scan: %v", err)
	}

	wantKeys := []string{"b/1", "b/2", "b/3"}
	if len(gotKeys) != len(wantKeys) {
		t.Fatalf("scan returned %v, want %v", gotKeys, wantKeys)
	}
	for i, want := range wantKeys {
		if gotKeys[i] != want {
			t.Errorf("scan[%d] = %q, want %q (order or exclusion wrong)", i, gotKeys[i], want)
		}
	}
}

// TestStoreScanStopsEarly proves returning false from fn actually stops the scan, not just
// skips one entry - a real behavioral guarantee the doc comment promises.
func TestStoreScanStopsEarly(t *testing.T) {
	s := openTestStore(t)
	for _, k := range []string{"p/1", "p/2", "p/3"} {
		if err := s.Put([]byte(k), []byte("v")); err != nil {
			t.Fatalf("put %q: %v", k, err)
		}
	}

	var seen int
	if err := s.Scan([]byte("p/"), func(key, value []byte) bool {
		seen++
		return false
	}); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if seen != 1 {
		t.Fatalf("scan visited %d entries after fn returned false, want 1", seen)
	}
}

func TestPrefixUpperBound(t *testing.T) {
	cases := []struct {
		name   string
		prefix []byte
		want   []byte
	}{
		{"normal", []byte("ab"), []byte("ac")},
		{"carry once", []byte{0x01, 0xFF}, []byte{0x02}},
		{"carry twice", []byte{0x01, 0xFF, 0xFF}, []byte{0x02}},
		{"all 0xFF", []byte{0xFF, 0xFF}, nil},
		{"single 0xFF", []byte{0xFF}, nil},
		{"empty", []byte{}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := prefixUpperBound(c.prefix)
			if !bytes.Equal(got, c.want) {
				t.Errorf("prefixUpperBound(%v) = %v, want %v", c.prefix, got, c.want)
			}
		})
	}
}
