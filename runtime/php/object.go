package php

import (
	"strings"
	"unsafe"
)

// Object is a PHP object instance.
//
// Objects are handles, not values. $b = $a gives both names the same instance,
// so mutating through one is visible through the other -- the exact opposite of
// arrays, which copy. Value.Copy therefore leaves objects alone, and getting
// that backwards would be invisible until someone mutated a shared object.
// ClassNameOf is the class a value names when it stands where a class name
// goes -- `$n::K`, `$obj::$s`, `$obj::m()`. An **object names its own class**:
// PHP accepts an instance in place of the name, and reading the value as a
// string instead gave the empty name, so `$o::$foo` reported
// `Access to undeclared static property ::$foo`.
//
// Anything else is read as the name, which is the string case that already
// worked.
// A **leading backslash is stripped**: `'\A'` is the fully-qualified spelling
// of the same class as `'A'`, and `$n = '\A'; $n::$b` reaches it. Without this
// the name went into the class tables verbatim and earned
// `Access to undeclared static property \A::$b`. One backslash only, which is
// what the engine removes.
// ClassNameFor is ClassNameOf with the engine's refusal: a class expression
// that is neither an object nor a string is
// `Class name must be a valid object or a string`, not a name made by
// stringifying it. `$n = 1; $n::$b` reported
// `Access to undeclared static property 1::$b`, which is a different error
// about a different thing.
func ClassNameFor(v Value) (string, error) {
	switch v.Deref().Kind() {
	case KindObject, KindString:
		return ClassNameOf(v), nil
	}
	return "", Raise("Error", "%s", "Class name must be a valid object or a string")
}

func ClassNameOf(v Value) string {
	v = v.Deref()
	if v.Kind() == KindObject {
		return v.Object().ClassName
	}
	return strings.TrimPrefix(v.String(), "\\")
}

type Object struct {
	// ClassName is the declared name. Method and hierarchy lookup live with
	// whoever owns the class table, keeping this package free of IR types.
	ClassName string
	// Props is an ordered map, so var_dump and foreach see declaration order.
	// For an object in slot mode it holds only the dynamic properties; the
	// declared ones are in Slots (objectslots.go). Outside this package and
	// the interpreter, reach properties through the Prop* accessors.
	Props *Array
	// Slots are a compiled instance's declared properties, inside the
	// per-class struct it was allocated as, in Layout order. Nil in table
	// mode.
	Slots  []Value
	Layout *Layout
	// ID is the handle PHP prints as #1, #2 and so on.
	ID int
	// SoleOwned marks an object the **caller proved has exactly one owner** at
	// the moment it was handed over -- created for one call and stored nowhere
	// else. It licenses the callee to give the handle back when it drops the
	// object, which is otherwise a refcount question nothing here can answer.
	//
	// Set only by a caller that did the proof; absent means "assume something
	// else may hold it", which is the safe reading.
	SoleOwned bool
	// EnumCase names the case when this object is one. An enum case is an
	// ordinary object with name and value properties, but it does not print
	// like one: var_dump writes enum(Suit::Hearts) rather than the object form.
	EnumCase string
	// EnumBacking is the case's declared backing type -- "int" or "string" --
	// and "" for a pure enum or a non-enum. `var_dump` never needs it, which is
	// why it was not here before; `print_r` writes `Suit Enum:string` and does.
	// Carried on the object rather than asked of a class table, because the
	// rendering is in this package and the class table is not.
	EnumBacking string
	// Declared records the declaration of each typed property. A typed property
	// with no initializer starts uninitialized rather than null, and both
	// var_dump and the diagnostic for reading one name the declaration.
	Declared map[string]Declaration
	// PlainAccess records that the object's class has no property hooks, so
	// a property whose declaration is Plain needs no lookup beyond its own
	// declaration and slot. Set by compiled constructors; false means the full
	// path.
	PlainAccess bool
	// NoDestructor records that the object's class has no `__destruct`
	// anywhere in its chain, so releasing it runs nothing and need not ask
	// the class table -- which, per object, was most of a `new` loop's time.
	// Set by compiled constructors; false means ask.
	NoDestructor bool
	// declShared marks Declared as the class's table, shared by every
	// instance and never to be written in place (UseDeclarations).
	declShared bool
	// destructed records that the destructor has run (Ctx.MarkDestructed),
	// and idReleased that the handle number went back to the free list
	// (TakeIDRelease). Flags rather than maps keyed by object or number: both
	// maps grew with every object that ever died.
	destructed bool
	idReleased bool
	// extraHolds counts holders a producer handed the same object to on top
	// of the one that made it (HoldHandle): each absorbs one release.
	extraHolds int32
	// weakHolders are the WeakMaps keyed by this object and the
	// WeakReferences pointing at it, so a back-end that learns the object
	// was freed can clear them (ObjectFreed). Nil for almost every object.
	weakHolders []weakHolder
	// Shadowed names the properties this object has more than one slot for: a
	// private property an ancestor declared and a subclass declared again is
	// two properties in the engine, and the ancestor's lives under its mangled
	// key. Nil for almost every object, which is what keeps the lookup off the
	// ordinary path.
	Shadowed map[string]bool
	// Dynamic names the properties created at run time over a declared name
	// that was not visible -- an ancestor's private property. PHP keeps the two
	// in separate slots because the declared one is mangled per class; here one
	// map holds both, so which is which has to be recorded.
	Dynamic map[string]bool
	// Cleared names declared properties that unset() removed. The slot stays,
	// holding undef, because var_dump still lists a declared typed property as
	// uninitialized -- but the magic accessors answer for it, which they do not
	// for one that was merely never assigned.
	Cleared map[string]bool
	// Reinitable names readonly properties that may be written once more.
	// PHP 8.3 lets __clone reinitialize them on the clone, and only there:
	// the permission is per property and is spent by the first write.
	Reinitable map[string]bool

	// Guard, when set, is the handle whose in-flight magic accessors this
	// object shares. A lazy proxy and the instance behind it are one object as
	// far as property access is concerned, so an accessor already running on
	// the proxy must not be re-entered on the instance.
	Guard int

	// Lazy is set while an object's properties have not been produced yet.
	// PHP 8.4 lets Reflection make one: it has a class and a handle from the
	// start, and its state appears the first time anything looks at it.
	Lazy *Lazy

	// Trace is the call stack as it stood when a throwable was created, which
	// is what `getTraceAsString()` and the uncaught-fatal block render. Nil
	// means no frames were recorded, and the renderer then prints the
	// `#0 {main}` sentinel alone -- the interpreter records none today, so
	// leaving this nil preserves exactly what it printed before.
	Trace []StackFrame
	// TraceReplaced marks a throwable whose private `trace` property was
	// written through reflection (ReflectionProperty::setValue): from then
	// on getTraceAsString and the string form render that array, as the
	// engine does, instead of the recorded frames.
	TraceReplaced bool

	// Internal is state belonging to an engine-provided class that is not a PHP
	// property -- a Closure's code and bound variables, for instance. It is
	// opaque here so that this package stays free of IR types; whoever creates
	// the object owns the type stored in it.
	Internal any

	// fresh carries a clone's struct from cloneSlots to CloneObject.
	fresh *Object
}

// DisplayName is a class name as a diagnostic shows it. An anonymous class is
// stored under a name with a NUL and its definition site appended; PHP prints
// class names with C's %s, which stops at the NUL, so everything user-visible
// shows just "class@anonymous". Lookups still use the full name -- and so does
// get_class(), which returns the whole string, NUL included.
func DisplayName(class string) string { return CutAtNul(class) }

// DisplayFunction is a **function** name as a diagnostic shows it, which is not
// simply CutAtNul: an anonymous class's method reaches this package already
// joined as `Two@anonymous\0FILE:11$1::a`, and cutting at the NUL would drop
// the `::a` the engine keeps. The engine never joins them -- it interpolates
// the class (cut at its NUL) and the method separately -- so this puts the two
// halves back together.
func DisplayFunction(name string) string {
	if !strings.ContainsRune(name, 0) {
		return name
	}
	head := CutAtNul(name)
	// `->` as well as `::`: a **trace** frame names an instance method with the
	// arrow, and it reaches here joined the same way.
	at := strings.LastIndex(name, "::")
	if arrow := strings.LastIndex(name, "->"); arrow > at {
		at = arrow
	}
	if at >= 0 {
		return head + name[at:]
	}
	return head
}

// CutAtNul truncates at the first NUL byte, which is what every diagnostic the
// engine builds does: the names it interpolates are C strings, and an anonymous
// class's real name carries a NUL before the file and line that make it unique.
// A message naming one therefore ends at `class@anonymous` -- losing the
// `::method` that was appended after it, which is not obvious until a message
// is missing its method name.
func CutAtNul(text string) string {
	if at := strings.IndexByte(text, 0); at >= 0 {
		return text[:at]
	}
	return text
}

// CloneObject is the value half of `clone $o`: a **shallow** copy. The
// properties are copied, so writing one on the clone leaves the original alone,
// and a property holding an object still points at the same object -- measured,
// and it is what makes `$x->o === $y->o` true after a clone.
//
// The handle is the caller's to supply: this package does not own the
// registry. `__clone` is the caller's too, for the same reason method dispatch
// is.
func CloneObject(o *Object, id int) *Object {
	if o == nil {
		return nil
	}
	c := *o
	c.ID = id
	// A new object: its destructor has not run and its number is live,
	// whatever the original's state.
	c.destructed, c.idReleased = false, false
	// No weak container names the clone, whatever names the original.
	c.weakHolders = nil
	// **A cloned WeakMap has its own entries** (probed: writing or unsetting
	// one leaves the other alone). Sharing the store made the clone an alias.
	if m, weak := o.Internal.(*WeakMapStore); weak {
		c.Internal = m.clone()
	}
	if o.Props != nil {
		c.Props = o.Props.Clone()
	}
	if o.Slots != nil {
		// A clone is an instance of the same struct, with its own slots.
		cloneSlots(o, &c)
	}
	// Still the class's table, shared: the clone keeps sharing it. A table the
	// original made its own is copied.
	if !o.declShared {
		c.Declared = copyDeclarations(o.Declared)
	}
	c.Shadowed = copyFlags(o.Shadowed)
	c.Dynamic = copyFlags(o.Dynamic)
	c.Cleared = copyFlags(o.Cleared)
	// **Not carried over.** A readonly property's one extra write is a
	// permission `__clone` is granted on the clone, so it starts fresh rather
	// than inheriting whatever the original had left.
	c.Reinitable = nil
	if c.Slots != nil {
		// Into the struct cloneSlots allocated, so the object and its slots
		// are one allocation, as every structured instance is.
		fresh := c.fresh
		c.fresh = nil
		*fresh = c
		RegisterObject(fresh)
		return fresh
	}
	RegisterObject(&c)
	return &c
}

func copyFlags(m map[string]bool) map[string]bool {
	if m == nil {
		return nil
	}
	out := make(map[string]bool, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func copyDeclarations(m map[string]Declaration) map[string]Declaration {
	if m == nil {
		return nil
	}
	out := make(map[string]Declaration, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// UnmangleProperty splits a property name that carries the engine's visibility
// mangling: `\0Owner\0name` for a private one and `\0*\0name` for a protected
// one. Ordinary code never sees these -- the engine stores the mangling and
// hands out the plain name -- but a key that arrived through an (object) cast
// can look like one, and then it is treated as one.
// MangleProperty is the key the engine holds a non-public property under:
// `\0Owner\0name` for a private one, `\0*\0name` for a protected one.
func MangleProperty(owner, name string) string {
	return "\x00" + owner + "\x00" + name
}

func UnmangleProperty(key string) (name, owner string, ok bool) {
	if len(key) < 2 || key[0] != 0 {
		return key, "", false
	}
	at := strings.IndexByte(key[1:], 0)
	if at < 0 {
		return key, "", false
	}
	return key[at+2:], key[1 : at+1], true
}

// Obj wraps an instance as a value.
// A **nil instance is null**, not an object with nothing in it. `$this` inside
// a static closure is absent, and the emitted body carries it as a nil
// `*Object`; wrapping that as KindObject made `isset($this)` answer true, since
// isset only asks about undef and null. There is no PHP value that is "an
// object that is not there".
func Obj(o *Object) Value {
	if o == nil {
		return Null()
	}
	return Value{kind: KindObject, ptr: unsafe.Pointer(o)}
}

// Object returns the instance, or nil.
func (v Value) Object() *Object { return v.o() }

// NewObject creates an instance with no properties set.
func NewObject(class string, id int) *Object {
	o := &Object{ClassName: class, Props: NewArray(), ID: id}
	// Every way an __PHP_Incomplete_Class object is made gets the marker
	// that routes its property access to the handlers (incomplete.go).
	if len(class) == len(IncompleteClass) && class == IncompleteClass {
		o.Lazy = &Lazy{Incomplete: true}
	}
	RegisterObject(o)
	return o
}

// Get reads a property.
// Lazy is what a not-yet-initialized object carries. A *ghost* becomes an
// ordinary object once the initializer has run on it; a *proxy* stays a wrapper
// and forwards to the instance the initializer returned.
type Lazy struct {
	// OwnsInitializer says the lazy state is the **only** holder of the
	// initializer, so its object handle may be handed back when initialisation
	// drops it.
	//
	// Measured, and the reason this cannot simply always be true: with the
	// closure also kept in a variable the engine does *not* reuse its number,
	// while with the closure written inline it does. The caller proves sole
	// ownership -- a closure created and passed to this one call and used
	// nowhere else -- and marks it before handing it over.
	OwnsInitializer bool
	// Proxy distinguishes the two kinds, which differ in where the state ends
	// up and in how var_dump prints them.
	Proxy bool
	// Initializer is the callable that produces the state.
	Initializer Value
	// Instance is the object a proxy forwards to, once there is one.
	Instance *Object
	// FreshFactory says the initializer is a closure whose every return is
	// an object made there and kept nowhere else (the compiler's proof,
	// carried on the closure), so the proxy is the instance's only holder
	// once the factory returns -- as long as nothing running on the instance
	// keeps `$this` (Ctx.InstanceConfined).
	FreshFactory bool
	// InstanceOwned is that conclusion for the instance the factory made:
	// the proxy is its only holder, so it dies with the proxy
	// (OwnedProxyInstance).
	InstanceOwned bool
	// Running guards against an initializer that touches the object it is
	// initializing, which would otherwise ask for the initializer again.
	Running bool
	// Skipped names the properties that have been given a value already and so
	// no longer defer anything. Touching one of them does not initialize, and
	// once every declared property is in here the object is not lazy at all.
	Skipped map[string]bool
	// SkipSerialize is ReflectionClass::SKIP_INITIALIZATION_ON_SERIALIZE: the
	// caller has said that serializing this object must not produce its state.
	// What gets written is whatever the object holds already.
	SkipSerialize bool
	// SkipDestructor is ReflectionClass::SKIP_DESTRUCTOR.
	SkipDestructor bool
	// Incomplete marks an __PHP_Incomplete_Class object (incomplete.go). It
	// is not lazy: the marker only routes every property access through
	// ResolveLazy, which refuses one the handlers did not answer, so a path
	// that forgets the class is a gap rather than a wrong answer.
	Incomplete bool
}

func (o *Object) Get(name string) (Value, bool) {
	if o == nil {
		return Undef(), false
	}
	return o.PropGet(StringKey(name))
}

// Declaration is a typed property's declaration.
type Declaration struct {
	// Type is written as the source wrote it, since var_dump prints it back.
	Type string
	// Class is the class that declared the property, which is the name PHP
	// uses when it complains about the property rather than the instance's own
	// class -- they differ for anything inherited.
	Class string
	// Visibility is "protected" or "private" for a property that is not public.
	// var_dump and print_r annotate the name with it, and private additionally
	// names the class that declared it.
	Visibility string
	// Readonly marks a property that may be written once, from inside the class
	// that declared it, and never again. Whether it *has* been written is not
	// recorded separately: an unwritten declared property holds undef, which is
	// the same third state `var_dump` prints as `uninitialized(int)`.
	Readonly bool
	// SetVisibility is PHP 8.4's asymmetric visibility -- the `(set)` half of
	// `public private(set) int $x`. Empty when the write visibility is the same
	// as the read one.
	//
	// It is **not** the same fact as Readonly, even though readonly implies
	// `protected(set)`: readonly refuses a second write from anywhere, and this
	// refuses any write from outside, with a message that says so.
	SetVisibility string
	// SetClass is the class a **protected(set)** question resolves against,
	// which is not always the one that wrote this entry: a subclass may
	// redeclare a property its parent already declared, and the engine measures
	// the write against the *first* class in the chain to declare the name --
	// so a sibling of the redeclaring subclass may still write it.
	//
	// Separate from Class because the two disagree and both are observable:
	// the refusal names `Mid::$p` (the redeclaration) while permitting any
	// scope descending from `Base`. Empty when nothing above declares the name.
	SetClass string
	// Plain marks a property an ordinary read reaches with nothing to check:
	// public, and no hook on it. PlainWrite adds that a write has nothing to
	// check either: not readonly, untyped, no separate set visibility. Both
	// are the compiler's precomputation for its property fast path
	// (Object.PlainAccess); nothing else reads them, and false only means the
	// full path runs.
	Plain, PlainWrite bool
}

// setOwner is the class a write-visibility question resolves against.
func (d Declaration) setOwner() string {
	if d.SetClass != "" {
		return d.SetClass
	}
	return d.Class
}

// Declare records a property's declaration.
func (o *Object) Declare(name string, declaration Declaration) {
	if o == nil {
		return
	}
	if o.declShared {
		// The class's table, shared with every other instance: this object
		// takes a copy before declaring anything of its own.
		o.Declared = copyDeclarations(o.Declared)
		o.declShared = false
	}
	if o.Declared == nil {
		o.Declared = map[string]Declaration{}
	}
	o.Declared[name] = declaration
}

// TakeIDRelease reports whether this is the first time the object's handle
// number is given back, and records that it has been. Giving one number back
// twice hands it to two live objects.
func (o *Object) TakeIDRelease() bool {
	if o == nil || o.idReleased {
		return false
	}
	if o.extraHolds > 0 {
		o.extraHolds--
		return false
	}
	o.idReleased = true
	return true
}

// HoldHandle records one more holder of an object a producer hands out
// again rather than making anew -- `(new ReflectionFunction($fn))->getClosure()`
// is `$fn` itself -- so the release the compiler gives that result, believing
// it fresh, does not give back a number `$fn` still holds.
func (o *Object) HoldHandle() {
	if o != nil {
		o.extraHolds++
	}
}

// PinHandle keeps the object's handle number from ever going back: a producer
// that cannot vouch for being the only holder marks it, and every later
// release of it is then a no-op (unserialize's result, when a hook or a
// back-reference may have kept it).
func (o *Object) PinHandle() {
	if o != nil {
		o.idReleased = true
	}
}

// Finished reports an object whose destructor has run and whose number has
// gone back: nothing about it is left for a frame drain to do.
func (o *Object) Finished() bool { return o != nil && o.destructed && o.idReleased }

// UseDeclarations gives the object its class's declaration table, shared with
// every other instance until one of them declares something of its own
// (Declare copies first). Compiled classes build the table once; declaring
// property by property filled a Go map on every `new`.
//
// An object that already declared something -- an exception, whose engine
// properties runtime/php declares first -- takes the entries into its own table
// instead.
func (o *Object) UseDeclarations(table map[string]Declaration) {
	if o == nil || len(table) == 0 {
		return
	}
	if len(o.Declared) == 0 {
		o.Declared = table
		o.declShared = true
		return
	}
	for name, d := range table {
		o.Declare(name, d)
	}
}

// MarkDynamic records that a property was created at run time rather than
// being the declared one of the same name.
func (o *Object) MarkDynamic(name string) {
	if o == nil {
		return
	}
	if o.Dynamic == nil {
		o.Dynamic = map[string]bool{}
	}
	o.Dynamic[name] = true
}

// Set writes a property, creating it if absent. PHP allows this on any object
// that does not forbid dynamic properties.
func (o *Object) Set(name string, value Value) {
	if o == nil {
		return
	}
	delete(o.Cleared, name)
	o.PropSet(StringKey(name), value)
}

// Clear is unset() on a declared property: the slot survives holding undef so
// the declaration is still visible, and the name is recorded as removed.
func (o *Object) Clear(name string) {
	if o == nil {
		return
	}
	if o.Cleared == nil {
		o.Cleared = map[string]bool{}
	}
	o.Cleared[name] = true
	// The slot is emptied, not written through: a reference the property shared
	// keeps its value, and the property is no longer one of its sharers.
	if held := o.PropRefHeld(StringKey(name)); held != nil {
		Unshare(MakeRef(held))
		declared := o.Declared[name]
		owner := declared.Class
		if owner == "" {
			owner = o.ClassName
		}
		held.DropSource(owner, name)
	}
	if !o.PropRebind(StringKey(name), Undef()) {
		o.PropSet(StringKey(name), Undef())
	}
}

// AllowReinit grants one further write to a readonly property, which is what
// __clone may do on the object being cloned.
func (o *Object) AllowReinit(name string) {
	if o == nil {
		return
	}
	if o.Reinitable == nil {
		o.Reinitable = map[string]bool{}
	}
	o.Reinitable[name] = true
}

// ConsumeReinit spends the permission, reporting whether there was one.
func (o *Object) ConsumeReinit(name string) bool {
	if o == nil || !o.Reinitable[name] {
		return false
	}
	delete(o.Reinitable, name)
	return true
}

// EndReinit withdraws every remaining permission, which happens when __clone
// returns: a readonly property is fixed again afterwards.
func (o *Object) EndReinit() {
	if o != nil {
		o.Reinitable = nil
	}
}

// WasCleared reports whether unset() removed a declared property.
func (o *Object) WasCleared(name string) bool {
	return o != nil && o.Cleared[name]
}

// mangledName is one property's key outside the object.
func MangledName(object *Object, name string) string {
	if _, _, already := UnmangleProperty(name); already {
		// A shadowed ancestor's private property is held under the engine's
		// key already, because that is the only thing that tells it apart from
		// the subclass's property of the same name.
		return name
	}
	declaration, declared := object.Declared[name]
	if !declared || object.Dynamic[name] {
		return name
	}
	switch declaration.Visibility {
	case "private":
		owner := declaration.Class
		if owner == "" {
			owner = object.ClassName
		}
		return "\x00" + DisplayName(owner) + "\x00" + name
	case "protected":
		return "\x00*\x00" + name
	}
	return name
}

// notePropertyType records, on the box a property's slot holds, which typed
// property that box belongs to. A reference bound to the slot then carries the
// declared type with it: `$r = &$o->i` makes `$r = "x"` a TypeError about
// `$o->i`, not a plain string write into an anonymous box.
//
// A dynamic property has no declaration to carry, and neither does an untyped
// one -- there is nothing for a write to fail against.
func NotePropertyType(object *Object, name string, ref *Ref) *Ref {
	if ref == nil || object == nil {
		return ref
	}
	declared, ok := object.Declared[name]
	if !ok || declared.Type == "" || object.Dynamic[name] {
		return ref
	}
	owner := declared.Class
	if owner == "" {
		owner = object.ClassName
	}
	ref.AddSource(RefSource{Class: owner, Name: name, Type: declared.Type, Owner: object})
	return ref
}

// ObjectMaker allocates an object. Handles are **numbered by the program** --
// the interpreter's Unit hands them out, the generated unit counts them -- so a
// shared builtin that has to make one asks upward rather than inventing a
// number.
type ObjectMaker func(ctx *Ctx, class string) *Object

// SetObjectMaker installs the hook.
func (c *Ctx) SetObjectMaker(fn ObjectMaker) { c.newObject = fn }

// NewObjectOf makes one, reporting false when no layer here can number it.
func (c *Ctx) NewObjectOf(class string) (*Object, bool) {
	if c.newObject == nil {
		return nil, false
	}
	return c.newObject(c, class), true
}

// PropertyHider is an internal payload whose object shows **no** properties at
// all -- `SensitiveParameterValue` is the one that exists. It is an interface
// because what the payload *is* belongs to the layer that made it; all this
// needs to know is that the table must not be read.
type PropertyHider interface {
	HidesProperties() bool
}

// HidesProperties reports it for an object.
func HidesProperties(o *Object) bool {
	if o == nil {
		return false
	}
	hider, ok := o.Internal.(PropertyHider)
	return ok && hider.HidesProperties()
}

// LazyState follows an **initialized** proxy to the instance that holds the
// state. An uninitialized one is its own state, and nothing here initializes
// it: reading a lazy object's table is not a trigger.
func LazyState(o *Object) *Object {
	if o != nil && o.Lazy != nil && o.Lazy.Instance != nil {
		return o.Lazy.Instance
	}
	return o
}

// OwnedProxyInstance is the instance an initialized lazy proxy holds alone,
// which dies when the proxy does; nil otherwise. The engine frees it as the
// proxy's storage goes -- its destructor runs then, and its number goes back
// before the proxy's own (probed: `$p = null` on a proxy over `new C`
// prints C's destructor for the instance and the next three objects take the
// proxy's, the instance's and the initializer's numbers in that order).
func OwnedProxyInstance(o *Object) *Object {
	if o == nil || o.Lazy == nil || !o.Lazy.Proxy || !o.Lazy.InstanceOwned {
		return nil
	}
	return o.Lazy.Instance
}

// InitializedProxy reports a lazy proxy that has its instance. It has no
// destructor of its own: the engine runs the instance's when the instance
// dies, never the proxy's (lazy_objects/dtor_called_if_init).
func InitializedProxy(o *Object) bool {
	return o != nil && o.Lazy != nil && o.Lazy.Proxy && o.Lazy.Instance != nil && !o.Lazy.Incomplete
}
