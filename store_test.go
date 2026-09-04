package gordian

import (
	"bytes"
	"path/filepath"
	"testing"

	bolt "go.etcd.io/bbolt"
)

// TestBoltRoundTrip proves the project's storage foundation is real: a value written in one
// bbolt write transaction is durably readable back in a separate read transaction.
func TestBoltRoundTrip(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gordian.db")

	db, err := bolt.Open(dbPath, 0600, nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	bucket := []byte("bucket")
	key := []byte("key")
	want := []byte("value")

	err = db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(bucket)
		if err != nil {
			return err
		}
		return b.Put(key, want)
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	var got []byte
	err = db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucket)
		v := b.Get(key)
		got = bytes.Clone(v) // Get's return is only valid for the transaction's lifetime
		return nil
	})
	if err != nil {
		t.Fatalf("view: %v", err)
	}

	if !bytes.Equal(got, want) {
		t.Fatalf("round trip mismatch: got %q, want %q", got, want)
	}
}
