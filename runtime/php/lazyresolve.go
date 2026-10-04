package php

// Resolving a lazy object, for a library function that has to see the real
// state rather than the placeholder.
//
// Reading a lazy object's property table is not a trigger -- `LazyState`
// follows an *already* initialized proxy and initializes nothing. A handful of
// library functions do trigger: `json_encode`, `serialize`, `var_export` and
// `get_object_vars` all run the initializer before they look. Those need this.
//
// Declared here and supplied from above, because initializing means calling the
// program's own initializer, which is the class table's business.

// LazyResolver runs a lazy object's initializer and yields the value holding
// the state. A value that is not a lazy object comes back unchanged.
type LazyResolver func(ctx *Ctx, v Value) (Value, error)

// SetLazyResolver installs the hook. Per Ctx, like every other one.
func (c *Ctx) SetLazyResolver(fn LazyResolver) { c.lazyResolver = fn }

// Materialize applies the hook, and is the **identity** when nothing installed
// one. That is not a gap papered over: a program with no way to make a lazy
// object has none to initialize, which is the compiled back-end's position
// until `ReflectionClass` reaches it.
func (c *Ctx) Materialize(v Value) (Value, error) {
	if c.lazyResolver == nil {
		return v, nil
	}
	return c.lazyResolver(c, v)
}

// The property table as the object **presents** it to a scope: a hook is run
// rather than a slot read, a virtual property is included though it has no
// slot, and an uninitialised typed one is absent.
//
// This is the question `get_object_vars` asks, and `json_encode` asks it with
// the empty scope because the engine's encoder sees what global scope sees.
// Declared here and supplied from above, because the answer needs the class
// table's hooks and visibility.
type VisibleProperties func(ctx *Ctx, o *Object, scope string) (*Array, error)

// SetVisibleProperties installs the hook.
func (c *Ctx) SetVisibleProperties(fn VisibleProperties) { c.visibleProps = fn }

// VisibleProperties applies the hook. Nothing here can answer without a class
// table, and an empty table would be a wrong answer rather than a missing one,
// so an uninstalled hook is a gap.
func (c *Ctx) VisibleProperties(o *Object, scope string) (*Array, error) {
	if c.visibleProps == nil {
		return nil, Unsupported("the properties an object presents")
	}
	return c.visibleProps(c, o, scope)
}

// Two questions about a class that only a class table can answer, and that
// `serialize` has to ask: whether a method exists -- asked **without** running
// `__call`, which is why it is not a call attempt -- and whether the class is
// one the engine refuses to write at all.

// HasMethodFn reports whether a class declares or inherits a method.
type HasMethodFn func(class, method string) bool

// SetHasMethod installs the hook.
func (c *Ctx) SetHasMethod(fn HasMethodFn) { c.hasMethod = fn }

// HasMethod applies it. False with no table, which is the same answer a class
// this program never declared would get.
func (c *Ctx) HasMethod(class, method string) bool {
	return c.hasMethod != nil && c.hasMethod(class, method)
}

// ClassFacts is what a library function needs to know about a class it did not
// declare. One hook rather than a setter per question, because they are all the
// same lookup and a caller that wants one usually wants two.
type ClassFacts struct {
	// Known is false for a name the program never declared, which is a
	// different answer from every zero value below.
	Known bool
	// NotSerializable marks a class whose state has no written form: the engine
	// refuses rather than writing an empty object.
	NotSerializable bool
	// NoDynamicProperties and AllowDynamicProperties are the two explicit
	// answers about a name the class does not declare -- refuse outright, or
	// accept without the deprecation. Neither set is the ordinary case: accept
	// *with* it.
	NoDynamicProperties    bool
	AllowDynamicProperties bool
	// Uninstantiable is why `new` would refuse -- an enum or an abstract class
	// has no instance to give -- and nil when it would not.
	Uninstantiable error
}

// ClassFactsFn answers them.
type ClassFactsFn func(class string) ClassFacts

// SetClassFacts installs the hook.
func (c *Ctx) SetClassFacts(fn ClassFactsFn) { c.classFacts = fn }

// ClassFactsOf applies it. An uninstalled hook answers `Known: false`, which is
// what a program with no class table would truthfully say.
func (c *Ctx) ClassFactsOf(class string) ClassFacts {
	if c.classFacts == nil {
		return ClassFacts{}
	}
	return c.classFacts(class)
}

// PropertyFactsFn reports whether a class declares a property, and whether that
// declaration is **virtual** -- a hooked property with no storage, which there
// is nowhere to put an unserialized value in.
type PropertyFactsFn func(class, property string) (declared, virtual bool)

// SetPropertyFacts installs the hook.
func (c *Ctx) SetPropertyFacts(fn PropertyFactsFn) { c.propertyFacts = fn }

// PropertyFactsOf applies it.
func (c *Ctx) PropertyFactsOf(class, property string) (declared, virtual bool) {
	if c.propertyFacts == nil {
		return false, false
	}
	return c.propertyFacts(class, property)
}

// NewInstanceFn allocates an object **without running its constructor**, laid
// out as `new` would: every declared property in its slot, a typed one
// uninitialised. `unserialize` needs exactly that, and `ObjectMaker` is not it
// -- that one only numbers a bare object.
type NewInstanceFn func(ctx *Ctx, class string) (*Object, error)

// SetNewInstance installs the hook.
func (c *Ctx) SetNewInstance(fn NewInstanceFn) { c.newInstance = fn }

// SetInstanceConfined installs the compiler's answer to whether code running
// on an object of a class -- its methods, hooks, magic methods, inherited
// ones -- may keep `$this`. A proxy's instance stays the proxy's alone only
// when none can (Lazy.InstanceOwned).
func (c *Ctx) SetInstanceConfined(fn func(class string) bool) { c.instanceConfined = fn }

// NewInstance applies it, reporting false when nothing here can lay one out.
func (c *Ctx) NewInstance(class string) (*Object, bool, error) {
	if c.newInstance == nil {
		return nil, false, nil
	}
	o, err := c.newInstance(c, class)
	if err != nil {
		return nil, true, err
	}
	return o, o != nil, nil
}

// EnumLookup is how an enum-case lookup failed, which `unserialize` reports
// differently for each: a class that is not an enum at all, a name that is a
// constant but not a case, and a name that is neither.
type EnumLookup int

const (
	EnumCaseFound EnumLookup = iota
	EnumNotAnEnum
	EnumNotACase
	EnumNoSuchConstant
)

// EnumCaseFn answers an enum's case by name, which `unserialize` needs because
// a case serializes as its name and the case object is the identity.
type EnumCaseFn func(ctx *Ctx, class, name string) (Value, EnumLookup)

// SetEnumCase installs the hook.
func (c *Ctx) SetEnumCase(fn EnumCaseFn) { c.enumCase = fn }

// EnumCaseOf applies it.
func (c *Ctx) EnumCaseOf(class, name string) (Value, EnumLookup) {
	if c.enumCase == nil {
		return Undef(), EnumNotAnEnum
	}
	return c.enumCase(c, class, name)
}
