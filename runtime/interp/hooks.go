package interp

import (
	"sort"
	"strings"

	pb "github.com/moriyoshi/php2go/internal/ir/irpb"
	"github.com/moriyoshi/php2go/runtime/php"
)

// PHP 8.4 property hooks. `public $p { get { ... } set { ... } }` compiles the
// bodies into op_arrays that hang off the property rather than going into the
// class's function table, so they are harvested with the property and invoked
// from the property-access opcodes here.
//
// A hooked property may still have storage behind it -- it is "backed" when a
// hook mentions `$this->p` -- or be virtual, existing only through its hooks.
// The engine decides which by flagging the property, and the flag is harvested
// rather than inferred.
//
// The recursion rule is the subtle part. Inside a hook, `$this->p` reaches the
// backing store instead of calling the hook again, but *only* directly inside
// it: a hook that calls a method which touches the property does re-enter, and
// PHP lets that recurse until the stack gives out. The engine's test is on the
// currently executing frame, so this one is too.

// hookIdentity names the property a hook frame belongs to. The engine compares
// the property's *prototype*, so a subclass redeclaring a hooked property does
// not re-enter the parent's hook from inside it; the equivalent here is the
// class that first declared the property, which is what the prototype points at.
type hookIdentity struct {
	name string
	root string
}

// hookIdentityOf builds the identity for a property reached through a class.
func (u *Unit) hookIdentityOf(declaring *Class, name string) hookIdentity {
	return hookIdentity{name: name, root: strings.ToLower(u.propertyRoot(declaring, name))}
}

// propertyRoot names the highest ancestor that declares the property, which is
// what a redeclaration's prototype points at.
func (u *Unit) propertyRoot(class *Class, name string) string {
	root := ""
	for current := class; current != nil; {
		for _, prop := range u.properties(current) {
			if prop.GetName() == name && !prop.GetStatic() {
				root = current.Name
				break
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
	if root == "" && class != nil {
		root = class.Name
	}
	return root
}

// hookedProperty finds a property with hooks, searching the class and its
// ancestors. It reports the property and the class that declared it.
func (u *Unit) hookedProperty(class *Class, name string) (*pb.Property, *Class, bool) {
	if plain, owner, mangled := php.UnmangleProperty(name); mangled {
		// A private property held under its own key. Only the class that wrote
		// it answers: a subclass's hooked property of the same name is a
		// different property, and its hooks are not this one's.
		declaring, known := u.findClass(owner)
		if !known {
			return nil, nil, false
		}
		prop, found := u.ownPrivateProperty(declaring, plain)
		if !found || (prop.GetGetHook() == nil && prop.GetSetHook() == nil) {
			return nil, nil, false
		}
		return prop, declaring, true
	}
	for current := class; current != nil; {
		for _, prop := range u.properties(current) {
			if prop.GetName() != name || prop.GetStatic() {
				continue
			}
			if prop.GetGetHook() == nil && prop.GetSetHook() == nil {
				return nil, nil, false
			}
			declaring := current
			if named := prop.GetDeclaringClass(); named != "" {
				if found, ok := u.findClass(named); ok {
					declaring = found
				}
			}
			return prop, declaring, true
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
	return nil, nil, false
}

// hooksFor reports the hooked property behind an object's member, if the access
// should go through the hooks at all. An access made from inside that very
// property's hook, on the same object, is the backing-store access instead.
func (u *Unit) hooksFor(f *frame, object *php.Object, name string) (*pb.Property, *Class, bool) {
	var scope *Class
	if f != nil {
		scope = f.class
	}
	return u.hooksForScope(f, object, name, scope)
}

// topFrame is the frame running now, which is what a hook's recursion guard is
// asked about. Nil when nothing is running -- a builtin reached from outside a
// frame.
func (u *Unit) topFrame() *frame {
	if len(u.frames) == 0 {
		return nil
	}
	return u.frames[len(u.frames)-1]
}

// hooksForScope is hooksFor with the scope given rather than taken from the
// frame. A property walk has a scope of its own -- `get_object_vars($this)`
// inside a parent asks as that parent -- and no frame to read it from.
func (u *Unit) hooksForScope(f *frame, object *php.Object, name string,
	scope *Class) (*pb.Property, *Class, bool) {
	if object == nil {
		return nil, nil, false
	}
	class, known := u.findClass(object.ClassName)
	if !known {
		return nil, nil, false
	}
	plain, _, _ := php.UnmangleProperty(name)
	prop, declaring, found := u.hookedProperty(class, name)
	// A scope that declares its own *private* property of this name means that
	// one, whatever a subclass declares -- and a hooked property has no slot to
	// tell them apart by, since a virtual one has no storage at all. So the
	// declaration is chosen the way propertyAccess chooses it, by the scope.
	if scope != nil {
		if own, mine := u.ownPrivateProperty(scope, plain); mine &&
			u.instanceOf(class, scope.Name) {
			if own.GetGetHook() == nil && own.GetSetHook() == nil {
				return nil, nil, false
			}
			prop, declaring, found = own, scope, true
		}
	}
	if !found {
		return nil, nil, false
	}
	// The recursion guard is about the property a hook is running for, and a
	// hook knows its property by the declared name rather than by the key the
	// object holds it under.
	if f != nil && f.hook.name == plain && f.this == object &&
		f.hook.root == strings.ToLower(u.propertyRoot(class, plain)) {
		return nil, nil, false
	}
	return prop, declaring, true
}

// callGetHook runs a property's get hook and returns what it produced.
func (in *Interp) callGetHook(ctx *php.Ctx, unit *Unit, object *php.Object,
	prop *pb.Property, declaring *Class, depth int) (php.Value, error) {
	class, _ := unit.findClass(object.ClassName)
	return in.runHook(ctx, unit, unit.hookIdentityOf(declaring, prop.GetName()),
		prop.GetGetHook(), object, declaring, class, nil, depth)
}

// callSetHook runs a property's set hook with the assigned value.
func (in *Interp) callSetHook(ctx *php.Ctx, unit *Unit, object *php.Object,
	prop *pb.Property, declaring *Class, value php.Value, depth int) error {
	class, _ := unit.findClass(object.ClassName)
	_, err := in.runHook(ctx, unit, unit.hookIdentityOf(declaring, prop.GetName()),
		prop.GetSetHook(), object, declaring, class, []php.Value{value}, depth)
	return err
}

// hookBackingAccess stands in for the trampoline the engine builds when
// `parent::$p::get()` names a property the parent declares without that hook:
// the access goes straight to the storage.
func (u *Unit) hookBackingAccess(call *pendingCall) (php.Value, error) {
	prop := call.hookFallback
	name := prop.GetName()
	// The trampoline has the arity of the hook it stands in for, and names
	// itself the way the hook would: by the class that declared the property.
	owner := prop.GetDeclaringClass()
	if owner == "" && call.class != nil {
		owner = call.class.Name
	}
	if call.hookIsSet {
		if len(call.args) != 1 {
			return php.Undef(), Raise("ArgumentCountError",
				"%s::$%s::set() expects exactly 1 argument, %d given",
				php.DisplayName(owner), name, len(call.args))
		}
		written := call.args[0].Copy()
		call.this.Set(name, written)
		// Writing through the trampoline answers with what was written, which
		// is what `var_dump(parent::$p::set($value))` prints.
		return written.Deref(), nil
	}
	if len(call.args) != 0 {
		return php.Undef(), Raise("ArgumentCountError",
			"%s::$%s::get() expects exactly 0 arguments, %d given",
			php.DisplayName(owner), name, len(call.args))
	}
	value, _ := call.this.Get(name)
	if value.Deref().IsUndef() {
		// Reaching the storage is reaching the property: a typed one that has
		// never been written is an Error rather than a null.
		declared := call.this.Declared[name]
		reported := declared.Class
		if reported == "" {
			reported = call.this.ClassName
		}
		return php.Undef(), Raise("Error",
			"Typed property %s::$%s must not be accessed before initialization",
			php.DisplayName(reported), name)
	}
	return value.Deref(), nil
}

// parentHook is the property a `parent::$p::kind()` call names.
type parentHook struct {
	prop      *pb.Property
	declaring *Class
}

// parentHookTarget resolves the parent's property for a parent hook call. The
// lookup starts from the *scope's* parent, so a hook inherited two levels up
// still reaches the right one.
func (u *Unit) parentHookTarget(in *Interp, ctx *php.Ctx, f *frame, name string) (parentHook, error) {
	if f.class == nil || f.class.Parent == "" {
		return parentHook{}, Raise("Error",
			"Cannot use \"parent\" when current class scope has no parent")
	}
	parent, ok := u.findClass(f.class.Parent)
	if !ok {
		return parentHook{}, Raise("Error",
			"Cannot use \"parent\" when current class scope has no parent")
	}
	for current := parent; current != nil; {
		for _, prop := range u.properties(current) {
			if prop.GetName() != name || prop.GetStatic() {
				continue
			}
			if prop.GetVisibility() == "private" {
				return parentHook{}, Raise("Error", "Cannot access private property %s::$%s",
					php.DisplayName(parent.Name), name)
			}
			return parentHook{prop: prop, declaring: current}, nil
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
	return parentHook{}, Raise("Error", "Undefined property %s::$%s",
		php.DisplayName(parent.Name), name)
}

// hookedCompound implements `$o->p op= x` and the increment forms on a hooked
// property: the value is read through the get hook, combined, and written back
// through the set hook.
func (in *Interp) hookedCompound(ctx *php.Ctx, unit *Unit, f *frame, line *pb.Opline,
	object *php.Object, prop *pb.Property, declaring *Class, operand php.Value, depth int) (php.Value, error) {
	current, err := in.hookedRead(ctx, unit, object, prop, declaring, depth)
	if err != nil {
		return php.Undef(), err
	}
	updated, err := in.binaryOp(ctx, unit, f, line, current, operand)
	if err != nil {
		return php.Undef(), err
	}
	return updated, in.hookedWrite(ctx, unit, object, prop, declaring, updated, depth)
}

// hookedRead reads a hooked property, through the get hook when there is one
// and from the backing store when there is not.
func (in *Interp) hookedRead(ctx *php.Ctx, unit *Unit, object *php.Object,
	prop *pb.Property, declaring *Class, depth int) (php.Value, error) {
	if prop.GetGetHook() == nil {
		if prop.GetVirtual() {
			// A set hook with no get and no backing storage: there is nothing
			// to read and no hook to ask, so reading is refused rather than
			// answered with null.
			return php.Undef(), in.throwError(ctx, unit, "Error",
				"Property %s::$%s is write-only",
				php.DisplayName(object.ClassName), prop.GetName())
		}
		value, _ := object.Get(prop.GetName())
		return value.Deref(), nil
	}
	value, err := in.callGetHook(ctx, unit, object, prop, declaring, depth)
	return value.Deref(), err
}

// hookedRefRead is the write-mode read: a `&get` hook returns a slot rather
// than a value, and that slot is what a write, a `[]` append or a `=&` bind
// reaches through. The reference the hook returned is handed back undisturbed;
// anything else means the property cannot be modified indirectly.
func (in *Interp) hookedRefRead(ctx *php.Ctx, unit *Unit, object *php.Object,
	prop *pb.Property, declaring *Class, depth int) (*php.Ref, bool, error) {
	if !prop.GetGetHook().GetReturnsRef() {
		return nil, false, nil
	}
	value, err := in.callGetHook(ctx, unit, object, prop, declaring, depth)
	if err != nil {
		return nil, false, err
	}
	if ref, ok := value.RefOf(); ok {
		return ref, true, nil
	}
	return nil, false, nil
}

// hookedWrite writes a hooked property, through the set hook when there is one.
func (in *Interp) hookedWrite(ctx *php.Ctx, unit *Unit, object *php.Object,
	prop *pb.Property, declaring *Class, value php.Value, depth int) error {
	if prop.GetSetHook() == nil {
		if prop.GetVirtual() {
			return in.readOnlyHookError(ctx, unit, object, prop.GetName())
		}
		object.Set(prop.GetName(), value)
		return nil
	}
	return in.callSetHook(ctx, unit, object, prop, declaring, value, depth)
}

// readOnlyHookError is what writing to a property with a get hook and no set
// hook produces. PHP words it "read-only" rather than "readonly", which is the
// separate `readonly` modifier's wording.
func (in *Interp) readOnlyHookError(ctx *php.Ctx, unit *Unit, object *php.Object, name string) error {
	// The object's class is named, not the one that declared the hooks: a
	// subclass inheriting them is what the program was holding.
	return in.throwError(ctx, unit, "Error", "Property %s::$%s is read-only",
		php.DisplayName(object.ClassName), name)
}

// hookedPropertyName reports the property a hook body belongs to, taken from
// the compiled name `$prop::get`. Nothing else in the IR records it.
func hookedPropertyName(op *pb.OpArray) (string, bool) {
	name := op.GetName()
	if !strings.HasPrefix(name, "$") {
		return "", false
	}
	property, _, found := strings.Cut(name[1:], "::")
	return property, found
}

// hookedVisibleProps is visibleProps for the places that *observe* an object's
// state -- foreach and get_object_vars -- where a hooked property is read
// through its get hook and a virtual one appears even though it has no slot.
// The raw table is what a (array) cast and ArrayIterator see instead.
// deferredHook is a get hook a property walk has not run yet. A foreach reads
// each one at its own turn, which a hook that prints or throws can tell apart
// from reading them all up front.
type deferredHook struct {
	prop      *pb.Property
	declaring *Class
}

// exportProperties is the resolver var_export needs: the declarations of every
// class in the chain, root first and in each class's own declaration order,
// with a get hook run where there is one and a virtual property included even
// though it has no slot. Names are plain and duplicates are kept -- two classes
// may each declare a private property of one name, and var_export writes both.
func (in *Interp) exportProperties(unit *Unit, depth int) php.ExportProperties {
	return func(ctx *php.Ctx, object *php.Object) ([]php.ExportPair, error) {
		object = lazyState(object)
		if hidesProperties(object) {
			return nil, nil
		}
		class, known := unit.findClass(object.ClassName)
		if !known {
			return php.TableExportProperties(object), nil
		}
		type entry struct {
			key       string
			name      string
			prop      *pb.Property
			declaring *Class
		}
		var order []*entry
		at := map[string]*entry{}
		for _, current := range unit.rootFirst(class) {
			for _, prop := range unit.ownDeclarations(current) {
				if prop.GetStatic() {
					continue
				}
				// Keyed by *slot*, not by name: an override of a public
				// property is the same slot and so one entry, while two
				// classes' private properties of one name are two. A virtual
				// property has no slot and is keyed by its name.
				key := slotKeyFor(object, prop, current)
				if prop.GetVirtual() {
					key = "\x00virtual\x00" + strings.ToLower(prop.GetName())
				}
				if held, seen := at[key]; seen {
					// The most derived declaration answers, in the position the
					// name first had.
					held.prop, held.declaring = prop, current
					continue
				}
				made := &entry{key: key, name: prop.GetName(), prop: prop, declaring: current}
				at[key] = made
				order = append(order, made)
			}
		}
		var out []php.ExportPair
		taken := map[string]bool{}
		for _, held := range order {
			if hook := held.prop.GetGetHook(); hook != nil {
				value, err := in.hookedRead(ctx, unit, object, held.prop, held.declaring, depth)
				if err != nil {
					// A hook that throws is the caller's business; there is
					// nothing to write for this entry.
					continue
				}
				if !held.prop.GetVirtual() {
					taken[held.key] = true
				}
				out = append(out, php.ExportPair{Name: held.name, Value: value})
				continue
			}
			if held.prop.GetVirtual() {
				continue
			}
			slot, present := object.Props.Get(php.StringKey(held.key))
			if !present || slot.Deref().IsUndef() {
				continue
			}
			taken[held.key] = true
			out = append(out, php.ExportPair{Name: held.name, Value: slot.Deref()})
		}
		// Whatever the class never declared -- a dynamic property -- keeps its
		// place at the end of the table.
		object.Props.Each(func(k php.Key, item php.Value) bool {
			if taken[k.String()] || item.Deref().IsUndef() {
				return true
			}
			name := k.String()
			if plain, _, mangled := php.UnmangleProperty(name); mangled {
				name = plain
			}
			out = append(out, php.ExportPair{Name: name, Value: item.Deref()})
			return true
		})
		return out, nil
	}
}

// slotKeyFor is the key an object's table holds one declaration under, which is
// the mangled one when a private property shares its name with something else.
func slotKeyFor(object *php.Object, prop *pb.Property, declaring *Class) string {
	if prop.GetVisibility() != "private" || object.Shadowed == nil ||
		!object.Shadowed[prop.GetName()] {
		return prop.GetName()
	}
	owner := prop.GetDeclaringClass()
	if owner == "" {
		owner = declaring.Name
	}
	return php.MangleProperty(php.DisplayName(owner), prop.GetName())
}

// rootFirst lists a class and its ancestors with the root first, which is the
// order declarations are laid out in.
func (u *Unit) rootFirst(class *Class) []*Class {
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
	return chain
}

// ownDeclarations is what a class declared itself -- its own properties and its
// traits' -- in declaration order, rather than the whole inherited table.
func (u *Unit) ownDeclarations(class *Class) []*pb.Property {
	own := make([]*pb.Property, 0, 8)
	for _, prop := range u.properties(class) {
		owner := prop.GetDeclaringClass()
		if owner != "" && !strings.EqualFold(owner, class.Name) {
			if _, known := u.findClass(owner); known && !u.isTraitOf(class, owner) {
				continue
			}
		}
		own = append(own, prop)
	}
	sort.SliceStable(own, func(i, j int) bool {
		return own[i].GetOrder() < own[j].GetOrder()
	})
	return own
}

// declarationOrdered is a class's properties in the order the engine's
// properties_info holds them, which is what the *hooked* enumerations follow.
// The table itself travels sorted by slot offset -- what var_dump and a plain
// object's foreach expose -- and a hooked property that overrides an inherited
// one does not keep the slot it overrides, so the two orders differ.
func (u *Unit) declarationOrdered(class *Class) []*pb.Property {
	at := map[string]int{}
	var out []*pb.Property
	for _, current := range u.rootFirst(class) {
		for _, prop := range u.ownDeclarations(current) {
			if where, seen := at[strings.ToLower(prop.GetName())]; seen {
				// An override keeps the position the name first had, which is
				// why a subclass that only redeclares changes nothing about the
				// order and one that adds a name appends.
				out[where] = prop
				continue
			}
			at[strings.ToLower(prop.GetName())] = len(out)
			out = append(out, prop)
		}
	}
	return out
}

// isTraitOf reports whether a name is one of the traits a class uses, which is
// what makes a property the class's own even though the declaration names
// something else.
func (u *Unit) isTraitOf(class *Class, name string) bool {
	for _, used := range class.decl.GetTraits() {
		if strings.EqualFold(used, name) {
			return true
		}
	}
	return false
}

// lazyVisibleProps is hookedVisibleProps for a walk that must not run the hooks
// yet: the array carries a placeholder for each hooked name, and the hooks come
// back separately for the walk to run as it reaches them.
func (in *Interp) lazyVisibleProps(unit *Unit, f *frame, object *php.Object,
	scope *Class) (*php.Array, map[string]deferredHook) {
	class, known := unit.findClass(object.ClassName)
	if !known || !unit.hasHooks(class) {
		return nil, nil
	}
	visible := php.NewArray()
	deferred := map[string]deferredHook{}
	declared := map[string]bool{}
	for _, prop := range unit.declarationOrdered(class) {
		name := prop.GetName()
		if prop.GetStatic() || declared[name] {
			continue
		}
		declared[name] = true
		// A private property a dynamic one has taken the name of lives under
		// its own key. Both are marked handled: the scope that owns the private
		// sees that, and nothing sees the name twice.
		slot := propertySlot(object, name, scope)
		declared[slot] = true
		// propertyAccess, not propVisible: the two differ when the *scope*
		// declares its own private property of the name. propVisible answers
		// about the declaration the subclass's table holds, which is the
		// subclass's own private one and invisible from the parent -- so a
		// parent walking `$this` lost its own property.
		if _, verdict := unit.propertyAccess(object, name, scope); verdict != accessAllowed {
			continue
		}
		hooked, declaring, isHooked := unit.hooksForScope(f, object, name, scope)
		// A private property of the reading scope is a *different* property
		// from a subclass's of the same name, and it is the one this scope
		// sees -- hooks and all.
		if own, found := unit.ownPrivateProperty(scope, name); found {
			hooked, declaring, isHooked = own, scope, own.GetGetHook() != nil ||
				own.GetSetHook() != nil
		}
		if isHooked && hooked.GetGetHook() == nil {
			isHooked = false
			if hooked.GetVirtual() {
				continue
			}
		}
		if !isHooked {
			if held, present := object.Props.Get(php.StringKey(slot)); present &&
				!held.Deref().IsUndef() {
				visible.Set(php.StringKey(name), held)
			}
			continue
		}
		deferred[name] = deferredHook{prop: hooked, declaring: declaring}
		visible.Set(php.StringKey(name), php.Null())
	}
	object.Props.Each(func(k php.Key, held php.Value) bool {
		if held.Deref().IsUndef() {
			return true
		}
		if _, already := visible.Get(k); already {
			return true
		}
		if declared[k.String()] && !object.Dynamic[k.String()] {
			// The loop above answered for the class's own declarations. A
			// *dynamic* property of a declared name is a second entry, and the
			// loop above only answered for it when the scope could see the
			// declared one.
			return true
		}
		if _, _, mangled := php.UnmangleProperty(k.String()); mangled {
			// A declared property's own key. The loop above answered for every
			// one the class knows; anything left here belongs to a scope that
			// cannot see it.
			if _, isDeclared := object.Declared[k.String()]; isDeclared {
				return true
			}
		}
		if object.Dynamic[k.String()] || unit.propVisible(class, scope, k.String()) {
			visible.Set(k, held)
		}
		return true
	})
	return visible, deferred
}

func (in *Interp) hookedVisibleProps(ctx *php.Ctx, unit *Unit, f *frame,
	object *php.Object, scope *Class, depth int, unmangle bool) (*php.Array, error) {
	class, known := unit.findClass(object.ClassName)
	if !known || !unit.hasHooks(class) {
		return unit.visibleProps(object, scope, unmangle), nil
	}
	visible := php.NewArray()
	declared := map[string]bool{}
	for _, prop := range unit.declarationOrdered(class) {
		name := prop.GetName()
		if prop.GetStatic() || declared[name] {
			continue
		}
		declared[name] = true
		// A private property a dynamic one has taken the name of lives under
		// its own key. Both are marked handled: the scope that owns the private
		// sees that, and nothing sees the name twice.
		slot := propertySlot(object, name, scope)
		declared[slot] = true
		// propertyAccess, not propVisible: the two differ when the *scope*
		// declares its own private property of the name. propVisible answers
		// about the declaration the subclass's table holds, which is the
		// subclass's own private one and invisible from the parent -- so a
		// parent walking `$this` lost its own property.
		if _, verdict := unit.propertyAccess(object, name, scope); verdict != accessAllowed {
			continue
		}
		hooked, declaring, isHooked := unit.hooksForScope(f, object, name, scope)
		if isHooked && hooked.GetGetHook() == nil {
			// A property with only a set hook has nothing to read. If it has
			// backing storage the raw value is what shows, and if it is
			// virtual there is nothing at all.
			isHooked = false
			if hooked.GetVirtual() {
				continue
			}
		}
		if !isHooked {
			if held, present := object.Props.Get(php.StringKey(slot)); present &&
				!held.Deref().IsUndef() {
				visible.Set(php.StringKey(name), held)
			}
			continue
		}
		value, err := in.hookedRead(ctx, unit, object, hooked, declaring, depth)
		if err != nil {
			return nil, err
		}
		visible.Set(php.StringKey(name), value)
	}
	// Whatever the class never declared keeps its place at the end.
	var failed error
	object.Props.Each(func(k php.Key, held php.Value) bool {
		if held.Deref().IsUndef() {
			return true
		}
		if _, already := visible.Get(k); already {
			return true
		}
		if declared[k.String()] && !object.Dynamic[k.String()] {
			// The loop above answered for the class's own declarations. A
			// *dynamic* property of a declared name is a second entry, and the
			// loop above only answered for it when the scope could see the
			// declared one.
			return true
		}
		if _, _, mangled := php.UnmangleProperty(k.String()); mangled {
			if _, isDeclared := object.Declared[k.String()]; isDeclared {
				// A declared property's own key. The loop above answered for
				// every one the class knows; anything left belongs to a scope
				// that cannot see it.
				return true
			}
		}
		if known && !object.Dynamic[k.String()] &&
			!unit.propVisible(class, scope, k.String()) {
			// A property the object grew at run time is public, whatever the
			// class declares under that name.
			return true
		}
		if plain, _, mangled := php.UnmangleProperty(k.String()); mangled && unmangle {
			// foreach yields such a key under its plain name; get_object_vars
			// hands back the table as it stands, mangling and all.
			visible.Set(php.StringKey(plain), held)
			return true
		}
		visible.Set(k, held)
		return true
	})
	return visible, failed
}

// hasHooks reports whether any of a class's properties has one, which is what
// decides whether reading its state has to run code.
func (u *Unit) hasHooks(class *Class) bool {
	for _, prop := range u.properties(class) {
		if prop.GetGetHook() != nil || prop.GetSetHook() != nil || prop.GetVirtual() {
			return true
		}
	}
	return false
}
