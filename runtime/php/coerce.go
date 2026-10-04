package php

import (
	"fmt"
	"math"
	"strings"
)

// Coercing a value to a declared parameter or return type.
//
// **Parameter coercion is not arithmetic coercion**, and reusing the arithmetic
// path would be the natural mistake. `"5abc"` passed to an `int` parameter is a
// TypeError; the same string in `"5abc" + 0` is `5` with a
// `A non-numeric value encountered` warning. Every row below was read back from
// the engine by calling a typed function and printing what arrived.
//
// A declared type **transforms** as well as verifies -- `f(float $x)` called
// with `5` must make `$x` a float -- so dropping one corrupts a value rather
// than merely skipping a diagnostic.

// DeclaredType is a parsed scalar type declaration.
type DeclaredType struct {
	Name     string // "int", "float", "string", "bool", "array", "object", or ""
	Nullable bool
	// VerifyOnly marks a type that rejects but never converts.
	VerifyOnly bool
	// Union holds the alternatives of a scalar union type, in declaration
	// order, with any `null` already folded into Nullable. Empty for a single
	// type. A union naming a class is not one of these -- it is left to
	// declaredClassRejects, which reasons about classes and not conversion.
	Union []string
}

// ParseDeclaredType reads a type declaration, reporting whether it is one this
// package can coerce. Anything else -- a class name, a union, `iterable` --
// verifies without transforming, so it is left alone rather than guessed at.
// parseDeclaredFor is ParseDeclaredType for a particular value. A union that
// names a class cannot be coerced to as a whole, but a **non-object** can
// never satisfy its class members, so for one the union is its scalar part:
// `X|Y|int` given "42" is int(42) in weak mode, as the engine gives it, where
// leaving the whole union alone stored the string. A value no scalar member
// takes is still refused, naming the declaration as written. `callable` and
// `mixed` keep the old answer: they accept non-objects this cannot judge.
func parseDeclaredFor(declared string, v Value) (DeclaredType, bool) {
	d, ok := ParseDeclaredType(declared)
	if ok || !strings.Contains(declared, "|") || v.Deref().Kind() == KindObject {
		return d, ok
	}
	var kept []string
	for _, alt := range strings.Split(declared, "|") {
		alt = strings.TrimSpace(alt)
		bare := strings.TrimPrefix(alt, "?")
		switch strings.ToLower(bare) {
		case "int", "float", "string", "bool", "array", "object", "true", "false", "null":
			kept = append(kept, alt)
		case "iterable":
			kept = append(kept, strings.Replace(alt, bare, "array", 1))
		case "callable", "mixed":
			return d, ok
		}
		// Anything else -- a class, `self`, `static`, a DNF term -- takes
		// only objects, and this value is not one.
	}
	if len(kept) == 0 {
		return d, ok
	}
	return ParseDeclaredType(strings.Join(kept, "|"))
}

func ParseDeclaredType(declared string) (DeclaredType, bool) {
	d := DeclaredType{}
	if rest, ok := strings.CutPrefix(declared, "?"); ok {
		d.Nullable, declared = true, rest
	}
	switch declared {
	case "int", "float", "string", "bool":
		d.Name = declared
		return d, true
	case "array", "object":
		// **Verify-only.** These reject without transforming, so getting one
		// wrong loses a TypeError but never corrupts a value -- the opposite
		// risk profile from the scalars, and the reason they are separated.
		d.Name, d.VerifyOnly = declared, true
		return d, true
	case "null", "true", "false":
		// PHP 8.2's literal types, and each admits **exactly one value** with
		// no coercion at all: `public false $f` given `true` is a TypeError,
		// not a converted `false`. Verify-only for the same reason as above,
		// and read back from the engine rather than assumed -- a `bool`
		// declaration would have coerced here, and these are not `bool`.
		d.Name, d.VerifyOnly = declared, true
		return d, true
	}
	if strings.Contains(declared, "|") {
		members := []string{}
		for _, alt := range strings.Split(declared, "|") {
			alt = strings.TrimSpace(alt)
			if rest, ok := strings.CutPrefix(alt, "?"); ok {
				d.Nullable, alt = true, rest
			}
			switch alt {
			case "null":
				d.Nullable = true
				continue
			case "int", "float", "string", "bool", "array", "object", "true", "false":
				members = append(members, alt)
			default:
				// A class name, `iterable`, `callable`, `mixed`: this package
				// cannot convert to it, so the whole union is left alone rather
				// than half-applied.
				return DeclaredType{}, false
			}
		}
		switch len(members) {
		case 0:
			return DeclaredType{}, false
		case 1:
			inner, ok := ParseDeclaredType(members[0])
			if !ok {
				return DeclaredType{}, false
			}
			inner.Nullable = inner.Nullable || d.Nullable
			return inner, true
		}
		d.Union = members
		return d, true
	}
	return d, false
}

// alreadyDeclared reports a value that a declaration passes through untouched
// and silently: a scalar already of the one scalar type declared, or anything
// against `mixed`. Every coercion entry point asks it first, because each
// parsed the declaration -- and the internal one split it into an allocated
// slice -- on every call, which was half the cost of calling a library
// function (`dechex` in bench.php's `hash1`). Only exact matches: an int
// against `float` converts, and `?int` given null still takes the full path.
func alreadyDeclared(v Value, declared string) bool {
	switch v.kind {
	case KindInt:
		return declared == "int" || declared == "?int" || declared == "mixed"
	case KindString:
		return declared == "string" || declared == "?string" || declared == "mixed"
	case KindFloat:
		return declared == "float" || declared == "?float" || declared == "mixed"
	case KindTrue, KindFalse:
		return declared == "bool" || declared == "?bool" || declared == "mixed"
	case KindArray:
		return declared == "array" || declared == "?array" || declared == "mixed"
	}
	return false
}

// declaredClassRejects reports whether a value **provably** fails a class type
// declaration.
//
// It is deliberately one-sided. Until now a class-typed parameter checked
// nothing at all, so anything this starts refusing had better be certain: a
// false rejection turns a working program into a TypeError, which is worse than
// the silent acceptance it replaces. So it answers true only when every part is
// known -- the declaration names one class, the class table is installed, the
// value is an object, and its class is unrelated to the declared one.
//
// Everything else is left alone: unions and intersections, `self`/`static`/
// `parent`, `iterable`, `callable`, `mixed`, an engine class the unit does not
// declare, and a non-object value, which the engine may still coerce.
//
// `nullIsDecided` says the **declaration is the whole truth about null**, which
// it is for a return type, a property and a user function's parameter -- the
// emitter folds a `= null` default into a leading `?` before calling, so a
// declaration that still lacks one cannot admit null. It is false for a library
// function's parameter, whose nullability comes from harvested arginfo rather
// than from this string, and where a wrong rejection would break a working
// call.
func declaredClassRejects(ctx *Ctx, v Value, declared string, nullIsDecided bool) bool {
	if ctx == nil || !ctx.HasClassTable() {
		return false
	}
	name := strings.TrimPrefix(declared, "?")
	if name == "" || strings.Contains(name, " ") {
		return false
	}
	if strings.Contains(name, "|") {
		// A union of **class names only**, matched against an object, is the
		// same provable question asked several times: it fails when no
		// alternative accepts. A union mentioning any scalar is left alone --
		// weak mode coerces across the alternatives in a preference order
		// (`int|string` given 1.5 stores int(1) with the precision
		// deprecation), and guessing at that would corrupt values rather than
		// merely miss a diagnostic.
		if v.Deref().Kind() != KindObject {
			return nonObjectRejectedByUnion(v.Deref(), declared)
		}
		for _, alt := range strings.Split(name, "|") {
			alt = strings.TrimSpace(strings.TrimPrefix(alt, "?"))
			// `null` in the union accepts no object.
			if strings.EqualFold(alt, "null") {
				continue
			}
			// A DNF term, `(X&Y)`, accepts an object satisfying every part.
			if strings.HasPrefix(alt, "(") && strings.HasSuffix(alt, ")") {
				all := true
				for _, part := range strings.Split(alt[1:len(alt)-1], "&") {
					part = strings.TrimSpace(part)
					if !isNamedClassType(part) {
						return false
					}
					if !ctx.InstanceOfClass(v, part) {
						all = false
					}
				}
				if all {
					return false
				}
				continue
			}
			// A scalar member **never takes an object**: `X|Y|int` given a
			// stdClass is refused when neither class accepts it. `string`
			// takes one only through `__toString` -- which is what makes a
			// class Stringable -- so a stdClass against `self|string` is
			// refused (union_types/anonymous_class), and a Stringable one is
			// left to the coercion. `object`, `iterable`, `callable` and
			// `mixed` stay undecided.
			switch strings.ToLower(alt) {
			case "int", "float", "bool", "array", "true", "false":
				continue
			case "string":
				if o := v.Deref().Object(); ctx.HasMethod(o.ClassName, "__tostring") ||
					ThrowableRoot(o.ClassName) != "" || ctx.InstanceOfClass(v, "Stringable") {
					return false
				}
				continue
			}
			if !isNamedClassType(alt) {
				return false
			}
			if ctx.InstanceOfClass(v, alt) {
				return false
			}
		}
		return true
	}
	if strings.Contains(name, "&") {
		// An intersection must satisfy **every** part, so one failure is
		// enough -- but only when all of them are class names.
		if v.Deref().Kind() != KindObject {
			return false
		}
		fails := false
		for _, alt := range strings.Split(name, "&") {
			alt = strings.TrimSpace(alt)
			if !isNamedClassType(alt) {
				return false
			}
			if !ctx.InstanceOfClass(v, alt) {
				fails = true
			}
		}
		return fails
	}
	// **`callable` refuses a scalar outright**: an int, a float or a bool
	// names nothing callable, whatever the class table holds. A string, an
	// array or an object needs the callable check, which this cannot make.
	if strings.EqualFold(name, "callable") {
		switch v.Deref().Kind() {
		case KindInt, KindFloat, KindTrue, KindFalse:
			return true
		case KindNull:
			return nullIsDecided && !strings.HasPrefix(declared, "?")
		}
		return false
	}
	// The engine classes isNamedClassType leaves out are undecidable only for
	// an object, whose place in their hierarchy this layer cannot see: a
	// non-object is no more a Traversable than it is a stdClass.
	engineClass := false
	switch strings.ToLower(name) {
	case "traversable", "generator", "closure":
		engineClass = true
	}
	// **An unresolved `static` accepts no object**: the word reaches here
	// only when there was no called class to put in its place -- an unbound
	// closure's `: static` -- and nothing is an instance of nothing
	// (static_type_return: `Return value must be of type static, stdClass
	// returned`).
	if strings.EqualFold(name, "static") && v.Deref().Kind() == KindObject {
		return true
	}
	if !isNamedClassType(name) && !engineClass {
		return false
	}
	if v.Deref().Kind() != KindObject {
		// A non-object against a named class type **is** a TypeError: PHP has
		// no conversion from a scalar to a class, so `function f(): stdClass`
		// returning a string is refused rather than left alone. Read back from
		// the engine, which says `Return value must be of type stdClass,
		// string returned`.
		//
		// `null` is the exception and it is a declaration-level one: a nullable
		// type admits it, and so does the implicit nullability a `= null`
		// default gives a class-typed parameter. Deciding that here would need
		// the default, which this does not have -- so null is left to the
		// caller exactly as before, and only the cases that cannot be nullable
		// are refused.
		//
		// The `?` prefix used to exempt the whole declaration, which was one
		// step too conservative: `?Type` admits *null*, not an array --
		// `$ref = [1]` through a reference held by `public ?Type $prop` is a
		// TypeError, and nothing else about the value is in doubt.
		//
		// And where the declaration **is** the whole truth about null, a bare
		// class type refuses it: `function f(T $a) {}` given null is
		// `Argument #1 ($a) must be of type T, null given`. Nullability got
		// here as the `?` the declaration carries or the emitter added, so its
		// absence is a fact rather than a gap.
		if v.Deref().Kind() == KindNull {
			return nullIsDecided && !strings.HasPrefix(declared, "?")
		}
		return true
	}
	if engineClass {
		// **`Closure` and `Generator` are final**, so an object is one
		// exactly when its class is: `f(new stdClass)` against a `Closure`
		// parameter is refused (closure_027). Traversable is an interface,
		// whose implementers this layer cannot see.
		switch strings.ToLower(name) {
		case "closure", "generator":
			return !strings.EqualFold(v.Deref().Object().ClassName, name)
		}
		return false
	}
	return !ctx.InstanceOfClass(v, name)
}

// nonObjectRejectedByUnion is the union question for a value that is not an
// object, decidable whenever every alternative is a class, `array` or `null`:
// nothing coerces into any of those, so the value is refused unless it is the
// array or the null an alternative names. `iterable` arrives as
// `Traversable|array`, which is how `function f(): iterable { return 1; }`
// gets its TypeError. A union naming any scalar is left to coercion.
func nonObjectRejectedByUnion(v Value, declared string) bool {
	nullable := strings.HasPrefix(declared, "?")
	hasArray := false
	for _, alt := range strings.Split(strings.TrimPrefix(declared, "?"), "|") {
		switch lower := strings.ToLower(strings.TrimSpace(alt)); lower {
		case "null":
			nullable = true
		case "array":
			hasArray = true
		case "object", "traversable", "generator", "closure":
		default:
			// A DNF term `(X&Y)` is classes only, so it admits no non-object.
			if strings.HasPrefix(lower, "(") && strings.HasSuffix(lower, ")") {
				for _, part := range strings.Split(lower[1:len(lower)-1], "&") {
					if !isNamedClassType(strings.TrimSpace(part)) {
						return false
					}
				}
				continue
			}
			if !isNamedClassType(lower) {
				return false
			}
		}
	}
	switch v.Kind() {
	case KindArray:
		return !hasArray
	case KindNull:
		return !nullable
	case KindUndef:
		return false
	}
	return true
}

// isNamedClassType reports whether a type name is one this check may reason
// about: a plain class name, and not a keyword or an engine class whose
// hierarchy the generated unit does not carry.
func isNamedClassType(name string) bool {
	switch strings.ToLower(name) {
	case "self", "static", "parent", "iterable", "callable", "mixed",
		"object", "array", "int", "float", "string", "bool", "null",
		"false", "true", "void", "never", "closure", "generator", "traversable":
		return false
	}
	return isClassLike(name)
}

// unionStringify is weak mode's last resort for an **object** against a
// union of classes and `string`: when no class member takes it, `string`
// takes it through `__toString` -- `Named|string` given a Str is "str!"
// (union_class_string_objects). Only a union of named classes and scalars
// is answered; `object`, `mixed`, `callable` and the like are left alone.
func unionStringify(ctx *Ctx, v Value, declared string, strict bool) (Value, bool, error) {
	o := v.Deref().Object()
	if strict || o == nil || !strings.Contains(declared, "|") {
		return Value{}, false, nil
	}
	hasString := false
	for _, alt := range strings.Split(strings.TrimPrefix(declared, "?"), "|") {
		alt = strings.TrimSpace(strings.TrimPrefix(alt, "?"))
		switch strings.ToLower(alt) {
		case "string":
			hasString = true
			continue
		case "int", "float", "bool", "array", "true", "false", "null":
			continue
		}
		if !isNamedClassType(alt) || ctx.InstanceOfClass(v, alt) {
			return Value{}, false, nil
		}
	}
	if !hasString {
		return Value{}, false, nil
	}
	s, handled, err := ctx.ObjectToString(o)
	if err != nil || !handled {
		return Value{}, err != nil, err
	}
	return s, true, nil
}

// DisplayType is a declared type as a TypeError spells it: an **anonymous
// class** in it is `class@anonymous`, cut at the NUL that starts the rest of
// its internal name, as the engine's C-string formatting cuts it
// (union_types/anonymous_class: `must be of type class@anonymous|string`).
func DisplayType(declared string) string {
	if strings.IndexByte(declared, 0) < 0 {
		return declared
	}
	var b strings.Builder
	skipping := false
	for _, r := range declared {
		switch {
		case r == 0:
			skipping = true
		case strings.ContainsRune("|&()", r):
			skipping = false
			b.WriteRune(r)
		case !skipping:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// isClassLike is a conservative shape test: a class name starts with a letter
// or underscore and carries no punctuation a type expression would use.
//
// An **anonymous class's** internal name is the exception, and is a class
// all the same: `class@anonymous\0FILE:LINE$0`, which `self` in its own
// method's type resolves to. Left undecided, `self|string` let a stdClass
// through.
func isClassLike(name string) bool {
	if i := strings.Index(name, "@anonymous\x00"); i > 0 {
		return true
	}
	for i, r := range name {
		switch {
		case r == '_' || r == '\\':
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return name != ""
}

// Every message in this file names the offending value with `ValueName`, not
// `TypeName`, and the difference is only visible on a bool:
//
//	Cannot assign true to property C::$i of type int
//	g(): Argument #1 ($x) must be of type int, true given
//	h(): Return value must be of type int, true returned
//
// All three shapes say `true`/`false` where `TypeName` says `bool`. Asked of
// the engine for each shape rather than generalised from one, because they are
// three separate renderings and agreeing was not a given.

// CoerceArgument applies a declared parameter type, in PHP's weak mode.
//
// `called in FILE on line LINE` names the **call site**, which is why the frame
// is consulted rather than the current location: by the time a parameter is
// bound, the current line is the callee's own.
//
// Every refusal in this file raises through `RaiseAt` rather than `Raise`, and
// the difference is visible in two places at once. A coercion failure happens
// *inside* a call whose frame the unwind drops before anything materialises the
// throwable, so `Raise` gave it the caller's position and an empty trace:
// `in r.php:6` with `#0 {main}` where the engine says `in r.php:3` with
// `#0 r.php(6): test1()`. The position and the first trace frame are two
// different facts -- where the failure happened, and what was being called --
// and reading both after the pop collapses them into one wrong answer.
// argLabel renders the parameter half of an argument diagnostic. A **variadic**
// parameter has no name in it: `g(int ...$ns)` given a string reports
// `Argument #2 must be of type int`, where a named parameter reports
// `Argument #2 ($x) must be of type int`. Measured, not assumed -- the name is
// there in the declaration either way.
func argLabel(position int, name string) string {
	if name == "" {
		return fmt.Sprintf("Argument #%d", position)
	}
	return fmt.Sprintf("Argument #%d ($%s)", position, name)
}

// ArgLabel is argLabel for a caller outside this package. A **variadic** slot
// reaches it with an empty name: past the declared list the engine reports the
// position alone, even though the declaration carries a name.
func ArgLabel(position int, name string) string { return argLabel(position, name) }

func CoerceArgument(ctx *Ctx, v Value, declared, function string, position int, name string, strict bool) (Value, error) {
	if alreadyDeclared(v, declared) {
		return v, nil
	}
	d, ok := parseDeclaredFor(declared, v)
	if !ok {
		if declaredClassRejects(ctx, v, declared, true) || callableRejects(ctx, v, declared) {
			site := callSite(ctx)
			return Value{}, RaiseAt(ctx, "TypeError",
				"%s(): %s must be of type %s, %s given%s",
				DisplayFunction(function), argLabel(position, name), DisplayType(declared), ValueName(v), site)
		}
		if out, converted, err := unionStringify(ctx, v, declared, strict); converted || err != nil {
			return out, err
		}
		return v, nil
	}
	out, err := coerce(ctx, v, d, strict)
	if err != nil {
		// A `__toString` that failed is that failure -- its own
		// `Return value must be of type string` -- not a refused argument.
		if isPHPException(err) {
			return Value{}, err
		}
		site := callSite(ctx)
		return Value{}, RaiseAt(ctx, "TypeError",
			"%s(): %s must be of type %s, %s given%s",
			DisplayFunction(function), argLabel(position, name), DisplayType(declared), ValueName(v), site)
	}
	return out, nil
}

// callSite is the `, called in FILE on line N` a refused argument names --
// nothing when the engine made the call, which has no site to name: a
// fiber's callable given `'test'` by start() is `... string given` and no
// more (start-arguments).
func callSite(ctx *Ctx) string {
	stack := ctx.Stack()
	if len(stack) == 0 {
		return ""
	}
	top := stack[len(stack)-1]
	if IsInternalFile(top.File) {
		return ""
	}
	return fmt.Sprintf(", called in %s on line %d", top.File, top.Line)
}

// callableRejects is a `callable` declaration refusing a value that is not a
// valid callback: `function foo(callable $f)` given `["C", "instanceMethod"]`
// from outside C is `must be of type callable, array given`. Only the plain
// and nullable forms, and only when the program's callable check can decide.
func callableRejects(ctx *Ctx, v Value, declared string) bool {
	if ctx == nil || !strings.EqualFold(strings.TrimPrefix(declared, "?"), "callable") {
		return false
	}
	held := v.Deref()
	if held.Kind() == KindNull {
		return !strings.HasPrefix(declared, "?")
	}
	reason, decided := ctx.CallableReasonFor(held)
	// A keyword callable that resolves is deprecated here too, as the
	// engine's zend_is_callable says it for a `callable` parameter
	// (callable_self_parent_static_deprecation).
	if kw := CallableKeyword(held); kw != "" && !strings.HasPrefix(reason, `cannot access "`) {
		ctx.Deprecated(`Use of "%s" in callables is deprecated`, kw)
	}
	return decided && reason != ""
}

// CallableKeyword is the `self`, `parent` or `static` a callable names its
// class with -- `"self::m"`, `["parent", "m"]` -- or "".
func CallableKeyword(value Value) string {
	class := ""
	switch value.Kind() {
	case KindString:
		c, _, ok := strings.Cut(value.String(), "::")
		if !ok {
			return ""
		}
		class = c
	case KindArray:
		first, ok := value.Array().Get(IntKey(0))
		if !ok || first.Deref().Kind() != KindString {
			return ""
		}
		class = first.Deref().String()
	default:
		return ""
	}
	switch kw := strings.ToLower(class); kw {
	case "self", "parent", "static":
		return kw
	}
	return ""
}

// CoerceLibraryArgument is CoerceInternalArgument for a library function php2go
// implements in PHP, checked in its own prologue. The body's location is
// already the library's file by then, and a diagnostic the check raises -- the
// null-to-scalar deprecation -- belongs to the **caller's** line, as it does
// for any internal function; the callee's frame records that line.
func CoerceLibraryArgument(ctx *Ctx, v Value, declared, function string, position int, name string, strict bool) (Value, error) {
	if stack := ctx.Stack(); len(stack) > 0 {
		top := stack[len(stack)-1]
		file, line := ctx.Location()
		ctx.SetLocation(top.File, top.Line)
		defer ctx.SetLocation(file, line)
	}
	return CoerceInternalArgument(ctx, v, declared, function, position, name, strict)
}

// CoerceInternalArgument is CoerceArgument for an **internal** function.
//
// The only difference is the call site, and it is not a nuance: PHP appends
// `, called in FILE on line LINE` for a userland callee and **never** for an
// internal one. Probed side by side:
//
//	strlen(): Argument #1 ($string) must be of type string, int given
//	u(): Argument #1 ($x) must be of type int, string given, called in ... on line 4
//
// The rule itself -- weak coercion, strict widening, what throws -- is shared,
// so only the message differs, with **one** exception: in weak mode an internal
// function takes null for a non-nullable *scalar* parameter and only deprecates
// it, where a user function throws. The allowance comes from the ZPP macros
// that coerce, so it stops at bool|int|float|string; `object`, `array` and a
// class type refuse null outright, and strict mode removes the difference.
func CoerceInternalArgument(ctx *Ctx, v Value, declared, function string, position int, name string, strict bool) (Value, error) {
	if alreadyDeclared(v, declared) {
		return v, nil
	}
	if !strict && scalarOnlyType(declared) {
		if k := v.Deref().Kind(); k == KindNull || k == KindUndef {
			ctx.Deprecated("%s(): Passing null to parameter #%d ($%s) of type %s is deprecated",
				function, position, name, declared)
			if pending := ctx.TakePendingError(); pending != nil {
				return Value{}, pending
			}
			// ZPP writes the type's **zero** into the parameter, so the callee
			// never sees the null: measured, `strlen(null)` is 0 and
			// `str_repeat("ab", null)` is "". `coerce` refuses null for a
			// non-nullable type -- correctly, for a userland callee -- so the
			// value is built here rather than run through it.
			return nullAsScalar(declared), nil
		}
	}
	d, ok := parseDeclaredFor(declared, v)
	if !ok {
		if declaredClassRejects(ctx, v, declared, false) {
			return Value{}, RaiseAt(ctx, "TypeError",
				"%s(): %s must be of type %s, %s given",
				function, argLabel(position, name), DisplayType(declared), ValueName(v))
		}
		return v, nil
	}
	out, err := coerce(ctx, v, d, strict)
	if err != nil {
		if isPHPException(err) {
			return Value{}, err
		}
		return Value{}, RaiseAt(ctx, "TypeError",
			"%s(): %s must be of type %s, %s given",
			function, argLabel(position, name), DisplayType(declared), ValueName(v))
	}
	return out, nil
}

// CoerceReturn applies a declared return type. The message is a different shape
// from the argument one -- `Return value must be of type int, string returned`
// -- and carries no call site.
func CoerceReturn(ctx *Ctx, v Value, declared, function string, strict bool) (Value, error) {
	// **A reference returned by `function &f(): T` is checked by its
	// value** and coerced in place, and the reference is still what is
	// returned. Checked as the box, it failed for any value -- `int
	// returned` against `: int` (gh18736).
	if r, boxed := v.RefOf(); boxed {
		out, err := CoerceReturn(ctx, r.Get().Deref(), declared, function, strict)
		if err != nil {
			return Value{}, err
		}
		if out.Kind() != r.Get().Deref().Kind() {
			r.Set(out)
		}
		return v, nil
	}
	if alreadyDeclared(v, declared) {
		return v, nil
	}
	d, ok := parseDeclaredFor(declared, v)
	if !ok {
		// The same provable class check the argument path uses. A return type
		// naming a class was accepted unchecked for the same reason: the parser
		// answers false for anything it cannot *coerce*, and the caller read
		// that as "fine".
		if declaredClassRejects(ctx, v, declared, true) {
			return Value{}, RaiseAt(ctx, "TypeError",
				"%s(): Return value must be of type %s, %s returned",
				DisplayFunction(function), DisplayType(declared), ValueName(v))
		}
		if out, converted, err := unionStringify(ctx, v, declared, strict); converted || err != nil {
			return out, err
		}
		return v, nil
	}
	out, err := coerce(ctx, v, d, strict)
	if err != nil {
		return Value{}, RaiseAt(ctx, "TypeError",
			"%s(): Return value must be of type %s, %s returned",
			DisplayFunction(function), DisplayType(declared), ValueName(v))
	}
	return out, nil
}

// CoerceProperty applies a **typed property**'s declared type on assignment.
// A third message shape again, naming the class that declared the property
// rather than the instance's own:
//
//	Cannot assign string to property T::$i of type int
//
// Weak mode coerces exactly as it does for an argument -- `$t->i = "7"` stores
// `int(7)`, and `5.5` stores `5` with the precision deprecation rather than
// throwing -- so the rule lives in `coerce` and only the phrasing is here.
func CoerceProperty(ctx *Ctx, v Value, declared, class, name string, strict bool) (Value, error) {
	check := relativeTypeFor(ctx, declared, class)
	d, ok := parseDeclaredFor(check, v)
	if !ok {
		// The same provable class check the argument and return paths make.
		// A property declared `stdClass` accepted **any** object, and one
		// declared `X&Y` accepted an object satisfying neither -- because
		// `ParseDeclaredType` answers false for anything it cannot *coerce*,
		// and this read that as "no type to apply". A class type is not
		// uncoercible-so-unchecked; it is uncoercible-so-exact.
		if declaredClassRejects(ctx, v, check, true) {
			return Value{}, RaiseAt(ctx, "TypeError",
				"Cannot assign %s to property %s::$%s of type %s",
				ValueName(v), DisplayName(class), name, declared)
		}
		if out, converted, err := unionStringify(ctx, v, check, strict); converted || err != nil {
			return out, err
		}
		return v, nil
	}
	out, err := coerce(ctx, v, d, strict)
	if err != nil {
		// A `__toString` that threw is that exception (gh14969).
		if isPHPException(err) {
			return Value{}, err
		}
		return Value{}, RaiseAt(ctx, "TypeError",
			"Cannot assign %s to property %s::$%s of type %s",
			ValueName(v), DisplayName(class), name, declared)
	}
	return out, nil
}

// noParent stands for `parent` where the class has none: a class name no
// object is an instance of.
const noParent = "php2go_no_parent"

// relativeTypeFor is a property's declared type with `self` and `parent`
// named: `self` is the class that declared it -- for a trait reached
// directly, the trait -- and `parent` that class's parent, which a trait
// and a root class do not have. Only for checking; a refusal still quotes
// the declared spelling. Unresolved, both names were read as class names
// nothing could be proved about, and any object was accepted
// (typed_properties_043).
func relativeTypeFor(ctx *Ctx, declared, class string) string {
	lower := strings.ToLower(declared)
	if class == "" || !strings.Contains(lower, "self") && !strings.Contains(lower, "parent") {
		return declared
	}
	var out strings.Builder
	word := func(w string) {
		switch strings.ToLower(w) {
		case "self":
			out.WriteString(class)
		case "parent":
			p := ""
			if ctx != nil && ctx.parentOf != nil {
				p = ctx.parentOf(class)
			}
			if p == "" {
				p = noParent
			}
			out.WriteString(p)
		default:
			out.WriteString(w)
		}
	}
	start := -1
	for i := 0; i < len(declared); i++ {
		b := declared[i]
		ident := b == '_' || b == '\\' || b >= 0x80 || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
		if ident {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			word(declared[start:i])
			start = -1
		}
		out.WriteByte(b)
	}
	if start >= 0 {
		word(declared[start:])
	}
	return out.String()
}

// unionPreference is the order PHP tries a union's alternatives in when the
// value is not already one of them. Read off the engine rather than the RFC:
// `string|float` given `7` stores `float(7)` and given `true` stores
// `float(1)`, so float outranks string; `int|string` given `1.5` stores
// `int(1)` with the precision deprecation, so int outranks string; and
// `bool|int` given `"x"` stores `bool(true)`, so bool is last.
var unionPreference = []string{"int", "float", "string", "bool"}

// coerceUnion applies a scalar union type in PHP's weak mode.
//
// Three rules in order, all measured:
//
//  1. A value whose type is **already** one of the alternatives is kept, with
//     no conversion at all -- `int|float` given `1.0` stays `float(1)` where
//     the preference order below would have made it `int(1)`.
//  2. A **numeric string** prefers its own numeric type when the union offers
//     it: `int|float` given `"5.5"` is `float(5.5)`, not the `int(5)` the
//     preference order gives. Where it does not -- `bool|int` given `"5.5"` --
//     the ordinary order applies, and the precision deprecation with it.
//  3. Otherwise the first alternative in unionPreference that accepts the
//     value wins.
//
// Until this existed a scalar union was **not applied at all**: it failed
// ParseDeclaredType, and every caller read that as "no type here", so
// `public int|string $a = ...` stored a null, an array, anything.
func coerceUnion(ctx *Ctx, v Value, d DeclaredType, strict bool) (Value, error) {
	if v.Kind() == KindNull {
		if d.Nullable {
			return v, nil
		}
		return Value{}, errNotCoercible
	}
	for _, m := range d.Union {
		if SatisfiesDeclared(v, m) {
			return v, nil
		}
	}
	if strict {
		// The one strict-mode conversion, as for a single type: an int widens
		// to float when the union offers float.
		if v.Kind() == KindInt && unionHas(d.Union, "float") {
			return Float(v.Float()), nil
		}
		return Value{}, errNotCoercible
	}
	if v.Kind() == KindString {
		if n, ok := v.IsNumericString(); ok {
			want := "int"
			if n.Kind() == KindFloat {
				want = "float"
			}
			if unionHas(d.Union, want) {
				return coerce(ctx, v, DeclaredType{Name: want}, false)
			}
		}
	}
	for _, m := range unionPreference {
		if !unionHas(d.Union, m) {
			continue
		}
		out, err := coerce(ctx, v, DeclaredType{Name: m}, false)
		if err == nil {
			return out, nil
		}
	}
	return Value{}, errNotCoercible
}

func unionHas(members []string, name string) bool {
	for _, m := range members {
		if m == name {
			return true
		}
	}
	return false
}

// errNotCoercible is internal: the caller shapes the message, because the
// argument and return forms differ.
var errNotCoercible = fmt.Errorf("php: not coercible")

func coerce(ctx *Ctx, v Value, d DeclaredType, strict bool) (Value, error) {
	if len(d.Union) > 0 {
		return coerceUnion(ctx, v, d, strict)
	}
	// A literal type is answered **before** the null guard below, which exists
	// to refuse null for a non-nullable scalar and would refuse it for a
	// declared type of `null` too.
	switch d.Name {
	case "null", "true", "false":
		// `?false` is a real declaration -- and the commonest way to get one is
		// a `= null` default, which makes a parameter implicitly nullable -- so
		// the nullability has to be honoured here rather than left to the guard
		// below, which this branch runs ahead of.
		if d.Nullable && v.Kind() == KindNull {
			return v, nil
		}
		if SatisfiesDeclared(v, d.Name) {
			return v, nil
		}
		return Value{}, errNotCoercible
	}
	if v.Kind() == KindNull {
		// **Null is a TypeError for every non-nullable scalar**, including
		// `bool` and `string`, which is the row most likely to be assumed
		// otherwise: `f(string $s)` given null does *not* produce "".
		if d.Nullable {
			return v, nil
		}
		return Value{}, errNotCoercible
	}
	if strict {
		// `strict_types=1` converts nothing, with **one** exception: an int
		// still widens to float. Everything else that is not already the
		// declared type throws.
		if d.Name == "float" && v.Kind() == KindInt {
			return Float(v.Float()), nil
		}
		if SatisfiesDeclared(v, d.Name) {
			return v, nil
		}
		return Value{}, errNotCoercible
	}
	if d.VerifyOnly {
		if SatisfiesDeclared(v, d.Name) {
			return v, nil
		}
		return Value{}, errNotCoercible
	}
	switch d.Name {
	case "int":
		return coerceInt(ctx, v)
	case "float":
		return coerceFloat(v)
	case "string":
		return coerceString(ctx, v)
	case "bool":
		switch v.Kind() {
		case KindTrue, KindFalse:
			return v, nil
		case KindInt, KindFloat, KindString:
			return Bool(v.Bool()), nil
		}
		return Value{}, errNotCoercible
	}
	return v, nil
}

// representableAsInt reports whether a float is exactly an int64's worth of
// value. The upper bound is written as a comparison against 2^63 rather than
// against math.MaxInt64, which is not representable as a float64 and rounds up
// to 2^63 -- so `f <= math.MaxInt64` would have admitted 2^63 itself.
func representableAsInt(f float64) bool {
	return !math.IsNaN(f) && f >= -9223372036854775808.0 && f < 9223372036854775808.0
}

func coerceInt(ctx *Ctx, v Value) (Value, error) {
	switch v.Kind() {
	case KindInt:
		return v, nil
	case KindTrue, KindFalse:
		return Int(v.Int()), nil
	case KindFloat:
		f := v.Float()
		if !representableAsInt(f) {
			// **Not coercible**, not a lossy conversion: a float outside
			// int64's range, an infinity and a NaN are all
			// `Cannot assign float to property D::$i of type int` rather than
			// a wrapped integer with a deprecation. This wrapped, so
			// `$d->i = 1e20` stored `int(-9223372036854775808)` -- and
			// `$d->i++` at PHP_INT_MAX did too, where the engine refuses.
			return Value{}, errNotCoercible
		}
		if math.Trunc(f) != f {
			ctx.Deprecated("Implicit conversion from float %s to int loses precision",
				DumpFloat(f))
		}
		return Int(v.Int()), nil
	case KindString:
		n, ok := v.IsNumericString()
		if !ok {
			// A *leading*-numeric string such as "5abc" is not accepted here,
			// though arithmetic takes it with a warning.
			return Value{}, errNotCoercible
		}
		if n.Kind() == KindFloat {
			f := n.Float()
			if !representableAsInt(f) {
				return Value{}, errNotCoercible
			}
			if math.Trunc(f) != f {
				// A different message from the plain-float one, and it
				// **quotes** the original string: `Implicit conversion from
				// float-string "5.5" to int loses precision`.
				ctx.Deprecated("Implicit conversion from float-string \"%s\" to int loses precision",
					v.String())
			}
			return Int(int64(f)), nil
		}
		return n, nil
	}
	return Value{}, errNotCoercible
}

func coerceFloat(v Value) (Value, error) {
	switch v.Kind() {
	case KindFloat:
		return v, nil
	case KindInt, KindTrue, KindFalse:
		return Float(v.Float()), nil
	case KindString:
		n, ok := v.IsNumericString()
		if !ok {
			return Value{}, errNotCoercible
		}
		return Float(n.Float()), nil
	}
	return Value{}, errNotCoercible
}

func coerceString(ctx *Ctx, v Value) (Value, error) {
	switch v.Kind() {
	case KindString:
		return v, nil
	case KindInt, KindFloat, KindTrue, KindFalse:
		return String(v.String()), nil
	case KindObject:
		// **An object with `__toString` satisfies a `string` parameter in weak
		// mode.** Without this arm `function u(string $x)` handed one was
		// `must be of type string, S given` -- a TypeError where PHP prints
		// "hello", and the same hole made `strlen(new S)` throw.
		//
		// An object *without* one falls through to errNotCoercible, so the
		// caller reports the TypeError naming the class, which is what the
		// engine does. A `__toString` that **throws** is that throw, not a
		// coercion failure, so it is returned rather than swallowed.
		s, has, err := ctx.ObjectToString(v.Object())
		if err != nil {
			return Value{}, err
		}
		if !has {
			return Value{}, errNotCoercible
		}
		return s, nil
	}
	return Value{}, errNotCoercible
}

// WidenDefault applies the one implicit conversion PHP performs on a *declared
// default* -- a property, static property, class constant or parameter
// initializer: an `int` default under a type that accepts `float` but not `int`
// is stored as a float.
//
// This is not weak-mode coercion wearing another name, and the difference is
// what makes it a separate function:
//
//   - It applies under `strict_types=1` too. `scalar_float_with_integer_default_strict`
//     wants `float(0)` from `function test(float $arg = 0)` in a strict file.
//   - It is the *only* conversion allowed. `public float $x = "2"` is a
//     compile error in the engine, not a coerced `2.0`, so this must never
//     reach for the general coercion path -- doing so would accept a program
//     PHP rejects.
//
// The membership rule was read back from the engine rather than assumed:
// `float`, `?float`, `float|bool` and `float|string` all widen `2` to
// `float(2)`, while `int|float` leaves `int(2)` alone. So the question is not
// "is the type float" but "does it accept float and not int" -- and
// `float|string` widening is the row that rules out any weak-coercion-order
// reading of it.
func WidenDefault(v Value, declared string) Value {
	if v.Kind() != KindInt || declared == "" {
		return v
	}
	if !declaredNames(declared, "float") || declaredNames(declared, "int") {
		return v
	}
	return Float(float64(v.Int()))
}

// declaredNames reports whether a declared type's union spelling names one
// particular member.
//
// Only `|` splits: an intersection cannot contain a scalar, so a `&` term never
// matches one, and the parentheses a DNF type carries around its intersections
// are stripped rather than parsed.
func declaredNames(declared, name string) bool {
	declared = strings.TrimPrefix(declared, "?")
	for _, part := range strings.Split(declared, "|") {
		if strings.EqualFold(strings.Trim(part, "()"), name) {
			return true
		}
	}
	return false
}

// StepRefusesOverflow reports whether a declaration refuses the float that an
// integer `++` or `--` produces at the ends of the range.
//
// **Not answerable by trying the coercion**, which is the obvious thing and is
// wrong: `PHP_INT_MIN - 1` is exactly representable as a float, so assigning it
// to an `int` property *succeeds* and stores `PHP_INT_MIN` again. Measured --
// the same value assigned plainly is accepted while the step is refused, so the
// rule is about the operation and not about the value.
//
// The test is therefore "does this declaration admit a float at all": `int`,
// `?int` and `int|bool` do not and want the step's own TypeError; `float`,
// `int|float` and `mixed` do and widen quietly, as does an untyped property.
// `int` must be present because nothing else could be holding PHP_INT_MAX.
func StepRefusesOverflow(declared string) bool {
	if declared == "" {
		return false
	}
	if declaredNames(declared, "float") || declaredNames(declared, "mixed") {
		return false
	}
	return declaredNames(declared, "int")
}

// CheckRefStep refuses an integer `++` or `--` that ran off the end of the
// range when the slot it is about to be written to shares a **typed property's**
// box.
//
// Asked before the write rather than inside it, because the write cannot tell a
// step from an assignment: `$r++` on a reference bound to `public int $n`
// lowers to a plain `write_var`, with nothing on it saying a step produced the
// value. The emitter knows -- it just rendered the `increment` -- so it emits
// this guard ahead of the store.
//
// Answers nil for everything else, including a slot holding no reference, a
// reference with no typed source, and a declaration that admits a float.
func CheckRefStep(ctx *Ctx, slot *Value, v Value) error {
	if slot == nil || v.Deref().Kind() != KindFloat {
		return nil
	}
	r, ok := slot.RefOf()
	if !ok {
		return nil
	}
	for _, src := range r.Sources() {
		if StepRefusesOverflow(src.Type) {
			return ThrowStepOutOfRange(src.Class, src.Name, src.Type,
				v.Deref().Float() > 0, true)
		}
	}
	return nil
}

// CheckClassConstantType applies a **typed class constant**'s declared type,
// which is not the check a parameter or a property gets.
//
// Probed, and recorded next door in the interpreter's `checkConstantType`:
// nothing is coerced. Not a numeric string to int, not an object with
// `__toString` to string -- the `__toString` is not even called -- except the
// one widening the language always allows, int to float. So this is
// `WidenDefault` followed by a *verification*, never the weak-mode path.
//
// The verification is **one-sided**, for the same reason `declaredClassRejects`
// is: until now a typed constant checked nothing, so anything this starts
// refusing had better be certain. A false rejection turns a working program
// into a TypeError, which is worse than the silent acceptance it replaces. It
// therefore refuses only when *every* alternative of the declared type is
// provably unsatisfied, and treats anything it cannot decide -- `iterable`,
// `callable`, a class the unit does not declare -- as satisfied.
//
// `owner` resolves `self` and `static`, which in a constant's type name the
// declaring class and nothing else.
func CheckClassConstantType(ctx *Ctx, v Value, declared, owner, class, name string) (Value, error) {
	if declared == "" || strings.EqualFold(declared, "mixed") {
		return v, nil
	}
	widened := WidenDefault(v.Deref(), declared)
	if !provablyFailsType(ctx, widened, declared, owner) {
		return widened, nil
	}
	return Value{}, RaiseAt(ctx, "TypeError",
		"Cannot assign %s to class constant %s::%s of type %s",
		ValueName(v), DisplayName(class), name, declared)
}

// provablyFailsType reports whether a value fails **every** alternative of a
// declared type, with every part of the question answerable.
//
// The shape is DNF -- `|` over `&`-groups -- because that is what PHP 8.2's
// type syntax is, and `(X&Y)|null` is a real declaration rather than a corner.
func provablyFailsType(ctx *Ctx, v Value, declared, owner string) bool {
	for _, alt := range strings.Split(strings.TrimPrefix(declared, "?"), "|") {
		alt = strings.Trim(strings.TrimSpace(alt), "()")
		if alt == "" {
			return false
		}
		fails, known := conjunctionFails(ctx, v, alt, owner)
		if !known || !fails {
			// Either it is satisfied, or this alternative cannot be decided --
			// and an undecidable alternative is an acceptance, never a refusal.
			return false
		}
	}
	// `?T` also admits null, and the loop above never saw that alternative.
	if strings.HasPrefix(declared, "?") && v.Kind() == KindNull {
		return false
	}
	return true
}

// conjunctionFails answers one `A&B&C` group: it fails when any atom fails, and
// is only *known* when every atom is decidable.
func conjunctionFails(ctx *Ctx, v Value, conjunction, owner string) (fails, known bool) {
	for _, atom := range strings.Split(conjunction, "&") {
		matches, ok := atomMatches(ctx, v, strings.TrimSpace(atom), owner)
		if !ok {
			return false, false
		}
		if !matches {
			fails = true
		}
	}
	return fails, true
}

// atomMatches answers one type atom, reporting whether it could answer at all.
func atomMatches(ctx *Ctx, v Value, atom, owner string) (matches, known bool) {
	if strings.EqualFold(atom, "self") || strings.EqualFold(atom, "static") {
		if owner == "" {
			return false, false
		}
		atom = owner
	}
	switch strings.ToLower(atom) {
	case "mixed":
		return true, true
	case "int":
		return v.Kind() == KindInt, true
	case "float":
		return v.Kind() == KindFloat, true
	case "string":
		return v.Kind() == KindString, true
	case "bool":
		return v.Kind() == KindTrue || v.Kind() == KindFalse, true
	case "true":
		return v.Kind() == KindTrue, true
	case "false":
		return v.Kind() == KindFalse, true
	case "null":
		return v.Kind() == KindNull, true
	case "array":
		return v.Kind() == KindArray, true
	case "object":
		return v.Kind() == KindObject, true
	case "iterable", "callable", "parent", "void", "never":
		// Not decidable here: `iterable` needs the class table *and* the
		// Traversable hierarchy, `callable` admits strings and arrays by shape.
		return false, false
	}
	if !isClassLike(atom) {
		return false, false
	}
	if v.Kind() != KindObject {
		// A non-object against a class type is a real failure, and a decidable
		// one -- no coercion applies to a class-typed constant.
		return false, true
	}
	if ctx == nil || !ctx.HasClassTable() {
		return false, false
	}
	return ctx.InstanceOfClass(v, atom), true
}

// scalarOnlyType reports whether every atom of a declared type is one the
// engine's parameter parsing will coerce null into. Only those parameters get
// the null deprecation; anything else refuses null with a TypeError.
func scalarOnlyType(declared string) bool {
	if declared == "" || strings.HasPrefix(declared, "?") {
		return false
	}
	for _, atom := range strings.Split(declared, "|") {
		switch strings.ToLower(strings.TrimSpace(atom)) {
		case "bool", "int", "float", "string":
		default:
			return false
		}
	}
	return true
}

// nullAsScalar is what ZPP leaves in a non-nullable scalar parameter handed a
// null. A union takes its **first** atom, which is the order the engine's
// preference walks: `int|float` given null is `float(0)` because the float arm
// is the one that accepts, and `round(null)` shows it.
func nullAsScalar(declared string) Value {
	first := declared
	if at := strings.IndexByte(declared, '|'); at >= 0 {
		first = declared[:at]
	}
	switch strings.ToLower(strings.TrimSpace(first)) {
	case "int":
		return Int(0)
	case "float":
		return Float(0)
	case "bool":
		return Bool(false)
	}
	return String("")
}
