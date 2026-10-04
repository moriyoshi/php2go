package lower_test

import (
	"strings"
	"testing"
	"time"

	"github.com/moriyoshi/php2go/internal/lower"
)

// annotation returns the intrinsic each instruction was specialized to.
func annotations(fn *lower.Function) []string {
	var out []string
	for _, b := range fn.Blocks {
		for _, in := range b.Insts {
			if in.Opcode != lower.IntrinsicCall {
				continue
			}
			out = append(out, strings.Trim(in.Operands[0], `"`))
		}
	}
	return out
}

// TestSpecializeRewritesOperationsIntoIntrinsicCalls is the point of the pass.
//
// An intrinsic is a **function**, so the IR should contain a *call* to it -- not
// an arithmetic opcode wearing an annotation that names one. The difference is
// not cosmetic: with a real call the emitter has one case for the whole family
// and re-derives nothing, where an annotated opcode still needs a case per
// operator to read the annotation from.
func TestSpecializeRewritesOperationsIntoIntrinsicCalls(t *testing.T) {
	fn := &lower.Function{Blocks: []*lower.Block{{
		ID: 0,
		Insts: []lower.Inst{
			// Two literals: provable, so the narrow variant.
			{Opcode: "add", Operands: []string{"1", "2"}, Result: &lower.Value{N: 1, Type: "zval"}},
			// A result of add.int is an int, so this narrows too -- which is
			// the whole reason inference is a forward pass rather than a
			// per-instruction lookup.
			{Opcode: "mul", Operands: []string{"%1", "3"}, Result: &lower.Value{N: 2, Type: "zval"}},
			// An unknown operand: the general variant, and correct.
			{Opcode: "add", Operands: []string{"$x", "1"}, Result: &lower.Value{N: 3, Type: "zval"}},
			// A string operand cannot use the int variant.
			{Opcode: "add", Operands: []string{`"a"`, "1"}, Result: &lower.Value{N: 4, Type: "zval"}},
		},
	}}}

	specialized, total := specializePipeline(fn)
	for _, in := range fn.Blocks[0].Insts {
		if in.Opcode != lower.IntrinsicCall {
			t.Errorf("%s was left as an opcode rather than becoming a call", in.Opcode)
		}
	}
	got := annotations(fn)
	// `mul.numeric`, not `mul.int`: `add.int`'s result is int **or float**,
	// because `PHP_INT_MAX + 1` overflows. The chain still narrows -- two
	// numerics cannot diagnose -- but it narrows to the variant the types
	// actually justify.
	want := []string{"add.int", "mul.numeric", "add", "add"}
	if len(got) != len(want) {
		t.Fatalf("annotated %d instructions, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("instruction %d: got %q, want %q", i, got[i], want[i])
		}
	}
	if total != 4 {
		t.Errorf("counted %d specializable calls, want 4", total)
	}
	if specialized != 2 {
		t.Errorf("specialized %d, want 2", specialized)
	}
}

// TestSpecializeIsSoundOnUnknowns is the property that matters more than reach.
//
// A wrong narrowing selects a variant that skips a diagnostic PHP emits, which
// is a wrong answer. An absent one selects the general variant, which is merely
// slower. So the pass must never narrow on anything it has not proved to be
// **numeric** -- a string, a bool, a null and anything unknown all reach the
// arithmetic walk, and the walk is what diagnoses them.
func TestSpecializeIsSoundOnUnknowns(t *testing.T) {
	for _, operand := range []string{"$x", "%9", `"5"`, "true", "null"} {
		fn := &lower.Function{Blocks: []*lower.Block{{ID: 0, Insts: []lower.Inst{
			{Opcode: "add", Operands: []string{operand, "1"}, Result: &lower.Value{N: 1, Type: "zval"}},
		}}}}
		if specialized, _ := specializePipeline(fn); specialized != 0 {
			t.Errorf("operand %q was narrowed to a pure variant, but it is not proven numeric", operand)
		}
	}
	// A float *is* proven numeric, and `1.5 + 1` diagnoses nothing -- settled
	// with the engine, which is why this moved out of the list above rather
	// than staying there on the strength of the old comment.
	fn := &lower.Function{Blocks: []*lower.Block{{ID: 0, Insts: []lower.Inst{
		{Opcode: "add", Operands: []string{"1.5", "1"}, Result: &lower.Value{N: 1, Type: "zval"}},
	}}}}
	if specialized, _ := specializePipeline(fn); specialized != 1 {
		t.Errorf("a float operand did not narrow; two numerics cannot diagnose")
	}
}

// TestSpecializeTracksPlaces is the step that makes the mechanism pay: this IR
// puts anything outliving a block into a place, so an analysis over values alone
// proves nothing about `$a = 1; $b = $a + 2;`.
func TestSpecializeTracksPlaces(t *testing.T) {
	fn := &lower.Function{Blocks: []*lower.Block{{ID: 0, Insts: []lower.Inst{
		{Opcode: "local.set", Operands: []string{"$a", "1"}},
		{Opcode: "add", Operands: []string{"$a", "2"}, Result: &lower.Value{N: 1, Type: "zval"}},
		{Opcode: "local.set", Operands: []string{"$b", "%1"}},
		{Opcode: "mul", Operands: []string{"$b", "$a"}, Result: &lower.Value{N: 2, Type: "zval"}},
	}}}}
	specialized, total := specializePipeline(fn)
	if total != 2 {
		t.Fatalf("counted %d specializable calls, want 2", total)
	}
	if specialized != 2 {
		t.Errorf("specialized %d of 2; a place assigned only from known values is known", specialized)
	}
}

// TestSpecializeRefusesHazardousPlaces is the soundness half, and it matters
// more than the reach: a place another name can write is not knowable here, and
// narrowing it would skip a diagnostic PHP emits.
func TestSpecializeRefusesHazardousPlaces(t *testing.T) {
	hazards := map[string]lower.Inst{
		"bound by reference": {Opcode: "bind_ref", Operands: []string{"$r", "$a"}},
		"unset":              {Opcode: "unset", Operands: []string{"$a"}},
		"made global":        {Opcode: "bind_global", Operands: []string{"$a"}},
		"captured by a closure": {Opcode: "bind_lexical", Operands: []string{"%9", "$a"},
			Attrs: []string{"use=ref"}},
		"a write target": {Opcode: "dim.ref", Operands: []string{"$a", "0"},
			Result: &lower.Value{N: 8, Type: "place"}},
	}
	for why, hazard := range hazards {
		fn := &lower.Function{Blocks: []*lower.Block{{ID: 0, Insts: []lower.Inst{
			{Opcode: "local.set", Operands: []string{"$a", "1"}},
			hazard,
			{Opcode: "add", Operands: []string{"$a", "2"}, Result: &lower.Value{N: 1, Type: "zval"}},
		}}}}
		if specialized, _ := specializePipeline(fn); specialized != 0 {
			t.Errorf("%s: $a was narrowed anyway, but another name can change it", why)
		}
	}
}

// TestSpecializeConvergesOnASelfReferentialPlace: `$i = $i + 1` types `$i` from
// a value typed from `$i`. The union widens monotonically, so it settles.
func TestSpecializeConvergesOnASelfReferentialPlace(t *testing.T) {
	fn := &lower.Function{Blocks: []*lower.Block{{ID: 0, Insts: []lower.Inst{
		{Opcode: "local.set", Operands: []string{"$i", "0"}},
		{Opcode: "add", Operands: []string{"$i", "1"}, Result: &lower.Value{N: 1, Type: "zval"}},
		{Opcode: "local.set", Operands: []string{"$i", "%1"}},
	}}}}
	done := make(chan struct{})
	go func() { specializePipeline(fn); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("inference did not converge on a self-referential place")
	}
}

// TestSpecializeReadsPlacesThroughLocalGet is the propagation half. Every
// operand in this IR is an SSA value, so a place's type reaches an expression
// only through the `local.get` that reads it. Without that hop the place
// analysis is computed and never consulted -- which is what the corpus showed:
// tracking places moved the rate by 0.1 points.
func TestSpecializeReadsPlacesThroughLocalGet(t *testing.T) {
	fn := &lower.Function{
		Params: []string{`0 $n "int"`},
		Blocks: []*lower.Block{{ID: 0, Insts: []lower.Inst{
			{Opcode: "local.set", Operands: []string{"$a", "1"}},
			{Opcode: "local.get", Operands: []string{"$a"}, Result: &lower.Value{N: 1, Type: "zval"}},
			{Opcode: "add", Operands: []string{"%1", "2"}, Result: &lower.Value{N: 2, Type: "zval"}},
		}}},
	}
	specialized, total := specializePipeline(fn)
	if total != 1 {
		t.Fatalf("counted %d specializable calls, want 1", total)
	}
	if specialized != 1 {
		t.Errorf("specialized %d of 1; the place is int and local.get should carry that", specialized)
	}
}

// TestSpecializeRefusesAPlaceReadableBeforeItIsWritten. A place assigned on one
// arm of a branch holds the union of the assigned types, and **undef** on the
// other arm, which is in none of them. Narrowing on that union hands a null to
// a variant whose justification is that it cannot receive one.
func TestSpecializeRefusesAPlaceReadableBeforeItIsWritten(t *testing.T) {
	//	b0: if $c -> b1 else b2
	//	b1: $a = 1
	//	b2: $a + 2        <- reached with $a unwritten
	fn := &lower.Function{Blocks: []*lower.Block{
		{ID: 0, Succs: []int{1, 2}, Term: "br $c, b1, b2"},
		{ID: 1, Succs: []int{2}, Term: "jmp b2", Insts: []lower.Inst{
			{Opcode: "local.set", Operands: []string{"$a", "1"}},
		}},
		{ID: 2, Term: "ret", Insts: []lower.Inst{
			{Opcode: "local.get", Operands: []string{"$a"}, Result: &lower.Value{N: 1, Type: "zval"}},
			{Opcode: "add", Operands: []string{"%1", "2"}, Result: &lower.Value{N: 2, Type: "zval"}},
		}},
	}}
	if specialized, _ := specializePipeline(fn); specialized != 0 {
		t.Errorf("narrowed $a to int, but the branch that skips the assignment reads undef")
	}
}

// TestSpecializeRefusesAByReferenceParameter is the hazard that was written
// down and did not work: the name was assembled as `$$x` and matched no
// candidate, and the hazard test covered only instruction-level hazards. The
// caller shares the box, so anything can be in it at any point.
func TestSpecializeRefusesAByReferenceParameter(t *testing.T) {
	fn := &lower.Function{
		Params: []string{`0 &$a "int"`},
		Blocks: []*lower.Block{{ID: 0, Insts: []lower.Inst{
			{Opcode: "local.set", Operands: []string{"$a", "1"}},
			{Opcode: "local.get", Operands: []string{"$a"}, Result: &lower.Value{N: 1, Type: "zval"}},
			{Opcode: "add", Operands: []string{"%1", "2"}, Result: &lower.Value{N: 2, Type: "zval"}},
		}}},
	}
	if specialized, _ := specializePipeline(fn); specialized != 0 {
		t.Errorf("narrowed a by-reference parameter; the caller shares the box")
	}
}

// TestSpecializeTypesAByValueParameterFromItsDeclaredType is the other side of
// the same parse: a by-value parameter is definitely assigned at entry, so
// reading it before any `local.set` is not a hazard.
func TestSpecializeTypesAByValueParameterIsNotAHazard(t *testing.T) {
	fn := &lower.Function{
		Params: []string{`0 $a "int"`},
		Blocks: []*lower.Block{{ID: 0, Insts: []lower.Inst{
			{Opcode: "local.set", Operands: []string{"$a", "1"}},
			{Opcode: "local.get", Operands: []string{"$a"}, Result: &lower.Value{N: 1, Type: "zval"}},
			{Opcode: "add", Operands: []string{"%1", "2"}, Result: &lower.Value{N: 2, Type: "zval"}},
		}}},
	}
	if specialized, _ := specializePipeline(fn); specialized != 1 {
		t.Errorf("refused a by-value parameter assigned before it is read")
	}
}

// TestIntrinsicForNamesRealOpcodes pins the two vocabularies against each
// other. The catalogue names an intrinsic `ShiftLeft` and spells its variants
// `shift_left`; the lowering emits the engine's own opcode name lowercased,
// which is `sl`. Six entries were written in the catalogue's vocabulary and
// therefore matched nothing:
//
//	bit_and bit_or bit_xor bit_not shift_left shift_right
//
// Nothing noticed, because the specialization counter only counts entries that
// matched -- a dead entry is invisible to a ratio. `$n << 2` reached the
// emitter as `sl` and was refused as unsupported, so an operator PHP has had
// since forever did not compile.
//
// The authority is the lowering's own binaryOps, not a second hand-written
// list, because a second list is the thing that drifted.
func TestIntrinsicForNamesRealOpcodes(t *testing.T) {
	emitted := lower.BinaryOpcodeNames()
	for _, op := range lower.IntrinsicOpcodes() {
		if lower.UnaryIntrinsicOpcodes[op] {
			continue
		}
		if !emitted[op] {
			t.Errorf("intrinsicFor names %q, which the lowering never emits", op)
		}
	}
}

// specializePipeline is what the compiler does: `lower.Lower` forms the
// intrinsic calls before it returns, and `lower.Specialize` then chooses each
// call's variant. These fixtures are built by hand rather than lowered, so they
// have to run the first step themselves -- and running it here rather than
// hand-writing `call.intrinsic` in every fixture keeps the fixtures readable as
// the PHP operations they stand for.
func specializePipeline(fn *lower.Function) (int, int) {
	lower.IntrinsicCalls(fn)
	return lower.Specialize(fn, nil)
}

// TestExplicitReadsDoNotTouchTargets is the soundness half of making reads
// explicit. A place used as a target is not read, and inventing a read there
// invents a diagnostic: `$a[0] = 1` on an unset `$a` **autovivifies silently**,
// and `unset($a)` on an unset `$a` says nothing at all.
func TestExplicitReadsDoNotTouchTargets(t *testing.T) {
	for why, in := range map[string]lower.Inst{
		"an assignment target": {Opcode: "local.set", Operands: []string{"$a", "1"}},
		"a rebind":             {Opcode: "bind_ref", Operands: []string{"$a", "$b"}},
		"an unset":             {Opcode: "unset", Operands: []string{"$a"}},
		"a global":             {Opcode: "bind_global", Operands: []string{"$a"}},
		"a static":             {Opcode: "bind_static", Operands: []string{"$a"}},
	} {
		fn := &lower.Function{Blocks: []*lower.Block{{ID: 0, Insts: []lower.Inst{in}}}}
		lower.ExplicitReads(fn)
		for _, got := range fn.Blocks[0].Insts {
			if got.Opcode == lower.IntrinsicCall {
				t.Errorf("%s was read: %s", why, got.String())
			}
		}
	}
}

// TestExplicitReadsSkipCompilerTemporaries. A `$__t3` is written before it is
// read by construction and the program cannot name it, so reading it through
// the warning path would invent a diagnostic PHP never emits.
func TestExplicitReadsSkipCompilerTemporaries(t *testing.T) {
	fn := &lower.Function{Blocks: []*lower.Block{{ID: 0, Insts: []lower.Inst{
		{Opcode: "verify_return", Operands: []string{"$__t3"}, Result: &lower.Value{N: 1, Type: "zval"}},
	}}}}
	lower.ExplicitReads(fn)
	if n := len(fn.Blocks[0].Insts); n != 1 {
		t.Errorf("a compiler temporary was read: %d instructions, want 1", n)
	}
}

// TestExplicitReadsUseTheSilentFormUnderIS. `$q ?? 5` must not warn: the engine
// has one opcode for it and the suppression is intrinsic to it, so the read is
// a *different intrinsic* rather than the same one with a flag.
func TestExplicitReadsUseTheSilentFormUnderIS(t *testing.T) {
	// The consumer is the intrinsic the coalesce becomes, because that is what
	// now carries the fact. This used to hand ExplicitReads a `verify_return`
	// wearing a `mode=IS` attribute -- a shape the lowering never emits, so the
	// test exercised the attribute mechanism rather than the path.
	fn := &lower.Function{Blocks: []*lower.Block{{ID: 0, Insts: []lower.Inst{
		{Opcode: lower.IntrinsicCall, Operands: []string{`"copy.silent"`}, ArgList: "($q)",
			Result: &lower.Value{N: 1, Type: "zval"}},
	}}}}
	lower.ExplicitReads(fn)
	got := fn.Blocks[0].Insts[0].String()
	if !strings.Contains(got, `"read_var.silent"`) {
		t.Errorf("an IS read warned: %s", got)
	}
}

// TestExplicitReadsReachTheTerminator. `return $sum` reads `$sum`, and the
// terminator is text rather than operands -- the one place a scan is needed.
func TestExplicitReadsReachTheTerminator(t *testing.T) {
	fn := &lower.Function{Blocks: []*lower.Block{{ID: 0, Term: "ret $sum"}}}
	lower.ExplicitReads(fn)
	if len(fn.Blocks[0].Insts) != 1 {
		t.Fatalf("the terminator's read was not emitted: %+v", fn.Blocks[0])
	}
	if fn.Blocks[0].Term != "ret %0" {
		t.Errorf("terminator = %q, want it to name the value it read", fn.Blocks[0].Term)
	}
}

// TestMem2RegPromotesAcrossAMerge is the pass doing its job: a place written on
// both arms of a branch becomes a block parameter, and the branch carries the
// value each arm produced.
func TestMem2RegPromotesAcrossAMerge(t *testing.T) {
	fn := &lower.Function{Blocks: []*lower.Block{
		{ID: 0, Region: -1, Succs: []int{1, 2}, Term: "br %0, bb1, bb2", Insts: []lower.Inst{
			{Opcode: "local.set", Operands: []string{"$x", "1"}},
		}},
		{ID: 1, Region: -1, Succs: []int{2}, Term: "jmp bb2", Insts: []lower.Inst{
			{Opcode: "local.set", Operands: []string{"$x", "2"}},
		}},
		{ID: 2, Region: -1, Term: "ret %9", Insts: []lower.Inst{
			{Opcode: lower.IntrinsicCall, Operands: []string{`"read_var"`}, ArgList: "($x)",
				Result: &lower.Value{N: 9, Type: "zval"}},
		}},
	}}
	if got, _ := lower.Mem2Reg(fn); got != 1 {
		t.Fatalf("promoted %d places, want 1:\n%s", got, fn)
	}
	out := fn.String()
	if len(fn.Blocks[2].Params) != 1 {
		t.Errorf("the merge took no parameter:\n%s", out)
	}
	for _, want := range []string{"bb2(1)", "bb2(2)"} {
		if !strings.Contains(out, want) {
			t.Errorf("edge does not carry %s:\n%s", want, out)
		}
	}
	if strings.Contains(out, "local.set $x") || strings.Contains(out, `"read_var"($x`) {
		t.Errorf("the place survived its own promotion:\n%s", out)
	}
}

// TestMem2RegRefusesWhatItCannotSee. Each of these lets a place change without
// a `local.set`, so the value a promoted read would produce is not the one the
// last store put there.
func TestMem2RegRefusesWhatItCannotSee(t *testing.T) {
	for why, hazard := range map[string]lower.Inst{
		"bound by reference":    {Opcode: "bind_ref", Operands: []string{"$r", "$x"}},
		"unset":                 {Opcode: "unset", Operands: []string{"$x"}},
		"made global":           {Opcode: "bind_global", Operands: []string{"$x"}},
		"captured by a closure": {Opcode: "bind_lexical", Operands: []string{"%3", "$x"}},
		"address taken": {Opcode: lower.IntrinsicCall, Operands: []string{`"dim.ref"`},
			ArgList: "($x, 0)", Result: &lower.Value{N: 8, Type: "place"}},
	} {
		fn := &lower.Function{Blocks: []*lower.Block{{ID: 0, Region: -1, Term: "ret null", Insts: []lower.Inst{
			{Opcode: "local.set", Operands: []string{"$x", "1"}},
			hazard,
			{Opcode: lower.IntrinsicCall, Operands: []string{`"read_var"`}, ArgList: "($x)",
				Result: &lower.Value{N: 9, Type: "zval"}},
		}}}}
		if got, _ := lower.Mem2Reg(fn); got != 0 {
			t.Errorf("%s: promoted anyway, but the place can change without a store", why)
		}
	}
}

// TestMem2RegRefusesAPlaceReadableBeforeItIsWritten. A promoted place has no
// value at all before its first store, where the unpromoted one reads null and
// **warns**. Keeping the read keeps the diagnostic.
func TestMem2RegRefusesAPlaceReadableBeforeItIsWritten(t *testing.T) {
	fn := &lower.Function{Blocks: []*lower.Block{
		{ID: 0, Region: -1, Succs: []int{1, 2}, Term: "br %0, bb1, bb2"},
		{ID: 1, Region: -1, Succs: []int{2}, Term: "jmp bb2", Insts: []lower.Inst{
			{Opcode: "local.set", Operands: []string{"$x", "1"}},
		}},
		{ID: 2, Region: -1, Term: "ret %9", Insts: []lower.Inst{
			{Opcode: lower.IntrinsicCall, Operands: []string{`"read_var"`}, ArgList: "($x)",
				Result: &lower.Value{N: 9, Type: "zval"}},
		}},
	}}
	if got, _ := lower.Mem2Reg(fn); got != 0 {
		t.Errorf("promoted a place the branch can skip the assignment of")
	}
}

// TestMem2RegLeavesASuspendingBodyAlone. The CPS pass rewrites a generator
// afterwards and **parks values in places** so they survive a resume; an SSA
// value has nowhere to live across that. Found by the corpus: a running sum
// printed the loop counter.
func TestMem2RegLeavesASuspendingBodyAlone(t *testing.T) {
	fn := &lower.Function{Blocks: []*lower.Block{
		{ID: 0, Region: -1, Succs: []int{1}, Term: "yield %1, bb1", Insts: []lower.Inst{
			{Opcode: "local.set", Operands: []string{"$x", "1"}},
		}},
		{ID: 1, Region: -1, Term: "ret %9", Insts: []lower.Inst{
			{Opcode: lower.IntrinsicCall, Operands: []string{`"read_var"`}, ArgList: "($x)",
				Result: &lower.Value{N: 9, Type: "zval"}},
		}},
	}}
	if got, _ := lower.Mem2Reg(fn); got != 0 {
		t.Errorf("promoted inside a body the CPS pass will rewrite")
	}
}

// TestDefiniteAssignmentSeesTheReadIntrinsic. The check that keeps a narrowing
// from skipping an undefined-variable warning scanned for the `local.get`
// opcode. Reads became `call.intrinsic "read_var"`, so it found none and
// pronounced every place definitely assigned -- **passing vacuously** for as
// long as that was true. This pins the shape it actually has to look for.
func TestDefiniteAssignmentSeesTheReadIntrinsic(t *testing.T) {
	//	b0: if $c -> b1 else b2
	//	b1: $a = 1
	//	b2: read $a; $a + 2      <- reached with $a unwritten
	fn := &lower.Function{Blocks: []*lower.Block{
		{ID: 0, Region: -1, Succs: []int{1, 2}, Term: "br %0, bb1, bb2"},
		{ID: 1, Region: -1, Succs: []int{2}, Term: "jmp bb2", Insts: []lower.Inst{
			{Opcode: "local.set", Operands: []string{"$a", "1"}},
		}},
		{ID: 2, Region: -1, Term: "ret", Insts: []lower.Inst{
			{Opcode: lower.IntrinsicCall, Operands: []string{`"read_var"`}, ArgList: "($a)",
				Result: &lower.Value{N: 1, Type: "zval"}},
			{Opcode: "add", Operands: []string{"%1", "2"}, Result: &lower.Value{N: 2, Type: "zval"}},
		}},
	}}
	lower.IntrinsicCalls(fn)
	if specialized, _ := lower.Specialize(fn, nil); specialized != 0 {
		t.Errorf("narrowed $a to int, but the branch that skips the assignment reads undef")
	}
}

// TestMem2RegRefusesAStoreInsideAProtectedRange.
//
// The renaming walk carries the value at the **end** of a block into the blocks
// it dominates, and a handler is entered from the *middle* of a protected
// range. So a store that appears before the throw, and never executed, would
// still be the value a handler sees:
//
//	$a = 1;
//	try { $a /= 0; } catch (DivisionByZeroError) {}
//	var_dump($a);        // int(1) -- the store never happened
//
// Promotion answered NULL. Four tests in Zend's own suite exist for exactly
// this, because it is what PHP is careful about: a compound assignment that
// throws leaves the variable alone.
func TestMem2RegRefusesAStoreInsideAProtectedRange(t *testing.T) {
	fn := &lower.Function{
		Regions: []lower.Region{{Catch: 2, Finally: -1, Parent: -1}},
		Blocks: []*lower.Block{
			{ID: 0, Region: -1, Succs: []int{1}, Term: "jmp bb1", Insts: []lower.Inst{
				{Opcode: "local.set", Operands: []string{"$a", "1"}},
			}},
			{ID: 1, Region: 0, Succs: []int{3}, Term: "jmp bb3", Insts: []lower.Inst{
				{Opcode: "local.set", Operands: []string{"$a", "2"}},
			}},
			{ID: 2, Region: -1, Succs: []int{3}, Term: "jmp bb3"},
			{ID: 3, Region: -1, Term: "ret %9", Insts: []lower.Inst{
				{Opcode: lower.IntrinsicCall, Operands: []string{`"read_var"`}, ArgList: "($a)",
					Result: &lower.Value{N: 9, Type: "zval"}},
			}},
		},
	}
	if got, _ := lower.Mem2Reg(fn); got != 0 {
		t.Errorf("promoted a place stored inside a try; the handler would see a store that never ran:\n%s", fn)
	}
}

// TestMem2RegLeavesTheFileBodyAlone. **The file body's locals are globals.**
// `$a = 41;` at file scope is readable by `global $a` in any function, by
// `$GLOBALS["a"]`, and by an included file -- none of which this frame can see.
// So a store is never dead there, and promotion drops stores it believes dead.
//
// Both escapes happen to be refused by the emitter today for other reasons, so
// nothing was wrong yet; this pins it so the guarantee does not depend on that.
func TestMem2RegLeavesTheFileBodyAlone(t *testing.T) {
	body := func(name string) *lower.Function {
		return &lower.Function{Name: name, Blocks: []*lower.Block{
			{ID: 0, Region: -1, Term: "ret null", Insts: []lower.Inst{
				{Opcode: "local.set", Operands: []string{"$a", "41"}},
				{Opcode: lower.IntrinsicCall, Operands: []string{`"read_var"`}, ArgList: "($a)",
					Result: &lower.Value{N: 9, Type: "zval"}},
			}},
		}}
	}
	if got, _ := lower.Mem2Reg(body("{main}")); got != 0 {
		t.Errorf("promoted a file-scope variable, which is a global")
	}
	if got, _ := lower.Mem2Reg(body("f")); got != 1 {
		t.Errorf("did not promote inside an ordinary function: %d", got)
	}
}

// TestInferenceConvergesOnALoopCarriedValue.
//
// Promoted, `$s` in `$s = 0; for (...) { $s = $s + $x; }` is a block parameter
// joined from `0` and the add's **own result**. On the first round that result
// is still being computed, and treating "no fact yet" as *unknown* rather than
// *not yet known* made the join Any -- which is absorbing under a union, so the
// add could never narrow afterwards.
//
// It converged before promotion, which is exactly why promotion looked like a
// regression when it was this.
func TestInferenceConvergesOnALoopCarriedValue(t *testing.T) {
	fns := lowerFixture(t, "inference.php")
	fn := fns["accumulate"]
	if fn == nil {
		t.Fatal("fixture has no accumulate")
	}
	specialized, total := lower.Specialize(fn, nil)
	if total == 0 {
		t.Fatalf("nothing specializable in:\n%s", fn)
	}
	if specialized != total {
		t.Errorf("specialized %d of %d; a loop-carried int did not converge:\n%s",
			specialized, total, fn)
	}
}

// TestPromotionIsStrictlyStrongerThanAPlaceFact.
//
// A place carries **one** fact for the whole function -- the join over every
// store -- where an SSA value carries one per definition. So a variable holding
// a string and then an int narrows only once promoted. This is where promotion
// pays: not by typing anything a place could not, but by not having to join
// what happens at different points into one answer.
func TestPromotionIsStrictlyStrongerThanAPlaceFact(t *testing.T) {
	promoted, _ := lower.Specialize(lowerFixture(t, "inference.php")["reassigned"], nil)
	unpromoted, _ := lower.Specialize(lowerFixtureUnpromoted(t, "inference.php")["reassigned"], nil)
	if unpromoted != 0 {
		t.Errorf("the place fact narrowed %d calls; it is the join of a string and an int", unpromoted)
	}
	if promoted != 1 {
		t.Errorf("promoted form specialized %d of 1; the second definition is an int", promoted)
	}
}

// TestContainerElementTypesReachTheOperators is the point of the generic
// lattice. `[1, 2, 3]` is an `array<int, int>`, so the iterator yields int and
// the sum narrows -- a flat mask says "array" and everything downstream of it
// is `mixed`.
func TestContainerElementTypesReachTheOperators(t *testing.T) {
	fns := lowerFixture(t, "inference.php")
	for _, name := range []string{"overTypedLiteral", "overBuilt"} {
		fn := fns[name]
		if fn == nil {
			t.Fatalf("fixture has no %s", name)
		}
		specialized, total := lower.Specialize(fn, nil)
		if total == 0 {
			t.Fatalf("%s: nothing specializable:\n%s", name, fn)
		}
		if specialized == 0 {
			t.Errorf("%s: specialized none of %d; the element type did not reach the operator:\n%s",
				name, total, fn)
		}
	}
}

// TestMixedElementsDoNotNarrow is the soundness half, and it matters more.
// `[$int, $string]` is an `array<int|string>`, the loop binds `int|string`, and
// `+` on a string must reach the arithmetic walk that diagnoses it. A lattice
// that narrowed here would skip a diagnostic PHP emits.
func TestMixedElementsDoNotNarrow(t *testing.T) {
	fn := lowerFixture(t, "inference.php")["overMixed"]
	if fn == nil {
		t.Fatal("fixture has no overMixed")
	}
	lower.Specialize(fn, nil)
	// The **arithmetic** must not narrow. Counting total specializations was a
	// proxy, and it stopped meaning this the moment `iter.new.array` started
	// firing -- correctly, since iterating an `array<int|string>` with the
	// array iterator is right whatever the elements are. What must not happen
	// is `+` skipping the walk that diagnoses the string.
	for _, name := range annotations(fn) {
		if strings.HasPrefix(name, "add.") {
			t.Errorf("selected %s over array<int|string>; the walk owes a diagnostic on the string:\n%s",
				name, fn)
		}
	}
}

// TestContainerShapeSelectsTheNarrowerEffectContract pins both directions of
// the covariant match, because only the pair is evidence: a `Subsumes` that
// always said yes would pass the first half alone.
//
// The variants narrow no representation -- all still take and return
// `php.Value`. What the shape buys is the effect half of the signature: an
// array has no `rewind()` to run, no `Countable::count()` to call, and no
// non-container offset to throw on.
func TestContainerShapeSelectsTheNarrowerEffectContract(t *testing.T) {
	fns := lowerFixture(t, "inference.php")
	want := map[string]bool{"count.array": false, "dim.get.array": false}
	fn := fns["shaped"]
	if fn == nil {
		t.Fatal("fixture has no shaped")
	}
	lower.Specialize(fn, nil)
	for _, name := range annotations(fn) {
		if _, ok := want[name]; ok {
			want[name] = true
		}
	}
	for name, got := range want {
		if !got {
			t.Errorf("declared `array` did not select %s:\n%s", name, fn)
		}
	}

	// The soundness half. `?array` may be null; both operations throw on null,
	// so neither may reach an implementation that cannot.
	loose := fns["maybeShaped"]
	if loose == nil {
		t.Fatal("fixture has no maybeShaped")
	}
	lower.Specialize(loose, nil)
	for _, name := range annotations(loose) {
		if _, ok := want[name]; ok {
			t.Errorf("selected %s for ?array; null reaches it and it cannot throw:\n%s",
				name, loose)
		}
	}
}

// TestArrayBackedIteratorNarrowsOnlyForAnArray pins both directions of the
// `Backing` axis, and the second direction is the one that matters: stepping a
// Traversable is three user-code calls, and selecting the array variants for
// one would skip `current()`, `key()` and `next()` entirely -- a wrong answer,
// not a slow one.
func TestArrayBackedIteratorNarrowsOnlyForAnArray(t *testing.T) {
	fns := lowerFixture(t, "inference.php")
	narrow := map[string]bool{
		"iter.current.array": true, "iter.key.array": true, "iter.next.array": true,
	}

	// A declared `array` parameter is enough: nothing is known about the
	// elements, and the backing is what these variants ask for.
	over := fns["overBuilt"]
	if over == nil {
		t.Fatal("fixture has no overBuilt")
	}
	lower.Specialize(over, nil)
	var got int
	for _, name := range annotations(over) {
		if narrow[name] {
			got++
		}
	}
	if got == 0 {
		t.Errorf("a foreach over a known array did not narrow:\n%s", over)
	}

	for _, name := range []string{"overTraversable", "overMaybeArray"} {
		fn := fns[name]
		if fn == nil {
			t.Fatalf("fixture has no %s", name)
		}
		lower.Specialize(fn, nil)
		for _, sel := range annotations(fn) {
			if narrow[sel] {
				t.Errorf("%s: selected %s; stepping it can run user code:\n%s", name, sel, fn)
			}
		}
	}
}
