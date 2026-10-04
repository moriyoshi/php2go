package interp

import (
	"strings"

	"github.com/moriyoshi/php2go/runtime/php"
)

// The state behind ArrayIterator and ArrayObject, and the methods over it, live
// in `runtime/php` -- both back-ends need them, since a compiled program that
// writes `new ArrayIterator($a)` gets the same object with the same methods.
// What stays here is the part that has to ask *this* back-end's class table.

type arrayStore = php.ArrayStore

var newArrayStore = php.NewArrayStore

func asArrayStore(object *php.Object) (*arrayStore, bool) { return php.AsArrayStore(object) }

// isArrayBackedClass answers for the subclasses too. Neither engine class is
// final, and a program that extends one gets the same store -- which is what
// makes an inherited offsetSet reach it and what makes var_dump show the
// storage the parent declares.
func isArrayBackedClass(unit *Unit, className string) bool {
	if php.ArrayBackedClasses[strings.ToLower(className)] {
		return true
	}
	class, known := unit.findClass(className)
	if !known {
		return false
	}
	for name := range php.ArrayBackedClasses {
		if unit.instanceOf(class, name) {
			return true
		}
	}
	return false
}

// plainArrayStore is asArrayStore for the case where the engine's own dimension
// handler answers: the class must not have written its own offsetGet, since the
// handler calls an override when there is one and the answer is then a value
// like any other accessor's.
func (u *Unit) plainArrayStore(object *php.Object) (*arrayStore, bool) {
	store, ok := asArrayStore(object)
	if !ok || store.Items() == nil {
		return nil, false
	}
	class, known := u.findClass(object.ClassName)
	if !known {
		return nil, false
	}
	if _, _, overridden := u.resolveMethod(class, offsetGet); overridden {
		return nil, false
	}
	return store, true
}

// arrayStoreMethod dispatches to the shared implementation. The hooks it needs
// -- the subclass test and instantiating the ArrayIterator getIterator hands
// back -- are the ones the Ctx already carries.
func (in *Interp) arrayStoreMethod(ctx *php.Ctx, unit *Unit, object *php.Object,
	name string, args []php.Value) (php.Value, bool, error) {
	return ctx.ArrayStoreMethod(object, name, args)
}

// countOf answers count(). A Countable object counts itself through its own
// method, which for an engine class this runtime implements is a Go function
// and for a user class is an ordinary call.
func (in *Interp) countOf(ctx *php.Ctx, unit *Unit, value php.Value, depth int) (php.Value, bool, error) {
	object := value.Deref().Object()
	if object == nil {
		return php.Undef(), false, nil
	}
	if counted, handled, err := in.weakMethod(ctx, unit, object, "count", nil); handled {
		return counted, true, err
	}
	if counted, handled, err := in.arrayStoreMethod(ctx, unit, object, "count", nil); handled {
		return counted, true, err
	}
	class, known := unit.findClass(object.ClassName)
	if !known || !unit.instanceOf(class, "Countable") {
		return php.Undef(), false, nil
	}
	body, declaring, found := unit.resolveMethod(class, "count")
	if !found {
		return php.Undef(), false, nil
	}
	counted, err := in.invokeDriven(ctx, unit, body, object, declaring, class, nil, depth)
	return php.Int(counted.Deref().Int()), true, err
}

// The ArrayAccess method names, lowercased once so the dispatch switch above
// does not have to spell the mixed-case forms.
const (
	offsetGetLower    = "offsetget"
	offsetSetLower    = "offsetset"
	offsetExistsLower = "offsetexists"
	offsetUnsetLower  = "offsetunset"
)
