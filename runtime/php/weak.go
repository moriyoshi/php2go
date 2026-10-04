package php

import "strings"

// WeakMap and WeakReference.
//
// A weak container holds its key by pointer and takes no strong reference, so
// the entry is meant to disappear when the key dies. **Knowing when that is** is
// the back-end's problem and the two differ: the interpreter can sweep from its
// roots, and compiled output cannot -- it has no refcount yet, so its entries
// persist. That is a wrong answer for a test that drops a key and a right one
// for everything else, and it is the same lifetime gap the whole of D3 is about
// rather than anything particular to weak maps.

// WeakObserve gives the back-end a chance to drop entries whose key has died,
// before a WeakMap answers anything. Optional: a back-end that cannot tell
// installs nothing.
type WeakObserve func(c *Ctx)

// WeakRelease runs whatever a weak container letting go of a value implies --
// a destructor, when that value held the last reference.
type WeakRelease func(c *Ctx, v Value) error

// SetWeakObserve installs the sweep hook.
func (c *Ctx) SetWeakObserve(fn WeakObserve) { c.weakObserve = fn }

// SetWeakRelease installs the release hook.
func (c *Ctx) SetWeakRelease(fn WeakRelease) { c.weakRelease = fn }

func (c *Ctx) observeWeak() {
	if c.weakObserve != nil {
		c.weakObserve(c)
	}
}

func (c *Ctx) releaseWeak(v Value) error {
	if c.weakRelease == nil {
		return nil
	}
	return c.weakRelease(c, v)
}

// WeakClasses are the engine classes this file implements.
var WeakClasses = map[string]bool{"weakmap": true, "weakreference": true}

// WeakEntry is one key/value pair. The key is held by pointer, which is exactly
// the strong reference a real WeakMap does not take.
type WeakEntry struct {
	key   *Object
	value Value
}

// WeakMapStore is the store behind a WeakMap. Insertion order is preserved, since
// both iteration and var_dump expose it.
type WeakMapStore struct {
	entries []WeakEntry
}

// WeakRefStore is the store behind a WeakReference.
type WeakRefStore struct {
	target *Object
}

func newWeakMap(object *Object, _ []Value) { object.Internal = &WeakMapStore{} }

func AsWeakMapStore(object *Object) (*WeakMapStore, bool) {
	if object == nil {
		return nil, false
	}
	held, ok := object.Internal.(*WeakMapStore)
	return held, ok
}

func (m *WeakMapStore) find(key *Object) (int, bool) {
	for i, entry := range m.entries {
		if entry.key == key {
			return i, true
		}
	}
	return 0, false
}

func (m *WeakMapStore) set(key *Object, value Value) {
	if at, found := m.find(key); found {
		m.entries[at].value = value
		return
	}
	m.entries = append(m.entries, WeakEntry{key: key, value: value})
	key.holdWeakly(m)
}

// clone copies the entries into a new store, which names each key weakly as
// the original does. A value holding a reference stays that reference, as the
// engine's hash copy keeps it; anything else is copied.
func (m *WeakMapStore) clone() *WeakMapStore {
	c := &WeakMapStore{entries: make([]WeakEntry, len(m.entries))}
	for i, entry := range m.entries {
		v := entry.value
		if _, ref := v.RefOf(); !ref {
			v = v.Copy()
		}
		c.entries[i] = WeakEntry{key: entry.key, value: v}
		entry.key.holdWeakly(c)
	}
	return c
}

// weakHolder is a weak container that has to let go of an object once it is
// freed: a WeakMap drops the entry keyed by it, a WeakReference its target.
type weakHolder interface{ Drop(dead *Object) }

func (o *Object) holdWeakly(h weakHolder) {
	if o == nil {
		return
	}
	for _, held := range o.weakHolders {
		if held == h {
			return
		}
	}
	o.weakHolders = append(o.weakHolders, h)
}

// HeldWeakly reports whether any weak container names o.
func (o *Object) HeldWeakly() bool { return o != nil && len(o.weakHolders) > 0 }

// ObjectFreed clears every weak container naming o, for a back-end that
// knows the object has just been **freed** -- not merely found unreachable,
// and not while a destructor could still have resurrected it. The engine
// clears weak references on the free path (zend_weakrefs_notify), so a
// WeakMap entry keyed by a freed object is gone at the next read and a
// WeakReference to it answers null.
//
// Only the entry goes: what its value held is let go of by the entry, which
// this does not release -- the value may be held elsewhere.
func ObjectFreed(o *Object) {
	if o == nil || len(o.weakHolders) == 0 {
		return
	}
	holders := o.weakHolders
	o.weakHolders = nil
	for _, h := range holders {
		h.Drop(o)
	}
}

// refAt hands out the slot behind an entry, upgrading it to a reference the
// first time. A by-reference foreach binds the loop variable to this, so a
// write inside the loop reaches the map.
func (m *WeakMapStore) refAt(at int) *Ref {
	ref := m.slotAt(at)
	// The box is made because the program wrote `&`, and the map goes on
	// holding it afterwards -- unlike an array element, whose box flattens once
	// the loop variable moves on. That is why var_dump keeps the `&` on every
	// entry a by-reference loop visited, not only on the last.
	Shared(MakeRef(ref))
	return ref
}

// slotAt is refAt for a write that is not a program `&`: `$map[$o][] = 1` and
// `$map[$o]++` reach the entry itself, because a WeakMap's dimension handler
// answers with the slot rather than with a copy the way an overloaded
// ArrayAccess does. Nothing asked for a reference, so var_dump must not mark
// one.
func (m *WeakMapStore) slotAt(at int) *Ref {
	if at < 0 || at >= len(m.entries) {
		return NewRef(Null())
	}
	if ref, held := m.entries[at].value.RefOf(); held {
		return ref
	}
	ref := NewRef(m.entries[at].value.Deref())
	m.entries[at].value = MakeRef(ref)
	return ref
}

// bindAt puts a box in an entry outright, which is what `$map[$o] = &$x` does:
// the entry stops holding its own value and holds the one the program named.
func (m *WeakMapStore) bindAt(at int, value Value) {
	if at < 0 || at >= len(m.entries) {
		return
	}
	m.entries[at].value = value
}

func (m *WeakMapStore) remove(key *Object) bool {
	at, found := m.find(key)
	if !found {
		return false
	}
	m.entries = append(m.entries[:at:at], m.entries[at+1:]...)
	return true
}

// WeakMapKey resolves the object a subscript names, with the refusal PHP gives for
// anything else. A WeakMap has no integer or string keys at all.
func WeakMapKey(index Value) (*Object, error) {
	object := index.Deref().Object()
	if object == nil {
		return nil, Raise("TypeError", "WeakMap key must be an object")
	}
	return object, nil
}

// weakMethod implements both classes. It sits where arrayStoreMethod does, so
// the ArrayAccess opcodes reach it through the same path.

// WeakMethod implements both classes.
func WeakMethod(c *Ctx, object *Object, name string, args []Value) (Value, bool, error) {
	argument := func(i int) Value {
		if i < len(args) {
			return args[i].Deref()
		}
		return Null()
	}
	if it, ok := object.Internal.(*InternalIteratorStore); ok {
		return it.method(c, name)
	}
	if held, ok := object.Internal.(*WeakRefStore); ok {
		switch strings.ToLower(name) {
		case "get":
			if held.target == nil {
				return Null(), true, nil
			}
			return Obj(held.target), true, nil
		case "__construct":
			// PHP gives WeakReference no usable constructor: it is created
			// through the static factory.
			return Undef(), true, RaiseAt(c, "Error",
				"Direct instantiation of WeakReference is not allowed, use WeakReference::create() instead")
		}
		return Undef(), false, nil
	}
	held, ok := AsWeakMapStore(object)
	if ok {
		// Reading a WeakMap is when its answer has to be right.
		c.observeWeak()
	}
	if !ok {
		return Undef(), false, nil
	}
	switch strings.ToLower(name) {
	case "offsetexists":
		key, err := WeakMapKey(argument(0))
		if err != nil {
			return Undef(), true, err
		}
		at, found := held.find(key)
		return Bool(found && held.entries[at].value.Kind() != KindNull), true, nil

	case "offsetget":
		key, err := WeakMapKey(argument(0))
		if err != nil {
			return Undef(), true, err
		}
		at, found := held.find(key)
		if !found {
			return Undef(), true, RaiseAt(c, "Error", "Object %s#%d not contained in WeakMap",
				DisplayName(key.ClassName), key.ID)
		}
		return held.entries[at].value, true, nil

	case "offsetset":
		if argument(0).Kind() == KindNull && len(args) > 0 &&
			args[0].Deref().Kind() == KindNull {
			// `$map[] = $v` sends a null offset, and a WeakMap has no key to
			// invent for it.
			return Undef(), true, RaiseAt(c, "Error", "Cannot append to WeakMap")
		}
		key, err := WeakMapKey(argument(0))
		if err != nil {
			return Undef(), true, err
		}
		displaced := Undef()
		if at, found := held.find(key); found {
			displaced = held.entries[at].value
		}
		held.set(key, argument(1).Copy())
		// Writing over an entry lets go of what it held, so a destructor there
		// runs at the write rather than at the end of the request -- and what
		// it puts in the map afterwards is in the map.
		if err := c.releaseWeak(displaced); err != nil {
			return Undef(), true, err
		}
		return Null(), true, nil

	case "offsetunset":
		key, err := WeakMapKey(argument(0))
		if err != nil {
			return Undef(), true, err
		}
		removed := Undef()
		if at, found := held.find(key); found {
			removed = held.entries[at].value
		}
		held.remove(key)
		if err := c.releaseWeak(removed); err != nil {
			return Undef(), true, err
		}
		return Null(), true, nil

	case "count":
		return Int(int64(len(held.entries))), true, nil

	case "getiterator":
		// The engine's InternalIterator over the pairs: the walk foreach
		// does (WeakMapTraversal), held by an object the program can step by
		// hand (gh16371). It is final, uncloneable and not serializable.
		// The engine's zend_object_iterator is an object of its own and
		// takes the handle before the InternalIterator does (probed: map #1,
		// getIterator() #3, the next object #4), and it is freed just before
		// the InternalIterator is.
		hidden, ok, err := c.NewInstance("stdClass")
		if err != nil {
			return Undef(), true, err
		}
		if !ok {
			return Undef(), true, Unsupported("WeakMap::getIterator")
		}
		made, ok, err := c.NewInstance("InternalIterator")
		if err != nil {
			return Undef(), true, err
		}
		if !ok {
			return Undef(), true, Unsupported("WeakMap::getIterator")
		}
		walk := NewWeakMapTraversal(held)
		if err := walk.Rewind(c); err != nil {
			return Undef(), true, err
		}
		made.Internal = &InternalIteratorStore{walk: walk, hidden: hidden}
		return Obj(made), true, nil
	}
	return Undef(), false, nil
}

// weakMapDebugInfo is what var_dump shows: a list of pairs, since the keys are
// objects and an array cannot hold one as a key.
func WeakMapDebugInfo(c *Ctx, object *Object) (*Array, bool) {
	held, ok := AsWeakMapStore(object)
	if !ok {
		return nil, false
	}
	c.observeWeak()
	info := NewArray()
	for i, entry := range held.entries {
		pair := NewArray()
		pair.Set(StringKey("key"), Obj(entry.key))
		pair.Set(StringKey("value"), entry.value)
		info.Set(IntKey(int64(i)), Arr(pair))
	}
	return info, true
}

// weakRefDebugInfo is what var_dump shows for a WeakReference: the object it
// points at while that object is still there, and null once it is not.
func WeakRefDebugInfo(object *Object) (*Array, bool) {
	held, ok := object.Internal.(*WeakRefStore)
	if !ok {
		return nil, false
	}
	info := NewArray()
	if held.target != nil {
		info.Set(StringKey("object"), Obj(held.target))
	} else {
		info.Set(StringKey("object"), Null())
	}
	return info, true
}

// newWeakReference is the constructor a program is not allowed to reach: a
// WeakReference is made by WeakReference::create and by nothing else.

// NewWeakMapStore fills the object `new WeakMap()` produces.
func NewWeakMapStore(object *Object) { object.Internal = &WeakMapStore{} }

// NewWeakReferenceFor builds the object `WeakReference::create($o)` hands back.
//
// **One per object**: a second create() of the same object answers the
// WeakReference already made, so `$r === WeakReference::create($o)` is true
// and no second handle is taken (weakmap_multiple_weakrefs). The engine makes
// a new one once the first is destroyed, which this runtime cannot see; a
// WeakReference whose target has been cleared is replaced, since it no
// longer refers to this object.
func NewWeakReferenceFor(c *Ctx, target *Object) (Value, error) {
	if made := c.weakRefs[target]; made != nil {
		if store, ok := made.Internal.(*WeakRefStore); ok && store.target == target {
			return Obj(made), nil
		}
	}
	made, ok, err := c.NewInstance("WeakReference")
	if err != nil || !ok {
		return Value{}, err
	}
	store := &WeakRefStore{target: target}
	made.Internal = store
	target.holdWeakly(store)
	if c.weakRefs == nil {
		c.weakRefs = map[*Object]*Object{}
	}
	c.weakRefs[target] = made
	return Obj(made), nil
}

// WeakMapSlot is the box behind a WeakMap entry for key, when o is a WeakMap
// holding one: what a write through the element reaches.
func WeakMapSlot(o *Object, key Value) (*Ref, bool) {
	held, ok := AsWeakMapStore(o)
	if !ok {
		return nil, false
	}
	k := key.Deref().Object()
	if k == nil {
		return nil, false
	}
	at, found := held.find(k)
	if !found {
		return nil, false
	}
	return held.slotAt(at), true
}

// WeakMapTraversal is `foreach` over a WeakMap: its pairs in insertion order,
// each key the object itself. It holds a **position**, as the engine's hash
// iterator does, not a snapshot: an entry removed while the loop is on it or
// ahead of it is not reached (weakmap_iteration), and a by-reference loop
// binds the entry's own slot, which var_dump then shows as a reference.
type WeakMapTraversal struct {
	m   *WeakMapStore
	pos int
	cur *Object
}

// NewWeakMapTraversal walks one WeakMap.
func NewWeakMapTraversal(m *WeakMapStore) *WeakMapTraversal { return &WeakMapTraversal{m: m} }

// sync puts the position back on the entry the loop is on, which the map may
// have moved by removing one before it; a removed entry leaves the position on
// the one that followed it.
func (t *WeakMapTraversal) sync() {
	if t.cur == nil {
		return
	}
	if at, found := t.m.find(t.cur); found {
		t.pos = at
	}
	t.cur = nil
	if t.pos < len(t.m.entries) {
		t.cur = t.m.entries[t.pos].key
	}
}

func (t *WeakMapTraversal) Rewind(ctx *Ctx) error {
	ctx.observeWeak()
	t.pos, t.cur = 0, nil
	if len(t.m.entries) > 0 {
		t.cur = t.m.entries[0].key
	}
	return nil
}

func (t *WeakMapTraversal) Valid(ctx *Ctx) (bool, error) {
	t.sync()
	return t.pos < len(t.m.entries), nil
}

func (t *WeakMapTraversal) Current(ctx *Ctx) (Value, error) {
	t.sync()
	if t.pos >= len(t.m.entries) {
		return Null(), nil
	}
	return t.m.entries[t.pos].value.Deref(), nil
}

func (t *WeakMapTraversal) Key(ctx *Ctx) (Value, error) {
	t.sync()
	if t.pos >= len(t.m.entries) {
		return Null(), nil
	}
	return Obj(t.m.entries[t.pos].key), nil
}

func (t *WeakMapTraversal) Next(ctx *Ctx) error {
	t.sync()
	t.pos++
	t.cur = nil
	if t.pos < len(t.m.entries) {
		t.cur = t.m.entries[t.pos].key
	}
	return nil
}

// CurrentRef binds the entry's own slot, marked as a reference.
func (t *WeakMapTraversal) CurrentRef(ctx *Ctx) (Value, error) {
	t.sync()
	if t.pos >= len(t.m.entries) {
		return Null(), nil
	}
	return MakeRef(t.m.refAt(t.pos)), nil
}

// Find answers the index of a key, for a caller that holds the store directly.
func (m *WeakMapStore) Find(key *Object) (int, bool) { return m.find(key) }

// RefAt and SlotAt hand out the box behind an entry.
func (m *WeakMapStore) RefAt(at int) *Ref  { return m.refAt(at) }
func (m *WeakMapStore) SlotAt(at int) *Ref { return m.slotAt(at) }

// BindAt replaces an entry's value with a given box.
func (m *WeakMapStore) BindAt(at int, value Value) { m.bindAt(at, value) }

// Sweep drops every entry whose key is not in the reachable set, which is how a
// back-end that can compute reachability makes the map weak. One that cannot
// installs no observer and the entries persist.
func (m *WeakMapStore) Sweep(reachable map[*Object]bool) {
	kept := m.entries[:0]
	for _, entry := range m.entries {
		if reachable[entry.key] {
			kept = append(kept, entry)
		}
	}
	m.entries = kept
}

// Clear drops a WeakReference's target when it is no longer reachable.
func (r *WeakRefStore) Clear(reachable map[*Object]bool) {
	if r.target != nil && !reachable[r.target] {
		r.target = nil
	}
}

// Drop removes the entry for one object, which is what a back-end that learns
// of a single death calls rather than sweeping everything.
func (m *WeakMapStore) Drop(dead *Object) {
	kept := m.entries[:0]
	for _, entry := range m.entries {
		if entry.key != dead {
			kept = append(kept, entry)
		}
	}
	m.entries = kept
}

// DropTarget clears a WeakReference whose target has died.
func (r *WeakRefStore) DropTarget(dead *Object) {
	if r.target == dead {
		r.target = nil
	}
}

// Drop is DropTarget, as a weakHolder.
func (r *WeakRefStore) Drop(dead *Object) { r.DropTarget(dead) }

// Entries hands out the pairs, for a caller walking the map -- a collector
// marking what it holds, or an iterator.
func (m *WeakMapStore) Entries() []WeakEntry { return m.entries }

// Key and Value read one pair.
func (e WeakEntry) Key() *Object { return e.key }
func (e WeakEntry) Value() Value { return e.value }

// Target is what a WeakReference still points at, or nil.
func (r *WeakRefStore) Target() *Object { return r.target }

// NewWeakRefStore is the payload behind a WeakReference, for a back-end that
// makes the object itself.
func NewWeakRefStore(target *Object) *WeakRefStore { return &WeakRefStore{target: target} }

// InternalIteratorStore is the payload of the InternalIterator that
// WeakMap::getIterator returns. Probed: current and key answer NULL past the
// end, next moves on, rewind starts over, an entry removed ahead is skipped,
// and foreach over the iterator rewinds it first -- WeakMapTraversal's
// behaviour, which foreach over the map itself already uses.
type InternalIteratorStore struct {
	walk   *WeakMapTraversal
	hidden *Object
}

// InternalIteratorHidden is the engine iterator object behind an
// InternalIterator, whose handle goes back just before the InternalIterator's.
func InternalIteratorHidden(o *Object) *Object {
	if it, ok := o.Internal.(*InternalIteratorStore); ok {
		return it.hidden
	}
	return nil
}

func (it *InternalIteratorStore) method(c *Ctx, name string) (Value, bool, error) {
	switch strings.ToLower(name) {
	case "current":
		v, err := it.walk.Current(c)
		return v, true, err
	case "key":
		v, err := it.walk.Key(c)
		return v, true, err
	case "next":
		return Null(), true, it.walk.Next(c)
	case "valid":
		ok, err := it.walk.Valid(c)
		return Bool(ok), true, err
	case "rewind":
		return Null(), true, it.walk.Rewind(c)
	case "__construct":
		// Private in the engine; its message names the calling scope, which
		// this runtime does not reach from here.
		return Undef(), true, Unsupported("new InternalIterator")
	}
	return Undef(), false, nil
}
