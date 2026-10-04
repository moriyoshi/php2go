package php

import "strings"

// SplFixedArray and SplObjectStorage, natively: the two SPL containers whose
// state is not properties. Both keep their storage in Object.Internal, made
// on first use when a subclass's constructor never called the parent's --
// the engine allocates it with the object, empty.
//
// What neither can show faithfully is refused rather than approximated:
// the Serializable `serialize()` / `unserialize()` pair, getIterator()'s
// InternalIterator, a subclass that overrides getHash(), and any state
// operation on a subclass carrying non-public properties.

// SplFixed is an SplFixedArray's storage.
type SplFixed struct {
	Vals []Value
}

// SplStorage is an SplObjectStorage's storage, in attachment order, with the
// internal iterator's position.
type SplStorage struct {
	entries []splStorageEntry
	pos     int
}

// GCVisit exposes the values held by SPL's native storage.
func (f *SplFixed) GCVisit(visit func(Value)) bool {
	for _, v := range f.Vals {
		visit(v)
	}
	return true
}

func (s *SplStorage) GCVisit(visit func(Value)) bool {
	for _, entry := range s.entries {
		visit(Obj(entry.obj))
		visit(entry.inf)
	}
	return true
}

type splStorageEntry struct {
	obj *Object
	inf Value
}

func (s *SplStorage) find(o *Object) int {
	for i, e := range s.entries {
		if e.obj == o {
			return i
		}
	}
	return -1
}

func (s *SplStorage) attach(o *Object, inf Value) {
	if i := s.find(o); i >= 0 {
		s.entries[i].inf = inf
		return
	}
	s.entries = append(s.entries, splStorageEntry{obj: o, inf: inf})
}

func (s *SplStorage) detach(o *Object) {
	if i := s.find(o); i >= 0 {
		s.entries = append(s.entries[:i:i], s.entries[i+1:]...)
		if i < s.pos {
			s.pos--
		}
	}
}

// Clone is the engine's clone: the same objects and infos, a list of its own.
func (s *SplStorage) Clone() *SplStorage {
	c := &SplStorage{entries: make([]splStorageEntry, len(s.entries))}
	copy(c.entries, s.entries)
	return c
}

// Clone copies the elements.
func (f *SplFixed) Clone() *SplFixed {
	c := &SplFixed{Vals: make([]Value, len(f.Vals))}
	for i, v := range f.Vals {
		c.Vals[i] = v.Deref().Copy()
	}
	return c
}

func init() {
	splDeclared["SplFixedArray"] = map[string]string{
		"__construct": "__construct", "__wakeup": "__wakeup", "__serialize": "__serialize",
		"__unserialize": "__unserialize", "count": "count", "toarray": "toArray",
		"fromarray": "fromArray", "getsize": "getSize", "setsize": "setSize",
		"offsetexists": "offsetExists", "offsetget": "offsetGet", "offsetset": "offsetSet",
		"offsetunset": "offsetUnset", "getiterator": "getIterator", "jsonserialize": "jsonSerialize",
	}
	splDeclared["SplObjectStorage"] = map[string]string{
		"attach": "attach", "detach": "detach", "contains": "contains", "addall": "addAll",
		"removeall": "removeAll", "removeallexcept": "removeAllExcept", "getinfo": "getInfo",
		"setinfo": "setInfo", "count": "count", "rewind": "rewind", "valid": "valid", "key": "key",
		"current": "current", "next": "next", "seek": "seek", "unserialize": "unserialize",
		"serialize": "serialize", "offsetexists": "offsetExists", "offsetget": "offsetGet",
		"offsetset": "offsetSet", "offsetunset": "offsetUnset", "gethash": "getHash",
		"__serialize": "__serialize", "__unserialize": "__unserialize", "__debuginfo": "__debugInfo",
	}
	SplClasses = append(SplClasses, "SplFixedArray", "SplObjectStorage")
}

// SplFixedOf is an SplFixedArray's storage, made empty if the object has none.
func SplFixedOf(o *Object) *SplFixed {
	if f, ok := o.Internal.(*SplFixed); ok && f != nil {
		return f
	}
	f := &SplFixed{}
	o.Internal = f
	return f
}

// SplStorageOf is an SplObjectStorage's storage, made empty if it has none.
func SplStorageOf(o *Object) *SplStorage {
	if s, ok := o.Internal.(*SplStorage); ok && s != nil {
		return s
	}
	s := &SplStorage{}
	o.Internal = s
	return s
}

// splHasNonPublic reports a property whose serialized or dumped name this
// file would have to mangle, which it does not do.
func splHasNonPublic(o *Object) bool {
	found := false
	o.View().Each(func(k Key, _ Value) bool {
		if d := o.Declared[k.String()]; !k.IsInt && d.Visibility != "" && d.Visibility != "public" && !o.Dynamic[k.String()] {
			found = true
			return false
		}
		return true
	})
	return found
}

// fixedIndex is spl_offset_convert_to_long plus the range check.
func fixedIndex(ctx *Ctx, o *Object, f *SplFixed, key Value, checkRange bool) (int, error) {
	key = key.Deref()
	var idx int64
	switch key.Kind() {
	case KindInt:
		idx = key.Int()
	case KindString:
		k := ArrayKey(nil, key)
		if !k.IsInt {
			return 0, RaiseAt(ctx, "TypeError", "Cannot access offset of type string on %s", DisplayName(o.ClassName))
		}
		idx = k.Int
	case KindFloat, KindTrue, KindFalse:
		k := ArrayKey(ctx, key)
		if err := ctx.TakePendingError(); err != nil {
			return 0, err
		}
		idx = k.Int
	case KindObject, KindArray, KindNull, KindUndef:
		return 0, RaiseAt(ctx, "TypeError", "Cannot access offset of type %s on %s", TypeName(key), DisplayName(o.ClassName))
	default:
		return 0, Unsupported("an SplFixedArray offset of type %s", TypeName(key))
	}
	if checkRange && (idx < 0 || idx >= int64(len(f.Vals))) {
		return 0, RaiseAt(ctx, "OutOfBoundsException", "Index invalid or out of range")
	}
	if idx < 0 || idx >= int64(len(f.Vals)) {
		return -1, nil
	}
	return int(idx), nil
}

func splSize(ctx *Ctx, function string, v Value) (int, error) {
	c, err := CoerceInternalArgument(ctx, v.Deref(), "int", function, 1, "size", false)
	if err != nil {
		return 0, err
	}
	if c.Int() < 0 {
		return 0, RaiseAt(ctx, "ValueError", "%s(): Argument #1 ($size) must be greater than or equal to 0", function)
	}
	return int(c.Int()), nil
}

func fixedToArray(f *SplFixed) *Array {
	out := NewArray()
	for _, v := range f.Vals {
		out.Append(v.Deref().Copy())
	}
	return out
}

func fixedMethod(ctx *Ctx, o *Object, folded, spelled string, args []Value, h SplHooks) (Value, error) {
	least, most := 0, 0
	switch folded {
	case "__construct":
		most = 1
	case "offsetexists", "offsetget", "offsetunset", "setsize", "__unserialize":
		least, most = 1, 1
	case "offsetset":
		least, most = 2, 2
	case "fromarray":
		least, most = 1, 2
	}
	if err := splArity(ctx, "SplFixedArray", spelled, args, least, most); err != nil {
		return Value{}, err
	}
	if folded == "fromarray" {
		return Value{}, Unsupported("SplFixedArray::fromArray() called on an instance")
	}
	f := SplFixedOf(o)
	switch folded {
	case "__construct":
		n := 0
		if len(args) > 0 && !args[0].IsUndef() {
			var err error
			if n, err = splSize(ctx, "SplFixedArray::__construct", args[0]); err != nil {
				return Value{}, err
			}
		}
		// A second construction of a live array is a no-op in the engine
		// only when the size is unchanged; not modelled.
		if len(f.Vals) != 0 {
			return Value{}, Unsupported("SplFixedArray::__construct() on a constructed array")
		}
		f.Vals = make([]Value, n)
		for i := range f.Vals {
			f.Vals[i] = Null()
		}
		return Null(), nil
	case "count", "getsize":
		return Int(int64(len(f.Vals))), nil
	case "toarray", "jsonserialize":
		return Arr(fixedToArray(f)), nil
	case "setsize":
		n, err := splSize(ctx, "SplFixedArray::setSize", args[0])
		if err != nil {
			return Value{}, err
		}
		for len(f.Vals) < n {
			f.Vals = append(f.Vals, Null())
		}
		f.Vals = f.Vals[:n:n]
		return Bool(true), nil
	case "offsetexists":
		i, err := fixedIndex(ctx, o, f, args[0], false)
		if err != nil {
			return Value{}, err
		}
		return Bool(i >= 0 && f.Vals[i].Deref().Kind() != KindNull), nil
	case "offsetget":
		i, err := fixedIndex(ctx, o, f, args[0], true)
		if err != nil {
			return Value{}, err
		}
		return f.Vals[i].Deref(), nil
	case "offsetset":
		if args[0].Deref().Kind() == KindNull {
			return Value{}, RaiseAt(ctx, "Error", "[] operator not supported for %s", DisplayName(o.ClassName))
		}
		i, err := fixedIndex(ctx, o, f, args[0], true)
		if err != nil {
			return Value{}, err
		}
		f.Vals[i] = args[1].Deref().Copy()
		return Null(), nil
	case "offsetunset":
		i, err := fixedIndex(ctx, o, f, args[0], true)
		if err != nil {
			return Value{}, err
		}
		f.Vals[i] = Null()
		return Null(), nil
	case "getiterator":
		return Value{}, Unsupported("SplFixedArray::getIterator() (InternalIterator)")
	case "__wakeup":
		ctx.Deprecated("Method SplFixedArray::__wakeup() is deprecated since 8.4, this method is obsolete, as serialization hooks are provided by __unserialize() and __serialize()")
		if err := ctx.TakePendingError(); err != nil {
			return Value{}, err
		}
		return Null(), nil
	case "__serialize":
		if o.View().Len() > 0 {
			return Value{}, Unsupported("serializing an %s with properties", DisplayName(o.ClassName))
		}
		return Arr(fixedToArray(f)), nil
	case "__unserialize":
		data := args[0].Deref()
		if data.Kind() != KindArray {
			return Value{}, RaiseAt(ctx, "TypeError", "SplFixedArray::__unserialize(): Argument #1 ($data) must be of type array, %s given", ValueName(data))
		}
		if len(f.Vals) != 0 {
			return Null(), nil
		}
		var refused error
		data.Array().Each(func(k Key, v Value) bool {
			if !k.IsInt {
				refused = Unsupported("SplFixedArray::__unserialize() with properties")
				return false
			}
			f.Vals = append(f.Vals, v.Deref().Copy())
			return true
		})
		return Null(), refused
	}
	return Value{}, ThrowUndefinedMethod(o.ClassName, spelled)
}

// SplFixedFromArray is SplFixedArray::fromArray(); it always makes an
// SplFixedArray, whatever class it was called through (probed).
func SplFixedFromArray(ctx *Ctx, args []Value, h SplHooks) (Value, error) {
	bound, err := BindNamedArgs(ctx, args, []string{"array", "preserveKeys"}, false)
	if err != nil {
		return Value{}, err
	}
	bound = UnboxArgs(bound)
	if err := splArity(ctx, "SplFixedArray", "fromArray", bound, 1, 2); err != nil {
		return Value{}, err
	}
	src := bound[0].Deref()
	if src.Kind() != KindArray {
		return Value{}, RaiseAt(ctx, "TypeError", "SplFixedArray::fromArray(): Argument #1 ($array) must be of type array, %s given", ValueName(src))
	}
	preserve := true
	if len(bound) > 1 && !bound[1].IsUndef() {
		c, err := CoerceInternalArgument(ctx, bound[1].Deref(), "bool", "SplFixedArray::fromArray", 2, "preserveKeys", false)
		if err != nil {
			return Value{}, err
		}
		preserve = c.Bool()
	}
	f := &SplFixed{}
	if preserve {
		max := int64(-1)
		var bad bool
		src.Array().Each(func(k Key, _ Value) bool {
			if !k.IsInt || k.Int < 0 {
				bad = true
				return false
			}
			if k.Int > max {
				max = k.Int
			}
			return true
		})
		if bad {
			return Value{}, RaiseAt(ctx, "InvalidArgumentException", "array must contain only positive integer keys")
		}
		f.Vals = make([]Value, max+1)
		for i := range f.Vals {
			f.Vals[i] = Null()
		}
		src.Array().Each(func(k Key, v Value) bool {
			f.Vals[k.Int] = v.Deref().Copy()
			return true
		})
	} else {
		src.Array().Each(func(_ Key, v Value) bool {
			f.Vals = append(f.Vals, v.Deref().Copy())
			return true
		})
	}
	o := h.NewObject("SplFixedArray")
	o.Internal = f
	return Obj(o), nil
}

// SplFixedTraversal walks an SplFixedArray by index, live: a resize or a
// write during the loop is seen, as the engine's iterator sees it.
type SplFixedTraversal struct {
	o   *Object
	pos int
}

// NewSplFixedTraversal walks one SplFixedArray; ok is false for any other
// object.
func NewSplFixedTraversal(o *Object) (*SplFixedTraversal, bool) {
	if _, ok := o.Internal.(*SplFixed); !ok {
		if o.Internal != nil {
			return nil, false
		}
	}
	return &SplFixedTraversal{o: o}, true
}

func (t *SplFixedTraversal) Rewind(ctx *Ctx) error { t.pos = 0; return nil }
func (t *SplFixedTraversal) Valid(ctx *Ctx) (bool, error) {
	return t.pos < len(SplFixedOf(t.o).Vals), nil
}
func (t *SplFixedTraversal) Current(ctx *Ctx) (Value, error) {
	f := SplFixedOf(t.o)
	if t.pos >= len(f.Vals) {
		return Null(), nil
	}
	return f.Vals[t.pos].Deref(), nil
}
func (t *SplFixedTraversal) Key(ctx *Ctx) (Value, error) { return Int(int64(t.pos)), nil }
func (t *SplFixedTraversal) Next(ctx *Ctx) error         { t.pos++; return nil }

func storageObjectArg(ctx *Ctx, spelled, name string, v Value) (*Object, error) {
	v = v.Deref()
	if v.Kind() != KindObject {
		return nil, RaiseAt(ctx, "TypeError", "SplObjectStorage::%s(): Argument #1 ($%s) must be of type object, %s given",
			spelled, name, ValueName(v))
	}
	return v.Object(), nil
}

func storageMethod(ctx *Ctx, o *Object, folded, spelled string, args []Value, h SplHooks) (Value, error) {
	least, most := 0, 0
	switch folded {
	case "detach", "contains", "addall", "removeall", "removeallexcept", "setinfo", "seek",
		"offsetexists", "offsetget", "offsetunset", "gethash", "__unserialize", "unserialize":
		least, most = 1, 1
	case "attach", "offsetset":
		least, most = 1, 2
	case "count":
		most = 1
	}
	if err := splArity(ctx, "SplObjectStorage", spelled, args, least, most); err != nil {
		return Value{}, err
	}
	// Identity is getHash()'s answer, which a subclass may redefine; this
	// file keys by the object itself.
	if h.Compiled != nil && h.Compiled(o.ClassName, "gethash") {
		return Value{}, Unsupported("an SplObjectStorage subclass that overrides getHash()")
	}
	s := SplStorageOf(o)
	switch folded {
	case "attach", "offsetset":
		name := "object"
		obj, err := storageObjectArg(ctx, spelled, name, args[0])
		if err != nil {
			return Value{}, err
		}
		inf := Null()
		if len(args) > 1 && !args[1].IsUndef() {
			inf = args[1].Deref().Copy()
		}
		s.attach(obj, inf)
		return Null(), nil
	case "detach", "offsetunset":
		obj, err := storageObjectArg(ctx, spelled, "object", args[0])
		if err != nil {
			return Value{}, err
		}
		s.detach(obj)
		return Null(), nil
	case "contains", "offsetexists":
		obj, err := storageObjectArg(ctx, spelled, "object", args[0])
		if err != nil {
			return Value{}, err
		}
		return Bool(s.find(obj) >= 0), nil
	case "offsetget":
		obj, err := storageObjectArg(ctx, spelled, "object", args[0])
		if err != nil {
			return Value{}, err
		}
		i := s.find(obj)
		if i < 0 {
			return Value{}, RaiseAt(ctx, "UnexpectedValueException", "Object not found")
		}
		return s.entries[i].inf, nil
	case "gethash":
		if _, err := storageObjectArg(ctx, spelled, "object", args[0]); err != nil {
			return Value{}, err
		}
		return ctx.Call(String("spl_object_hash"), []Value{args[0].Deref()})
	case "addall", "removeall", "removeallexcept":
		other := args[0].Deref()
		if other.Kind() != KindObject || !h.IsA(other, "SplObjectStorage") {
			return Value{}, RaiseAt(ctx, "TypeError", "SplObjectStorage::%s(): Argument #1 ($storage) must be of type SplObjectStorage, %s given",
				spelled, ValueName(other))
		}
		src := SplStorageOf(other.Object())
		switch folded {
		case "addall":
			for _, e := range append([]splStorageEntry(nil), src.entries...) {
				s.attach(e.obj, e.inf)
			}
		case "removeall":
			for _, e := range append([]splStorageEntry(nil), src.entries...) {
				s.detach(e.obj)
			}
		default:
			for _, e := range append([]splStorageEntry(nil), s.entries...) {
				if src.find(e.obj) < 0 {
					s.detach(e.obj)
				}
			}
		}
		s.pos = 0
		return Int(int64(len(s.entries))), nil
	case "getinfo":
		if s.pos >= len(s.entries) {
			return Null(), nil
		}
		return s.entries[s.pos].inf, nil
	case "setinfo":
		if s.pos < len(s.entries) {
			s.entries[s.pos].inf = args[0].Deref().Copy()
		}
		return Null(), nil
	case "count":
		if len(args) > 0 && !args[0].IsUndef() {
			mode, err := CoerceInternalArgument(ctx, args[0].Deref(), "int", "SplObjectStorage::count", 1, "mode", false)
			if err != nil {
				return Value{}, err
			}
			if mode.Int() != 0 {
				return Value{}, Unsupported("SplObjectStorage::count() with COUNT_RECURSIVE")
			}
		}
		return Int(int64(len(s.entries))), nil
	case "rewind":
		s.pos = 0
		return Null(), nil
	case "valid":
		return Bool(s.pos < len(s.entries)), nil
	case "key":
		return Int(int64(s.pos)), nil
	case "current":
		if s.pos >= len(s.entries) {
			return Value{}, RaiseAt(ctx, "RuntimeException", "Called current() on invalid iterator")
		}
		return Obj(s.entries[s.pos].obj), nil
	case "next":
		if s.pos < len(s.entries) {
			s.pos++
		}
		return Null(), nil
	case "seek":
		c, err := CoerceInternalArgument(ctx, args[0].Deref(), "int", "SplObjectStorage::seek", 1, "offset", false)
		if err != nil {
			return Value{}, err
		}
		if c.Int() < 0 || c.Int() >= int64(len(s.entries)) {
			return Value{}, RaiseAt(ctx, "OutOfBoundsException", "Seek position %d is out of range", c.Int())
		}
		s.pos = int(c.Int())
		return Null(), nil
	case "serialize":
		return storageSerialize(ctx, o, s)
	case "unserialize":
		return storageUnserialize(ctx, o, s, args[0])
	case "__serialize":
		pairs := NewArray()
		for _, e := range s.entries {
			pairs.Append(Obj(e.obj))
			pairs.Append(e.inf)
		}
		out := NewArray()
		out.Append(Arr(pairs))
		out.Append(Arr(splPropsTable(o)))
		return Arr(out), nil
	case "__unserialize":
		data := args[0].Deref()
		if data.Kind() != KindArray {
			return Value{}, RaiseAt(ctx, "TypeError", "SplObjectStorage::__unserialize(): Argument #1 ($data) must be of type array, %s given", ValueName(data))
		}
		pairs, ok1 := data.Array().Get(IntKey(0))
		props, ok2 := data.Array().Get(IntKey(1))
		if !ok1 || !ok2 || pairs.Deref().Kind() != KindArray || props.Deref().Kind() != KindArray {
			return Value{}, RaiseAt(ctx, "UnexpectedValueException", "Incomplete or ill-typed serialization data")
		}
		list := pairs.Deref().Array()
		if list.Len()%2 != 0 {
			return Value{}, RaiseAt(ctx, "UnexpectedValueException", "Odd number of elements")
		}
		var vals []Value
		list.Each(func(_ Key, v Value) bool {
			vals = append(vals, v.Deref())
			return true
		})
		for i := 0; i < len(vals); i += 2 {
			if vals[i].Kind() != KindObject {
				return Value{}, RaiseAt(ctx, "UnexpectedValueException", "Non-object key")
			}
			s.attach(vals[i].Object(), vals[i+1])
		}
		return Null(), LoadMembers(ctx, o, props.Deref().Array(), "SplObjectStorage::__unserialize()")
	case "__debuginfo":
		info, _, err := SplMultipleDebugInfo(o)
		if err != nil {
			return Value{}, err
		}
		return Arr(info), nil
	}
	return Value{}, ThrowUndefinedMethod(o.ClassName, spelled)
}

// SplStoreDebugInfo is what var_dump shows for the two containers here: an
// SplObjectStorage lists its storage after its own properties, an
// SplFixedArray its elements before them. ok is false for other objects.
func SplStoreDebugInfo(o *Object) (*Array, bool, error) {
	switch st := o.Internal.(type) {
	case *SplStorage:
		return SplMultipleDebugInfo(o)
	case *SplFixed:
		if splHasNonPublic(o) {
			return nil, false, Unsupported("var_dump of an %s subclass with non-public properties", DisplayName(o.ClassName))
		}
		info := fixedToArray(st)
		o.View().Each(func(k Key, v Value) bool {
			info.Set(k, v)
			return true
		})
		return info, true, nil
	}
	return nil, false, nil
}

// splFixedCast is `(array)` of an SplFixedArray: its elements, then its
// properties (probed).
func splFixedCast(o *Object) (*Array, bool) {
	f, ok := o.Internal.(*SplFixed)
	if !ok {
		return nil, false
	}
	out := fixedToArray(f)
	o.PropEach(func(k Key, e Value) bool {
		if !e.Deref().IsUndef() {
			out.Set(castKey(o, k), e.Deref().Copy())
		}
		return true
	})
	return out, true
}

// splStoreKind reports the container a class name is, for the dispatch in
// SplMethod.
func splStoreKind(owner string) bool {
	return owner == "SplFixedArray" || owner == "SplObjectStorage"
}

func splStoreMethod(ctx *Ctx, o *Object, owner, folded, spelled string, args []Value, h SplHooks) (Value, error) {
	if strings.EqualFold(owner, "SplFixedArray") {
		return fixedMethod(ctx, o, folded, spelled, args, h)
	}
	return storageMethod(ctx, o, folded, spelled, args, h)
}

// splUninitialized reports a declared property with no value yet, which the
// engine's property table carries as an empty slot this runtime does not
// write the way it does.
func splUninitialized(o *Object) bool {
	found := false
	o.PropEach(func(_ Key, v Value) bool {
		found = v.Deref().IsUndef()
		return !found
	})
	return found
}

// storageSerialize is the Serializable format: `x:` and the count, then each
// object and its info, then `m:` and the properties, written as one session.
func storageSerialize(ctx *Ctx, o *Object, s *SplStorage) (Value, error) {
	if splUninitialized(o) {
		return Value{}, Unsupported("SplObjectStorage::serialize() of an object with an uninitialized property")
	}
	w, err := splWriter(ctx, "SplObjectStorage")
	if err != nil {
		return Value{}, err
	}
	var b strings.Builder
	write := func(v Value) error {
		piece, err := w.Write(v)
		b.WriteString(piece)
		return err
	}
	b.WriteString("x:")
	if err := write(Int(int64(len(s.entries)))); err != nil {
		return Value{}, err
	}
	entries := append([]splStorageEntry(nil), s.entries...)
	for i, e := range entries {
		if err := write(Obj(e.obj)); err != nil {
			return Value{}, err
		}
		b.WriteString(",")
		if err := write(e.inf); err != nil {
			return Value{}, err
		}
		b.WriteString(";")
		if len(s.entries) != len(entries) || s.entries[i].obj != e.obj {
			return Value{}, Unsupported("SplObjectStorage::serialize() of a storage changed while it is written")
		}
	}
	b.WriteString("m:")
	if err := write(Arr(splPropsTable(o))); err != nil {
		return Value{}, err
	}
	return String(b.String()), nil
}

// storageUnserialize reads the Serializable format into the storage, adding
// to what it holds: the engine attaches each object as it reads it, and an
// object already there has its info replaced.
func storageUnserialize(ctx *Ctx, o *Object, s *SplStorage, arg Value) (Value, error) {
	c, err := CoerceInternalArgument(ctx, arg.Deref(), "string", "SplObjectStorage::unserialize", 1, "data", false)
	if err != nil {
		return Value{}, err
	}
	buf := c.String()
	if buf == "" {
		return Null(), nil
	}
	r, err := splReader(ctx, "SplObjectStorage", buf)
	if err != nil {
		return Value{}, err
	}
	fail := func(at int) (Value, error) { return Value{}, splSerialError(ctx, at, buf) }
	at := func(i int) byte {
		if i < len(buf) {
			return buf[i]
		}
		return 0
	}
	if len(buf) < 2 || buf[0] != 'x' || buf[1] != ':' {
		if buf[0] != 'x' {
			return fail(0)
		}
		return fail(1)
	}
	r.Skip(2)
	read := func() (Value, bool, error) {
		v, ok, err := r.Read()
		if err == nil && r.SharedRefs() {
			err = Unsupported("SplObjectStorage::unserialize() of data holding a reference")
		}
		return v, ok, err
	}
	count, ok, err := read()
	if err != nil {
		return Value{}, err
	}
	if !ok {
		return fail(r.FailOffset())
	}
	if count.Deref().Kind() != KindInt {
		return fail(r.At())
	}
	r.Skip(-1)
	if count.Deref().Int() < 0 {
		return fail(r.At())
	}
	for n := count.Deref().Int(); n > 0; n-- {
		if at(r.At()) != ';' {
			return fail(r.At())
		}
		r.Skip(1)
		if b := at(r.At()); b != 'O' && b != 'C' && b != 'r' {
			return fail(r.At())
		}
		entry, ok, err := read()
		if err != nil {
			return Value{}, err
		}
		if !ok {
			return fail(r.FailOffset())
		}
		inf := Null()
		if at(r.At()) == ',' {
			r.Skip(1)
			v, ok, err := read()
			if err != nil {
				return Value{}, err
			}
			if !ok {
				return fail(r.FailOffset())
			}
			inf = v.Deref().Copy()
		}
		if entry.Deref().Kind() != KindObject {
			return fail(r.At())
		}
		if i := s.find(entry.Deref().Object()); i >= 0 {
			if err := splDiscardGap(ctx, s.entries[i].inf, "SplObjectStorage::unserialize()"); err != nil {
				return Value{}, err
			}
		}
		s.attach(entry.Deref().Object(), inf)
	}
	if at(r.At()) != ';' {
		return fail(r.At())
	}
	r.Skip(1)
	if at(r.At()) != 'm' {
		return fail(r.At())
	}
	if at(r.At()+1) != ':' {
		return fail(r.At() + 1)
	}
	r.Skip(2)
	members, ok, err := read()
	if err != nil {
		return Value{}, err
	}
	if !ok {
		return fail(r.FailOffset())
	}
	if members.Deref().Kind() != KindArray {
		return fail(r.At())
	}
	return Null(), LoadMembers(ctx, o, members.Deref().Array(), "SplObjectStorage::unserialize()")
}
