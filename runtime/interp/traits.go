package interp

import (
	"fmt"
	"strings"

	pb "github.com/moriyoshi/php2go/internal/ir/irpb"
	"github.com/moriyoshi/php2go/runtime/php"
)

// Composing a class out of traits is a link-time step, and the engine performs
// it when the declaration executes rather than when the file compiles -- which
// is why the diagnostics below appear after whatever the script printed first.
//
// The engine flattens a trait's members into the composing class. php2go does
// not: `resolveMethod` consults the traits directly, so that self:: inside a
// trait method names the using class. That leaves the conflict checks to be
// made explicitly, which is what this file is.

// composeTraits performs the checks the engine makes while binding traits. A
// failure is a fatal error rather than an exception: it stops the request.
func (in *Interp) composeTraits(ctx *php.Ctx, unit *Unit, class *Class) error {
	if err := in.checkTraitRules(ctx, unit, class); err != nil {
		return err
	}
	names := class.decl.GetTraits()
	if len(names) == 0 {
		return nil
	}
	if unit.composed == nil {
		unit.composed = map[string]bool{}
	}
	if unit.composed[strings.ToLower(class.Name)] {
		return nil
	}
	unit.composed[strings.ToLower(class.Name)] = true

	traits := make([]*Class, 0, len(names))
	for _, name := range names {
		trait, ok := unit.findClass(name)
		if !ok {
			// An unknown name is not this check's business: the declaration
			// itself will report it.
			traits = append(traits, nil)
			continue
		}
		if trait.decl.GetKind() != pb.Class_KIND_TRAIT {
			return in.fatal(ctx, "%s cannot use %s - it is not a trait",
				php.DisplayName(class.Name), php.DisplayName(name))
		}
		traits = append(traits, trait)
	}

	if err := in.checkTraitConstants(ctx, unit, class, traits); err != nil {
		return err
	}
	if err := in.checkTraitMethods(ctx, unit, class, names, traits); err != nil {
		return err
	}
	return in.checkTraitProperties(ctx, unit, class, traits)
}

// checkTraitMethods reproduces zend_do_traits_method_binding. Each trait
// contributes its own methods under their own names, and any `as` alias whose
// source it is under the alias name; a name a later trait would claim again is
// a collision, and only `insteadof` -- or the class declaring the method itself
// -- resolves it.
func (in *Interp) checkTraitMethods(ctx *php.Ctx, unit *Unit, class *Class,
	names []string, traits []*Class) error {
	own := map[string]bool{}
	for name := range class.methods {
		own[name] = true
	}
	// What the class declares itself wins silently, so it is not a collision.
	claimed := map[string]contribution{}
	claim := func(from *Class, method, as string) error {
		key := strings.ToLower(as)
		if own[key] {
			return nil
		}
		if held, taken := claimed[key]; taken {
			if strings.EqualFold(held.from, from.Name) {
				return nil
			}
			return in.fatal(ctx,
				"Trait method %s::%s has not been applied as %s::%s, "+
					"because of collision with %s::%s",
				php.DisplayName(from.Name), method,
				php.DisplayName(class.Name), as,
				php.DisplayName(held.from), as)
		}
		claimed[key] = contribution{from: from.Name, has: true}
		return nil
	}
	for i, trait := range traits {
		if trait == nil {
			continue
		}
		for _, method := range trait.decl.GetMethods() {
			name := method.GetName()
			if method.GetAbstract() {
				// An abstract declaration is a *requirement*, not an
				// implementation: another trait's concrete method satisfies it
				// rather than colliding with it.
				continue
			}
			if unit.traitExcluded(class, names[i], strings.ToLower(name)) {
				continue
			}
			if err := claim(trait, name, name); err != nil {
				return err
			}
		}
		// An alias adds a name; it does not take the original away.
		for _, alias := range class.decl.GetTraitAliases() {
			if alias.GetAlias() == "" {
				continue
			}
			source, ok := unit.aliasSource(class, alias)
			if !ok || source != trait {
				continue
			}
			if err := claim(trait, alias.GetMethod(), alias.GetAlias()); err != nil {
				return err
			}
		}
	}
	in.notePrivateFinalAliases(ctx, unit, class)
	return nil
}

// notePrivateFinalAliases warns about `use T { m as final; }` where m is
// private. Final means "no subclass may replace this", and a private method is
// never reached through a subclass in the first place, so the combination says
// nothing.
//
// The engine warns only when the *alias* is what made it final: a trait method
// already declared `final private` was warned about where it was written, and
// `final1 as private` earns nothing new. A constructor is exempt.
func (in *Interp) notePrivateFinalAliases(ctx *php.Ctx, unit *Unit, class *Class) {
	for _, alias := range class.decl.GetTraitAliases() {
		if !alias.GetFinal() {
			continue
		}
		// The name checked is the one the class composes it under, which an
		// `as` clause may have changed.
		claimed := alias.GetAlias()
		if claimed == "" {
			claimed = alias.GetMethod()
		}
		if strings.EqualFold(claimed, "__construct") {
			continue
		}
		source, ok := unit.aliasSource(class, alias)
		if !ok {
			continue
		}
		method, found := source.methods[strings.ToLower(alias.GetMethod())]
		if !found || method.GetFinal() {
			continue
		}
		visibility := alias.GetVisibility()
		if visibility == "" {
			visibility = method.GetVisibility()
		}
		if visibility != "private" {
			continue
		}
		ctx.Warn("Private methods cannot be final as they are never overridden by other classes")
	}
}

// aliasSource is the trait an `as` clause takes its method from: the one it
// names, or -- unqualified -- the first of the class's traits that has it.
func (u *Unit) aliasSource(class *Class, alias *pb.TraitAlias) (*Class, bool) {
	if alias.GetClass() != "" {
		trait, ok := u.findClass(alias.GetClass())
		return trait, ok
	}
	for _, name := range class.decl.GetTraits() {
		trait, ok := u.findClass(name)
		if !ok {
			continue
		}
		if _, has := trait.methods[strings.ToLower(alias.GetMethod())]; has {
			return trait, true
		}
	}
	return nil, false
}

// contribution records who put a member into the composing class, which is what
// the first half of a conflict message names.
type contribution struct {
	from  string
	flags string
	value *pb.Literal
	has   bool
	// hooked marks a property with a get or set hook. Two of those cannot be
	// composed at all, whatever else about them agrees.
	hooked bool
}

// hookedProperty reports whether a declaration carries a get or set body.
func hookedProperty(prop *pb.Property) bool {
	return prop.GetGetHook() != nil || prop.GetSetHook() != nil
}

// checkTraitConstants reproduces zend_do_traits_constant_binding. Two
// definitions conflict unless their visibility, finality and value all agree.
func (in *Interp) checkTraitConstants(ctx *php.Ctx, unit *Unit, class *Class, traits []*Class) error {
	// Inheritance runs before traits are bound, so the parent's constants are
	// already present and can be the half a trait collides with.
	seen := map[string]contribution{}
	for _, current := range unit.ancestry(class) {
		for _, constant := range current.decl.GetConstants() {
			key := constant.GetName()
			if _, taken := seen[key]; taken {
				continue
			}
			seen[key] = contribution{
				from:  current.Name,
				flags: constantSignature(constant),
				value: constant.GetValue(),
				has:   constant.GetHasValue(),
			}
		}
	}

	for _, trait := range traits {
		if trait == nil {
			continue
		}
		for _, constant := range trait.decl.GetConstants() {
			key := constant.GetName()
			// An enum case is a constant, but a trait constant that collides
			// with one is refused in its own words rather than as an
			// incompatible redefinition -- and before the ordinary comparison,
			// which would otherwise report the case's missing value.
			if enumCase, _, found := unit.findClassConstant(class, key); found &&
				enumCase.GetIsCase() {
				return in.fatal(ctx,
					"Cannot use trait %s, because %s::%s conflicts with enum case %s::%s",
					php.DisplayName(trait.Name), php.DisplayName(trait.Name), key,
					php.DisplayName(class.Name), key)
			}
			existing, taken := seen[key]
			if !taken {
				seen[key] = contribution{
					from:  trait.Name,
					flags: constantSignature(constant),
					value: constant.GetValue(),
					has:   constant.GetHasValue(),
				}
				continue
			}
			if existing.flags == constantSignature(constant) &&
				in.sameConstantValue(ctx, unit, class,
					existing.value, existing.has, constant.GetValue(), constant.GetHasValue()) {
				continue
			}
			return in.fatal(ctx,
				"%s and %s define the same constant (%s) in the composition of %s. "+
					"However, the definition differs and is considered incompatible. Class was composed",
				php.DisplayName(existing.from), php.DisplayName(trait.Name),
				key, php.DisplayName(class.Name))
		}
	}
	return nil
}

// checkTraitProperties is the same rule for properties, where static counts as
// part of the definition alongside visibility.
func (in *Interp) checkTraitProperties(ctx *php.Ctx, unit *Unit, class *Class, traits []*Class) error {
	seen := map[string]contribution{}
	for _, current := range unit.ancestry(class) {
		for _, prop := range current.decl.GetProperties() {
			key := prop.GetName()
			if _, taken := seen[key]; taken {
				continue
			}
			owner := prop.GetDeclaringClass()
			if owner == "" {
				owner = current.Name
			}
			if prop.GetVisibility() == "private" &&
				!strings.EqualFold(owner, class.Name) {
				// A private property another class in the chain declared is
				// not a conflict at all: the composition simply gets its own
				// slot beside it.
				//
				//	if ((colliding_prop->flags & ZEND_ACC_PRIVATE)
				//	 && colliding_prop->ce != ce) {
				//		zend_hash_del(&ce->properties_info, prop_name);
				continue
			}
			seen[key] = contribution{
				from:   current.Name,
				flags:  propertyFlags(prop),
				value:  prop.GetDefaultValue(),
				has:    prop.GetHasDefault(),
				hooked: hookedProperty(prop),
			}
		}
	}

	for _, trait := range traits {
		if trait == nil {
			continue
		}
		for _, prop := range trait.decl.GetProperties() {
			key := prop.GetName()
			// `readonly class` makes every property the class *declares*
			// readonly; a trait's is not, and the engine refuses the
			// composition rather than promoting it. A static one is refused
			// too -- zend_do_traits_property_binding makes no exception for it,
			// even though a readonly class may not declare a static property
			// at all.
			if class.decl.GetReadonly() && !prop.GetReadonly() {
				return in.fatal(ctx,
					"Readonly class %s cannot use trait with a non-readonly property %s::$%s",
					php.DisplayName(class.Name), php.DisplayName(trait.Name), key)
			}
			existing, taken := seen[key]
			if !taken {
				seen[key] = contribution{
					from:   trait.Name,
					flags:  propertyFlags(prop),
					value:  prop.GetDefaultValue(),
					has:    prop.GetHasDefault(),
					hooked: hookedProperty(prop),
				}
				continue
			}
			if existing.hooked || hookedProperty(prop) {
				// A hooked property has bodies, and the engine has no rule for
				// choosing between two of them -- not even when everything
				// else about the two declarations agrees.
				return in.fatal(ctx,
					"%s and %s define the same hooked property ($%s) in the composition of %s. "+
						"Conflict resolution between hooked properties is currently not "+
						"supported. Class was composed",
					php.DisplayName(existing.from), php.DisplayName(trait.Name),
					key, php.DisplayName(class.Name))
			}
			if existing.flags == propertyFlags(prop) &&
				in.sameConstantValue(ctx, unit, class,
					existing.value, existing.has, prop.GetDefaultValue(), prop.GetHasDefault()) {
				continue
			}
			return in.fatal(ctx,
				"%s and %s define the same property ($%s) in the composition of %s. "+
					"However, the definition differs and is considered incompatible. Class was composed",
				php.DisplayName(existing.from), php.DisplayName(trait.Name),
				key, php.DisplayName(class.Name))
		}
	}
	return nil
}

// sameConstantValue compares two declared values, evaluating an initializer the
// compiler deferred. One that cannot be evaluated counts as different, which is
// what the engine does when folding it fails.
func (in *Interp) sameConstantValue(ctx *php.Ctx, unit *Unit, scope *Class,
	left *pb.Literal, leftHas bool, right *pb.Literal, rightHas bool) bool {
	if leftHas != rightHas {
		return false
	}
	if !leftHas {
		return true
	}
	a, ok, err := in.constantLiteral(ctx, unit, left, scope, 0)
	if !ok || err != nil {
		return false
	}
	b, ok, err := in.constantLiteral(ctx, unit, right, scope, 0)
	if !ok || err != nil {
		return false
	}
	return php.StrictEqual(a, b)
}

// constantSignature is what has to match for two contributions of the same
// constant name to be compatible: the modifiers *and* the declared type. PHP
// 8.3 gave class constants types, and `?array` beside untyped is a conflict.
func constantSignature(constant *pb.ClassConstant) string {
	return constantFlags(constant) + " " + constant.GetType()
}

func constantFlags(c *pb.ClassConstant) string {
	visibility := c.GetVisibility()
	if visibility == "" {
		visibility = "public"
	}
	if c.GetFinal() {
		return visibility + " final"
	}
	return visibility
}

func propertyFlags(p *pb.Property) string {
	visibility := p.GetVisibility()
	if visibility == "" {
		visibility = "public"
	}
	if p.GetStatic() {
		visibility += " static"
	}
	if p.GetReadonly() {
		visibility += " readonly"
	}
	// The declared type is part of the signature too: `int` beside
	// `readonly int`, or beside untyped, is a conflict.
	return visibility + " " + p.GetType()
}

// ancestry lists a class and its parents, nearest first.
func (u *Unit) ancestry(class *Class) []*Class {
	var chain []*Class
	for current := class; current != nil; {
		chain = append(chain, current)
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

// fatal reports an unrecoverable error the way the engine does: the text goes
// to the script's own output, and the request stops without unwinding as an
// exception would.
func (in *Interp) fatal(ctx *php.Ctx, format string, args ...any) error {
	file, line := ctx.Location()
	ctx.Write(fmt.Sprintf("\nFatal error: %s in %s on line %d\n",
		fmt.Sprintf(format, args...), file, line))
	return php.Exit(255)
}

// linkFatal reports an exception that escaped while a class was being linked.
// The engine does not let it out: it catches it, prints it inside a fatal of
// its own naming what was being linked, and stops. That is why the last line is
// the trace's "{main} in <file> on line <n>" rather than the "thrown in" an
// uncaught exception ends with.
func (in *Interp) linkFatal(ctx *php.Ctx, err error, format string, args ...any) error {
	escaped, ok := asThrown(err)
	if !ok {
		return err
	}
	return in.fatal(ctx, "%s: Uncaught %s",
		fmt.Sprintf(format, args...), throwableString(escaped.object))
}

// fatalUnknown is fatal for a check the engine settles after compilation, when
// there is no source line left to attribute it to. The engine prints "Unknown"
// and line 0 rather than the last line it saw.
func (in *Interp) fatalUnknown(ctx *php.Ctx, format string, args ...any) error {
	ctx.Write(fmt.Sprintf("\nFatal error: %s in Unknown on line 0\n",
		fmt.Sprintf(format, args...)))
	return php.Exit(255)
}

// checkTraitRules validates the `as` and `insteadof` clauses of a `use`
// statement, which the engine does in zend_traits_init_trait_structures before
// any method is bound. Every clause names a trait and a method, and both have
// to be there.
func (in *Interp) checkTraitRules(ctx *php.Ctx, unit *Unit, class *Class) error {
	used := func(name string) error {
		named, known := unit.findClass(name)
		if !known {
			return in.fatal(ctx, "Could not find trait %s", php.DisplayName(name))
		}
		if named.decl.GetKind() != pb.Class_KIND_TRAIT {
			return in.fatal(ctx, "Class %s is not a trait, Only traits may be used "+
				"in 'as' and 'insteadof' statements", php.DisplayName(named.Name))
		}
		for _, candidate := range class.decl.GetTraits() {
			if strings.EqualFold(candidate, name) {
				return nil
			}
		}
		return in.fatal(ctx, "Required Trait %s wasn't added to %s",
			php.DisplayName(named.Name), php.DisplayName(class.Name))
	}
	// Every exclusion a method has collected, so a second one for the same
	// trait and method can be told from the first.
	excludedOnce := map[string]bool{}
	for _, rule := range class.decl.GetTraitPrecedences() {
		method := rule.GetMethod()
		if rule.GetClass() != "" {
			if err := used(rule.GetClass()); err != nil {
				return err
			}
			winner, _ := unit.findClass(rule.GetClass())
			if _, has := unit.traitMethod(winner, strings.ToLower(method), nil); !has {
				return in.fatal(ctx,
					"A precedence rule was defined for %s::%s but this method does not exist",
					php.DisplayName(winner.Name), method)
			}
		}
		for _, excluded := range rule.GetExcludes() {
			if err := used(excluded); err != nil {
				return err
			}
			if strings.EqualFold(excluded, rule.GetClass()) {
				// The trait the method is to be *used* from cannot also be the
				// one it is excluded from.
				return in.fatal(ctx, "Inconsistent insteadof definition. "+
					"The method %s is to be used from %s, but %s is also on the exclude list",
					method, php.DisplayName(rule.GetClass()), php.DisplayName(excluded))
			}
			key := strings.ToLower(excluded) + "::" + strings.ToLower(method)
			if excludedOnce[key] {
				return in.fatal(ctx, "Failed to evaluate a trait precedence (%s). "+
					"Method of trait %s was defined to be excluded multiple times",
					method, php.DisplayName(excluded))
			}
			excludedOnce[key] = true
		}
	}
	for _, alias := range class.decl.GetTraitAliases() {
		if alias.GetClass() != "" {
			if err := used(alias.GetClass()); err != nil {
				return err
			}
			trait, _ := unit.findClass(alias.GetClass())
			if _, found := trait.methods[strings.ToLower(alias.GetMethod())]; !found {
				return in.fatal(ctx, "An alias was defined for %s::%s but this method does not exist",
					php.DisplayName(trait.Name), alias.GetMethod())
			}
			continue
		}
		// Unqualified: exactly one of the class's traits has to provide it.
		var providers []*Class
		for _, name := range class.decl.GetTraits() {
			trait, known := unit.findClass(name)
			if !known {
				continue
			}
			if _, found := trait.methods[strings.ToLower(alias.GetMethod())]; found {
				providers = append(providers, trait)
			}
		}
		switch {
		case len(providers) > 1:
			first, second := providers[0], providers[1]
			return in.fatal(ctx, "An alias was defined for method %s(), which exists in both "+
				"%s and %s. Use %s::%s or %s::%s to resolve the ambiguity",
				alias.GetMethod(), php.DisplayName(first.Name), php.DisplayName(second.Name),
				php.DisplayName(first.Name), alias.GetMethod(),
				php.DisplayName(second.Name), alias.GetMethod())
		case len(providers) == 0 && alias.GetAlias() != "":
			return in.fatal(ctx, "An alias (%s) was defined for method %s(), but this method does not exist",
				alias.GetAlias(), alias.GetMethod())
		case len(providers) == 0:
			return in.fatal(ctx, "The modifiers of the trait method %s() are changed, "+
				"but this method does not exist. Error", alias.GetMethod())
		}
	}
	return nil
}
