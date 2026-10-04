package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moriyoshi/php2go/internal/zend"
	"github.com/moriyoshi/php2go/runtime/hybrid"
	"github.com/moriyoshi/php2go/runtime/php"
)

func TestEmittedHybridRunsUnseenEvalString(t *testing.T) {
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	previousDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(previousDirectory)

	staticPath := filepath.Join(t.TempDir(), "static.php")
	if err := os.WriteFile(staticPath, []byte("<?php echo 'lowered';\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source := fmt.Sprintf(`<?php
function showLocal() {
    $choice = getenv('P2G_HYBRID_CHOICE');
    $local = 'before';
    eval('$local .= ' . var_export($choice, true) . ';');
    echo $local;
    $newName = 'fresh';
    eval('$fresh = ' . var_export($choice, true) . ';');
    echo $$newName;
    $path = getenv('P2G_HYBRID_INCLUDE');
    if ($path) {
        $first = include_once $path;
        $second = include_once $path;
        $third = include $path;
        $fromName = 'fromFile';
        echo $first, $second, $third, $local, $$fromName;
    }
    if ($choice === 'B') {
        $bad = getenv('P2G_HYBRID_BAD');
        try { eval($bad); } catch (ParseError $e) { echo 'caught'; }
    }
}

function generated_hybrid_value($message) { return $message; }
eval("echo generated_hybrid_value(message: 'G');");
class HybridThis { function identify() { $code = 'echo get_class($this)' . getenv('P2G_HYBRID_THIS_SUFFIX'); eval($code); } }
(new HybridThis())->identify();
showLocal();
$top = 'before';
eval('$top .= ' . var_export(getenv('P2G_HYBRID_CHOICE'), true) . ';');
echo $top;
$topName = 'added';
eval('$added = ' . var_export(getenv('P2G_HYBRID_CHOICE'), true) . ';');
echo $$topName;
include %q;
`, staticPath)
	path := filepath.Join(t.TempDir(), "case.php")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	includePath := filepath.Join(t.TempDir(), "dynamic.php")
	if err := os.WriteFile(includePath, []byte("<?php $local .= 'I'; $fromFile = 'F'; return 'R';\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("P2G_HYBRID_INCLUDE", "")
	oldEmit, oldRoot := emitDir, sourceRoot
	emitDir, sourceRoot = t.TempDir(), repo
	defer func() { emitDir, sourceRoot = oldEmit, oldRoot }()
	if err := run([]string{path}, 0, false, ""); err != nil {
		t.Fatal(err)
	}
	module := filepath.Join(emitDir, sanitize(path))
	hybridBinary := filepath.Join(t.TempDir(), "hybrid")
	build := exec.Command("go", "build", "-tags", "php2go_hybrid", "-o", hybridBinary, ".")
	build.Dir = module
	build.Env = append(os.Environ(), "GOWORK=off")
	if log, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building emitted hybrid module: %v\n%s", err, log)
	}
	// A second build without the tag must keep the ordinary compiled path.
	ordinary := filepath.Join(t.TempDir(), "ordinary")
	plainBuild := exec.Command("go", "build", "-o", ordinary, ".")
	plainBuild.Dir = module
	plainBuild.Env = append(os.Environ(), "GOWORK=off")
	if log, err := plainBuild.CombinedOutput(); err != nil {
		t.Fatalf("building ordinary emitted module: %v\n%s", err, log)
	}
	for _, choice := range []string{"A", "B"} {
		if choice == "B" {
			if err := os.WriteFile(staticPath, []byte("<?php echo 'interpreted';\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		t.Setenv("P2G_HYBRID_CHOICE", choice)
		t.Setenv("P2G_HYBRID_THIS_SUFFIX", ";")
		t.Setenv("P2G_HYBRID_BAD", ")")
		t.Setenv("P2G_HYBRID_INCLUDE", includePath)
		runHybrid := exec.Command(hybridBinary)
		runHybrid.Env = append(os.Environ(), "P2G_HYBRID_CHOICE="+choice, "P2G_HYBRID_BAD=)", "P2G_HYBRID_INCLUDE="+includePath, "P2G_HYBRID_THIS_SUFFIX=;")
		got, err := runHybrid.CombinedOutput()
		if err != nil {
			t.Fatalf("running emitted hybrid with %s: %v\n%s", choice, err, got)
		}
		engine, err := zend.Start()
		if err != nil {
			t.Fatal(err)
		}
		want, evalErr := engine.EvalAs("     "+source[5:], path)
		engine.Close()
		if evalErr != nil {
			t.Fatal(evalErr)
		}
		if string(got) != want {
			t.Fatalf("emitted hybrid with %s = %q, Zend = %q", choice, got, want)
		}
	}
	plainRun := exec.Command(ordinary)
	plainRun.Env = append(os.Environ(), "P2G_HYBRID_CHOICE=B")
	output, err := plainRun.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 3 || !strings.Contains(string(output), "unsupported: include or eval") {
		t.Fatalf("ordinary emitted eval = %q, %v; want unsupported exit 3", output, err)
	}
}

func TestHybridGradingInstallsEmittedBridge(t *testing.T) {
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	mod := fmt.Sprintf("module hybridgrading\n\ngo 1.21\n\nrequire github.com/moriyoshi/php2go v0.0.0\n\nreplace github.com/moriyoshi/php2go => %s\n", repo)
	if err := os.WriteFile(filepath.Join(work, "go.mod"), []byte(mod), 0o644); err != nil {
		t.Fatal(err)
	}
	seed := "package main\n\nimport \"github.com/moriyoshi/php2go/runtime/php\"\n\nfunc main() { _ = php.Null() }\n"
	if err := os.WriteFile(filepath.Join(work, "main.go"), []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	seed = "//go:build php2go_hybrid\n\npackage main\n\nimport \"github.com/moriyoshi/php2go/runtime/hybrid\"\n\nvar _ = hybrid.EvalScoped\n"
	if err := os.WriteFile(filepath.Join(work, "hybrid_seed.go"), []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = work
	tidy.Env = buildEnv()
	if output, err := tidy.CombinedOutput(); err != nil {
		t.Fatalf("tidying hybrid grading module: %v\n%s", err, output)
	}
	source := `package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/moriyoshi/php2go/runtime/php"
)

var evalRuntimeString = func(_ *php.Ctx, _ string, _ map[string]*php.Value, _ *[]string) (php.Value, error) {
	return php.Value{}, php.Unsupported("include or eval")
}
var includeRuntimeFile = func(_ *php.Ctx, _, _ string, _ map[string]*php.Value, _ *[]string) (php.Value, error) {
	return php.Value{}, php.Unsupported("include or eval")
}

func generated_function(_ *php.Ctx, _ []php.Value) (php.Value, error) { return php.String("from Go"), nil }
var userFuncs = map[string]func(*php.Ctx, []php.Value) (php.Value, error){"generated_function": generated_function}
var hybridFuncOrder = []string{"generated_function"}
var hybridFuncParams = map[string][]string{"generated_function": {"message"}}
var hybridFuncByRef = map[string][]bool{"generated_function": {false}}
var hybridFuncVariadic = map[string]int{"generated_function": -1}
var hybridFuncRequired = map[string]int{"generated_function": 1}

func dispatchHybridUserFunction(ctx *php.Ctx, name string, args []php.Value) (php.Value, error) {
	fn, ok := userFuncs[strings.ToLower(name)]
	if !ok { return php.Undef(), php.Unsupported("hybrid eval function %s", name) }
	file, line := ctx.Location()
	ctx.PushCall(php.StackFrame{Function: name, File: file, Line: line, Args: args})
	defer ctx.PopCall()
	return fn(ctx, args)
}

func main() {
	var output strings.Builder
	ctx := php.NewCtx(&output)
	value := php.String("local")
	vars := map[string]*php.Value{"value": &value}
	var order []string
	if _, err := evalRuntimeString(ctx, "echo $value; echo generated_function(message: 'from Go'); debug_zval_dump(1);", vars, &order); err != nil {
		fmt.Fprintln(os.Stderr, "php2go unsupported: "+err.Error())
		os.Exit(3)
	}
	fmt.Print(output.String())
}
`
	oldEmit, oldHybrid := emitDir, hybridGrading
	emitDir, hybridGrading = "", true
	defer func() { emitDir, hybridGrading = oldEmit, oldHybrid }()

	oracle, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, err := oracle.EvalAs("function generated_function($message) { return $message; } $value = 'local'; echo $value; echo generated_function(message: 'from Go'); debug_zval_dump(1);", "case.php")
	oracle.Close()
	if err != nil {
		t.Fatal(err)
	}
	got, reason := buildAndRun(source, work, filepath.Join(work, "case.php"), "case.php", nil, want, "")
	if got != passed {
		t.Fatalf("tagged grading got %s (%s), want passed", got, reason)
	}
}

func TestHybridScopedEvalRefusalIsUnsupported(t *testing.T) {
	ctx := php.NewCtx(io.Discard)
	if _, err := hybrid.EvalScoped(ctx, "new stdClass();", nil, nil); !php.IsUnsupported(err) {
		t.Fatalf("hybrid eval refusal = %v, want a php.UnsupportedError", err)
	}
}

func TestHybridScopedEvalCallsGeneratedFunction(t *testing.T) {
	var output strings.Builder
	ctx := php.NewCtx(&output)
	dispatch := func(ctx *php.Ctx, name string, args []php.Value) (php.Value, error) {
		if name != "generated_function" || len(args) != 1 || args[0].String() != "from Go" {
			t.Fatalf("generated function dispatch = %s(%d args)", name, len(args))
		}
		return args[0], nil
	}
	if _, err := hybrid.EvalScopedWithFunctions(ctx, "echo generated_function(value: 'from Go');", nil, nil,
		[]string{"generated_function"}, map[string]hybrid.FunctionSignature{
			"generated_function": {Parameters: []hybrid.FunctionParameter{{Name: "value"}}, Required: 1},
		}, dispatch); err != nil {
		t.Fatalf("hybrid eval calling generated function: %v", err)
	}

	oracle, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, err := oracle.EvalAs("function generated_function($value) { return $value; } eval(\"echo generated_function(value: 'from Go');\");", "case.php")
	oracle.Close()
	if err != nil {
		t.Fatal(err)
	}
	if got := output.String(); got != want {
		t.Fatalf("hybrid eval output = %q, Zend = %q", got, want)
	}
}

func TestHybridScopedEvalFunctionPersistsAcrossCalls(t *testing.T) {
	var output strings.Builder
	ctx := php.NewCtx(&output)
	first := "if (true) { function hybrid_eval_fn($value) { return $value . '!'; } } echo hybrid_eval_fn('A');"
	second := "echo hybrid_eval_fn('B');"
	for _, source := range []string{first, second} {
		if _, err := hybrid.EvalScoped(ctx, source, nil, nil); err != nil {
			t.Fatalf("hybrid eval %q: %v", source, err)
		}
	}

	oracle, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, err := oracle.EvalAs(`eval('if (true) { function hybrid_eval_fn($value) { return $value . "!"; } } echo hybrid_eval_fn("A");'); eval('echo hybrid_eval_fn("B");');`, "case.php")
	oracle.Close()
	if err != nil {
		t.Fatal(err)
	}
	if got := output.String(); got != want {
		t.Fatalf("hybrid eval output = %q, Zend = %q", got, want)
	}
}

func TestHybridScopedEvalRefusesGeneratedFunctionCollision(t *testing.T) {
	ctx := php.NewCtx(io.Discard)
	_, err := hybrid.EvalScopedWithFunctions(ctx, "function generated_function() {}", nil, nil,
		[]string{"generated_function"}, nil,
		func(*php.Ctx, string, []php.Value) (php.Value, error) { return php.Null(), nil })
	if !php.IsUnsupported(err) {
		t.Fatalf("generated function collision = %v, want unsupported", err)
	}
}

func TestHybridScopedEvalRefusesUnsupportedGeneratedCallConventions(t *testing.T) {
	ctx := php.NewCtx(io.Discard)
	dispatch := func(*php.Ctx, string, []php.Value) (php.Value, error) {
		t.Fatal("unsupported call convention reached generated code")
		return php.Undef(), nil
	}
	cases := []struct {
		code      string
		name      string
		signature hybrid.FunctionSignature
	}{
		{
			code: "generated_function(value: 'x');", name: "generated_function",
			signature: hybrid.FunctionSignature{Parameters: []hybrid.FunctionParameter{{Name: "value", ByRef: true}}, Required: 1},
		},
		{
			code: "generated_variadic(extra: 'x');", name: "generated_variadic",
			signature: hybrid.FunctionSignature{Parameters: []hybrid.FunctionParameter{{Name: "rest", Variadic: true}}},
		},
	}
	for _, tc := range cases {
		_, err := hybrid.EvalScopedWithFunctions(ctx, tc.code, nil, nil, []string{tc.name},
			map[string]hybrid.FunctionSignature{tc.name: tc.signature}, dispatch)
		if !php.IsUnsupported(err) {
			t.Errorf("eval %q error = %v, want unsupported", tc.code, err)
		}
	}
}

func TestHybridScopedEvalSharesGeneratedGlobals(t *testing.T) {
	var output strings.Builder
	ctx := php.NewCtx(&output)
	ctx.Globals.Set(php.StringKey("shared"), php.String("before"))
	if _, err := hybrid.EvalScoped(ctx, `$GLOBALS["shared"] = "via table"; echo $GLOBALS["shared"];`, nil, nil); err != nil {
		t.Fatalf("eval through $GLOBALS: %v", err)
	}
	if got := output.String(); got != "via table" {
		t.Fatalf("$GLOBALS output = %q", got)
	}

	ctx.Globals.Set(php.StringKey("shared"), php.String("before"))
	local := php.String("local")
	vars := map[string]*php.Value{"shared": &local}
	if _, err := hybrid.EvalScoped(ctx, `global $shared; $shared = "via global"; echo $shared;`, vars, nil); err != nil {
		t.Fatalf("eval global binding: %v", err)
	}
	global, ok := ctx.Globals.Get(php.StringKey("shared"))
	if !ok || global.String() != "via global" || local.Deref().String() != "via global" {
		t.Fatalf("global binding left global=%v local=%v", global, local)
	}
	oracle, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, err := oracle.EvalAs(`$shared='before'; eval('$GLOBALS["shared"] = "via table"; echo $GLOBALS["shared"];'); function f(){ $shared='local'; eval('global $shared; $shared = "via global"; echo $shared;'); } f();`, "case.php")
	oracle.Close()
	if err != nil {
		t.Fatal(err)
	}
	if got := output.String(); got != want {
		t.Fatalf("hybrid global output = %q, Zend = %q", got, want)
	}
}

func TestHybridScopedEvalInheritsThisForGlobalCalls(t *testing.T) {
	var output strings.Builder
	ctx := php.NewCtx(&output)
	ctx.PushCall(php.StackFrame{Function: "EvalClass->run", This: php.NewObject("EvalClass", 1)})
	defer ctx.PopCall()
	if _, err := hybrid.EvalScoped(ctx, `echo get_class($this);`, nil, nil); err != nil {
		t.Fatalf("eval using caller $this: %v", err)
	}
	if _, err := hybrid.EvalScoped(ctx, `$this->value;`, nil, nil); !php.IsUnsupported(err) {
		t.Fatalf("eval reading a generated object's property = %v, want unsupported", err)
	}
	oracle, err := zend.Start()
	if err != nil {
		t.Fatal(err)
	}
	want, err := oracle.EvalAs(`class EvalClass { function run() { eval('echo get_class($this);'); } } (new EvalClass())->run();`, "case.php")
	oracle.Close()
	if err != nil {
		t.Fatal(err)
	}
	if got := output.String(); got != want {
		t.Fatalf("hybrid eval with $this = %q, Zend = %q", got, want)
	}
}

func TestConditionalIncludeOnceUsesLoweredBody(t *testing.T) {
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	previousDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(previousDirectory)

	directory := t.TempDir()
	included := filepath.Join(directory, "part.php")
	if err := os.WriteFile(included, []byte("<?php echo 'file'; return 'R';\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source := fmt.Sprintf(`<?php
if (getenv('P2G_INCLUDE_BRANCH') === 'yes') {
    $first = include_once(%q);
    $again = include_once(%q);
    echo $first, $again;
} else {
    echo 'else';
}
`, included, included)
	path := filepath.Join(directory, "main.php")
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("P2G_INCLUDE_BRANCH", "")
	oldEmit, oldRoot := emitDir, sourceRoot
	emitDir, sourceRoot = t.TempDir(), repo
	defer func() { emitDir, sourceRoot = oldEmit, oldRoot }()
	if err := run([]string{path}, 0, false, ""); err != nil {
		t.Fatal(err)
	}
	module := filepath.Join(emitDir, sanitize(path))
	binary := filepath.Join(t.TempDir(), "conditional")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir = module
	build.Env = append(os.Environ(), "GOWORK=off")
	if log, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building lowered conditional include: %v\n%s", err, log)
	}
	for _, choice := range []string{"yes", "no"} {
		t.Setenv("P2G_INCLUDE_BRANCH", choice)
		compiled := exec.Command(binary)
		compiled.Env = append(os.Environ(), "P2G_INCLUDE_BRANCH="+choice)
		got, err := compiled.CombinedOutput()
		if err != nil {
			t.Fatalf("running %s branch: %v\n%s", choice, err, got)
		}
		engine, err := zend.Start()
		if err != nil {
			t.Fatal(err)
		}
		want, evalErr := engine.EvalAs("     "+source[5:], path)
		engine.Close()
		if evalErr != nil {
			t.Fatal(evalErr)
		}
		if string(got) != want {
			t.Fatalf("branch %s = %q, Zend = %q", choice, got, want)
		}
	}
}
