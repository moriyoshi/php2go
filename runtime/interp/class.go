package interp

import (
	"google.golang.org/protobuf/proto"
	"strings"

	pb "github.com/moriyoshi/php2go/internal/ir/irpb"
	"github.com/moriyoshi/php2go/runtime/php"
)

// Class is a declared class as the interpreter needs it. The php package holds
// only instances, so the hierarchy and method table live here rather than
// dragging IR types into the runtime's value layer.
type Class struct {
	Name    string
	Parent  string
	decl    *pb.Class
	methods map[string]*pb.OpArray
	// constants is the same for class constants, which traits may also declare.
	constants []*pb.ClassConstant
	// properties is the declared list with the traits' contributions folded in,
	// computed on first use. The harvest cannot carry them: a class using a
	// trait is not linked when the file compiles, so the engine has not copied
	// them into its table yet.
	properties []*pb.Property
	// enginePrototypes are the op_array shapes built on demand from the
	// signatures the harvest recorded for methods the engine supplies. They
	// exist only so a class implementing this one can be checked against it,
	// and are kept apart from `methods` because that table is what decides
	// whether a method has a body to run.
	enginePrototypes map[string]*pb.OpArray
}

// hasMethod reports whether a class has a method at all, including the ones the
// engine supplies and so have no op_array. method_exists asks this question,
// and its answer is about the linked build rather than about what this runtime
// implements -- calling an engine method that is not modelled still reports as
// a gap rather than quietly doing nothing.
func (u *Unit) hasMethod(class *Class, name string, viaObject bool) bool {
	if method, declaring, found := u.resolveMethod(class, name); found {
		// Asked about an *object*, method_exists ignores visibility entirely.
		// Asked about a class *name*, a private method the class inherited
		// rather than wrote is excluded -- it is not part of that class.
		return viaObject || method.GetVisibility() != "private" ||
			(declaring != nil && strings.EqualFold(declaring.Name, class.Name))
	}
	// Closure::__invoke is not in the class's function table -- the engine
	// answers a call on it with a trampoline -- but it exists, and both
	// method_exists and is_callable say so.
	if strings.EqualFold(name, "__invoke") && u.instanceOf(class, "Closure") {
		return true
	}
	_, found := u.engineMethod(class, name)
	return found
}

// engineMethod finds a method the build supplies, walking parents and
// interfaces the way the class's own lookup does.
func (u *Unit) engineMethod(class *Class, name string) (*pb.EngineMethod, bool) {
	for current := class; current != nil; {
		for _, engine := range current.decl.GetEngineMethods() {
			if strings.EqualFold(engine.GetName(), name) {
				return engine, true
			}
		}
		for _, iface := range current.decl.GetInterfaces() {
			if parent, ok := u.findClass(iface); ok {
				if engine, found := u.engineMethod(parent, name); found {
					return engine, true
				}
			}
		}
		if current.Parent == "" {
			break
		}
		next, ok := u.findClass(current.Parent)
		if !ok {
			break
		}
		current = next
	}
	return nil, false
}

// constants returns a class's constants with its traits' folded in. As with
// properties, the harvest cannot carry them: the class is not linked yet.
func (u *Unit) constants(class *Class) []*pb.ClassConstant {
	if class == nil {
		return nil
	}
	if class.constants != nil {
		return class.constants
	}
	declared := class.decl.GetConstants()
	if len(class.decl.GetTraits()) == 0 {
		class.constants = declared
		return declared
	}
	seen := map[string]bool{}
	for _, constant := range declared {
		seen[constant.GetName()] = true
	}
	combined := append([]*pb.ClassConstant(nil), declared...)
	for _, name := range class.decl.GetTraits() {
		trait, ok := u.findClass(name)
		if !ok {
			continue
		}
		for _, constant := range u.constants(trait) {
			if seen[constant.GetName()] {
				continue
			}
			seen[constant.GetName()] = true
			combined = append(combined, constant)
		}
	}
	class.constants = combined
	return combined
}

// traitConstant finds a constant a trait declares, directly or through another
// trait it uses.
func (u *Unit) traitConstant(trait *Class, name string) (*pb.ClassConstant, bool) {
	for _, constant := range u.constants(trait) {
		if constant.GetName() == name {
			return constant, true
		}
	}
	return nil, false
}

// properties returns everything a class declares, including what its traits
// contribute. A flattened trait property belongs to the *using* class -- that
// is the name var_dump prints for a private one, and the class whose static
// storage a trait's static property lands in.
func (u *Unit) properties(class *Class) []*pb.Property {
	if class == nil {
		return nil
	}
	if class.properties != nil {
		return class.properties
	}
	declared := class.decl.GetProperties()
	if len(class.decl.GetTraits()) == 0 {
		class.properties = declared
		return declared
	}
	seen := map[string]bool{}
	for _, prop := range declared {
		seen[prop.GetName()] = true
	}
	combined := append([]*pb.Property(nil), declared...)
	for _, name := range class.decl.GetTraits() {
		trait, ok := u.findClass(name)
		if !ok {
			continue
		}
		// A trait may itself use traits, so the contribution is the trait's own
		// effective list rather than only what it wrote.
		for _, prop := range u.properties(trait) {
			if seen[prop.GetName()] {
				continue
			}
			seen[prop.GetName()] = true
			copied, _ := proto.Clone(prop).(*pb.Property)
			copied.DeclaringClass = class.Name
			combined = append(combined, copied)
		}
	}
	class.properties = combined
	return combined
}

// resolveMethod finds a method on this class or an ancestor, matching
// case-insensitively as PHP does.
func (u *Unit) resolveMethod(class *Class, name string) (*pb.OpArray, *Class, bool) {
	lower := strings.ToLower(name)
	// An abstract declaration is a requirement, not an implementation. A trait
	// that declares `abstract function m()` ranks above the parent in the
	// lookup, but a concrete `m` further up still satisfies the call -- so the
	// walk keeps going and only falls back to the abstract one if nothing
	// implements it.
	var abstractMethod *pb.OpArray
	var abstractScope *Class
	remember := func(method *pb.OpArray, scope *Class) {
		if abstractMethod == nil {
			abstractMethod, abstractScope = method, scope
		}
	}
	for current := class; current != nil; {
		if method, ok := current.methods[lower]; ok {
			if !method.GetAbstract() {
				return method, current, true
			}
			remember(method, current)
		}
		// A trait's methods are harvested with the trait, not copied into every
		// class that uses one -- copying them would make self:: inside such a
		// method name the using class. They rank above the parent's, which is
		// the precedence PHP applies when flattening.
		// `use T { a as b; }` adds a *name*, not a method: the body stays the
		// trait's and the original name keeps working.
		if method, ok := u.aliasedTraitMethod(current, lower); ok {
			if !method.GetAbstract() {
				return method, current, true
			}
			remember(method, current)
		}
		for _, name := range current.decl.GetTraits() {
			trait, ok := u.findClass(name)
			if !ok {
				continue
			}
			// `A::m insteadof B` takes B's m out of the running entirely.
			if u.traitExcluded(current, name, lower) {
				continue
			}
			// A trait may use traits of its own, and what they provide is
			// provided by it: `trait HelloWorld { use Hello, World; }` gives a
			// class that uses HelloWorld both of their methods.
			if method, ok := u.traitMethod(trait, lower, nil); ok {
				// The declaring scope is the using class: `use T` makes the
				// method a member of it, so self:: means the class.
				if !method.GetAbstract() {
					return method, current, true
				}
				remember(method, current)
			}
		}
		if current.Parent == "" {
			break
		}
		next, ok := u.findClass(current.Parent)
		if !ok {
			break
		}
		current = next
	}
	if abstractMethod != nil {
		return abstractMethod, abstractScope, true
	}
	return nil, nil, false
}

// privateInScope is `zend_std_get_method`'s second lookup: when the method a
// call resolves to is private and belongs to some *other* class, the engine
// looks for a private method of the same name declared by the **calling
// scope** and prefers that. It is what makes a private method genuinely
// per-class -- `$this->t()` inside A::callIt reaches A's `t` even on a B
// instance that declares its own -- and it applies to a private method a trait
// provided as much as to one written out.
func (u *Unit) privateInScope(scope *Class, name string) (*pb.OpArray, bool) {
	if scope == nil {
		return nil, false
	}
	lower := strings.ToLower(name)
	if method, ok := scope.methods[lower]; ok {
		return method, method.GetVisibility() == "private"
	}
	if method, ok := u.composedTraitMethod(scope, lower); ok {
		return method, method.GetVisibility() == "private"
	}
	return nil, false
}

// composedTraitMethod finds a concrete method a class gets from the traits it
// uses directly. It answers for that class alone: walking the parent chain is
// the caller's business, and an abstract trait method is a requirement rather
// than something a subclass can override.
func (u *Unit) composedTraitMethod(class *Class, lower string) (*pb.OpArray, bool) {
	if method, ok := u.aliasedTraitMethod(class, lower); ok && !method.GetAbstract() {
		return method, true
	}
	for _, name := range class.decl.GetTraits() {
		trait, ok := u.findClass(name)
		if !ok || u.traitExcluded(class, name, lower) {
			continue
		}
		if method, ok := u.traitMethod(trait, lower, nil); ok && !method.GetAbstract() {
			return method, true
		}
	}
	return nil, false
}

// composedFinalMethod reports whether a method a class composes from a trait is
// final in that class, and under what name. The trait may have declared it so,
// or a `use T { T::m as final; }` clause may have: the engine copies the
// function in with the clause's modifiers applied, so the two are the same
// thing by the time a subclass tries to override it.
func (u *Unit) composedFinalMethod(class *Class, lower string) (string, bool) {
	method, ok := u.composedTraitMethod(class, lower)
	if !ok {
		return "", false
	}
	if method.GetFinal() {
		return method.GetName(), true
	}
	for _, alias := range class.decl.GetTraitAliases() {
		if !alias.GetFinal() {
			continue
		}
		name := alias.GetAlias()
		if name == "" {
			name = alias.GetMethod()
		}
		if strings.EqualFold(name, lower) {
			return name, true
		}
	}
	return "", false
}

// traitMethod finds a method a trait provides, following the traits it uses
// itself. seen guards a `use` cycle, which the engine refuses at declaration
// but which this walk must not loop on regardless.
func (u *Unit) traitMethod(trait *Class, lower string, seen map[*Class]bool) (*pb.OpArray, bool) {
	if trait == nil || seen[trait] {
		return nil, false
	}
	if method, ok := trait.methods[lower]; ok {
		return method, true
	}
	if method, ok := u.aliasedTraitMethod(trait, lower); ok {
		return method, true
	}
	if seen == nil {
		seen = map[*Class]bool{}
	}
	seen[trait] = true
	for _, name := range trait.decl.GetTraits() {
		used, ok := u.findClass(name)
		if !ok || u.traitExcluded(trait, name, lower) {
			continue
		}
		if method, ok := u.traitMethod(used, lower, seen); ok {
			return method, true
		}
	}
	return nil, false
}

// instantiate creates an object with declared properties set to their defaults,
// walking from the root of the hierarchy down so a subclass overrides.
func (u *Unit) instantiate(class *Class) *php.Object {
	object := u.born(php.NewObject(class.Name, u.handle()))
	if u.hasDestructor(class) {
		u.live = append(u.live, object)
	}

	var chain []*Class
	for current := class; current != nil; {
		chain = append([]*Class{current}, chain...)
		if current.Parent == "" {
			break
		}
		next, ok := u.findClass(current.Parent)
		if !ok {
			break
		}
		current = next
	}
	// A private property one class declares and another in the same chain
	// declares again is two properties, not one. The engine keeps them apart by
	// mangling; php2go leaves the most-derived one under the plain name -- so
	// everything that reads an object's table by name goes on working -- and
	// gives each *shadowed* ancestor its own `\0Owner\0name` slot.
	declarers := map[string][]string{}
	for _, ancestor := range chain {
		for _, prop := range u.properties(ancestor) {
			if prop.GetStatic() || prop.GetVirtual() {
				continue
			}
			owner := prop.GetDeclaringClass()
			if owner == "" {
				owner = ancestor.Name
			}
			if !strings.EqualFold(owner, ancestor.Name) {
				// Declared further up, or contributed by a trait this ancestor
				// does not use: it is that ancestor's turn to record it. A
				// class's harvested table does not carry an ancestor's private
				// property at all.
				continue
			}
			seen := declarers[prop.GetName()]
			duplicate := false
			for _, have := range seen {
				if strings.EqualFold(have, owner) {
					duplicate = true
					break
				}
			}
			if !duplicate {
				declarers[prop.GetName()] = append(seen, owner)
			}
		}
	}
	for _, ancestor := range chain {
		for _, prop := range u.properties(ancestor) {
			if prop.GetStatic() {
				continue
			}
			// A virtual hooked property has no storage: it is not one of the
			// object's properties and never appears in a dump or a foreach.
			if prop.GetVirtual() {
				continue
			}
			// Declared properties exist before the constructor runs, holding
			// their declared initializer.
			value := php.Null()
			declaring := prop.GetDeclaringClass()
			if declaring == "" {
				declaring = ancestor.Name
			}
			// The declared type travels whether or not there is an initializer:
			// unset() can put an initialized typed property back into the
			// uninitialized state, and the dump names the type either way.
			declaration := php.Declaration{
				Class: declaring, Visibility: prop.GetVisibility(), Type: prop.GetType(),
			}
			switch {
			case prop.GetHasDefault():
				// A default the compiler could not fold was resolved when the
				// class was first used; the harvest still holds the shape.
				if resolved, ok := u.defaults[prop]; ok {
					value = resolved.Copy()
					break
				}
				value = literalValue(prop.GetDefaultValue())
			case prop.GetType() != "":
				// A typed property with no initializer is uninitialized, which
				// is a third state distinct from null: reading it is an Error,
				// it is not counted among the object's properties, and
				// var_dump names the type rather than printing a value.
				value = php.Undef()
			}
			key := prop.GetName()
			if prop.GetVisibility() == "private" && len(declarers[key]) > 1 {
				// A private property beside another declaration of the same
				// name is a slot of its own -- whatever the other one's
				// visibility is, since the engine keys a private property
				// `\0Owner\0name` and nothing else can collide with that.
				// Anything not private keeps the plain name, so an object with
				// no shadowing has the table php2go always had.
				//
				// The key is the *declaring* class, not the ancestor whose
				// table it turned up in: a class that redeclares nothing lists
				// both of its ancestors' private properties under the one
				// name, and writing them by that name would make a third slot.
				key = php.MangleProperty(php.DisplayName(declaring), prop.GetName())
				if object.Shadowed == nil {
					object.Shadowed = map[string]bool{}
				}
				object.Shadowed[prop.GetName()] = true
			}
			object.Declare(key, declaration)
			object.Set(key, value)
		}
	}
	return object
}

// methodAccessible reports whether a call made from scope may reach method.
// PHP decides by the class that declared it rather than the one the call site
// named: private is visible only inside the class that wrote it, protected
// anywhere in the same hierarchy, in either direction.
// effectiveFinal is a method's finality as the class that holds it sees it.
// `use T { m as final; }` makes the composed method final without touching the
// trait, exactly as an `as protected` changes its visibility.
func (u *Unit) effectiveFinal(declaring *Class, method *pb.OpArray, name string) bool {
	if method.GetFinal() {
		return true
	}
	if declaring == nil {
		return false
	}
	if name == "" {
		name = method.GetName()
	}
	alias, found := u.aliasFor(declaring, name, nil)
	return found && alias.GetFinal()
}

// effectiveVisibility is a method's visibility as the class that holds it sees
// it. A trait's `use T { m as protected; }` changes it for the composing class
// without touching the trait, and `use T { m as public alias; }` gives the new
// name its own -- so the answer belongs to the pair, not to the op_array.
func (u *Unit) effectiveVisibility(declaring *Class, method *pb.OpArray, name string) string {
	if declaring == nil || method == nil {
		return method.GetVisibility()
	}
	if name == "" {
		name = method.GetName()
	}
	if alias, found := u.aliasFor(declaring, name, nil); found && alias.GetVisibility() != "" {
		return alias.GetVisibility()
	}
	return method.GetVisibility()
}

// aliasFor finds the `as` clause that governs a name in a class, following the
// traits it uses. A trait may compose another and change its visibility on the
// way in -- `trait T2 { use T1 { aa as public; } }` -- and a class using T2 gets
// the method as T2 composed it, not as T1 wrote it.
func (u *Unit) aliasFor(class *Class, name string, seen map[*Class]bool) (*pb.TraitAlias, bool) {
	if class == nil || seen[class] {
		return nil, false
	}
	for _, alias := range class.decl.GetTraitAliases() {
		claimed := alias.GetAlias()
		if claimed == "" {
			claimed = alias.GetMethod()
		}
		if strings.EqualFold(claimed, name) {
			return alias, true
		}
	}
	if seen == nil {
		seen = map[*Class]bool{}
	}
	seen[class] = true
	for _, used := range class.decl.GetTraits() {
		trait, ok := u.findClass(used)
		if !ok {
			continue
		}
		if alias, found := u.aliasFor(trait, name, seen); found {
			return alias, true
		}
	}
	return nil, false
}

func (u *Unit) methodAccessible(method *pb.OpArray, declaring, scope *Class) bool {
	return u.methodAccessibleAs(method, declaring, scope, "")
}

// methodAccessibleAs is methodAccessible for a caller that knows which *name*
// it reached the method by, which is what a trait alias's visibility applies to.
func (u *Unit) methodAccessibleAs(method *pb.OpArray, declaring, scope *Class, name string) bool {
	if declaring == nil {
		return true
	}
	switch u.effectiveVisibility(declaring, method, name) {
	case "private":
		return scope != nil && strings.EqualFold(scope.Name, declaring.Name)
	case "protected":
		if scope == nil {
			return false
		}
		// The hierarchy check runs against the class that *first* declared the
		// method, not the one the lookup landed on. An override does not narrow
		// where a protected method can be reached from: `B1::ma` overriding
		// `A::ma` is still reachable from a sibling `B2 extends A`. A private
		// parent method is a different method, so the walk stops there.
		root := u.methodRoot(declaring, method.GetName())
		return u.isSubclassOf(scope.Name, root.Name) ||
			u.isSubclassOf(root.Name, scope.Name)
	}
	return true
}

// methodRoot walks up to the highest ancestor declaring the same method without
// making it private, which is what the engine's prototype pointer records.
func (u *Unit) methodRoot(declaring *Class, name string) *Class {
	root := declaring
	if strings.EqualFold(name, "__construct") {
		// A constructor has no prototype -- overriding one is not overriding
		// anything -- so the class that declared it is the root. That is what
		// makes a *sibling* unable to reach it: `B extends A` calling
		// `new C` where C also extends A and redeclares the constructor is
		// refused, while the same pair of ordinary methods is not.
		return root
	}
	for current := root; current != nil && current.Parent != ""; {
		parent, ok := u.findClass(current.Parent)
		if !ok {
			break
		}
		inherited, declared, found := u.resolveMethod(parent, name)
		if !found || inherited.GetVisibility() == "private" || declared == nil {
			break
		}
		root, current = declared, declared
	}
	return root
}

// namedMethodCallable answers is_callable for a method named through a class
// name rather than an instance.
func (u *Unit) namedMethodCallable(ctx *php.Ctx, className, method string, viaObject bool) bool {
	resolved, ok := u.callableClass(ctx, className)
	if !ok {
		return false
	}
	class, known := u.findClass(resolved)
	if !known {
		return false
	}
	return u.methodCallable(ctx, class, method, viaObject)
}

// methodCallable reports what is_callable answers for a method: it exists, is
// not abstract, and the calling scope may reach it -- or the magic forwarder
// that would answer for it does exist. Which forwarder applies depends on how
// the callable named its target, not on whether the method is static.
func (u *Unit) methodCallable(ctx *php.Ctx, class *Class, name string, viaObject bool) bool {
	method, declaring, found := u.resolveMethod(class, name)
	if found && !method.GetAbstract() && u.methodAccessibleAs(method, declaring, u.callingScope(ctx), name) {
		if viaObject || method.GetStatic() {
			return true
		}
		// An instance method reached through a *class name* is only callable
		// when the calling scope has an instance of that class. Probed: outside
		// one, is_callable(["C", "m"]) is false; inside a method of C it is
		// true -- even though the call itself is refused either way.
		this := u.callingThis(ctx)
		if this == nil {
			return false
		}
		held, known := u.findClass(this.ClassName)
		return known && u.instanceOf(held, class.Name)
	}
	// A method the engine supplies has no op_array, but it is still callable.
	if !found {
		if engine, ok := u.engineMethod(class, name); ok &&
			!engine.GetAbstract() && engine.GetVisibility() != "private" {
			return true
		}
	}
	// A class name reached from inside an instance of that class forwards to
	// __call rather than __callStatic -- the same choice the call itself makes
	// -- so is_callable answers true there even though the call is refused.
	if u.forwarderFor(ctx, class, viaObject) != "" {
		return true
	}
	forwarder := "__callStatic"
	if viaObject {
		forwarder = "__call"
	}
	_, _, hasForwarder := u.resolveMethod(class, forwarder)
	return hasForwarder
}

// scopeName names the calling scope the way a visibility diagnostic does.
func scopeName(scope *Class) string {
	if scope == nil {
		return "global scope"
	}
	return "scope " + php.DisplayName(scope.Name)
}

// forwardToMagicCall queues the __call that answers for a method the caller
// cannot reach -- whether because the class does not declare it or because it
// declares it privately. PHP treats the two the same way.
func (u *Unit) forwardToMagicCall(object *php.Object, class *Class, name string) (*pendingCall, bool) {
	magic, magicDeclaring, ok := u.magicMethod(object, magicCall)
	if !ok {
		return nil, false
	}
	body, _, _ := u.resolveMethod(magic, magicCall)
	return &pendingCall{
		name: object.ClassName + "::" + name, this: object,
		method: body, class: magicDeclaring, called: class, forwardTo: name, forwarding: true,
	}, true
}

// findClass looks a class up by name, case-insensitively, preferring a
// user declaration so a program can shadow a built-in.
func (u *Unit) findClass(name string) (*Class, bool) {
	// A fully qualified name may be written with a leading separator, and the
	// class table is keyed without one: `\ns\foo` and `ns\foo` are the same
	// class, as they are to zend_lookup_class.
	lower := strings.ToLower(strings.TrimPrefix(name, "\\"))
	if class, ok := u.classes[lower]; ok {
		return class, true
	}
	class, ok := u.builtins[lower]
	return class, ok
}

// isBuiltinOnly reports whether a class comes from the engine rather than the
// program. Such a class has no op_arrays, so its behaviour must be supplied by
// the runtime or it has none at all.
func (u *Unit) isBuiltinOnly(class *Class) bool {
	_, declared := u.classes[strings.ToLower(class.Name)]
	return !declared
}

// visibleProps is the subset of an object's properties reachable from a given
// scope, which is what foreach over an object iterates. From outside the class
// that is the public ones; from inside, everything declared there as well.
func (u *Unit) visibleProps(object *php.Object, scope *Class, unmangle bool) *php.Array {
	visible := php.NewArray()
	if hidesProperties(object) {
		return visible
	}
	class, known := u.findClass(object.ClassName)
	// Names already answered by a private property of the asking scope. A
	// subclass's redeclaration of one does not replace it: the first visible
	// slot wins, and the private slot comes first because its class does.
	answered := map[string]bool{}
	object.Props.EachRaw(func(k php.Key, raw php.Value) bool {
		// A property that holds a *program-made* reference stays one in the
		// answer: `get_object_vars` copies the table, and copying an array
		// keeps a reference two names share as a reference.
		v := php.CopyElement(raw)
		// An uninitialized typed property has no value to hand out, and
		// foreach skips it rather than yielding null.
		if v.Deref().Kind() == php.KindUndef {
			return true
		}
		plain, owner, mangled := php.UnmangleProperty(k.String())
		if declared, isDeclared := object.Declared[k.String()]; mangled && isDeclared &&
			declared.Visibility == "private" {
			// A private property held under its own key because another
			// declaration answers to the same name. Only the class that wrote
			// it sees it, and it is seen under the plain name -- unlike a
			// *dynamic* property whose name merely happens to be mangled,
			// which is handed back as it stands.
			if scope == nil || !strings.EqualFold(scope.Name, owner) {
				return true
			}
			answered[plain] = true
			visible.Set(php.StringKey(plain), v)
			return true
		}
		if answered[k.String()] {
			return true
		}
		// A property the object grew at run time is public, whatever the class
		// declares under that name -- and it may well declare a private one,
		// since that is what let the dynamic property be created at all.
		if !known || object.Dynamic[k.String()] || u.propVisible(class, scope, k.String()) {
			if mangled && unmangle {
				visible.Set(php.StringKey(plain), v)
				return true
			}
			visible.Set(k, v)
		}
		return true
	})
	return visible
}

// propVisible reports whether a property is reachable from scope. A property
// with no declaration is dynamic, and dynamic properties are always public.
func (u *Unit) propVisible(class, scope *Class, name string) bool {
	for current := class; current != nil; {
		for _, prop := range u.properties(current) {
			if prop.GetName() != name {
				continue
			}
			// The class the property was *declared* in decides, not the one
			// whose table it was found in: Zend flattens inherited properties
			// into the subclass's table, so `class D extends C {}` lists C's
			// private $priv, and iterating $this from inside C must see it.
			owner := current
			if declaring := prop.GetDeclaringClass(); declaring != "" &&
				!strings.EqualFold(declaring, current.Name) {
				if found, ok := u.findClass(declaring); ok {
					owner = found
				}
			}
			switch prop.GetVisibility() {
			case "private":
				return scope != nil && strings.EqualFold(scope.Name, owner.Name)
			case "protected":
				return scope != nil && (u.instanceOf(scope, owner.Name) || u.instanceOf(owner, scope.Name))
			default:
				return true
			}
		}
		if current.Parent == "" {
			break
		}
		next, ok := u.findClass(current.Parent)
		if !ok {
			break
		}
		current = next
	}
	return true
}

// staticPropRef returns the shared slot behind a static property, initializing
// it from the declaration the first time. Static properties belong to the class
// and outlive any call, so they are boxed: `self::$n++` and a reference taken
// with =& must see the same storage.
// rebindStaticProp makes a static property share a box, which is what
// `C::$p = &$x` does: the property stops holding whatever it held rather than
// writing through it. It answers what the property held before, so the caller
// can let the destructor of that value run at the right moment.
func (u *Unit) rebindStaticProp(className, propName string, box *php.Ref) (php.Value, bool) {
	key, ok := u.staticPropKey(className, propName)
	if !ok {
		return php.Undef(), false
	}
	var previous php.Value
	if held, known := u.staticProps[key]; known {
		previous = held.Get()
		// The box stops being this property's storage, so it stops carrying
		// the property's type: a name still bound to it is an ordinary
		// reference from here on.
		held.DropSource(u.staticPropOwner(className, propName), propName)
	}
	if u.staticProps == nil {
		u.staticProps = map[string]*php.Ref{}
	}
	u.staticProps[key] = box
	return previous, true
}

// staticPropKey names the storage a static property uses. A subclass shares its
// parent's unless it redeclares, so the key comes from where the property was
// declared rather than from where it was named.
func (u *Unit) staticPropKey(className, propName string) (string, bool) {
	class, ok := u.findClass(className)
	if !ok {
		return "", false
	}
	decl, found := u.staticPropDecl(class, propName)
	if !found {
		return "", false
	}
	owner := decl.GetDeclaringClass()
	if owner == "" {
		owner = class.Name
	}
	return owner + "::$" + propName, true
}

func (u *Unit) staticPropRef(className, propName string) (*php.Ref, bool) {
	class, ok := u.findClass(className)
	if !ok {
		return nil, false
	}
	// A subclass shares its parent's storage unless it redeclares, and the
	// class's own table includes what it inherited -- so the key comes from
	// where the property was declared, not where it was read from.
	decl, found := u.staticPropDecl(class, propName)
	if !found {
		return nil, false
	}
	owner := decl.GetDeclaringClass()
	if owner == "" {
		owner = class.Name
	}
	// And so does the *declaration*: a subclass's flattened copy of an
	// inherited static carries no initializer, so building the storage from it
	// would give null to a property the parent gave a value.
	if !strings.EqualFold(owner, class.Name) {
		if ownerClass, known := u.findClass(owner); known {
			if ownerDecl, found := u.staticPropDecl(ownerClass, propName); found {
				decl = ownerDecl
			}
		}
	}
	key := owner + "::$" + propName
	if ref, ok := u.staticProps[key]; ok {
		return ref, true
	}
	value := php.Null()
	switch {
	case decl.GetHasDefault():
		// An initializer the compiler could not fold has already been evaluated
		// by updateClassConstants, in the frame and at the moment the engine
		// evaluates it. Reading it through literalValue instead would quietly
		// produce null -- and skip whatever the evaluation itself reports.
		if resolved, ok := u.defaults[decl]; ok {
			value = resolved.Copy()
			break
		}
		value = literalValue(decl.GetDefaultValue())
	case decl.GetType() != "":
		// A typed static property with no default holds nothing, not null:
		// reading it before it has been assigned is an Error, the same third
		// state an instance property has.
		value = php.Undef()
	}
	if u.staticProps == nil {
		u.staticProps = map[string]*php.Ref{}
	}
	ref := php.NewRef(value)
	// A static property's slot *is* the box, so binding a name to it shares
	// the box itself -- and with it the declared type. That is what makes
	// `$r = &A::$s; $r = "x"` a TypeError rather than a plain string write.
	if decl.GetType() != "" {
		ref.AddSource(php.RefSource{Class: owner, Name: propName, Type: decl.GetType()})
	}
	u.staticProps[key] = ref
	return ref, true
}

// staticProp reads a static property without creating storage for one that was
// never declared, which is what isset() needs. An inaccessible one reads as
// absent: isset() does not raise, it answers false.
func (u *Unit) staticProp(className, propName string, scope *Class) (php.Value, bool) {
	if _, _, ok := u.staticPropAccess(className, propName, scope); !ok {
		return php.Undef(), false
	}
	ref, ok := u.staticPropRef(className, propName)
	if !ok {
		return php.Undef(), false
	}
	// A typed property that was never assigned holds nothing, and isset()
	// answers about a value: there is none, so it reads as absent rather than
	// raising the way an ordinary read does.
	if value := ref.Get(); !value.Deref().IsUndef() {
		return value, true
	}
	return php.Undef(), false
}

// staticPropAccess reports whether scope may reach a static property, and names
// the class that declared it so a diagnostic can quote it. A property that does
// not exist is reported as accessible: that is a different failure, and the
// caller distinguishes it.
func (u *Unit) staticPropAccess(className, propName string, scope *Class) (owner string, visibility string, ok bool) {
	class, found := u.findClass(className)
	if !found {
		return "", "", true
	}
	decl, found := u.staticPropDecl(class, propName)
	if !found {
		return "", "", true
	}
	owner = decl.GetDeclaringClass()
	if owner == "" {
		owner = class.Name
	}
	switch decl.GetVisibility() {
	case "private":
		return owner, "private", scope != nil && strings.EqualFold(scope.Name, owner)
	case "protected":
		if scope == nil {
			return owner, "protected", false
		}
		// As with an instance property: the class a protected member is
		// measured against is the first one to declare it, so two children of
		// the same parent can reach each other's.
		against := u.protectedOwner(class, propName, owner)
		return owner, "protected",
			u.isSubclassOf(scope.Name, against) || u.isSubclassOf(against, scope.Name)
	}
	return owner, decl.GetVisibility(), true
}

// constantAccess applies a class constant's visibility, the same way
// staticPropAccess applies a static property's. PHP 7.1 gave constants
// visibility and 8.4 still measures a protected one against the first class to
// declare it, so two children of one parent can reach each other's.
func (u *Unit) constantAccess(className, name string, scope *Class) (owner, visibility string, ok bool) {
	class, found := u.findClass(className)
	if !found {
		return "", "", true
	}
	decl, holder, found := u.findClassConstant(class, name)
	if !found {
		return "", "", true
	}
	owner = decl.GetDeclaringClass()
	if declared, known := u.findClass(owner); owner == "" ||
		(known && declared.decl.GetKind() == pb.Class_KIND_TRAIT) {
		// `use T` makes a trait's constant a member of the *using* class, so
		// that is what its visibility is measured against: a private trait
		// constant is reachable from the class that used the trait.
		owner = ""
		if holder != nil {
			owner = holder.Name
		}
	}
	if owner == "" {
		owner = class.Name
	}
	switch decl.GetVisibility() {
	case "private":
		return owner, "private", scope != nil && strings.EqualFold(scope.Name, owner)
	case "protected":
		if scope == nil {
			return owner, "protected", false
		}
		return owner, "protected",
			u.isSubclassOf(scope.Name, owner) || u.isSubclassOf(owner, scope.Name)
	}
	return owner, decl.GetVisibility(), true
}

func (u *Unit) staticPropDecl(class *Class, name string) (*pb.Property, bool) {
	for current := class; current != nil; {
		for _, prop := range u.properties(current) {
			if prop.GetStatic() && prop.GetName() == name {
				return prop, true
			}
		}
		if current.Parent == "" {
			break
		}
		next, ok := u.findClass(current.Parent)
		if !ok {
			break
		}
		current = next
	}
	return nil, false
}

// classConstant resolves Name::CONST, walking to ancestors and interfaces --
// a class constant is inherited, and an interface can declare one too.
// constantKey names one class constant by where it was *declared*, which is
// what the engine's evaluation cache is keyed by: an inherited constant is
// flattened into every subclass's table but evaluated once.
type constantKey struct {
	owner string
	name  string
}

func (in *Interp) classConstant(ctx *php.Ctx, u *Unit, className, name string, depth int) (php.Value, bool, error) {
	class, ok := u.findClass(className)
	if !ok {
		return php.Undef(), false, nil
	}
	if class.decl.GetKind() == pb.Class_KIND_TRAIT {
		// A trait's constant belongs to whatever uses the trait. Reaching it
		// through the trait's own name is refused outright rather than being
		// reported as missing.
		if _, found := u.traitConstant(class, name); found {
			return php.Undef(), false, Raise("Error",
				"Cannot access trait constant %s::%s directly",
				php.DisplayName(class.Name), name)
		}
		return php.Undef(), false, nil
	}
	for current := class; current != nil; {
		for _, constant := range u.constants(current) {
			if constant.GetName() != name {
				continue
			}
			in.noteDeprecatedConstant(ctx, u, current, constant, depth)
			// An enum case is not a plain value: it is a singleton object of
			// the enum's own class, built on first reference.
			if constant.GetIsCase() {
				// Reaching a case by name builds the backed table, which is
				// where duplicate values are noticed.
				if err := in.verifyBackedValues(ctx, u, current, depth); err != nil {
					return php.Undef(), false, err
				}
				value, err := in.enumCase(ctx, u, current, constant, depth)
				if err != nil {
					return php.Undef(), false, err
				}
				return value, true, nil
			}
			// A constant whose initializer the compiler left as an expression
			// has no recorded value; reporting it as missing is better than
			// substituting null, which would silently compute wrong answers.
			if !constant.GetHasValue() {
				return php.Undef(), false, &UnsupportedError{
					Opcode: "class constant " + current.Name + "::" + name,
				}
			}
			// self:: inside the initializer means the class that declared it,
			// which the engine records on the constant -- inherited constants
			// are flattened into every subclass's table, so where the walk
			// found it is not where it was written. `const B = self::C;` in a
			// parent keeps meaning the parent's C when read through a child
			// that declares its own.
			owner := current
			if declaring := constant.GetDeclaringClass(); declaring != "" &&
				!strings.EqualFold(declaring, current.Name) {
				if found, ok := u.findClass(declaring); ok {
					owner = found
				}
			}
			// The engine evaluates an unfoldable initializer once and keeps the
			// result on the declaring class, so reading the same constant
			// through a subclass does not evaluate it again -- which is
			// observable whenever the evaluation reports something.
			cacheKey := constantKey{owner: owner.Name, name: name}
			cached, wasCached := u.constantValues[cacheKey]
			value, resolved, err := cached, true, error(nil)
			if !wasCached {
				// An initializer the compiler could not fold is evaluated in a
				// frame of its own, the same as a property default is.
				leave := func() {}
				if evaluatedInOwnFrame(constant.GetValue()) {
					leave = u.enterConstExpression(ctx, owner.decl.GetFilename(),
						constant.GetValue().GetExpression().GetLine())
				}
				value, resolved, err = in.constantLiteral(ctx, u, constant.GetValue(), owner, depth)
				leave()
			}
			if err != nil {
				return php.Undef(), false, err
			}
			if resolved && !wasCached {
				if u.constantValues == nil {
					u.constantValues = map[constantKey]php.Value{}
				}
				u.constantValues[cacheKey] = value
			}
			if !resolved {
				// The initializer is a shape the harvest could not read. That
				// is a gap in this runtime, and reporting it as a constant that
				// does not exist would hide it behind a plausible PHP error.
				return php.Undef(), false, &UnsupportedError{
					Opcode: "class constant " + current.Name + "::" + name +
						unresolvedSuffix(constant.GetValue()),
				}
			}
			// A typed constant is checked against its type where it is *read*,
			// since the initializer may be an expression whose value only
			// exists then -- a global constant defined after the class, say.
			if declared := constant.GetType(); declared != "" {
				converted, ok := in.checkConstantType(ctx, u, declared, current, value)
				if !ok {
					return php.Undef(), false, Raise("TypeError",
						"Cannot assign %s to class constant %s::%s of type %s",
						givenTypeName(value), php.DisplayName(current.Name), name, declared)
				}
				value = converted
			}
			return value, true, nil
		}
		for _, iface := range current.decl.GetInterfaces() {
			value, ok, err := in.classConstant(ctx, u, iface, name, depth)
			if ok || err != nil {
				return value, ok, err
			}
		}
		if current.Parent == "" {
			break
		}
		next, ok := u.findClass(current.Parent)
		if !ok {
			break
		}
		current = next
	}
	return php.Undef(), false, nil
}

// isSubclassOf reports whether the named class descends from another, used to
// decide whether an inherited $this survives a self:: call.
func (u *Unit) isSubclassOf(name, ancestor string) bool {
	class, ok := u.findClass(name)
	if !ok {
		return false
	}
	return u.instanceOf(class, ancestor)
}

// instanceOf reports whether a class is, or descends from, the named one.
func (u *Unit) instanceOf(class *Class, name string) bool {
	// class_alias gives one class a second name, so a name comparison alone
	// answers no where PHP answers yes. The resolved entry is shared, which
	// makes identity the reliable test; the name comparison stays for the case
	// where the name is not declared at all.
	target, aliased := u.findClass(name)
	lower := strings.ToLower(strings.TrimPrefix(name, "\\"))
	// The whole graph, not the direct interfaces: an interface has parents of
	// its own, and `class C implements Iterator` is a Traversable through
	// Iterator. abstractSources walks parents and interfaces transitively.
	for _, source := range u.abstractSources(class) {
		if source.decl.GetKind() == pb.Class_KIND_TRAIT {
			// Composing a trait is not a subtype relation.
			continue
		}
		if strings.ToLower(source.Name) == lower || (aliased && source == target) {
			return true
		}
	}
	// A class with __toString is a Stringable whether it said so or not. The
	// engine adds the interface when it links the class, and the harvest is
	// taken before that -- which is invisible for a method the class writes
	// itself, since the engine had already linked *that* one, but not for one a
	// trait provides: the trait is not composed until link time either.
	if lower == "stringable" && class != nil && class.decl.GetKind() == pb.Class_KIND_CLASS {
		if method, _, found := u.resolveMethod(class, "__tostring"); found && !method.GetAbstract() {
			return true
		}
	}
	return false
}

// hasDestructor reports whether a class provides a body for __destruct, which
// is what decides whether an object is worth remembering for shutdown.
func (u *Unit) hasDestructor(class *Class) bool {
	method, _, found := u.resolveMethod(class, "__destruct")
	return found && method != nil && !method.GetAbstract()
}

// forwardStaticToCall answers a static-syntax call that found no method with
// __call rather than __callStatic. The engine prefers it whenever there is a
// $this that belongs to the class being named -- `self::m()` inside a method is
// an instance call written the other way round, and behaves like one.
func (u *Unit) forwardStaticToCall(f *frame, class *Class, className, methodName string) (*pendingCall, bool) {
	if f.this == nil {
		return nil, false
	}
	current, known := u.findClass(f.this.ClassName)
	if !known || !u.instanceOfName(current, class.Name) {
		return nil, false
	}
	body, magicDeclaring, ok := u.resolveMethod(class, magicCall)
	if !ok {
		return nil, false
	}
	return &pendingCall{
		name: className + "::" + methodName, this: f.this, method: body,
		class: magicDeclaring, called: class, forwardTo: methodName, forwarding: true,
	}, true
}

// verifyPropertyDefaults checks each declared default against the property's
// type. Only a *deferred* default can fail -- a literal one that does not match
// is refused when the file compiles -- and the check is made afresh for every
// instantiation, which is why two `new` of the same class report twice.
//
// The check is strict whatever the file declared: `public int $n = NUMSTR;`
// where NUMSTR is "7" is refused in a weak-mode file too, while `public float
// $f = FIVE;` widens, which is exactly what strict mode allows.
func (in *Interp) verifyPropertyDefaults(ctx *php.Ctx, unit *Unit, class *Class) error {
	for _, current := range unit.ancestry(class) {
		for _, prop := range unit.properties(current) {
			if prop.GetType() == "" || !prop.GetHasDefault() || prop.GetVirtual() {
				continue
			}
			value, resolved := unit.defaults[prop]
			if !resolved {
				// A literal default was settled when the file compiled.
				continue
			}
			owner := prop.GetDeclaringClass()
			if owner == "" {
				owner = current.Name
			}
			declared := prop.GetType()
			if declaring, known := unit.findClass(owner); known {
				declared = frameType(declared, &frame{class: declaring})
			}
			converted, ok := in.checkType(ctx, unit, declared, value, true)
			if !ok {
				return in.throwError(ctx, unit, "TypeError",
					"Cannot assign %s to property %s::$%s of type %s",
					givenTypeName(value), php.DisplayName(owner), prop.GetName(), prop.GetType())
			}
			// The one conversion strict mode makes is int to float, and the
			// default is stored widened: `public float $f = FIVE;` holds
			// float(5), not int(5).
			unit.defaults[prop] = converted
		}
	}
	return nil
}

// updateClassConstants is zend_update_class_constants: the engine resolves a
// class's deferred initializers -- its constants and its property defaults --
// when the class is first *used*, not when each one is read. That timing is
// observable, because an initializer naming an undeclared class autoloads, and
// an autoloader can print and can throw.
//
// It runs once per class per request. The results are cached on the unit rather
// than written back into the harvest, which is shared.
func (in *Interp) updateClassConstants(ctx *php.Ctx, unit *Unit, class *Class, depth int) error {
	if class == nil || unit.updated[class] || depth >= maxDepth {
		return nil
	}
	for _, current := range unit.ancestry(class) {
		for _, constant := range unit.constants(current) {
			if constant.GetIsCase() || !constant.GetHasValue() ||
				constant.GetValue().GetKind() != pb.Literal_KIND_EXPR {
				continue
			}
			if _, _, err := in.classConstant(ctx, unit, current.Name, constant.GetName(), depth); err != nil {
				return err
			}
		}
		for _, prop := range unit.properties(current) {
			if !prop.GetHasDefault() || prop.GetDefaultValue().GetKind() != pb.Literal_KIND_EXPR {
				continue
			}
			if _, resolved := unit.defaults[prop]; resolved {
				continue
			}
			// A property default the compiler could not fold runs in a frame
			// of its own, so a diagnostic raised inside one names
			// `[constant expression]` above the site that triggered it.
			// Probed: static and instance property defaults get the frame, a
			// class constant does not, and a parameter default is reported
			// against the function it belongs to.
			leave := func() {}
			if evaluatedInOwnFrame(prop.GetDefaultValue()) {
				leave = unit.enterConstExpression(ctx, current.decl.GetFilename(),
					prop.GetDefaultValue().GetExpression().GetLine())
			}
			value, ok, err := in.constantLiteral(ctx, unit, prop.GetDefaultValue(), current, depth)
			leave()
			if err != nil {
				return err
			}
			if !ok {
				// Unresolvable rather than wrong: leave it for the read path to
				// report as the gap it is.
				continue
			}
			if unit.defaults == nil {
				unit.defaults = map[*pb.Property]php.Value{}
			}
			unit.defaults[prop] = value
		}
	}
	// Marked only once the walk has finished. An initializer can autoload, and
	// an autoloader can instantiate the very class being resolved -- gh10709_2
	// does exactly that, and the inner `new` has to resolve the default itself
	// rather than find a half-finished class marked done.
	if unit.updated == nil {
		unit.updated = map[*Class]bool{}
	}
	unit.updated[class] = true
	return nil
}

// aliasedTraitMethod resolves a name a `use ... { ... as ... }` clause
// introduced. The alias names an existing trait method, optionally qualified by
// the trait it came from when more than one provides it.
func (u *Unit) aliasedTraitMethod(class *Class, lower string) (*pb.OpArray, bool) {
	for _, alias := range class.decl.GetTraitAliases() {
		if alias.GetAlias() == "" || !strings.EqualFold(alias.GetAlias(), lower) {
			continue
		}
		if alias.GetClass() != "" {
			if trait, ok := u.findClass(alias.GetClass()); ok {
				if method, ok := trait.methods[strings.ToLower(alias.GetMethod())]; ok {
					return method, true
				}
			}
			continue
		}
		// Unqualified: whichever of the class's traits provides it.
		for _, name := range class.decl.GetTraits() {
			trait, ok := u.findClass(name)
			if !ok {
				continue
			}
			if method, ok := trait.methods[strings.ToLower(alias.GetMethod())]; ok {
				return method, true
			}
		}
	}
	return nil, false
}

// traitExcluded reports whether an `insteadof` clause took one trait's version
// of a method out of the running for this class.
func (u *Unit) traitExcluded(class *Class, traitName, lower string) bool {
	for _, rule := range class.decl.GetTraitPrecedences() {
		if !strings.EqualFold(rule.GetMethod(), lower) {
			continue
		}
		for _, excluded := range rule.GetExcludes() {
			if strings.EqualFold(excluded, traitName) {
				return true
			}
		}
	}
	return false
}

// noteTraitStaticAccess reports the deprecation PHP 8.1 attached to reaching a
// trait's static member through the trait itself. The storage belongs to each
// class that uses the trait, so naming the trait is asking for a slot that has
// no single answer.
func (u *Unit) noteTraitStaticAccess(ctx *php.Ctx, className, member string, isProperty bool) {
	class, ok := u.findClass(className)
	if !ok || class.decl.GetKind() != pb.Class_KIND_TRAIT {
		return
	}
	if isProperty {
		ctx.Deprecated("Accessing static trait property %s::$%s is deprecated, "+
			"it should only be accessed on a class using the trait",
			php.DisplayName(class.Name), member)
		return
	}
	ctx.Deprecated("Calling static trait method %s::%s is deprecated, "+
		"it should only be called on a class using the trait",
		php.DisplayName(class.Name), member)
}

// staticPropOwner names the class a static property should be blamed on, which
// is where it was declared rather than where it was reached from.
func (u *Unit) staticPropOwner(className, propName string) string {
	class, ok := u.findClass(className)
	if !ok {
		return className
	}
	if decl, found := u.staticPropDecl(class, propName); found && decl.GetDeclaringClass() != "" {
		return decl.GetDeclaringClass()
	}
	return class.Name
}

// traitComposition is a class and every trait it composes, transitively. A
// trait may use traits of its own, and what they declare -- methods, and the
// aliases the `use` clause introduces -- belongs to the class just as much as
// what the class wrote itself.
func (u *Unit) traitComposition(class *Class) []*Class {
	if class == nil {
		return nil
	}
	out := []*Class{class}
	seen := map[*Class]bool{class: true}
	for i := 0; i < len(out); i++ {
		for _, name := range out[i].decl.GetTraits() {
			trait, ok := u.findClass(name)
			if !ok || seen[trait] {
				continue
			}
			seen[trait] = true
			out = append(out, trait)
		}
	}
	return out
}
