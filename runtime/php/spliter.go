package php

import "strings"

// The SPL iterators this runtime implements natively: EmptyIterator, the
// "dual" family (IteratorIterator, LimitIterator, InfiniteIterator,
// FilterIterator, CallbackFilterIterator) and MultipleIterator.
//
// **Every step is the engine's own**, taken from spl_iterators.c and then
// checked against an inner Iterator whose methods echo: IteratorIterator
// caches the inner iterator's current and key after each move (`fetch`), so
// `valid()` answers from the cache and asks nothing, and LimitIterator's
// rewind reaches its offset by `next()` calls unless the inner iterator is
// a SeekableIterator. A test that counts calls sees the difference, which is
// why none of it is simplified.
//
// Calling back into PHP -- the inner iterator's methods, `accept()` on a
// FilterIterator subclass, the CallbackFilterIterator callback -- needs the
// class table, which lives above this package; SplHooks carries it down.

// SplHooks is what the SPL iterators ask of the layer that has a class table.
type SplHooks struct {
	// Call invokes a method on an object as a PHP call would, frame and all:
	// the engine reaches `accept()` and a MultipleIterator's sub-iterators
	// through zend_call_method.
	Call func(ctx *Ctx, recv Value, name string, args []Value) (Value, error)
	// Inner drives the wrapped iterator one step. It is Call for a user
	// Iterator, and the driver itself for a Generator, which the engine
	// moves through its iterator handlers without a method frame.
	Inner func(ctx *Ctx, recv Value, name string, args []Value) (Value, error)
	// IsA is `instanceof`.
	IsA func(v Value, class string) bool
	// Compiled reports that the program declares a body for class::name
	// (folded), which then answers instead of the engine's.
	Compiled func(class, name string) bool
	// NewObject makes an object of a class with a fresh handle.
	NewObject func(class string) *Object
}

// SplIterator is the state behind IteratorIterator and every class derived
// from it: the engine's spl_dual_it_object.
type SplIterator struct {
	// Inner is what getInnerIterator() answers: the constructor's argument,
	// or what its getIterator() returned.
	Inner Value
	// walk is the Iterator the steps are taken on. It differs from Inner only
	// when Inner is itself an IteratorAggregate, which the engine walks
	// through its own get_iterator.
	walk     Value
	seekable bool
	Offset   int64
	Count    int64
	Callback Value
	Pos      int64
	data     Value
	key      Value
	fetched  bool
}

// SplMultiple is a MultipleIterator: its flags and the sub-iterators in
// attachment order, each with its info.
type SplMultiple struct {
	Flags   int64
	entries []splMultiEntry
}

type splMultiEntry struct {
	it   *Object
	info Value
}

// Clone is the engine's clone of the storage: the same sub-iterators, a list
// of its own.
func (m *SplMultiple) Clone() *SplMultiple {
	c := &SplMultiple{Flags: m.Flags, entries: make([]splMultiEntry, len(m.entries))}
	copy(c.entries, m.entries)
	return c
}

// The MultipleIterator flags, as the engine numbers them.
const (
	MultipleNeedAny     = 0
	MultipleNeedAll     = 1
	MultipleKeysNumeric = 0
	MultipleKeysAssoc   = 2
)

// splParent is `extends` among the classes here.
var splParent = map[string]string{
	"LimitIterator":          "IteratorIterator",
	"InfiniteIterator":       "IteratorIterator",
	"FilterIterator":         "IteratorIterator",
	"CallbackFilterIterator": "FilterIterator",
}

// splDeclared are the methods each class declares itself, folded to the
// engine's spelling. A call is answered by the nearest declaration up the
// chain, which is also the class an arity error names.
var splDeclared = map[string]map[string]string{
	"EmptyIterator": {"current": "current", "next": "next", "key": "key", "valid": "valid", "rewind": "rewind"},
	"IteratorIterator": {"__construct": "__construct", "getinneriterator": "getInnerIterator",
		"rewind": "rewind", "valid": "valid", "key": "key", "current": "current", "next": "next"},
	"FilterIterator":         {"__construct": "__construct", "rewind": "rewind", "next": "next", "accept": "accept"},
	"CallbackFilterIterator": {"__construct": "__construct", "accept": "accept"},
	"LimitIterator": {"__construct": "__construct", "rewind": "rewind", "valid": "valid", "next": "next",
		"seek": "seek", "getposition": "getPosition"},
	"InfiniteIterator": {"__construct": "__construct", "next": "next"},
	"MultipleIterator": {"__construct": "__construct", "getflags": "getFlags", "setflags": "setFlags",
		"attachiterator": "attachIterator", "detachiterator": "detachIterator",
		"containsiterator": "containsIterator", "countiterators": "countIterators",
		"rewind": "rewind", "valid": "valid", "key": "key", "current": "current", "next": "next"},
}

// SplClasses are the class names this file answers, spelled as the engine
// registers them.
var SplClasses = []string{"EmptyIterator", "IteratorIterator", "FilterIterator",
	"CallbackFilterIterator", "LimitIterator", "InfiniteIterator", "MultipleIterator"}

// splOwner is the class whose declaration answers `kind::folded`, and that
// declaration's spelling.
func splOwner(kind, folded string) (string, string) {
	for c := kind; c != ""; c = splParent[c] {
		if spelled, ok := splDeclared[c][folded]; ok {
			return c, spelled
		}
	}
	return "", ""
}

// SplHasMethod reports whether an SPL class answers a method.
func SplHasMethod(kind, name string) bool {
	owner, _ := splOwner(kind, strings.ToLower(name))
	return owner != ""
}

// SplUncloneable reports whether the engine refuses to clone an instance:
// the dual iterators have no clone handler.
func SplUncloneable(kind string) bool {
	switch kind {
	case "", "EmptyIterator", "MultipleIterator", "SplFixedArray", "SplObjectStorage":
		return false
	}
	if splContainerOf(kind) != "" {
		return false
	}
	return true
}

func splArity(ctx *Ctx, owner, spelled string, args []Value, least, most int) error {
	if msg, bad := BuiltinArityError(owner+"::"+spelled, len(args), least, most); bad {
		return RaiseAt(ctx, "ArgumentCountError", "%s", msg)
	}
	return nil
}

// SplConstruct runs the constructor `kind` declares, or inherits, over o.
func SplConstruct(ctx *Ctx, o *Object, kind string, args []Value, h SplHooks) error {
	owner, _ := splOwner(kind, "__construct")
	switch owner {
	case "SplFixedArray":
		_, err := fixedMethod(ctx, o, "__construct", "__construct", UnboxArgs(args), h)
		return err
	case "":
		// EmptyIterator has no constructor: `new EmptyIterator(1)` is fine.
		return nil
	case "MultipleIterator":
		bound, err := BindNamedArgs(ctx, args, []string{"flags"}, false)
		if err != nil {
			return err
		}
		bound = UnboxArgs(bound)
		if err := splArity(ctx, owner, "__construct", bound, 0, 1); err != nil {
			return err
		}
		m := &SplMultiple{Flags: MultipleNeedAll | MultipleKeysNumeric}
		if len(bound) > 0 && !bound[0].IsUndef() {
			v, err := CoerceInternalArgument(ctx, bound[0].Deref(), "int", owner+"::__construct", 1, "flags", false)
			if err != nil {
				return err
			}
			m.Flags = v.Int()
		}
		o.Internal = m
		return nil
	}
	var names []string
	least, most := 1, 1
	switch owner {
	case "IteratorIterator":
		names, most = []string{"iterator", "class"}, 2
	case "LimitIterator":
		names, most = []string{"iterator", "offset", "limit"}, 3
	case "CallbackFilterIterator":
		names, least, most = []string{"iterator", "callback"}, 2, 2
	default:
		names = []string{"iterator"}
	}
	bound, err := BindNamedArgs(ctx, args, names, false)
	if err != nil {
		return err
	}
	bound = UnboxArgs(bound)
	if err := splArity(ctx, owner, "__construct", bound, least, most); err != nil {
		return err
	}
	function := owner + "::__construct"
	inner := bound[0].Deref()
	want := "Iterator"
	if owner == "IteratorIterator" {
		want = "Traversable"
	}
	if inner.Kind() != KindObject || !h.IsA(inner, want) {
		return RaiseAt(ctx, "TypeError", "%s(): Argument #1 ($iterator) must be of type %s, %s given",
			function, want, ValueName(inner))
	}
	s := &SplIterator{Count: -1}
	switch owner {
	case "IteratorIterator":
		if len(bound) > 1 && !bound[1].IsUndef() && bound[1].Deref().Kind() != KindNull {
			// The downcast picks another class's iterator handler, which
			// only differs from the object's own for a subclass that
			// overrides the iteration methods -- not modelled.
			cast := bound[1].Deref()
			if cast.Kind() != KindString || !strings.EqualFold(cast.String(), inner.Object().ClassName) {
				return Unsupported("IteratorIterator::__construct() with a class to downcast to")
			}
		}
		if h.IsA(inner, "IteratorAggregate") {
			got, err := h.Call(ctx, inner, "getIterator", nil)
			if err != nil {
				return err
			}
			inner = got.Deref()
		}
	case "LimitIterator":
		for i, name := range []string{"offset", "limit"} {
			if len(bound) <= i+1 || bound[i+1].IsUndef() {
				continue
			}
			v, err := CoerceInternalArgument(ctx, bound[i+1].Deref(), "int", function, i+2, name, false)
			if err != nil {
				return err
			}
			if i == 0 {
				s.Offset = v.Int()
			} else {
				s.Count = v.Int()
			}
		}
		if s.Offset < 0 {
			return RaiseAt(ctx, "ValueError", "%s(): Argument #2 ($offset) must be greater than or equal to 0", function)
		}
		if s.Count < -1 {
			return RaiseAt(ctx, "ValueError", "%s(): Argument #3 ($limit) must be greater than or equal to -1", function)
		}
	case "CallbackFilterIterator":
		cb := bound[1].Deref()
		if err := ctx.CheckCallable(function, 2, "callback", cb); err != nil {
			return err
		}
		s.Callback = cb
	}
	s.Inner = inner
	// An aggregate is walked through the iterator it hands out, as the
	// engine's get_iterator does -- asked here, at construction, which is
	// when the engine asks.
	walk := inner
	for walk.Kind() == KindObject && !h.IsA(walk, "Iterator") && h.IsA(walk, "IteratorAggregate") {
		got, err := h.Call(ctx, walk, "getIterator", nil)
		if err != nil {
			return err
		}
		got = got.Deref()
		if got.Kind() != KindObject || !h.IsA(got, "Traversable") {
			return Unsupported("an IteratorAggregate whose getIterator() returns no Traversable, under an SPL iterator")
		}
		walk = got
	}
	s.walk = walk
	s.seekable = h.IsA(walk, "SeekableIterator")
	o.Internal = s
	return nil
}

func (s *SplIterator) free() {
	s.data, s.key, s.fetched = Value{}, Value{}, false
}

func (s *SplIterator) step(ctx *Ctx, h SplHooks, name string) (Value, error) {
	return h.Inner(ctx, s.walk, name, nil)
}

// valid is spl_dual_it_valid: the inner iterator's own answer.
func (s *SplIterator) valid(ctx *Ctx, h SplHooks) (bool, error) {
	v, err := s.step(ctx, h, "valid")
	if err != nil {
		return false, err
	}
	return v.Deref().Bool(), nil
}

// rewind is spl_dual_it_rewind.
func (s *SplIterator) rewind(ctx *Ctx, h SplHooks) error {
	s.free()
	s.Pos = 0
	_, err := s.step(ctx, h, "rewind")
	return err
}

// fetch is spl_dual_it_fetch: cache the inner iterator's current and key,
// after asking valid() when checkMore is set. It reports whether there was
// an element.
func (s *SplIterator) fetch(ctx *Ctx, h SplHooks, checkMore bool) (bool, error) {
	s.free()
	if checkMore {
		ok, err := s.valid(ctx, h)
		if err != nil || !ok {
			return false, err
		}
	}
	data, err := s.step(ctx, h, "current")
	if err != nil {
		return false, err
	}
	key, err := s.step(ctx, h, "key")
	if err != nil {
		s.data, s.fetched = data.Deref(), true
		return false, err
	}
	s.data, s.key, s.fetched = data.Deref(), key.Deref(), true
	return true, nil
}

// next is spl_dual_it_next.
func (s *SplIterator) next(ctx *Ctx, h SplHooks) error {
	s.free()
	if _, err := s.step(ctx, h, "next"); err != nil {
		return err
	}
	s.Pos++
	return nil
}

// limitSeek is spl_limit_it_seek.
func (s *SplIterator) limitSeek(ctx *Ctx, h SplHooks, pos int64) error {
	s.free()
	if pos < s.Offset {
		return RaiseAt(ctx, "OutOfBoundsException", "Cannot seek to %d which is below the offset %d", pos, s.Offset)
	}
	if s.Count != -1 && pos >= s.Offset+s.Count {
		return RaiseAt(ctx, "OutOfBoundsException", "Cannot seek to %d which is behind offset %d plus count %d", pos, s.Offset, s.Count)
	}
	if pos != s.Pos && s.seekable {
		s.free()
		if _, err := h.Call(ctx, s.walk, "seek", []Value{Int(pos)}); err != nil {
			return err
		}
		s.free()
		s.Pos = pos
		if s.Count != -1 && s.Pos >= s.Offset+s.Count {
			return nil
		}
		ok, err := s.valid(ctx, h)
		if err != nil || !ok {
			return err
		}
		_, err = s.fetch(ctx, h, false)
		return err
	}
	if pos < s.Pos {
		if err := s.rewind(ctx, h); err != nil {
			return err
		}
	}
	for pos > s.Pos {
		ok, err := s.valid(ctx, h)
		if err != nil {
			return err
		}
		if !ok {
			break
		}
		if err := s.next(ctx, h); err != nil {
			return err
		}
	}
	ok, err := s.valid(ctx, h)
	if err != nil || !ok {
		return err
	}
	_, err = s.fetch(ctx, h, true)
	return err
}

// filterFetch is spl_filter_it_fetch: move on until accept() says yes.
func (s *SplIterator) filterFetch(ctx *Ctx, o *Object, h SplHooks) error {
	for {
		ok, err := s.fetch(ctx, h, true)
		if err != nil {
			return err
		}
		if !ok {
			break
		}
		accepted, err := h.Call(ctx, Obj(o), "accept", nil)
		if err != nil {
			return err
		}
		if accepted.Deref().Bool() {
			return nil
		}
		if _, err := s.step(ctx, h, "next"); err != nil {
			return err
		}
	}
	s.free()
	return nil
}

func (s *SplIterator) current() Value {
	if !s.fetched {
		return Null()
	}
	return s.data
}

func (s *SplIterator) currentKey() Value {
	if !s.fetched || s.key.Kind() == KindUndef {
		return Null()
	}
	return s.key
}

// SplMethod answers a method of an SPL iterator. `kind` is the SPL class
// the call is made against -- the object's own for `$o->m()`, the named one
// for `parent::m()`. handled is false for a name nothing here declares.
func SplMethod(ctx *Ctx, o *Object, kind, name string, args []Value, h SplHooks) (Value, bool, error) {
	folded := strings.ToLower(name)
	owner, spelled := splOwner(kind, folded)
	if owner == "" {
		return Value{}, false, nil
	}
	args = UnboxArgs(args)
	if folded == "__construct" {
		return Null(), true, SplConstruct(ctx, o, kind, args, h)
	}
	if owner == "EmptyIterator" {
		if err := splArity(ctx, owner, spelled, args, 0, 0); err != nil {
			return Value{}, true, err
		}
		switch folded {
		case "current":
			return Value{}, true, RaiseAt(ctx, "BadMethodCallException", "Accessing the value of an EmptyIterator")
		case "key":
			return Value{}, true, RaiseAt(ctx, "BadMethodCallException", "Accessing the key of an EmptyIterator")
		case "valid":
			return Bool(false), true, nil
		}
		return Null(), true, nil
	}
	if owner == "MultipleIterator" {
		v, err := multipleMethod(ctx, o, folded, spelled, args, h)
		return v, true, err
	}
	if splStoreKind(owner) {
		v, err := splStoreMethod(ctx, o, owner, folded, spelled, args, h)
		return v, true, err
	}
	switch splContainerOf(owner) {
	case "SplDoublyLinkedList":
		v, err := dllMethod(ctx, o, owner, folded, spelled, args, h)
		return v, true, err
	case "SplHeap", "SplPriorityQueue":
		v, err := heapMethod(ctx, o, owner, folded, spelled, args, h)
		return v, true, err
	}
	if owner == "FilterIterator" && folded == "accept" {
		// Abstract: a subclass that does not declare it is not instantiable,
		// so this is only reached by calling it through the parent.
		return Value{}, true, RaiseAt(ctx, "Error", "Cannot call abstract method FilterIterator::accept()")
	}
	s, ok := o.Internal.(*SplIterator)
	if !ok || s == nil {
		return Value{}, true, RaiseAt(ctx, "Error", "The object is in an invalid state as the parent constructor was not called")
	}
	least, most := 0, 0
	if folded == "seek" {
		least, most = 1, 1
	}
	if err := splArity(ctx, owner, spelled, args, least, most); err != nil {
		return Value{}, true, err
	}
	switch owner + "::" + folded {
	case "IteratorIterator::getinneriterator":
		return s.Inner, true, nil
	case "IteratorIterator::rewind":
		if err := s.rewind(ctx, h); err != nil {
			return Value{}, true, err
		}
		_, err := s.fetch(ctx, h, true)
		return Null(), true, err
	case "IteratorIterator::valid":
		return Bool(s.fetched), true, nil
	case "IteratorIterator::key":
		return s.currentKey(), true, nil
	case "IteratorIterator::current":
		return s.current(), true, nil
	case "IteratorIterator::next":
		if err := s.next(ctx, h); err != nil {
			return Value{}, true, err
		}
		_, err := s.fetch(ctx, h, true)
		return Null(), true, err
	case "LimitIterator::rewind":
		if err := s.rewind(ctx, h); err != nil {
			return Value{}, true, err
		}
		return Null(), true, s.limitSeek(ctx, h, s.Offset)
	case "LimitIterator::valid":
		return Bool((s.Count == -1 || s.Pos < s.Offset+s.Count) && s.fetched), true, nil
	case "LimitIterator::next":
		if err := s.next(ctx, h); err != nil {
			return Value{}, true, err
		}
		if s.Count == -1 || s.Pos < s.Offset+s.Count {
			_, err := s.fetch(ctx, h, true)
			return Null(), true, err
		}
		return Null(), true, nil
	case "LimitIterator::seek":
		v, err := CoerceInternalArgument(ctx, args[0].Deref(), "int", "LimitIterator::seek", 1, "offset", false)
		if err != nil {
			return Value{}, true, err
		}
		if err := s.limitSeek(ctx, h, v.Int()); err != nil {
			return Value{}, true, err
		}
		return Int(s.Pos), true, nil
	case "LimitIterator::getposition":
		return Int(s.Pos), true, nil
	case "InfiniteIterator::next":
		if err := s.next(ctx, h); err != nil {
			return Value{}, true, err
		}
		ok, err := s.valid(ctx, h)
		if err != nil {
			return Value{}, true, err
		}
		if !ok {
			if err := s.rewind(ctx, h); err != nil {
				return Value{}, true, err
			}
			if ok, err = s.valid(ctx, h); err != nil || !ok {
				return Null(), true, err
			}
		}
		_, err = s.fetch(ctx, h, false)
		return Null(), true, err
	case "FilterIterator::rewind":
		if err := s.rewind(ctx, h); err != nil {
			return Value{}, true, err
		}
		return Null(), true, s.filterFetch(ctx, o, h)
	case "FilterIterator::next":
		if err := s.next(ctx, h); err != nil {
			return Value{}, true, err
		}
		return Null(), true, s.filterFetch(ctx, o, h)
	case "CallbackFilterIterator::accept":
		if !s.fetched || s.key.Kind() == KindUndef {
			return Bool(false), true, nil
		}
		v, err := ctx.Call(s.Callback, []Value{s.data, s.key, s.Inner})
		if err != nil {
			return Value{}, true, err
		}
		return v.Deref(), true, nil
	}
	return Value{}, false, nil
}

func (m *SplMultiple) find(it *Object) int {
	for i, e := range m.entries {
		if e.it == it {
			return i
		}
	}
	return -1
}

func multipleIteratorArg(ctx *Ctx, spelled string, args []Value, h SplHooks) (*Object, error) {
	v := args[0].Deref()
	if v.Kind() != KindObject || !h.IsA(v, "Iterator") {
		return nil, RaiseAt(ctx, "TypeError", "MultipleIterator::%s(): Argument #1 ($iterator) must be of type Iterator, %s given",
			spelled, ValueName(v))
	}
	return v.Object(), nil
}

func multipleMethod(ctx *Ctx, o *Object, folded, spelled string, args []Value, h SplHooks) (Value, error) {
	m, _ := o.Internal.(*SplMultiple)
	if m == nil {
		// The storage exists from creation, before any constructor: a
		// subclass that never calls the parent's still has one, with no
		// flags set.
		m = &SplMultiple{}
		o.Internal = m
	}
	least, most := 0, 0
	switch folded {
	case "setflags", "detachiterator", "containsiterator":
		least, most = 1, 1
	case "attachiterator":
		least, most = 1, 2
	}
	if err := splArity(ctx, "MultipleIterator", spelled, args, least, most); err != nil {
		return Value{}, err
	}
	switch folded {
	case "getflags":
		return Int(m.Flags), nil
	case "setflags":
		v, err := CoerceInternalArgument(ctx, args[0].Deref(), "int", "MultipleIterator::setFlags", 1, "flags", false)
		if err != nil {
			return Value{}, err
		}
		m.Flags = v.Int()
		return Null(), nil
	case "attachiterator":
		it, err := multipleIteratorArg(ctx, spelled, args, h)
		if err != nil {
			return Value{}, err
		}
		info := Null()
		if len(args) > 1 && !args[1].IsUndef() {
			info = args[1].Deref()
			switch info.Kind() {
			case KindNull, KindInt, KindString:
			default:
				return Value{}, RaiseAt(ctx, "TypeError", "MultipleIterator::attachIterator(): Argument #2 ($info) must be of type string|int|null, %s given",
					ValueName(info))
			}
		}
		if info.Kind() != KindNull {
			for _, e := range m.entries {
				if StrictEqual(e.info, info) {
					return Value{}, RaiseAt(ctx, "InvalidArgumentException", "Key duplication error")
				}
			}
		}
		if i := m.find(it); i >= 0 {
			m.entries[i].info = info
			return Null(), nil
		}
		m.entries = append(m.entries, splMultiEntry{it: it, info: info})
		return Null(), nil
	case "detachiterator":
		it, err := multipleIteratorArg(ctx, spelled, args, h)
		if err != nil {
			return Value{}, err
		}
		if i := m.find(it); i >= 0 {
			m.entries = append(m.entries[:i:i], m.entries[i+1:]...)
		}
		return Null(), nil
	case "containsiterator":
		it, err := multipleIteratorArg(ctx, spelled, args, h)
		if err != nil {
			return Value{}, err
		}
		return Bool(m.find(it) >= 0), nil
	case "countiterators":
		return Int(int64(len(m.entries))), nil
	case "rewind", "next":
		for _, e := range append([]splMultiEntry(nil), m.entries...) {
			if _, err := h.Call(ctx, Obj(e.it), folded, nil); err != nil {
				return Value{}, err
			}
		}
		return Null(), nil
	case "valid":
		if len(m.entries) == 0 {
			return Bool(false), nil
		}
		expect := m.Flags&MultipleNeedAll != 0
		for _, e := range append([]splMultiEntry(nil), m.entries...) {
			v, err := h.Call(ctx, Obj(e.it), "valid", nil)
			if err != nil {
				return Value{}, err
			}
			// **Strictly true**, not truthy: the engine compares the type.
			if (v.Deref().Kind() == KindTrue) != expect {
				return Bool(!expect), nil
			}
		}
		return Bool(expect), nil
	case "current", "key":
		return m.getAll(ctx, folded, h)
	}
	return Value{}, ThrowUndefinedMethod(o.ClassName, spelled)
}

// getAll is spl_multiple_iterator_get_all.
func (m *SplMultiple) getAll(ctx *Ctx, what string, h SplHooks) (Value, error) {
	if len(m.entries) == 0 {
		return Value{}, RaiseAt(ctx, "RuntimeException", "Called %s() on an invalid iterator", what)
	}
	out := NewArray()
	for _, e := range append([]splMultiEntry(nil), m.entries...) {
		v, err := h.Call(ctx, Obj(e.it), "valid", nil)
		if err != nil {
			return Value{}, err
		}
		var got Value
		switch {
		case v.Deref().Kind() == KindTrue:
			got, err = h.Call(ctx, Obj(e.it), what, nil)
			if err != nil {
				return Value{}, err
			}
			got = got.Deref()
		case m.Flags&MultipleNeedAll != 0:
			return Value{}, RaiseAt(ctx, "RuntimeException", "Called %s() with non valid sub iterator", what)
		default:
			got = Null()
		}
		if m.Flags&MultipleKeysAssoc == 0 {
			out.Append(got)
			continue
		}
		switch e.info.Kind() {
		case KindInt:
			out.Set(IntKey(e.info.Int()), got)
		case KindString:
			out.Set(ArrayKey(nil, e.info), got)
		default:
			return Value{}, RaiseAt(ctx, "InvalidArgumentException", "Sub-Iterator is associated with NULL")
		}
	}
	return Arr(out), nil
}

// SplMultipleDebugInfo is what var_dump shows for a MultipleIterator: its own
// properties, then the storage under SplObjectStorage's private name, each
// entry an `["obj" => ..., "inf" => ...]` pair. ok is false for any other
// object.
func SplMultipleDebugInfo(o *Object) (*Array, bool, error) {
	var pairs []splMultiEntry
	switch st := o.Internal.(type) {
	case *SplMultiple:
		pairs = st.entries
	case *SplStorage:
		for _, e := range st.entries {
			pairs = append(pairs, splMultiEntry{it: e.obj, info: e.inf})
		}
	default:
		return nil, false, nil
	}
	info := splPropsTable(o)
	storage := NewArray()
	for _, e := range pairs {
		pair := NewArray()
		pair.Set(StringKey("obj"), Obj(e.it))
		pair.Set(StringKey("inf"), e.info)
		storage.Append(Arr(pair))
	}
	info.Set(StringKey(MangleProperty("SplObjectStorage", "storage")), Arr(storage))
	return info, true, nil
}

// SplForwards reports whether a class hands a method it does not have to
// the iterator it wraps, as spl_dual_it_get_method does: `$it->offsetGet(0)`
// on an IteratorIterator over an ArrayIterator reaches the ArrayIterator
// (bug76901). EmptyIterator and MultipleIterator wrap nothing.
func SplForwards(kind string) bool {
	return SplUncloneable(kind)
}

// SplStoreClone gives a cloned container its own storage.
func SplStoreClone(o *Object) {
	switch st := o.Internal.(type) {
	case *SplStorage:
		o.Internal = st.Clone()
	case *SplFixed:
		o.Internal = st.Clone()
	case *SplMultiple:
		o.Internal = st.Clone()
	case *SplDLL:
		o.Internal = st.Clone()
	case *SplHeapStore:
		o.Internal = st.Clone()
	}
}

// splContainerOf is the root of the list or heap family a class belongs to:
// SplDoublyLinkedList, SplHeap or SplPriorityQueue, or "".
func splContainerOf(kind string) string {
	for c := kind; c != ""; c = splParent[c] {
		switch c {
		case "SplDoublyLinkedList", "SplHeap", "SplPriorityQueue":
			return c
		}
	}
	return ""
}

// SplContainerDebugInfo is what the list and heap classes' own __debugInfo
// answers, for var_dump and print_r of one whose class does not declare its
// own. ok is false for any other kind.
func SplContainerDebugInfo(o *Object, kind string) (*Array, bool) {
	switch splContainerOf(kind) {
	case "SplDoublyLinkedList":
		return dllDebugInfo(o, SplDLLOf(o)), true
	case "SplHeap", "SplPriorityQueue":
		return heapDebugInfo(o, SplHeapOf(o), splContainerOf(kind)), true
	}
	return nil, false
}

// SplContainerTraversal is the engine's own iterator over a list or heap,
// which `foreach` takes unless a subclass overrides an Iterator method.
// ok is false for any other kind.
func SplContainerTraversal(o *Object, kind string, h SplHooks) (Traversable, bool) {
	switch splContainerOf(kind) {
	case "SplDoublyLinkedList":
		return NewSplDLLTraversal(o), true
	case "SplHeap", "SplPriorityQueue":
		return NewSplHeapTraversal(o, h), true
	}
	return nil, false
}

// SplInner is the object a dual iterator wraps, once constructed.
func SplInner(o *Object) (Value, bool) {
	s, ok := o.Internal.(*SplIterator)
	if !ok || s == nil || s.Inner.Kind() != KindObject {
		return Value{}, false
	}
	return s.Inner, true
}
