package php

// Declared properties held in a per-class Go struct.
//
// The engine keeps an object's declared properties in a fixed table indexed by
// offset (`properties_table`) and only dynamic ones in a hash. Compiled code
// does the same: each compiled class gets a Go struct holding its declared
// properties, the parent's first, and an instance is allocated as that struct.
// `Slots` points into it, `Layout` says which property each slot is, and
// `Props` holds only what was created at run time. Compiled code reaches a
// property it has proved plain as a constant index into the struct, with no
// lookup at all.
//
// Everything else reaches properties through the accessors below, which
// present one ordered table -- the slots in layout order, then `Props` --
// exactly as `Props` alone presented it before. An object with no slots (every
// interpreter object, and any compiled one built some other way) is in
// **table mode**, and every accessor is then the `Props` operation it mirrors.
//
// A consumer that needs the properties as a live `*Array` -- `array_walk` over
// an object, `ArrayObject` wrapping one, `current()` on one -- calls Table,
// which **demotes** the object: its slots move into `Props` and it stays in
// table mode from then on. Compiled fast paths check for slots and fall back,
// so demotion only ever costs speed.

// Layout is a compiled class's declared-property layout.
type Layout struct {
	// Keys is the key of each slot, as Props would have keyed the property --
	// a shadowed private under its mangled name.
	Keys  []Key
	index map[string]int
	// alloc makes a fresh instance's storage; clone needs one.
	alloc func() *Object
}

// NewLayout describes a class whose instances alloc makes. keys are in slot
// order.
func NewLayout(keys []string, alloc func() *Object) *Layout {
	l := &Layout{Keys: make([]Key, len(keys)), index: make(map[string]int, len(keys)), alloc: alloc}
	for i, k := range keys {
		l.Keys[i] = StringKey(k)
		l.index[k] = i
	}
	return l
}

// Slot is the index of a property in the layout, for the emitter's tables.
func (l *Layout) Slot(key string) (int, bool) {
	i, ok := l.index[key]
	return i, ok
}

// Structured reports an object whose declared properties are in slots.
func (o *Object) Structured() bool { return o != nil && o.Slots != nil }

func (o *Object) slot(key Key) (int, bool) {
	if o == nil || o.Slots == nil || key.IsInt {
		return 0, false
	}
	i, ok := o.Layout.index[key.Str]
	return i, ok
}

// Table is the object's properties as one live array, demoting the object to
// table mode first. For a consumer that must write through an *Array.
func (o *Object) Table() *Array {
	if o == nil {
		return nil
	}
	if o.Slots != nil {
		o.demote()
	}
	return o.dyn()
}

func (o *Object) demote() {
	table := NewArray()
	for i, key := range o.Layout.Keys {
		table.Bind(key, o.Slots[i])
	}
	if o.Props != nil {
		o.Props.EachRaw(func(k Key, raw Value) bool {
			table.Bind(k, raw)
			return true
		})
	}
	o.Props, o.Slots, o.Layout = table, nil, nil
}

// View is the object's properties as an array to read: Props itself in table
// mode, otherwise a fresh array holding what each slot holds, box included.
// Writing to it does not reach the object.
func (o *Object) View() *Array {
	if o == nil {
		return nil
	}
	if o.Slots == nil {
		return o.Props
	}
	view := NewArray()
	o.PropEachRaw(func(k Key, raw Value) bool {
		view.Bind(k, raw)
		return true
	})
	return view
}

// dyn is the table of dynamic properties, made on first need. A structured
// object starts without one: its declared properties are in its struct, and
// most objects never get a dynamic property, so an empty table per `new` was
// a second and third allocation for nothing.
func (o *Object) dyn() *Array {
	if o.Props == nil {
		o.Props = NewArray()
	}
	return o.Props
}

// PropGet is Props.Get: the value, read through a reference.
func (o *Object) PropGet(key Key) (Value, bool) {
	if i, ok := o.slot(key); ok {
		return o.Slots[i].Deref(), true
	}
	if o == nil || o.Props == nil {
		return Undef(), false
	}
	return o.Props.Get(key)
}

// PropRaw is Props.Raw: what the property holds, box included.
func (o *Object) PropRaw(key Key) (Value, bool) {
	if i, ok := o.slot(key); ok {
		return o.Slots[i], true
	}
	if o == nil || o.Props == nil {
		return Undef(), false
	}
	return o.Props.Raw(key)
}

// PropSet is Props.Set: a write through a reference the property holds.
func (o *Object) PropSet(key Key, value Value) {
	if i, ok := o.slot(key); ok {
		if ref, isRef := o.Slots[i].RefOf(); isRef && !value.IsRef() {
			ref.Set(value)
			return
		}
		o.Slots[i] = value
		return
	}
	o.dyn().Set(key, value)
}

// PropBind is Props.Bind: the property holds value outright.
func (o *Object) PropBind(key Key, value Value) {
	if i, ok := o.slot(key); ok {
		o.Slots[i] = value
		return
	}
	o.dyn().Bind(key, value)
}

// PropRebind is Props.Rebind.
func (o *Object) PropRebind(key Key, value Value) bool {
	if i, ok := o.slot(key); ok {
		o.Slots[i] = value
		return true
	}
	return o.dyn().Rebind(key, value)
}

// PropRefAt is Props.RefAt: the property boxed in place.
func (o *Object) PropRefAt(key Key) *Ref {
	if i, ok := o.slot(key); ok {
		if ref, isRef := o.Slots[i].RefOf(); isRef {
			return ref
		}
		ref := NewRef(o.Slots[i])
		o.Slots[i] = MakeRef(ref)
		return ref
	}
	return o.dyn().RefAt(key)
}

// PropRefHeld is Props.RefHeld.
func (o *Object) PropRefHeld(key Key) *Ref {
	if i, ok := o.slot(key); ok {
		if ref, isRef := o.Slots[i].RefOf(); isRef {
			return ref
		}
		return nil
	}
	if o == nil {
		return nil
	}
	return o.Props.RefHeld(key)
}

// PropCollapseUnsharedRef is Props.CollapseUnsharedRef.
func (o *Object) PropCollapseUnsharedRef(key Key, expected *Ref) {
	if i, ok := o.slot(key); ok {
		if expected == nil || expected.IsShared() {
			return
		}
		if held, isRef := o.Slots[i].RefOf(); isRef && held == expected {
			o.Slots[i] = expected.Get()
		}
		return
	}
	o.dyn().CollapseUnsharedRef(key, expected)
}

// PropUnset is Props.Unset. A declared slot cannot be removed from a struct,
// so an object asked to lose one is demoted first.
func (o *Object) PropUnset(key Key) bool {
	if _, ok := o.slot(key); ok {
		o.demote()
	}
	return o.dyn().Unset(key)
}

// PropEachRaw is Props.EachRaw over slots and then dynamic properties.
func (o *Object) PropEachRaw(fn func(Key, Value) bool) {
	if o == nil {
		return
	}
	if o.Slots != nil {
		for i, key := range o.Layout.Keys {
			if !fn(key, o.Slots[i]) {
				return
			}
		}
	}
	if o.Props != nil {
		o.Props.EachRaw(fn)
	}
}

// PropEach is Props.Each: values read through references.
func (o *Object) PropEach(fn func(Key, Value) bool) {
	o.PropEachRaw(func(k Key, raw Value) bool { return fn(k, raw.Deref()) })
}

// PropKeys is Props.Keys.
func (o *Object) PropKeys() []Key {
	var keys []Key
	o.PropEachRaw(func(k Key, _ Value) bool {
		keys = append(keys, k)
		return true
	})
	return keys
}

// PropLen is Props.Len.
func (o *Object) PropLen() int {
	if o == nil {
		return 0
	}
	return len(o.Slots) + o.Props.Len()
}

// cloneSlots gives a clone its own struct: the original's slots copied the
// way Props.Clone copies elements, through the same rule, so a reference two
// names hold stays shared and anything else is flattened.
func cloneSlots(o, c *Object) {
	fresh := o.Layout.alloc()
	held := NewArray()
	for i, key := range o.Layout.Keys {
		held.Bind(key, o.Slots[i])
	}
	copied := held.Clone()
	for i, key := range o.Layout.Keys {
		v, _ := copied.Raw(key)
		fresh.Slots[i] = v
	}
	c.Slots, c.Layout, c.fresh = fresh.Slots, o.Layout, fresh
}
