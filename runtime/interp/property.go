package interp

import (
	"strings"

	pb "github.com/moriyoshi/php2go/internal/ir/irpb"
	"github.com/moriyoshi/php2go/runtime/php"
)

// access is what a property access from a given scope is allowed to do.
type access int

const (
	// accessAllowed reads or writes the declared property.
	accessAllowed access = iota
	// accessDenied raises "Cannot access <visibility> property C::$p".
	accessDenied
	// accessHidden means the property is not visible from here at all, so the
	// access behaves as though the property did not exist -- __get answers, or
	// a read warns about an undefined property and a write creates a dynamic
	// one. This is what an *inherited* private property does: the child's
	// table never sees the parent's.
	accessHidden
)

// propertyDecl finds a declared property. A harvested class carries the
// flattened table, inherited entries included, so one scan is enough --
// declaring_class is what says where an entry came from.
func (u *Unit) propertyDecl(class *Class, name string) (*pb.Property, bool) {
	if class == nil {
		return nil, false
	}
	for _, prop := range u.properties(class) {
		if prop.GetStatic() {
			continue
		}
		if prop.GetName() == name {
			return prop, true
		}
	}
	return nil, false
}

// ownPrivateProperty finds a private property a class declared itself, which is
// its own and not the same as a subclass's property of that name.
func (u *Unit) ownPrivateProperty(class *Class, name string) (*pb.Property, bool) {
	for _, prop := range u.properties(class) {
		if prop.GetStatic() || prop.GetName() != name {
			continue
		}
		if prop.GetVisibility() == "private" &&
			strings.EqualFold(prop.GetDeclaringClass(), class.Name) {
			return prop, true
		}
		return nil, false
	}
	return nil, false
}

// hideDeclaredSlot moves a declared private property onto its own key so that a
// *dynamic* property of the same name can take the plain one. The engine never
// had this problem: it keys a private property `\0Owner\0name` always, so a
// dynamic `name` is a different entry. php2go keys it plainly until something
// else claims the name, and this is that moment -- a write from a scope that
// cannot see the private one, which grows a dynamic property instead.
//
// The entry is renamed in place: the table's order is observable, and the
// declared property has to stay where it was declared.
func (u *Unit) hideDeclaredSlot(object *php.Object, name string) {
	if object == nil {
		return
	}
	declared, known := object.Declared[name]
	if !known || declared.Visibility != "private" {
		return
	}
	owner := declared.Class
	if owner == "" {
		owner = object.ClassName
	}
	key := php.MangleProperty(php.DisplayName(owner), name)
	if !object.Props.Rekey(php.StringKey(name), php.StringKey(key)) {
		return
	}
	object.Declare(key, declared)
	delete(object.Declared, name)
	if object.Shadowed == nil {
		object.Shadowed = map[string]bool{}
	}
	object.Shadowed[name] = true
}

// propertySlot is the key an object's table holds a property under, for a scope
// that is asking by name. Everything is under its plain name except a private
// property an ancestor declared and a subclass declared again: those are two
// properties, and inside the ancestor the name means the ancestor's. Outside
// both, the plain name stands -- the most-derived declaration keeps it, and an
// access from there is refused on visibility anyway.
func propertySlot(object *php.Object, name string, scope *Class) string {
	if object == nil || scope == nil || len(object.Shadowed) == 0 {
		return name
	}
	if !object.Shadowed[name] {
		return name
	}
	key := php.MangleProperty(php.DisplayName(scope.Name), name)
	if _, held := object.Props.Get(php.StringKey(key)); held {
		return key
	}
	return name
}

// propertyAccess decides what a scope may do with a property of an object.
func (u *Unit) propertyAccess(object *php.Object, name string, scope *Class) (*pb.Property, access) {
	class, ok := u.findClass(object.ClassName)
	if !ok {
		return nil, accessAllowed
	}
	if plain, owner, mangled := php.UnmangleProperty(name); mangled {
		// A shadowed ancestor's slot. The declaration is that ancestor's, and
		// only that ancestor may reach it -- which is the same answer the
		// plain-name path gives, reached without having to guess which of the
		// two declarations the name meant.
		declarer, known := u.findClass(owner)
		if !known {
			return nil, accessAllowed
		}
		prop, found := u.ownPrivateProperty(declarer, plain)
		if !found {
			return nil, accessAllowed
		}
		if scope != nil && strings.EqualFold(scope.Name, owner) {
			return prop, accessAllowed
		}
		return prop, accessHidden
	}
	// Two classes in one chain may each declare a *private* property of the
	// same name, and they are different properties. Which one a name means is
	// decided by the scope doing the reading: inside A, `$this->prop` is A's,
	// even on an instance of a B that declares its own.
	if scope != nil {
		if own, found := u.ownPrivateProperty(scope, name); found {
			if u.instanceOf(class, scope.Name) {
				return own, accessAllowed
			}
		}
	}
	prop, ok := u.propertyDecl(class, name)
	if !ok && !object.Dynamic[name] {
		// The class's own table does not list the property, but the object has
		// a slot for it: a private one an ancestor declared. That happens when
		// the chain was linked at run time -- a `use T` anywhere in it delays
		// the binding past the harvest -- and the object's own record of the
		// declaration is then the only one there is. Outside the class that
		// wrote it the name does not mean this property at all.
		if declared, held := object.Declared[name]; held &&
			declared.Visibility == "private" &&
			(scope == nil || !strings.EqualFold(scope.Name, declared.Class)) {
			return &pb.Property{
				Name: name, DeclaringClass: declared.Class,
				Visibility: "private", Type: declared.Type,
			}, accessHidden
		}
	}
	if !ok || object.Dynamic[name] {
		// Not declared, or shadowed by a property created at run time over an
		// invisible declared one. Neither is restricted.
		return nil, accessAllowed
	}
	switch prop.GetVisibility() {
	case "private":
		if scope != nil && strings.EqualFold(scope.Name, prop.GetDeclaringClass()) {
			return prop, accessAllowed
		}
		// A private property of an *ancestor* is not part of this object's
		// visible table at all, which is why the diagnostic is about an
		// undefined property rather than an inaccessible one.
		if !strings.EqualFold(prop.GetDeclaringClass(), class.Name) {
			return prop, accessHidden
		}
		return prop, accessDenied
	case "protected":
		if scope == nil {
			return prop, accessDenied
		}
		// The class a protected property is measured against is the *first*
		// one to declare it, not the last: redeclaring it in a subclass does
		// not narrow who may see it. That is what lets one child of an
		// abstract parent read the property off an instance of another child.
		owner := u.protectedOwner(class, name, prop.GetDeclaringClass())
		if u.isSubclassOf(scope.Name, owner) || u.isSubclassOf(owner, scope.Name) {
			return prop, accessAllowed
		}
		return prop, accessDenied
	}
	return prop, accessAllowed
}

// deniedPropertyError names the object's class rather than the declaring one,
// which is what PHP reports even for a property a parent wrote.
func (in *Interp) deniedPropertyError(ctx *php.Ctx, unit *Unit, object *php.Object,
	prop *pb.Property, name string) error {
	return in.throwError(ctx, unit, "Error", "Cannot access %s property %s::$%s",
		prop.GetVisibility(), php.DisplayName(object.ClassName), name)
}

// checkReadonlyWrite rejects a write to a readonly property. A readonly slot
// may be written exactly once, and only from the class that declared it: after
// that the value is fixed for every scope, including the declaring one.
func (in *Interp) checkReadonlyWrite(ctx *php.Ctx, unit *Unit, object *php.Object,
	prop *pb.Property, name string, scope *Class) error {
	if prop == nil || !prop.GetReadonly() {
		return nil
	}
	// Once the slot holds a value it is fixed for every scope, including the
	// one that wrote it, and that check comes first: the message says nothing
	// about visibility because visibility is no longer the reason.
	if propertyInitialized(object, name) {
		// __clone may reinitialize a readonly property on the object being
		// cloned, once each. Outside that, a value once written is fixed for
		// every scope including the one that wrote it -- and the message says
		// nothing about visibility, because visibility is not the reason.
		if object.ConsumeReinit(name) {
			return nil
		}
		// The class named is the one that *declared* the property, not the
		// instance's: `class B extends A {}` writing A's readonly property
		// reports A. Nothing about the instance is at fault.
		owner := prop.GetDeclaringClass()
		if owner == "" {
			owner = object.ClassName
		}
		return in.throwError(ctx, unit, "Error",
			"Cannot modify readonly property %s::$%s", php.DisplayName(owner), name)
	}
	// Uninitialized and out of reach is the set-visibility's refusal, which
	// checkSetVisibility has already made by the time this runs.
	return nil
}

// noteStaticAsInstance reports a static property reached through an instance.
// PHP does not redirect the access -- the object ends up with a dynamic
// property of the same name, and the class's static keeps its own value -- so
// this says only that what was written is not what the class declares.
// isset() and empty() ask without accessing and say nothing.
// magic names the accessor that would answer this kind of access; when the
// class has one it answers instead, and there is nothing to complain about.
func (u *Unit) noteStaticAsInstance(ctx *php.Ctx, object *php.Object, name, magic string) {
	if object == nil {
		return
	}
	class, ok := u.findClass(object.ClassName)
	if !ok {
		return
	}
	if u.magicAvailable(object, magic, name) {
		return
	}
	if _, static := u.staticPropDecl(class, name); !static {
		return
	}
	// A subclass may declare an instance property of the same name, and it wins
	// -- the access reaches that property and there is nothing to complain
	// about. Only a name the class knows *only* as a static earns this.
	if _, instance := u.propertyDecl(class, name); instance {
		return
	}
	ctx.Notice("Accessing static property %s::$%s as non static",
		php.DisplayName(object.ClassName), name)
}

// refuseReadonlyEarly is the readonly check made *before* the value is
// converted to the property's declared type. The engine settles readonly first,
// so `$this->readonlyString = new S()` never runs S::__toString.
//
// It leaves a __clone reinitialization alone rather than spending it: the check
// that consumes the permission is checkReadonlyWrite, further along.
func (in *Interp) refuseReadonlyEarly(ctx *php.Ctx, unit *Unit, object *php.Object,
	name string, scope *Class) error {
	prop, verdict := unit.propertyAccess(object, name, scope)
	if verdict != accessAllowed || prop == nil || !prop.GetReadonly() ||
		object == nil || object.Reinitable[name] {
		return nil
	}
	return in.checkReadonlyWrite(ctx, unit, object, prop, name, scope)
}

// checkPropertyWrite runs both checks a write has to pass. A denied write goes
// to __set when the class has one, which is why that case reports accessHidden
// rather than an error: the caller then takes its usual "not held" path.
func (in *Interp) checkPropertyWrite(ctx *php.Ctx, unit *Unit, object *php.Object,
	name string, scope *Class) (access, error) {
	if err := in.refuseReflectionWrite(ctx, unit, object, name, scope); err != nil {
		return accessDenied, err
	}
	unit.noteStaticAsInstance(ctx, object, name, magicSet)
	prop, verdict := unit.propertyAccess(object, name, scope)
	switch verdict {
	case accessDenied:
		// __set answers for a denied write -- but only when it can actually be
		// entered. Inside __set already, writing the same name reaches the
		// declared property directly, and the refusal stands: the engine does
		// not quietly grow a dynamic property alongside the one it refused.
		if unit.magicAvailable(object, magicSet, name) {
			return accessHidden, nil
		}
		return verdict, in.deniedPropertyError(ctx, unit, object, prop, name)
	case accessHidden:
		return verdict, nil
	}
	if hidden, err := in.checkPropertyModifyFor(ctx, unit, object, prop, name, scope); err != nil {
		return verdict, err
	} else if hidden {
		return accessHidden, nil
	}
	return verdict, nil
}

// checkPropertyWriteAccess is checkPropertyWrite without the half a compound
// assignment defers: whether the scope may reach the property at all, which is
// settled before the operation runs.
func (in *Interp) checkPropertyWriteAccess(ctx *php.Ctx, unit *Unit, object *php.Object,
	name string, scope *Class) (access, error) {
	if err := in.refuseReflectionWrite(ctx, unit, object, name, scope); err != nil {
		return accessDenied, err
	}
	unit.noteStaticAsInstance(ctx, object, name, magicSet)
	prop, verdict := unit.propertyAccess(object, name, scope)
	switch verdict {
	case accessDenied:
		if unit.magicAvailable(object, magicSet, name) {
			return accessHidden, nil
		}
		return verdict, in.deniedPropertyError(ctx, unit, object, prop, name)
	case accessHidden:
		return verdict, nil
	}
	if unit.slotAbsent(object, name) && unit.magicAvailable(object, magicSet, name) {
		return accessHidden, nil
	}
	return verdict, nil
}

// checkPropertyModify is the half of checkPropertyWrite a *compound* assignment
// makes after the operation rather than before it. Probed: `$o->prop += 1` on a
// readonly `object` property reports `Unsupported operand types: stdClass +
// int`, while the same on a readonly `int` reports `Cannot modify readonly
// property` -- so the value is computed first and only then judged. Visibility
// is not part of this: `Cannot access private property` still comes first.
func (in *Interp) checkPropertyModify(ctx *php.Ctx, unit *Unit, object *php.Object,
	name string, scope *Class) error {
	prop, verdict := unit.propertyAccess(object, name, scope)
	if verdict != accessAllowed {
		return nil
	}
	_, err := in.checkPropertyModifyFor(ctx, unit, object, prop, name, scope)
	return err
}

// checkPropertyModifyFor is the shared body. The bool answers "__set takes it
// instead", which only the write path acts on.
func (in *Interp) checkPropertyModifyFor(ctx *php.Ctx, unit *Unit, object *php.Object,
	prop *pb.Property, name string, scope *Class) (bool, error) {
	if err := in.checkSetVisibility(ctx, unit, object, prop, name, scope, "modify"); err != nil {
		// A property that was explicitly unset() has no slot at all, and a
		// write to one that is not there is what __set answers for. A declared
		// but uninitialized property still has its slot, so the refusal stands.
		if unit.slotAbsent(object, name) && unit.magicAvailable(object, magicSet, name) {
			return true, nil
		}
		return false, err
	}
	// The same applies without any refusal to make: unset() took the slot away,
	// so the write is to a property that is not there and __set answers. A
	// declared property that was merely never initialized still has its slot.
	if unit.slotAbsent(object, name) && unit.magicAvailable(object, magicSet, name) {
		return true, nil
	}
	return false, in.checkReadonlyWrite(ctx, unit, object, prop, name, scope)
}

// refuseReflectionWrite enforces ext/reflection's own write_property handler:
// every reflector refuses a write to `$name` or `$class`, from any scope,
// whenever its class declares that property. The two names are the reflectors'
// entire public surface -- everything else about them is read through a method
// -- so the handler is a two-name check rather than a readonly flag.
func (in *Interp) refuseReflectionWrite(ctx *php.Ctx, unit *Unit, object *php.Object,
	name string, scope *Class) error {
	if object == nil || (name != "name" && name != "class") {
		return nil
	}
	if _, held := object.Internal.(*reflector); !held {
		return nil
	}
	if prop, _ := unit.propertyAccess(object, name, scope); prop == nil {
		return nil
	}
	return in.throwError(ctx, unit, "ReflectionException",
		"Cannot set read-only property %s::$%s", php.DisplayName(object.ClassName), name)
}

// checkSetVisibility enforces PHP 8.4 asymmetric visibility: `public
// private(set) $p` reads from anywhere and is written only from the class that
// declared it. The write half is checked separately from the read half, which
// propertyAccess already settled.
func (in *Interp) checkSetVisibility(ctx *php.Ctx, unit *Unit, object *php.Object,
	prop *pb.Property, name string, scope *Class, operation string) error {
	setVisibility := prop.GetSetVisibility()
	if setVisibility == "" {
		return nil
	}
	// A readonly property is refused for two different reasons at two different
	// times. Before it holds a value, writing is an *initialization*, which the
	// set-visibility governs -- and `readonly` implies protected(set), so that
	// is the error even when the source wrote no such modifier. Once it holds
	// one, readonly's own rule takes over and applies to every scope.
	if prop.GetReadonly() && propertyInitialized(object, name) {
		return nil
	}
	owner := prop.GetDeclaringClass()
	if scope != nil && strings.EqualFold(scope.Name, owner) {
		return nil
	}
	if setVisibility == "protected" && scope != nil {
		// As with reading a protected property: the class a protected(set)
		// member is measured against is the first one to declare it, so a
		// sibling may write what it inherited from the same parent.
		against := owner
		if class, known := unit.findClass(object.ClassName); known {
			against = unit.protectedOwner(class, name, owner)
		}
		if unit.isSubclassOf(scope.Name, against) || unit.isSubclassOf(against, scope.Name) {
			return nil
		}
	}
	wording := setVisibility + "(set)"
	if prop.GetReadonly() && setVisibility == "protected" {
		wording = "protected(set) readonly"
	}
	return in.throwError(ctx, unit, "Error", "Cannot %s %s property %s::$%s from %s",
		operation, wording, php.DisplayName(owner), name, scopeName(scope))
}

// noteDynamicProperty reports the deprecation PHP 8.2 attached to creating a
// property a class never declared. stdClass and anything carrying
// #[AllowDynamicProperties] are exempt, and so is an object whose class is not
// in the table at all -- there is nothing to have declared it.
func (u *Unit) noteDynamicProperty(ctx *php.Ctx, object *php.Object, name string, verdict access) {
	class, ok := u.findClass(object.ClassName)
	if !ok || class.decl.GetAllowDynamicProperties() || object.ClassName == "stdClass" {
		return
	}
	// A declared property only counts as declared when it is visible from
	// here: an inherited private one is not, so writing that name really does
	// create a new property alongside it.
	if _, declared := u.propertyDecl(class, name); declared && verdict != accessHidden {
		return
	}
	ctx.Deprecated("Creation of dynamic property %s::$%s is deprecated", php.DisplayName(object.ClassName), name)
}

// checkPropertyUnset is the same decision for unset(). Readonly refuses only
// once the property holds a value: before that there is nothing to protect.
func (in *Interp) checkPropertyUnset(ctx *php.Ctx, unit *Unit, object *php.Object,
	name string, scope *Class) (access, error) {
	unit.noteStaticAsInstance(ctx, object, name, magicUnset)
	prop, verdict := unit.propertyAccess(object, name, scope)
	switch verdict {
	case accessDenied:
		if unit.magicAvailable(object, magicUnset, name) {
			return accessHidden, nil
		}
		return verdict, in.deniedPropertyError(ctx, unit, object, prop, name)
	case accessHidden:
		return verdict, nil
	}
	if err := in.checkSetVisibility(ctx, unit, object, prop, name, scope, "unset"); err != nil {
		if unit.slotAbsent(object, name) && unit.magicAvailable(object, magicUnset, name) {
			return accessHidden, nil
		}
		return verdict, err
	}
	if prop.GetReadonly() && propertyInitialized(object, name) {
		// An *uninitialized* readonly property may be unset from a scope that
		// could have written it: nothing is being taken back, and the slot goes
		// back to having no value. Once written it is fixed, and unset is a
		// modification like any other.
		return verdict, in.throwError(ctx, unit, "Error",
			"Cannot unset readonly property %s::$%s", php.DisplayName(object.ClassName), name)
	}
	return verdict, nil
}

// frameScope names the class whose body a frame is running, or "" at top level.
func frameScope(f *frame) string {
	if f == nil || f.class == nil {
		return ""
	}
	return f.class.Name
}

// frameCalledScope is what `static::` means inside a frame: the class the call
// site named, falling back to the declaring one when there was none.
func frameCalledScope(f *frame) string {
	if f == nil {
		return ""
	}
	if f.called != nil {
		return f.called.Name
	}
	return frameScope(f)
}

// callingScope is the class that made the call a builtin is answering. A
// builtin has no frame of its own, so the scope comes off the call stack --
// where the frame pushed for this very call recorded its caller's.
func (u *Unit) callingScope(ctx *php.Ctx) *Class {
	if ctx.Scope() == "" {
		return nil
	}
	class, ok := u.findClass(ctx.Scope())
	if !ok {
		return nil
	}
	return class
}

// callingThis is the object the innermost call in progress is bound to, which
// is what decides whether a static-syntax callable reaches __call rather than
// __callStatic. The stack carries it because a backtrace needs it anyway.
// callingCalledScope is what `static::` resolves to for a builtin, which is the
// late-static-binding class of the frame that made the call.
func (u *Unit) callingCalledScope(ctx *php.Ctx) *Class {
	if ctx.CalledScope() == "" {
		return nil
	}
	class, ok := u.findClass(ctx.CalledScope())
	if !ok {
		return nil
	}
	return class
}

func (u *Unit) callingThis(ctx *php.Ctx) *php.Object {
	stack := ctx.Stack()
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i].This != nil {
			return stack[i].This
		}
	}
	return nil
}

// visibleProperties lists the declared properties a scope may see on a class,
// in the order PHP hands them out: the class's own first, then each ancestor's,
// with the statics after the instance ones. That is properties_info's insertion
// order, which the harvest replaced with offset order because var_dump needs
// *that* one.
func (u *Unit) visibleProperties(class *Class, scope *Class) []*pb.Property {
	var own, inherited, ownStatic, inheritedStatic []*pb.Property
	for _, prop := range u.properties(class) {
		if !u.propertyVisibleFrom(prop, class, scope) {
			continue
		}
		// **Own before inherited, in both groups.** The harvest lists a class's
		// properties parent-first -- which is the order an *instance* carries,
		// and what `get_object_vars` reports -- while a question about the
		// *class* puts its own declarations first. The statics were grouped
		// last but not reordered, so a child's static came after its parent's.
		mine := strings.EqualFold(prop.GetDeclaringClass(), class.Name)
		switch {
		case prop.GetStatic() && mine:
			ownStatic = append(ownStatic, prop)
		case prop.GetStatic():
			inheritedStatic = append(inheritedStatic, prop)
		case mine:
			own = append(own, prop)
		default:
			inherited = append(inherited, prop)
		}
	}
	return append(append(append(own, inherited...), ownStatic...), inheritedStatic...)
}

// declaredStaticDefault is a static property's **declared** value, which is not
// the slot: the slot is shared with every subclass and carries whatever the
// program last wrote, and `get_class_vars` reports the declaration.
//
// The inherited copy of a property record does not carry the default -- only
// the declaring class's own does -- so a static that came down the chain is
// looked up where it was written. A default that is a constant expression is
// not a literal at all, and there the evaluated slot is the only answer
// available.
func (u *Unit) declaredStaticDefault(class *Class, prop *pb.Property) php.Value {
	declaring := class
	if !strings.EqualFold(prop.GetDeclaringClass(), class.Name) {
		if owner, ok := u.findClass(prop.GetDeclaringClass()); ok {
			declaring = owner
			for _, candidate := range u.properties(owner) {
				if candidate.GetStatic() && candidate.GetName() == prop.GetName() &&
					strings.EqualFold(candidate.GetDeclaringClass(), owner.Name) {
					prop = candidate
					break
				}
			}
		}
	}
	if prop.GetHasDefault() {
		if value := literalValue(prop.GetDefaultValue()); value.Kind() != php.KindUndef {
			return value
		}
	}
	if ref, ok := u.staticPropRef(declaring.Name, prop.GetName()); ok {
		return ref.Get().Deref()
	}
	return php.Null()
}

// propertyVisibleFrom is propertyAccess's rule without an instance: private is
// visible only inside the class that wrote it, protected anywhere in the
// hierarchy.
func (u *Unit) propertyVisibleFrom(prop *pb.Property, class, scope *Class) bool {
	switch prop.GetVisibility() {
	case "private":
		return scope != nil && strings.EqualFold(scope.Name, prop.GetDeclaringClass())
	case "protected":
		if scope == nil {
			return false
		}
		return u.isSubclassOf(scope.Name, prop.GetDeclaringClass()) ||
			u.isSubclassOf(prop.GetDeclaringClass(), scope.Name)
	}
	return true
}

// indirectWriteRefused reports the error a reference into a property earns, if
// any: readonly and an asymmetric set-visibility both refuse one, and PHP words
// both as "indirectly modify".
// It reports copyOnly for the case the engine spells out: a W/RW/UNSET fetch of
// a property that currently holds an *object* might not modify the property at
// all -- `$o->prop->field = x` reaches through it -- so the fetch is allowed and
// hands back a copy, which is what makes modifying the slot itself impossible.
func (in *Interp) indirectWriteRefused(ctx *php.Ctx, unit *Unit, object *php.Object,
	name string, scope *Class) (err error, copyOnly bool) {
	if object == nil {
		return nil, false
	}
	restricted := false
	declaring, readonly := unit.readonlyProperty(object, name)
	var prop *pb.Property
	if class, known := unit.findClass(object.ClassName); known {
		if declared, ok := unit.propertyDecl(class, name); ok {
			prop = declared
			restricted = in.checkSetVisibility(ctx, unit, object, declared, name, scope, "indirectly modify") != nil
		}
	}
	if !readonly && !restricted {
		return nil, false
	}
	if held, _ := object.Get(name); held.Deref().Kind() == php.KindObject {
		return nil, true
	}
	if readonly {
		return in.throwError(ctx, unit, "Error",
			"Cannot indirectly modify readonly property %s::$%s",
			php.DisplayName(declaring), name), false
	}
	return in.checkSetVisibility(ctx, unit, object, prop, name, scope, "indirectly modify"), false
}

// propertyInitialized reports whether a property currently holds a value, as
// opposed to being declared and empty -- either never assigned or unset().
func propertyInitialized(object *php.Object, name string) bool {
	if object == nil {
		return false
	}
	if object.WasCleared(name) {
		return false
	}
	value, held := object.Props.Get(php.StringKey(name))
	return held && value.Deref().Kind() != php.KindUndef
}

// slotAbsent reports that an object holds no slot for a property at all, which
// is the state unset() leaves a declared one in. A declared property that was
// never assigned still has its slot, holding undef -- the engine distinguishes
// the two with IS_PROP_UNINIT, and the magic accessors only answer for the
// first.
func (u *Unit) slotAbsent(object *php.Object, name string) bool {
	if object == nil {
		return true
	}
	if object.WasCleared(name) {
		return true
	}
	_, held := object.Props.Get(php.StringKey(name))
	return !held
}

// readonlyProperty reports whether a property refuses indirect modification,
// and names the class a diagnostic should blame. An enum case's `name` and
// `value` are readonly without being declared anywhere: they are built onto the
// object, so the class kind decides rather than a property flag.
func (u *Unit) readonlyProperty(object *php.Object, name string) (string, bool) {
	if object == nil {
		return "", false
	}
	class, known := u.findClass(object.ClassName)
	if !known {
		return "", false
	}
	if class.decl.GetKind() == pb.Class_KIND_ENUM {
		return class.Name, name == "name" || name == "value"
	}
	prop, found := u.propertyDecl(class, name)
	if !found || !prop.GetReadonly() {
		return "", false
	}
	declaring := prop.GetDeclaringClass()
	if declaring == "" {
		declaring = class.Name
	}
	return declaring, true
}

// bindableProperty reports whether `$o->p = &$x` has a slot of the object's own
// to bind. A declared property the caller may see has one, and so does a
// dynamic property that already exists or that nothing overloads. What does not
// is a name the caller cannot reach, or one that exists only through __get:
// binding there would make a second, shadowed property beside the overloaded
// one, which is why the engine refuses instead.
func (u *Unit) bindableProperty(object *php.Object, name string, scope *Class) bool {
	if object == nil {
		return false
	}
	_, verdict := u.propertyAccess(object, name, scope)
	if verdict != accessAllowed {
		return false
	}
	if value, present := object.Get(name); present &&
		!(value.Deref().IsUndef() && object.WasCleared(name)) {
		return true
	}
	// The property is not there. Creating it is fine unless the class answers
	// for the name itself, or refuses to grow at all. A __get already running
	// for this name does not answer for it: that is what the guard means, and
	// it is why a binding made from inside the accessor reaches the table.
	return !u.magicAvailable(object, magicGet, name) && !u.forbidsDynamic(object, name)
}

// forbidsDynamic reports whether a class refuses a property it did not declare
// -- an enum, or one of the engine's own classes carrying the flag.
func (u *Unit) forbidsDynamic(object *php.Object, name string) bool {
	if object == nil {
		return false
	}
	if _, declared := object.Declared[name]; declared {
		return false
	}
	class, known := u.findClass(object.ClassName)
	if !known {
		return false
	}
	return class.decl.GetKind() == pb.Class_KIND_ENUM || class.decl.GetNoDynamicProperties()
}

// nulProperty reports whether a name is one the engine refuses to reach a
// property under. A NUL *inside* a name is fine -- it is how a mangled private
// property is spelled -- but a name that starts with one names the mangling
// itself, and reading, writing or unsetting it is an Error. isset() is the
// exception: it answers false without complaining, like any absent name.
func nulProperty(name string) bool {
	return strings.HasPrefix(name, "\x00")
}

// refuseNulProperty is the error for reaching such a name. The check belongs
// where the property table is actually touched, not at the top of the handler:
// the magic accessors are asked first, which is why `unset($a->$prop)` on a
// class with __unset reports the failure from inside that method.
func (in *Interp) refuseNulProperty(ctx *php.Ctx, unit *Unit) error {
	return in.throwError(ctx, unit, "Error", `Cannot access property starting with "\0"`)
}

// uninitializedTyped is the Error a read-modify-write on a declared property
// that holds nothing must raise. `$o->n++` and `$o->n .= "x"` read before they
// write, and a typed property that was never assigned has no value to read: the
// engine reports that rather than treating it as null and stepping from there.
//
// Being declared is what makes it an Error instead of a magic call. `__get` is
// offered the name only once unset() has taken the property away -- until then
// the property is there, it just holds nothing.
func (in *Interp) uninitializedTyped(ctx *php.Ctx, unit *Unit, object *php.Object,
	property string) error {
	declaration, declared := object.Declared[property]
	if !declared {
		return nil
	}
	if value, present := object.Get(property); !present || !value.Deref().IsUndef() {
		return nil
	}
	if object.WasCleared(property) && unit.hasMagic(object, magicGet) {
		return nil
	}
	owner := declaration.Class
	if owner == "" {
		owner = object.ClassName
	}
	return in.throwError(ctx, unit, "Error",
		"Typed property %s::$%s must not be accessed before initialization",
		php.DisplayName(owner), property)
}

// refuseArrayAutoInit reports the write that would have to grow an array inside
// a typed property whose type cannot hold one. The property has to be holding
// nothing -- null or uninitialized -- for the question to arise at all; one
// already holding a string takes the ordinary `[] operator not supported for
// strings`.
func (in *Interp) refuseArrayAutoInit(ctx *php.Ctx, unit *Unit, object *php.Object,
	property string) error {
	declaration, declared := object.Declared[property]
	if !declared || declaration.Type == "" || object.Dynamic[property] {
		return nil
	}
	if held, present := object.Get(property); present {
		if kind := held.Deref().Kind(); kind != php.KindNull && kind != php.KindUndef {
			return nil
		}
	}
	owner := declaration.Class
	if owner == "" {
		owner = object.ClassName
	}
	resolved := declaration.Type
	if declaring, known := unit.findClass(owner); known {
		resolved = frameType(resolved, &frame{class: declaring})
	}
	if _, ok := in.checkType(ctx, unit, resolved, php.Arr(php.NewArray()), true); ok {
		return nil
	}
	return in.throwError(ctx, unit, "TypeError",
		"Cannot auto-initialize an array inside property %s::$%s of type %s",
		php.DisplayName(owner), property, declaration.Type)
}

// refuseStaticAutoInit is refuseArrayAutoInit for a static property, whose
// declaration is found through the class rather than through an instance.
func (in *Interp) refuseStaticAutoInit(ctx *php.Ctx, unit *Unit, className, property string,
	ref *php.Ref) error {
	if kind := ref.Get().Deref().Kind(); kind != php.KindNull && kind != php.KindUndef {
		return nil
	}
	class, known := unit.findClass(className)
	if !known {
		return nil
	}
	declared, found := unit.staticPropDecl(class, property)
	if !found || declared.GetType() == "" {
		return nil
	}
	owner := declared.GetDeclaringClass()
	if owner == "" {
		owner = class.Name
	}
	resolved := declared.GetType()
	if declaring, ok := unit.findClass(owner); ok {
		resolved = frameType(resolved, &frame{class: declaring})
	}
	if _, ok := in.checkType(ctx, unit, resolved, php.Arr(php.NewArray()), true); ok {
		return nil
	}
	return in.throwError(ctx, unit, "TypeError",
		"Cannot auto-initialize an array inside property %s::$%s of type %s",
		php.DisplayName(owner), property, declared.GetType())
}

// protectedOwner is the highest ancestor whose declaration of a property still
// carries the same protected visibility. Walking stops at a private one: that
// declaration is not part of the same property as far as visibility goes, and
// the child's protected declaration starts a new scope.
//
// Static and instance properties share this, and share a namespace for it: a
// class cannot declare both a static and an instance property of one name.
func (u *Unit) protectedOwner(class *Class, name, declared string) string {
	owner := declared
	for current := class; current != nil; {
		for _, prop := range u.properties(current) {
			if prop.GetName() != name {
				continue
			}
			if prop.GetVisibility() == "private" {
				return owner
			}
			owner = current.Name
			break
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
	return owner
}

// propertyName renders the operand that names a property. It is a literal
// string almost everywhere, but a variable may hold anything, and the engine
// converts it the way it converts anything else it needs as a string: an object
// goes through __toString, an array warns and becomes "Array". Both can fail,
// which is why this is not simply Value.String().
func (in *Interp) propertyName(ctx *php.Ctx, unit *Unit, f *frame,
	kind, operand uint32) (string, error) {
	value := f.fetch(kind, operand).Deref()
	if value.Kind() == php.KindString {
		return value.String(), nil
	}
	rendered, err := in.toString(ctx, unit, value)
	if err != nil {
		return "", err
	}
	return rendered.String(), nil
}
