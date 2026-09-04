package gordian

import (
	"encoding/binary"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cockroachdb/pebble"
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

// BenchmarkConcurrentShardedWrites measures aggregate write throughput across shardCount
// independent bbolt files, key hashed to a shard by simple modulo (mirroring goraphdb's
// DefaultShardKey - see cycle five's item 0). shardCount is grounded on the highest -cpu level
// this project's benchmarks test (16), the most favorable-but-still-principled case: at cpu=16,
// perfect distribution gives every concurrent writer its own shard and thus its own independent
// single-writer lock. This buys throughput by giving up atomic transactions across shards - a
// real cost the raw ns/op number below does not capture (see cycle five's obstacle 1).
func BenchmarkConcurrentShardedWrites(b *testing.B) {
	const shardCount = 16
	dir := b.TempDir()
	bucket := []byte("bench")

	shards := make([]*bolt.DB, shardCount)
	for i := range shards {
		db, err := bolt.Open(filepath.Join(dir, fmt.Sprintf("shard-%d.db", i)), 0600, nil)
		if err != nil {
			b.Fatalf("open shard %d: %v", i, err)
		}
		defer db.Close()
		if err := db.Update(func(tx *bolt.Tx) error {
			_, err := tx.CreateBucketIfNotExists(bucket)
			return err
		}); err != nil {
			b.Fatalf("create bucket shard %d: %v", i, err)
		}
		shards[i] = db
	}

	value := make([]byte, 32)
	var counter atomic.Uint64

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		key := make([]byte, 8)
		for pb.Next() {
			n := counter.Add(1)
			binary.BigEndian.PutUint64(key, n)
			shard := shards[n%shardCount]
			if err := shard.Update(func(tx *bolt.Tx) error {
				return tx.Bucket(bucket).Put(key, value)
			}); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkConcurrentPebbleWrites mirrors BenchmarkConcurrentWrites's shape (same key/value
// sizes, same b.RunParallel + -cpu sweep) but against a single pebble store instead of bbolt,
// so the storage engine is the only variable. pebble.Sync is passed explicitly for every write,
// matching bbolt's durable-by-default framing from cycle two - not an artificially fast
// unsynced comparison.
func BenchmarkConcurrentPebbleWrites(b *testing.B) {
	dir := filepath.Join(b.TempDir(), "gordian-bench-pebble")

	db, err := pebble.Open(dir, &pebble.Options{})
	if err != nil {
		b.Fatalf("open: %v", err)
	}
	defer db.Close()

	value := make([]byte, 32)
	var counter atomic.Uint64

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		key := make([]byte, 8)
		for pb.Next() {
			binary.BigEndian.PutUint64(key, counter.Add(1))
			if err := db.Set(key, value, pebble.Sync); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// readBenchKeyCount is the pre-populated dataset size shared by BenchmarkConcurrentReads and
// BenchmarkConcurrentPebbleReads - grounded on pebble's default 4MB MemTableSize (see cycle
// six's item 0): large enough to force several memtable flushes into L0 sstables and likely at
// least one compaction, so pebble's read path has real multi-level structure to exercise rather
// than serving everything from one warm memtable.
const readBenchKeyCount = 200_000

// BenchmarkConcurrentReads measures concurrent point-read throughput against a bbolt store
// pre-populated with readBenchKeyCount records (one bulk Update() transaction, NoSync during
// setup only - population speed isn't being measured and reads have no sync concept at all).
// The timed loop cycles through every key round-robin via a shared atomic counter, guaranteeing
// even coverage of the whole dataset rather than relying on random sampling.
func BenchmarkConcurrentReads(b *testing.B) {
	dbPath := filepath.Join(b.TempDir(), "gordian-bench-reads.db")

	db, err := bolt.Open(dbPath, 0600, nil)
	if err != nil {
		b.Fatalf("open: %v", err)
	}
	defer db.Close()

	bucket := []byte("bench")
	value := make([]byte, 32)

	db.NoSync = true
	if err := db.Update(func(tx *bolt.Tx) error {
		bk, err := tx.CreateBucketIfNotExists(bucket)
		if err != nil {
			return err
		}
		key := make([]byte, 8)
		for i := uint64(0); i < readBenchKeyCount; i++ {
			binary.BigEndian.PutUint64(key, i)
			if err := bk.Put(key, value); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		b.Fatalf("populate: %v", err)
	}
	db.NoSync = false

	var counter atomic.Uint64

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		key := make([]byte, 8)
		for pb.Next() {
			n := counter.Add(1) % readBenchKeyCount
			binary.BigEndian.PutUint64(key, n)
			if err := db.View(func(tx *bolt.Tx) error {
				if tx.Bucket(bucket).Get(key) == nil {
					return fmt.Errorf("missing key %d", n)
				}
				return nil
			}); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkConcurrentPebbleReads mirrors BenchmarkConcurrentReads exactly (same
// readBenchKeyCount, same round-robin key selection, same b.RunParallel + -cpu sweep) against a
// pebble store instead of bbolt, so the storage engine is the only variable. pebble.NoSync
// during setup only, same reasoning as the bbolt version.
func BenchmarkConcurrentPebbleReads(b *testing.B) {
	dir := filepath.Join(b.TempDir(), "gordian-bench-pebble-reads")

	db, err := pebble.Open(dir, &pebble.Options{})
	if err != nil {
		b.Fatalf("open: %v", err)
	}
	defer db.Close()

	value := make([]byte, 32)
	key := make([]byte, 8)
	for i := uint64(0); i < readBenchKeyCount; i++ {
		binary.BigEndian.PutUint64(key, i)
		if err := db.Set(key, value, pebble.NoSync); err != nil {
			b.Fatalf("populate: %v", err)
		}
	}

	var counter atomic.Uint64

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		key := make([]byte, 8)
		for pb.Next() {
			n := counter.Add(1) % readBenchKeyCount
			binary.BigEndian.PutUint64(key, n)
			v, closer, err := db.Get(key)
			if err != nil {
				b.Fatal(err)
			}
			_ = v
			if err := closer.Close(); err != nil {
				b.Fatal(err)
			}
		}
	})
}
