package interp

import (
	"fmt"
	"strings"

	pb "github.com/moriyoshi/php2go/internal/ir/irpb"
	"github.com/moriyoshi/php2go/runtime/php"
	"github.com/moriyoshi/php2go/runtime/stdlib"
)

// thrown carries a PHP exception out of the code that raised it. It travels as
// a Go error so that every existing return path propagates it without change;
// the interpreter loop intercepts it at each frame to look for a catch.
type thrown struct {
	object *php.Object
}

func (t *thrown) Error() string {
	message := ""
	if value, ok := t.object.Get("message"); ok {
		message = value.String()
	}
	return "Uncaught " + t.object.ClassName + ": " + message
}

// newThrowable builds an instance of an engine-provided exception class.
func newThrowable(ctx *php.Ctx, unit *Unit, class *Class, args []php.Value) *php.Object {
	object := unit.born(php.NewObject(class.Name, unit.handle()))
	recordThrowSite(ctx, unit, object)
	// Raised from inside the runtime, the arguments are this code's own and
	// cannot fail the signature.
	_ = throwableCtor(ctx, unit.throwableCtorScope(class), object, args)
	return object
}

// recordThrowSite stamps an exception with where it came from. PHP records this
// when the object is *created*, not when it is thrown, which is why getFile and
// getLine answer before a throw ever happens -- and the same is true of the
// stack trace.
func recordThrowSite(ctx *php.Ctx, unit *Unit, object *php.Object) {
	if ctx == nil {
		return
	}
	file, line := ctx.Location()
	object.Set("file", php.String(file))
	object.Set("line", php.Int(int64(line)))
	// The trace lives outside Props so that var_dump of an exception does not
	// grow a property PHP shows differently; it is engine state, not a value the
	// script assigned.
	trace := ctx.CallStack()
	if unit != nil && unit.setting("zend.exception_ignore_args") == "1" {
		// The setting is read where the trace is *captured*, so an ini_set
		// after the exception was made changes nothing about it. Dropping the
		// arguments here is what leaves `f()` rather than `f('secret')` in
		// every rendering of the trace at once.
		php.DropTraceArgs(trace)
	}
	object.Internal = &throwableState{trace: trace}
}

// throwableState is the part of an exception the engine owns rather than the
// script: for now, the stack captured where it was created.
type throwableState struct {
	trace []php.StackFrame
}

// traceOf returns the stack an exception captured, or nil for one built before
// any was recorded.
func traceOf(object *php.Object) []php.StackFrame {
	if state, ok := object.Internal.(*throwableState); ok {
		return state.trace
	}
	return nil
}

// throwableCtor applies Exception::__construct. Its parameters are message,
// code and previous, and every Throwable in the engine shares them, so one
// implementation covers the whole hierarchy.
func throwableCtor(ctx *php.Ctx, scope string, object *php.Object, args []php.Value) error {
	message := ""
	if len(args) > 0 {
		if args[0].Deref().Kind() == php.KindObject {
			// The engine's own signature is `string $message = ""`, and it is
			// enforced like any other: an object has no string form to coerce.
			return Raise("TypeError",
				"%s::__construct(): Argument #1 ($message) must be of type string, %s given",
				scope, givenTypeName(args[0].Deref()))
		}
		message = args[0].String()
	}
	var code int64
	if len(args) > 1 {
		// `int $code = 0` in weak mode: a float converts and reports the
		// precision it loses, a numeric string converts, and anything else is
		// refused. Only the *coercible* scalars get through.
		given := args[1].Deref()
		switch given.Kind() {
		case php.KindInt, php.KindTrue, php.KindFalse:
		case php.KindFloat:
			if ctx != nil && float64(int64(given.Float())) != given.Float() {
				ctx.Deprecated("Implicit conversion from float %s to int loses precision",
					php.DumpFloat(given.Float()))
			}
		case php.KindString:
			if _, numeric := given.IsNumericString(); !numeric {
				return Raise("TypeError",
					"%s::__construct(): Argument #2 ($code) must be of type int, string given",
					scope)
			}
		default:
			return Raise("TypeError",
				"%s::__construct(): Argument #2 ($code) must be of type int, %s given",
				scope, givenTypeName(given))
		}
		code = given.Int()
	}
	object.Set("message", php.String(message))
	object.Set("code", php.Int(code))
	object.Set("previous", php.Null())
	// ErrorException has a signature of its own --
	// (message, code, severity, filename, line, previous) -- so $previous is
	// argument #6 there rather than #3, and the three in between are its own.
	previousAt := 2
	if scope == "ErrorException" {
		previousAt = 5
		object.Set("severity", php.Int(errorExceptionSeverity(args)))
		if len(args) > 3 && args[3].Deref().Kind() != php.KindNull {
			object.Set("file", php.String(args[3].Deref().String()))
		}
		if len(args) > 4 && args[4].Deref().Kind() != php.KindNull {
			object.Set("line", php.Int(args[4].Deref().Int()))
		}
	}
	if len(args) > previousAt {
		// `?Throwable $previous = null`: only an object or null, and the
		// object has to be one.
		switch given := args[previousAt].Deref(); given.Kind() {
		case php.KindNull, php.KindUndef:
		case php.KindObject:
			object.Set("previous", args[previousAt])
		default:
			return Raise("TypeError",
				"%s::__construct(): Argument #%d ($previous) must be of type ?Throwable, %s given",
				scope, previousAt+1, givenTypeName(given))
		}
	}
	// file and line are not touched here: recordThrowSite has already stamped
	// them with the creation site, and __construct does not move them.
	return nil
}

// errorExceptionSeverity reads ErrorException's third parameter, whose default
// is E_ERROR rather than zero.
func errorExceptionSeverity(args []php.Value) int64 {
	if len(args) > 2 && args[2].Deref().Kind() != php.KindNull {
		return args[2].Deref().Int()
	}
	return 1
}

// throwableCtorScope names the class a Throwable's constructor belongs to,
// which is what its diagnostics say. Only three of the engine's classes declare
// one: Exception, Error and -- with a different signature -- ErrorException.
func (u *Unit) throwableCtorScope(class *Class) string {
	for _, name := range []string{"ErrorException", "Error"} {
		if u.instanceOf(class, name) {
			return name
		}
	}
	return "Exception"
}

// throwableMethod implements the accessors every Throwable provides. These are C
// functions in the engine, so nothing is harvested for them and they have to be
// supplied here.
func throwableMethod(ctx *php.Ctx, name string, this *php.Object) (php.Value, bool) {
	read := func(prop string) php.Value {
		value, _ := this.Get(prop)
		return value
	}
	switch strings.ToLower(name) {
	case "getmessage":
		return read("message"), true
	case "getcode":
		return read("code"), true
	case "getseverity":
		// ErrorException's own accessor. It reads the property the constructor
		// set, whose default is E_ERROR rather than zero.
		return read("severity"), true
	case "getprevious":
		return read("previous"), true
	case "getfile":
		return read("file"), true
	case "getline":
		return read("line"), true
	case "gettrace":
		// An exception's trace omits the receiver; debug_backtrace supplies it.
		return php.Arr(stdlib.TraceArray(ctx, traceOf(this), false)), true
	case "gettraceasstring":
		return php.String(php.TraceString(traceOf(this))), true
	case "__tostring":
		return php.String(throwableString(this)), true
	}
	return php.Undef(), false
}

// compileErrorLabel names the heading a compile failure is displayed under, if
// the class is one of those. The lookup is by name rather than by hierarchy:
// this runs while the request is ending, and a class table walk there would be
// answering a question about a program that has already stopped.
func compileErrorLabel(className string) (string, bool) {
	switch strings.ToLower(className) {
	case "parseerror":
		return "Parse error", true
	case "compileerror":
		return "Fatal error", true
	}
	return "", false
}

// throwableString is what `(string) $e` and an uncaught exception's report are
// both built from: the exception, its trace, and -- ahead of it -- everything it
// was chained to, oldest first.
//
//	RuntimeException: boom in file.php:1
//	Stack trace:
//	#0 file.php(2): inner()
//	#1 {main}
//
//	Next LogicException: wrap in file.php:2
//	Stack trace:
//	#0 {main}
func throwableString(object *php.Object) string {
	// The chain is followed outward-in and then rendered in reverse, since the
	// engine prints the cause before what it caused. A cycle would be a program
	// error rather than something to loop on.
	var chain []*php.Object
	seen := map[*php.Object]bool{}
	for current := object; current != nil && !seen[current]; {
		seen[current] = true
		chain = append(chain, current)
		previous, _ := current.Get("previous")
		current = previous.Deref().Object()
	}
	var out strings.Builder
	for i := len(chain) - 1; i >= 0; i-- {
		if i != len(chain)-1 {
			out.WriteString("\n\nNext ")
		}
		out.WriteString(oneThrowableString(chain[i]))
	}
	return out.String()
}

// chainsTo reports whether following an exception's causes reaches a given one.
// PHP refuses to make a link that would, since the chain is a list and not a
// graph.
func chainsTo(from, target *php.Object) bool {
	seen := map[*php.Object]bool{}
	for current := from; current != nil && !seen[current]; {
		if current == target {
			return true
		}
		seen[current] = true
		previous, _ := current.Get("previous")
		current = previous.Deref().Object()
	}
	return false
}

// oneThrowableString renders a single exception without its chain.
func oneThrowableString(object *php.Object) string {
	read := func(prop string) php.Value {
		value, _ := object.Get(prop)
		return value
	}
	// PHP drops the colon entirely for an exception with no message, so
	// `throw new Exception()` reports "Exception in ..." rather than leaving a
	// dangling separator.
	what := php.DisplayName(object.ClassName)
	if message := read("message").String(); message != "" {
		what += ": " + definedSuffix(object.ClassName, message)
	}
	return fmt.Sprintf("%s in %s:%d\nStack trace:\n%s",
		what, read("file").String(), read("line").Int(),
		php.TraceString(traceOf(object)))
}

// definedSuffix completes the "called in" clause an argument diagnostic carries.
// The message ends at the *call* site; rendering the exception adds the site the
// function was *defined* at, which the line after it already names -- so the
// text reads "... called in A on line 3 and defined in B:7".
//
// It applies to TypeError and ArgumentCountError *exactly*, not to their
// subclasses, and only when the message has the clause to complete. getMessage()
// never shows it: only `(string) $e` and the uncaught report do.
func definedSuffix(className, message string) string {
	switch strings.ToLower(className) {
	case "typeerror", "argumentcounterror":
	default:
		return message
	}
	if !strings.Contains(message, ", called in ") {
		return message
	}
	return message + " and defined"
}

// errorFileName is how zend_error names the file a diagnostic came from. A
// script that cleared the exception's own `file` leaves nothing to print, and
// the engine says so rather than printing an empty string.
func errorFileName(file string) string {
	if file == "" {
		return "Unknown"
	}
	return file
}

// settleMessages converts an exception's `message` to a string before it is
// reported. The property is ordinarily a string already, but a subclass may
// redeclare it and put an object there, and the engine's report is what asks
// for the string form -- so a __toString written to compute the message lazily
// runs exactly then. The whole `previous` chain is settled, since all of it is
// printed.
func (in *Interp) settleMessages(ctx *php.Ctx, unit *Unit, escaped *thrown) {
	if escaped == nil {
		return
	}
	seen := map[*php.Object]bool{}
	for current := escaped.object; current != nil && !seen[current]; {
		seen[current] = true
		if message, held := current.Get("message"); held &&
			message.Deref().Kind() == php.KindObject {
			if text, err := in.toString(ctx, unit, message.Deref()); err == nil {
				current.Set("message", text)
			}
		}
		previous, _ := current.Get("previous")
		current = previous.Deref().Object()
	}
}

// fatalErrorText renders an uncaught exception the way PHP does. The leading
// newline is PHP's, not a separator added here.
func (t *thrown) fatalErrorText() string {
	read := func(prop string) php.Value {
		value, _ := t.object.Get(prop)
		return value
	}
	// A failure to compile is reported as the compiler's own diagnostic: no
	// "Uncaught", no class name and no trace, because none of it was ever a
	// call. The two spellings are the engine's -- a syntax error is a "Parse
	// error", anything else it refused is a "Fatal error".
	if label, isCompile := compileErrorLabel(t.object.ClassName); isCompile {
		return fmt.Sprintf("\n%s: %s in %s on line %d\n",
			label, read("message").String(), errorFileName(read("file").String()),
			read("line").Int())
	}
	// The site named at the end is the outermost exception's -- the one that
	// actually escaped -- not the cause printed first. It is printed by
	// zend_error, which says "Unknown" for a file it does not have; the
	// exception's own `(string)` form leaves the same value empty.
	return fmt.Sprintf("\nFatal error: Uncaught %s\n  thrown in %s on line %d\n",
		throwableString(t.object), errorFileName(read("file").String()),
		read("line").Int())
}

// asExit reports whether an error is an exit rather than a failure. The type
// itself is `php.ExitError`, because a builtin in runtime/stdlib raises one too
// -- `trigger_error(E_USER_ERROR)` -- and both back-ends have to recognise it.
func asExit(err error) (*php.ExitError, bool) { return php.AsExit(err) }

// Raise builds a catchable failure of the named class. The type it produces
// moved to runtime/php -- the boundary the comment here used to describe is
// exactly that package's, and compiled builtins raise the same way.
func Raise(class, format string, args ...any) error {
	return php.Raise(class, format, args...)
}

// catchTarget finds where an exception raised at pc is handled within this
// frame. try_catch entries nest, so the innermost -- the one with the latest
// start -- wins.
// finallyRegion describes the block a throw was routed into, when that block is
// a finally rather than a catch.
type finallyRegion struct {
	viaFinally bool
	start, end int
}

// finallyTarget is catchTarget for an unwind no `catch` may see. A fiber being
// force-closed runs the `finally` blocks between the suspension point and the
// top of its stack, and nothing else: the engine's own exit is not an exception
// and no handler is offered it.
func (f *frame) finallyTarget(pc int) (target int, chosen finallyRegion, found bool) {
	best := -1
	for _, region := range f.op.GetTryCatch() {
		if pc < int(region.GetTryOp()) {
			continue
		}
		if region.GetFinallyOp() == 0 || pc >= int(region.GetFinallyOp()) {
			continue
		}
		start, handler := int(region.GetTryOp()), int(region.GetFinallyOp())
		if start > best || (start == best && handler < target) {
			best, target = start, handler
			chosen = finallyRegion{
				viaFinally: true,
				start:      handler,
				end:        int(region.GetFinallyEnd()),
			}
		}
	}
	if best < 0 {
		return 0, finallyRegion{}, false
	}
	return target, chosen, true
}

func (f *frame) catchTarget(pc int) (target int, chosen finallyRegion, found bool) {
	best := -1
	for _, region := range f.op.GetTryCatch() {
		if pc < int(region.GetTryOp()) {
			continue
		}
		// A region handles the throw at its catch clause, or -- for a try with
		// only a finally -- at the finally block, after which the exception
		// resumes its search.
		handler, finallyEntry := int(region.GetCatchOp()), false
		if region.GetCatchOp() == 0 || pc >= int(region.GetCatchOp()) {
			if region.GetFinallyOp() == 0 || pc >= int(region.GetFinallyOp()) {
				continue
			}
			handler, finallyEntry = int(region.GetFinallyOp()), true
		}
		start := int(region.GetTryOp())
		if start > best || (start == best && handler < target) {
			best, target = start, handler
			chosen = finallyRegion{viaFinally: finallyEntry}
			if finallyEntry {
				chosen.start = int(region.GetFinallyOp())
				chosen.end = int(region.GetFinallyEnd())
			}
		}
	}
	if best < 0 {
		return 0, finallyRegion{}, false
	}
	return target, chosen, true
}

// asThrown recognizes a PHP exception among the errors a call can return.
func asThrown(err error) (*thrown, bool) {
	var t *thrown
	if err == nil {
		return nil, false
	}
	if e, ok := err.(*thrown); ok {
		return e, true
	}
	return t, false
}

// throwError raises one of the engine's own error classes, which is how PHP
// reports a fault that user code is allowed to catch.
func (in *Interp) throwError(ctx *php.Ctx, unit *Unit, className, format string, args ...any) error {
	class, ok := unit.findClass(className)
	if !ok {
		return fmt.Errorf(format, args...)
	}
	return &thrown{object: newThrowable(ctx, unit, class, []php.Value{
		php.String(fmt.Sprintf(format, args...)),
	})}
}

// materialize turns a builtin's named error into a throwable object. Anything
// else passes through: a gap in the interpreter must not become catchable, or a
// test would pass while exercising nothing.
func (in *Interp) materialize(ctx *php.Ctx, unit *Unit, err error) error {
	// **An object a shared builtin threw**, as opposed to a class and a message
	// it raised. `assert(false, new RuntimeException("x"))` throws the object
	// it was given, and `php.Throw` is how runtime/stdlib says so; without this
	// arm it travelled out as a plain Go error and nothing could catch it.
	if object, thrownObject := php.AsThrown(err); thrownObject {
		return &thrown{object: object}
	}
	raised, ok := php.AsError(err)
	if !ok {
		return err
	}
	return in.throwError(ctx, unit, raised.Class, "%s", raised.Message)
}

// isThrowable reports whether a class can be thrown, which PHP requires.
func (u *Unit) isThrowable(class *Class) bool {
	return u.instanceOf(class, "Throwable") ||
		u.instanceOf(class, "Exception") || u.instanceOf(class, "Error")
}

// builtinMethodTarget reports whether a call lands on an engine-provided method
// rather than one with an op_array to execute.
func (u *Unit) builtinMethodTarget(class *Class, name string) bool {
	if _, _, found := u.resolveMethod(class, name); found {
		return false
	}
	return u.isThrowable(class)
}

var _ = (*pb.OpArray)(nil)

// throwChained is throwError with an exception that was already in flight
// recorded as the new one's `previous`. It is what the engine does when a
// diagnostic raised part-way through an operation reaches a user error handler
// that throws: the operation still reports its own failure, and the handler's
// exception hangs off it as the cause.
func (in *Interp) throwChained(ctx *php.Ctx, unit *Unit, previous error,
	className, format string, args ...any) error {
	class, ok := unit.findClass(className)
	if !ok {
		return fmt.Errorf(format, args...)
	}
	cause := php.Null()
	if raised, isThrown := asThrown(previous); isThrown && raised.object != nil {
		cause = php.Obj(raised.object)
	}
	return &thrown{object: newThrowable(ctx, unit, class, []php.Value{
		php.String(fmt.Sprintf(format, args...)), php.Int(0), cause,
	})}
}
