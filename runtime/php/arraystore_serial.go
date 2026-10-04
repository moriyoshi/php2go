package php

import "strings"

// ArrayObject's and ArrayIterator's flags, storage replacement and
// serialization (spl_array.c): getFlags/setFlags, exchangeArray, the
// `__serialize` / `__unserialize` pair and the older Serializable format.
//
// What the store does not model is refused where it would show: an iterator
// class other than ArrayIterator, ARRAY_AS_PROPS set through these methods,
// and storage that is another ArrayObject or ArrayIterator, whose own
// storage the engine would share (SPL_ARRAY_USE_OTHER).

// arrayStoreOf is the store an object of these classes has, made empty as
// the engine's create handler makes it when no constructor ran.
func (c *Ctx) arrayStoreOf(object *Object) (*ArrayStore, bool) {
	if store, ok := AsArrayStore(object); ok {
		return store, true
	}
	if !c.IsArrayBackedClass(object.ClassName) {
		return nil, false
	}
	NewArrayStore(object, nil)
	store, _ := AsArrayStore(object)
	return store, true
}

// arraySerialFlags is what the store's serialization writes for its flags.
func (s *ArrayStore) serialFlags(self *Object) int64 {
	flags := s.flags & arrayCloneMask
	if s.owner == self && self != nil {
		flags |= arrayIsSelf
	}
	return flags
}

// setStorage is spl_array_set_array for exchangeArray and unserialization:
// an array is copied, an object's property table becomes the storage.
func (c *Ctx) setStorage(object *Object, store *ArrayStore, storage Value, method string) error {
	storage = storage.Deref()
	if storage.Kind() == KindArray {
		items := NewArray()
		storage.Array().Each(func(k Key, v Value) bool {
			items.Set(k, v.Deref().Copy())
			return true
		})
		store.items, store.owner = items, nil
		return nil
	}
	other := storage.Object()
	switch other.Internal.(type) {
	case nil, *SplStorage, *SplDLL, *SplHeapStore, *Generator:
		// The standard property table, which the store walks (probed:
		// SplObjectStorage, SplStack, a heap and a Generator all give their
		// properties).
	case *ArrayStore:
		return Unsupported("%s with another ArrayObject or ArrayIterator as storage", method)
	case *SplFixed, *WeakMapStore:
		return RaiseAt(c, "InvalidArgumentException", arrayOverloadMsg, DisplayName(other.ClassName), DisplayName(object.ClassName))
	default:
		return Unsupported("%s with a %s as storage", method, DisplayName(other.ClassName))
	}
	if other.Lazy != nil {
		return Unsupported("%s with a lazy object as storage", method)
	}
	store.items, store.owner = other.Table(), other
	return nil
}

func (c *Ctx) arrayStoreSerialMethod(object *Object, name string, args []Value) (Value, bool, error) {
	owner := c.arrayStoreOwner(object.ClassName)
	switch name {
	case "__construct":
		if len(args) > 2 && owner == "ArrayObject" && !strings.EqualFold(args[2].Deref().String(), "ArrayIterator") {
			return Value{}, true, Unsupported("ArrayObject with an iterator class other than ArrayIterator")
		}
		return Value{}, false, nil
	case "getflags", "setflags", "exchangearray", "__serialize", "__unserialize", "serialize", "unserialize":
	default:
		return Value{}, false, nil
	}
	store, ok := c.arrayStoreOf(object)
	if !ok {
		return Value{}, false, nil
	}
	spelled := map[string]string{"getflags": "getFlags", "setflags": "setFlags", "exchangearray": "exchangeArray",
		"__serialize": "__serialize", "__unserialize": "__unserialize", "serialize": "serialize", "unserialize": "unserialize"}[name]
	if name == "exchangearray" && owner != "ArrayObject" {
		return Value{}, false, nil
	}
	least, most := 0, 0
	switch name {
	case "setflags", "exchangearray", "__unserialize", "unserialize":
		least, most = 1, 1
	}
	fn := owner + "::" + spelled
	if msg, bad := BuiltinArityError(fn, len(args), least, most); bad {
		return Value{}, true, RaiseAt(c, "ArgumentCountError", "%s", msg)
	}
	switch name {
	case "getflags":
		return Int(store.flags), true, nil
	case "setflags":
		v, err := CoerceInternalArgument(c, args[0].Deref(), "int", fn, 1, "flags", false)
		if err != nil {
			return Value{}, true, err
		}
		if v.Int()&arrayAsProps != 0 {
			return Value{}, true, Unsupported("%s", arrayStoreUnmodel)
		}
		store.flags = v.Int()
		store.stdPropList = store.flags&arrayStdPropList != 0
		return Null(), true, nil
	case "exchangearray":
		given := args[0].Deref()
		if given.Kind() != KindArray && given.Kind() != KindObject {
			return Value{}, true, RaiseAt(c, "TypeError", "%s(): Argument #1 ($array) must be of type array, %s given", fn, ValueName(given))
		}
		old := NewArray()
		store.items.Each(func(k Key, v Value) bool {
			old.Set(k, v.Deref().Copy())
			return true
		})
		if err := c.setStorage(object, store, given, fn+"()"); err != nil {
			return Value{}, true, err
		}
		return Arr(old), true, nil
	case "__serialize":
		if splUninitialized(object) {
			return Value{}, true, Unsupported("%s() of an object with an uninitialized property", fn)
		}
		out := NewArray()
		out.Append(Int(store.serialFlags(object)))
		out.Append(c.arrayStorageValue(object, store))
		out.Append(Arr(splPropsTable(object)))
		out.Append(Null())
		return Arr(out), true, nil
	case "__unserialize":
		return c.arrayUnserializeArray(object, store, args[0], fn)
	case "serialize":
		return c.arraySerialize(object, store)
	case "unserialize":
		return c.arrayUnserializeString(object, store, args[0], fn)
	}
	return Value{}, false, nil
}

// arrayStorageValue is the storage as serialization writes it: null for a
// store over the object itself, the object a store walks, else a copy.
func (c *Ctx) arrayStorageValue(object *Object, store *ArrayStore) Value {
	switch {
	case store.owner == object:
		return Null()
	case store.owner != nil:
		return Obj(store.owner)
	}
	items := NewArray()
	store.items.Each(func(k Key, v Value) bool {
		items.Set(k, v.Deref().Copy())
		return true
	})
	return Arr(items)
}

func (c *Ctx) arrayUnserializeArray(object *Object, store *ArrayStore, arg Value, fn string) (Value, bool, error) {
	data := arg.Deref()
	if data.Kind() != KindArray {
		return Value{}, true, RaiseAt(c, "TypeError", "%s(): Argument #1 ($data) must be of type array, %s given", fn, ValueName(data))
	}
	a := data.Array()
	flags, ok0 := a.Get(IntKey(0))
	storage, ok1 := a.Get(IntKey(1))
	members, ok2 := a.Get(IntKey(2))
	iterClass, ok3 := a.Get(IntKey(3))
	flags, storage, members, iterClass = flags.Deref(), storage.Deref(), members.Deref(), iterClass.Deref()
	if !ok0 || !ok1 || !ok2 || flags.Kind() != KindInt || members.Kind() != KindArray ||
		ok3 && iterClass.Kind() != KindNull && iterClass.Kind() != KindString {
		return Value{}, true, RaiseAt(c, "UnexpectedValueException", "Incomplete or ill-typed serialization data")
	}
	if flags.Int()&arrayAsProps != 0 {
		return Value{}, true, Unsupported("%s", arrayStoreUnmodel)
	}
	if err := c.applyArrayFlags(object, store, flags.Int()); err != nil {
		return Value{}, true, err
	}
	if flags.Int()&arrayIsSelf == 0 {
		if storage.Kind() != KindArray && storage.Kind() != KindObject {
			return Value{}, true, RaiseAt(c, "InvalidArgumentException", "Passed variable is not an array or object")
		}
		if err := c.setStorage(object, store, storage, fn+"()"); err != nil {
			return Value{}, true, err
		}
	}
	if err := LoadMembers(c, object, members.Array(), fn+"()"); err != nil {
		return Value{}, true, err
	}
	if ok3 && iterClass.Kind() == KindString {
		name := iterClass.String()
		if !c.ClassFactsOf(name).Known {
			return Value{}, true, RaiseAt(c, "UnexpectedValueException",
				"Cannot deserialize ArrayObject with iterator class '%s'; no such class exists", name)
		}
		if !strings.EqualFold(name, "ArrayIterator") && !(c.subclassOf != nil && c.subclassOf(name, "Iterator")) {
			return Value{}, true, RaiseAt(c, "UnexpectedValueException",
				"Cannot deserialize ArrayObject with iterator class '%s'; this class does not implement the Iterator interface", name)
		}
		if !strings.EqualFold(name, "ArrayIterator") {
			return Value{}, true, Unsupported("ArrayObject with an iterator class other than ArrayIterator")
		}
	}
	return Null(), true, nil
}

// applyArrayFlags takes a serialized flags word: the clonable bits, and the
// object itself as storage when IS_SELF is set.
func (c *Ctx) applyArrayFlags(object *Object, store *ArrayStore, flags int64) error {
	store.flags = flags & arrayCloneMask &^ arrayIsSelf
	store.stdPropList = store.flags&arrayStdPropList != 0
	if flags&arrayIsSelf != 0 {
		store.items, store.owner = object.Table(), object
	}
	return nil
}

// arraySerialize is the Serializable format: `x:` and the flags, the
// storage and `;` unless the object is its own storage, then `m:` and the
// properties, written as one session.
func (c *Ctx) arraySerialize(object *Object, store *ArrayStore) (Value, bool, error) {
	if splUninitialized(object) {
		return Value{}, true, Unsupported("%s::serialize() of an object with an uninitialized property", c.arrayStoreOwner(object.ClassName))
	}
	w, err := splWriter(c, c.arrayStoreOwner(object.ClassName))
	if err != nil {
		return Value{}, true, err
	}
	var b strings.Builder
	write := func(v Value) error {
		piece, err := w.Write(v)
		b.WriteString(piece)
		return err
	}
	b.WriteString("x:")
	flags := store.serialFlags(object)
	if err := write(Int(flags)); err != nil {
		return Value{}, true, err
	}
	if flags&arrayIsSelf == 0 {
		if err := write(c.arrayStorageValue(object, store)); err != nil {
			return Value{}, true, err
		}
		b.WriteString(";")
	}
	b.WriteString("m:")
	if err := write(Arr(splPropsTable(object))); err != nil {
		return Value{}, true, err
	}
	return String(b.String()), true, nil
}

// arrayUnserializeString reads the Serializable format back.
func (c *Ctx) arrayUnserializeString(object *Object, store *ArrayStore, arg Value, fn string) (Value, bool, error) {
	cv, err := CoerceInternalArgument(c, arg.Deref(), "string", fn, 1, "data", false)
	if err != nil {
		return Value{}, true, err
	}
	buf := cv.String()
	if buf == "" {
		return Null(), true, nil
	}
	r, err := splReader(c, c.arrayStoreOwner(object.ClassName), buf)
	if err != nil {
		return Value{}, true, err
	}
	fail := func(at int) (Value, bool, error) { return Value{}, true, splSerialError(c, at, buf) }
	at := func(i int) byte {
		if i < len(buf) {
			return buf[i]
		}
		return 0
	}
	read := func() (Value, bool, error) {
		v, ok, err := r.Read()
		if err == nil && r.SharedRefs() {
			err = Unsupported("%s() of data holding a reference", fn)
		}
		return v, ok, err
	}
	if at(0) != 'x' {
		return fail(0)
	}
	if at(1) != ':' {
		return fail(1)
	}
	r.Skip(2)
	flags, ok, err := read()
	if err != nil {
		return Value{}, true, err
	}
	if !ok {
		return fail(r.FailOffset())
	}
	if flags.Deref().Kind() != KindInt {
		return fail(r.At())
	}
	r.Skip(-1)
	if at(r.At()) != ';' {
		return fail(r.At())
	}
	r.Skip(1)
	f := flags.Deref().Int()
	if f&arrayAsProps != 0 {
		return Value{}, true, Unsupported("%s", arrayStoreUnmodel)
	}
	if f&arrayIsSelf != 0 {
		if err := c.applyArrayFlags(object, store, f); err != nil {
			return Value{}, true, err
		}
	} else {
		if b := at(r.At()); b != 'a' && b != 'O' && b != 'C' && b != 'r' {
			return fail(r.At())
		}
		storage, ok, err := read()
		if err != nil {
			return Value{}, true, err
		}
		if !ok {
			return fail(r.FailOffset())
		}
		if k := storage.Deref().Kind(); k != KindArray && k != KindObject {
			return fail(r.At())
		}
		if err := c.applyArrayFlags(object, store, f); err != nil {
			return Value{}, true, err
		}
		if err := c.setStorage(object, store, storage, fn+"()"); err != nil {
			return Value{}, true, err
		}
		if at(r.At()) != ';' {
			return fail(r.At())
		}
		r.Skip(1)
	}
	if at(r.At()) != 'm' {
		return fail(r.At())
	}
	if at(r.At()+1) != ':' {
		return fail(r.At() + 1)
	}
	r.Skip(2)
	members, ok, err := read()
	if err != nil {
		return Value{}, true, err
	}
	if !ok {
		return fail(r.FailOffset())
	}
	if members.Deref().Kind() != KindArray {
		return fail(r.At())
	}
	return Null(), true, LoadMembers(c, object, members.Deref().Array(), fn+"()")
}
