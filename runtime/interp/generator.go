package interp

import (
	"errors"
	"strings"

	"github.com/moriyoshi/php2go/runtime/php"
)

// errBecomeGenerator and errSuspended are control flow, not failures. They ride
// the error return so that the interpreter's several dozen propagation points
// need no second channel, and neither can be mistaken for a PHP exception --
// only *thrown is catchable.
var (
	errBecomeGenerator = errors.New("interp: frame is a generator body")
	errSuspended       = errors.New("interp: frame suspended at a yield")
)

// generator is a suspended function body plus where it got to.
//
// It holds a frame rather than running on a goroutine. PHP forbids yielding
// from anything but the generator's own body, so exactly one frame has to
// suspend -- and a saved frame cannot leak the way an abandoned goroutine
// blocked on a channel would.
type generator struct {
	frame *frame
	depth int
	// object is the Generator the program holds. The collector needs it: what
	// it decides reachability about is objects, and a generator reached only
	// through another generator's `yield from` is reached as this pointer.
	object *php.Object
	// started is false until the body has run to its first yield, which is what
	// current() and valid() trigger.
	started bool
	// finished is set when the body returned, after which the generator yields
	// nothing more.
	finished bool
	// advanced marks a generator that has been resumed past its first yield,
	// after which rewind() is refused.
	advanced bool
	// failed marks a generator whose body ended by throwing rather than by
	// returning. It is finished either way, but there is no return value to
	// hand out and getReturn() says so.
	failed bool
	// returned is what the body's return statement produced, read by
	// getReturn().
	returned php.Value
	// sent is what send() passes in, delivered to the yield that suspended.
	sent php.Value
	// pendingThrow is what throw() supplied, raised at the yield the generator
	// is suspended on rather than at the call site.
	pendingThrow error
	// running guards against a generator being resumed from inside its own
	// body. PHP rejects that; without the guard it is unbounded recursion.
	running bool
}

// errGeneratorClose unwinds a generator that was started, suspended and will
// never be resumed. Like a fiber's exit it is not an exception -- nothing can
// catch it -- but the finally blocks on the way out still run, which is the
// only reason PHP resumes an abandoned generator at all.
var errGeneratorClose = errors.New("generator close")

// closeGenerator runs the finally blocks of a generator nobody can reach any
// more. PHP does it when the Generator object is destroyed; php2go has no
// refcounts, so it happens where the collector notices -- the same timing its
// destructors have.
func (in *Interp) closeGenerator(ctx *php.Ctx, unit *Unit, g *generator) {
	err := in.closeGeneratorFor(ctx, unit, g)
	if err == nil {
		return
	}
	if escaped, isThrown := asThrown(err); isThrown {
		// A finally block that throws during the unwind has nowhere to go: the
		// program that abandoned the generator is not on the stack any more.
		in.settleMessages(ctx, unit, escaped)
		ctx.Write(escaped.fatalErrorText())
	}
}

// closeGeneratorFor is closeGenerator for a caller that is still on the stack
// and can carry what the unwind threw. A finally block that throws while the
// generator is being abandoned replaces whatever abandoned it.
func (in *Interp) closeGeneratorFor(ctx *php.Ctx, unit *Unit, g *generator) error {
	if g == nil || !g.started || g.finished || g.running || g.frame == nil {
		return nil
	}
	g.pendingThrow = errGeneratorClose
	// What still runs from here is finally blocks, and they may not suspend.
	g.frame.forceClosing = true
	err := in.advance(ctx, unit, g)
	g.finished = true
	if err == nil || errors.Is(err, errGeneratorClose) {
		// The unwind reaching the top of the body is the expected end.
		return nil
	}
	return err
}

// newGenerator wraps a frame that stopped at ZEND_GENERATOR_CREATE.
func (u *Unit) newGenerator(f *frame, depth int) *php.Object {
	object := u.born(php.NewObject("Generator", u.handle()))
	object.Internal = &generator{
		object: object, frame: f, depth: depth,
		returned: php.Null(), sent: php.Null(),
	}
	return object
}

func asGenerator(value php.Value) (*generator, bool) {
	if value.Kind() != php.KindObject {
		return nil, false
	}
	g, ok := value.Object().Internal.(*generator)
	return g, ok
}

// resumedByMethod reports whether the innermost frame is a Generator method,
// which is the case that already contributes a call site of its own.
// resumedInternally reports whether whatever resumed the generator was the
// engine rather than a line of script. A `Generator->` method and any builtin
// -- iterator_to_array, array_map over a generator -- both are: the body has no
// call site of its own to name, and PHP writes "[internal function]" for it. A
// `foreach` is not, and the body is reported at the loop's line.
func (in *Interp) resumedInternally(unit *Unit, ctx *php.Ctx) bool {
	stack := ctx.Stack()
	if len(stack) == 0 {
		return false
	}
	caller := stack[len(stack)-1].Function
	if strings.HasPrefix(caller, "Generator->") {
		return true
	}
	if strings.ContainsAny(caller, ":->") {
		// A method or a closure is script, unless it is one of the engine's
		// own, which the Generator case above already covered.
		return false
	}
	lower := strings.ToLower(caller)
	if _, declared := unit.functions[lower]; declared {
		return false
	}
	_, builtin := builtins[lower]
	return builtin || in.engineFunctions[lower]
}

// start runs the body up to its first yield, which is what any inspection of a
// generator has to do first.
func (in *Interp) start(ctx *php.Ctx, unit *Unit, g *generator) error {
	if g.started {
		return nil
	}
	if g.running {
		return Raise("Error", "Cannot resume an already running generator")
	}
	g.started = true
	err := in.advance(ctx, unit, g)
	// Reaching the first yield is not "already run": the start is what rewind
	// itself does, so a generator sitting on its first yielded value may still
	// be rewound.
	g.advanced = false
	return err
}

// advance resumes the body until it yields again or finishes.
func (in *Interp) advance(ctx *php.Ctx, unit *Unit, g *generator) error {
	if g.finished {
		return nil
	}
	if g.running {
		return Raise("Error", "Cannot resume an already running generator")
	}
	g.running = true
	defer func() { g.running = false }()
	// Resuming is what a rewind can no longer undo. Reaching the first yield is
	// not: `yield from []` followed by `yield 4` runs both inside the start, so
	// a generator sitting on its first yielded value may still be rewound.
	g.advanced = true
	if pending := g.pendingThrow; pending != nil {
		// throw() raises at the yield the generator is suspended on, so a
		// try/catch around that yield inside the body is what catches it.
		g.pendingThrow = nil
		target, caught := g.frame.raise(pending, g.frame.pc-1)
		if !caught {
			g.finished = true
			g.failed = true
			return pending
		}
		g.frame.pc = target
	} else {
		// The value send() supplied is what the suspended yield evaluates to.
		line := g.frame.op.GetOplines()
		if g.frame.pc > 0 && g.frame.pc <= len(line) {
			if previous := line[g.frame.pc-1]; previous.GetResultType() != opUnused &&
				in.name(previous.GetOpcode()) == "ZEND_YIELD" {
				g.frame.storeResult(previous, g.sent)
			}
		}
	}
	g.sent = php.Null()

	// The body's own frame goes on the stack while it runs. Where it says it
	// was called from depends on what resumed it: an explicit
	// Generator->rewind() is a frame of its own, and the body below it has no
	// call site of its own to name -- but a foreach resumes without any such
	// frame, and then the body is reported at the foreach's line.
	site, line := ctx.Location()
	if in.resumedInternally(unit, ctx) {
		site, line = php.InternalFrameFile, 0
	}
	ctx.PushCall(php.StackFrame{
		Function: generatorTraceName(g.frame), File: site, Line: line,
		Args: g.frame.args, This: g.frame.this,
	})
	value, err := in.execFrame(ctx, unit, g.frame, g.depth)
	ctx.PopCall()
	switch {
	case errors.Is(err, errSuspended):
		return nil
	case err != nil:
		g.finished = true
		g.failed = true
		return err
	default:
		g.finished = true
		g.returned = value
		g.frame.yieldKey, g.frame.yieldValue = php.Undef(), php.Undef()
		return nil
	}
}

func (g *generator) valid() bool { return !g.finished }

func (g *generator) current() php.Value {
	if g.finished {
		return php.Null()
	}
	// While delegating, current() is the *inner* generator's current value read
	// live rather than whatever this one last copied out of it. Two generators
	// delegating to one source both have to see where the source is now: the
	// engine proxies, it does not snapshot.
	return g.innermost().frame.yieldValue
}

// innermost follows the `yield from` chain to the generator actually producing
// values. The walk is iterative and bounded: a generator delegating to itself
// is a cycle PHP refuses to build, but nothing here should hang if one exists.
func (g *generator) innermost() *generator {
	current := g
	for hops := 0; hops < maxDelegateHops; hops++ {
		if current.frame == nil || current.frame.delegate == nil {
			break
		}
		inner := current.frame.delegate.gen
		if inner == nil || inner.finished {
			break
		}
		current = inner
	}
	return current
}

// maxDelegateHops bounds the `yield from` chain walk. Real chains are a handful
// deep; this only has to stop a cycle from running away.
const maxDelegateHops = 1000

func (g *generator) key() php.Value {
	if g.finished {
		return php.Null()
	}
	return g.innermost().frame.yieldKey
}

// builtinMethodCall dispatches a method on an engine-provided class. The
// Throwable accessors are plain property reads, but a Generator method runs
// user code, so it needs the interpreter rather than a standalone function.
func (in *Interp) builtinMethodCall(ctx *php.Ctx, unit *Unit, call *pendingCall) (php.Value, error) {
	if value, handled, err := directoryObjectMethod(call); handled {
		return value, in.materialize(ctx, unit, err)
	}
	if value, handled, err := in.reflectionMethod(ctx, unit, call); handled {
		return value, in.materialize(ctx, unit, err)
	}
	if value, handled, err := in.fiberMethod(ctx, unit, call, 0); handled {
		return value, in.materialize(ctx, unit, err)
	}
	if value, handled, err := in.weakMethod(ctx, unit, call.this, call.builtinMethod, call.args); handled {
		return value, in.materialize(ctx, unit, err)
	}
	if g, ok := call.this.Internal.(*generator); ok {
		value, err := in.generatorMethod(ctx, unit, g, call.builtinMethod, call.args)
		return value, in.materialize(ctx, unit, err)
	}
	if _, ok := call.this.Internal.(*closure); ok {
		value, err := in.closureMethod(ctx, unit, call.this, call.builtinMethod, call.args)
		return value, in.materialize(ctx, unit, err)
	}
	if value, handled, err := in.arrayStoreMethod(ctx, unit, call.this, call.builtinMethod, call.args); handled {
		return value, in.materialize(ctx, unit, err)
	}
	if strings.EqualFold(call.this.ClassName, "Deprecated") &&
		strings.EqualFold(call.builtinMethod, "__construct") {
		value, err := in.deprecatedConstruct(ctx, unit, call.this, call.args)
		return value, in.materialize(ctx, unit, err)
	}
	if value, handled, err := emptyIteratorMethod(unit, call); handled {
		return value, in.materialize(ctx, unit, err)
	}
	if value, handled, err := in.callbackFilterMethod(ctx, unit, call, 0); handled {
		return value, in.materialize(ctx, unit, err)
	}
	if value, handled, err := sensitiveMethod(call); handled {
		return value, in.materialize(ctx, unit, err)
	}
	if value, ok := throwableMethod(ctx, call.builtinMethod, call.this); ok {
		return value, nil
	}
	return php.Undef(), in.undefinedEngineMethod(ctx, unit, call.this.ClassName,
		call.builtinMethod, &UnsupportedError{Opcode: "internal method " + call.name})
}

// undefinedEngineMethod tells a method php2go has not implemented from one the
// engine does not have either. The class's harvested method list is the judge:
// a name it does not carry is the program's mistake and a catchable Error,
// while a name it does carry but nothing here implements stays an honest gap.
func (in *Interp) undefinedEngineMethod(ctx *php.Ctx, unit *Unit,
	className, method string, gap error) error {
	if class, known := unit.findClass(className); known && unit.isBuiltinOnly(class) {
		if _, engineHas := unit.engineMethod(class, method); !engineHas {
			return in.throwError(ctx, unit, "Error",
				"Call to undefined method %s::%s()", php.DisplayName(className), method)
		}
	}
	return gap
}

// generatorMethod implements the Generator class, whose methods are C functions
// in the engine and so have nothing harvested to execute.
func (in *Interp) generatorMethod(ctx *php.Ctx, unit *Unit, g *generator, name string, args []php.Value) (php.Value, error) {
	switch strings.ToLower(name) {
	case "current":
		if err := in.start(ctx, unit, g); err != nil {
			return php.Undef(), err
		}
		return g.current(), nil
	case "key":
		if err := in.start(ctx, unit, g); err != nil {
			return php.Undef(), err
		}
		return g.key(), nil
	case "valid":
		if err := in.start(ctx, unit, g); err != nil {
			return php.Undef(), err
		}
		return php.Bool(g.valid()), nil
	case "next":
		if err := in.start(ctx, unit, g); err != nil {
			return php.Undef(), err
		}
		return php.Null(), in.advance(ctx, unit, g)
	case "send":
		// send() on an unstarted generator runs to the first yield and sends
		// into that one, so the first yielded value is skipped.
		if err := in.start(ctx, unit, g); err != nil {
			return php.Undef(), err
		}
		if len(args) > 0 {
			g.sent = args[0]
		}
		if err := in.advance(ctx, unit, g); err != nil {
			return php.Undef(), err
		}
		return g.current(), nil
	case "rewind":
		// A generator cannot be rewound once it has moved on; starting it is
		// all rewind() does.
		if err := in.start(ctx, unit, g); err != nil {
			return php.Null(), err
		}
		if g.advanced {
			return php.Undef(), Raise("Exception",
				"Cannot rewind a generator that was already run")
		}
		return php.Null(), nil
	case "throw":
		// An object is not enough: what is thrown has to be throwable, and an
		// ordinary one is refused before the generator is even started.
		throwable := false
		if len(args) > 0 && args[0].Deref().Kind() == php.KindObject {
			if class, known := unit.findClass(args[0].Deref().Object().ClassName); known {
				throwable = unit.isThrowable(class)
			}
		}
		if !throwable {
			return php.Undef(), Raise("TypeError",
				"Generator::throw(): Argument #1 ($exception) must be of type Throwable, %s given",
				givenTypeName(first(args).Deref()))
		}
		if err := in.start(ctx, unit, g); err != nil {
			return php.Undef(), err
		}
		raised := &thrown{object: args[0].Deref().Object()}
		if g.finished {
			// Nothing is suspended, so the exception simply escapes here.
			return php.Undef(), raised
		}
		g.pendingThrow = raised
		if err := in.advance(ctx, unit, g); err != nil {
			return php.Undef(), err
		}
		return g.current(), nil
	case "getreturn":
		// Asking a generator that has not run yet primes it, which for one
		// whose body returns before its first yield is enough to answer.
		if err := in.start(ctx, unit, g); err != nil {
			return php.Undef(), err
		}
		if !g.finished || g.failed {
			// A body that threw is finished but has not *returned*, so there
			// is nothing to hand out and the same refusal applies.
			return php.Undef(), Raise("Exception",
				"Cannot get return value of a generator that hasn't returned")
		}
		return g.returned, nil
	}
	return php.Undef(), in.undefinedEngineMethod(ctx, unit, "Generator", name,
		&UnsupportedError{Opcode: "Generator::" + name})
}

// yieldsByRef reports whether the generator function was declared
// `function &gen()`, which is what makes `foreach ($gen as &$v)` legal.
// debugInfo is what var_dump shows for a Generator. The engine describes one
// through a debug-info handler rather than by its properties, which it has
// none of: the single entry names the function whose body it is running.
func (g *generator) debugInfo() *php.Array {
	info := php.NewArray()
	if g == nil || g.frame == nil {
		return info
	}
	name := g.frame.op.GetName()
	if g.frame.class != nil {
		name = php.DisplayName(g.frame.class.Name) + "::" + name
	}
	info.Set(php.StringKey("function"), php.String(name))
	return info
}

func (g *generator) yieldsByRef() bool {
	return g != nil && g.frame != nil && g.frame.op.GetReturnsRef()
}

// generatorTraceName is what a backtrace calls a generator body. The op_array
// carries only the bare name, so the declaring class has to be put back --
// with `->` for a body running on an instance and `::` for one that is not,
// exactly as any other frame is spelled.
func generatorTraceName(f *frame) string {
	name := f.op.GetName()
	if f.class == nil {
		return name
	}
	if f.this != nil {
		return php.DisplayName(f.class.Name) + "->" + name
	}
	return php.DisplayName(f.class.Name) + "::" + name
}
