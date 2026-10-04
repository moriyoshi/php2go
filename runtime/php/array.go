package php

import (
	"math"
	"strconv"
)

// Key is a PHP array key. PHP has exactly two: integers and strings, and a
// string that looks like a canonical decimal integer is silently converted, so
// $a["1"] and $a[1] are the same slot.
type Key struct {
	IsInt bool
	Int   int64
	Str   string
}

func IntKey(i int64) Key { return Key{IsInt: true, Int: i} }

// StringKey applies PHP's key coercion: "1" becomes the integer 1, but "01",
// "1.0" and " 1" stay strings.
func StringKey(s string) Key {
	if i, ok := canonicalIntString(s); ok {
		return Key{IsInt: true, Int: i}
	}
	return Key{Str: s}
}

func canonicalIntString(s string) (int64, bool) {
	if s == "" || s == "-" {
		return 0, false
	}
	start := 0
	if s[0] == '-' {
		start = 1
	}
	// "01" and "-0" are not canonical, so they remain string keys.
	if s[start] == '0' && len(s) > start+1 {
		return 0, false
	}
	if s == "-0" {
		return 0, false
	}
	for i := start; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	i, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return i, true
}

func (k Key) String() string {
	if k.IsInt {
		return strconv.FormatInt(k.Int, 10)
	}
	return k.Str
}

// Value renders a key as the value foreach would bind.
func (k Key) Value() Value {
	if k.IsInt {
		return Int(k.Int)
	}
	return String(k.Str)
}

// kindDeleted marks a deleted element's value: a tombstone the index skips.
// A kind of its own rather than a flag, because a flag cost every element
// eight bytes of padding. Never seen outside this package: every walk skips a
// dead position.
const kindDeleted Kind = 255

// deadAt reports a deleted position.
func (b *arrayBody) deadAt(i int) bool { return b.vals[i].kind == kindDeleted }

// keyAt is the key at a position: the position itself while the array is
// packed, which is why a packed array keeps no keys at all.
func (b *arrayBody) keyAt(i int) Key {
	if b.keys == nil {
		return IntKey(int64(i))
	}
	return b.keys[i]
}

// Array is PHP's ordered hash: simultaneously a list, a dictionary, and a
// record. Insertion order is preserved and is observable through foreach, so a
// Go map alone cannot represent it; buckets hold the order and an index maps
// keys to positions.
//
// Deleting leaves a tombstone rather than shifting, so positions stay stable
// while iterating. Iteration skips them.
// Array is one holder's view of a PHP array: its own internal pointer and
// foreach cursors, over a body of elements that other holders may share.
//
// **Copy-on-write, by holder** (OWNERSHIP-PERF.md P3b). `Share` gives a new
// holder its own header over the same body and marks the body shared; every
// method that changes the elements calls `own` first, which gives this header a
// body of its own if the one it has is shared. Nothing outside this file reaches
// the body, so this file is the whole of the audit: a mutator that forgets
// `own` is the only way two holders can see each other's writes.
//
// The interpreter never calls `Share` -- its assignments still `Clone` -- so
// for it every body is unshared and `own` is one branch.
type Array struct {
	*arrayBody
	// pointer is the internal array pointer current(), next() and reset()
	// move. It indexes buckets rather than counting live elements, so an
	// element unset behind it does not shift what it points at. It is the
	// holder's: `own` copies the buckets without compacting them, so it stays
	// valid across a separation.
	pointer int
	// cursors are the live foreach positions this array has to move when its
	// buckets are rebuilt. PHP registers its hash iterators with the hash for
	// the same reason.
	cursors []*int
}

// arrayBody is the elements, which headers share until one of them writes.
type arrayBody struct {
	// shares counts the headers pointing here beyond the first. Share adds
	// one; a header that separates takes its one away, and so does a header
	// the compiler proved dead (ReleaseShared). A count that is too high costs
	// a copy the next writer did not need; one that is too low lets two
	// holders see each other's writes -- so it is only ever lowered where a
	// header provably stops using this body.
	shares int
	// openWrites counts the nested writes in progress into this body's
	// elements: DimRef opened one of them for writing and has not released it.
	// A share taken meanwhile would be handed the elements the write is about
	// to change -- `$b[0][0] = $b` opens `$b[0]` and only then reads `$b` --
	// so Share takes an eager snapshot instead while it is non-zero.
	openWrites int
	// vals are the elements in insertion order, a deleted one holding the
	// kindDeleted tombstone. keys are their keys, position for position -- and
	// **nil while the array is packed**, where every key is its own position.
	// So a list costs one Value per element, as the engine's packed arrays
	// cost one zval: the two slices used to be one slice of key-value buckets,
	// 56 bytes an element, and building lists was mostly allocating and
	// copying the keys nobody read.
	vals []Value
	keys []Key
	// hashes are the keys' hashes, one per position, cached so the index can
	// grow without hashing again (arrayindex.go). Nil while packed, like keys.
	hashes []uint64
	// packed, table and tableUsed are the index from a key to its bucket;
	// see arrayindex.go.
	packed    bool
	table     []int32
	tableUsed int
	// interned records the immutable empty array carried by compiled literals.
	// Ordinary array semantics do not care; debug_zval_dump exposes it.
	interned bool
	// seenInt records that an integer key has been inserted, which is what
	// makes the first one authoritative for the append slot regardless of sign.
	seenInt bool
	// nextFree is the key an append gets. It only ever grows, so unsetting the
	// last element does not make the next append reuse its key -- matching PHP.
	nextFree int64
	// saturated records that an element occupies the largest integer key, so
	// there is no next one. An append then fails rather than wrapping.
	saturated bool
	live      int
	// walkers are the live foreach positions walking these elements, through
	// whichever header; copies are the positions of walks over an array this
	// one was copied from. Together they are the engine's hash iterators and
	// their copies (GH-11171): zend_array_dup of an array a by-reference loop
	// is walking gives the copy an iterator at the same place, so a loop whose
	// variable is handed a copy -- `$a += [...]`, `$b = $a; $a = $b + []` --
	// goes on where it was instead of starting again. See Iter.follow.
	walkers []*cursorWalker
	copies  []cursorCopy
}

// cursorWalker is one foreach position registered on an array. done marks a
// walk that has let go of it, whose copies nothing will ask for again.
type cursorWalker struct {
	pos  *int
	body *arrayBody
	done bool
}

// cursorCopy is where a walk would stand in a copy of the array it walks.
type cursorCopy struct {
	of  *cursorWalker
	pos *int
}

// inheritCursors gives c, a copy of b, the positions of the walks over b and of
// those b itself inherited, each mapped through at: the bucket index the
// position becomes in c. The live walks go first, so a walk's current position
// wins over a frozen one an earlier copy left behind (CopiedCursor takes the
// first match).
func (c *arrayBody) inheritCursors(b *arrayBody, at func(int) int) {
	for _, w := range b.walkers {
		if !w.done {
			pos := at(*w.pos)
			c.copies = append(c.copies, cursorCopy{of: w, pos: &pos})
		}
	}
	for _, cp := range b.copies {
		if !cp.of.done {
			pos := at(*cp.pos)
			c.copies = append(c.copies, cursorCopy{of: cp.of, pos: &pos})
		}
	}
}

// CopiedCursor answers where the walk whose position is cursor stands in this
// array, if this array is a copy -- however indirect -- of the one it walks.
func (a *Array) CopiedCursor(cursor *int) (int, bool) {
	if a == nil {
		return 0, false
	}
	for _, cp := range a.arrayBody.copies {
		if cp.of.pos == cursor && !cp.of.done {
			return *cp.pos, true
		}
	}
	return 0, false
}

// allCursors is every position a rebuild of this array has to move: its own
// walks, and those it holds for walks over an array it was copied from.
func (a *Array) allCursors() []*int {
	if len(a.arrayBody.copies) == 0 {
		return a.cursors
	}
	all := append([]*int(nil), a.cursors...)
	for _, cp := range a.arrayBody.copies {
		all = append(all, cp.pos)
	}
	return all
}

func NewArray() *Array {
	return &Array{arrayBody: &arrayBody{packed: true}}
}

// Share is the copy an assignment makes, deferred: a new header over the same
// elements, which neither holder can change without first taking a body of its
// own. O(1) where Clone is O(n) and allocates the whole table again.
func (a *Array) Share() *Array {
	if a == nil {
		return nil
	}
	if a.arrayBody.openWrites > 0 {
		return a.Clone()
	}
	a.arrayBody.shares++
	return &Array{arrayBody: a.arrayBody, pointer: a.pointer}
}

// Identity names the elements rather than the holder: two headers over one
// body are one array to a walk that has to notice it has come back round.
// `print_r` found a self-referencing array through `array_id`, which printed
// the header, and recursed forever once holders had headers of their own.
func (a *Array) Identity() any {
	if a == nil {
		return nil
	}
	return a.arrayBody
}

// own gives this header a body nobody else holds, before a write.
func (a *Array) own() {
	if a != nil && a.arrayBody.shares > 0 {
		old := a.arrayBody
		a.arrayBody = old.dup()
		old.shares--
		// This header's own walks go with it to the body it now writes. dup
		// also gave that body a frozen copy of each -- the position at the
		// separation -- but a later copy lists the live walk first, and
		// CopiedCursor takes the first match.
		kept := old.walkers[:0]
		for _, w := range old.walkers {
			if a.holdsCursor(w.pos) {
				w.body = a.arrayBody
				a.arrayBody.walkers = append(a.arrayBody.walkers, w)
				continue
			}
			kept = append(kept, w)
		}
		old.walkers = kept
	}
}

func (a *Array) holdsCursor(cursor *int) bool {
	for _, held := range a.cursors {
		if held == cursor {
			return true
		}
	}
	return false
}

// release hands back the share a header holds on its body, for a header that
// will not be used again. See ReleaseShared.
func (a *Array) release() {
	if a != nil && a.arrayBody != nil && a.arrayBody.shares > 0 {
		a.arrayBody.shares--
	}
}

// HeldArray is an array a **read-write fetch** is holding while user code
// runs inside it: the storage the fetch reached it through, and the elements
// that storage held then. The zero value holds nothing.
type HeldArray struct {
	slot *Value
	body *arrayBody
}

// Hold records the array slot holds now; nothing if it holds no array.
func Hold(slot *Value) HeldArray {
	if slot == nil {
		return HeldArray{}
	}
	if held := slot.Deref(); held.Kind() == KindArray && held.Array() != nil {
		return HeldArray{slot: slot, body: held.Array().arrayBody}
	}
	return HeldArray{}
}

// Lost reports that the storage no longer holds those elements: it was
// assigned something else, or it separated because it was written.
func (h HeldArray) Lost() bool {
	if h.body == nil {
		return false
	}
	held := h.slot.Deref()
	return held.Kind() != KindArray || held.Array() == nil || held.Array().arrayBody != h.body
}

// WarnUndefinedKeyHeld is the `Undefined array key` of a **read-write fetch**
// from the array in slot, which may run an error handler, and reports
// whether the fetch lost its array while the handler ran -- in which case
// it must not go on.
//
// The engine holds the array for the length of the warning (GC_ADDREF in
// zend_undefined_offset_write), so a handler that writes to it separates it
// and one that assigns the variable destroys it; either way the fetch gives
// up -- `$a["b"] += 1` whose handler sets `$a["b"] = 2` leaves 2 and
// evaluates to NULL (bug70662), and `$v[0] .= "x"` whose handler sets `$v =
// 0` leaves 0 (bug78598). The share taken here is that hold: the handler's
// write separates rather than showing through, which Lost then sees. root is
// the array a nested fetch was reached from, which the handler may have
// replaced as well.
//
// **A handler that keeps a copy** makes the engine give up too -- the
// refcount it restores is not the one it took -- but a share count here
// cannot tell a copy still held from a dead local's copy nobody released
// (arrayBody.shares is only ever too high), so that case is reported as
// unsupported rather than guessed.
func WarnUndefinedKeyHeld(ctx *Ctx, slot *Value, k Key, root HeldArray) (bool, error) {
	return holdArrayAcross(slot, root, "a read-write fetch is reading", func() {
		ctx.WarnUndefinedArrayKey(k)
	})
}

// holdArrayAcross runs warn -- a diagnostic, which may run an error handler --
// **holding** the array in slot, and reports whether the handler let go of it
// (or of root, the array a nested fetch was reached from), in which case the
// operation must give up. `what` names the operation for the two cases a
// share count cannot settle, which are reported unsupported; see
// WarnUndefinedKeyHeld. A slot holding no array is not held.
func holdArrayAcross(slot *Value, root HeldArray, what string, warn func()) (bool, error) {
	h := Hold(slot)
	shares := 0
	if h.body != nil {
		h.body.shares++
		shares = h.body.shares
	}
	warn()
	kept := h.body != nil && h.body.shares > shares
	// A handler's write separated its own header and took one away already.
	if h.body != nil && h.body.shares > 0 {
		h.body.shares--
	}
	if root.Lost() {
		return true, nil
	}
	if h.Lost() {
		// **Separated with nothing changed** is a handler that reached the
		// array for writing and wrote nothing -- which the engine counts as
		// letting go, `foreach ($a as &$v) {}` -- or a library routine that is
		// PHP here and walks by reference where the engine's does not write
		// (print_r). The two cannot be told apart from here.
		if now := slot.Deref(); now.Kind() == KindArray && StrictEqual(Arr(&Array{arrayBody: h.body}), now) {
			return false, Unsupported("an error handler reaching the array %s without changing it", what)
		}
		return true, nil
	}
	if kept {
		return false, Unsupported("an error handler copying the array %s", what)
	}
	return false, nil
}

// dup is the separation: the buckets as they stand -- deleted ones included,
// so the holder's pointer and cursors still index the same places -- with each
// element taken the way PHP's zend_array_dup takes it.
//
// **A reference held by two or more names survives as that reference**, so
// both arrays keep sharing the box, and so does one that points back at this
// array. **Any other one only this array held is flattened** into the copy's
// own value. PHP decides this when it separates, not when it
// assigns, and the difference is observable:
//
//	$a = [1]; $r = &$a[0]; $b = $a; unset($r); $a[0] = 7; echo $b[0]; // 1
//
// By the time `$a` is written, only the array holds the box, so the copy
// flattens it and `$b` keeps 1. The eager Clone this replaces decided at the
// assignment, while `$r` still held it, and printed 7.
//
// A nested array gets a header of its own over the same elements, so a write
// into it separates it in turn. Every write into a nested array descends
// through the outer one's mutators (RefAt), so the outer has always been owned
// first and its nested headers are this copy's alone.
func (b *arrayBody) dup() *arrayBody {
	c := &arrayBody{
		vals:      append([]Value(nil), b.vals...),
		keys:      append([]Key(nil), b.keys...),
		packed:    b.packed,
		table:     append([]int32(nil), b.table...),
		hashes:    append([]uint64(nil), b.hashes...),
		tableUsed: b.tableUsed,
		interned:  b.interned,
		seenInt:   b.seenInt,
		nextFree:  b.nextFree,
		saturated: b.saturated,
		live:      b.live,
	}
	if b.keys == nil {
		c.keys = nil
	}
	for i := range c.vals {
		if c.deadAt(i) {
			continue
		}
		v := c.vals[i]
		if _, isRef := v.RefOf(); isRef {
			// Kept too when it points back at the array being separated,
			// whoever holds it: zend_array_dup flattens a reference only when
			// its value is not the source. `$a[] = &$a; $b = $a; $b[0] = 1;`
			// writes through the box into both.
			if held := v.Deref(); SharedRef(v) || held.kind == KindArray && held.a() != nil && held.a().arrayBody == b {
				shareCopy(v)
				continue
			}
			v = v.Deref()
		}
		if v.kind == KindArray && v.a() != nil {
			v = Arr(v.a().Share())
		}
		c.vals[i] = v
	}
	// The buckets keep their places, so every position carries over as it is.
	c.inheritCursors(b, func(i int) int { return i })
	return c
}

// MarkInterned records that an empty array came directly from a compiled
// literal. The first content mutation turns it into an ordinary request array.
func (a *Array) MarkInterned() {
	if a != nil && a.live == 0 {
		a.own()
		a.interned = true
	}
}

// IsInterned reports the literal-empty provenance exposed by debug_zval_dump.
func (a *Array) IsInterned() bool { return a != nil && a.interned }

// Pointed returns the element the internal pointer is on, skipping any deleted
// buckets ahead of it. It reports false once the pointer has run off the end,
// which is the state current() and key() answer false and null for.
func (a *Array) Pointed() (Key, Value, bool) {
	// The out-of-range check comes first: the pointer parks at -1 once it has
	// run off the front, and the skip loop below would index that.
	if a == nil || a.pointer < 0 {
		return Key{}, Undef(), false
	}
	for a.pointer < len(a.vals) && a.deadAt(a.pointer) {
		a.pointer++
	}
	if a.pointer >= len(a.vals) {
		return Key{}, Undef(), false
	}
	return a.keyAt(a.pointer), a.vals[a.pointer].Deref(), true
}

// MovePointer steps the internal pointer by whole live elements in either
// direction and returns what it lands on.
func (a *Array) MovePointer(delta int) (Key, Value, bool) {
	if a == nil {
		return Key{}, Undef(), false
	}
	if a.pointer < 0 || a.pointer >= len(a.vals) {
		// Once the pointer has run off either end it stays there: only reset()
		// and end(), which place it absolutely, bring it back.
		return Key{}, Undef(), false
	}
	step := 1
	if delta < 0 {
		step = -1
	}
	for n := 0; n != delta; n += step {
		a.pointer += step
		for a.pointer >= 0 && a.pointer < len(a.vals) && a.deadAt(a.pointer) {
			a.pointer += step
		}
		if a.pointer < 0 {
			// Past the front is a distinct state from being on the first
			// element, and PHP does not let prev() wander back into range.
			a.pointer = -1
			return Key{}, Undef(), false
		}
		if a.pointer >= len(a.vals) {
			a.pointer = len(a.vals)
			return Key{}, Undef(), false
		}
	}
	return a.Pointed()
}

// RewindPointer puts the internal pointer on the first live element.
func (a *Array) RewindPointer() (Key, Value, bool) {
	if a == nil {
		return Key{}, Undef(), false
	}
	a.pointer = 0
	return a.Pointed()
}

// EndPointer puts the internal pointer on the last live element.
func (a *Array) EndPointer() (Key, Value, bool) {
	if a == nil {
		return Key{}, Undef(), false
	}
	a.pointer = len(a.vals) - 1
	for a.pointer >= 0 && a.deadAt(a.pointer) {
		a.pointer--
	}
	if a.pointer < 0 {
		a.pointer = -1
		return Key{}, Undef(), false
	}
	return a.Pointed()
}

// Len is the number of live elements.
// Used is the effective physical bucket span, including middle tombstones but
// excluding deleted buckets at the tail. A few engine algorithms sample that
// layout before skipping deleted entries.
func (a *Array) Used() int {
	if a == nil {
		return 0
	}
	used := len(a.vals)
	for used > 0 && a.deadAt(used-1) {
		used--
	}
	return used
}

// BucketKey reports the live key at one physical bucket position.
func (a *Array) BucketKey(position int) (Key, bool) {
	if a == nil || position < 0 || position >= len(a.vals) ||
		a.deadAt(position) {
		return Key{}, false
	}
	return a.keyAt(position), true
}

func (a *Array) Len() int {
	if a == nil {
		return 0
	}
	return a.live
}

// Get returns the value at key and whether it was present. An element bound by
// reference reads through the box, since reading in PHP is always a deref.
func (a *Array) Get(key Key) (Value, bool) {
	value, ok := a.getRaw(key)
	return value.Deref(), ok
}

// getRaw returns what the bucket holds, box included. Only the code that
// manipulates references itself wants this.
func (a *Array) getRaw(key Key) (Value, bool) {
	if a == nil {
		return Undef(), false
	}
	pos, ok := a.lookup(key)
	if !ok {
		return Undef(), false
	}
	return a.vals[pos], true
}

// Set inserts or replaces, preserving the position of an existing key. Writing
// to an element bound by reference goes through the box, so every name sharing
// it observes the change.
// setUnboxed is Set for an element already present and not a reference, in
// one lookup; it reports false, writing nothing, for anything else.
func (a *Array) setUnboxed(key Key, value Value) bool {
	if a == nil {
		return false
	}
	pos, ok := a.lookup(key)
	if !ok || a.vals[pos].IsRef() {
		return false
	}
	a.own()
	a.interned = false
	a.vals[pos] = value
	return true
}

// Rebind replaces what a key holds without writing through a reference the
// element already holds. unset() detaches the name from a box it shares rather
// than emptying the box every other sharer can see.
func (a *Array) Rebind(key Key, value Value) bool {
	if a == nil {
		return false
	}
	a.own()
	if pos, ok := a.lookup(key); ok {
		a.interned = false
		a.vals[pos] = value
		return true
	}
	return false
}

func (a *Array) Set(key Key, value Value) {
	a.own()
	a.interned = false
	if pos, ok := a.lookup(key); ok {
		if ref, isRef := a.vals[pos].RefOf(); isRef && !value.IsRef() {
			ref.Set(value)
			return
		}
		a.vals[pos] = value
		return
	}
	a.add(key, value, 0, false)
	a.live++
	if key.IsInt && !a.saturated {
		// The *first* integer key sets the append slot whatever its sign, which
		// is why `$a[-5] = 1; $a[] = 2;` gives -4 since PHP 8.3. After that the
		// slot only ever rises, so a later -9 does not pull it back down.
		if !a.seenInt || key.Int >= a.nextFree {
			if key.Int == math.MaxInt64 {
				// There is no next key after this one. PHP records that the
				// append slot is taken rather than wrapping to the negative end.
				a.saturated = true
			} else {
				a.nextFree = key.Int + 1
			}
		}
		a.seenInt = true
	}
}

// Append adds under the next free integer key, as $a[] = v does.
// CanAppend reports whether there is a next integer key to append under. PHP
// refuses rather than reusing a key or wrapping to the negative end.
func (a *Array) CanAppend() bool { return !a.saturated }

func (a *Array) Append(value Value) Key {
	key := IntKey(a.nextFree)
	// A packed list whose next key is its length: that key cannot be taken
	// (every bucket's key is its position) and the list stays packed, so the
	// append is a push -- no lookup, no index. What Set would record about
	// the key is recorded here the same way.
	if a.packed && !a.saturated && a.nextFree == int64(len(a.vals)) && a.nextFree != math.MaxInt64 {
		a.own()
		a.interned = false
		if cap(a.vals) == 0 {
			// **Eight slots on the first push**, as the engine's packed
			// arrays start: growing 1, 2, 4, 8 was three reallocations
			// before the list reached a size most loops pass.
			a.vals = make([]Value, 0, 8)
		}
		a.vals = append(a.vals, value)
		a.live++
		a.nextFree++
		a.seenInt = true
		return key
	}
	a.Set(key, value)
	return key
}

// PackedAppend is Append's push case for the emitter to test at the site:
// the list packed, unshared, its next key its length, and room in the slice
// already -- so no copy, no growth and no lookup. false appends nothing and
// the caller takes the general path, which grows the list or asks the rest.
func PackedAppend(slot *Value, v Value) bool {
	if slot.kind == KindArray {
		if a := (*Array)(slot.ptr); a.packed && a.shares == 0 && a.nextFree == int64(len(a.vals)) && len(a.vals) < cap(a.vals) {
			n := len(a.vals)
			a.vals = a.vals[:n+1]
			a.vals[n] = v
			a.live++
			a.nextFree++
			a.seenInt = true
			a.interned = false
			return true
		}
	}
	return false
}

// RefAt boxes an element in place and returns the box, so that a name bound to
// it with =& writes back into the array. The element is created if absent,
// which is what `$r = &$a["new"]` does.
func (a *Array) RefAt(key Key) *Ref {
	// Owned first: an element that is already a box would otherwise be handed
	// out of a body another holder still reads.
	a.own()
	if value, ok := a.getRaw(key); ok {
		if ref, isRef := value.RefOf(); isRef {
			return ref
		}
		ref := NewRef(value)
		a.Set(key, MakeRef(ref))
		return ref
	}
	ref := NewRef(Undef())
	a.Set(key, MakeRef(ref))
	return ref
}

// CollapseUnsharedRef removes an internal box after the operation that needed
// a writable slot has finished. It changes the slot only when it still holds
// exactly that box and no program-made reference escaped; an explicit alias
// must keep both the box and its identity.
func (a *Array) CollapseUnsharedRef(key Key, expected *Ref) {
	if a == nil || expected == nil || expected.IsShared() {
		return
	}
	a.own()
	pos, ok := a.lookup(key)
	if !ok {
		return
	}
	held, isRef := a.vals[pos].RefOf()
	if !isRef || held != expected {
		return
	}
	a.vals[pos] = expected.Get()
}

// Bind replaces what a slot holds outright, discarding any reference binding
// rather than writing through it. `$o->p = &$x` rebinds the property; `$o->p =
// $x` writes through it.
func (a *Array) Bind(key Key, value Value) {
	a.own()
	if pos, ok := a.lookup(key); ok {
		a.interned = false
		a.vals[pos] = value
		return
	}
	a.Set(key, value)
}

// Unset removes a key, reporting whether it was there.
func (a *Array) Unset(key Key) bool {
	a.own()
	pos, ok := a.lookup(key)
	if !ok {
		return false
	}
	a.interned = false
	// The index keeps its slot: a deleted bucket is a tombstone for free,
	// because a lookup checks the flag (arrayindex.go).
	a.vals[pos] = Value{kind: kindDeleted}
	a.live--
	return true
}

// Rekey renames an entry without moving it. Insertion order is observable, so
// an object that has to hold two properties of one name -- a private one and a
// dynamic one that shadows it -- cannot achieve that by unsetting and
// re-adding: the private one would move to the end of the table.
//
// It refuses when the destination is taken, and answers whether anything moved.
func (a *Array) Rekey(from, to Key) bool {
	if a == nil || from == to {
		return false
	}
	a.own()
	pos, ok := a.lookup(from)
	if !ok {
		return false
	}
	if _, exists := a.lookup(to); exists {
		return false
	}
	// A packed array has no keys to rename; it leaves the packed mode first.
	a.unpack()
	a.keys[pos] = to
	a.hashes[pos] = hashKey(to)
	a.indexed(pos)
	return true
}

// cloneAppend appends bucket i of a to a clone under construction, with its
// hash when the clone is hashed. A packed source keeps no hashes, so a clone
// that left the packed mode part-way -- the source's deleted buckets shifted
// the positions -- computes them.
func (a *Array) cloneAppend(clone *Array, i int, value Value) {
	if a.packed {
		clone.add(a.keyAt(i), value, 0, false)
		return
	}
	clone.add(a.keyAt(i), value, a.hashes[i], true)
}

// Keys returns the live keys in insertion order.
func (a *Array) Keys() []Key {
	if a == nil {
		return nil
	}
	keys := make([]Key, 0, a.live)
	for i := range a.vals {
		if !a.deadAt(i) {
			keys = append(keys, a.keyAt(i))
		}
	}
	return keys
}

// Each visits live elements in insertion order until fn returns false.
// Raw reads an element without dereferencing it, which is what a caller that
// cares whether the element *is* a reference needs -- Each and Get both hand
// back the value behind one.
// Slot is where a key's element sat when it was found: the body and the bucket
// position. A read-modify-write -- `$o->p++`, `$o->p += 2` -- finds the element
// once and comes back to it, instead of hashing the key again for the write.
//
// It stays good only while the array still holds that body and the bucket
// still holds that key, live. SlotGet and SlotSet check exactly that, so a slot
// that outlived a restructure, a deletion or a copy-on-write separation answers
// false and the caller takes its general path.
type Slot struct {
	body *arrayBody
	pos  int
}

// PosOf is the bucket position of key's element. Objects of one class are
// built with their declared properties in one order, so a position found on
// one instance names the same property on the next -- which At and SetAt
// check rather than assume.
func (a *Array) PosOf(key Key) (int, bool) {
	if a == nil {
		return 0, false
	}
	return a.lookup(key)
}

// At is the element at bucket position pos, if that bucket holds key, live.
func (a *Array) At(pos int, key Key) (Value, bool) {
	if a == nil || pos < 0 || pos >= len(a.vals) {
		return Undef(), false
	}
	if a.deadAt(pos) || a.keyAt(pos) != key {
		return Undef(), false
	}
	return a.vals[pos], true
}

// SetAt is SlotSet by position: it writes only a live bucket holding key and
// a plain value, in a body nothing else shares.
func (a *Array) SetAt(pos int, key Key, value Value) bool {
	if a == nil || pos < 0 || pos >= len(a.vals) || a.arrayBody.shares > 0 {
		return false
	}
	if a.deadAt(pos) || a.keyAt(pos) != key || a.vals[pos].IsUndef() || a.vals[pos].IsRef() {
		return false
	}
	a.interned = false
	a.vals[pos] = value
	return true
}

// Found reports a slot that was found; the zero Slot was not.
func (s Slot) Found() bool { return s.body != nil }

// SlotOf finds key's element and remembers where.
func (a *Array) SlotOf(key Key) (Slot, bool) {
	if a == nil {
		return Slot{}, false
	}
	pos, ok := a.lookup(key)
	if !ok {
		return Slot{}, false
	}
	return Slot{a.arrayBody, pos}, true
}

func (a *Array) slotHolds(s Slot, key Key) bool {
	return a != nil && s.body != nil && a.arrayBody == s.body && s.pos < len(a.vals) &&
		!a.deadAt(s.pos) && a.keyAt(s.pos) == key
}

// SlotGet is Raw through a slot: the element as the bucket holds it.
func (a *Array) SlotGet(s Slot, key Key) (Value, bool) {
	if !a.slotHolds(s, key) {
		return Undef(), false
	}
	return a.vals[s.pos], true
}

// SlotSet is Set through a slot, for the one case it can do without Set's
// questions: an element that holds a plain value -- not undef, not a
// reference to write through -- in a body nothing else shares. Anything else
// answers false and writes nothing.
func (a *Array) SlotSet(s Slot, key Key, value Value) bool {
	if !a.slotHolds(s, key) || a.arrayBody.shares > 0 {
		return false
	}
	current := a.vals[s.pos]
	if current.IsUndef() || current.IsRef() {
		return false
	}
	a.interned = false
	a.vals[s.pos] = value
	return true
}

func (a *Array) Raw(key Key) (Value, bool) {
	if a == nil {
		return Undef(), false
	}
	return a.getRaw(key)
}

func (a *Array) Each(fn func(Key, Value) bool) {
	if a == nil {
		return
	}
	for i := range a.vals {
		if a.deadAt(i) {
			continue
		}
		if !fn(a.keyAt(i), a.vals[i].Deref()) {
			return
		}
	}
}

// EachRaw walks the live elements handing over what the bucket holds, box
// included. Only code that has to decide what to do with a reference wants
// this; everything else reads through it with Each.
func (a *Array) EachRaw(fn func(Key, Value) bool) {
	if a == nil {
		return
	}
	for i := range a.vals {
		if a.deadAt(i) {
			continue
		}
		if !fn(a.keyAt(i), a.vals[i]) {
			return
		}
	}
}

// Clone makes an independent copy.
//
// PHP arrays are value types with copy-on-write. Go has no refcount to drive
// COW off, so assignment copies eagerly for now; making that lazy is an
// optimization that must not change observable behaviour.
func (a *Array) Clone() *Array {
	return a.clone(nil)
}

// clone carries the arrays already being copied, so a structure that reaches
// itself through nested arrays shares rather than recurses. PHP cannot build
// that shape without a reference -- and a reference is not copied through -- but
// unserialize can, and a runaway copy is not a failure a program can catch.
func (a *Array) clone(seen map[*arrayBody]*Array) *Array {
	if a == nil {
		return nil
	}
	if existing, ok := seen[a.arrayBody]; ok {
		return existing
	}
	clone := &Array{arrayBody: &arrayBody{
		vals:      make([]Value, 0, a.live),
		packed:    true,
		interned:  a.interned,
		nextFree:  a.nextFree,
		seenInt:   a.seenInt,
		saturated: a.saturated,
	}}
	if seen == nil {
		seen = map[*arrayBody]*Array{}
	}
	seen[a.arrayBody] = clone
	for i := range a.vals {
		if a.deadAt(i) {
			continue
		}
		value := a.vals[i]
		if _, isRef := value.RefOf(); isRef && (SharedRef(value) || heldWithin(value, seen)) {
			// A reference two or more names hold survives the copy as the same
			// slot: `$b = $a` gives $b's element the box $a's element has, so
			// writing through either is visible in both. The copy is one more
			// name holding it, which is what keeps var_dump marking it after
			// the name that made it has gone.
			//
			// A reference *into* the structure being copied counts as held
			// however the share counter reads: `$var[] = &$var` is reachable
			// from the array it points at, so something holds it whatever
			// php2go managed to track.
			shareCopy(value)
			a.cloneAppend(clone, i, value)
			clone.live++
			continue
		}
		// Anything else flattens: a box the runtime made to write through a
		// path is not a reference the program can see, and one last name held
		// is not a reference any more either. Nested arrays are values too, so
		// the copy has to go all the way down.
		value = value.Deref()
		if value.Kind() == KindArray && value.Array() != nil {
			value = Arr(value.Array().clone(seen))
		}
		a.cloneAppend(clone, i, value)
		clone.live++
	}
	// **The internal pointer survives a copy.** Measured: after `next($a)`,
	// both `$b = $a` and a by-value `array $x` parameter see `current()` as the
	// *advanced* element, not the first. PHP's copy is refcounted and the
	// pointer belongs to the array, so it travels; php2go's copy is eager and
	// was dropping it, which reset every copied array to its first element.
	//
	// The index cannot be carried over directly -- deleted buckets are skipped
	// while copying, so positions shift -- so the pointer is mapped through the
	// first live bucket at or after where it stood. A pointer parked past the
	// end stays past the end.
	clone.pointer = len(clone.vals)
	if a.pointer < 0 {
		clone.pointer = -1
	} else {
		for i := a.pointer; i < len(a.vals); i++ {
			if a.deadAt(i) {
				continue
			}
			if at, ok := clone.lookup(a.keyAt(i)); ok {
				clone.pointer = at
			}
			break
		}
	}
	// Deleted buckets are dropped, so a position lands where the live
	// elements before it end.
	clone.arrayBody.inheritCursors(a.arrayBody, a.livePosition)
	return clone
}

// heldWithin reports that a reference points at an array the copy in progress
// is already inside, which makes it part of a cycle: the structure holds the
// box that points back at it, so it has a holder whatever the share counter
// says.
func heldWithin(value Value, seen map[*arrayBody]*Array) bool {
	held := value.Deref()
	if held.Kind() != KindArray || held.a() == nil {
		return false
	}
	_, inProgress := seen[held.a().arrayBody]
	return inProgress
}

// IsList reports whether the keys are exactly 0..n-1 in order, which decides
// whether the array can become a Go slice rather than a map.
func (a *Array) IsList() bool {
	if a == nil {
		return true
	}
	var want int64
	list := true
	a.Each(func(k Key, _ Value) bool {
		if !k.IsInt || k.Int != want {
			list = false
			return false
		}
		want++
		return true
	})
	return list
}

// ReturnedAsIs reports that a library function which may hand back its input
// unchanged -- array_merge with one empty side, array_values of a list --
// hands back *this* array rather than a copy: the engine's `RETURN_COPY`, the
// same elements with the same internal pointer, which a by-reference foreach
// over the variable the result goes back into walks on from where it was.
//
// withStringKeys also accepts an array of string keys only (array_merge's
// other fast path); withNextFree also asks that the next append key be the
// count (array_values'). "Packed without holes" is read as keys 0..n-1 in
// order, which is what it shows.
func (a *Array) ReturnedAsIs(withStringKeys, withNextFree bool) bool {
	if a == nil {
		return false
	}
	if a.IsList() {
		return !withNextFree || a.nextFree == int64(a.Len())
	}
	if !withStringKeys {
		return false
	}
	strings := true
	a.Each(func(k Key, _ Value) bool {
		strings = !k.IsInt
		return strings
	})
	return strings
}

// RefHeld returns the shared box an element holds, if it holds one. A write to
// the element reaches through that box, so a caller that has to check the write
// first needs the box rather than the value.
func (a *Array) RefHeld(key Key) *Ref {
	if a == nil {
		return nil
	}
	pos, ok := a.lookup(key)
	if !ok {
		return nil
	}
	ref, isRef := a.vals[pos].RefOf()
	if !isRef {
		return nil
	}
	return ref
}

// PopLast removes the last element and hands it back, which is array_pop. The
// next-free key follows it down: PHP decrements nNextFreeElement when the key
// removed was the one an append would have used, so popping and appending round
// trip. Popping a string key leaves it alone.
func (a *Array) PopLast() (Key, Value, bool) {
	if a == nil || a.live == 0 {
		return Key{}, Undef(), false
	}
	a.own()
	for pos := len(a.vals) - 1; pos >= 0; pos-- {
		if a.deadAt(pos) {
			continue
		}
		key, value := a.keyAt(pos), a.vals[pos].Deref()
		a.Unset(key)
		if key.IsInt && key.Int == a.nextFree-1 {
			a.nextFree = key.Int
			a.saturated = false
		}
		a.RewindPointer()
		return key, value, true
	}
	return Key{}, Undef(), false
}

// ShiftFirst removes the first element, which is array_shift. Unlike a pop this
// renumbers: every integer key is rewritten from zero, and the string keys keep
// both their names and their places.
func (a *Array) ShiftFirst() (Key, Value, bool) {
	if a == nil || a.live == 0 {
		return Key{}, Undef(), false
	}
	a.own()
	for pos := range a.vals {
		if a.deadAt(pos) {
			continue
		}
		key, value := a.keyAt(pos), a.vals[pos].Deref()
		a.Unset(key)
		a.Renumber()
		a.RewindPointer()
		return key, value, true
	}
	return Key{}, Undef(), false
}

// Renumber rewrites the integer keys from zero in their existing order, leaving
// the string keys as they are. This is what the functions that change an
// array's length at the front do, and what a plain list assignment produces.
func (a *Array) Renumber() {
	if a == nil {
		return
	}
	a.own()
	// A live cursor counts buckets, so a rebuild has to move it: what it was
	// about to visit keeps its place among the *live* elements, which is where
	// it lands once the deleted ones are gone.
	for _, cursor := range a.allCursors() {
		*cursor = a.livePosition(*cursor)
	}
	type entry struct {
		key   Key
		value Value
	}
	kept := make([]entry, 0, a.live)
	for i := range a.vals {
		if !a.deadAt(i) {
			kept = append(kept, entry{a.keyAt(i), a.vals[i]})
		}
	}
	a.reset()
	next := int64(0)
	for _, e := range kept {
		if e.key.IsInt {
			a.Set(IntKey(next), e.value)
			next++
			continue
		}
		a.Set(e.key, e.value)
	}
	a.nextFree = next
	a.seenInt = next > 0
}

// Prepend puts values at the front, which is array_unshift, and renumbers.
func (a *Array) Prepend(values []Value) {
	if a == nil {
		return
	}
	a.own()
	// Everything already here shifts right by as many as are being put in
	// front, and a live cursor shifts with it -- which is why
	// `foreach ($a as &$v) { array_unshift($a, ...); }` still ends after the
	// elements it started with.
	for _, cursor := range a.allCursors() {
		*cursor += len(values)
	}
	rest := a.Clone()
	a.reset()
	for _, value := range values {
		a.Append(value)
	}
	rest.Each(func(k Key, v Value) bool {
		if k.IsInt {
			a.Append(v)
			return true
		}
		a.Set(k, v)
		return true
	})
	a.RewindPointer()
}

// reset empties an array in place, keeping the same box so every holder sees
// the change.
func (a *Array) reset() {
	a.vals, a.keys, a.hashes = nil, nil, nil
	a.resetIndex()
	a.live = 0
	a.nextFree = 0
	a.seenInt = false
	a.saturated = false
}

// Splice removes a run of elements and puts another in its place, which is
// array_splice, and answers what it removed. It is a method on the array rather
// than something a builtin can assemble out of the smaller ones because the
// engine's routine moves any live foreach position as it rebuilds -- and not by
// a fixed shift. It walks the buckets carrying the position along with the
// element it was on, so an insertion in front of one drags it forward again at
// every element after the insertion, which can carry it to the end of the
// array. Nothing about that is derivable; it is what the walk does.
//
// collect says the caller wants the removed elements. The engine's walk counts
// them into the same position it rebuilds with, so asking for them moves the
// cursor differently -- `array_splice($a, 0, 2)` and `$r = array_splice($a, 0,
// 2)` really do iterate differently afterwards.
func (a *Array) Splice(offset, length int, replacement []Value, collect bool) *Array {
	removed := NewArray()
	if a == nil {
		return removed
	}
	a.own()
	out := NewArray()
	// The engine keeps its iterators in one global table and finds the next one
	// by scanning for the lowest position at or after a bound; with the cursors
	// on the array itself the scan is the same, and answers -1 for none.
	lowerPos := func(from int) int {
		best := -1
		for _, cursor := range a.allCursors() {
			if *cursor >= from && (best < 0 || *cursor < best) {
				best = *cursor
			}
		}
		return best
	}
	moveTo := func(from, to int) {
		for _, cursor := range a.allCursors() {
			if *cursor == from {
				*cursor = to
			}
		}
	}
	// numUsed is the engine's nNumUsed: the buckets in play, which shrinks when
	// the last one is deleted and takes any position past it along.
	numUsed := len(a.vals)
	drop := func(idx int) {
		a.Unset(a.keyAt(idx))
		if idx != numUsed-1 {
			return
		}
		for numUsed > 0 && a.deadAt(numUsed-1) {
			numUsed--
		}
		for _, cursor := range a.allCursors() {
			if *cursor > numUsed {
				*cursor = numUsed
			}
		}
	}
	keep := func(idx int) {
		if a.keyAt(idx).IsInt {
			out.Append(a.vals[idx])
			return
		}
		out.Set(a.keyAt(idx), a.vals[idx])
	}

	iterPos := lowerPos(0)
	pos, idx := 0, 0
	for ; pos < offset && idx < numUsed; idx++ {
		if a.deadAt(idx) {
			continue
		}
		keep(idx)
		if idx == iterPos {
			if idx != pos {
				moveTo(idx, pos)
			}
			iterPos = lowerPos(iterPos + 1)
		}
		pos++
	}
	// A position inside the run being removed parks past it, and the walk below
	// brings it back to whatever ends up there.
	counted := pos
	for ; counted-offset < length && idx < numUsed; idx++ {
		if a.deadAt(idx) {
			continue
		}
		counted++
		if collect {
			// The engine moves the slot across rather than the value, so an
			// element a by-reference loop is holding arrives in the removed
			// array as the same reference. Anything else is copied, since two
			// arrays sharing one value is not what a plain element is.
			taken := a.vals[idx]
			if _, isRef := taken.RefOf(); !isRef {
				taken = taken.Deref().Copy()
			}
			removed.Append(taken)
			pos = counted
		}
		drop(idx)
		if idx == iterPos {
			moveTo(idx, offset+length)
			iterPos = lowerPos(iterPos + 1)
		}
	}
	for _, value := range replacement {
		out.Append(value)
		pos++
	}
	for ; idx < numUsed; idx++ {
		if a.deadAt(idx) {
			continue
		}
		keep(idx)
		if idx == iterPos {
			if idx != pos {
				moveTo(idx, pos)
			}
			iterPos = lowerPos(iterPos + 1)
		}
		pos++
	}

	// The rebuilt buckets replace this array's in place, so every holder sees
	// the change. The cursors carry over untouched -- the walk above already
	// moved them into the new array's terms, and nothing below reads them.
	a.reset()
	// The buckets move across as they are, references included: an element a
	// by-reference loop is holding stays the same slot after the rebuild, which
	// is what makes var_dump still mark it.
	for i := range out.vals {
		if out.deadAt(i) {
			continue
		}
		if k := out.keyAt(i); !k.IsInt {
			a.Set(k, out.vals[i])
			continue
		}
		a.Append(out.vals[i])
	}
	a.nextFree = out.nextFree
	a.seenInt = out.seenInt
	a.RewindPointer()
	return removed
}

// livePosition answers how many live elements sit before a bucket index, which
// is where a cursor at that index lands once the deleted buckets are gone.
func (a *Array) livePosition(cursor int) int {
	live := 0
	for i := 0; i < cursor && i < len(a.vals); i++ {
		if !a.deadAt(i) {
			live++
		}
	}
	return live
}

// CursorPositions answers where the live foreach positions this array carries
// sit, counted in live elements rather than buckets, and SetCursorPositions
// puts them back in the same order. A rebuild that is not a plain shift --
// array_splice, which can remove one run and insert another of a different
// length -- works out where each cursor lands itself and says so.
func (a *Array) CursorPositions() []int {
	if a == nil {
		return nil
	}
	positions := make([]int, len(a.allCursors()))
	for i, cursor := range a.allCursors() {
		positions[i] = a.livePosition(*cursor)
	}
	return positions
}

func (a *Array) SetCursorPositions(positions []int) {
	if a == nil {
		return
	}
	for i, cursor := range a.allCursors() {
		if i < len(positions) {
			*cursor = positions[i]
		}
	}
}

// RegisterCursor records a live position that rebuilding this array has to
// move, and answers a function that stops tracking it.
func (a *Array) RegisterCursor(cursor *int) func() {
	if a == nil || cursor == nil {
		return func() {}
	}
	a.cursors = append(a.cursors, cursor)
	w := &cursorWalker{pos: cursor, body: a.arrayBody}
	a.arrayBody.walkers = append(a.arrayBody.walkers, w)
	return func() {
		w.done = true
		for i, held := range w.body.walkers {
			if held == w {
				w.body.walkers = append(w.body.walkers[:i], w.body.walkers[i+1:]...)
				break
			}
		}
		for i, held := range a.cursors {
			if held == cursor {
				a.cursors = append(a.cursors[:i], a.cursors[i+1:]...)
				return
			}
		}
	}
}

// LiveNext walks the buckets from a position, skipping the deleted ones, and
// answers the element it lands on with the position after it. It is what a
// by-reference foreach needs: PHP keeps a live position in the hash rather than
// a list of keys, so a key added during the loop is reached and one removed is
// simply gone.
//
// The array's own internal pointer -- what current() and next() move -- is
// deliberately untouched: a foreach does not disturb it.
func (a *Array) LiveNext(from int) (Key, Value, int, bool) {
	if a == nil {
		return Key{}, Undef(), from, false
	}
	for i := from; i < len(a.vals); i++ {
		if a.deadAt(i) {
			continue
		}
		return a.keyAt(i), a.vals[i].Deref(), i + 1, true
	}
	return Key{}, Undef(), len(a.vals), false
}

// ArrayKey is the value-to-key coercion, which is PHP's and not either
// consumer's -- the compiled emitter had written its own copy, which is the
// divergence this boundary exists to prevent. It joins StringKey above: that
// one owns the *string* half of the rule, this one the rest.
//
// The whole table came from the engine, by writing each value into an array and
// reading the key back. Three entries are not guessable:
//
//	null   => ""      not 0, and the only non-numeric coercion
//	true   => 1       so $a[true] and $a[1] are the same slot
//	5.0    => 5       silently, while 5.9 => 5 *and deprecates*
//
// The deprecation fires on **precision loss**, not on being a float, which is
// why a whole float is quiet. NAN keys as 0 and deprecates; a resource keys by
// its id and warns every time.
// arrayKey applies PHP's key coercion: floats and bools become integers, null
// becomes the empty string, and a canonical numeric string becomes an integer.
func ArrayKey(ctx *Ctx, v Value) Key {
	if v.Kind() == KindFloat {
		// Truncating a fractional float to a key loses information, which PHP
		// reports wherever the key is used -- read, write, isset or unset. The
		// comparison has to go through the conversion the key itself uses: a
		// float too large for an int64 wraps rather than truncating, and Go's
		// own cast is undefined out of range, so it cannot be the judge.
		if ctx != nil && float64(v.Int()) != v.Float() {
			// The number is spelled the way var_dump spells one -- at full
			// precision -- rather than the way echo does. `9.2233720368548E+18`
			// is what the value prints as; what the engine reports is
			// `9.223372036854776E+18`.
			ctx.Deprecated("Implicit conversion from float %s to int loses precision",
				DumpFloat(v.Float()))
		}
	}
	switch v.Kind() {
	case KindInt:
		return IntKey(v.Int())
	case KindFloat, KindTrue, KindFalse:
		return IntKey(v.Int())
	case KindUndef, KindNull:
		return StringKey("")
	case KindResource:
		// A resource keys by its id, and PHP says so every time rather than
		// letting the conversion pass unremarked.
		id := int64(v.Resource().ID)
		if ctx != nil {
			ctx.Warn("Resource ID#%d used as offset, casting to integer (%d)", id, id)
		}
		return IntKey(id)
	}
	return StringKey(v.String())
}
