package php

import (
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
)

// Ctx owns everything a request touches.
//
// This is the design invariant of the whole runtime: PHP is shared-nothing per
// request, a Go process is not. Every superglobal, static variable, class
// static, ini setting and open resource lives here, and generated code takes a
// *Ctx as its first parameter. Nothing may be stored at package scope, or two
// concurrent requests will observe each other's data.
type Ctx struct {
	// suspendedDrains are frame drains waiting on a destructor that suspended its fiber.
	suspendedDrains map[*frameDrain]struct{}
	// random is the request's default Mt19937 engine (RandomCall), made on
	// first use.
	random *MT19937
	// runtimeState holds request-local attachments owned by runtime adapters.
	runtimeState map[any]any
	// requestEndClosers are the resources CloseRequestResources closes as
	// the request ends (userfilter.go).
	requestEndClosers []requestEndCloser
	out               io.Writer

	// libraryEntries names the library functions this program implements in
	// bundled PHP (SetLibraryEntries). Their bodies, and whatever they call
	// that is not the program's, are one internal function to the engine.
	libraryEntries map[string]bool

	// Globals is the variable scope of the top-level script.
	Globals *Array
	// superglobals names global slots that every function frame must bind
	// automatically. Extensions implemented in bundled PHP can register their
	// request-local state without teaching the compiler about extension-specific
	// variable names.
	superglobals map[string]struct{}
	// defineSuperglobal is the back-end's store for a superglobal a library
	// defines (superglobals.go); autoGlobals is the harvested list of the
	// just-in-time ones the program names.
	defineSuperglobal SuperglobalDefiner
	autoGlobals       []string
	// called is what `static::` resolves to, which is not always the scope --
	// an inherited static method reached through a subclass runs with the
	// subclass as its called scope.
	called string
	// resources hands out the ids open handles are known by. Per-Ctx, since a
	// resource belongs to one request.
	resources Resources
	// readSource hands back a file's bytes from a loader, when there is one.
	readSource SourceReader
	// objectToString calls an object's __toString; countObject its count().
	objectToString ObjectStringer
	countObject    ObjectCounter
	// newObject allocates an object, which needs a handle only the program can
	// number.
	newObject ObjectMaker
	// assertions is assert()'s configuration for this request.
	assertions *Assertions
	// ini is what the script has written to a setting, when nothing above
	// supplies its own storage.
	ini      map[string]string
	iniStore IniStore
	// exportProps answers what an object shows under var_export.
	exportProps ExportProperties
	// hideSensitive replaces an argument a backtrace must not show.
	hideSensitive SensitiveHider
	// buffers is the ob_start stack. The innermost receives every write, which
	// is what makes nesting work.
	buffers []*outputBuffer

	// traversable is the hop into user code that iterating a PHP object needs,
	// installed by the layer that has a class table. See traversable.go.
	traversable ResolveTraversable
	// subclassOf answers class-hierarchy questions for the visibility rule.
	subclassOf SubclassOf
	// weakRefs is the WeakReference handed out for each object, so a second
	// WeakReference::create() of it answers the same one (NewWeakReferenceFor).
	weakRefs map[*Object]*Object
	// parentOf names a class's parent, "" for none: what `parent` means in
	// a property's declared type.
	parentOf func(class string) string
	// resumableMaker builds the frame behind a callable, which is what a Fiber
	// drives. See fiberbind.go.
	resumableMaker ResumableMaker
	// declaredProps answers a class's declared properties. See propdesc.go.
	declaredProps DeclaredProperties
	// The three questions the shared lazy-object machinery cannot answer for
	// itself. See lazyresolve2.go.
	magicAvailable  MagicAvailable
	proxyCompatible ProxyCompatible
	lazyInitCall    LazyInitCall
	// classIsFinal answers the one class fact the lazy reset rule needs.
	classIsFinal ClassIsFinal
	// destructible is every object whose class declares __destruct, for
	// script-end teardown. See destruct.go.
	destructible []*Object
	// handleOrder makes script-end teardown walk the objects store in handle
	// order (SetHandleOrderTeardown). See destruct.go.
	handleOrder bool
	// generators are the Generators started in this request, so that the
	// ones still suspended when it ends can be closed (CloseLiveGenerators).
	generators []*Generator
	// stepAsDelegate is set by a generator about to step the one it yields
	// from, and taken by that generator's entry (Generator.step), so its
	// frame is marked Delegate.
	stepAsDelegate bool
	// fibers are the Fibers started in this request, closed with the
	// generators (CloseLiveGenerators).
	fibers        []*Fiber
	destructorRun DestructorRun
	// The two questions a weak container cannot answer for itself. See weak.go.
	weakObserve WeakObserve
	weakRelease WeakRelease
	// currentFiber is the fiber whose body is running right now. Fibers nest,
	// so this is a stack discipline through EnterFiber rather than a flag.
	currentFiber *Object
	// fiberSwitchBlocked counts execution contexts that refuse a fiber switch.
	fiberSwitchBlocked int
	// closingFibers counts force-closes under way: the fiber being destroyed
	// is the one its unwinding code runs in, though nothing has entered it.
	closingFibers int
	// arrayAccess is the hop into user code that indexing a PHP object needs,
	// installed by the layer that has a class table. See arrayaccess.go.
	arrayAccess ArrayAccessCall
	// objectProps is the same shape for var_dump: what an object shows. See
	// dump.go.
	objectProps ObjectProperties
	// argNames carries the names a call attached to its **trailing** arguments
	// for a variadic callee to collect. See SetArgNames.
	argNames []string
	// inConstExpression counts the nested constant-expression evaluations.
	// Only the outermost gets the synthetic `[constant expression]` frame.
	//
	// Named for `runtime/interp`'s field of the same name, which counts the
	// same thing. **Not** `constDepth`, which is a different counter there --
	// the recursion bound on deferred initializer evaluation, paired with
	// `maxConstDepth`. Two counters over constant expressions that mean
	// different things, so the names have to stay apart.
	inConstExpression int
	// releaseHandle hands an object's number back to the generated program's
	// free list; see SetReleaseHandle.
	releaseHandle func(*Object)
	// lazyResolver initializes a lazy object for a library function that has
	// to see the real state. See lazyresolve.go.
	lazyResolver LazyResolver
	// visibleProps answers what an object presents to a scope. See
	// lazyresolve.go.
	visibleProps VisibleProperties
	// hasMethod and notSerializable are the two class-table questions
	// `serialize` asks. See lazyresolve.go.
	hasMethod HasMethodFn
	// programMethod is hasMethod narrowed to the program's own bodies. See
	// arraystore.go.
	programMethod HasMethodFn
	// objectRefWalk is how a by-reference foreach over a variable walks the
	// plain object the variable holds. See RefSubject.
	objectRefWalk ObjectRefWalk
	classFacts    ClassFactsFn
	// engineHasClass and autoloadClass serve unserialize meeting a class it
	// cannot find (incomplete.go).
	engineHasClass func(string) bool
	autoloadClass  func(*Ctx, string) (bool, error)
	propertyFacts  PropertyFactsFn
	newInstance    NewInstanceFn
	enumCase       EnumCaseFn
	// call invokes a PHP callable, for the builtins that take one. See
	// callable.go.
	call CallValue
	// The stream registries. Per-Ctx because registering a wrapper or a filter
	// is something a *request* does; see streamregistry.go.
	streamWrappers     map[string]*StreamWrapper
	streamWrapperOrder []string
	streamFilters      map[string]string
	streamFilterOrder  []string
	openUserStream     UserStreamOpener
	// callableReason decides whether a value can be called, without calling
	// it. Installed beside call, by the same layer.
	callableReason CallableReason
	// errorHandlers is the stack set_error_handler pushes. Per request, and
	// shared by both consumers -- see callable.go.
	errorHandlers []Value
	// The other callable registries, same reasoning: a callable stored now and
	// invoked later by a trigger that lives above this package.
	exceptionHandlers []Value
	shutdown          []ShutdownCall
	autoloaders       []Value

	// constants are those the script defined. They hang off the context rather
	// than a package variable because PHP is shared-nothing per request and a
	// long-lived Go process is not: a package-level table would leak one
	// request's constants into the next.
	constants map[string]Value

	// errorReporting is the mask error_reporting() maintains. A diagnostic
	// whose level is outside it is not printed, which tests rely on: many set
	// error_reporting(0) and expect clean output.
	errorReporting int64
	silenceDepth   int
	silenceBase    int64

	// resultUnused says the call in progress throws away what it returns, which
	// array_splice's rebuild depends on. It is stored the negative way round so
	// that a zero context means the ordinary case: the result is wanted.
	resultUnused bool

	// file and line locate whatever is executing, so a diagnostic can say where
	// it came from. PHP prints that as part of the message, and a test's
	// expected output contains it.
	file string
	line uint32

	// lastError is the most recent diagnostic, kept whether or not it was
	// printed: error_get_last() answers about what happened.
	lastError *Diagnostic

	// diagnostics collects warnings and notices. PHP would print these; keeping
	// them addressable lets a harness compare against expected output.
	diagnostics []Diagnostic

	// stack is the chain of calls in progress, innermost last. An exception
	// snapshots it when it is created, which is what getTrace reports and what
	// the "Stack trace:" block of an uncaught fatal error prints.
	stack []StackFrame
	// pushes counts the frames ever pushed. Code the program wrote runs only
	// in a frame, so an unchanged count across an operation proves no such
	// code saw what the operation was handed (Pushes).
	pushes uint64
	// handOver holds the objects a call site gives up to the compiled
	// function it is about to enter (HandOver), until that function takes
	// them (TakeHandOver) or the call site drops them (DropHandOver).
	handOver []*Object
	// handOverAt is the push count when handOver was made: its callee is
	// entered at most one push later.
	handOverAt uint64
	// traceHeld is what an exception's trace holds of the arguments frames
	// took over and left when they threw (HoldInTrace).
	traceHeld map[*Object][]Value
	// argStack holds the argument slots of the calls in progress that took
	// theirs from it (PushCallArgs), innermost last, so a compiled call does
	// not allocate a slice per call. A frame's argMark says where its slots
	// begin, and PopCall gives them back.
	argStack []Value

	// errorHandler is what set_error_handler installed. inErrorHandler stops a
	// diagnostic raised inside it from being fed back to it, and pendingError
	// carries out an exception it threw.
	errorHandler   ErrorHandler
	inErrorHandler bool
	pendingError   error
	// enteredFibers are the fibers running now, outermost first (EnterFiber).
	enteredFibers []*Object
	// instanceConfined answers whether code running on an object of a class
	// may keep `$this` (SetInstanceConfined).
	instanceConfined func(class string) bool
	// shutdownDtorDepth is the stack depth the shutdown walk calls a
	// destructor at, or 0 when none is running (DrainingShutdownDestructor).
	shutdownDtorDepth int
	callSite          *StackFrame
	// bufferFlusher runs an output buffer's filter; flushingChunk keeps a write
	// from inside one from re-entering the overflow check.
	bufferFlusher func(filter any, text string, phase int64) (string, bool, error)
	flushingChunk bool

	// scope names the class whose body is executing. A builtin does not change
	// it, so a builtin that has to decide visibility -- get_object_vars,
	// get_class_vars, a callable naming self:: -- sees its caller's scope,
	// which is exactly the one PHP consults.
	scope string
}

// RuntimeState returns the value stored under key, creating it once for this
// request when none is present. Keys must be comparable and owned by the
// caller; the state dies with the Ctx.
func (c *Ctx) RuntimeState(key any, create func() any) any {
	if value, ok := c.runtimeState[key]; ok {
		return value
	}
	value := create()
	if c.runtimeState == nil {
		c.runtimeState = make(map[any]any)
	}
	c.runtimeState[key] = value
	return value
}

// Scope reports the class whose body is executing, or "" at top level.
func (c *Ctx) Scope() string { return c.scope }

// EnterScope sets the executing class and returns the previous one, which the
// caller restores when the frame ends.
func (c *Ctx) EnterScope(class string) string {
	previous := c.scope
	c.scope = class
	return previous
}

// CalledScope reports what `static::` means right now: the class the call site
// named, which differs from Scope whenever an inherited method is reached
// through a subclass. Empty at top level.
func (c *Ctx) CalledScope() string { return c.called }

// EnterCalledScope sets it and returns the previous one, the same way
// EnterScope does.
func (c *Ctx) EnterCalledScope(class string) string {
	previous := c.called
	c.called = class
	return previous
}

// StackFrame is one entry of a PHP stack trace. File and Line locate the *call
// site*, not the callee -- that is the frame's own line in PHP's rendering.
type StackFrame struct {
	Function string
	File     string
	Line     uint32
	Args     []Value
	// ArgNames names the trailing arguments that arrived under a name no
	// parameter claimed -- only a variadic callee has any, and they are always
	// last. A trace renders them as `name: value` and reports them under a
	// string key, which is the only place they are still visible once the frame
	// has been built. A name may itself be empty: `f("" => 1)` is legal and
	// renders as `: 1`.
	ArgNames []string
	// Sensitive marks the arguments whose parameter carries
	// `#[\SensitiveParameter]`. The value is still here -- the frame is what
	// the call really passed -- and it is the *rendering* that must not show
	// it, which is why this is a mark rather than a substitution: an object
	// made eagerly would consume a handle even when no trace is ever taken.
	Sensitive []bool
	// This is the receiver of a method call, which debug_backtrace reports and
	// an exception's trace does not.
	This *Object
	// LSB is the class the **call named**, which is what `static::` means
	// inside the body. It differs from the declaring class the Function field
	// carries: `Test2::test()` on a `test` declared by Test1 runs Test1's body
	// with `static` meaning Test2. Empty outside a class.
	LSB string
	// ArgsDropped marks a frame whose arguments were deliberately not kept --
	// `zend.exception_ignore_args`. It is not the same as a call that had no
	// arguments: the engine *omits* the `args` key entirely for the first and
	// reports an empty array for the second.
	ArgsDropped bool
	// argMark is one more than where this frame's arguments start on the
	// Ctx's argument stack, or zero for a frame whose Args are its own slice.
	argMark int
	// ints are the arguments of a call made with plain ints
	// (PushCallInts), kept unboxed and pointer-free; nInts says how many.
	// Args is built from them when something reads the frame (Stack,
	// CallStack), which only a trace or a backtrace does.
	ints  [4]int64
	nInts int8
	// general marks a frame that may hold anything -- pushed through
	// PushCall, or an int frame whose Args was built -- so PopCall clears it.
	// An int frame sets only the fields PushCallInts writes, which is what
	// lets the next one at the same depth write just those.
	general bool
	// Internal marks a frame that belongs to an **engine function** rather than
	// to PHP code. It exists because the two consumers push frames differently:
	// the interpreter pushes one for every call, library functions included,
	// and compiled code pushes none for a builtin. A reader that wants "the
	// frame of the PHP function that called me" -- `func_get_args`,
	// `debug_print_backtrace` -- cannot simply drop the top frame, because in
	// compiled code that is already the answer. Dropping *trailing* internal
	// frames is right for both, and is right for the interesting case too: a
	// callback invoked by `array_map` sees `array_map`'s frame, which is not
	// trailing and must stay.
	Internal bool
	// Live are the addresses of the callee's parameter slots, by argument
	// position, for the parameters its body writes (nil elsewhere). The
	// engine keeps a call's arguments *in* those slots, so a trace or
	// func_get_args() reports what a parameter holds now -- `$a = 5` shows 5,
	// `unset($a)` shows NULL -- where the frame's Args hold what was passed.
	// Set by BindLiveArgs; read through CurrentArgs.
	Live []*Value
	// Delegate marks a generator's frame entered as another generator's
	// `yield from` source rather than by a driver of its own. Only
	// LeaveGeneratorFrame reads it: the chain's driver is the frame under
	// the last delegate.
	Delegate bool
}

// CurrentArgs is the frame's arguments as the engine would report them now:
// Args, with each parameter the body writes read from its slot -- NULL once
// unset, the referenced value for a by-reference one. A frame with no live
// slots answers its Args as they are.
func (f StackFrame) CurrentArgs() []Value {
	if len(f.Live) == 0 {
		return f.Args
	}
	out := append([]Value(nil), f.Args...)
	for i, slot := range f.Live {
		if slot == nil || i >= len(out) {
			continue
		}
		v := *slot
		if v.IsUndef() {
			out[i] = Null()
			continue
		}
		out[i] = v.Deref().Share()
	}
	return out
}

// BindLiveArgs gives the innermost frame the addresses of the parameters its
// body writes, so the frame reads them where a trace is taken (StackFrame.Live).
// The callee calls it once its parameters are bound; only a frame naming the
// callee is touched, so a call path that pushed none leaves the caller's frame
// alone -- the same guard HideTopCall keeps.
func (c *Ctx) BindLiveArgs(function string, live []*Value) {
	if len(c.stack) == 0 {
		return
	}
	f := &c.stack[len(c.stack)-1]
	if !frameNames(f.Function, function) {
		return
	}
	f.Live = live
	// Cleared by PopCall with the rest of a general frame, so an int frame
	// pushed later at this depth never sees another call's slots.
	f.general = true
}

// CurrentArgs is the innermost frame's arguments read through its live slots,
// for a compiled func_get_args(): args is the body's own argument slice,
// answered as-is when the frame keeps no slots or is not this call's.
func (c *Ctx) CurrentArgs(args []Value) []Value {
	if len(c.stack) == 0 {
		return args
	}
	f := c.stack[len(c.stack)-1]
	if len(f.Live) == 0 || len(f.Args) != len(args) {
		return args
	}
	f.Args = args
	return f.CurrentArgs()
}

// frameNames reports whether a frame's Function names the callee `function`:
// the plain name, or the method under any receiver spelling (`C->m`, `C::m`).
func frameNames(frame, function string) bool {
	name := strings.ToLower(frame)
	want := strings.ToLower(function)
	if name == want || strings.HasSuffix(name, "->"+want) || strings.HasSuffix(name, "::"+want) {
		return true
	}
	// A closure's frame spells its file as the program refers to itself,
	// which need not be the path the body was compiled under.
	return strings.HasPrefix(want, "{closure") && strings.Contains(name, "{closure")
}

// IsSensitive reports whether the nth argument must be hidden from a trace.
func (f StackFrame) IsSensitive(i int) bool {
	return i < len(f.Sensitive) && f.Sensitive[i]
}

// SensitiveArgRendering is what a trace prints in place of a hidden argument.
const SensitiveArgRendering = "Object(SensitiveParameterValue)"

// ArgName answers the name the nth argument arrived under, reporting false for
// a positional one.
func (f StackFrame) ArgName(i int) (string, bool) {
	first := len(f.Args) - len(f.ArgNames)
	if i < first || i-first >= len(f.ArgNames) {
		return "", false
	}
	return f.ArgNames[i-first], true
}

// PushCall records that a call is starting. Every push needs a matching PopCall.
// InternalFrameFile marks a frame the engine entered on its own -- resuming a
// generator, calling a callback -- which a backtrace renders without a file or
// line because there is no call site to name.
const InternalFrameFile = "[internal function]"

// NoActiveFile is where the engine is when no script is running: a shutdown
// function is called from here, so an error its own library call raises is
// `in [no active file]:0` and the call's frame renders as internal
// (bug51827, bug71221).
const NoActiveFile = "[no active file]"

// IsIncludeFunction reports the pseudo-function an include's frame is named
// after. No user function can take one of these names: they are keywords.
// DropTraceArgs is DEBUG_BACKTRACE_IGNORE_ARGS and zend.exception_ignore_args:
// every frame loses its arguments **except an include's**, whose file name is
// not an argument to the engine but part of how it describes the frame, and is
// still shown (probed: `include('/path/x.inc')` under both settings).
func DropTraceArgs(stack []StackFrame) {
	for i := range stack {
		if IsIncludeFunction(stack[i].Function) {
			continue
		}
		stack[i].Args, stack[i].ArgNames = nil, nil
		stack[i].ArgsDropped = true
	}
}

func IsIncludeFunction(name string) bool {
	switch name {
	case "include", "include_once", "require", "require_once":
		return true
	}
	return false
}

// IsInternalFile reports a frame the engine entered on its own, which a trace
// renders as `[internal function]` with no line.
func IsInternalFile(file string) bool { return file == InternalFrameFile || file == NoActiveFile }

func (c *Ctx) PushCall(frame StackFrame) {
	// **The frame records how the arguments arrived**, which is what a trace
	// renders: `test(2, b: Object(...))`, and what `debug_backtrace()` keys its
	// `args` array by. The names are on the Ctx because the argument slice is
	// positional; this is where they become part of the call.
	//
	// Not cleared here: the callee's variadic collection takes them, and this
	// runs first.
	if frame.ArgNames == nil {
		frame.ArgNames = c.argNames
	}
	// **Called from inside a library function?** Then the call site is not a
	// line of PHP, and the engine says so: a callback reached through
	// `array_map` renders as `#0 [internal function]: boom(1)`, not as the file
	// and line of the `array_map(...)` call.
	//
	// The frame below is the library function's own, marked by
	// `MarkInternalCall`, so the question is answerable here rather than at
	// every site that might invoke a callback. `runtime/interp` sets this
	// explicitly at its two callback sites and so is unaffected; compiled
	// output had no equivalent and printed the caller's file throughout.
	if n := len(c.stack); n > 0 && c.stack[n-1].Internal && frame.File != InternalFrameFile {
		if site := c.callSite; site != nil {
			// Except when the engine calls back **at** a place in the script:
			// an error handler runs where the diagnostic was raised, and says
			// so. `runtime/interp` reads the same field at its own push.
			frame.File, frame.Line = site.File, site.Line
		} else {
			frame.File, frame.Line = InternalFrameFile, 0
		}
	}
	frame.general = true
	c.stack = append(c.stack, frame)
	c.pushes++
}

// Pushes is the number of frames pushed so far. Compared across an
// operation, an unchanged count means no function, method, hook, handler or
// destructor ran inside it -- which is how compiled code knows a refused store
// handed its value to nothing that could have kept it.
func (c *Ctx) Pushes() uint64 { return c.pushes }

// PushCallArgs is PushCall for a compiled call to a known function: the
// arguments are copied onto the Ctx's argument stack and the frame's Args is
// that region, so the call allocates nothing -- a slice per call, kept because
// the frame shows it in traces, was a tenth of a recursive function's time.
// The slots are the frame's until PopCall, which clears and releases them, so
// anything that outlives the call has to copy them: CallStack does. The callee
// must not keep the slice either, which is why a generator -- whose frame
// outlives the call that made it -- is never called this way.
func (c *Ctx) PushCallArgs(function, file string, line uint32, args []Value) []Value {
	mark := len(c.argStack)
	c.argStack = append(c.argStack, args...)
	own := c.argStack[mark:len(c.argStack):len(c.argStack)]
	n := len(c.stack)
	if n == cap(c.stack) || n > 0 && c.stack[n-1].Internal {
		// Growing, or called from inside a library function: the general
		// push, which knows what that means for the frame's position.
		c.PushCall(StackFrame{Function: function, File: file, Line: line, Args: own, argMark: mark + 1})
		return own
	}
	// **Built in place**: the frame is ~150 bytes, and passing it to PushCall
	// by value and appending it copied it twice per call.
	c.stack = c.stack[:n+1]
	c.pushes++
	f := &c.stack[n]
	*f = StackFrame{Function: function, File: file, Line: line, Args: own, ArgNames: c.argNames, argMark: mark + 1, general: true}
	return own
}

// PushCallInts is PushCallArgs for a call whose arguments are all plain ints,
// at most four: they are kept in the frame as int64s, so the push copies no
// values and allocates nothing, and Args is materialised only if a trace asks.
func (c *Ctx) PushCallInts(function, file string, line uint32, n int, a0, a1, a2, a3 int64) {
	top := len(c.stack)
	if top == cap(c.stack) || top > 0 && c.stack[top-1].Internal {
		c.PushCall(StackFrame{Function: function, File: file, Line: line,
			Args: intArgs(n, [4]int64{a0, a1, a2, a3})})
		return
	}
	c.stack = c.stack[:top+1]
	c.pushes++
	// **Only the fields an int frame uses.** The slot is clean apart from
	// them: a general frame is cleared when it is popped, and an int frame
	// sets nothing else. Writing a whole frame literal zeroed ~190 bytes per
	// call and was most of the push.
	f := &c.stack[top]
	f.Function, f.File, f.Line = function, file, line
	f.ints = [4]int64{a0, a1, a2, a3}
	f.nInts = int8(n)
	f.ArgNames = c.argNames
}

func intArgs(n int, ints [4]int64) []Value {
	out := make([]Value, n)
	for i := range out {
		out[i] = Int(ints[i])
	}
	return out
}

// materialise gives every frame kept with unboxed int arguments its Args,
// for a reader of the frames.
func (c *Ctx) materialise() {
	for i := range c.stack {
		if f := &c.stack[i]; f.nInts > 0 {
			f.Args = intArgs(int(f.nInts), f.ints)
			f.nInts = 0
			f.general = true
		}
	}
}

// EnterConstExpression opens a constant expression's evaluation and reports
// whether this is the outermost one. A constant expression that reaches through
// another moves the reported position again but adds no second frame, so only
// the outermost may push one.
//
// The depth lives here rather than on either back-end because both ask the same
// question; `runtime/interp` keeps its own on the Unit and is unaffected.
func (c *Ctx) EnterConstExpression() bool {
	c.inConstExpression++
	return c.inConstExpression == 1
}

// LeaveConstExpression closes what EnterConstExpression opened.
func (c *Ctx) LeaveConstExpression() { c.inConstExpression-- }

// ConstExpressionFrame is the name the engine gives a constant expression's
// synthetic frame. It has no arguments and no callee: it records where the
// program was when an initializer was forced.
const ConstExpressionFrame = "[constant expression]"

// Stack is the call stack as it stands, outermost first.
func (c *Ctx) Stack() []StackFrame {
	c.materialise()
	return c.stack
}

// HideTopCall takes the innermost frame off the stack until the returned
// function puts it back, for a diagnostic the engine raises **before** the
// call is entered -- a `#[\Deprecated]` function's deprecation, whose trace
// has no frame for the function itself -- but which compiled code raises from
// the callee's prologue, after the caller pushed that frame. The argument
// stack is left alone, so the frame's own arguments are intact when it
// returns; anything pushed in between is popped before it does.
//
// Only a frame naming `function` is taken -- the callee's own, whatever
// the receiver spelling (`test`, `C->m`, `C::m`) -- so a call path that
// pushed none leaves the caller's frame where it is.
func (c *Ctx) HideTopCall(function string) func() {
	c.materialise()
	if len(c.stack) == 0 {
		return func() {}
	}
	top := c.stack[len(c.stack)-1]
	name := strings.ToLower(top.Function)
	want := strings.ToLower(function)
	if name != want && !strings.HasSuffix(name, "->"+want) && !strings.HasSuffix(name, "::"+want) {
		return func() {}
	}
	c.stack = c.stack[:len(c.stack)-1]
	return func() { c.stack = append(c.stack, top) }
}

// PopCall records that the innermost call has returned, and **restores the
// location to the call site**.
//
// Execution resumes where the call was made, and the frame already records
// exactly that -- the caller pushed it with its own position. Without this the
// location is left wherever the callee's body last set it, so a diagnostic
// raised after a call reports a line inside the function that already returned.
// Two calls on one source line show it plainly: `f(); g();` emits one
// `SetLocation`, so `g`'s diagnostic inherited `f`'s last line.
func (c *Ctx) PopCall() {
	if len(c.stack) == 0 {
		return
	}
	f := &c.stack[len(c.stack)-1]
	c.stack = c.stack[:len(c.stack)-1]
	file, line, mark := f.File, f.Line, f.argMark
	if f.general {
		// Cleared so the slot is clean for an int frame (PushCallInts writes
		// only its own fields), and so the stack holds nothing the program
		// has let go of.
		*f = StackFrame{}
	}
	if mark > 0 {
		// The arguments are cleared as well as released, for the same reason.
		m := mark - 1
		clear(c.argStack[m:])
		c.argStack = c.argStack[:m]
	}
	if file != "" {
		c.file, c.line = file, line
	}
}

// CallStack snapshots the calls in progress, innermost last. The copy matters:
// the live slice keeps changing as the program runs, and an exception must
// report the stack as it was when it was created.
// SwapCallStack replaces the call stack and returns the one it displaced. A
// Fiber runs on its own stack: while it is suspended its frames belong to it,
// not to whoever resumes it next, and a backtrace taken meanwhile must not see
// them.
func (c *Ctx) SwapCallStack(replacement []StackFrame) []StackFrame {
	c.materialise()
	previous := c.stack
	c.stack = replacement
	return previous
}

// ExceptionTrace is CallStack as an exception records it: under
// zend.exception_ignore_args the frames keep no arguments -- read when the
// trace is captured, so an ini_set after the exception was made changes
// nothing about it (exception_ignore_args).
func (c *Ctx) ExceptionTrace() []StackFrame {
	stack := c.CallStack()
	if v, held := c.IniRead("zend.exception_ignore_args"); held {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "", "0", "off", "false", "no", "none":
		default:
			DropTraceArgs(stack)
		}
	}
	return stack
}

func (c *Ctx) CallStack() []StackFrame {
	c.materialise()
	out := append([]StackFrame(nil), c.stack...)
	for i := range out {
		// **An include's frame names the file only from inside a function**
		// the included code called: `#1 f.php(3): require('/path/x.inc')`
		// under a function frame, and `require()` when the trace starts in
		// the included body itself or in a library call it made -- the
		// engine names the file from the frame it processed before, and
		// neither of those has one (probed).
		if IsIncludeFunction(out[i].Function) && (i+1 >= len(out) || out[i+1].Internal) {
			out[i].Args = nil
		}
		// A parameter the body wrote is reported as it stands now, which is
		// what the engine's CV slot holds when the trace is taken.
		if out[i].Live != nil {
			out[i].Args = out[i].CurrentArgs()
			out[i].Live = nil
			out[i].argMark = 0
		}
		// A frame whose arguments live on the argument stack gets its own
		// copy: the slots are reused as soon as the call returns, and an
		// exception's trace is read long after.
		if out[i].argMark > 0 {
			out[i].Args = append([]Value(nil), out[i].Args...)
			out[i].argMark = 0
		}
		// **A by-reference argument is recorded by its value now**: the
		// trace shows what the parameter held when it was taken, not what
		// the box holds when the trace is printed -- `$item` still NULL in
		// gh16188, assigned an Exception a line later.
		for _, a := range out[i].Args {
			if _, boxed := a.RefOf(); boxed {
				args := make([]Value, len(out[i].Args))
				for j, v := range out[i].Args {
					if _, isRef := v.RefOf(); isRef {
						v = v.Deref().Share()
					}
					args[j] = v
				}
				out[i].Args = args
				break
			}
		}
	}
	return c.hideLibraryFrames(out)
}

// SetLibraryEntries names the library functions implemented in bundled PHP.
func (c *Ctx) SetLibraryEntries(names map[string]bool) { c.libraryEntries = names }

// libraryInternal is a frame a bundled library body makes of its own: a
// helper in the library's namespace or a closure written there.
func libraryInternal(name string) bool {
	return strings.HasPrefix(name, `PHP2Go\`) || strings.HasPrefix(name, `{closure:PHP2Go\`)
}

// hideLibraryFrames is what a trace shows of a library function written in
// PHP: its own frame, called from the program, and nothing it called on the
// way -- the engine's function is one frame (probed:
// `#0 f.php(3): array_multisort(Array, 2)` for a conversion Error raised in
// its comparison). A callback it makes into the program renders as
// `[internal function]`, as it does from any engine function.
func (c *Ctx) hideLibraryFrames(frames []StackFrame) []StackFrame {
	if len(c.libraryEntries) == 0 {
		return frames
	}
	kept := frames[:0]
	inside := false
	for _, f := range frames {
		name := f.Function
		if inside {
			if libraryInternal(name) || c.libraryEntries[strings.ToLower(name)] {
				continue
			}
			if _, builtin := LookupBuiltin(name); builtin && !strings.Contains(name, "->") && !strings.Contains(name, "::") {
				continue
			}
			f.File, f.Line = InternalFrameFile, 0
			inside = false
		}
		kept = append(kept, f)
		if c.libraryEntries[strings.ToLower(name)] {
			inside = true
		}
	}
	return kept
}

// ReportLocation is where a diagnostic or exception raised now says it was:
// the current position, unless that is inside a bundled library body, where
// the engine names the line that called the library function.
func (c *Ctx) ReportLocation() (string, uint32) {
	if len(c.libraryEntries) == 0 {
		return c.file, c.line
	}
	for i := len(c.stack) - 1; i >= 0; i-- {
		name := c.stack[i].Function
		if c.libraryEntries[strings.ToLower(name)] {
			return c.stack[i].File, c.stack[i].Line
		}
		if libraryInternal(name) {
			continue
		}
		if _, builtin := LookupBuiltin(name); builtin && !strings.Contains(name, "->") && !strings.Contains(name, "::") {
			continue
		}
		break
	}
	return c.file, c.line
}

// MarkInternalCall marks the top frame as an engine function's, **if that frame
// is the call named**. The guard is not defensive padding: a frameless ICALL
// pushes no frame at all, so the top one there belongs to the caller, and
// marking it would corrupt an ordinary PHP frame. Comparing the name makes the
// mark self-checking at the one place that knows the call is internal.
func (c *Ctx) MarkInternalCall(name string) {
	if n := len(c.stack); n > 0 && strings.EqualFold(c.stack[n-1].Function, name) {
		c.stack[n-1].Internal = true
		c.stack[n-1].general = true
	}
}

// TraceString renders a stack as PHP prints it after "Stack trace:", innermost
// frame first and numbered from zero, ending with the {main} pseudo-frame.
func TraceString(stack []StackFrame) string {
	return fmt.Sprintf("%s#%d {main}", BacktraceString(stack), len(stack))
}

// BacktraceString is the same rendering without the {main} pseudo-frame, which
// is what debug_print_backtrace prints. Each line ends with a newline, so the
// caller decides what follows.
func BacktraceString(stack []StackFrame) string {
	return BacktraceStringLimit(stack, 0)
}

// BacktraceStringLimit is BacktraceString with debug_print_backtrace's $limit:
// the number of frames to print, counted from the innermost, with zero meaning
// all of them.
func BacktraceStringLimit(stack []StackFrame, limit int) string {
	var out strings.Builder
	position := 0
	for i := len(stack) - 1; i >= 0; i-- {
		if limit > 0 && position >= limit {
			break
		}
		frame := stack[i]
		if IsInternalFile(frame.File) {
			fmt.Fprintf(&out, "#%d %s: %s(%s)\n",
				position, InternalFrameFile, DisplayFunction(frame.Function), traceArgs(frame))
		} else {
			fmt.Fprintf(&out, "#%d %s(%d): %s(%s)\n",
				position, frame.File, frame.Line, DisplayFunction(frame.Function), traceArgs(frame))
		}
		position++
	}
	return out.String()
}

// escapeTraceString is what a trace does to a string argument: a byte that is
// not printable ASCII becomes \xNN, in upper case, and a backslash doubles. The
// few whitespace bytes with a shorter spelling get it. A quote does not, even
// though the result is wrapped in quotes -- the engine leaves it alone and so
// does this. Truncation happens before escaping, since the engine counts the
// bytes it took, not the characters it printed.
func escapeTraceString(s string) string {
	needs := false
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 32 || c > 126 || c == '\\' {
			needs = true
			break
		}
	}
	if !needs {
		return s
	}
	var out strings.Builder
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '\n':
			out.WriteString(`\n`)
		case '\r':
			out.WriteString(`\r`)
		case '\t':
			out.WriteString(`\t`)
		case '\f':
			out.WriteString(`\f`)
		case '\v':
			out.WriteString(`\v`)
		case '\\':
			out.WriteString(`\\`)
		default:
			if c < 32 || c > 126 {
				fmt.Fprintf(&out, `\x%02X`, c)
			} else {
				out.WriteByte(c)
			}
		}
	}
	return out.String()
}

// traceArgs renders call arguments the way PHP's trace does: abbreviated, never
// recursive, and with strings cut off at 15 characters.
func traceArgs(frame StackFrame) string {
	rendered := make([]string, 0, len(frame.Args))
	for i, arg := range frame.Args {
		shown := traceArg(arg)
		if frame.IsSensitive(i) {
			shown = SensitiveArgRendering
		}
		if name, named := frame.ArgName(i); named {
			rendered = append(rendered, name+": "+shown)
			continue
		}
		rendered = append(rendered, shown)
	}
	return strings.Join(rendered, ", ")
}

func traceArg(v Value) string {
	switch v.Kind() {
	case KindUndef, KindNull:
		return "NULL"
	case KindFalse:
		return "false"
	case KindTrue:
		return "true"
	case KindString:
		s := v.String()
		if len(s) > traceStringLimit {
			return "'" + escapeTraceString(s[:traceStringLimit]) + "...'"
		}
		return "'" + escapeTraceString(s) + "'"
	case KindArray:
		return "Array"
	case KindObject:
		if held := v.Object(); held.EnumCase != "" {
			// An enum case is written as the case itself. It is an object, but
			// a trace names it the way the source does.
			return DisplayName(held.ClassName) + "::" + held.EnumCase
		}
		return "Object(" + DisplayName(v.Object().ClassName) + ")"
	case KindFloat:
		// A trace prints a float the way var_export does -- with a fractional
		// part or an exponent -- not the way a string cast does.
		return traceFloat(v.Float())
	case KindRef:
		return traceArg(v.Deref())
	default:
		return v.String()
	}
}

// traceFloat renders a float so it reads back as one: 1.0 rather than 1.
func traceFloat(value float64) string {
	// Go spells these Go's way; PHP spells them PHP's way, and a trace uses
	// PHP's -- NAN rather than NaN, INF rather than +Inf.
	switch {
	case math.IsNaN(value):
		return "NAN"
	case math.IsInf(value, 1):
		return "INF"
	case math.IsInf(value, -1):
		return "-INF"
	}
	body := strconv.FormatFloat(value, 'G', -1, 64)
	if at := strings.IndexByte(body, 'E'); at >= 0 {
		if !strings.Contains(body[:at], ".") {
			return body[:at] + ".0" + body[at:]
		}
		return body
	}
	if !strings.Contains(body, ".") {
		return body + ".0"
	}
	return body
}

// traceStringLimit is how much of a string argument PHP shows before eliding
// the rest: `zend.exception_string_param_max_len`, 15 unless the request set
// it (exception_025). A package variable because a trace is rendered from a
// stack with no Ctx to hand; each request's Ctx starts it afresh.
var traceStringLimit = 15

// SetTraceStringLimit is ini_set of `zend.exception_string_param_max_len`.
func SetTraceStringLimit(n int) { traceStringLimit = n }

// Define registers a constant, reporting false if the name is already taken --
// PHP warns and keeps the original rather than replacing it.
func (c *Ctx) Define(name string, value Value) bool {
	name = constantKey(name)
	if _, taken := c.constants[name]; taken {
		return false
	}
	if c.constants == nil {
		c.constants = map[string]Value{}
	}
	c.constants[name] = value
	return true
}

// Constants answers every script-defined constant. A constant can hold an
// object, and one that does keeps it alive for the whole request.
func (c *Ctx) Constants() map[string]Value {
	return c.constants
}

// Constant looks up a script-defined constant.
//
// **`true`, `false` and `null` answer under any spelling.** The table holds
// them as TRUE, FALSE and NULL, but the engine finds them case-insensitively
// by name: `defined('true')` and `constant('nUll')` both succeed, and so
// does an INI fallback `${X:-true}`, which resolves its words as constants.
func (c *Ctx) Constant(name string) (Value, bool) {
	value, ok := c.constants[constantKey(name)]
	if !ok && (len(name) == 4 || len(name) == 5) {
		switch strings.ToLower(name) {
		case "true":
			return Bool(true), true
		case "false":
			return Bool(false), true
		case "null":
			return Null(), true
		}
	}
	return value, ok
}

// constantKey is a constant's name as the table holds it: **the namespace
// part folded**, the constant's own name as written. `define('NS1\ns2\c', 1)`
// is found as `ns1\NS2\c` and not as `NS1\ns2\C`, which is how the engine
// registers and looks one up.
func constantKey(name string) string {
	i := strings.LastIndexByte(name, '\\')
	if i < 0 {
		return name
	}
	for j := 0; j < i; j++ {
		if b := name[j]; b >= 'A' && b <= 'Z' {
			return strings.ToLower(name[:i]) + name[i:]
		}
	}
	return name
}

// silenceMask is what `@` sets error_reporting to. Suppression is not a flag
// the engine keeps beside the mask -- it *is* the mask, dropped to the levels
// nothing can hide. That is why calling error_reporting() inside a suppressed
// expression un-suppresses the rest of it.
const silenceMask = 1 | 4 | 16 | 64 | 256 | 4096 // E_ERROR|E_PARSE|E_CORE_ERROR|
// E_COMPILE_ERROR|E_USER_ERROR|E_RECOVERABLE_ERROR

// BeginSilence starts an `@expr`, answering with the mask to hand back to
// EndSilence.
func (c *Ctx) BeginSilence() int64 {
	previous := c.errorReporting
	if c.silenceDepth == 0 {
		c.silenceBase = previous
	}
	c.silenceDepth++
	// The level `@` installs is the *current* one narrowed to what no setting
	// can hide, not that mask outright: `error_reporting(E_WARNING)` followed by
	// `@f()` answers 0 inside f, because E_WARNING is not one of the fatal
	// levels. Probed.
	c.errorReporting = previous & silenceMask
	return previous
}

// UnsuppressedReporting is the level with any `@` currently in force
// disregarded. A fiber started inside a suppressed expression does not inherit
// the suppression -- `@$fiber->start()` still reports what the fiber's body
// raises -- so the value it starts from is this one rather than the mask.
func (c *Ctx) UnsuppressedReporting() int64 {
	if c.silenceDepth > 0 && onlyFatal(c.errorReporting) {
		return c.silenceBase
	}
	return c.errorReporting
}

// defaultErrorReporting is what `error_reporting()` answers before a script
// sets it. **Measured, not assumed**: the linked engine returns
//
//	var_dump(error_reporting());   // int(30719)
//	var_dump(E_ALL);               // int(30719)
//
// so it is this build's `E_ALL`, which excludes `E_STRICT` (2048) -- PHP 8.4
// dropped it from `E_ALL` and no longer raises it, so nothing real is filtered
// by the difference. The previous value was -1: every bit set, which reports
// the same diagnostics but *answers* a number no PHP ever returns, and five
// corpus files compared that number.
const defaultErrorReporting = 30719

// EndSilence closes an `@expr`. The saved mask is restored only if the script
// did not raise the level meanwhile: `@f()` where f calls error_reporting()
// leaves f's choice standing, and every diagnostic after that call is reported.
//
// "Did not raise it" is measured the way the engine measures it -- whether
// anything but a fatal level is still enabled -- rather than by comparing
// against the mask `@` installed. That is what makes `error_reporting(0)`
// inside a suppressed call restore afterwards while `error_reporting(E_ALL)`
// does not: zero hides no less than the silence did.
func (c *Ctx) EndSilence(previous int64) {
	if c.silenceDepth > 0 {
		c.silenceDepth--
	}
	if onlyFatal(c.errorReporting) && !onlyFatal(previous) {
		c.errorReporting = previous
	}
}

// onlyFatal reports whether a mask has nothing but the levels no setting can
// hide, which is the engine's E_HAS_ONLY_FATAL_ERRORS.
func onlyFatal(mask int64) bool { return mask&^silenceMask == 0 }

// Diagnostic is a warning or notice raised while running.
type Diagnostic struct {
	Level   string
	Message string
	// File and Line are only filled in for the record error_get_last() reads;
	// the printed list carries the location in the text.
	File string
	Line uint32
}

// LastError is what error_get_last() reports, or nil when nothing has been
// raised. ClearLastError is error_clear_last().
func (c *Ctx) LastError() *Diagnostic { return c.lastError }

func (c *Ctx) ClearLastError() { c.lastError = nil }

// LevelBit is the E_* number a diagnostic's printed name corresponds to, which
// error_get_last() reports as its "type".
func LevelBit(level string) int64 { return levelBit(level) }

func NewCtx(out io.Writer) *Ctx {
	traceStringLimit = 15
	c := newCtx(out)
	traceWarn = func(message string) { c.Warn("%s", message) }
	return c
}

func newCtx(out io.Writer) *Ctx {
	// Every bit set until error_reporting() says otherwise: the engine's E_ALL
	// is build-specific and harvested, so it is not spelled out here.
	return &Ctx{
		out: out, Globals: NewArray(), superglobals: map[string]struct{}{},
		constants: map[string]Value{}, errorReporting: defaultErrorReporting,
	}
}

// RegisterSuperglobal marks name as an automatically global variable and
// initializes its request-local slot only when the program has not already
// supplied one. It returns the shared slot so active interpreter frames can be
// rebound immediately.
func (c *Ctx) RegisterSuperglobal(name string, initial Value) *Ref {
	if c.superglobals == nil {
		c.superglobals = map[string]struct{}{}
	}
	key := StringKey(name)
	if _, exists := c.Globals.Get(key); !exists {
		c.Globals.Set(key, initial)
	}
	c.superglobals[name] = struct{}{}
	return c.Globals.RefAt(key)
}

// EachSuperglobal visits the names registered for this request.
func (c *Ctx) EachSuperglobal(visit func(string) bool) {
	for name := range c.superglobals {
		if !visit(name) {
			return
		}
	}
}

// Error levels, as the E_* constants number them. Only the ones a diagnostic
// can be raised at are named; the rest come from the harvested constant table.
const (
	ErrError          = 1 << 0
	ErrWarning        = 1 << 1
	ErrParse          = 1 << 2
	ErrNotice         = 1 << 3
	ErrCompileError   = 1 << 6
	ErrUserWarning    = 1 << 9
	ErrUserNotice     = 1 << 10
	ErrDeprecated     = 1 << 13
	ErrUserDeprecated = 1 << 14
)

// ResultUsed reports whether the call being made will keep what it returns.
// Almost nothing needs to know -- but array_splice rebuilds the array through a
// different walk depending on it, and the difference is visible to a foreach
// running over that array, so the flag has to reach the builtin.
func (c *Ctx) ResultUsed() bool { return !c.resultUnused }

// SetResultUsed is called just before dispatching a builtin. The caller that
// knows nothing about its opline leaves it true, which is the common case.
func (c *Ctx) SetResultUsed(used bool) { c.resultUnused = !used }

// silenceDepth counts the `@` expressions currently open, and silenceBase is
// the level the outermost of them replaced. Together they answer what the level
// would be without the suppression, which is what a fiber inherits.

// ErrorReporting reports which levels are being reported.
func (c *Ctx) ErrorReporting() int64 { return c.errorReporting }

// SilenceState is the error-reporting level with the `@` nesting behind it --
// what a fiber carries as its own, since `@` belongs to the code it is written
// in: a fiber suspended inside `@` hands its caller back the caller's level.
type SilenceState struct {
	level, base int64
	depth       int
}

// SwapSilence installs s and answers what it replaced.
func (c *Ctx) SwapSilence(s SilenceState) SilenceState {
	old := SilenceState{level: c.errorReporting, base: c.silenceBase, depth: c.silenceDepth}
	c.errorReporting, c.silenceBase, c.silenceDepth = s.level, s.base, s.depth
	return old
}

// FreshSilence is the state a new fiber starts in: the caller's level with
// none of its `@` -- `@$fiber->start()` does not silence the fiber's body.
func (c *Ctx) FreshSilence() SilenceState {
	level := c.errorReporting
	if c.silenceDepth > 0 {
		level = c.silenceBase
	}
	return SilenceState{level: level, base: level}
}

// SetErrorReporting changes the mask, returning the previous one -- which is
// what error_reporting() itself returns.
func (c *Ctx) SetErrorReporting(mask int64) int64 {
	previous := c.errorReporting
	c.errorReporting = mask
	return previous
}

// Echo writes a value using PHP's string conversion.
func (c *Ctx) Echo(v Value) {
	// Converting an array to a string warns in PHP, and the value still prints
	// as "Array"; the warning is raised here because only Ctx can record it.
	if v.Kind() == KindArray {
		c.Warn("Array to string conversion")
	}
	c.Write(v.String())
}

// Write emits raw bytes, as inline HTML outside <?php does.
func (c *Ctx) Write(s string) {
	// Output buffering nests: ob_start inside a buffer captures what the outer
	// one would have received, so only the innermost buffer sees the write.
	if n := len(c.buffers); n > 0 {
		held := c.buffers[n-1]
		if held.flags&outputHandlerDisabled != 0 {
			// A callback that returned false stays on the stack but is disabled;
			// subsequent writes pass through it to the next level.
			c.buffers = c.buffers[:n-1]
			c.Write(s)
			c.buffers = append(c.buffers, held)
			return
		}
		held.append(s)
		c.chunkOverflow()
		return
	}
	io.WriteString(c.out, s)
}

// chunkOverflow hands the innermost buffer to its filter as soon as it has
// collected a chunk's worth. `ob_start($cb, 10)` is not merely a size hint: the
// callback runs once per chunk, and a program can count the calls.
func (c *Ctx) chunkOverflow() {
	n := len(c.buffers)
	if n == 0 || c.flushingChunk || !c.canRunBufferFilter() {
		return
	}
	held := c.buffers[n-1]
	if held.chunk <= 0 || int64(held.text.Len()) < held.chunk {
		return
	}
	text := held.text.String()
	held.text.Reset()
	phase := int64(0)
	if held.flags&outputHandlerStarted == 0 {
		phase = outputHandlerStart
	}
	// The filter's own output belongs to the level *below* this buffer, and a
	// write from inside it must not re-enter this check.
	c.flushingChunk = true
	produced, accepted, err := c.RunBufferFilter(held.filter, text, phase)
	c.flushingChunk = false
	held.flags |= outputHandlerStarted
	if err != nil || !accepted {
		held.flags |= outputHandlerDisabled
		held.size = 0
	} else {
		held.flags |= outputHandlerProcessed
	}
	if err != nil {
		// The filter failed. What it was given is written out unchanged, the
		// same as returning false, and the throw is parked for the interpreter
		// to pick up at the next opcode.
		produced = text
		if c.pendingError == nil {
			c.pendingError = err
		}
	}
	c.buffers = c.buffers[:n-1]
	c.Write(produced)
	c.buffers = append(c.buffers, held)
}

// SetBufferFlusher installs what runs an output buffer's filter. It overrides
// RunBufferFilter's own handling, and exists for a caller that needs the filter
// run at a particular place in its own machinery rather than through `Call`.
func (c *Ctx) SetBufferFlusher(run func(filter any, text string, phase int64) (string, bool, error)) {
	c.bufferFlusher = run
}

// InternalOutputFilter is an output handler written in Go rather than as a PHP
// callable -- `output_add_rewrite_var`'s rewriter is the one that exists. It is
// an interface because the handler belongs to the layer that installed it; this
// package only has to be able to run it.
type InternalOutputFilter interface {
	FilterOutput(text string, phase int64) (string, bool, error)
}

// RunBufferFilter hands a buffer's contents to its filter and answers what
// should be written in their place. The second result is false for a filter
// that declined -- PHP's `return false` means "use what I was given" -- and a
// declining filter is disabled for the rest of the buffer's life.
//
// A filter is either a Go handler or a PHP callable, and the callable case is
// `Call`: this used to need a flusher installed from above because there was no
// way to invoke one from here, and the hook it needed now exists. A buffer with
// no runnable filter passes its text through unchanged, which is what a buffer
// without a filter does anyway.
func (c *Ctx) RunBufferFilter(filter any, text string, phase int64) (string, bool, error) {
	if c.bufferFlusher != nil {
		return c.bufferFlusher(filter, text, phase)
	}
	if internal, ok := filter.(InternalOutputFilter); ok {
		return internal.FilterOutput(text, phase)
	}
	callee, ok := filter.(Value)
	if !ok || c.call == nil {
		return text, true, nil
	}
	produced, err := c.Call(callee, []Value{String(text), Int(phase)})
	if err != nil {
		return "", false, err
	}
	if produced.Deref().Kind() == KindFalse {
		return text, false, nil
	}
	return produced.Deref().String(), true, nil
}

// canRunBufferFilter reports whether a filter could be run at all, which is
// what decides whether the chunk-size machinery has anything to do.
func (c *Ctx) canRunBufferFilter() bool { return c.bufferFlusher != nil || c.call != nil }

// WriteDirect emits bytes past the output buffers, which is what writing to
// the STDOUT stream does: `fwrite(STDOUT, ...)` is not `echo` and an ob_start
// does not capture it.
func (c *Ctx) WriteDirect(s string) { io.WriteString(c.out, s) }

// outputBuffer is one ob_start level: what it has collected, and the filter
// ob_start was given. The filter is opaque here -- it is a PHP callable, which
// only the interpreter can invoke -- so the context carries it and hands it
// back rather than doing anything with it.
const (
	OutputHandlerAbilityMask int64 = -61456 // all bits except type and runtime state
	OutputHandlerCleanable   int64 = 0x0010
	OutputHandlerFlushable   int64 = 0x0020
	OutputHandlerRemovable   int64 = 0x0040
	OutputHandlerStdFlags    int64 = 0x0070

	outputHandlerUser      int64 = 0x0001
	outputHandlerStart     int64 = 0x0001
	outputHandlerStarted   int64 = 0x1000
	outputHandlerDisabled  int64 = 0x2000
	outputHandlerProcessed int64 = 0x4000
)

type outputBuffer struct {
	text strings.Builder
	// filter is the callable ob_start was given, or nil.
	filter any
	name   string
	flags  int64
	// chunk is ob_start's second argument: the size at which the filter is run
	// rather than waiting for the buffer to end. Zero means never.
	chunk int64
	// size is the engine-visible logical allocation. The Go builder owns its
	// own storage; this value reproduces output.c's aligned growth contract.
	size int64
}

// PushBuffer starts an output buffer, which is ob_start.
func (c *Ctx) PushBuffer() {
	c.PushBufferConfigured(nil, "default output handler", 0, OutputHandlerStdFlags)
}

// PushBufferWith starts one with a filter callback attached.
func (c *Ctx) PushBufferWith(filter any) { c.PushBufferChunked(filter, 0) }

// PushBufferChunked is PushBufferWith with ob_start's chunk size.
func (c *Ctx) PushBufferChunked(filter any, chunk int) {
	name := "default output handler"
	if filter != nil {
		name = "Closure::__invoke"
	}
	c.PushBufferConfigured(filter, name, int64(chunk), OutputHandlerStdFlags)
}

// PushBufferConfigured records the public ob_start configuration. Handler
// names are resolved by the interpreter, which owns PHP callable semantics.
func (c *Ctx) PushBufferConfigured(filter any, name string, chunk, flags int64) {
	if chunk < 0 {
		chunk = 0
	}
	flags &= OutputHandlerAbilityMask
	if filter != nil {
		flags |= outputHandlerUser
	}
	c.buffers = append(c.buffers, &outputBuffer{
		filter: filter, name: name, chunk: chunk, flags: flags,
		size: initialOutputBufferSize(chunk),
	})
}

// PushInternalBufferConfigured starts an engine-owned output handler. It has a
// filter but does not carry the user-handler type bit exposed by ob_get_status.
func (c *Ctx) PushInternalBufferConfigured(filter any, name string, chunk, flags int64) {
	if chunk < 0 {
		chunk = 0
	}
	flags &= OutputHandlerAbilityMask
	c.buffers = append(c.buffers, &outputBuffer{
		filter: filter, name: name, chunk: chunk, flags: flags,
		size: initialOutputBufferSize(chunk),
	})
}

func initialOutputBufferSize(size int64) int64 {
	if size > 1 {
		return size + 4096 - size%4096
	}
	return 16384
}

func (b *outputBuffer) append(text string) {
	incoming := int64(len(text))
	if incoming > 0 && b.size-int64(b.text.Len()) <= incoming {
		growConfigured := initialOutputBufferSize(b.chunk)
		growIncoming := initialOutputBufferSize(
			incoming - (b.size - int64(b.text.Len())),
		)
		if growIncoming > growConfigured {
			growConfigured = growIncoming
		}
		b.size += growConfigured
	}
	b.text.WriteString(text)
}

// BufferFilter is the filter of the innermost buffer, if it has one.
func (c *Ctx) BufferFilter() (any, bool) {
	if n := len(c.buffers); n > 0 && c.buffers[n-1].filter != nil {
		return c.buffers[n-1].filter, true
	}
	return nil, false
}

// BufferLevel is how many buffers are open, which is ob_get_level.
func (c *Ctx) BufferLevel() int { return len(c.buffers) }

// BufferStatus is the irreducible state exposed by ob_get_status and
// ob_list_handlers. Public array shaping stays in the harvested PHP library.
type BufferStatus struct {
	Name       string
	Type       int64
	Flags      int64
	Level      int64
	ChunkSize  int64
	BufferSize int64
	BufferUsed int64
}

// BufferStatuses returns the output stack from outermost to innermost.
func (c *Ctx) BufferStatuses() []BufferStatus {
	statuses := make([]BufferStatus, 0, len(c.buffers))
	for level, held := range c.buffers {
		statuses = append(statuses, BufferStatus{
			Name:       held.name,
			Type:       held.flags & 0xf,
			Flags:      held.flags,
			Level:      int64(level),
			ChunkSize:  held.chunk,
			BufferSize: held.size,
			BufferUsed: int64(held.text.Len()),
		})
	}
	return statuses
}

// BufferContents peeks at the innermost buffer without ending it.
func (c *Ctx) BufferContents() (string, bool) {
	if n := len(c.buffers); n > 0 {
		return c.buffers[n-1].text.String(), true
	}
	return "", false
}

// PopBuffer ends the innermost buffer and hands back what it held. `flush`
// passes that on to whatever is outside it rather than discarding it.
func (c *Ctx) PopBuffer(flush bool) (string, bool) {
	n := len(c.buffers)
	if n == 0 {
		return "", false
	}
	held := c.buffers[n-1].text.String()
	c.buffers = c.buffers[:n-1]
	if flush {
		c.Write(held)
	}
	return held, true
}

// ClearBuffer empties the innermost buffer without popping it, optionally
// writing what it held to the one below first. That is the difference between
// ob_clean/ob_flush and ob_end_clean/ob_end_flush: the level stays.
func (c *Ctx) ClearBuffer(flush bool) bool {
	n := len(c.buffers)
	if n == 0 {
		return false
	}
	held := c.buffers[n-1].text.String()
	c.buffers[n-1].text.Reset()
	if c.buffers[n-1].flags&outputHandlerDisabled == 0 {
		c.buffers[n-1].flags |= outputHandlerStarted | outputHandlerProcessed
	}
	if flush {
		// Write goes to the innermost buffer, which is the one just emptied, so
		// the contents have to be handed to the level below directly.
		saved := c.buffers
		c.buffers = saved[:n-1]
		c.Write(held)
		c.buffers = saved
	}
	return true
}

// ProcessBuffer runs ob_clean or ob_flush against the innermost buffer without
// popping it. The operation is the phase bit sent to a user handler.
func (c *Ctx) ProcessBuffer(operation int64, flush bool) (bool, error) {
	n := len(c.buffers)
	if n == 0 {
		return false, nil
	}
	held := c.buffers[n-1]
	required := OutputHandlerCleanable
	if flush {
		required = OutputHandlerFlushable
	}
	if held.flags&required == 0 {
		return false, nil
	}
	text := held.text.String()
	held.text.Reset()
	if held.flags&outputHandlerDisabled != 0 {
		return true, nil
	}
	phase := operation
	if held.flags&outputHandlerStarted == 0 {
		phase |= outputHandlerStart
	}
	produced, accepted := text, true
	var err error
	if c.canRunBufferFilter() {
		c.flushingChunk = true
		produced, accepted, err = c.RunBufferFilter(held.filter, text, phase)
		c.flushingChunk = false
	}
	held.flags |= outputHandlerStarted
	if err != nil || !accepted {
		held.flags |= outputHandlerDisabled
		held.size = 0
		produced = text
	} else {
		held.flags |= outputHandlerProcessed
	}
	if flush && produced != "" {
		c.buffers = c.buffers[:n-1]
		c.Write(produced)
		c.buffers = append(c.buffers, held)
	}
	return true, err
}

// SetLocation records where execution is, for diagnostics to report.
func (c *Ctx) SetLocation(file string, line uint32) {
	c.file, c.line = file, line
}

// Location reports where execution is. An exception records the site where it
// was created, which is the same thing a diagnostic reports.
func (c *Ctx) Location() (string, uint32) { return c.file, c.line }

func (c *Ctx) Warn(format string, args ...any)       { c.raise("Warning", format, args...) }
func (c *Ctx) Deprecated(format string, args ...any) { c.raise("Deprecated", format, args...) }

// UserDeprecated is the same diagnostic raised at E_USER_DEPRECATED. It prints
// identically -- PHP labels both "Deprecated" -- but an error handler is told a
// different code, and #[\Deprecated] on user code raises this one.
func (c *Ctx) UserDeprecated(format string, args ...any) {
	c.raise("UserDeprecated", format, args...)
}

// UserWarning and UserNotice are what trigger_error raises. They print under
// the engine levels' labels but are filtered on their own E_USER_* bits, which
// is what makes `error_reporting(E_ALL & ~E_USER_NOTICE)` hide a
// trigger_error() notice while leaving the engine's own notices alone.
func (c *Ctx) UserWarning(format string, args ...any) {
	c.raise("UserWarning", format, args...)
}

func (c *Ctx) UserNotice(format string, args ...any) {
	c.raise("UserNotice", format, args...)
}

// ErrorHandler is what set_error_handler installed, wired in by whoever can
// call PHP code. It reports whether it handled the diagnostic -- in which case
// nothing is printed -- and returns an exception if it threw one.
//
// The level is the printed name ("Warning"); mapping it to an E_* number is the
// caller's job, since the numbers belong to the linked build.
type ErrorHandler func(level, message, file string, line uint32) (handled bool, err error)

// SetErrorHandler installs the callback consulted for every diagnostic.
func (c *Ctx) SetErrorHandler(handler ErrorHandler) { c.errorHandler = handler }

// EnterErrorHandler takes the running user error handler off the stack while
// it runs, and answers the function that puts it back. The engine swaps its
// handler slot to undefined for the call: a diagnostic inside the handler goes
// to the default path unless the handler installed one of its own, which then
// catches it; afterwards the running one returns only if the slot is still
// empty, and a replacement the handler left in place stays (probed). The top of
// the stack is the current handler, so "off" is a null there -- the same entry
// `set_error_handler(null)` makes.
func (c *Ctx) EnterErrorHandler() (restore func()) {
	n := len(c.errorHandlers)
	if n == 0 {
		return func() {}
	}
	running := c.errorHandlers[n-1]
	c.errorHandlers[n-1] = Null()
	return func() {
		if len(c.errorHandlers) == n && c.errorHandlers[n-1].Kind() == KindNull {
			c.errorHandlers[n-1] = running
		}
	}
}

// ParkError records an exception raised somewhere that cannot unwind, to be
// picked up at the next opcode boundary. The first one wins: a later throw
// during the same unwinding does not replace what is already waiting.
//
// A **deferred property initializer** is one such place: it renders as
// `func(*Ctx) Value` and a class constructor answers with an object, so neither
// can return an error -- yet `public int $p = NOPE::X;` is
// `Class "NOPE" not found`, not a silent null.
func (c *Ctx) ParkError(err error) {
	if err != nil && c.pendingError == nil {
		c.pendingError = err
	}
}

// PendingError reports the parked exception without clearing it, for a caller
// deciding whether to report a failure of its own.
func (c *Ctx) PendingError() error { return c.pendingError }

// TakePendingError returns and clears an exception a user error handler threw.
// A diagnostic is raised from places that cannot unwind -- deep inside the
// value model -- so the throw is parked here and picked up at the next opcode
// boundary, which is where the interpreter can act on it.
func (c *Ctx) TakePendingError() error {
	pending := c.pendingError
	c.pendingError = nil
	return pending
}

// raise records a diagnostic and prints it, which is what PHP does: the text
// goes to the script's own output, so it is part of what a test compares.
func (c *Ctx) raise(level, format string, args ...any) {
	c.raiseAt(level, c.file, c.line, fmt.Sprintf(format, args...))
}

// DeprecatedAt is a deprecation reported at a line other than the one
// executing: linking a class reports an override at the method's own line,
// while the handler is called from the declaration (probed).
func (c *Ctx) DeprecatedAt(file string, line uint32, format string, args ...any) {
	c.raiseAt("Deprecated", file, line, fmt.Sprintf(format, args...))
}

func (c *Ctx) raiseAt(level, file string, line uint32, message string) {
	// The location is snapshotted: running a user handler moves it, since the
	// handler's own body reports where it is, and the execution must resume
	// where it was.
	executing, executingLine := c.file, c.line
	// **No script running is `Unknown`** for a diagnostic, where an
	// exception made there says `[no active file]`: an array message
	// converted while an uncaught exception is rendered warns `Array to
	// string conversion in Unknown on line 0`, and so does a library
	// function registered for shutdown (probed).
	at := file
	if file == NoActiveFile {
		at = "Unknown"
	}
	// A user handler sees every diagnostic, including one @ suppressed and one
	// error_reporting would hide: the mask gates the *default* handler, not the
	// callback. A diagnostic raised inside the handler is not fed back to it.
	//
	// No guard here beyond the flag for a hook with no stack: a hook that
	// consults the stack takes the running handler off it for the duration
	// (EnterErrorHandler), which is what stops a diagnostic inside the
	// handler from reaching it again.
	if c.errorHandler != nil && !c.inErrorHandler {
		guarded := len(c.errorHandlers) == 0
		if guarded {
			c.inErrorHandler = true
		}
		handled, err := c.errorHandler(level, message, at, line)
		if guarded {
			c.inErrorHandler = false
		}
		c.file, c.line = executing, executingLine
		if err != nil {
			c.pendingError = err
		}
		if handled {
			return
		}
	}
	// The last error is recorded whatever error_reporting says and whatever `@`
	// suppressed: error_get_last() answers about what happened, not about what
	// was printed.
	c.lastError = &Diagnostic{Level: level, Message: message, File: at, Line: line}
	if c.errorReporting&levelBit(level) == 0 {
		return
	}
	label := level
	switch label {
	// The E_USER_* levels print under the same label as their engine
	// counterparts; only the code an error handler sees differs.
	case "UserDeprecated":
		label = "Deprecated"
	case "UserWarning":
		label = "Warning"
	case "UserNotice":
		label = "Notice"
	}
	c.diagnostics = append(c.diagnostics, Diagnostic{Level: label, Message: message})
	// A diagnostic is output like any other: an open buffer captures it, which
	// is what lets a test collect warnings with ob_get_clean.
	c.Write(fmt.Sprintf("\n%s: %s in %s on line %d\n", label, message, at, line))
}

func (c *Ctx) Notice(format string, args ...any) { c.raise("Notice", format, args...) }

// levelBit maps a diagnostic's printed name to the E_* bit error_reporting
// filters on.
func levelBit(level string) int64 {
	switch level {
	case "Fatal error":
		return ErrError
	case "Parse error":
		return ErrParse
	case "Compile error":
		// Printed as `Fatal error`; only the recorded code differs.
		return ErrCompileError
	case "Warning":
		return ErrWarning
	case "Notice":
		return ErrNotice
	case "Deprecated":
		return ErrDeprecated
	case "UserDeprecated":
		return ErrUserDeprecated
	case "UserWarning":
		return ErrUserWarning
	case "UserNotice":
		return ErrUserNotice
	}
	// An unrecognized level is reported rather than swallowed: silence would
	// hide it, and a diagnostic that never appears is worse than a noisy one.
	return -1
}

// Diagnostics returns what was raised, oldest first.
func (c *Ctx) Diagnostics() []Diagnostic {
	return c.diagnostics
}

// NewResource hands out the next resource id for this request.
func (c *Ctx) NewResource(typeName string, payload any) *Resource {
	return c.resources.NewResource(typeName, payload)
}

// SetFirstResourceID tells this request that the host already holds resources,
// so the first the script opens should take a later number.
//
// For a caller reproducing a *different* host than the one running: `php2phpt`
// grades against output recorded under the CLI, which opens four before a
// script starts. Nothing else needs it -- see firstScriptResource.
func (c *Ctx) SetFirstResourceID(id int) { c.resources.First = id }

// StandardStream returns the handle for STDIN, STDOUT or STDERR once it has
// been asked for, which is when the CLI's three are materialised here.
func (c *Ctx) StandardStream(name string) (*Resource, bool) {
	held, ok := c.resources.Standard[name]
	return held, ok
}

// SetStandardStream records one of the three so a second mention is the same
// resource rather than a new one.
func (c *Ctx) SetStandardStream(name string, handle *Resource) {
	if c.resources.Standard == nil {
		c.resources.Standard = map[string]*Resource{}
	}
	c.resources.Standard[name] = handle
}

// callSite is where a callback the engine invokes should say it was called
// from, when that is a place in the script rather than an internal function.
// An error handler is the case the engine has.
func (c *Ctx) SetCallSite(site *StackFrame) func() {
	previous := c.callSite
	c.callSite = site
	return func() { c.callSite = previous }
}

// CallSite reports the frame location a Go-side call should adopt, or nil when
// there is none and `[internal function]` is right.
func (c *Ctx) CallSite() *StackFrame { return c.callSite }

// Named arguments a **variadic** parameter collects.
//
// `v(1, x: "X")` against `function v($a, ...$rest)` leaves `$rest` holding
// `["x" => "X"]`. The argument slice is positional and cannot carry the key, so
// the names travel beside it: the call site sets them and the callee's variadic
// collection takes them.
//
// They are always the **trailing** arguments, because a positional argument
// after a named one does not compile -- so a count is enough to line them up
// with the tail of the slice.
//
// This is the pending-call channel the engine has and this package does not:
// set immediately before the call, taken once inside it. Taking clears them, so
// a callee that collects nothing cannot leave them for the next call.
func (c *Ctx) SetArgNames(names []string) { c.argNames = names }

// ArgNames is what SetArgNames last recorded, for a caller that has to **add**
// to the list rather than replace it.
//
// Unpacking needs that: `f(...["b" => 20])` turns a string key into a named
// argument, and the call may already carry names the emitter wrote out for an
// explicit `f(c: 30)` beside it. Without a way to read the list back, the two
// sources silently overwrote each other.
func (c *Ctx) ArgNames() []string { return c.argNames }

// SetReleaseHandle installs the hook that hands an object's number back.
//
// Declared here and supplied from above, like every other question this package
// cannot answer for itself: object handles are the **generated program's**,
// allocated from its own free list, and `runtime/php` has no counter to return
// one to. What it does know is *when* -- a lazy object dropping the initializer
// it solely owns -- so the moment lives here and the mechanism does not.
func (c *Ctx) SetReleaseHandle(fn func(*Object)) { c.releaseHandle = fn }

// ReleasesHandles reports whether a back-end hands object numbers back at
// all. Making an object only to drop it again is right where the number goes
// back, and a leaked number everywhere else.
func (c *Ctx) ReleasesHandles() bool { return c != nil && c.releaseHandle != nil }

// ReleaseHandle applies it, doing nothing when no back-end installed one.
func (c *Ctx) ReleaseHandle(o *Object) {
	if c == nil || c.releaseHandle == nil || o == nil {
		return
	}
	c.releaseHandle(o)
}

// MarkSensitiveArgs records which of the running call's arguments carry
// `#[\SensitiveParameter]`, so a backtrace shows the placeholder instead of the
// value.
//
// Set by the **callee**, because it is a property of the declaration and the
// caller that pushed the frame does not know it. The frame is already on the
// stack by then, which is why this reaches back rather than travelling with the
// push.
func (c *Ctx) MarkSensitiveArgs(flags []bool) {
	if len(c.stack) == 0 {
		return
	}
	c.stack[len(c.stack)-1].Sensitive = flags
	c.stack[len(c.stack)-1].general = true
}

// TakeArgNames answers the names the call being entered attached, and clears
// them.
func (c *Ctx) TakeArgNames() []string {
	names := c.argNames
	c.argNames = nil
	return names
}

// BindNamedArgs places the named arguments a **call site could not**.
//
// A call whose callee is unknown at compile time -- `$f(a3: "x")`, a first-class
// callable, a `__call` trampoline -- sends its named arguments trailing, under
// their names, because only the callee knows which position each claims. This
// runs in the callee's prologue, before anything reads `args`.
//
// A name matching a declared parameter moves to that position; the gap it
// leaves behind is `undef`, which is the same "not passed" a short argument
// list gives, so the default still applies. A name matching nothing stays
// trailing for a variadic to collect under its key -- and is an Error when
// there is no variadic to take it.
//
// Nothing happens when the caller resolved the names itself: it announces only
// what no parameter claimed, and `names` then matches no declared parameter.
func BindNamedArgs(c *Ctx, args []Value, params []string, variadic bool) ([]Value, error) {
	names := c.argNames
	if len(names) == 0 || len(names) > len(args) {
		return args, nil
	}
	first := len(args) - len(names)
	var kept []Value
	var keptNames []string
	out := append([]Value(nil), args[:first]...)
	// **A name never claims the variadic parameter itself.** Against
	// `function t($a, string ...$extra)`, `t(extra: "x")` puts "x" into the
	// collected array under `"extra"`; the parameter has no single position to
	// bind, and matching it here sent the value to the wrong slot. The variadic
	// is always the last one.
	placeable := params
	if variadic && len(placeable) > 0 {
		placeable = placeable[:len(placeable)-1]
	}
	var claimed map[int]bool
	for i, name := range names {
		at := -1
		for p, declared := range placeable {
			if declared == name {
				at = p
				break
			}
		}
		if at < 0 {
			kept = append(kept, args[first+i])
			keptNames = append(keptNames, name)
			continue
		}
		// **A position already filled is not taken again**: `f(...[1, 2],
		// b: 20)` has `$b` from the unpack, and the name is refused rather
		// than overwriting it.
		// So is one an earlier name claimed -- the unpack's string key `b`
		// followed by an explicit `b:`.
		if at < first || claimed[at] {
			c.argNames = nil
			return args, ThrowDuplicateNamedParameter(name)
		}
		if claimed == nil {
			claimed = map[int]bool{}
		}
		claimed[at] = true
		for len(out) <= at {
			out = append(out, Undef())
		}
		out[at] = args[first+i]
	}
	if !variadic && len(keptNames) > 0 {
		// **Nobody else can report this.** The call site did not know the
		// callee, so there was no signature to check the name against; here
		// there is, and a name no parameter claims with no variadic to collect
		// it is an Error the program can catch.
		c.argNames = nil
		return args, ThrowUnknownNamedParameter(keptNames[0])
	}
	c.argNames = keptNames
	return append(out, kept...), nil
}

// RandomCall is php.RandomCall over this request's default engine.
func (c *Ctx) RandomCall(name string, args []Value) (Value, bool, error) {
	return RandomCall(c, &c.random, name, args)
}
