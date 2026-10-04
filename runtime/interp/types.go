package interp

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	pb "github.com/moriyoshi/php2go/internal/ir/irpb"
	"github.com/moriyoshi/php2go/runtime/php"
)

// Type declarations are checked at the two places the compiler emits a check:
// ZEND_RECV / ZEND_RECV_INIT for a parameter, ZEND_VERIFY_RETURN_TYPE for a
// return. The declared type arrives as the engine's own rendering --
// "?string", "string|int", "Foo" -- so the union order and the wording of a
// TypeError both come from the harvest rather than from anything reconstructed
// here.
//
// Coercion is decided by the *caller's* file: declare(strict_types=1) at the
// call site turns off the conversions, which is why the flag travels on the
// frame rather than on the unit.

// verifyArgument checks one incoming argument against its declared type and
// returns the value to bind.
func (in *Interp) verifyArgument(ctx *php.Ctx, unit *Unit, f *frame, op *pb.OpArray,
	position int, value php.Value) (php.Value, error) {
	args := op.GetArgs()
	index := position
	if index >= len(args) {

		// Everything past the last declared parameter belongs to a variadic,
		// whose type is the one it declared.
		index = len(args) - 1
	}
	if index < 0 {
		return value, nil
	}
	arg := args[index]
	// A variadic parameter is not named in the diagnostic: the position is the
	// argument's, not the parameter's, so `$rest` would name the wrong thing.
	// Probed -- `v(int $a, ?int ...$rest)` reports "Argument #2 must be of
	// type ?int" where a plain second parameter reports "Argument #2 ($b)".
	named := " ($" + arg.GetName() + ")"
	if arg.GetVariadic() {
		named = ""
	}
	if arg.GetByRef() {
		// A by-reference parameter keeps the caller's slot -- that is what the
		// callee writes through -- but the declared type is still checked, and
		// what the check converts is written *back into the slot*: after
		// `to_int($x)` on a float 1.0, the caller's $x is int(1). Probed,
		// including the precision deprecation a lossy float earns.
		if arg.GetType() != "" {
			declared := frameType(arg.GetType(), f)
			converted, ok := in.checkType(ctx, unit, declared, value, f.callerStrict)
			if !ok {
				if thrown := coercionThrow(ctx); thrown != nil {
					return value, thrown
				}
				return value, in.throwError(ctx, unit, "TypeError",
					"%s(): Argument #%d%s must be of type %s, %s given%s",
					functionLabel(f, op), position+1, named, displayType(declared),
					givenTypeName(value.Deref()), f.calledAt(ctx))
			}
			if ref, isRef := value.RefOf(); isRef &&
				converted.Deref().Kind() != value.Deref().Kind() {
				ref.Set(converted.Deref())
			}
		}
		return value, nil
	}
	// A by-value parameter never shares the caller's slot, even when the
	// argument arrived as a reference -- which it can, from an array element
	// that is one.
	value = value.Deref()
	if arg.GetType() == "" {
		return value, nil
	}
	// `self` and `static` in a parameter type name classes, and which ones is
	// only known here: the frame being built is the one that says.
	declared := frameType(arg.GetType(), f)
	converted, ok := in.checkType(ctx, unit, declared, value, f.callerStrict)
	if ok {
		in.noteCallableForm(ctx, unit, declared, converted)
		return converted, nil
	}
	if thrown := coercionThrow(ctx); thrown != nil {
		return value, thrown
	}
	return value, in.throwError(ctx, unit, "TypeError",
		"%s(): Argument #%d%s must be of type %s, %s given%s",
		functionLabel(f, op), position+1, named, displayType(declared),
		givenTypeName(value), f.calledAt(ctx))
}

// verifyBuiltinArgs rejects an argument the linked build would reject, using
// the harvested signature. An internal function names no call site in its
// message -- it is not defined in a file -- which is the one way its wording
// differs from a user function's.
func (in *Interp) verifyBuiltinArgs(ctx *php.Ctx, unit *Unit, name string, args []php.Value, strict bool) error {
	signature, ok := in.engineSignatures[strings.ToLower(name)]
	if !ok {
		return nil
	}
	declared := signature.GetArgs()
	if err := in.verifyBuiltinArity(ctx, unit, signature, args); err != nil {
		return err
	}
	if ownArgumentDiagnostics[strings.ToLower(name)] {
		// The signature admits more than the function does, and the function
		// says so in its own words. Checking it here would report the declared
		// type instead, which is not what the engine prints.
		return nil
	}
	for i, value := range args {
		index := i
		if index >= len(declared) {
			if !signature.GetVariadic() || len(declared) == 0 {
				break
			}
			index = len(declared) - 1
		}
		arg := declared[index]
		// **The variadic parameter is never named.** It carries a name in the
		// declaration -- `array ...$arrays` -- and the engine does not print
		// it: `array_map` says `Argument #2 ($array)` for the declared array
		// and `Argument #3 must be of type array` for the first variadic slot
		// as well as every one after it. Printing the name was the easy
		// mistake, and it is the *parameter* that decides, not whether the
		// position ran past the list.
		argName := arg.GetName()
		if arg.GetVariadic() {
			argName = ""
		}
		// Native implementations parse their own by-reference arguments and may
		// deliberately narrow the manifest type. A harvested PHP replacement has
		// no internal parser behind it, so validate before entering its cloned body
		// to preserve internal-function wording and the caller's reference slot.
		if arg.GetByRef() {
			if _, replaced := unit.userlandBuiltins[strings.ToLower(name)]; !replaced {
				continue
			}
		}
		if arg.GetType() == "" {
			continue
		}
		// A parameter that takes a callback says why the one it was given is
		// not one, which is the internal functions' own wording -- a user
		// function typed `callable` only reports the type mismatch.
		if acceptsCallable(arg.GetType()) {
			given := value.Deref()
			nullish := given.Kind() == php.KindNull || given.Kind() == php.KindUndef
			orNull := ""
			if admitsNull(arg.GetType()) {
				if nullish {
					continue
				}
				orNull = " or null"
			}
			// Only these three kinds can name a callable at all; the rest are
			// refused for being the wrong shape rather than for what they name.
			reason := "no array or string given"
			switch given.Kind() {
			case php.KindString, php.KindArray, php.KindObject:
				reason = in.callableReasonQuiet(ctx, unit, given, true)
			}
			if reason == "" {
				continue
			}
			return in.throwError(ctx, unit, "TypeError",
				"%s(): %s must be a valid callback%s, %s",
				signature.GetName(), php.ArgLabel(i+1, argName), orNull, reason)
		}
		// An internal function coerces by the same rule a user function does,
		// and the rule is about the *call site*: a call written in a
		// strict-types file gets no conversions, wherever the callee came from.
		// The converted value is written back, since the conversion is what the
		// function is meant to receive.
		if converted, ok := in.checkType(ctx, unit, arg.GetType(), value, strict); ok {
			if arg.GetByRef() {
				if ref, isRef := value.RefOf(); isRef {
					if converted.Deref().Kind() != value.Deref().Kind() {
						ref.Set(converted.Deref())
					}
					args[i] = value
				} else {
					args[i] = converted
				}
			} else {
				args[i] = converted
			}
			continue
		}
		if thrown := coercionThrow(ctx); thrown != nil {
			// __toString threw while rendering the argument. The call never
			// happens and the throw is what the program sees.
			return thrown
		}
		if !strict && scalarOnlyType(arg.GetType()) &&
			(value.Deref().Kind() == php.KindNull || value.Deref().Kind() == php.KindUndef) {
			// In weak mode an internal function takes null for a non-nullable
			// *scalar* parameter and only deprecates it, where a user function
			// throws. The allowance comes from the ZPP macros that coerce, so
			// it stops at bool|int|float|string: `object`, `array` and a class
			// type refuse null outright. Strict mode removes the difference.
			ctx.Deprecated("%s(): Passing null to parameter #%d ($%s) of type %s is deprecated",
				signature.GetName(), i+1, arg.GetName(), arg.GetType())
			continue
		}
		return in.throwError(ctx, unit, "TypeError",
			"%s(): %s must be of type %s, %s given",
			signature.GetName(), php.ArgLabel(i+1, argName), arg.GetType(), givenTypeName(value))
	}
	return nil
}

// scalarOnlyType reports whether every atom of a declared type is one the
// engine's parameter parsing will coerce null into. Only those parameters get
// the null deprecation; anything else refuses null with a TypeError.
func scalarOnlyType(declared string) bool {
	if declared == "" {
		return false
	}
	for _, atom := range strings.Split(strings.TrimPrefix(declared, "?"), "|") {
		switch strings.ToLower(strings.TrimSpace(atom)) {
		case "bool", "int", "float", "string":
		default:
			return false
		}
	}
	return true
}

// verifyBuiltinArity rejects a call with the wrong number of arguments, which
// an internal function reports before it looks at any of them. The three
// wordings and which count they name come from the engine's own formatting:
// "exactly" when there is no choice, "at least" when too few arrived, "at most"
// when too many did, and the number is whichever bound was missed.
func (in *Interp) verifyBuiltinArity(ctx *php.Ctx, unit *Unit, signature *pb.Builtin, args []php.Value) error {
	for _, value := range args {
		if value.IsUndef() {
			// A hole means named arguments skipped an optional parameter, and
			// the defaults that fill them have not been applied yet. Counting
			// slots here would report an arity the call does not have.
			return nil
		}
	}
	given := len(args)
	least := int(signature.GetRequiredArgs())
	if parsed := builtinMinimumArgs[strings.ToLower(signature.GetName())]; parsed > least {
		least = parsed
	}
	most := len(signature.GetArgs())
	if signature.GetVariadic() {
		if given >= least {
			return nil
		}
		most = -1
	}
	// **One home for the rule.** The wording and the choice between
	// `exactly`/`at least`/`at most` live in `php.CheckBuiltinArity`, so the
	// compiled back-end states them the same way rather than growing its own
	// copy -- which is how this check came to exist in one back-end only.
	if msg, bad := php.BuiltinArityError(signature.GetName(), given, least, most); bad {
		return in.throwError(ctx, unit, "ArgumentCountError", "%s", msg)
	}
	return nil
}

// A handful of ext/standard variadics declare only their first array in the
// stub, then parse one or two trailing callbacks themselves. The parser's
// minimum, not the deliberately wider reflection signature, determines the
// ArgumentCountError.
var builtinMinimumArgs = map[string]int{
	"array_diff_uassoc":       2,
	"array_diff_ukey":         2,
	"array_intersect_uassoc":  2,
	"array_intersect_ukey":    2,
	"array_udiff":             2,
	"array_udiff_assoc":       2,
	"array_udiff_uassoc":      3,
	"array_uintersect":        2,
	"array_uintersect_assoc":  2,
	"array_uintersect_uassoc": 3,
}

// ownArgumentDiagnostics are the functions whose parameter is declared wider
// than what they accept, so the refusal is theirs to word rather than the
// signature's. get_parent_class takes `object|string|null` and rejects every
// string that does not name a class -- and null, which is its own default.
var ownArgumentDiagnostics = map[string]bool{"get_parent_class": true}

// functionLabel names a function the way a diagnostic about its signature does.
// functionLabel names the callee for an *argument* diagnostic, where the engine
// builds "Class::method" as one string and interpolates it with `%s` -- so a
// method of an anonymous class loses the `::method` along with everything else
// after the NUL: `class@anonymous(): Argument #1 ...`.
func functionLabel(f *frame, op *pb.OpArray) string {
	if f.class != nil {
		return php.CutAtNul(f.class.Name + "::" + op.GetName())
	}
	return op.GetName()
}

// functionLabelScoped names the callee for a *return* diagnostic, where the
// class, the separator and the method are three separate arguments -- so only
// the class name is cut and `class@anonymous::test()` survives whole. The
// difference between the two is exactly where the NUL falls in a printf.
func functionLabelScoped(f *frame, op *pb.OpArray) string {
	if f.class != nil {
		return php.DisplayName(f.class.Name) + "::" + op.GetName()
	}
	return op.GetName()
}

// displayType renders a declared type the way a diagnostic shows it. Each class
// name is cut at its own NUL rather than the whole string being cut, so an
// anonymous class in a union leaves the rest of the union standing:
// `class@anonymous|string`. `resolve_class_name` does the same, and says why --
// otherwise the printer would drop everything after it.
func displayType(declared string) string {
	if !strings.ContainsRune(declared, 0) {
		return declared
	}
	var out strings.Builder
	start := 0
	for i := 0; i <= len(declared); i++ {
		if i < len(declared) && !strings.ContainsRune("?|&()", rune(declared[i])) {
			continue
		}
		out.WriteString(php.CutAtNul(declared[start:i]))
		if i < len(declared) {
			out.WriteByte(declared[i])
		}
		start = i + 1
	}
	return out.String()
}

// coercionThrow reports an exception a coercion raised -- only __toString can
// -- so a caller about to report a type error yields to it instead. The engine
// never reports both: the conversion did not happen, and the throw is what the
// program sees.
func coercionThrow(ctx *php.Ctx) error {
	if ctx == nil {
		return nil
	}
	if pending := ctx.PendingError(); pending != nil {
		return ctx.TakePendingError()
	}
	return nil
}

// calledAt reports where the call was made, which an argument TypeError names
// and a return TypeError does not. The innermost stack frame is the call site,
// since a frame records where the call happened rather than where the callee is.
func (f *frame) calledAt(ctx *php.Ctx) string {
	if f != nil && f.callerInternal {
		// A trampoline the engine built stands between the script and this
		// frame, so there is no call site to name.
		return ""
	}
	stack := ctx.CallStack()
	if len(stack) == 0 {
		return ""
	}
	site := stack[len(stack)-1]
	if site.File == php.InternalFrameFile {
		// The engine made the call -- a fiber's entry, a callback -- so there
		// is no script location to name and PHP leaves the clause off.
		return ""
	}
	return fmt.Sprintf(", called in %s on line %d", site.File, site.Line)
}

// checkType verifies a value against a declared type, coercing it when the
// caller is not in strict mode. It returns the value to use and whether the
// type was satisfied.
func (in *Interp) checkType(ctx *php.Ctx, unit *Unit, declared string, value php.Value, strict bool) (php.Value, bool) {
	if declared == "" || declared == "mixed" {
		return value, true
	}
	value = value.Deref()

	// A declared type is a disjunction of conjunctions: `int|string` has one
	// atom per alternative, `(X&Y)|Z` has two of one and one of two. A value
	// satisfies the type when it satisfies every atom of *some* alternative.
	conjunctions := typeConjunctions(declared)
	var alternatives []string
	for _, conjunction := range conjunctions {
		if len(conjunction) == 1 {
			// Only a single-atom alternative can be reached by coercion; an
			// intersection names classes, and nothing converts to one.
			alternatives = append(alternatives, conjunction[0])
		}
	}

	// An exact match wins over any conversion, which is what makes
	// f(int|string $v) keep a string as a string.
	for _, conjunction := range conjunctions {
		if in.matchesAll(ctx, unit, conjunction, value) {
			return value, true
		}
	}
	// An object satisfies a string declaration when it can render itself --
	// but only in weak mode. `__toString` is a coercion like any other, and
	// `declare(strict_types=1)` refuses it for an argument, a return and a
	// property alike. Probed all three.
	if !strict && value.Kind() == php.KindObject && contains(alternatives, "string") {
		if text, ok := in.stringify(ctx, unit, value); ok {
			return text, true
		}
	}
	// An int satisfies a float declaration in strict mode too, but as a float:
	// the value widens rather than passing through unchanged.
	if value.Kind() == php.KindInt && contains(alternatives, "float") {
		return php.Float(value.Float()), true
	}
	if strict {
		return value, false
	}
	// A *numeric string* keeps the type it parses to, when the union offers
	// it: `"42.0"` against `int|float` is a float and `"42"` is an int, where
	// the fixed order below would make both ints. Only the string source has
	// this rule -- everything else follows the order.
	if value.Kind() == php.KindString {
		if numeric, ok := value.IsNumericString(); ok {
			parsed := "int"
			if numeric.Kind() == php.KindFloat {
				parsed = "float"
			}
			if contains(alternatives, parsed) {
				if converted, ok := coerceScalar(ctx, parsed, value); ok {
					return converted, true
				}
			}
		}
	}
	// Weak mode converts, preferring the alternatives in the order PHP does.
	for _, alternative := range []string{"int", "float", "string", "bool"} {
		if !contains(alternatives, alternative) {
			continue
		}
		if converted, ok := coerceScalar(ctx, alternative, value); ok {
			return converted, true
		}
	}
	return value, false
}

// acceptsCallable reports whether one alternative of a declared type is
// `callable`, and admitsNull whether one is null. Together they say which
// wording an internal function's callback diagnostic takes: `?callable` asks
// for "a valid callback or null", a bare `callable` for "a valid callback".
func acceptsCallable(declared string) bool {
	for _, conjunction := range typeConjunctions(declared) {
		if len(conjunction) == 1 && conjunction[0] == "callable" {
			return true
		}
	}
	return false
}

func admitsNull(declared string) bool {
	for _, conjunction := range typeConjunctions(declared) {
		if len(conjunction) == 1 && (conjunction[0] == "null" || conjunction[0] == "mixed") {
			return true
		}
	}
	return false
}

// typeConjunctions splits a declared type into disjunctive normal form. `?T` is
// the shorthand for `T|null`, and a parenthesised group is one conjunction --
// PHP only ever nests that one level, which is what makes DNF the whole story.
func typeConjunctions(declared string) [][]string {
	nullable := strings.HasPrefix(declared, "?")
	declared = strings.TrimPrefix(declared, "?")
	var parts []string
	depth, start := 0, 0
	for i := 0; i < len(declared); i++ {
		switch declared[i] {
		case '(':
			depth++
		case ')':
			depth--
		case '|':
			if depth == 0 {
				parts = append(parts, declared[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, declared[start:])
	var conjunctions [][]string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		part = strings.TrimSuffix(strings.TrimPrefix(part, "("), ")")
		var atoms []string
		for _, atom := range strings.Split(part, "&") {
			if atom = strings.TrimSpace(atom); atom != "" {
				atoms = append(atoms, atom)
			}
		}
		if len(atoms) > 0 {
			conjunctions = append(conjunctions, atoms)
		}
	}
	if nullable {
		conjunctions = append(conjunctions, []string{"null"})
	}
	return conjunctions
}

// matchesAll reports whether a value satisfies every atom of one conjunction,
// which for more than one atom means being an instance of each named class.
// checkConstantType is the type check a *class constant* gets, which is not the
// one a parameter or a property gets. Probed: nothing is coerced -- not a
// numeric string to int, not an object with __toString to string, and the
// object's __toString is not even called -- except the one widening the
// language always allows, int to float. `static` names the declaring class.
func (in *Interp) checkConstantType(ctx *php.Ctx, unit *Unit, declared string,
	owner *Class, value php.Value) (php.Value, bool) {
	if declared == "" || declared == "mixed" {
		return value, true
	}
	value = value.Deref()
	widening := false
	for _, conjunction := range typeConjunctions(declared) {
		resolved := make([]string, len(conjunction))
		for i, atom := range conjunction {
			if strings.EqualFold(atom, "static") || strings.EqualFold(atom, "self") {
				// A constant declared `static` holds an instance of the class
				// it was declared in and nothing else.
				if owner == nil {
					return value, false
				}
				atom = owner.Name
			}
			resolved[i] = atom
			if len(conjunction) == 1 && strings.EqualFold(atom, "float") {
				widening = true
			}
		}
		if in.matchesAll(ctx, unit, resolved, value) {
			return value, true
		}
	}
	if widening && value.Kind() == php.KindInt {
		return php.Float(float64(value.Int())), true
	}
	return value, false
}

// frameType resolves the keywords a declared type may use against the frame it
// is being checked in. `self` is the class whose body is running, `static` is
// the class the call was made on, and `parent` is the first's parent -- the
// same three answers `classNameFrom` gives an operand. Away from a class body
// they resolve to nothing and are left as written, which is how they were
// treated before there was a frame to ask.
func frameType(declared string, f *frame) string {
	lower := strings.ToLower(declared)
	if !strings.Contains(lower, "self") && !strings.Contains(lower, "static") &&
		!strings.Contains(lower, "parent") {
		return declared
	}
	var out strings.Builder
	start := 0
	for i := 0; i <= len(declared); i++ {
		if i < len(declared) && !strings.ContainsRune("?|&()", rune(declared[i])) {
			continue
		}
		atom := declared[start:i]
		switch strings.ToLower(atom) {
		case "self":
			if f != nil && f.class != nil {
				atom = f.class.Name
			}
		case "static":
			if f != nil {
				if called, ok := f.calledClass(); ok {
					atom = called
				}
			}
		case "parent":
			if f != nil && f.class != nil && f.class.Parent != "" {
				atom = f.class.Parent
			}
		}
		out.WriteString(atom)
		if i < len(declared) {
			out.WriteByte(declared[i])
		}
		start = i + 1
	}
	return out.String()
}

func (in *Interp) matchesAll(ctx *php.Ctx, unit *Unit, atoms []string, value php.Value) bool {
	for _, atom := range atoms {
		if !in.matchesType(ctx, unit, atom, value) {
			return false
		}
	}
	return true
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

// matchesType reports whether a value already satisfies one alternative of a
// declared type, with no conversion.
func (in *Interp) matchesType(ctx *php.Ctx, unit *Unit, declared string, value php.Value) bool {
	switch declared {
	case "mixed":
		return true
	case "null", "void", "never":
		return value.Kind() == php.KindNull || value.Kind() == php.KindUndef
	case "bool":
		return value.Kind() == php.KindTrue || value.Kind() == php.KindFalse
	case "true":
		return value.Kind() == php.KindTrue
	case "false":
		return value.Kind() == php.KindFalse
	case "int":
		return value.Kind() == php.KindInt
	case "float":
		return value.Kind() == php.KindFloat
	case "string":
		return value.Kind() == php.KindString
	case "array":
		return value.Kind() == php.KindArray
	case "object":
		return value.Kind() == php.KindObject
	case "callable":
		// A string, an array pair or an object is only callable when it names
		// something callable, so the check is the lookup a call would do. The
		// reason it failed is not reported here: a parameter typed `callable`
		// earns the ordinary "must be of type callable" wording, and only an
		// internal function's dedicated callback check spells out why.
		switch value.Kind() {
		case php.KindString, php.KindArray, php.KindObject:
			return in.callableReasonQuiet(ctx, unit, value, true) == ""
		}
		return false
	case "iterable":
		if value.Kind() == php.KindArray {
			return true
		}
		return in.isInstanceOfName(unit, value, "Traversable")
	case "static", "self", "parent":
		// The check sites resolve these against the frame before asking, so a
		// keyword arriving here is one no class scope could name -- a closure
		// declared `: static` and never bound, say. Nothing satisfies it, and
		// the diagnostic keeps the word as written.
		return false
	}
	return in.isInstanceOfName(unit, value, declared)
}

// isInstanceOfName answers a class-typed declaration.
func (in *Interp) isInstanceOfName(unit *Unit, value php.Value, name string) bool {
	object := value.Object()
	if object == nil {
		return false
	}
	if strings.EqualFold(object.ClassName, name) {
		return true
	}
	class, ok := unit.findClass(object.ClassName)
	if !ok {
		return false
	}
	return unit.instanceOf(class, name)
}

// coerceScalar performs one weak-mode conversion. It reports false when PHP
// would refuse -- a non-numeric string to int, anything from null, an array or
// an object without __toString.
func coerceScalar(ctx *php.Ctx, declared string, value php.Value) (php.Value, bool) {
	switch value.Kind() {
	case php.KindNull, php.KindUndef, php.KindArray:
		// null is only accepted by a nullable declaration, which the exact
		// match already answered; an array converts to nothing.
		return value, false
	case php.KindObject:
		// Only __toString makes an object a string, and the runtime handles
		// that conversion where the object is: refusing here would be wrong
		// for a class that has one, so it is left unconverted.
		return value, false
	}

	switch declared {
	case "int":
		switch value.Kind() {
		case php.KindTrue, php.KindFalse:
			return php.Int(value.Int()), true
		case php.KindFloat:
			if !doubleFitsLong(value.Float()) {
				return value, false
			}
			return truncateToInt(ctx, value.Float(), ""), true
		case php.KindString:
			text := strings.TrimSpace(value.String())
			if whole, err := strconv.ParseInt(text, 10, 64); err == nil {
				return php.Int(whole), true
			}
			if fractional, err := strconv.ParseFloat(text, 64); err == nil {
				if !doubleFitsLong(fractional) {
					return value, false
				}
				return truncateToInt(ctx, fractional, value.String()), true
			}
			return value, false
		}
	case "float":
		switch value.Kind() {
		case php.KindTrue, php.KindFalse, php.KindInt:
			return php.Float(value.Float()), true
		case php.KindString:
			if number, err := strconv.ParseFloat(strings.TrimSpace(value.String()), 64); err == nil {
				return php.Float(number), true
			}
			return value, false
		}
	case "string":
		switch value.Kind() {
		case php.KindTrue, php.KindFalse, php.KindInt, php.KindFloat:
			return php.String(value.String()), true
		}
	case "bool":
		switch value.Kind() {
		case php.KindInt, php.KindFloat, php.KindString:
			return php.Bool(value.Bool()), true
		}
	}
	return value, false
}

// doubleFitsLong is ZEND_DOUBLE_FITS_LONG: a float outside the integer range,
// or a NaN, is not converted at all. Losing the magnitude is a refusal, where
// losing only the fraction is the deprecation truncateToInt reports.
func doubleFitsLong(number float64) bool {
	if math.IsNaN(number) {
		return false
	}
	return number < 9223372036854775808.0 && number >= -9223372036854775808.0
}

// truncateToInt narrows a float to an int, warning when that loses information.
// The wording differs for a float and for a string holding one, which is the
// only way a test can tell which path it took.
func truncateToInt(ctx *php.Ctx, number float64, fromString string) php.Value {
	whole := int64(number)
	if float64(whole) != number {
		if fromString != "" {
			ctx.Deprecated("Implicit conversion from float-string %q to int loses precision", fromString)
		} else {
			ctx.Deprecated("Implicit conversion from float %s to int loses precision",
				php.Float(number).String())
		}
	}
	return php.Int(whole)
}

// givenTypeName names a value the way a TypeError does: an object is reported
// by its class, not as "object".
func givenTypeName(value php.Value) string {
	value = value.Deref()
	switch value.Kind() {
	case php.KindTrue:
		// A boolean is reported by its singleton type, not as "bool".
		return "true"
	case php.KindFalse:
		return "false"
	case php.KindObject:
		return php.DisplayName(value.Object().ClassName)
	}
	return phpTypeName(value)
}

// verifyStaticPropertyWrite is the same check for `C::$p = v`. The declaration
// comes from the class rather than the object, but the rule and the wording are
// the property one.
func (in *Interp) verifyStaticPropertyWrite(ctx *php.Ctx, unit *Unit, className, name string,
	value php.Value, strict bool) (php.Value, error) {
	class, known := unit.findClass(className)
	if !known {
		return value, nil
	}
	prop, found := unit.staticPropDecl(class, name)
	if !found || prop.GetType() == "" {
		return value, nil
	}
	converted, ok := in.checkType(ctx, unit, prop.GetType(), value.Deref(), strict)
	if ok {
		return converted, nil
	}
	if thrown := coercionThrow(ctx); thrown != nil {
		return value, thrown
	}
	owner := prop.GetDeclaringClass()
	if owner == "" {
		owner = class.Name
	}
	return value, in.throwError(ctx, unit, "TypeError",
		"Cannot assign %s to property %s::$%s of type %s",
		givenTypeName(value.Deref()), php.DisplayName(owner), name, prop.GetType())
}

// verifyPropertyWrite applies a typed property's declared type to a value being
// assigned to it: weak mode coerces, strict mode does not, and either way a
// value the type does not admit is a TypeError naming the property.
func (in *Interp) verifyPropertyWrite(ctx *php.Ctx, unit *Unit, object *php.Object,
	name string, value php.Value, strict bool) (php.Value, error) {
	if object == nil {
		return value, nil
	}
	declaration := object.Declared[name]
	declared := declaration.Type
	if declared == "" || object.Dynamic[name] {
		return value, nil
	}
	if declaration.Visibility == "private" {
		// A private property of an *ancestor* is not part of the object's
		// visible table from anywhere else, so writing that name creates a
		// property alongside it rather than into it -- and the declared type
		// does not apply. `class a extends Exception { ... $this->string = null; }`
		// is exactly that: Exception::$string is private and typed, and the
		// subclass's write is a dynamic property.
		scope := unit.callingScope(ctx)
		if scope == nil || !strings.EqualFold(scope.Name, declaration.Class) {
			return value, nil
		}
	}
	owner := object.Declared[name].Class
	if owner == "" {
		owner = object.ClassName
	}
	// `self` in a property type is the class that declared the property. The
	// diagnostic keeps the word as written -- `of type PA|self` -- so only the
	// check is resolved.
	resolved := declared
	if declaringClass, known := unit.findClass(owner); known {
		resolved = frameType(declared, &frame{class: declaringClass})
	}
	converted, ok := in.checkType(ctx, unit, resolved, value.Deref(), strict)
	if ok {
		return converted, nil
	}
	if thrown := coercionThrow(ctx); thrown != nil {
		return value, thrown
	}
	return value, in.throwError(ctx, unit, "TypeError",
		"Cannot assign %s to property %s::$%s of type %s",
		givenTypeName(value.Deref()), php.DisplayName(owner), name, declared)
}

// magicReadType applies a declared property's type to what __get answered for
// it. Unsetting a typed property does not remove its declaration, so the
// accessor standing in for it is still held to the type -- which is why a
// __get returning "42" for an `int $val` reads back as int(42). A property the
// caller cannot see is a different matter: the declaration is not part of what
// that scope was promised, so nothing is enforced. The engine
// words the failure as a return-value problem rather than an assignment,
// because no assignment took place.
func (in *Interp) magicReadType(ctx *php.Ctx, unit *Unit, object *php.Object,
	name string, value php.Value, visible, strict bool) (php.Value, error) {
	if object == nil || !visible {
		return value, nil
	}
	declared, isDeclared := object.Declared[name]
	if !isDeclared || declared.Type == "" || object.Dynamic[name] {
		return value, nil
	}
	if converted, ok := in.checkType(ctx, unit, declared.Type, value.Deref(), strict); ok {
		return converted, nil
	}
	owner := declared.Class
	if owner == "" {
		owner = object.ClassName
	}
	return value, in.throwError(ctx, unit, "TypeError",
		"Value of type %s returned from %s::__get() must be compatible with "+
			"unset property %s::$%s of type %s",
		givenTypeName(value.Deref()), php.DisplayName(object.ClassName),
		php.DisplayName(owner), name, declared.Type)
}

// verifyRefWrite applies the types of every property a reference is shared with
// to a value being written through it. The engine keeps these as the
// reference's "type sources"; the diagnostic names the property rather than the
// name being assigned, since the name itself has no type.
func (in *Interp) verifyRefWrite(ctx *php.Ctx, unit *Unit, ref *php.Ref,
	value php.Value, strict bool) (php.Value, error) {
	sources := ref.Sources()
	converted := value
	first := -1
	for i, source := range sources {
		// Each property is asked about the value that was written, not about
		// what the property before it made of it: two declarations that would
		// each coerce it differently are in conflict, and chaining would hide
		// that by handing the second one the first one's answer.
		next, ok := in.checkType(ctx, unit, source.Type, value.Deref(), strict)
		if !ok {
			return value, in.throwError(ctx, unit, "TypeError",
				"Cannot assign %s to reference held by property %s::$%s of type %s",
				givenTypeName(value.Deref()), php.DisplayName(source.Class),
				source.Name, source.Type)
		}
		if first < 0 {
			first, converted = i, next
			continue
		}
		if next.Deref().Kind() == converted.Deref().Kind() {
			continue
		}
		// One box cannot hold two different values. The engine names both
		// properties, in the order they were bound.
		return value, in.throwError(ctx, unit, "TypeError",
			"Cannot assign %s to reference held by property %s::$%s of type %s "+
				"and property %s::$%s of type %s, as this would result in an "+
				"inconsistent type conversion",
			givenTypeName(value.Deref()),
			php.DisplayName(sources[first].Class), sources[first].Name, sources[first].Type,
			php.DisplayName(source.Class), source.Name, source.Type)
	}
	return converted, nil
}

// verifyRefStep is the check `++` and `--` make. Overflowing an int property's
// range is reported as a step past the type's limit rather than as an
// assignment of the float it produced, and the engine decides that on the shape
// of the step alone: an int that became a float, held by a source that does not
// accept floats.
func (in *Interp) verifyRefStep(ctx *php.Ctx, unit *Unit, ref *php.Ref,
	old, updated php.Value, opcode string, strict bool) (php.Value, error) {
	if old.Deref().Kind() == php.KindInt && updated.Deref().Kind() == php.KindFloat {
		for _, source := range ref.Sources() {
			if _, ok := in.checkType(ctx, unit, source.Type, updated.Deref(), true); ok {
				continue
			}
			verb, limit := "increment", "maximal"
			if strings.Contains(opcode, "_DEC") {
				verb, limit = "decrement", "minimal"
			}
			return old, in.throwError(ctx, unit, "TypeError",
				"Cannot %s a reference held by property %s::$%s of type %s past its %s value",
				verb, php.DisplayName(source.Class), source.Name, source.Type, limit)
		}
	}
	return in.verifyRefWrite(ctx, unit, ref, updated, strict)
}

// typedRef returns the box a slot holds when that box carries type sources, so
// a write through it has to be checked first.
func (f *frame) typedRef(kind, operand uint32) *php.Ref {
	if kind != opCV && kind != opVar && kind != opTmpVar {
		return nil
	}
	if int(operand) >= len(f.slots) {
		return nil
	}
	ref, ok := f.slots[operand].RefOf()
	if !ok || len(ref.Sources()) == 0 {
		return nil
	}
	return ref
}

// typedRefHeld returns the box an array element or object property holds when
// that box carries type sources. A write into the container reaches through it,
// so the constraint applies even though the name being assigned has no type.
func typedRefHeld(array *php.Array, key php.Key) *php.Ref {
	ref := array.RefHeld(key)
	if ref == nil || len(ref.Sources()) == 0 {
		return nil
	}
	return ref
}

// verifyPropertyStep is what `++` and `--` face on a typed property: an int
// that overflowed into a float is reported as a step past the type's limit
// rather than as an assignment the type refuses.
func (in *Interp) verifyPropertyStep(ctx *php.Ctx, unit *Unit, object *php.Object,
	name string, old, updated php.Value, opcode string, strict, viaRef bool) (php.Value, error) {
	declared := object.Declared[name].Type
	if declared == "" || object.Dynamic[name] {
		return updated, nil
	}
	if old.Deref().Kind() == php.KindInt && updated.Deref().Kind() == php.KindFloat {
		if _, ok := in.checkType(ctx, unit, declared, updated.Deref(), true); !ok {
			verb, limit := "increment", "maximal"
			if strings.Contains(opcode, "_DEC") {
				verb, limit = "decrement", "minimal"
			}
			owner := object.Declared[name].Class
			if owner == "" {
				owner = object.ClassName
			}
			// Once the slot holds a reference, the step is reported as one
			// through that reference -- even though the name being stepped is
			// the property's own. A plain assignment to the same name keeps
			// the property wording; only the overflow changes.
			held := ""
			if viaRef {
				held = "a reference held by "
			}
			return old, in.throwError(ctx, unit, "TypeError",
				"Cannot %s %sproperty %s::$%s of type %s past its %s value",
				verb, held, php.DisplayName(owner), name, declared, limit)
		}
	}
	return in.verifyPropertyWrite(ctx, unit, object, name, updated, strict)
}

// notePropertyType records a typed property's declaration on a reference,
// which runtime/php owns along with the reference.
func notePropertyType(object *php.Object, name string, ref *php.Ref) *php.Ref {
	return php.NotePropertyType(object, name, ref)
}

// adoptPropertyTypes registers a copy's own claim on every reference its
// properties share. Cloning binds the same box to a second object's slot, and
// the engine counts that as a second type source -- so dropping the original's
// when the original goes leaves the copy's in place.
func adoptPropertyTypes(object *php.Object) {
	if object == nil {
		return
	}
	for _, key := range object.Props.Keys() {
		if held := object.Props.RefHeld(key); held != nil {
			notePropertyType(object, key.String(), held)
		}
	}
}

// refBindingConflict is the error binding a reference to a typed property
// raises when the box being bound is already another typed property's storage
// and the value in it does not fit the destination outright.
//
// The engine's order, probed rather than inferred: a value the destination type
// cannot take at all is reported as a plain assignment to the property, because
// there is nothing to argue about; a value it *could* coerce is refused with
// this message instead, since coercing would change what the property already
// holding the box sees.
func (in *Interp) refBindingConflict(ctx *php.Ctx, unit *Unit, source *php.Ref,
	owner, name, declared string, strict bool) error {
	if source == nil || declared == "" || len(source.Sources()) == 0 {
		return nil
	}
	value := source.Get().Deref()
	// "Fits outright" means the declaration takes the value as it stands. Even
	// strict mode widens an int to a float, and that widening is a conversion
	// like any other here -- what the box holds would have to change.
	if next, ok := in.checkType(ctx, unit, declared, value, true); ok &&
		next.Deref().Kind() == value.Kind() {
		return nil
	}
	if _, ok := in.checkType(ctx, unit, declared, value, strict); !ok {
		return nil
	}
	held := source.Sources()[0]
	return in.throwError(ctx, unit, "TypeError",
		"Reference with value of type %s held by property %s::$%s of type %s "+
			"is not compatible with property %s::$%s of type %s",
		givenTypeName(value), php.DisplayName(held.Class), held.Name, held.Type,
		php.DisplayName(owner), name, declared)
}

// staticRefBindingConflict is refBindingConflict for a static property, whose
// declaration is looked up on the class rather than on an instance.
func (in *Interp) staticRefBindingConflict(ctx *php.Ctx, unit *Unit,
	className, name string, source *php.Ref, strict bool) error {
	class, known := unit.findClass(className)
	if !known {
		return nil
	}
	prop, found := unit.staticPropDecl(class, name)
	if !found || prop.GetType() == "" {
		return nil
	}
	owner := prop.GetDeclaringClass()
	if owner == "" {
		owner = class.Name
	}
	return in.refBindingConflict(ctx, unit, source, owner, name, prop.GetType(), strict)
}

// noteStaticPropType is notePropertyType for a static property: the box taking
// the property's place as its storage carries that property's declared type and
// nothing else. The types of other properties that happened to share the box it
// replaces stay with the box they belong to.
func (u *Unit) noteStaticPropType(className, name string, ref *php.Ref) {
	if ref == nil {
		return
	}
	class, known := u.findClass(className)
	if !known {
		return
	}
	prop, found := u.staticPropDecl(class, name)
	if !found || prop.GetType() == "" {
		return
	}
	owner := prop.GetDeclaringClass()
	if owner == "" {
		owner = class.Name
	}
	ref.AddSource(php.RefSource{Class: owner, Name: name, Type: prop.GetType()})
}

// refuseUninitializedRef is the Error taking a reference to a typed property
// that holds nothing must raise. A reference has to point at a value and there
// is none, so a non-nullable property refuses outright -- while a nullable one
// takes null and hands the box out, which is why `$r = &$o->n` leaves `?int $n`
// holding NULL.
//
// The box is asked rather than the property: by the time a reference is being
// bound, what is known about where it came from is the sources recorded on it.
func (in *Interp) refuseUninitializedRef(ctx *php.Ctx, unit *Unit, slot php.Value) error {
	ref, isRef := slot.RefOf()
	if !isRef || ref == nil || !ref.Get().Deref().IsUndef() {
		return nil
	}
	for _, source := range ref.Sources() {
		if acceptsNull(source.Type) {
			continue
		}
		return in.throwError(ctx, unit, "Error",
			"Cannot access uninitialized non-nullable property %s::$%s by reference",
			php.DisplayName(source.Class), source.Name)
	}
	return nil
}

// acceptsNull reports whether a declared type takes null, which is what decides
// whether an uninitialized property can be handed out by reference: the box has
// to hold something, and null is the only thing there is to put in it.
func acceptsNull(declared string) bool {
	for _, atoms := range typeConjunctions(declared) {
		if len(atoms) != 1 {
			continue
		}
		if atoms[0] == "null" || atoms[0] == "mixed" {
			return true
		}
	}
	return false
}

// noteCallableForm reports what the *shape* of a callable earns when one is
// accepted for a `callable` parameter. Checking the type is not a bare shape
// test in the engine: it resolves the callable, and resolving is what reports
// `Use of "self" in callables is deprecated` and the compound-array form. A
// `callable` type hint therefore says the same things call_user_func would.
func (in *Interp) noteCallableForm(ctx *php.Ctx, unit *Unit, declared string, value php.Value) {
	switch value.Deref().Kind() {
	case php.KindString, php.KindArray:
	default:
		return
	}
	for _, atoms := range typeConjunctions(declared) {
		for _, atom := range atoms {
			if strings.EqualFold(atom, "callable") {
				unit.dynamicTarget(ctx, value.Deref())
				return
			}
		}
	}
}

// verifyStaticPropStep is verifyPropertyStep for a static property, which has
// no instance to read the declaration off. Stepping past the type's limit is
// reported as a write to the property -- the "reference held by" wording is for
// a write through a name *bound* to one, and a static property's own slot is
// not that.
func (in *Interp) verifyStaticPropStep(ctx *php.Ctx, unit *Unit, className, name string,
	old, updated php.Value, opcode string, strict bool) (php.Value, error) {
	if old.Deref().Kind() == php.KindInt && updated.Deref().Kind() == php.KindFloat {
		class, known := unit.findClass(className)
		if known {
			if decl, found := unit.staticPropDecl(class, name); found && decl.GetType() != "" {
				if _, ok := in.checkType(ctx, unit, decl.GetType(), updated.Deref(), true); !ok {
					verb, limit := "increment", "maximal"
					if strings.Contains(opcode, "_DEC") {
						verb, limit = "decrement", "minimal"
					}
					owner := decl.GetDeclaringClass()
					if owner == "" {
						owner = class.Name
					}
					return old, in.throwError(ctx, unit, "TypeError",
						"Cannot %s property %s::$%s of type %s past its %s value",
						verb, php.DisplayName(owner), name, decl.GetType(), limit)
				}
			}
		}
	}
	return in.verifyStaticPropertyWrite(ctx, unit, className, name, updated, strict)
}
