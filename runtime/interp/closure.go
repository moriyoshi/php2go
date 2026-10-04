package interp

import (
	"strings"

	pb "github.com/moriyoshi/php2go/internal/ir/irpb"
	"github.com/moriyoshi/php2go/runtime/php"
)

// closure is what a `function () {}` expression evaluates to. PHP models it as
// an instance of the Closure class, so it is carried on an object rather than a
// value kind of its own: `$f instanceof Closure` and get_class($f) then work
// without special cases.
type closure struct {
	op *pb.OpArray
	// bound holds the `use` variables, captured when the closure was created
	// rather than when it is called.
	bound map[string]php.Value
	// this and class are the scope the closure was defined in, which is what
	// $this and self:: mean inside its body.
	this  *php.Object
	class *Class
	// called is what static:: means inside the body. A closure created inside
	// a method keeps the late static binding of the call that made it, which
	// is not always the class that declared the method.
	called *Class
	// builtin names an engine-provided function wrapped by the first-class
	// callable syntax, which has no op_array to run.
	builtin string
	// forwardTo is set when the closure stands for a method the class does not
	// declare: `$obj->whatever(...)` on a class with __call wraps __call, and
	// invoking it has to rebuild the (name, args) pair that accessor expects.
	forwardTo string
	// fake marks a closure made from an existing function -- `f(...)` or
	// Closure::fromCallable -- rather than written as one. The engine calls
	// these fake closures, and var_dump describes them differently.
	fake bool
	// scopeName is the class a fake closure's method belongs to, kept so the
	// debug rendering can say `K::m` even when the class entry is not around.
	scopeName string
}

// asClosure recovers the code behind a Closure instance.
func asClosure(value php.Value) (*closure, bool) {
	if value.Kind() != php.KindObject {
		return nil, false
	}
	fn, ok := value.Object().Internal.(*closure)
	return fn, ok
}

// newClosure creates the Closure instance for a lambda declaration.
func (u *Unit) newClosure(op *pb.OpArray, this *php.Object, class, called *Class) *php.Object {
	object := u.born(php.NewObject("Closure", u.handle()))
	object.Internal = &closure{
		op:     op,
		bound:  map[string]php.Value{},
		this:   this,
		class:  class,
		called: called,
	}
	return object
}

// closureTraceName is what a stack trace calls a closure. Since PHP 8.4 that is
// the compiled name -- `{closure:file:line}` -- rather than a bare `{closure}`,
// and the compiler already put it on the op_array.
func closureTraceName(fn *closure) string {
	if fn == nil {
		return "{closure}"
	}
	if fn.builtin != "" {
		return fn.builtin
	}
	if name := fn.op.GetName(); name != "" {
		// A closure with a scope is named in a trace the way a method is:
		// `K->{closure:...}` when it has a $this, `K::{closure:...}` when it
		// does not. The compiled name already carries where the closure was
		// written; the prefix says what it is bound to.
		if fn.class != nil {
			separator := "::"
			if fn.this != nil {
				separator = "->"
			}
			return php.DisplayName(fn.class.Name) + separator + name
		}
		if fn.this != nil {
			// Bound to an object with the scope explicitly dropped
			// (`bindTo($obj, null)`). There is no class to name, so the trace
			// names the one the value actually is.
			return "Closure->" + name
		}
		return name
	}
	return "{closure}"
}

// bindLexical captures one `use` variable. PHP captures by value at creation
// time, so a later change to the outer variable is not visible inside.
func bindLexical(target php.Value, name string, value php.Value) bool {
	fn, ok := asClosure(target)
	if !ok {
		return false
	}
	// A by-reference capture arrives already boxed and must stay shared; a
	// by-value one is copied so later changes outside are not visible.
	if value.IsRef() {
		fn.bound[name] = value
	} else {
		fn.bound[name] = value.Copy()
	}
	return true
}

// staticDefault returns the initializer declared for a static variable, which
// is what ZEND_BIND_STATIC installs the first time it runs.
func staticDefault(op *pb.OpArray, name string) (*pb.Literal, bool) {
	for _, entry := range op.GetStaticVars() {
		if entry.GetName() != name {
			continue
		}
		if !entry.GetHasDefault() {
			return nil, false
		}
		return entry.GetDefaultValue(), true
	}
	return nil, false
}

// closureFromCall turns a pending call into a Closure, which is what the
// first-class callable syntax `f(...)` produces. The target was already
// resolved by the INIT opline, so nothing is looked up again here.
func (u *Unit) closureFromCall(call *pendingCall) (php.Value, bool) {
	if call.closureObject != nil {
		// `$c(...)` where $c is already a Closure is the same object, not a
		// copy of it: the engine hands it straight back and `===` says so.
		return php.Obj(call.closureObject), true
	}
	switch {
	case call.closure != nil:
		object := u.born(php.NewObject("Closure", u.handle()))
		object.Internal = call.closure
		return php.Obj(object), true
	case call.method != nil:
		object := u.born(php.NewObject("Closure", u.handle()))
		scopeName := ""
		if call.class != nil {
			scopeName = call.class.Name
		}
		object.Internal = &closure{
			op:    call.method,
			bound: map[string]php.Value{},
			this:  call.this,
			class: call.class,
			// The late static binding travels with the callable: `Bar::m(...)`
			// where Bar inherits m from Foo keeps Bar, so `static::` inside the
			// body answers Bar when the closure is called.
			called:    call.called,
			forwardTo: call.forwardTo,
			fake:      true,
			scopeName: scopeName,
		}
		return php.Obj(object), true
	}
	if call.this != nil && strings.EqualFold(call.builtinMethod, magicInvoke) {
		if _, held := asClosure(php.Obj(call.this)); held {
			// `$closure->__invoke(...)` is the closure itself written the long
			// way, and hands back that very object.
			return php.Obj(call.this), true
		}
	}
	// A builtin has no op_array to wrap. Its name is enough for a dynamic call,
	// so the closure is represented by that name instead.
	if call.name != "" {
		object := u.born(php.NewObject("Closure", u.handle()))
		object.Internal = &closure{builtin: call.name, bound: map[string]php.Value{}, fake: true}
		return php.Obj(object), true
	}
	return php.Undef(), false
}

// validClosureBinding is the check both bindTo and call make before rebinding.
// Most of it is about a *fake* closure -- one made from a function or a method
// that already exists rather than written as one. Such a closure is a handle on
// something with a fixed identity, so it cannot be moved to another scope, and
// a method's handle cannot be moved to an object the method is not on.
func (u *Unit) validClosureBinding(ctx *php.Ctx, fn *closure, newThis *php.Object, scope *Class) bool {
	switch {
	case newThis != nil && fn.op.GetStatic():
		// A static closure never has an instance, and asking for one is not
		// silently downgraded: the bind fails and returns null.
		ctx.Warn("Cannot bind an instance to a static closure")
		return false
	case newThis != nil && fn.fake && fn.class != nil:
		held, found := u.findClass(newThis.ClassName)
		if !found || !u.instanceOf(held, fn.class.Name) {
			ctx.Warn("Cannot bind method %s::%s() to object of class %s",
				php.DisplayName(fn.class.Name), fn.op.GetName(),
				php.DisplayName(newThis.ClassName))
			return false
		}
	case newThis == nil && fn.fake && fn.class != nil && !fn.op.GetStatic():
		ctx.Warn("Cannot unbind $this of method")
		return false
	case newThis == nil && fn.this != nil && fn.op.GetUsesThis():
		// Unbinding is refused when the body actually uses $this: the closure
		// would fail on its first statement, so the bind reports it instead.
		ctx.Warn("Cannot unbind $this of closure using $this")
		return false
	}
	if fn.fake && scope != fn.class {
		// call() always names a scope, which is why it refuses every fake
		// closure while bindTo($obj) alone does not.
		kind := "function"
		if fn.class != nil {
			kind = "method"
		}
		ctx.Warn("Cannot rebind scope of closure created from %s", kind)
		return false
	}
	return true
}

// closureMethod implements the Closure class, whose methods the engine provides
// in C.
func (in *Interp) closureMethod(ctx *php.Ctx, unit *Unit, this *php.Object, name string, args []php.Value) (php.Value, error) {
	fn, ok := this.Internal.(*closure)
	if !ok {
		return php.Undef(), &UnsupportedError{Opcode: "Closure::" + name + " on a non-closure"}
	}
	switch strings.ToLower(name) {
	case "__invoke":
		// Calling a closure through the method the language gives every one of
		// them, rather than through the call syntax. The engine reaches the
		// body through a trampoline, so a diagnostic raised while binding the
		// arguments has no script call site to name.
		unit.pending.viaTrampoline = true
		return in.invokeClosure(ctx, unit, fn, args, 0)

	case "call":
		// call() runs the closure with a different $this, which also becomes
		// its scope.
		if len(args) == 0 {
			return php.Undef(), Raise("ArgumentCountError",
				"Closure::call() expects at least 1 argument, 0 given")
		}
		if given := args[0].Deref(); given.Kind() != php.KindObject {
			return php.Undef(), Raise("TypeError",
				"Closure::call(): Argument #1 ($newThis) must be of type object, %s given",
				givenTypeName(given))
		}
		rebound := *fn
		rebound.this = args[0].Object()
		scope := fn.class
		if class, found := unit.findClass(args[0].Object().ClassName); found {
			// call() sets the scope to the object's class, and a class the
			// engine supplies has no scope a closure may enter -- there is no
			// PHP body for its private members to belong to.
			if refused := unit.refuseInternalScope(ctx, class); refused {
				return php.Null(), nil
			}
			scope = class
		}
		if !unit.validClosureBinding(ctx, fn, rebound.this, scope) {
			return php.Null(), nil
		}
		rebound.class = scope
		return in.invokeClosure(ctx, unit, &rebound, args[1:], 0)

	case "bindto":
		bound, err := unit.bindClosure(ctx, fn, args, "Closure::bindTo", 0)
		if err != nil {
			return php.Undef(), err
		}
		return bound, nil
	}
	return php.Undef(), in.undefinedEngineMethod(ctx, unit, "Closure", name,
		&UnsupportedError{Opcode: "Closure::" + name})
}

// bindClosure produces a copy of a closure with a new $this and scope, which is
// what both bindTo() and the static bind() do.
func (u *Unit) bindClosure(ctx *php.Ctx, fn *closure, args []php.Value,
	spelling string, offset int) (php.Value, error) {
	// Both spellings share these parameters and differ only in where they sit,
	// so the argument numbers are offset rather than written twice.
	var newThis *php.Object
	if len(args) > 0 {
		switch given := args[0].Deref(); given.Kind() {
		case php.KindObject:
			newThis = given.Object()
		case php.KindNull, php.KindUndef:
		default:
			return php.Undef(), Raise("TypeError",
				"%s(): Argument #%d ($newThis) must be of type ?object, %s given",
				spelling, offset+1, givenTypeName(given))
		}
	}
	if len(args) > 1 {
		// `object|string|null` in weak mode: an int, float or bool has a string
		// form and is taken as a class name (which then does not exist). An
		// array has none, and that is the refusal.
		switch given := args[1].Deref(); given.Kind() {
		case php.KindArray:
			return php.Undef(), Raise("TypeError",
				"%s(): Argument #%d ($newScope) must be of type object|string|null, %s given",
				spelling, offset+2, givenTypeName(given))
		}
	}

	// The scope argument defaults to the string "static", which means keep the
	// current one. Omitting it and passing null are different requests: null
	// unscopes. The instance never decides the scope -- it decides what
	// static:: resolves to, which is a separate thing.
	scope := fn.class
	if len(args) > 1 {
		switch given := args[1].Deref(); given.Kind() {
		case php.KindNull, php.KindUndef:
			scope = nil
		case php.KindObject:
			if class, found := u.findClass(given.Object().ClassName); found {
				if u.refuseInternalScope(ctx, class) {
					return php.Null(), nil
				}
				scope = class
			}
		default:
			if name := given.String(); !strings.EqualFold(name, "static") {
				class, found := u.findClass(name)
				if !found {
					ctx.Warn("Class %q not found", name)
					return php.Null(), nil
				}
				if u.refuseInternalScope(ctx, class) {
					return php.Null(), nil
				}
				scope = class
			}
		}
	}

	if !u.validClosureBinding(ctx, fn, newThis, scope) {
		return php.Null(), nil
	}

	rebound := *fn
	rebound.this = newThis
	rebound.class = scope
	// zend_create_closure takes called_scope from the new instance when there
	// is one and from the new scope otherwise.
	rebound.called = scope
	if newThis != nil {
		if class, found := u.findClass(newThis.ClassName); found {
			rebound.called = class
		}
	}
	object := u.born(php.NewObject("Closure", u.handle()))
	object.Internal = &rebound
	return php.Obj(object), nil
}

// closureStatic implements Closure::fromCallable and Closure::bind, which are
// static and so never reach closureMethod.
func (in *Interp) closureStatic(ctx *php.Ctx, u *Unit, name string, args []php.Value) (php.Value, bool, error) {
	switch strings.ToLower(name) {
	case "fromcallable":
		if len(args) == 0 {
			return php.Undef(), true, Raise("ArgumentCountError",
				"Closure::fromCallable() expects exactly 1 argument, 0 given")
		}
		if fn, ok := asClosure(args[0]); ok {
			_ = fn
			return args[0], true, nil
		}
		// The engine words every one of these the same way, with the reason
		// the callable check produced: `Failed to create closure from
		// callable: class "X" not found`. It is the same reason
		// call_user_func puts after "must be a valid callback".
		if reason := in.callableReason(ctx, u, args[0]); reason != "" {
			return php.Undef(), true, Raise("TypeError",
				"Failed to create closure from callable: %s", reason)
		}
		// Quiet: the check above has already reported whatever the *form*
		// earns, and the engine reports a deprecated spelling once.
		target, ok := u.dynamicTargetQuiet(ctx, args[0], true, true)
		if !ok {
			return php.Undef(), true, Raise("TypeError",
				"Failed to create closure from callable")
		}
		if target.refusal != "" {
			// The callable resolves but could not be *called* -- an instance
			// method named through a class name. fromCallable makes the
			// closure eagerly, so it refuses here rather than at the call, and
			// says so in its own words with the reason appended.
			return php.Undef(), true, Raise("TypeError",
				"Failed to create closure from callable: %s",
				strings.ToLower(target.refusal[:1])+target.refusal[1:])
		}
		converted, ok := u.closureFromCall(target)
		if !ok {
			return php.Undef(), true, Raise("TypeError",
				"Failed to create closure from callable")
		}
		return converted, true, nil

	case "bind":
		if len(args) < 2 {
			// `$newThis` has no default: bind() wants two arguments even
			// though the second may be null, and the count is checked before
			// the first one's type.
			return php.Undef(), true, Raise("ArgumentCountError",
				"Closure::bind() expects at least 2 arguments, %d given", len(args))
		}
		fn, ok := asClosure(args[0])
		if !ok {
			return php.Undef(), true, Raise("TypeError",
				"Closure::bind(): Argument #1 ($closure) must be of type Closure, %s given",
				givenTypeName(args[0].Deref()))
		}
		bound, err := u.bindClosure(ctx, fn, args[1:], "Closure::bind", 1)
		return bound, true, err
	}
	return php.Undef(), false, nil
}

// staticSlot returns the current value of a function's `static $x`, creating it
// from its declared initializer the first time. Statics outlive the call, so
// they are stored on the unit rather than the frame -- and, per the project's
// shared-nothing rule, on the unit rather than a package variable.
func (in *Interp) staticSlot(ctx *php.Ctx, u *Unit, op *pb.OpArray, name string, scope *Class, depth int) (php.Value, bool, error) {
	key := u.staticKey(op, name, scope)
	if value, ok := u.statics[key]; ok {
		return value, true, nil
	}
	initializer, declared := staticDefault(op, name)
	if !declared {
		return php.Undef(), false, nil
	}
	// `static $s = new B(5);` and `static $n = A + 1;` leave the compiler an
	// expression rather than a value, the same as any other initializer it
	// could not fold.
	value, resolved, err := in.constantLiteral(ctx, u, initializer, scope, depth)
	if err != nil {
		return php.Undef(), false, err
	}
	if !resolved {
		return php.Undef(), false, &UnsupportedError{
			Opcode: "static initializer for $" + name + unresolvedSuffix(initializer),
			File:   op.GetFilename(),
		}
	}
	return u.setStatic(op, name, value, scope), true, nil
}

// syncStatics used to write a frame's static variables back to where they
// persist. It no longer has to: ZEND_BIND_STATIC binds the compiled variable to
// the very box the table holds, so a write through the variable is the write to
// the static. What remains is the case where a slot was tracked before the box
// existed -- a static with a computed initializer, registered on the way in.
func (u *Unit) syncStatics(op *pb.OpArray, f *frame) {
	scope := f.class
	if len(f.staticSlots) == 0 {
		return
	}
	for slot, name := range f.staticSlots {
		if int(slot) >= len(f.slots) {
			continue
		}
		if _, boxed := u.statics[u.staticKey(op, name, scope)]; boxed {
			continue
		}
		if f.slots[slot].IsUndef() {
			// The initializer never produced a value -- it threw, which is the
			// only way to leave the slot empty. Recording it would make the
			// static count as initialized and stop the next call retrying,
			// which is what the engine does instead.
			continue
		}
		u.setStatic(op, name, f.slots[slot], scope)
	}
}

// setStatic records a static variable's value where it persists across calls,
// answering the box it now lives in. A static is *shared*, not copied in and
// out: a recursive call must see what the outer call wrote, which is what makes
// `static $first = true; $first = false;` guard a re-entry rather than reset it.
func (u *Unit) setStatic(op *pb.OpArray, name string, value php.Value, scope *Class) php.Value {
	if u.statics == nil {
		u.statics = map[string]php.Value{}
	}
	boxed := php.MakeRef(php.NewRef(value))
	u.statics[u.staticKey(op, name, scope)] = boxed
	return boxed
}

func (u *Unit) staticKey(op *pb.OpArray, name string, scope *Class) string {
	return op.GetFilename() + "\x00" + op.GetName() + "\x00" + name +
		"\x00" + u.staticNamespace(op, scope)
}

// staticNamespace is what keeps two methods' statics apart when the op_array
// alone cannot. The file and function name are not enough: `Base::c` and
// `Over::c` are both "c" in one file, and a trait's method is one op_array
// shared by every class that composes it.
//
// The declaring scope answers all of it. An inherited method's scope is the
// class that declared it, so `Base::c()` and `Sub::c()` share one static, which
// is what PHP does; a trait's method resolves with the *using* class as its
// scope, so each user gets its own copy, which is also what PHP does.
func (u *Unit) staticNamespace(op *pb.OpArray, scope *Class) string {
	if scope == nil {
		return ""
	}
	return strings.ToLower(scope.Name)
}

// staticInitialized reports whether a static variable already holds a value
// from an earlier call, which is what decides whether its initializer runs.
func (u *Unit) staticInitialized(op *pb.OpArray, name string, scope *Class) (php.Value, bool) {
	value, ok := u.statics[u.staticKey(op, name, scope)]
	return value, ok
}

// dynamicTarget resolves what `$f(...)` calls. PHP accepts a Closure, a
// function name as a string, "Class::method", and a two-element array of an
// object (or class name) and a method name.
func (u *Unit) dynamicTarget(ctx *php.Ctx, callee php.Value) (*pendingCall, bool) {
	return u.dynamicTargetQuiet(ctx, callee, false, true)
}

// dynamicTargetLiteral is dynamicTarget for `$var()`, which resolves no scope
// keywords: `["self", "m"]` called that way is `Class "self" not found`, while
// the same value handed to a `callable` parameter resolves and is merely
// deprecated. The keyword handling lives in the callable ZPP, not in the
// opcode.
func (u *Unit) dynamicTargetLiteral(ctx *php.Ctx, callee php.Value) (*pendingCall, bool) {
	return u.dynamicTargetQuiet(ctx, callee, false, false)
}

// dynamicTargetQuiet is dynamicTarget for a caller that has already resolved
// the callable once -- to validate it -- and so has already reported whatever
// deprecation the *form* earns. The engine reports it inside the one function
// every path goes through; here the two steps are separate, and reporting twice
// would be worse than passing the fact along.
func (u *Unit) dynamicTargetQuiet(ctx *php.Ctx, callee php.Value,
	quiet, keywords bool) (*pendingCall, bool) {
	if fn, ok := asClosure(callee); ok {
		if fn.builtin != "" {
			return &pendingCall{name: fn.builtin, closureObject: callee.Deref().Object()}, true
		}
		return &pendingCall{
			name: closureTraceName(fn), closure: fn,
			closureObject: callee.Deref().Object(),
		}, true
	}

	switch callee.Kind() {
	case php.KindString:
		name := callee.String()
		if class, method, found := strings.Cut(name, "::"); found {
			resolved, ok := u.callableClassQuiet(ctx, class, quiet, keywords)
			if !ok {
				return nil, false
			}
			call, found := u.staticTarget(ctx, resolved, method)
			if found {
				u.forwardCalled(ctx, call, class)
				if scopeKeyword(class) {
					// `"parent::m"` names a scope rather than a class, so the
					// instance in scope is the one to call it on -- the same
					// rule the ["C", "parent::m"] form follows. A literal class
					// name gets no such instance.
					u.forwardThis(ctx, call)
				}
			}
			return call, found
		}
		// Resolving a user function here rather than at the call rules on
		// whether its parameters are by reference, which the caller needs
		// before it has finished collecting the arguments.
		if fn, ok := u.functions[functionKey(name)]; ok {
			// A trace names the *declaration*, not the spelling the call site
			// used: `call_user_func("ABC")` shows `abc()`.
			return &pendingCall{name: fn.GetName(), method: fn}, true
		}
		return &pendingCall{name: name}, true

	case php.KindObject:
		// An object is callable when its class has __invoke, which is an
		// ordinary method: `$obj(...)` and `Closure::fromCallable($obj)` both
		// end up here.
		object := callee.Object()
		class, known := u.findClass(object.ClassName)
		if !known {
			return nil, false
		}
		method, declaring, found := u.resolveMethod(class, "__invoke")
		if !found {
			return nil, false
		}
		return &pendingCall{
			name:   object.ClassName + "::__invoke",
			this:   object,
			method: method,
			class:  declaring,
			called: class,
		}, true

	case php.KindArray:
		items := callee.Array()
		if items.Len() != 2 {
			return nil, false
		}
		target, _ := items.Get(php.IntKey(0))
		methodName, _ := items.Get(php.IntKey(1))
		if target.Kind() == php.KindString {
			resolved, ok := u.callableClassQuiet(ctx, target.String(), quiet, keywords)
			if !ok {
				return nil, false
			}
			if scoped, method, compound := strings.Cut(methodName.String(), "::"); compound {
				named, known := u.findClass(resolved)
				if !known {
					return nil, false
				}
				within, ok := u.scopedCallableClass(ctx, named, scoped)
				if !ok {
					return nil, false
				}
				if !u.instanceOf(named, within) {
					// `["A", "B::foo"]` names a scope A is not in. There is no
					// callable here, so there is nothing to call deprecated.
					return nil, false
				}
				u.noteCompoundCallable(ctx, named, methodName.String(), quiet)
				call, ok := u.staticTarget(ctx, within, method)
				if ok {
					u.forwardThis(ctx, call)
					u.forwardCalled(ctx, call, scoped)
				}
				return call, ok
			}
			call, found := u.staticTarget(ctx, resolved, methodName.String())
			if found {
				u.forwardCalled(ctx, call, target.String())
				if scopeKeyword(target.String()) {
					u.forwardThis(ctx, call)
				}
			}
			return call, found
		}
		if target.Kind() != php.KindObject {
			return nil, false
		}
		if fn, isClosure := asClosure(target); isClosure &&
			strings.EqualFold(methodName.String(), "__invoke") {
			// A closure reached through the method every closure has. The
			// engine goes through the `Closure::__invoke` trampoline to get
			// here, and its own diagnostics name that rather than the closure.
			return &pendingCall{
				name: closureTraceName(fn), closure: fn,
				invokedAs: "Closure::__invoke",
			}, true
		}
		object := target.Object()
		class, ok := u.findClass(object.ClassName)
		if !ok {
			return nil, false
		}
		lookup, name := class, methodName.String()
		if scoped, method, compound := strings.Cut(name, "::"); compound {
			// `[$obj, "parent::m"]` names the scope to start the lookup from.
			// The scope is the *object's* class, not the calling function's,
			// and the form is deprecated rather than refused.
			within, ok := u.scopedCallableClass(ctx, class, scoped)
			if !ok {
				return nil, false
			}
			if !u.instanceOf(class, within) {
				return nil, false
			}
			u.noteCompoundCallable(ctx, class, name, quiet)
			if lookup, ok = u.findClass(within); !ok {
				return nil, false
			}
			name = method
		}
		method, declaring, found := u.resolveMethod(lookup, name)
		if scope := u.callingScope(ctx); found && scope != nil && declaring != scope &&
			u.instanceOf(lookup, scope.Name) {
			// A private method is per-class here too: `call_user_func([$this,
			// $m])` inside A reaches A's private `m` even on a B instance that
			// overrides it. The same second lookup an ordinary `$this->m()`
			// makes.
			if own, isPrivate := u.privateInScope(scope, name); isPrivate {
				method, declaring = own, scope
			}
		}
		if !found || !u.methodAccessibleAs(method, declaring, u.callingScope(ctx), name) {
			// A method the class does not declare, or declares out of reach, is
			// answered by __call -- which makes the callable valid.
			if forwarded, ok := u.forwardToMagicCall(object, lookup, name); ok {
				return forwarded, true
			}
		}
		if !found {
			return nil, false
		}
		bound := object
		if method.GetStatic() {
			// A static method never has a $this, however the callable named it:
			// `[$obj, "staticMethod"]` runs it without one, and `$this` inside
			// is an Error. Only the *class* travels.
			bound = nil
		}
		return &pendingCall{
			name:   lookup.Name + "::" + name,
			this:   bound,
			method: method,
			class:  declaring,
			called: class,
		}, true
	}
	return nil, false
}

func (u *Unit) staticTarget(ctx *php.Ctx, className, methodName string) (*pendingCall, bool) {
	class, ok := u.findClass(className)
	if !ok {
		return nil, false
	}
	method, declaring, found := u.resolveMethod(class, methodName)
	if !found || !u.methodAccessibleAs(method, declaring, u.callingScope(ctx), methodName) {
		// A magic forwarder answers for a method that is missing or out of
		// reach, and which one applies depends on whether a $this is in play.
		if forwarder := u.forwarderFor(ctx, class, false); forwarder != "" {
			body, magicDeclaring, _ := u.resolveMethod(class, forwarder)
			call := &pendingCall{
				name: className + "::" + methodName, method: body,
				class: magicDeclaring, called: class, forwardTo: methodName, forwarding: true,
			}
			if forwarder == magicCall {
				// Reached through a class name, __call stands in for an
				// instance method, and the static-call refusal applies to it --
				// naming the method that was asked for rather than __call.
				// Unless the class also declares __callStatic: probed, a class
				// with both runs __call from inside an instance of itself and
				// is not refused, while one with only __call is.
				//
				// $this is left for forwardThis to supply, which is what makes
				// call_user_func lend it and clear the refusal while a plain
				// `$c()` keeps both.
				if _, _, hasStatic := u.resolveMethod(class, magicCallStat); !hasStatic {
					call.refusal = "Non-static method " + php.DisplayName(class.Name) +
						"::" + methodName + "() cannot be called statically"
				} else {
					call.this = u.callingThis(ctx)
				}
			}
			return call, true
		}
	}
	if !found {
		return nil, false
	}
	call := &pendingCall{
		// The declared spelling, not the one the callable used: a trace of
		// `call_user_func("k::M")` shows `K::m()`.
		name:   className + "::" + method.GetName(),
		method: method,
		class:  declaring,
		called: class,
	}
	if !method.GetStatic() {
		// Reaching an instance method through a class name has no instance to
		// call it on, and PHP stopped guessing one in 8.0 -- probed: even from
		// inside a method of that very class the call is refused, though
		// is_callable() there answers true. So the callable *resolves* and the
		// refusal happens where the call would have been.
		call.refusal = "Non-static method " + php.DisplayName(class.Name) +
			"::" + methodName + "() cannot be called statically"
	}
	return call, true
}

// callableClass resolves the class half of a callable. self, parent and static
// name something only the calling scope knows, and naming them in a callable at
// all has been deprecated since PHP 8.2 -- the deprecation is raised wherever
// the callable is *resolved*, is_callable included.
func (u *Unit) callableClass(ctx *php.Ctx, name string) (string, bool) {
	return u.callableClassQuiet(ctx, name, false, true)
}

// callableClassQuiet is callableClass for a caller that has already resolved
// this callable once and reported the deprecation then.
func (u *Unit) callableClassQuiet(ctx *php.Ctx, name string,
	quiet, keywords bool) (string, bool) {
	keyword := strings.ToLower(name)
	switch keyword {
	case "self", "parent", "static":
	default:
		return name, true
	}
	if !keywords {
		// A dynamic call resolves no keyword: the name stands, and looking it
		// up as a class is what fails.
		return name, true
	}
	// The deprecation is for a keyword that *worked*. One that resolves to
	// nothing -- `parent` in a class with no parent, `self` outside a class --
	// is not a callable at all, and the engine says nothing about a callable
	// it does not have.
	note := func(name string) (string, bool) {
		if !quiet {
			ctx.Deprecated("Use of %q in callables is deprecated", keyword)
		}
		return name, true
	}
	scope := u.callingScope(ctx)
	if scope == nil {
		return "", false
	}
	if keyword == "parent" {
		if scope.Parent == "" {
			return "", false
		}
		return note(scope.Parent)
	}
	if keyword == "static" {
		// Late static binding: `static::` is the class the *call site* named,
		// which an inherited static method reached through a subclass makes
		// different from the scope.
		if called := u.callingCalledScope(ctx); called != nil {
			return note(called.Name)
		}
	}
	return note(scope.Name)
}

// keywordClass resolves self, parent or static for a builtin, which has no
// frame of its own to read them from. `static` is the called class, which late
// static binding makes different from the scope.
func (u *Unit) keywordClass(ctx *php.Ctx, keyword string) (string, bool) {
	scope := u.callingScope(ctx)
	switch strings.ToLower(keyword) {
	case "parent":
		if scope == nil || scope.Parent == "" {
			return "", false
		}
		return scope.Parent, true
	case "static":
		if called := u.callingCalledScope(ctx); called != nil {
			return called.Name, true
		}
	}
	if scope == nil {
		return "", false
	}
	return scope.Name, true
}

// callableError words the Error PHP throws for a value that cannot be called.
// It is only asked once dynamicTarget has failed, and returns "" when the
// failure is a gap in the interpreter rather than the program's mistake -- a
// class the runtime does not model, say -- so that stays an honest UnsupportedError.
func (u *Unit) callableError(callee php.Value) string {
	switch callee.Kind() {
	case php.KindString:
		name := callee.String()
		if class, method, found := strings.Cut(name, "::"); found {
			held, known := u.findClass(class)
			if !known {
				return "Class \"" + class + "\" not found"
			}
			// The class is named as it was *declared*, not as the callable
			// spelled it: `["stdclass", "b"]` says stdClass.
			return "Call to undefined method " + php.DisplayName(held.Name) +
				"::" + method + "()"
		}
		return "Call to undefined function " + name + "()"
	case php.KindArray:
		items := callee.Array()
		if items.Len() != 2 {
			return "Array callback must have exactly two elements"
		}
		target, hasTarget := items.Get(php.IntKey(0))
		method, hasMethod := items.Get(php.IntKey(1))
		if !hasTarget || !hasMethod {
			// Two elements, but not under the keys a callable needs:
			// `[1 => 0, 2 => 0]` is the right length and the wrong shape.
			return "Array callback has to contain indices 0 and 1"
		}
		className := target.String()
		if target.Kind() == php.KindObject {
			className = target.Object().ClassName
		} else if _, known := u.findClass(className); !known {
			return "Class \"" + className + "\" not found"
		}
		held, known := u.findClass(className)
		if !known {
			// The class exists in the program but not in a form the runtime
			// models, so this is a gap rather than a bad callable.
			return ""
		}
		return "Call to undefined method " + php.DisplayName(held.Name) +
			"::" + method.String() + "()"
	case php.KindObject:
		// An object with __invoke is callable; one without is not, and naming
		// its class is what PHP does.
		return "Object of type " + callee.Object().ClassName + " is not callable"
	}
	return "Value of type " + php.TypeName(callee) + " is not callable"
}

// closureDebugInfo builds what var_dump and print_r show for a Closure. The
// engine gives the class a get_debug_info handler rather than properties, and
// the shape is specific: a closure written as one is described by name, file
// and line, while one made from an existing function -- `f(...)`,
// Closure::fromCallable -- is described by that function's name instead.
func (u *Unit) closureDebugInfo(in *Interp, fn *closure) *php.Array {
	info := php.NewArray()
	switch {
	case fn.fake && fn.builtin != "":
		info.Set(php.StringKey("function"), php.String(fn.builtin))
	case fn.fake:
		name := fn.op.GetName()
		if fn.scopeName != "" {
			name = php.DisplayName(fn.scopeName) + "::" + name
		}
		info.Set(php.StringKey("function"), php.String(name))
	default:
		info.Set(php.StringKey("name"), php.String(fn.op.GetName()))
		info.Set(php.StringKey("file"), php.String(fn.op.GetFilename()))
		info.Set(php.StringKey("line"), php.Int(int64(fn.op.GetLineStart())))
	}

	// The `use` variables are the closure's static-variable table, which is
	// where their declaration order survives -- the captured map does not keep
	// it.
	if !fn.fake && fn.op != nil {
		captured := php.NewArray()
		for _, entry := range fn.op.GetStaticVars() {
			name := entry.GetName()
			switch value, held := fn.bound[name]; {
			case held:
				// A `use` capture: kept as it is rather than dereferenced, so a
				// by-reference one still shows its marker.
				captured.Set(php.StringKey(name), value)
			default:
				// A `static $x` inside the closure. It shares the op_array's
				// slot, and before the closure has ever run there is no slot at
				// all -- which the engine shows as null rather than omitting.
				stored, ok := u.statics[u.staticKey(fn.op, name, fn.class)]
				if !ok {
					stored = php.Null()
				}
				captured.Set(php.StringKey(name), stored)
			}
		}
		if captured.Len() > 0 {
			info.Set(php.StringKey("static"), php.Arr(captured))
		}
	}
	if fn.this != nil {
		info.Set(php.StringKey("this"), php.Obj(fn.this))
	}

	if parameters := u.closureParameters(in, fn); parameters != nil {
		info.Set(php.StringKey("parameter"), php.Arr(parameters))
	}
	return info
}

// closureParameters renders the `parameter` Entry: every declared parameter,
// named with a leading & when it is taken by reference, mapped to whether it is
// required. A function with no parameters has no entry at all.
func (u *Unit) closureParameters(in *Interp, fn *closure) *php.Array {
	type parameter struct {
		name     string
		byRef    bool
		variadic bool
	}
	var params []parameter
	required := 0
	switch {
	case fn.builtin != "":
		signature, known := in.engineSignatures[strings.ToLower(fn.builtin)]
		if !known {
			return nil
		}
		required = int(signature.GetRequiredArgs())
		for _, arg := range signature.GetArgs() {
			params = append(params, parameter{arg.GetName(), arg.GetByRef(), arg.GetVariadic()})
		}
	case fn.op != nil:
		required = int(fn.op.GetRequiredArgs())
		for _, arg := range fn.op.GetArgs() {
			params = append(params, parameter{arg.GetName(), arg.GetByRef(), arg.GetVariadic()})
		}
	}
	if len(params) == 0 {
		return nil
	}
	rendered := php.NewArray()
	for i, param := range params {
		name := "$" + param.name
		if param.byRef {
			name = "&" + name
		}
		state := "<required>"
		if i >= required || param.variadic {
			state = "<optional>"
		}
		rendered.Set(php.StringKey(name), php.String(state))
	}
	return rendered
}

// noteCompoundCallable reports the deprecation `["C", "parent::m"]` earns. The
// first name is the class the callable named, not the one the scope resolves
// to, and the second is the method string as written.
func (u *Unit) noteCompoundCallable(ctx *php.Ctx, named *Class, method string, quiet bool) {
	if quiet {
		return
	}
	ctx.Deprecated("Callables of the form [\"%s\", \"%s\"] are deprecated",
		php.DisplayName(named.Name), method)
}

// forwardCalled keeps the late static binding when a callable named its class
// with a keyword. `self`, `parent` and `static` *forward* the call: the class
// they resolve to decides where the method is looked up, but `static::` inside
// it still means whatever the original call site meant. Naming the class
// outright does not forward -- that is the whole difference the lsb tests
// exercise.
func (u *Unit) forwardCalled(ctx *php.Ctx, call *pendingCall, named string) {
	if call == nil {
		return
	}
	switch strings.ToLower(named) {
	case "self", "parent", "static":
	default:
		return
	}
	// Only when the call site's class is one the resolved class would accept.
	// `["B", "parent::who"]` from an unrelated C reports A, because C is not
	// an A and there is no binding of C's to forward.
	called := u.callingCalledScope(ctx)
	if called == nil || call.called == nil || !u.instanceOf(called, call.called.Name) {
		return
	}
	call.called = called
}

// forwardThis binds the caller's $this to a call that named its target by
// class. A non-static method reached that way is still called on the object the
// caller has, when that object is one the method belongs to -- which is what
// makes `call_user_func(["B", "parent::m"])` from inside B report B rather than
// A as the late static binding.
func (u *Unit) forwardThis(ctx *php.Ctx, call *pendingCall) {
	if call == nil || call.this != nil || call.method == nil || call.method.GetStatic() {
		return
	}
	this := u.callingThis(ctx)
	if this == nil {
		return
	}
	held, known := u.findClass(this.ClassName)
	if !known || call.class == nil || !u.instanceOf(held, call.class.Name) {
		return
	}
	call.this = this
	call.called = held
	// A compound name is the one form that still finds an instance to call an
	// instance method on, so the refusal staticTarget attached no longer
	// applies. Probed: `["C", "parent::m"]` from inside a method of C runs,
	// while `["C", "m"]` from the same place does not.
	call.refusal = ""
}

// scopeKeyword reports whether the class half of a callable names a *scope*
// rather than a class. Those keep the calling instance; a literal class name
// does not.
func scopeKeyword(name string) bool {
	switch strings.ToLower(name) {
	case "self", "parent", "static":
		return true
	}
	return false
}

// scopedCallableClass resolves the class half of a compound method name. Unlike
// the bare `["parent", "m"]` form, `self` and `parent` are relative to the class
// the callable named rather than to the calling scope -- `["B", "parent::m"]`
// works from anywhere, even where there is no class scope at all. `static` is
// the exception: it is late static binding, so it needs a calling scope and is
// resolved by the caller before this is reached.
func (u *Unit) scopedCallableClass(ctx *php.Ctx, named *Class, scoped string) (string, bool) {
	switch strings.ToLower(scoped) {
	case "self":
		return named.Name, true
	case "parent":
		if named.Parent == "" {
			return "", false
		}
		return named.Parent, true
	case "static":
		// Late static binding, which is a property of where the call is being
		// made and not of the class the callable named: at global scope there
		// is nothing for it to mean.
		return u.callableClassQuiet(ctx, scoped, true, true)
	}
	return scoped, true
}

// A callback a builtin cannot use is a TypeError naming the argument and the
// reason, and PHP's reasons are specific enough that a generic "not callable"
// fails the tests that exercise them. These are the strings zend_is_callable_ex
// writes into its `error` out-parameter.
//
// An empty result means the callable is usable, or that the failure is a gap in
// this runtime rather than the program's mistake -- which must stay an
// UnsupportedError rather than becoming a program error.
func (in *Interp) callableReason(ctx *php.Ctx, unit *Unit, callee php.Value) string {
	return in.callableReasonQuiet(ctx, unit, callee, false)
}

// callableReasonQuiet is callableReason for a caller that only wants the
// verdict. A `callable` type check is one: the engine reports the deprecated
// spellings from the check itself, so the caller that has already reported them
// -- or is only asking whether a union alternative matches -- asks quietly and
// leaves the reporting to whoever resolves the callable for real.
func (in *Interp) callableReasonQuiet(ctx *php.Ctx, unit *Unit, callee php.Value, quiet bool) string {
	callee = callee.Deref()
	if _, ok := asClosure(callee); ok {
		return ""
	}
	switch callee.Kind() {
	case php.KindObject:
		class, known := unit.findClass(callee.Object().ClassName)
		if !known {
			return ""
		}
		if _, _, found := unit.resolveMethod(class, "__invoke"); !found {
			// **The same text as an int or a float.** An object with no
			// `__invoke` is refused for being the wrong *shape*, not for what
			// it names: the engine's callback-argument check says `no array or
			// string given` for `new stdClass` exactly as it does for `1.5`.
			// `Object of type X is not callable` is a different diagnostic --
			// what *invoking* one reports -- and reporting it here put the
			// invocation's wording on the argument check.
			return "no array or string given"
		}
		return ""
	case php.KindArray:
		items := callee.Array()
		if items.Len() != 2 {
			return "array callback must have exactly two members"
		}
		target, hasTarget := items.Get(php.IntKey(0))
		method, hasMethod := items.Get(php.IntKey(1))
		if !hasTarget || !hasMethod {
			return "array callback has to contain indices 0 and 1"
		}
		// The *first* member is checked first: `[1, 2]` is about the class,
		// not about the method.
		switch target.Deref().Kind() {
		case php.KindObject, php.KindString:
		default:
			return "first array member is not a valid class name or object"
		}
		if method.Deref().Kind() != php.KindString {
			return "second array member is not a valid method"
		}
		return in.methodCallableReason(ctx, unit, target.Deref(), method.Deref().String(), quiet)
	case php.KindString:
		name := callee.String()
		class, method, compound := strings.Cut(name, "::")
		if !compound {
			if in.namedFunctionExists(unit, name) {
				return ""
			}
			return "function \"" + name + "\" not found or invalid function name"
		}
		return in.methodCallableReason(ctx, unit, php.String(class), method, quiet)
	}
	// Anything that is not a string, an array or an invokable object is not a
	// callable at all, and the engine says what it wanted rather than what it
	// got.
	return "no array or string given"
}

// namedFunctionExists reports whether a plain function name resolves. A string
// is a callable only when it names something callable, so the lookup that a
// call would do has to happen for the check as well -- against the script's own
// functions, this runtime's builtins, and the build's, in that order.
func (in *Interp) namedFunctionExists(unit *Unit, name string) bool {
	key := functionKey(name)
	if _, ok := unit.functions[key]; ok {
		return true
	}
	if _, ok := unit.libraryFunctions[key]; ok {
		return true
	}
	if _, ok := unit.userlandBuiltins[key]; ok {
		return true
	}
	if _, ok := builtins[key]; ok {
		return true
	}
	return in.engineFunctions[key]
}

// methodCallableReason answers for the [target, "method"] form, where target is
// an object or a class name. The class the diagnostic names is the one the
// callable resolved to, not the one that declared the method.
func (in *Interp) methodCallableReason(ctx *php.Ctx, unit *Unit, target php.Value, method string, quiet bool) string {
	var class *Class
	viaObject := target.Kind() == php.KindObject
	switch target.Kind() {
	case php.KindObject:
		found, known := unit.findClass(target.Object().ClassName)
		if !known {
			return ""
		}
		class = found
	case php.KindString:
		name := target.String()
		switch strings.ToLower(name) {
		case "self", "parent", "static":
			if unit.callingScope(ctx) == nil {
				return "cannot access \"" + strings.ToLower(name) + "\" when no class scope is active"
			}
		}
		resolved, ok := unit.callableClassQuiet(ctx, name, quiet, true)
		if !ok {
			return "class \"" + name + "\" not found"
		}
		if resolved == "" {
			// An empty name is not a class the engine goes looking for.
			return "class \"\" not found"
		}
		found, known := in.resolveTypeClass(ctx, unit, resolved)
		if !known {
			return "class \"" + resolved + "\" not found"
		}
		class = found
	default:
		return "first array member is not a valid class name or object"
	}
	// A compound name moves the lookup to the class it names, and the rest of
	// the answer is about that class rather than the one written down.
	if scoped, rest, compound := strings.Cut(method, "::"); compound {
		within, ok := unit.scopedCallableClass(ctx, class, scoped)
		if !ok {
			if strings.EqualFold(scoped, "static") {
				return "cannot access \"static\" when no class scope is active"
			}
			return "class \"" + scoped + "\" not found"
		}
		found, known := unit.findClass(within)
		if !known {
			return "class \"" + within + "\" not found"
		}
		if !unit.instanceOf(class, within) {
			// Naming a scope the class is not in is refused before the form is
			// even called deprecated: there is no callable here to deprecate.
			return "class " + php.DisplayName(class.Name) +
				" is not a subclass of " + php.DisplayName(found.Name)
		}
		// The engine reports the deprecated form from inside the same check
		// that answers whether the callable is usable, and before it looks the
		// method up -- so the notice appears even when the answer is no.
		unit.noteCompoundCallable(ctx, class, method, quiet)
		class, method = found, rest
	}
	body, declaring, found := unit.resolveMethod(class, method)
	// A magic forwarder makes the callable valid whatever the lookup says, and
	// which forwarder applies is decided by how the callable named its target:
	// __call for an object, __callStatic for a class name.
	if unit.forwarderFor(ctx, class, viaObject) != "" {
		return ""
	}
	if !found {
		if unit.hasMethod(class, method, viaObject) {
			// The engine has it and this runtime does not model it: a gap.
			return ""
		}
		return "class " + php.DisplayName(class.Name) + " does not have a method \"" + method + "\""
	}
	if !unit.methodAccessibleAs(body, declaring, unit.callingScope(ctx), method) {
		return "cannot access " + body.GetVisibility() + " method " +
			php.DisplayName(class.Name) + "::" + method + "()"
	}
	// A callable that named a class rather than an object needs an instance to
	// call an instance method on. The one in scope will do, if it is one of
	// this class -- that is how ["C", "parent::m"] works from inside a method
	// -- and PHP stopped guessing anything else in 8.0.
	if !viaObject && !body.GetStatic() {
		usable := false
		if this := unit.callingThis(ctx); this != nil {
			if held, known := unit.findClass(this.ClassName); known {
				usable = unit.instanceOf(held, class.Name)
			}
		}
		if !usable {
			return "non-static method " + php.DisplayName(class.Name) + "::" +
				method + "() cannot be called statically"
		}
	}
	return ""
}

// forwarderFor names the magic method that answers for one this callable could
// not resolve, or "" when there is none. An object names __call. A class name
// names __call too when the innermost call is bound to an instance of that
// class -- `self::m()` from inside a method is an instance call written the
// other way round -- and __callStatic otherwise.
func (u *Unit) forwarderFor(ctx *php.Ctx, class *Class, viaObject bool) string {
	if !viaObject {
		if this := u.callingThis(ctx); this != nil {
			if current, known := u.findClass(this.ClassName); known &&
				u.instanceOfName(current, class.Name) {
				viaObject = true
			}
		}
	}
	forwarder := magicCallStat
	if viaObject {
		forwarder = magicCall
	}
	if _, _, ok := u.resolveMethod(class, forwarder); ok {
		return forwarder
	}
	return ""
}

// refuseInternalScope reports the warning binding a closure into an engine
// class earns, and whether it was refused. Such a class has no PHP body, so
// there is no scope for a closure to enter: the bind answers null rather than
// pretending the closure can see private members that do not exist.
//
// Only an explicit scope is refused. `bindTo($obj)` with no second argument
// keeps the closure's own scope and is fine whatever $obj's class is.
func (u *Unit) refuseInternalScope(ctx *php.Ctx, class *Class) bool {
	if class == nil || !u.isBuiltinOnly(class) {
		return false
	}
	ctx.Warn("Cannot bind closure to scope of internal class %s", php.DisplayName(class.Name))
	return true
}
