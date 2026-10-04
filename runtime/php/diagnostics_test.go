package php_test

import (
	"strings"
	"testing"

	"github.com/moriyoshi/php2go/runtime/php"
)

// The whole point of this table is that **the kind is not guessable from the
// text**, so every row asserts the class as well as the message. Each was read
// back from the engine by running the condition and printing `get_class($e)`
// and `$e->getMessage()`.
func TestThrownDiagnosticsMatchTheEngine(t *testing.T) {
	obj := php.NewObject("stdClass", 1)
	for _, c := range []struct {
		err  error
		want string
	}{
		{php.ThrowUndefinedFunction("nope_fn"),
			`Error: Call to undefined function nope_fn()`},
		{php.ThrowUndefinedMethod("C", "nope"),
			`Error: Call to undefined method C::nope()`},
		// A method call on a non-object THROWS...
		{php.ThrowMethodCallOnNonObject("m", php.Int(5)),
			`Error: Call to a member function m() on int`},
		{php.ThrowMethodCallOnNonObject("m", php.Null()),
			`Error: Call to a member function m() on null`},
		{php.ThrowScalarAsArray(),
			`Error: Cannot use a scalar value as an array`},
		{php.ThrowObjectAsArray("stdClass"),
			`Error: Cannot use object of type stdClass as array`},
		{php.ThrowUndefinedConstant("UNKNOWN_CONST"),
			`Error: Undefined constant "UNKNOWN_CONST"`},
		{php.ThrowClassNotFound("NoSuchClass"),
			`Error: Class "NoSuchClass" not found`},
		{php.ThrowInaccessibleProperty("C", "private", "p"),
			`Error: Cannot access private property C::$p`},
		// ...and this one is a TypeError where every neighbour is an Error.
		{php.ThrowUnsupportedOperands(php.Arr(php.NewArray()), "+", php.Int(1)),
			`TypeError: Unsupported operand types: array + int`},
		// Two different messages, one class -- the class does not follow the
		// words, which is why they are separate constructors.
		{php.ThrowDivisionByZero(), `DivisionByZeroError: Division by zero`},
		{php.ThrowModuloByZero(), `DivisionByZeroError: Modulo by zero`},
		{php.ThrowMethodNameNotString(), `Error: Method name must be a string`},
		{php.ThrowAbstractInstantiation("A2"), `Error: Cannot instantiate abstract class A2`},
		// The rows the first version of this table got wrong, and why: every
		// row above was read back from the engine with an **unnamespaced**
		// name, so `%q` was correct for every input tested and wrong for a
		// whole class of inputs that were not. A namespaced name always
		// contains a separator, so this is the common case, not an exotic one.
		{php.ThrowClassNotFound(`Foo\Bar`), `Error: Class "Foo\Bar" not found`},
		{php.ThrowUndefinedConstant(`Foo\NOPE`), `Error: Undefined constant "Foo\NOPE"`},
		// And a name containing a percent must not be read as a format verb.
		{php.ThrowClassNotFound(`Ten%Off`), `Error: Class "Ten%Off" not found`},
	} {
		raised, ok := php.AsError(c.err)
		if !ok {
			t.Errorf("%v is not a raised PHP condition", c.err)
			continue
		}
		if got := raised.Class + ": " + raised.Message; got != c.want {
			t.Errorf("got  %s\nwant %s", got, c.want)
		}
		// A condition that names a class must name one that exists, or a caller
		// trusting this will try to construct something the engine has not got.
		if !php.IsThrowableClass(raised.Class) {
			t.Errorf("%s is not a throwable class", raised.Class)
		}
	}
	_ = obj
}

// The five that warn. Getting one of these wrong in the other direction --
// throwing where PHP warns -- stops a program that should have carried on, so
// the mechanism is asserted by the shape of the API: these return nothing.
func TestWarnedDiagnosticsMatchTheEngine(t *testing.T) {
	for _, c := range []struct {
		emit func(*php.Ctx)
		want string
	}{
		{func(c *php.Ctx) { c.WarnUndefinedVariable("undefined_var") },
			"Undefined variable $undefined_var"},
		// Quoted for a string key, bare for an int -- the detail that earns
		// this a helper.
		{func(c *php.Ctx) { c.WarnUndefinedArrayKey(php.IntKey(99)) },
			"Undefined array key 99"},
		{func(c *php.Ctx) { c.WarnUndefinedArrayKey(php.StringKey("k")) },
			`Undefined array key "k"`},
		// A colon after the word, where the array-key message has none.
		{func(c *php.Ctx) { c.WarnUndefinedProperty("C", "nope_prop") },
			"Undefined property: C::$nope_prop"},
		// Reading a property on an int WARNS where calling a method on one
		// THROWS -- the same subject, opposite mechanisms.
		{func(c *php.Ctx) { c.WarnPropertyOnNonObject("nothing", php.Int(5)) },
			`Attempt to read property "nothing" on int`},
		{func(c *php.Ctx) { c.WarnForeachOverNonIterable(php.Int(5)) },
			"foreach() argument must be of type array|object, int given"},
		// PHP wraps the bytes in quotes and escapes **nothing** -- not the
		// separator, and not an embedded quote either.
		{func(c *php.Ctx) { c.WarnUndefinedArrayKey(php.StringKey(`a\b`)) },
			`Undefined array key "a\b"`},
		{func(c *php.Ctx) { c.WarnUndefinedArrayKey(php.StringKey(`say "hi"`)) },
			`Undefined array key "say "hi""`},
		{func(c *php.Ctx) { c.WarnPropertyOnNonObject(`we\ird`, php.Int(5)) },
			`Attempt to read property "we\ird" on int`},
	} {
		var out strings.Builder
		ctx := php.NewCtx(&out)
		c.emit(ctx)
		if !strings.Contains(out.String(), c.want) {
			t.Errorf("emitted %q, want it to contain %q", out.String(), c.want)
		}
		if !strings.HasPrefix(strings.TrimSpace(out.String()), "Warning:") {
			t.Errorf("emitted %q, want a Warning -- throwing here stops a program that should carry on",
				strings.TrimSpace(out.String()))
		}
	}
}
