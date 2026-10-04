package interp

import (
	"sort"
	"strings"

	pb "github.com/moriyoshi/php2go/internal/ir/irpb"
	"github.com/moriyoshi/php2go/runtime/php"
)

// A concrete class must implement everything abstract it inherits, and the
// engine checks that when the declaration is linked -- which for php2go is when
// ZEND_DECLARE_CLASS runs. The harvest cannot carry the verdict because a class
// with an interface or an unresolved parent is not linked at compile time.

// maxAbstractNames is how many the engine lists before writing ", ...".
const maxAbstractNames = 3

// bindDeclaration records that a class declaration has executed, and reports
// the fatal error a second one earns.
func (in *Interp) bindDeclaration(ctx *php.Ctx, unit *Unit, class *Class) error {
	key := strings.ToLower(class.Name)
	if unit.bound == nil {
		unit.bound = map[string]int{}
	}
	// A name the engine already owns is taken before the program starts, and
	// there is no earlier declaration site to name.
	if builtin, taken := unit.builtins[key]; taken {
		return in.fatal(ctx, "Cannot redeclare %s %s",
			classKindWord(builtin.decl.GetKind()), php.DisplayName(builtin.Name))
	}
	// An unconditional declaration is bound when the file compiles and has no
	// opline of its own, so a name the harvest saw twice arrives here already
	// taken. One the harvest saw once is free until its opline runs.
	// A declaration the compiler bound itself has no opline, so it is taken
	// before any of these run. One declared only by oplines is free until an
	// opline runs, which is what lets a program retry a declaration whose first
	// attempt failed to link.
	unit.deferredClasses(in)
	already := unit.bound[key] > 0 || unit.declaredCount[key] > unit.deferredCount[key]
	unit.bound[key]++
	if !already {
		return nil
	}
	first := unit.firstDeclaration[key]
	if first == nil {
		first = class.decl
	}
	return in.fatal(ctx, "Cannot redeclare %s %s (previously declared in %s:%d)",
		classKindWord(first.GetKind()), php.DisplayName(class.Name),
		first.GetFilename(), first.GetLineStart())
}

// unbindDeclaration takes a name back after the declaration that claimed it
// failed to link. PHP unlinks such a class entirely, which is what lets a
// program retry the same declaration -- the two attempts in
// `type_declarations/variance/loading_exception1` are exactly that.
func (u *Unit) unbindDeclaration(class *Class) {
	key := strings.ToLower(class.Name)
	if u.bound[key] > 0 {
		u.bound[key]--
	}
}

// classKindWord names a declaration the way a diagnostic about it does.
func classKindWord(kind pb.Class_Kind) string {
	switch kind {
	case pb.Class_KIND_INTERFACE:
		return "interface"
	case pb.Class_KIND_TRAIT:
		return "trait"
	case pb.Class_KIND_ENUM:
		return "enum"
	}
	return "class"
}

// verifyAbstract reports the fatal error a concrete class earns by leaving
// abstract methods unimplemented.
func (in *Interp) verifyAbstract(ctx *php.Ctx, unit *Unit, class *Class) error {
	kind := class.decl.GetKind()
	if kind == pb.Class_KIND_INTERFACE || kind == pb.Class_KIND_TRAIT {
		return nil
	}
	explicitlyAbstract := class.decl.GetAbstract()

	var missing []string
	seen := map[string]bool{}
	report := func(scope, name string) {
		key := strings.ToLower(name)
		if seen[key] {
			return
		}
		seen[key] = true
		missing = append(missing, php.DisplayName(scope)+"::"+name)
	}

	usedTraits := map[string]bool{}
	for _, name := range class.decl.GetTraits() {
		usedTraits[strings.ToLower(name)] = true
	}
	for _, current := range unit.abstractSources(class) {
		fromTrait := usedTraits[strings.ToLower(current.Name)]
		for _, method := range current.decl.GetMethods() {
			if !method.GetAbstract() {
				continue
			}
			// An explicitly abstract class only has to declare its own private
			// abstract methods; everything else is its subclasses' problem.
			if explicitlyAbstract && method.GetVisibility() != "private" {
				continue
			}
			if fromTrait {
				// A trait's members are flattened into the using class, so a
				// declaration the class makes itself replaces the trait's
				// requirement -- and the diagnostic names the class, not the
				// trait it came from.
				if _, own := class.methods[strings.ToLower(method.GetName())]; own {
					continue
				}
			}
			// The implementation can be anywhere in the hierarchy, and the
			// nearest *declaration* is not necessarily it: a trait's abstract
			// method ranks above the parent's concrete one in the lookup, but
			// the parent's still satisfies it.
			if unit.concreteMethod(class, method.GetName()) {
				continue
			}
			owner := current.Name
			if fromTrait {
				owner = class.Name
			}
			report(owner, method.GetName())
		}
		if fromTrait && !explicitlyAbstract {
			// `use T { T::m as other; }` adds a *name*, and when T::m is
			// abstract the requirement comes with it: the class has to
			// implement `other` as well, and the diagnostic names it on the
			// class rather than on the trait.
			for _, alias := range class.decl.GetTraitAliases() {
				if alias.GetAlias() == "" {
					continue
				}
				if source, ok := unit.aliasSource(class, alias); !ok || source != current {
					continue
				}
				method, found := current.methods[strings.ToLower(alias.GetMethod())]
				if !found || !method.GetAbstract() {
					continue
				}
				if _, own := class.methods[strings.ToLower(alias.GetAlias())]; own {
					continue
				}
				if unit.concreteMethod(class, alias.GetAlias()) {
					continue
				}
				report(class.Name, alias.GetAlias())
			}
		}
		if explicitlyAbstract {
			continue
		}
		// An interface may require hooks rather than methods, and those are
		// abstract the same way. They are named after the hook, not the
		// property: `I::$prop::get`.
		for _, prop := range unit.properties(current) {
			for _, hook := range []*pb.OpArray{prop.GetGetHook(), prop.GetSetHook()} {
				if hook == nil || !hook.GetAbstract() {
					continue
				}
				// A plain declared property satisfies a hook requirement: the
				// interface asks that reading or writing work, not that a hook
				// exist. Only a property that is itself abstract fails.
				if declared, ok := unit.declaredProperty(class, prop.GetName()); ok {
					body := declared.GetGetHook()
					if strings.HasSuffix(hook.GetName(), "::set") {
						body = declared.GetSetHook()
					}
					if body == nil || !body.GetAbstract() {
						continue
					}
				}
				report(current.Name, hook.GetName())
			}
		}
	}
	if len(missing) == 0 {
		return nil
	}

	plural := "s"
	if len(missing) == 1 {
		plural = ""
	}
	listed := missing
	suffix := ""
	if len(listed) > maxAbstractNames {
		listed, suffix = listed[:maxAbstractNames], ", ..."
	}
	kindWord := "Class"
	if class.decl.GetKind() == pb.Class_KIND_ENUM {
		kindWord = "Enum"
	}
	if explicitlyAbstract || class.decl.GetKind() == pb.Class_KIND_ENUM {
		// Something that cannot be made abstract -- an enum, or a class that
		// already is -- is not offered the choice, and the engine calls what
		// remains "abstract private" because that is all it can be.
		return in.fatal(ctx, "%s %s must implement %d abstract private method%s (%s%s)",
			kindWord, php.DisplayName(class.Name), len(missing), plural,
			strings.Join(listed, ", "), suffix)
	}
	return in.fatal(ctx, "%s %s contains %d abstract method%s and must therefore be declared abstract or implement the remaining methods (%s%s)",
		kindWord, php.DisplayName(class.Name), len(missing), plural,
		strings.Join(listed, ", "), suffix)
}

// concreteMethod reports whether any class in the hierarchy provides a body for
// a method, as opposed to another abstract declaration of it.
func (u *Unit) concreteMethod(class *Class, name string) bool {
	lower := strings.ToLower(name)
	for _, source := range u.abstractSources(class) {
		if method, ok := source.methods[lower]; ok && !method.GetAbstract() {
			return true
		}
		// An ancestor may have got its implementation from a trait of its own.
		// Flattening puts that method in the ancestor's table, but this class's
		// source list holds only its *own* traits, so the ancestor's have to be
		// looked at directly.
		for _, name := range source.decl.GetTraits() {
			trait, ok := u.findClass(name)
			if !ok {
				continue
			}
			if method, ok := trait.methods[lower]; ok && !method.GetAbstract() {
				return true
			}
		}
	}
	return false
}

// declaredProperty finds a property declared anywhere in the hierarchy.
func (u *Unit) declaredProperty(class *Class, name string) (*pb.Property, bool) {
	for current := class; current != nil; {
		for _, prop := range u.properties(current) {
			if prop.GetName() == name && !prop.GetStatic() {
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

// reflectableProperty is declaredProperty for reflection, which describes
// static properties too: `new ReflectionProperty("C", "s")` answers for one,
// while an instance lookup must never see it.
func (u *Unit) reflectableProperty(class *Class, name string) (*pb.Property, bool) {
	for _, prop := range u.reflectableProperties(class) {
		if prop.GetName() == name {
			return prop, true
		}
	}
	return nil, false
}

// reflectableProperties lists a class's properties the way ReflectionClass
// sees them, which is not how the object's table is laid out.
//
//   - An ancestor's *private* property is not there at all. Every one of
//     hasProperty, getProperty and getProperties applies the same test,
//     `(pptr->flags & ZEND_ACC_PRIVATE) && pptr->ce != ce`, so a subclass
//     cannot reach what it was never able to see.
//   - The order is the class's own declarations first, then the nearest
//     parent's, then further up. The engine iterates `ce->properties_info`,
//     and inheritance appends the parent's entries *after* the class's own
//     were compiled; the property table travels the other way, by slot
//     offset, with the base class's slots first.
//
// A trait's contribution counts as the using class's own, and lands after what
// the class body declared, because the binding runs at link time.
func (u *Unit) reflectableProperties(class *Class) []*pb.Property {
	levels := map[string]bool{}
	for current := class; current != nil; {
		levels[strings.ToLower(current.Name)] = true
		if current.Parent == "" {
			break
		}
		next, ok := u.findClass(current.Parent)
		if !ok {
			break
		}
		current = next
	}
	var out []*pb.Property
	seen := map[string]bool{}
	for current := class; current != nil; {
		// Within one class the order is *declaration* order, which is what the
		// engine's properties_info holds. The table travels sorted by slot
		// offset instead -- what var_dump and foreach expose -- and a static
		// property has no slot among the instance ones, so it would otherwise
		// land at the end rather than where it was written.
		level := append([]*pb.Property(nil), u.properties(current)...)
		sort.SliceStable(level, func(i, j int) bool {
			return level[i].GetOrder() < level[j].GetOrder()
		})
		for _, prop := range level {
			owner := strings.ToLower(prop.GetDeclaringClass())
			if owner != "" && levels[owner] && owner != strings.ToLower(current.Name) {
				// Declared further up: it belongs to that level's turn, not
				// to this one. A name the harvest attributes to a trait is not
				// a level, so it stays here.
				continue
			}
			if current != class && prop.GetVisibility() == "private" {
				continue
			}
			if seen[prop.GetName()] {
				continue
			}
			seen[prop.GetName()] = true
			out = append(out, prop)
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
	return out
}

// abstractSources lists where an abstract declaration can come from, in the
// order the engine's function table ends up holding them: the class itself,
// then its ancestors nearest first, then the interfaces, then the traits.
func (u *Unit) abstractSources(class *Class) []*Class {
	var sources []*Class
	seen := map[string]bool{}
	// The dedupe result is the recursion guard as well: an interface graph
	// that names itself would otherwise walk forever.
	add := func(c *Class) bool {
		if c == nil || seen[strings.ToLower(c.Name)] {
			return false
		}
		seen[strings.ToLower(c.Name)] = true
		sources = append(sources, c)
		return true
	}
	add(class)
	for current := class; current != nil && current.Parent != ""; {
		next, ok := u.findClass(current.Parent)
		if !ok || !add(next) {
			break
		}
		current = next
	}
	// Interfaces come after the inheritance chain, and an interface's own
	// parents count too.
	var addInterfaces func(c *Class)
	addInterfaces = func(c *Class) {
		if c == nil {
			return
		}
		for _, name := range c.decl.GetInterfaces() {
			iface, ok := u.findClass(name)
			if !ok || !add(iface) {
				continue
			}
			addInterfaces(iface)
		}
	}
	for _, current := range append([]*Class(nil), sources...) {
		addInterfaces(current)
	}
	for _, name := range class.decl.GetTraits() {
		if trait, ok := u.findClass(name); ok {
			add(trait)
		}
	}
	return sources
}

// classDeclared reports whether a class is available to the program *now*, as
// opposed to merely being present in the harvest. A class the compiler could
// bind early exists from the start; one that needs a ZEND_DECLARE_CLASS does
// not exist until that opline runs, even though the harvest carries it. That is
// the difference class_exists() and its siblings ask about.
func (u *Unit) classDeclared(in *Interp, name string) bool {
	key := strings.ToLower(strings.TrimPrefix(name, "\\"))
	if _, known := u.classes[key]; !known {
		// A builtin, or nothing at all; either way there is no opline to wait
		// for.
		return true
	}
	if u.bound[key] > 0 {
		return true
	}
	return !u.deferredClasses(in)[key]
}

// classAvailable reports whether a class can be linked against right now: it
// has to exist at all and to have been declared. classDeclared answers only the
// second half -- a name it has never heard of is "not waiting for an opline",
// which is the right answer for class_exists and the wrong one for linking.
func (u *Unit) classAvailable(in *Interp, name string) bool {
	if _, ok := u.findClass(name); !ok {
		return false
	}
	if u.linking[strings.ToLower(strings.TrimPrefix(name, "\\"))] {
		// Its own declaration has not finished, so there is nothing to link
		// against yet.
		return false
	}
	return u.classDeclared(in, name)
}

// deferredClasses names every class the file declares with an opline rather
// than at compile time, computed once by scanning the oplines for it.
func (u *Unit) deferredClasses(in *Interp) map[string]bool {
	if u.deferred != nil {
		return u.deferred
	}
	u.deferred = map[string]bool{}
	if u.declareOpcodes == nil {
		// Opcode numbers move between releases, so the two that declare a class
		// are resolved through the manifest's table rather than written down.
		u.declareOpcodes = map[uint32]bool{}
		for opcode, name := range in.opcodeNames {
			if name == "ZEND_DECLARE_CLASS" || name == "ZEND_DECLARE_CLASS_DELAYED" {
				u.declareOpcodes[uint32(opcode)] = true
			}
		}
	}
	var scan func(op *pb.OpArray)
	seen := map[*pb.OpArray]bool{}
	scan = func(op *pb.OpArray) {
		if op == nil || seen[op] {
			return
		}
		seen[op] = true
		literals := op.GetLiterals()
		for _, line := range op.GetOplines() {
			if !u.declareOpcodes[line.GetOpcode()] {
				continue
			}
			if line.GetOp1Type() == opConst && int(line.GetOp1()) < len(literals) {
				lit := literals[line.GetOp1()]
				if lit.GetKind() == pb.Literal_KIND_STRING {
					key := strings.ToLower(lit.GetStringValue())
					u.deferred[key] = true
					if u.deferredCount == nil {
						u.deferredCount = map[string]int{}
					}
					u.deferredCount[key]++
				}
			}
		}
		for _, nested := range op.GetClosures() {
			scan(nested)
		}
	}
	scan(u.file)
	for _, fn := range u.functions {
		scan(fn)
	}
	for _, class := range u.classes {
		for _, method := range class.decl.GetMethods() {
			scan(method)
		}
		for _, prop := range u.properties(class) {
			scan(prop.GetGetHook())
			scan(prop.GetSetHook())
		}
	}
	return u.deferred
}

// enumForbiddenMagic lists the magic methods an enum may not declare, in the
// order the engine checks them and spelled the way it spells them. Only
// __call, __callStatic and __invoke are allowed.
var enumForbiddenMagic = []string{
	"__construct", "__destruct", "__clone",
	"__get", "__set", "__unset", "__isset",
	"__toString", "__debugInfo", "__serialize", "__unserialize",
	"__sleep", "__wakeup", "__set_state",
}

// verifyEnum reports the fatal error an enum earns by declaring a magic method
// that makes no sense for a value with no state and no identity to construct.
// The engine calls this while linking, which is why it is here rather than
// falling out of the harvest.
func (in *Interp) verifyEnum(ctx *php.Ctx, unit *Unit, class *Class) error {
	if class.decl.GetKind() != pb.Class_KIND_ENUM {
		return nil
	}
	// A trait's members are flattened into the enum, so a magic method or a
	// property it contributes is the enum's own as far as this check goes.
	declares := func(method string) bool {
		if _, ok := class.methods[strings.ToLower(method)]; ok {
			return true
		}
		for _, name := range class.decl.GetTraits() {
			trait, ok := unit.findClass(name)
			if !ok {
				continue
			}
			if _, ok := trait.methods[strings.ToLower(method)]; ok {
				return true
			}
		}
		return false
	}
	for _, magic := range enumForbiddenMagic {
		if declares(magic) {
			return in.fatal(ctx, "Enum %s cannot include magic method %s",
				php.DisplayName(class.Name), magic)
		}
	}
	// An enum case is a singleton the engine builds; letting Serializable
	// rebuild one from a string would make a second. The refusal reaches an
	// enum that implements it *indirectly*, through an interface that extends
	// it, and comes after the deprecation the interface itself earns.
	if unit.instanceOf(class, "Serializable") {
		return in.fatal(ctx, "Enum %s cannot implement the Serializable interface",
			php.DisplayName(class.Name))
	}
	// An enum case has no state, so there is nowhere for a property to live.
	// The engine gives every case `name`, and a backed one `value`; the harvest
	// carries those like any other, and neither is something the source could
	// have written -- both names are reserved.
	for _, prop := range unit.properties(class) {
		if prop.GetStatic() || prop.GetName() == "name" || prop.GetName() == "value" {
			continue
		}
		return in.fatal(ctx, "Enum %s cannot include properties", php.DisplayName(class.Name))
	}
	// cases(), from() and tryFrom() are the engine's, and an enum may not
	// supply its own.
	for _, reserved := range []string{"cases", "from", "tryFrom"} {
		if _, ok := class.methods[strings.ToLower(reserved)]; ok {
			return in.fatal(ctx, "Cannot redeclare %s::%s()",
				php.DisplayName(class.Name), reserved)
		}
	}
	for _, iface := range class.decl.GetInterfaces() {
		switch strings.ToLower(strings.TrimPrefix(iface, "\\")) {
		// UnitEnum cannot be checked here: the engine adds it to *every* enum,
		// and the harvest carries the linked list rather than the one the
		// source wrote, so refusing on it would refuse every enum. BackedEnum
		// can: the engine adds it only to a backed enum, so finding it on one
		// with no backing type means the source asked for it.
		case "backedenum":
			if class.decl.GetEnumBackingType() == "" {
				return in.fatal(ctx, "Non-backed enum %s cannot implement interface BackedEnum",
					php.DisplayName(class.Name))
			}
		case "throwable":
			return in.fatal(ctx, "Enum %s cannot implement interface Throwable",
				php.DisplayName(class.Name))
		case "serializable":
			// An enum case has no state to serialize, and the interface is
			// deprecated besides -- the notice comes first, from the ordinary
			// check every class faces.
			return in.fatal(ctx, "Enum %s cannot implement the Serializable interface",
				php.DisplayName(class.Name))
		}
	}
	return nil
}

// visibilityRank orders the three access levels, weakest first, so a comparison
// can say which of two declarations is more restrictive.
func visibilityRank(visibility string) int {
	switch visibility {
	case "private":
		return 2
	case "protected":
		return 1
	}
	return 0
}

// verifyAccessLevels reports a member that an ancestor declares more openly
// than this declaration does. An override may widen access and never narrow it.
func (in *Interp) verifyAccessLevels(ctx *php.Ctx, unit *Unit, class *Class) error {
	if class.decl.GetKind() == pb.Class_KIND_TRAIT {
		return nil
	}
	// The wording says "class" whatever the ancestor really is, and adds
	// "or weaker" unless the ancestor's level is public.
	weaker := func(visibility string) string {
		if visibility == "public" || visibility == "" {
			return ""
		}
		return " or weaker"
	}
	for _, source := range unit.abstractSources(class) {
		if source == class || source.decl.GetKind() == pb.Class_KIND_TRAIT {
			continue
		}
		for _, constant := range unit.constants(source) {
			own, owner, found := unit.findClassConstant(class, constant.GetName())
			if !found || owner != class {
				continue
			}
			if visibilityRank(own.GetVisibility()) > visibilityRank(constant.GetVisibility()) {
				level := constant.GetVisibility()
				if level == "" {
					level = "public"
				}
				return in.fatal(ctx, "Access level to %s::%s must be %s (as in %s %s)%s",
					php.DisplayName(class.Name), constant.GetName(), level,
					strings.ToLower(declarationWord(source)), php.DisplayName(source.Name),
					weaker(level))
			}
		}
		for name, method := range source.methods {
			if method == nil {
				continue
			}
			own, declaring, found := unit.resolveMethod(class, name)
			if !found || own == nil || declaring != class && own == method {
				continue
			}
			if visibilityRank(own.GetVisibility()) > visibilityRank(method.GetVisibility()) {
				level := method.GetVisibility()
				if level == "" {
					level = "public"
				}
				return in.fatal(ctx, "Access level to %s::%s() must be %s (as in class %s)%s",
					php.DisplayName(class.Name), own.GetName(), level,
					php.DisplayName(source.Name), weaker(level))
			}
		}
	}
	return nil
}

// verifyConstantTypes checks a redeclared constant's type against the type it
// overrides. Probed in all four directions: the redeclaration may *narrow* the
// type -- `?B1` to `?B2` where B2 extends B1, or `int|string` to `int` -- and
// may add one where the overridden constant had none, but may not widen it and
// may not drop it. Declaring nothing counts as `mixed`, which is why
// redeclaring a typed constant untyped is refused.
func (in *Interp) verifyConstantTypes(ctx *php.Ctx, unit *Unit, class *Class) error {
	for _, constant := range unit.constants(class) {
		if constant.GetIsCase() {
			continue
		}
		owner, overridden, found := unit.overriddenConstant(class, constant.GetName())
		if !found || overridden.GetType() == "" {
			continue
		}
		// A class named in either type is resolved -- which autoloads, and the
		// corpus can see that it does. Unlike a method's variance check, an
		// unresolved name is not deferred here: the engine has no obligation
		// machinery for constants, so a class that stays missing makes the
		// types incompatible rather than the question undecidable.
		accepts, _, _ := in.typeAccepts(ctx, unit, overridden.GetType(), owner,
			constant.GetType(), class)
		if !accepts {
			return in.fatal(ctx, "Type of %s::%s must be compatible with %s::%s of type %s",
				php.DisplayName(class.Name), constant.GetName(),
				php.DisplayName(owner.Name), constant.GetName(), overridden.GetType())
		}
	}
	return nil
}

// overriddenConstant finds the declaration a class's own constant overrides:
// the nearest one in the parent chain, or failing that one an interface
// declares. It answers about *another* class's declaration, never the class's
// own.
func (u *Unit) overriddenConstant(class *Class, name string) (*Class, *pb.ClassConstant, bool) {
	look := func(owner *Class) (*pb.ClassConstant, bool) {
		for _, constant := range u.constants(owner) {
			if constant.GetName() == name {
				return constant, true
			}
		}
		return nil, false
	}
	for current := class; current.Parent != ""; {
		parent, ok := u.findClass(current.Parent)
		if !ok {
			break
		}
		if constant, held := look(parent); held {
			return parent, constant, true
		}
		current = parent
	}
	for _, iface := range u.implementedInterfaces(class) {
		if iface == class {
			continue
		}
		if constant, held := look(iface); held {
			return iface, constant, true
		}
	}
	return nil, nil, false
}

// verifyConstantInheritance reports the two things that can go wrong when a
// declaration takes a constant from somewhere else: taking one that was marked
// final, and taking the same name from two different places.
//
// The rule for the second is not "two interfaces". A parent class's constant
// counts as an origin too -- `class C2 extends C implements I` where both
// declare `C` is ambiguous -- but the class's *own* declaration does not, which
// is what makes overriding an interface constant legal since PHP 8.1.
func (in *Interp) verifyConstantInheritance(ctx *php.Ctx, unit *Unit, class *Class) error {
	if class.decl.GetKind() == pb.Class_KIND_TRAIT {
		return nil
	}
	if err := in.verifyConstantTypes(ctx, unit, class); err != nil {
		return err
	}
	// Where each name has come from so far, in the order the engine builds the
	// table: the class's own declarations, then the parent chain, then the
	// interfaces.
	declaring := map[string]*Class{}
	record := func(owner *Class) {
		for _, constant := range unit.constants(owner) {
			if _, taken := declaring[constant.GetName()]; !taken {
				declaring[constant.GetName()] = owner
			}
		}
	}
	// The class's own declarations, told from what it inherited by the class
	// each constant records -- the table it is read from carries both.
	own := map[string]bool{}
	for _, constant := range unit.constants(class) {
		if declaring := constant.GetDeclaringClass(); declaring == "" ||
			strings.EqualFold(declaring, class.Name) {
			own[constant.GetName()] = true
		}
	}
	record(class)
	for current := class; current.Parent != ""; {
		parent, ok := unit.findClass(current.Parent)
		if !ok {
			break
		}
		for _, constant := range unit.constants(parent) {
			if !constant.GetFinal() || !own[constant.GetName()] {
				continue
			}
			owner := constant.GetDeclaringClass()
			if owner == "" {
				owner = parent.Name
			}
			return in.fatal(ctx, "%s::%s cannot override final constant %s::%s",
				php.DisplayName(class.Name), constant.GetName(),
				php.DisplayName(owner), constant.GetName())
		}
		record(parent)
		current = parent
	}
	for _, iface := range unit.implementedInterfaces(class) {
		for _, constant := range unit.constants(iface) {
			owner, taken := declaring[constant.GetName()]
			if !taken {
				declaring[constant.GetName()] = iface
				continue
			}
			if constant.GetFinal() {
				return in.fatal(ctx, "%s::%s cannot override final constant %s::%s",
					php.DisplayName(class.Name), constant.GetName(),
					php.DisplayName(iface.Name), constant.GetName())
			}
			if owner != iface && owner != class {
				return in.fatal(ctx, "%s %s inherits both %s::%s and %s::%s, which is ambiguous",
					declarationWord(class), php.DisplayName(class.Name),
					php.DisplayName(owner.Name), constant.GetName(),
					php.DisplayName(iface.Name), constant.GetName())
			}
		}
	}
	return nil
}

// implementedInterfaces lists the interfaces a declaration brings in, each
// before the ones it extends, with duplicates removed.
func (u *Unit) implementedInterfaces(class *Class) []*Class {
	var out []*Class
	seen := map[string]bool{}
	var walk func(*Class)
	walk = func(current *Class) {
		if current == nil {
			return
		}
		for _, name := range current.decl.GetInterfaces() {
			iface, ok := u.findClass(name)
			if !ok || seen[strings.ToLower(iface.Name)] {
				continue
			}
			seen[strings.ToLower(iface.Name)] = true
			out = append(out, iface)
			walk(iface)
		}
	}
	walk(class)
	return out
}

// declarationWord is how a diagnostic names a kind of declaration, capitalised
// the way the engine's zend_get_object_type_uc spells it.
func declarationWord(class *Class) string {
	switch class.decl.GetKind() {
	case pb.Class_KIND_INTERFACE:
		return "Interface"
	case pb.Class_KIND_TRAIT:
		return "Trait"
	case pb.Class_KIND_ENUM:
		return "Enum"
	}
	return "Class"
}

// verifyInheritance reports what a declaration may not inherit or implement.
// The engine settles both while linking.
// verifyInterfaceList refuses a declaration that names the same interface twice.
// What counts is the declaration's *own* list, resolved -- so an alias of one
// already there is a duplicate -- while an interface reached through another
// entry, or inherited from the parent, is not: `class C implements I, J` where
// J extends I is ordinary. An enum's implicit UnitEnum -- and a backed one's
// BackedEnum -- is already in the harvested list, so naming it explicitly makes
// it appear twice and is caught by the same rule.
// verifyParentKind refuses `class A extends T` where T is not a class. The
// engine settles this during inheritance, before a trait is bound, so it comes
// before anything the composition would have to say.
func (in *Interp) verifyParentKind(ctx *php.Ctx, unit *Unit, class *Class) error {
	if class.Parent == "" {
		return nil
	}
	parent, ok := unit.findClass(class.Parent)
	if !ok {
		return nil
	}
	kind := parent.decl.GetKind()
	if kind == pb.Class_KIND_CLASS || kind == pb.Class_KIND_ENUM ||
		(kind == pb.Class_KIND_INTERFACE && class.decl.GetKind() == pb.Class_KIND_INTERFACE) {
		// An enum is a class that happens to be final, and that is how the
		// engine words the refusal -- so it belongs to the finality check
		// rather than to this one.
		return nil
	}
	return in.fatal(ctx, "%s %s cannot extend %s %s",
		strings.Title(classKindWord(class.decl.GetKind())), php.DisplayName(class.Name),
		classKindWord(kind), php.DisplayName(parent.Name))
}

// verifyFinalMethods refuses a method that overrides one an ancestor marked
// final. A trait's method counts: composing it into a subclass is an override
// like any other.
func (in *Interp) verifyFinalMethods(ctx *php.Ctx, unit *Unit, class *Class) error {
	if class.Parent == "" {
		return nil
	}
	provided := map[string]bool{}
	for name := range class.methods {
		provided[name] = true
	}
	for _, name := range class.decl.GetTraits() {
		trait, ok := unit.findClass(name)
		if !ok {
			continue
		}
		for _, method := range trait.decl.GetMethods() {
			if !method.GetAbstract() {
				provided[strings.ToLower(method.GetName())] = true
			}
		}
	}
	for _, ancestor := range unit.ancestry(class)[1:] {
		for lower := range provided {
			shown := ""
			if existing, ok := ancestor.methods[lower]; ok {
				if !existing.GetFinal() {
					continue
				}
				shown = existing.GetName()
			} else if name, final := unit.composedFinalMethod(ancestor, lower); final {
				// `use T { T::m as final; }` makes the method the ancestor
				// composed final, which the trait itself never said.
				shown = name
			} else {
				continue
			}
			return in.fatal(ctx, "Cannot override final method %s::%s()",
				php.DisplayName(ancestor.Name), shown)
		}
	}
	return nil
}

func (in *Interp) verifyInterfaceList(ctx *php.Ctx, unit *Unit, class *Class) error {
	seen := map[*Class]bool{}
	for _, name := range class.decl.GetInterfaces() {
		named, ok := unit.findClass(name)
		if !ok {
			continue
		}
		if seen[named] {
			return in.fatal(ctx, "%s %s cannot implement previously implemented interface %s",
				strings.Title(classKindWord(class.decl.GetKind())),
				php.DisplayName(class.Name), php.DisplayName(named.Name))
		}
		seen[named] = true
	}
	if err := in.verifyInterfaceMethodStaticness(ctx, unit, class); err != nil {
		return err
	}
	return in.verifyThrowable(ctx, unit, class)
}

// verifyInterfaceMethodStaticness compares the methods two interfaces in one
// `implements`/`extends` list contribute under the same name. The engine adds
// them in order and treats each new interface as the *parent* of what is
// already there, which is why the diagnostic reads back-to-front: for
// `interface a extends d, c` it names c as the parent, d's spelling of the
// method, and d as the class being changed.
//
// Only staticness is checked here. A class redeclaring an inherited method
// incompatibly is a compile error the harvest already refuses; two interfaces
// disagreeing is not, because interfaces are linked when the class binds.
func (in *Interp) verifyInterfaceMethodStaticness(ctx *php.Ctx, unit *Unit, class *Class) error {
	type contributed struct {
		from   *Class
		method *pb.OpArray
	}
	seen := map[string]contributed{}
	for _, name := range class.decl.GetInterfaces() {
		named, ok := unit.findClass(name)
		if !ok {
			continue
		}
		for _, method := range named.decl.GetMethods() {
			key := strings.ToLower(method.GetName())
			held, taken := seen[key]
			if !taken {
				seen[key] = contributed{from: named, method: method}
				continue
			}
			if held.from == named || held.method.GetStatic() == method.GetStatic() {
				continue
			}
			made := "static method %s::%s() non static in class %s"
			if held.method.GetStatic() {
				made = "non static method %s::%s() static in class %s"
			}
			return in.fatalAt(ctx, held.method.GetFilename(),
				int(held.method.GetLineStart()), "Cannot make "+made,
				php.DisplayName(named.Name), held.method.GetName(),
				php.DisplayName(held.from.Name))
		}
	}
	return nil
}

// verifyThrowable applies zend_exception_implement: only a class whose *root*
// ancestor is Exception or Error may carry Throwable. The interface itself may
// be extended by another interface -- that is how a program declares its own
// throwable contract -- so only classes and enums are checked.
func (in *Interp) verifyThrowable(ctx *php.Ctx, unit *Unit, class *Class) error {
	switch class.decl.GetKind() {
	case pb.Class_KIND_CLASS:
	default:
		// An interface extending Throwable is legal; an enum is refused by
		// verifyEnum, in its own words.
		return nil
	}
	if !unit.instanceOfName(class, "Throwable") {
		return nil
	}
	root := class
	for root.Parent != "" {
		parent, ok := unit.findClass(root.Parent)
		if !ok {
			break
		}
		root = parent
	}
	switch strings.ToLower(php.DisplayName(root.Name)) {
	case "exception", "error":
		return nil
	}
	return in.fatal(ctx, "%s %s cannot implement interface Throwable, "+
		"extend Exception or Error instead",
		strings.Title(classKindWord(class.decl.GetKind())), php.DisplayName(class.Name))
}

func (in *Interp) verifyInheritance(ctx *php.Ctx, unit *Unit, class *Class) error {
	if class.Parent != "" {
		if parent, ok := unit.findClass(class.Parent); ok && parent.decl.GetFinal() {
			return in.fatal(ctx, "Class %s cannot extend final class %s",
				php.DisplayName(class.Name), php.DisplayName(parent.Name))
		}
	}
	// A name in an `implements` or `extends` list that turns out not to be an
	// interface is refused before anything else is checked against it.
	for _, name := range class.decl.GetInterfaces() {
		iface, ok := unit.findClass(name)
		if ok && iface.decl.GetKind() != pb.Class_KIND_INTERFACE {
			return in.fatal(ctx, "%s cannot implement %s - it is not an interface",
				php.DisplayName(class.Name), php.DisplayName(iface.Name))
		}
	}
	// Serializable has been deprecated since 8.1. The notice is for something
	// that can actually be instantiated and serialized -- an interface or an
	// abstract class carrying it is not the problem, but an enum is, and gets
	// the notice even though implementing it is then refused outright -- and once per class,
	// however many routes to Serializable it has. A class supplying *both*
	// replacements, its own or inherited, has already done what the notice
	// asks for; one of the two is not enough.
	if class.decl.GetKind() != pb.Class_KIND_INTERFACE && !class.decl.GetAbstract() &&
		unit.instanceOf(class, "Serializable") && !unit.hasSerializePair(class) {
		ctx.Deprecated("%s implements the Serializable interface, which is deprecated. "+
			"Implement __serialize() and __unserialize() instead (or in addition, if "+
			"support for old PHP versions is necessary)", php.DisplayName(class.Name))
		if pending := ctx.TakePendingError(); pending != nil {
			// A handler turned the deprecation into an exception. It does not
			// escape the link: the engine reports it as part of the link's own
			// failure and names what was being implemented.
			return in.linkFatal(ctx, pending, "During inheritance of %s, while implementing Serializable",
				php.DisplayName(class.Name))
		}
	}
	// Traversable is the marker Iterator and IteratorAggregate share, and a
	// concrete declaration may not claim it on its own -- an abstract class may,
	// so that a subclass can decide which of the two it is. An enum is checked
	// too, and named as one: `gh7792_5` wants
	// `Enum Foo must implement interface Traversable ...`. The engine reports
	// this one with no location at all: it is settled after the file has been
	// compiled, when there is no line left to name.
	if !class.decl.GetAbstract() && class.decl.GetKind() != pb.Class_KIND_INTERFACE {
		traversable, concrete := false, false
		for _, source := range unit.abstractSources(class) {
			switch strings.ToLower(source.Name) {
			case "traversable":
				traversable = true
			case "iterator", "iteratoraggregate":
				concrete = true
			}
		}
		if traversable && !concrete {
			return in.fatalUnknown(ctx, "%s %s must implement interface Traversable as part of "+
				"either Iterator or IteratorAggregate",
				declarationWord(class), php.DisplayName(class.Name))
		}
	}
	if class.decl.GetKind() == pb.Class_KIND_ENUM {
		// What follows is about classes: an enum *is* the thing UnitEnum marks,
		// and whether the engine also refuses an enum claiming both iterator
		// interfaces is not something the corpus settles.
		return nil
	}
	// Iterator and IteratorAggregate answer the same question two ways, and
	// the engine refuses a class that claims both.
	iterator, aggregate := false, false
	for _, source := range unit.abstractSources(class) {
		switch strings.ToLower(source.Name) {
		case "iterator":
			iterator = true
		case "iteratoraggregate":
			aggregate = true
		}
	}
	if iterator && aggregate {
		return in.fatal(ctx, "Class %s cannot implement both Iterator and "+
			"IteratorAggregate at the same time", php.DisplayName(class.Name))
	}
	// The two enum interfaces are the engine's marker for "this is an enum";
	// nothing else may claim them.
	for _, iface := range class.decl.GetInterfaces() {
		switch strings.ToLower(iface) {
		case "unitenum", "backedenum":
			return in.fatal(ctx, "Non-enum class %s cannot implement interface %s",
				php.DisplayName(class.Name), php.DisplayName(iface))
		}
	}
	return nil
}

// hasSerializePair reports whether a class supplies both of the replacements
// Serializable's deprecation points at. Either one alone still earns the
// notice.
func (u *Unit) hasSerializePair(class *Class) bool {
	for _, name := range []string{"__serialize", "__unserialize"} {
		if _, _, found := u.resolveMethod(class, name); found {
			continue
		}
		// An engine class supplies these as C functions with no op_array, so
		// the harvested-body lookup does not see them. ArrayIterator has both,
		// which is why extending it does not earn the notice.
		if _, found := u.engineMethod(class, name); !found {
			return false
		}
	}
	return true
}
