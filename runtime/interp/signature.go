package interp

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	pb "github.com/moriyoshi/php2go/internal/ir/irpb"
	"github.com/moriyoshi/php2go/runtime/php"
)

// Overriding a method is not free: the override has to still work everywhere
// the original did. The engine settles that while linking, in
// `zend_do_perform_implementation_check`, and reports a mismatch by printing
// both declarations:
//
//	Declaration of C::foo($a) must be compatible with T::foo($a, $b = 0)
//
// Two things have to be right for that message: the *rule* -- arity,
// by-reference and variance constraints -- and the *rendering*, which is
// `zend_get_function_declaration` and has a shape of its own.
//
// A check that cannot be decided is skipped rather than guessed. An unknown
// class in a type means the relation between two types is unknown, and
// inventing a fatal error the program did not earn is worse than missing one.

// verifySignatures reports the first incompatible override a declaration makes.
// The prototypes come from the same sources abstract methods do -- ancestors,
// interfaces, then traits -- because that is the order the engine's function
// table ends up holding them.
func (in *Interp) verifySignatures(ctx *php.Ctx, unit *Unit, class *Class) error {
	// Any earlier failure belongs to whoever was linking then; this check
	// answers only for what happens below it.
	unit.linkAbort, unit.linkAbortName = nil, ""
	sources := unit.abstractSources(class)
	for _, candidate := range unit.overridingMethods(class, sources) {
		if class.decl.GetKind() == pb.Class_KIND_ENUM && enumReservedMethod(candidate.name) {
			// cases(), from() and tryFrom() are the engine's. An enum writing
			// one is redeclaring it, not overriding it incompatibly, and
			// verifyEnum says so in those words.
			continue
		}
		for _, source := range sources[1:] {
			if source == candidate.owner {
				continue
			}
			if candidate.against != nil && source != candidate.against {
				continue
			}
			proto, ok := unit.prototypeMethod(source, candidate.name)
			if !ok || proto == nil || proto == candidate.op {
				continue
			}
			if !prototypeEnforced(source, proto, candidate.name) {
				continue
			}
			// Staticness is settled before the signature is looked at, and the
			// message names the prototype's own class, the method, and the
			// class the overriding declaration belongs to -- which for a trait
			// being bound is the trait.
			if proto.GetStatic() != candidate.op.GetStatic() {
				made := "static method %s::%s() non static in class %s"
				if candidate.op.GetStatic() {
					made = "non static method %s::%s() static in class %s"
				}
				return in.fatalAt(ctx, candidate.op.GetFilename(), int(candidate.op.GetLineStart()),
					"Cannot make "+made, php.DisplayName(source.Name),
					candidate.op.GetName(), php.DisplayName(candidate.owner.Name))
			}
			protoScope, childScope := selfScope(source, class), selfScope(candidate.owner, class)
			compatible, decidable, unresolved := in.signatureCompatible(ctx, unit,
				candidate.op, class, proto, protoScope)
			if unit.linkAbort != nil {
				// Something below this check ended the request. It has already
				// printed whatever it prints; carrying on would report this
				// class's own check as undecidable on top of it.
				aborted, autoloading := unit.linkAbort, unit.linkAbortName
				unit.linkAbort, unit.linkAbortName = nil, ""
				if autoloading != "" {
					// The autoload was asked for by *this* link, so what it
					// threw is reported as the link's own failure rather than
					// escaping on its own.
					return in.linkFatal(ctx, aborted,
						"During inheritance of %s, while autoloading %s",
						php.DisplayName(class.Name), php.DisplayName(autoloading))
				}
				return aborted
			}
			// Which class the message names depends on where the prototype
			// came from. A trait's method is checked against a *parent* while
			// it is being bound, when it is still the trait's; against an
			// interface it is checked afterwards, when it belongs to the class
			// that composed it.
			reported := candidate.owner
			if source.decl.GetKind() == pb.Class_KIND_INTERFACE {
				reported = class
			}
			switch {
			case decidable && !compatible:
				return in.fatalAt(ctx, candidate.op.GetFilename(), int(candidate.op.GetLineStart()),
					"Declaration of %s must be compatible with %s",
					unit.renderSignatureIn(reported, childScope, candidate.op),
					unit.renderSignatureIn(source, protoScope, proto))
			case !decidable && unresolved != "":
				// The engine names the first class it could not obtain, which
				// is the only reason it cannot answer.
				return in.fatalAt(ctx, candidate.op.GetFilename(), int(candidate.op.GetLineStart()),
					"Could not check compatibility between %s and %s, because class %s is not available",
					unit.renderSignatureIn(reported, childScope, candidate.op),
					unit.renderSignatureIn(source, protoScope, proto), php.DisplayName(unresolved))
			}
			if proto.GetTentativeReturn() {
				if err := in.noteTentativeReturn(ctx, unit, class, candidate, reported, source, proto); err != nil {
					return err
				}
			}
			// Only the nearest declaration of a name in the inheritance chain
			// is the prototype; an interface or trait further along still is.
			if source.decl.GetKind() == pb.Class_KIND_CLASS {
				break
			}
		}
	}
	return nil
}

// linkClass runs the checks the engine runs when a class is linked, in the
// order it runs them -- which is observable, since each prints and stops.
// Binding traits is part of linking and happens here rather than at compile
// time, which is why a trait conflict is reported after whatever the script has
// already printed.
func (in *Interp) linkClass(ctx *php.Ctx, unit *Unit, class *Class) error {
	for _, check := range []func(*php.Ctx, *Unit, *Class) error{
		in.verifyParentKind, in.composeTraits, in.verifyFinalMethods,
		in.verifyInterfaceList, in.verifyInheritance,
		in.verifyConstantInheritance,
		in.verifyAccessLevels, in.verifySignatures,
		in.verifyOverrides, in.verifyPropertyTypes, in.verifyEnum, in.verifyAbstract,
	} {
		if err := check(ctx, unit, class); err != nil {
			return err
		}
	}
	return nil
}

// verifyOverrides reports a method carrying #[\Override] that overrides
// nothing. The engine checks this when the class is linked, against the parent
// chain and the interfaces -- a trait is not a parent, so a class that
// redeclares a trait's method with the attribute still has nothing to override.
// The diagnostic names the class doing the using, not the trait that wrote the
// method.
func (in *Interp) verifyOverrides(ctx *php.Ctx, unit *Unit, class *Class) error {
	if class.decl.GetKind() == pb.Class_KIND_TRAIT {
		// A trait is never linked on its own; its methods are checked wherever
		// the trait is used.
		return nil
	}
	sources := unit.abstractSources(class)
	for _, candidate := range unit.overridingMethods(class, sources) {
		if !hasOverrideAttribute(candidate.op) {
			continue
		}
		inherited := false
		for _, source := range sources {
			if source == class {
				continue
			}
			if overridable(unit, source, candidate.name) {
				inherited = true
				break
			}
		}
		if inherited {
			continue
		}
		return in.fatalAt(ctx, candidate.op.GetFilename(), int(candidate.op.GetLineStart()),
			"%s::%s() has #[\\Override] attribute, but no matching parent method exists",
			php.DisplayName(class.Name), candidate.op.GetName())
	}
	return nil
}

// overridable reports whether a source supplies a method that #[\Override]
// can be said to override. Three cases are not that:
//
//   - a trait's *concrete* method, which the class replaces rather than
//     overrides. Its abstract method is a prototype and does count.
//   - a private ancestor method, which is not inherited at all.
//   - the class's own declaration, which the caller excludes.
//
// An engine class carries no op_arrays, so its method table is consulted
// separately.
func overridable(unit *Unit, source *Class, name string) bool {
	if method, ok := source.methods[name]; ok && method != nil {
		switch {
		case source.decl.GetKind() == pb.Class_KIND_TRAIT && !method.GetAbstract():
			return false
		case name == "__construct" && !method.GetAbstract():
			// A concrete constructor is not a prototype: the engine gives one
			// to an abstract constructor and to nothing else, which is why
			// #[\Override] on a constructor needs an abstract parent.
			return false
		}
		return method.GetVisibility() != "private"
	}
	if source.decl.GetKind() == pb.Class_KIND_TRAIT {
		return false
	}
	_, engine := unit.engineMethod(source, name)
	return engine
}

// hasOverrideAttribute reports whether a method carries #[\Override].
func hasOverrideAttribute(op *pb.OpArray) bool {
	for _, attribute := range op.GetAttributes() {
		if strings.EqualFold(strings.TrimPrefix(attribute.GetName(), "\\"), "Override") {
			return true
		}
	}
	return false
}

// overridingMethod is one method a declaration puts into its function table,
// with the class the diagnostic names as its owner. A trait's method keeps the
// trait as its owner: the engine copies the function in without rewriting its
// scope, so an incompatibility it causes is reported against the trait.
type overridingMethod struct {
	name  string
	op    *pb.OpArray
	owner *Class
	// against, when set, is the only source this candidate answers to. An
	// inherited method is checked against a trait's abstract requirement when
	// the trait is bound, and against nothing else: it was already checked
	// against its own ancestry when the class that declared it linked.
	against *Class
}

// overridingMethods lists what a declaration contributes, in a stable order.
// The class's own methods come first, then each trait's, since a method the
// class writes itself replaces a trait's outright.
func (u *Unit) overridingMethods(class *Class, sources []*Class) []overridingMethod {
	var methods []overridingMethod
	for _, name := range sortedMethodNames(class.methods) {
		if op := class.methods[name]; op != nil {
			methods = append(methods, overridingMethod{name: name, op: op, owner: class})
		}
	}
	for _, source := range sources[1:] {
		if source.decl.GetKind() != pb.Class_KIND_TRAIT {
			continue
		}
		for _, name := range sortedMethodNames(source.methods) {
			op := source.methods[name]
			if op == nil || op.GetAbstract() {
				continue
			}
			if _, own := class.methods[name]; own {
				continue
			}
			methods = append(methods, overridingMethod{name: name, op: op, owner: source})
		}
		// An abstract trait method is a requirement, and what satisfies it may
		// have been inherited rather than written here. The engine checks the
		// entry already in the function table against the requirement, so a
		// parent's method can be the one reported as incompatible.
		for _, name := range sortedMethodNames(source.methods) {
			op := source.methods[name]
			if op == nil || !op.GetAbstract() {
				continue
			}
			if _, own := class.methods[name]; own {
				continue
			}
			if _, concrete := u.composedTraitMethod(class, name); concrete {
				continue
			}
			for _, ancestor := range sources[1:] {
				if ancestor.decl.GetKind() != pb.Class_KIND_CLASS {
					continue
				}
				inherited, ok := u.prototypeMethod(ancestor, name)
				if !ok || inherited == nil || inherited.GetAbstract() {
					continue
				}
				methods = append(methods, overridingMethod{
					name: name, op: inherited, owner: ancestor, against: source,
				})
				break
			}
		}
	}
	return methods
}

// prototypeMethod is the declaration a class's method has to be compatible
// with. For a PHP class that is the harvested op_array; for an engine one there
// is no op_array at all, so the signature the harvest recorded is turned into
// the same shape. It is deliberately not put into `class.methods`, which is
// what decides whether a method has a body to run.
func (u *Unit) prototypeMethod(source *Class, name string) (*pb.OpArray, bool) {
	if op, ok := source.methods[name]; ok {
		return op, true
	}
	// The engine flattens a trait's methods into the class that composes it, so
	// a subclass overriding one is overriding the *class's* method. php2go keeps
	// the method with the trait (see traits.go), so the composition has to be
	// followed here or the prototype looks as if it were not there at all.
	if op, ok := u.composedTraitMethod(source, strings.ToLower(name)); ok {
		return op, true
	}
	if source.enginePrototypes == nil {
		source.enginePrototypes = map[string]*pb.OpArray{}
	}
	if op, built := source.enginePrototypes[name]; built {
		return op, op != nil
	}
	for _, method := range source.decl.GetEngineMethods() {
		if !strings.EqualFold(method.GetName(), name) {
			continue
		}
		op := &pb.OpArray{
			Name:            method.GetName(),
			Args:            method.GetArgs(),
			ReturnType:      method.GetReturnType(),
			TentativeReturn: method.GetTentativeReturn(),
			RequiredArgs:    method.GetRequiredArgs(),
			Visibility:      method.GetVisibility(),
			Static:          method.GetStatic(),
			Abstract:        method.GetAbstract(),
			Final:           method.GetFinal(),
		}
		source.enginePrototypes[name] = op
		return op, true
	}
	source.enginePrototypes[name] = nil
	return nil, false
}

// noteTentativeReturn reports a return type that does not match a prototype the
// engine has declared tentative -- one it means to enforce eventually. It is a
// deprecation, not an error, and the class can silence it by saying it knows:
// `#[\ReturnTypeWillChange]` on the method.
func (in *Interp) noteTentativeReturn(ctx *php.Ctx, unit *Unit, class *Class,
	candidate overridingMethod, reported, source *Class, proto *pb.OpArray) error {
	if hasReturnTypeWillChange(candidate.op) {
		return nil
	}
	if returns := candidate.op.GetReturnType(); returns != "" {
		ok, known, _ := in.typeAccepts(ctx, unit, proto.GetReturnType(), selfScope(source, class),
			returns, selfScope(candidate.owner, class))
		if ok || !known {
			return nil
		}
	}
	// The engine reports it where the offending method is written.
	file, line := ctx.Location()
	ctx.SetLocation(candidate.op.GetFilename(), candidate.op.GetLineStart())
	ctx.Deprecated("Return type of %s should either be compatible with %s, or the "+
		"#[\\ReturnTypeWillChange] attribute should be used to temporarily suppress the notice",
		unit.renderSignatureIn(reported, selfScope(candidate.owner, class), candidate.op),
		unit.renderSignatureIn(source, selfScope(source, class), proto))
	ctx.SetLocation(file, line)
	if pending := ctx.TakePendingError(); pending != nil {
		// A handler turned the deprecation into an exception. It does not
		// escape the link: the engine reports it as the link's own failure,
		// naming the class the prototype came from.
		return in.linkFatal(ctx, pending, "During inheritance of %s", php.DisplayName(source.Name))
	}
	return nil
}

// hasReturnTypeWillChange reports the attribute that silences the notice above.
func hasReturnTypeWillChange(op *pb.OpArray) bool {
	for _, attribute := range op.GetAttributes() {
		if strings.EqualFold(strings.TrimPrefix(attribute.GetName(), "\\"), "ReturnTypeWillChange") {
			return true
		}
	}
	return false
}

// enumReservedMethod names the three an enum may not declare.
func enumReservedMethod(name string) bool {
	switch strings.ToLower(name) {
	case "cases", "from", "tryfrom":
		return true
	}
	return false
}

// sortedMethodNames keeps the order a check reports in from depending on Go's
// map iteration, which would otherwise pick a different mismatch each run.
func sortedMethodNames(methods map[string]*pb.OpArray) []string {
	names := make([]string, 0, len(methods))
	for name := range methods {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// selfScope is what `self` means inside a declaration. In a trait it is the
// *using* class, not the trait: composition copies the method in and the type
// resolves against where it landed. The trait is still what the diagnostic
// names, so the two are kept apart.
func selfScope(source, using *Class) *Class {
	if source.decl.GetKind() == pb.Class_KIND_TRAIT {
		return using
	}
	return source
}

// prototypeEnforced reports whether a declaration constrains an override at
// all. A private method is not inherited, so it constrains nothing -- unless it
// is abstract, which only a trait can be. A constructor is exempt for the same
// practical reason PHP exempts it: it is not called through the parent type,
// unless an interface or an abstract declaration says otherwise.
func prototypeEnforced(source *Class, proto *pb.OpArray, name string) bool {
	if proto.GetVisibility() == "private" && !proto.GetAbstract() {
		return false
	}
	// A concrete trait method is not a prototype: composing a trait copies its
	// methods in, and a method the class declares itself replaces one outright
	// rather than overriding it.
	if source.decl.GetKind() == pb.Class_KIND_TRAIT && !proto.GetAbstract() {
		return false
	}
	if strings.EqualFold(name, "__construct") {
		return source.decl.GetKind() == pb.Class_KIND_INTERFACE || proto.GetAbstract()
	}
	return true
}

// signatureCompatible is `zend_do_perform_implementation_check`. The second
// result is whether the answer is known at all: a type naming a class the
// program has not declared leaves the relation undecided.
func (in *Interp) signatureCompatible(ctx *php.Ctx, unit *Unit, child *pb.OpArray, childScope *Class,
	proto *pb.OpArray, protoScope *Class) (compatible, decidable bool, unresolved string) {
	// The number of required arguments cannot increase.
	if proto.GetRequiredArgs() < child.GetRequiredArgs() {
		return false, true, ""
	}
	// By-reference returns are covariant: dropping one is a refusal.
	if proto.GetReturnsRef() && !child.GetReturnsRef() {
		return false, true, ""
	}
	protoVariadic, childVariadic := variadicArgs(proto), variadicArgs(child)
	if protoVariadic && !childVariadic {
		return false, true, ""
	}

	protoArgs, childArgs := proto.GetArgs(), child.GetArgs()
	count := len(protoArgs)
	if len(childArgs) > count {
		count = len(childArgs)
	}
	decided := true
	for i := 0; i < count; i++ {
		protoArg := argAt(protoArgs, i, protoVariadic)
		if protoArg == nil {
			// A new optional argument is fine.
			continue
		}
		childArg := argAt(childArgs, i, childVariadic)
		if childArg == nil {
			// Removing one is not: arity is checked against the declaration.
			return false, true, ""
		}
		// A parameter type is contravariant: the override has to accept
		// everything the prototype accepted.
		switch ok, known, missing := in.typeAccepts(ctx, unit, childArg.GetType(), childScope,
			protoArg.GetType(), protoScope); {
		case !known:
			decided = false
			if unresolved == "" {
				unresolved = missing
			}
		case !ok:
			return false, true, ""
		}
		// By-reference is invariant.
		if childArg.GetByRef() != protoArg.GetByRef() {
			return false, true, ""
		}
	}

	// A return type may be narrowed, never widened, and never removed. A
	// tentative one is not enforced here at all: a mismatch against it is a
	// deprecation rather than an error, reported separately -- while the
	// diagnostic about the *parameters* still prints the type, which is why the
	// prototype keeps it.
	if proto.GetReturnType() != "" && !proto.GetTentativeReturn() {
		if child.GetReturnType() == "" {
			return false, true, ""
		}
		switch ok, known, missing := in.typeAccepts(ctx, unit, proto.GetReturnType(), protoScope,
			child.GetReturnType(), childScope); {
		case !known:
			decided = false
			if unresolved == "" {
				unresolved = missing
			}
		case !ok:
			return false, true, ""
		}
	}
	return true, decided, unresolved
}

// variadicArgs reports whether the last parameter collects the rest.
func variadicArgs(op *pb.OpArray) bool {
	args := op.GetArgs()
	return len(args) > 0 && args[len(args)-1].GetVariadic()
}

// argAt is the engine's indexing: past the end of a variadic declaration every
// further position is the variadic parameter again.
func argAt(args []*pb.ArgInfo, i int, variadic bool) *pb.ArgInfo {
	if i < len(args) {
		return args[i]
	}
	if variadic && len(args) > 0 {
		return args[len(args)-1]
	}
	return nil
}

// typeAccepts reports whether `wider` admits everything `narrower` does --
// `zend_perform_covariant_type_check` with the arguments named for what the
// relation means rather than for where it is used. The second result is false
// when the answer depends on a class the program has not declared, or on a type
// form this runtime does not pb.
func (in *Interp) typeAccepts(ctx *php.Ctx, unit *Unit, wider string, widerScope *Class,
	narrower string, narrowerScope *Class) (accepts, known bool, unresolved string) {
	if wider == "" || wider == "mixed" {
		// No declaration accepts anything, and so does mixed.
		return true, true, ""
	}
	if narrower == "" {
		// Something typed cannot stand in for something untyped.
		return false, true, ""
	}
	if strings.EqualFold(wider, narrower) {
		// Identical types need no class to be available, which is the engine's
		// fast path too -- and the reason most overrides never autoload.
		return true, true, ""
	}
	// Both sides are disjunctions of conjunctions. `narrower` is admitted when
	// each of its alternatives is admitted by some alternative of `wider`, and
	// one conjunction admits another only if every type it names is satisfied
	// by something the other names.
	for _, member := range dnfMembers(narrower, narrowerScope, true) {
		matched, decidable, missing := false, true, ""
		for _, candidate := range dnfMembers(wider, widerScope, false) {
			ok, resolved, absent := in.conjunctionAccepts(ctx, unit, candidate, member)
			if !resolved {
				decidable = false
				if missing == "" {
					missing = absent
				}
				continue
			}
			if ok {
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		if !decidable {
			return false, false, missing
		}
		return false, true, ""
	}
	return true, true, ""
}

// dnfMembers is typeMembers over a disjunctive normal form: each alternative
// becomes the list of types it names, with the same keyword resolution.
func dnfMembers(declared string, scope *Class, resolveStatic bool) [][]string {
	var members [][]string
	for _, conjunction := range typeConjunctions(declared) {
		var atoms []string
		for _, atom := range conjunction {
			atoms = append(atoms, typeMembers(atom, scope, resolveStatic)...)
		}
		if len(atoms) > 0 {
			members = append(members, atoms)
		}
	}
	return members
}

// conjunctionAccepts reports whether one alternative of the wider type admits
// one alternative of the narrower: every type the wider names has to be
// satisfied by something the narrower names.
func (in *Interp) conjunctionAccepts(ctx *php.Ctx, unit *Unit, wider, narrower []string) (accepts, known bool, unresolved string) {
	for _, candidate := range wider {
		matched, decidable, missing := false, true, ""
		for _, member := range narrower {
			ok, resolved, absent := in.typeMemberAccepts(ctx, unit, candidate, member)
			if !resolved {
				decidable = false
				if missing == "" {
					missing = absent
				}
				continue
			}
			if ok {
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		if !decidable {
			return false, false, missing
		}
		return false, true, ""
	}
	return true, true, ""
}

// typeMembers splits a declared type into the alternatives it admits, with the
// shorthands PHP expands: `?T` is `T|null`, and `iterable` is `array|Traversable`.
func typeMembers(declared string, scope *Class, resolveStatic bool) []string {
	var members []string
	for _, part := range strings.Split(strings.TrimPrefix(declared, "?"), "|") {
		part = strings.TrimSpace(part)
		switch strings.ToLower(part) {
		case "iterable":
			members = append(members, "array", "Traversable")
		case "self":
			if scope != nil {
				members = append(members, scope.Name)
				continue
			}
			members = append(members, part)
		case "static":
			// `static` on the *prototype* side is a promise about the called
			// class, and only `static` keeps it: a child naming its own class
			// would break for a grandchild. On the narrower side it is the
			// class being declared, which is what a subclass returns.
			if resolveStatic && scope != nil {
				members = append(members, scope.Name)
				continue
			}
			members = append(members, "static")
		case "parent":
			if scope != nil && scope.Parent != "" {
				members = append(members, scope.Parent)
				continue
			}
			members = append(members, part)
		default:
			members = append(members, part)
		}
	}
	if strings.HasPrefix(declared, "?") {
		members = append(members, "null")
	}
	return members
}

// scalarTypes are the ones with no hierarchy: they accept themselves and, for
// `float`, an int, which is the one widening the type system allows.
var scalarTypes = map[string]bool{
	"int": true, "float": true, "string": true, "bool": true, "array": true,
	"null": true, "false": true, "true": true, "void": true, "never": true,
	"callable": true, "object": true, "mixed": true, "resource": true,
}

// namesAClass reports that a type is a class name rather than one of the
// language's own, which is what decides whether resolving it can autoload.
func namesAClass(key string) bool {
	if scalarTypes[key] {
		return false
	}
	switch key {
	case "iterable", "static", "self", "parent", "": // keywords, not classes
		return false
	}
	return true
}

// typeMemberAccepts answers the relation for one alternative of each side.
func (in *Interp) typeMemberAccepts(ctx *php.Ctx, unit *Unit, wider, narrower string) (accepts, known bool, unresolved string) {
	widerKey, narrowerKey := strings.ToLower(wider), strings.ToLower(narrower)
	if widerKey == narrowerKey {
		return true, true, ""
	}
	// `never` is the bottom type: a function that cannot return satisfies any
	// return declaration.
	if narrowerKey == "never" {
		return true, true, ""
	}
	switch widerKey {
	case "mixed":
		return true, true, ""
	case "float":
		// int is the one scalar that widens.
		return narrowerKey == "int", true, ""
	case "bool":
		return narrowerKey == "true" || narrowerKey == "false", true, ""
	case "object":
		// Any class satisfies `object`; no scalar does. The class is still
		// looked up: the engine resolves the *narrower* side's names whatever
		// the wider side names, and a lookup can autoload -- an autoloader that
		// prints or throws is the only way to tell, and the corpus does.
		if scalarTypes[narrowerKey] {
			return false, true, ""
		}
		if namesAClass(narrowerKey) {
			if _, have := in.resolveTypeClass(ctx, unit, narrower); !have {
				return false, false, narrower
			}
		}
		return true, true, ""
	case "iterable":
		return narrowerKey == "array", true, ""
	case "static":
		// A prototype's `static` is a promise about the called class, and only
		// `static` keeps it. It names no class, so nothing is looked up.
		return false, true, ""
	}
	if scalarTypes[widerKey] || scalarTypes[narrowerKey] {
		return false, true, ""
	}
	// Both name classes: the relation is inheritance, and it is only knowable
	// once both are available. Both sides go through the class table, so an
	// alias is compared as the class it names rather than as a name of its own.
	// A name the table does not have is what the engine's delayed autoloads
	// are for: the check asks for the class, and only a class that stays
	// missing afterwards leaves the relation unresolved.
	// The child's class is looked up first, and a child that cannot be
	// obtained leaves the relation unresolved without the parent's ever being
	// asked for -- which is observable, since a lookup can autoload and an
	// autoloader can print.
	narrowerClass, haveNarrower := in.resolveTypeClass(ctx, unit, narrower)
	if !haveNarrower {
		return false, false, narrower
	}
	widerClass, haveWider := in.resolveTypeClass(ctx, unit, wider)
	if !haveWider {
		return false, false, wider
	}
	return unit.instanceOfName(narrowerClass, widerClass.Name), true, ""
}

// resolveTypeClass finds the class a type names, autoloading it if the program
// registered a way to. Linking is where the engine discovers it needs a class
// it has not seen, and a variance check is one of the few places that happens.
func (in *Interp) resolveTypeClass(ctx *php.Ctx, unit *Unit, name string) (*Class, bool) {
	if !unit.classAvailable(in, name) {
		// Asking is what runs the autoloaders, and an autoloader can print --
		// so the request has to be made even when the harvest happens to hold
		// a declaration the program has not reached yet.
		if _, _, err := in.autoload(ctx, unit, name, 0); err != nil {
			// The autoloader stopped -- it threw, exited, or declared a class
			// that could not be linked. That is the end of the request, not a
			// class this check could not obtain, so it is kept for
			// verifySignatures to hand on rather than reported as "not
			// available".
			if unit.linkAbort == nil {
				unit.linkAbort, unit.linkAbortName = err, name
			}
			return nil, false
		}
	}
	class, ok := unit.findClass(name)
	return class, ok
}

// instanceOfName reports whether a class is the named one or descends from it,
// through parents and interfaces alike.
func (u *Unit) instanceOfName(class *Class, name string) bool {
	for _, source := range u.abstractSources(class) {
		if strings.EqualFold(source.Name, name) {
			return true
		}
	}
	return false
}

// renderSignature is `zend_get_function_declaration`: the shape a mismatch is
// reported in. Defaults are rendered from the RECV_INIT opline that carries
// them, which is where the engine reads them from too.
func (u *Unit) renderSignature(owner *Class, op *pb.OpArray) string {
	return u.renderSignatureIn(owner, owner, op)
}

// renderSignatureIn is the same rendering with the scope the types resolve
// against named separately from the class the method is attributed to. The two
// part company for a trait: the engine keeps the method's own scope for the
// `T::` prefix but resolves `self` against the class that composed it, so
// `abstract private function method(self $x)` in a trait prints as
// `T::method(C $x)`.
func (u *Unit) renderSignatureIn(owner, scope *Class, op *pb.OpArray) string {
	var out strings.Builder
	if op.GetReturnsRef() {
		out.WriteString("& ")
	}
	if owner != nil {
		out.WriteString(php.DisplayName(owner.Name))
		out.WriteString("::")
	}
	out.WriteString(op.GetName())
	out.WriteByte('(')
	required := int(op.GetRequiredArgs())
	for i, arg := range op.GetArgs() {
		if i > 0 {
			out.WriteString(", ")
		}
		if arg.GetType() != "" {
			out.WriteString(resolvedTypeText(arg.GetType(), scope))
			out.WriteByte(' ')
		}
		if arg.GetByRef() {
			out.WriteByte('&')
		}
		if arg.GetVariadic() {
			out.WriteString("...")
		}
		out.WriteByte('$')
		out.WriteString(arg.GetName())
		if i >= required && !arg.GetVariadic() {
			out.WriteString(" = ")
			out.WriteString(defaultText(op, i))
		}
	}
	out.WriteByte(')')
	if op.GetReturnType() != "" {
		out.WriteString(": ")
		out.WriteString(resolvedTypeText(op.GetReturnType(), scope))
	}
	return out.String()
}

// resolvedTypeText is `zend_type_to_string_resolved`: the printer substitutes
// the scope for `self` and its parent for `parent`, and leaves everything else
// -- `static` included, since that one stays a promise about the called class
// rather than a name.
func resolvedTypeText(declared string, scope *Class) string {
	if scope == nil || !strings.Contains(strings.ToLower(declared), "self") &&
		!strings.Contains(strings.ToLower(declared), "parent") {
		return declared
	}
	var out strings.Builder
	name := 0
	for i := 0; i <= len(declared); i++ {
		if i < len(declared) && !strings.ContainsRune("?|&()", rune(declared[i])) {
			continue
		}
		switch part := declared[name:i]; strings.ToLower(part) {
		case "self":
			out.WriteString(php.DisplayName(scope.Name))
		case "parent":
			if scope.Parent != "" {
				out.WriteString(php.DisplayName(scope.Parent))
			} else {
				out.WriteString(part)
			}
		default:
			out.WriteString(part)
		}
		if i < len(declared) {
			out.WriteByte(declared[i])
		}
		name = i + 1
	}
	return out.String()
}

// defaultText renders one parameter's default the way the declaration does. The
// engine scans the op_array for the RECV_INIT that carries it rather than
// keeping it with the argument, and so does this.
func defaultText(op *pb.OpArray, index int) string {
	if args := op.GetArgs(); index < len(args) && args[index].GetDefaultValue() != "" {
		// An internal declaration keeps the default with the parameter; there
		// is no opline to scan.
		return args[index].GetDefaultValue()
	}
	for _, line := range op.GetOplines() {
		if line.GetOp1() != uint32(index+1) {
			continue
		}
		literal := literalOperand(op, line)
		if literal == nil {
			continue
		}
		return literalDeclarationText(literal)
	}
	return "<default>"
}

// literalOperand returns the constant an opline's op2 names, when it has one.
func literalOperand(op *pb.OpArray, line *pb.Opline) *pb.Literal {
	if line.GetOp2Type() != opConst {
		return nil
	}
	literals := op.GetLiterals()
	if int(line.GetOp2()) >= len(literals) {
		return nil
	}
	return literals[line.GetOp2()]
}

// literalDeclarationText renders a default value the way a declaration shows
// it, which is not how var_export or an error message would: a string is
// single-quoted and cut at ten bytes, and an array is never expanded.
func literalDeclarationText(literal *pb.Literal) string {
	switch literal.GetKind() {
	case pb.Literal_KIND_NULL:
		return "null"
	case pb.Literal_KIND_BOOL:
		if literal.GetIntValue() != 0 {
			return "true"
		}
		return "false"
	case pb.Literal_KIND_INT:
		return strconv.FormatInt(literal.GetIntValue(), 10)
	case pb.Literal_KIND_FLOAT:
		return php.Float(literal.GetFloatValue()).String()
	case pb.Literal_KIND_STRING:
		text := literal.GetStringValue()
		if len(text) > 10 {
			return "'" + text[:10] + "...'"
		}
		return "'" + text + "'"
	case pb.Literal_KIND_ARRAY:
		if len(literal.GetEntries()) == 0 {
			return "[]"
		}
		return "[...]"
	case pb.Literal_KIND_EXPR:
		expr := literal.GetExpression()
		switch expr.GetKind() {
		case "constant":
			return expr.GetName()
		case "class_constant":
			return expr.GetName() + "::" + expr.GetMember()
		}
		return "<expression>"
	}
	return "<default>"
}

// fatalAt is fatal for a diagnostic the engine attributes to a declaration
// rather than to the statement being executed: a link-time refusal names the
// method's own file and line, which is usually not where the class is declared.
func (in *Interp) fatalAt(ctx *php.Ctx, file string, line int, format string, args ...any) error {
	if file == "" {
		return in.fatal(ctx, format, args...)
	}
	ctx.Write(fmt.Sprintf("\nFatal error: %s in %s on line %d\n",
		fmt.Sprintf(format, args...), file, line))
	return php.Exit(255)
}

// resolveDependencies asks for everything linking a declaration needs -- its
// parent, its interfaces and its traits -- which is where the engine finds out
// it has not seen a class yet and runs the autoloaders. A name that stays
// missing is left alone: what a program does with an unlinkable class is a
// separate question from what its autoloader prints.
func (in *Interp) resolveDependencies(ctx *php.Ctx, unit *Unit, class *Class) error {
	// While a class is waiting for its own parent, it cannot itself be
	// extended: an autoloader that declares `class X extends B` while B is
	// looking for *its* parent finds B unavailable, which is what the engine
	// reports as `Class "B" not found`.
	if unit.linking == nil {
		unit.linking = map[string]bool{}
	}
	key := strings.ToLower(class.Name)
	unit.linking[key] = true
	defer delete(unit.linking, key)
	type dependency struct{ kind, name string }
	names := make([]dependency, 0, 4)
	if class.Parent != "" {
		names = append(names, dependency{"Class", class.Parent})
	}
	for _, name := range class.decl.GetInterfaces() {
		names = append(names, dependency{"Interface", name})
	}
	for _, name := range class.decl.GetTraits() {
		names = append(names, dependency{"Trait", name})
	}
	for _, needed := range names {
		if unit.classAvailable(in, needed.name) {
			continue
		}
		if unit.linking[strings.ToLower(strings.TrimPrefix(needed.name, "\\"))] {
			// The name is in the class table -- its own declaration is running
			// -- so there is nothing to autoload. It simply cannot be linked
			// against yet, which the engine reports as not found.
			if needed.kind == "Trait" {
				return in.fatal(ctx, "Trait %q not found", php.DisplayName(needed.name))
			}
			return in.throwError(ctx, unit, "Error", "%s %q not found",
				needed.kind, php.DisplayName(needed.name))
		}
		file, line := ctx.Location()
		_, _, err := in.autoload(ctx, unit, needed.name, 0)
		// An autoloader is a call, and a call moves the reported location. What
		// comes next is still attributed to the declaration.
		ctx.SetLocation(file, line)
		if err != nil {
			return err
		}
		if unit.classAvailable(in, needed.name) {
			continue
		}
		// Still missing after the autoloaders had their turn: the declaration
		// cannot be linked. A missing parent or interface is a catchable Error
		// -- the class simply does not exist yet -- but a missing *trait* is a
		// refusal to compose, and the engine reports it as a plain fatal.
		if needed.kind == "Trait" {
			return in.fatal(ctx, "Trait %q not found", php.DisplayName(needed.name))
		}
		return in.throwError(ctx, unit, "Error", "%s %q not found",
			needed.kind, php.DisplayName(needed.name))
	}
	return nil
}

// verifyPropertyTypes reports a redeclared property whose type differs from the
// one it inherits. Property types are **invariant**: unlike a method's, a
// child's may not narrow or widen, only repeat. The engine compares the two
// types in both directions and refuses unless each admits the other.
func (in *Interp) verifyPropertyTypes(ctx *php.Ctx, unit *Unit, class *Class) error {
	if err := in.verifySetHookTypes(ctx, unit, class); err != nil {
		return err
	}
	if err := in.verifyHookSignatures(ctx, unit, class); err != nil {
		return err
	}
	ancestors := unit.ancestry(class)
	if len(ancestors) < 2 {
		return nil
	}
	for _, prop := range class.decl.GetProperties() {
		if prop.GetDeclaringClass() != "" && !strings.EqualFold(prop.GetDeclaringClass(), class.Name) {
			// Inherited rather than redeclared: nothing to compare it against.
			continue
		}
		for _, ancestor := range ancestors[1:] {
			inherited, found := ancestorProperty(ancestor, prop.GetName())
			if !found {
				continue
			}
			if sameDeclaredType(in, ctx, unit, prop.GetType(), class, inherited.GetType(), ancestor) {
				break
			}
			return in.fatal(ctx, "Type of %s::$%s must be %s (as in class %s)",
				php.DisplayName(class.Name), prop.GetName(),
				declaredOrNone(inherited.GetType()), php.DisplayName(ancestor.Name))
		}
	}
	return nil
}

// verifyHookSignatures compares a property's hooks against the ones an
// ancestor or interface declared for the same property. A hook is an ordinary
// declaration as far as compatibility goes -- the parameter is contravariant,
// the return covariant, and dropping a by-reference return is a refusal -- and
// it is named `C::$prop::get` in the diagnostic.
func (in *Interp) verifyHookSignatures(ctx *php.Ctx, unit *Unit, class *Class) error {
	sources := unit.abstractSources(class)
	if len(sources) < 2 {
		return nil
	}
	for _, prop := range class.decl.GetProperties() {
		if prop.GetDeclaringClass() != "" && !strings.EqualFold(prop.GetDeclaringClass(), class.Name) {
			continue
		}
		if err := in.verifyImplicitSetType(ctx, unit, class, prop, sources); err != nil {
			return err
		}
		if err := in.verifySetAccess(ctx, unit, class, prop, sources); err != nil {
			return err
		}
		for _, hook := range []*pb.OpArray{prop.GetGetHook(), prop.GetSetHook()} {
			if hook == nil {
				continue
			}
			for _, source := range sources[1:] {
				inherited, found := ancestorProperty(source, prop.GetName())
				if !found {
					continue
				}
				proto := inherited.GetGetHook()
				if strings.HasSuffix(hook.GetName(), "::set") {
					proto = inherited.GetSetHook()
				}
				if proto == nil || proto == hook {
					continue
				}
				compatible, decidable, _ := in.signatureCompatible(ctx, unit, hook, class, proto, source)
				if decidable && !compatible {
					return in.fatalAt(ctx, hook.GetFilename(), int(hook.GetLineStart()),
						"Declaration of %s must be compatible with %s",
						unit.renderSignatureIn(class, class, hook),
						unit.renderSignatureIn(source, source, proto))
				}
				// The nearest declaration is the one that constrains; anything
				// further along was already checked when it was linked.
				break
			}
		}
	}
	return nil
}

// setVisibilityRank orders how restrictive a set access level is. A property
// with none is as open as its own visibility, since that is what governs the
// write.
func setVisibilityRank(visibility string) int {
	switch visibility {
	case "private":
		return 2
	case "protected":
		return 1
	}
	return 0
}

// verifySetAccess refuses a redeclaration that narrows who may write the
// property. `readonly` carries an implicit `protected(set)`, which is what puts
// an ordinary-looking readonly declaration in front of this check.
func (in *Interp) verifySetAccess(ctx *php.Ctx, unit *Unit, class *Class,
	prop *pb.Property, sources []*Class) error {
	if prop.GetSetVisibility() == "" {
		return nil
	}
	for _, source := range sources[1:] {
		inherited, found := ancestorProperty(source, prop.GetName())
		if !found {
			continue
		}
		if inherited.GetVirtual() && inherited.GetSetHook() == nil &&
			inherited.GetGetHook() != nil {
			// A get-only virtual property is never written, so it says nothing
			// about who may write the one that redeclares it.
			return nil
		}
		// With no set level of its own the ancestor is written wherever it can
		// be read, so its own visibility is the bar.
		bar, suffix := inherited.GetSetVisibility(), " or weaker"
		if bar == "" {
			bar, suffix = inherited.GetVisibility(), ""
		}
		if setVisibilityRank(prop.GetSetVisibility()) <= setVisibilityRank(bar) {
			return nil
		}
		word := "omitted"
		if inherited.GetSetVisibility() != "" {
			word = inherited.GetSetVisibility() + "(set)"
		}
		// The engine says "as in class" whatever the ancestor really is.
		return in.fatal(ctx, "Set access level of %s::$%s must be %s (as in class %s)%s",
			php.DisplayName(class.Name), prop.GetName(), word,
			php.DisplayName(source.Name), suffix)
	}
	return nil
}

// verifyImplicitSetType covers the property that has *no* set hook of its own.
// Writing it goes straight to the storage, so what it accepts is its declared
// type -- and an abstract declaration that named a wider set type is asking for
// something the storage cannot take.
func (in *Interp) verifyImplicitSetType(ctx *php.Ctx, unit *Unit, class *Class,
	prop *pb.Property, sources []*Class) error {
	if prop.GetSetHook() != nil {
		return nil
	}
	for _, source := range sources[1:] {
		inherited, found := ancestorProperty(source, prop.GetName())
		if !found {
			continue
		}
		hook := inherited.GetSetHook()
		if hook == nil || len(hook.GetArgs()) == 0 ||
			(source.decl.GetKind() != pb.Class_KIND_INTERFACE && !hook.GetAbstract()) {
			return nil
		}
		declared := hook.GetArgs()[0].GetType()
		if declared == "" {
			return nil
		}
		accepts, known, _ := in.typeAccepts(ctx, unit, prop.GetType(), class, declared, source)
		if accepts || !known {
			return nil
		}
		return in.fatal(ctx, "Set type of %s::$%s must be supertype of %s (as in %s %s)",
			php.DisplayName(class.Name), prop.GetName(), declared,
			classKindWord(source.decl.GetKind()), php.DisplayName(source.Name))
	}
	return nil
}

// verifySetHookTypes checks a set hook's parameter against the property it
// writes. The hook is handed the property's value, so its parameter has to
// accept that type -- the parameter is contravariant, as any parameter is. A
// hook with no declared parameter type accepts anything and is always fine; a
// declared one on an *untyped* property has nothing to be compatible with and
// is refused outright.
func (in *Interp) verifySetHookTypes(ctx *php.Ctx, unit *Unit, class *Class) error {
	for _, prop := range class.decl.GetProperties() {
		hook := prop.GetSetHook()
		if hook == nil || len(hook.GetArgs()) == 0 {
			continue
		}
		declared := hook.GetArgs()[0].GetType()
		if declared == "" {
			continue
		}
		if prop.GetType() != "" {
			accepts, known, _ := in.typeAccepts(ctx, unit, declared, class, prop.GetType(), class)
			if accepts || !known {
				continue
			}
		}
		return in.fatalAt(ctx, hook.GetFilename(), int(hook.GetLineStart()),
			"Type of parameter $%s of hook %s::$%s::set must be compatible with property type",
			hook.GetArgs()[0].GetName(), php.DisplayName(class.Name), prop.GetName())
	}
	return nil
}

// ancestorProperty finds a property an ancestor declares itself.
func ancestorProperty(class *Class, name string) (*pb.Property, bool) {
	for _, prop := range class.decl.GetProperties() {
		if prop.GetName() != name {
			continue
		}
		if prop.GetDeclaringClass() != "" && !strings.EqualFold(prop.GetDeclaringClass(), class.Name) {
			return nil, false
		}
		return prop, true
	}
	return nil, false
}

// sameDeclaredType reports whether two property types are the same type, which
// for an invariant position means each admits the other. An undecidable
// comparison counts as the same: refusing on a class the program has not
// declared would invent an error.
func sameDeclaredType(in *Interp, ctx *php.Ctx, unit *Unit, child string, childScope *Class,
	parent string, parentScope *Class) bool {
	if child == parent {
		return true
	}
	if (child == "") != (parent == "") {
		return false
	}
	forward, known, _ := in.typeAccepts(ctx, unit, parent, parentScope, child, childScope)
	if !known {
		// A class in one of the types is not declared yet, so neither direction
		// can be decided by resolution -- but the names themselves still can
		// be, and invariance is the one check where that is enough.
		return sameTypeAtoms(child, childScope, parent, parentScope)
	}
	backward, known, _ := in.typeAccepts(ctx, unit, child, childScope, parent, parentScope)
	if !known {
		return sameTypeAtoms(child, childScope, parent, parentScope)
	}
	return forward && backward
}

// declaredOrNone names a type the way the invariance message does, where an
// absent one is written as "no type".
func declaredOrNone(declared string) string {
	if declared == "" {
		return "no type"
	}
	return declared
}

// sameTypeAtoms compares two declared types by the names they mention. It is
// what settles invariance when a class in one of them cannot be resolved: the
// engine still refuses `public X|B $p` against `public B $p` with X undeclared,
// because two types naming different things cannot be the same type whatever
// those things turn out to be.
//
// Order does not matter -- `X|Y` and `Y|X` are one type -- so the comparison is
// over sorted sets. Two spellings have to be normalised first: `iterable` is
// `Traversable|array` written short, and `self` names the class it is written
// in, which the other side may name outright.
func sameTypeAtoms(child string, childScope *Class, parent string, parentScope *Class) bool {
	normalize := func(declared string, scope *Class) []string {
		var out []string
		for _, atoms := range typeConjunctions(declared) {
			var resolved []string
			for _, atom := range atoms {
				switch strings.ToLower(atom) {
				case "iterable":
					// Only alone: `iterable&X` is an intersection with a name in
					// it, and expanding a disjunction inside one is not a thing
					// this comparison can do.
					if len(atoms) == 1 {
						out = append(out, "array", "traversable")
						resolved = nil
						continue
					}
					resolved = append(resolved, "iterable")
				case "self", "static":
					if scope != nil {
						resolved = append(resolved, strings.ToLower(scope.Name))
						continue
					}
					resolved = append(resolved, strings.ToLower(atom))
				case "parent":
					if scope != nil && scope.Parent != "" {
						resolved = append(resolved, strings.ToLower(scope.Parent))
						continue
					}
					resolved = append(resolved, strings.ToLower(atom))
				default:
					resolved = append(resolved, strings.ToLower(atom))
				}
			}
			if len(resolved) == 0 {
				continue
			}
			sort.Strings(resolved)
			out = append(out, strings.Join(resolved, "&"))
		}
		sort.Strings(out)
		return out
	}
	left, right := normalize(child, childScope), normalize(parent, parentScope)
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
