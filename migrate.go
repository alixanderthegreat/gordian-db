// Package gordian: this file is a generic, reusable primitive for migrating data out of a
// bbolt file into a gordian-db Store - built for kata-journal's real migration (see kata cycle
// 14) but deliberately not tied to its specific schema, since bbolt's nested-bucket layout is a
// pattern several of this project's own reference projects (chai, goraphdb) share, not a
// one-off.
package gordian

import (
	bolt "go.etcd.io/bbolt"
)

// WalkBbolt opens the bbolt file at path READ-ONLY and calls fn once for every leaf key/value
// pair in it, recursing into nested buckets at any depth - bbolt buckets can contain further
// nested buckets alongside plain key/value pairs at the same level, so this does not assume any
// fixed depth (kata-journal's own schema happens to nest two levels deep, but that's a property
// of the data, not something this walker hard-codes).
//
// bucketPath is the sequence of bucket names from the top level down to (and including) the
// bucket directly holding key/value - e.g. a key inside bucket "threadA" inside top-level bucket
// "cycles" is reported with bucketPath = [][]byte{[]byte("cycles"), []byte("threadA")}. Each
// call gets its own freshly-allocated bucketPath slice (safe to retain across calls without
// aliasing another call's path).
//
// key and value are only valid for the duration of the call - the same convention as bbolt's
// own Cursor/ForEach and every other read path in this project. Copy anything fn needs to keep.
// WalkBbolt returns fn's first non-nil error, aborting the walk - a partial migration is
// reported as an error, not silently completed short.
func WalkBbolt(path string, fn func(bucketPath [][]byte, key, value []byte) error) error {
	db, err := bolt.Open(path, 0600, &bolt.Options{ReadOnly: true})
	if err != nil {
		return err
	}
	defer db.Close()

	return db.View(func(tx *bolt.Tx) error {
		return tx.ForEach(func(name []byte, b *bolt.Bucket) error {
			return walkBoltBucket(nil, name, b, fn)
		})
	})
}

func walkBoltBucket(parentPath [][]byte, name []byte, b *bolt.Bucket, fn func([][]byte, []byte, []byte) error) error {
	path := make([][]byte, len(parentPath), len(parentPath)+1)
	copy(path, parentPath)
	path = append(path, name)

	return b.ForEach(func(k, v []byte) error {
		if v == nil {
			// A nil value at this key means k names a nested bucket, not a leaf entry - bbolt's
			// own convention (see bbolt's Bucket.ForEach documentation).
			nested := b.Bucket(k)
			return walkBoltBucket(path, k, nested, fn)
		}
		return fn(path, k, v)
	})
}
