package php

import (
	"errors"
	"fmt"
)

// Iter is what a `$__iterN: *iter` frame slot holds: the state of one `foreach`.
//
// The IR declares iterator slots at a type of their own rather than as `zval`,
// and the point of that is what this type is *not*. An Iter is **not a Value**.
// It cannot be boxed into one, stored in an array, or handed to PHP code. A
// uniform `[]Value` frame could not hold one, which is the argument that forced
// a generated struct per function; this file is the other end of it.
//
// # The opcodes it serves
//
//	%1 = iter.new $rows            -> NewIterator, which wraps this
//	br iter.valid $__iter5, .., ..  -> Valid
//	iter.current $__iter5           -> Current
//	iter.key $__iter5               -> Key
//	iter.next $__iter5              -> Next
//	iter.free $__iter5              -> Free
//
// The block structure matters more than the operations. `FE_FETCH` is the loop
// *header* and tests for exhaustion **before** it fetches, so the emitted shape
// is a block that only calls Valid, branching to a body block that calls
// Current/Key/Next. Getting that backwards drops the last iteration, which is
// the defect this path was born with -- see the journal.
//
// # Snapshot, not a live view
//
// `foreach ($a as $v)` iterates a **copy**. Settled by asking the engine rather
// than by reasoning about refcounts:
//
//	$b = ['x'=>1,'y'=>2,'z'=>3];
//	foreach ($b as $k => $v) { if ($k=='x') { $b['z'] = 30; } echo "$k=$v,"; }
//	// x=1,y=2,z=3,   -- not z=30
//
// So it is not enough to snapshot the *keys* and read values through the array:
// an assignment to an element not yet reached is invisible too. Appends during
// the loop are not seen, and neither is an `unset` of an element still ahead.
// Both keys and values are taken once, here.
//
// The same is true of `yield from` over an array, including when the array
// arrives by reference -- `function h(&$r) { yield from $r; }` still yields the
// value the array had when the delegation started.
type Iter struct {
	keys   []Key
	values []Value
	at     int

	// snap is a **by-value** walk over an array: a copy-on-write share of it,
	// which is the snapshot PHP's foreach iterates without copying anything.
	// A write to the array in the loop separates the writer and leaves this
	// body alone; an element holding a reference is read when it is reached,
	// as the engine reads it. spos is the bucket position, always a live one
	// or past the end.
	snap *Array
	spos int

	// gen is set when iterating a Generator instead of an array. A generator is
	// *not* snapshotted -- it has no elements until it is asked -- so the two
	// cases share an interface and nothing else.
	gen *Generator

	// trav is set when iterating a PHP object through the hop in
	// traversable.go, and valid caches its `valid()` answer. The cache is what
	// keeps Valid a pure read: the engine calls `valid()` exactly once per
	// iteration, always straight after `rewind()` or `next()`, so NewIter and
	// Next make that call and this holds the result. Calling `valid()` again
	// would run user code the engine did not run.
	trav  Traversable
	valid bool

	// live is set for a **by-reference** walk over an array, which is the one
	// case that does not snapshot. `foreach ($a as &$v)` follows the array as
	// the body mutates it -- an element appended inside the loop is reached, an
	// element unset inside it is skipped -- because the engine holds a bucket
	// position rather than a list of keys. A by-value loop iterates a copy and
	// sees none of that, which is why the two modes are a field and not a flag
	// on every call.
	//
	// `array` is the live source and `pos` the bucket position `LiveNext`
	// advances. `key` is the key the current step landed on, which is what
	// `CurrentRef` needs to hand out the element's box.
	// untrack stops the array moving `pos` when it is rebuilt. A live walk
	// registers one; every other kind leaves it nil.
	untrack func()
	live    bool
	array   *Array
	pos     int
	key     Key
	held    bool

	// source is the reference a by-reference walk was opened on, when it
	// was opened on one: a variable (FE_RESET_RW makes the CV a reference
	// and the loop holds it) or a property's box. The engine re-reads it at
	// every fetch, so an array assigned to the variable in the body is the
	// one walked from then on (follow). ctx, file and line are what a fetch
	// that finds no array any more warns with: the foreach's own line.
	source *Ref
	ctx    *Ctx
	file   string
	line   uint32
	// object is the plain object a by-reference walk over a variable is
	// walking through its property slots (array holds them), nil while it
	// walks an array. failed is what re-reading the variable threw -- a lazy
	// object's initializer runs when its properties are fetched -- for Next to
	// report.
	object *Object
	failed error
	scope  string
}

// SnapIter is the cursor behind an iterator value when it is a **by-value
// walk over an array** -- the snapshot mode, fixed when the cursor is built --
// and nil for anything else. The emitter tests it at each foreach step and
// then uses the three methods below, which inline; the general Iterator*
// functions do not.
func SnapIter(v Value) *Iter {
	if v.kind == KindObject {
		if it, _ := (*Object)(v.ptr).Internal.(*Iter); it != nil && it.snap != nil && !it.live && it.trav == nil && it.gen == nil {
			return it
		}
	}
	return nil
}

// SnapValid is Valid for a SnapIter cursor.
func (it *Iter) SnapValid() bool { return it.spos < len(it.snap.vals) }

// SnapCurrent is Current for a valid SnapIter cursor: an element holding a
// reference is read through it when reached, as the engine reads it.
func (it *Iter) SnapCurrent() Value { return it.snap.vals[it.spos].Deref() }

// SnapNext is Next for a SnapIter cursor.
func (it *Iter) SnapNext() { it.spos = it.snap.liveFrom(it.spos + 1) }

// NewIter is the cursor `iter.new` builds: the FE_RESET_R half, which evaluates the subject
// once and decides whether the loop runs at all.
//
// A source this cannot iterate is reported rather than treated as empty. An
// empty iterator would make `foreach` over a Traversable silently produce
// nothing, which is the gap-masquerading-as-behaviour QUALITY_GATE.md forbids.
func NewIter(ctx *Ctx, subject Value) (*Iter, error) {
	// Read by value, as FE_RESET_R reads it: a by-reference function's
	// result arrives as the reference it returned.
	subject = subject.Deref()
	switch subject.Kind() {
	case KindArray:
		return NewArrayIter(subject.Array()), nil
	case KindObject:
		obj := subject.Object()
		if g, ok := obj.Internal.(*Generator); ok {
			if g.Finished() {
				// **A different refusal from rewind's**, and the operation is
				// what picks it: opening a `foreach` over a generator that has
				// run to the end is `Cannot traverse an already closed
				// generator`, while `$g->rewind()` on the same object is
				// `Cannot rewind a generator that was already run`. This
				// answered an empty loop instead.
				return nil, ErrGeneratorClosed
			}
			it := &Iter{gen: g}
			// `foreach` over a generator **rewinds** it, as the engine's
			// iterator does: that primes it -- the first element has to exist
			// before `valid` can answer -- and refuses one already past its
			// first yield, `Cannot rewind a generator that was already run`,
			// where priming alone went on iterating from wherever it was.
			if err := g.Rewind(ctx); err != nil {
				return nil, err
			}
			return it, nil
		}
		// A Traversable is iterated by calling its methods, which needs the hop
		// installed from above.
		trav, ok, err := ctx.traverse(subject)
		if err != nil {
			return nil, err
		}
		if ok {
			it := &Iter{trav: trav}
			// `rewind` then `valid`, which is how the engine opens every
			// foreach over an Iterator -- including one reached through
			// `getIterator()`, and including a `yield from`.
			if err := trav.Rewind(ctx); err != nil {
				return nil, err
			}
			if it.valid, err = trav.Valid(ctx); err != nil {
				return nil, err
			}
			return it, nil
		}
		// A plain object iterates its properties, and from outside the class
		// only the public ones -- checked: `class P { public $pub=1; protected
		// $prot=2; private $priv=3; }` gives `pub=1,` and nothing else. Which
		// properties are visible depends on the *calling scope*, and
		// `runtime/php` has no scope to consult, so this belongs to the layer
		// that has one. Reported, not guessed at -- a genuine gap.
		return nil, fmt.Errorf("%w: foreach over an object", ErrIterUnsupported)
	}
	// A scalar is not a gap: it is the *program* being wrong, and PHP says so
	// and carries on. Settled against the engine rather than assumed --
	// `foreach (5 as $v)` warns and the body runs **zero times**, with
	// execution continuing afterwards:
	//
	//	Warning: foreach() argument must be of type array|object, int given
	//
	// Returning an error here instead would have made a diagnostic into a
	// refusal, which is the same conflation the `unsupported` bucket had: two
	// populations, one of which is not a gap at all.
	ctx.Warn("foreach() argument must be of type array|object, %s given", TypeName(subject))
	return &Iter{}, nil
}

// NewArrayRefIter is the by-reference walk: `foreach ($a as &$v)`. It holds the
// array rather than a snapshot of it, so the loop follows the live buckets.
//
// The first step is taken here, because `Valid` must be a pure read -- the loop
// header calls it before the body and must not advance.
func NewArrayRefIter(a *Array) *Iter {
	it := &Iter{live: true, array: a}
	// **The position has to move when the array is rebuilt.** `array_unshift`
	// inside the loop shifts every element right, and a cursor that stays put
	// walks the new elements and then the old ones again -- `foreach ($a as &$v)
	// { array_unshift($a, 0); }` never ended. `Array.RegisterCursor` existed for
	// exactly this and had no callers.
	it.untrack = a.RegisterCursor(&it.pos)
	it.step()
	return it
}

// follow is the half of FE_FETCH_RW that re-reads the walked variable. The
// engine keeps a hash iterator rather than the array, and asks it for a
// position in whatever array the variable holds at each fetch
// (zend_hash_iterator_pos_ex). Probed:
//
//   - the same array, written in place or handed to another name and back
//     (`$h = $g; $g = $h;`), goes on where it was -- and so does a copy of
//     it, however made (`$g += [...]`, `$g = $g + []`, a separated copy
//     handed back), at the same element (Array.CopiedCursor);
//   - any other array -- a literal, a copy of some other array -- starts
//     again at **its** internal pointer -- `0=1`,
//     then `0=10, 1=20, ...` after `$g = [10, 20, ...]`, and `1=6` after
//     `next($g2); $g = $g2;`. An array another name still holds is copied
//     first, and a copy's pointer that ran off the end starts at 0 (`end`
//     and `next` on `$g2` still walk all of it); one nobody else holds keeps
//     its pointer, so the walk ends there;
//   - anything else but an object warns `foreach() argument must be of type
//     array|object` on the foreach's line and ends the loop. An object would
//     go on over its properties, which this cursor cannot do: reported.
//
// It answers false when the walk has to end.
func (it *Iter) follow() bool {
	if it.source == nil {
		return true
	}
	cur := it.source.Get()
	if cur.Kind() == KindObject {
		return it.followObject(cur.Object())
	}
	if cur.Kind() != KindArray {
		if it.ctx != nil {
			file, line := it.ctx.Location()
			it.ctx.SetLocation(it.file, it.line)
			it.ctx.Warn("foreach() argument must be of type array|object, %s given", TypeName(cur))
			it.ctx.SetLocation(file, line)
		}
		it.held = false
		return false
	}
	a := cur.Array()
	if a == it.array {
		return true
	}
	it.object = nil
	// Asked before letting go: letting go retires the walk's copies.
	copied, isCopy := a.CopiedCursor(&it.pos)
	if it.untrack != nil {
		it.untrack()
	}
	if a.arrayBody == it.array.arrayBody {
		// Another header over the same elements: the same array to the
		// engine, so the walk goes on from the position it had.
		it.array = a
		it.untrack = a.RegisterCursor(&it.pos)
		return true
	}
	if isCopy {
		// A copy of the walked array, however indirect -- `$a += [...]`,
		// `$b = $a; unset($b[3]); $a = $b;` -- carries the walk's position
		// (the engine's iterator copies, GH-11171), so the walk goes on
		// there and reaches what the copy added.
		it.array = a
		it.untrack = a.RegisterCursor(&it.pos)
		it.pos = copied
		return true
	}
	pos := a.pointer
	if pos < 0 || pos >= len(a.vals) {
		pos = len(a.vals)
		if a.arrayBody.shares > 0 {
			pos = 0
		}
	}
	it.array = a
	it.untrack = a.RegisterCursor(&it.pos)
	it.pos = pos
	return true
}

// step advances the live walk to the next undeleted bucket and records what it
// landed on.
// followObject is follow when the walked variable holds an object. The one
// the walk is already on goes on from where it was. Any other is walked from
// its first property, as an array assigned in the body is (the engine's
// iterator lands on a fresh property table's internal pointer, probed) --
// through its property table even when it is a Traversable -- and fetching
// its properties initialises a lazy object, whose initializer may throw
// (gh20085). An object the program's ObjectRefWalk does not model is
// reported.
func (it *Iter) followObject(o *Object) bool {
	if o == it.object {
		return true
	}
	var boxes Value
	modelled := false
	var err error
	if it.ctx != nil && it.ctx.objectRefWalk.Boxes != nil {
		boxes, modelled, err = it.ctx.objectRefWalk.Boxes(it.ctx, o, it.scope)
	}
	if err != nil {
		it.failed = err
		it.held = false
		return false
	}
	if !modelled {
		if it.ctx != nil {
			it.ctx.ParkError(Unsupported("foreach by reference over a variable reassigned to an object"))
		}
		it.held = false
		return false
	}
	if it.untrack != nil {
		it.untrack()
	}
	it.object = o
	it.array = boxes.Array()
	it.untrack = it.array.RegisterCursor(&it.pos)
	it.pos = 0
	return true
}

func (it *Iter) step() {
	k, _, next, ok := it.array.LiveNext(it.pos)
	it.pos, it.key, it.held = next, k, ok
}

// CurrentRef is the **element's box** for a by-reference walk, which is what
// the loop variable binds to. A `false` second result means the source was not
// an array walked by reference -- an object or a generator -- and the caller
// falls back to the ordinary by-value fetch rather than inventing storage.
func (it *Iter) CurrentRef() (*Ref, bool) {
	if it == nil || !it.live || it.array == nil || !it.held {
		return nil, false
	}
	return it.array.RefAt(it.key), true
}

// NewArrayIter takes the snapshot. It is separate from NewIter because the
// array case needs no ctx and cannot fail, which is worth keeping visible.
func NewArrayIter(a *Array) *Iter {
	if a == nil {
		return &Iter{}
	}
	it := &Iter{snap: a.Share()}
	it.spos = it.snap.liveFrom(0)
	return it
}

// liveFrom is the first live bucket at or after pos, or len(buckets).
func (a *Array) liveFrom(pos int) int {
	for pos < len(a.vals) && a.deadAt(pos) {
		pos++
	}
	return pos
}

// arraySnapshot is the keys and values of an array as they stand, for a
// consumer that holds them past any change to it -- `yield from` over an
// array.
func arraySnapshot(a *Array) ([]Key, []Value) {
	keys := a.Keys()
	values := make([]Value, len(keys))
	for i, k := range keys {
		values[i], _ = a.Get(k)
	}
	return keys, values
}

// Valid is `iter.valid`, and the only thing the loop header may call. It must
// not advance or fetch: FE_FETCH tests before it fetches, and a header that
// touches the iterator is how the last iteration goes missing.
func (it *Iter) Valid() bool {
	if it.live {
		return it.held
	}
	if it.trav != nil {
		return it.valid
	}
	if it.gen != nil {
		return !it.gen.Finished()
	}
	if it.snap != nil {
		return it.spos < len(it.snap.vals)
	}
	return it.at < len(it.keys)
}

// Current is `iter.current`. It takes a ctx and can fail because a Traversable's
// `current()` is user code: the engine calls it once per iteration, when the
// body runs, and prefetching it into NewIter would call it in an order the
// engine does not use.
func (it *Iter) Current(ctx *Ctx) (Value, error) {
	if !it.Valid() {
		return Null(), nil
	}
	if it.live {
		v, _ := it.array.Get(it.key)
		return v, nil
	}
	if it.trav != nil {
		return it.trav.Current(ctx)
	}
	if it.gen != nil {
		return it.gen.peekCurrent(ctx)
	}
	if it.snap != nil {
		return it.snap.vals[it.spos].Deref(), nil
	}
	return it.values[it.at], nil
}

// Key is `iter.key`. A generator's keys are its own -- the automatic counter
// belongs to the generator instance, not to the loop.
//
// It stays a separate call rather than something Current returns alongside,
// because the engine does not call `key()` at all when the loop binds no key:
// `foreach (new It as $v)` runs rewind/valid/current/next and never key. So the
// emitter must call this only where the IR has `iter.key`.
func (it *Iter) Key(ctx *Ctx) (Value, error) {
	if !it.Valid() {
		return Null(), nil
	}
	if it.live {
		return it.key.Value(), nil
	}
	if it.trav != nil {
		return it.trav.Key(ctx)
	}
	if it.gen != nil {
		return it.gen.peekKey(ctx)
	}
	if it.snap != nil {
		return it.snap.keyAt(it.spos).Value(), nil
	}
	return it.keys[it.at].Value(), nil
}

// Next is `iter.next`. Advancing a generator can fail -- its body runs -- while
// advancing an array cannot, so this reports an error that an array iterator
// never produces.
func (it *Iter) Next(ctx *Ctx) error {
	if it.live {
		// The **live** walk: `LiveNext` starts from the bucket after the one
		// this step landed on, so an element unset in the body is skipped and
		// one appended in it is reached.
		if it.follow() {
			it.step()
		}
		if err := it.failed; err != nil {
			it.failed = nil
			return err
		}
		return nil
	}
	if it.trav != nil {
		// `next` then `valid`, which is the pair the engine always emits
		// together. Folding them is what lets Valid stay a pure read.
		if err := it.trav.Next(ctx); err != nil {
			return err
		}
		valid, err := it.trav.Valid(ctx)
		it.valid = valid
		return err
	}
	if it.gen != nil {
		return it.gen.Next(ctx)
	}
	if it.snap != nil {
		it.spos = it.snap.liveFrom(it.spos + 1)
		return nil
	}
	it.at++
	return nil
}

// CurrentArray, KeyArray and NextArray are the array-subject paths of the three
// above, with the Traversable and generator branches removed rather than merely
// not taken. The compiler selects them only where it proved the subject is an
// array, so a cursor reaching them cannot be over anything else.
//
// **They still have to ask which array walk this is.** "The subject is an
// array" is what the compiler proved, and a by-reference cursor over an array
// satisfies it while holding a bucket position rather than a snapshot index.
// Stepping `at` on one advanced nothing, so `Valid` never became false and the
// loop ran forever.
func (it *Iter) CurrentArray() Value {
	if !it.Valid() {
		return Null()
	}
	if it.live {
		v, _ := it.array.Get(it.key)
		return v
	}
	if it.snap != nil {
		return it.snap.vals[it.spos].Deref()
	}
	return it.values[it.at]
}

func (it *Iter) KeyArray() Value {
	if !it.Valid() {
		return Null()
	}
	if it.live {
		return it.key.Value()
	}
	if it.snap != nil {
		return it.snap.keyAt(it.spos).Value()
	}
	return it.keys[it.at].Value()
}

func (it *Iter) NextArray() {
	if it.live {
		if it.follow() {
			it.step()
		}
		if err := it.failed; err != nil {
			// No error result here: parked, for the next opcode boundary.
			it.failed = nil
			it.ctx.ParkError(err)
		}
		return
	}
	if it.snap != nil {
		it.spos = it.snap.liveFrom(it.spos + 1)
		return
	}
	it.at++
}

// Close unwinds a generator this cursor is the last holder of, so its `finally`
// blocks run. Anything else has nothing to unwind.
func (it *Iter) Close(ctx *Ctx) error {
	if it == nil || it.gen == nil {
		return nil
	}
	return it.gen.Close(ctx)
}

// Free is `iter.free`. It releases the snapshot but deliberately leaves the
// slot itself alone: "every declared slot outlives the frame" is structural,
// and a generator's `foreach` may be abandoned mid-loop with the frame still
// alive.
func (it *Iter) Free() {
	if it.untrack != nil {
		it.untrack()
		it.untrack = nil
	}
	it.keys, it.values, it.at = nil, nil, 0
	if it.snap != nil {
		// Hand the share back, so the array's next write need not copy.
		it.snap.release()
		it.snap, it.spos = nil, 0
	}
	it.trav, it.valid = nil, false
	it.array, it.live, it.held, it.pos = nil, false, false, 0
	it.object, it.failed = nil, nil
}

// ArrayUnpack merges `...$v` into an array literal under construction, and
// returns the container the way ArrayAppend does.
//
// **Integer keys renumber and string keys keep their name**, which is the whole
// difference from a merge: `[...[PHP_INT_MAX => "m"], ...[1]]` is `[0 => "m",
// 1 => 1]`. That is about the *source*'s keys, and it does not exempt the
// **destination** from saturating: `[PHP_INT_MAX-1 => 0, ...[1,2,3]]` raises
// `Cannot add element to the array as the next element is already occupied`,
// measured, for the array and the iterator arms alike. A string key does not
// append and so never asks. A string key already present is overwritten.
//
// The two refusals are **different classes**, read back from the engine rather
// than assumed:
//
//	[...$int]        Error: Only arrays and Traversables can be unpacked, int given
//	[...$stdClass]   TypeError: Only arrays and Traversables can be unpacked, stdClass given
func ArrayUnpack(ctx *Ctx, into, v Value) (Value, error) {
	held := v.Deref()
	switch held.Kind() {
	case KindArray:
		out := into.Array()
		var full error
		held.Array().Each(func(k Key, element Value) bool {
			if k.IsInt {
				if !out.CanAppend() {
					full = ThrowNextElementOccupied()
					return false
				}
				out.Append(element.Copy())
				return true
			}
			out.Set(k, element.Copy())
			return true
		})
		return into, full
	case KindObject:
		// A generator or an Iterator is drained through the ordinary protocol,
		// which is what makes this ReEnters: `[...gen()]` runs the body.
		it, err := NewIter(ctx, held)
		if errors.Is(err, ErrIterUnsupported) {
			// **Not iterable is the TypeError**; anything else NewIter reports
			// is the program's own throw -- a `rewind()` that raised -- and
			// converting that would swallow it.
			return into, RaiseAt(ctx, "TypeError",
				"Only arrays and Traversables can be unpacked, %s given", DisplayName(held.Object().ClassName))
		}
		if err != nil {
			return into, err
		}
		defer it.Free()
		out := into.Array()
		for it.Valid() {
			element, err := it.Current(ctx)
			if err != nil {
				return into, err
			}
			key, err := it.Key(ctx)
			if err != nil {
				return into, err
			}
			// **Only int|string**, and the refusal is an Error rather than a
			// TypeError. An array key or a float key reaches here from a
			// generator -- `yield [] => 1` is legal to write and only fails
			// when something unpacks it.
			k := key.Deref()
			switch k.Kind() {
			case KindInt, KindString:
			default:
				return into, RaiseAt(ctx, "Error", "%s",
					"Keys must be of type int|string during array unpacking")
			}
			// An **integer-like string is an integer key**, and integer keys
			// are renumbered rather than preserved: `yield "42" => 1` unpacks
			// to `[0 => 1]`, not `[42 => 1]`. Setting the string key verbatim
			// kept the 42.
			normalised := ArrayKey(ctx, k)
			if !normalised.IsInt {
				out.Set(normalised, element.Copy())
			} else {
				if !out.CanAppend() {
					return into, ThrowNextElementOccupied()
				}
				out.Append(element.Copy())
			}
			if err := it.Next(ctx); err != nil {
				return into, err
			}
		}
		return into, nil
	}
	return into, RaiseAt(ctx, "Error",
		"Only arrays and Traversables can be unpacked, %s given", TypeName(held))
}
