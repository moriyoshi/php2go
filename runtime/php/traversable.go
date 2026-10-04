package php

// Iterating a *PHP object* means calling PHP methods, and `runtime/php` cannot
// call one: dispatching `Iterator::current()` needs a class table, which lives
// above this package. So the capability is **declared here and supplied from
// above** rather than discovered -- the same move as `.suspends_to_caller` going
// on the function and `mode=IS` on the operand.
//
// Both consumers need exactly this one hop. `foreach ($obj as $v)` and
// `yield from $obj` were separately reported as `ErrIterUnsupported` and
// `ErrDelegateUnsupported`; they are one gap wearing two hats, and they share a
// resolver here.
//
// # The call sequence is the engine's, not a convenient approximation
//
// Taken from an `Iterator` whose methods echo, rather than from the source:
//
//	foreach (new It as $k => $v)     rewind, valid, current, key, [body], next, valid, ...
//	foreach (new It as $v)           rewind, valid, current,      [body], next, valid, ...
//	foreach (new Agg as $k => $v)    getIterator, then the above
//	yield from new It                the same, rewind included
//
// Three properties fall out, and each one shapes the interface:
//
//   - **`key()` is not called when the loop does not bind a key.** So Key must
//     stay a separate call the emitter makes only where the IR has `iter.key`,
//     never something prefetched alongside Current.
//   - **`next` is always immediately followed by `valid`.** So Next may do both,
//     which is what lets Iter.Valid stay a pure read of a cached answer instead
//     of calling into PHP -- see the note on Valid in iter.go.
//   - **`current()` is called once per iteration, when the body runs.**
//     Prefetching it would call user code the engine does not call, in an order
//     it does not use, and an `Iterator` that echoes would show the difference.

// Traversable is one PHP object being iterated, as the layer that can call
// methods presents it. It is `Iterator`'s five methods and nothing else --
// `IteratorAggregate` is resolved by calling `getIterator()` before this, and a
// `Generator` never reaches here because Iter drives it directly.
//
// Every method may run user code, so every method may throw.
type Traversable interface {
	// Rewind is `rewind()`, called once before the first Valid.
	Rewind(ctx *Ctx) error
	// Valid is `valid()`. Iter calls it exactly once per iteration, folded into
	// NewIter and Next, and caches the answer.
	Valid(ctx *Ctx) (bool, error)
	// Current is `current()`, called when the body runs.
	Current(ctx *Ctx) (Value, error)
	// Key is `key()`, called only where the loop binds a key.
	Key(ctx *Ctx) (Value, error)
	// Next is `next()`.
	Next(ctx *Ctx) error
}

// ResolveTraversable turns an object into a Traversable, and is installed by the
// layer that has a class table. It reports false for an object that is not
// Traversable at all, which is a PHP `Error` the caller above phrases; an error
// is one raised while resolving, which for an `IteratorAggregate` means
// `getIterator()` itself threw.
//
// A `Generator` must not be resolved through this. Iter and the `yield from`
// delegates drive one directly, because a generator is not rewindable and its
// keys are its own.
type ResolveTraversable func(ctx *Ctx, object Value) (Traversable, bool, error)

// SetTraversableResolver installs the hop. It hangs off the Ctx rather than a
// package variable for the reason the constant table does: PHP is
// shared-nothing per request and a long-lived Go process is not.
func (c *Ctx) SetTraversableResolver(fn ResolveTraversable) { c.traversable = fn }

// traverse resolves object through the installed resolver. With none installed
// the capability is honestly absent rather than silently empty: an iterator that
// yields nothing would make `foreach` over a Traversable look like a working
// loop over an empty collection.
// hasTraversableResolver reports whether the hop is installed at all, which is
// a different question from whether a given object is Traversable. Without it
// the two answers are the same `false`, and a **program error** -- `yield from
// new stdClass` -- was reported as a missing capability.
func (c *Ctx) hasTraversableResolver() bool { return c.traversable != nil }

func (c *Ctx) traverse(object Value) (Traversable, bool, error) {
	if c.traversable == nil {
		return nil, false, nil
	}
	return c.traversable(c, object)
}
