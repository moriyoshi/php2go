package php

import (
	"errors"
	"fmt"
	"strings"
)

// The diagnostics a running program raises: the exact text, and -- the half that
// matters more -- **whether it warns or throws**.
//
// This exists because the compiler side was burned four times in one round on
// strings written from memory, and three of the four had the right words and the
// **wrong mechanism**: a condition PHP throws was emitted as a Go error, which a
// PHP `catch` cannot see. A catchable condition made uncatchable is not a
// wording bug; the program takes a different path.
//
// So each entry carries both, and the constructors below make the kind
// impossible to get wrong by accident: a `Throw*` returns an error carrying a
// class, a `Warn*` writes through the Ctx and returns nothing.
//
// Every message and every kind was read back from the engine, by running the
// condition and printing `get_class($e)` and `$e->getMessage()`. The kinds are
// not guessable from the text -- `Modulo by zero` and `Division by zero` are both
// `DivisionByZeroError`, `Unsupported operand types` is a `TypeError` while
// everything else here is a plain `Error`, and the five at the bottom warn where
// their neighbours throw.
//
// # Quoting is PHP's, not Go's
//
// Where a message quotes a name, the quotes are **part of the message text** and
// the name goes in raw. Go's `%q` is not PHP quoting: it escapes backslashes,
// escapes embedded quotes, and would escape non-printables. PHP does none of
// that -- it wraps the bytes in `"` and stops:
//
//	Class "Foo\Bar" not found            not  Class "Foo\\Bar" not found
//	Undefined array key "say "hi""        not  Undefined array key "say \"hi\""
//
// So every quoted name below uses a **literal quote in the format string** with
// the name as a `%s` argument. Concatenating it into the format instead would be
// the same bug one step over: a name containing a `%` would be read as a verb,
// which `go vet` catches as a non-constant format string.
//
// A namespaced class name always contains a separator, so `%q` was wrong for the
// common case rather than an exotic one.
//
// This is not the whole surface and does not pretend to be. It is the conditions
// a compiled program hits constantly, chosen because the corpus's tail of
// distinct messages is long and flat; anything not here is still worth probing
// rather than recalling.

// --- Conditions that THROW. The class is part of the answer. ---

// ThrowUndefinedFunction is `Call to undefined function f()`.
//
// Reaching this is a decision, not a default: a function absent from *PHP* is
// the program's error and throws, while one PHP has and `runtime/php` does not
// is **our gap** and must be refused instead. The two look identical at the call
// site and want opposite answers.
func ThrowUndefinedFunction(name string) error {
	return Raise("Error", "Call to undefined function %s()", name)
}

// ThrowUndefinedMethod is `Call to undefined method C::m()`.
func ThrowUndefinedMethod(class, method string) error {
	// An incomplete object's methods are its get_method handler's Error
	// ("tried to call a method"), which needs the object; a path that only
	// has the class name here is refused (callMethod answers the common one).
	if strings.EqualFold(class, IncompleteClass) {
		return Unsupported("method call on an incomplete object")
	}
	return Raise("Error", "Call to undefined method %s::%s()", DisplayName(class), method)
}

// ThrowInaccessibleMethod is `Call to private method A::p() from scope B`.
//
// Three parts of it are settled by asking the engine rather than guessed:
//
//   - the class named is the one that **declared** the method, not the
//     receiver's -- `$b->p()` on a `B extends A` says `A::p()`;
//   - the method is named as the **call site spelled it**, not as the
//     declaration did, so `$x->CAMEL()` reports `CAMEL()`;
//   - a static private reads `Call to private method`, with no mention of
//     static.
//
// RaiseAt, not Raise: the refusal is reported at the **call site**, several
// frames deep, and the unwind pops those frames before anything materialises
// the throwable. Captured here, the trace reads `#0 a->test()`; captured later
// it reads `#0 {main}` with the top-level line number.
func ThrowInaccessibleMethod(ctx *Ctx, class, method, visibility, scope string) error {
	return RaiseAt(ctx, "Error", "Call to %s method %s::%s() from %s",
		visibility, DisplayName(class), method, scopeWord(scope))
}

// scopeWord renders a calling scope the way the engine's refusals do: the class
// name for a method body, and `global scope` for none.
func scopeWord(scope string) string {
	if scope == "" {
		return "global scope"
	}
	return "scope " + DisplayName(scope)
}

// ThrowAbstractMethodCall is `Cannot call abstract method T::Meth()`.
//
// The spelling is the **declaration's**, not the call site's: `T::METH()`
// reports `T::Meth()`. That is the opposite of ThrowInaccessibleMethod, which
// quotes the call site -- two neighbouring refusals about the same call, and
// each was measured rather than derived from the other.
func ThrowAbstractMethodCall(ctx *Ctx, class, method string) error {
	return RaiseAt(ctx, "Error", "Cannot call abstract method %s::%s()",
		DisplayName(class), method)
}

// ThrowNonStaticCall is `Non-static method Foo::Bar() cannot be called
// statically`.
//
// It quotes the declaration's spelling and names the declaring class, like
// ThrowAbstractMethodCall -- and it beats `__callStatic`: a class with both an
// instance `m()` and a `__callStatic` refuses `C::m()` rather than
// trampolining.
func ThrowNonStaticCall(ctx *Ctx, class, method string) error {
	return RaiseAt(ctx, "Error", "Non-static method %s::%s() cannot be called statically",
		DisplayName(class), method)
}

// ThrowMethodCallOnNonObject is `Call to a member function m() on int`. The type
// is named as PHP names it, so a null reads `on null`.
//
// **ValueName, not TypeName**, and the difference is visible only on a bool:
// the engine says `on false` and `on true`, never `on bool`. The same split the
// three type-declaration messages make, asked again here rather than assumed
// from them.
func ThrowMethodCallOnNonObject(method string, subject Value) error {
	return Raise("Error", "Call to a member function %s() on %s", method, ValueName(subject))
}

// ThrowPropertyAssignOnNonObject is `Attempt to assign property "p" on null`.
//
// An **Error**, not a warning: reading a property of a non-object warns and
// yields null, while writing one throws. The two live next to each other here
// so the asymmetry is visible rather than surprising.
//
// The subject is named by value -- `true`, not `bool` -- like every other type
// diagnostic the engine renders.
func ThrowPropertyAssignOnNonObject(property string, subject Value) error {
	return ThrowPropertyOnNonObject("assign", property, subject)
}

// ThrowPropertyOnNonObject is the same refusal for any of the **three verbs**
// the engine uses, and the operation picks it, not the value:
//
//	$v->d = 1   $v->d += 1   $v->d .= "x"    Attempt to assign property
//	$v->d++     $v->d--                      Attempt to increment/decrement property
//	$v->d[] = 1   $r = &$v->d                Attempt to modify property
//
// Measured for all seven non-object kinds, which agree on the verb. `+=` is an
// *assign* and `++` is not, which is the distinction that costs something: the
// two lower to the same read-step-write triple, so the store has to be told
// which it came from.
func ThrowPropertyOnNonObject(verb, property string, subject Value) error {
	return Raise("Error", "Attempt to %s property \"%s\" on %s",
		verb, property, ValueName(subject))
}

// ThrowScalarAsArray is `Cannot use a scalar value as an array`, which **throws**
// rather than warning -- the mistake that started this file.
// IllegalOffsetKind reports whether a value cannot be an array offset at all.
// Only objects and arrays: every scalar has a conversion, including null and
// bool, and those are silent -- see ArrayKey.
func IllegalOffsetKind(v Value) bool {
	switch v.Deref().Kind() {
	case KindObject, KindArray:
		return true
	}
	return false
}

// The three refusals for an offset PHP will not convert, which differ by what
// the offset was being used *for*. Taken from the engine:
//
//	$a[$o]           Cannot access offset of type stdClass on array
//	$a[$o] = 1       Cannot access offset of type stdClass on array
//	isset($a[$o])    Cannot access offset of type stdClass in isset or empty
//	unset($a[$o])    Cannot unset offset of type stdClass on array
//
// The type is named the way `TypeName` names it, so an object is its class:
// `Cannot access offset of type Closure on array`.
func ThrowIllegalOffset(v Value) error {
	return Raise("TypeError", "Cannot access offset of type %s on array", TypeName(v.Deref()))
}

func ThrowIllegalOffsetIsset(v Value) error {
	return Raise("TypeError", "Cannot access offset of type %s in isset or empty", TypeName(v.Deref()))
}

func ThrowIllegalUnsetOffset(v Value) error {
	return Raise("TypeError", "Cannot unset offset of type %s on array", TypeName(v.Deref()))
}

// ThrowStringOffsetReference is `&$s[0]` on a string, which PHP refuses
// outright rather than handing back storage.
//
// **An empty string refuses too**, which is the edge worth pinning: `$a = "";
// &$a[0]` is this error, while `$a = null; &$a[0]` autovivifies an array and
// succeeds. So the refusal follows the *type*, not whether an offset exists --
// and a scalar that is not a string says `Cannot use a scalar value as an
// array` instead, a different error for a different reason.
func ThrowStringOffsetReference() error {
	return Raise("Error", "Cannot create references to/from string offsets")
}

func ThrowScalarAsArray() error {
	return Raise("Error", "Cannot use a scalar value as an array")
}

// ThrowObjectAsArray is `Cannot use object of type stdClass as array`, and names
// the class rather than saying "object".
func ThrowObjectAsArray(class string) error {
	return Raise("Error", "Cannot use object of type %s as array", DisplayName(class))
}

// ThrowUndefinedConstant is `Undefined constant "NAME"`, with the name quoted.
func ThrowUndefinedConstant(name string) error {
	return Raise("Error", `Undefined constant "%s"`, name)
}

// ThrowUndefinedConstantAt is the same failure raised **inside a call whose
// frame the unwind will drop**: a deferred initializer runs in the callee, and
// the trace PHP prints names it -- `#0 file(7): foo\foo()`. Raise records no
// stack, so materialising it after the return left only `#0 {main}`.
func ThrowUndefinedConstantAt(ctx *Ctx, name string) error {
	return RaiseAt(ctx, "Error", `Undefined constant "%s"`, name)
}

// ThrowClassNotFound is `Class "C" not found`, quoted the same way.
func ThrowClassNotFound(class string) error {
	return Raise("Error", `Class "%s" not found`, DisplayName(class))
}

// ThrowInaccessibleProperty is `Cannot access private property C::$p`. The
// visibility word is the declaration's, so a protected one reads `protected`.
//
// A shadowed private arrives as its slot key, `\0B\0x`; the message names the
// property as the program wrote it.
func ThrowInaccessibleProperty(class, visibility, property string) error {
	if name, _, mangled := UnmangleProperty(property); mangled {
		property = name
	}
	return Raise("Error", "Cannot access %s property %s::$%s",
		visibility, DisplayName(class), property)
}

// ThrowUnsupportedOperands is `Unsupported operand types: array + int` -- a
// **TypeError**, not a plain Error, which is the kind most easily got wrong here
// because every neighbour in this list is an Error.
func ThrowUnsupportedOperands(left Value, operator string, right Value) error {
	return Raise("TypeError", "Unsupported operand types: %s %s %s",
		TypeName(left), operator, TypeName(right))
}

// ThrowDivisionByZero is `Division by zero`, and ThrowModuloByZero is `Modulo by
// zero`. **Both are DivisionByZeroError** -- the class does not follow the words,
// which is why they are two constructors over one class rather than one
// constructor taking the text.
func ThrowDivisionByZero() error { return Raise("DivisionByZeroError", "Division by zero") }

// ThrowModuloByZero is `%` by zero. See ThrowDivisionByZero on the shared class.
func ThrowModuloByZero() error { return Raise("DivisionByZeroError", "Modulo by zero") }

// ThrowMethodNameNotString is `Method name must be a string`.
func ThrowMethodNameNotString() error {
	return Raise("Error", "Method name must be a string")
}

// ThrowAbstractInstantiation is `Cannot instantiate abstract class A`.
func ThrowAbstractInstantiation(class string) error {
	return Raise("Error", "Cannot instantiate abstract class %s", DisplayName(class))
}

// ThrowInvalidBackingValue is `"A" is not a valid backing value for enum Suit`.
//
// The value is named **after** the coercion to the backing type, because that is
// what the enum was actually searched for: `Suit::from(42)` on a string-backed
// enum reports `"42"`, not `42`. A string is quoted and nothing else is, which
// is why this is a diagnostic of its own rather than a `%v` at the call site.
//
// The caller coerces first and raises the `TypeError` from that coercion --
// `Foo::from(): Argument #1 ($value) must be of type int, string given` -- so a
// value reaching here is already of the backing type.
func ThrowInvalidBackingValue(given Value, class string) error {
	named := given.String()
	if given.Deref().Kind() == KindString {
		named = `"` + named + `"`
	}
	return Raise("ValueError", "%s is not a valid backing value for enum %s",
		named, DisplayName(class))
}

// ThrowDynamicProperty is `Cannot create dynamic property Foo::$baz`.
//
// This is the *Error* form, which is what an enum and a readonly class raise.
// An ordinary class raises 8.2's deprecation instead and the write succeeds, so
// a caller must know which rule it is under before reaching for this.
func ThrowDynamicProperty(class, property string) error {
	return Raise("Error", "Cannot create dynamic property %s::$%s",
		DisplayName(class), property)
}

// ThrowEnumInstantiation is `Cannot instantiate enum Foo`.
func ThrowEnumInstantiation(class string) error {
	return Raise("Error", "Cannot instantiate enum %s", DisplayName(class))
}

// ThrowTraitInstantiation is `Cannot instantiate trait T`.
//
// Separate from the abstract and enum forms because the engine names the kind,
// and the three kinds are three different sentences rather than one with a
// substitution -- which is only visible by asking it for each.
func ThrowTraitInstantiation(class string) error {
	return Raise("Error", "Cannot instantiate trait %s", DisplayName(class))
}

// --- Conditions that WARN. Execution continues, and the expression has a value. ---

// WarnUndefinedVariable is `Undefined variable $name`. It warns and the read
// yields NULL, which is why a compiled program must carry on rather than unwind.
func (c *Ctx) WarnUndefinedVariable(name string) {
	c.Warn("Undefined variable $%s", name)
}

// WarnUndefinedArrayKey is `Undefined array key 99` for an int and
// `Undefined array key "k"` for a string -- **quoted only when it is a string**,
// which is the detail that makes this worth a helper.
func (c *Ctx) WarnUndefinedArrayKey(key Key) {
	if key.IsInt {
		c.Warn("Undefined array key %d", key.Int)
	} else {
		c.Warn(`Undefined array key "%s"`, key.Str)
	}
}

// WarnUndefinedProperty is `Undefined property: C::$p`, with a colon after the
// word where the array-key message has none.
func (c *Ctx) WarnUndefinedProperty(class, property string) {
	c.Warn("Undefined property: %s::$%s", DisplayName(class), property)
}

// NoticeStaticAsNonStatic is `Accessing static property B::$a as non static`.
//
// It names the **instance's** class, not the one that declared the static:
// `$this->b` on a `D extends B` reports `D::$b`. And it is emitted in front of
// the ordinary undefined-property path rather than instead of it, so the read
// still warns `Undefined property` and yields null.
//
// Visibility does not enter into it -- a `private static` in a parent draws the
// notice from a subclass instance just the same.
func (c *Ctx) NoticeStaticAsNonStatic(class, property string) {
	c.Notice("Accessing static property %s::$%s as non static", DisplayName(class), property)
}

// WarnObjectConversion is `Object of class C could not be converted to int`.
// It is a **warning** and the conversion still answers 1 -- unlike the string
// cast, which is an Error, because `__toString` is a thing an object can have
// and there is no `__toInt`.
func (c *Ctx) WarnObjectConversion(class, target string) {
	c.Warn("Object of class %s could not be converted to %s", DisplayName(class), target)
}

// WarnPropertyOnNonObject is `Attempt to read property "p" on int` -- the
// property quoted, the type not, and the opposite mechanism from
// ThrowMethodCallOnNonObject: reading a property on an int **warns** and yields
// NULL, while calling a method on one **throws**.
func (c *Ctx) WarnPropertyOnNonObject(property string, subject Value) {
	// ValueName for the same reason as ThrowMethodCallOnNonObject: `on false`,
	// not `on bool`.
	c.Warn(`Attempt to read property "%s" on %s`, property, ValueName(subject))
}

// WarnForeachOverNonIterable is `foreach() argument must be of type array|object,
// int given`. It warns and the body runs zero times; see NewIter.
func (c *Ctx) WarnForeachOverNonIterable(subject Value) {
	c.Warn("foreach() argument must be of type array|object, %s given", TypeName(subject))
}

// diagnosticText is used by the tests to render a raised condition the way a
// caller would report it, so the table can assert text and class together.
func diagnosticText(err error) string {
	if raised, ok := AsError(err); ok {
		return fmt.Sprintf("%s: %s", raised.Class, raised.Message)
	}
	return err.Error()
}

// ThrowReadonlyModification is writing a readonly property that already holds a
// value. It fires **even inside the declaring class**: readonly means written
// once, not written only from inside.
func ThrowReadonlyModification(class, property string) error {
	return readonlyRefusal("modify", class, property)
}

// ThrowUnsetNonArray is `unset($x[0])` where `$x` is a scalar that cannot hold
// offsets at all. `null` and `false` do not reach here -- the first is silent
// and the second deprecates, both leaving the variable alone -- and a string
// has its own wording.
func ThrowUnsetNonArray() error {
	return Raise("Error", "%s", "Cannot unset offset in a non-array variable")
}

// ThrowUnsetStringOffset is `unset($s[0])` on a string. A string offset can be
// written but never removed, so this is a refusal rather than a no-op.
func ThrowUnsetStringOffset() error {
	return Raise("Error", "%s", "Cannot unset string offsets")
}

// DeprecateFalseToArray is the one scalar-to-array conversion PHP still
// allows. It is emitted by the write paths *and* by `unset($f[0])`, which
// deprecates and then leaves the variable `false` -- the fetch is what
// converts, and unset has nothing to convert for.
func DeprecateFalseToArray(ctx *Ctx) error {
	ctx.Deprecated("Automatic conversion of false to array is deprecated")
	return ctx.TakePendingError()
}

// ThrowNulProperty is a property name **beginning** with a NUL byte, which is
// the engine's private namespace: `MangleProperty` writes `"\0Class\0name"`
// there, so a program that reaches it by a computed name would be reading a
// mangled slot as though it were an ordinary one.
//
// Measured, and the shape is narrower than it looks. Only the *first* byte
// counts -- `"a\0b"` is an ordinary name that merely prints truncated -- an
// empty name is fine, and `isset` answers `false` in silence rather than
// raising. The refusal also comes **after** the magic method for the operation:
// a class with `__get` answers a `\0` read, and the same class still refuses a
// `\0` write, because it is `__set` that is missing.
func ThrowNulProperty() error {
	return Raise("Error", "%s", `Cannot access property starting with "\0"`)
}

// IsNulProperty reports whether a property name falls in that private
// namespace. Exported because the generated unit asks it at every point where
// a name would otherwise reach the property table.
func IsNulProperty(name string) bool {
	return len(name) > 0 && name[0] == 0
}

// ThrowReadonlyIndirect is reaching a readonly property as **storage** rather
// than writing it: `$o->arr[] = 1`, `$o->arr[0] = 1`, `$r =& $o->arr`, and
// passing it to a by-reference parameter.
//
// Unlike ThrowReadonlyModification this has **no exceptions at all**: measured,
// it fires inside the constructor, inside another method of the declaring
// class, and on a property that was never initialised. There is no state in
// which a readonly property may be opened for indirect writing, so neither the
// scope nor the initialisation state is asked about.
func ThrowReadonlyIndirect(class, property string) error {
	return Raise("Error", "Cannot indirectly modify readonly property %s::$%s",
		DisplayName(class), property)
}

// ThrowReadonlyUnset is `unset($o->p)` on a readonly property that holds a
// value. Same rule as ThrowReadonlyModification and, read back from the engine,
// the same sentence with one word changed -- which is why the two share a
// body rather than repeating it.
func ThrowReadonlyUnset(class, property string) error {
	return readonlyRefusal("unset", class, property)
}

func readonlyRefusal(verb, class, property string) error {
	return Raise("Error", "Cannot %s readonly property %s::$%s",
		verb, DisplayName(class), property)
}

// ThrowReadonlyInitOutsideScope is *initialising* a readonly property from
// outside the class that declared it.
//
// A different message from the one above, and the difference is not cosmetic:
// **readonly implies `protected(set)`**, so the first write is a visibility
// failure while a later one is a readonly failure. `scope` empty means global.
func ThrowReadonlyInitOutsideScope(class, property, scope string) error {
	return readonlyScopeRefusal("modify", class, property, scope)
}

// ThrowReadonlyUnsetOutsideScope is `unset($o->p)` on an *uninitialised*
// readonly property from outside the declaring class. Unsetting an
// uninitialised one from inside is allowed -- the engine lets a constructor do
// it -- so this is the scope half of the same two-rule split as the write path.
func ThrowReadonlyUnsetOutsideScope(class, property, scope string) error {
	return readonlyScopeRefusal("unset", class, property, scope)
}

// ThrowAsymmetricWrite is `Cannot modify private(set) property C::$a from
// global scope`.
//
// Close to the readonly refusal and not the same: that one says `protected(set)
// readonly property`, because readonly *implies* the asymmetry. This names
// whichever modifier the declaration wrote and no readonly.
func ThrowAsymmetricWrite(setVisibility, class, property, scope string) error {
	return ThrowAsymmetricAccess("modify", setVisibility, class, property, scope)
}

// ThrowAsymmetricAccess is the `(set)` refusal for any of the three operations
// that would write: `modify`, `unset`, `indirectly modify`.
func ThrowAsymmetricAccess(verb, setVisibility, class, property, scope string) error {
	return Raise("Error", "Cannot %s %s(set) property %s::$%s from %s",
		verb, setVisibility, DisplayName(class), property, scopeWord(scope))
}

func readonlyScopeRefusal(verb, class, property, scope string) error {
	return Raise("Error", "Cannot %s protected(set) readonly property %s::$%s from %s",
		verb, DisplayName(class), property, scopeWord(scope))
}

// ThrowStepOutOfRange is `++` or `--` running an `int`-typed property off the
// end of the integer range.
//
// PHP's usual answer to integer overflow is to promote to float, and an `int`
// declaration refuses that -- so the operation fails rather than widening, and
// the property keeps the value it had. The wording is its own: `Cannot
// increment property C::$n of type int past its maximal value`, not the
// generic `Cannot assign float to property ...` the refused write would
// otherwise produce.
//
// `up` picks increment/maximal over decrement/minimal, and the caller reads it
// off the **sign of the overflowed float** rather than off the opcode: only an
// increment can leave a positive one there and only a decrement a negative.
//
// `viaRef` is the same failure reached through a reference bound to the
// property, which the engine names differently again.
func ThrowStepOutOfRange(class, property, declared string, up, viaRef bool) error {
	verb, end := "decrement", "minimal"
	if up {
		verb, end = "increment", "maximal"
	}
	held := "property"
	if viaRef {
		held = "a reference held by property"
	}
	return Raise("TypeError", "Cannot %s %s %s::$%s of type %s past its %s value",
		verb, held, DisplayName(class), property, declared, end)
}

// ThrowUninitializedProperty is reading a declared *typed* property that has
// never been written. It is not `Undefined property`: the slot exists and is a
// third state, which `var_dump` shows as `uninitialized(int)`.
func ThrowUninitializedProperty(class, property string) error {
	return Raise("Error", "Typed property %s::$%s must not be accessed before initialization",
		DisplayName(class), property)
}

// UnsupportedError marks a capability php2go has not implemented, as opposed to
// a PHP error the program earned.
//
// The distinction is load-bearing and QUALITY_GATE.md turns on it: a gap must
// never be reported as a wrong answer, and a wrong answer must never be excused
// as a gap. Compiled code needs this at *run time* as well as at emit time,
// because some gaps are only observable on values -- a declared parameter type
// is invisible while the argument already matches it and changes the answer the
// moment it does not.
type UnsupportedError struct {
	What string
	// Err is the condition this gap is, when there is a sentinel for it, so a
	// caller can both classify the error as a gap and match what happened. A
	// gap reported only as a sentinel is graded as a *failure* rather than as a
	// known gap, which understates one bucket and overstates the other.
	Err error
}

func (e *UnsupportedError) Error() string { return "php2go: unsupported: " + e.What }

func (e *UnsupportedError) Unwrap() error { return e.Err }

// Unsupported reports a capability gap discovered while running.
func Unsupported(format string, args ...any) error {
	return &UnsupportedError{What: fmt.Sprintf(format, args...)}
}

// UnsupportedFor is Unsupported for a gap that already has a sentinel.
func UnsupportedFor(err error, format string, args ...any) error {
	return &UnsupportedError{What: fmt.Sprintf(format, args...), Err: err}
}

// IsUnsupported reports whether err is a php2go gap rather than a PHP error.
func IsUnsupported(err error) bool {
	var u *UnsupportedError
	return errors.As(err, &u)
}

// SatisfiesDeclared reports whether a value already matches a declared scalar
// type, so that no coercion is needed.
//
// It answers for the four scalars, `array`/`object`, PHP 8.2's three **literal**
// types -- `null`, `true` and `false`, each of which admits exactly one value --
// plus a leading `?`, and reports
// true for everything else. That is deliberate: the *silent* half of a dropped
// type declaration is scalar widening -- `function f(float $x)` called with `5`
// must make `$x` a float, and printing `int(5)` corrupts a value rather than
// merely skipping a diagnostic. Class, union and iterable types verify without
// transforming, so dropping them loses a TypeError but never a value.
func SatisfiesDeclared(v Value, declared string) bool {
	if declared == "" {
		return true
	}
	if rest, nullable := strings.CutPrefix(declared, "?"); nullable {
		if v.Kind() == KindNull {
			return true
		}
		declared = rest
	}
	switch declared {
	case "int":
		return v.Kind() == KindInt
	case "float":
		// An int argument to a float parameter is *widened*, so it does not
		// already satisfy it.
		return v.Kind() == KindFloat
	case "string":
		return v.Kind() == KindString
	case "bool":
		// A bool is two kinds here, not one.
		return v.Kind() == KindTrue || v.Kind() == KindFalse
	case "array":
		return v.Kind() == KindArray
	case "object":
		return v.Kind() == KindObject
	case "null":
		return v.Kind() == KindNull
	case "true":
		return v.Kind() == KindTrue
	case "false":
		return v.Kind() == KindFalse
	}
	return true
}

// ThrowStringOffsetType is `$s["x"]` -- a non-numeric string used as a string
// offset. It **throws** where a float offset merely warns, which is not
// guessable from either message.
func ThrowStringOffsetType(offered, subject string) error {
	return Raise("TypeError", "Cannot access offset of type %s on %s", offered, subject)
}

// WarnOffsetOnNonContainer is reading `$x[0]` where `$x` is not an array,
// string or object.
//
// The subject is spelled the way PHP spells it here, and **`true` and `false`
// are spelled out** rather than reported as `bool` -- the one row in this
// family that a reasonable implementation gets wrong.
func (c *Ctx) WarnOffsetOnNonContainer(subject Value) {
	c.Warn("Trying to access array offset on %s", offsetSubjectName(subject))
}

// WarnUninitializedStringOffset is `$s[9]` past the end. The read yields "",
// not null.
func (c *Ctx) WarnUninitializedStringOffset(offset int64) {
	// A constant format with the value as an argument, never concatenation: an
	// offset cannot contain a `%`, but the rule is the one `go vet` enforces and
	// the one this package has already been bitten by twice.
	c.Warn("Uninitialized string offset %d", offset)
}

// WarnStringOffsetCast is a float or numeric-string offset on a string.
func (c *Ctx) WarnStringOffsetCast() { c.Warn("String offset cast occurred") }

func offsetSubjectName(v Value) string {
	switch v.Kind() {
	case KindTrue:
		return "true"
	case KindFalse:
		return "false"
	case KindNull, KindUndef:
		return "null"
	}
	return TypeName(v)
}

// ThrowAppendToString is `$s[] = 1` on a string. The `[]` operator has no
// meaning there, and it is a distinct message from the scalar-as-array one.
func ThrowAppendToString() error {
	return Raise("Error", "[] operator not supported for strings")
}

// WarnMultibyteStringOffsetAssign is `$s[1] = "LONG"`: only the first byte
// lands, and PHP says so rather than silently truncating.
func (c *Ctx) WarnMultibyteStringOffsetAssign() {
	c.Warn("Only the first byte will be assigned to the string offset")
}

// WarnNegativeStringOffset is `$s[-9] = "x"` reaching before the start: a
// warning, and the write does not happen.
func (c *Ctx) WarnNegativeStringOffset(offset int64) {
	c.Warn("Illegal string offset %d", offset)
}

// ThrowMissingArgument is the callee's own report that a required parameter
// never arrived: `f(): Argument #1 ($a) not passed`.
//
// It is raised by the *parameter binding*, not by the call site, which is why
// it names the callee and appears inside its frame. A parameter counts as
// required by **position**, not by whether it has a default:
// `function f($a = 1, $b)` deprecates at compile time and then treats `$a` as
// required too, so `f(b: 2)` reports `$a` -- the default is compiled in and
// still not applied.
// ThrowTooFewArguments is the **short-call** form, which is a different message
// from ThrowMissingArgument's hole form and names the *call site*:
//
//	Too few arguments to function f(), 0 passed in t.php on line 3 and
//	exactly 1 expected
//
// `exactly` when every non-variadic parameter is required and `at least` when
// any has a default -- measured, and a **variadic** does not make it `at
// least`: `function b($x, ...$r)` reports `exactly 1 expected`.
func ThrowTooFewArguments(ctx *Ctx, fn string, passed, required int, exact bool) error {
	word := "at least"
	if exact {
		word = "exactly"
	}
	site := ""
	if stack := ctx.Stack(); len(stack) > 0 {
		// The **top** frame is the callee's own, and it carries the position
		// that drove the call -- the same frame CoerceArgument reads for its
		// `called in` clause.
		top := stack[len(stack)-1]
		site = fmt.Sprintf(" in %s on line %d", top.File, top.Line)
	}
	return RaiseAt(ctx, "ArgumentCountError",
		"Too few arguments to function %s(), %d passed%s and %s %d expected",
		fn, passed, site, word, required)
}

func ThrowMissingArgument(fn string, position int, name string) error {
	return Raise("ArgumentCountError", "%s(): %s not passed", fn, argLabel(position, name))
}

// ThrowArgumentNotByReference is `br(1+1)` on a `function br(&$r)`.
//
// The argument has no storage for the callee to write back through, so the call
// is refused before it runs. A *computed* argument -- `br(f())` -- is only a
// notice and the call proceeds, which is why this is raised for literals alone.
func ThrowArgumentNotByReference(fn string, position int, param string) error {
	return Raise("Error", "%s(): Argument #%d (%s) could not be passed by reference",
		fn, position, param)
}

// ThrowNoUnknownNamedParameters is `array_merge([1], nope: [2])`.
//
// A **library** function refuses a name it does not declare even when it is
// variadic, where a user variadic keeps it: `function uv(...$rest)` reached as
// `uv(1, x: "X")` leaves `$rest` holding `["x" => "X"]`, and `array_merge` with
// the same shape is an ArgumentCountError. The difference is the callee, not the
// call, which is why this is a separate refusal from the one below.
func ThrowNoUnknownNamedParameters(fn string) error {
	return Raise("ArgumentCountError", "%s() does not accept unknown named parameters", fn)
}

// ThrowUnknownNamedParameter is `f(zz: 1)` where the callee has no `$zz`.
//
// The message names no function, which is worth keeping exactly: every other
// argument diagnostic here is prefixed with the callee, and this one is not.
func ThrowUnknownNamedParameter(name string) error {
	return Raise("Error", "Unknown named parameter $%s", name)
}

// ThrowDuplicateNamedParameter is `f(1, a: 2)` and `f(a: 1, a: 2)`, which the
// engine reports identically: a name that reaches a slot some earlier argument
// already filled, whether that one was positional or named.
func ThrowDuplicateNamedParameter(name string) error {
	return Raise("Error", "Named parameter $%s overwrites previous argument", name)
}

// ThrowCannotPassByReference is `function f($a, &$e)` reached as `f(e: 42)`.
// A by-reference parameter needs a *place* at the call site, and a named
// argument that skipped the positional ones has none to offer.
func ThrowCannotPassByReference(fn string, position int, name string) error {
	return Raise("Error", "%s(): %s could not be passed by reference", fn, argLabel(position, name))
}

// ThrowUnhandledMatch is `match` with no arm and no default:
// `UnhandledMatchError: Unhandled match case 5`.
//
// The subject is spelled by **value** for a scalar and by *type* for anything
// else, and the spelling is var_export's rather than var_dump's -- measured,
// because the two differ exactly where it matters:
//
//	5        Unhandled match case 5
//	0.0      Unhandled match case 0.0        -- var_dump says float(0)
//	1e100    Unhandled match case 1.0E+100
//	'a'b'    Unhandled match case 'a'b'      -- quoted and NOT escaped
//	null     Unhandled match case NULL
//	[1]      Unhandled match case of type array
//	object   Unhandled match case of type stdClass
//	Foo::Bar Unhandled match case Foo::Bar       -- an enum case
func ThrowUnhandledMatch(v Value) error {
	held := v.Deref()
	switch held.Kind() {
	case KindNull, KindUndef:
		return Raise("UnhandledMatchError", "Unhandled match case %s", "NULL")
	case KindTrue:
		return Raise("UnhandledMatchError", "Unhandled match case %s", "true")
	case KindFalse:
		return Raise("UnhandledMatchError", "Unhandled match case %s", "false")
	case KindInt:
		return Raise("UnhandledMatchError", "Unhandled match case %d", held.Int())
	case KindFloat:
		return Raise("UnhandledMatchError", "Unhandled match case %s", exportFloat(held.Float()))
	case KindString:
		// Escaped and cut at 15 bytes, as a trace renders a string argument:
		// `'e\ne\n...'`.
		s := held.String()
		if len(s) > traceStringLimit {
			return Raise("UnhandledMatchError", "Unhandled match case '%s...'", escapeTraceString(s[:traceStringLimit]))
		}
		return Raise("UnhandledMatchError", "Unhandled match case '%s'", escapeTraceString(s))
	case KindObject:
		// An **enum case** is spelled as written, `Foo::Bar`, the way
		// var_export spells it; any other object by its class.
		if o := held.Object(); o.EnumCase != "" {
			return Raise("UnhandledMatchError", "Unhandled match case %s::%s", DisplayName(o.ClassName), o.EnumCase)
		}
	}
	return Raise("UnhandledMatchError", "Unhandled match case of type %s", TypeName(held))
}

// exportFloat is var_export's spelling: DumpFloat's, plus the `.0` that keeps a
// whole number looking like a float. `var_dump(0.0)` is `float(0)` and
// `var_export(0.0)` is `0.0`, and the match error uses the second.
func exportFloat(f float64) string {
	switch out := DumpFloat(f); out {
	case "INF", "-INF", "NAN":
		return out
	}
	return ReprFloat(f, 'E', true)
}

// ThrowNextElementOccupied is `$a[] = v` when the largest integer key is
// already taken, so there is no next one.
//
// It **throws** rather than wrapping or warning, and the element is not added:
// measured, `$a = [PHP_INT_MAX => "m"]; $a[] = "next";` leaves `count($a)` at 1.
func ThrowNextElementOccupied() error {
	return Raise("Error", "%s", "Cannot add element to the array as the next element is already occupied")
}

// BuiltinArityError is the engine's argument-count rule for a library
// function, shared so both back-ends state it once. It returns the **message**
// rather than raising, because the two back-ends raise differently -- the
// interpreter attaches a frame the compiled program does not have.
//
// The wordings are measured, and the choice between them is not obvious:
//
//	strtoupper("a","b")       exactly 1 argument, 2 given
//	strtoupper()              exactly 1 argument, 0 given
//	substr("abc")             at least 2 arguments, 1 given
//	count([1],0,9)            at most 2 arguments, 3 given
//	array_merge()             (variadic, no minimum -- accepted)
//
// `exactly` is used only when the parameter list is fixed and fully required;
// otherwise too few is `at least` and too many is `at most`. The count named is
// the bound that was missed, not the number given, and it is singular for 1.
//
// `most` of -1 means variadic. A caller that cannot count its arguments --
// because named arguments left holes the defaults have not filled yet -- should
// not call this at all rather than pass a number it does not believe.
func BuiltinArityError(name string, given, least, most int) (string, bool) {
	if given >= least && (most < 0 || given <= most) {
		return "", false
	}
	word, count := "exactly", most
	switch {
	case most >= 0 && least == most:
	case given < least:
		word, count = "at least", least
	default:
		word = "at most"
	}
	plural := "s"
	if count == 1 {
		plural = ""
	}
	return fmt.Sprintf("%s() expects %s %d argument%s, %d given",
		name, word, count, plural, given), true
}
