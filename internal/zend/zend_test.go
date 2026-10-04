package zend_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moriyoshi/php2go/internal/zend"
)

// Zend keeps its globals in process-wide statics, so the whole package shares
// one engine rather than starting one per test.
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

func TestEvalRunsAfterRequestStartup(t *testing.T) {
	out, err := engine.Eval(`set_error_handler(function ($level, $message) {
		echo $message; return true;
	}, E_WARNING); addcslashes("z", "z..A"); restore_error_handler();`)
	if err != nil {
		t.Fatal(err)
	}
	want := "addcslashes(): Invalid '..'-range, '..'-range needs to be incrementing"
	if out != want {
		t.Fatalf("warning = %q, want %q", out, want)
	}
}

// The layout has to come from the linked build: znode_op's width changes with
// ZEND_USE_ABS_JMP_ADDR / ZEND_USE_ABS_CONST_ADDR, and decoding operands
// against the wrong width would be silently wrong rather than a crash.
func TestDetectLayout(t *testing.T) {
	layout := zend.DetectLayout()
	switch layout.SizeofZnodeOp {
	case 4:
		if layout.AbsoluteJumpAddresses || layout.AbsoluteConstantAddresses {
			t.Errorf("4-byte znode_op contradicts absolute addressing: %+v", layout)
		}
	case 8:
		if !layout.AbsoluteJumpAddresses && !layout.AbsoluteConstantAddresses {
			t.Errorf("8-byte znode_op implies absolute addressing: %+v", layout)
		}
	default:
		t.Fatalf("unexpected znode_op width %d", layout.SizeofZnodeOp)
	}
	if layout.SizeofZendOp <= layout.SizeofZnodeOp*3 {
		t.Errorf("zend_op should hold three operands plus fields: %+v", layout)
	}
}

func TestCompileFile(t *testing.T) {
	op, err := engine.CompileFile(filepath.Join("testdata", "basic.php"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(op.Filename, "basic.php") {
		t.Errorf("filename = %q", op.Filename)
	}
	if len(op.Oplines) == 0 {
		t.Fatal("no oplines produced")
	}

	// 6 * 7 is folded at compile time, so the assignment takes a constant and
	// no MUL survives. Asserting that keeps the test honest about compiling
	// rather than merely parsing.
	var opcodes []string
	for _, line := range op.Oplines {
		opcodes = append(opcodes, line.OpcodeName)
	}
	joined := strings.Join(opcodes, " ")
	for _, want := range []string{"ZEND_ASSIGN", "ZEND_ECHO", "ZEND_RETURN"} {
		if !strings.Contains(joined, want) {
			t.Errorf("opcode %s missing from: %s", want, joined)
		}
	}
	if strings.Contains(joined, "ZEND_MUL") {
		t.Errorf("constant expression was not folded: %s", joined)
	}

	// Every opline must carry a usable source line; the whole pipeline is
	// annotated from these.
	for _, line := range op.Oplines {
		if line.Line < op.LineStart || line.Line > op.LineEnd {
			t.Errorf("opline %d line %d outside %d-%d", line.Index, line.Line, op.LineStart, op.LineEnd)
		}
	}
	// The echo is on line 3 of the fixture.
	var sawLine3 bool
	for _, line := range op.Oplines {
		if line.OpcodeName == "ZEND_ECHO" && line.Line == 3 {
			sawLine3 = true
		}
	}
	if !sawLine3 {
		t.Errorf("echo not attributed to line 3: %+v", op.Oplines)
	}
}

func TestCompileHarvestsOpArrayDetail(t *testing.T) {
	op, err := engine.CompileFile(filepath.Join("testdata", "detail.php"))
	if err != nil {
		t.Fatal(err)
	}

	// Compiled variables, in declaration order, including the catch binding.
	want := []string{"name", "n", "pi", "e"}
	if len(op.VarNames) != len(want) {
		t.Fatalf("var names = %v, want %v", op.VarNames, want)
	}
	for i, name := range want {
		if op.VarNames[i] != name {
			t.Errorf("var %d = %q, want %q", i, op.VarNames[i], name)
		}
	}

	// Literals must carry real values, not just kinds.
	var sawString, sawInt, sawFloat bool
	for _, lit := range op.Literals {
		switch {
		case lit.Kind == "string" && lit.Str == "world":
			sawString = true
		case lit.Kind == "int" && lit.Int == 42:
			sawInt = true
		case lit.Kind == "float" && lit.Float == 3.5:
			sawFloat = true
		}
	}
	if !sawString || !sawInt || !sawFloat {
		t.Errorf("literals missing values: %+v", op.Literals)
	}

	if len(op.TryCatch) != 1 {
		t.Fatalf("try/catch = %+v, want one range", op.TryCatch)
	}
	tc := op.TryCatch[0]
	// try < catch < finally, and every handler inside the op_array.
	if !(tc.Try < tc.Catch && tc.Catch < tc.Finally && tc.Finally <= tc.FinallyEnd) {
		t.Errorf("try/catch offsets out of order: %+v", tc)
	}
	if int(tc.FinallyEnd) > len(op.Oplines) {
		t.Errorf("finally_end %d beyond %d oplines", tc.FinallyEnd, len(op.Oplines))
	}
}

// Top-level functions are bound into the engine during compilation rather than
// appearing in the file's op_array, and PHP 8 records their real parameter
// types — type information available without any inference.
func TestCompileUnitHarvestsFunctions(t *testing.T) {
	unit, err := engine.CompileUnit(filepath.Join("testdata", "functions.php"))
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]*zend.OpArray{}
	for _, fn := range unit.Functions {
		byName[fn.Name] = fn
	}

	greet, ok := byName["greet"]
	if !ok {
		t.Fatalf("greet not harvested, got %v", byName)
	}
	if greet.ReturnType != "string" {
		t.Errorf("greet return type = %q, want string", greet.ReturnType)
	}
	// $times has a default, so it is declared but not required.
	if greet.RequiredArgs != 1 {
		t.Errorf("greet required args = %d, want 1", greet.RequiredArgs)
	}
	if len(greet.Args) != 2 || greet.Args[0].Type != "string" || greet.Args[1].Type != "int" {
		t.Errorf("greet args = %+v", greet.Args)
	}

	tally, ok := byName["tally"]
	if !ok {
		t.Fatal("tally not harvested")
	}
	// num_args excludes the variadic parameter, so reading only num_args
	// entries silently drops it.
	if len(tally.Args) != 2 {
		t.Fatalf("tally args = %+v, want the variadic included", tally.Args)
	}
	if !tally.Args[0].ByRef {
		t.Errorf("array &$acc not reported by reference: %+v", tally.Args[0])
	}
	if !tally.Args[1].Variadic || tally.Args[1].Type != "int" {
		t.Errorf("variadic parameter = %+v", tally.Args[1])
	}
}

// Closures and arrow functions are nested in the enclosing op_array instead of
// being registered by name.
func TestCompileUnitHarvestsClosures(t *testing.T) {
	unit, err := engine.CompileUnit(filepath.Join("testdata", "functions.php"))
	if err != nil {
		t.Fatal(err)
	}
	if len(unit.File.Closures) != 2 {
		t.Fatalf("closures = %d, want 2", len(unit.File.Closures))
	}
	// The arrow function declares an int return type; the other does not.
	var typed int
	for _, closure := range unit.File.Closures {
		if closure.ReturnType == "int" {
			typed++
		}
		if len(closure.Oplines) == 0 {
			t.Errorf("closure %q has no oplines", closure.Name)
		}
	}
	if typed != 1 {
		t.Errorf("expected exactly one closure with an int return type, got %d", typed)
	}
}

// Compiling binds top-level functions into the engine's function table, where
// they would persist and make the next file declaring that name fail to
// compile. Real projects do declare a name in more than one file — conditional
// includes, vendored duplicates, alternative implementations — so harvesting
// has to leave the engine as it found it.
func TestCompileUnitLeavesEngineClean(t *testing.T) {
	path := filepath.Join("testdata", "functions.php")
	first, err := engine.CompileUnit(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := engine.CompileUnit(path)
	if err != nil {
		t.Fatalf("recompiling the same file failed, so state leaked: %v", err)
	}
	if len(first.Functions) != len(second.Functions) || len(first.Functions) == 0 {
		t.Fatalf("function counts differ across compiles: %d then %d",
			len(first.Functions), len(second.Functions))
	}
	for i := range first.Functions {
		if first.Functions[i].Name != second.Functions[i].Name {
			t.Errorf("function %d = %q then %q", i, first.Functions[i].Name, second.Functions[i].Name)
		}
	}
}

func TestCompileUnitHarvestsClasses(t *testing.T) {
	unit, err := engine.CompileUnit(filepath.Join("testdata", "classes.php"))
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]*zend.Class{}
	for _, class := range unit.Classes {
		byName[class.Name] = class
	}

	for name, kind := range map[string]string{
		"Nameable": "interface",
		"Loggable": "trait",
		"Base":     "class",
		"Model":    "class",
		"Status":   "enum",
	} {
		class, ok := byName[name]
		if !ok {
			t.Fatalf("%s not harvested, got %v", name, byName)
		}
		if class.Kind != kind {
			t.Errorf("%s kind = %q, want %q", name, class.Kind, kind)
		}
	}

	base := byName["Base"]
	if !base.Abstract || len(base.Interfaces) != 1 || base.Interfaces[0] != "Nameable" {
		t.Errorf("Base = %+v", base)
	}
	// Non-public names are mangled in properties_info; the declared name is
	// what callers need.
	props := map[string]zend.Property{}
	for _, p := range base.Properties {
		props[p.Name] = p
	}
	if p, ok := props["id"]; !ok || p.Type != "int" || p.Visibility != "protected" {
		t.Errorf("Base::$id = %+v (all props %+v)", p, base.Properties)
	}
	if p, ok := props["registry"]; !ok || p.Visibility != "private" || !p.Static {
		t.Errorf("Base::$registry = %+v", p)
	}
	if p, ok := props["slug"]; !ok || !p.Readonly {
		t.Errorf("Base::$slug = %+v", p)
	}

	model := byName["Model"]
	if model.Parent != "Base" || !model.Final {
		t.Errorf("Model parent/final = %q/%t", model.Parent, model.Final)
	}
	if len(model.Traits) != 1 || model.Traits[0] != "Loggable" {
		t.Errorf("Model traits = %v", model.Traits)
	}
	// Magic accessors mean a property read can run arbitrary code, so a class
	// carrying them cannot become plain struct field access.
	if !model.HasGet || !model.HasCall {
		t.Errorf("Model magic accessors not detected: %+v", model)
	}
	if base.HasGet || base.HasCall {
		t.Errorf("Base wrongly reports magic accessors: %+v", base)
	}
	if p, ok := propByName(model.Properties, "title"); !ok || p.Type != "?string" {
		t.Errorf("Model::$title = %+v", p)
	}

	status := byName["Status"]
	if status.EnumBackingType != "string" {
		t.Errorf("Status backing type = %q, want string", status.EnumBackingType)
	}
	if !base.Abstract || status.EnumBackingType == "" {
		t.Errorf("enum/abstract flags wrong")
	}
}

func propByName(props []zend.Property, name string) (zend.Property, bool) {
	for _, p := range props {
		if p.Name == name {
			return p, true
		}
	}
	return zend.Property{}, false
}

// Builtin signatures are read from the linked engine, not from the *.stub.php
// sources. The stubs say what PHP intends to build; the function table says
// what this build has, which is what a translation must match. Compiling a stub
// is not even possible — it redeclares functions the engine already provides.
func TestBuiltins(t *testing.T) {
	builtins, err := engine.Builtins()
	if err != nil {
		t.Fatal(err)
	}
	if len(builtins) < 100 {
		t.Fatalf("only %d builtins; the function table looks unpopulated", len(builtins))
	}
	byName := map[string]*zend.Builtin{}
	for _, fn := range builtins {
		byName[fn.Name] = fn
	}

	// A plain signature.
	repeat, ok := byName["str_repeat"]
	if !ok {
		t.Fatal("str_repeat missing")
	}
	if repeat.ReturnType != "string" || len(repeat.Args) != 2 || repeat.Args[0].Type != "string" {
		t.Errorf("str_repeat = %+v", repeat)
	}

	// A union return type, and optional parameters that are declared but not
	// required.
	encode, ok := byName["json_encode"]
	if !ok {
		t.Fatal("json_encode missing")
	}
	if encode.ReturnType != "string|false" {
		t.Errorf("json_encode return = %q, want string|false", encode.ReturnType)
	}
	if encode.RequiredArgs != 1 || len(encode.Args) != 3 {
		t.Errorf("json_encode arity = %d required of %d", encode.RequiredArgs, len(encode.Args))
	}

	// By-reference output parameters have to be visible: they cannot be
	// compiled as ordinary values.
	match, ok := byName["preg_match"]
	if !ok {
		t.Fatal("preg_match missing")
	}
	if len(match.Args) < 3 || !match.Args[2].ByRef {
		t.Errorf("preg_match $matches not reported by reference: %+v", match.Args)
	}

	// Variadics, which num_args excludes.
	printf, ok := byName["printf"]
	if !ok {
		t.Fatal("printf missing")
	}
	if !printf.Variadic || len(printf.Args) < 2 || !printf.Args[len(printf.Args)-1].Variadic {
		t.Errorf("printf variadic not reported: %+v", printf)
	}
}

func TestIniEntriesAndExtensions(t *testing.T) {
	entries, err := engine.IniEntries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) < 50 {
		t.Fatalf("only %d INI entries; the directive table looks unpopulated", len(entries))
	}
	byName := map[string]zend.IniEntry{}
	for _, entry := range entries {
		byName[entry.Name] = entry
	}
	separator, ok := byName["arg_separator.output"]
	if !ok || separator.LocalValue == nil || *separator.LocalValue != "&" || separator.Access == 0 {
		t.Errorf("arg_separator.output = %+v, present=%v", separator, ok)
	}
	tags, ok := byName["url_rewriter.tags"]
	if !ok || tags.Extension != "standard" {
		t.Errorf("url_rewriter.tags = %+v, present=%v", tags, ok)
	}
	stack, ok := byName["fiber.stack_size"]
	if !ok || stack.LocalValue != nil {
		t.Errorf("fiber.stack_size = %+v, present=%v; want nullable value", stack, ok)
	}

	extensions, err := engine.Extensions()
	if err != nil {
		t.Fatal(err)
	}
	loaded := map[string]bool{}
	for _, extension := range extensions {
		loaded[extension] = true
	}
	for _, want := range []string{"standard", "json"} {
		if !loaded[want] {
			t.Errorf("extension %q not harvested", want)
		}
	}
}

func TestOpcodeNames(t *testing.T) {
	names := zend.OpcodeNames()
	if len(names) != 256 {
		t.Fatalf("table length = %d, want 256", len(names))
	}
	var named int
	for _, name := range names {
		if name != "" {
			named++
		}
	}
	if named < 150 {
		t.Errorf("only %d opcodes named; the table looks truncated", named)
	}
	if names[1] != "ZEND_ADD" {
		t.Errorf("opcode 1 = %q, want ZEND_ADD", names[1])
	}
}

// Raw operands are addresses, not indices, and neither survives serialization:
// IS_CONST is a byte offset from the opline itself, so the same literal has a
// different operand in every instruction referencing it, and the variable kinds
// are frame offsets past a header whose size is a build detail. Consumers need
// indices, so harvesting normalizes them.
func TestOperandsAreIndices(t *testing.T) {
	op, err := engine.CompileFile(filepath.Join("testdata", "basic.php"))
	if err != nil {
		t.Fatal(err)
	}
	if len(op.Literals) < 2 || op.Literals[0].Kind != "int" || op.Literals[0].Int != 42 {
		t.Fatalf("fixture literals changed: %+v", op.Literals)
	}

	var checked int
	for _, line := range op.Oplines {
		for _, operand := range []struct {
			kind  uint8
			value uint32
			label string
		}{
			{line.Op1Type, line.Op1, "op1"},
			{line.Op2Type, line.Op2, "op2"},
		} {
			switch operand.kind {
			case OperandConst:
				if int(operand.value) >= len(op.Literals) {
					t.Errorf("%s %s = %d, outside the %d-entry literal table",
						line.OpcodeName, operand.label, operand.value, len(op.Literals))
				}
				checked++
			case OperandCV:
				if int(operand.value) >= len(op.VarNames) {
					t.Errorf("%s %s = %d, outside the %d compiled variables",
						line.OpcodeName, operand.label, operand.value, len(op.VarNames))
				}
				checked++
			}
		}
	}
	if checked == 0 {
		t.Fatal("no CONST or CV operands to check")
	}

	// $total = 42 must read as literal 0 into compiled variable 0.
	assign := op.Oplines[0]
	if assign.OpcodeName != "ZEND_ASSIGN" {
		t.Fatalf("first opline = %s", assign.OpcodeName)
	}
	if assign.Op1Type != OperandCV || assign.Op1 != 0 {
		t.Errorf("assign target = %d (type %d), want CV 0", assign.Op1, assign.Op1Type)
	}
	if assign.Op2Type != OperandConst || op.Literals[assign.Op2].Int != 42 {
		t.Errorf("assign source does not resolve to 42: operand %d", assign.Op2)
	}
}

// Operand type tags, mirrored from the engine so tests need not import cgo
// constants.
const (
	OperandUnused = 0
	OperandConst  = 1
	OperandTmp    = 2
	OperandCV     = 8
)

// Jump targets are byte offsets from the opline too, and a raw offset is
// meaningless once serialized. They are normalized to absolute indices, which
// makes the op_array a usable control-flow graph.
func TestJumpTargetsAreIndices(t *testing.T) {
	op, err := engine.CompileFile(filepath.Join("testdata", "loop.php"))
	if err != nil {
		t.Fatal(err)
	}

	var jumps int
	for i, line := range op.Oplines {
		var target uint32
		switch line.OpcodeName {
		case "ZEND_JMP":
			target = line.Op1
		case "ZEND_JMPZ", "ZEND_JMPNZ":
			target = line.Op2
		default:
			continue
		}
		jumps++
		if int(target) >= len(op.Oplines) {
			t.Errorf("opline %d (%s) jumps to %d, past the %d oplines",
				i, line.OpcodeName, target, len(op.Oplines))
		}
	}
	if jumps < 2 {
		t.Fatalf("fixture should contain a forward and a backward jump, found %d", jumps)
	}

	// The while loop must close: some jump has to go backwards.
	var backward bool
	for i, line := range op.Oplines {
		if line.OpcodeName == "ZEND_JMPNZ" && int(line.Op2) < i {
			backward = true
		}
	}
	if !backward {
		t.Error("no backward jump, so the loop does not close")
	}
}

// The engine fuses a comparison with the branch that follows it and records
// that on the result type. Masking the flag away without keeping it would lose
// information a consumer needs; leaving it in would corrupt the operand kind.
func TestSmartBranchIsRecordedSeparately(t *testing.T) {
	op, err := engine.CompileFile(filepath.Join("testdata", "loop.php"))
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, line := range op.Oplines {
		if line.OpcodeName != "ZEND_IS_SMALLER" {
			continue
		}
		found = true
		if line.SmartBranch == "" {
			t.Errorf("comparison feeding a branch not flagged: %+v", line)
		}
		// The kind must be a bare operand type, with no flag bits left in it.
		if line.ResultType != OperandTmp {
			t.Errorf("result type = %d, want a bare IS_TMP_VAR (%d)", line.ResultType, OperandTmp)
		}
		if int(line.Result) >= op.NumVars+int(op.NumTemps) {
			t.Errorf("result slot %d outside the frame", line.Result)
		}
	}
	if !found {
		t.Fatal("fixture has no comparison")
	}
}

// Harvesting must never run the project being harvested. A file that would
// print and write a marker is compiled; neither may happen.
func TestCompileDoesNotExecute(t *testing.T) {
	marker := filepath.Join("testdata", "executed.marker")
	os.Remove(marker)
	t.Cleanup(func() { os.Remove(marker) })

	if _, err := engine.CompileFile(filepath.Join("testdata", "sideeffect.php")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("compilation executed the script")
	}
}

func TestCompileMissingFile(t *testing.T) {
	if _, err := engine.CompileFile(filepath.Join("testdata", "does-not-exist.php")); err == nil {
		t.Fatal("expected an error for a missing file")
	}
}

// A second engine would share Zend's process-wide globals with the first.
func TestStartIsExclusive(t *testing.T) {
	if _, err := zend.Start(); err == nil {
		t.Fatal("expected the second engine to be refused")
	}
}

// A class declared by one file must be completely gone before the next file is
// compiled -- not merely removed from the class table, but dropped from the
// class-entry cache hanging off its interned name. zend_lookup_class_ex answers
// from that cache before it consults the table, so a stale slot hands the
// compiler a freed zend_class_entry and it dereferences it inside
// zend_can_early_bind. That was a segfault 216 files into CakePHP.
//
// The assertion is on the pointer, not on what the next compile makes of it. A
// stale slot is a use-after-free, and what the engine does with freed memory
// depends on what has since been allocated over it -- the same two files that
// crash inside a corpus run compile quietly on their own. So the invariant to
// hold is that no cached entry outlives the class table, which is exact and
// does not depend on the allocator.
func TestForgettingAClassAlsoDropsItsNameCache(t *testing.T) {
	dir := t.TempDir()
	parent := filepath.Join(dir, "parent.php")
	child := filepath.Join(dir, "child.php")
	write := func(path, src string) {
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The parent needs a parent of its own: the slot is filled by inheritance
	// linking, and a class with no parent never goes through it.
	write(parent, "<?php\nclass CacheProbeParent extends RuntimeException { protected string $t = ''; public function f(): int { return 1; } }\n")
	write(child, "<?php\nclass CacheProbeChild extends CacheProbeParent { public function g(): int { return 2; } }\n")

	if _, err := engine.CompileUnit(parent); err != nil {
		t.Fatal(err)
	}
	if n := zend.DanglingClassNameCaches(); n != 0 {
		t.Errorf("%d class names still cache a class entry the class table has dropped", n)
	}
	if _, err := engine.CompileUnit(child); err != nil {
		t.Fatal(err)
	}
	if n := zend.DanglingClassNameCaches(); n != 0 {
		t.Errorf("after the child, %d class names still cache a dropped class entry", n)
	}
}

// The just-in-time superglobals a file creates are read back from its compile,
// per file, in the order the compiler met them -- and a `$GLOBALS["_ENV"]`
// fetch, which compiles to the same opcode as `$_ENV`, creates nothing.
func TestCompileUnitReportsAutoGlobals(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		source string
		want   []string
	}{
		{`<?php function f() { return $_REQUEST; } $a = $_SERVER; $b = $_ENV; $c = $_SERVER;`,
			[]string{"_REQUEST", "_SERVER", "_ENV"}},
		{`<?php $x = $GLOBALS["_ENV"]; $n = "_SERVER"; $y = $$n; $z = $_GET;`, nil},
		{`<?php echo $_ENV["HOME"] ?? "";`, []string{"_ENV"}},
	}
	for i, c := range cases {
		path := filepath.Join(dir, "case"+string(rune('0'+i))+".php")
		if err := os.WriteFile(path, []byte(c.source), 0o644); err != nil {
			t.Fatal(err)
		}
		unit, err := engine.CompileUnit(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(unit.AutoGlobals, ",") != strings.Join(c.want, ",") {
			t.Errorf("case %d: AutoGlobals = %v, want %v", i, unit.AutoGlobals, c.want)
		}
	}
}
