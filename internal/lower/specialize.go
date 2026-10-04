package lower

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/moriyoshi/php2go/runtime/php"
)

// Calls to intrinsics: forming them, and choosing their variant.
//
// # Intrinsics are functions, not opcodes
//
// `add` is an IR opcode: part of the compiler's instruction set. `add` and
// `add.int` are **intrinsics** -- essential builtin functions that drive the
// runtime, living in `runtime/php` with type-aware signatures. They are
// different layers, and an opcode *calls into* an intrinsic rather than being
// one.
//
// # Two steps, and only one of them is a choice
//
// Forming the call is **part of lowering**, not an optimisation:
//
//	%1:zval = add $a, 1     becomes     %1:zval = call.intrinsic "add"($a, 1)
//
// Every consumer of this IR sees a call, because that is what the operation is.
// `Lower` runs IntrinsicCalls before it returns, so there is no such thing as an
// IR with an `add` opcode in it -- and the goldens under testdata/ show calls,
// because the IR has calls.
//
// Choosing a *narrower variant* is the optimisation, and it is a choice about
// the consumer -- an interpreter would make a different one, or none:
//
//	%1:zval = call.intrinsic "add"(1, 2)  becomes  ... "add.int"(1, 2)
//
// That is Specialize, and it is optional in the honest sense: skipping it leaves
// correct code that calls the general variant.
//
// These were one pass at first, which made the *call form itself* look optional
// and left every golden showing opcodes -- so the IR that the docs described and
// the IR the goldens recorded were different languages. Splitting them is what
// makes "an operation is a call" true of the IR rather than true of one
// consumer's view of it.

// intrinsicFor maps an IR opcode to the operation it performs. An opcode with
// no entry is not an intrinsic call and is left alone.
// The keys are **opcodes the lowering emits**, which are the engine's own names
// lowercased: `bw_and`, not `bit_and`; `sl`, not `shift_left`. Six of the
// original entries were spelled the way the *catalogue* names the intrinsic, so
// they matched no opcode and the whole bitwise and shift family silently never
// became an intrinsic call -- `$n << 2` reached the emitter as `sl` and was
// refused as unsupported. TestIntrinsicForNamesRealOpcodes pins the two
// vocabularies against each other so a name cannot drift again.
var intrinsicFor = map[string]string{
	"add": "Add", "sub": "Sub", "mul": "Mul", "div": "Div", "mod": "Mod",
	"pow": "Pow", "concat": "Concat", "fast_concat": "Concat",
	"bw_and": "BitAnd", "bw_or": "BitOr", "bw_xor": "BitXor",
	"bw_not": "BitNot", "sl": "ShiftLeft", "sr": "ShiftRight",
	"is_equal": "LooseEqual", "is_identical": "StrictEqual",
	// `switch` compares with `==` and `match` with `===`, and each gets its own
	// opcode rather than reusing IS_EQUAL when the subject is a **temporary**:
	// the comparison must not free op1, because the next case needs it. That is
	// a difference in operand lifetime and none at all in the operation, so
	// they are the same two intrinsics.
	"case": "LooseEqual", "case_strict": "StrictEqual",
	"spaceship": "Compare",
	// array_key_exists() has its own opcode, so it is an operation rather than a
	// library call -- and the two differ: the opcode raises the argument
	// TypeError that the dynamic call gets from argument verification instead.
	"array_key_exists": "ArrayKeyExists",
	// The negations and the orderings. `is_equal` and `is_identical` were
	// intrinsics while their negations went through a second operator table in
	// the emitter -- a half-converted family, which is the asymmetry that keeps
	// biting here.
	"is_not_equal": "NotLooseEqual", "is_not_identical": "NotStrictEqual",
	"is_smaller": "IsSmaller", "is_smaller_or_equal": "IsSmallerOrEqual",
	"bool_xor": "BoolXor",

	// Not operators. An intrinsic is any **essential builtin that drives the
	// runtime**, so `echo` and the boolean coercion belong in the call layer
	// for the same reason `+` does: the IR should say what it calls.
	"echo": "Echo", "bool": "ToBool", "bool_not": "NotBool",
	"array.new": "NewArray", "array.append": "ArrayAppend", "array.set": "ArraySet",
	// `[&$x]`: the element keeps the box rather than copying through it.
	"array.append.bound": "ArrayAppendBound", "array.set.bound": "ArraySetBound",
	"array.unpack": "ArrayUnpack",
	"iter.new":     "IterNew", "iter.new.ref": "IterNewRef",
	"iter.current": "IterCurrent", "iter.current.ref": "IterCurrentRef",
	"iter.key":  "IterKey",
	"iter.next": "IterNext", "iter.free": "IterFree", "iter.free.owned": "IterFreeOwned",
	"iter.valid": "IterValid",
	"dim.get":    "ReadDim",
	"prop.get":   "PropGet", "prop.ref": "PropPlace", "prop.ref.rw": "PropPlaceRW",
	"prop.ref.unset": "PropPlaceUnset",
	"dim.ref":        "DimPlace", "dim.ref.rw": "DimPlaceRW", "dim.ref.unset": "DimPlaceUnset",
	"warn.undefined_container": "WarnUndefinedContainer",
	"dim.append.ref":           "AppendPlace",
	// The writes. `$a[$k] = $v` is one call, not a fetch plus a store.
	"dim.set": "WriteDim", "dim.append": "AppendDim", "prop.set": "WriteProp",
	"sprop.set": "WriteStaticProp", "sprop.get": "ReadStaticProp",
	"sprop.ref": "StaticPropPlace",
	"count":     "Count", "copy": "CopyValue", "is_set": "IsSet",
	"strlen": "StrLen", "get_type": "GetType",
	// The whole is_* family, which the engine compiles to ZEND_TYPE_CHECK with
	// a mask rather than to eight calls.
	"type_check":    "TypeCheck",
	"verify_return": "CoerceReturn",
	"strbuf.new":    "NewStrBuf", "strbuf.append": "StrBufAppend",
	"strbuf.finish": "StrBufFinish",
	// Every cast is an intrinsic. `(string)` shares ToString with the implicit
	// conversion, which is the same operation; the rest have their own.
	"to.string": "ToString", "to.int": "ToInt", "to.float": "ToFloat",
	"to.array": "ToArray", "to.object": "ToObject", "to.null": "ToNull",
	"to.bool": "ToBool",
	// The frame questions. Not library functions: the engine gives each its
	// own opcode because the answer is a property of the call.
	"func.args": "FuncGetArgs", "func.num_args": "FuncNumArgs",
}

// silentIntrinsicFor is the intrinsic an opcode becomes when it carries the
// engine's IS fetch mode. `$a[$k] ?? 5` is not `$a[$k]` with the warning turned
// off -- an out-of-range string offset is *not set* under IS and yields null,
// where the loud read yields `""`. Two questions, two answers, two intrinsics.
//
// An opcode with no entry here has no silent form, and its `mode=` is then not
// behavioural: W, RW and UNSET already chose their callee at lowering, where a
// dim write became `dim.ref` rather than `dim.get`.
var silentIntrinsicFor = map[string]string{
	"dim.get":   "ReadDimSilent",
	"prop.get":  "PropGetSilent",
	"sprop.get": "ReadStaticPropSilent",
	"copy":      "CopyValueSilent",
}

// listIntrinsicFor is the intrinsic a destructuring read (mode=LIST, from
// ZEND_FETCH_LIST_R) becomes: `[$a] = "str"` is NULL with no warning, where
// `"str"[0]` is "s"; an array and an object read as they always do.
var listIntrinsicFor = map[string]string{
	"dim.get": "ReadDimList",
}

// nonOperatorIntrinsics are the entries above that are not binary operators, so
// the test that checks this table against the lowering's binaryOps knows not to
// look for them there.
var nonOperatorIntrinsics = map[string]bool{
	"echo": true, "bool": true, "bool_not": true, "bw_not": true,
	"array.new": true, "array.append": true, "array.set": true,
	"array.append.bound": true, "array.set.bound": true,
	"array.unpack": true,
	"iter.new":     true, "iter.current": true, "iter.key": true,
	"iter.next": true, "iter.free": true, "iter.free.owned": true, "iter.valid": true, "dim.get": true,
	"prop.get": true, "prop.ref": true, "prop.ref.rw": true, "prop.ref.unset": true,
	"dim.ref":    true,
	"dim.ref.rw": true, "dim.ref.unset": true, "dim.append": true,
	"warn.undefined_container": true,
	"count":                    true, "copy": true, "is_set": true, "verify_return": true,
	"iter.current.ref": true, "iter.new.ref": true,
	"strlen": true, "get_type": true,
	"dim.set": true, "dim.append.ref": true, "prop.set": true,
	"sprop.set": true, "sprop.get": true, "sprop.ref": true,
	"strbuf.new": true, "strbuf.append": true,
	"strbuf.finish": true,
	// Every cast: unary, and named `to.<target>` by the lowering rather than
	// after the engine's opcode, because ZEND_CAST is one opcode whose
	// extended_value picks the target.
	"to.string": true, "to.int": true, "to.float": true, "to.array": true,
	"to.object": true, "to.null": true, "to.bool": true,
	"func.args": true, "func.num_args": true,
	// `type_check` takes one **value**; its second operand is the type mask the
	// instruction carries, not something the program computed. So it is unary
	// as far as this table is concerned, and listing it here is what keeps it
	// from looking like a binary operator whose name drifted.
	"type_check": true,
}

// UnaryIntrinsicOpcodes are the emitted opcodes in intrinsicFor that take one
// operand. Exported for the test that checks this table against the lowering's
// own, because a unary operator is not in binaryOps and would otherwise look
// like a name that drifted.
var UnaryIntrinsicOpcodes = nonOperatorIntrinsics

// IntrinsicOpcodes are the opcodes this table claims to rewrite.
func IntrinsicOpcodes() []string {
	out := make([]string, 0, len(intrinsicFor))
	for op := range intrinsicFor {
		out = append(out, op)
	}
	return out
}

// BinaryOpcodeNames is the set of opcodes the lowering emits for binary
// operators, so a test can check intrinsicFor against it rather than against a
// second hand-written list.
func BinaryOpcodeNames() map[string]bool {
	out := map[string]bool{}
	for n := range binaryOps {
		out[intrinsicName(n)] = true
	}
	return out
}

// IntrinsicNameFor reports the intrinsic an opcode is an operation of, so a
// consumer can tell "this should have been rewritten" from "I do not know this
// opcode". The two want different answers.
func IntrinsicNameFor(opcode string) (string, bool) {
	n, ok := intrinsicFor[opcode]
	return n, ok
}

// SlotSet writes a **frame slot**: an iterator handle, a rope buffer, a spilled
// temporary. It is not a PHP variable, which is why no runtime intrinsic writes
// it -- there is nothing in `runtime/php` that knows what an iterator slot is.
//
// Assignment to a variable is the `write_var` intrinsic and a by-reference
// parameter arriving is `bind_var`. All three were `local.set` until the split.
const SlotSet = "slot.set"

// ReleasePrev marks where a promoted store used to be, naming the value the
// local stopped holding there.
//
// **It is not a use of that value.** PHP destroys what `$a` held at the line
// that overwrites it; promotion removes that line, and with it the only place a
// release could be emitted. The renamer is the last pass that knows -- it walks
// in order and holds the outgoing value -- so it leaves this behind rather than
// nothing. Every walk that reasons about lifetimes has to skip it, or a value
// would look as though the marker kept it alive.
const ReleasePrev = "release_prev"

// ReleaseTrackAttr marks a ReleasePrev that names the value the local **takes**
// at the store rather than the one it drops. The renamer can name the dropped
// value only when it knows it, and inside a loop a local that is only ever
// written -- `$x = new Foo()` with `$x` never read -- reaches the header with
// no block argument to carry its previous value. So every promoted store to a
// local that may hold an object leaves a tracking marker, and the emitter keeps
// the local's last confined object itself, releasing it at the next store.
// SettleReleaseTracks marks the sites where that would be early.
const ReleaseTrackAttr = "track"

// ReleaseTrackKeepAttr marks a tracking site where the local's previous object
// must not be released: some other value stored to the local is still live
// after the store -- `f($x, $x = new Foo())` passes the old object on.
const ReleaseTrackKeepAttr = "keep"

// IntrinsicCall is the opcode of a call to an intrinsic. Its first operand is
// the intrinsic's **name**; the rest are the arguments.
const IntrinsicCall = "call.intrinsic"

// IntrinsicCalls rewrites every operation into a call to the general variant of
// the intrinsic it performs. `Lower` runs it, so it is not optional and no
// consumer ever meets a bare operator.
//
// The arguments go in ArgList, not Operands, so the **rendering** is a call too:
//
//	%2:zval = call.intrinsic "add"(%1, 1)
//	%1:zval = call.fn "helper"(byval $who, byval 2)
//
// rather than `call.intrinsic "add", %1, 1`, which reads as an opcode whose
// first operand happens to be a string. ArgList exists for exactly this: it
// attaches the parentheses to the callee. Intrinsic arguments carry no send
// mode, because an intrinsic has no PHP calling convention to vary.
func IntrinsicCalls(fn *Function) {
	_, byRefRecv := valueFacts(fn)
	placeTypes := placeRepresentation(fn)
	// Which values are write targets, so an operation on one can be expanded
	// into the storage form rather than the by-name form.
	valueTypes := map[string]string{}
	for _, b := range fn.Blocks {
		for _, in := range b.Insts {
			if in.Result != nil {
				valueTypes[in.Result.String()] = in.Result.Type
			}
		}
	}
	for _, b := range fn.Blocks {
		var out []Inst
		for _, in := range b.Insts {
			// A compound assignment is a **read, an operation and a write**.
			// It arrives as one instruction carrying the operator as an
			// attribute, because the builder-promotion pass reads that shape;
			// expanding it here is what puts its operation in the intrinsic
			// layer with every other operation.
			//
			// The emitter used to resolve that attribute through an operator
			// table of its own -- a second copy of the catalogue, which had
			// drifted the same way the opcode table had. It spelled the bitwise
			// operators `bit_and` where the engine emits `bw_and`, and had no
			// entry for `div`, `mod`, `sl` or `sr`, so `$a &= $b`, `$a /= 2`
			// and `$a <<= 1` did not compile at all.
			if in.Opcode == "assign_op" && len(in.Operands) == 2 {
				if expanded, ok := expandAssignOp(fn, in); ok {
					out = append(out, expanded...)
					continue
				}
			}
			// `local.set` is three operations wearing one spelling, and which
			// one it is was decided *in the emitter* from state the IR did not
			// carry. The lowering knows: the assigned value's representation
			// type says whether the slot holds a PHP value at all, and the
			// parameter list says whether a `recv` handed over a box.
			//
			//	$x = $y      write_var   writes through a reference, and copies
			//	f(&$x) entry bind_var    the slot BECOMES the box; no copy
			//	iterator     local.set   a frame slot, not a PHP variable
			//
			// The first two the wrong way round is silently wrong, which is why
			// they are separate intrinsics rather than one with a flag.
			// `$x++` is a read, an operation and a write, exactly as `$x += 1`
			// is. It was one instruction whose *operation* the emitter picked
			// from the opcode suffix -- the decoration shape, in the last
			// family that still had it.
			if expanded, ok := expandIncDec(fn, valueTypes, in); ok {
				out = append(out, expanded...)
				continue
			}
			if in.Opcode == "local.set" && len(in.Operands) == 2 {
				if variant, ok := storeVariant(placeTypes, byRefRecv, in); ok {
					in.ArgList = "(" + strings.Join(in.Operands, ", ") + ")"
					in.Operands = []string{strconv.Quote(variant)}
					in.Opcode = IntrinsicCall
					out = append(out, in)
					continue
				}
				// Not a PHP variable, so not a write to one. What is left is a
				// **frame slot** -- an iterator handle, a rope buffer, a
				// spilled temporary -- and it gets a name that says so.
				// `local.set` meant three things until the two that are PHP
				// operations became calls; leaving the third under the old name
				// would keep the IR claiming a local was set when none was.
				in.Opcode = SlotSet
			}
			if name, isOperation := intrinsicFor[in.Opcode]; isOperation {
				// The engine's IS fetch mode selects a **different intrinsic**,
				// not a flag on this one: the silent forms answer a different
				// question and give a different answer for it. This is the only
				// place the mode is consulted, and it is the right one -- the
				// mode came off the opcode, so it is spent where the opcode
				// becomes a call. Downstream passes read the callee.
				if silent, ok := silentIntrinsicFor[in.Opcode]; ok && hasAttr(in, "mode=IS") {
					name = silent
				}
				if list, ok := listIntrinsicFor[in.Opcode]; ok && hasAttr(in, "mode=LIST") {
					name = list
				}
				if general, ok := php.GeneralVariant(name); ok {
					// Nothing about the source opcode survives, because nothing
					// about it is needed: even the operator's PHP spelling is a
					// property of the intrinsic.
					in.ArgList = "(" + strings.Join(in.Operands, ", ") + ")"
					in.Operands = []string{strconv.Quote(general.Name)}
					in.Opcode = IntrinsicCall
					// The access mode is **spent**: it has just chosen the
					// callee, and the callee is where it now lives -- `dim.get`
					// against `dim.get.silent` against `dim.ref`. Leaving it
					// printed on the call would leave a fact in the IR that
					// nothing reads, which is how it came to be scanned for by
					// three passes in the first place.
					in.Attrs = withoutMode(in.Attrs)
				}
			}
			out = append(out, in)
		}
		b.Insts = out
	}
}

// withoutMode drops the access-mode attribute, keeping every other one.
func withoutMode(attrs []string) []string {
	out := attrs[:0:0]
	for _, a := range attrs {
		if !strings.HasPrefix(a, "mode=") {
			out = append(out, a)
		}
	}
	return out
}

// expandAssignOp turns `assign_op <target>, <value>, op=<binop>` into the read,
// the intrinsic call and the write it stands for. It reports false for an
// operator with no intrinsic, leaving the instruction alone so the emitter
// refuses it by name rather than compiling something else.
func expandAssignOp(fn *Function, in Inst) ([]Inst, bool) {
	binop := ""
	for _, a := range in.Attrs {
		if op, ok := strings.CutPrefix(a, "op="); ok {
			binop = op
		}
	}
	name, known := intrinsicFor[binop]
	if !known {
		return nil, false
	}
	general, ok := php.GeneralVariant(name)
	if !ok {
		return nil, false
	}
	target, value := in.Operands[0], in.Operands[1]

	var out []Inst
	// **The value is read before the variable** (probed): `$a .= $y` with
	// both unset says `Undefined variable $y` and then `$a` -- ZEND_ASSIGN_OP
	// fetches op2 first -- and a handler run by the first warning has already
	// happened by the time `$a` is read (str_offset_006). Left to
	// ExplicitReads the reads come in operand order, the variable first.
	if strings.HasPrefix(target, "$") {
		if _, ok := readableVar(value); ok {
			rv := fn.newValue("zval")
			out = append(out, Inst{Result: &rv, Opcode: IntrinsicCall,
				Operands: []string{strconv.Quote("read_var")}, ArgList: "(" + value + ")"})
			value = rv.String()
		}
	}
	// A local reads as itself -- the operand carries the undefined-variable
	// warning. A place is not a name, and reading it through a second fetch
	// would evaluate the container twice.
	read := target
	if !strings.HasPrefix(target, "$") {
		rv := fn.newValue("zval")
		out = append(out, Inst{Result: &rv, Opcode: "place.get", Operands: []string{target}})
		read = rv.String()
	}
	res := in.Result
	if res == nil {
		v := fn.newValue("zval")
		res = &v
	}
	out = append(out, Inst{
		Result:   res,
		Opcode:   IntrinsicCall,
		Operands: []string{strconv.Quote(general.Name)},
		ArgList:  "(" + read + ", " + value + ")",
	})
	if strings.HasPrefix(target, "$") {
		out = append(out, Inst{
			Opcode: IntrinsicCall, Operands: []string{strconv.Quote("write_var")},
			ArgList: "(" + target + ", " + res.String() + ")",
		})
		return out, true
	}
	out = append(out, Inst{Opcode: "store", Operands: []string{target, res.String()}})
	return out, true
}

// CallArgs are the arguments of a call, from the parenthesised ArgList.
func CallArgs(in Inst) []string {
	list := strings.TrimSpace(in.ArgList)
	list = strings.TrimSuffix(strings.TrimPrefix(list, "("), ")")
	var out []string
	for _, a := range splitCallArgs(list) {
		if a = strings.TrimSpace(a); a != "" {
			out = append(out, a)
		}
	}
	return out
}

// splitCallArgs splits on commas that are not inside a string or a bracket, so
// an array literal or a quoted comma stays one argument.
func splitCallArgs(list string) []string {
	var out []string
	depth, quoted, escaped, start := 0, false, false, 0
	for i, r := range list {
		switch {
		case escaped:
			escaped = false
		case r == '\\' && quoted:
			escaped = true
		case r == '"':
			quoted = !quoted
		case quoted:
		case r == '[' || r == '(':
			depth++
		case r == ']' || r == ')':
			depth--
		case r == ',' && depth == 0:
			out = append(out, list[start:i])
			start = i + 1
		}
	}
	return append(out, list[start:])
}

// IntrinsicOf reports the catalogue name an intrinsic call names, and the
// variant it currently calls.
func IntrinsicOf(in Inst) (php.Signature, php.Variant, bool) {
	if in.Opcode != IntrinsicCall || len(in.Operands) == 0 {
		return php.Signature{}, php.Variant{}, false
	}
	variant, err := strconv.Unquote(in.Operands[0])
	if err != nil {
		return php.Signature{}, php.Variant{}, false
	}
	v, ok := php.LookupVariant(variant)
	if !ok {
		return php.Signature{}, php.Variant{}, false
	}
	sig, ok := php.SignatureOfVariant(variant)
	if !ok {
		return php.Signature{}, php.Variant{}, false
	}
	return sig, v, true
}

// Specialize replaces each intrinsic call's variant with the narrowest one the
// operand types allow.
//
// Returns how many calls chose a narrower variant, and **how many could have**.
//
// The second number is the honest denominator. Most operations in this family
// have exactly one variant -- `concat` and the comparisons cannot specialize at
// all -- and counting them makes the rate look worse than the inference is: they
// were 89% of the calls, so a real 3% read as 0.3%. A ratio whose denominator
// includes cases that can never move measures the corpus, not the analysis.
func Specialize(fn *Function, constants map[string]string) (specialized, specializable int) {
	types := inferTypes(fn, constants)
	for _, b := range fn.Blocks {
		for i := range b.Insts {
			in := &b.Insts[i]
			sig, _, ok := IntrinsicOf(*in)
			if !ok {
				continue
			}
			args := CallArgs(*in)
			operands := make([]php.Type, 0, len(args))
			for _, a := range args {
				operands = append(operands, types.of(a))
			}
			v := sig.Select(operands)
			if len(sig.Variants) > 1 {
				specializable++
				if general, ok := php.GeneralVariant(sig.Name); ok && v.Name != general.Name {
					specialized++
				}
			}
			in.Operands = []string{strconv.Quote(v.Name)}
		}
	}
	return specialized, specializable
}

// producedType is the result type of an opcode that has exactly one, whatever
// its operands. These are the cheapest facts available and they need no
// analysis at all: a cast produces the type it names, a comparison produces a
// bool, `array.new` produces an array.
//
// Membership is a claim about PHP, so each entry is one the engine settles.
// `get_class` is deliberately absent: its result across the argument forms is
// not something this pass has asked the engine about, and a guess here selects
// a variant rather than merely failing to.
var producedType = map[string]php.Type{
	"to.null": php.Of(php.MaskNull), "to.int": php.Of(php.MaskInt), "to.float": php.Of(php.MaskFloat),
	"to.string": php.Of(php.MaskString), "to.array": php.Of(php.MaskArray),
	"to.object": php.Of(php.MaskObject), "to.bool": php.Of(php.MaskBool),

	"strlen": php.Of(php.MaskInt), "count": php.Of(php.MaskInt), "get_type": php.Of(php.MaskString),
	"bool": php.Of(php.MaskBool), "bool_not": php.Of(php.MaskBool), "type_check": php.Of(php.MaskBool),
	"instanceof": php.Of(php.MaskBool), "const.defined": php.Of(php.MaskBool),

	"strbuf.finish": php.Of(php.MaskString),
	"array.new":     php.Of(php.MaskArray),
	"new":           php.Of(php.MaskObject),
	"closure":       php.Of(php.MaskObject),
}

// typeFacts is what the pass could prove, keyed by IR value or place name.
type typeFacts map[string]php.Type

// bottom is `of` for the fixed point: an operand with no fact yet contributes
// **nothing** rather than everything, so a value still being computed does not
// poison the join it feeds. Sound only because every value the pass cannot
// compute is seeded MaskAny before the iteration starts.
func (t typeFacts) bottom(operand string) php.Type {
	operand = strings.TrimSpace(operand)
	if strings.HasPrefix(operand, "%") {
		return t[operand]
	}
	return t.of(operand)
}

func (t typeFacts) of(operand string) php.Type {
	operand = strings.TrimSpace(operand)
	// A literal is the one thing that needs no analysis.
	if _, err := strconv.ParseInt(operand, 10, 64); err == nil {
		return php.Of(php.MaskInt)
	}
	if _, err := strconv.ParseFloat(operand, 64); err == nil {
		return php.Of(php.MaskFloat)
	}
	if strings.HasPrefix(operand, `"`) {
		return php.Of(php.MaskString)
	}
	// An array literal is spelled `[0 => 2]` in the IR's operand text.
	if strings.HasPrefix(operand, "[") && strings.HasSuffix(operand, "]") {
		// The entries are right there, and `ParseArrayLiteral` is the renderer's
		// own scanner -- the pair sit together deliberately -- so reading them
		// back adds no second spelling of the format. A nested array arrives as
		// another `[...]` and recurses, which is where the lattice earns its
		// nesting.
		entries, ok := ParseArrayLiteral(operand)
		if !ok {
			return php.Of(php.MaskArray)
		}
		var key, elem php.Type
		for _, e := range entries {
			if e.IntKey {
				key = php.Union(key, php.Of(php.MaskInt))
			} else {
				key = php.Union(key, php.Of(php.MaskString))
			}
			elem = php.Union(elem, t.of(e.Value))
		}
		return php.ArrayOf(&key, &elem)
	}
	switch operand {
	case "true", "false":
		return php.Of(php.MaskBool)
	case "null":
		return php.Of(php.MaskNull)
	}
	if m, ok := t[operand]; ok && m.Known() {
		return m
	}
	return php.Of(php.MaskAny)
}

// inferable reports whether inferTypes has a rule for an instruction's result.
// Anything else is unknown from the start rather than unknown-so-far.
func inferable(in Inst) bool {
	if _, ok := producedType[in.Opcode]; ok {
		return true
	}
	switch in.Opcode {
	case "recv", "store", "local.get", "const.fetch", IntrinsicCall:
		return true
	}
	return false
}

// inferTypes computes what can be proved about values **and places**.
//
// Places are the point. This IR's rule is that *a value outliving its block is a
// place*, so `$a = $n + 1; $b = $a + 2;` puts `$a` in one -- and an analysis
// that tracks only values proves nothing about almost any real expression. The
// first version of this pass specialized **4 of 2,277 calls (0.2%)** for exactly
// that reason.
//
// # Soundness
//
// A wrong narrowing selects a variant that skips a diagnostic PHP emits, which
// is a wrong answer; failing to narrow is merely slower. So a place is tracked
// only when **every** way it can change is visible here, and anything else makes
// it unknown for the whole function:
//
//   - bound by reference, or passed by reference: another name can write it;
//   - a by-reference parameter: the caller shares the box;
//   - written through a place -- a dimension or property write -- which this
//     pass does not model;
//   - `unset`, which makes it undef again.
//
// # Fixed point
//
// Types feed back through a loop: `$i = $i + 1` types `$i` from a value typed
// from `$i`. Iterating to a fixed point over the union handles it, and the
// iteration is bounded -- the lattice has a handful of levels and widens
// monotonically, so it converges in a few passes and the bound only guards
// against a bug here.
func inferTypes(fn *Function, constants map[string]string) typeFacts {
	tracked := trackablePlaces(fn)
	facts := typeFacts{}
	edges := blockArguments(fn)

	// **Seed every value this pass has no rule for as unknown**, so that during
	// the fixed point "no fact yet" means *not computed yet* rather than
	// *unknown*. Without the distinction a loop-carried value cannot converge:
	// its block parameter joins an edge whose value is still being computed,
	// reads MaskAny for it, and MaskAny is absorbing under a union.
	//
	//	$s = 0; for (...) { $s = $s + $x; }
	//
	// promoted `$s` to a parameter joined from `0` and the add's result, which
	// on the first round was unknown -- so the parameter was Any, so the add
	// was Any, for ever. Unpromoted it converged, which is why promotion looked
	// like a regression when it was this.
	//
	// **Including the inferable instructions that cannot answer.** An untyped
	// parameter, a read of a place this pass does not track, an unregistered
	// constant: the rules below leave these with no fact at all, and a block
	// parameter joining one read it as bottom -- so `$n` joined from an
	// untyped parameter and `1e19` was proved a float, the float
	// representation rounded PHP_INT_MAX, and a guard-free arithmetic variant
	// could have skipped the TypeError a string owes. Unknown is not bottom;
	// the note on this pass in LTM says so, and this is the case it missed.
	for _, b := range fn.Blocks {
		for _, in := range b.Insts {
			if in.Result != nil && (!inferable(in) || unanswerable(fn, in, tracked, constants)) {
				facts[in.Result.String()] = php.Of(php.MaskAny)
			}
		}
	}

	for round := 0; round < 16; round++ {
		changed := false
		// A block parameter is the **join of what the edges carry**. Without
		// this, promoting a place to SSA *loses* the inference: the facts used
		// to flow through `local.set` and the read, and after mem2reg they
		// flow along edges instead. The rate fell 6.9% -> 6.3% with promotion
		// on, which is the measurement that says an analysis is looking at the
		// wrong shape.
		for _, params := range edges {
			for i, name := range params.names {
				// Recomputed from scratch each round, not accumulated: a union
				// that keeps what it saw cannot narrow when an edge's value
				// does, and the whole point of iterating is that the edges get
				// better.
				var m php.Type
				for _, arg := range params.args[i] {
					m = php.Union(m, facts.bottom(arg))
				}
				if m.Known() && facts[name] != m {
					facts[name] = m
					changed = true
				}
			}
		}
		for _, b := range fn.Blocks {
			for _, in := range b.Insts {
				// A place assignment contributes its value's type.
				if place, value, isStore := StoreOf(in); isStore {
					if tracked[place] {
						// `of`, not `bottom`. The block-parameter join can treat
						// an uncomputed edge as bottom because every value the
						// pass cannot type is seeded MaskAny first, so absent
						// really means "not yet". A **place** join cannot: its
						// stores include reads of other places, whose facts are
						// keyed by name and are absent both when unknown and
						// when uncomputed -- and treating those as bottom
						// dropped an `array` out of the join, so `$q = $arr;
						// $q -= 1` narrowed and skipped the TypeError it owes.
						//
						// Tried, measured, reverted: the rate went 96 -> 265
						// and one file started answering an array where PHP
						// throws. A narrowing that skips a diagnostic is a
						// wrong answer, and no rate buys one.
						if m := facts.of(value); m.Known() {
							if merged := php.Union(facts[place], m); merged != facts[place] {
								facts[place] = merged
								changed = true
							}
						}
					}
					continue
				}
				if in.Result == nil {
					continue
				}
				// A read of a place is where a place's type reaches an
				// expression. Every operand in this IR is an SSA value, so
				// without this hop the place facts above are computed and then
				// never consulted -- which is exactly what happened: tracking
				// places moved the specialization rate by 0.1 points because
				// `add %1, 1` asks about `%1`, never about `$a`.
				if m, ok := producedType[in.Opcode]; ok {
					if facts[in.Result.String()] != m {
						facts[in.Result.String()] = m
						changed = true
					}
					continue
				}
				switch in.Opcode {
				case "recv":
					// A **declared parameter type is a fact about the value in
					// the body**, not a comment on the call: the emitter coerces
					// through php.CoerceArgument, and control does not reach the
					// body when that fails. So `function f(int $n)` really does
					// have an int in `$n`.
					//
					// This is the largest single source of untypeable places in
					// the corpus -- 41 of the 225 blocked operands -- and it was
					// harvested all along.
					if m := declaredParamType(fn, in.Operands); m.Known() {
						if facts[in.Result.String()] != m {
							facts[in.Result.String()] = m
							changed = true
						}
					}
					continue
				case "copy":
					if len(in.Operands) >= 1 {
						if m := facts.of(in.Operands[0]); m.Known() && facts[in.Result.String()] != m {
							facts[in.Result.String()] = m
							changed = true
						}
					}
					continue
				case "local.get":
					if len(in.Operands) == 1 && tracked[in.Operands[0]] {
						if m := facts.of(in.Operands[0]); m.Known() && facts[in.Result.String()] != m {
							facts[in.Result.String()] = m
							changed = true
						}
					}
					continue
				case "store":
					// `$a = expr` is an expression whose value is what was
					// stored, so the result carries the source's type
					// regardless of what the destination is.
					if len(in.Operands) == 2 {
						if m := facts.of(in.Operands[1]); m.Known() && facts[in.Result.String()] != m {
							facts[in.Result.String()] = m
							changed = true
						}
					}
					continue
				}
				// A constant's type is the type of its **value**, and the
				// engine knows every constant it registers before user code
				// runs. Harvested rather than tabulated: `PHP_INT_MAX` is
				// build-specific, and a hand-written table of engine facts is
				// the defect this file has hit five times.
				if in.Opcode == "const.fetch" && len(in.Operands) == 1 {
					if name, err := strconv.Unquote(in.Operands[0]); err == nil {
						if text, known := constants[name]; known {
							if m := facts.of(text); m.Known() && facts[in.Result.String()] != m {
								facts[in.Result.String()] = m
								changed = true
							}
						}
					}
					continue
				}
				// **Generic results.** A container's element type is what makes
				// everything downstream of it typed: without this, reading an
				// element or stepping an iterator answers `mixed` and the
				// operator variants have nothing to narrow on.
				if m, ok := genericResult(in, facts); ok {
					if facts[in.Result.String()] != m {
						facts[in.Result.String()] = m
						changed = true
					}
					continue
				}
				sig, variant, isCall := IntrinsicOf(in)
				if !isCall {
					continue
				}
				args := CallArgs(in)
				// A read is where a place's type reaches an expression. It was
				// `local.get` and is now a call, and keying on the old opcode
				// alone dropped the specialization rate from 5.8% to 2.2% the
				// moment reads became explicit -- the facts were still computed
				// and no longer consulted.
				if variant.Name == "read_var" || variant.Name == "read_var.silent" {
					if len(args) > 0 && tracked[args[0]] {
						if m := facts.of(args[0]); m.Known() && facts[in.Result.String()] != m {
							facts[in.Result.String()] = m
							changed = true
						}
					}
					continue
				}
				// `bottom`, not `of`: this computes a *result*, and results are
				// recomputed every round, so an operand still being computed
				// must contribute nothing. Reading it as `mixed` recorded the
				// general result on round one, and the place join -- which
				// unions and cannot narrow -- kept it for ever.
				//
				// Third place in this pass needing the same correction. The
				// rule worth remembering: **`of` is for a use, `bottom` is for
				// a fixed point.**
				operands := make([]php.Type, 0, len(args))
				for _, a := range args {
					operands = append(operands, facts.bottom(a))
				}
				// The *variant's* result, not the intrinsic's: `add.int` yields
				// an int where `add` yields int|float|array.
				m := sig.Result
				if v := sig.Select(operands); v.Result.Known() {
					m = v.Result
				}
				if facts[in.Result.String()] != m {
					facts[in.Result.String()] = m
					changed = true
				}
			}
		}
		if !changed {
			break
		}
	}
	return facts
}

// unanswerable reports an instruction inferTypes has a rule for whose rule
// can never produce a fact here: each case below is one where the loop leaves
// the result unset rather than unknown.
func unanswerable(fn *Function, in Inst, tracked map[string]bool, constants map[string]string) bool {
	switch in.Opcode {
	case "recv":
		return !declaredParamType(fn, in.Operands).Known()
	case "local.get":
		return len(in.Operands) != 1 || !tracked[in.Operands[0]]
	case "const.fetch":
		if len(in.Operands) != 1 {
			return true
		}
		name, err := strconv.Unquote(in.Operands[0])
		if err != nil {
			return true
		}
		text, known := constants[name]
		return !known || !(typeFacts{}).of(text).Known()
	case IntrinsicCall:
		if _, ok := producedType[in.Opcode]; ok {
			return false
		}
		_, variant, isCall := IntrinsicOf(in)
		if !isCall {
			return true
		}
		if variant.Name == "read_var" || variant.Name == "read_var.silent" {
			args := CallArgs(in)
			return len(args) == 0 || !tracked[args[0]]
		}
	}
	return false
}

// trackablePlaces are the places whose every write this pass can see.
//
// It is a whitelist rather than a blacklist on purpose: a way of changing a
// place that nobody thought of makes it *untracked* and therefore general,
// which is slow rather than wrong. The reverse -- assuming a place is safe until
// a known hazard appears -- fails open on the hazard nobody listed.
func trackablePlaces(fn *Function) map[string]bool {
	candidates := map[string]bool{}
	hazardous := map[string]bool{}

	byValue := map[string]bool{}
	for _, p := range fn.Params {
		// The entry is positional text -- `0 &$name "int"` -- so the name field
		// already carries its `$`. An earlier version added another, producing
		// `$$name`, which matches no candidate: the by-reference hazard was
		// written down and then never fired. Nothing caught it because the
		// hazard test covered only the instruction-level hazards, so the one
		// hazard with no test was the one that did not work.
		fields := strings.SplitN(strings.TrimSpace(p), " ", 3)
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimPrefix(fields[1], "&")
		if ParamIsByRef(p) {
			hazardous[name] = true
		} else {
			byValue[name] = true
		}
	}
	for _, b := range fn.Blocks {
		for _, in := range b.Insts {
			if place, _, isStore := StoreOf(in); isStore {
				candidates[place] = true
				continue
			}
			switch in.Opcode {
			case "bind_ref", "bind_ref.loop", "unset", "bind_static", "bind_global", "bind_lexical":
				for _, op := range in.Operands {
					hazardous[op] = true
				}
			default:
				// Anything producing a *place* over a local can write through
				// it later, and any by-reference argument hands the box away.
				//
				// **Operands and call arguments both.** These instructions are
				// now `call.intrinsic "dim.ref"($a, 0)`, so the place lives in
				// ArgList; scanning Operands alone silently stopped seeing the
				// hazard the moment they became calls, which would narrow a
				// variable another name can write.
				if in.Result != nil && in.Result.Type == "place" {
					for _, op := range in.Operands {
						hazardous[op] = true
					}
					for _, a := range CallArgs(in) {
						hazardous[a] = true
					}
				}
				for _, m := range byRefArg.FindAllStringSubmatch(in.ArgList, -1) {
					hazardous[m[1]] = true
				}
			}
		}
	}
	assigned := assignedBeforeEveryRead(fn, byValue)
	out := map[string]bool{}
	for name := range candidates {
		if !hazardous[name] && assigned[name] {
			out[name] = true
		}
	}
	return out
}

// assignedBeforeEveryRead reports the places that cannot be read before they are
// written.
//
// This is a soundness condition, not a precision one. A place assigned only on
// one arm of a branch has a *union* of the assigned types -- and reading it on
// the other arm yields **undef**, which is in none of them. Narrowing on that
// union hands a null to a variant whose whole justification is that it cannot
// receive one.
//
//	if ($c) { $a = 1; }
//	return $a + 1;      // $a is int here, and null at run time
//
// The condition is ordinary definite assignment: every `local.get` of the place
// is preceded by a `local.set` of it, either earlier in the same block or in a
// block that dominates this one. A by-value parameter is assigned at entry and
// so is always definitely assigned. `local.set` is the only writer here because
// every other way of changing a place is already a hazard above.
func assignedBeforeEveryRead(fn *Function, byValue map[string]bool) map[string]bool {
	cfg := Analyze(fn)
	setsIn := map[string]map[int]bool{}
	for i, b := range fn.Blocks {
		for _, in := range b.Insts {
			if place, _, isStore := StoreOf(in); isStore {
				if setsIn[place] == nil {
					setsIn[place] = map[int]bool{}
				}
				setsIn[place][i] = true
			}
		}
	}

	ok := map[string]bool{}
	for name := range setsIn {
		ok[name] = true
	}
	for name := range byValue {
		ok[name] = true
	}
	for i, b := range fn.Blocks {
		if b.Dead || !cfg.Reachable(i) {
			continue
		}
		for j, in := range b.Insts {
			for _, name := range readPlaces(in) {
				if !ok[name] || byValue[name] {
					continue
				}
				if !readIsCovered(fn, cfg, setsIn[name], name, i, j) {
					ok[name] = false
				}
			}
		}
		// **A terminator reads too.** For a program variable that read has
		// already become a `read_var` instruction, but a compiler temporary is
		// skipped by ExplicitReads -- it cannot warn -- so `br.null $__t3` is
		// the only mention of it, and scanning instructions alone pronounced it
		// definitely assigned when the branch that writes it may not have run.
		for _, name := range termVars(b.Term) {
			if !ok[name] || byValue[name] {
				continue
			}
			if !readIsCovered(fn, cfg, setsIn[name], name, i, len(b.Insts)) {
				ok[name] = false
			}
		}
	}
	return ok
}

// readPlace is the place an instruction reads, if it is a read.
//
// It looked for the `local.get` opcode, which stopped existing when reads
// became `call.intrinsic "read_var"`. Nothing failed: the scan simply found no
// reads and pronounced **every** place definitely assigned, so the check that
// keeps a narrowing from skipping an undefined-variable warning was passing
// vacuously. Found by a mem2reg test built to break it, which is the only way
// a check that fails open ever gets found.
func readPlaces(in Inst) []string {
	if in.Opcode == "local.get" && len(in.Operands) == 1 {
		return in.Operands[0:1]
	}
	_, v, isCall := IntrinsicOf(in)
	if !isCall {
		return nil
	}
	// **Every value-position argument**, not just the read intrinsic's. A place
	// passed to `iter.valid` or `iter.free` is read there as surely as one
	// passed to `read_var`, and counting only the latter pronounced such a
	// place definitely assigned when nothing had written it -- so promotion
	// dropped its stores and the edge into a merge carried an empty argument.
	//
	// Sixth or seventh time this session that a check knew about one of the two
	// places a thing can live. The shape to watch for is a scan keyed on a
	// *spelling* where the question is about a *role*.
	var out []string
	for i, a := range CallArgs(in) {
		if strings.HasPrefix(a, "$") && valueArgShape(v, i) {
			out = append(out, a)
		}
	}
	return out
}

// readIsCovered reports whether a `local.get` at instruction j of block i is
// reached only through a `local.set` of the same place.
func readIsCovered(fn *Function, cfg *CFG, sets map[int]bool, name string, i, j int) bool {
	if j > len(fn.Blocks[i].Insts) {
		j = len(fn.Blocks[i].Insts)
	}
	for k := 0; k < j; k++ {
		if place, _, isStore := StoreOf(fn.Blocks[i].Insts[k]); isStore && place == name {
			return true
		}
	}
	for b := range sets {
		if b != i && cfg.Dominates(b, i) {
			return true
		}
	}
	return false
}

// declaredParamType is the type a `recv` result must have, from the parameter's
// declaration.
//
// Only an unambiguous, non-nullable scalar or array declaration counts. A union,
// a `?`, `mixed`, `iterable` or a class name all describe a set this lattice
// would have to widen anyway, and the coercion rules for them are not the ones
// below. A by-reference parameter is excluded here as well as by
// trackablePlaces, because the emitter deliberately does **not** coerce one --
// the slot is the caller's box.
func declaredParamType(fn *Function, operands []string) php.Type {
	if len(operands) != 1 {
		return php.Type{}
	}
	i, err := strconv.Atoi(strings.TrimSpace(operands[0]))
	if err != nil || i < 0 || i >= len(fn.Params) {
		return php.Type{}
	}
	p := fn.Params[i]
	if ParamIsByRef(p) {
		return php.Type{}
	}
	if fn.assumedInt(operands[0]) {
		return php.Of(php.MaskInt)
	}
	q := strings.Index(p, `"`)
	if q < 0 {
		return php.Type{}
	}
	decl, err := strconv.Unquote(p[q:])
	if err != nil {
		return php.Type{}
	}
	// A nullable declaration is the union, and saying so is what makes `?array`
	// **fail** to be an array rather than be unknown. The difference is not
	// academic: an unknown subject narrows nothing by accident, so a test built
	// on `?array` passed while the guard it meant to exercise was loosened.
	// Unknown and "known to be wider" look the same from the outside and are
	// not the same fact.
	null := php.TypeMask(0)
	if rest, nullable := strings.CutPrefix(decl, "?"); nullable {
		decl, null = rest, php.MaskNull
	}
	switch decl {
	case "int":
		return php.Of(php.MaskInt | null)
	case "float":
		// Widening is why this is sound and also why it is worth having: a
		// declared float parameter passed an int holds a **float** in the body.
		return php.Of(php.MaskFloat | null)
	case "string":
		return php.Of(php.MaskString | null)
	case "bool":
		return php.Of(php.MaskBool | null)
	case "array":
		return php.Of(php.MaskArray | null)
	}
	return php.Type{}
}

// blockParamEdges is a block's parameter names and, per parameter, the values
// each incoming edge carries.
type blockParamEdges struct {
	names []string
	args  [][]string
}

// blockArguments reads the arguments terminators pass to block parameters.
// After mem2reg a merge is `jmp bb2(%7)` and `bb2(%8: zval)`, so this is where
// a value's incoming set lives -- the SSA form's answer to what a phi joins.
func blockArguments(fn *Function) map[int]*blockParamEdges {
	out := map[int]*blockParamEdges{}
	for i, b := range fn.Blocks {
		if len(b.Params) == 0 {
			continue
		}
		e := &blockParamEdges{args: make([][]string, len(b.Params))}
		for _, p := range b.Params {
			name, _, _ := strings.Cut(p, ":")
			e.names = append(e.names, strings.TrimSpace(name))
		}
		out[i] = e
	}
	for _, b := range fn.Blocks {
		for _, m := range branchWithArgs.FindAllStringSubmatch(b.Term, -1) {
			var to int
			if _, err := fmt.Sscanf(m[1], "bb%d", &to); err != nil {
				continue
			}
			e, ok := out[to]
			if !ok {
				continue
			}
			for i, a := range splitCallArgs(m[2]) {
				if i < len(e.args) {
					e.args[i] = append(e.args[i], strings.TrimSpace(a))
				}
			}
		}
	}
	return out
}

// branchWithArgs finds `bb2(%7, %8)` in a terminator.
var branchWithArgs = regexp.MustCompile(`(bb\d+)\(([^)]*)\)`)

// valueFacts are, for each SSA value, its representation type, and whether it is
// a by-reference parameter arriving.
// placeRepresentation is each place's representation type, joined over every
// write. A place written with two different kinds carries a `zval`.
//
// The join is the point: deciding from the *value* being stored gave one slot
// two spellings -- `local.set $__t2, %2` for a bool and `write_var($__t2, %3)`
// for a zval -- which is one variable written two ways. A place has one type,
// so it has one kind of store.
func placeRepresentation(fn *Function) map[string]string {
	types := map[string]string{}
	for _, b := range fn.Blocks {
		for _, in := range b.Insts {
			if in.Result != nil {
				types[in.Result.String()] = in.Result.Type
			}
		}
	}
	// **A place a closure captures by reference holds a box**, whatever the
	// values written into it look like. `$f = function () use (&$f) {...}` puts
	// a closure in `$f` and nothing else, so narrowing on the stores alone
	// typed the place `*obj` -- which made the store a frame-slot write that
	// *replaced* the box `bind_lexical` had just put there, and the recursive
	// call inside the body then found undef.
	//
	// Collected before the narrowing rather than as an exception inside it,
	// because it is not about what is stored: it is about the slot having
	// storage of its own that something else holds.
	boxed := map[string]bool{}
	// A parameter the frame reads through its address stays a zval
	// (LiveParams): the frame's view is a *php.Value.
	for place := range fn.LiveParams {
		boxed[place] = true
	}
	readsOf := map[string]string{}
	for _, b := range fn.Blocks {
		for _, in := range b.Insts {
			if in.Opcode == "local.get" && len(in.Operands) == 1 && in.Result != nil {
				readsOf[in.Result.String()] = strings.TrimSpace(in.Operands[0])
			}
		}
	}
	for _, b := range fn.Blocks {
		for _, in := range b.Insts {
			// **A by-reference foreach holds its variable's box**
			// (FE_RESET_RW): the loop re-reads the variable at every step, so
			// an assignment in the body has to write through that box, not
			// replace it (gh20085). An array place is a zval already; an
			// object one narrowed to `*obj` made `$o = new Q` a frame-slot
			// write the loop never saw.
			if in.Opcode == "iter.new.ref" && len(in.Operands) > 0 {
				subject := strings.TrimSpace(in.Operands[0])
				if place, isRead := readsOf[subject]; isRead {
					subject = place
				}
				if strings.HasPrefix(subject, "$") {
					boxed[subject] = true
				}
			}
			if in.Opcode == "bind_lexical" && len(in.Operands) == 2 && hasAttr(in, "use=ref") {
				boxed[strings.TrimSpace(in.Operands[1])] = true
			}
			// **`global` and `static` bind a box too**, and for the same
			// reason: the storage belongs to the global table or to the
			// function's own static slot, and the local is a second name for
			// it. Narrowing such a place on its stores made
			//
			//	class C { function m() { global $k; $k = $this; } }
			//
			// type `$k` as `*obj`, which turned the assignment into a
			// frame-slot write that **replaced** the binding rather than
			// writing through it -- so the global kept its old value and the
			// caller saw null. Exactly the bug the closure case above
			// describes; `bind_global` and `bind_static` were simply never
			// added beside it.
			//
			// Only visible with a narrowable value: `$k = 7` leaves the place
			// `zval` and takes the correct path, which is why this survived.
			if (in.Opcode == "bind_global" || in.Opcode == "bind_static") && len(in.Operands) > 0 {
				boxed[strings.TrimSpace(in.Operands[0])] = true
			}
			// `$b = &$a` puts **both** names on one box, so neither may be
			// narrowed: writing through either has to reach the other, and a
			// frame-slot write to one would silently unbind it.
			// **So does a by-reference foreach** (`bind_ref.loop`): the loop
			// variable is each element's box in turn, and a narrowed store
			// replaced the box instead of writing the element --
			// `foreach ($a as &$v) { $v = new C; }` left the array as it was
			// (temporary_cleaning_009).
			if in.Opcode == "bind_ref" || in.Opcode == "bind_ref.loop" {
				for _, op := range in.Operands {
					boxed[strings.TrimSpace(op)] = true
				}
			}
			// **A reference taken to it hands the box out**: `[&$a]` puts
			// $a's box in the array (`make_ref $a`). Narrowed, `$a = [&$a]`
			// became a frame-slot write that replaced the box rather than
			// filling it, so the element held NULL and the array was not
			// recursive (recursive_array_comparison).
			if in.Opcode == "make_ref" && len(in.Operands) > 0 && strings.HasPrefix(strings.TrimSpace(in.Operands[0]), "$") {
				boxed[strings.TrimSpace(in.Operands[0])] = true
			}
		}
	}
	out := map[string]string{}
	for _, b := range fn.Blocks {
		for _, in := range b.Insts {
			if in.Opcode != "local.set" || len(in.Operands) != 2 {
				continue
			}
			if boxed[in.Operands[0]] {
				out[in.Operands[0]] = "zval"
				continue
			}
			t := types[strings.TrimSpace(in.Operands[1])]
			if t == "" {
				t = "zval"
			}
			if seen, ok := out[in.Operands[0]]; ok && seen != t {
				out[in.Operands[0]] = "zval"
				continue
			}
			out[in.Operands[0]] = t
		}
	}
	return out
}

func valueFacts(fn *Function) (map[string]string, map[string]bool) {
	types := map[string]string{}
	byRef := map[string]bool{}
	for _, b := range fn.Blocks {
		for _, in := range b.Insts {
			if in.Result == nil {
				continue
			}
			types[in.Result.String()] = in.Result.Type
			if in.Opcode != "recv" || len(in.Operands) == 0 {
				continue
			}
			i, err := strconv.Atoi(strings.TrimSpace(in.Operands[0]))
			if err == nil && i >= 0 && i < len(fn.Params) && ParamIsByRef(fn.Params[i]) {
				byRef[in.Result.String()] = true
			}
		}
	}
	return types, byRef
}

// storeVariant is which store a `local.set` performs, or false when it is a
// frame slot rather than a PHP variable -- an iterator or a string builder,
// which no runtime intrinsic writes because they are not PHP values.
func storeVariant(placeTypes map[string]string, byRef map[string]bool, in Inst) (string, bool) {
	if t, known := placeTypes[in.Operands[0]]; known && t != "" && t != "zval" {
		return "", false
	}
	if byRef[strings.TrimSpace(in.Operands[1])] || hasAttr(in, SeparateAttr) {
		return "bind_var", true
	}
	return "write_var", true
}

// StoreOf is the place and value a **store** names, whatever spelling it wears.
//
// There are three: `local.set` for a frame slot, and the `write_var` and
// `bind_var` intrinsics for a PHP variable. Every analysis that asks "where is
// this place written" has to accept all three, and each time a store changed
// spelling the analyses that did not silently stopped finding any -- the
// specialization rate fell 6.0% to 1.9% the moment `local.set` became a call,
// with nothing failing. This is the one place that knows.
func StoreOf(in Inst) (place, value string, ok bool) {
	// `local.set` is the pre-conversion spelling -- IntrinsicCalls turns it into
	// a `write_var` call or a `slot.set` -- and this is called from both sides
	// of that, so it accepts all three.
	if (in.Opcode == "local.set" || in.Opcode == SlotSet) && len(in.Operands) == 2 {
		return in.Operands[0], in.Operands[1], true
	}
	if _, v, isCall := IntrinsicOf(in); isCall && (v.Name == "write_var" || v.Name == "bind_var") {
		if args := CallArgs(in); len(args) == 2 {
			return args[0], args[1], true
		}
	}
	return "", "", false
}

// incdecFor maps the four step opcodes to the intrinsic they perform and
// whether the instruction's value is the one *before* the step.
var incdecFor = map[string]struct {
	intrinsic string
	post      bool
}{
	"pre_inc":  {"increment", false},
	"pre_dec":  {"decrement", false},
	"post_inc": {"increment", true},
	"post_dec": {"decrement", true},
}

// expandIncDec turns `$x++` into the read, the call and the write it stands for.
//
// The read is the warning kind: `$e++` on an undefined `$e` warns, and that is
// the read's diagnostic, not the step's. On failure the place keeps what it had,
// which falls out of the split -- the call's error path leaves the write
// unreached, where the fused form had to remember to write only in the else
// branch.
func expandIncDec(fn *Function, types map[string]string, in Inst) ([]Inst, bool) {
	step, known := incdecFor[in.Opcode]
	if !known || len(in.Operands) != 1 {
		return nil, false
	}
	target := in.Operands[0]
	local := strings.HasPrefix(target, "$")
	if !local && types[target] != "place" {
		// A step whose target is neither a named local nor a write target is
		// not something PHP can produce, and guessing would put a value where
		// the emitter expects storage.
		return nil, false
	}
	before := fn.newValue("zval")
	after := fn.newValue("zval")
	// **A place reads and writes differently from a local.** `$a["k"]++` and
	// `$o->p++` name storage the IR is already holding, so the read is a copy
	// of what the place has and the write is a `store` -- where a local reads
	// and writes by name. Requiring a local here left every one of those as a
	// raw opcode the emitter had no case for.
	// `place.get` is what the sibling expansion for `$a["k"] += 1` already
	// uses: a place is not a name, and reading it through a second fetch would
	// evaluate the container twice.
	// The read is marked too: the engine refuses a **non-object receiver at the
	// fetch**, before the step runs, so `$v->d--` on a null is the
	// increment/decrement Error alone -- marking only the store let the
	// decrement emit `Decrement on type null has no effect` first.
	// And which step, for a readonly property's refusal: the engine steps a
	// copy before the write refuses, and chains what the step threw.
	read := Inst{Result: &before, Opcode: "place.get", Operands: []string{target},
		Attrs: []string{"op=incdec", "step=" + step.intrinsic}}
	// **Marked as a step**, because the store cannot otherwise tell `$o->p++`
	// from `$o->p += 1`: both arrive here as the same read-call-write triple,
	// and a non-object receiver refuses them with different verbs --
	// `Attempt to increment/decrement property` against `Attempt to assign`.
	write := Inst{Opcode: "store", Operands: []string{target, after.String()}, Attrs: []string{"op=incdec"}}
	if local {
		// The read is the **warning** kind: `$e++` on an undefined `$e` warns,
		// and that is the read's diagnostic rather than the step's. Written out
		// here rather than left to ExplicitReads because the post- forms need to
		// name the value the read produced. A place warns too -- `$b["n"]++`
		// says `Undefined array key "n"` -- and gets there through the loud
		// `place.get` the copy above resolves to.
		read = Inst{Result: &before, Opcode: IntrinsicCall,
			Operands: []string{strconv.Quote("read_var")}, ArgList: "(" + target + ")"}
		write = Inst{Opcode: IntrinsicCall, Operands: []string{strconv.Quote("write_var")},
			ArgList: "(" + target + ", " + after.String() + ")"}
	}
	out := []Inst{
		read,
		{Result: &after, Opcode: IntrinsicCall,
			Operands: []string{strconv.Quote(step.intrinsic)},
			ArgList:  "(" + before.String() + ")"},
		write,
	}
	// `$a++` evaluates to the value before the step; `++$a` to the value after.
	if in.Result != nil {
		src := after
		if step.post {
			src = before
		}
		out = append(out, Inst{Result: in.Result, Opcode: IntrinsicCall,
			Operands: []string{strconv.Quote("copy")}, ArgList: "(" + src.String() + ")"})
	}
	return out, true
}

// genericResult is the result type of an operation that carries element types
// through, or false when the instruction is not one.
//
// These are the rules a bare mask cannot express, and they are the reason the
// lattice grew a parameter:
//
//	array.new()                 array<never>   -- empty holds nothing
//	array.append(a<T>, v:U)     array<T|U>     -- a write WIDENS; not covariant
//	array.set(a<K,V>, k:J, v:U) array<K|J, V|U>
//	dim.get(a<K,V>, k)          V
//	iter.new(a<K,V>)            iterator<K,V>
//	iter.current(i<K,V>)        V
//	iter.key(i<K,V>)            K
//	iter.next(i<K,V>)           iterator<K,V>  -- stepping preserves the type
//
// The append rule is the one worth staring at. Covariance says an `array<int>`
// may be *read* where an `array<numeric>` is wanted; it does not say anything
// may be written into it. Appending a string to an `array<int>` produces an
// `array<int|string>`, and computing that union rather than checking
// containment is what keeps the two directions apart.
func genericResult(in Inst, facts typeFacts) (php.Type, bool) {
	_, v, isCall := IntrinsicOf(in)
	if !isCall || in.Result == nil {
		return php.Type{}, false
	}
	args := CallArgs(in)
	// `bottom`, not `of`: these results are recomputed every round, so an
	// argument still being computed must contribute nothing rather than
	// everything. Reading it as `mixed` made a loop-carried iterator settle on
	// `mixed` on the first round and stay there.
	arg := func(i int) php.Type {
		if i < len(args) {
			return facts.bottom(args[i])
		}
		return php.Of(php.MaskAny)
	}
	elem := func(t php.Type) *php.Type { return t.Elem }
	key := func(t php.Type) *php.Type { return t.Key }
	// `answer` distinguishes the two reasons a parameter can be absent, which
	// is the whole difficulty of an optimistic fixed point:
	//
	//   - the **container** is not computed yet -> bottom, so the next round
	//     can do better;
	//   - the container is known and says nothing about its elements -> top,
	//     because an array nobody looked inside may hold anything.
	//
	// Returning top for the first case made `iter.current` answer `mixed` on
	// round one, which the place join then kept for ever. Fourth place in this
	// pass to need the same distinction.
	answer := func(container php.Type, p *php.Type) (php.Type, bool) {
		switch {
		case !container.Known():
			return php.Type{}, true
		case p == nil:
			return php.Of(php.MaskAny), true
		}
		return *p, true
	}

	switch v.Name {
	case "array.new":
		empty := php.Type{}
		return php.ArrayOf(&empty, &empty), true
	case "array.append", "array.append.bound":
		return php.Union(arg(0), php.ArrayOf(nil, ptrTo(arg(1)))), true
	case "array.set", "array.set.bound":
		return php.Union(arg(0), php.ArrayOf(ptrTo(arg(1)), ptrTo(arg(2)))), true
	case "dim.get":
		return answer(arg(0), elem(arg(0)))
	case "iter.new.ref":
		// The **live** cursor. Its element type is the array's, exactly as the
		// snapshot form's is; what differs is when it reads, not what it holds.
		subject := arg(0)
		if subject.Known() && subject.Mask == php.MaskArray {
			return php.IterOfArray(key(subject), elem(subject)), true
		}
		return php.IterOf(key(subject), elem(subject)), true
	case "iter.new":
		subject := arg(0)
		if subject.Known() && subject.Mask == php.MaskArray {
			// **Only** where the subject is provably an array, and nothing
			// else: `array|null` does not qualify, because the null path
			// reaches NewIter and a Traversable is an object. Getting this
			// wrong makes a foreach over a user Iterator skip the user code it
			// owes, which is a wrong answer rather than a slow one.
			return php.IterOfArray(key(subject), elem(subject)), true
		}
		return php.IterOf(key(subject), elem(subject)), true
	case "iter.current", "iter.current.ref":
		return answer(arg(0), elem(arg(0)))
	case "iter.key":
		return answer(arg(0), key(arg(0)))
	case "iter.next":
		return arg(0), true
	}
	return php.Type{}, false
}

func ptrTo(t php.Type) *php.Type { return &t }
