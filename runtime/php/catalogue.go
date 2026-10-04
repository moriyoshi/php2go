package php

// The intrinsic catalogue: a **type-aware definition** for each essential
// builtin function that drives the runtime.
//
// # Why this is a table and not a document
//
// `INTRINSICS.md` carried these contracts in prose and drifted twice: it named
// the wrong layer for a year, and it says of itself that no entry is covered by
// a test. Prose cannot be checked against the code, so it decays into a claim
// about what someone once believed.
//
// This table can be checked. `TestCatalogueCoversTheIntrinsicSurface` fails when
// an intrinsic exists with no entry, so the catalogue cannot silently fall
// behind the package.
//
// # What a definition says
//
// Three things, and the third is the one that makes specialization possible:
//
//   - the **PHP types** each operand may hold, as a mask -- an analysis fact,
//     not a representation;
//   - the **PHP type** of the result, which for most operators depends on the
//     operands and is therefore the widest possibility here;
//   - the **effects**: whether it can warn, deprecate, throw, or re-enter user
//     code. An operation with no effects at given operand types may be
//     specialized; one with effects may not, because the diagnostics are
//     observable and ordered.
//
// # Honesty about coverage
//
// Every entry carries `Verified`, which is true only where the contract was
// **read back from the engine** rather than believed. An unverified entry is a
// statement of intent, and nothing may specialize on one. The split is reported
// by the test rather than hidden, because a catalogue that cannot say which of
// its rows are evidence is the same failure as the prose it replaces.

// TypeMask is a set of PHP types. It is an *analysis* fact -- which values may
// flow here -- and deliberately not a representation: `MaskInt` says the value
// is a PHP int, not that it is an unboxed int64.
type TypeMask uint16

const (
	MaskNull TypeMask = 1 << iota
	MaskBool
	MaskInt
	MaskFloat
	MaskString
	MaskArray
	MaskObject
	MaskResource
	// MaskRef is a value that may arrive as a reference and must be
	// dereferenced before use. It is a property of the *slot*, not of the
	// value, which is why it is kept apart from the rest.
	MaskRef
)

// MaskScalar and friends name the combinations the catalogue actually uses, so
// an entry reads as a rule rather than as arithmetic on bits.
const (
	MaskNumeric = MaskInt | MaskFloat
	MaskScalar  = MaskNull | MaskBool | MaskInt | MaskFloat | MaskString
	MaskAny     = MaskScalar | MaskArray | MaskObject | MaskResource
	MaskAnyRef  = MaskAny | MaskRef
)

// Effect is what an intrinsic can do besides producing a value. These are
// **observable**: php2phpt grades stdout byte for byte, so a diagnostic that is
// skipped, reordered or invented is a wrong answer.
type Effect uint8

const (
	// Warns emits a Warning or Notice.
	Warns Effect = 1 << iota
	// Deprecates emits a Deprecated.
	Deprecates
	// Throws raises a catchable Error or TypeError.
	Throws
	// ReEnters can call back into user code -- `__toString`, a property hook,
	// an autoloader -- which means it can do *anything*, including output.
	ReEnters
	// Pure is the absence of all of the above.
	Pure Effect = 0
)

// Signature is one intrinsic's type-aware definition.
type Signature struct {
	// Name is the exported Go identifier in this package.
	Name string
	// Group is what part of the runtime it drives, for reading the catalogue.
	Group string
	// Symbol is how PHP spells the operator in a diagnostic:
	// `Unsupported operand types: float % string`. It belongs to the intrinsic
	// rather than to the call site, because an intrinsic has exactly one
	// spelling -- which is why the IR call carries only a name and arguments.
	Symbol string
	// Params are the PHP types each operand may hold.
	Params []Type
	// Result is the widest PHP type the intrinsic can produce.
	Result Type
	// Effects is what it can do at *some* operand types. A specialization may
	// only fire where the effects are provably absent.
	Effects Effect
	// Verified is true only when the contract was read back from the engine.
	// An unverified entry is intent, and nothing may specialize on it.
	Verified bool
	// Note records the rule that is easiest to get wrong, where there is one.
	Note string
	// Variants are the type-specialized implementations, narrowest first. The
	// last is the general one and always applies, so a consumer with no type
	// information is still correct.
	Variants []Variant
}

// Catalogue is the intrinsic surface. Ordered by group so it reads as a
// description of the runtime rather than an alphabetical list.
func Catalogue() []Signature {
	return []Signature{
		// ---- arithmetic ----
		//
		// The diagnostic ORDER is observable: `2.5 % ""` reports the left
		// operand's precision loss and then the TypeError, while `"" % "2abc"`
		// reports only the TypeError because the walk stops at the first
		// operand that cannot convert. `AcceptsArithmetic` owns that walk.
		{Name: "Add", Group: "arithmetic", Symbol: "+", Params: []Type{Of(MaskAny), Of(MaskAny)},
			Result: Of(MaskNumeric | MaskArray), Effects: Warns | Deprecates | Throws | ReEnters, Verified: true,
			Note: "array + array is union and the only non-numeric result; every other pair walks left to right",
			Variants: []Variant{
				// Two ints cannot warn, deprecate or re-enter, so the walk is not
				// owed and none of the diagnostic machinery is emitted.
				{Name: "add.int", Result: Of(MaskNumeric), When: []Type{Of(MaskInt), Of(MaskInt)}, Go: "Add", Effects: Pure},
				// Two **numerics** cannot diagnose either: the guard exists to
				// report a non-numeric operand, and neither of these is one.
				// Settled with the engine -- `1.5 + 2` and `NAN + 2` say
				// nothing, where `1.5 & 2` deprecates, which is why the bitwise
				// family has no numeric variant.
				{Name: "add.numeric", Result: Of(MaskNumeric), When: []Type{Of(MaskNumeric), Of(MaskNumeric)}, Go: "Add", Effects: Pure},
				{Name: "add", When: []Type{Of(MaskAny), Of(MaskAny)}, Go: "Add", Effects: Warns | Deprecates | Throws | ReEnters, Guard: "AcceptsArithmetic"},
			},
		},
		{Name: "Sub", Group: "arithmetic", Symbol: "-", Params: []Type{Of(MaskAny), Of(MaskAny)},
			Result: Of(MaskNumeric), Effects: Warns | Deprecates | Throws | ReEnters, Verified: true,
			Note: "the walk stops at the first operand that cannot convert, so a later one never reports",
			Variants: []Variant{
				// Two ints cannot warn, deprecate or re-enter, so the walk is not
				// owed and none of the diagnostic machinery is emitted.
				{Name: "sub.int", Result: Of(MaskNumeric), When: []Type{Of(MaskInt), Of(MaskInt)}, Go: "Sub", Effects: Pure},
				// Two **numerics** cannot diagnose either: the guard exists to
				// report a non-numeric operand, and neither of these is one.
				// Settled with the engine -- `1.5 + 2` and `NAN + 2` say
				// nothing, where `1.5 & 2` deprecates, which is why the bitwise
				// family has no numeric variant.
				{Name: "sub.numeric", Result: Of(MaskNumeric), When: []Type{Of(MaskNumeric), Of(MaskNumeric)}, Go: "Sub", Effects: Pure},
				{Name: "sub", When: []Type{Of(MaskAny), Of(MaskAny)}, Go: "Sub", Effects: Warns | Deprecates | Throws | ReEnters, Guard: "AcceptsArithmetic"},
			},
		},
		{Name: "Mul", Group: "arithmetic", Symbol: "*", Params: []Type{Of(MaskAny), Of(MaskAny)},
			Result: Of(MaskNumeric), Effects: Warns | Deprecates | Throws | ReEnters, Verified: true,
			Note: "same walk as Sub; an array operand is Unsupported operand types",
			Variants: []Variant{
				// Two ints cannot warn, deprecate or re-enter, so the walk is not
				// owed and none of the diagnostic machinery is emitted.
				{Name: "mul.int", Result: Of(MaskNumeric), When: []Type{Of(MaskInt), Of(MaskInt)}, Go: "Mul", Effects: Pure},
				// Two **numerics** cannot diagnose either: the guard exists to
				// report a non-numeric operand, and neither of these is one.
				// Settled with the engine -- `1.5 + 2` and `NAN + 2` say
				// nothing, where `1.5 & 2` deprecates, which is why the bitwise
				// family has no numeric variant.
				{Name: "mul.numeric", Result: Of(MaskNumeric), When: []Type{Of(MaskNumeric), Of(MaskNumeric)}, Go: "Mul", Effects: Pure},
				{Name: "mul", When: []Type{Of(MaskAny), Of(MaskAny)}, Go: "Mul", Effects: Warns | Deprecates | Throws | ReEnters, Guard: "AcceptsArithmetic"},
			},
		},
		{Name: "Div", Group: "arithmetic", Symbol: "/", Params: []Type{Of(MaskAny), Of(MaskAny)},
			Result: Of(MaskNumeric), Effects: Warns | Deprecates | Throws | ReEnters, Verified: true,
			Note: "a zero divisor is DivisionByZeroError, raised by the caller because runtime/php has no class vocabulary",
			Variants: []Variant{
				{Name: "div", When: []Type{Of(MaskAny), Of(MaskAny)}, Go: "Div", Effects: Warns | Deprecates | Throws | ReEnters,
					Guard: "AcceptsArithmetic", Fails: true,
					FailClass: "DivisionByZeroError", FailMessage: "Division by zero"},
			},
		},
		{Name: "Mod", Group: "arithmetic", Symbol: "%", Params: []Type{Of(MaskAny), Of(MaskAny)},
			Result: Of(MaskInt), Effects: Warns | Deprecates | Throws | ReEnters, Verified: true,
			Note: "\"Modulo by zero\" is a second message on DivisionByZeroError, and % casts both operands to int first",
			Variants: []Variant{
				{Name: "mod", When: []Type{Of(MaskAny), Of(MaskAny)}, Go: "Mod", Effects: Warns | Deprecates | Throws | ReEnters,
					Guard: "AcceptsArithmetic", Fails: true,
					FailClass: "DivisionByZeroError", FailMessage: "Modulo by zero"},
			},
		},
		{Name: "Pow", Group: "arithmetic", Symbol: "**", Params: []Type{Of(MaskAny), Of(MaskAny)},
			Result: Of(MaskNumeric), Effects: Warns | Deprecates | Throws | ReEnters, Verified: true,
			Note: "same walk; the result is int only when both operands are int and the exponent is non-negative",
			Variants: []Variant{
				// Two ints cannot warn, deprecate or re-enter, so the walk is not
				// owed and none of the diagnostic machinery is emitted.
				{Name: "pow.int", Result: Of(MaskNumeric), When: []Type{Of(MaskInt), Of(MaskInt)}, Go: "Pow", Effects: Pure},
				// Two **numerics** cannot diagnose either: the guard exists to
				// report a non-numeric operand, and neither of these is one.
				// Settled with the engine -- `1.5 + 2` and `NAN + 2` say
				// nothing, where `1.5 & 2` deprecates, which is why the bitwise
				// family has no numeric variant.
				{Name: "pow.numeric", Result: Of(MaskNumeric), When: []Type{Of(MaskNumeric), Of(MaskNumeric)}, Go: "Pow", Effects: Pure},
				{Name: "pow", When: []Type{Of(MaskAny), Of(MaskAny)}, Go: "Pow", Effects: Warns | Deprecates | Throws | ReEnters, Guard: "AcceptsArithmetic"},
			},
		},
		{Name: "Concat", Group: "arithmetic", Symbol: ".", Params: []Type{Of(MaskAny), Of(MaskAny)},
			Result: Of(MaskString), Effects: Warns | Throws | ReEnters, Verified: true,
			Note: "no numeric walk at all: every scalar has a string form, so the only failure is an object without __toString",
			Variants: []Variant{
				{Name: "concat", Ctx: true, Ret: "err", When: []Type{Of(MaskAny), Of(MaskAny)},
					Go: "ConcatValues", Effects: Warns | Throws | ReEnters},
			},
		},
		// ---- bitwise ----
		//
		// Two strings combine **bytewise** and yield a string, bypassing numeric
		// conversion; anything else converts to int and walks like arithmetic.
		{Name: "BitAnd", Group: "bitwise", Symbol: "&", Params: []Type{Of(MaskAny), Of(MaskAny)},
			Result: Of(MaskInt | MaskString), Effects: Warns | Deprecates | Throws | ReEnters, Verified: true,
			Note: "string & string is bytewise and yields a string, bypassing numeric conversion entirely",
			Variants: []Variant{
				// Two ints cannot warn, deprecate or re-enter, so the walk is not
				// owed and none of the diagnostic machinery is emitted.
				{Name: "bit_and.int", Result: Of(MaskInt), When: []Type{Of(MaskInt), Of(MaskInt)}, Go: "BitAnd", Effects: Pure},
				{Name: "bit_and", When: []Type{Of(MaskAny), Of(MaskAny)}, Go: "BitAnd", Effects: Warns | Deprecates | Throws | ReEnters, Guard: "AcceptsArithmetic"},
			},
		},
		{Name: "BitOr", Group: "bitwise", Symbol: "|", Params: []Type{Of(MaskAny), Of(MaskAny)},
			Result: Of(MaskInt | MaskString), Effects: Warns | Deprecates | Throws | ReEnters, Verified: true,
			Note: "string | string is bytewise, as for BitAnd",
			Variants: []Variant{
				// Two ints cannot warn, deprecate or re-enter, so the walk is not
				// owed and none of the diagnostic machinery is emitted.
				{Name: "bit_or.int", Result: Of(MaskInt), When: []Type{Of(MaskInt), Of(MaskInt)}, Go: "BitOr", Effects: Pure},
				{Name: "bit_or", When: []Type{Of(MaskAny), Of(MaskAny)}, Go: "BitOr", Effects: Warns | Deprecates | Throws | ReEnters, Guard: "AcceptsArithmetic"},
			},
		},
		{Name: "BitXor", Group: "bitwise", Symbol: "^", Params: []Type{Of(MaskAny), Of(MaskAny)},
			Result: Of(MaskInt | MaskString), Effects: Warns | Deprecates | Throws | ReEnters, Verified: true,
			Note: "string ^ string is bytewise, as for BitAnd",
			Variants: []Variant{
				// Two ints cannot warn, deprecate or re-enter, so the walk is not
				// owed and none of the diagnostic machinery is emitted.
				{Name: "bit_xor.int", Result: Of(MaskInt), When: []Type{Of(MaskInt), Of(MaskInt)}, Go: "BitXor", Effects: Pure},
				{Name: "bit_xor", When: []Type{Of(MaskAny), Of(MaskAny)}, Go: "BitXor", Effects: Warns | Deprecates | Throws | ReEnters, Guard: "AcceptsArithmetic"},
			},
		},
		{Name: "ShiftLeft", Group: "bitwise", Symbol: "<<", Params: []Type{Of(MaskAny), Of(MaskAny)},
			Result: Of(MaskInt), Effects: Warns | Deprecates | Throws | ReEnters, Verified: true,
			Note: "a negative shift is an ArithmeticError, not a wrap",
			Variants: []Variant{
				{Name: "shift_left", When: []Type{Of(MaskAny), Of(MaskAny)}, Go: "ShiftLeft", Effects: Warns | Deprecates | Throws | ReEnters,
					Guard: "AcceptsArithmetic", Fails: true,
					FailClass: "ArithmeticError", FailMessage: "Bit shift by negative number"},
			},
		},
		{Name: "ShiftRight", Group: "bitwise", Symbol: ">>", Params: []Type{Of(MaskAny), Of(MaskAny)},
			Result: Of(MaskInt), Effects: Warns | Deprecates | Throws | ReEnters, Verified: true,
			Note: "a negative shift is an ArithmeticError, as for ShiftLeft",
			Variants: []Variant{
				{Name: "shift_right", When: []Type{Of(MaskAny), Of(MaskAny)}, Go: "ShiftRight", Effects: Warns | Deprecates | Throws | ReEnters,
					Guard: "AcceptsArithmetic", Fails: true,
					FailClass: "ArithmeticError", FailMessage: "Bit shift by negative number"},
			},
		},
		{Name: "BitNot", Group: "bitwise", Symbol: "~", Params: []Type{Of(MaskAny)},
			Result: Of(MaskInt | MaskString), Effects: Deprecates | Throws | ReEnters, Verified: true,
			Note: "~ on a string is bytewise; on anything else it converts to int, deprecating a float that loses precision",
			Variants: []Variant{
				{Name: "bit_not", Ctx: true, Ret: "err", When: []Type{Of(MaskAny)},
					Go: "BitNotValue", Effects: Deprecates | Throws | ReEnters},
			},
		},
		// ---- comparison ----
		{Name: "Compare", Group: "comparison", Params: []Type{Of(MaskAny), Of(MaskAny)},
			Result: Of(MaskInt), Effects: Warns | ReEnters, Verified: true,
			Note: "`<=>` yields -1, 0 or 1; two numeric strings compare numerically, so \"10\" <=> \"1e1\" is 0",
			Variants: []Variant{
				{Name: "compare", GoScalar: "int", Ctx: true, Ret: "err", When: []Type{Of(MaskAny), Of(MaskAny)}, Go: "CompareValues", Effects: Warns | ReEnters},
			},
		},
		{Name: "LooseEqual", Group: "comparison", Params: []Type{Of(MaskAny), Of(MaskAny)},
			Result: Of(MaskBool), Effects: Warns | ReEnters, Verified: true,
			Note: "PHP 8: a non-numeric string is NOT coerced, so 0 == \"a\" is false where PHP 7 said true",
			Variants: []Variant{
				{Name: "loose_equal", GoScalar: "bool", Ctx: true, Ret: "err", When: []Type{Of(MaskAny), Of(MaskAny)}, Go: "LooseEqualValues", Effects: Warns | ReEnters},
			},
		},
		{Name: "NotLooseEqual", Group: "comparison", Symbol: "!=", Params: []Type{Of(MaskAny), Of(MaskAny)},
			Result: Of(MaskBool), Effects: Warns | ReEnters, Verified: true,
			Note: "the negation of `==`, with its coercion and so its diagnostics",
			Variants: []Variant{
				{Name: "not_loose_equal", GoScalar: "bool", Ctx: true, Ret: "err", When: []Type{Of(MaskAny), Of(MaskAny)},
					Go: "NotLooseEqualValues", Effects: Warns | ReEnters},
			},
		},
		{Name: "NotStrictEqual", Group: "comparison", Symbol: "!==", Params: []Type{Of(MaskAny), Of(MaskAny)},
			Result: Of(MaskBool), Effects: Throws, Verified: true,
			Note: "identity never calls user code; like `===` it refuses a recursive structure",
			Variants: []Variant{
				{Name: "not_strict_equal", GoScalar: "bool", Ctx: true, Ret: "err", When: []Type{Of(MaskAny), Of(MaskAny)},
					Go: "NotStrictEqualValues", Effects: Throws},
			},
		},
		{Name: "IsSmaller", Group: "comparison", Symbol: "<", Params: []Type{Of(MaskAny), Of(MaskAny)},
			Result: Of(MaskBool), Effects: Warns | ReEnters, Verified: true,
			Note: "`<` is Compare below zero; the engine has no `greater` opcode and swaps the operands instead",
			Variants: []Variant{
				{Name: "is_smaller", GoScalar: "bool", Ctx: true, Ret: "err", When: []Type{Of(MaskAny), Of(MaskAny)},
					Go: "IsSmallerValues", Effects: Warns | ReEnters},
			},
		},
		{Name: "IsSmallerOrEqual", Group: "comparison", Symbol: "<=", Params: []Type{Of(MaskAny), Of(MaskAny)},
			Result: Of(MaskBool), Effects: Warns | ReEnters, Verified: true,
			Note: "as `<`, at or below zero",
			Variants: []Variant{
				{Name: "is_smaller_or_equal", GoScalar: "bool", Ctx: true, Ret: "err", When: []Type{Of(MaskAny), Of(MaskAny)},
					Go: "IsSmallerOrEqualValues", Effects: Warns | ReEnters},
			},
		},
		{Name: "BoolXor", Group: "comparison", Symbol: "xor", Params: []Type{Of(MaskAny), Of(MaskAny)},
			Result: Of(MaskBool), Effects: Pure, Verified: true,
			Note: "boolifies both operands, and boolean coercion emits nothing",
			Variants: []Variant{
				{Name: "bool_xor", GoScalar: "bool", When: []Type{Of(MaskAny), Of(MaskAny)},
					Go: "BoolXor", Effects: Pure},
			},
		},
		{Name: "StrictEqual", Group: "comparison", Params: []Type{Of(MaskAny), Of(MaskAny)},
			Result: Of(MaskBool), Effects: Throws, Verified: true,
			Note: "no coercion and no re-entry -- identity never calls user code -- but a recursive structure is `Nesting level too deep`, an Error",
			Variants: []Variant{
				{Name: "strict_equal", GoScalar: "bool", Ctx: true, Ret: "err", When: []Type{Of(MaskAny), Of(MaskAny)}, Go: "StrictEqualValues", Effects: Throws},
			},
		},
		// ---- increment ----
		{Name: "Increment", Group: "incdec", Symbol: "++", Params: []Type{Of(MaskAny)},
			Result: Of(MaskAny), Effects: Warns | Deprecates | Throws, Verified: true,
			Note: "\"\" increments to \"1\"; the alphanumeric string carry is its own rule",
			Variants: []Variant{
				// An int steps to int **or float**: `PHP_INT_MAX++` overflows,
				// as does `PHP_INT_MIN--`. Settled with the engine, like the
				// arithmetic overflow it is the same fact as. Nothing to
				// diagnose either way, so the narrow form is pure.
				{Name: "increment.int", Ctx: true, Ret: "err", Local: true, Result: Of(MaskNumeric),
					When: []Type{Of(MaskInt)}, Go: "stepIncrement", Effects: Pure},
				// A float steps to a float and diagnoses nothing either, so the
				// pure form covers both -- which is what lets `$i++` in a loop
				// stay narrow after the first overflow-aware round.
				{Name: "increment.numeric", Ctx: true, Ret: "err", Local: true, Result: Of(MaskNumeric),
					When: []Type{Of(MaskNumeric)}, Go: "stepIncrement", Effects: Pure},
				// **Local**, unlike the two narrow forms above. Refusing to
				// step an array or an object is a `TypeError`, and this package
				// reports that as a sentinel rather than an exception -- the
				// layer that knows about classes maps it. Bound straight to
				// `php.Increment`, the sentinel escaped the generated program
				// as a Go error, so `$o++` on an object printed
				// `php2go: php: cannot increment` and no PHP output at all.
				{Name: "increment", Ctx: true, Ret: "err", Local: true, When: []Type{Of(MaskAny)},
					Go: "stepIncrement", Effects: Warns | Deprecates | Throws},
			},
		},
		{Name: "Decrement", Group: "incdec", Symbol: "--", Params: []Type{Of(MaskAny)},
			Result: Of(MaskAny), Effects: Warns | Deprecates | Throws, Verified: true,
			Note: "\"\" decrements to -1, which is NOT the mirror of Increment",
			Variants: []Variant{
				// An int steps to int **or float**: `PHP_INT_MAX++` overflows,
				// as does `PHP_INT_MIN--`. Settled with the engine, like the
				// arithmetic overflow it is the same fact as. Nothing to
				// diagnose either way, so the narrow form is pure.
				{Name: "decrement.int", Ctx: true, Ret: "err", Local: true, Result: Of(MaskNumeric),
					When: []Type{Of(MaskInt)}, Go: "stepDecrement", Effects: Pure},
				// A float steps to a float and diagnoses nothing either, so the
				// pure form covers both -- which is what lets `$i++` in a loop
				// stay narrow after the first overflow-aware round.
				{Name: "decrement.numeric", Ctx: true, Ret: "err", Local: true, Result: Of(MaskNumeric),
					When: []Type{Of(MaskNumeric)}, Go: "stepDecrement", Effects: Pure},
				// Local, for the reason `increment` above is.
				{Name: "decrement", Ctx: true, Ret: "err", Local: true, When: []Type{Of(MaskAny)},
					Go: "stepDecrement", Effects: Warns | Deprecates | Throws},
			},
		},

		// ---- coercion ----
		{Name: "ArrayKey", Group: "coercion", Params: []Type{Of(MaskScalar | MaskResource)},
			Result: Of(MaskInt | MaskString), Effects: Warns | Deprecates, Verified: true,
			Note: "null is \"\" not 0; the float deprecation is about precision loss, not about being a float"},
		{Name: "CoerceArgument", Group: "coercion", Params: []Type{Of(MaskAny)},
			Result: Of(MaskAny), Effects: Deprecates | Throws, Verified: true,
			Note: "\"5abc\" is a TypeError here where arithmetic takes it with a warning"},
		{Name: "CoerceReturn", Group: "coercion", Symbol: "return", Params: []Type{Of(MaskAny)},
			Result: Of(MaskAny), Effects: Deprecates | Throws, Verified: true,
			Note: "a different message shape from the argument form, and no call site",
			Variants: []Variant{
				// The declared type, the function name and the strict-types
				// flag are the emitter's to supply: they are properties of the
				// body being emitted, not operands of the instruction.
				{Name: "coerce_return", Ctx: true, Ret: "err", Args: []string{"read", "retctx"},
					When: []Type{Of(MaskAny)}, Go: "CoerceReturn", Effects: Deprecates | Throws},
			},
		},

		// ---- output ----
		{Name: "Echo", Group: "output", Symbol: "echo", Params: []Type{Of(MaskAny)},
			Result: Type{}, Effects: Warns | Throws | ReEnters, Verified: true,
			Note: "converts through ToString, so an object re-enters __toString or throws and an array warns",
			Variants: []Variant{
				{Name: "echo", Ctx: true, Ret: "errno", When: []Type{Of(MaskAny)}, Go: "Echo",
					Effects: Warns | Throws | ReEnters},
			},
		},

		// ---- boolean conversion ----
		{Name: "ToBool", Group: "coercion", Symbol: "(bool)", Params: []Type{Of(MaskAny)},
			Result: Of(MaskBool), Effects: Pure, Verified: true,
			Note: "`(bool)$x` and an `if` condition are the same coercion; \"0\" is false but \"0.0\" is true",
			Variants: []Variant{
				{Name: "to_bool", When: []Type{Of(MaskAny)}, Go: "ToBool", Effects: Pure},
			},
		},
		{Name: "ToInt", Group: "coercion", Symbol: "(int)", Params: []Type{Of(MaskAny)},
			Result: Of(MaskInt), Effects: Warns, Verified: true,
			Note: "an object WARNS and still converts, to 1; an array does not warn -- `(int)[1,2]` is 1 quietly",
			Variants: []Variant{
				{Name: "to.int", Ctx: true, When: []Type{Of(MaskAny)}, Go: "ToInt",
					Effects: Warns, Result: Of(MaskInt)},
			},
		},
		{Name: "ToFloat", Group: "coercion", Symbol: "(float)", Params: []Type{Of(MaskAny)},
			Result: Of(MaskFloat), Effects: Warns, Verified: true,
			Note: "as ToInt; `(float)\"-0\"` keeps the sign where `(float)0` does not",
			Variants: []Variant{
				{Name: "to.float", Ctx: true, When: []Type{Of(MaskAny)}, Go: "ToFloat",
					Effects: Warns, Result: Of(MaskFloat)},
			},
		},
		{Name: "ToArray", Group: "coercion", Symbol: "(array)", Params: []Type{Of(MaskAny)},
			Result: Of(MaskArray), Effects: Pure, Verified: true,
			Note: "null becomes the EMPTY array, not a one-element one; an object hands over its mangled property keys",
			Variants: []Variant{
				{Name: "to.array", When: []Type{Of(MaskAny)}, Go: "ToArray",
					Effects: Pure, Result: Of(MaskArray)},
			},
		},
		{Name: "ToNull", Group: "coercion", Symbol: "(unset)", Params: []Type{Of(MaskAny)},
			Result: Of(MaskNull), Effects: Pure, Verified: true,
			Note: "gone from PHP 8, but the cast target still has a number",
			Variants: []Variant{
				{Name: "to.null", When: []Type{Of(MaskAny)}, Go: "ToNull",
					Effects: Pure, Result: Of(MaskNull)},
			},
		},
		{Name: "ToObject", Group: "coercion", Symbol: "(object)", Params: []Type{Of(MaskAny)},
			Result: Of(MaskObject), Effects: Pure, Verified: true,
			Note: "a scalar goes under the name `scalar`; the handle is the generated unit's to number, so this is Local",
			Variants: []Variant{
				{Name: "to.object", Local: true, When: []Type{Of(MaskAny)}, Go: "castToObject",
					Effects: Pure, Result: Of(MaskObject)},
			},
		},
		{Name: "NotBool", Group: "coercion", Symbol: "!", Params: []Type{Of(MaskAny)},
			Result: Of(MaskBool), Effects: Pure, Verified: true,
			Note: "its own engine opcode, so a consumer meeting it need not know `!` is a coercion and a negation",
			Variants: []Variant{
				{Name: "not_bool", When: []Type{Of(MaskAny)}, Go: "NotBool", Effects: Pure},
			},
		},

		// ---- string conversion and building ----
		{Name: "ToString", Group: "coercion", Symbol: "(string)", Params: []Type{Of(MaskAny)},
			Result: Of(MaskString), Effects: Warns | Throws | ReEnters, Verified: true,
			Note: "an object re-enters __toString or throws Error; an array warns and still converts, to \"Array\"",
			Variants: []Variant{
				{Name: "to_string", Ctx: true, Ret: "err", When: []Type{Of(MaskAny)}, Go: "ToString",
					Effects: Warns | Throws | ReEnters, Result: Of(MaskString)},
			},
		},
		{Name: "NewStrBuf", Group: "construction", Symbol: "\"$a\"", Params: []Type{Of(MaskAny)},
			Result: Of(MaskString), Effects: Warns | Throws | ReEnters, Verified: true,
			Note: "each piece converts as it is ADDED, so a __toString that echoes does so in build order",
			Variants: []Variant{
				{Name: "strbuf.new", Ctx: true, Ret: "err", When: []Type{Of(MaskAny)}, Go: "NewStrBuf",
					Effects: Warns | Throws | ReEnters},
			},
		},
		{Name: "StrBufAppend", Group: "construction", Params: []Type{Of(MaskAny), Of(MaskAny)},
			Result: Of(MaskString), Effects: Warns | Throws | ReEnters, Verified: true,
			Note: "mutates the buffer: building a string is O(n), not O(n^2) as a concat chain is",
			Variants: []Variant{
				{Name: "strbuf.append", Ctx: true, Ret: "err", Args: []string{"raw", ""},
					When: []Type{Of(MaskAny), Of(MaskAny)}, Go: "StrBufAppend", Effects: Warns | Throws | ReEnters},
			},
		},
		{Name: "StrBufFinish", Group: "construction", Params: []Type{Of(MaskAny)},
			Result: Of(MaskString), Effects: Pure, Verified: true,
			Note: "the completed string; the buffer is dead after it",
			Variants: []Variant{
				{Name: "strbuf.finish", Args: []string{"raw"}, When: []Type{Of(MaskAny)},
					Go: "StrBufFinish", Effects: Pure, Result: Of(MaskString)},
			},
		},

		// ---- the compiler's own opcodes ----
		//
		// A handful of library calls the compiler specializes into opcodes, so
		// they never reach the library table at all and need an implementation
		// on this side of it.
		{Name: "StrLen", Group: "string", Symbol: "strlen", Params: []Type{Of(MaskAny)},
			Result: Of(MaskInt), Effects: Deprecates | Throws | ReEnters, Verified: true,
			Note: "ZEND_STRLEN: the ordinary string coercion then a byte count, so __toString runs and an array is a TypeError",
			Variants: []Variant{
				{Name: "strlen", Ctx: true, Ret: "err", Args: []string{"", "strict"},
					When: []Type{Of(MaskAny)}, Go: "StrLen",
					Effects: Deprecates | Throws | ReEnters, Result: Of(MaskInt)},
			},
		},
		{Name: "GetType", Group: "variable", Symbol: "gettype", Params: []Type{Of(MaskAny)},
			Result: Of(MaskString), Effects: Pure, Verified: true,
			Note: "ZEND_GET_TYPE: the OLD vocabulary -- integer, double, boolean, NULL -- not the one diagnostics use",
			Variants: []Variant{
				{Name: "get_type", When: []Type{Of(MaskAny)}, Go: "GetType",
					Effects: Pure, Result: Of(MaskString)},
			},
		},

		// ---- counting ----
		{Name: "Count", Group: "container", Symbol: "count", Params: []Type{Of(MaskAny)},
			Result: Of(MaskInt), Effects: Throws | ReEnters, Verified: true,
			Note: "PHP 8 takes only Countable|array; the TypeError names the CLASS for an object, not \"object\"",
			Variants: []Variant{
				// An array has a length; only an object has a `count()` to call.
				{Name: "count.array", Result: Of(MaskInt), When: []Type{ArrayOf(nil, nil)},
					Go: "CountArray", Effects: Pure},
				{Name: "count", Ctx: true, Ret: "err", When: []Type{Of(MaskAny)}, Go: "Count",
					Effects: Throws | ReEnters, Result: Of(MaskInt)},
			},
		},

		// ---- value materialisation ----
		{Name: "CopyValue", Group: "variable", Params: []Type{Of(MaskAny)},
			Result: Of(MaskAny), Effects: Pure, Verified: true,
			Note: "QM_ASSIGN: materialises a value into a temporary. The node copy elision elides",
			Variants: []Variant{
				{Name: "copy", Args: []string{"read"}, When: []Type{Of(MaskAny)},
					Go: "CopyValue", Effects: Pure},
			},
		},
		// ZEND_COALESCE materialises its operand the same way, and the copy
		// itself is identical -- what differs is that **its operand is read
		// silently**, because `$q ?? 5` on an unset `$q` emits nothing where
		// `echo $q` warns. That was carried as `mode=IS` on the instruction and
		// scanned for by two passes; it is a property of the intrinsic, so the
		// intrinsic says it.
		{Name: "CopyValueSilent", Group: "variable", Params: []Type{Of(MaskAny)},
			Result: Of(MaskAny), Effects: Pure, Verified: true,
			Note: "QM_ASSIGN under ??: the copy is the same, the read of its operand does not warn",
			Variants: []Variant{
				{Name: "copy.silent", Args: []string{"read"}, SilentReads: true,
					When: []Type{Of(MaskAny)}, Go: "CopyValue", Effects: Pure},
			},
		},

		// ---- array construction ----
		{Name: "NewArray", Group: "construction", Symbol: "[]", Params: nil,
			Result: Of(MaskArray), Effects: Pure, Verified: true,
			Note: "the accumulator an array literal is built into, threaded through each element",
			Variants: []Variant{
				{Name: "array.new", When: []Type{}, Go: "NewArrayValue", Effects: Pure, Result: Of(MaskArray)},
			},
		},
		{Name: "ArrayAppend", Group: "construction", Symbol: "[]=", Params: []Type{Of(MaskArray), Of(MaskAny)},
			Result: Of(MaskArray), Effects: Throws, Verified: true,
			Note: "the element is COPIED, so a later write to the source does not reach inside the literal; " +
				"it THROWS when the largest integer key is taken, which a literal is not exempt from",
			Variants: []Variant{
				{Name: "array.append", Ret: "err", When: []Type{Of(MaskArray), Of(MaskAny)}, Go: "ArrayAppend",
					Effects: Throws, Result: Of(MaskArray), PlaceGap: "array element by reference"},
			},
		},
		{Name: "ArrayAppendBound", Group: "construction", Symbol: "[]=&",
			Params: []Type{Of(MaskArray), Of(MaskAny)},
			Result: Of(MaskArray), Effects: Throws, Verified: true,
			Note: "`[&$x]`: the element KEEPS the box rather than copying through it, so a later write " +
				"through either name is visible from the other; the engine marks which element is meant " +
				"in extended_value's low bit -- `[$c]` is 4 and `[&$c]` is 5",
			Variants: []Variant{
				{Name: "array.append.bound", Ret: "err", When: []Type{Of(MaskArray), Of(MaskAny)},
					Go: "ArrayAppendBound", Effects: Throws, Result: Of(MaskArray)},
			},
		},
		{Name: "ArraySetBound", Group: "construction", Symbol: "[k]=&",
			Params: []Type{Of(MaskArray), Of(MaskScalar), Of(MaskAny)},
			Result: Of(MaskArray), Effects: Deprecates | Warns, Verified: true,
			Note: "ArrayAppendBound with a key: `[\"k\" => &$x]`",
			Variants: []Variant{
				{Name: "array.set.bound", Ctx: true,
					When: []Type{Of(MaskArray), Of(MaskScalar), Of(MaskAny)},
					Go:   "ArraySetBound", Effects: Deprecates | Warns, Result: Of(MaskArray)},
			},
		},
		{Name: "ArrayUnpack", Group: "construction", Symbol: "...", Params: []Type{Of(MaskArray), Of(MaskAny)},
			Result: Of(MaskArray), Effects: Throws | ReEnters, Verified: true,
			Note: "integer keys RENUMBER and string keys keep their name, which is about the SOURCE and does not " +
				"exempt the destination from saturating; " +
				"a scalar is an Error and a non-Traversable object a TypeError, which are different classes",
			Variants: []Variant{
				{Name: "array.unpack", Ctx: true, Ret: "err", When: []Type{Of(MaskArray), Of(MaskAny)},
					Go: "ArrayUnpack", Effects: Throws | ReEnters, Result: Of(MaskArray)},
			},
		},
		{Name: "ArraySet", Group: "construction", Symbol: "[k]=", Params: []Type{Of(MaskArray), Of(MaskScalar), Of(MaskAny)},
			Result: Of(MaskArray), Effects: Deprecates | Warns, Verified: true,
			Note: "the key goes through ArrayKey, which is where the float-precision deprecation lives",
			Variants: []Variant{
				// An int or string key never diagnoses, so knowing the key
				// type -- not just the container's shape -- makes the whole
				// operation pure.
				{Name: "array.set.key", When: []Type{Of(MaskArray), Of(MaskInt | MaskString), Of(MaskAny)},
					Go: "ArraySetKey", Effects: Pure, Result: Of(MaskArray),
					PlaceGap: "array element by reference"},
				{Name: "array.set", Ctx: true, When: []Type{Of(MaskArray), Of(MaskScalar), Of(MaskAny)}, Go: "ArraySet",
					Effects: Deprecates | Warns, Result: Of(MaskArray), PlaceGap: "array element by reference"},
			},
		},

		// ---- the iterator protocol ----
		//
		// The state is a **value**: `iter.new` returns an internal object
		// carrying the cursor, and every other step takes it as an ordinary
		// argument. It used to be a `*Iter` -- a Go type that is not a
		// php.Value -- which cost a representation type, a frame slot and an
		// argument shape that passed it through unconverted, and left mem2reg
		// unable to promote the slot because nothing else could name it.
		{Name: "IterNew", Group: "iteration", Symbol: "foreach", Params: []Type{Of(MaskAny)},
			Result: Of(MaskObject), Effects: Throws | ReEnters, Verified: true,
			Note: "a user Iterator's rewind runs here, so this re-enters and can throw before the first step",
			Variants: []Variant{
				// `iter.new`, not `iter.reset`: it is the **constructor**, and it
				// takes the subject rather than an iterator. Every other member
				// of the family takes the instance as its first argument, which
				// is the rule -- and `array.new` and `strbuf.new` are the same
				// exception for the same reason. Calling it `reset` put a
				// constructor in the stepping family and read as though there
				// were an iterator already.
				// Over an **array** there is no rewind to run, so the whole
				// reason the general form takes a ctx and can throw is absent.
				// This is the shape of specialization a flat mask cannot
				// express: "may be an array" buys nothing, "is an array" buys
				// the error path.
				// Local like the general form, because an operation has one
				// home: the constructor moved into the generated unit when the
				// plain-object case needed the class table, and a family split
				// across two homes is the mistake the catalogue test names.
				// What is *value*-level about it still lives in runtime/php and
				// this calls it.
				{Name: "iter.new.array", Local: true, Result: Of(MaskObject),
					When: []Type{ArrayOf(nil, nil)},
					Go:   "newArrayIterator", Effects: Pure},
				// Local, because a **plain object** iterates the properties
				// visible from the calling scope, and neither the scope nor the
				// class table exists in this package.
				// A place here is **read**: the by-value reset takes a
				// snapshot, so a copy is what it wanted anyway.
				{Name: "iter.new", Ctx: true, Local: true, Ret: "err",
					Args: []string{"", "scope"}, When: []Type{Of(MaskAny)},
					Go: "newIterator", Effects: Throws | ReEnters, Result: Of(MaskObject)},
			},
		},
		// A separate **operation**, not a variant of IterNew: a variant is
		// chosen by the operand types, and these two differ by the *opcode* the
		// engine emitted -- FE_RESET_R against FE_RESET_RW -- over the same
		// array. What they build differs too: a snapshot against a live walk,
		// which is the difference between a loop seeing an element the body
		// appends and not seeing it.
		{Name: "IterNewRef", Group: "iteration", Symbol: "foreach &", Params: []Type{Of(MaskAny)},
			Result: Of(MaskObject), Effects: Throws | ReEnters, Verified: true,
			Note: "the by-reference reset; over an array it holds the live source, and anything else " +
				"falls back to the ordinary constructor rather than being refused",
			Variants: []Variant{
				// Takes the scope for the same reason `iter.new` does: a plain
				// object walked by reference binds to the properties **visible
				// from the calling scope**, and which those are is not a
				// question this package can answer.
				{Name: "iter.new.ref", Ctx: true, Local: true, Ret: "err",
					Args: []string{"", "scope"}, When: []Type{Of(MaskAny)},
					Go: "newRefIterator", Effects: Throws | ReEnters, Result: Of(MaskObject),
					PlaceBox: true},
			},
		},
		{Name: "IterCurrentRef", Group: "iteration", Params: []Type{Of(MaskObject)},
			Result: Of(MaskAny), Effects: Throws | ReEnters, Verified: true,
			Note: "the by-reference fetch: over an array it answers the ELEMENT'S BOX so the loop " +
				"variable binds to it, and over anything else it falls back to the by-value read",
			Variants: []Variant{
				{Name: "iter.current.ref", Ctx: true, Ret: "err", When: []Type{Of(MaskAny)},
					Go: "IteratorCurrentRef", Effects: Throws | ReEnters},
			},
		},
		{Name: "IterValid", Group: "iteration", Params: []Type{Of(MaskObject)},
			Result: Of(MaskBool), Effects: Pure, Verified: true,
			Note: "reads what iter.next cached; the user Iterator's valid() ran there, not here",
			Variants: []Variant{
				{Name: "iter.valid", GoScalar: "bool", When: []Type{Of(MaskAny)},
					Go: "IteratorValid", Effects: Pure, Result: Of(MaskBool)},
			},
		},
		{Name: "IterCurrent", Group: "iteration", Params: []Type{Of(MaskObject)},
			Result: Of(MaskAny), Effects: Throws | ReEnters, Verified: true,
			Note: "a user Iterator's current() runs here, so it re-enters",
			Variants: []Variant{
				{Name: "iter.current.array", When: []Type{IterOfArray(nil, nil)},
					Go: "IteratorCurrentArray", Effects: Pure},
				{Name: "iter.current", Ctx: true, Ret: "err", When: []Type{Of(MaskAny)},
					Go: "IteratorCurrent", Effects: Throws | ReEnters},
			},
		},
		{Name: "IterKey", Group: "iteration", Params: []Type{Of(MaskObject)},
			Result: Of(MaskAny), Effects: Throws | ReEnters, Verified: true,
			Note: "an array yields int|string keys; a user Iterator's key() may yield anything",
			Variants: []Variant{
				{Name: "iter.key.array", When: []Type{IterOfArray(nil, nil)},
					Go: "IteratorKeyArray", Effects: Pure},
				{Name: "iter.key", Ctx: true, Ret: "err", When: []Type{Of(MaskAny)},
					Go: "IteratorKey", Effects: Throws | ReEnters},
			},
		},
		{Name: "IterNext", Group: "iteration", Params: []Type{Of(MaskObject)},
			Result: Of(MaskObject), Effects: Throws | ReEnters, Verified: true,
			Note: "a user Iterator's next() runs here, and the valid() the engine pairs with it; it RETURNS the advanced state, so the stepping is a def-use chain rather than a hidden mutation",
			Variants: []Variant{
				// The result keeps the array backing, so the *next* round of
				// the loop narrows too -- without it the fixed point would
				// widen the cursor to a plain object on the back edge and only
				// the first iteration would specialize.
				{Name: "iter.next.array", When: []Type{IterOfArray(nil, nil)},
					Go: "IteratorNextArray", Effects: Pure, Result: IterOfArray(nil, nil)},
				{Name: "iter.next", Ctx: true, Ret: "err", When: []Type{Of(MaskAny)},
					Go: "IteratorNext", Effects: Throws | ReEnters, Result: Of(MaskObject)},
			},
		},
		{Name: "IterFree", Group: "iteration", Params: []Type{Of(MaskObject)},
			Result: Type{}, Effects: Pure, Verified: true,
			Note: "runs even when the loop exits early, which is why it is an instruction rather than a scope",
			Variants: []Variant{
				{Name: "iter.free", Ret: "none", When: []Type{Of(MaskAny)},
					Go: "IteratorFree", Effects: Pure},
			},
		},
		// The **owning** free: the loop's subject was a temporary, so ending
		// the loop destroys it. For an abandoned generator that is when its
		// `finally` runs, which is user code -- so unlike the plain free this
		// one re-enters and can throw.
		{Name: "IterFreeOwned", Group: "iteration", Params: []Type{Of(MaskObject)},
			Result: Type{}, Effects: Throws | ReEnters, Verified: true,
			Note: "a generator held only by the loop is destroyed here, and its finally runs; measured " +
				"against one held in a variable, which waits for the unset, and one held to the end of " +
				"the script, which waits for shutdown",
			Variants: []Variant{
				{Name: "iter.free.owned", Ctx: true, Ret: "errno", When: []Type{Of(MaskAny)},
					Go: "IteratorFreeOwned", Effects: Throws | ReEnters},
			},
		},

		// ---- variables and references ----
		{Name: "ReadVar", Group: "variable", Symbol: "$x", Params: []Type{Of(MaskAnyRef)},
			Result: Of(MaskAny), Effects: Warns, Verified: true,
			Note: "an unset read warns; a bound reference reads through",
			Variants: []Variant{
				// The name is an argument because the warning names the
				// variable, and the silent form is a separate variant because
				// `$q ?? 5` must not warn -- the engine has one opcode for it
				// and the suppression is intrinsic to it, not a hint.
				{Name: "read_var", Ctx: true, Args: []string{"var"},
					When: []Type{Of(MaskAnyRef)}, Go: "ReadVar", Effects: Warns},
			},
		},
		{Name: "ReadVarSilent", Group: "variable", Symbol: "$x", Params: []Type{Of(MaskAnyRef)},
			Result: Of(MaskAny), Effects: Pure, Verified: true,
			Note: "the IS form: reads without the undefined-variable warning, because `??` asks whether it is set",
			Variants: []Variant{
				{Name: "read_var.silent", Args: []string{"place"},
					When: []Type{Of(MaskAnyRef)}, Go: "ReadVarSilent", Effects: Pure},
			},
		},
		{Name: "WriteVar", Group: "variable", Symbol: "=", Params: []Type{Of(MaskAnyRef), Of(MaskAny)},
			Result: Type{}, Effects: Throws, Verified: true,
			Note: "writes THROUGH a reference, and COPIES: `$c = $b; $c[0] = 99` leaves $b alone",
			Variants: []Variant{
				// **Fallible**, because a reference can be typed: one bound to
				// a typed property carries that property's declaration, and a
				// write through any name sharing the box is checked against it.
				{Name: "write_var", Ctx: true, Ret: "errno", Args: []string{"ptr", "", "strict"},
					When: []Type{Of(MaskAnyRef), Of(MaskAny)}, Go: "StoreVar", Effects: Throws},
			},
		},
		{Name: "BindVar", Group: "variable", Symbol: "=&", Params: []Type{Of(MaskAnyRef), Of(MaskAnyRef)},
			Result: Type{}, Effects: Pure, Verified: true,
			Note: "the slot BECOMES the box; copying here would sever the link the `&` exists to make",
			Variants: []Variant{
				{Name: "bind_var", Ret: "none", Args: []string{"ptr", ""},
					When: []Type{Of(MaskAnyRef), Of(MaskAnyRef)}, Go: "BindVar", Effects: Pure},
			},
		},
		{Name: "BindRef", Group: "variable", Params: []Type{Of(MaskAnyRef), Of(MaskAnyRef)},
			Result: Type{}, Effects: Pure, Verified: true,
			Note: "boxes the source if it is not boxed, so both names share one box"},
		{Name: "RefArg", Group: "variable", Params: []Type{Of(MaskAnyRef)},
			Result: Of(MaskRef), Effects: Pure, Verified: true,
			Note: "a fresh local is boxed at the call site, so the callee's writes reach it"},

		{Name: "NoRefArg", Group: "variable", Params: []Type{Of(MaskAnyRef)},
			Result: Of(MaskAnyRef), Effects: Warns | Throws | ReEnters, Verified: true,
			Note: "SEND_VAR_NO_REF: a non-variable sent to a by-reference parameter notices " +
				"`Only variables should be passed by reference` and is detached; a reference passes"},
		{Name: "RefReturn", Group: "variable", Params: []Type{Of(MaskAnyRef)},
			Result: Of(MaskRef), Effects: Warns | Throws | ReEnters, Verified: true,
			Note: "RETURN_BY_REF of a non-variable notices `Only variable references should be " +
				"returned by reference` and wraps it; a call's result that is a reference passes"},
		{Name: "AsRef", Group: "variable", Params: []Type{Of(MaskAnyRef)},
			Result: Of(MaskRef), Effects: Pure, Verified: true,
			Note: "RETURN_BY_REF of a promoted variable: the value in the reference the engine returns"},
		{Name: "Unbox", Group: "variable", Params: []Type{Of(MaskAnyRef)},
			Result: Of(MaskAny), Effects: Pure, Verified: true,
			Note: "RefArg's other end: a BY-VALUE parameter takes the value out of the box the " +
				"caller sent, because a dynamically dispatched send cannot know which the callee wanted"},

		// ---- existence ----
		// `is_int`, `is_float`, `is_string`, `is_array`, `is_object`, `is_null`,
		// `is_bool` and `is_scalar` are **one** operation, not eight: the engine
		// compiles every one of them to ZEND_TYPE_CHECK with a type mask, and
		// the mask is the only thing that differs. `is_numeric` is deliberately
		// not among them -- it stays a library call, because it asks whether a
		// *string* would read as a number rather than what type the value is.
		//
		// The mask is carried by the instruction rather than computed, which is
		// what the `int` argument shape is for.
		{Name: "TypeCheck", Group: "existence", Symbol: "is_*", Params: []Type{Of(MaskAny)},
			Result: Of(MaskBool), Effects: Pure, Verified: true,
			Note: "one operation for the whole is_* family; the mask says which types pass",
			Variants: []Variant{
				{Name: "type_check", GoScalar: "bool", Args: []string{"", "int"},
					When: []Type{Of(MaskAny)}, Go: "TypeCheck", Effects: Pure, Result: Of(MaskBool)},
			},
		},
		{Name: "IsSet", Group: "existence", Symbol: "isset", Params: []Type{Of(MaskAnyRef)},
			Result: Of(MaskBool), Effects: Pure, Verified: true,
			Note: "set AND NOT NULL, and never warns: the missing case is the question",
			Variants: []Variant{
				{Name: "is_set", GoScalar: "bool", When: []Type{Of(MaskAnyRef)},
					Go: "IsSet", Effects: Pure, Result: Of(MaskBool)},
			},
		},

		// ---- containers ----
		{Name: "ReadDim", Group: "container", Symbol: "[]", Params: []Type{Of(MaskAnyRef), Of(MaskScalar)},
			Result: Of(MaskAny), Effects: Warns | Deprecates | Throws, Verified: true,
			Note: "a string is a container; a non-container warns rather than yielding null silently",
			Variants: []Variant{
				// An array subject cannot throw: throwing is what an offset on
				// a non-container does. The missing-key warning stays, because
				// that is the read's own diagnostic, and so does the float-key
				// deprecation.
				{Name: "dim.get.array", Ctx: true,
					When: []Type{ArrayOf(nil, nil), Of(MaskScalar)},
					Go:   "ReadDimArray", Effects: Warns | Deprecates},
				{Name: "dim.get", Ctx: true, Ret: "err",
					When: []Type{Of(MaskAnyRef), Of(MaskScalar)}, Go: "ReadDim",
					Effects: Warns | Deprecates | Throws},
			},
		},
		// The IS form is its own intrinsic, not a flag on the one above, for
		// the reason ReadVar/ReadVarSilent are two: the suppression is
		// intrinsic to the engine's opcode. It was a `silent bool` argument,
		// and that understated what differs -- the two modes **return
		// different values**. `$s[9] ?? "d"` is `"d"` because an out-of-range
		// offset is *not set*, where the loud read is `""` with a warning. A
		// boolean parameter that changes the answer is a second function
		// wearing the first one's name, and the effect contract could not tell
		// the truth about either: one signature had to claim `Warns` for a form
		// that does not warn.
		{Name: "ReadDimSilent", Group: "container", Symbol: "[]", Params: []Type{Of(MaskAnyRef), Of(MaskScalar)},
			Result: Of(MaskAny), Effects: Deprecates | Throws, Verified: true,
			Note: "the IS form: an out-of-range string offset is NOT SET, so null rather than \"\"",
			Variants: []Variant{
				// No warning and no throw: an array is a container, so the
				// offset error cannot arise. The float-key deprecation still
				// can, which is why this is not `Pure` and still takes a `*Ctx`
				// -- settled by probing the engine, not by reading ArrayKey.
				{Name: "dim.get.array.silent", Ctx: true, SilentReads: true,
					When: []Type{ArrayOf(nil, nil), Of(MaskScalar)},
					Go:   "ReadDimArraySilent", Effects: Deprecates},
				{Name: "dim.get.silent", Ctx: true, Ret: "err", SilentReads: true,
					When: []Type{Of(MaskAnyRef), Of(MaskScalar)}, Go: "ReadDimSilent",
					Effects: Deprecates | Throws},
			},
		},
		// Destructuring: ZEND_FETCH_LIST_R. Measured: `[$a, $b] = "str"`,
		// `[$c] = null` and `[$d] = 5` give NULL and say nothing, nested ones
		// too; an array warns about a missing key as any read does, and an
		// object is asked through ArrayAccess.
		{Name: "ReadDimList", Group: "container", Symbol: "list()", Params: []Type{Of(MaskAnyRef), Of(MaskScalar)},
			Result: Of(MaskAny), Effects: Warns | Deprecates | Throws, Verified: true,
			Note: "a destructuring read: NULL, silently, from anything that is not an array or an object",
			Variants: []Variant{
				{Name: "dim.get.list.array", Ctx: true,
					When: []Type{ArrayOf(nil, nil), Of(MaskScalar)},
					Go:   "ReadDimArray", Effects: Warns | Deprecates},
				{Name: "dim.get.list", Ctx: true, Ret: "err",
					When: []Type{Of(MaskAnyRef), Of(MaskScalar)}, Go: "ReadDimList",
					Effects: Warns | Deprecates | Throws},
			},
		},
		// `$a[$k] = $v` is **one operation**, so it is one call: the container,
		// the key and the value. It was a place-producing instruction followed
		// by a `store`, and the store dispatched on the place's *shape at run
		// time* -- `p.direct`, then `p.slot != nil`, then `p.recv` -- to decide
		// whether it was writing a slot, a dimension or a property. The
		// lowering already knew which, because it had just emitted `dim.ref`
		// rather than `prop.ref`. Naming the operation puts that knowledge in
		// the IR instead of re-deriving it from struct fields.
		{Name: "WriteDim", Group: "container", Symbol: "[]=", Params: []Type{Of(MaskAnyRef), Of(MaskScalar), Of(MaskAny)},
			Result: Of(MaskAny), Effects: Warns | Deprecates | Throws, Verified: true,
			Note: "autovivifies from null; writes a string BY OFFSET rather than converting it",
			Variants: []Variant{
				{Name: "dim.set", Ctx: true, Ret: "err", Args: []string{"ptr", "", ""},
					When: []Type{Of(MaskAnyRef), Of(MaskScalar), Of(MaskAny)},
					Go:   "WriteDim", Effects: Warns | Deprecates | Throws},
			},
		},
		{Name: "AppendDim", Group: "container", Symbol: "[]=", Params: []Type{Of(MaskAnyRef), Of(MaskAny)},
			Result: Of(MaskAny), Effects: Throws, Verified: true,
			Note: "`$a[] = v` chooses the next integer index at write time; appending to a string is its own error",
			Variants: []Variant{
				{Name: "dim.append", Ctx: true, Ret: "err", Args: []string{"ptr", ""},
					When: []Type{Of(MaskAnyRef), Of(MaskAny)},
					Go:   "AppendDim", Effects: Throws},
			},
		},
		{Name: "ReadStaticProp", Group: "property", Symbol: "::$", Params: nil,
			Result: Of(MaskAny), Effects: Throws, Verified: true,
			Note: "an undeclared static property is an Error, not a warning and a null",
			Variants: []Variant{
				{Name: "sprop.get", Ctx: true, Local: true, Ret: "err",
					Args: []string{"lit", "class", "scope"}, When: nil,
					Go: "staticGet", Effects: Throws},
			},
		},
		{Name: "ReadStaticPropSilent", Group: "property", Symbol: "::$", Params: nil,
			Result: Of(MaskAny), Effects: Pure, Verified: true,
			Note: "the IS form: `isset(C::$nope)` is false and `C::$nope ?? \"d\"` is \"d\", with no Error",
			Variants: []Variant{
				// Genuinely pure: there is no slot to warn about and no Error to
				// raise, because answering **not set** is the whole point. The
				// loud form's Throws is the difference the pair exists to carry.
				// `Ctx` is a **binding** fact and `Effects` a behavioural one:
				// this variant takes the ctx only because the slot lookup it
				// calls is spelled that way, and it still emits nothing and
				// raises nothing. The two axes are independent, and the first
				// variant to separate them is this one.
				{Name: "sprop.get.silent", Ctx: true, Local: true, SilentReads: true,
					Args: []string{"lit", "class", "scope"}, When: nil,
					Go: "staticGetSilent", Effects: Pure},
			},
		},
		{Name: "WriteStaticProp", Group: "property", Symbol: "::$", Params: []Type{Of(MaskAny)},
			Result: Of(MaskAny), Effects: Throws, Verified: true,
			Note: "a static property is a plain slot owned by a class: no __set, no hooks, no autovivification",
			Variants: []Variant{
				// The class is a **reference** rather than a value, so it has
				// its own argument shape; `self`/`parent`/`static` resolve
				// against the emitting scope. Access to an undeclared static
				// property is an Error, which is the only effect left -- there
				// is no magic accessor to re-enter and nothing to warn about.
				{Name: "sprop.set", Ctx: true, Local: true, Ret: "err",
					Args: []string{"lit", "class", "", "scope"},
					When: []Type{Of(MaskAny)},
					Go:   "staticSet", Effects: Throws},
			},
		},
		{Name: "WriteProp", Group: "property", Symbol: "->", Params: []Type{Of(MaskObject), Of(MaskAny)},
			Result: Of(MaskAny), Effects: Warns | Throws | ReEnters, Verified: true,
			Note: "__set and property hooks run here; readonly and visibility are checked before the write",
			Variants: []Variant{
				// The scope is supplied by the emitter, not carried by the IR,
				// and the gap filler takes such arguments from the END -- so
				// the value comes before it, and `propSet` is spelled to match.
				// SilentReads: the receiver is fetched **for write**, and a
				// write fetch does not warn about an undefined variable.
				{Name: "prop.set", Ctx: true, Local: true, Ret: "err", SilentReads: true,
					Args:     []string{"obj", "lit", "", "scope", "strict"},
					PlaceGap: "nested write through a place",
					When:     []Type{Of(MaskAny), Of(MaskAny)},
					Go:       "propSet", Effects: Warns | Throws | ReEnters},
			},
		},
		// ---- property access ----
		//
		// Local bindings: which properties a class has, and which are visible
		// from here, are properties of the **compiled unit** rather than of
		// PHP, so the implementation is the generated program's own helper.
		// QUALITY_GATE.md calls this not-shared, and saying so in the catalogue
		// is better than pretending the function lives in runtime/php.
		{Name: "PropGet", Group: "property", Symbol: "->", Params: []Type{Of(MaskObject)},
			Result: Of(MaskAny), Effects: Warns | Throws | ReEnters, Verified: true,
			Note: "__get runs here, so it re-enters; an undefined property warns and yields null",
			Variants: []Variant{
				{Name: "prop.get", Ctx: true, Local: true, Ret: "err", Args: []string{"obj", "lit", "scope"},
					When: []Type{Of(MaskAny)}, Go: "propGet", Effects: Warns | Throws | ReEnters},
			},
		},
		{Name: "PropGetSilent", Group: "property", Symbol: "->", Params: []Type{Of(MaskObject)},
			Result: Of(MaskAny), Effects: Throws | ReEnters, Verified: true,
			Note: "the IS form: `$o->missing ?? \"d\"` does not warn, but __get still runs and can throw",
			Variants: []Variant{
				{Name: "prop.get.silent", Ctx: true, Local: true, Ret: "err", SilentReads: true,
					Args: []string{"obj", "lit", "scope"},
					When: []Type{Of(MaskAny)}, Go: "propGetSilent", Effects: Throws | ReEnters},
			},
		},
		{Name: "PropPlace", Group: "property", Symbol: "->", Params: []Type{Of(MaskObject)},
			Result: Type{}, Effects: Pure, Verified: true,
			Note: "builds the write target; the visibility check and __set happen when it is written",
			Variants: []Variant{
				// **SilentReads**: the container of a *write* fetch is read
				// without the undefined-variable warning. `$d->p['x'] = 1` on
				// an undefined `$d` warns about nothing in the engine -- it
				// refuses with `Attempt to modify property "p" on null` and
				// says no more. The dimension form already behaves this way
				// because `dim.set` takes the place itself; a property fetch
				// needs the container as a value, so the read was loud.
				{Name: "prop.ref", Local: true, Args: []string{"obj", "lit", "scope"},
					When: []Type{Of(MaskAny)}, Go: "propPlace", Effects: Pure,
					SilentReads: true,
					PlaceGap:    "nested write through a place"},
			},
		},
		// A **read-write** place fetch is its own operation, not `PropPlace`
		// with a flag, because the two answer differently: the write form reads
		// its container silently (`$d->p = 1` on an undefined `$d` says
		// nothing) while the read-write form reads it out loud (`$d->p .= "s"`
		// warns `Undefined variable $d`). The mode chooses the callee at
		// lowering and is spent there, which is the same shape `dim.get` versus
		// `dim.get.silent` has -- a flag would have to survive to a pass that
		// deliberately drops it.
		{Name: "PropPlaceRW", Group: "property", Symbol: "->", Params: []Type{Of(MaskObject)},
			Result: Type{}, Effects: Pure, Verified: true,
			Note: "the RW fetch: same place, but the container read keeps its diagnostics",
			Variants: []Variant{
				{Name: "prop.ref.rw", Local: true, Args: []string{"obj", "lit", "scope"},
					When: []Type{Of(MaskAny)}, Go: "propPlaceRW", Effects: Pure,
					PlaceGap: "nested write through a place"},
			},
		},
		// The unset form of a property fetch. `unset($o->a[0])` on an
		// **uninitialized** property is allowed -- there is nothing to modify,
		// so neither readonly nor `private(set)` refuses it, and not even from
		// outside the class -- while `$o->a[0] = 9` on that same property is
		// `Cannot indirectly modify readonly property`. One place, two answers,
		// decided by the operation.
		{Name: "PropPlaceUnset", Group: "property", Symbol: "->", Params: []Type{Of(MaskObject)},
			Result: Type{}, Effects: Pure, Verified: true,
			Note: "the unset fetch: an uninitialized property has nothing to refuse about",
			Variants: []Variant{
				{Name: "prop.ref.unset", Local: true, Args: []string{"obj", "lit", "scope"},
					When: []Type{Of(MaskAny)}, Go: "propPlaceUnset", Effects: Pure,
					SilentReads: true,
					PlaceGap:    "nested write through a place"},
			},
		},
		{Name: "DimPlace", Group: "container", Symbol: "[]", Params: []Type{Of(MaskAnyRef), Of(MaskScalar)},
			Result: Type{}, Effects: Pure, Verified: true,
			Note: "carries the SLOT, not the container, so `$u[2] = \"x\"` on an unset $u autovivifies",
			Variants: []Variant{
				{Name: "dim.ref", Local: true, Args: []string{"ptr", ""},
					When: []Type{Of(MaskAnyRef), Of(MaskScalar)}, Go: "dimPlace", Effects: Pure},
			},
		},
		// The dimension twins of PropPlaceRW. They exist as separate operations
		// because the **emitter** has to treat them differently in two places
		// a flag could not reach: the container's `Undefined variable` warning
		// is emitted as a prelude, before the key is evaluated -- `$a[$b] += 1`
		// warns about `$a` first -- and a nested fetch opens the outer place
		// with a different opener, one that warns about a missing key (RW) or
		// refuses to create one at all (UNSET).
		// The container warning of a read-write or unset dimension fetch, as its
		// **own instruction** so it lands before the key is read. `$a[$b] += 1`
		// warns about `$a` and then about `$b`, and the key's read is hoisted
		// ahead of the fetch by ExplicitReads -- so a warning emitted by the
		// fetch itself, or in its argument prelude, comes out second.
		{Name: "WarnUndefinedContainer", Group: "variable", Params: nil,
			Result: Type{}, Effects: Warns, Verified: true,
			Note: "`$a[$b] += 1` and `unset($a[$b])` warn about $a before evaluating $b",
			Variants: []Variant{
				{Name: "warn.undefined_container", Ctx: true, Ret: "none",
					Args: []string{"ptr", "lit"}, When: nil, Go: "WarnIfUndefinedVar"},
			},
		},
		// array_key_exists() has its own opcode, so it is an operation rather
		// than a library call: unlike isset() it reports true for a key whose
		// value is null, and unlike the dynamic call it raises the argument
		// TypeError itself.
		{Name: "ArrayKeyExists", Group: "container", Params: []Type{Of(MaskScalar), Of(MaskArray)},
			Result: Of(MaskBool), Effects: Throws, Verified: true,
			Note: "true for a null-valued key, where isset() is false; a non-array argument is a TypeError",
			Variants: []Variant{
				{Name: "array_key_exists", Ctx: true, Ret: "err",
					Args: []string{"", ""}, When: nil, Go: "ArrayKeyExists"},
			},
		},
		{Name: "DimPlaceRW", Group: "container", Symbol: "[]", Params: []Type{Of(MaskAnyRef), Of(MaskScalar)},
			Result: Type{}, Effects: Warns, Verified: true,
			Note: "the RW fetch warns about an undefined container and then autovivifies it",
			Variants: []Variant{
				{Name: "dim.ref.rw", Ctx: true, Local: true, Args: []string{"ptr", ""},
					When: []Type{Of(MaskAnyRef), Of(MaskScalar)}, Go: "dimPlaceRW", Effects: Warns},
			},
		},
		{Name: "DimPlaceUnset", Group: "container", Symbol: "[]", Params: []Type{Of(MaskAnyRef), Of(MaskScalar)},
			Result: Type{}, Effects: Warns, Verified: true,
			Note: "`unset($x[0])` warns about an undefined $x and does NOT autovivify it",
			Variants: []Variant{
				{Name: "dim.ref.unset", Local: true, Args: []string{"ptr", ""},
					When: []Type{Of(MaskAnyRef), Of(MaskScalar)}, Go: "dimPlace", Effects: Warns},
			},
		},
		{Name: "StaticPropPlace", Group: "property", Symbol: "::$", Params: nil,
			Result: Type{}, Effects: Throws, Verified: true,
			Note: "a static property is written directly, not as a dimension: the place IS the slot",
			Variants: []Variant{
				// The only place builder that can fail. `dim.ref` and
				// `prop.ref` are total -- they defer every check to the write --
				// but an undeclared static property is an Error, and the lookup
				// is where it surfaces.
				{Name: "sprop.ref", Ctx: true, Local: true, Ret: "err",
					Args: []string{"lit", "class", "scope"}, When: nil,
					Go: "staticPlace", Effects: Throws},
			},
		},
		{Name: "AppendPlace", Group: "container", Symbol: "[]=", Params: []Type{Of(MaskAnyRef)},
			Result: Type{}, Effects: Pure, Verified: true,
			Note: "`$a[] = v` has no key; the next integer index is chosen at write time",
			Variants: []Variant{
				// `.ref` because this is the **fetch**, not the write: it
				// materialises a place for a by-reference bind or an unset,
				// where `dim.append` above is `$a[] = v` itself.
				{Name: "dim.append.ref", Local: true, Args: []string{"ptr"},
					When: []Type{Of(MaskAnyRef)}, Go: "appendPlace", Effects: Pure},
			},
		},

		// ---- the frame ----
		//
		// These answer about the *call*, not about a value, which is why they
		// are intrinsics and not library functions: `runtime/php` has no frame
		// to ask. The generated unit has the argument slice in hand.
		{Name: "FuncGetArgs", Group: "frame", Symbol: "func_get_args", Params: nil,
			Result: Of(MaskArray), Effects: Pure, Verified: true,
			Note: "the arguments **passed**, not the parameters declared: `f($a, $b = 2)` called `f(1)` gives `[1]`",
			Variants: []Variant{
				// Ctx, because an **extra named argument** is not one of the
				// call's arguments here and only the frame records which of
				// the trailing ones arrived under a name.
				{Name: "func.args", Ctx: true, Local: true, Args: []string{"frameargs"},
					When: nil, Go: "funcGetArgs", Effects: Pure},
			},
		},
		{Name: "FuncNumArgs", Group: "frame", Symbol: "func_num_args", Params: nil,
			Result: Of(MaskInt), Effects: Pure, Verified: true,
			Note: "how many were passed, so it counts the same set func_get_args returns",
			Variants: []Variant{
				{Name: "func.num_args", Ctx: true, Local: true, Args: []string{"frameargs"},
					When: nil, Go: "funcNumArgs", Effects: Pure},
			},
		},

		// ---- properties ----
		{Name: "CheckPropertyRead", Group: "property", Params: []Type{Of(MaskObject)},
			Result: Of(MaskInt), Effects: Pure, Verified: true,
			Note: "an inherited private is INVISIBLE, not inaccessible; the message names the instance's class"},
		{Name: "CheckPropertyWrite", Group: "property", Params: []Type{Of(MaskObject)},
			Result: Of(MaskInt), Effects: Throws, Verified: true,
			Note: "readonly fails even inside the declaring class once initialised"},
		{Name: "CheckPropertyUnset", Group: "property", Params: []Type{Of(MaskObject)},
			Result: Of(MaskInt), Effects: Throws, Verified: true,
			Note: "same two-rule split as the write, with the verb changed: an initialised readonly refuses everywhere, an uninitialised one is a scope question"},
		{Name: "ReadDeclaredProp", Group: "property", Params: []Type{Of(MaskObject)},
			Result: Of(MaskAny), Effects: Throws, Verified: true,
			Note: "an uninitialised typed property is a third state, and reading it throws"},
	}
}

// Variant is one **type-specialized** implementation of an intrinsic.
//
// The whole point of a type-aware catalogue is that an intrinsic is not one
// function but a family: `+` on two ints cannot warn, cannot deprecate and
// cannot re-enter user code, while `+` on two unknowns can do all three. A
// consumer that knows the operand types can emit the narrow variant and skip
// the machinery the wide one needs.
//
// Selection is by *subset*: a variant applies when every operand's inferred
// type mask is contained in the variant's. That makes the general variant --
// masks of MaskAny -- the always-applicable fallback, so a consumer with no
// inference at all still gets correct code.
type Variant struct {
	// Name is the variant's **own** name, unique across the catalogue:
	// `add` is the general one, `add.int` the two-int specialization.
	//
	// Variants are named because the choice between them is made in a
	// pre-emission pass and has to travel *in the IR*: the instruction says
	// which intrinsic it calls, and the emitter translates that name to a Go
	// function without deciding anything. A variant that could only be
	// described -- "the pure one" -- could not be recorded, and a decision that
	// cannot be written down is one the emitter has to re-derive, which is the
	// failure IR-DESIGN.md names.
	Name string
	// When is the operand types this variant requires.
	When []Type
	// Go is the function in this package that implements it.
	Go string
	// Effects are what *this variant* can do, which is the narrowing that makes
	// specialization worth doing.
	Effects Effect
	// GoScalar names the Go type a variant's function returns when that is a
	// plain Go scalar rather than a `Value` -- "bool" for `StrictEqual`, "int"
	// for `Compare`. Empty means it returns a `Value`.
	//
	// The catalogue describes PHP types, but the emitter translates through the
	// Go binding, so the binding's shape has to be described too: a
	// `Result: MaskBool` says nothing about whether the function hands back
	// `true` or `php.Bool(true)`, and assuming wrongly is a build failure in
	// every generated program.
	//
	// This was a boolean field naming exactly one shape. The second shape then
	// arrived -- `Compare` returns an `int` -- and a boolean could not say so,
	// while the test that checked it only knew how to look for `bool` and so
	// reported nothing wrong. **A field that enumerates one case of a set is a
	// bug waiting for the second case**, and so is the test that checks it.
	GoScalar string
	// SilentReads marks an intrinsic whose **place operands are read without
	// the undefined-variable warning**, because the engine's opcode suppresses
	// it: `$q ?? 5`, `$a[$k] ?? 5` and `isset($o->p)` emit nothing where the
	// loud forms warn.
	//
	// This was the `mode=IS` attribute, scanned for as a string by the emitter
	// twice and by ExplicitReads once. The mode is a property of the *opcode*,
	// so it belongs where the opcode's meaning is recorded -- the intrinsic it
	// became -- rather than riding alongside as decoration three passes have to
	// agree about. Declaring it here is what lets the choice of read intrinsic
	// be made from the callee alone.
	SilentReads bool
	// Ctx marks a binding whose first Go parameter is the `*Ctx`. Most
	// intrinsics need it -- to warn, to write output, to reach the object
	// registry.
	//
	// It is a **binding** fact, not a behavioural one, and this comment used to
	// claim the two coincided: "the ones that do not are exactly the pure
	// ones". `sprop.get.silent` is the counterexample -- it takes the ctx
	// because the slot lookup it calls is spelled that way, and it emits
	// nothing and raises nothing. Read `Effects` for behaviour.
	Ctx bool
	// Local marks an intrinsic whose implementation is the **generated unit's
	// own helper** rather than a function in this package, because answering it
	// needs the program's class or function tables. `propGet` is the case:
	// property access is core runtime behaviour, but which properties exist is
	// a property of the compiled unit. QUALITY_GATE.md calls this not-shared,
	// and it is the honest way to say it rather than pretending the function
	// lives here.
	Local bool
	// Args names each argument's **shape at the call site**, parallel to When.
	// An empty string is an ordinary value; the others are:
	//
	//	"raw"    the operand verbatim, for one that is not a php.Value
	//	         (an *Iter)
	//	"obj"    a receiver: `$this` is the method's own receiver rather than
	//	         a local slot, so it does not render like an ordinary operand
	//	"lit"    the IR literal as a Go string literal, not a php.Value
	//	"ptr"    the address of a place: `&p_x`
	//	"place"  the place itself, unread: `p_x`. The one shape that does
	//	         **not** read, for the intrinsic that *is* the read
	//	"var"    the place *and its name*, from one operand: `p_x, "x"`.
	//	         The warning names the variable, and `$x` already says what
	//	         it is called -- carrying the name a second time in the IR
	//	         would be a copy nothing keeps honest
	//	"scope"  supplied by the emitter, not from the IR: the class whose
	//	         body is being emitted, which decides visibility
	//	"read"   the operand as read, honouring the instruction's access
	//	         mode: an IS read does not warn. `$q ?? 5` is one opcode in
	//	         the engine and the suppression is intrinsic to it
	//	"retctx" supplied by the emitter: the declared return type, the
	//	         function's name and its strict-types flag, which are
	//	         properties of the body rather than operands of the call
	//	"silent" supplied from the instruction's access mode: IS means the
	//	         read must not warn, which is a different operation rather
	//	         than a hint -- `$a[$k] ?? 5` sees NOT SET, not ""
	//
	// Empty means every argument is an ordinary value.
	Args []string
	// PlaceGap is what to refuse when a **place** reaches an argument that
	// wants a value -- an array element by reference, a foreach over a write
	// target. Empty means a place there is impossible rather than unhandled.
	PlaceGap string
	// PlaceBox says a place here is **opened**, not read: the argument wants
	// the storage box rather than a copy of what is in it.
	//
	// `foreach ($a as &$v)` is the whole of it. Reading the place would walk a
	// copy and every write through the loop variable would land in it and
	// vanish -- which is why this used to be a `PlaceGap` instead. It is not
	// the same as the by-value reset beside it: that one takes a snapshot, and
	// reading the place is exactly right there.
	PlaceBox bool
	// Ret names the binding's Go **return shape**, which is not the same
	// question as what PHP value it produces:
	//
	//	""       a Value
	//	"none"   nothing at all -- `echo`
	//	"err"    (Value, error)
	//	"errno"  error alone, with no value -- `IterNext`
	//
	// with GoScalar covering the plain-Go-scalar cases. Together these are the
	// whole of what a translator needs to know to write the call, which is the
	// point: an emitter that has to *infer* the shape is an emitter deciding
	// something, and it will decide wrongly for the case nobody tried.
	Ret string
	// Result is what *this variant* produces, narrower than the intrinsic's
	// where the operand types allow: `add.int` yields int|float where `add`
	// yields int|float|array. Zero means "the intrinsic's own result", so a
	// variant that narrows nothing need not repeat it.
	//
	// **Two ints do not make an int.** `PHP_INT_MAX + 1` is a float, and so are
	// `$m * 2` and `PHP_INT_MIN - 1`; `add.int` said MaskInt for as long as
	// nothing asked the engine. Bitwise really does stay int -- `PHP_INT_MAX << 1`
	// is int(-2) -- which is why the two families differ here, and why the
	// difference is settled rather than assumed.
	Result Type
	// Guard is a predicate in this package the consumer must call first, and
	// which emits the diagnostics the walk owes, reporting false when the
	// operands cannot be used at all. Empty when the variant needs none --
	// which is exactly when the variant is pure.
	Guard string
	// Fails names a second result reporting failure, for an operation that can
	// refuse rather than diagnose: `Div` answers false on a zero divisor
	// because raising DivisionByZeroError needs class vocabulary this package
	// does not have.
	Fails bool
	// FailClass and FailMessage are what the consumer raises when Fails fires.
	FailClass, FailMessage string
}

// Select returns the narrowest variant whose operand types contain the given
// ones, falling back to the last. A consumer with no type information passes
// `Of(MaskAny)` for every operand and gets the general variant, which is why
// inference is an optimisation rather than a prerequisite.
//
// Containment is `Type.Subsumes`, so it is **covariant in a container's
// elements**: a variant declared over `array<numeric>` applies to an
// `array<int>`, and one declared over `array<int>` does not apply to an array
// whose elements nobody has looked at.
func (s Signature) Select(operands []Type) Variant {
	for _, v := range s.Variants {
		if len(v.When) != len(operands) {
			continue
		}
		ok := true
		for i, want := range v.When {
			if !want.Subsumes(operands[i]) {
				ok = false
				break
			}
		}
		if ok {
			return v
		}
	}
	if len(s.Variants) == 0 {
		return Variant{Go: s.Name, Effects: s.Effects}
	}
	return s.Variants[len(s.Variants)-1]
}

// Variants is every named variant in the catalogue, flattened. This is the set
// an IR instruction may name.
func Variants() []Variant {
	var out []Variant
	for _, s := range Catalogue() {
		out = append(out, s.Variants...)
	}
	return out
}

// SymbolOf is the PHP spelling of the operator a variant implements.
func SymbolOf(variant string) string {
	for _, sig := range Catalogue() {
		for _, v := range sig.Variants {
			if v.Name == variant {
				return sig.Symbol
			}
		}
	}
	return ""
}

// SignatureOfVariant finds the intrinsic a variant belongs to. A variant name is
// the IR's own vocabulary -- `add.int` is what a call names -- so a consumer
// reading the IR needs the way back to the signature that owns it.
func SignatureOfVariant(variant string) (Signature, bool) {
	for _, sig := range Catalogue() {
		for _, v := range sig.Variants {
			if v.Name == variant {
				return sig, true
			}
		}
	}
	return Signature{}, false
}

// LookupVariant finds a variant by its own name. This is what the emitter uses:
// the choice was made before it ran, and it only has to translate.
func LookupVariant(name string) (Variant, bool) {
	for _, v := range Variants() {
		if v.Name == name {
			return v, true
		}
	}
	return Variant{}, false
}

// GeneralVariant is the always-applicable variant of an operation, which is what
// a consumer emits when inference proved nothing.
func GeneralVariant(intrinsic string) (Variant, bool) {
	s, ok := Lookup(intrinsic)
	if !ok || len(s.Variants) == 0 {
		return Variant{}, false
	}
	return s.Variants[len(s.Variants)-1], true
}

// Lookup finds an intrinsic by name.
func Lookup(name string) (Signature, bool) {
	for _, s := range Catalogue() {
		if s.Name == name {
			return s, true
		}
	}
	return Signature{}, false
}
