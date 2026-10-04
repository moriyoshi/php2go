package php_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/moriyoshi/php2go/runtime/php"
)

// These are a **conformance suite for the ObjectProperties contract**, not only
// tests of the renderer. `var_dump` of an object is split across a boundary --
// the formatting here, the question "what should this object show" answered by
// whoever has a class table -- so what the hook must return is a contract, and a
// contract can be tested without any implementation of it.
//
// That matters concretely: the compiler side has the `__debugInfo` path emitted
// but **unexercised**, because writing such a method needs an array literal and
// `<array>` is not renderable there yet. They cannot construct the test from a
// PHP program; it can be constructed here by calling the hook directly. Any
// implementation -- theirs, the interpreter's -- can be run against these.
//
// Every expectation is the engine's answer to the equivalent program, from
// `php2eval -engine`, not an inference about how it ought to look.

// dumpWith renders v with a hook that answers shown/described for any object.
func dumpWith(t *testing.T, v php.Value, shown *php.Array, described bool) string {
	t.Helper()
	ctx := php.NewCtx(&strings.Builder{})
	ctx.SetObjectProperties(func(_ *php.Ctx, _ *php.Object) (*php.Array, bool, error) {
		return shown, described, nil
	})
	out, err := php.VarDump(ctx, v)
	if err != nil {
		t.Fatalf("VarDump: %v", err)
	}
	return out
}

// What __debugInfo returns is an *ordinary array*, so its keys carry no
// visibility annotation and an integer key prints unquoted the way an array's
// does. Engine, for a class whose __debugInfo returns
// ['a' => 1, 7 => 'seven', 'nested' => [1,2]]:
//
//	object(D)#1 (3) {
//	  ["a"]=>
//	  int(1)
//	  [7]=>
//	  string(5) "seven"
//	  ...
func TestDumpOfADescribedObjectUsesArrayKeyRules(t *testing.T) {
	described := php.NewArray()
	described.Set(php.StringKey("a"), php.Int(1))
	described.Set(php.IntKey(7), php.String("seven"))
	described.Set(php.StringKey("nested"), php.Arr(ints(1, 2)))

	got := dumpWith(t, php.Obj(php.NewObject("D", 1)), described, true)
	want := `object(D)#1 (3) {
  ["a"]=>
  int(1)
  [7]=>
  string(5) "seven"
  ["nested"]=>
  array(2) {
    [0]=>
    int(1)
    [1]=>
    int(2)
  }
}
`
	if got != want {
		t.Errorf("described object:\n got %q\nwant %q", got, want)
	}
}

// Real properties do carry the annotation, and private additionally names the
// declaring class. Engine, for `class P { public $pub=1; protected $prot=2;
// private $priv=3; }`:
//
//	["pub"]=>  ["prot":protected]=>  ["priv":"P":private]=>
func TestDumpOfRealPropertiesCarriesVisibility(t *testing.T) {
	o := php.NewObject("P", 1)
	o.Props.Set(php.StringKey("pub"), php.Int(1))
	o.Props.Set(php.StringKey(php.MangleProperty("*", "prot")), php.Int(2))
	o.Props.Set(php.StringKey(php.MangleProperty("P", "priv")), php.Int(3))

	got := dumpWith(t, php.Obj(o), o.Props, false)
	for _, want := range []string{`["pub"]=>`, `["prot":protected]=>`, `["priv":"P":private]=>`} {
		if !strings.Contains(got, want) {
			t.Errorf("dump is missing %s\ngot %q", want, got)
		}
	}
	if !strings.HasPrefix(got, "object(P)#1 (3) {") {
		t.Errorf("header is %q, want object(P)#1 (3) {", strings.SplitN(got, "\n", 2)[0])
	}
}

// An uninitialized *typed* property is listed but not counted -- the header
// counts what actually holds a value. Engine, for
// `class E { public $u; public int $typed; }`:
//
//	object(E)#1 (1) { ["u"]=> NULL ["typed"]=> uninitialized(int) }
//
// And an *untyped* property holding nothing is not printed at all, which is what
// makes the type the deciding factor rather than the emptiness.
func TestDumpListsAnUninitializedTypedPropertyWithoutCountingIt(t *testing.T) {
	o := php.NewObject("E", 1)
	o.Props.Set(php.StringKey("u"), php.Null())
	o.Props.Set(php.StringKey("typed"), php.Undef())
	o.Props.Set(php.StringKey("untypedEmpty"), php.Undef())
	o.Declared = map[string]php.Declaration{"typed": {Type: "int"}}

	got := dumpWith(t, php.Obj(o), o.Props, false)
	want := `object(E)#1 (1) {
  ["u"]=>
  NULL
  ["typed"]=>
  uninitialized(int)
}
`
	if got != want {
		t.Errorf("uninitialized typed property:\n got %q\nwant %q", got, want)
	}
}

// A hook that refuses propagates its error rather than producing a partial
// dump. The interpreter uses this for a `__debugInfo` that did not return an
// array, which PHP reports *instead of* the dump.
func TestDumpPropagatesAHookRefusal(t *testing.T) {
	ctx := php.NewCtx(&strings.Builder{})
	boom := errors.New("__debuginfo() must return an array")
	ctx.SetObjectProperties(func(_ *php.Ctx, _ *php.Object) (*php.Array, bool, error) {
		return nil, false, boom
	})
	out, err := php.VarDump(ctx, php.Obj(php.NewObject("D", 1)))
	if !errors.Is(err, boom) {
		t.Errorf("VarDump gave %v, want the hook's error", err)
	}
	if strings.Contains(out, "object(D)") {
		t.Errorf("a refused dump still emitted an object header: %q", out)
	}
}

// With no hook installed the object case is refused, and only the object case:
// dumping a scalar must not require a class table.
func TestDumpWithoutAHookRefusesOnlyObjects(t *testing.T) {
	ctx := php.NewCtx(&strings.Builder{})
	if got, err := php.VarDump(ctx, php.Int(5)); err != nil || got != "int(5)\n" {
		t.Errorf("scalar dump with no hook = %q, %v; want int(5) and no error", got, err)
	}
	if _, err := php.VarDump(ctx, php.Obj(php.NewObject("D", 1))); !errors.Is(err, php.ErrNoObjectProperties) {
		t.Errorf("object dump with no hook gave %v, want ErrNoObjectProperties", err)
	}
}

// A cycle through a reference prints *RECURSION* rather than descending
// forever. Value semantics make a cycle impossible through assignment alone, so
// this is reachable only by reference -- which is why the guard is on the array
// identity rather than on depth.
func TestDumpStopsAtRecursion(t *testing.T) {
	arr := php.NewArray()
	arr.Set(php.StringKey("self"), php.Arr(arr))

	ctx := php.NewCtx(&strings.Builder{})
	got, err := php.VarDump(ctx, php.Arr(arr))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "*RECURSION*") {
		t.Errorf("a self-referencing array dumped as %q, want *RECURSION*", got)
	}
}

// The `described` result is observable, which is why it is part of the contract
// rather than an implementation detail: the same array renders differently
// depending on it. An implementor that always answers one way is wrong in a way
// that shows.
//
//	described=true   [7]=>      -- an ordinary array's key
//	described=false  ["7"]=>    -- a property name, quoted
func TestTheDescribedFlagIsObservable(t *testing.T) {
	a := php.NewArray()
	a.Set(php.IntKey(7), php.String("seven"))

	if got := dumpWith(t, php.Obj(php.NewObject("D", 1)), a, true); !strings.Contains(got, "[7]=>") {
		t.Errorf("described=true rendered %q, want an unquoted [7]", got)
	}
	if got := dumpWith(t, php.Obj(php.NewObject("D", 1)), a, false); !strings.Contains(got, `["7"]=>`) {
		t.Errorf(`described=false rendered %q, want a quoted ["7"]`, got)
	}
}

// ---------------------------------------------------------------------------
// CallValue: the hook a builtin uses to invoke user code
// ---------------------------------------------------------------------------

// A conformance suite for the CallValue contract, for the same reason the
// ObjectProperties one exists: the hook is a boundary, so what an implementation
// must do is testable without any implementation of it.
func TestArrayReduceCallsBackThroughTheHook(t *testing.T) {
	ctx := php.NewCtx(&strings.Builder{})
	var seen [][2]int64
	ctx.SetCallValue(func(_ *php.Ctx, _ php.Value, args []php.Value) (php.Value, error) {
		seen = append(seen, [2]int64{args[0].Int(), args[1].Int()})
		return php.Int(args[0].Int() + args[1].Int()), nil
	})

	fn, ok := php.LookupBuiltin("array_reduce")
	if !ok {
		t.Fatal("array_reduce is not registered")
	}
	got, err := fn(ctx, []php.Value{php.Arr(ints(1, 2, 3)), php.Null(), php.Int(10)})
	if err != nil {
		t.Fatal(err)
	}
	if got.Int() != 16 {
		t.Errorf("array_reduce = %s, want 16", got.String())
	}
	// The carry threads through: each call sees the previous result.
	if want := "[[10 1] [11 2] [13 3]]"; fmt.Sprint(seen) != want {
		t.Errorf("callbacks got %v, want %s", seen, want)
	}
}

// A callback that fails stops the fold and the error comes out, rather than the
// remaining elements being folded into a carry that no longer means anything.
func TestArrayReduceStopsWhenTheCallbackFails(t *testing.T) {
	ctx := php.NewCtx(&strings.Builder{})
	boom := errors.New("thrown from the callback")
	calls := 0
	ctx.SetCallValue(func(_ *php.Ctx, _ php.Value, _ []php.Value) (php.Value, error) {
		calls++
		return php.Null(), boom
	})
	fn, _ := php.LookupBuiltin("array_reduce")
	if _, err := fn(ctx, []php.Value{php.Arr(ints(1, 2, 3)), php.Null()}); !errors.Is(err, boom) {
		t.Errorf("got %v, want the callback's error", err)
	}
	if calls != 1 {
		t.Errorf("the callback ran %d times after failing, want 1", calls)
	}
}

// With no hook installed the builtin refuses rather than skipping the callback.
// Skipping would return the initial carry, which is exactly what an empty array
// returns -- a missing capability wearing the shape of a correct answer.
func TestABuiltinWithNoCallValueRefuses(t *testing.T) {
	ctx := php.NewCtx(&strings.Builder{})
	if ctx.CanCall() {
		t.Fatal("a fresh Ctx reports it can call")
	}
	fn, _ := php.LookupBuiltin("array_reduce")
	_, err := fn(ctx, []php.Value{php.Arr(ints(1, 2)), php.Null(), php.Int(7)})
	if !errors.Is(err, php.ErrNoCallValue) {
		t.Errorf("got %v, want ErrNoCallValue", err)
	}
}

// An empty array never calls back, and answers the initial value -- which is
// why the refusal above has to be distinguishable from it.
func TestArrayReduceOverAnEmptyArrayNeverCallsBack(t *testing.T) {
	ctx := php.NewCtx(&strings.Builder{})
	called := false
	ctx.SetCallValue(func(_ *php.Ctx, _ php.Value, _ []php.Value) (php.Value, error) {
		called = true
		return php.Null(), nil
	})
	fn, _ := php.LookupBuiltin("array_reduce")
	got, err := fn(ctx, []php.Value{php.Arr(php.NewArray()), php.Null(), php.Int(7)})
	if err != nil || got.Int() != 7 {
		t.Errorf("empty array reduced to %s, %v; want 7 and no error", got.String(), err)
	}
	if called {
		t.Error("an empty array called the callback")
	}
}

// ---------------------------------------------------------------------------
// The error-handler stack
// ---------------------------------------------------------------------------

// `set_error_handler(null)` installs "no handler" **on the stack** rather than
// clearing it. Probed against the engine: pushing null then restoring twice
// reaches the handler two below, which clearing could never do. It is the one
// piece of this that a second implementation would get wrong.
func TestSetErrorHandlerNullIsPushedNotCleared(t *testing.T) {
	ctx := php.NewCtx(&strings.Builder{})
	set, _ := php.LookupBuiltin("set_error_handler")
	restore, _ := php.LookupBuiltin("restore_error_handler")

	set(ctx, []php.Value{php.String("first")})
	set(ctx, []php.Value{php.String("second")})
	set(ctx, []php.Value{php.Null()})

	// With null on top there is no handler to offer a diagnostic to...
	if _, ok := ctx.CurrentErrorHandler(); ok {
		t.Error("a null on top should mean no handler, not the one below it")
	}
	// ...but the ones below are still there.
	restore(ctx, nil)
	if h, ok := ctx.CurrentErrorHandler(); !ok || h.String() != "second" {
		t.Errorf("after one restore the handler is %q,%v; want second", h.String(), ok)
	}
	restore(ctx, nil)
	if h, ok := ctx.CurrentErrorHandler(); !ok || h.String() != "first" {
		t.Errorf("after two restores the handler is %q,%v; want first", h.String(), ok)
	}
}

// set_error_handler answers the handler it replaced, and NULL when there was
// none.
func TestSetErrorHandlerReturnsThePrevious(t *testing.T) {
	ctx := php.NewCtx(&strings.Builder{})
	set, _ := php.LookupBuiltin("set_error_handler")

	if got, _ := set(ctx, []php.Value{php.String("a")}); got.Kind() != php.KindNull {
		t.Errorf("first set returned %s, want NULL", got.String())
	}
	if got, _ := set(ctx, []php.Value{php.String("b")}); got.String() != "a" {
		t.Errorf("second set returned %q, want a", got.String())
	}
}

// restore_error_handler answers true even with nothing to pop, and popping an
// empty stack is not an error.
func TestRestoreErrorHandlerOnAnEmptyStack(t *testing.T) {
	ctx := php.NewCtx(&strings.Builder{})
	restore, _ := php.LookupBuiltin("restore_error_handler")
	got, err := restore(ctx, nil)
	if err != nil || got.Kind() != php.KindTrue {
		t.Errorf("restore on an empty stack = %s, %v; want true and no error", got.String(), err)
	}
}

// Every installed handler is a root, including the nulls: a cycle collector that
// walked only the top of the stack would free a closure that a later restore
// brings back.
func TestErrorHandlersAreAllReachableAsRoots(t *testing.T) {
	ctx := php.NewCtx(&strings.Builder{})
	set, _ := php.LookupBuiltin("set_error_handler")
	set(ctx, []php.Value{php.String("a")})
	set(ctx, []php.Value{php.Null()})
	set(ctx, []php.Value{php.String("c")})

	if got := len(ctx.ErrorHandlers()); got != 3 {
		t.Errorf("the collector sees %d roots, want 3 -- a buried handler is still alive", got)
	}
}

// The callable registries. Every rule is the engine's, and two of them are not
// guessable -- one contradicts what the interpreter did.
func TestAutoloaderRegistryMatchesTheEngine(t *testing.T) {
	ctx := php.NewCtx(&strings.Builder{})
	f1, f2 := php.String("auto1"), php.String("auto2")

	if !ctx.RegisterAutoloader(f1, false) {
		t.Error("register should answer true")
	}
	// prepend puts it first, so it is consulted first.
	ctx.RegisterAutoloader(f2, true)
	// A duplicate answers **true** and is **not added** -- checked against the
	// engine, where count(spl_autoload_functions()) stays at 2. The interpreter
	// appended unconditionally, so a double registration ran the autoloader
	// twice.
	if !ctx.RegisterAutoloader(f1, false) {
		t.Error("a duplicate registration still answers true")
	}
	if got := len(ctx.Autoloaders()); got != 2 {
		t.Errorf("%d autoloaders after a duplicate registration, want 2", got)
	}
	if got := ctx.Autoloaders()[0].String(); got != "auto2" {
		t.Errorf("first autoloader is %q, want the prepended auto2", got)
	}
	if !ctx.UnregisterAutoloader(f2) || len(ctx.Autoloaders()) != 1 {
		t.Error("unregister should remove exactly the one named")
	}
	if ctx.UnregisterAutoloader(php.String("never registered")) {
		t.Error("unregistering something absent should answer false")
	}
}

// Shutdown functions run in **registration** order, which is the opposite of
// the handler stacks where the last registered wins, and they carry the extra
// arguments they were registered with.
func TestShutdownRegistryKeepsOrderAndArguments(t *testing.T) {
	ctx := php.NewCtx(&strings.Builder{})
	ctx.RegisterShutdown(php.String("first"), []php.Value{php.Int(7)})
	ctx.RegisterShutdown(php.String("second"), nil)

	calls := ctx.ShutdownCalls()
	if len(calls) != 2 || calls[0].Callee.String() != "first" {
		t.Fatalf("registration order not preserved: %v", calls)
	}
	if len(calls[0].Args) != 1 || calls[0].Args[0].Int() != 7 {
		t.Errorf("extra arguments lost: %v", calls[0].Args)
	}
	ctx.ClearShutdown()
	if len(ctx.ShutdownCalls()) != 0 {
		t.Error("ClearShutdown left entries that will never run")
	}
}

// Suppression is not a pop. The engine clears the handler while one runs, so an
// exception the handler throws has none -- **unless the handler installed one**,
// which is what makes a chain work. Popping would discard that.
func TestSuppressExceptionHandlerIsNotAPop(t *testing.T) {
	ctx := php.NewCtx(&strings.Builder{})
	set, _ := php.LookupBuiltin("set_exception_handler")
	set(ctx, []php.Value{php.String("outer")})

	undo := ctx.SuppressExceptionHandler()
	if _, ok := ctx.CurrentExceptionHandler(); ok {
		t.Error("the handler should be suppressed while one runs")
	}
	// The running handler installs its own; it must survive the undo.
	set(ctx, []php.Value{php.String("nested")})
	undo()
	if h, ok := ctx.CurrentExceptionHandler(); !ok || h.String() != "nested" {
		t.Errorf("after undo the handler is %q,%v; want the nested one -- a pop would have discarded it", h.String(), ok)
	}
	// And the undo is idempotent.
	undo()
	if h, _ := ctx.CurrentExceptionHandler(); h.String() != "nested" {
		t.Error("a second undo changed the stack")
	}
}

// var_dump escapes **nothing**, which is the diagnostics rule one layer over:
// it writes the byte length, a quote, the bytes, and a quote. A string holding a
// quote, a backslash, a newline or a NUL comes through raw, and the length is in
// **bytes** -- "café" is 5, not 4.
//
// These rows exist because the diagnostics table was correct for every input it
// was probed with and wrong for a class it was not. This path had the same
// shape: right by construction, never exercised on a value that could tell it
// apart from a version using Go quoting.
func TestVarDumpEscapesNothingAndCountsBytes(t *testing.T) {
	ctx := php.NewCtx(&strings.Builder{})
	for _, c := range []struct{ in, want string }{
		{`say "hi"`, "string(8) \"say \"hi\"\"\n"},
		{`a\b`, "string(3) \"a\\b\"\n"},
		{"line\nbreak", "string(10) \"line\nbreak\"\n"},
		// Byte length, not rune count: four characters, five bytes.
		{"café", "string(5) \"café\"\n"},
		{"nul\x00byte", "string(8) \"nul\x00byte\"\n"},
	} {
		got, err := php.VarDump(ctx, php.String(c.in))
		if err != nil {
			t.Fatal(err)
		}
		if got != c.want {
			t.Errorf("var_dump(%q):\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}

// An array key is quoted the same way -- raw bytes between two quote characters,
// so a key holding a quote reads ["k"q"].
func TestVarDumpOfAHostileArrayKey(t *testing.T) {
	ctx := php.NewCtx(&strings.Builder{})
	a := php.NewArray()
	a.Set(php.StringKey(`k"q`), php.Int(1))

	got, err := php.VarDump(ctx, php.Arr(a))
	if err != nil {
		t.Fatal(err)
	}
	want := "array(1) {\n  [\"k\"q\"]=>\n  int(1)\n}\n"
	if got != want {
		t.Errorf("hostile array key:\n got %q\nwant %q", got, want)
	}
}

func TestDebugZvalDumpTracksLiteralAndReferenceFacts(t *testing.T) {
	ctx := php.NewCtx(&strings.Builder{})

	for _, test := range []struct {
		name  string
		value php.Value
		want  string
	}{
		{"interned string", php.InternedString("literal"), "string(7) \"literal\" interned\n"},
		{"dynamic string", php.String("dynamic"), "string(7) \"dynamic\" refcount(2)\n"},
	} {
		got, err := php.DebugZvalDump(ctx, test.value)
		if err != nil {
			t.Fatal(err)
		}
		if got != test.want {
			t.Errorf("%s: got %q, want %q", test.name, got, test.want)
		}
	}

	empty := php.NewArray()
	empty.MarkInterned()
	copy := empty.Clone()
	if got, err := php.DebugZvalDump(ctx, php.Arr(copy)); err != nil || got != "array(0) interned {\n}\n" {
		t.Errorf("interned empty array = %q, %v", got, err)
	}
	copy.Append(php.Int(1))
	if copy.IsInterned() {
		t.Error("mutating a literal empty array left it interned")
	}

	reference := php.NewRef(php.Int(1))
	reference.AddBinding()
	array := php.NewArray()
	array.Append(php.Shared(php.MakeRef(reference)))
	got, err := php.DebugZvalDump(ctx, php.Arr(array))
	if err != nil {
		t.Fatal(err)
	}
	want := "array(1) packed refcount(2){\n  [0]=>\n  reference refcount(2) {\n    int(1)\n  }\n}\n"
	if got != want {
		t.Errorf("reference array:\n got %q\nwant %q", got, want)
	}
}
