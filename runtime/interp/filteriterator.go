package interp

import (
	"strings"

	pb "github.com/moriyoshi/php2go/internal/ir/irpb"
	"github.com/moriyoshi/php2go/runtime/php"
)

// CallbackFilterIterator wraps another Iterator and drops the elements a
// callback rejects. It is not an array-backed class: what it holds is another
// object, and every question it answers it asks that object first -- which is
// why it lives here rather than with the array stores.
//
// The engine gives it an accept() the subclasses override, so the filtering is
// expressed through that method rather than by calling the callback directly:
// a subclass that overrides accept() and never touches the callback still
// filters, which is the point of the class being open.

// callbackFilter is what one of these objects holds.
type callbackFilter struct {
	inner    *php.Object
	callback php.Value
}

func asCallbackFilter(object *php.Object) (*callbackFilter, bool) {
	if object == nil {
		return nil, false
	}
	held, ok := object.Internal.(*callbackFilter)
	return held, ok
}

// filterIteratorClasses are the wrapping iterators implemented here.
// IteratorIterator is the plain one: it wraps a Traversable and passes every
// question through, which is the same machinery with nothing filtering.
var filterIteratorClasses = map[string]bool{
	"callbackfilteriterator": true,
	"iteratoriterator":       true,
}

// isFilterIteratorClass answers for the subclasses too: a program that extends
// IteratorIterator gets the same machinery, which is the whole reason the
// engine class is not final.
func isFilterIteratorClass(unit *Unit, className string) bool {
	if filterIteratorClasses[strings.ToLower(className)] {
		return true
	}
	class, known := unit.findClass(className)
	if !known {
		return false
	}
	for name := range filterIteratorClasses {
		if unit.instanceOf(class, name) {
			return true
		}
	}
	return false
}

// isEmptyIteratorClass is the same question for EmptyIterator.
func isEmptyIteratorClass(unit *Unit, className string) bool {
	if strings.EqualFold(className, "EmptyIterator") {
		return true
	}
	class, known := unit.findClass(className)
	return known && unit.instanceOf(class, "EmptyIterator")
}

// emptyIteratorMethod answers the Iterator protocol for EmptyIterator: never
// valid, and asking for what it is on is a BadMethodCallException rather than
// null -- there is nothing to be on.
func emptyIteratorMethod(unit *Unit, call *pendingCall) (php.Value, bool, error) {
	if call.this == nil || !isEmptyIteratorClass(unit, call.this.ClassName) {
		return php.Undef(), false, nil
	}
	switch strings.ToLower(call.builtinMethod) {
	case "__construct", "rewind", "next":
		return php.Null(), true, nil
	case "valid":
		return php.Bool(false), true, nil
	case "current":
		return php.Undef(), true, Raise("BadMethodCallException",
			"Accessing the value of an EmptyIterator")
	case "key":
		return php.Undef(), true, Raise("BadMethodCallException",
			"Accessing the key of an EmptyIterator")
	}
	return php.Undef(), false, nil
}

// callbackFilterMethod answers the Iterator protocol for one of these. Each
// question is put to the inner iterator, and the two that move -- rewind and
// next -- keep moving until accept() agrees or the inner iterator runs out.
func (in *Interp) callbackFilterMethod(ctx *php.Ctx, unit *Unit,
	call *pendingCall, depth int) (php.Value, bool, error) {
	held, ok := asCallbackFilter(call.this)
	if !ok {
		if !strings.EqualFold(call.builtinMethod, "__construct") ||
			!isFilterIteratorClass(unit, call.this.ClassName) {
			return php.Undef(), false, nil
		}
		return in.newCallbackFilter(ctx, unit, call)
	}
	inner := func(name string) (php.Value, error) {
		return in.callInnerIterator(ctx, unit, held.inner, name, depth)
	}
	switch strings.ToLower(call.builtinMethod) {
	case "getinneriterator":
		return php.Obj(held.inner), true, nil
	case "current", "key":
		value, err := inner(strings.ToLower(call.builtinMethod))
		return value, true, err
	case "valid":
		value, err := inner("valid")
		return php.Bool(value.Deref().Bool()), true, err
	case "accept":
		if held.callback.IsUndef() {
			return php.Bool(true), true, nil
		}
		// The callback receives what the inner iterator is on, plus the
		// iterator itself -- so a filter can look further than the element.
		current, err := inner("current")
		if err != nil {
			return php.Undef(), true, err
		}
		key, err := inner("key")
		if err != nil {
			return php.Undef(), true, err
		}
		accepted, err := in.callValue(ctx, unit, held.callback,
			[]php.Value{current.Deref(), key.Deref(), php.Obj(held.inner)}, depth)
		return php.Bool(accepted.Deref().Bool()), true, err
	case "rewind", "next":
		if _, err := inner(strings.ToLower(call.builtinMethod)); err != nil {
			return php.Undef(), true, err
		}
		return php.Null(), true, in.skipRejected(ctx, unit, call.this, held, depth)
	}
	// Anything else the class does not answer is passed through to the inner
	// iterator, which is how `method_exists($it, 'offsetGet')` on an
	// IteratorIterator over an ArrayIterator comes out true and the call
	// reaches the array.
	if body, found := in.innerPassthrough(unit, held.inner, call.builtinMethod); found {
		value, err := in.callPassthrough(ctx, unit, held.inner, body, call.builtinMethod,
			call.args, depth)
		return value, true, err
	}
	return php.Undef(), false, nil
}

// innerPassthrough asks whether the wrapped object answers a method the wrapper
// does not. The second result is what to invoke, nil when the inner object is
// an engine class this runtime implements in Go.
func (in *Interp) innerPassthrough(unit *Unit, inner *php.Object, name string) (*pb.OpArray, bool) {
	if inner == nil {
		return nil, false
	}
	if _, backed := asArrayStore(inner); backed {
		switch strings.ToLower(name) {
		case "count", "getarraycopy", "append", offsetGetLower, offsetSetLower,
			offsetExistsLower, offsetUnsetLower, "getiterator":
			return nil, true
		}
		return nil, false
	}
	class, known := unit.findClass(inner.ClassName)
	if !known {
		return nil, false
	}
	body, _, found := unit.resolveMethod(class, name)
	return body, found
}

// callPassthrough runs what innerPassthrough found.
func (in *Interp) callPassthrough(ctx *php.Ctx, unit *Unit, inner *php.Object,
	body *pb.OpArray, name string, args []php.Value, depth int) (php.Value, error) {
	if body == nil {
		value, _, err := in.arrayStoreMethod(ctx, unit, inner, name, args)
		return value, err
	}
	class, _ := unit.findClass(inner.ClassName)
	_, declaring, _ := unit.resolveMethod(class, name)
	return in.invoke(ctx, unit, body, inner, declaring, class, args, depth)
}

// newCallbackFilter is the constructor: an Iterator and a callable, both
// checked, since the class is reached from PHP and either can be wrong.
func (in *Interp) newCallbackFilter(ctx *php.Ctx, unit *Unit,
	call *pendingCall) (php.Value, bool, error) {
	argument := func(i int) php.Value {
		if i < len(call.args) {
			return call.args[i].Deref()
		}
		return php.Null()
	}
	// IteratorIterator takes any Traversable -- a generator or an aggregate as
	// well as an Iterator -- while the filtering subclass takes an Iterator.
	// CallbackFilterIterator descends from IteratorIterator, so the question is
	// which of the two the class is *below*, not which name it carries.
	plain := true
	if class, known := unit.findClass(call.this.ClassName); known {
		plain = !unit.instanceOf(class, "CallbackFilterIterator")
	}
	wanted := "Iterator"
	if plain {
		wanted = "Traversable"
	}
	iterator := argument(0)
	if iterator.Object() == nil || !in.isInstanceOfName(unit, iterator, wanted) {
		return php.Undef(), true, in.throwError(ctx, unit, "TypeError",
			"%s::__construct(): Argument #1 ($iterator) must be of type %s, %s given",
			php.DisplayName(call.this.ClassName), wanted, givenTypeName(iterator))
	}
	if plain {
		// Its second argument names a class to seek an inner iterator of, which
		// nothing here needs: what it wraps is what it was given.
		//
		// An aggregate is unwrapped once, here: getIterator() runs at
		// construction and what it produced is what getInnerIterator() hands
		// back afterwards, the same object every time.
		inner := iterator.Object()
		if !in.isInstanceOfName(unit, iterator, "Iterator") {
			produced, err := in.aggregateIterator(ctx, unit, inner, 0)
			if err != nil {
				return php.Undef(), true, err
			}
			if produced == nil {
				return php.Undef(), true, &UnsupportedError{
					Opcode: "IteratorIterator over " + inner.ClassName,
				}
			}
			inner = produced
		}
		call.this.Internal = &callbackFilter{inner: inner}
		return php.Null(), true, nil
	}
	callback := argument(1)
	if reason := in.callableReason(ctx, unit, callback); reason != "" {
		return php.Undef(), true, in.throwError(ctx, unit, "TypeError",
			"%s::__construct(): Argument #2 ($callback) must be a valid callback, %s",
			php.DisplayName(call.this.ClassName), reason)
	}
	call.this.Internal = &callbackFilter{inner: iterator.Object(), callback: callback}
	return php.Null(), true, nil
}

// skipRejected advances the inner iterator past everything accept() turns down,
// which is what makes rewind() and next() land on an element the filter keeps.
// accept() is resolved as a method so a subclass overriding it is honoured.
func (in *Interp) skipRejected(ctx *php.Ctx, unit *Unit, object *php.Object,
	held *callbackFilter, depth int) error {
	for {
		valid, err := in.callInnerIterator(ctx, unit, held.inner, "valid", depth)
		if err != nil || !valid.Deref().Bool() {
			return err
		}
		accepted, err := in.callFilterAccept(ctx, unit, object, held, depth)
		if err != nil || accepted {
			return err
		}
		if _, err := in.callInnerIterator(ctx, unit, held.inner, "next", depth); err != nil {
			return err
		}
	}
}

// callFilterAccept runs accept(), preferring a subclass's override.
func (in *Interp) callFilterAccept(ctx *php.Ctx, unit *Unit, object *php.Object,
	held *callbackFilter, depth int) (bool, error) {
	if held.callback.IsUndef() {
		// A plain IteratorIterator filters nothing, so there is no accept() to
		// call and every element is kept.
		return true, nil
	}
	if class, known := unit.findClass(object.ClassName); known {
		if body, declaring, found := unit.resolveMethod(class, "accept"); found {
			answer, err := in.invoke(ctx, unit, body, object, declaring, class, nil, depth)
			return answer.Deref().Bool(), err
		}
	}
	current, err := in.callInnerIterator(ctx, unit, held.inner, "current", depth)
	if err != nil {
		return false, err
	}
	key, err := in.callInnerIterator(ctx, unit, held.inner, "key", depth)
	if err != nil {
		return false, err
	}
	accepted, err := in.callValue(ctx, unit, held.callback,
		[]php.Value{current.Deref(), key.Deref(), php.Obj(held.inner)}, depth)
	return accepted.Deref().Bool(), err
}

// aggregateIterator runs getIterator() on an IteratorAggregate and returns the
// object it produced, or nil when the class is not an aggregate at all.
func (in *Interp) aggregateIterator(ctx *php.Ctx, unit *Unit, object *php.Object,
	depth int) (*php.Object, error) {
	class, known := unit.findClass(object.ClassName)
	if !known || !unit.instanceOf(class, "IteratorAggregate") {
		return nil, nil
	}
	if _, backed := asArrayStore(object); backed {
		produced, _, err := in.arrayStoreMethod(ctx, unit, object, "getIterator", nil)
		return produced.Deref().Object(), err
	}
	body, declaring, found := unit.resolveMethod(class, "getIterator")
	if !found {
		return nil, nil
	}
	produced, err := in.invoke(ctx, unit, body, object, declaring, class, nil, depth)
	if err != nil {
		return nil, err
	}
	return produced.Deref().Object(), nil
}

// callInnerIterator puts one of the Iterator questions to the wrapped object,
// which may be an engine array store, another filter, a generator, or a PHP
// class.
func (in *Interp) callInnerIterator(ctx *php.Ctx, unit *Unit, object *php.Object,
	name string, depth int) (php.Value, error) {
	if value, handled, err := emptyIteratorMethod(unit, &pendingCall{this: object, builtinMethod: name}); handled {
		return value, err
	}
	if g, ok := asGenerator(php.Obj(object)); ok {
		return in.generatorMethod(ctx, unit, g, name, nil)
	}
	if held, ok := asCallbackFilter(object); ok {
		nested := &pendingCall{this: object, builtinMethod: name}
		value, handled, err := in.callbackFilterMethod(ctx, unit, nested, depth)
		if handled {
			return value, err
		}
		_ = held
	}
	if value, handled, err := in.arrayStoreMethod(ctx, unit, object, name, nil); handled {
		return value, err
	}
	class, known := unit.findClass(object.ClassName)
	if !known {
		return php.Undef(), &UnsupportedError{Opcode: "Iterator::" + name + " on " + object.ClassName}
	}
	body, declaring, found := unit.resolveMethod(class, name)
	if !found {
		return php.Undef(), &UnsupportedError{Opcode: "Iterator::" + name + " on " + object.ClassName}
	}
	return in.invokeDriven(ctx, unit, body, object, declaring, class, nil, depth)
}
