package interp

import (
	"strings"

	"github.com/moriyoshi/php2go/runtime/php"
)

// WeakMap and WeakReference, as far as a runtime with no reachability
// information can honestly go.
//
// The *weakness* is not modelled: php2go cannot know when an object's last
// holder goes away, so nothing is ever dropped and a WeakReference::get() keeps
// answering. That is wrong for the handful of tests that check collection --
// they are the point of the classes -- and right for the many that use a
// WeakMap as an object-keyed map, which is most of what the corpus does with
// them. Where it is wrong the test fails; nothing here reports a pass it has
// not earned.

// weakClasses are the engine classes this file implements.
var weakClasses = map[string]bool{
	"weakmap":       true,
	"weakreference": true,
}

// The store and the methods live in `runtime/php`: both back-ends need them,
// and the two questions they cannot answer for themselves -- when a key has
// died, and what letting go of a value implies -- are hooks installed in
// call.go.

type weakMap = php.WeakMapStore

type weakRef = php.WeakRefStore

func asWeakMap(object *php.Object) (*weakMap, bool) { return php.AsWeakMapStore(object) }

func newWeakMap(object *php.Object, _ []php.Value) { php.NewWeakMapStore(object) }

// registerWeak is called wherever a weak container is made, so a collection
// knows where to look for entries to drop.
func (u *Unit) registerWeak(object *php.Object) { u.noteWeak(object) }

func (in *Interp) weakMethod(ctx *php.Ctx, unit *Unit, object *php.Object,
	name string, args []php.Value) (php.Value, bool, error) {
	return php.WeakMethod(ctx, object, name, args)
}

func weakMapDebugInfo(ctx *php.Ctx, unit *Unit, object *php.Object) (*php.Array, bool) {
	return php.WeakMapDebugInfo(ctx, object)
}

func weakRefDebugInfo(object *php.Object) (*php.Array, bool) {
	return php.WeakRefDebugInfo(object)
}

func newWeakReference(object *php.Object, _ []php.Value) error {
	// **No parentheses on `create`** -- the engine's wording for the `new`
	// path, which is not the same as the one WeakMethod raises.
	return php.Raise("Error",
		"Direct instantiation of WeakReference is not allowed, use WeakReference::create instead")
}

func (in *Interp) weakStaticCall(unit *Unit, className, method string,
	args []php.Value) (php.Value, bool, error) {
	if !strings.EqualFold(className, "WeakReference") || !strings.EqualFold(method, "create") {
		return php.Undef(), false, nil
	}
	if len(args) == 0 || args[0].Deref().Kind() != php.KindObject {
		given := "no value"
		if len(args) > 0 {
			given = givenTypeName(args[0].Deref())
		}
		return php.Undef(), true, Raise("TypeError",
			"WeakReference::create(): Argument #1 ($object) must be of type object, %s given", given)
	}
	class, known := unit.findClass("WeakReference")
	if !known {
		return php.Undef(), false, nil
	}
	object := unit.instantiate(class)
	object.Internal = php.NewWeakRefStore(args[0].Deref().Object())
	unit.registerWeak(object)
	return php.Obj(object), true, nil
}
