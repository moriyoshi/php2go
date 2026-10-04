package lower

import (
	"fmt"
	"strings"

	pb "github.com/moriyoshi/php2go/internal/ir/irpb"
	"math"
	"strconv"
)

// Zend's operand kinds. These are a bitmask in the opline, not an enum.
const (
	opUnused = 0
	opConst  = 1
	opTmpVar = 2
	opVar    = 4
	opCV     = 8
)

// zendLastCatch is ZEND_LAST_CATCH in CATCH's extended_value: the final catch
// of a chain has no next one to fall to.
const zendLastCatch = 1 << 0

// binaryOps take two value operands and produce one, and each lowers to an
// intrinsic of the same name. INTRINSICS.md carries their effect contracts --
// which diagnostics they can emit and in what order -- and no specialization
// may fire at operand types where that contract says something is printed.
var binaryOps = map[string]bool{
	"ZEND_CONCAT": true, "ZEND_FAST_CONCAT": true,
	"ZEND_ADD": true, "ZEND_SUB": true, "ZEND_MUL": true, "ZEND_DIV": true,
	"ZEND_MOD": true, "ZEND_POW": true,
	"ZEND_BW_AND": true, "ZEND_BW_OR": true, "ZEND_BW_XOR": true,
	"ZEND_SL": true, "ZEND_SR": true,
	"ZEND_IS_IDENTICAL": true, "ZEND_IS_NOT_IDENTICAL": true,
	"ZEND_IS_EQUAL": true, "ZEND_IS_NOT_EQUAL": true,
	"ZEND_IS_SMALLER": true, "ZEND_IS_SMALLER_OR_EQUAL": true,
	"ZEND_SPACESHIP": true, "ZEND_BOOL_XOR": true,
	"ZEND_CASE": true, "ZEND_CASE_STRICT": true,
	"ZEND_INSTANCEOF": true, "ZEND_IN_ARRAY": true, "ZEND_ARRAY_KEY_EXISTS": true,
}

// castTargets maps ZEND_CAST's extended_value to the type it produces, using
// the engine's IS_* numbering; _IS_BOOL sits outside that range at 16.
var castTargets = map[uint32]string{
	1: "null", 4: "int", 5: "float", 6: "string", 7: "array", 8: "object", 16: "bool",
}

// accessMode is the mode an access opcode names. It decides semantics rather
// than performance: R warns on a missing key, W autovivifies, IS is silent, RW
// does both, and FUNC_ARG is undecided until the callee's signature is known.
// Carrying it on the instruction is what lets a write path be a place instead
// of a temporary holding an indirection.
func accessMode(opcode string) string {
	switch {
	case opcode == "ZEND_FETCH_LIST_R":
		// Destructuring: `[$a, $b] = $v` reads nothing from a value that is
		// not a container -- a string, null or a number gives NULL, silently.
		return "LIST"
	case strings.HasSuffix(opcode, "_RW"):
		return "RW"
	case strings.HasSuffix(opcode, "_W"):
		return "W"
	case strings.HasSuffix(opcode, "_IS"):
		return "IS"
	case strings.HasSuffix(opcode, "_UNSET"):
		return "UNSET"
	case strings.HasSuffix(opcode, "_FUNC_ARG"):
		return "FUNC_ARG"
	}
	return "R"
}

func sendMode(opcode string) string {
	switch opcode {
	case "ZEND_SEND_VAL", "ZEND_SEND_VAL_EX", "ZEND_SEND_VAR":
		return "byval"
	case "ZEND_SEND_VAR_EX":
		// The `_EX` suffix on a **VAR** send means the callee's arginfo decides
		// at run time, and the engine emits it wherever the callee is not
		// resolved at compile time -- a closure in a variable, a method call, a
		// plain `get_class($a)`. Measured: a *known* callee with a
		// by-reference parameter gets `SEND_REF` instead, so this is exactly
		// the undecided case `preferref` already names for `SEND_USER`.
		//
		// Reading it as `byval` beside plain `SEND_VAR` is why a by-reference
		// parameter reached through a variable discarded its writes. Passing
		// the caller's storage instead needs **every** receiving end to unbox,
		// and the compiled unit has ten places that consume an argument list.
		// Seven route to a compiled `recv`, which does; three read `args[i]`
		// themselves -- callMethod's Generator and Closure arms, callStatic's
		// Closure arm, callCtor's Throwable arm -- and were classified rather
		// than discovered one corpus failure at a time.
		return "preferref"
	case "ZEND_SEND_REF":
		return "byref"
	case "ZEND_SEND_VAR_NO_REF", "ZEND_SEND_VAR_NO_REF_EX":
		return "preferref"
	case "ZEND_SEND_UNPACK", "ZEND_SEND_ARRAY":
		return "unpack"
	case "ZEND_SEND_FUNC_ARG":
		// **Undecided, not by value.** This is the send for an argument the
		// compiler *fetched* as a write target -- `f($a["k"])` -- where only
		// the callee knows whether it wanted a reference. Calling it `byval`
		// read the element instead of binding it, so `$f = "byref";
		// $f($a["k"])` warned about an undefined key and left the array empty
		// where the engine autovivifies and binds. A static call to a known
		// callee never reaches here: the lowering emits `byref` outright.
		return "preferref"
	case "ZEND_SEND_USER":
		// `call_user_func("f", $a)`, which the compiler turns into a call of
		// its own. **By value**, measured: passing `$a` to a `&$x` parameter
		// warns `Argument #1 ($x) must be passed by reference, value given`
		// and leaves `$a` alone. It was `preferref`, which handed the callee
		// the caller's box, so the callee's write came back.
		//
		// The warning is the call's to raise, since only the callee's
		// signature says which positions wanted a reference: the lowering
		// marks the call `user_send` (see ZEND_SEND_ in lower.go).
		return "byval"
	}
	return ""
}

// literalString is the text of a CONST string operand, or "" for anything else.
func literalString(oa *pb.OpArray, kind, idx uint32) string {
	if kind != opConst || int(idx) >= len(oa.GetLiterals()) {
		return ""
	}
	lit := oa.GetLiterals()[idx]
	if lit.GetKind() != pb.Literal_KIND_STRING {
		return ""
	}
	return lit.GetStringValue()
}

// findFiberSuspends returns the DO_* oplines that complete a `Fiber::suspend()`.
//
// Suspension has no opcode of its own -- the engine compiles it as an ordinary
// static call -- but leaving it as one would mean the *runtime* has to implement
// suspension, with real coroutines behind it. Recognising it here makes the
// suspension point explicit in the IR, which is what lets the compiler own the
// mechanism and the runtime stay an ordinary library.
//
// The class name is unambiguous: `Fiber` is a final internal class, and an
// unqualified class name inside a namespace does *not* fall back to the global
// one, so the compiler has already resolved `use Fiber` to the literal "Fiber"
// by the time it reaches the opline.
func findFiberSuspends(oa *pb.OpArray, oplines []*pb.Opline, name func(int) string) map[int]bool {
	out := map[int]bool{}
	for _, c := range matchCalls(oa, oplines, name) {
		if c.fiberSuspend && c.do >= 0 {
			out[c.do] = true
		}
	}
	return out
}

// callInits names the opcodes that open a call. Matching on the `ZEND_INIT_`
// prefix is wrong and was: **ZEND_INIT_ARRAY** shares it while being an array
// literal, so it pushed a frame that the next DO popped instead of the call's
// own -- which silently mispaired every call following an array literal.
var callInits = map[string]bool{
	"ZEND_INIT_FCALL":              true,
	"ZEND_INIT_FCALL_BY_NAME":      true,
	"ZEND_INIT_NS_FCALL_BY_NAME":   true,
	"ZEND_INIT_METHOD_CALL":        true,
	"ZEND_INIT_STATIC_METHOD_CALL": true,
	"ZEND_INIT_DYNAMIC_CALL":       true,
	"ZEND_INIT_USER_CALL":          true,
	"ZEND_NEW":                     true,
}

// call is one INIT/SEND*/DO run recovered from the opline stream.
type call struct {
	init         int
	do           int
	sends        []int
	fiberSuspend bool
}

// matchCalls pairs each call's INIT with the DO that completes it. The three
// phases are a stack, not a sequence: an argument can itself be a call.
func matchCalls(oa *pb.OpArray, oplines []*pb.Opline, name func(int) string) []call {
	var out []call
	var stack []int // indices into out
	for i, op := range oplines {
		switch n := name(i); {
		case n == "ZEND_INIT_STATIC_METHOD_CALL":
			out = append(out, call{init: i, do: -1,
				fiberSuspend: strings.EqualFold(literalString(oa, op.GetOp1Type(), op.GetOp1()), "Fiber") &&
					strings.EqualFold(literalString(oa, op.GetOp2Type(), op.GetOp2()), "suspend")})
			stack = append(stack, len(out)-1)
		case callInits[n]:
			out = append(out, call{init: i, do: -1})
			stack = append(stack, len(out)-1)
		case strings.HasPrefix(n, "ZEND_SEND_"):
			if len(stack) > 0 {
				at := stack[len(stack)-1]
				out[at].sends = append(out[at].sends, i)
			}
		case strings.HasPrefix(n, "ZEND_DO_"), n == "ZEND_CALLABLE_CONVERT":
			if len(stack) == 0 {
				continue
			}
			at := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if strings.HasPrefix(n, "ZEND_DO_") {
				out[at].do = i
			}
		}
	}
	return out
}

// findSpanningCalls reports the INIT oplines whose call is not completed in the
// same block it started in.
//
// Such a call captures its receiver and arguments in one block and emits the
// instruction in another, so the operands it recorded -- SSA values -- would be
// referenced outside the block that defines them. Every other value crossing a
// block is a place, and a resumable function depends on that being true of all
// of them: what has to survive a suspension is then exactly the set of places
// rather than a liveness computation over values. A call's arguments are spilled
// to places for the same reason.
//
// These appear because an argument can contain a short-circuit -- `f($a ?? $b)`
// -- which ends the block part way through the call's own opline run.
func findSpanningCalls(oa *pb.OpArray, oplines []*pb.Opline, name func(int) string, blockOf []int) map[int]bool {
	out := map[int]bool{}
	for _, c := range matchCalls(oa, oplines, name) {
		if c.do < 0 {
			continue
		}
		spans := blockOf[c.init] != blockOf[c.do]
		for _, sN := range c.sends {
			if blockOf[sN] != blockOf[c.do] {
				spans = true
			}
		}
		if spans {
			out[c.init] = true
		}
	}
	return out
}

// blockLeaders splits the opline stream into basic blocks, returning the leader
// index of each block and, for every opline, the block it belongs to. splitAfter
// names oplines that end a block for a reason the opcode alone does not show.
func blockLeaders(oa *pb.OpArray, oplines []*pb.Opline, name func(int) string, splitAfter map[int]bool) ([]int, []int) {
	leader := make([]bool, len(oplines)+1)
	leader[0] = true
	mark := func(t uint32) {
		if int(t) < len(oplines) {
			leader[t] = true
		}
	}
	// A `try` begins a block.
	//
	// A block's region is taken from the opline it *starts* at, so a try that
	// begins in the middle of one leaves its protected oplines in a block whose
	// region is the enclosing scope -- or none. The protection was silently
	// absent: an exception raised inside such a try propagated out instead of
	// reaching the handler, with nothing in the IR to show it.
	for _, tc := range oa.GetTryCatch() {
		mark(tc.GetTryOp())
	}
	for i := range oplines {
		op := oplines[i]
		if splitAfter[i] {
			// A fiber suspension ends its block for the same reason a yield
			// does: what follows is the continuation it resumes into.
			leader[i+1] = true
		}
		switch n := name(i); {
		case n == "ZEND_JMP", n == "ZEND_FAST_CALL":
			mark(op.GetOp1())
			leader[i+1] = true
		case n == "ZEND_CATCH":
			if op.GetExtendedValue()&zendLastCatch == 0 {
				mark(op.GetOp2())
			}
			leader[i+1] = true
		case n == "ZEND_JMPZ", n == "ZEND_JMPNZ", n == "ZEND_JMPZ_EX", n == "ZEND_JMPNZ_EX",
			n == "ZEND_JMP_SET", n == "ZEND_COALESCE", n == "ZEND_ASSERT_CHECK",
			n == "ZEND_JMP_NULL", n == "ZEND_BIND_INIT_STATIC_OR_JMP",
			n == "ZEND_JMP_FRAMELESS", n == "ZEND_FE_RESET_R", n == "ZEND_FE_RESET_RW":
			mark(op.GetOp2())
			leader[i+1] = true
		case n == "ZEND_FE_FETCH_R", n == "ZEND_FE_FETCH_RW":
			mark(op.GetExtendedValue())
			leader[i+1] = true
		case n == "ZEND_MATCH", n == "ZEND_SWITCH_LONG", n == "ZEND_SWITCH_STRING":
			// **Every arm is a target.** The jump table is op2's literal --
			// value to opline -- and extended_value is where an unmatched
			// subject goes. Marking only `leader[i+1]` left the whole match in
			// one block, so the terminator was overwritten by the first arm's
			// fallthrough and every subject took arm one.
			mark(op.GetExtendedValue())
			if int(op.GetOp2()) < len(oa.GetLiterals()) {
				for _, en := range oa.GetLiterals()[op.GetOp2()].GetEntries() {
					mark(uint32(en.GetValue().GetIntValue()))
				}
			}
			leader[i+1] = true
		case n == "ZEND_RETURN", n == "ZEND_RETURN_BY_REF", n == "ZEND_GENERATOR_RETURN",
			n == "ZEND_THROW", n == "ZEND_MATCH_ERROR", n == "ZEND_VERIFY_NEVER_TYPE",
			n == "ZEND_FAST_RET":
			leader[i+1] = true
		case n == "ZEND_YIELD", n == "ZEND_YIELD_FROM", n == "ZEND_GENERATOR_CREATE":
			// Each ends a block: a yield suspends the frame and resumes on an
			// edge, and GENERATOR_CREATE separates the eager prologue -- where
			// parameter binding and type verification run at call time -- from
			// the body that runs on resume.
			leader[i+1] = true
		}
	}
	blockOf := make([]int, len(oplines))
	var starts []int
	for i := range oplines {
		if leader[i] {
			starts = append(starts, i)
		}
		blockOf[i] = len(starts) - 1
	}
	return starts, blockOf
}

// findCrossBlock reports the TMP/VAR slots read in a block other than the one
// that defined them. A linear scan is enough: a slot is cross-block if a read
// precedes any definition within its own block.
func findCrossBlock(oplines []*pb.Opline, leaders []int, name func(int) string) map[uint32]bool {
	isLeader := map[int]bool{}
	for _, l := range leaders {
		isLeader[l] = true
	}
	out := map[uint32]bool{}
	defined := map[uint32]bool{}
	for i, op := range oplines {
		if isLeader[i] {
			defined = map[uint32]bool{}
		}
		read := func(kind, idx uint32) {
			if (kind == opTmpVar || kind == opVar) && !defined[idx] {
				out[idx] = true
			}
		}
		read(op.GetOp1Type(), op.GetOp1())
		read(op.GetOp2Type(), op.GetOp2())
		if readsItsResult[name(i)] {
			// The array accumulator arrives through the *result* slot, which the
			// engine reuses across the whole literal -- so for these the result
			// is an input as much as an output. Counting it only as a definition
			// hides every literal that spans a block, which is any literal
			// containing a short-circuit: `[$a ?? $b, $c]`.
			read(op.GetResultType(), op.GetResult())
		}
		if k := op.GetResultType(); k == opTmpVar || k == opVar {
			defined[op.GetResult()] = true
		}
	}
	return out
}

// readsItsResult lists the opcodes whose result slot is also an operand.
var readsItsResult = map[string]bool{
	"ZEND_ADD_ARRAY_ELEMENT": true,
	"ZEND_ADD_ARRAY_UNPACK":  true,
}

// regionOf reports the innermost try region containing an opline, or -1. The
// harvest carries these as opline ranges rather than as CFG edges, which is
// the region-based shape IR-DESIGN.md adopts -- exceptions are not modelled as
// LLVM-style `invoke` edges out of every call.
func regionOf(oa *pb.OpArray, opline int) int {
	best, bestLen := -1, 1<<30
	for i, tc := range oa.GetTryCatch() {
		start, stop := int(tc.GetTryOp()), int(tc.GetCatchOp())
		if stop == 0 || stop < start {
			stop = int(tc.GetFinallyEnd())
		}
		if opline >= start && opline < stop && stop-start < bestLen {
			best, bestLen = i, stop-start
		}
	}
	return best
}

// catchBodyOf is which region's catch body an opline sits in, or -1. Only a
// region with a `finally` answers: without one there is nothing for a throw
// inside the catch to route through, and the ordinary outward walk is right.
func catchBodyOf(oa *pb.OpArray, opline int) int {
	best, bestLen := -1, 1<<30
	for i, tc := range oa.GetTryCatch() {
		start, stop := int(tc.GetCatchOp()), int(tc.GetFinallyOp())
		if start == 0 || stop == 0 || stop <= start {
			continue
		}
		if opline >= start && opline < stop && stop-start < bestLen {
			best, bestLen = i, stop-start
		}
	}
	return best
}

// tempName is the place a cross-block temporary lives in.
func tempName(slot uint32) string { return fmt.Sprintf("$__t%d", slot) }

// iterName is the place a foreach iterator lives in, named after the temp slot
// the engine used so nested loops stay distinct. The iterator is frame state
// rather than a value -- FE_RESET defines it in one block, FE_FETCH reads it in
// another and FE_FREE ends it in a third -- which is why the interpreter keeps
// it in a side map keyed by the same slot.
func iterName(slot uint32) string { return fmt.Sprintf("$__iter%d", slot) }

// closureRef names the module-level entity a DECLARE_* instruction binds. The
// operand is typed IS_UNUSED but carries an index into OpArray.closures, which
// holds conditionally-declared *named* functions as well as closures.
// closureRef names the closure a declaration instruction points at.
//
// **The name alone is not unique.** PHP calls a closure `{closure:file:line}`,
// so two written on one line -- `[fn() => 1, fn() => 2]` -- share it, and a map
// keyed by the name kept only the second: both elements of that array became
// the same body. The reference therefore carries the **index** within the
// declaring op_array, which is what actually distinguishes them, and the
// parent's name so the key stays unique across bodies.
func closureRef(oa *pb.OpArray, idx uint32) (string, bool) {
	cs := oa.GetClosures()
	if int(idx) >= len(cs) {
		return "", false
	}
	return "@" + ClosureKey(displayName(oa), int(idx), cs[idx].GetName()), true
}

// ClosureKey is the name a closure body is registered and looked up under. The
// display name -- what a trace prints -- stays `OpArray.Name`; this is only the
// key.
func ClosureKey(parent string, idx int, name string) string {
	return fmt.Sprintf("%s#%d %s", parent, idx, name)
}

func displayName(oa *pb.OpArray) string {
	if n := oa.GetName(); n != "" {
		return n
	}
	return "{main}"
}

func cvName(oa *pb.OpArray, slot uint32) string {
	if int(slot) < len(oa.GetVarNames()) {
		return oa.GetVarNames()[slot]
	}
	return fmt.Sprintf("v%d", slot)
}

func literalText(oa *pb.OpArray, idx uint32) string {
	if int(idx) >= len(oa.GetLiterals()) {
		return "?"
	}
	return LiteralText(oa.GetLiterals()[idx])
}

// FloatText spells a float as IR operand text, so that reading it back gives a
// float again.
//
// `fmt.Sprint(9.0)` is "9", and every scanner of this text -- the emitter's
// `operand`, `Specialize`'s type facts -- tries `ParseInt` before `ParseFloat`,
// so an integral float literal came back an int and `var_dump(9.0)` printed
// `int(9)`. It reached further than a literal: the engine folds a typed class
// constant into the opline, so `const float F = 9` arrived here as the double
// it had already been widened to and was un-widened on the way out.
//
// This is the third time a renderer and its parser have disagreed about what
// this text says, which is why the two live beside each other in this file.
//
// `+Inf` and `NaN` keep their `%v` spelling. They are not Go literals either,
// and the emitter already translates them by name.
func FloatText(f float64) string {
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return fmt.Sprint(f)
	}
	// The same shortest-round-trip digits `%v` produces, so nothing but the
	// integral case changes spelling.
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return s
}

// LiteralText renders a literal as IR operand text, the same spelling an
// instruction operand uses. Exported so a consumer that meets a literal outside
// an opline -- a property's declared default, say -- renders it identically
// rather than inventing a second spelling.
func LiteralText(lit *pb.Literal) string {
	switch lit.GetKind() {
	case pb.Literal_KIND_STRING:
		return fmt.Sprintf("%q", lit.GetStringValue())
	case pb.Literal_KIND_INT:
		return fmt.Sprint(lit.GetIntValue())
	case pb.Literal_KIND_FLOAT:
		return FloatText(lit.GetFloatValue())
	case pb.Literal_KIND_BOOL:
		// Which bool, not that it is one. Rendering both as "bool" made
		// `$t = true` and `$f = false` the same instruction, so any consumer of
		// the IR -- the emitter is the one that noticed -- could not tell them
		// apart. The harvest never lost it: IS_TRUE arrives as Int 1.
		if lit.GetIntValue() != 0 {
			return "true"
		}
		return "false"
	case pb.Literal_KIND_NULL:
		return "null"
	case pb.Literal_KIND_ARRAY:
		// The entries, not the fact that it is an array. `<array>` discarded a
		// value the harvest had kept -- the same loss as rendering both booleans
		// as `bool` -- and it made every array-shaped body unrenderable for any
		// consumer, not only the emitter.
		//
		// Spelled the way PHP spells it, so the IR stays readable by hand, and
		// parsed back by ParseArrayLiteral below. The renderer and the parser
		// sit together deliberately: a scanner and a renderer that disagree
		// about what the text says is a defect this project has already had
		// twice.
		var parts []string
		for _, e := range lit.GetEntries() {
			v := LiteralText(e.GetValue())
			switch {
			case e.GetIntKey():
				parts = append(parts, fmt.Sprintf("%d => %s", e.GetIntKeyValue(), v))
			default:
				parts = append(parts, fmt.Sprintf("%q => %s", e.GetStringKey(), v))
			}
		}
		return "[" + strings.Join(parts, ", ") + "]"
	}
	return "<lit>"
}

// ArrayEntry is one entry of a constant array literal, as ParseArrayLiteral
// recovers it from the operand text.
type ArrayEntry struct {
	IntKey    bool
	IntValue  int64
	StringKey string
	// Value is the entry's value as operand text, so a consumer renders it
	// through whatever path it already uses for operands -- including a nested
	// array, which arrives as another `[...]`.
	Value string
}

// ParseArrayLiteral recovers the entries of an array literal operand.
//
// The inverse of what LiteralText writes for KIND_ARRAY, and adjacent to it on
// purpose: the two are one contract, and `TestArrayLiteralRoundTrips` fails if
// they drift. Reports false for anything that is not an array literal.
func ParseArrayLiteral(text string) ([]ArrayEntry, bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "[") || !strings.HasSuffix(text, "]") {
		return nil, false
	}
	body := strings.TrimSpace(text[1 : len(text)-1])
	if body == "" {
		return nil, true
	}
	var out []ArrayEntry
	for _, part := range splitTop(body, ',') {
		kv := splitTop(part, '=')
		if len(kv) != 2 {
			return nil, false
		}
		key := strings.TrimSpace(kv[0])
		val := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(kv[1]), ">"))
		e := ArrayEntry{Value: val}
		if n, err := strconv.ParseInt(key, 10, 64); err == nil {
			e.IntKey, e.IntValue = true, n
		} else {
			k, err := strconv.Unquote(key)
			if err != nil {
				return nil, false
			}
			e.StringKey = k
		}
		out = append(out, e)
	}
	return out, true
}

// splitTop splits on a separator that is outside every string literal and every
// bracket, so a nested array and a comma inside a string both survive.
func splitTop(s string, sep rune) []string {
	var out []string
	depth, quoted, escaped, start := 0, false, false, 0
	for i, r := range s {
		switch {
		case escaped:
			escaped = false
		case r == '\\' && quoted:
			escaped = true
		case r == '"':
			quoted = !quoted
		case quoted:
		case r == '[':
			depth++
		case r == ']':
			depth--
		case r == sep && depth == 0:
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}
