package php

import (
	"errors"
	"fmt"
	"strings"
)

// The engine-defined exception hierarchy, which compiled code has no other way
// to get.
//
// The interpreter names these classes by string and takes their *declarations*
// from the harvest -- the linked engine declares `Exception`, `TypeError` and
// the rest, so nothing on that path had to write them down. A compiled program
// has no harvest at run time, so every `catch` currently refuses with
// `instanceof undeclared class Exception`: the machinery is emitted and cannot
// run because the class does not exist.
//
// This declares them. It is the one place in `runtime/php` that knows a set of
// class names, and it is defensible for the reason the `E_*` constants are not:
// **these classes are part of the language rather than of a program.** A user
// class hierarchy still belongs to the layer with a class table; this is the
// floor that hierarchy sits on.
//
// # Every relationship below came from the engine
//
//	foreach ($classes as $c) { $r = new ReflectionClass($c); ... }
//
// which is how `ParseError extends CompileError` got in -- writing the list from
// memory would have made it extend `Error` directly, and `catch (Error $e)`
// would still have caught it, so the mistake would not have shown.

// throwableParents is the engine's hierarchy: class -> parent, with "" for a
// root. Every implementor of Throwable roots at Exception or Error.
var throwableParents = map[string]string{
	"Exception":                "",
	"Error":                    "",
	"ErrorException":           "Exception",
	"RuntimeException":         "Exception",
	"LogicException":           "Exception",
	"JsonException":            "Exception",
	"ReflectionException":      "Exception",
	"InvalidArgumentException": "LogicException",
	"BadFunctionCallException": "LogicException",
	"BadMethodCallException":   "BadFunctionCallException",
	"OutOfRangeException":      "LogicException",
	"DomainException":          "LogicException",
	"LengthException":          "LogicException",
	"UnexpectedValueException": "RuntimeException",
	"OutOfBoundsException":     "RuntimeException",
	"RangeException":           "RuntimeException",
	"OverflowException":        "RuntimeException",
	"UnderflowException":       "RuntimeException",
	"TypeError":                "Error",
	"ValueError":               "Error",
	"ArithmeticError":          "Error",
	"AssertionError":           "Error",
	"UnhandledMatchError":      "Error",
	"ClosedGeneratorException": "Exception",
	"FiberError":               "Error",
	"CompileError":             "Error",
	"ArgumentCountError":       "TypeError",
	"DivisionByZeroError":      "ArithmeticError",
	"ParseError":               "CompileError",
	// The date extension's (probed with get_parent_class): the
	// DateInterval constructor throws one.
	"DateException":                        "Exception",
	"DateError":                            "Error",
	"DateObjectError":                      "DateError",
	"DateRangeError":                       "DateError",
	"DateInvalidOperationException":        "DateException",
	"DateInvalidTimeZoneException":         "DateException",
	"DateMalformedStringException":         "DateException",
	"DateMalformedIntervalStringException": "DateException",
	"DateMalformedPeriodStringException":   "DateException",
}

// throwableByLower indexes the hierarchy for lookup, because **PHP class names
// are case-insensitive and the IR carries them lowercased** -- Zend folds them
// into the literal, so a compiled `new Exception` arrives as `"exception"`.
//
// Lookup folding and *display* are different questions, which is why this is a
// separate index rather than a change to DisplayName: the engine answers
// `get_class(new EXCEPTION("x"))` with **`"Exception"`**, the declared spelling
// and not the written one. So a consumer needs both -- match case-insensitively,
// then use the canonical name for everything observable.
var throwableByLower = func() map[string]string {
	out := make(map[string]string, len(throwableParents)+1)
	for name := range throwableParents {
		out[strings.ToLower(name)] = name
	}
	out[strings.ToLower(ThrowableInterface)] = ThrowableInterface
	return out
}()

// CanonicalThrowableClass folds a written class name to its declared spelling,
// and reports whether it names one of these at all. `exception` -> `Exception`.
//
// It is observable: `var_dump(new EXCEPTION("x"))` prints `object(Exception)`,
// so an object built under the written spelling would print the wrong class.
func CanonicalThrowableClass(name string) (string, bool) {
	canonical, ok := throwableByLower[strings.ToLower(DisplayName(name))]
	return canonical, ok
}

// ThrowableRoot is the class the private properties belong to -- `Exception` or
// `Error`. Checked rather than assumed: `class MyEx extends RuntimeException`
// dumps its trace as `["trace":"Exception":private]`, so the owner is the
// **root** and not the declaring class.
func ThrowableRoot(name string) string {
	c, ok := CanonicalThrowableClass(name)
	if !ok {
		return ""
	}
	for {
		parent := throwableParents[c]
		if parent == "" {
			return c
		}
		c = parent
	}
}

// ThrowableRootFor is ThrowableRoot for **any** class, a program's own
// included: `class X extends Error` walks through the program's parent hook
// to `Error`. ThrowableRoot alone knows only the engine's classes, and a
// user subclass fell back to `Exception` -- so an Error subclass dumped
// `["trace":"Exception":private]` and its own `$this->message = ...` was
// refused as `Cannot access protected property X::$message`, the property
// being declared by a class X is not related to (probed: both are fine).
// "" when no ancestor is an engine throwable.
func ThrowableRootFor(ctx *Ctx, class string) string {
	for c, i := class, 0; c != "" && i < 64; i++ {
		if root := ThrowableRoot(c); root != "" {
			return root
		}
		if ctx == nil || ctx.parentOf == nil {
			break
		}
		c = ctx.parentOf(c)
	}
	return ""
}

// ObjectThrowableRoot is the root an exception object was built with, for a
// reader with no ctx to walk the hierarchy: InitThrowable declares the
// private `trace` as the root's. `Exception` when nothing says otherwise,
// which is what every reader assumed before.
func ObjectThrowableRoot(o *Object) string {
	if root := ThrowableRoot(o.ClassName); root != "" {
		return root
	}
	if d, ok := o.Declared["trace"]; ok && IsThrowableClass(d.Class) {
		return d.Class
	}
	return "Exception"
}

// ThrowableInterface is the name every one of them implements. It is an
// interface rather than a class, so it has no parent and cannot be constructed.
const ThrowableInterface = "Throwable"

// IsThrowableClass reports whether name is one of the engine-defined throwable
// classes. It is false for `Throwable` itself, which is an interface -- `new
// Throwable` is not a thing, and the distinction is what stops it being
// constructed by accident.
func IsThrowableClass(name string) bool {
	canonical, ok := CanonicalThrowableClass(name)
	return ok && canonical != ThrowableInterface
}

// ThrowableParent answers the declared parent, and "" for a root or an unknown
// class.
func ThrowableParent(name string) string {
	canonical, ok := CanonicalThrowableClass(name)
	if !ok {
		return ""
	}
	return throwableParents[canonical]
}

// ThrowableIsA is `instanceof` over this hierarchy alone: does class descend
// from, or equal, want. `Throwable` matches every class here, since they all
// implement it.
//
// It answers only about the engine-defined classes. A user class extending
// `Exception` is the class table's business, and a caller that has one asks it
// first and falls through to this.
func ThrowableIsA(class, want string) bool {
	c, ok := CanonicalThrowableClass(class)
	if !ok {
		return false
	}
	w, ok := CanonicalThrowableClass(want)
	if !ok {
		return false
	}
	if w == ThrowableInterface {
		return c != ThrowableInterface
	}
	for {
		if c == w {
			return true
		}
		parent := throwableParents[c]
		if parent == "" {
			return false
		}
		c = parent
	}
}

// ThrowableClasses lists them, so a class table above can register the whole
// hierarchy without duplicating it.
func ThrowableClasses() []string {
	out := make([]string, 0, len(throwableParents))
	for name := range throwableParents {
		out = append(out, name)
	}
	return out
}

// IsThrowableName reports whether a name is one `ThrowableIsA` answers about,
// the `Throwable` interface included. `IsThrowableClass` deliberately says no
// to the interface, because a class is what that one is asked about.
func IsThrowableName(name string) bool {
	_, ok := CanonicalThrowableClass(name)
	return ok
}

// ThrowableNames is every name `ThrowableIsA` can answer about: the classes
// **and the `Throwable` interface**.
//
// Separate from ThrowableClasses because the two questions differ. A consumer
// asking what it may *instantiate* wants the classes; one asking what
// `instanceof` can test wants the interface too, and `instanceof Throwable` is
// the commonest test of all. Seeding the class list alone refused it in 68
// corpus files.
func ThrowableNames() []string {
	out := make([]string, 0, len(throwableByLower))
	for _, name := range throwableByLower {
		out = append(out, name)
	}
	return out
}

// InitThrowable fills the engine-defined properties on an object the caller
// made. It does **not** allocate the object, because a handle is not this
// package's to hand out: the interpreter numbers them from its own counter and
// registers each one with its cycle collector in the same breath, and a second
// source of handles would print the wrong `#N`.
//
// The properties are the engine's, in the engine's order -- `message`, `code`,
// `file`, `line`, `trace`, `previous` -- because `var_dump` of an exception
// prints them and the order is observable.
//
// `trace` is set to an empty array rather than left out: `getTrace()` answers
// `array(0) {}` for a throw at the top level, so empty is the engine's answer
// and not a placeholder. A caller with a call stack overwrites it.
func InitThrowable(ctx *Ctx, o *Object, message string, code int64, previous Value) *Object {
	file, line := ctx.ReportLocation()
	root := ThrowableRootFor(ctx, o.ClassName)
	if root == "" {
		root = "Exception"
	}
	// Order, names and **visibility** are all the engine's, and all three are
	// observable through var_dump:
	//
	//	["message":protected]=>  ["string":"Exception":private]=>
	//	["code":protected]=>     ["file":protected]=>  ["line":protected]=>
	//	["trace":"Exception":private]=>  ["previous":"Exception":private]=>
	//
	// The private ones belong to the **root**, not to the concrete class:
	// `class MyEx extends RuntimeException` still dumps
	// `["trace":"Exception":private]`. Checked, because declaring-class would
	// have been the natural guess and would print the wrong label.
	protected := func(name string, v Value) {
		o.PropSet(StringKey(MangleProperty("*", name)), v)
		o.Declare(name, Declaration{Class: root, Visibility: "protected"})
	}
	private := func(name string, v Value) {
		o.PropSet(StringKey(MangleProperty(root, name)), v)
		o.Declare(name, Declaration{Class: root, Visibility: "private"})
	}
	protected("message", String(message))
	private("string", String(""))
	protected("code", Int(code))
	protected("file", String(file))
	protected("line", Int(int64(line)))
	private("trace", Arr(NewArray()))
	if previous.IsUndef() {
		previous = Null()
	}
	private("previous", previous)
	return o
}

// The accessors, as functions rather than methods: `runtime/php` has no method
// dispatch, so the layer that does wires these to `getMessage()` and the rest.
// They read the property, which is what the engine's own implementations do.

func ThrowableMessage(o *Object) Value  { return throwableProp(o, "message") }
func ThrowableCode(o *Object) Value     { return throwableProp(o, "code") }
func ThrowableFile(o *Object) Value     { return throwableProp(o, "file") }
func ThrowableLine(o *Object) Value     { return throwableProp(o, "line") }
func ThrowablePrevious(o *Object) Value { return throwableProp(o, "previous") }

// throwableProp reads through the mangling. The properties are protected or
// private, so their keys carry the owner -- reading the plain name would find
// nothing, and would silently answer NULL for every accessor.
func throwableProp(o *Object, name string) Value {
	if o == nil || o.Props == nil {
		return Null()
	}
	root := ObjectThrowableRoot(o)
	for _, key := range []string{MangleProperty("*", name), MangleProperty(root, name), name} {
		if v, ok := o.PropGet(StringKey(key)); ok {
			return v.Deref()
		}
	}
	return Null()
}

// UncaughtBlock is what PHP prints for an uncaught throwable, **including its
// previous-chain**. Taken from the engine:
//
//	Fatal error: Uncaught Exception: A in f.php:1
//	Stack trace:
//	#0 {main}
//
//	Next RuntimeException: B in f.php:4
//	Stack trace:
//	#0 {main}
//	  thrown in f.php on line 4
//
// Three things are not guessable and were checked: the **root cause prints
// first** as `Uncaught` and each exception that wrapped it follows as `Next`,
// outward; the entries are separated by a blank line; and the closing
// `thrown in` names the **outermost** exception's file and line, not the root's.
//
// We printed the outermost alone, so every chained fatal lost its cause.
func UncaughtBlock(o *Object) string { return UncaughtBlockAt(o, "Fatal error") }

// UncaughtBlockAt is UncaughtBlock at another severity: `Warning: Uncaught`
// is what an ASSERT_BAIL exit prints for an exception its callback threw.
func UncaughtBlockAt(o *Object, level string) string {
	if label, _, ok := compileErrorForm(o); ok && level == "Fatal error" {
		return fmt.Sprintf("\n%s: %s in %s on line %d\n", label,
			ThrowableMessage(o).String(), thrownFile(o), ThrowableLine(o).Int())
	}
	return UncaughtTextAt(o, level, ThrowableChainString(o))
}

// ReportedAsError reports whether an uncaught throwable is printed as the
// error it stands for rather than through its `__toString` (compileErrorForm).
func ReportedAsError(o *Object, level string) bool {
	_, _, ok := compileErrorForm(o)
	return ok && level == "Fatal error"
}

// UncaughtTextAt is the block for an uncaught throwable whose `__toString`
// answered `text`. **The engine prints what `__toString` returns**, a
// program's override included -- `Uncaught CUSTOM` and then `thrown in`, with
// no trace of its own (probed) -- so the chain is only what the engine's
// `Exception::__toString` renders, and UncaughtBlockAt is that case.
func UncaughtTextAt(o *Object, level, text string) string {
	var b strings.Builder
	b.WriteString("\n" + level + ": Uncaught ")
	b.WriteString(text)
	// An empty file is `Unknown` on this line only -- the heading above
	// keeps it empty, `Uncaught a in :0` (bug64821.3).
	thrownIn := ThrowableFile(o).String()
	if thrownIn == "" {
		thrownIn = "Unknown"
	}
	fmt.Fprintf(&b, "\n  thrown in %s on line %d\n",
		thrownIn, ThrowableLine(o).Int())
	return b.String()
}

// compileErrorForm answers for the two throwables the engine reports
// **as the error they stand for** when nothing catches them: an uncaught
// ParseError prints `Parse error: MESSAGE in FILE on line N` and records
// E_PARSE, and a CompileError prints `Fatal error:` the same way and records
// E_COMPILE_ERROR -- no `Uncaught`, no trace, no previous chain (bug69640).
// Only the exact classes: a subclass is reported as any exception is
// (probed).
func compileErrorForm(o *Object) (label, recorded string, ok bool) {
	switch {
	case strings.EqualFold(o.ClassName, "ParseError"):
		return "Parse error", "Parse error", true
	case strings.EqualFold(o.ClassName, "CompileError"):
		return "Fatal error", "Compile error", true
	}
	return "", "", false
}

// thrownFile is the file a report names, `Unknown` when none was recorded.
func thrownFile(o *Object) string {
	if file := ThrowableFile(o).String(); file != "" {
		return file
	}
	return "Unknown"
}

// RecordUncaught makes an uncaught throwable the **last error**, as the
// engine's fatal does: error_get_last() in a shutdown function answers
// E_ERROR with `Uncaught ` and the chain, closed by `thrown` -- no `in
// FILE on line N`, which the file and line fields carry (bug61767).
func RecordUncaught(c *Ctx, o *Object) { RecordUncaughtAt(c, o, "Fatal error") }

// RecordUncaughtAt is RecordUncaught at another severity: an ASSERT_BAIL
// exit's `Warning: Uncaught` is the last error too, as E_WARNING.
func RecordUncaughtAt(c *Ctx, o *Object, level string) {
	if _, recorded, ok := compileErrorForm(o); ok && level == "Fatal error" {
		c.lastError = &Diagnostic{
			Level:   recorded,
			Message: ThrowableMessage(o).String(),
			File:    ThrowableFile(o).String(),
			Line:    uint32(ThrowableLine(o).Int()),
		}
		return
	}
	RecordUncaughtTextAt(c, o, level, ThrowableChainString(o))
}

// RecordUncaughtTextAt is RecordUncaughtAt for the text `__toString` answered
// (UncaughtTextAt).
func RecordUncaughtTextAt(c *Ctx, o *Object, level, text string) {
	c.lastError = &Diagnostic{
		Level:   level,
		Message: "Uncaught " + text + "\n  thrown",
		File:    ThrowableFile(o).String(),
		Line:    uint32(ThrowableLine(o).Int()),
	}
}

// MessagesAsStrings converts, for the length of a rendering, each message in
// an exception's chain to its string, as ConvertMessages does, and drops the
// failure a conversion raises: it is for the renderings that have nowhere to
// report one.
func MessagesAsStrings(ctx *Ctx, o *Object) (restore func()) {
	restore, _ = ConvertMessages(ctx, o)
	return restore
}

// ConvertMessages converts, for the length of a rendering, each message in an
// exception's chain that is not a string to its string: the engine's
// Exception::__toString reads the property through zval_get_string, so a
// message object's `__toString` runs and its text is printed
// (exception_delayed_message). The returned function puts the properties
// back, so nothing is stored that the engine would not have stored itself --
// a `__toString` that assigns the message, as that test's does, keeps what
// it assigned through its own reference.
//
// **The conversion is PHP's, failures included** (exception_009): a message
// that is an object with no `__toString` throws `Object of class stdClass
// could not be converted to string` out of the `__toString` rendering it, and
// an array warns `Array to string conversion` and reads `Array` (probed). The
// walk stops at the first failure, as the engine's does; `restore` is valid
// either way.
func ConvertMessages(ctx *Ctx, o *Object) (restore func(), err error) {
	type saved struct {
		o   *Object
		key Key
		v   Value
	}
	var undo []saved
	restore = func() {
		for _, u := range undo {
			u.o.PropSet(u.key, u.v)
		}
	}
	if ctx == nil {
		return restore, nil
	}
	seen := map[*Object]bool{}
	for cur := o; cur != nil && !seen[cur] && len(seen) < 64; {
		seen[cur] = true
		for _, name := range []string{MangleProperty("*", "message"), "message"} {
			key := StringKey(name)
			raw, ok := cur.PropGet(key)
			if !ok {
				continue
			}
			if d := raw.Deref(); d.Kind() != KindString {
				s, cerr := ToString(ctx, d)
				if cerr != nil {
					return restore, cerr
				}
				if now, _ := cur.PropGet(key); now.Deref().Kind() != KindString {
					undo = append(undo, saved{cur, key, raw})
					cur.PropSet(key, String(s.Deref().String()))
				}
			}
			break
		}
		prev := ThrowablePrevious(cur).Deref()
		if prev.Kind() != KindObject {
			break
		}
		cur = prev.Object()
	}
	return restore, nil
}

// ThrowableChainString is `Exception::__toString()`: the exception and its
// previous-chain, the **root cause first** and each wrapper after it as
// `Next ...`, separated by a blank line. `(string)$e` for a Dummy whose previous
// is Foo reads `Exception: Foo ...` and then `Next Exception: Dummy ...`.
func ThrowableChainString(o *Object) string {
	var chain []*Object
	seen := map[*Object]bool{}
	for cur := o; cur != nil && !seen[cur] && len(chain) < 64; {
		seen[cur] = true
		chain = append(chain, cur)
		prev := ThrowablePrevious(cur).Deref()
		if prev.Kind() != KindObject {
			break
		}
		cur = prev.Object()
	}
	var b strings.Builder
	for i := len(chain) - 1; i >= 0; i-- {
		if i != len(chain)-1 {
			b.WriteString("\n\nNext ")
		}
		b.WriteString(ThrowableString(chain[i]))
	}
	return b.String()
}

// ChainInFlight links an exception that was in flight onto one thrown from a
// `finally` that interrupted it. The finally's exception wins and the
// interrupted one becomes its ancestor -- settled by asking the engine:
//
//	try { throw new Exception("Bar", 0, $foo); }
//	finally { throw new Exception("Dummy"); }
//	// caught: Dummy -> Bar -> Foo
//
// It attaches at the **end** of the chain, not at the head: a finally throwing
// `new Exception("Fin", 0, $c)` over an in-flight `InFlight` gives
// `Fin -> C -> InFlight`. Nested finallys compose the same way.
//
// Nothing is linked when the in-flight exception is already somewhere in the
// chain, which is what the engine does and is also the only thing that keeps
// the walk finite: `try { throw new Exception("Bar2", 0, $b); } finally
// { throw $b; }` catches `$b` alone.
func ChainInFlight(thrown *Object, inFlight Value) {
	v := inFlight.Deref()
	if thrown == nil || v.Kind() != KindObject {
		return
	}
	// A generator's or fiber's close unwind is no exception to chain: what
	// a finally throws while it runs stands alone.
	if v.Object().ClassName == GeneratorCloseClass {
		return
	}
	prior := v.Object()
	// Walk to the end of the thrown exception's chain, remembering every node
	// on it: that set is what the link must not close a loop through.
	on := map[*Object]bool{}
	last := thrown
	for i := 0; i < 64 && !on[last]; i++ {
		on[last] = true
		next := ThrowablePrevious(last)
		if next.Deref().Kind() != KindObject {
			break
		}
		last = next.Deref().Object()
	}
	// **Both directions.** Checking only whether the interrupted exception is
	// already an ancestor of the thrown one misses the case the engine's own
	// test uses: `throw new Exception("Bar2", 0, $b)` interrupted by
	// `throw $b` -- there `$b` is the thrown one and `Bar2` carries it as an
	// ancestor, so linking `$b -> Bar2` closes the loop from the other side.
	// It did, and walking the chain afterwards never terminated.
	for p, i := prior, 0; p != nil && i < 64; i++ {
		if on[p] {
			return
		}
		next := ThrowablePrevious(p)
		if next.Deref().Kind() != KindObject {
			break
		}
		p = next.Deref().Object()
	}
	if last.Props == nil {
		return
	}
	root := ObjectThrowableRoot(last)
	last.PropSet(StringKey(MangleProperty(root, "previous")), Obj(prior))
}

// ThrowableString is `(string)$e`, which the engine renders as
//
//	Exception: boom in php2go-eval:1
//	Stack trace:
//	#0 {main}
//
// with the trace line coming from getTraceAsString. Taken from the engine, not
// composed from the parts.
func ThrowableString(o *Object) string {
	// An **empty message drops the separator entirely**: `throw new Exception()`
	// renders `Exception in t.php:1`, not `Exception: `. Checked against the
	// engine for both `new Exception()` and `new Exception("")`, which agree.
	// This read `%s: %s` unconditionally, which is right for every message it
	// was tested with and wrong for the one it was not.
	subject := DisplayName(o.ClassName)
	message := ThrowableMessage(o).String()
	if message != "" {
		subject += ": " + message
	}
	// **`and defined in`, not `in`**, when the message already names the *call
	// site*. A userland argument TypeError ends `, called in FILE on line N`,
	// and the position appended after it is where the function was **declared**
	// -- so the engine joins the two with `and defined`:
	//
	//	a(): Argument #1 ($obj) must be of type object, int given,
	//	called in t.php on line 7 and defined in t.php:4
	//
	// A return-type TypeError, an internal function's argument error and every
	// ordinary throw carry no call-site clause and take the plain ` in`. The
	// message is what decides, which is also how the engine decides it.
	joiner := " in "
	if strings.Contains(message, ", called in ") {
		joiner = " and defined in "
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s%s%s:%d\nStack trace:\n%s",
		subject, joiner, ThrowableFile(o).String(), ThrowableLine(o).Int(),
		ThrowableTraceAsString(o))
	return b.String()
}

// ThrowableTraceAsString is `getTraceAsString()`. With no frames recorded the
// engine prints the sentinel alone, which is what a throw at the top level
// gives -- and is still exactly what a caller that never records frames sees.
func ThrowableTraceAsString(o *Object) string {
	if o != nil && o.TraceReplaced {
		if v, ok := o.PropRaw(StringKey(MangleProperty(ObjectThrowableRoot(o), "trace"))); ok {
			if a := v.Deref().Array(); a != nil {
				return TraceArrayString(a, traceWarn)
			}
		}
	}
	if o == nil || o.Trace == nil {
		return "#0 {main}"
	}
	return TraceString(o.Trace)
}

// SetThrowableTrace records the call stack an exception was created on.
//
// It is separate from InitThrowable because the two have different owners: the
// message and code are the exception's own, while the stack belongs to whoever
// is running the program. Compiled code maintains a real stack and calls this;
// the interpreter does not yet, and leaving the field nil keeps its output
// unchanged rather than turning an empty trace into a wrong one.
func SetThrowableTrace(o *Object, stack []StackFrame) {
	if o != nil {
		o.Trace = stack
	}
}

// Thrown carries a PHP exception as a Go error, which is how a thrown value
// travels through compiled code and through this package's builtins.
//
// It is a type rather than a sentinel because the *object* is the payload: a
// `catch` has to test its class and bind it, so the error has to hand back the
// value rather than only a message.
type Thrown struct{ Object *Object }

func (t *Thrown) Error() string {
	if t == nil || t.Object == nil {
		return "php: thrown"
	}
	return DisplayName(t.Object.ClassName) + ": " + ThrowableMessage(t.Object).String()
}

// Value is the caught value, for binding to a catch clause's variable.
func (t *Thrown) Value() Value { return Obj(t.Object) }

// Throw wraps an already-built throwable as a Go error, which is the one step a
// raising builtin still needs. Building it stays with the caller for the handle
// reason above.
func Throw(o *Object) error { return &Thrown{Object: o} }

// AsThrown reports whether err is a PHP exception travelling as a Go error, and
// hands back the object so a catch can test and bind it.
func AsThrown(err error) (*Object, bool) {
	var t *Thrown
	if errors.As(err, &t) && t.Object != nil {
		return t.Object, true
	}
	return nil, false
}

// ThrowableFor is what a driver refusal *is*, in PHP's terms: the class to
// construct and the message to give it.
//
// The sentinels were deliberately class-free -- constructing an exception needs
// a class table, which this package did not have. It has one now for the
// engine-defined classes, so the mapping can live here instead of being
// rediscovered by every layer that reports one. Two consumers each writing
// `"Cannot start a fiber that has already been started"` is the divergence
// factory this whole boundary exists to avoid, and the messages are graded
// byte-exact.
//
// Every pair below is the engine's answer to the equivalent program, and the
// classes are not guessable: a generator's refusals are plain `Exception`, a
// fiber's are `FiberError`, and a bad `yield from` operand is `Error`. Three
// different roots for what reads like one category.
//
// The second result is false for an error that is not a PHP condition at all --
// a gap, a protocol violation, or something from the body itself. Those must not
// become exceptions: reporting a missing capability as a PHP error is the
// masquerade the quality gate forbids.
func ThrowableFor(err error) (class, message string, ok bool) {
	// A raised failure already names its class -- it *is* the answer.
	if raised, is := AsError(err); is {
		return raised.Class, raised.Message, true
	}
	switch {
	case errors.Is(err, ErrGeneratorRunning):
		// **Error**, where the two refusals below it are Exception. Re-asked of
		// the engine rather than kept in line with its neighbours: it is the
		// odd one out, and having it as Exception made the *class* wrong in
		// every test that catches this one specifically.
		return "Error", "Cannot resume an already running generator", true
	case errors.Is(err, ErrGeneratorClosed):
		return "Exception", "Cannot traverse an already closed generator", true
	case errors.Is(err, ErrYieldFromSelf):
		return "Error", "Impossible to yield from the Generator being currently run", true
	case errors.Is(err, ErrGeneratorNotReturned):
		return "Exception", "Cannot get return value of a generator that hasn't returned", true
	case errors.Is(err, ErrGeneratorAlreadyRun):
		return "Exception", "Cannot rewind a generator that was already run", true
	case errors.Is(err, ErrNotDelegatable):
		return "Error", `Can use "yield from" only with arrays and Traversables`, true
	case errors.Is(err, ErrFiberAlreadyStarted):
		return "FiberError", "Cannot start a fiber that has already been started", true
	case errors.Is(err, ErrFiberNotSuspended):
		return "FiberError", "Cannot resume a fiber that is not suspended", true
	case errors.Is(err, ErrFiberNotStarted):
		return "FiberError", "Cannot get fiber return value: The fiber has not been started", true
	case errors.Is(err, ErrFiberNotReturned):
		return "FiberError", "Cannot get fiber return value: The fiber has not returned", true
	case errors.Is(err, ErrFiberThrew):
		return "FiberError", "Cannot get fiber return value: The fiber threw an exception", true
	case errors.Is(err, ErrFiberSuspendClosed):
		return "FiberError", "Cannot suspend in a force-closed fiber", true
	case errors.Is(err, ErrFiberSwitchBlocked):
		return "FiberError", "Cannot switch fibers in current execution context", true
	}
	return "", "", false
}

// Error is a PHP-level failure a builtin raises by *naming* its class, before
// any object exists.
//
// The interpreter had this exact type and its comment states the boundary:
// "the builtins have no access to the class table, so they name the error class
// and the interpreter turns it into a real exception object on the way out."
// That is a description of `runtime/php`, so it lives here now and both
// consumers materialise it the same way.
//
// It is deliberately separate from Thrown. Thrown carries a *constructed*
// exception being propagated; this is a *request* to construct one, and the
// difference is the handle: numbering an object is the caller's, so a builtin
// several frames down cannot do it.
type Error struct {
	Class   string
	Message string
	// File and Line are where the error was **raised**, which is not where the
	// caller materialises it: `PopCall` restores the caller's position when the
	// callee returns, so by then the location names the call site. The engine
	// reports the raise site -- for a missing return, the closing brace -- and
	// the call site appears in the trace's first frame instead.
	File string
	Line uint32
	// Stack is the call stack **as it stood when the error was raised**, which
	// is not the stack the caller sees when it materialises one.
	//
	// A raised error travels as a Go error up through `callUser`, whose
	// deferred `PopCall` runs on the way out -- so by the time the top frame
	// turns it into an object, the frame that raised it has gone and the trace
	// read `#0 {main}` where the engine names the call. Snapshotting here is
	// the only point that still has it.
	Stack []StackFrame
	// Callee is a call the error was raised *inside*, at File and Line, when
	// that frame is no longer on the stack by the time it is materialised:
	// the `Fiber::suspend()` a force-closed fiber is resumed into.
	Callee string
	// callSite marks a refusal of a call's argument list that the callee's
	// prologue raised on the call's behalf (CallSiteError).
	callSite bool
	// Code is the exception's `code`, for the few library refusals that set
	// one -- a JsonException carries its JSON_ERROR_* (RaiseCode).
	Code int64
}

func (e *Error) Error() string { return e.Class + ": " + e.Message }

// RaisedCallee is the call an error names as its innermost frame, if any.
func RaisedCallee(err error) (string, bool) {
	var e *Error
	if errors.As(err, &e) && e.Callee != "" {
		return e.Callee, true
	}
	return "", false
}

// RaisedCode is the code a raised failure carries, zero when none.
func RaisedCode(err error) int64 {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return 0
}

// RaiseCode is Raise with the exception's `code` set.
func RaiseCode(class string, code int64, format string, args ...any) error {
	return &Error{Class: class, Message: fmt.Sprintf(format, args...), Code: code}
}

// Raise builds a catchable failure of the named class.
//
// It records no stack: most callers have no ctx to hand. RaiseAt is the form
// that does, and the two differ only in whether the trace survives.
func Raise(class, format string, args ...any) error {
	return &Error{Class: class, Message: fmt.Sprintf(format, args...)}
}

// RaiseAt is Raise with the call stack captured, for a failure raised **inside**
// a call whose frame the unwind will drop before anyone materialises it.
func RaiseAt(ctx *Ctx, class, format string, args ...any) error {
	e := &Error{Class: class, Message: fmt.Sprintf(format, args...)}
	if ctx != nil {
		e.Stack = ctx.ExceptionTrace()
		e.File, e.Line = ctx.ReportLocation()
	}
	return e
}

// AttachStack records where a failure was raised on one that did not say.
//
// Raise takes no ctx, so most failures carry no position and no trace; that is
// invisible until one is raised **inside a call whose frame the unwind drops**
// before anyone materialises it. A deferred initializer is exactly that, and
// PHP's trace names the frame: `#0 file(7): foo\foo()`. Whoever parks such a
// failure has a ctx even when whoever built it did not.
//
// An error that already recorded a position keeps it: the first site to know
// where it was is the right one.
func AttachStack(ctx *Ctx, err error) error {
	var e *Error
	if ctx == nil || !errors.As(err, &e) || e.File != "" {
		return err
	}
	e.Stack = ctx.ExceptionTrace()
	e.File, e.Line = ctx.ReportLocation()
	return err
}

// CallSiteError gives a refusal of a call's named arguments the trace of
// the call site. A callee the call site could not see binds the names in
// its own prologue (BindNamedArgs), which runs under the frame its caller
// pushed; the engine refuses an unknown or duplicate name before it enters
// the callee, so that frame is not in the trace (probed: `$c(a: 1)` against
// `function ($b)` traces from the caller, and through call_user_func from
// call_user_func's own frame).
func CallSiteError(ctx *Ctx, err error) error {
	var e *Error
	if ctx == nil || !errors.As(err, &e) || e.File != "" {
		return err
	}
	stack := ctx.ExceptionTrace()
	if n := len(stack); n > 0 {
		stack = stack[:n-1]
	}
	e.Stack = stack
	e.File, e.Line = ctx.Location()
	e.callSite = true
	return err
}

// DropCallSiteFrame removes a dispatching frame -- `Closure->__invoke` --
// from a call-site refusal raised beneath it: the engine refuses the names
// at the trampoline, which leaves no frame either (probed).
func DropCallSiteFrame(err error, function string) error {
	var e *Error
	if errors.As(err, &e) && e.callSite {
		if n := len(e.Stack); n > 0 && e.Stack[n-1].Function == function {
			e.Stack = e.Stack[:n-1]
		}
	}
	return err
}

// RaisedAt is where an error was raised, if it recorded a position.
func RaisedAt(err error) (string, uint32, bool) {
	var e *Error
	if errors.As(err, &e) && e.File != "" {
		return e.File, e.Line, true
	}
	return "", 0, false
}

// RaisedStack is the stack recorded when an error was raised, if any.
func RaisedStack(err error) ([]StackFrame, bool) {
	var e *Error
	if errors.As(err, &e) && len(e.Stack) > 0 {
		return e.Stack, true
	}
	return nil, false
}

// AsError reports a raised class-and-message failure. It is what a caller with
// a class table uses to turn one into a real exception.
func AsError(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

// traceWarn raises a warning on the request's context while a trace array
// renders. Package state, as traceStringLimit is: a trace string is rendered
// from places that hold no Ctx, and NewCtx installs it.
var traceWarn = func(string) {}

// TraceArrayString renders a trace **array** as the engine's
// zend_trace_to_string does, for a trace a program wrote itself (probed):
// each frame an array, numbered in the order rendered; a frame that is not an
// array warns "Expected array for frame N" with its key and is skipped; a
// `file` key gives `file(line)`, `line` read as an int and warning when it is
// not one (absent reads 0), and no `file` gives `[internal function]`; a
// file that is not a string warns and prints `[unknown file]` without reading
// the line; class, type and function print as given, `[unknown]` with a
// warning when present and not a string; `args` must be an array (else a
// warning and nothing), its string keys printed as names. A string frame key
// renders the engine's own garbage, so it is left to the caller to refuse
// (TraceArrayRenderable).
func TraceArrayString(a *Array, warn func(string)) string {
	var b strings.Builder
	n := 0
	a.Each(func(k Key, v Value) bool {
		frame := v.Deref().Array()
		if frame == nil {
			warn(fmt.Sprintf("Expected array for frame %d", k.Int))
			return true
		}
		fmt.Fprintf(&b, "#%d ", n)
		n++
		if file, ok := frame.Get(StringKey("file")); ok {
			if f := file.Deref(); f.Kind() == KindString {
				line := int64(0)
				if l, ok := frame.Get(StringKey("line")); ok {
					if l.Deref().Kind() == KindInt {
						line = l.Deref().Int()
					} else {
						warn("Line is not an int")
					}
				}
				fmt.Fprintf(&b, "%s(%d): ", f.String(), line)
			} else {
				warn("File name is not a string")
				b.WriteString("[unknown file]: ")
			}
		} else {
			b.WriteString(InternalFrameFile + ": ")
		}
		for _, part := range []string{"class", "type", "function"} {
			if x, ok := frame.Get(StringKey(part)); ok {
				if x.Deref().Kind() == KindString {
					b.WriteString(x.Deref().String())
				} else {
					warn("Value for " + part + " is not a string")
					b.WriteString("[unknown]")
				}
			}
		}
		b.WriteString("(")
		if args, ok := frame.Get(StringKey("args")); ok {
			if list := args.Deref().Array(); list != nil {
				first := true
				list.Each(func(ak Key, av Value) bool {
					if !first {
						b.WriteString(", ")
					}
					first = false
					if !ak.IsInt {
						b.WriteString(ak.Str + ": ")
					}
					b.WriteString(traceArg(av))
					return true
				})
			} else {
				warn("args element is not an array")
			}
		}
		b.WriteString(")\n")
		return true
	})
	fmt.Fprintf(&b, "#%d {main}", n)
	return b.String()
}

// TraceArrayRenderable reports whether TraceArrayString renders a as the
// engine would: every frame key an integer.
func TraceArrayRenderable(a *Array) bool {
	ok := true
	a.Each(func(k Key, _ Value) bool {
		ok = k.IsInt
		return ok
	})
	return ok
}
