package interp

import (
	pb "github.com/moriyoshi/php2go/internal/ir/irpb"
	"github.com/moriyoshi/php2go/runtime/php"
)

// The ArrayAccess methods, which are ordinary methods the engine calls on the
// object's behalf whenever it is indexed.
const (
	offsetGet    = "offsetGet"
	offsetSet    = "offsetSet"
	offsetExists = "offsetExists"
	offsetUnset  = "offsetUnset"
)

// implementsArrayAccess reports whether an object can be indexed. The interface
// is checked rather than the method: a class can declare offsetGet without
// implementing ArrayAccess, and PHP still refuses to index it.
func (u *Unit) implementsArrayAccess(object *php.Object) bool {
	class, ok := u.findClass(object.ClassName)
	if !ok {
		return false
	}
	return u.instanceOf(class, "ArrayAccess")
}

// offsetCall invokes one of the ArrayAccess methods. It reports handled=false
// when the object is not an ArrayAccess at all, which is the caller's cue to
// raise the "cannot use as array" error rather than inventing a value.
func (in *Interp) offsetCall(ctx *php.Ctx, unit *Unit, object *php.Object, method string,
	args []php.Value, depth int) (php.Value, bool, error) {
	if !unit.implementsArrayAccess(object) {
		return php.Undef(), false, nil
	}
	if value, handled, err := in.weakMethod(ctx, unit, object, method, args); handled {
		return value, true, in.materialize(ctx, unit, err)
	}
	class, ok := unit.findClass(object.ClassName)
	if !ok {
		return php.Undef(), false, nil
	}
	body, declaring, found := unit.resolveMethod(class, method)
	if !found {
		// No body of the program's own: an engine class this runtime models
		// answers here, and one it does not is a gap rather than a program
		// error.
		if value, handled, err := in.arrayStoreMethod(ctx, unit, object, method, args); handled {
			return value, true, in.materialize(ctx, unit, err)
		}
		return php.Undef(), false, nil
	}
	value, err := in.invokeDriven(ctx, unit, body, object, declaring, class, args, depth)
	return value, true, err
}

// readOffset performs $o[$k]. The quiet form -- isset($o[$k]) and $o[$k] ?? x --
// asks offsetExists first, so a missing offset answers without offsetGet ever
// seeing it.
func (in *Interp) readOffset(ctx *php.Ctx, unit *Unit, object *php.Object, index php.Value,
	quiet bool, line *pb.Opline, op *pb.OpArray, depth int) (php.Value, error) {
	if !unit.implementsArrayAccess(object) {
		return php.Undef(), in.indexedObjectError(ctx, unit, object)
	}
	if quiet {
		exists, handled, err := in.offsetCall(ctx, unit, object, offsetExists, []php.Value{index}, depth)
		if err != nil {
			return php.Undef(), err
		}
		if handled && !exists.Bool() {
			return php.Null(), nil
		}
	}
	value, handled, err := in.offsetCall(ctx, unit, object, offsetGet, []php.Value{index}, depth)
	if err != nil {
		return php.Undef(), err
	}
	if !handled {
		return php.Undef(), &UnsupportedError{
			Opcode: "index read on " + object.ClassName,
			Line:   line.GetLine(), File: op.GetFilename(),
		}
	}
	return value, nil
}

// weakMapSlot answers with the box behind a WeakMap entry. A WeakMap is not an
// overloaded container the way a userland ArrayAccess is: its dimension handler
// hands out the slot itself, so `$map[$o][] = 1`, `$map[$o]++` and
// `$map[$o] = &$x` all reach the map rather than a copy, and none of them earns
// "Indirect modification of overloaded element". A key the map does not hold is
// left to offsetGet, which owns the refusal for it.
func weakMapSlot(object *php.Object, index php.Value) (php.Value, *weakMap, int, bool) {
	held, ok := asWeakMap(object)
	if !ok {
		return php.Undef(), nil, 0, false
	}
	key := index.Deref().Object()
	if key == nil {
		return php.Undef(), nil, 0, false
	}
	at, found := held.Find(key)
	if !found {
		return php.Undef(), nil, 0, false
	}
	return php.MakeRef(held.SlotAt(at)), held, at, true
}

// writeFetchOffset opens a write path through an ArrayAccess: `$o[$k][$j] = x`
// and `$o[] = ...` both descend through offsetGet first. What an *append*
// passes that accessor depends on where the accessor comes from, and the three
// answers are not derivable from one another:
//
//   - A plain PHP class is called with null, because the standard handler turns
//     the missing dimension into one.
//   - An engine array store -- ArrayObject and its kin -- is not called at all:
//     its handler is given no dimension and has nothing to look up.
//   - A PHP class *extending* one of those keeps the engine's handler, which
//     hands the override an undefined argument -- so it is called with no
//     arguments at all, and a required parameter makes that an
//     ArgumentCountError.
//
// The second result says the accessor was consulted, which is what the caller
// needs to know: PHP reports the indirect modification for whatever the
// accessor produced, an exception included, and reports nothing when the
// descent never got that far.
func (in *Interp) writeFetchOffset(ctx *php.Ctx, unit *Unit, object *php.Object,
	f *frame, line *pb.Opline, op *pb.OpArray, depth int) (php.Value, bool, error) {
	if line.GetOp2Type() != opUnused {
		value, err := in.readOffset(ctx, unit, object, f.fetch(line.GetOp2Type(), line.GetOp2()),
			false, line, op, depth)
		return value, unit.implementsArrayAccess(object), err
	}
	if _, isWeakMap := asWeakMap(object); isWeakMap {
		// A WeakMap has no key to invent for an append, and it refuses the
		// descent in those words rather than asking offsetGet(null) -- which
		// would complain about the key instead.
		// `consulted` is false: no accessor ran, so there is nothing the
		// caller could report as modified to no effect.
		return php.Undef(), false, Raise("Error", "Cannot append to WeakMap")
	}
	if _, engineStore := asArrayStore(object); !engineStore {
		value, err := in.readOffset(ctx, unit, object, php.Null(), false, line, op, depth)
		return value, unit.implementsArrayAccess(object), err
	}
	class, known := unit.findClass(object.ClassName)
	if !known {
		return php.Null(), true, nil
	}
	body, declaring, found := unit.resolveMethod(class, offsetGet)
	if !found {
		return php.Null(), true, nil
	}
	if body.GetRequiredArgs() > 0 {
		// The argument the engine hands the override is an undefined one, and
		// a parameter with no default cannot take it. This is the hole's own
		// diagnostic, not the one a call with too few arguments earns.
		name := ""
		if args := body.GetArgs(); len(args) > 0 {
			name = args[0].GetName()
		}
		return php.Undef(), true, Raise("ArgumentCountError",
			"%s::%s(): Argument #1 ($%s) not passed",
			php.DisplayName(class.Name), offsetGet, name)
	}
	value, err := in.invokeDriven(ctx, unit, body, object, declaring, class, nil, depth)
	return value, true, err
}

// indexedObjectError is what PHP raises when a program indexes an object that
// is not an ArrayAccess.
func (in *Interp) indexedObjectError(ctx *php.Ctx, unit *Unit, object *php.Object) error {
	return in.throwError(ctx, unit, "Error",
		"Cannot use object of type %s as array", php.DisplayName(object.ClassName))
}

// iteratorBuiltin implements the SPL functions that drain a Traversable. They
// need the interpreter, since pulling from one runs user code.
func (in *Interp) iteratorBuiltin(ctx *php.Ctx, unit *Unit, name string,
	args []php.Value, depth int) (php.Value, bool, error) {
	switch name {
	case "iterator_to_array", "iterator_count", "iterator_apply":
	default:
		return php.Undef(), false, nil
	}
	if len(args) == 0 {
		return php.Undef(), true, nil
	}
	subject := args[0].Deref()
	// An array is accepted by iterator_to_array and refused by the others,
	// which is the engine's own asymmetry rather than a simplification.
	if subject.Kind() == php.KindArray && name == "iterator_to_array" {
		preserve := len(args) < 2 || args[1].Deref().Bool()
		result := php.NewArray()
		subject.Array().Each(func(k php.Key, v php.Value) bool {
			if preserve {
				result.Set(k, v.Deref().Copy())
			} else {
				result.Append(v.Deref().Copy())
			}
			return true
		})
		return php.Arr(result), true, nil
	}
	if subject.Kind() != php.KindObject {
		return php.Undef(), true, Raise("TypeError",
			"%s(): Argument #1 ($iterator) must be of type Traversable, %s given",
			name, phpTypeName(subject))
	}
	it, ok, err := in.newObjectIterator(ctx, unit, subject, depth)
	if err != nil || !ok {
		if err == nil {
			err = Raise("TypeError",
				"%s(): Argument #1 ($iterator) must be of type Traversable, %s given",
				name, php.DisplayName(subject.Object().ClassName))
		}
		return php.Undef(), true, err
	}
	preserve := len(args) < 2 || args[1].Deref().Bool()
	it.skipCurrent = name == "iterator_count"
	it.skipKey = it.skipCurrent || !preserve
	result := php.NewArray()
	count := int64(0)
	for {
		key, value, more, err := in.iterNext(ctx, unit, it)
		if err != nil {
			return php.Undef(), true, err
		}
		if !more {
			break
		}
		count++
		if name == "iterator_to_array" {
			if preserve {
				// An array is being built, so the key is converted -- which is
				// what makes a float key report losing precision here and not
				// in the foreach that produced it.
				result.Set(php.ArrayKey(ctx, key), value.Deref().Copy())
			} else {
				result.Append(value.Deref().Copy())
			}
		}
	}
	if name == "iterator_count" {
		return php.Int(count), true, nil
	}
	return php.Arr(result), true, nil
}
