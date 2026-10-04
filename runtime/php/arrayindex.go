package php

import "hash/maphash"

// The index from a key to its bucket, in place of a Go map.
//
// A map was the obvious first choice and three measurements say it is the wrong
// one for PHP's arrays. Building a list spent a third of its time in
// `mapassign` (`hack/bench/arrpass.php`); every lookup hashed a three-field
// struct; and a copy-on-write separation had to rebuild the map entry by entry,
// where the buckets beside it copy with one memmove.
//
// So the index is the engine's shape, in two modes:
//
//   - **Packed**: every bucket's key is its own position -- a list, the
//     commonest array there is. There is no table at all; `$a[$i]` is a bounds
//     check. Deleted buckets keep their key, so an unset in the middle leaves
//     an array packed.
//   - **Hashed**: an open-addressed table of bucket positions, linear probing,
//     sized to a power of two. Each bucket caches its key's hash, so growing
//     the table never hashes a string twice.
//
// A table slot is never removed. A slot whose bucket was deleted, or renamed by
// Rekey, stays in the probe chain and is skipped because a lookup always
// compares the bucket's actual key and its deleted flag -- the tombstone is
// free. Stale slots count against the load factor, and a rebuild drops them.

// stringSeed hashes string keys. Fixed for the process and never written after
// initialisation, so it is not the shared mutable state this package avoids.
var stringSeed = maphash.MakeSeed()

// hashKey is a key's hash. Integers are mixed rather than used as they are, so
// that keys differing only in their high bits do not all land in one probe run.
func hashKey(k Key) uint64 {
	if !k.IsInt {
		return maphash.String(stringSeed, k.Str)
	}
	h := uint64(k.Int)
	h ^= h >> 33
	h *= 0xff51afd7ed558ccd
	h ^= h >> 33
	h *= 0xc4ceb9fe1a85ec53
	h ^= h >> 33
	return h
}

// lookup answers the position of a key's live bucket.
func (b *arrayBody) lookup(key Key) (int, bool) {
	if b.packed {
		if !key.IsInt || key.Int < 0 || key.Int >= int64(len(b.vals)) {
			return 0, false
		}
		pos := int(key.Int)
		if b.deadAt(pos) {
			return 0, false
		}
		return pos, true
	}
	if len(b.table) == 0 {
		return 0, false
	}
	mask := uint64(len(b.table) - 1)
	h := hashKey(key)
	for i := h & mask; ; i = (i + 1) & mask {
		pos := b.table[i]
		if pos < 0 {
			return 0, false
		}
		if b.hashes[pos] == h && !b.deadAt(int(pos)) && b.keys[pos] == key {
			return int(pos), true
		}
	}
}

// add places a new element at the end under key. A packed array stays packed
// while the key is the next position; the first key that is not ends the
// packed mode (unpack). hash is the key's hash when the caller has it already
// (a clone of a hashed array), which spares hashing a string twice.
func (b *arrayBody) add(key Key, value Value, hash uint64, haveHash bool) {
	if b.packed && key == IntKey(int64(len(b.vals))) {
		b.vals = append(b.vals, value)
		return
	}
	b.unpack()
	b.vals = append(b.vals, value)
	b.keys = append(b.keys, key)
	if !haveHash {
		hash = hashKey(key)
	}
	b.hashes = append(b.hashes, hash)
	b.indexed(len(b.vals) - 1)
}

// unpack ends the packed mode: every position's key, spelled out, and the
// hashes and table a hashed array keeps. A no-op on a hashed array.
func (b *arrayBody) unpack() {
	if !b.packed {
		return
	}
	b.packed = false
	b.keys = make([]Key, len(b.vals), cap(b.vals))
	b.hashes = make([]uint64, len(b.vals), cap(b.vals))
	for i := range b.vals {
		b.keys[i] = IntKey(int64(i))
		b.hashes[i] = hashKey(b.keys[i])
	}
	b.rebuildTable()
}

// indexed records the element just placed at pos -- appended, or renamed in
// place -- in the index of a hashed array.
func (b *arrayBody) indexed(pos int) {
	if b.packed {
		return
	}
	if (b.tableUsed+1)*4 > len(b.table)*3 {
		b.rebuildTable()
		return
	}
	b.place(pos)
}

// place puts one bucket position into the table, which has room.
func (b *arrayBody) place(pos int) {
	mask := uint64(len(b.table) - 1)
	for i := b.hashes[pos] & mask; ; i = (i + 1) & mask {
		if b.table[i] < 0 {
			b.table[i] = int32(pos)
			b.tableUsed++
			return
		}
	}
}

// rebuildTable sizes a fresh table for the live buckets and drops the stale
// slots.
func (b *arrayBody) rebuildTable() {
	size := 8
	for size*3 < (b.live+1)*4 {
		size *= 2
	}
	b.table = make([]int32, size)
	for i := range b.table {
		b.table[i] = -1
	}
	b.tableUsed = 0
	for pos := range b.vals {
		if !b.deadAt(pos) {
			b.place(pos)
		}
	}
}

// resetIndex empties the index along with the buckets.
func (b *arrayBody) resetIndex() {
	b.packed = true
	b.keys = nil
	b.hashes = nil
	b.table = nil
	b.tableUsed = 0
}
