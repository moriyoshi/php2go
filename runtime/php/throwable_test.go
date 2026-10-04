package php_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/moriyoshi/php2go/runtime/php"
)

// Every relationship here is the engine's answer to
//
//	$r = new ReflectionClass($c); $r->getParentClass()->getName();
//
// run over the classes the corpus actually catches. Writing the list from
// memory would have got `ParseError` wrong -- it extends `CompileError`, not
// `Error` -- and `catch (Error $e)` would still have caught it, so the mistake
// would never have shown.
func TestThrowableHierarchyMatchesTheEngine(t *testing.T) {
	for _, c := range []struct{ class, parent string }{
		{"Exception", ""},
		{"Error", ""},
		{"ErrorException", "Exception"},
		{"RuntimeException", "Exception"},
		{"LogicException", "Exception"},
		{"InvalidArgumentException", "LogicException"},
		{"UnexpectedValueException", "RuntimeException"},
		{"TypeError", "Error"},
		{"ValueError", "Error"},
		{"ArithmeticError", "Error"},
		{"AssertionError", "Error"},
		{"UnhandledMatchError", "Error"},
		{"FiberError", "Error"},
		{"ArgumentCountError", "TypeError"},
		{"DivisionByZeroError", "ArithmeticError"},
		{"ParseError", "CompileError"},
		{"CompileError", "Error"},
	} {
		if got := php.ThrowableParent(c.class); got != c.parent {
			t.Errorf("%s extends %q, want %q", c.class, got, c.parent)
		}
	}
}

// `instanceof` over the hierarchy, including the cases that decide whether a
// catch clause matches. The engine: a TypeError is an Error and a Throwable and
// is **not** an Exception -- the two roots are disjoint, which is the single
// most load-bearing fact for `catch`.
func TestThrowableIsA(t *testing.T) {
	for _, c := range []struct {
		class, want string
		is          bool
	}{
		{"TypeError", "Error", true},
		{"TypeError", "Throwable", true},
		{"TypeError", "Exception", false},
		{"TypeError", "TypeError", true},
		{"ArgumentCountError", "TypeError", true},
		{"ArgumentCountError", "Error", true},
		{"DivisionByZeroError", "ArithmeticError", true},
		{"DivisionByZeroError", "Error", true},
		{"DivisionByZeroError", "TypeError", false},
		{"ParseError", "Error", true},
		{"RuntimeException", "Exception", true},
		{"RuntimeException", "Error", false},
		{"UnexpectedValueException", "Exception", true},
		{"Exception", "Throwable", true},
		{"Error", "Throwable", true},
	} {
		if got := php.ThrowableIsA(c.class, c.want); got != c.is {
			t.Errorf("%s instanceof %s = %v, want %v", c.class, c.want, got, c.is)
		}
	}
}

// Throwable is an interface, so it is not constructible and must not appear as
// a class. A `new Throwable` that succeeded would be a class the engine does
// not have.
func TestThrowableItselfIsNotAClass(t *testing.T) {
	if php.IsThrowableClass("Throwable") {
		t.Error("Throwable reports as a class; it is an interface")
	}
	if !php.IsThrowableClass("Exception") || !php.IsThrowableClass("TypeError") {
		t.Error("Exception and TypeError should report as classes")
	}
	if php.IsThrowableClass("ArrayObject") {
		t.Error("a non-throwable class reports as one")
	}
}

// The strongest available form: render a constructed exception through VarDump
// and compare with the engine's own output, byte for byte. That pins the
// property **order**, their **visibility labels** and the **mangling** in one
// assertion, where checking the names alone missed all three.
//
// The engine, for `var_dump(new Exception("x"))` at php2go-eval line 1.
func TestVarDumpOfAThrowableMatchesTheEngine(t *testing.T) {
	ctx := php.NewCtx(&strings.Builder{})
	ctx.SetLocation("php2go-eval", 1)
	o := php.InitThrowable(ctx, php.NewObject("Exception", 1), "x", 0, php.Null())
	ctx.SetObjectProperties(func(_ *php.Ctx, obj *php.Object) (*php.Array, bool, error) {
		return obj.Props, false, nil
	})

	got, err := php.VarDump(ctx, php.Obj(o))
	if err != nil {
		t.Fatal(err)
	}
	want := `object(Exception)#1 (7) {
  ["message":protected]=>
  string(1) "x"
  ["string":"Exception":private]=>
  string(0) ""
  ["code":protected]=>
  int(0)
  ["file":protected]=>
  string(11) "php2go-eval"
  ["line":protected]=>
  int(1)
  ["trace":"Exception":private]=>
  array(0) {
  }
  ["previous":"Exception":private]=>
  NULL
}
`
	if got != want {
		t.Errorf("var_dump of an Exception:\n got %q\nwant %q", got, want)
	}
}

// The private properties belong to the **root**, not to the declaring class:
// `class MyEx extends RuntimeException` still dumps
// `["trace":"Exception":private]`. Declaring-class is the natural guess and
// prints the wrong label, so it was checked.
func TestThrowablePrivatePropertiesBelongToTheRoot(t *testing.T) {
	for _, c := range []struct{ class, root string }{
		{"Exception", "Exception"},
		{"RuntimeException", "Exception"},
		{"UnexpectedValueException", "Exception"},
		{"TypeError", "Error"},
		{"DivisionByZeroError", "Error"},
		{"ParseError", "Error"},
	} {
		if got := php.ThrowableRoot(c.class); got != c.root {
			t.Errorf("root of %s is %q, want %q", c.class, got, c.root)
		}
	}
}

// The accessors read through the mangling. Reading the plain name would find
// nothing and answer NULL for every one of them -- silently, since NULL is a
// legitimate answer for getPrevious().
func TestThrowableAccessorsReadThroughTheMangling(t *testing.T) {
	ctx := php.NewCtx(&strings.Builder{})
	ctx.SetLocation("f.php", 12)
	o := php.InitThrowable(ctx, php.NewObject("TypeError", 1), "boom", 3, php.Null())

	if got := php.ThrowableMessage(o).String(); got != "boom" {
		t.Errorf("getMessage = %q, want boom", got)
	}
	if got := php.ThrowableCode(o).Int(); got != 3 {
		t.Errorf("getCode = %d, want 3", got)
	}
	if got := php.ThrowableFile(o).String(); got != "f.php" {
		t.Errorf("getFile = %q, want f.php", got)
	}
	if got := php.ThrowableLine(o).Int(); got != 12 {
		t.Errorf("getLine = %d, want 12", got)
	}
}

// A previous exception chains, which is what `getPrevious()` walks.
func TestThrowablePreviousChains(t *testing.T) {
	ctx := php.NewCtx(&strings.Builder{})
	inner := php.InitThrowable(ctx, php.NewObject("Exception", 1), "boom", 0, php.Null())
	outer := php.InitThrowable(ctx, php.NewObject("RuntimeException", 2), "outer", 0, php.Obj(inner))

	prev := php.ThrowablePrevious(outer)
	if prev.Kind() != php.KindObject {
		t.Fatalf("getPrevious gave %s, want the inner exception", prev.Kind())
	}
	if got := php.ThrowableMessage(prev.Object()).String(); got != "boom" {
		t.Errorf("previous message %q, want boom", got)
	}
}

// `(string)$e`, byte for byte as the engine renders it:
//
//	Exception: boom in php2go-eval:1
//	Stack trace:
//	#0 {main}
func TestThrowableStringMatchesTheEngine(t *testing.T) {
	ctx := php.NewCtx(&strings.Builder{})
	ctx.SetLocation("php2go-eval", 1)
	o := php.InitThrowable(ctx, php.NewObject("Exception", 1), "boom", 0, php.Null())

	want := "Exception: boom in php2go-eval:1\nStack trace:\n#0 {main}"
	if got := php.ThrowableString(o); got != want {
		t.Errorf("(string)$e =\n%q\nwant\n%q", got, want)
	}
}

// A thrown exception travels as a Go error and hands the *object* back, because
// a catch has to test its class and bind the value -- a message alone could do
// neither.
func TestThrownCarriesTheObject(t *testing.T) {
	ctx := php.NewCtx(&strings.Builder{})
	o := php.InitThrowable(ctx, php.NewObject("TypeError", 1), "bad type", 0, php.Null())
	err := php.Throw(o)

	caught, ok := php.AsThrown(err)
	if !ok {
		t.Fatal("AsThrown did not recognise a thrown exception")
	}
	if !php.ThrowableIsA(caught.ClassName, "Error") {
		t.Error("the caught object lost its class")
	}
	// And it survives wrapping, which is how it travels out through layers that
	// annotate errors.
	wrapped := errors.Join(errors.New("context"), err)
	if _, ok := php.AsThrown(wrapped); !ok {
		t.Error("a wrapped throw is no longer recognisable")
	}
}

// The driver sentinels carry their PHP identity, so no consumer has to
// rediscover a message that is graded byte-exact. Every pair is the engine's
// answer, and the classes are not guessable: a generator's refusals are plain
// `Exception`, a fiber's are `FiberError`, and a bad `yield from` operand is
// `Error` -- three roots for what reads like one category.
func TestThrowableForMatchesTheEngine(t *testing.T) {
	for _, c := range []struct {
		err            error
		class, message string
	}{
		{php.ErrGeneratorNotReturned, "Exception", "Cannot get return value of a generator that hasn't returned"},
		{php.ErrGeneratorAlreadyRun, "Exception", "Cannot rewind a generator that was already run"},
		{php.ErrNotDelegatable, "Error", `Can use "yield from" only with arrays and Traversables`},
		{php.ErrFiberAlreadyStarted, "FiberError", "Cannot start a fiber that has already been started"},
		{php.ErrFiberNotReturned, "FiberError", "Cannot get fiber return value: The fiber has not returned"},
		{php.ErrFiberThrew, "FiberError", "Cannot get fiber return value: The fiber threw an exception"},
	} {
		class, message, ok := php.ThrowableFor(c.err)
		if !ok {
			t.Errorf("%v has no PHP identity", c.err)
			continue
		}
		if class != c.class || message != c.message {
			t.Errorf("%v -> %s(%q), want %s(%q)", c.err, class, message, c.class, c.message)
		}
		// The class it names must exist in the hierarchy, or a consumer that
		// trusts this will try to construct something the engine does not have.
		if !php.IsThrowableClass(class) {
			t.Errorf("%v names %s, which is not a throwable class", c.err, class)
		}
	}
}

// A gap is not a PHP condition and must not become one. Reporting a missing
// capability as an exception is the masquerade the quality gate forbids -- and
// it would be invisible, because a PHP program cannot tell a real Error from a
// fabricated one.
func TestThrowableForRefusesToDressUpAGap(t *testing.T) {
	for _, err := range []error{
		php.ErrDelegateUnsupported, php.ErrIterUnsupported, php.ErrNoObjectProperties,
		php.ErrNoCallValue, php.ErrSuspendAcrossDriver, php.ErrProtocol,
	} {
		if class, _, ok := php.ThrowableFor(err); ok {
			t.Errorf("%v was given the PHP identity %s; it is a gap, not a condition", err, class)
		}
	}
}

// PHP class names are case-insensitive and **the IR carries them lowercased** --
// Zend folds them into the literal, so a compiled `new Exception` arrives as
// `"exception"`. Lookup must fold; display must not.
//
// The engine settles both halves at once:
//
//	get_class(new EXCEPTION("x"))   =>  "Exception"     -- the declared spelling
//	catch (typeerror $t)            =>  catches TypeError
//	$e instanceof throwable         =>  true
func TestThrowableLookupFoldsCaseButDisplayDoesNot(t *testing.T) {
	for _, written := range []string{"exception", "EXCEPTION", "Exception", "eXcEpTiOn"} {
		if !php.IsThrowableClass(written) {
			t.Errorf("IsThrowableClass(%q) = false; PHP class names are case-insensitive", written)
		}
		if got, ok := php.CanonicalThrowableClass(written); !ok || got != "Exception" {
			t.Errorf("CanonicalThrowableClass(%q) = %q,%v; want Exception -- get_class answers the declared spelling", written, got, ok)
		}
	}
	// instanceof folds on both sides.
	if !php.ThrowableIsA("typeerror", "ERROR") {
		t.Error(`ThrowableIsA("typeerror","ERROR") = false; catch (ERROR $t) catches a TypeError`)
	}
	if !php.ThrowableIsA("exception", "throwable") {
		t.Error(`ThrowableIsA("exception","throwable") = false`)
	}
	// And the disjointness survives folding, which is the fact a catch clause
	// rests on.
	if php.ThrowableIsA("typeerror", "exception") {
		t.Error("a TypeError must not match a catch (exception) clause")
	}
	// Throwable is still not constructible, whatever the spelling.
	if php.IsThrowableClass("throwable") {
		t.Error("Throwable reports as a class; it is an interface")
	}
	// A name that is not one of these stays unknown rather than folding into one.
	if _, ok := php.CanonicalThrowableClass("myexception"); ok {
		t.Error("a user class folded into the builtin hierarchy")
	}
}
