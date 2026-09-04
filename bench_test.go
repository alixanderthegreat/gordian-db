package gordian

import (
	"encoding/binary"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

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

// BenchmarkConcurrentBatchWrites mirrors BenchmarkConcurrentWrites exactly (same bucket setup,
// same key/value shape, same b.RunParallel + -cpu sweep) except it calls db.Batch() instead of
// db.Update(), so the write method is the only variable and the two benchmarks are directly
// comparable. Put(key, value) with a fresh atomic-counter key each call is naturally idempotent,
// so it's safe to pass to Batch() even though Batch may re-run a call's function.
//
// Sub-benchmarks sweep DB.MaxBatchDelay across a grounded set of values (see cycle four's item 0
// for the reasoning): 0 isolates Batch()'s own per-call machinery overhead from any deliberate
// wait, 100us/1ms bracket the Update() floor (~15-17us/op) at realistic concurrency, and 10ms is
// the default, re-run here as an in-session anchor against cycle three's numbers.
func BenchmarkConcurrentBatchWrites(b *testing.B) {
	delays := []struct {
		name  string
		delay time.Duration
	}{
		{"0", 0},
		{"100us", 100 * time.Microsecond},
		{"1ms", time.Millisecond},
		{"10ms", 10 * time.Millisecond},
	}

	for _, d := range delays {
		b.Run(d.name, func(b *testing.B) {
			dbPath := filepath.Join(b.TempDir(), "gordian-bench-batch.db")

			db, err := bolt.Open(dbPath, 0600, nil)
			if err != nil {
				b.Fatalf("open: %v", err)
			}
			defer db.Close()
			db.MaxBatchDelay = d.delay

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
					if err := db.Batch(func(tx *bolt.Tx) error {
						return tx.Bucket(bucket).Put(key, value)
					}); err != nil {
						b.Fatal(err)
					}
				}
			})
		})
	}
}
