package interp

import (
	"strings"

	pb "github.com/moriyoshi/php2go/internal/ir/irpb"
	"github.com/moriyoshi/php2go/runtime/php"
)

// PHP 8.4's lazy objects. Reflection can make an object that has a class and a
// handle but no state: the state appears the first time anything looks at it,
// produced by an initializer the caller supplied.
//
// There are two kinds and they differ in where the state ends up. A **ghost**
// is initialized in place -- the initializer is handed the object itself,
// usually calls its constructor, and afterwards the object is an ordinary one
// with nothing lazy left about it. A **proxy** stays a wrapper: the initializer
// *returns* a real instance, and every property access forwards to it from then
// on. That difference is visible: var_dump of an initialized ghost is the plain
// object, while an initialized proxy still prints as a proxy holding an
// `instance`.
//
// Every path that can observe an object's state has to ask for it first, which
// is what `resolveLazy` is for. It answers with the object property access
// should actually use -- the instance for an initialized proxy, the object
// itself otherwise -- so a caller that has asked can go on to work with plain
// property operations.

// resolveLazy initializes an object if it has not been initialized yet, and
// answers the object whose properties stand for it.
// resolveLazy is the shared rule, reached through the three hooks installed in
// call.go. What used to be here now lives in `runtime/php`, because a compiled
// program needs the same algorithm and it is too subtle to keep two copies of.
func (in *Interp) resolveLazy(ctx *php.Ctx, unit *Unit, object *php.Object,
	property, magic string, depth int) (*php.Object, error) {
	return php.ResolveLazy(ctx, object, property, magic)
}

// The flags ReflectionClass takes beside an initializer. They are engine
// constants rather than anything derived, so they are written down here.
const (
	skipInitializationOnSerialize = 8
	skipDestructor                = 16
)

// applyLazyFlags records what the caller asked for beyond deferring the state.
func applyLazyFlags(object *php.Object, flags php.Value) {
	if object == nil || object.Lazy == nil {
		return
	}
	mask := flags.Deref().Int()
	object.Lazy.SkipSerialize = mask&skipInitializationOnSerialize != 0
	object.Lazy.SkipDestructor = mask&skipDestructor != 0
}

// compatibleProxy reports whether a factory's result may stand in behind the
// proxy. The direction is the opposite of the obvious one: the *proxy* has to
// be the instance's class or a subclass of it, not the other way round -- the
// proxy is what the program holds and type-checks against, so it may promise
// more than the real object is, never less.
//
// Even a subclass has to add nothing that would need a slot of its own or would
// run instead of the instance's: no property (static ones count), and no
// __destruct or __clone declared below the instance's class. Constants,
// interfaces and ordinary methods are fine, which is what makes the pattern
// useful -- a proxy subclass exists to be nameable, not to hold state.
func (u *Unit) compatibleProxy(ctx *php.Ctx, proxyClass, instanceClass string) bool {
	if strings.EqualFold(proxyClass, instanceClass) {
		return true
	}
	class, ok := u.findClass(proxyClass)
	if !ok || !u.instanceOf(class, instanceClass) {
		return false
	}
	real, known := u.findClass(instanceClass)
	if !known {
		return false
	}
	// The count is the shared rule, so both back-ends answer it the same way:
	// one entry per declared name, a private one per declaring class, and an
	// override sharing the entry it overrides.
	if php.PropertyInfoCount(ctx, class.Name) != php.PropertyInfoCount(ctx, real.Name) {
		return false
	}
	for _, magic := range []string{"__destruct", "__clone"} {
		// Inheriting one from the instance's own class or above is not an
		// override -- it is the same body either way, and running it on the
		// proxy would be running the instance's.
		if _, declaring, found := u.resolveMethod(class, magic); found &&
			!u.instanceOf(real, declaring.Name) {
			return false
		}
	}
	return true
}

// lazyState is the object whose property table holds the state, for a caller
// that wants to read it without initializing anything. A proxy keeps its state
// in the instance the initializer returned, so once there is one that is where
// to look; an uninitialized proxy is answered with its own empty table rather
// than being made to produce one.
func lazyState(object *php.Object) *php.Object {
	if object != nil && object.Lazy != nil && object.Lazy.Instance != nil {
		return object.Lazy.Instance
	}
	return object
}

// lazyTarget follows a chain of *initialized proxies* to the object that
// actually holds the state. `ReflectionProperty`'s three lazy methods each open
// with the same loop:
//
//	while (zend_object_is_lazy_proxy(object) && zend_lazy_object_initialized(object)) {
//	    object = zend_lazy_object_get_instance(object);
//	}
//
// so they ask about the real instance rather than about the stand-in -- and the
// loop is a loop because a proxy's instance may itself be a proxy. An
// uninitialized proxy, and a ghost at any stage, is its own target.
func lazyTarget(object *php.Object) *php.Object {
	for object != nil && object.Lazy != nil && object.Lazy.Proxy && object.Lazy.Instance != nil {
		object = object.Lazy.Instance
	}
	return object
}

// lazyUninitialized reports whether an object still has no state, which is what
// var_dump prints differently and what makes a second initialization a no-op.
func lazyUninitialized(object *php.Object) bool {
	return object != nil && object.Lazy != nil && object.Lazy.Instance == nil &&
		!object.Lazy.Running
}

// newLazy builds an uninitialized object of a class. Its declared properties
// have no values at all -- not even their defaults, which is why var_dump shows
// a typed one as uninitialized and leaves an untyped one out entirely.
func (u *Unit) newLazy(class *Class, initializer php.Value, proxy bool) *php.Object {
	object := u.instantiate(class)
	stripState(object)
	object.Lazy = &php.Lazy{Proxy: proxy, Initializer: initializer}
	if u.allSkipped(object) {
		// A class that declares no properties has nothing to defer, so the
		// engine hands back an ordinary object: the initializer is never
		// called and var_dump prints no label.
		object.Lazy = nil
		dropDynamic(object)
	}
	return object
}

// skipProperty gives one property a value without initializing the object. The
// value is the declared default unless the caller supplied one, which is the
// difference between skipLazyInitialization and
// setRawValueWithoutLazyInitialization. Once every declared property has been
// given a value there is nothing left to defer, and the object stops being lazy.
func (u *Unit) skipProperty(object *php.Object, prop *pb.Property, value php.Value, given bool) {
	if object.Lazy == nil {
		return
	}
	if !given && object.Lazy.Skipped[prop.GetName()] {
		// skipLazyInitialization on a property that is already not lazy does
		// nothing at all: it does not write the default a second time over
		// whatever the program has since put there. A *raw* write does
		// overwrite, since it was given a value to write.
		return
	}
	if given {
		object.Props.Set(php.StringKey(prop.GetName()), value)
	} else {
		// An untyped property with no initializer starts as null, not as
		// uninitialized: only a typed one has that third state.
		u.skipDefault(object, prop)
	}
	if object.Lazy.Skipped == nil {
		object.Lazy.Skipped = map[string]bool{}
	}
	object.Lazy.Skipped[prop.GetName()] = true
	if u.allSkipped(object) {
		object.Lazy = nil
	}
}

// allSkipped reports whether every property the object defers has been given a
// value, which is what ends its laziness without an initializer ever running.
func (u *Unit) allSkipped(object *php.Object) bool {
	class, ok := u.findClass(object.ClassName)
	if !ok {
		return false
	}
	for _, current := range u.ancestry(class) {
		for _, prop := range u.properties(current) {
			if prop.GetStatic() || prop.GetVirtual() {
				continue
			}
			if !object.Lazy.Skipped[prop.GetName()] {
				return false
			}
		}
	}
	return true
}

// stripState takes an object back to having no properties at all. Every
// declared property keeps its slot, so that the order the class declares them
// in survives; what changes is that none of them holds a value.
// stripStateForReset is stripState for resetAsLazy*, which keeps one thing
// back. A readonly property that already holds a value survives the reset when
// the object's class is final: with no subclass able to reach it there is
// nothing left that could legitimately write it again, so the engine leaves
// the value in place and the reconstructing initializer is refused. A
// non-final class clears it, and `__construct` may set it once more.
// stripStateForReset is the shared rule, reached with the reflected class's
// name. The walk it used to do by hand is what the declared-property hook now
// supplies.
func (u *Unit) stripStateForReset(ctx *php.Ctx, object *php.Object, within *Class) []string {
	reflected := ""
	if within != nil {
		reflected = within.Name
	}
	return php.StripStateForReset(ctx, object, reflected)
}

func stripState(object *php.Object) { php.StripLazyState(object) }

func dropDynamic(object *php.Object) { php.DropDynamicProperties(object) }

// materializeLazy is resolveLazy for a builtin, which has nowhere to jump to
// and so reports the failure as an ordinary error. It answers the object whose
// properties stand for the argument.
func (in *Interp) materializeLazy(ctx *php.Ctx, unit *Unit, value php.Value,
	depth int) (php.Value, error) {
	object := value.Deref().Object()
	if object == nil || object.Lazy == nil {
		return value, nil
	}
	resolved, err := in.resolveLazy(ctx, unit, object, "", "", depth)
	if err != nil {
		return value, err
	}
	return php.Obj(resolved), nil
}

// declaresProperty reports whether a class declares a property by that name.
// A name it does not declare cannot be what a lazy object is deferring.
func (u *Unit) declaresProperty(className, name string) bool {
	class, ok := u.findClass(className)
	if !ok {
		return false
	}
	for _, current := range u.ancestry(class) {
		for _, prop := range u.properties(current) {
			if prop.GetName() == name && !prop.GetStatic() {
				return true
			}
		}
	}
	return false
}

// hasMagicNamed reports whether a class supplies one of the property accessors.
func (u *Unit) hasMagicNamed(className, magic string) bool {
	class, ok := u.findClass(className)
	if !ok {
		return false
	}
	_, _, found := u.resolveMethod(class, magic)
	return found
}

// restoreDefaults puts every property the object still defers back to what the
// class declares for it, which is where a ghost's initializer starts.
// restoreDefaults is the shared rule, reached through the declared-property
// hook. The walk it used to do by hand is what the hook now supplies.
func (u *Unit) restoreDefaults(ctx *php.Ctx, object *php.Object) {
	php.RestoreDeclaredDefaults(ctx, object)
}

// skipDefault writes one property's declared default, leaving a typed property
// with no initializer uninitialized.
func (u *Unit) skipDefault(object *php.Object, prop *pb.Property) {
	value := php.Undef()
	switch {
	case prop.GetHasDefault():
		if resolved, ok := u.defaults[prop]; ok {
			value = resolved.Copy()
		} else {
			value = literalValue(prop.GetDefaultValue())
		}
	case prop.GetType() == "":
		value = php.Null()
	}
	object.Props.Set(php.StringKey(prop.GetName()), value)
}

// lazyVisible is what a dump of an uninitialized lazy object shows: its typed
// properties, named but with no value. An untyped property has no type to name
// and PHP does not list it at all -- there is nothing to say about it.
func lazyVisible(object *php.Object) *php.Array {
	return php.LazyVisibleProperties(object)
}

// checkLazyOptions rejects an options mask with a bit the method does not
// accept. SKIP_DESTRUCTOR only means something when there is state to destroy,
// so only the reset forms take it; anything outside the two known bits is
// refused outright, which is what makes -1 an error rather than "everything".
func checkLazyOptions(spelled string, position int, options php.Value, resetting bool) error {
	mask := options.Deref().Int()
	allowed := int64(skipInitializationOnSerialize)
	if resetting {
		allowed |= skipDestructor
	}
	if mask&^(skipInitializationOnSerialize|skipDestructor) != 0 {
		return Raise("ReflectionException",
			"ReflectionClass::%s(): Argument #%d ($options) contains invalid flags",
			spelled, position)
	}
	if mask&^allowed != 0 {
		return Raise("ReflectionException",
			"ReflectionClass::%s(): Argument #%d ($options) does not accept "+
				"ReflectionClass::SKIP_DESTRUCTOR", spelled, position)
	}
	return nil
}

// refuseLazyInternal is the Error a class carrying engine state raises when it
// is asked to be made lazy. What decides it is the engine's own object-creation
// handler rather than the class being internal: stdClass has none and may be
// made lazy, while Exception and ArrayObject have one and may not. The handler
// is inherited, so a user class extending such a class is refused too -- and
// the message says which ancestor is to blame.
func (u *Unit) refuseLazyInternal(class *Class) error {
	if class == nil || !class.decl.GetCustomCreate() {
		return nil
	}
	if u.isBuiltinOnly(class) {
		return Raise("Error",
			"Cannot make instance of internal class lazy: %s is internal",
			php.DisplayName(class.Name))
	}
	for current := class; current != nil; {
		if u.isBuiltinOnly(current) && current.decl.GetCustomCreate() {
			return Raise("Error",
				"Cannot make instance of internal class lazy: %s inherits internal class %s",
				php.DisplayName(class.Name), php.DisplayName(current.Name))
		}
		if current.Parent == "" {
			break
		}
		parent, ok := u.findClass(current.Parent)
		if !ok {
			break
		}
		current = parent
	}
	return Raise("Error",
		"Cannot make instance of internal class lazy: %s is internal",
		php.DisplayName(class.Name))
}

// lazyReflection implements the ReflectionClass methods that make and inspect
// lazy objects.
func (in *Interp) lazyReflection(ctx *php.Ctx, unit *Unit, class *Class,
	method string, args []php.Value, depth int) (php.Value, bool, error) {
	argument := func(i int) php.Value {
		if i < len(args) {
			return args[i].Deref()
		}
		return php.Null()
	}
	switch method {
	case "newlazyghost", "newlazyproxy":
		if err := instantiable(class); err != nil {
			return php.Undef(), true, err
		}
		if err := unit.refuseLazyInternal(class); err != nil {
			return php.Undef(), true, err
		}
		if reason := in.callableReason(ctx, unit, argument(0)); reason != "" {
			return php.Undef(), true, Raise("TypeError",
				"ReflectionClass::%s(): Argument #1 ($initializer) must be a valid callback, %s",
				map[bool]string{true: "newLazyProxy", false: "newLazyGhost"}[method == "newlazyproxy"],
				reason)
		}
		spelled := "newLazyGhost"
		if method == "newlazyproxy" {
			spelled = "newLazyProxy"
		}
		if err := checkLazyOptions(spelled, 2, argument(1), false); err != nil {
			return php.Undef(), true, err
		}
		object := unit.newLazy(class, argument(0), method == "newlazyproxy")
		applyLazyFlags(object, argument(1))
		return php.Obj(object), true, nil

	case "resetaslazyghost", "resetaslazyproxy":
		spelled := "resetAsLazyGhost"
		if method == "resetaslazyproxy" {
			spelled = "resetAsLazyProxy"
		}
		object := argument(0).Object()
		if object == nil {
			return php.Undef(), true, Raise("TypeError",
				"ReflectionClass::%s(): Argument #1 ($object) must be of type object, %s given",
				spelled, givenTypeName(argument(0)))
		}
		// The reflected class is what the object has to be -- a subclass will
		// do, since it is one -- and the diagnostic names the declared type
		// rather than "object".
		if held, known := unit.findClass(object.ClassName); !known ||
			!unit.instanceOf(held, class.Name) {
			return php.Undef(), true, Raise("TypeError",
				"ReflectionClass::%s(): Argument #1 ($object) must be of type %s, %s given",
				spelled, php.DisplayName(class.Name), php.DisplayName(object.ClassName))
		}
		if err := unit.refuseLazyInternal(class); err != nil {
			return php.Undef(), true, err
		}
		if err := checkLazyOptions(spelled, 3, argument(2), true); err != nil {
			return php.Undef(), true, err
		}
		if lazyUninitialized(object) {
			// An object waiting for its initializer has no state to throw
			// away. An *initialized* one -- proxy or ghost -- may be reset.
			return php.Undef(), true, Raise("ReflectionException", "Object is already lazy")
		}
		// The state is thrown away, and throwing away an object's state is what
		// its destructor is for. SKIP_DESTRUCTOR says not to.
		if object.Lazy == nil && argument(2).Int()&skipDestructor == 0 {
			if err := in.destructAt(ctx, unit, object); err != nil {
				return php.Undef(), true, err
			}
			// The object lives on, so it is due a destructor again when it
			// really does go away.
			delete(unit.destructed, object)
		}
		if object.Lazy != nil && object.Lazy.Instance != nil {
			// Resetting an initialized proxy lets go of the instance, and the
			// engine does that while the object is *between* lazy states. A
			// destructor that looks at the stand-in therefore sees an ordinary
			// object rather than a `lazy proxy` one.
			instance := object.Lazy.Instance
			object.Lazy = nil
			if err := in.releaseCheck(ctx, unit, php.Obj(instance)); err != nil {
				return php.Undef(), true, err
			}
		}
		// Resetting throws the state away and puts the object back to where a
		// freshly made lazy one starts.
		kept := unit.stripStateForReset(ctx, object, class)
		object.Lazy = &php.Lazy{Proxy: method == "resetaslazyproxy", Initializer: argument(1)}
		// A property the reset kept is not deferred: it already holds its
		// value, so the ghost's default-restoring pass must leave it alone.
		// That is the same "not lazy" mark skipLazyInitialization makes -- and
		// when it covers everything, the object is not lazy at all and its
		// initializer never runs.
		for _, name := range kept {
			if object.Lazy.Skipped == nil {
				object.Lazy.Skipped = map[string]bool{}
			}
			object.Lazy.Skipped[name] = true
		}
		if unit.allSkipped(object) {
			object.Lazy = nil
			dropDynamic(object)
			return php.Null(), true, nil
		}
		applyLazyFlags(object, argument(2))
		return php.Null(), true, nil

	case "initializelazyobject":
		object := argument(0).Object()
		if object == nil {
			return php.Undef(), true, Raise("TypeError",
				"ReflectionClass::initializeLazyObject(): Argument #1 ($object) "+
					"must be of type object, %s given", givenTypeName(argument(0)))
		}
		resolved, err := in.resolveLazy(ctx, unit, object, "", "", depth)
		if err != nil {
			return php.Undef(), true, err
		}
		return php.Obj(resolved), true, nil

	case "isuninitializedlazyobject":
		return php.Bool(lazyUninitialized(argument(0).Object())), true, nil

	case "marklazyobjectasinitialized":
		// The object stops being lazy without the initializer ever running, so
		// its properties are the declared defaults -- which a ghost had put
		// aside rather than never had. A proxy is marked the same way: there is
		// no real instance behind it, and it becomes an ordinary object of the
		// class it was standing in for. The object itself comes back.
		object := argument(0).Object()
		if object != nil && object.Lazy != nil {
			for _, prop := range unit.properties(class) {
				if prop.GetStatic() || prop.GetVirtual() {
					continue
				}
				if held, ok := object.Get(prop.GetName()); ok && !held.Deref().IsUndef() {
					continue
				}
				unit.skipDefault(object, prop)
			}
			object.Lazy = nil
		}
		return argument(0), true, nil
	}
	return php.Undef(), false, nil
}
