package php

import (
	cryptorand "crypto/rand"
	"crypto/sha1"
	"encoding/binary"
	"unsafe"
)

// Ref is a PHP reference: a slot two or more names share, so that assigning
// through one is visible through the others.
//
// PHP references are not pointers. `$b = &$a` does not make $b point at $a; it
// makes both names point at the same underlying slot, and unsetting either
// leaves the other intact. A single box shared by both slots models that
// exactly, where a Go pointer to a variable would not.
type Ref struct {
	value Value
	// shared counts the bindings an explicit `&` in the program made to this
	// reference, as opposed to the boxes the runtime makes to write through a
	// path. A binding held by a local disappears when that frame ends, which is
	// what stops var_dump marking a reference nobody shares any more.
	shared int
	// bindings counts only explicit PHP & bindings. shared also includes the
	// eager array copies php2go makes in place of Zend COW; debug_zval_dump must
	// not expose those implementation-only holders as PHP reference slots.
	bindings int
	// sources are the typed properties this slot is shared with. Binding a
	// reference to `public int $p` makes the *reference* typed: every write
	// through any name sharing it has to satisfy every source's type, which is
	// why the constraint travels with the box rather than with the property.
	sources []RefSource
}

// RefSource is one typed property a reference is shared with. Owner is the
// object whose slot it is -- the engine counts sources per slot, so two objects
// sharing a box through the same property are two constraints, and one of them
// going away leaves the other standing. It is nil for a static property, which
// belongs to the class.
type RefSource struct {
	Class string
	Name  string
	Type  string
	Owner any
}

// AddSource records that this slot is shared with a typed property. Binding the
// same property twice is not two constraints.
func (r *Ref) AddSource(src RefSource) {
	if r == nil || src.Type == "" {
		return
	}
	for _, have := range r.sources {
		if have == src {
			return
		}
	}
	r.sources = append(r.sources, src)
}

// DropSource takes a typed property off the list, which is what rebinding the
// property to a different box does: the old box is no longer that property's
// storage, so writes through the names still holding it stop being checked
// against a declaration it no longer answers to.
func (r *Ref) DropSource(class, name string) {
	if r == nil {
		return
	}
	kept := r.sources[:0]
	for _, have := range r.sources {
		if have.Class == class && have.Name == name {
			continue
		}
		kept = append(kept, have)
	}
	r.sources = kept
}

// DropSourceOwned takes one object's claim off the list, leaving any other
// object's claim on the same property in place. This is what freeing an object
// does: `zend_ref_del_type_source` removes one source, not every source that
// happens to name the same declaration.
func (r *Ref) DropSourceOwned(owner any, class, name string) {
	if r == nil {
		return
	}
	kept := r.sources[:0]
	for _, have := range r.sources {
		if have.Owner == owner && have.Class == class && have.Name == name {
			continue
		}
		kept = append(kept, have)
	}
	r.sources = kept
}

// Sources lists the typed properties this slot is shared with.
func (r *Ref) Sources() []RefSource {
	if r == nil {
		return nil
	}
	return r.sources
}

// NewRef boxes a value so it can be shared.
func NewRef(v Value) *Ref { return &Ref{value: v, bindings: 1} }

// Get reads through the reference.
func (r *Ref) Get() Value {
	if r == nil {
		return Undef()
	}
	// A reference to a reference collapses: PHP has no double indirection.
	return r.value.Deref()
}

// Set writes through the reference, which every sharer observes.
func (r *Ref) Set(v Value) {
	if r == nil {
		return
	}
	r.value = v.Deref()
}

// Slot exposes the storage a reference shares, so a writer mutates it **in
// place** rather than copying the value out and back.
//
// The copy-back it replaces was load-bearing, not ceremony: writing to
// `$r[0]` where `$r = &$u` and `$u` is unset autovivifies an array into the
// copy, and only the write-back made `$u` see it. A pointer has that property
// without the step, which is the point -- a required write-back is exactly the
// kind of thing a later refactor drops, and its absence is invisible until
// someone autovivifies through a reference.
//
// A reference to a reference collapses, as `Get` does: PHP has no double
// indirection.
func (r *Ref) Slot() *Value {
	if r == nil {
		return nil
	}
	if inner, ok := r.value.RefOf(); ok {
		return inner.Slot()
	}
	return &r.value
}

// MakeRef wraps a reference as a value, so a slot or array element can hold it.
func MakeRef(r *Ref) Value { return Value{kind: KindRef, ptr: unsafe.Pointer(r)} }

// Shared marks the reference inside a value as one the *program* asked for --
// an explicit `&` -- and hands the value back.
//
// php2go boxes a slot into a reference for its own reasons too: descending into
// a property to write through it, or fetching a variable for a write, both
// produce a box that no PHP program can observe. var_dump's `&` is about the
// former and never about the latter, so the two have to be told apart at the
// point the box is made rather than by looking at it afterwards.
func Shared(v Value) Value {
	if r, ok := v.RefOf(); ok {
		r.shared++
	}
	return v
}

// AddBinding records one durable PHP storage slot added by an explicit &.
// This is diagnostic accounting only; Shared remains the execution-lifetime
// marker and includes php2go's implementation-only eager array copies.
func (r *Ref) AddBinding() {
	if r != nil {
		r.bindings++
	}
}

// shareCopy retains a reference because php2go eagerly copied an array. It is
// relevant to runtime lifetime and var_dump's shared marker, but is not a new
// PHP & binding and therefore does not contribute to debug_zval_dump.
func shareCopy(v Value) Value {
	if r, ok := v.RefOf(); ok {
		r.shared++
	}
	return v
}

// Unshare records that one of a reference's program-made bindings has gone --
// a local holding it went out of scope.
func Unshare(v Value) {
	if r, ok := v.RefOf(); ok && r.shared > 0 {
		r.shared--
		if r.bindings > 0 {
			r.bindings--
		}
	}
}

// DropElementRefs records that an array has gone as a holder of the references
// among its elements: the compiler proved the local holding it dies with the
// frame and never let it go anywhere else (see refIteratedLocals in gogen).
//
// **Only an array no other header shares.** One that does is still held, and
// its elements with it, so nothing has gone.
func DropElementRefs(v Value) {
	held := v.Deref()
	if held.kind != KindArray {
		return
	}
	a := held.a()
	if a == nil || a.arrayBody == nil || a.arrayBody.shares > 0 {
		return
	}
	for _, raw := range a.arrayBody.vals {
		Unshare(raw)
	}
}

// IsShared reports whether the program asked for this reference with an
// explicit `&`, as opposed to the runtime boxing a slot to write through it.
func (r *Ref) IsShared() bool { return r != nil && r.shared > 0 }

// RefCount is the number of durable PHP storage slots bound to this reference.
func (r *Ref) RefCount() int {
	if r == nil {
		return 0
	}
	return r.bindings
}

// SharedRef reports whether a value holds a reference the program asked for.
func SharedRef(v Value) bool {
	r, ok := v.RefOf()
	return ok && r != nil && r.shared > 0
}

// RefOf returns the box a value holds, if it is a reference.
func (v Value) RefOf() (*Ref, bool) {
	if v.kind != KindRef {
		return nil, false
	}
	return v.r(), true
}

// Deref reads through any number of references and yields the plain value.
// Reading a variable in PHP is always a dereference, so almost every consumer
// wants this rather than the raw slot.
func (v Value) Deref() Value {
	for v.kind == KindRef {
		if v.r() == nil {
			return Undef()
		}
		v = v.r().value
	}
	return v
}

// IsRef reports whether a slot holds a reference rather than a plain value.
func (v Value) IsRef() bool { return v.kind == KindRef }

// refIDKey is the per-process salt ReflectionReference::getId hashes with,
// as the engine's REFLECTION_KEY is: an id is stable within a request and
// says nothing about the address.
var refIDKey = func() [16]byte {
	var k [16]byte
	_, _ = cryptorand.Read(k[:])
	return k
}()

// RefID is ReflectionReference::getId for the box r: a 20-byte SHA-1, the
// same for every array element bound to one reference (probed) and
// different for another.
func RefID(r *Ref) string {
	h := sha1.New()
	var p [8]byte
	binary.LittleEndian.PutUint64(p[:], uint64(uintptr(unsafe.Pointer(r))))
	h.Write(p[:])
	h.Write(refIDKey[:])
	return string(h.Sum(nil))
}
