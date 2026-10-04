package gogen_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/moriyoshi/php2go/internal/gogen"
	"github.com/moriyoshi/php2go/internal/ir"
	"github.com/moriyoshi/php2go/internal/lower"
	"github.com/moriyoshi/php2go/internal/zend"
)

func emitFixture(t *testing.T, name string) string {
	t.Helper()
	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	path, err := filepath.Abs(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	unit, err := engine.CompileUnit(path)
	if err != nil {
		t.Fatal(err)
	}
	frameless, err := engine.FramelessFunctions()
	if err != nil {
		t.Fatal(err)
	}
	lw := &lower.Lowerer{Names: zend.OpcodeNames(), Frameless: frameless}
	encoded := ir.EncodeUnit(unit)
	prog := &gogen.Program{}
	if f := encoded.GetFile(); f != nil {
		fn, err := lw.Lower(f)
		if err != nil {
			t.Fatal(err)
		}
		// The emitter consumes a *specialized* IR: operations have become
		// calls to intrinsics by the time it runs.
		lower.Specialize(fn, nil)
		prog.File = fn
	}
	for _, oa := range encoded.GetFunctions() {
		fn, err := lw.Lower(oa)
		if err != nil {
			t.Fatal(err)
		}
		// The emitter consumes a *specialized* IR: operations have become
		// calls to intrinsics by the time it runs.
		lower.Specialize(fn, nil)
		prog.Functions = append(prog.Functions, fn)
	}
	src, err := gogen.Emit(prog)
	if err != nil {
		t.Fatal(err)
	}
	return src
}

// A whole file emits: the top-level body becomes main, the functions become Go
// functions, and a call between them becomes a Go call. A function in isolation
// proves nothing, because nothing calls it and nothing runs.
//
// Whether the result is *correct* is not a question this test can answer —
// that is `php2gotest`, which builds the output and diffs it against the
// engine. This asserts only that the shape is there.
func TestAWholeFileEmits(t *testing.T) {
	src := emitFixture(t, "whole.php")
	for _, want := range []string{
		"func php_fact(ctx *php.Ctx, args []php.Value) (php.Value, error)",
		"func phpMain(ctx *php.Ctx, args []php.Value) (php.Value, error)",
		// A call to a program-defined function goes through `callUser`, which
		// records the stack frame PHP's traces need. The direct-call spelling
		// this used to assert stopped existing when uncaught-error reporting
		// landed -- the frame is not decoration, it is what makes
		// `#0 t.php(2): fact(5)` possible. `fact` keeps nothing of its
		// argument slice, so the call borrows one (callUserArgs).
		`callUserArgs(ctx, "fact", php_fact, []php.Value{`,
		"func main() {",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("emitted source lacks %q\n%s", want, src)
		}
	}
}

// Go rejects an unused label, so a block nothing jumps to must not get one.
// This is not style: it is the difference between output that compiles and
// output that does not, and it was found by Go rather than by any IR invariant.
func TestNoLabelOnAFallenIntoBlock(t *testing.T) {
	src := emitFixture(t, "whole.php")
	for _, line := range strings.Split(src, "\n") {
		l := strings.TrimSpace(line)
		if !strings.HasPrefix(l, "bb") || !strings.HasSuffix(l, ":") {
			continue
		}
		if !strings.Contains(src, "goto "+strings.TrimSuffix(l, ":")) {
			t.Errorf("label %q is emitted but nothing jumps to it", l)
		}
	}
}

// A call resolves three ways, and the middle one is the distinction
// QUALITY_GATE.md insists on.
//
//   - the program defines it            -> an ordinary call
//   - runtime/php provides it           -> callBuiltin
//   - the *engine* has it and we do not -> refused by name, a php2go gap
//   - nobody has it                     -> throws `Call to undefined function`,
//     because that is the program being wrong rather than php2go lacking
//     something, and PHP reports it at the call
//
// This replaces an assertion that `var_dump` is refused. It was true when
// runtime/php had no builtin surface and stopped being true when var_dump
// landed — the **fourth** refusal test in this package invalidated by the
// capability it was written against arriving. The pattern is worth naming: a
// test that pins a refusal has to state what would make it obsolete, and this
// one does by pinning the *classification* rather than one class's membership.
func TestACallResolvesByWhoActuallyHasTheFunction(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lib.php")
	src := "<?php\nfunction mine() { return 1; }\nmine();\nvar_dump(1);\nstrrev(\"x\");\nnope_fn();\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	unit, err := engine.CompileUnit(path)
	if err != nil {
		t.Fatal(err)
	}
	frameless, _ := engine.FramelessFunctions()
	lw := &lower.Lowerer{Names: zend.OpcodeNames(), Frameless: frameless}
	encoded := ir.EncodeUnit(unit)
	fn, err := lw.Lower(encoded.GetFile())
	if err != nil {
		t.Fatal(err)
	}
	// The emitter consumes a *specialized* IR: operations have become calls
	// to intrinsics by the time it runs. Skipping this leaves raw operators
	// it deliberately no longer understands.
	lower.Specialize(fn, nil)
	mine, err := lw.Lower(encoded.GetFunctions()[0])
	if err != nil {
		t.Fatal(err)
	}
	lower.Specialize(mine, nil)

	builtins := map[string]bool{"var_dump": true}
	engineFns := map[string]bool{"var_dump": true, "strrev": true}

	// `strrev` is the gap: the engine has it, runtime/php does not.
	_, err = gogen.Emit(&gogen.Program{
		File: fn, Functions: []*lower.Function{mine},
		Builtins: builtins, EngineFunctions: engineFns,
	})
	if err == nil {
		t.Fatal("a function the engine has and runtime/php lacks was not refused")
	}
	if !strings.Contains(err.Error(), "strrev") {
		t.Fatalf("refused, but not by name: %v", err)
	}

	// With that gap closed, the remaining unknown must *throw* rather than
	// refuse: nobody has `nope_fn`, so it is the program's error.
	engineFns["strrev"] = true
	builtins["strrev"] = true
	out, err := gogen.Emit(&gogen.Program{
		File: fn, Functions: []*lower.Function{mine},
		Builtins: builtins, EngineFunctions: engineFns,
	})
	if err != nil {
		t.Fatalf("a call nobody can satisfy was refused rather than thrown: %v", err)
	}
	// The *call* to the shared constructor, not the message text: the wording
	// belongs to runtime/php's diagnostics table, and asserting it here would
	// pin a copy of a string this package deliberately no longer owns.
	if !strings.Contains(out, `php.ThrowUndefinedFunction(`) {
		t.Errorf("no thrown error for a function that does not exist:\n%s", out)
	}
	if !strings.Contains(out, "php_mine(ctx") {
		t.Error("the program's own function was not called directly")
	}
	// The **calling scope** rides along, because one library name --
	// `property_exists` -- answers differently depending on it. What this
	// asserts is still the routing.
	if !strings.Contains(out, `"var_dump"`) || !strings.Contains(out, "callBuiltin(ctx, ") {
		t.Error("a builtin was not routed through the shared surface")
	}
}

func TestACatchOfAnUndeclaredClassIsRefused(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.php")
	src := "<?php\ntry { echo 1; } catch (NoSuchClassAnywhere $e) { echo 2; }\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	unit, err := engine.CompileUnit(path)
	if err != nil {
		t.Fatal(err)
	}
	frameless, _ := engine.FramelessFunctions()
	lw := &lower.Lowerer{Names: zend.OpcodeNames(), Frameless: frameless}
	fn, err := lw.Lower(ir.EncodeUnit(unit).GetFile())
	if err != nil {
		t.Fatal(err)
	}
	lower.Specialize(fn, nil)
	// The emitter consumes a *specialized* IR: operations have become calls
	// to intrinsics by the time it runs. Skipping this leaves raw operators
	// it deliberately no longer understands.
	lower.Specialize(fn, nil)
	// **Which undeclared class it is decides the answer**, and the test used to
	// assert the coarser rule that every one is refused.
	//
	// Measured: `catch (Nope $e)` on a class nobody declares simply does not
	// match -- PHP raises nothing and does not even autoload for it -- so
	// refusing there replaced an answer with a gap. Refusing is right only when
	// the *engine* has the class and this emitter does not, where a
	// never-matching arm would be a wrong answer.
	//
	// So the name is tested twice, against two different engine class lists.
	if _, err := gogen.Emit(&gogen.Program{
		File: fn, EngineClasses: []string{"NoSuchClassAnywhere"},
	}); err == nil {
		t.Fatal("emitted a catch of a class the engine has and this emitter does not")
	} else if !strings.Contains(err.Error(), "NoSuchClassAnywhere") {
		// Refused by *name*, so the gap is legible rather than generic.
		t.Fatalf("refused, but not by name: %v", err)
	}
	out, err := gogen.Emit(&gogen.Program{File: fn})
	if err != nil {
		t.Fatalf("refused a catch of a class nobody declares, which PHP answers: %v", err)
	}
	if !strings.Contains(out, "NoSuchClassAnywhere") {
		t.Errorf("the arm was emitted but does not name the class it tests:\n%s", out)
	}
}

// And a `catch` of an engine exception class is *not* refused.
//
// This assertion replaces one that pinned the opposite. `catch (Exception $e)`
// used to refuse, correctly, because runtime/php had no exception hierarchy; it
// has one now, so the refusal narrowed again — to a class neither the program
// nor the engine declares.
//
// A test that pins a refusal has to be revisited when the capability lands, and
// the failure is the notification. That is the second time in this file.
func TestACatchOfAnEngineExceptionClassIsEmitted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.php")
	src := "<?php\ntry { throw new TypeError(\"t\"); } catch (Error $e) { echo 2; }\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	unit, err := engine.CompileUnit(path)
	if err != nil {
		t.Fatal(err)
	}
	frameless, _ := engine.FramelessFunctions()
	lw := &lower.Lowerer{Names: zend.OpcodeNames(), Frameless: frameless}
	fn, err := lw.Lower(ir.EncodeUnit(unit).GetFile())
	if err != nil {
		t.Fatal(err)
	}
	lower.Specialize(fn, nil)
	// The emitter consumes a *specialized* IR: operations have become calls
	// to intrinsics by the time it runs. Skipping this leaves raw operators
	// it deliberately no longer understands.
	lower.Specialize(fn, nil)
	out, err := gogen.Emit(&gogen.Program{File: fn, Filename: "c.php"})
	if err != nil {
		t.Fatalf("refused a catch of an engine class: %v", err)
	}
	// The test spans both hierarchies rather than comparing names, because a
	// subclass matches a parent clause.
	if !strings.Contains(out, `instanceOf(`) {
		t.Errorf("no type test was emitted:\n%s", out)
	}
	if !strings.Contains(out, "php.Throw(") {
		t.Errorf("the throw does not go through the runtime's exception type:\n%s", out)
	}
}

func TestNonFiniteFloatsAreEmittedAsGoExpressions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "inf.php")
	if err := os.WriteFile(path, []byte("<?php\n$a = INF;\n$b = -INF;\n$c = NAN;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	unit, err := engine.CompileUnit(path)
	if err != nil {
		t.Fatal(err)
	}
	frameless, _ := engine.FramelessFunctions()
	lw := &lower.Lowerer{Names: zend.OpcodeNames(), Frameless: frameless}
	fn, err := lw.Lower(ir.EncodeUnit(unit).GetFile())
	if err != nil {
		t.Fatal(err)
	}
	lower.Specialize(fn, nil)
	// The emitter consumes a *specialized* IR: operations have become calls
	// to intrinsics by the time it runs. Skipping this leaves raw operators
	// it deliberately no longer understands.
	lower.Specialize(fn, nil)
	src, err := gogen.Emit(&gogen.Program{File: fn})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(src, "php.Float(+Inf)") || strings.Contains(src, "php.Float(NaN)") {
		t.Errorf("a non-finite float was emitted as a bare identifier:\n%s", src)
	}
	for _, want := range []string{"math.Inf(1)", "math.Inf(-1)", "math.NaN()"} {
		if !strings.Contains(src, want) {
			t.Errorf("emitted source lacks %q\n%s", want, src)
		}
	}
}

// emitSource lowers a snippet and emits it, reporting `name` as the file.
// emitSourceAllowingRefusal is emitSource for a program the emitter is expected
// to refuse: it returns whatever was emitted (empty, on a refusal) instead of
// failing the test, so a caller can assert on the *absence* of something in the
// output either way.
func emitSourceAllowingRefusal(t *testing.T, php, name string) (string, error) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "s.php")
	if err := os.WriteFile(path, []byte(php), 0o644); err != nil {
		t.Fatal(err)
	}
	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	unit, err := engine.CompileUnit(path)
	if err != nil {
		t.Fatal(err)
	}
	frameless, _ := engine.FramelessFunctions()
	lw := &lower.Lowerer{Names: zend.OpcodeNames(), Frameless: frameless}
	fn, err := lw.Lower(ir.EncodeUnit(unit).GetFile())
	if err != nil {
		return "", err
	}
	lower.Specialize(fn, nil)
	return gogen.Emit(&gogen.Program{File: fn, Filename: name})
}

func emitSource(t *testing.T, php, name string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "s.php")
	if err := os.WriteFile(path, []byte(php), 0o644); err != nil {
		t.Fatal(err)
	}
	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	unit, err := engine.CompileUnit(path)
	if err != nil {
		t.Fatal(err)
	}
	frameless, _ := engine.FramelessFunctions()
	lw := &lower.Lowerer{Names: zend.OpcodeNames(), Frameless: frameless}
	fn, err := lw.Lower(ir.EncodeUnit(unit).GetFile())
	if err != nil {
		t.Fatal(err)
	}
	lower.Specialize(fn, nil)
	// The emitter consumes a *specialized* IR: operations have become calls
	// to intrinsics by the time it runs. Skipping this leaves raw operators
	// it deliberately no longer understands.
	lower.Specialize(fn, nil)
	src, err := gogen.Emit(&gogen.Program{File: fn, Filename: name})
	if err != nil {
		t.Fatal(err)
	}
	return src
}

// emitFuncSource lowers a snippet and emits just the named function.
func emitFuncSource(t *testing.T, php, want, name string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "s.php")
	if err := os.WriteFile(path, []byte(php), 0o644); err != nil {
		t.Fatal(err)
	}
	engine, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	unit, err := engine.CompileUnit(path)
	if err != nil {
		t.Fatal(err)
	}
	frameless, _ := engine.FramelessFunctions()
	lw := &lower.Lowerer{Names: zend.OpcodeNames(), Frameless: frameless}
	prog := &gogen.Program{Filename: name}
	for _, oa := range ir.EncodeUnit(unit).GetFunctions() {
		if oa.GetName() != want {
			continue
		}
		fn, err := lw.Lower(oa)
		if err != nil {
			t.Fatal(err)
		}
		// The emitter consumes a *specialized* IR: operations have become
		// calls to intrinsics by the time it runs.
		lower.Specialize(fn, nil)
		prog.Functions = append(prog.Functions, fn)
	}
	src, err := gogen.Emit(prog)
	if err != nil {
		t.Fatal(err)
	}
	return src
}

// A diagnostic names the position it was raised at, and that position is graded
// byte for byte. The IR carries `.line` directives; dropping them leaves the
// position at whatever it last happened to be.
func TestLineDirectivesBecomeLocationUpdates(t *testing.T) {
	src := emitSource(t, "<?php\n$a = 1;\necho $a;\necho $b;\n", "/x/y.php")
	if !strings.Contains(src, `const srcFile = "/x/y.php"`) {
		t.Errorf("the emitted program does not carry its source name:\n%s", src)
	}
	for _, line := range []string{"ctx.SetLocation(srcFile, 2)", "ctx.SetLocation(srcFile, 4)"} {
		if !strings.Contains(src, line) {
			t.Errorf("emitted source lacks %q\n%s", line, src)
		}
	}
}

// Reading a never-written variable warns in PHP, and the mapping is exact
// rather than approximate: a PHP local starts unset, a Go var starts as the
// zero php.Value, and the zero Value is KindUndef — distinct from Null. So
// "still undef at the read" is precisely "PHP would warn here".
//
// A compiler temporary is written before it is read by construction and the
// program cannot name it, so warning about one would invent a diagnostic the
// engine never emits.
func TestAnUnsetReadWarnsButACompilerTemporaryDoesNot(t *testing.T) {
	src := emitSource(t, "<?php\necho $nope;\n", "/x/y.php")
	if !strings.Contains(src, `php.ReadVar(ctx, p_nope, "nope")`) {
		t.Errorf("a program variable is read without the unset check:\n%s", src)
	}
	// foreach invents `$__iterN` and `$__tN`; neither may be warned about. The
	// subject is a parameter rather than a literal because a constant array
	// literal is unrenderable from the IR text and is refused.
	iter := emitFuncSource(t, "<?php\nfunction f($a) { foreach ($a as $k => $v) { echo $k; } }\n", "f", "/x/y.php")
	for _, line := range strings.Split(iter, "\n") {
		if strings.Contains(line, "php.ReadVar(ctx, p_x_") {
			t.Errorf("a compiler temporary is checked for being unset: %s", line)
		}
	}
}

// `$q ?? 5` must not warn, and `echo $q` must.
//
// The engine compiles `??` to a single ZEND_COALESCE and suppression is
// intrinsic to that opcode — verified both halves: `$c = $q ?? 5;` prints 5
// silently, `echo $r;` warns. The lowering used to split it into an unmarked
// `copy` plus a branch, discarding the no-warn property, so a consumer that
// warns on undefined reads warned where PHP does not.
//
// Raised by the runtime session from the other side, before it could be
// mistaken for an emitter bug. It is a lowering defect: the `mode=` vocabulary
// already rode on the dim and dynvar operations and simply was not applied here.
func TestACoalesceReadDoesNotWarnButAnOrdinaryReadDoes(t *testing.T) {
	silent := emitSource(t, "<?php\n$c = $q ?? 5;\n", "/x/y.php")
	if strings.Contains(silent, `php.ReadVar(ctx, p_q`) {
		t.Errorf("a coalesce read goes through the undefined-variable check:\n%s", silent)
	}
	loud := emitSource(t, "<?php\necho $q;\n", "/x/y.php")
	if !strings.Contains(loud, `php.ReadVar(ctx, p_q, "q")`) {
		t.Errorf("an ordinary read skips the undefined-variable check:\n%s", loud)
	}
}

// Every place a generated program reads must go through the undefined check,
// unless the instruction is marked silent.
//
// This is a structural test rather than a per-opcode one on purpose. The
// runtime session's finding was that "reads a local" cannot be recognised by
// which helper an emitter happens to route through: some opcodes consume a
// place with no intervening read at all (`$e++`, `$f .= "x"`), and one opcode
// looks exactly like a read and is not (the `copy` a coalesce lowers to). So
// the rule has to be enforced over the output rather than remembered at each
// call site — otherwise the next opcode implemented reintroduces the hole.
//
// The engine's answers, probed: `$e++`, `$f .= "x"` and `$g += 1` all warn on
// an undefined name; `$q ?? 5` does not.
// goStringLiteral matches an interpreted or raw Go string literal.
var goStringLiteral = regexp.MustCompile("\"(?:[^\"\\\\]|\\\\.)*\"|`[^`]*`")

func TestEveryPlaceReadIsCheckedUnlessMarkedSilent(t *testing.T) {
	src := emitSource(t, "<?php\n$f .= \"x\";\n$g = $g + 1;\necho $h;\n$i = $j ?? 5;\n", "/x/y.php")
	for _, line := range strings.Split(src, "\n") {
		l := strings.TrimSpace(line)
		// Only look at emitted statements, not the declarations or the helper.
		if !strings.Contains(l, "p_") || strings.HasPrefix(l, "var ") ||
			strings.HasPrefix(l, "_ = ") || strings.Contains(l, "func ") {
			continue
		}
		// Strip assignment targets: `p_f = ...` writes p_f, it does not read it.
		//
		// A write is also spelled `php.WriteVar(&p_f, ...)` since references
		// landed -- a local holding a `&` binding must be written *through*, so
		// the write stopped being a Go assignment. Missing that form made every
		// such write look like an unchecked read.
		rhs := l
		if i := strings.Index(l, " = "); i >= 0 {
			rhs = l[i+3:]
		}
		for {
			i := strings.Index(rhs, "php.WriteVar(&p_")
			if i < 0 {
				break
			}
			j := strings.Index(rhs[i:], ",")
			if j < 0 {
				break
			}
			rhs = rhs[:i] + rhs[i+j:]
		}
		// An **address** is never a read: `php.StoreVar(&p_f, v3)` writes the
		// place. Removing the `&p_` mentions before scanning is what keeps this
		// check about reads now that a store is a call taking a pointer rather
		// than an assignment with the place on the left.
		rhs = strings.ReplaceAll(rhs, "&p_", "&")
		// **A string literal is not a place.** `"__php_incomplete_class"`
		// in an emitted table spells `p_` without naming a local, as a
		// comment once did with `php_user_filter`.
		rhs = goStringLiteral.ReplaceAllString(rhs, `""`)
		for _, m := range strings.Split(rhs, "p_")[1:] {
			name := m
			if i := strings.IndexAny(name, ", )\t"); i >= 0 {
				name = name[:i]
			}
			// p_j is the coalesce read, which must NOT be checked.
			if name == "j" {
				continue
			}
			// A compiler temporary (`$__tN`, mangled to `p_x_tN`) is written
			// before it is read by construction and the program cannot name it,
			// so warning about one would invent a diagnostic PHP never emits.
			if strings.HasPrefix(name, "x_") {
				continue
			}
			if !strings.Contains(rhs, "php.ReadVar(ctx, p_"+name) {
				t.Errorf("place p_%s is read without the undefined check: %s", name, l)
			}
		}
	}
	// And the silent one really is silent.
	if strings.Contains(src, "php.ReadVar(ctx, p_j") {
		t.Errorf("the coalesce read is checked:\n%s", src)
	}
}

// Diagnostics the engine emitted while *compiling* are replayed before the
// program's own output.
//
// `continue` targeting a switch warns at compile time — the enclosing function
// is never called and the warning still appears — so a script emits it before
// its first statement runs. An AOT compiler performs that compile at build
// time, and php2go is unusually placed to reproduce it: the engine prints the
// text while we harvest, and the harvest already captures it as
// `Unit.compile_diagnostic`. It was being discarded.
func TestCompileTimeDiagnosticsArePrintedFirst(t *testing.T) {
	src, err := gogen.Emit(&gogen.Program{
		Filename:          "/x/y.php",
		CompileDiagnostic: "\nWarning: something in /x/y.php on line 5\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(src, `const compileDiagnostic = "\nWarning: something in /x/y.php on line 5\n"`) {
		t.Errorf("the compile diagnostic is not carried into the program:\n%s", src)
	}
	body := src[strings.Index(src, "func main() {"):]
	write := strings.Index(body, "ctx.Write(compileDiagnostic)")
	if write < 0 {
		t.Fatalf("the compile diagnostic is never written:\n%s", body)
	}
	// Before the file body runs, or it lands after output the engine put after it.
	if call := strings.Index(body, "phpMain(ctx"); call >= 0 && call < write {
		t.Error("the compile diagnostic is written after the program body runs")
	}
}

// The build machine's directory layout must not reach the artifact.
//
// The engine canonicalises: compiling `x.php`, `/abs/x.php` and `a/../x.php`
// all put the same absolute path in the compile-time diagnostic, so it cannot
// be steered by how the file is named on the way in. Emitted verbatim, that
// path becomes a string constant in the shipped binary — which is disclosure,
// not merely a question about which path is truthful. Go has the same problem
// and answers it with `-trimpath`.
//
// Byte-exact grading does not force the absolute path, because the harness
// controls *both* arms and rewrites them together.
//
// Raised by the runtime session, who checked the canonicalisation claim
// themselves rather than taking the caveat as written.
func TestNoBuildMachinePathReachesTheArtifact(t *testing.T) {
	const build = "/home/someone/private/project/src/app.php"
	const reported = "src/app.php"

	diag := "\nWarning: something in " + build + " on line 5\n"
	src, err := gogen.Emit(&gogen.Program{
		Filename:          reported,
		CompileDiagnostic: gogen.TrimBuildPath(diag, build, reported),
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(src, build) {
		t.Errorf("the build path reached the emitted program:\n%s", src)
	}
	if strings.Contains(src, "/home/someone") {
		t.Error("part of the build machine's layout reached the emitted program")
	}
	// It is rewritten, not merely dropped: the diagnostic still has to name the
	// source file, or it stops being actionable for anyone holding the source.
	if !strings.Contains(src, "Warning: something in "+reported+" on line 5") {
		t.Errorf("the diagnostic lost the source name entirely:\n%s", src)
	}
	if !strings.Contains(src, `const srcFile = "`+reported+`"`) {
		t.Errorf("runtime diagnostics do not use the reported path:\n%s", src)
	}
}

// A source file above the compilation root must be reported as `../x.php`, not
// as an absolute path.
//
// The first version of the trimming fell back to the absolute path whenever
// `filepath.Rel` produced a `..` prefix, which reintroduced exactly the
// disclosure it existed to prevent — and the original guard did not catch it,
// because that guard only exercised a file *inside* the root. A guard that
// covers the happy path is the comment-shaped kind one level in.
//
// The `../` is honest rather than a defect: it says the project is shaped that
// way.
func TestAFileAboveTheRootIsReportedRelativelyNotAbsolutely(t *testing.T) {
	const build = "/home/someone/private/other/app.php"
	const reported = "../other/app.php"

	diag := "\nWarning: something in " + build + " on line 5\n"
	src, err := gogen.Emit(&gogen.Program{
		Filename:          reported,
		RootPolicy:        "entry-dir",
		CompileDiagnostic: gogen.TrimBuildPath(diag, build, reported),
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(src, "/home/someone") {
		t.Errorf("a file above the root leaked the build path:\n%s", src)
	}
	if !strings.Contains(src, `const srcFile = "../other/app.php"`) {
		t.Errorf("the ../ form was not used:\n%s", src)
	}
}

// The artifact records how its paths should be read, but never the root itself.
//
// Without the policy the paths are relative to something the reader cannot
// recover, and two artifacts cannot be compared without guessing. Recording the
// root instead of the policy would undo the whole point, since the root is a
// build-machine path.
func TestTheArtifactRecordsTheRootPolicyAndNotTheRoot(t *testing.T) {
	src, err := gogen.Emit(&gogen.Program{
		Filename: "src/app.php", RootPolicy: "explicit", EntryFile: "src/app.php",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(src, `const sourceRootPolicy = "explicit"`) {
		t.Errorf("the artifact does not say how its paths should be read:\n%s", src)
	}
	// Under an explicit root the policy alone says only that *some* root was
	// chosen. The entry's root-relative path pins what the paths hang off, and
	// leaks nothing because it is already relative.
	if !strings.Contains(src, `const sourceEntry = "src/app.php"`) {
		t.Errorf("the artifact does not pin what its paths hang off:\n%s", src)
	}
	// The root itself must not appear. Recording it would re-disclose the build
	// path and, worse, make two checkouts in different directories produce
	// different artifacts.
	for _, leak := range []string{"/home/", "/Users/", "/build/"} {
		if strings.Contains(src, leak) {
			t.Errorf("an absolute root reached the artifact (%q):\n%s", leak, src)
		}
	}
}

// DefaultRoot is a function of the input set, not of the filesystem or the
// working directory.
func TestDefaultRootIsTheEntryDirectoryAndNotDiscovered(t *testing.T) {
	dir := t.TempDir()
	deep := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	// A marker a discovery heuristic would latch onto. It must make no
	// difference: the artifact may not depend on filesystem context outside the
	// compiled source.
	if err := os.WriteFile(filepath.Join(dir, "composer.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(deep, "main.php")
	if got := gogen.DefaultRoot([]string{entry}); got != deep {
		t.Errorf("DefaultRoot = %q, want the entry's directory %q", got, deep)
	}
	// Stable under adding inputs, which the longest-common-prefix alternative
	// is not.
	more := []string{entry, filepath.Join(dir, "other.php")}
	if got := gogen.DefaultRoot(more); got != deep {
		t.Errorf("DefaultRoot moved when an input was added: %q, want %q", got, deep)
	}
}

// A format verb that reaches the emitted program must survive `e.p`.
//
// `e.p` runs its own Sprintf, so a `%s` meant for the *generated* code has to be
// written `%%s`. Getting that wrong produces `%!!(string=x)s(MISSING)` at run
// time rather than a build failure, so nothing catches it until a diagnostic
// fires — and it has now happened four times: the class-not-found message, the
// static-property message, the class-constant messages, and the dynamic-call
// gap report.
//
// The check is cheap because the symptom is unmistakable: Go's own "bad format"
// marker cannot legitimately appear in emitted source.
func TestNoMangledFormatVerbsInEmittedSource(t *testing.T) {
	src := emitSource(t, "<?php\n$a = 1;\necho $a;\n", "/x/y.php")
	for _, bad := range []string{"%!", "(MISSING)", "(EXTRA "} {
		if strings.Contains(src, bad) {
			for _, line := range strings.Split(src, "\n") {
				if strings.Contains(line, bad) {
					t.Errorf("emitted source contains %q, a mangled format verb: %s", bad, line)
				}
			}
		}
	}
}

// TestANonIdentifierVariableIsNamedWhereverItAppears.
//
// A PHP variable name is an arbitrary byte string. `${"\0"}` is legal, and for
// a long time it was **refused**: the place scanner only found
// `$[A-Za-z0-9_]+`, so such a name was never declared and referring to one
// produced `undefined: p_u0000` at Go build time.
//
// The IR now spells it `${"\0"}` -- quoted, so a newline, a space or a quote in
// the middle of a name cannot end the operand early -- and the scanner matches
// that form before it blanks string literals, which would otherwise erase the
// name it is looking for.
//
// Both arms are here for the same reason they were before: an operand can live
// in `Inst.Operands` or in `ArgList`, and a rule keyed on one of the two is a
// rule with a hole in it. Reading an undefined variable puts the name in the
// argument list and nowhere else.
func TestANonIdentifierVariableIsNamedWhereverItAppears(t *testing.T) {
	for _, tc := range []struct{ name, php string }{
		{"in an intrinsic call", "<?php\necho ${\"\\0\"} . \"x\";\n"},
		{"in a call argument", "<?php\nfunction f($v) { return $v; }\nf(${\"\\0\"});\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src, err := emitSourceAllowingRefusal(t, tc.php, "/x/y.php")
			if err != nil {
				t.Fatalf("refused a variable this emitter can now name: %v", err)
			}
			// **Declared as well as used.** Checking only that the mangled name
			// appears would pass on the build failure this test exists to
			// prevent, where it appears in a reference and nowhere else.
			if !strings.Contains(src, "var p_u0000 ") {
				t.Errorf("place not declared:\n%s", src)
			}
			if strings.Count(src, "p_u0000") < 2 {
				t.Errorf("place declared but never used:\n%s", src)
			}
		})
	}
}
