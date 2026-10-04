package lower_test

import (
	"flag"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/moriyoshi/php2go/internal/ir"
	pb "github.com/moriyoshi/php2go/internal/ir/irpb"
	"github.com/moriyoshi/php2go/internal/lower"
	"github.com/moriyoshi/php2go/internal/zend"
)

var update = flag.Bool("update", false, "rewrite the golden IR files")

var engine *zend.Engine

func TestMain(m *testing.M) {
	started, err := zend.Start()
	if err != nil {
		panic(err)
	}
	engine = started
	code := m.Run()
	engine.Close()
	os.Exit(code)
}

// lowerFixture compiles a testdata file and lowers every body in it, keyed by
// the name the assembly prints. The filename is reduced to its base so a golden
// does not encode the checkout's path.
// lowerFixtureUnpromoted lowers with mem2reg off.
//
// `FindLoopBuilders` is specified on the **unpromoted** shape: it looks for
// `$out .= $x` inside a loop, which promotion turns into a loop-carried block
// parameter with no store to find. When builder promotion becomes a pass rather
// than an analysis it will have to run *before* mem2reg for the same reason,
// and this helper is where that ordering is written down.
func lowerFixtureUnpromoted(t *testing.T, name string) map[string]*lower.Function {
	t.Helper()
	return lowerWith(t, name, true)
}

func lowerFixture(t *testing.T, name string) map[string]*lower.Function {
	t.Helper()
	return lowerWith(t, name, false)
}

func lowerWith(t *testing.T, name string, skipPromotion bool) map[string]*lower.Function {
	t.Helper()
	unit, err := engine.CompileUnit(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	frameless, err := engine.FramelessFunctions()
	if err != nil {
		t.Fatal(err)
	}
	lw := &lower.Lowerer{Names: zend.OpcodeNames(), Frameless: frameless, SkipMem2Reg: skipPromotion}
	out := map[string]*lower.Function{}
	for _, oa := range bodies(ir.EncodeUnit(unit)) {
		fn, err := lw.Lower(oa)
		if err != nil {
			t.Fatalf("%s: %s: %v", name, oa.GetName(), err)
		}
		fn.File = filepath.Base(fn.File)
		out[fn.Name] = fn
	}
	return out
}

func bodies(unit *pb.Unit) []*pb.OpArray {
	var out []*pb.OpArray
	var add func(*pb.OpArray)
	add = func(oa *pb.OpArray) {
		if oa == nil {
			return
		}
		out = append(out, oa)
		for _, nested := range oa.GetClosures() {
			add(nested)
		}
	}
	add(unit.GetFile())
	for _, fn := range unit.GetFunctions() {
		add(fn)
	}
	for _, class := range unit.GetClasses() {
		for _, method := range class.GetMethods() {
			add(method)
		}
	}
	return out
}

// text renders one named body, failing if the fixture has no such body -- a
// silently absent function would make every assertion below vacuous.
func text(t *testing.T, fns map[string]*lower.Function, name string) string {
	t.Helper()
	fn, ok := fns[name]
	if !ok {
		var have []string
		for n := range fns {
			have = append(have, n)
		}
		t.Fatalf("no body named %q; have %v", name, have)
	}
	return fn.String()
}

// TestGoldenIR pins the textual format itself. IR-ASSEMBLY.md exists so the IR
// can be read and diffed by hand, which makes the rendered text part of the
// contract rather than a debugging aid -- a change to spacing, operand order or
// directive placement is a change to the format and should have to be admitted.
func TestGoldenIR(t *testing.T) {
	for _, name := range []string{"calls.php", "write.php", "finally.php", "foreach.php", "shortcircuit.php", "namespaced.php", "builder.php", "regions.php", "fiber.php"} {
		t.Run(name, func(t *testing.T) {
			fns := lowerFixture(t, name)
			var names []string
			for n := range fns {
				names = append(names, n)
			}
			// Deterministic order: the file body first, then the rest sorted.
			var out strings.Builder
			for _, n := range sortedWithMainFirst(names) {
				out.WriteString(fns[n].String())
			}
			golden := filepath.Join("testdata", strings.TrimSuffix(name, ".php")+".ir")
			if *update {
				if err := os.WriteFile(golden, []byte(out.String()), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (run with -update to create it)", err)
			}
			if got := out.String(); got != string(want) {
				t.Errorf("lowered IR changed.\n--- want ---\n%s\n--- got ---\n%s", want, got)
			}
		})
	}
}

// TestGoldenSpecializedIR pins the IR **after** specialization, which is the
// form the emitter reads and which no other golden covers.
//
// The plain goldens run Lower alone, deliberately: Specialize is a choice about
// the consumer, not part of lowering, and an interpreter over the same IR would
// make a different choice or none. But that left the rendering of the one
// instruction the whole operator family goes through -- call.intrinsic --
// pinned by nothing at all. specialize_test.go counts variants; it never looks
// at the text.
func TestGoldenSpecializedIR(t *testing.T) {
	fns := lowerFixture(t, "specialized.php")
	var names []string
	for n := range fns {
		names = append(names, n)
	}
	var out strings.Builder
	for _, n := range sortedWithMainFirst(names) {
		lower.Specialize(fns[n], nil)
		out.WriteString(fns[n].String())
	}
	golden := filepath.Join("testdata", "specialized.ir")
	if *update {
		if err := os.WriteFile(golden, []byte(out.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if got := out.String(); got != string(want) {
		t.Errorf("specialized IR changed.\n--- want ---\n%s\n--- got ---\n%s", want, got)
	}
}

func sortedWithMainFirst(names []string) []string {
	var rest []string
	for _, n := range names {
		if n != "{main}" {
			rest = append(rest, n)
		}
	}
	for i := 1; i < len(rest); i++ {
		for j := i; j > 0 && rest[j] < rest[j-1]; j-- {
			rest[j], rest[j-1] = rest[j-1], rest[j]
		}
	}
	return append([]string{"{main}"}, rest...)
}

// The three-phase call protocol -- INIT_FCALL, a SEND per argument, DO_FCALL --
// is 43.9% of the corpus's oplines, and collapsing each run into one node is the
// point of the pass. If it regressed, the opcodes would still lower one by one
// and nothing else in the suite would notice.
func TestCallProtocolCollapsesIntoOneInstruction(t *testing.T) {
	got := text(t, lowerFixture(t, "calls.php"), "make")
	// An argument is a *read* now, so `$who` reaches the call as the SSA value
	// the read produced. What this test is about is the collapse -- INIT_FCALL,
	// two SENDs and DO_FCALL becoming one instruction -- so it matches the
	// shape and the arity, not the spelling of the operand.
	for _, want := range []regexp.Regexp{
		*regexp.MustCompile(`call\.fn "helper"\(byval %\d+, byval 2\)`),
		// A **method** callee is not resolved at compile time, so the engine
		// sends `ZEND_SEND_VAR_EX` -- the callee's arginfo decides at run time
		// -- and the argument goes as the caller's storage under `preferref`.
		// The place survives instead of being promoted, which is the cost of
		// passing storage; what this test is about is still the collapse.
		*regexp.MustCompile(`call\.method \$this, "decorate"\(preferref \$who\)`),
	} {
		if !want.MatchString(got) {
			t.Errorf("missing %s in:\n%s", want.String(), got)
		}
	}
	// The protocol's own opcodes must not survive as instructions.
	for _, gone := range []string{"init_fcall", "send_val", "send_var", "do_fcall", "do_ucall"} {
		if strings.Contains(got, gone) {
			t.Errorf("call protocol did not collapse: %s survives in:\n%s", gone, got)
		}
	}
}

// An opline holds two operands, so a three-operand node spills its third into a
// following ZEND_OP_DATA. Folding the pair back into one instruction is the
// un-flattening; leaving OP_DATA standing would be a lowering of the encoding
// rather than of the program.
func TestOpDataFoldsIntoTheInstructionThatNeedsIt(t *testing.T) {
	got := text(t, lowerFixture(t, "write.php"), "fill")
	if strings.Contains(got, "op_data") {
		t.Errorf("OP_DATA survived lowering:\n%s", got)
	}
	// The fold is now visible in the call itself: container, key and value are
	// the three arguments of one instruction, where the encoding had them
	// spread across two oplines.
	if !strings.Contains(got, `"dim.set"(%1, "b", 1)`) {
		t.Errorf("nested write did not fold into one call:\n%s", got)
	}
}

// `$a[] = v` addresses the next free slot, which is not a value any expression
// can name. It is a place kind of its own, not a dim.ref whose key operand
// happens to be missing -- which would also render as an empty operand the
// assembly grammar has no term for.
func TestAppendIsItsOwnPlaceKind(t *testing.T) {
	got := text(t, lowerFixture(t, "write.php"), "fill")
	if !strings.Contains(got, `"dim.append"($rows, 2)`) {
		t.Errorf("append did not lower to its own intrinsic:\n%s", got)
	}
	if strings.Contains(got, `"dim.set"($rows, , `) {
		t.Errorf("append rendered as a dim.set with an empty key:\n%s", got)
	}
}

// IR-DESIGN.md inlines finally bodies at each exit rather than treating them as
// a subroutine, because FAST_RET is an indirect jump and a CFG with one is not
// static. The cost is duplication, measured at 0.70% of the corpus.
func TestFinallyBodyIsInlinedAtEachExit(t *testing.T) {
	fns := lowerFixture(t, "finally.php")
	got := text(t, fns, "guarded")
	if n := strings.Count(got, `call.intrinsic "echo"("done\n")`); n < 2 {
		t.Errorf("finally body appears %d times, expected one copy per exit:\n%s", n, got)
	}
	// FAST_CALL and FAST_RET must both disappear: the first becomes the splice,
	// the second becomes ordinary fallthrough.
	for _, gone := range []string{"fast_call", "fast_ret"} {
		if strings.Contains(got, gone) {
			t.Errorf("%s survived inlining:\n%s", gone, got)
		}
	}
	// The body's own blocks exist only as those copies.
	var dead int
	for _, b := range fns["guarded"].Blocks {
		if b.Dead {
			dead++
		}
	}
	if dead == 0 {
		t.Errorf("no block was marked dead, so the finally body was also emitted standalone")
	}
}

// Inlining splices oplines from outside the current block into it, so anything
// bounded by the block rather than by the opline stream breaks on a finally
// body. A three-operand node is the case that bites: `$this->last = 'done'`
// inside a finally is an ASSIGN_OBJ whose OP_DATA sits right after it in the
// op_array but far outside the block it gets spliced into. Five CakePHP bodies
// bailed on exactly this while claiming the OP_DATA was missing.
func TestThreeOperandNodeInsideAnInlinedFinally(t *testing.T) {
	got := text(t, lowerFixture(t, "finally.php"), "run")
	if n := strings.Count(got, `"prop.set"($this, "last"`); n < 2 {
		t.Errorf("property write appears %d times, expected one copy per exit:\n%s", n, got)
	}
	if strings.Contains(got, "op_data") {
		t.Errorf("OP_DATA survived:\n%s", got)
	}
}

// A foreach iterator is frame state, not a value: FE_RESET defines it in one
// block, FE_FETCH reads it in another and FE_FREE ends it in a third. Anything
// outliving its block is a place under IR-DESIGN.md's rule.
func TestForeachIteratorIsAPlace(t *testing.T) {
	// Lowered without promotion: this is a **lowering** property about where a
	// value is parked, and mem2reg legitimately removes the parking when the
	// value has one definition and no aliasing.
	got := text(t, lowerFixtureUnpromoted(t, "foreach.php"), "total")
	// `write_var`, not `slot.set`: the handle is a php.Value now, so parking it
	// is an ordinary value write rather than a frame-slot move.
	if !strings.Contains(got, "iter.new") || !strings.Contains(got, `"write_var"($__iter`) {
		t.Errorf("iterator was not given a place:\n%s", got)
	}
	// The test is a call now, not part of the terminator: `iter.valid` is an
	// operation on the iterator like every other step of the protocol, and the
	// branch takes the value it produced.
	if !strings.Contains(got, `"iter.valid"($__iter`) {
		t.Errorf("loop back-edge does not test the iterator place:\n%s", got)
	}
}

// `foreach ($a as $k => $v)` puts the value in a CV but the key in a temporary,
// so where each lands has to be read off the operand kind. Assuming both are
// CVs invents a variable name out of the slot number *and* leaves the place the
// next block reads the key from unwritten -- wrong in both directions at once,
// and invisible to any coverage count.
func TestForeachKeyGoesWhereTheOperandSays(t *testing.T) {
	// Lowered without promotion: this is a **lowering** property about where a
	// value is parked, and mem2reg legitimately removes the parking when the
	// value has one definition and no aliasing.
	got := text(t, lowerFixtureUnpromoted(t, "foreach.php"), "total")
	i := strings.Index(got, "iter.key")
	if i < 0 {
		t.Fatalf("no key fetch emitted:\n%s", got)
	}
	// The key is written to a temporary place, and that is the same place the
	// assignment to $k reads from.
	// `write_var`, not `slot.set`: the key temporary holds a **php.Value**, so
	// writing it is a value write. `slot.set` is for the slots that hold
	// something else -- an iterator handle, a rope buffer.
	if !strings.Contains(got[i:], `"write_var"($__t`) {
		t.Errorf("key was not written to its temporary place:\n%s", got[i:])
	}
	for _, line := range strings.Split(got, "\n") {
		if !strings.Contains(line, "slot.set $") && !strings.Contains(line, `"write_var"($`) {
			continue
		}
		place := strings.TrimPrefix(strings.Fields(strings.TrimSpace(line))[1], "$")
		place = strings.TrimSuffix(place, ",")
		// A name like "v6" is cvName's fallback for a slot that is not a CV at
		// all, which is exactly the bug: no such variable exists in the source.
		if len(place) > 1 && place[0] == 'v' && place[1] >= '0' && place[1] <= '9' {
			t.Errorf("wrote to %q, a variable name invented from a slot number:\n%s", place, got)
		}
	}
}

// A return type check *coerces* -- `function f(): int { return 1.0; }` widens --
// so the verified value has to land somewhere the following RETURN can read.
// For a CV operand that means writing back to the variable; computing the
// coercion and dropping it would return the value the check was asked to widen.
func TestVerifyReturnWritesTheCoercedValueBack(t *testing.T) {
	// Lowered without promotion: this is a **lowering** property, and mem2reg
	// legitimately removes the place it is about -- a value with one definition
	// and no aliasing becomes an SSA value, which is the whole point. The
	// default pipeline's shape is recorded by the goldens.
	got := text(t, lowerFixtureUnpromoted(t, "foreach.php"), "total")
	// The checked value is what the read of `$sum` produced, so the check names
	// an SSA value; the write-back is what this is about.
	loc := regexp.MustCompile(`"coerce_return"\(%\d+\)`).FindStringIndex(got)
	if loc == nil {
		t.Fatalf("no return-type check emitted:\n%s", got)
	}
	i := loc[0]
	// A **binding**, marked `separate`: in a by-value function the write-back
	// replaces a reference the variable holds rather than writing through it,
	// so a by-reference parameter's caller keeps its own value.
	if !strings.Contains(got[i:], `"bind_var"($sum,`) || !strings.Contains(got[i:], lower.SeparateAttr) {
		t.Errorf("coerced value was not written back to the variable, separated:\n%s", got[i:])
	}
}

// A body the pass cannot lower must say which opcode stopped it, and must not
// produce a Function. Reporting the opcode is what makes the bail histogram a
// worklist rather than a number, and returning nothing is what stops a
// half-lowered body being mistaken for a complete one.
func TestUnmodelledOpcodeBailsNamingIt(t *testing.T) {
	lw := &lower.Lowerer{Names: []string{"ZEND_NOP", "ZEND_ECHO"}}
	// Opcode 7 is past the end of that table, so the pass cannot even name it.
	fn, err := lw.Lower(&pb.OpArray{Oplines: []*pb.Opline{{Opcode: 7}}})
	if fn != nil {
		t.Errorf("a body that bailed still produced a function")
	}
	var bail *lower.Bail
	if !asBail(err, &bail) {
		t.Fatalf("error was %T, want *lower.Bail", err)
	}
	if bail.Opcode != "?" || bail.Reason != "not lowered" {
		t.Errorf("bail = %+v, want the unnamed opcode and \"not lowered\"", bail)
	}
	if got := bail.Error(); got != "?: not lowered" {
		t.Errorf("Error() = %q", got)
	}
}

// An empty op_array is a bail with no opcode to blame, and must not be reported
// as an opcode named "".
func TestEmptyBodyBails(t *testing.T) {
	lw := &lower.Lowerer{Names: []string{"ZEND_NOP"}}
	_, err := lw.Lower(&pb.OpArray{})
	var bail *lower.Bail
	if !asBail(err, &bail) {
		t.Fatalf("error was %T, want *lower.Bail", err)
	}
	if bail.Opcode != "" || bail.Reason != "empty" {
		t.Errorf("bail = %+v", bail)
	}
	if got := bail.Error(); got != "empty" {
		t.Errorf("Error() = %q, want the reason alone", got)
	}
}

func asBail(err error, out **lower.Bail) bool {
	b, ok := err.(*lower.Bail)
	if ok {
		*out = b
	}
	return ok
}

// `&&`, `||`, `??` and `?:` all compile to the same shape -- branch, and on the
// short-circuit path leave a value in a slot the fallback would otherwise write
// -- but they do not leave the *same* value, and the opcode names do not say so.
// Settled with php2eval against the engine: `0 && $b` is bool(false) and
// `1 || $b` is bool(true), so those two boolify; `0 ?? $b` is int(0) and
// `"a" ?: $b` is string("a"), so those two copy the operand through unchanged.
// Getting this backwards would turn every `?:` into a boolean.
func TestShortCircuitBoolifiesOrCopiesAsTheEngineDoes(t *testing.T) {
	// Lowered without promotion: this is a **lowering** property, and mem2reg
	// legitimately removes the place it is about -- a value with one definition
	// and no aliasing becomes an SSA value, which is the whole point. The
	// default pipeline's shape is recorded by the goldens.
	got := text(t, lowerFixtureUnpromoted(t, "shortcircuit.php"), "pick")
	// Two boolifying forms and two copying ones, in source order.
	// `$a` reaches these as the value its read produced, so the operand is an
	// SSA name. Resolving the name back to the variable it was read from makes
	// this a **stronger** assertion than the old text match: it says these two
	// forms boolify *the operand `$a`*, where matching `to_bool($a)` only said
	// something somewhere boolified.
	if n := appliedTo(got, "to_bool", "a"); n != 2 {
		t.Errorf("expected && and || to boolify $a, found %d:\n%s", n, got)
	}
	// `??` and `?:` both copy, but **not the same intrinsic**: `$a ?? 5` does
	// not warn on an unset `$a` and `$a ?: 5` does, which the engine confirms.
	// Asserting one of each is stronger than counting two copies, and it is the
	// distinction the two intrinsics exist to carry.
	if n := appliedTo(got, "copy.silent", "a"); n != 1 {
		t.Errorf("expected ?? to copy $a silently, found %d:\n%s", n, got)
	}
	if n := appliedTo(got, "copy", "a"); n != 1 {
		t.Errorf("expected ?: to copy $a loudly, found %d:\n%s", n, got)
	}
}

// The short-circuit slot is written on both arms and read after the join, so it
// is a place -- the same rule that covers any cross-block temporary. If it
// stayed an SSA value the fallback's definition would be invisible to the
// reader and the merge would silently take whichever arm the pass saw last.
func TestShortCircuitMergesThroughAPlace(t *testing.T) {
	// Lowered without promotion: this is a **lowering** property about a value
	// being parked in a place, and mem2reg legitimately removes the parking.
	got := text(t, lowerFixtureUnpromoted(t, "shortcircuit.php"), "pick")
	for _, want := range []string{`"write_var"($__t2, %2)`, `"write_var"($__t2, %3)`} {
		if !strings.Contains(got, want) {
			t.Errorf("both arms should write the same place; missing %q in:\n%s", want, got)
		}
	}
}

// `??` takes the right-hand side only when the left is null, so null is the
// fall-through and anything else jumps past it. Reversed, `$a ?? $b` would
// evaluate $b exactly when it must not.
func TestCoalesceFallsThroughOnNull(t *testing.T) {
	got := text(t, lowerFixture(t, "shortcircuit.php"), "pick")
	// The test is an `is_set` call and the branch takes its answer, so the arms
	// are the other way round from the `br.null` this used to read: the **null**
	// arm is the second one.
	// Each target may carry block arguments now -- `bb6(%9)` -- so the label is
	// followed by an optional argument list.
	m := regexp.MustCompile(
		`"is_set"\(%\d+\)\n\s*br %\d+, (bb\d+)(?:\([^)]*\))?, (bb\d+)(?:\([^)]*\))?`,
	).FindStringSubmatch(got)
	if m == nil {
		t.Fatalf("no coalesce branch emitted:\n%s", got)
	}
	ifSet, ifNull := blockNumber(t, m[1]), blockNumber(t, m[2])
	if ifNull >= ifSet {
		t.Errorf("null should fall through to the next block, got %v", m[0])
	}
}

// An array literal whose elements short-circuit spans several blocks, and the
// accumulator arrives through the *result* slot rather than op1 or op2 -- so
// the cross-block analysis has to be told that these opcodes read what they
// write. Missing that hid every such literal behind a bail, which was 56 bodies
// of CakePHP once the short-circuit opcodes started lowering.
func TestArrayLiteralAccumulatorSurvivesABlockBoundary(t *testing.T) {
	// Lowered without promotion: this is a **lowering** property about a value
	// being parked in a place, and mem2reg legitimately removes the parking.
	got := text(t, lowerFixtureUnpromoted(t, "shortcircuit.php"), "pick")
	if !strings.Contains(got, `"array.append"($__t3,`) {
		t.Errorf("accumulator did not become a place:\n%s", got)
	}
	if n := strings.Count(got, "array.new"); n != 1 {
		t.Errorf("literal produced %d arrays, expected exactly one:\n%s", n, got)
	}
}

// A frameless call names no function: the callee is an index into a table the
// manifest carries, and the arity is in the opcode's own name, with the third
// argument spilled into an OP_DATA. Built by hand rather than harvested so the
// assertion does not depend on which calls this build chooses to make frameless.
func TestFramelessCallResolvesItsNameAndFoldsOpData(t *testing.T) {
	const icall3, opData, ret = 1, 2, 3
	names := make([]string, 8)
	names[icall3] = "ZEND_FRAMELESS_ICALL_3"
	names[opData] = "ZEND_OP_DATA"
	names[ret] = "ZEND_RETURN"
	lw := &lower.Lowerer{Names: names, Frameless: []string{"strlen", "count", "in_array"}}

	fn, err := lw.Lower(&pb.OpArray{
		Name:     "f",
		VarNames: []string{"a", "b"},
		Oplines: []*pb.Opline{
			{Opcode: icall3, Op1Type: 8, Op1: 0, Op2Type: 8, Op2: 1,
				ResultType: 2, Result: 5, ExtendedValue: 2},
			{Opcode: opData, Op1Type: 8, Op1: 0},
			{Opcode: ret, Op1Type: 2, Op1: 5},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := fn.String()
	if !regexp.MustCompile(`call\.fn "in_array"\(byval %\d+, byval %\d+, byval %\d+\), frameless`).MatchString(got) {
		t.Errorf("frameless call did not resolve or did not fold its OP_DATA:\n%s", got)
	}
	if strings.Contains(got, "op_data") {
		t.Errorf("OP_DATA survived:\n%s", got)
	}
}

// Without the table the opcode names nothing, so an index it cannot resolve has
// to bail rather than emit a call to an unnamed function.
func TestFramelessIndexOutOfRangeBails(t *testing.T) {
	names := make([]string, 4)
	names[1] = "ZEND_FRAMELESS_ICALL_1"
	lw := &lower.Lowerer{Names: names} // no Frameless table at all
	_, err := lw.Lower(&pb.OpArray{
		Oplines: []*pb.Opline{{Opcode: 1, Op1Type: 8, Op1: 0, ResultType: 2, Result: 1}},
	})
	var bail *lower.Bail
	if !asBail(err, &bail) {
		t.Fatalf("error was %T, want *lower.Bail", err)
	}
	if bail.Reason != "frameless function index out of range" {
		t.Errorf("bail = %+v", bail)
	}
}

func blockNumber(t *testing.T, label string) int {
	t.Helper()
	n, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(label), "bb"))
	if err != nil {
		t.Fatalf("not a block label: %q", label)
	}
	return n
}

// An unqualified call inside a namespace names two functions, and PHP resolves
// whichever exists: `implode()` in namespace App\Support means
// `App\Support\implode` if that is defined and the global `implode` otherwise.
// The compiler stores all three forms in consecutive literals and op2 names only
// the first, so keeping just that one claims a call that may well resolve
// elsewhere -- silently, and for most calls a framework makes.
func TestNamespacedCallCarriesItsGlobalFallback(t *testing.T) {
	got := text(t, lowerFixture(t, "namespaced.php"), "App\\Support\\render")
	// `$rows` reaches the call as the value its read produced, so the argument
	// is an SSA name. What is under test is the fallback, not the operand.
	want := regexp.MustCompile(`call\.fn "App\\\\Support\\\\implode"\(byval ",", preferref \$rows\), fallback="implode"`)
	if !want.MatchString(got) {
		t.Errorf("missing %s in:\n%s", want.String(), got)
	}
}

// A fully qualified call resolves to exactly one function, so inventing a
// fallback for it would claim a resolution PHP does not perform.
func TestFullyQualifiedCallHasNoFallback(t *testing.T) {
	got := text(t, lowerFixture(t, "namespaced.php"), "App\\Support\\render")
	i := strings.Index(got, `call.fn "str_repeat"`)
	if i < 0 {
		t.Fatalf("qualified call not found:\n%s", got)
	}
	line := got[i : i+strings.Index(got[i:], "\n")]
	if strings.Contains(line, "fallback=") {
		t.Errorf("qualified call was given a fallback: %s", line)
	}
}

// `strtoupper(...)` inside a namespace resolves the same pair the call would
// have, so the closure has to carry both names too.
func TestFirstClassCallableCarriesTheFallback(t *testing.T) {
	got := text(t, lowerFixture(t, "namespaced.php"), "App\\Support\\render")
	want := `closure.from_call "App\\Support\\strtoupper", fallback="strtoupper"`
	if !strings.Contains(got, want) {
		t.Errorf("missing %s in:\n%s", want, got)
	}
}

// The fallback lives two literals past the one op2 names. If the table is
// shorter than that the pass must bail rather than render literalText's "?"
// placeholder as a function name.
func TestNamespacedCallWithoutItsFallbackLiteralBails(t *testing.T) {
	names := make([]string, 4)
	names[1] = "ZEND_INIT_NS_FCALL_BY_NAME"
	lw := &lower.Lowerer{Names: names}
	_, err := lw.Lower(&pb.OpArray{
		// Only the name itself, not the two literals that should follow it.
		Literals: []*pb.Literal{{Kind: pb.Literal_KIND_STRING, StringValue: "App\\f"}},
		Oplines:  []*pb.Opline{{Opcode: 1, Op2Type: 1, Op2: 0}},
	})
	var bail *lower.Bail
	if !asBail(err, &bail) {
		t.Fatalf("error was %T, want *lower.Bail", err)
	}
	if bail.Reason != "namespaced call has no fallback literal" {
		t.Errorf("bail = %+v", bail)
	}
}

// An interpolation is a *builder*, and the engine's own encoding says so:
// ROPE_ADD's result slot is its own op1, an in-place append into a TMP the
// compiler invented for this one literal. Lowering it to a chain of pure
// `concat` throws that away and leaves D3 to rediscover it by inference.
// A plain `$a . $b` is not a builder and must stay a binary op.
func TestRopeLowersToABuilderAndPlainConcatDoesNot(t *testing.T) {
	got := text(t, lowerFixtureUnpromoted(t, "builder.php"), "build")
	for _, want := range []string{
		`%4:*strbuf = call.intrinsic "strbuf.new"("x")`,
		`%9:*str = call.intrinsic "strbuf.finish"(%8)`,
		// A plain concat is an intrinsic **call**, formed by lowering itself:
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in:\n%s", want, got)
		}
	}
	// The rope appends into its buffer and the plain concat is a call on two
	// read values. Operands are SSA names now, so these match the shape.
	for _, want := range []*regexp.Regexp{
		regexp.MustCompile(`%5:\*strbuf = call\.intrinsic "strbuf\.append"\(%4, %\d+\)`),
		regexp.MustCompile(`%3:zval = call\.intrinsic "concat"\(%\d+, %\d+\)`),
	} {
		if !want.MatchString(got) {
			t.Errorf("missing %s in:\n%s", want.String(), got)
		}
	}
	// The rope must not have become a concat chain, and the plain concat must
	// not have become a buffer.
	if strings.Contains(got, `"concat"(%4`) || strings.Contains(got, `"strbuf.new"($a)`) {
		t.Errorf("rope and plain concat were confused:\n%s", got)
	}
}

// The claim that a builder needs no escape analysis rests on the buffer never
// reaching anything but another builder op. That is checkable rather than
// assertable, so it is checked -- here on the fixture, and over both corpora at
// 1,919 definitions and zero escapes when this landed.
func TestBuilderValuesAreConsumedOnlyByBuilderOps(t *testing.T) {
	for _, fixture := range []string{"builder.php", "calls.php", "shortcircuit.php"} {
		for name, fn := range lowerFixture(t, fixture) {
			bufs := map[string]bool{}
			for _, b := range fn.Blocks {
				for _, in := range b.Insts {
					if in.Result != nil && in.Result.Type == "*strbuf" {
						bufs[in.Result.String()] = true
					}
				}
			}
			for _, b := range fn.Blocks {
				for _, in := range b.Insts {
					for i, operand := range in.Operands {
						if !bufs[operand] {
							continue
						}
						builder := strings.HasPrefix(in.Opcode, "strbuf.") && i == 0
						promoted := in.Opcode == "local.set" && i == 1 &&
							strings.HasPrefix(in.Operands[0], "$__t")
						if !builder && !promoted {
							t.Errorf("%s/%s: buffer %s escaped into %q",
								fixture, name, operand, in.Opcode)
						}
					}
				}
			}
		}
	}
}

// Successors are recorded structurally so passes can walk the CFG, while the
// terminator text is what a reader sees. Those are two encodings of the same
// fact, and nothing stops them drifting apart -- so this checks every block of
// every fixture: the block labels appearing in the text must be exactly Succs,
// in order.
func TestSuccessorsMatchTheTerminatorText(t *testing.T) {
	label := regexp.MustCompile(`bb(\d+)`)
	for _, fixture := range []string{
		"calls.php", "write.php", "finally.php", "foreach.php",
		"shortcircuit.php", "namespaced.php", "builder.php",
	} {
		for name, fn := range lowerFixture(t, fixture) {
			for _, b := range fn.Blocks {
				var fromText []int
				for _, m := range label.FindAllStringSubmatch(b.Term, -1) {
					n, _ := strconv.Atoi(m[1])
					fromText = append(fromText, n)
				}
				if len(fromText) != len(b.Succs) {
					t.Errorf("%s/%s bb%d: text names %v, Succs is %v (%q)",
						fixture, name, b.ID, fromText, b.Succs, b.Term)
					continue
				}
				for i := range fromText {
					if fromText[i] != b.Succs[i] {
						t.Errorf("%s/%s bb%d: text names %v, Succs is %v (%q)",
							fixture, name, b.ID, fromText, b.Succs, b.Term)
						break
					}
				}
				for _, sN := range b.Succs {
					if sN < 0 || sN >= len(fn.Blocks) {
						t.Errorf("%s/%s bb%d: successor bb%d out of range",
							fixture, name, b.ID, sN)
					}
				}
			}
		}
	}
}

// The loop-carried builder analysis is the one place in this package where being
// wrong changes what a program prints rather than how fast it runs: promoting a
// variable that is aliased means appends land somewhere the alias cannot see.
// So every disqualifier gets a case built to defeat it, and the analysis has to
// refuse each one for the stated reason. A promotion that fires here is a bug,
// not an optimisation.
func TestLoopBuilderRefusesEveryAliasingPath(t *testing.T) {
	fns := lowerFixtureUnpromoted(t, "loopbuilder.php")
	want := map[string]string{
		"byReference":          "aliased by reference",
		"capturedByClosure":    "captured by a closure",
		"passedByReference":    "passed by reference",
		"boundToAGlobal":       "bound to a global",
		"boundToAStatic":       "bound to a static",
		"readInsideTheLoop":    "read inside the loop",
		"unsetMidway":          "unset",
		"variableVariables":    "the frame uses variable variables",
		"extractsIntoTheFrame": "the frame calls extract",
	}
	for name, why := range want {
		fn, ok := fns[name]
		if !ok {
			t.Errorf("fixture has no %s", name)
			continue
		}
		found, rejected := lower.FindLoopBuilders(fn)
		for _, b := range found {
			if b.Name == "$out" {
				t.Errorf("%s: promoted $out, but it is %s", name, why)
			}
		}
		var got []string
		for _, r := range rejected {
			if r.Name == "$out" {
				got = append(got, r.Why)
			}
		}
		if len(got) == 0 {
			t.Errorf("%s: $out was neither promoted nor refused", name)
			continue
		}
		if got[0] != why {
			t.Errorf("%s: refused as %q, want %q", name, got[0], why)
		}
	}
}

// And the control: the plain shape must actually be recognised, or the analysis
// above is passing by refusing everything.
func TestLoopBuilderFindsThePlainShape(t *testing.T) {
	fn := lowerFixtureUnpromoted(t, "loopbuilder.php")["promotable"]
	found, rejected := lower.FindLoopBuilders(fn)
	if len(found) != 1 || found[0].Name != "$out" {
		t.Fatalf("found %+v, rejected %+v", found, rejected)
	}
	if found[0].Appends != 1 {
		t.Errorf("appends = %d, want 1", found[0].Appends)
	}
	if found[0].ReadsOutside == 0 {
		t.Errorf("the return reads the string, so it must count as a read outside the loop")
	}
}

// An append outside any loop is not a loop-carried builder. Promoting it would
// buy nothing and the analysis should not claim it.
func TestAppendOutsideALoopIsNotABuilder(t *testing.T) {
	fn := lowerFixture(t, "builder.php")["build"]
	found, _ := lower.FindLoopBuilders(fn)
	if len(found) != 0 {
		t.Errorf("found %+v in a function with no loop", found)
	}
}

var cfgFixtures = []string{
	"calls.php", "write.php", "finally.php", "foreach.php",
	"shortcircuit.php", "namespaced.php", "builder.php", "regions.php",
	"loopbuilder.php", "finallybranch.php", "fiber.php",
}

// The dominator tree is about to be relied on by transforms, so its defining
// properties are checked over every fixture rather than spot-checked: a tree
// that is subtly wrong produces a transform that is subtly wrong.
func TestDominanceProperties(t *testing.T) {
	for _, fixture := range cfgFixtures {
		for name, fn := range lowerFixture(t, fixture) {
			c := lower.Analyze(fn)
			where := fixture + "/" + name
			var reachable []int
			for i := range fn.Blocks {
				if c.Reachable(i) {
					reachable = append(reachable, i)
				}
			}
			if len(reachable) == 0 {
				continue
			}
			entry := reachable[0]
			if entry != 0 {
				t.Errorf("%s: entry is bb%d, expected bb0", where, entry)
			}
			for _, b := range reachable {
				if !c.Dominates(b, b) {
					t.Errorf("%s: bb%d does not dominate itself", where, b)
				}
				if !c.Dominates(entry, b) {
					t.Errorf("%s: entry does not dominate reachable bb%d", where, b)
				}
				// The idom chain must terminate rather than cycle.
				steps := 0
				for at := c.Idom(b); at != -1; at = c.Idom(at) {
					if steps++; steps > len(fn.Blocks) {
						t.Fatalf("%s: idom chain from bb%d does not terminate", where, b)
					}
				}
			}
			for _, a := range reachable {
				for _, b := range reachable {
					// Antisymmetry.
					if a != b && c.Dominates(a, b) && c.Dominates(b, a) {
						t.Errorf("%s: bb%d and bb%d dominate each other", where, a, b)
					}
					if !c.Dominates(a, b) {
						continue
					}
					// Transitivity.
					for _, x := range reachable {
						if c.Dominates(b, x) && !c.Dominates(a, x) {
							t.Errorf("%s: dominance is not transitive at bb%d/bb%d/bb%d",
								where, a, b, x)
						}
					}
				}
			}
		}
	}
}

// Predecessors are derived from successors rather than stored, so the two must
// agree by construction -- but the graph also folds in exception edges that
// Block.Succs does not carry, and that is where a mistake would hide.
func TestPredecessorsAndSuccessorsAgree(t *testing.T) {
	for _, fixture := range cfgFixtures {
		for name, fn := range lowerFixture(t, fixture) {
			c := lower.Analyze(fn)
			for b := range fn.Blocks {
				for _, sN := range c.Succs(b) {
					found := false
					for _, p := range c.Preds(sN) {
						if p == b {
							found = true
						}
					}
					if !found {
						t.Errorf("%s/%s: bb%d -> bb%d has no matching predecessor",
							fixture, name, b, sN)
					}
				}
			}
		}
	}
}

// A catch block is entered from anywhere in the protected range and nothing
// jumps to it, so without exception edges it is unreachable -- and dominance
// over an unreachable block says nothing, which is how a transform would end up
// reasoning about a handler as if it could not run.
func TestCatchBlocksAreReachableThroughExceptionEdges(t *testing.T) {
	fn := lowerFixture(t, "regions.php")["nested"]
	if len(fn.Regions) != 2 {
		t.Fatalf("expected two regions, got %+v", fn.Regions)
	}
	// The inner region is nested in the outer one.
	var inner, outer int = -1, -1
	for i, r := range fn.Regions {
		if r.Parent >= 0 {
			inner, outer = i, r.Parent
		}
	}
	if inner < 0 {
		t.Fatalf("no region records a parent: %+v", fn.Regions)
	}
	if fn.Regions[outer].Parent != -1 {
		t.Errorf("the outer region should have no parent: %+v", fn.Regions)
	}
	c := lower.Analyze(fn)
	for i, r := range fn.Regions {
		if r.Catch < 0 {
			t.Errorf("region r%d has no catch", i)
			continue
		}
		if !c.Reachable(r.Catch) {
			t.Errorf("catch block bb%d of r%d is unreachable, so exception edges are missing",
				r.Catch, i)
		}
		// Nothing may jump to a handler explicitly; it is reached only by the
		// exception edge.
		for _, b := range fn.Blocks {
			for _, sN := range b.Succs {
				if sN == r.Catch {
					t.Errorf("bb%d names catch block bb%d as an explicit successor",
						b.ID, r.Catch)
				}
			}
		}
	}
}

// A foreach is one loop, and its header must dominate the latch that closes it.
func TestNaturalLoopOfAForeach(t *testing.T) {
	fn := lowerFixture(t, "foreach.php")["total"]
	c := lower.Analyze(fn)
	loops := c.Loops()
	if len(loops) != 1 {
		t.Fatalf("found %d loops in one foreach: %+v", len(loops), loops)
	}
	l := loops[0]
	if len(l.Latches) == 0 {
		t.Fatalf("loop has no latch: %+v", l)
	}
	for _, latch := range l.Latches {
		if !c.Dominates(l.Header, latch) {
			t.Errorf("header bb%d does not dominate latch bb%d", l.Header, latch)
		}
	}
	for _, b := range l.Blocks {
		if !c.Dominates(l.Header, b) {
			t.Errorf("header bb%d does not dominate loop block bb%d", l.Header, b)
		}
	}
	// And every block of the loop is in a cycle, which is the weaker notion the
	// builder analysis uses on purpose.
	cyc := c.InCycle()
	for _, b := range l.Blocks {
		if !cyc[b] {
			t.Errorf("bb%d is in the natural loop but not reported as being in a cycle", b)
		}
	}
}

// A function with no back edge has no loop, or the loop finder is inventing them.
func TestStraightLineCodeHasNoLoops(t *testing.T) {
	fn := lowerFixture(t, "builder.php")["build"]
	c := lower.Analyze(fn)
	if loops := c.Loops(); len(loops) != 0 {
		t.Errorf("found loops in straight-line code: %+v", loops)
	}
	if edges := c.BackEdges(); len(edges) != 0 {
		t.Errorf("found back edges in straight-line code: %v", edges)
	}
}

// lowerAllowingBails is lowerFixture's counterpart for fixtures that are meant
// to be refused.
func lowerAllowingBails(t *testing.T, name string) map[string]error {
	t.Helper()
	unit, err := engine.CompileUnit(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	frameless, err := engine.FramelessFunctions()
	if err != nil {
		t.Fatal(err)
	}
	lw := &lower.Lowerer{Names: zend.OpcodeNames(), Frameless: frameless}
	out := map[string]error{}
	for _, oa := range bodies(ir.EncodeUnit(unit)) {
		fn, err := lw.Lower(oa)
		key := oa.GetName()
		if key == "" {
			key = "{main}"
		}
		if err != nil {
			out[key] = err
			continue
		}
		out[key] = nil
		_ = fn
	}
	return out
}

// An exception leaving a protected range has to run the finally and then keep
// unwinding. The normal exits each get an inlined copy; this is the copy for
// the exceptional one, and it ends in `rethrow` rather than falling through --
// which is the entire difference between the two exits.
//
// Before this existed the region pointed at a block marked Dead, so the CFG's
// exception edge led somewhere no pass would look and every catch-and-finally
// analysis was quietly incomplete.
func TestFinallyHasAnUnwindPath(t *testing.T) {
	fn := lowerFixture(t, "finally.php")["guarded"]
	if len(fn.Regions) != 1 {
		t.Fatalf("expected one region, got %+v", fn.Regions)
	}
	target := fn.Regions[0].Finally
	if target < 0 {
		t.Fatal("region records no finally")
	}
	blk := fn.Blocks[target]
	if blk.Dead {
		t.Errorf("the region's finally points at a dead block bb%d", target)
	}
	if blk.Term != "rethrow" {
		t.Errorf("unwind block bb%d ends in %q, want rethrow", target, blk.Term)
	}
	if !strings.Contains(fn.Blocks[target].String(), `"echo"("done`) {
		t.Errorf("unwind block does not run the finally body: %+v", blk.Insts)
	}
	c := lower.Analyze(fn)
	if !c.Reachable(target) {
		t.Errorf("unwind block bb%d is unreachable", target)
	}
}

// A finally body with its own control flow. The body is duplicated as real
// blocks, once per exit, with every jump that stays inside it remapped to that
// copy -- which is what "inlined" was always supposed to mean. An earlier
// version spliced the body's *oplines* into the block that reached it, which
// cannot represent a branch: the terminator was set part way and instructions
// kept being appended after it, so an `if` inside a `finally` ran **both arms
// unconditionally**.
func TestBranchingFinallyKeepsItsBranch(t *testing.T) {
	fns := lowerFixture(t, "finallybranch.php")
	fn, ok := fns["branchingFinally"]
	if !ok {
		t.Fatalf("fixture has no branchingFinally: %v", fns)
	}
	// The direct statement of the bug: the two arms must never share a block.
	for _, b := range fn.Blocks {
		text := b.String()
		if strings.Contains(text, `echo "a"`) && strings.Contains(text, `echo "b"`) {
			t.Errorf("bb%d runs both arms of the if:\n%s", b.ID, text)
		}
	}
	// One copy per exit, each with its own branch.
	branches := 0
	for _, b := range fn.Blocks {
		// The condition is the value read from `$x`, so the terminator names an
		// SSA value rather than the variable.
		if regexp.MustCompile(`^br %\d+,`).MatchString(b.Term) {
			branches++
		}
	}
	if branches < 2 {
		t.Errorf("expected a copy of the branch per exit, found %d", branches)
	}
	// Copies must not share blocks: every `echo "a"` is its own.
	arms := 0
	for _, b := range fn.Blocks {
		if strings.Contains(b.String(), `"echo"("a")`) {
			arms++
		}
	}
	if arms != branches {
		t.Errorf("%d branches but %d copies of an arm; copies are sharing blocks", branches, arms)
	}
	// Exactly one copy is the unwinding one.
	rethrows := 0
	for _, b := range fn.Blocks {
		if b.Term == "rethrow" {
			rethrows++
		}
	}
	if rethrows != 1 {
		t.Errorf("expected exactly one unwinding copy, found %d", rethrows)
	}
}

// A `yield` inside a finally is duplicated with the rest of the body, and each
// copy resumes into its *own* next block. That is the case duplication was
// least obviously able to handle: a suspension point is fine to copy because
// each copy is a distinct program point, exactly as it is in the engine after
// its own inlining.
func TestYieldInsideAFinallyResumesIntoItsOwnCopy(t *testing.T) {
	fn := lowerFixture(t, "finallybranch.php")["yieldingFinally"]
	var yields []*lower.Block
	for _, b := range fn.Blocks {
		if strings.HasPrefix(b.Term, "yield ") {
			yields = append(yields, b)
		}
	}
	if len(yields) < 2 {
		t.Fatalf("expected the finally's yield to be copied, found %d yields", len(yields))
	}
	seen := map[int]bool{}
	for _, b := range yields {
		if len(b.Succs) != 1 {
			t.Errorf("bb%d: yield has %d successors, want 1", b.ID, len(b.Succs))
			continue
		}
		if seen[b.Succs[0]] {
			t.Errorf("two yields resume into the same block bb%d, so the copies share a resume point",
				b.Succs[0])
		}
		seen[b.Succs[0]] = true
	}
}

// Suspension is made explicit in the IR rather than left as the static call the
// engine compiles it to. If it stayed a call, the only way to implement it is a
// suspension-aware runtime -- real coroutines underneath -- whereas an explicit
// suspension point lets the compiler own the mechanism, which is the same
// treatment `yield` already gets and what a later CPS rewrite needs.
func TestFiberSuspendIsAnIntrinsicTerminator(t *testing.T) {
	fn := lowerFixture(t, "fiber.php")["App\\inside"]
	got := fn.String()
	if strings.Contains(got, `call.static "Fiber"`) {
		t.Errorf("suspension was left as an opaque call:\n%s", got)
	}
	var suspends []*lower.Block
	for _, b := range fn.Blocks {
		if strings.HasPrefix(b.Term, "fiber.suspend") {
			suspends = append(suspends, b)
		}
	}
	// Both spellings -- the `use`d one and the fully qualified one -- resolve to
	// the same literal and must both be recognised.
	if len(suspends) != 2 {
		t.Fatalf("expected two suspension points, found %d:\n%s", len(suspends), got)
	}
	seen := map[int]bool{}
	for _, b := range suspends {
		if len(b.Succs) != 1 {
			t.Errorf("bb%d: suspension has %d successors, want 1", b.ID, len(b.Succs))
			continue
		}
		// The resumed value arrives on the edge, so the continuation is its own
		// block and takes it as a parameter -- exactly as a yield does.
		cont := fn.Blocks[b.Succs[0]]
		if len(cont.Params) != 1 {
			t.Errorf("bb%d resumes into bb%d, which takes %d parameters, want 1",
				b.ID, cont.ID, len(cont.Params))
		}
		if seen[cont.ID] {
			t.Errorf("two suspensions resume into bb%d", cont.ID)
		}
		seen[cont.ID] = true
	}
	if !strings.Contains(lowerFixture(t, "fiber.php")["App\\driver"].String(), "fiber.current()") {
		t.Errorf("Fiber::getCurrent was not recognised")
	}
}

// The driver side is method calls on a value whose class the lowering cannot
// know, so they stay calls. Pinned so the gap is visible rather than assumed:
// making these intrinsics too needs the receiver's type from Phase D.
func TestFiberDriverMethodsStayCallsUntilTypesExist(t *testing.T) {
	got := lowerFixture(t, "fiber.php")["App\\driver"].String()
	// The receiver is the value read from `$f`, so it is an SSA name; the
	// method names are what this is about.
	for _, want := range []*regexp.Regexp{
		regexp.MustCompile(`call\.method %\d+, "start"`),
		regexp.MustCompile(`call\.method %\d+, "getReturn"`),
		regexp.MustCompile(`new "Fiber"`),
	} {
		if !want.MatchString(got) {
			t.Errorf("missing %s in:\n%s", want.String(), got)
		}
	}
}

// A suspension inside a finally is duplicated with the rest of the body, so
// each copy is its own suspension point. That is the property a CPS rewrite
// will rely on, and it is why copying a finally containing one is sound.
func TestFiberSuspendInAFinallyIsCopiedPerExit(t *testing.T) {
	fn := lowerFixture(t, "finallybranch.php")["suspendingFinally"]
	suspends := 0
	for _, b := range fn.Blocks {
		if strings.HasPrefix(b.Term, "fiber.suspend") {
			suspends++
		}
	}
	if suspends < 2 {
		t.Errorf("expected one suspension per copy of the finally, found %d:\n%s", suspends, fn)
	}
	rethrows := 0
	for _, b := range fn.Blocks {
		if b.Term == "rethrow" {
			rethrows++
		}
	}
	if rethrows != 1 {
		t.Errorf("expected one unwinding copy, found %d", rethrows)
	}
}

// A finally body's own blocks are dead: they exist only as the copies made at
// each exit. So nothing live may jump to one — which is exactly what fails if a
// copy's internal jumps are not remapped to that copy, and it is invisible to
// any test that only counts instructions, because the copy still *contains* the
// right code while branching into blocks that are never emitted.
func TestNoLiveBlockJumpsIntoADeadOne(t *testing.T) {
	for _, fixture := range cfgFixtures {
		for name, fn := range lowerFixture(t, fixture) {
			for _, b := range fn.Blocks {
				if b.Dead {
					continue
				}
				for _, sN := range b.Succs {
					if sN < 0 || sN >= len(fn.Blocks) {
						t.Errorf("%s/%s: bb%d names bb%d, which does not exist",
							fixture, name, b.ID, sN)
						continue
					}
					if fn.Blocks[sN].Dead {
						t.Errorf("%s/%s: live bb%d jumps into dead bb%d",
							fixture, name, b.ID, sN)
					}
				}
			}
			// And every block's ID must equal its position, which the copies
			// rely on to allocate contiguously.
			for i, b := range fn.Blocks {
				if b.ID != i {
					t.Errorf("%s/%s: block at index %d has ID %d", fixture, name, i, b.ID)
				}
			}
		}
	}
}

// A generator or fiber body is entered many times and continues where it left
// off. Implemented directly that needs a real stack per instance -- a goroutine
// -- which is what makes a runtime suspension-aware. Reifying the continuation
// instead turns the body into a state machine: suspension becomes a return that
// records where to resume, and resuming becomes an ordinary call.
func TestResumableRewritesSuspensionsIntoAStateMachine(t *testing.T) {
	fn := lowerFixture(t, "resumable.php")["counter"]
	before := 0
	for _, b := range fn.Blocks {
		if strings.HasPrefix(b.Term, "yield") || strings.HasPrefix(b.Term, "generator_create") {
			before++
		}
	}
	if before == 0 {
		t.Fatal("fixture has no suspension to rewrite")
	}
	if err := lower.Resumable(fn, nil); err != nil {
		t.Fatal(err)
	}
	// Nothing suspends any more.
	for _, b := range fn.Blocks {
		if strings.HasPrefix(b.Term, "yield") || strings.HasPrefix(b.Term, "fiber.suspend") ||
			strings.HasPrefix(b.Term, "generator_create") {
			t.Errorf("bb%d still suspends: %q", b.ID, b.Term)
		}
	}
	// Every suspension became a recorded state and a return, and control comes
	// back through the dispatch rather than along an edge.
	got := fn.String()
	// One per suspension, plus one per `ret` for the terminal state: a finished
	// body records that it is finished the same way it records where to resume.
	rets := 0
	for _, b := range fn.Blocks {
		if !b.Dead && strings.HasPrefix(b.Term, "ret ") {
			rets++
		}
	}
	if n := strings.Count(got, "frame.set_state"); n != before+rets-1 {
		t.Errorf("%d suspensions and %d returns became %d state records, want %d",
			before, rets, n, before+rets-1)
	}
	for _, b := range fn.Blocks {
		if strings.HasPrefix(b.Term, "ret.") && len(b.Succs) != 0 {
			t.Errorf("bb%d returns but still has successors %v", b.ID, b.Succs)
		}
	}
	if fn.Entry == 0 {
		t.Error("the entry did not move to the dispatch")
	}
	if len(fn.States) != before+2 {
		t.Errorf("states = %v, want the entry, one per suspension, and the terminal state",
			fn.States)
	}
	if fn.Terminal != len(fn.States)-1 {
		t.Errorf("Terminal = %d, want the last state (%d)", fn.Terminal, len(fn.States)-1)
	}
	// The dispatch reaches exactly the states, and the states are real blocks.
	entry := fn.Blocks[fn.Entry]
	if len(entry.Succs) != len(fn.States) {
		t.Errorf("dispatch reaches %v, states are %v", entry.Succs, fn.States)
	}
	for _, sN := range fn.States {
		if sN < 0 || sN >= len(fn.Blocks) || fn.Blocks[sN].Dead {
			t.Errorf("state block bb%d is not a live block", sN)
		}
	}
	// A resumed value arrived on an edge; it now comes out of the frame, and no
	// continuation still takes it as a parameter.
	if !strings.Contains(got, "frame.resumed") {
		t.Errorf("the resumed value is not read from the frame:\n%s", got)
	}
	for _, sN := range fn.States[1:] {
		if len(fn.Blocks[sN].Params) != 0 {
			t.Errorf("continuation bb%d still takes a block parameter", sN)
		}
	}
}

// The rewrite must not touch a function that does not suspend, or every
// ordinary body would pay for the machinery.
func TestResumableLeavesOrdinaryFunctionsAlone(t *testing.T) {
	fn := lowerFixture(t, "resumable.php")["plain"]
	before := fn.String()
	if err := lower.Resumable(fn, nil); err != nil {
		t.Fatal(err)
	}
	if fn.String() != before {
		t.Errorf("a non-suspending function was rewritten")
	}
	if fn.Entry != 0 || len(fn.States) != 0 {
		t.Errorf("entry = %d, states = %v; both should be untouched", fn.Entry, fn.States)
	}
}

// The transform is only tractable because nothing live at a suspension is an
// SSA value -- everything crossing a block is already a place. If that stopped
// being true the frame would need a liveness computation, so it is checked here
// on the rewritten function as well as over the corpora.
func TestResumableKeepsValuesInsideTheirBlocks(t *testing.T) {
	fn := lowerFixture(t, "resumable.php")["counter"]
	if err := lower.Resumable(fn, nil); err != nil {
		t.Fatal(err)
	}
	defined := map[string]int{}
	for _, b := range fn.Blocks {
		for _, in := range b.Insts {
			if in.Result != nil {
				defined[in.Result.String()] = b.ID
			}
		}
	}
	quoted := regexp.MustCompile(`"(\\.|[^"\\])*"`)
	value := regexp.MustCompile(`%\d+`)
	for _, b := range fn.Blocks {
		var texts []string
		for _, in := range b.Insts {
			texts = append(texts, strings.Join(in.Operands, " "), in.ArgList)
		}
		texts = append(texts, b.Term)
		for _, text := range texts {
			for _, m := range value.FindAllString(quoted.ReplaceAllString(text, `""`), -1) {
				if at, ok := defined[m]; ok && at != b.ID {
					t.Errorf("%s defined in bb%d is used in bb%d", m, at, b.ID)
				}
			}
		}
	}
}

// A resumable function is entered many times, so what outlives a suspension
// cannot be a Go local. That set is exactly the places -- established, not
// assumed: no SSA value is used outside its defining block, and a suspension is
// a terminator. The frame declares it so the emitter does not re-derive it.
func TestFrameHoldsEveryPlace(t *testing.T) {
	fn := lowerFixture(t, "resumable.php")["counter"]
	if len(fn.Frame) != 0 {
		t.Fatal("a function that has not been rewritten should have no frame")
	}
	if err := lower.Resumable(fn, nil); err != nil {
		t.Fatal(err)
	}
	byName := map[string]lower.FrameSlot{}
	for _, sl := range fn.Frame {
		byName[sl.Name] = sl
	}
	// Every variable the source names, and the compiler's own temporaries.
	for _, want := range []string{"$rows", "$seen", "$r", "$reply"} {
		sl, ok := byName[want]
		if !ok {
			t.Errorf("%s is not in the frame: %+v", want, fn.Frame)
			continue
		}
		if sl.Compiler {
			t.Errorf("%s is a program variable, not a compiler slot", want)
		}
	}
	var iters, compiler int
	for _, sl := range fn.Frame {
		if strings.HasPrefix(sl.Name, "$__iter") {
			iters++
			// An iterator **is** a zval now: the cursor lives in an internal
			// object, so the handle is an ordinary value. It used to be a
			// `*Iter` -- a Go type that is not a php.Value -- which cost a
			// representation of its own, a slot that only it could use, and an
			// argument shape that passed it through unconverted.
			if sl.Type != "zval" {
				t.Errorf("%s declared as %s, want zval", sl.Name, sl.Type)
			}
		}
		if sl.Compiler != strings.HasPrefix(sl.Name, "$__") {
			t.Errorf("%s: Compiler = %v", sl.Name, sl.Compiler)
		}
		if sl.Compiler {
			compiler++
		}
	}
	if iters == 0 {
		t.Error("the foreach iterator is not in the frame, so it would not survive a suspension")
	}
	if compiler == 0 {
		t.Error("no compiler slot in the frame; cross-block temporaries have to persist too")
	}
	// Every place the body mentions is declared.
	place := regexp.MustCompile(`\$[A-Za-z_][A-Za-z0-9_]*`)
	quoted := regexp.MustCompile(`"(\\.|[^"\\])*"`)
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		for _, in := range b.Insts {
			for _, m := range place.FindAllString(
				quoted.ReplaceAllString(strings.Join(in.Operands, " ")+" "+in.ArgList, `""`), -1) {
				if _, ok := byName[m]; !ok {
					t.Errorf("%s is used but not in the frame", m)
				}
			}
		}
	}
}

// A slot's type is the join of everything written to it. Taking the first write
// would declare `$x` an object in `$x = new C; yield; $x = 5;`.
func TestFrameTypeIsTheJoinOfEveryWrite(t *testing.T) {
	fn := lowerFixture(t, "resumable.php")["retyped"]
	if err := lower.Resumable(fn, nil); err != nil {
		t.Fatal(err)
	}
	for _, sl := range fn.Frame {
		if sl.Name == "$x" && sl.Type != "zval" {
			t.Errorf("$x declared as %s; it holds an object and an int, so it is a zval", sl.Type)
		}
	}
}

// A function that does not suspend has no frame: its locals are ordinary locals.
func TestOrdinaryFunctionsHaveNoFrame(t *testing.T) {
	fn := lowerFixture(t, "resumable.php")["plain"]
	if err := lower.Resumable(fn, nil); err != nil {
		t.Fatal(err)
	}
	if len(fn.Frame) != 0 {
		t.Errorf("a non-suspending function was given a frame: %+v", fn.Frame)
	}
}

// FE_FETCH is the loop *header* and tests before it fetches: exhausted means
// jump to the exit, otherwise take the current element, advance, and fall into
// the body. Fetching first and testing afterwards asks whether there is a
// *further* element before running the body for this one, so `[1,2,3]` runs the
// body twice and `[5]` not at all.
//
// That bug shipped, was in the committed golden, and moved no measured number,
// because coverage counts bodies lowered rather than bodies lowered correctly.
// The structural signature is exact and is what this asserts: **the block that
// tests the iterator must not touch it**.
func TestForeachTestsBeforeItFetches(t *testing.T) {
	for _, fixture := range []string{"foreach.php", "resumable.php", "loopbuilder.php"} {
		for name, fn := range lowerFixture(t, fixture) {
			for _, b := range fn.Blocks {
				if !strings.HasPrefix(b.Term, "br iter.valid") {
					continue
				}
				body := b.String()
				for _, forbidden := range []string{"iter.current", "iter.key", "iter.next"} {
					if strings.Contains(body, forbidden) {
						t.Errorf("%s/%s bb%d tests the iterator after %s, which drops the last iteration:\n%s",
							fixture, name, b.ID, forbidden, body)
					}
				}
				// The taken edge must be where the fetch happens.
				if len(b.Succs) != 2 {
					t.Errorf("%s/%s bb%d: %q has %d successors", fixture, name, b.ID, b.Term, len(b.Succs))
					continue
				}
				taken := fn.Blocks[b.Succs[0]].String()
				if !strings.Contains(taken, "iter.current") {
					continue // the FE_RESET test, whose taken edge is the header
				}
				if !strings.Contains(taken, "iter.next") {
					t.Errorf("%s/%s bb%d fetches without advancing:\n%s",
						fixture, name, b.Succs[0], taken)
				}
			}
		}
	}
}

// Generator::throw() and Fiber::throw() resume by *raising* at the suspension
// point, so the read of the resumed value is where a thrown-in exception
// surfaces and where the function's own `try` around the yield can catch it.
// Emitting it only where a block parameter existed loses the exception for
// `yield $x;` written as a statement -- silently, since nothing else marks the
// resumption.
func TestEveryContinuationReadsTheResumedValue(t *testing.T) {
	fn := lowerFixture(t, "resumable.php")["counter"]
	if err := lower.Resumable(fn, nil); err != nil {
		t.Fatal(err)
	}
	if len(fn.States) < 2 {
		t.Fatalf("expected continuations, got states %v", fn.States)
	}
	// States[1:Terminal] — the terminal state is a dispatch target but not a
	// continuation, since nothing resumes a finished generator.
	for _, sN := range fn.States[1:fn.Terminal] {
		blk := fn.Blocks[sN]
		if len(blk.Insts) == 0 || blk.Insts[0].Opcode != "frame.resumed" {
			t.Errorf("continuation bb%d does not begin by reading the resumed value: %+v",
				sN, blk.Insts)
		}
	}
	// Including the one whose value the source discards -- `yield $seen;`.
	if n := strings.Count(fn.String(), "frame.resumed"); n != fn.Terminal-1 {
		t.Errorf("%d continuations but %d reads", fn.Terminal-1, n)
	}
}

// A suspending function is emitted as CPS unconditionally, because that form is
// the common prefix of every option for fibers — uniform, dual entry, selective,
// or keeping goroutines. Building it commits to none of them.
//
// What is *not* decided here is the caller. A generator's suspension cannot
// reach a PHP caller: calling one returns a Generator object and the suspension
// happens inside the driver. `Fiber::suspend()` has no mediator and propagates
// to whoever called the function, so those bodies carry `.suspends_to_caller` —
// an open question recorded in the IR rather than a refusal to lower.
func TestFiberBodyIsRewrittenAndMarksTheCallerQuestion(t *testing.T) {
	fn := lowerFixture(t, "fiber.php")["App\\inside"]
	if err := lower.Resumable(fn, nil); err != nil {
		t.Fatalf("a fiber body was refused rather than rewritten: %v", err)
	}
	if !fn.SuspendsToCaller {
		t.Error("a fiber suspension propagates to the caller and must say so")
	}
	if len(fn.States) < 2 || fn.Entry == 0 {
		t.Errorf("not actually rewritten: states=%v entry=%d", fn.States, fn.Entry)
	}
	for _, b := range fn.Blocks {
		if strings.HasPrefix(b.Term, "fiber.suspend") {
			t.Errorf("bb%d still suspends: %q", b.ID, b.Term)
		}
	}
	if !strings.Contains(fn.String(), ".suspends_to_caller") {
		t.Error("the marker is not in the rendered IR, so an emitter cannot see it")
	}
}

// A generator must not carry the marker: its suspension is mediated, which is
// why it needs no decision about callers and works end to end today.
func TestGeneratorDoesNotSuspendToItsCaller(t *testing.T) {
	fn := lowerFixture(t, "resumable.php")["counter"]
	if err := lower.Resumable(fn, nil); err != nil {
		t.Fatal(err)
	}
	if fn.SuspendsToCaller {
		t.Error("a generator's suspension is mediated by the driver and does not reach its caller")
	}
	if strings.Contains(fn.String(), ".suspends_to_caller") {
		t.Error("the marker leaked into a generator")
	}
}

// A generator, by contrast, is rewritten.
func TestGeneratorIsStillRewritten(t *testing.T) {
	fn := lowerFixture(t, "resumable.php")["counter"]
	if err := lower.Resumable(fn, nil); err != nil {
		t.Fatalf("a generator was refused: %v", err)
	}
}

// Every continuation reads the resumed value, including the ones that discard
// it, because that read is where a thrown-in exception surfaces. A continuation
// can also be a join point, and then control can arrive without a resumption
// having happened -- so reading a *stale* value there would be wrong.
//
// Two things make it safe. The driver reports a pending throw once and clears
// it, so the *effect* cannot fire twice however many paths reach the read. And
// the *value* is only read where it cannot be stale.
//
// An earlier version of this comment claimed the second followed structurally --
// a suspension terminates its block, so a continuation is a fresh leader and any
// join lands in a later block. **That is false.** A fresh leader can still have
// other in-edges: in `counter`, bb6 is state 3's dispatch target *and* the false
// arm of `br $reply, bb5, bb6`, so joins do land on continuations. It is a
// counterexample to the reason, not to the assertion -- bb6 does not use its
// resumed value.
//
// So the property is the narrow one below and it is **checked, not proven**:
// verified over 335 rewritten bodies in both corpora, and guarded permanently by
// `php2lower -cps` rather than argued from the block structure.
//
// The runtime session raised this and checked four hand-written shapes; this
// asserts the condition itself -- used implies exactly one predecessor -- and
// `php2lower -cps` asks it of every rewritten body in a corpus.
func TestAContinuationThatUsesItsResumedValueIsOnlyReachedByResuming(t *testing.T) {
	for _, name := range []string{"counter", "joined", "retyped"} {
		fn := lowerFixture(t, "resumable.php")[name]
		if err := lower.Resumable(fn, nil); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		c := lower.Analyze(fn)
		value := regexp.MustCompile(`%\d+`)
		for _, sN := range fn.States[1:fn.Terminal] {
			blk := fn.Blocks[sN]
			if len(blk.Insts) == 0 || blk.Insts[0].Opcode != "frame.resumed" {
				t.Errorf("%s: continuation bb%d does not read the resumed value", name, sN)
				continue
			}
			read := blk.Insts[0].Result.String()
			used := false
			for _, in := range blk.Insts[1:] {
				for _, m := range value.FindAllString(strings.Join(in.Operands, " "), -1) {
					if m == read {
						used = true
					}
				}
			}
			if used && len(c.Preds(sN)) > 1 {
				t.Errorf("%s: bb%d uses its resumed value but is also reachable without resuming, from %v",
					name, sN, c.Preds(sN))
			}
		}
	}
}

// The call graph is what sizes "how far does a suspension spread". Its two
// approximations have to stay distinguishable, because the whole point of the
// measurement is which of them costs more: a call site naming a callee, versus
// one naming only a method whose receiver class is unknown.
func TestCallGraphSeparatesItsApproximations(t *testing.T) {
	fns := lowerFixture(t, "calls.php")
	g := lower.NewCallGraph()
	g.Add("helper", fns["helper"])
	g.Add("Greeter::make", fns["make"])
	g.Add("Greeter::decorate", fns["decorate"])
	g.Link()

	st := g.Stats()
	if st.Functions != 3 {
		t.Fatalf("functions = %d, want 3", st.Functions)
	}
	// make() calls helper() by name and $this->decorate() by method.
	if st.Edges < 2 {
		t.Errorf("edges = %d, want at least the named call and the method call", st.Edges)
	}
	if st.MethodEdges == 0 {
		t.Error("the method call was not recorded as a method edge")
	}
	if st.MethodEdges == st.Edges {
		t.Error("the call to helper() by name was not recorded as a direct edge")
	}
}

// A namespaced callee arrives from the IR with its backslashes doubled, because
// literals are rendered with %q. Failing to undo that makes every namespaced
// name miss its own definition — which in a fully namespaced corpus is all of
// them, and showed up as "100% of edges are method edges".
func TestCallGraphResolvesNamespacedNames(t *testing.T) {
	fns := lowerFixture(t, "namespaced.php")
	g := lower.NewCallGraph()
	g.Add("App\\Support\\render", fns["App\\Support\\render"])
	// The callee the fixture names, defined here so the edge has somewhere to go.
	g.Add("App\\Support\\implode", fns["App\\Support\\render"])
	g.Link()
	if st := g.Stats(); st.Edges-st.MethodEdges == 0 {
		t.Errorf("no direct edge resolved: a namespaced name did not match its definition")
	}
}

// Receiver inference exists to narrow `call.method`, which the call graph
// measured as the single largest source of imprecision in a real application.
// It reads declarations only: $this, declared parameter types, `new "C"`, and
// declared property and return types. No dataflow.
func TestReceiverInferenceUsesDeclarationsOnly(t *testing.T) {
	fns := lowerFixture(t, "calls.php")
	w := lower.NewWorld()
	w.Returns["greeter::decorate"] = "string"
	w.Close()

	fn := fns["make"]
	resolved, total := lower.InferReceivers(fn, "Greeter", w)
	if total == 0 {
		t.Fatal("fixture has no method call to infer")
	}
	// `$this->decorate(...)` — the enclosing class, which needs no analysis.
	if resolved != total {
		t.Errorf("resolved %d of %d; $this alone should account for all of them", resolved, total)
	}
	if !strings.Contains(fn.String(), `recv="Greeter"`) {
		t.Errorf("the annotation is not in the rendered IR:\n%s", fn)
	}
}

// A scalar or union declaration names no class, and inventing one would narrow a
// call to something the value cannot be.
func TestReceiverInferenceIgnoresNonClassDeclarations(t *testing.T) {
	fns := lowerFixture(t, "foreach.php")
	w := lower.NewWorld()
	w.Close()
	fn := fns["total"]
	// $rows is declared `array`, which is not a class.
	lower.InferReceivers(fn, "", w)
	if strings.Contains(fn.String(), "recv=") {
		t.Errorf("a non-class declaration produced a receiver annotation:\n%s", fn)
	}
}

// Narrowing to the declared class alone would be unsound, since a subclass may
// override. The hierarchy is what makes it safe.
func TestDescendantsCoverOverrides(t *testing.T) {
	w := lower.NewWorld()
	w.Parent["b"] = "a"
	w.Parent["c"] = "b"
	w.Parent["d"] = "other"
	w.Close()
	got := map[string]bool{}
	for _, c := range w.Descendants("A") {
		got[c] = true
	}
	for _, want := range []string{"a", "b", "c"} {
		if !got[want] {
			t.Errorf("%s missing from the descendants of A: %v", want, got)
		}
	}
	if got["d"] {
		t.Errorf("d does not extend A but was included")
	}
}

// A finished body must stay finished.
//
// `frame.set_state` records where to *resume*, so before the terminal state
// existed the last thing a body recorded was its final continuation. Stepping a
// returned generator again re-entered the loop: the runtime session ran the
// first generated frame and got `counter(3)` returning 3, then 4.
//
// Both drivers there guard — Generator on `finished`, Fiber on
// `FiberTerminated` — so nothing reaches it. That is the problem: it made
// correctness a property of every driver rather than of the emitted code, and
// the third driver gets it wrong silently. The fix belongs on this side.
//
// Found by the runtime session pushing on a generated artifact rather than
// reviewing the IR, which is the same way Go's unused-label rule found the dead
// block. Neither is expressible as an IR invariant, which is why neither
// `php2lower -verify` nor any test here caught them.
func TestAFinishedBodyStaysFinished(t *testing.T) {
	for _, name := range []string{"counter", "joined", "retyped"} {
		fn := lowerFixture(t, "resumable.php")[name]
		if err := lower.Resumable(fn, nil); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if fn.Terminal == 0 || fn.Terminal != len(fn.States)-1 {
			t.Fatalf("%s: Terminal = %d of %d states", name, fn.Terminal, len(fn.States))
		}
		term := fn.Blocks[fn.States[fn.Terminal]]

		// Re-entering the terminal state must produce a value and nothing else:
		// no resumed read, no state change, no further control flow.
		if len(term.Insts) != 0 {
			t.Errorf("%s: terminal state bb%d does more than return: %+v",
				name, term.ID, term.Insts)
		}
		if term.Term != "ret $__ret" {
			t.Errorf("%s: terminal state bb%d returns %q, want the recorded value",
				name, term.ID, term.Term)
		}
		if len(term.Succs) != 0 {
			t.Errorf("%s: terminal state bb%d has successors %v", name, term.ID, term.Succs)
		}

		// Every ordinary return records the terminal state on the way out, or
		// the state left behind is still a continuation and the next step
		// resumes the loop.
		for _, b := range fn.Blocks {
			if b.Dead || !strings.HasPrefix(b.Term, "ret ") || b.ID == term.ID {
				continue
			}
			if n := len(b.Insts); n < 2 ||
				b.Insts[n-1].Opcode != "frame.set_state" ||
				b.Insts[n-1].Operands[0] != strconv.Itoa(fn.Terminal) {
				t.Errorf("%s: bb%d returns without recording the terminal state: %+v",
					name, b.ID, b.Insts)
			}
		}

		// The recorded value has to survive the return, so it is a frame slot —
		// a local would be gone by the time the terminal state answered.
		found := false
		for _, sl := range fn.Frame {
			if sl.Name == "$__ret" {
				found = true
				if !sl.Compiler {
					t.Errorf("%s: $__ret is not marked a compiler slot", name)
				}
			}
		}
		if !found {
			t.Errorf("%s: $__ret is not in the frame: %+v", name, fn.Frame)
		}
	}
}

// A boolean literal must lower to its value, not to its type.
//
// Both `true` and `false` rendered as the operand `bool`, which made
// `$t = true` and `$f = false` the same instruction — indistinguishable to
// every consumer of the IR. The harvest never lost it (`IS_TRUE` arrives with
// Int 1); the operand renderer did.
//
// Found by writing an emitter: the gap is invisible while the IR is only read,
// because "bool" looks like a type annotation rather than a discarded value.
// Nothing in `php2lower -verify` could have caught it — the IR was structurally
// perfect and semantically lossy.
func TestABooleanLiteralKeepsItsValue(t *testing.T) {
	// Lowered without promotion: this is a **lowering** property, and mem2reg
	// legitimately removes the place it is about -- a value with one definition
	// and no aliasing becomes an SSA value, which is the whole point. The
	// default pipeline's shape is recorded by the goldens.
	fn := lowerFixtureUnpromoted(t, "bools.php")["pick"]
	got := fn.String()
	if !strings.Contains(got, `"write_var"($t, true)`) {
		t.Errorf("true did not survive lowering:\n%s", got)
	}
	if !strings.Contains(got, `"write_var"($f, false)`) {
		t.Errorf("false did not survive lowering:\n%s", got)
	}
	if strings.Contains(got, ", bool\n") {
		t.Errorf("a bool literal still renders as its type:\n%s", got)
	}
}

// A by-reference parameter must survive lowering.
//
// `.params` recorded only index, name and type, so `function f($v)` and
// `function f(&$v)` were identical in the IR — and a consumer could not tell a
// by-value call from one that binds the caller's place. The emitter is what
// found it: it compiled `function f(&$v) { $v = 5; }` as a by-value function,
// silently dropping the write-back, and `errmsg_022.phpt` disagreed with the
// engine as a result.
//
// The harvest always carried it (`ArgInfo.by_ref`). Same shape as the
// `true`/`false` defect: information the engine gave us, discarded on the way
// into the IR, invisible until a second consumer needed it.
func TestAByReferenceParameterSurvivesLowering(t *testing.T) {
	fns := lowerFixture(t, "byref.php")
	byval := fns["takesValue"]
	byref := fns["takesRef"]
	if byval == nil || byref == nil {
		t.Fatalf("fixture is missing a function: %v", fns)
	}
	if len(byref.Params) != 1 || !lower.ParamIsByRef(byref.Params[0]) {
		t.Errorf("by-reference parameter did not survive: %q", byref.Params)
	}
	if len(byval.Params) != 1 || lower.ParamIsByRef(byval.Params[0]) {
		t.Errorf("a by-value parameter is reported as by-reference: %q", byval.Params)
	}
	// The point of the fix: the two must not be the same text.
	if byval.Params[0] == byref.Params[0] {
		t.Errorf("f($v) and f(&$v) lower identically: %q", byval.Params[0])
	}
}

// A return expression is evaluated at its own line, before the finally body
// runs — and the source positions must say so.
//
// Settled against the engine, not reasoned about. For the fixture below the
// engine prints:
//
//	Warning: Undefined variable $undef_in_return in %s on line 4
//	finally ran
//
// so the read is attributed to line 4 (the return) and the finally body to its
// own line, in that order. An earlier lowering had it backwards: the finally's
// line was attached before the return's.
//
// This exists because the golden IR alone would not have caught it. A golden
// regenerated with `-update` records what the code does, not what PHP does —
// the same failure the project's rule about hand-written expected output warns
// against, one level up. The `finally.ir` golden was in fact silently wrong on
// this point and was corrected by a regeneration nobody had reviewed; it
// happened to be right, which is not a method.
//
// It became load-bearing when the emitter started rendering `.line` as
// `ctx.SetLocation` and diagnostics began to be graded byte-exact.
func TestAReturnExpressionIsAttributedBeforeTheFinallyBody(t *testing.T) {
	fn := lowerFixture(t, "finallyline.php")["f"]
	if fn == nil {
		t.Fatal("fixture has no f")
	}

	// Walk blocks in order, tracking the position each instruction sits at.
	lineOf := map[string]int{}
	// The position persists until something changes it, across block
	// boundaries: `.line 4` is set in the block that branches and the block it
	// branches to inherits it. That is the runtime semantics -- the emitter
	// renders `.line` as a `ctx.SetLocation` call and the location simply stays
	// set -- so a reader that resets per block reports no position at all.
	cur := 0
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		for _, in := range b.Insts {
			if in.Directive() {
				cur, _ = strconv.Atoi(in.Operands[0])
				continue
			}
			// in.String(), not a hand-rolled spelling: a test that renders
			// instructions its own way stops matching the moment the IR gains
			// a field, and does so silently.
			key := in.String()
			if _, seen := lineOf[key]; !seen {
				lineOf[key] = cur
			}
		}
	}

	read, echo := 0, 0
	for key, line := range lineOf {
		// The read of the variable is now its own instruction, and it is the
		// read whose position the engine attributes -- which is the thing under
		// test, so this follows it to the read rather than to the copy.
		if strings.Contains(key, `"read_var"($undef_in_return`) {
			read = line
		}
		if strings.Contains(key, `"echo"("finally ran`) {
			echo = line
		}
	}
	if read == 0 || echo == 0 {
		t.Fatalf("fixture did not lower as expected: read=%d echo=%d\n%s", read, echo, fn)
	}
	// The engine's answer: the return's line, then the finally's.
	if read != 4 {
		t.Errorf("the return expression is attributed to line %d, want 4 (the return)", read)
	}
	if echo != 6 {
		t.Errorf("the finally body is attributed to line %d, want 6 (its own line)", echo)
	}
	if read >= echo {
		t.Errorf("the finally body (line %d) is not after the return expression (line %d)", echo, read)
	}
}

// A constant array literal renders with its entries, and parses back to them.
//
// It used to render as `<array>`, discarding a value the harvest had kept --
// the same loss as rendering both booleans as `bool`, and it made every
// array-shaped body unrenderable for *any* consumer rather than only for the
// emitter.
//
// The renderer and the parser are asserted together because they are one
// contract. A scanner and a renderer disagreeing about what the text says has
// already been a defect here twice: `${"10"}` was spelled by one and not
// recognised by the other, and `<array>` is the same shape with the loss on the
// other side.
func TestArrayLiteralRoundTrips(t *testing.T) {
	// Lowered without promotion: this is a **lowering** property, and mem2reg
	// legitimately removes the place it is about -- a value with one definition
	// and no aliasing becomes an SSA value, which is the whole point. The
	// default pipeline's shape is recorded by the goldens.
	fn := lowerFixtureUnpromoted(t, "arrays.php")["shapes"]
	if fn == nil {
		t.Fatal("fixture has no shapes()")
	}
	got := fn.String()

	// Rendered the way PHP spells it, so the IR stays readable by hand.
	for _, want := range []string{
		`"write_var"($list, [0 => 1, 1 => 2, 2 => 3])`,
		`"write_var"($map, ["x" => 1, "y" => "two"])`,
		`"write_var"($empty, [])`,
		`"write_var"($nested, [0 => [0 => 1, 1 => 2], 1 => [0 => 3]])`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "<array>") {
		t.Errorf("an array literal still renders opaquely:\n%s", got)
	}

	// And every rendered literal parses back to the entries it was written
	// from -- including a nested array and a key containing the separator.
	for _, line := range strings.Split(got, "\n") {
		i := strings.Index(line, ", [")
		if !strings.Contains(line, "local.set") || i < 0 {
			continue
		}
		text := strings.TrimSpace(line[i+2:])
		entries, ok := lower.ParseArrayLiteral(text)
		if !ok {
			t.Errorf("rendered %q does not parse back", text)
			continue
		}
		if text == "[]" && len(entries) != 0 {
			t.Errorf("the empty array parsed to %d entries", len(entries))
		}
	}

	// A comma inside a key must not split the entry, and a nested array must
	// arrive whole.
	entries, ok := lower.ParseArrayLiteral(`["a, b" => 1, 2 => [3 => "x"]]`)
	if !ok || len(entries) != 2 {
		t.Fatalf("parsed %d entries, ok=%v", len(entries), ok)
	}
	if entries[0].StringKey != "a, b" {
		t.Errorf("a comma inside a key split the entry: %q", entries[0].StringKey)
	}
	if !entries[1].IntKey || entries[1].IntValue != 2 || entries[1].Value != `[3 => "x"]` {
		t.Errorf("a nested array did not arrive whole: %+v", entries[1])
	}
}

// A `catch` clause tests the exception's class before binding, and a non-match
// falls to the next clause.
//
// `ZEND_CATCH` carries the accepted class in op1 and the lowering dropped it,
// so every clause bound unconditionally: the *first* handler ran for any
// exception, and `catch (ValueError $a)` caught a TypeError. Nothing in the IR
// distinguished the two clauses.
//
// Both halves settled against the engine rather than reasoned about. A clause
// that does not match **does not bind its variable** — after `catch (ValueError
// $a)` fails to catch a TypeError, `isset($a)` is false — so the test precedes
// the assignment. And a subclass matches a parent clause, so the test is
// `instanceof` rather than a name comparison.
//
// The chain is read from the engine's own output too: with three clauses op2
// points at the next catch except for the last, where it points at itself.
func TestACatchTestsItsClassBeforeBinding(t *testing.T) {
	fn := lowerFixture(t, "catches.php")["multi"]
	if fn == nil {
		t.Fatal("fixture has no multi()")
	}
	got := fn.String()

	for _, want := range []string{
		`instanceof $__exc`,
		`"ValueError"`,
		`"TypeError"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected %q in:\n%s", want, got)
		}
	}

	// The bind must not sit in the block that tests, or a non-matching clause
	// assigns its variable on the way past.
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		tests, binds := false, false
		for _, in := range b.Insts {
			if in.Opcode == "instanceof" {
				tests = true
			}
			if in.Opcode == "local.set" && len(in.Operands) == 2 &&
				(in.Operands[0] == "$a" || in.Operands[0] == "$b") {
				binds = true
			}
		}
		if tests && binds {
			t.Errorf("bb%d both tests the class and binds the variable:\n%s", b.ID, got)
		}
	}

	// Every clause reads one place rather than a value defined elsewhere: the
	// exception outlives the block that receives it, and the IR's rule is that
	// such a value is a place.
	if strings.Count(got, `"write_var"($__exc`)+strings.Count(got, "local.set $__exc") != 1 {
		t.Errorf("the exception is parked more than once, so a chain would collide:\n%s", got)
	}
}

// appliedTo counts calls to an intrinsic whose argument is the value read from
// a given variable. Reads are explicit instructions now, so an operand is an
// SSA name and the variable behind it is one hop away -- which a test wanting
// to say "this operation was applied to $a" has to follow.
func appliedTo(ir, intrinsic, variable string) int {
	from := map[string]string{}
	readOf := regexp.MustCompile(`(%\d+):\w+ = call\.intrinsic "read_var(?:\.silent)?"\(\$(\w+)`)
	for _, m := range readOf.FindAllStringSubmatch(ir, -1) {
		from[m[1]] = m[2]
	}
	n := 0
	call := regexp.MustCompile(`call\.intrinsic "` + regexp.QuoteMeta(intrinsic) + `"\((%\d+)[,)]`)
	for _, m := range call.FindAllStringSubmatch(ir, -1) {
		if from[m[1]] == variable {
			n++
		}
	}
	return n
}

// TestLoweringIsReproducible. The same input must lower to the same IR, byte for
// byte, on every run.
//
// Block-parameter value numbers were allocated in **map iteration order**, so
// two runs of the same file produced two different-looking IRs and `TestGoldenIR`
// passed or failed by luck -- the flaky verdict QUALITY_GATE.md treats as a
// defect rather than a result. An IR that is not reproducible cannot be diffed,
// cached, or pinned by anything.
//
// Lowering whole fixtures rather than a hand-built function, because the
// hand-built one did not reproduce it: the ordering only varies where a place
// needs a parameter in more than one block, which wants a CFG with real merges.
//
// **This detector is probabilistic**, and saying so matters. Go randomises map
// iteration per run, so re-introducing the bug fails this on some runs and not
// others -- three tries were needed to see it. It catches the class rather than
// guaranteeing the instance, which is still far better than the golden, whose
// single comparison caught it by luck. Run it a few times when the ordering of
// anything in mem2reg changes.
func TestLoweringIsReproducible(t *testing.T) {
	for _, name := range []string{"finally.php", "regions.php", "foreach.php", "shortcircuit.php"} {
		t.Run(name, func(t *testing.T) {
			render := func() string {
				fns := lowerFixture(t, name)
				var names []string
				for n := range fns {
					names = append(names, n)
				}
				var out strings.Builder
				for _, n := range sortedWithMainFirst(names) {
					out.WriteString(fns[n].String())
				}
				return out.String()
			}
			want := render()
			for i := 0; i < 8; i++ {
				if got := render(); got != want {
					t.Fatalf("run %d lowered differently:\n--- first ---\n%s\n--- now ---\n%s",
						i, want, got)
				}
			}
		})
	}
}

// A static property is a plain slot owned by a class -- no __set, no hooks, no
// autovivification -- so the write is one call like any other. It was the last
// assignment still lowering to a place plus a `store`, and the store then
// recovered the place's kind at run time from which struct field was set.
func TestStaticPropertyWriteIsOneCall(t *testing.T) {
	got := text(t, lowerFixture(t, "write.php"), "stash")
	if !strings.Contains(got, `"sprop.set"("n", "Reg", %0)`) {
		t.Errorf("static property write did not lower to one call:\n%s", got)
	}
	if strings.Contains(got, "store ") || strings.Contains(got, "sprop.ref") {
		t.Errorf("the write still went through a place:\n%s", got)
	}
}

// The static-property read is a call, and its silent form is a **different**
// intrinsic: `C::$nope` raises `Access to undeclared static property` where
// `C::$nope ?? "d"` answers "d" with no Error at all. The engine settles both.
func TestStaticPropertyReadHasTwoForms(t *testing.T) {
	got := text(t, lowerFixture(t, "write.php"), "peek")
	for _, want := range []string{`"sprop.get"("n", "Reg")`, `"sprop.get.silent"("n", "Reg")`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s:\n%s", want, got)
		}
	}
	if strings.Contains(got, "mode=") {
		t.Errorf("the access mode outlived the call that spent it:\n%s", got)
	}
}

// TestKeyedForeachBindsTheKey. A foreach binds its key through a compiler
// temporary: `write_var($__t4, <key>)` then `write_var($k, $__t4)`. Promoting
// the temporary dropped its store while the second instruction still named it,
// so `$k` was bound to a slot nothing wrote and every keyed foreach inside a
// function produced an **empty key** -- a wrong answer, in the default
// pipeline, for one of PHP's most common loops.
//
// The assertion is that no promoted temporary is still named after promotion.
// Checking the key's value directly would pass just as well against the stale
// name, because the stale name is a perfectly well-formed operand.
func TestKeyedForeachBindsTheKey(t *testing.T) {
	fn := lowerFixture(t, "foreach.php")["joined"]
	if fn == nil {
		t.Fatal("fixture has no joined")
	}
	got := fn.String()
	if !strings.Contains(got, "iter.key") {
		t.Fatalf("the fixture no longer binds a key:\n%s", got)
	}
	if strings.Contains(got, "$__") {
		t.Errorf("a promoted compiler temporary is still named after promotion:\n%s", got)
	}
}

// TestFloatTextRoundTrips pins the property that makes IR operand text safe to
// read back: a float literal must not come back an int.
//
// The scanners this protects -- the emitter's `operand`, `Specialize`'s type
// facts -- both try `ParseInt` before `ParseFloat`, so `9` is an int no matter
// what produced it. The assertion is therefore made in the reader's terms
// rather than on the spelling: what matters is not that the text is "9.0" but
// that `ParseInt` refuses it.
func TestFloatTextRoundTrips(t *testing.T) {
	for _, f := range []float64{0, 9, -9, 1e21, 2.5, -0.5, 1 << 62} {
		got := lower.FloatText(f)
		if _, err := strconv.ParseInt(got, 10, 64); err == nil {
			t.Errorf("FloatText(%v) = %q, which reads back as an int", f, got)
		}
		back, err := strconv.ParseFloat(got, 64)
		if err != nil {
			t.Errorf("FloatText(%v) = %q, which is not a float: %v", f, got, err)
			continue
		}
		if back != f {
			t.Errorf("FloatText(%v) = %q, which reads back as %v", f, got, back)
		}
	}
	// INF and NAN are real PHP float values and keep their %v spelling, which
	// the emitter translates by name. They must still not read as ints.
	for _, f := range []float64{math.Inf(1), math.Inf(-1), math.NaN()} {
		got := lower.FloatText(f)
		if _, err := strconv.ParseInt(got, 10, 64); err == nil {
			t.Errorf("FloatText(%v) = %q, which reads back as an int", f, got)
		}
	}
}

// A call to a function that may suspend has to become a suspension point in
// the caller, or the callee hands an outcome back to a frame with nowhere to
// put one. That is the caller question `Resumable` records and does not answer
// on its own: the answer is supplied, because "can this callee suspend" is a
// whole-program property (`CallGraph.Reach`) and this pass sees one function.
//
// The shape is `yield from`'s: control leaves at the call, and the function
// resumes with the callee's **return value** -- so the call's result is the
// continuation's parameter, which is where the rewrite takes the resumed value
// from.
func TestResumableSplitsAtACallThatMaySuspend(t *testing.T) {
	fns := lowerFixture(t, "suspending_call.php")
	outer := fns["App\\outer"]
	if outer == nil {
		t.Fatal("fixture did not lower App\\outer")
	}
	if outer.SuspendsToCaller {
		t.Fatal("outer suspends only through its callee; nothing should mark it before the split")
	}
	suspends := func(kind, name, recv string) bool { return name == "app\\inner" }
	if err := lower.Resumable(outer, suspends); err != nil {
		t.Fatalf("a caller of a suspending function was refused: %v", err)
	}
	if !outer.SuspendsToCaller {
		t.Error("control leaves outer for the fiber's driver, so it suspends to its own caller too")
	}
	if len(outer.States) < 2 {
		t.Errorf("not rewritten into a state machine: states=%v", outer.States)
	}
	var left int
	for _, b := range outer.Blocks {
		if strings.HasPrefix(b.Term, "ret.call ") {
			left++
		}
		if strings.HasPrefix(b.Term, "call.suspending") {
			t.Errorf("bb%d kept the pre-rewrite terminator: %q", b.ID, b.Term)
		}
	}
	if left != 1 {
		t.Errorf("want exactly one suspending call terminator, got %d:\n%s", left, outer.String())
	}
	// The continuation reads the callee's return value out of the frame, not
	// off an edge.
	if !strings.Contains(outer.String(), "frame.resumed") {
		t.Errorf("no frame.resumed, so the call's result is lost:\n%s", outer.String())
	}
	// **The call keeps its arguments and its attributes**, because it stays an
	// instruction: only its opcode and result change. `fallback=` in
	// particular decides which function a namespaced call reaches.
	if !strings.Contains(outer.String(), `frame.of.fn "App\\inner"(`) {
		t.Errorf("the suspending call lost its argument list:\n%s", outer.String())
	}
	if !strings.Contains(outer.String(), `fallback="inner"`) {
		t.Errorf("the suspending call lost its fallback attribute, so a namespaced call resolves wrongly:\n%s", outer.String())
	}
}

// Two suspending calls in one block split twice, and a call whose result
// nothing reads splits too -- the resumption has to be marked either way.
func TestResumableSplitsEverySuspendingCallInABlock(t *testing.T) {
	fns := lowerFixture(t, "suspending_call.php")
	twice := fns["App\\twice"]
	if twice == nil {
		t.Fatal("fixture did not lower App\\twice")
	}
	suspends := func(kind, name, recv string) bool { return name == "app\\inner" }
	if err := lower.Resumable(twice, suspends); err != nil {
		t.Fatalf("two suspending calls in one block were refused: %v", err)
	}
	var left int
	for _, b := range twice.Blocks {
		if strings.HasPrefix(b.Term, "ret.call ") {
			left++
		}
	}
	if left != 2 {
		t.Errorf("want two suspending call terminators, got %d:\n%s", left, twice.String())
	}
}

// Nothing splits without an answer, which is what keeps this dormant until the
// call graph reaches the compile path.
func TestResumableSplitsNothingWithoutASuspendingSet(t *testing.T) {
	outer := lowerFixture(t, "suspending_call.php")["App\\outer"]
	if err := lower.Resumable(outer, nil); err != nil {
		t.Fatalf("lowering refused with no suspending set: %v", err)
	}
	if outer.SuspendsToCaller {
		t.Error("marked as suspending with no set to judge against")
	}
	for _, b := range outer.Blocks {
		if strings.HasPrefix(b.Term, "ret.call ") || strings.HasPrefix(b.Term, "call.suspending") {
			t.Errorf("bb%d split with no suspending set: %q", b.ID, b.Term)
		}
	}
}
