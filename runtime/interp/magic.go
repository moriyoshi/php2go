package interp

import (
	"github.com/moriyoshi/php2go/runtime/php"
)

// The magic accessors, which turn a property read, write, isset or unset on a
// property the object does not have into a method call.
//
// PHP applies them only when the property is *inaccessible* -- absent, or
// declared but not visible from here. Visibility is not modelled yet, so
// "absent" is the whole test; that is right for the common case of a class
// that stores everything in an array and exposes it through __get.
const (
	magicGet      = "__get"
	magicSet      = "__set"
	magicIsset    = "__isset"
	magicUnset    = "__unset"
	magicCall     = "__call"
	magicCallStat = "__callStatic"
	magicToString = "__toString"
	magicInvoke   = "__invoke"
)

// magicGuard names one in-progress magic call: PHP does not re-enter an
// accessor for the property it is already handling, so `return $this->$name`
// inside __get reads the real (missing) property rather than looping.
// guardOf is the handle an object's in-flight accessors are recorded under. A
// lazy proxy lends its own to the instance behind it, since the two are one
// object as far as a property access is concerned.
func guardOf(object *php.Object) int {
	if object.Guard != 0 {
		return object.Guard
	}
	return object.ID
}

type magicGuard struct {
	object int
	method string
	prop   string
}

// enterMagic reports whether the accessor may run, and marks it in progress.
// The caller must call leaveMagic when it returns.
func (u *Unit) enterMagic(object *php.Object, method, prop string) bool {
	key := magicGuard{guardOf(object), method, prop}
	if u.magicInFlight[key] {
		return false
	}
	if u.magicInFlight == nil {
		u.magicInFlight = map[magicGuard]bool{}
	}
	u.magicInFlight[key] = true
	return true
}

// magicAvailable reports whether an accessor could be entered for this
// property right now -- it exists and is not already running for it. A check
// that wants to know whether the accessor will answer, rather than to call it,
// asks this instead of claiming the guard.
func (u *Unit) magicAvailable(object *php.Object, method, prop string) bool {
	if _, _, ok := u.magicMethod(object, method); !ok {
		return false
	}
	return !u.magicInFlight[magicGuard{guardOf(object), method, prop}]
}

func (u *Unit) leaveMagic(object *php.Object, method, prop string) {
	delete(u.magicInFlight, magicGuard{guardOf(object), method, prop})
}

// magicMethod finds an accessor on the object's class, reporting false when
// there is none to call.
func (u *Unit) magicMethod(object *php.Object, name string) (*Class, *Class, bool) {
	class, ok := u.findClass(object.ClassName)
	if !ok {
		return nil, nil, false
	}
	if _, declaring, found := u.resolveMethod(class, name); found {
		return class, declaring, true
	}
	return nil, nil, false
}

// stringify renders an object through __toString, reporting false when the
// class has none. It is not a coercion the value model can perform on its own:
// the conversion is a method call, and only the interpreter can make one.
func (in *Interp) stringify(ctx *php.Ctx, unit *Unit, value php.Value) (php.Value, bool) {
	object := value.Deref().Object()
	if object == nil {
		return value, false
	}
	class, declaring, ok := unit.magicMethod(object, magicToString)
	if !ok {
		return value, false
	}
	body, _, found := unit.resolveMethod(class, magicToString)
	if !found {
		return value, false
	}
	rendered, err := in.invokeDriven(ctx, unit, body, object, declaring, class, nil, 0)
	if err != nil {
		// __toString threw. The coercion never happened, and there is no type
		// error to report either: the throw is the outcome. checkType has
		// nowhere to return it, so it is parked and picked up by whichever
		// caller was about to complain -- or by the opcode loop.
		ctx.ParkError(err)
		return value, false
	}
	return php.String(rendered.Deref().String()), true
}

// hasMagic reports whether the object's class declares an accessor. It is
// asked before turning an inaccessible property into an error, since PHP hands
// such an access to the accessor instead.
func (u *Unit) hasMagic(object *php.Object, name string) bool {
	_, _, ok := u.magicMethod(object, name)
	return ok
}

// callMagic runs an accessor, reporting false when the class has none or the
// same call is already in progress -- in which case the caller falls back to
// its ordinary behaviour.
func (in *Interp) callMagic(ctx *php.Ctx, unit *Unit, object *php.Object, name, prop string, args []php.Value, depth int) (php.Value, bool, error) {
	class, declaring, ok := unit.magicMethod(object, name)
	if !ok || !unit.enterMagic(object, name, prop) {
		return php.Undef(), false, nil
	}
	defer unit.leaveMagic(object, name, prop)

	method, _, _ := unit.resolveMethod(class, name)
	// An accessor is a frame like any other in a backtrace: an exception raised
	// inside __get or __unset names it, attributed to the access that ran it.
	file, line := ctx.Location()
	ctx.PushCall(php.StackFrame{
		Function: php.DisplayName(declaring.Name) + "->" + name,
		File:     file, Line: line, This: object, Args: args,
	})
	value, err := in.invoke(ctx, unit, method, object, declaring, class, args, depth)
	ctx.PopCall()
	return value, true, err
}

// toString renders a value where PHP needs a string: echo, a (string) cast,
// concatenation, interpolation. An object is only convertible through
// __toString, and a class without one is an Error rather than an empty string.
//
// The method's return value is checked like any other -- __toString has an
// implicit `string` return type since PHP 8 -- so a body that returns nothing
// raises rather than yielding "".
func (in *Interp) toString(ctx *php.Ctx, unit *Unit, value php.Value) (php.Value, error) {
	held := value.Deref()
	if held.Kind() != php.KindObject {
		if held.Kind() == php.KindArray {
			ctx.Warn("Array to string conversion")
		}
		return php.String(held.String()), nil
	}
	object := held.Object()
	class, declaring, ok := unit.magicMethod(object, magicToString)
	if !ok {
		// Every Throwable has a __toString the engine supplies in C, so
		// nothing is harvested for it and the class table cannot answer.
		if declared, known := unit.findClass(object.ClassName); known && unit.isThrowable(declared) {
			if rendered, handled := throwableMethod(ctx, magicToString, object); handled {
				return rendered, nil
			}
		}
		// Any other engine class that declares __toString answers through the
		// builtin dispatcher: ReflectionNamedType renders as the type it names.
		if declared, known := unit.findClass(object.ClassName); known {
			if _, engineHas := unit.engineMethod(declared, magicToString); engineHas {
				rendered, err := in.builtinMethodCall(ctx, unit, &pendingCall{
					this: object, builtinMethod: magicToString,
					name: object.ClassName + "::" + magicToString,
				})
				if err != nil {
					return php.Undef(), err
				}
				return php.String(rendered.Deref().String()), nil
			}
		}
		return php.Undef(), Raise("Error",
			"Object of class %s could not be converted to string",
			php.DisplayName(object.ClassName))
	}
	body, _, found := unit.resolveMethod(class, magicToString)
	if !found {
		return php.Undef(), Raise("Error",
			"Object of class %s could not be converted to string",
			php.DisplayName(object.ClassName))
	}
	rendered, err := in.invokeDriven(ctx, unit, body, object, declaring, class, nil, 0)
	if err != nil {
		return php.Undef(), err
	}
	return php.String(rendered.Deref().String()), nil
}

// renderForComparison converts an object to a string when it is being compared
// with one. PHP does this for the loose operators only -- `===` compares types,
// so an object is never equal to a string under it -- and only when exactly one
// side is an object: two objects compare as objects.
func (in *Interp) renderForComparison(ctx *php.Ctx, unit *Unit, a, b php.Value) (php.Value, php.Value, error) {
	// Comparing an object reads its state, so a lazy one produces it first.
	for _, side := range []*php.Value{&a, &b} {
		if object := side.Deref().Object(); object != nil && object.Lazy != nil {
			resolved, err := in.resolveLazy(ctx, unit, object, "", "", 0)
			if err != nil {
				return a, b, err
			}
			*side = php.Obj(resolved)
		}
	}
	left, right := a.Deref(), b.Deref()
	switch {
	case left.Kind() == php.KindObject && right.Kind() == php.KindString:
		if !unit.hasMagic(left.Object(), magicToString) {
			return a, b, nil
		}
		rendered, err := in.toString(ctx, unit, left)
		return rendered, b, err
	case right.Kind() == php.KindObject && left.Kind() == php.KindString:
		if !unit.hasMagic(right.Object(), magicToString) {
			return a, b, nil
		}
		rendered, err := in.toString(ctx, unit, right)
		return a, rendered, err

	// Beside a number an object has no string form to fall back on -- even one
	// with __toString. It converts to 1, and says so -- naming the type it was
	// being converted *to*, which is the other operand's.
	case left.Kind() == php.KindObject && isNumber(right.Kind()):
		ctx.Notice("Object of class %s could not be converted to %s",
			php.DisplayName(left.Object().ClassName), numberTypeName(right.Kind()))
		return php.Int(1), b, nil
	case right.Kind() == php.KindObject && isNumber(left.Kind()):
		ctx.Notice("Object of class %s could not be converted to %s",
			php.DisplayName(right.Object().ClassName), numberTypeName(left.Kind()))
		return a, php.Int(1), nil
	}
	return a, b, nil
}

func isNumber(k php.Kind) bool { return k == php.KindInt || k == php.KindFloat }

func numberTypeName(k php.Kind) string {
	if k == php.KindFloat {
		return "float"
	}
	return "int"
}

// overloadedWriteFetch answers a write-mode fetch of a property the object does
// not really have but __get does. There is no slot to alias, so the engine runs
// the accessor, warns that the modification will not stick, and hands back the
// value -- which the caller then modifies to no effect.
func (in *Interp) overloadedWriteFetch(ctx *php.Ctx, unit *Unit, object *php.Object,
	property string, scope *Class, strict bool, depth int) (bool, php.Value, error) {
	if object == nil || !unit.hasMagic(object, magicGet) {
		return false, php.Undef(), nil
	}
	_, verdict := unit.propertyAccess(object, property, scope)
	if verdict == accessAllowed {
		if value, ok := object.Get(property); ok && value.Deref().Kind() != php.KindUndef {
			return false, php.Undef(), nil
		}
	}
	// The accessor is a call and a call moves the reported location; the notice
	// belongs to the modification site, not to __get's body.
	file, line := ctx.Location()
	value, handled, err := in.callMagic(ctx, unit, object, magicGet,
		property, []php.Value{php.String(property)}, depth)
	ctx.SetLocation(file, line)
	if err != nil || !handled {
		return handled, php.Undef(), err
	}
	// The declaration outlives the unset that sent the fetch here, so whatever
	// __get produced for a declared typed property still has to be that type.
	converted, typeErr := in.magicReadType(ctx, unit, object, property, value,
		verdict == accessAllowed, strict)
	if typeErr != nil {
		return true, php.Undef(), typeErr
	}
	// A `&__get` hands back the slot itself, so writing through what it
	// returned reaches the object and there is nothing to warn about. The
	// reference travels out unchanged for the caller to bind -- carrying the
	// coerced value, since the box is what the caller is about to alias.
	if ref, byRef := value.RefOf(); byRef {
		ref.Set(converted.Deref())
		return true, value, nil
	}
	value = converted
	// An object needs no writing back either: modifying it through the handle
	// the accessor returned reaches the same instance. Anything else is a copy
	// the write would be lost in.
	if value.Deref().Kind() != php.KindObject {
		ctx.Notice("Indirect modification of overloaded property %s::$%s has no effect",
			php.DisplayName(object.ClassName), property)
		// And it really has none: what the accessor returned is a value, so
		// the caller modifies a copy. Handing back the original array would
		// let the write reach the object after all.
		return true, value.Deref().Copy(), nil
	}
	return true, value.Deref(), nil
}

// magicCompound runs a read-modify-write against a property the object's own
// table cannot serve: `$o->n++` and `$o->n .= "x"` both read before they write,
// and when the name reaches __get it is the accessor that supplies the old
// value. Where the new one goes depends on what else the class defines -- __set
// if there is one, and otherwise a real property, created right there with the
// deprecation that creating one earns.
//
// It reports handled=false when there is no __get to ask, which leaves the
// caller to go on with the property table as before.
func (in *Interp) magicCompound(ctx *php.Ctx, unit *Unit, object *php.Object,
	property string, scope *Class, step func(php.Value) (php.Value, error),
	depth int) (old, updated php.Value, handled bool, err error) {
	if object == nil || !unit.slotAbsent(object, property) ||
		!unit.magicAvailable(object, magicGet, property) {
		return php.Undef(), php.Undef(), false, nil
	}
	// The accessor is a call and a call moves the reported location; what the
	// step and the write report afterwards belongs to the modification site,
	// not to __get's body.
	file, at := ctx.Location()
	old, called, err := in.callMagic(ctx, unit, object, magicGet,
		property, []php.Value{php.String(property)}, depth)
	ctx.SetLocation(file, at)
	if err != nil || !called {
		return php.Undef(), php.Undef(), called, err
	}
	if updated, err = step(old.Deref()); err != nil {
		return old, php.Undef(), true, err
	}
	if unit.magicAvailable(object, magicSet, property) {
		_, _, err = in.callMagic(ctx, unit, object, magicSet,
			property, []php.Value{php.String(property), updated}, depth)
		return old, updated, true, err
	}
	// No __set: the value lands in the table, and the property it lands in is
	// one the class never declared.
	_, verdict := unit.propertyAccess(object, property, scope)
	unit.noteDynamicProperty(ctx, object, property, verdict)
	object.Set(property, updated)
	return old, updated, true, nil
}
