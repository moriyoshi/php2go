package interp

import (
	"sort"
	"strings"

	"github.com/moriyoshi/php2go/runtime/php"
)

// The functions that ask the running program about itself: what classes it has
// declared, what a given object's identity is, what an ancestry looks like.
// They need the unit's tables, so they cannot be plain builtins.

// introspectBuiltin implements them. It reports handled=false for anything it
// does not know, so the caller can carry on to the next family.
func (in *Interp) introspectBuiltin(ctx *php.Ctx, unit *Unit, name string,
	args []php.Value) (php.Value, bool, error) {
	argument := func(i int) php.Value {
		if i < len(args) {
			return args[i].Deref()
		}
		return php.Null()
	}
	switch name {
	case "get_declared_classes":
		// An enum is a class here, as it is to class_exists.
		return php.Arr(in.declaredOfKind(unit, "KIND_CLASS", "KIND_ENUM")), true, nil
	case "get_declared_interfaces":
		return php.Arr(in.declaredOfKind(unit, "KIND_INTERFACE")), true, nil
	case "get_declared_traits":
		return php.Arr(in.declaredOfKind(unit, "KIND_TRAIT")), true, nil

	case "is_subclass_of", "is_a":
		return in.ancestryTest(ctx, unit, name, args)

	case "class_implements":
		return in.relatives(unit, name, argument(0), func(class *Class) []string {
			return interfaceNames(unit, class)
		})
	case "class_parents":
		return in.relatives(unit, name, argument(0), func(class *Class) []string {
			var out []string
			for current := class; current != nil; {
				parent, ok := unit.findClass(current.Parent)
				if !ok {
					break
				}
				out = append(out, parent.Name)
				current = parent
			}
			return out
		})
	case "class_uses":
		return in.relatives(unit, name, argument(0), func(class *Class) []string {
			var out []string
			for _, trait := range class.decl.GetTraits() {
				if used, ok := unit.findClass(trait); ok {
					out = append(out, used.Name)
				}
			}
			return out
		})

	case "get_defined_functions":
		// Declaration order, not alphabetical: the engine's function table is
		// insertion-ordered and get_defined_functions() reports it that way.
		userFunctions := php.NewArray()
		for _, name := range unit.functionOrder {
			userFunctions.Append(php.String(name))
		}
		internal := php.NewArray()
		for _, fn := range sortedKeys(builtins) {
			internal.Append(php.String(fn))
		}
		out := php.NewArray()
		out.Set(php.StringKey("internal"), php.Arr(internal))
		out.Set(php.StringKey("user"), php.Arr(userFunctions))
		return php.Arr(out), true, nil

	case "get_included_files", "get_required_files":
		// The order files were pulled in, which is what the engine reports.
		out := php.NewArray()
		for _, path := range unit.includeOrder {
			out.Append(php.String(path))
		}
		return php.Arr(out), true, nil
	}
	return php.Undef(), false, nil
}

// declaredOfKind lists the classes of one kind: the engine's first, then the
// program's in declaration order, which is the order the class table has them
// in and what the corpus's expectations are written against.
func (in *Interp) declaredOfKind(unit *Unit, kinds ...string) *php.Array {
	wanted := func(class *Class) bool {
		for _, kind := range kinds {
			if class.decl.GetKind().String() == kind {
				return true
			}
		}
		return false
	}
	out := php.NewArray()
	for _, key := range sortedKeys(unit.builtins) {
		if class := unit.builtins[key]; wanted(class) {
			out.Append(php.String(class.Name))
		}
	}
	for _, key := range unit.classOrder {
		class, ok := unit.classes[key]
		if !ok || !wanted(class) {
			continue
		}
		if strings.EqualFold(key, class.Name) {
			// A class the file declares with an opline is not in the table
			// until that opline has run, and the engine does not list it
			// before then. An alias is in the table the moment class_alias
			// returns, and is listed under the name the table holds it by.
			if !unit.classDeclared(in, key) {
				continue
			}
			out.Append(php.String(class.Name))
			continue
		}
		out.Append(php.String(key))
	}
	return out
}

// ancestryTest implements is_subclass_of and is_a, which differ in two ways:
// whether a string subject is accepted by default, and whether the class itself
// counts as its own answer.
func (in *Interp) ancestryTest(ctx *php.Ctx, unit *Unit, name string, args []php.Value) (php.Value, bool, error) {
	if len(args) < 2 {
		return php.Bool(false), true, nil
	}
	subject, wanted := args[0].Deref(), args[1].Deref().String()
	allowString := name == "is_subclass_of"
	if len(args) > 2 {
		allowString = args[2].Bool()
	}
	var className string
	switch {
	case subject.Kind() == php.KindObject:
		className = subject.Object().ClassName
	case subject.Kind() == php.KindString && allowString:
		className = subject.String()
	default:
		return php.Bool(false), true, nil
	}
	// is_subclass_of excludes the class itself; is_a includes it.
	if name == "is_subclass_of" && strings.EqualFold(className, wanted) {
		return php.Bool(false), true, nil
	}
	// A name given as a *string* is looked up the way any other run-time class
	// name is, autoloaders included -- that is what accepting a string means.
	// The class being tested *against* is never autoloaded: if it is not
	// declared, nothing can be an instance of it anyway.
	class, ok := unit.findClass(className)
	if !ok && subject.Kind() == php.KindString {
		resolved, found, err := in.resolveClass(ctx, unit, className, 0)
		if err != nil {
			return php.Undef(), true, err
		}
		class, ok = resolved, found
	}
	if !ok {
		return php.Bool(false), true, nil
	}
	return php.Bool(unit.instanceOf(class, wanted)), true, nil
}

// relatives builds class_implements, class_parents and class_uses, which all
// answer with a name-keyed map or false.
func (in *Interp) relatives(unit *Unit, name string, subject php.Value,
	of func(*Class) []string) (php.Value, bool, error) {
	var className string
	switch subject.Kind() {
	case php.KindObject:
		className = subject.Object().ClassName
	case php.KindString:
		className = subject.String()
	default:
		// Anything else is a type error rather than a false answer: the
		// argument is declared `object|string`.
		return php.Undef(), true, Raise("TypeError",
			"%s(): Argument #1 ($object_or_class) must be of type object|string, %s given",
			name, givenTypeName(subject))
	}
	class, ok := unit.findClass(className)
	if !ok {
		return php.Bool(false), true, nil
	}
	out := php.NewArray()
	for _, related := range of(class) {
		out.Set(php.StringKey(related), php.String(related))
	}
	return php.Arr(out), true, nil
}

// interfaceNames is every interface a class implements, its ancestors' included.
func interfaceNames(unit *Unit, class *Class) []string {
	var out []string
	seen := map[string]bool{}
	var walk func(*Class)
	walk = func(current *Class) {
		if current == nil {
			return
		}
		for _, name := range current.decl.GetInterfaces() {
			resolved, ok := unit.findClass(name)
			if !ok {
				continue
			}
			if !seen[strings.ToLower(resolved.Name)] {
				seen[strings.ToLower(resolved.Name)] = true
				out = append(out, resolved.Name)
				walk(resolved)
			}
		}
		if parent, ok := unit.findClass(current.Parent); ok {
			walk(parent)
		}
	}
	walk(class)
	// A class with __toString implements Stringable whether it said so or not.
	// The engine adds the interface at link time, which the harvest is taken
	// before -- see Unit.instanceOf for why that is only visible when a trait
	// provides the method.
	if !seen["stringable"] && unit.instanceOf(class, "Stringable") {
		if resolved, ok := unit.findClass("Stringable"); ok {
			out = append(out, resolved.Name)
		}
	}
	return out
}

// sortedKeys gives a map's keys in a fixed order, so a listing built from one
// does not change between runs.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
