package php

import (
	"strconv"
	"strings"
)

// `ArrayIterator` and `ArrayObject` -- an array plus, for the iterator, a
// position of its own. Both are engine classes, so nothing is harvested to
// execute and the behaviour has to be supplied; the same arrangement Generator
// and Closure use.
//
// **Here** because both back-ends need it: the interpreter has had it, and a
// compiled program that writes `new ArrayIterator($a)` needs the same object
// with the same methods. Everything it has to ask about the program's classes
// goes through the Ctx hooks.

// ArrayStore is the state behind both classes.
type ArrayStore struct {
	items *Array
	// owner is the object whose property table `items` *is*, when the store was
	// built over an object rather than an array. A reference bound to one of
	// those slots is still the property's, so it carries the property's
	// declared type -- which is the only thing the object is needed for.
	owner *Object
	// keys is the snapshot an ArrayIterator walks, and pos indexes it.
	// ArrayObject has no position: it hands out a fresh iterator instead.
	keys []Key
	pos  int
	// stdPropList is ArrayObject::STD_PROP_LIST, given to the constructor:
	// an array cast then lists the object's properties, not the storage.
	stdPropList bool
	// flags are the constructor's or setFlags()'s flags, which getFlags()
	// and serialization report. Only STD_PROP_LIST changes behaviour here.
	flags int64
}

// The ArrayObject flags the store carries (spl_array.h).
const (
	arrayStdPropList  = 0x00000001
	arrayAsProps      = 0x00000002
	arrayIsSelf       = 0x01000000
	arrayCloneMask    = 0x0100FFFF
	arrayOverloadMsg  = "Overloaded object of type %s is not compatible with %s"
	arrayStoreUnmodel = "an ArrayObject or ArrayIterator with ARRAY_AS_PROPS"
)

// Items is the array behind the store, which the dimension handler reaches.
func (s *ArrayStore) Items() *Array {
	if s == nil {
		return nil
	}
	return s.items
}

// Owner is the object whose property table the store walks, or nil.
func (s *ArrayStore) Owner() *Object {
	if s == nil {
		return nil
	}
	return s.owner
}

// ArrayBackedClasses are the engine classes this store implements. Anything
// else engine-provided stays an honest gap.
var ArrayBackedClasses = map[string]bool{
	"arrayiterator": true,
	"arrayobject":   true,
}

// ArrayBackedClassNames is the same set spelled as the engine spells it, in a
// fixed order so a diagnostic built from it reads the same every run.
var ArrayBackedClassNames = []string{"ArrayIterator", "ArrayObject"}

// arrayStoreOwner is the engine class a diagnostic names: `new Sub("x")` on a
// `Sub extends ArrayObject` reports `ArrayObject::__construct()`, because that
// is the constructor that refused. Measured, not assumed.
func (c *Ctx) arrayStoreOwner(class string) string {
	for _, name := range ArrayBackedClassNames {
		if strings.EqualFold(class, name) {
			return name
		}
	}
	if c.subclassOf != nil {
		for _, name := range ArrayBackedClassNames {
			if c.subclassOf(class, name) {
				return name
			}
		}
	}
	return class
}

// checkArrayStoreArg is the constructor's declared type. `array|object` is what
// it accepts -- an object becomes a view on its property table -- and anything
// else is a TypeError the program can catch, naming the parameter `$array`.
func (c *Ctx) checkArrayStoreArg(class string, args []Value) error {
	if len(args) == 0 {
		return nil
	}
	switch given := args[0].Deref(); given.Kind() {
	case KindArray, KindObject:
		return nil
	default:
		return RaiseAt(c, "TypeError",
			"%s::__construct(): Argument #1 ($array) must be of type array, %s given",
			c.arrayStoreOwner(class), ValueName(given))
	}
}

// IsArrayBackedClass answers for the subclasses too. Neither engine class is
// final, and a program that extends one gets the same store -- which is what
// makes an inherited offsetSet reach it, and what makes var_dump show the
// storage the parent declares.
func (c *Ctx) IsArrayBackedClass(class string) bool {
	if ArrayBackedClasses[strings.ToLower(class)] {
		return true
	}
	if c.subclassOf == nil {
		return false
	}
	for name := range ArrayBackedClasses {
		if c.subclassOf(class, name) {
			return true
		}
	}
	return false
}

// NewArrayStore fills the object `new ArrayIterator(...)` produces.
//
// An **array** argument is copied, as PHP's own constructor does: writing
// through the object afterwards must not reach the caller's array. An **object**
// argument is not -- the store becomes that object's property table, so writing
// through the iterator reaches the object and a by-reference loop over it binds
// the property's own slot.
func NewArrayStore(object *Object, args []Value) {
	items, owner := NewArray(), (*Object)(nil)
	if len(args) > 0 {
		switch given := args[0].Deref(); given.Kind() {
		case KindArray:
			given.Array().Each(func(k Key, v Value) bool {
				items.Set(k, v.Deref().Copy())
				return true
			})
		case KindObject:
			// The store writes through: a live table.
			if props := given.Object().Table(); props != nil {
				items, owner = props, given.Object()
			}
		}
	}
	store := &ArrayStore{items: items, owner: owner}
	if len(args) > 1 && args[1].Deref().Kind() == KindInt {
		store.flags = args[1].Deref().Int()
		store.stdPropList = store.flags&arrayStdPropList != 0
	}
	object.Internal = store
}

// SetProgramMethod installs the question storeElementRef asks: whether the
// program's own classes declare or inherit a method of that name, leaving the
// engine class's methods out.
func (c *Ctx) SetProgramMethod(fn HasMethodFn) { c.programMethod = fn }

// storeElementRef is `$ao["k"][] = 1` on an ArrayObject or ArrayIterator: the
// engine's dimension handler hands out the **storage element itself**, so the
// nested write lands, where an ordinary ArrayAccess earns `Indirect
// modification of overloaded element` and writes a copy. Measured:
//
//	$ao["a"][] = 2;   // appends to the stored array
//	$ao["x"][] = 3;   // creates "x" silently, no undefined-key warning
//	$ao[][] = 5;      // the notice, and nothing written
//	$r = &$ao["a"];   // binds the element: var_dump shows &array
//
// Only when the program declares no `offsetGet` of its own: a subclass that
// does is called like any ArrayAccess (the notice again, after its body ran).
// Nor for a store over an object's property table, whose slots carry the
// property's rules; nor without the hook, which leaves the old answer.
// handled=false sends the caller on to overloadedElement.
func storeElementRef(ctx *Ctx, o *Object, key Value, appending bool) (*Ref, func(), bool, error) {
	noop := func() {}
	store, ok := AsArrayStore(o)
	if !ok || store.owner != nil || store.items == nil || ctx == nil || ctx.programMethod == nil ||
		ctx.programMethod(o.ClassName, "offsetget") {
		return nil, noop, false, nil
	}
	if appending {
		// The handler has no element to hand out for a missing offset, and
		// asks no offsetGet for it either.
		ctx.Notice("Indirect modification of overloaded element of %s has no effect", DisplayName(o.ClassName))
		if err := ctx.TakePendingError(); err != nil {
			return nil, noop, true, err
		}
		return NewRef(Null()), noop, true, nil
	}
	if IllegalOffsetKind(key) {
		// Named after the object, not `array`: the handler refuses it.
		return nil, noop, true, RaiseAt(ctx, "TypeError", "Cannot access offset of type %s on %s",
			TypeName(key.Deref()), DisplayName(o.ClassName))
	}
	// A missing offset is created by RefAt, silently.
	k := ArrayKey(ctx, key)
	items := store.items
	ref := items.RefAt(k)
	return ref, func() { items.CollapseUnsharedRef(k, ref) }, true, nil
}

// AsArrayStore answers the payload an object carries, if it is one of these.
func AsArrayStore(object *Object) (*ArrayStore, bool) {
	if object == nil {
		return nil, false
	}
	store, ok := object.Internal.(*ArrayStore)
	return store, ok
}

// ArrayStoreMethod implements the methods both classes share. The two differ
// only in that ArrayIterator is itself the iterator, so it also answers the
// Iterator protocol.
func (c *Ctx) ArrayStoreMethod(object *Object, name string, args []Value) (Value, bool, error) {
	if v, handled, err := c.arrayStoreSerialMethod(object, strings.ToLower(name), args); handled {
		return v, true, err
	}
	store, ok := AsArrayStore(object)
	if !ok {
		// A subclass that declares its own constructor is instantiated without
		// a store, because the engine class's constructor is what makes one --
		// and `parent::__construct($array)` is the call that reaches it.
		if strings.EqualFold(name, "__construct") && c.IsArrayBackedClass(object.ClassName) {
			if err := c.checkArrayStoreArg(object.ClassName, args); err != nil {
				return Undef(), true, err
			}
			NewArrayStore(object, args)
			return Null(), true, nil
		}
		return Undef(), false, nil
	}
	argument := func(i int) Value {
		if i < len(args) {
			return args[i].Deref()
		}
		return Null()
	}

	switch strings.ToLower(name) {
	case "__construct":
		if err := c.checkArrayStoreArg(object.ClassName, args); err != nil {
			return Undef(), true, err
		}
		NewArrayStore(object, args)
		return Null(), true, nil
	case "count":
		return Int(int64(store.items.Len())), true, nil
	case "getarraycopy":
		copied := NewArray()
		store.items.Each(func(k Key, v Value) bool {
			copied.Set(k, v.Deref().Copy())
			return true
		})
		return Arr(copied), true, nil
	case "append":
		store.items.Append(argument(0).Copy())
		return Null(), true, nil

	case "offsetget":
		value, held := store.items.Get(ArrayKey(c, argument(0)))
		if !held {
			c.Warn("Undefined array key %s", QuoteKey(argument(0)))
			return Null(), true, nil
		}
		return value, true, nil
	case "offsetset":
		if len(args) == 0 || args[0].Deref().Kind() == KindNull {
			store.items.Append(argument(1).Copy())
			return Null(), true, nil
		}
		store.items.Set(ArrayKey(c, argument(0)), argument(1).Copy())
		return Null(), true, nil
	case "offsetexists":
		_, held := store.items.Get(ArrayKey(c, argument(0)))
		return Bool(held), true, nil
	case "offsetunset":
		store.items.Unset(ArrayKey(c, argument(0)))
		return Null(), true, nil

	// The Iterator protocol, which only ArrayIterator answers -- ArrayObject
	// hands out a separate iterator through getIterator.
	case "rewind":
		store.keys, store.pos = store.items.Keys(), 0
		return Null(), true, nil
	case "valid":
		return Bool(store.pos < len(store.keys)), true, nil
	case "current":
		if store.pos >= len(store.keys) {
			return Bool(false), true, nil
		}
		value, _ := store.items.Get(store.keys[store.pos])
		return value, true, nil
	case "key":
		if store.pos >= len(store.keys) {
			return Null(), true, nil
		}
		return store.keys[store.pos].Value(), true, nil
	case "next":
		store.pos++
		return Null(), true, nil
	case "seek":
		// SeekableIterator. The refusal is an **OutOfBoundsException**, which
		// a program can catch, and it names the position it was given rather
		// than the bound -- measured, not assumed.
		want := argument(0).Int()
		store.keys = store.items.Keys()
		if want < 0 || want >= int64(len(store.keys)) {
			return Undef(), true, RaiseAt(c, "OutOfBoundsException",
				"Seek position %d is out of range", want)
		}
		store.pos = int(want)
		return Null(), true, nil

	case "getiterator":
		// ArrayObject is an aggregate: it produces a fresh ArrayIterator over
		// the **same storage**, not a copy -- measured, `$it[0] = 7` through
		// the iterator is read back from the ArrayObject, and so is a write
		// through `foreach ($ao as &$v)`.
		iterator, made, err := c.NewInstance("ArrayIterator")
		if err != nil || !made {
			return Undef(), false, err
		}
		iterator.Internal = &ArrayStore{items: store.items, owner: store.owner}
		return Obj(iterator), true, nil
	}
	// **Unhandled, not "no such method".** `asort`, `exchangeArray`, `setFlags`
	// and the rest of the surface are real methods this runtime has not
	// implemented. Each caller decides what to say about that -- and both say
	// the same thing, that it is a gap and not a program error.
	return Undef(), false, nil
}

// QuoteKey renders an array key the way PHP's own diagnostics do: a string key
// appears in quotes and an integer one bare.
func QuoteKey(index Value) string {
	// The key the index became, not the index: null becomes the empty *string*
	// key, and the diagnostic quotes it as one. No context is passed: whatever
	// the conversion had to say was already said when the key was taken.
	key := ArrayKey(nil, index)
	if key.IsInt {
		return strconv.FormatInt(key.Int, 10)
	}
	return "\"" + key.Str + "\""
}

// StoreRefTraversal is `foreach ($it as &$v)` over an ArrayIterator: the loop
// variable binds to the storage's own slot, so a write through it lands in the
// iterator's array -- or, for a store built over an object, in the property.
// It walks the iterator's own position, as the by-value loop does.
type StoreRefTraversal struct{ store *ArrayStore }

// NewStoreRefTraversal walks store by reference.
func NewStoreRefTraversal(store *ArrayStore) *StoreRefTraversal {
	return &StoreRefTraversal{store: store}
}

// Rewind snapshots the keys, skipping what the engine skips over an object: a
// private or protected property is not handed out, whatever scope built the
// iterator (probed). Such a property can sit under its plain name here, so the
// declaration is asked as well as the key's spelling.
func (t *StoreRefTraversal) Rewind(ctx *Ctx) error {
	s := t.store
	s.keys, s.pos = s.items.Keys(), 0
	if s.owner != nil {
		kept := s.keys[:0]
		for _, k := range s.keys {
			if k.IsInt {
				kept = append(kept, k)
				continue
			}
			if vis := s.owner.Declared[k.Str].Visibility; strings.HasPrefix(k.Str, "\x00") ||
				vis == "private" || vis == "protected" {
				continue
			}
			kept = append(kept, k)
		}
		s.keys = kept
	}
	return nil
}

func (t *StoreRefTraversal) Valid(ctx *Ctx) (bool, error) {
	return t.store.pos < len(t.store.keys), nil
}

func (t *StoreRefTraversal) Current(ctx *Ctx) (Value, error) {
	v, _ := t.store.items.Get(t.store.keys[t.store.pos])
	return v, nil
}

func (t *StoreRefTraversal) Key(ctx *Ctx) (Value, error) {
	return t.store.keys[t.store.pos].Value(), nil
}

func (t *StoreRefTraversal) Next(ctx *Ctx) error {
	t.store.pos++
	return nil
}

// CurrentRef is the slot's box. Over an object the slot is a property, and the
// box keeps its declaration: a readonly one is refused before the body runs,
// and a typed one refuses a write of the wrong type -- `Cannot assign array to
// reference held by property A::$foo of type string` (typed_properties_114).
func (t *StoreRefTraversal) CurrentRef(ctx *Ctx) (Value, error) {
	s := t.store
	key := s.keys[s.pos]
	if s.owner == nil || key.IsInt {
		return MakeRef(s.items.RefAt(key)), nil
	}
	d, declared := s.owner.Declared[key.Str]
	owner := d.Class
	if owner == "" {
		owner = s.owner.ClassName
	}
	if declared && d.Readonly {
		return Value{}, RaiseAt(ctx, "Error", "Cannot acquire reference to readonly property %s::$%s",
			DisplayName(owner), key.Str)
	}
	ref := s.owner.PropRefAt(key)
	if declared && d.Type != "" {
		ref.AddSource(RefSource{Class: owner, Name: key.Str, Type: d.Type, Owner: s.owner})
	}
	return MakeRef(ref), nil
}
