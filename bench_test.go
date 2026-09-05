package gordian

import (
	"encoding/binary"
	"fmt"
	"path/filepath"
	"runtime"
	"sync"
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

// BenchmarkMixedReadsUnderBoltWrites measures bbolt's read latency while runtime.GOMAXPROCS(0)
// background writer goroutines continuously commit durable Update()s to a SEPARATE key range
// (readBenchKeyCount and above) - not the range being read, so this isolates "does write
// pressure hurt reads" from "does writing to the same hot keys behave differently" (see cycle
// eight's obstacle 3). Writer count is tied to GOMAXPROCS/-cpu, same principle as every reader/
// writer count elsewhere in this file, not an arbitrary fixed number (obstacle 2). Compare this
// function's ns/op directly against BenchmarkConcurrentReads (cycle six's pure-read baseline)
// to see the actual degradation, if any, caused by concurrent writes.
func BenchmarkMixedReadsUnderBoltWrites(b *testing.B) {
	dbPath := filepath.Join(b.TempDir(), "gordian-bench-mixed-bolt.db")

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

	stop := make(chan struct{})
	var writerWg sync.WaitGroup
	var writeCount, writeErrs atomic.Uint64
	numWriters := runtime.GOMAXPROCS(0)
	writerWg.Add(numWriters)
	for i := 0; i < numWriters; i++ {
		go func() {
			defer writerWg.Done()
			key := make([]byte, 8)
			for {
				select {
				case <-stop:
					return
				default:
				}
				binary.BigEndian.PutUint64(key, readBenchKeyCount+writeCount.Add(1))
				if err := db.Update(func(tx *bolt.Tx) error {
					return tx.Bucket(bucket).Put(key, value)
				}); err != nil {
					writeErrs.Add(1)
				}
			}
		}()
	}

	var readCounter atomic.Uint64

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		key := make([]byte, 8)
		for pb.Next() {
			n := readCounter.Add(1) % readBenchKeyCount
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
	b.StopTimer()

	close(stop)
	writerWg.Wait()
	b.Logf("background writes during mixed load: %d completed, %d errors", writeCount.Load(), writeErrs.Load())
	if errs := writeErrs.Load(); errs > 0 {
		b.Errorf("%d background write errors during mixed load", errs)
	}
}

// BenchmarkMixedReadsUnderPebbleWrites mirrors BenchmarkMixedReadsUnderBoltWrites exactly
// (readBenchKeyCount, separate writer key range, GOMAXPROCS(0)-driven background writers,
// pebble.Sync for realistic durable write pressure matching cycle five's methodology) against
// pebble instead of bbolt. Logs pebble.Metrics().Flush.Count and .Compact.Count after the run
// to confirm real flush/compaction activity actually happened during the timed window (per
// item 0) - a benchmark that never triggers either wouldn't actually be testing what this cycle
// claims to test.
func BenchmarkMixedReadsUnderPebbleWrites(b *testing.B) {
	dir := filepath.Join(b.TempDir(), "gordian-bench-mixed-pebble")

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

	stop := make(chan struct{})
	var writerWg sync.WaitGroup
	var writeCount, writeErrs atomic.Uint64
	numWriters := runtime.GOMAXPROCS(0)
	writerWg.Add(numWriters)
	for i := 0; i < numWriters; i++ {
		go func() {
			defer writerWg.Done()
			key := make([]byte, 8)
			for {
				select {
				case <-stop:
					return
				default:
				}
				binary.BigEndian.PutUint64(key, readBenchKeyCount+writeCount.Add(1))
				if err := db.Set(key, value, pebble.Sync); err != nil {
					writeErrs.Add(1)
				}
			}
		}()
	}

	var readCounter atomic.Uint64

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		key := make([]byte, 8)
		for pb.Next() {
			n := readCounter.Add(1) % readBenchKeyCount
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
	b.StopTimer()

	close(stop)
	writerWg.Wait()
	m := db.Metrics()
	b.Logf("background writes during mixed load: %d completed, %d errors; pebble flushes=%d compactions=%d",
		writeCount.Load(), writeErrs.Load(), m.Flush.Count, m.Compact.Count)
	if errs := writeErrs.Load(); errs > 0 {
		b.Errorf("%d background write errors during mixed load", errs)
	}
}

// largeScaleKeyCount and largeScaleValueSize ground cycle ten's real-scale benchmarks: 2048
// bytes approximates simple-bot's real book_chunks record (float[384] embedding = 1536 bytes,
// plus text overhead) rather than every prior benchmark's unrealistic 32-byte placeholder.
// 1,000,000 keys x 2048 bytes ~= 2GB per database - ~270x more data than readBenchKeyCount's
// ~7.6MB, a deliberate practical compromise (see cycle ten's item 0), NOT equivalent to
// simple-bot's real ~71GB scale (still ~34,000x smaller) - large enough to make B+tree depth
// and LSM level count real factors, small enough to bulk-populate in seconds, confirmed by a
// throwaway timing check before this benchmark was written: bolt ~11.3s, pebble ~1.1s per
// population at this scale (bbolt's B+tree insertion cost, even NoSync/single-transaction, is
// real and ~10x pebble's - not just an fsync-driven gap, a genuinely new finding at this record
// size, larger values mean fewer records fit per B+tree page, more splits).
const largeScaleKeyCount = 1_000_000
const largeScaleValueSize = 2048

// BenchmarkLargeScaleReads mirrors BenchmarkConcurrentReads exactly (round-robin key selection,
// same -cpu sweep) but against largeScaleKeyCount records of largeScaleValueSize each instead of
// readBenchKeyCount's small placeholder dataset - checking whether bbolt's read advantage (cycle
// six) holds once real B+tree depth and page-cache pressure are present, not just at 8MB scale.
func BenchmarkLargeScaleReads(b *testing.B) {
	dbPath := filepath.Join(b.TempDir(), "gordian-bench-largescale-reads.db")

	db, err := bolt.Open(dbPath, 0600, nil)
	if err != nil {
		b.Fatalf("open: %v", err)
	}
	defer db.Close()

	bucket := []byte("bench")
	value := make([]byte, largeScaleValueSize)

	db.NoSync = true
	if err := db.Update(func(tx *bolt.Tx) error {
		bk, err := tx.CreateBucketIfNotExists(bucket)
		if err != nil {
			return err
		}
		key := make([]byte, 8)
		for i := uint64(0); i < largeScaleKeyCount; i++ {
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
			n := counter.Add(1) % largeScaleKeyCount
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

// BenchmarkLargeScalePebbleReads mirrors BenchmarkLargeScaleReads exactly against pebble instead
// of bbolt, so the storage engine is the only variable - checking whether pebble's read
// disadvantage (cycle six) widens or narrows once real LSM level count is present at scale.
func BenchmarkLargeScalePebbleReads(b *testing.B) {
	dir := filepath.Join(b.TempDir(), "gordian-bench-largescale-pebble-reads")

	db, err := pebble.Open(dir, &pebble.Options{})
	if err != nil {
		b.Fatalf("open: %v", err)
	}
	defer db.Close()

	value := make([]byte, largeScaleValueSize)
	key := make([]byte, 8)
	for i := uint64(0); i < largeScaleKeyCount; i++ {
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
			n := counter.Add(1) % largeScaleKeyCount
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

// BenchmarkLargeScaleWrites measures INCREMENTAL concurrent write throughput on top of an
// already-populated largeScaleKeyCount-record bbolt store (new keys, starting at
// largeScaleKeyCount, so they don't collide with the pre-populated range) - checking whether
// pebble's write advantage (cycle five) holds once the underlying store already has real size,
// not just when writing into a store that starts empty and only reaches ~8MB during the
// benchmark itself, as every prior write benchmark did.
func BenchmarkLargeScaleWrites(b *testing.B) {
	dbPath := filepath.Join(b.TempDir(), "gordian-bench-largescale-writes.db")

	db, err := bolt.Open(dbPath, 0600, nil)
	if err != nil {
		b.Fatalf("open: %v", err)
	}
	defer db.Close()

	bucket := []byte("bench")
	value := make([]byte, largeScaleValueSize)

	db.NoSync = true
	if err := db.Update(func(tx *bolt.Tx) error {
		bk, err := tx.CreateBucketIfNotExists(bucket)
		if err != nil {
			return err
		}
		key := make([]byte, 8)
		for i := uint64(0); i < largeScaleKeyCount; i++ {
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
			binary.BigEndian.PutUint64(key, largeScaleKeyCount+counter.Add(1))
			if err := db.Update(func(tx *bolt.Tx) error {
				return tx.Bucket(bucket).Put(key, value)
			}); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkLargeScalePebbleWrites mirrors BenchmarkLargeScaleWrites exactly against pebble
// instead of bbolt (pebble.Sync for durable writes, matching cycle five's methodology).
func BenchmarkLargeScalePebbleWrites(b *testing.B) {
	dir := filepath.Join(b.TempDir(), "gordian-bench-largescale-pebble-writes")

	db, err := pebble.Open(dir, &pebble.Options{})
	if err != nil {
		b.Fatalf("open: %v", err)
	}
	defer db.Close()

	value := make([]byte, largeScaleValueSize)
	key := make([]byte, 8)
	for i := uint64(0); i < largeScaleKeyCount; i++ {
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
			binary.BigEndian.PutUint64(key, largeScaleKeyCount+counter.Add(1))
			if err := db.Set(key, value, pebble.Sync); err != nil {
				b.Fatal(err)
			}
		}
	})
}
