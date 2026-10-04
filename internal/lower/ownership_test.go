package lower_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/moriyoshi/php2go/internal/lower"
	"github.com/moriyoshi/php2go/runtime/php"
)

// Summaries that say every other body keeps nothing, so a case isolates what
// this body does.
var noOtherBody = lower.OwnershipOptions{
	KeepsArg:      func([]string, int) bool { return false },
	LeaksThis:     func(string) bool { return false },
	ArgKept:       func(string) bool { return false },
	CtorLeaksThis: func(string) bool { return false },
	ReEntry:       func(string) bool { return false },
}

func traceOne(t *testing.T, src string, opts lower.OwnershipOptions) ([]lower.Ownership, *lower.Function) {
	t.Helper()
	fn := lowerOne(t, "<?php class D { static $s; } class H { function __construct($x) { $this->x = $x; } } "+src)
	return lower.TraceOwnership(fn, opts), fn
}

// siteOf picks the trace of the `new` of a class. Every case names each class
// it asks about once.
func siteOf(t *testing.T, got []lower.Ownership, fn *lower.Function, class string) lower.Ownership {
	t.Helper()
	for _, o := range got {
		if o.Class == class {
			return o
		}
	}
	t.Fatalf("no allocation of %s traced\n%s", class, fn)
	return lower.Ownership{}
}

// The container shapes from the CakePHP and Twig measurements. The object never
// escapes on its own in any of them; its container decides.
func TestOwnershipFollowsTheContainer(t *testing.T) {
	for _, c := range []struct {
		name    string
		src     string
		escapes []string
		via     int
	}{
		{"kept in a local array", `function t() { $a = []; $a[] = new D(); return count($a); }`, nil, 0},
		{"local array returned", `function t() { $a = []; $a[] = new D(); return $a; }`, []string{"return"}, 1},
		{"array literal returned", `function t() { return [new D()]; }`, []string{"return"}, 1},
		{"nested and kept", `function t() { $a = []; $a[] = [new D()]; return count($a); }`, nil, 0},
		{"nested and returned", `function t() { $a = []; $a[] = [new D()]; return $a; }`, []string{"return"}, 2},
		{"into a parameter's property", `function t($o) { $o->list[] = new D(); }`, []string{"param"}, 2},
		{"into a parameter array", `function t($p) { $p[] = new D(); return count($p); }`, []string{"param"}, 1},
		{"into a static property", `function t() { D::$s = new D(); }`, []string{"static"}, 0},
		{"returned directly", `function t() { return new D(); }`, []string{"return"}, 0},
		{"read back out and returned", `function t() { $a = [new D()]; return $a[0]; }`, []string{"return"}, 0},
		{"bound by reference into a returned array", `function t() { $d = new D(); $a = [&$d]; return $a; }`, []string{"return"}, 1},
		{"captured by a returned closure", `function t() { $d = new D(); return function () use ($d) {}; }`, []string{"return"}, 1},
		{"yielded inside an array", `function t() { yield [new D()]; }`, []string{"yield"}, 1},
		{"unpacked into a returned array", `function t() { $a = [new D()]; return [...$a]; }`, []string{"return"}, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, fn := traceOne(t, c.src, noOtherBody)
			o := siteOf(t, got, fn, "D")
			if !reflect.DeepEqual(o.Escapes, c.escapes) {
				t.Fatalf("escapes = %v, want %v\n%s", o.Escapes, c.escapes, fn)
			}
			if len(o.Via) != c.via {
				t.Fatalf("via = %v, want %d containers\n%s", o.Via, c.via, fn)
			}
		})
	}
}

// Moves and shares. A copy of an object handle is a second owner; a value that
// goes from one owner to the next and nowhere else is only ever moved.
func TestOwnershipTellsMovesFromShares(t *testing.T) {
	for _, c := range []struct {
		name   string
		src    string
		shared bool
	}{
		{"returned directly", `function t() { return new D(); }`, false},
		{"through one local", `function t() { $a = new D(); return $a; }`, false},
		{"into one array", `function t() { return [new D()]; }`, false},
		{"two elements are two objects", `function t() { return [new D(), new H(1)]; }`, false},
		// Promotion folds a copy that is not observable, so both holders are read.
		{"copied to a second local", `function t() { $a = new D(); $b = $a; return [$a, $b]; }`, true},
		{"in an array and a local", `function t() { $a = new D(); $arr = [$a]; return [$arr, $a]; }`, true},
		{"read back out of its array", `function t() { $arr = [new D()]; $x = $arr[0]; return $arr; }`, true},
		{"kept by a callee", `function t() { $a = new D(); f($a); return $a; }`, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			opts := noOtherBody
			opts.KeepsArg = func([]string, int) bool { return true }
			got, fn := traceOne(t, c.src, opts)
			o := siteOf(t, got, fn, "D")
			if o.Shared != c.shared {
				t.Fatalf("shared = %v (owners %d), want %v\n%s", o.Shared, o.Owners, c.shared, fn)
			}
		})
	}
}

// A constructor argument is contained in the constructed object. The shipping
// escape analysis once read it as the receiver, which freed it early.
func TestOwnershipContainsAConstructorArgument(t *testing.T) {
	src := `function t() { return new H(new D()); }`
	got, fn := traceOne(t, src, noOtherBody)
	if o := siteOf(t, got, fn, "D"); !o.Confined() {
		t.Fatalf("a constructor measured to keep nothing: want confined, got %v\n%s", o.Escapes, fn)
	}
	opts := noOtherBody
	opts.ArgKept = nil
	got, fn = traceOne(t, src, opts)
	o := siteOf(t, got, fn, "D")
	// Its own reason comes first, so the chain is empty -- and the holder's
	// return still reaches it through the containment edge.
	if !reflect.DeepEqual(o.Escapes, []string{"ctor.arg", "return"}) {
		t.Fatalf("with no summary: escapes %v, want [ctor.arg return]\n%s", o.Escapes, fn)
	}
}

// Every nil predicate is the safe answer.
func TestOwnershipDefaultsToEscape(t *testing.T) {
	for _, c := range []struct {
		name, src, reason string
	}{
		{"method receiver", `function t() { $a = new D(); $a->m(); }`, "method.this"},
		{"method argument", `function t($o) { $o->m(new D()); }`, "method.arg"},
		{"function argument", `function t() { f(new D()); }`, "call.arg"},
		{"constructor receiver", `function t() { new D(); }`, "ctor.this"},
		{"re-entering echo", `function t() { $a = new D(); echo $a; }`, "reenter:echo"},
		{"re-entering offsetGet", `function t() { $a = new D(); return $a[0] === 1; }`, "reenter:dim.get"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, fn := traceOne(t, c.src, lower.OwnershipOptions{})
			o := siteOf(t, got, fn, "D")
			if !contains(o.Escapes, c.reason) {
				t.Fatalf("escapes = %v, want %s among them\n%s", o.Escapes, c.reason, fn)
			}
		})
	}
}

// The file body's locals are globals.
func TestOwnershipFileBodyLocalsAreGlobals(t *testing.T) {
	fn := &lower.Function{Name: "{main}", Blocks: []*lower.Block{{
		ID: 0,
		Insts: []lower.Inst{
			{Result: &lower.Value{N: 0, Type: "*obj"}, Opcode: "new", Operands: []string{`"D"`}},
			{Opcode: lower.IntrinsicCall, Operands: []string{`"write_var"`}, ArgList: "($a, %0)"},
		},
		Term: "ret 1",
	}}}
	o := lower.TraceOwnership(fn, lower.OwnershipOptions{FileBody: true, CtorLeaksThis: noOtherBody.CtorLeaksThis})[0]
	if !reflect.DeepEqual(o.Escapes, []string{"global"}) {
		t.Fatalf("escapes = %v, want [global]", o.Escapes)
	}
}

// Anything the rules do not know is a sink, never a pass: an unrecognised
// intrinsic or opcode that mentions the object lets it out.
func TestOwnershipUnknownIsASink(t *testing.T) {
	for _, in := range []lower.Inst{
		{Opcode: lower.IntrinsicCall, Operands: []string{`"mystery"`}, ArgList: "(%0)"},
		{Opcode: "mystery_op", Operands: []string{"%0"}},
	} {
		fn := &lower.Function{Name: "t", Blocks: []*lower.Block{{
			ID: 0,
			Insts: []lower.Inst{
				{Result: &lower.Value{N: 0, Type: "*obj"}, Opcode: "new", Operands: []string{`"D"`}},
				in,
			},
			Term: "ret null",
		}}}
		o := lower.TraceOwnership(fn, noOtherBody)[0]
		if o.Confined() {
			t.Fatalf("%s: an unknown use must escape", in)
		}
	}
	// A string literal that happens to spell a value is not a use of it.
	fn := &lower.Function{Name: "t", Blocks: []*lower.Block{{
		ID: 0,
		Insts: []lower.Inst{
			{Result: &lower.Value{N: 0, Type: "*obj"}, Opcode: "new", Operands: []string{`"D"`}},
			{Opcode: lower.IntrinsicCall, Operands: []string{`"echo"`}, ArgList: `("%0 and $a, \"%0\"")`},
		},
		Term: "ret null",
	}}}
	if o := lower.TraceOwnership(fn, noOtherBody)[0]; !o.Confined() {
		t.Fatalf("a literal mentioning %%0 was read as a use: %v", o.Escapes)
	}
}

// Every intrinsic the lowering can emit has declared edges. The completeness is
// the point: an intrinsic missing here is silently a sink, and the analysis
// this replaced was wrong three times by missing an edge.
func TestOwnershipRulesAreComplete(t *testing.T) {
	rules := lower.OwnershipRuleNames()
	emitted := map[string]bool{}
	for _, s := range php.Catalogue() {
		for _, v := range s.Variants {
			emitted[v.Name] = true
		}
	}
	for _, op := range lower.IntrinsicOpcodes() {
		name, _ := lower.IntrinsicNameFor(op)
		if v, ok := php.GeneralVariant(name); ok {
			emitted[v.Name] = true
		} else {
			t.Errorf("intrinsicFor maps %s to %s, which the catalogue does not have", op, name)
		}
	}
	for name := range emitted {
		if !rules[name] {
			t.Errorf("intrinsic %q has no ownership rule", name)
		}
	}
	for name := range rules {
		if !emitted[name] {
			t.Errorf("ownership rule %q names no intrinsic", name)
		}
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

// kindSite picks the one site of a kind, for cases with a single array.
func kindSite(t *testing.T, got []lower.Ownership, fn *lower.Function, kind string) lower.Ownership {
	t.Helper()
	var found []lower.Ownership
	for _, o := range got {
		if o.Kind == kind {
			found = append(found, o)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want one %s site, got %v\n%s", kind, found, fn)
	}
	return found[0]
}

// An array is written when anything that shares its identity changes it in
// place. Building it is not a write; neither is replacing what a variable holds.
func TestOwnershipTracksWrites(t *testing.T) {
	for _, c := range []struct {
		name, src string
		written   bool
	}{
		{"built and counted", `function t($x) { $a = [$x, 2]; return count($a); }`, false},
		{"literal only read", `function t() { $a = [1, 2]; return $a[0]; }`, false},
		{"appended to", `function t($x) { $a = [1]; $a[] = $x; return $a; }`, true},
		{"written through a copy", `function t($x) { $a = [1]; $b = $a; $b[0] = $x; return [$a, $b]; }`, true},
		{"iterated by reference", `function t() { $a = [1]; foreach ($a as &$v) { $v = 2; } return $a; }`, true},
		{"sent by reference", `function t() { $a = [3, 1]; sort($a); return $a; }`, true},
		{"captured by reference", `function t() { $a = [1]; $f = function () use (&$a) {}; return $f; }`, true},
		{"replaced, not written", `function t($x) { $a = [$x]; if ($x) { $a = [2]; } return $a; }`, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			fn := lowerOne(t, "<?php "+c.src)
			var arrays []lower.Ownership
			for _, o := range lower.TraceOwnership(fn, noOtherBody) {
				if o.Kind == "literal" || o.Kind == "array" {
					arrays = append(arrays, o)
				}
			}
			if len(arrays) == 0 {
				t.Fatalf("no array site traced\n%s", fn)
			}
			// Constant propagation gives every use of a constant array its own
			// literal, so a case asks whether any site was written, not all.
			any := false
			for _, o := range arrays {
				any = any || o.Written
			}
			if any != c.written {
				t.Fatalf("written = %v, want %v: %+v\n%s", any, c.written, arrays, fn)
			}
		})
	}
}

// A store is a move only when the value is fresh, dies there, and nothing else
// holds it -- including another name for the same identity.
func TestOwnershipFindsMoves(t *testing.T) {
	fresh := noOtherBody
	fresh.FnReturnsFresh = func(names []string) bool { return names[0] == `"mk"` }
	for _, c := range []struct {
		name, src string
		moves     int
	}{
		{"literal into a local", `function t() { $a = [1, 2]; $a[] = 3; return $a; }`, 1},
		{"fresh call result", `function t() { $b = mk(); $b[] = 1; return $b; }`, 1},
		{"unknown call result", `function t() { $b = other(); $b[] = 1; return $b; }`, 0},
		{"element of another array", `function t($p) { $b = $p[0]; $b[] = 1; return $b; }`, 0},
		{"parameter", `function t($p) { $b = $p; $b[] = 1; return $b; }`, 0},
		{"still held by another local", `function t() { $a = mk(); $b = $a; $b[] = 1; return [$a, $b]; }`, 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			fn := lowerOne(t, "<?php function mk() { return [1]; } function other() {} "+c.src)
			tr := lower.Trace(fn, fresh)
			if len(tr.Moves) != c.moves {
				t.Fatalf("moves = %v, want %d\n%s", tr.Moves, c.moves, fn)
			}
		})
	}
}

// The alias check is what makes a move sound: a value that dies at the store
// may still have a second SSA name that is read afterwards.
func TestOwnershipMoveRefusesALiveAlias(t *testing.T) {
	v := func(n int) *lower.Value { return &lower.Value{N: n, Type: "zval"} }
	fn := &lower.Function{Name: "t", Blocks: []*lower.Block{{
		ID: 0,
		Insts: []lower.Inst{
			{Result: v(1), Opcode: lower.IntrinsicCall, Operands: []string{`"array.new"`}, ArgList: "()"},
			{Result: v(2), Opcode: lower.IntrinsicCall, Operands: []string{`"copy"`}, ArgList: "(%1)"},
			{Opcode: lower.IntrinsicCall, Operands: []string{`"write_var"`}, ArgList: "($x, %2)"},
			{Opcode: lower.IntrinsicCall, Operands: []string{`"dim.append"`}, ArgList: "($x, 1)"},
			{Result: v(3), Opcode: lower.IntrinsicCall, Operands: []string{`"count"`}, ArgList: "(%1)"},
		},
		Term: "ret %3",
	}}}
	if tr := lower.Trace(fn, noOtherBody); len(tr.Moves) != 0 {
		t.Fatalf("%%1 is read after the store, so %%2 is not the array's only name: %v", tr.Moves)
	}
}

// A loop-carried concatenation whose left operand dies is an append in place.
func TestOwnershipFindsInPlaceAppends(t *testing.T) {
	for _, c := range []struct {
		name, src string
		appends   bool
	}{
		{"loop-carried", `function t($n) { $s = ""; while ($n-- > 0) { $s .= "x"; } return strlen($s); }`, true},
		{"read inside the loop", `function t($n) { $s = ""; while ($n-- > 0) { echo $s; $s .= "x"; } return $s; }`, true},
		{"kept a copy", `function t($n) { $s = "a"; $t = $s; $s .= "x"; return [$s, $t]; }`, false},
		{"old value read after", `function t($a) { $s = $a . "y"; $u = $s . "x"; return [$s, $u]; }`, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			fn := lowerOne(t, "<?php "+c.src)
			tr := lower.Trace(fn, noOtherBody)
			if (len(tr.Appends) > 0) != c.appends {
				t.Fatalf("appends = %v, want any = %v\n%s", tr.Appends, c.appends, fn)
			}
		})
	}
}

// Whether a body hands back something nothing else holds.
func TestOwnershipReturnsFresh(t *testing.T) {
	for _, c := range []struct {
		name, src string
		fresh     bool
	}{
		{"built array", `function t($n) { $a = []; for ($i = 0; $i < $n; $i++) { $a[] = $i; } return $a; }`, true},
		{"literal", `function t() { return [1, 2]; }`, true},
		{"new object", `function t() { return new D(); }`, true},
		{"scalar", `function t($a) { return $a + 1; }`, true},
		{"parameter", `function t($a) { return $a; }`, false},
		{"stored in a static too", `function t($x) { $a = [$x]; D::$s = $a; return $a; }`, false},
		{"either fresh or a parameter", `function t($p, $c) { $a = $c ? [1] : $p; return $a; }`, false},
		{"a generator", `function t() { yield 1; return [1]; }`, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			fn := lowerOne(t, "<?php class D { static $s; } "+c.src)
			if got := lower.Trace(fn, noOtherBody).ReturnsFresh; got != c.fresh {
				t.Fatalf("returns fresh = %v, want %v\n%s", got, c.fresh, fn)
			}
		})
	}
}

// Whether a body changes a by-value parameter in place.
func TestOwnershipParamWritten(t *testing.T) {
	for _, c := range []struct {
		name, src string
		written   []bool
	}{
		{"read only", `function t($a, $b) { return count($a) + $b; }`, []bool{false, false}},
		{"appended to", `function t($a, $b) { $a[] = $b; return $a; }`, []bool{true, false}},
		{"element written", `function t($a) { $a["k"] = 1; return $a; }`, []bool{true}},
		{"iterated by value", `function t($a) { $s = 0; foreach ($a as $v) { $s += $v; } return $s; }`, []bool{false}},
		{"sorted", `function t($a) { sort($a); return $a; }`, []bool{true}},
		// Rebinding the variable to a call's result is not a write to either.
		{"rebound from a call", `function t($s) { $s = strtolower($s); return $s; }`, []bool{false}},
		{"rebound, then written", `function t($a) { $a = array_values($a); $a[] = 1; return $a; }`, []bool{true}},
	} {
		t.Run(c.name, func(t *testing.T) {
			fn := lowerOne(t, "<?php "+c.src)
			if got := lower.Trace(fn, noOtherBody).ParamWritten; !reflect.DeepEqual(got, c.written) {
				t.Fatalf("param written = %v, want %v\n%s", got, c.written, fn)
			}
		})
	}
}

// Every intrinsic that takes a place for writing, or changes a container, has
// to say so; a missing write makes an array look shareable while it is being
// changed.
func TestOwnershipWritingRulesDeclareWrites(t *testing.T) {
	for _, name := range []string{
		"dim.set", "dim.append", "prop.set", "dim.ref", "dim.ref.rw", "dim.ref.unset",
		"dim.append.ref", "prop.ref", "prop.ref.rw", "prop.ref.unset", "iter.new.ref",
		"iter.current.ref", "array.append", "array.set", "array.set.key", "array.unpack",
		"array.append.bound", "array.set.bound", "strbuf.append",
	} {
		if !lower.OwnershipRuleWrites(name) {
			t.Errorf("%s changes a container in place but declares no write", name)
		}
	}
}

// Storing into a container copies, so writing the stored copy through the
// container is not a write to the array that was stored. The first array each
// body allocates is `$a`.
func TestOwnershipContainerWritesAreNotWritesToWhatWasStored(t *testing.T) {
	for _, c := range []struct{ name, src string }{
		{"into a property", `class O { public $p; }
			function t($x) { $a = [$x]; $o = new O(); $o->p = $a; $o->p[] = 1; return count($a); }`},
		{"into an array", `function t($x) { $a = [$x]; $l = [$a]; $l[0][] = 1; return [count($a), $l]; }`},
	} {
		t.Run(c.name, func(t *testing.T) {
			fn := lowerOne(t, "<?php "+c.src)
			for _, o := range lower.TraceOwnership(fn, noOtherBody) {
				if o.Kind != "array" {
					continue
				}
				if o.Written {
					t.Fatalf("%s written through its container\n%s", o.Site, fn)
				}
				return
			}
			t.Fatalf("no array site\n%s", fn)
		})
	}
}

// A `preferref` send writes only when the callee's parameter is by reference.
// Every method call's arguments are `preferref`, so without an answer they all
// count as writes; with one that says "by value", none do.
func TestOwnershipPreferRefAsksTheCallee(t *testing.T) {
	src := `class O { function m($x) {} } function t($a) { $o = new O(); $o->m($a); return $a; }`
	fn := lowerOne(t, "<?php "+src)
	if got := lower.Trace(fn, noOtherBody).ParamWritten; !reflect.DeepEqual(got, []bool{true}) {
		t.Fatalf("with no by-reference answer the send must count as a write: %v\n%s", got, fn)
	}
	opts := noOtherBody
	opts.ByRefArg = func(lower.Inst, int) bool { return false }
	if got := lower.Trace(fn, opts).ParamWritten; !reflect.DeepEqual(got, []bool{false}) {
		t.Fatalf("a by-value parameter does not write the argument: %v\n%s", got, fn)
	}
	opts.ByRefArg = func(lower.Inst, int) bool { return true }
	if got := lower.Trace(fn, opts).ParamWritten; !reflect.DeepEqual(got, []bool{true}) {
		t.Fatalf("a by-reference parameter writes the argument: %v\n%s", got, fn)
	}
}

// A literal is shared -- built once -- only when nothing writes it in place and
// nothing carries it out of the frame; a copy of such a value is not made.
func TestOwnershipSharedLiterals(t *testing.T) {
	for _, c := range []struct {
		name, src      string
		shared, copies bool
	}{
		{"read in a loop", `function t($n) { for ($i = 0; $i < $n; $i++) { $x = [1, 2] ?: null; } }`, true, true},
		{"counted", `function t() { return count([1, 2, 3]); }`, true, false},
		{"returned", `function t() { return [1, 2]; }`, false, false},
		{"appended to", `function t() { $a = [1]; $a[] = 2; return count($a); }`, false, false},
		{"sorted", `function t() { $a = [2, 1]; sort($a); return $a[0]; }`, false, false},
		{"iterated by reference", `function t() { $a = [1]; foreach ($a as &$v) { $v++; } unset($v); return $a[0]; }`, false, false},
		{"stored in a static", `function t() { static $s; $s = [1]; return 1; }`, false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			fn := lowerOne(t, "<?php "+c.src)
			tr := lower.Trace(fn, noOtherBody)
			if (len(tr.SharedLiterals) > 0) != c.shared {
				t.Fatalf("shared literals = %v, want any = %v\n%s", tr.SharedLiterals, c.shared, fn)
			}
			if (len(tr.SharedCopies) > 0) != c.copies {
				t.Fatalf("shared copies = %v, want any = %v\n%s", tr.SharedCopies, c.copies, fn)
			}
		})
	}
}

// A store need not copy when the value's whole identity, destination included,
// is an array this body built that nothing writes and nothing carries out.
func TestOwnershipSharedStores(t *testing.T) {
	for _, c := range []struct {
		name, src string
		shared    bool
	}{
		{"ternary merge read only", `function t($n, $f) { for ($i = 0; $i < $n; ++$i) { $x = $f ? null : [1, 2]; } return 0; }`, true},
		{"ternary merge then written", `function t($f) { $x = $f ? [0] : [1, 2]; $x[] = 3; return count($x); }`, false},
		{"ternary merge then returned", `function t($f) { $x = $f ? [0] : [1, 2]; return $x; }`, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			fn := lowerOne(t, "<?php "+c.src)
			if got := len(lower.Trace(fn, noOtherBody).SharedStores) > 0; got != c.shared {
				t.Fatalf("shared stores = %v, want %v\n%s", got, c.shared, fn)
			}
		})
	}
}

// A parameter's share on the caller's array may be handed back at exit only
// when the holder it arrived as never leaves the frame as itself.
func TestOwnershipParamReleasable(t *testing.T) {
	for _, c := range []struct {
		name, src string
		want      []bool
	}{
		{"read only", `function t($a) { return count($a); }`, []bool{true}},
		{"iterated", `function t($a) { $s = 0; foreach ($a as $v) { $s += $v; } return $s; }`, []bool{true}},
		{"passed on", `function t($a) { return f($a); }`, []bool{true}},
		{"written", `function t($a) { $a[] = 1; return count($a); }`, []bool{true}},
		{"returned", `function t($a) { return $a; }`, []bool{false}},
		{"captured by reference", `function t($a) { return function () use (&$a) {}; }`, []bool{false}},
		{"a generator", `function t($a) { yield count($a); }`, []bool{false}},
	} {
		t.Run(c.name, func(t *testing.T) {
			fn := lowerOne(t, "<?php "+c.src)
			if got := lower.Trace(fn, lower.OwnershipOptions{}).ParamReleasable; !reflect.DeepEqual(got, c.want) {
				t.Fatalf("releasable = %v, want %v\n%s", got, c.want, fn)
			}
		})
	}
}
