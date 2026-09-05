// Package gordian is a bbolt/pebble-backed key-value engine. This file holds gordian-db's first
// real query-layer primitive: a Store wrapping pebble with Put/Get/Scan on raw bytes. It
// deliberately does not yet provide a schema, a query language, or a planner - see kata cycle
// eleven's own scope decision. pebble was chosen as the underlying engine over bbolt per kata
// cycles nine and ten's measured recommendation for gordian-db's real workload.
package gordian

import (
	"bytes"
	"errors"
	"log"

	"github.com/cockroachdb/pebble"
)

// Store is a minimal, durable key-value store with prefix-range scanning.
type Store struct {
	db *pebble.DB
}

// quietLogger suppresses pebble's default informational logging (e.g. "WAL file ... replayed N
// keys" on every Open) - found live migrating kata-journal (kata cycle 13) onto Store: pebble's
// DefaultLogger writes routine Infof messages straight to stderr on every single invocation,
// which is fine for a long-lived server but unacceptable noise for a short-lived CLI tool that
// opens and closes its store on every command. Errorf still surfaces real problems, and Fatalf
// still terminates the process, matching what pebble internally expects for unrecoverable
// conditions - only routine informational noise is silenced.
type quietLogger struct{}

func (quietLogger) Infof(format string, args ...interface{})  {}
func (quietLogger) Errorf(format string, args ...interface{}) { log.Printf(format, args...) }
func (quietLogger) Fatalf(format string, args ...interface{}) { log.Fatalf(format, args...) }

// Open opens (or creates) a Store at the given directory.
func Open(dir string) (*Store, error) {
	opts := &pebble.Options{Logger: quietLogger{}}
	db, err := pebble.Open(dir, opts)
	if err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

// Close closes the underlying store.
func (s *Store) Close() error {
	return s.db.Close()
}

// Put durably writes key/value, overwriting any existing value for key.
func (s *Store) Put(key, value []byte) error {
	return s.db.Set(key, value, pebble.Sync)
}

// Get reads the value for key. ok is false if key does not exist.
func (s *Store) Get(key []byte) (value []byte, ok bool, err error) {
	v, closer, err := s.db.Get(key)
	if errors.Is(err, pebble.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	value = bytes.Clone(v)
	if cerr := closer.Close(); cerr != nil {
		return nil, false, cerr
	}
	return value, true, nil
}

// Delete durably removes key. It is not an error if key does not exist.
func (s *Store) Delete(key []byte) error {
	return s.db.Delete(key, pebble.Sync)
}

// Scan calls fn for every key with the given prefix, in ascending key order, stopping early if
// fn returns false. The key and value passed to fn are only valid for the duration of that
// call - fn must copy anything it needs to retain afterward, the same convention this project's
// own bbolt/pebble benchmarks already follow for Get.
func (s *Store) Scan(prefix []byte, fn func(key, value []byte) bool) error {
	bound := prefixUpperBound(prefix)
	opts := &pebble.IterOptions{LowerBound: prefix, UpperBound: bound}
	iter, err := s.db.NewIter(opts)
	if err != nil {
		return err
	}
	defer iter.Close()

	for valid := iter.First(); valid; valid = iter.Next() {
		value, err := iter.ValueAndErr()
		if err != nil {
			return err
		}
		if !fn(iter.Key(), value) {
			break
		}
	}
	return iter.Error()
}

// prefixUpperBound returns the smallest key greater than every key starting with prefix - the
// standard exclusive upper bound for a prefix scan (increment the last byte, carrying into
// preceding bytes on 0xFF overflow). Returns nil (no upper bound - scan runs to the end of the
// keyspace) if prefix is empty or every byte is 0xFF, since no finite key excludes everything
// above such a prefix.
func prefixUpperBound(prefix []byte) []byte {
	bound := bytes.Clone(prefix)
	for i := len(bound) - 1; i >= 0; i-- {
		if bound[i] < 0xFF {
			bound[i]++
			return bound[:i+1]
		}
	}
	return nil
}
