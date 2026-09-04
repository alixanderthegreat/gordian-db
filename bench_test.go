package gordian

import (
	"encoding/binary"
	"path/filepath"
	"sync/atomic"
	"testing"

	bolt "go.etcd.io/bbolt"
)

// BenchmarkConcurrentWrites measures how a single shared bbolt file behaves under concurrent
// goroutine write contention - real, durable Update() transactions (bbolt's default sync
// behavior, not NoSync), driven by testing.B's own parallel-benchmark primitive so the -cpu flag
// (e.g. -cpu=1,2,4,8,16) varies effective writer count across runs.
func BenchmarkConcurrentWrites(b *testing.B) {
	dbPath := filepath.Join(b.TempDir(), "gordian-bench.db")

	db, err := bolt.Open(dbPath, 0600, nil)
	if err != nil {
		b.Fatalf("open: %v", err)
	}
	defer db.Close()

	bucket := []byte("bench")
	if err := db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(bucket)
		return err
	}); err != nil {
		b.Fatalf("create bucket: %v", err)
	}

	value := make([]byte, 32)
	var counter atomic.Uint64

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		key := make([]byte, 8)
		for pb.Next() {
			binary.BigEndian.PutUint64(key, counter.Add(1))
			if err := db.Update(func(tx *bolt.Tx) error {
				return tx.Bucket(bucket).Put(key, value)
			}); err != nil {
				b.Fatal(err)
			}
		}
	})
}
