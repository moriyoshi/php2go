package interp

import (
	"fmt"
	"strconv"
	"strings"

	pb "github.com/moriyoshi/php2go/internal/ir/irpb"
	"github.com/moriyoshi/php2go/runtime/php"
)

// maxDepth bounds recursion. PHP has no default limit and relies on the stack
// running out; this is set high enough that a program PHP would run does not
// hit it -- the corpus has a test recursing 3000 deep -- while still stopping
// runaway recursion before Go's growable stack takes the process down with it.
const maxDepth = 8192

// maxSteps bounds one frame's execution. PHP has no such limit, so this is not
// modelling the engine: it stops a bug in the interpreter from hanging a whole
// corpus run instead of failing one test. A legitimate program can exceed it,
// which is why the message says what it actually means -- and why the bound is
// generous. `object_gc_in_shutdown` allocates a million objects in one loop,
// which is roughly 8M steps; at 5M it stopped part-way through.
const maxSteps = 40_000_000

// pendingCall accumulates arguments between INIT_FCALL and DO_FCALL. The engine
// builds a real stack frame there; here it is enough to collect the values.
type pendingCall struct {
	name string
	args []php.Value
	// releasedTemps records that an argument came out of a temporary that this
	// call now owns. When it returns, whatever those held may have no reader
	// left, which is where PHP would have destroyed it.
	releasedTemps bool
	// dynamic records that the callee was named at run time rather than written
	// at the call site. The functions that read their caller's frame refuse
	// such a call, and refuse it from inside themselves -- after their own
	// arguments have been checked, which is the order the engine reports.
	dynamic bool
	// this is set for a method call, including a constructor, and binds $this
	// inside the callee.
	this *php.Object
	// fiberStatic names Fiber::suspend or Fiber::getCurrent.
	fiberStatic string
	// weakStatic names a WeakReference static, whose arguments arrive through
	// the ordinary SEND_* oplines like any other call's.
	weakStatic string
	// ctorObject is the object a constructor call is building. It is not
	// destructible until that call returns: PHP does not destruct what it did
	// not construct.
	ctorObject *php.Object
	// method is resolved up front for a method call, since the name alone does
	// not say which class in the hierarchy provides it.
	method *pb.OpArray
	// class is the class the call was made against, which is what self:: and
	// parent:: mean inside the callee.
	class *Class
	// called is the class named at the call site rather than the one that
	// declared the method, which is what static:: resolves to.
	called *Class
	// builtinMethod names an engine-provided method, which has no op_array and
	// is implemented in Go instead.
	builtinMethod string
	// builtinCtor initializes an instance of an engine-provided class once the
	// arguments have been collected.
	builtinCtor func(*php.Object, []php.Value) error
	// closure is set when the callee is a Closure instance, which carries its
	// own captured scope rather than being looked up by name.
	closure *closure
	// enum and enumMethod name one of the methods every enum has without
	// declaring it: cases, from and tryFrom.
	enum       *Class
	enumMethod string
	// closureStatic names Closure::fromCallable or Closure::bind.
	closureStatic string
	// named holds the arguments given by parameter name rather than position.
	// They are resolved against the callee's signature once it is known, which
	// for a call by name is not until DO_FCALL.
	named []namedArgument
	// hook is set when the callee is a property hook body, so the frame can
	// know which property it belongs to.
	hook hookIdentity
	// hookFallback is set for `parent::$p::get()` where the parent declares no
	// such hook. The engine builds a trampoline that reads or writes the
	// backing store, and this stands for it.
	hookFallback *pb.Property
	hookIsSet    bool
	// viaUserCall marks a call made through call_user_func and its siblings,
	// which hand their arguments over by value whatever the callee asked for.
	viaUserCall bool
	// forwardTo is set when the callee is __call or __callStatic, and names the
	// method the program actually asked for. Those accessors take that name and
	// the arguments as an array rather than the arguments themselves.
	//
	// forwarding says that is what is happening, which the name cannot: PHP
	// allows an empty method name -- `$class::$empty()` reaches __callStatic
	// with "" -- so a non-empty name is not the test.
	forwardTo  string
	forwarding bool
	// closureObject is the Closure the callee *is*, when the call was made on
	// one. `$c(...)` and `$c->__invoke(...)` hand back that very object rather
	// than a new Closure over the same body, which `===` can see.
	closureObject *php.Object
	// refusal is the Error a resolved call earns before it runs -- reaching an
	// instance method through a class name with no instance to call it on. The
	// callable *resolves*, which is why it is carried here rather than reported
	// as "not callable", and it is raised where the call would have happened.
	refusal string
	// invokedAs is the name the engine's *diagnostics* give the callee, when
	// that is not the name a backtrace gives it. `[$closure, "__invoke"]`
	// reaches the closure through Closure's trampoline, so the by-reference
	// complaint says `Closure::__invoke()` while the trace still shows the
	// closure's own compiled name.
	invokedAs string
}

// diagnosticName is the callee's name as a diagnostic raised against the call
// spells it: the name it was reached by, unless a trampoline stands in front of
// it.
func (c *pendingCall) diagnosticName() string {
	if c.invokedAs != "" {
		return c.invokedAs
	}
	return c.name
}

// diagnosticTraceName is diagnosticName for the diagnostics that otherwise
// spell the callee the way a backtrace does.
func (c *pendingCall) diagnosticTraceName() string {
	if c.invokedAs != "" {
		return c.invokedAs
	}
	return c.traceName()
}

// pendingFrame is what a call site hands to the frame it is about to create,
// for the things the callee's own op_array cannot say.
type pendingFrame struct {
	// extraNamed are named arguments with no parameter of their own, which only
	// a variadic takes.
	extraNamed []namedArgument
	// strict is `declare(strict_types=1)` in the *caller's* file, which is what
	// decides whether an argument is coerced. The callee's own setting governs
	// only its return value.
	strict bool
	// viaTrampoline marks a call the engine reached through a trampoline it
	// built in C -- `Closure::__invoke`. The caller of the body is then that
	// trampoline rather than the script, so an argument TypeError has no call
	// site to name and leaves the `, called in ...` clause off.
	viaTrampoline bool
}

// namedArgument is `f(name: value)`. The compiler puts the parameter name in
// op2 as a constant, where a positional argument carries its number instead.
type namedArgument struct {
	name  string
	value php.Value
}

// forwardArgs shapes the argument list the callee expects. A magic __call takes
// two: the method name that was asked for, and everything that was passed to
// it collected into an array.
// traceArgs is what a stack frame shows for this call: the positional
// arguments, followed by any named ones no parameter claimed, under the names
// they arrived with. A variadic callee is the only one that has such extras --
// and the only reason a name survives to be seen in a backtrace at all.
func (c *pendingCall) traceArgs(extras []namedArgument) ([]php.Value, []string) {
	args := c.forwardArgs()
	if len(extras) == 0 {
		return args, nil
	}
	names := make([]string, 0, len(extras))
	shown := make([]php.Value, 0, len(args)+len(extras))
	shown = append(shown, args...)
	for _, extra := range extras {
		shown = append(shown, extra.value)
		names = append(names, extra.name)
	}
	return shown, names
}

func (c *pendingCall) forwardArgs() []php.Value {
	if !c.forwarding {
		return c.args
	}
	passed := php.NewArray()
	for _, arg := range c.args {
		passed.Append(arg.Deref().Copy())
	}
	// A named argument to a method the class does not declare keeps its name:
	// __call receives it as a string key rather than a position.
	for _, argument := range c.named {
		passed.Set(php.StringKey(argument.name), argument.value.Deref().Copy())
	}
	return []php.Value{php.String(c.forwardTo), php.Arr(passed)}
}

// plain reports whether the callee is an ordinary function named by string,
// with none of the resolutions -- method, closure, constructor -- that the call
// site may already have performed.
func (c *pendingCall) plain() bool {
	return c.this == nil && c.method == nil && c.builtinMethod == "" && c.builtinCtor == nil &&
		c.closure == nil && c.enum == nil && c.closureStatic == "" && c.weakStatic == "" &&
		c.fiberStatic == ""
}

// traceName renders the callee as a stack trace names it. A call queued as
// "C::m" is written "C->m" when it has an instance, which is the distinction
// PHP's traces draw between a method call and a static one.
func (c *pendingCall) traceName() string {
	if c.forwarding {
		// The frame is the accessor's, not the name the caller wrote: a
		// backtrace through __call shows `C->__call('m', Array)`.
		name := magicCall
		if c.this == nil {
			name = magicCallStat
		}
		if c.class != nil {
			if c.this != nil {
				return php.DisplayName(c.class.Name) + "->" + name
			}
			return php.DisplayName(c.class.Name) + "::" + name
		}
		return name
	}
	name := c.name
	if at := strings.Index(name, "::"); at >= 0 && c.method != nil &&
		c.closure == nil && c.class != nil {
		// A frame names the class that *declares* the method, not the one the
		// call site named nor the object's own: `$child->inherited()` shows
		// the parent, an override shows the overriding class, and a trait
		// method shows the class that used the trait.
		name = php.DisplayName(c.class.Name) + "::" + name[at+len("::"):]
	}
	if c.this != nil {
		if at := strings.Index(name, "::"); at >= 0 {
			return name[:at] + "->" + name[at+len("::"):]
		}
	}
	return name
}

// Unit is a program: the file's own code plus the functions it declared.
type Unit struct {
	file *pb.OpArray
	// compileDiagnostic is what the compiler printed while compiling this file
	// and still succeeding. It is the program's output, and it comes before
	// anything the program itself prints.
	compileDiagnostic string
	// autoGlobals are the just-in-time superglobals compiling the file
	// created (`Unit.auto_globals`), seeded before it runs.
	autoGlobals []string
	functions   map[string]*pb.OpArray
	// libraryFunctions are harvested PHP helpers supplied by the embedding
	// application. They are callable from one another but are deliberately not
	// reported as user declarations.
	libraryFunctions map[string]*pb.OpArray
	// userlandBuiltins maps engine function names to harvested PHP bodies. The
	// public name keeps the engine's signature and internal-function identity.
	userlandBuiltins map[string]*pb.OpArray
	// userlandInitializers are trusted library functions run before the
	// application body. They register request-local extension state.
	userlandInitializers []*pb.OpArray
	classes              map[string]*Class
	// builtins are the engine's own classes, harvested into the manifest. They
	// are kept apart from the user's so a program can shadow one.
	builtins map[string]*Class
	// staticProps hold class static properties, boxed so that every reader
	// shares one slot. Like statics, they belong to the unit rather than a
	// package variable.
	staticProps map[string]*php.Ref
	// silentDeprecation counts the nested reasons not to report a deprecation
	// at all: defined() asks whether a constant exists without using it.
	silentDeprecation int
	// reportingDeprecation names the constants whose deprecation message is
	// being built right now. A message may name another deprecated constant --
	// which is reported, and before the outer one -- but a message that names
	// its *own* constant would otherwise report it forever.
	reportingDeprecation map[string]bool
	// visitingConstant names the class constants whose initializers are being
	// evaluated right now. A constant expression that reaches one of them again
	// is a cycle with no bottom, and the engine says so rather than recursing.
	// The fetch the *program* made is not in here: only the ones a constant
	// expression makes are, which is what decides where the cycle is noticed.
	visitingConstant map[string]bool
	// inConstExpression counts the nested constant-expression evaluations. The
	// engine makes the whole evaluation one `[constant expression]` frame, however
	// many constants it reaches through, so only the outermost pushes one.
	inConstExpression int
	// constShortCircuit records that a `?->` on null fired inside a constant
	// expression. Everything further along the same chain -- another property,
	// a subscript -- is skipped and the whole chain is null; anything outside
	// the chain still runs, which is what makes `(null)?->a + f()` call f.
	constShortCircuit bool
	// callStrict is `declare(strict_types=1)` in the file the call about to be
	// dispatched was written in, which decides whether its arguments convert.
	callStrict bool

	// resultUnused says the call about to be dispatched throws its return value
	// away, which the opline records and one builtin's rebuild depends on.
	resultUnused bool

	// dynamicCall says the call about to be dispatched was reached through a
	// name computed at run time. It is set immediately before the dispatch and
	// consumed by it, so it never outlives the one call it describes.
	dynamicCall bool
	// ini holds request-local changes to settings php2go models. The harvested
	// registry supplies ini_get_all's build-specific defaults and ownership.
	ini                  map[string]string
	iniEntries           []*pb.IniEntry
	iniByName            map[string]*pb.IniEntry
	extensions           map[string]bool
	iniRegistryAvailable bool
	// realpathCache is Zend virtual-cwd state made observable by
	// realpath_cache_get/size. Entries retain their native allocation size and
	// insertion order because both are public behavior.
	realpathCache         map[string]*realpathCacheBucket
	realpathCacheSize     int64
	realpathCacheSequence uint64
	// syslogLocked means this request owns libc's process-global logger until
	// closelog() or teardown.
	syslogLocked bool
	// Assertion options are request-local mutable INI-era state. They remain
	// separate from zend.assertions, which decides whether the call is compiled.
	assertOptionsInitialized bool
	assertActive             bool
	assertWarning            bool
	assertBail               bool
	assertException          bool
	assertCallback           php.Value
	// random is the request-local default MT19937 engine shared by mt_rand,
	// array randomization and string shuffling.
	random *php.MT19937
	// keeping is a value that is live but not yet stored anywhere a walk would
	// find it, held aside for the length of one collection.
	keeping php.Value
	// collecting guards against a destructor triggering the collection that is
	// already running.
	collecting bool
	// collectDepth bounds the cascade a destructor sets off by releasing
	// something whose destructor releases something else, so a long chain
	// cannot recurse without end.
	collectDepth int
	// sinceCollect counts the release points that have passed without a walk,
	// which is how a large heap avoids one walk per dropped name.
	sinceCollect int
	// weakContainers are the WeakMap and WeakReference objects the program has
	// made, which a collection prunes.
	weakContainers []*php.Object
	// fibers are every fiber the program made, so the ones left suspended can
	// be unwound when the request ends.
	fibers []*fiber
	// currentFiber is the fiber running right now, if any. Fiber::suspend has
	// no argument saying which fiber to suspend: it suspends this one.
	currentFiber *fiber
	// fiberSwitchBlocked counts nested execution contexts, such as tick lists,
	// from which Zend forbids every fiber handoff.
	fiberSwitchBlocked int
	// frames is the stack of frames currently executing, innermost last. A
	// builtin has no frame, so this is how one reaches its caller's variables.
	frames []*frame
	// functionOrder lists the user functions in declaration order, which the
	// map cannot keep and get_defined_functions() reports.
	functionOrder []string
	// classOrder lists the user classes in declaration order, which the map
	// cannot carry and get_declared_classes needs.
	classOrder []string
	// composed records the classes whose traits have already been checked, so
	// a declaration reached twice does not report the same conflict twice.
	composed map[string]bool
	// backedChecked records the enums whose case values have been checked for
	// duplicates, which the engine does once when it builds the table.
	backedChecked map[string]error
	// pending carries what the call site knows and the frame needs, from the
	// dispatch to the frame about to be created. A single slot is enough
	// because nothing else creates a frame in between, and runFrame clears it
	// whether or not it uses it.
	pending pendingFrame
	// constantValues caches class constants whose initializer had to be
	// evaluated, keyed by the class that declared it. The engine keeps the
	// result on the declaring class, so a diagnostic the evaluation raises is
	// reported once however many subclasses the constant is read through.
	constantValues map[constantKey]php.Value
	// forwardedNamed holds the named arguments a builtin call had no parameter
	// for, moved out of `pending` because no builtin builds a frame to consume
	// them. Only call_user_func reads them; it forwards them to its callee.
	forwardedNamed []namedArgument
	// included records the files already pulled in, which is what the _once
	// forms of include and require consult. includeOrder keeps the order they
	// arrived in, which is what get_included_files() reports.
	included     map[string]bool
	includeOrder []string
	// shutdown holds what register_shutdown_function collected, run after the
	// body finishes.
	// Tick callbacks are a request-local live linked list. Dispatch observes
	// callbacks appended or removed by callbacks earlier in the same pass.
	tickHead *tickFunction
	tickTail *tickFunction
	// tickCount is shared by every ZEND_TICKS opcode in the request.
	tickCount uint32
	// outputRewriter backs the internal URL-Rewriter output handler. It is
	// created lazily by output_add_rewrite_var and lives for this request.
	outputRewriter *outputRewriter
	// User stdlib.Stream filters are volatile registrations: visible for this request,
	// discarded before the next bundle run.
	// Stream wrappers are volatile registrations layered over the six linked
	// startup wrappers. Order is observable through stream_get_wrappers().
	// enumCases hold the singleton object for each case. Cases compare by
	// identity, so a case must be built once and shared.
	enumCases map[string]php.Value
	// statics hold `static $x` values, which persist across calls but not across
	// programs -- so they live on the unit, never in a package variable.
	statics map[string]php.Value
	// nextObjectID hands out the handles PHP prints as #1, #2 and so on.
	nextObjectID int
	// freeHandles are the handles of objects that have been freed. The engine
	// keeps its object store's free slots on a list and gives the most recently
	// freed one to the next `new`, which is why two statements each making and
	// dropping an object both print #1.
	freeHandles []int
	// lastWalk is how many values the last reachability walk touched, which is
	// what the next walk's budget is measured against.
	lastWalk int
	// building is the objects whose constructors are running. Between ZEND_NEW
	// and the end of the constructor an object is named by nothing the walk can
	// see, so it is a root in its own right for that window.
	building []*php.Object
	// updated records the classes whose deferred initializers have been
	// resolved, which the engine does once per class per request.
	updated map[*Class]bool
	// defaults holds the resolved value of a property default the compiler
	// could not fold. The harvest is shared, so the answer is cached here.
	defaults map[*pb.Property]php.Value
	// live is every object `new` produced, in creation order, so the request
	// can still run their destructors when it ends. PHP destroys an object the
	// moment its last holder goes away; php2go has no refcounts, so what it can
	// reproduce faithfully is the *shutdown* half of that.
	live []*php.Object
	// objects is every object the request has made, destructible or not. It is
	// only read by gc_collect_cycles(), which has to know what is unreachable
	// and not merely what has a destructor waiting.
	objects []*php.Object
	// refHosts is every array a reference has been bound into. A cycle made
	// only of arrays needs one, so these are the seeds of the search for it.
	refHosts []*php.Array
	// destructed guards against running a destructor twice, which the engine
	// also guards -- an object destroyed during shutdown is not destroyed again
	// when the store is torn down.
	destructed map[*php.Object]bool
	// magicInFlight stops a magic accessor from re-entering itself for the
	// property it is already handling, which is how PHP keeps `$this->$name`
	// inside __get from looping.
	magicInFlight map[magicGuard]bool
	// linkAbort carries a failure out of a variance check that had to autoload.
	// The check itself can only answer "decidable" or not; a request that ended
	// underneath it is neither, and has to travel rather than be reported as a
	// class that could not be obtained.
	linkAbort error
	// linkAbortName is the class the autoload was for, which the diagnostic
	// names: the failure is reported as part of the link that asked for it.
	linkAbortName string
	// autoloading names the classes an autoloader is currently resolving, so a
	// callback that names the same class again does not recurse.
	autoloading map[string]bool
	// constDepth bounds evaluation of deferred constant initializers. One that
	// refers to itself is a cycle, and it has to be stopped by something other
	// than the Go stack.
	constDepth int
	// declaredCount is how many times the harvest saw each class name, and
	// bound is how many ZEND_DECLARE_CLASS oplines have run for it. A class
	// declared unconditionally is bound when the file compiles, so the first
	// opline for a name the harvest saw twice is already a redeclaration.
	declaredCount map[string]int
	// deferredCount is how many ZEND_DECLARE_CLASS oplines the file has for
	// each name. What is left of declaredCount after subtracting it is what the
	// compiler bound itself, which is taken before any opline runs.
	deferredCount map[string]int
	// deferred names the classes whose declaration is an opline rather than a
	// compile-time bind, and declareOpcodes is the opcode numbers that do it,
	// resolved through the manifest rather than assumed.
	deferred map[string]bool
	// linking names the classes whose own declaration is in progress -- waiting
	// for a parent, an interface or a trait. Such a class cannot be linked
	// against: an autoloader that tries reports it as not found.
	linking          map[string]bool
	declareOpcodes   map[uint32]bool
	firstDeclaration map[string]*pb.Class
	bound            map[string]int
}

// Load prepares a harvested unit for execution. Function names are matched
// case-insensitively, as PHP does.
func Load(unit *pb.Unit) *Unit {
	loaded := &Unit{
		file:              unit.GetFile(),
		compileDiagnostic: unit.GetCompileDiagnostic(),
		autoGlobals:       unit.GetAutoGlobals(),
		functions:         map[string]*pb.OpArray{},
		libraryFunctions:  map[string]*pb.OpArray{},
		userlandBuiltins:  map[string]*pb.OpArray{},
		classes:           map[string]*Class{},
		builtins:          map[string]*Class{},
	}
	for _, fn := range unit.GetFunctions() {
		key := strings.ToLower(fn.GetName())
		if _, seen := loaded.functions[key]; !seen {
			loaded.functionOrder = append(loaded.functionOrder, key)
		}
		loaded.functions[key] = fn
	}
	// A name the harvest carries more than once was declared more than once in
	// the file. The class table keeps only the last, so the count is what says
	// a later ZEND_DECLARE_CLASS is a redeclaration rather than the first bind.
	loaded.declaredCount = map[string]int{}
	loaded.firstDeclaration = map[string]*pb.Class{}
	for _, decl := range unit.GetClasses() {
		key := strings.ToLower(decl.GetName())
		loaded.declaredCount[key]++
		if _, seen := loaded.firstDeclaration[key]; !seen {
			// The redeclaration diagnostic names the *first* declaration's kind
			// and site, and the class table keeps only the last.
			loaded.firstDeclaration[key] = decl
		}
		if _, seen := loaded.classes[key]; !seen {
			// The class table is a map; this keeps the order the file declares
			// them in, which is the order get_declared_classes answers with.
			loaded.classOrder = append(loaded.classOrder, key)
		}
		loaded.classes[key] = newClass(decl)
	}
	return loaded
}

// functionKey normalises a function name for the function table. A fully
// qualified name may be written with a leading separator; the table is keyed
// without one, and case never matters.
func functionKey(name string) string {
	return strings.ToLower(strings.TrimPrefix(name, "\\"))
}

// newClass prepares a harvested declaration for execution.
func newClass(decl *pb.Class) *Class {
	class := &Class{
		Name:    decl.GetName(),
		Parent:  decl.GetParent(),
		decl:    decl,
		methods: map[string]*pb.OpArray{},
	}
	for _, method := range decl.GetMethods() {
		class.methods[strings.ToLower(method.GetName())] = method
	}
	return class
}

// RunUnit executes a program and returns its return value.
//
// An exception that reaches the top is not an error in the harness sense: PHP
// reports it as a fatal error on the script's own output and exits. Printing it
// rather than returning it is what makes a test expecting that text pass.
func (in *Interp) RunUnit(ctx *php.Ctx, unit *Unit) (php.Value, error) {
	defer in.restoreProcessUmask()
	defer in.cleanupTemporaryStreams()
	defer in.cleanupProcessStreams()
	defer cleanupSyslog(unit)
	unit.builtins = in.builtinClasses
	unit.iniEntries = in.iniEntries
	unit.iniByName = in.iniByName
	unit.extensions = in.extensions
	unit.iniRegistryAvailable = in.iniRegistryAvailable
	// The script itself is the first entry get_included_files() reports: the
	// engine counts the file it was asked to run alongside everything that
	// file pulls in.
	if path := unit.file.GetFilename(); path != "" && !unit.included[path] {
		if unit.included == nil {
			unit.included = map[string]bool{}
		}
		unit.included[path] = true
		unit.includeOrder = append(unit.includeOrder, path)
	}
	// Which just-in-time superglobals the program names, for the library
	// initializer that defines the request's superglobals
	// (`register_superglobals_impl`) -- the compile knows, PHP cannot.
	ctx.SetAutoGlobals(unit.autoGlobals)
	ctx.SetErrorReporting(in.defaultErrorReporting)
	in.installErrorHandler(ctx, unit)
	for _, initializer := range unit.userlandInitializers {
		if _, err := in.exec(ctx, unit, initializer, nil, 0); err != nil {
			return php.Undef(), in.materialize(ctx, unit, err)
		}
	}
	// What an object shows under `var_dump` and `print_r`. It is installed
	// here rather than at each call site because `print_r` is harvested PHP
	// now and reaches the question through a `PHP2Go\Internal` leaf, which has
	// no interpreter to ask.
	ctx.SetObjectProperties(newDumper(in, unit, ctx).properties)
	// A lazy object owes its initializer to any library function that looks at
	// the real state. `json_encode` is the first to ask through the Ctx rather
	// than from inside the interpreter.
	ctx.SetLazyResolver(func(c *php.Ctx, v php.Value) (php.Value, error) {
		return in.materializeLazy(c, unit, v, 0)
	})
	// The hierarchy hop. Only the generated program installed one, so a shared
	// builtin asking `ctx.InstanceOfClass` under the interpreter was told "no
	// class table" and had to answer as if nothing matched -- which is how
	// `json_encode` of a `JsonSerializable` came back `{}`.
	// The declared-property list the shared lazy machinery reads. The walk is
	// here because the hierarchy is: `ancestry` is the interpreter's own order,
	// and `u.defaults` is where an already-evaluated initializer lives.
	ctx.SetDeclaredProperties(func(class string) []php.PropertyDesc {
		found, ok := unit.findClass(class)
		if !ok {
			return nil
		}
		var out []php.PropertyDesc
		for _, current := range unit.ancestry(found) {
			for _, prop := range unit.properties(current) {
				desc := php.PropertyDesc{
					Name:       prop.GetName(),
					Class:      current.Name,
					Type:       prop.GetType(),
					Static:     prop.GetStatic(),
					Visibility: prop.GetVisibility(),
					Readonly:   prop.GetReadonly(),
					Virtual:    prop.GetVirtual(),
					HasDefault: prop.GetHasDefault(),
				}
				if desc.HasDefault {
					if resolved, cached := unit.defaults[prop]; cached {
						desc.Default = resolved.Copy()
					} else {
						desc.Default = literalValue(prop.GetDefaultValue())
					}
				}
				out = append(out, desc)
			}
		}
		return out
	})
	// The three questions the shared lazy-object machinery cannot answer for
	// itself, all of them about this back-end's class table.
	ctx.SetMagicAvailable(func(object *php.Object, method, prop string) bool {
		return unit.magicAvailable(object, method, prop)
	})
	// What a weak container cannot answer for itself. The interpreter can
	// compute reachability, so it sweeps before answering; and letting go of a
	// value may run a destructor there and then.
	ctx.SetWeakObserve(func(c *php.Ctx) { unit.observeWeak(c) })
	ctx.SetWeakRelease(func(c *php.Ctx, v php.Value) error {
		return in.releaseCheck(c, unit, v)
	})
	ctx.SetClassIsFinal(func(class string) bool {
		found, ok := unit.findClass(class)
		return ok && found.decl.GetFinal()
	})
	ctx.SetProxyCompatible(func(c *php.Ctx, proxyClass, instanceClass string) bool {
		return unit.compatibleProxy(c, proxyClass, instanceClass)
	})
	ctx.SetLazyInitCall(func(c *php.Ctx, initializer php.Value, object *php.Object) (php.Value, error) {
		// **At the access**, not from inside an internal function: the engine
		// runs the initializer where the state was produced, so its frame names
		// the line that touched the object.
		at, line := c.Location()
		return in.callValueAt(c, unit, initializer, []php.Value{php.Obj(object)}, at, line, 0)
	})
	ctx.SetSubclassOf(func(child, ancestor string) bool {
		class, known := unit.findClass(child)
		return known && unit.instanceOf(class, ancestor)
	})
	// What an object presents to a scope: hooks run, virtual properties in,
	// non-public names left mangled for the caller to filter.
	// The two class-table questions `serialize` asks. `hasMagicNamed` is the
	// same lookup the interpreter's own dispatch uses, so neither arm has its
	// own idea of what "has this method" means.
	ctx.SetHasMethod(func(class, method string) bool {
		return unit.hasMagicNamed(class, method)
	})
	ctx.SetClassFacts(func(class string) php.ClassFacts {
		found, known := unit.findClass(class)
		if !known {
			// An engine class the program uses but does not declare.
			if php.EngineNotSerializable(class) {
				return php.ClassFacts{Known: true, NotSerializable: true}
			}
			return php.ClassFacts{}
		}
		return php.ClassFacts{
			Known:                  true,
			NotSerializable:        found.decl.GetNotSerializable() || php.EngineNotSerializable(class),
			NoDynamicProperties:    found.decl.GetNoDynamicProperties(),
			AllowDynamicProperties: found.decl.GetAllowDynamicProperties(),
			Uninstantiable:         instantiable(found),
		}
	})
	ctx.SetPropertyFacts(func(class, property string) (bool, bool) {
		found, known := unit.findClass(class)
		if !known {
			return false, false
		}
		decl, declared := unit.propertyDecl(found, property)
		return declared, declared && decl.GetVirtual()
	})
	ctx.SetNewInstance(func(_ *php.Ctx, class string) (*php.Object, error) {
		found, known := unit.findClass(class)
		if !known {
			return nil, nil
		}
		return unit.instantiate(found), nil
	})
	ctx.SetEnumCase(func(c *php.Ctx, class, name string) (php.Value, php.EnumLookup) {
		found, known := unit.findClass(class)
		if !known || found.decl.GetKind() != pb.Class_KIND_ENUM {
			return php.Undef(), php.EnumNotAnEnum
		}
		decl, isCase := unit.enumCaseDecl(found, name)
		if !isCase {
			if _, _, isConstant := unit.findClassConstant(found, name); isConstant {
				return php.Undef(), php.EnumNotACase
			}
			return php.Undef(), php.EnumNoSuchConstant
		}
		value, err := in.enumCase(c, unit, found, decl, 0)
		if err != nil {
			return php.Undef(), php.EnumNoSuchConstant
		}
		return value, php.EnumCaseFound
	})
	ctx.SetVisibleProperties(func(c *php.Ctx, o *php.Object, scope string) (*php.Array, error) {
		var within *Class
		if scope != "" {
			if found, known := unit.findClass(scope); known {
				within = found
			}
		}
		return in.hookedVisibleProps(c, unit, nil, o, within, 0, false)
	})
	// Converting an object to a string and counting one: both are method
	// lookups, which is the class table's. They were package-level vars in
	// runtime/php that only the generated program ever set, so a shared builtin
	// reaching `php.ToString` with an object under the interpreter threw where
	// PHP converts -- `settype($withToString, "string")` is how that surfaced.
	ctx.SetObjectToString(func(c *php.Ctx, o *php.Object) (php.Value, bool, error) {
		rendered, err := in.toString(c, unit, php.Obj(o))
		if err != nil {
			if _, refused := php.AsError(err); refused {
				// "could not be converted to string" is the *caller's* to
				// report, from the same words, so it is a false answer here
				// rather than an error.
				return php.Null(), false, nil
			}
			return php.Null(), false, err
		}
		return rendered, true, nil
	})
	ctx.SetCountObject(func(c *php.Ctx, o *php.Object) (php.Value, bool, error) {
		return in.countOf(c, unit, php.Obj(o), 0)
	})
	// The INI settings a script writes. The rule -- which names are modelled,
	// which refuse a write -- is in runtime/stdlib so compiled code has it too;
	// the *storage* is the Unit's, because a setting the build declares has a
	// value in the harvested registry that nothing below could know.
	ctx.SetIniStore(php.IniStore{
		Read: func(name string) (string, bool) {
			if value, held := unit.ini[name]; held {
				return value, true
			}
			if entry := unit.iniByName[name]; entry != nil && entry.LocalValue != nil {
				return entry.GetLocalValue(), true
			}
			return "", false
		},
		Write: unit.setSetting,
	})
	// An argument marked `#[\SensitiveParameter]` is replaced in a backtrace
	// by a SensitiveParameterValue, and building one needs the object store.
	ctx.SetSensitiveHider(unit.sensitiveHider())
	// A chunked output buffer runs its filter from inside Ctx.Write, which has
	// no way to call a PHP callable on its own.
	ctx.SetBufferFlusher(func(filter any, text string, phase int64) (string, bool, error) {
		if internal, ok := filter.(php.InternalOutputFilter); ok {
			return internal.FilterOutput(text, phase)
		}
		callee, ok := filter.(php.Value)
		if !ok {
			return text, true, nil
		}
		produced, err := in.callValue(ctx, unit, callee,
			[]php.Value{php.String(text), php.Int(phase)}, 0)
		if err != nil {
			return "", false, in.materialize(ctx, unit, err)
		}
		if produced.Deref().Kind() == php.KindFalse {
			return text, false, nil
		}
		return produced.Deref().String(), true, nil
	})
	// Whatever compiling the file printed comes first: PHP emits it as the file
	// is compiled, before any of it runs.
	ctx.Write(unit.compileDiagnostic)
	value, err := in.runGlobal(ctx, unit, unit.file)
	if _, exited := asExit(err); exited {
		// exit() stops the body but not the request: shutdown functions still
		// run, and the script is not a failure.
		value, err = php.Undef(), nil
	}
	if escaped, ok := asThrown(err); ok {
		// An installed handler receives the exception instead of the engine
		// printing a fatal error; the script still stops either way.
		if handled := in.callExceptionHandler(ctx, unit, escaped); !handled {
			in.settleMessages(ctx, unit, escaped)
			ctx.Write(escaped.fatalErrorText())
		}
		value, err = php.Undef(), nil
	}
	// Shutdown functions run after the body, including after it died, and the
	// destructors of whatever is still alive run after them. A gap inside one
	// is reported: the body may have finished, but the request did not.
	if failed := in.runShutdown(ctx, unit); failed != nil && err == nil {
		err = failed
	}
	// A fiber left suspended is unwound before the destructors run, since the
	// unwind can itself drop the last reference to something.
	in.closeFibers(ctx, unit)
	in.runDestructors(ctx, unit)
	// A buffer the script never closed is flushed by the engine at the end of
	// the request, innermost first and through its filter. Without this the
	// contents are simply lost, which is not a gap the tests can see -- it just
	// looks like the script printed nothing.
	if drained := in.drainBuffers(ctx, unit); drained != nil {
		if escaped, ok := asThrown(drained); ok {
			if handled := in.callExceptionHandler(ctx, unit, escaped); !handled {
				in.settleMessages(ctx, unit, escaped)
				ctx.Write(escaped.fatalErrorText())
			}
		}
	}
	return value, err
}

// drainBuffers ends every output buffer still open, as the engine does when the
// request finishes. A filter that throws here reaches the exception handler,
// which is why this runs where an exception can still be reported.
func (in *Interp) drainBuffers(ctx *php.Ctx, unit *Unit) error {
	// The same shared body ob_end_flush() runs, so a filter behaves identically
	// whether the script ended the buffer or the request did.
	in.installCallValue(ctx, unit, 0)
	fn, _ := php.LookupBuiltin("ob_end_flush")
	for ctx.BufferLevel() > 0 {
		if _, err := fn(ctx, nil); err != nil {
			return in.materialize(ctx, unit, err)
		}
	}
	return nil
}

// callExceptionHandler passes an escaping exception to what
// set_exception_handler installed, reporting whether there was one.
func (in *Interp) callExceptionHandler(ctx *php.Ctx, unit *Unit, escaped *thrown) bool {
	handler, installed := ctx.CurrentExceptionHandler()
	if !installed {
		return false
	}
	target, ok := unit.dynamicTarget(ctx, handler)
	if !ok {
		return false
	}
	// The engine clears the handler for the duration of the call, so an
	// exception the handler itself throws has no handler -- unless the handler
	// installed one, which is exactly what makes a chain of them work.
	drop := ctx.SuppressExceptionHandler()
	defer drop()
	target.args = []php.Value{php.Obj(escaped.object)}
	// The body has finished, so there is no line to attribute anything the
	// *call itself* raises to -- a deprecated handler, for one. The engine
	// says "Unknown on line 0"; the handler's own frame sets real locations
	// again as soon as it starts.
	ctx.SetLocation("Unknown", 0)
	// The engine calls the handler from C, so a trace taken inside it shows the
	// handler as a frame with no file and no line. Without the push, an
	// exception thrown *by the handler* reports a trace of `#0 {main}` alone.
	ctx.PushCall(php.StackFrame{
		Function: target.traceName(), File: php.InternalFrameFile,
		Args: target.args, This: target.this,
	})
	defer ctx.PopCall()
	_, err := in.dispatch(ctx, unit, target, 0)
	if err == nil {
		return true
	}
	second, isThrown := asThrown(err)
	if !isThrown {
		return true
	}
	// Whatever the handler installed *while it ran* is what answers for its own
	// exception. The placeholder stays in place for that lookup: with nothing
	// installed it is what the lookup finds, so the handler is not called a
	// second time and the exception is uncaught -- which PHP reports as a fatal
	// error naming it.
	if in.callExceptionHandler(ctx, unit, second) {
		return true
	}
	in.settleMessages(ctx, unit, second)
	ctx.Write(second.fatalErrorText())
	return true
}

// resolveNamespacedFunction applies the fallback an unqualified call inside a
// namespace gets: foo\strlen() means foo\strlen if that is defined and the
// global strlen otherwise. Only functions fall back this way -- classes do not
// -- and the compiler marks the sites by emitting INIT_NS_FCALL_BY_NAME, which
// is why this is decided here rather than in call.
func (in *Interp) resolveNamespacedFunction(unit *Unit, name string) string {
	if in.functionExists(unit, name) {
		return name
	}
	global := name
	if at := strings.LastIndexByte(name, '\\'); at >= 0 {
		global = name[at+1:]
	}
	if in.functionExists(unit, global) {
		return global
	}
	// Neither exists. Reporting the namespaced name is what PHP does, and the
	// message is what the tests expect.
	return name
}

// functionExists reports whether a call to this name could be answered by the
// program, by the runtime, or by the linked build.
func (in *Interp) functionExists(unit *Unit, name string) bool {
	lower := strings.ToLower(name)
	if _, ok := unit.functions[lower]; ok {
		return true
	}
	if _, ok := unit.libraryFunctions[lower]; ok {
		return true
	}
	if _, ok := unit.userlandBuiltins[lower]; ok {
		return true
	}
	if in.scopedFunctions[lower] {
		return true
	}
	if _, ok := builtins[lower]; ok {
		return true
	}
	return in.engineFunctions[lower]
}

// userFunctionBody resolves a PHP function body without treating an engine
// builtin alias as a user function. Installed helpers otherwise behave as PHP.
func (unit *Unit) userFunctionBody(name string) (*pb.OpArray, bool) {
	if unit == nil {
		return nil, false
	}
	key := functionKey(name)
	if fn, ok := unit.functions[key]; ok {
		return fn, true
	}
	fn, ok := unit.libraryFunctions[key]
	return fn, ok
}

// call invokes a function by name, preferring a user definition over a builtin
// so a program can shadow one -- which PHP permits for many names.
func (in *Interp) call(ctx *php.Ctx, unit *Unit, name string, args []php.Value, depth int) (php.Value, error) {
	// Consume the flags at once: a call this one makes is not itself dynamic,
	// and its own result is wanted whatever this one's caller does with it.
	dynamic := unit.dynamicCall
	unit.dynamicCall = false
	resultUsed := !unit.resultUnused
	unit.resultUnused = false
	callStrict := unit.callStrict
	unit.callStrict = false
	lower := strings.ToLower(name)
	if fn, ok := unit.functions[lower]; ok {
		if depth >= maxDepth {
			return php.Undef(), fmt.Errorf("maximum call depth %d exceeded calling %s", maxDepth, name)
		}
		return in.exec(ctx, unit, fn, args, depth+1)
	}
	if fn, ok := unit.libraryFunctions[lower]; ok {
		if depth >= maxDepth {
			return php.Undef(), fmt.Errorf("maximum call depth %d exceeded calling %s", maxDepth, name)
		}
		return in.exec(ctx, unit, fn, args, depth+1)
	}
	if in.scopedFunctions[lower] && in.scopedDispatch != nil {
		if signature, ok := in.scopedSignatures[lower]; ok {
			for i, parameter := range signature.Parameters {
				if !parameter.ByRef {
					continue
				}
				if i < len(args) && !args[i].IsUndef() || parameter.Variadic && i < len(args) {
					return php.Undef(), &UnsupportedError{Opcode: "by-reference call to generated function " + name}
				}
			}
			if len(unit.pending.extraNamed) != 0 {
				return php.Undef(), &UnsupportedError{Opcode: "named variadic arguments to generated function " + name}
			}
		}
		if depth >= maxDepth {
			return php.Undef(), fmt.Errorf("maximum call depth %d exceeded calling %s", maxDepth, name)
		}
		return in.scopedDispatch(ctx, name, args)
	}
	// Everything below this point is an engine function, whose parameter types
	deprecated := ""
	switch lower {
	case "assert_options":
		deprecated = "Function assert_options() is deprecated since 8.3"
	case "strptime":
		deprecated = "Function strptime() is deprecated since 8.2, use date_parse_from_format() (for locale-independent parsing), or IntlDateFormatter::parse() (for locale-dependent parsing) instead"
	}
	if deprecated != "" {
		ctx.Deprecated("%s", deprecated)
		if pending := ctx.TakePendingError(); pending != nil {
			return php.Undef(), pending
		}
	}
	// the harvest knows. Checking them here covers all three dispatch paths at
	// once rather than once per builtin.
	if lower == "stream_context_set_option" && len(args) == 2 {
		ctx.Deprecated("Calling stream_context_set_option() with 2 arguments is deprecated, use stream_context_set_options() instead")
		if pending := ctx.TakePendingError(); pending != nil {
			return php.Undef(), pending
		}
	}
	// A public builtin replaced by a hidden PHP body still retains engine
	// passability rules. `shuffle`'s array is required-ref (not prefer-ref), so a
	// a temporary is refused before arity or type validation. `getopt` has the same
	// rule for its third argument. Both refusals happen at SEND.
	if lower == "shuffle" && len(args) > 0 && !args[0].IsRef() {
		return php.Undef(), in.materialize(ctx, unit, Raise(
			"Error", "shuffle(): Argument #1 ($array) could not be passed by reference",
		))
	}
	if lower == "getopt" && len(args) > 2 && !args[2].IsRef() {
		return php.Undef(), in.materialize(ctx, unit, Raise(
			"Error", "getopt(): Argument #3 ($rest_index) could not be passed by reference",
		))
	}

	if err := in.verifyBuiltinArgs(ctx, unit, lower, args, callStrict); err != nil {
		return php.Undef(), err
	}
	if dynamic && scopeIntrospection[lower] {
		// zend_forbid_dynamic_call, which each of these calls itself after
		// parsing its parameters -- so a wrong argument count is reported
		// first.
		return php.Undef(), in.materialize(ctx, unit,
			Raise("Error", "Cannot call %s() dynamically", lower))
	}
	if fn, ok := unit.userlandBuiltins[lower]; ok {
		if depth >= maxDepth {
			return php.Undef(), fmt.Errorf("maximum call depth %d exceeded calling %s", maxDepth, name)
		}
		return in.exec(ctx, unit, fn, args, depth+1)
	}
	// **Only now is the call known to be answered in Go.** A userland builtin is
	// an engine function whose body is PHP -- `http_build_query` is one -- and
	// its frame is a PHP frame: the body reads `debug_backtrace()[1]` to find
	// its caller's scope, which is off by one the moment this frame is skipped.
	// Marking before the branch above cost exactly that, and the bundle test
	// caught it.
	ctx.MarkInternalCall(name)
	// Named arguments this call had no parameter for belong to it and to
	// nothing after it. No builtin builds a frame, which is what would
	// otherwise consume them, so they are moved here: call_user_func forwards
	// them to its callee, and every other builtin lets them go.
	unit.forwardedNamed, unit.pending.extraNamed = unit.pending.extraNamed, nil
	// These need the class table or the interpreter itself, neither of which a
	// ctx-only builtin can reach.
	if value, handled, err := in.programBuiltin(ctx, unit, lower, args, depth); handled {
		return value, in.materialize(ctx, unit, err)
	}
	// The constant builtins need the engine's constant table, which lives on the
	// interpreter rather than the context, so they are handled here instead of
	// in the ctx-only builtins map.
	if value, handled, err := in.constantBuiltin(ctx, unit, lower, args, depth); handled {
		return value, in.materialize(ctx, unit, err)
	}
	if builtin, ok := builtins[lower]; ok {
		// A few builtins take their arguments as *text* rather than as a
		// declared string parameter the signature check would coerce, so an
		// object reaches them unconverted. They have no interpreter to call
		// __toString with, so it happens here.
		rendered, refused := in.renderTextArgs(ctx, unit, lower, args)
		args = rendered
		ctx.SetResultUsed(resultUsed)
		value, err := builtin(ctx, args)
		ctx.SetResultUsed(true)
		if refused != nil {
			// A conversion that failed still let the call run: printf has
			// written what it managed to build by then, which is what the
			// engine does. The error is what comes back.
			return php.Undef(), in.materialize(ctx, unit, refused)
		}
		return value, in.materialize(ctx, unit, err)
	}
	if !in.engineFunctions[lower] && len(in.engineFunctions) > 0 {
		// The linked build has no such function either, so the program named
		// something that does not exist -- a PHP Error, not a missing builtin.
		return php.Undef(), in.throwError(ctx, unit, "Error", "Call to undefined function %s()", name)
	}
	return php.Undef(), &UnsupportedError{Opcode: "call to " + name}
}

// textArgBuiltins names the builtins whose arguments are used as text without
// a declared string parameter to coerce them: the formatting family takes
// `mixed ...$values` and implode takes an array of anything. The value inside
// is still rendered the way PHP renders one, so an object goes through
// __toString and a class without one is an Error.
//
// The index is where the rendering starts; -1 marks the v- forms, whose values
// arrive inside the array at argument 2.
var textArgBuiltins = map[string]int{
	"sprintf": 1, "printf": 1, "fprintf": 2,
	"vsprintf": -1, "vprintf": -1, "vfprintf": -1,
	"php2go\\internal\\formatted_print": -1,
	"implode":                           0, "join": 0,
}

// renderTextArgs converts the object arguments of such a builtin to strings.
// It answers the error a failed conversion produced without stopping: the
// engine renders such an object as the empty string, finishes the call and lets
// the error surface afterwards, which is why printf prints what it had.
func (in *Interp) renderTextArgs(ctx *php.Ctx, unit *Unit, name string,
	args []php.Value) ([]php.Value, error) {
	from, wanted := textArgBuiltins[name]
	if !wanted {
		return args, nil
	}
	var refused error
	render := func(v php.Value) (php.Value, error) {
		if v.Deref().Kind() != php.KindObject {
			return v, nil
		}
		text, err := in.toString(ctx, unit, v)
		if err != nil {
			if refused == nil {
				refused = err
			}
			return php.String(""), nil
		}
		return text, nil
	}
	out := append([]php.Value(nil), args...)
	if from < 0 {
		// vsprintf and friends: the values are the elements of the array.
		if len(out) < 2 || out[1].Deref().Kind() != php.KindArray {
			return out, refused
		}
		list := php.NewArray()
		var failed error
		out[1].Deref().Array().Each(func(k php.Key, v php.Value) bool {
			converted, err := render(v)
			if err != nil {
				failed = err
				return false
			}
			list.Set(k, converted)
			return true
		})
		if failed != nil {
			return out, failed
		}
		out[1] = php.Arr(list)
		return out, refused
	}
	for i := from; i < len(out); i++ {
		if held := out[i].Deref(); held.Kind() == php.KindArray {
			// implode's pieces, whichever argument they arrived in.
			list := php.NewArray()
			var failed error
			held.Array().Each(func(k php.Key, v php.Value) bool {
				converted, err := render(v)
				if err != nil {
					failed = err
					return false
				}
				list.Set(k, converted)
				return true
			})
			if failed != nil {
				return out, failed
			}
			out[i] = php.Arr(list)
			continue
		}
		converted, err := render(out[i])
		if err != nil {
			return out, err
		}
		out[i] = converted
	}
	return out, refused
}

// dispatch performs a call that was assembled outside the main loop -- a
// shutdown function, or an exception handler. The loop has its own copy that
// can attribute errors to an opline; this one has no opline to name.
// canonicalCallName replaces an engine function's name with the one it was
// declared under. A trace and an ArgumentCountError both name the declaration
// rather than the spelling the call site used, so `call_user_func("exploDe")`
// reports `explode()`.
func (in *Interp) canonicalCallName(call *pendingCall) {
	if call == nil || call.method != nil || call.closure != nil || call.class != nil {
		return
	}
	if signature, ok := in.engineSignatures[strings.ToLower(call.name)]; ok &&
		signature.GetName() != "" {
		call.name = signature.GetName()
	}
}

func (in *Interp) dispatch(ctx *php.Ctx, unit *Unit, call *pendingCall, depth int) (php.Value, error) {
	if call.refusal != "" {
		return php.Undef(), in.throwError(ctx, unit, "Error", "%s", call.refusal)
	}
	if call.invokedAs != "" {
		unit.pending.viaTrampoline = true
	}
	// A call assembled here never passes DO_FCALL, so the deprecation that
	// opcode would have reported is reported here instead. That covers a
	// callback given to an array function and a handler the engine itself
	// calls, both of which are calls as far as #[\Deprecated] is concerned.
	switch {
	case call.closure != nil:
		// A closure that wraps a method is reported as that method --
		// `Method D::m() is deprecated` -- so the name must not carry the
		// `D->` a trace would put in front of it. A closure with no scope is a
		// function, and there the trace name is exactly right.
		as := closureTraceName(call.closure)
		if call.closure.class != nil {
			as = call.closure.op.GetName()
		}
		if err := in.noteDeprecatedCallAs(ctx, unit, call.closure.op, call.closure.class,
			as, depth); err != nil {
			return php.Undef(), err
		}
	case call.method != nil:
		if err := in.noteDeprecatedCallAs(ctx, unit, call.method, call.class,
			call.forwardTo, depth); err != nil {
			return php.Undef(), err
		}
	case call.name != "":
		if fn, ok := unit.userFunctionBody(call.name); ok {
			if err := in.noteDeprecatedCall(ctx, unit, fn, nil, depth); err != nil {
				return php.Undef(), err
			}
		}
	}
	switch {
	case call.closure != nil:
		return in.invokeClosureNamed(ctx, unit, call.closure, call.args, call.named, depth)
	case call.method != nil:
		return in.invoke(ctx, unit, call.method, call.this, call.class, call.called, call.forwardArgs(), depth)
	case call.builtinMethod != "":
		return in.builtinMethodCall(ctx, unit, call)
	default:
		unit.dynamicCall = call.dynamic
		return in.call(ctx, unit, call.name, call.args, depth)
	}
}

// invokeClosure runs a Closure. The captured variables are seeded into the
// frame before the body runs, which is where ZEND_BIND_STATIC then finds them.
func (in *Interp) invokeClosure(ctx *php.Ctx, unit *Unit, fn *closure, args []php.Value, depth int) (php.Value, error) {
	return in.invokeClosureNamed(ctx, unit, fn, args, nil, depth)
}

// invokeClosureNamed is invokeClosure for a call site that still holds the
// named arguments: a closure over __call keeps their names, since they go into
// the array the accessor receives rather than onto a parameter.
func (in *Interp) invokeClosureNamed(ctx *php.Ctx, unit *Unit, fn *closure,
	args []php.Value, named []namedArgument, depth int) (php.Value, error) {
	if depth >= maxDepth {
		return php.Undef(), fmt.Errorf("maximum call depth %d exceeded in closure", maxDepth)
	}
	if fn.forwardTo != "" {
		// The closure wraps __call rather than a method of its own, so the
		// arguments have to be repacked the way that accessor expects.
		forwarded := (&pendingCall{
			forwardTo: fn.forwardTo, forwarding: true, args: args, named: named,
		}).forwardArgs()
		return in.execClosure(ctx, unit, fn, forwarded, depth+1)
	}
	return in.execClosure(ctx, unit, fn, args, depth+1)
}

// invoke runs a resolved method with $this bound.
func (in *Interp) invoke(ctx *php.Ctx, unit *Unit, method *pb.OpArray, this *php.Object, class, called *Class, args []php.Value, depth int) (php.Value, error) {
	if depth >= maxDepth {
		return php.Undef(), fmt.Errorf("maximum call depth %d exceeded in %s", maxDepth, method.GetName())
	}
	return in.execMethod(ctx, unit, method, this, class, called, args, depth+1)
}

// invokeDriven runs a method the engine reaches for on the program's behalf: an
// Iterator method a foreach advances, __toString where a string is needed, an
// ArrayAccess offset, Countable::count. Each of those is an ordinary call as far
// as the engine is concerned, so it is a frame of its own in a backtrace --
// named for the class that declared the body, not the object's, and attributed
// to wherever the program was when the engine reached for it.
func (in *Interp) invokeDriven(ctx *php.Ctx, unit *Unit, body *pb.OpArray,
	object *php.Object, declaring, class *Class, args []php.Value,
	depth int) (php.Value, error) {
	file, line := ctx.Location()
	ctx.PushCall(php.StackFrame{
		Function: php.DisplayName(declaring.Name) + "->" + body.GetName(),
		File:     file, Line: line, This: object, Args: args,
	})
	value, err := in.invoke(ctx, unit, body, object, declaring, class, args, depth)
	ctx.PopCall()
	// The engine may ask several of these in a row -- current() then key() --
	// and each one leaves the location inside the method that just ran. The
	// next frame is pushed from the same place in the program, so put it back.
	ctx.SetLocation(file, line)
	return value, err
}

// constantBuiltin implements define, defined and constant. It reports whether
// it handled the name so an unrelated call falls through.
func (in *Interp) constantBuiltin(ctx *php.Ctx, unit *Unit, name string, args []php.Value, depth int) (php.Value, bool, error) {
	switch name {
	case "define":
		if len(args) < 2 {
			return php.Undef(), true, fmt.Errorf("define() expects at least 2 arguments, %d given", len(args))
		}
		constName := args[0].String()
		if strings.Contains(constName, "::") {
			// A class constant belongs to a class body. define() is refused
			// whatever is on either side of the separator -- "::" alone is
			// refused too.
			return php.Undef(), true, Raise("ValueError",
				"define(): Argument #1 ($constant_name) cannot be a class constant")
		}
		if constName == "__COMPILER_HALT_OFFSET__" {
			// The name belongs to __halt_compiler(), which defines it per file
			// at compile time. define() is refused whether or not any file has
			// claimed it, and the constant stays undefined either way. **By
			// its exact spelling**: `define("__compiler_halt_offset__", 2)`
			// defines an ordinary constant (probed).
			ctx.Warn("Constant %s already defined", constName)
			return php.Bool(false), true, nil
		}
		if _, exists := in.lookupConstant(ctx, constName); exists {
			ctx.Warn("Constant %s already defined", constName)
			return php.Bool(false), true, nil
		}
		ctx.Define(constName, args[1].Copy())
		return php.Bool(true), true, nil
	case "defined":
		if len(args) < 1 {
			return php.Bool(false), true, nil
		}
		// defined() does not *use* the constant, so a deprecated one is not
		// reported here.
		unit.silentDeprecation++
		_, ok, err := in.namedConstant(ctx, unit, args[0].String(), depth)
		unit.silentDeprecation--
		if _, raised := php.AsError(err); raised {
			// defined() asks silently: a name the engine would refuse to fetch
			// -- a trait constant reached through the trait -- is simply not
			// defined here. A gap in this runtime still propagates.
			return php.Bool(false), true, nil
		}
		return php.Bool(ok), true, err
	case "constant":
		if len(args) < 1 {
			return php.Undef(), true, fmt.Errorf("constant() expects 1 argument, 0 given")
		}
		value, ok, err := in.namedConstant(ctx, unit, args[0].String(), depth)
		if err != nil {
			return php.Undef(), true, err
		}
		if !ok {
			// A catchable Error, as the FETCH_CONSTANT opcode raises -- and
			// worded the same way, unquoted for a class constant.
			name := args[0].String()
			if strings.Contains(name, "::") {
				return php.Undef(), true, Raise("Error", "Undefined constant %s", name)
			}
			return php.Undef(), true, Raise("Error", "Undefined constant \"%s\"", name)
		}
		return value, true, nil
	}
	return php.Undef(), false, nil
}

// namedConstant resolves what define(), defined() and constant() accept, which
// is a superset of what the FETCH_CONSTANT opcode sees: a name containing "::"
// names a class constant, and the class half is resolved -- and autoloaded --
// the same way a Foo::A in source would be.
func (in *Interp) namedConstant(ctx *php.Ctx, unit *Unit, name string, depth int) (php.Value, bool, error) {
	className, member, isClassConstant := strings.Cut(name, "::")
	if !isClassConstant {
		value, ok := in.lookupConstant(ctx, name)
		if ok {
			in.noteDeprecatedConstantName(ctx, unit, name)
		}
		return value, ok, nil
	}
	// self, parent and static mean what they would in source, and `static`
	// means the *called* class rather than the executing one.
	switch strings.ToLower(className) {
	case "self", "parent", "static":
		resolved, ok := unit.keywordClass(ctx, className)
		if !ok {
			return php.Undef(), false, nil
		}
		className = resolved
	}
	_, found, err := in.resolveClass(ctx, unit, className, depth)
	if err != nil {
		return php.Undef(), false, err
	}
	if !found {
		return php.Undef(), false, nil
	}
	if owner, visibility, allowed := unit.constantAccess(className, member,
		unit.callingScope(ctx)); !allowed {
		return php.Undef(), false, Raise("Error", "Cannot access %s constant %s::%s",
			visibility, php.DisplayName(owner), member)
	}
	return in.classConstant(ctx, unit, className, member, depth)
}

// bindArgs places arguments into the callee's compiled variables. Parameters
// occupy the first slots in declaration order; a missing argument falls back to
// undef, which is what an unset variable reads as.
func bindArgs(f *frame, op *pb.OpArray, args []php.Value) {
	for i, arg := range args {
		if i >= len(op.GetArgs()) {
			// Extra arguments are legal in PHP. They stay in f.args, which is
			// what ZEND_RECV_VARIADIC and func_get_args read, but there is no
			// declared parameter slot to put them in.
			break
		}
		if i < len(f.slots) {
			f.slots[i] = arg
		}
	}
}

// internalDefault is the value an engine function's declaration wrote for a
// parameter. Only an internal declaration carries one as text; the forms it
// uses are the ones the stubs allow, and anything outside them is a gap rather
// than a guess.
//
// The second result is whether there was a default at all: an optional
// parameter with none is one the engine refuses to fill in.
func (in *Interp) internalDefault(ctx *php.Ctx, unit *Unit, arg *pb.ArgInfo) (php.Value, bool, error) {
	text := strings.TrimSpace(arg.GetDefaultValue())
	if text == "" {
		return php.Undef(), false, nil
	}
	switch strings.ToLower(text) {
	case "null":
		return php.Null(), true, nil
	case "true":
		return php.Bool(true), true, nil
	case "false":
		return php.Bool(false), true, nil
	case "[]", "array()":
		return php.Arr(php.NewArray()), true, nil
	}
	if len(text) >= 2 && (text[0] == '\'' || text[0] == '"') && text[len(text)-1] == text[0] {
		// The stubs write a plain literal, with no interpolation to run.
		return php.String(text[1 : len(text)-1]), true, nil
	}
	if parts := strings.Split(text, "|"); len(parts) > 1 {
		// A flags parameter's default is written as the bitmask it is:
		// `ENT_QUOTES | ENT_SUBSTITUTE | ENT_HTML401`.
		combined := int64(0)
		for _, part := range parts {
			value, ok, err := in.internalDefault(ctx, unit, &pb.ArgInfo{
				Name: arg.GetName(), DefaultValue: part,
			})
			if err != nil || !ok {
				return php.Undef(), false, err
			}
			combined |= value.Int()
		}
		return php.Int(combined), true, nil
	}
	if number, err := strconv.ParseInt(text, 0, 64); err == nil {
		return php.Int(number), true, nil
	}
	if number, err := strconv.ParseFloat(text, 64); err == nil {
		return php.Float(number), true, nil
	}
	if value, ok, err := in.namedConstant(ctx, unit, text, 0); err != nil {
		return php.Undef(), false, err
	} else if ok {
		return value, true, nil
	}
	return php.Undef(), false, &UnsupportedError{
		Opcode: "default value " + text + " for parameter $" + arg.GetName(),
	}
}

// applyNamedArguments places `f(name: value)` arguments into the positions the
// callee declares for them, leaving undef holes where nothing was passed. The
// callee is only known here -- a call by name is resolved at DO_FCALL -- so the
// resolution cannot happen when the argument is sent.
func (in *Interp) applyNamedArguments(ctx *php.Ctx, unit *Unit, call *pendingCall) error {
	if len(call.named) == 0 {
		return nil
	}
	if call.forwarding {
		// __call and __callStatic take the arguments as an array, so a named
		// one keeps its name there rather than resolving against the accessor's
		// own two parameters. forwardArgs builds it.
		return nil
	}
	if call.closure != nil && call.closure.forwardTo != "" {
		// A first-class callable of a method the class does not declare --
		// `$o->missing(...)` -- is a closure over the *forwarder*. It takes any
		// name, for the same reason the direct call does: the name lands in the
		// array __call receives, not on a parameter of the accessor.
		return nil
	}
	parameters, required, known := in.calleeParameters(unit, call)
	if !known {
		return &UnsupportedError{Opcode: "named arguments to " + call.name}
	}
	// Named arguments with no parameter of their own, which only a variadic
	// takes. They are handed to the frame about to be created; see
	// Unit.pendingExtraNamed.
	var extras []namedArgument
	// Positions the caller filled explicitly, so a name landing on one is an
	// overwrite rather than a second chance.
	// An Undef slot is padding rather than an argument: unpacking a named key
	// places it in its parameter's slot and pads the ones before it, so those
	// are still free for a later name to take.
	filled := make([]bool, len(call.args))
	for i := range filled {
		filled[i] = !call.args[i].IsUndef()
	}
	for _, argument := range call.named {
		index := -1
		for i, parameter := range parameters {
			if parameter.GetName() == argument.name && !parameter.GetVariadic() {
				index = i
				break
			}
		}
		if index < 0 {
			// A variadic callee accepts any name and keeps it as a string key.
			if len(parameters) > 0 && parameters[len(parameters)-1].GetVariadic() {
				extras = append(extras, argument)
				continue
			}
			return in.throwError(ctx, unit, "Error", "Unknown named parameter $%s", argument.name)
		}
		for len(call.args) <= index {
			call.args = append(call.args, php.Undef())
			filled = append(filled, false)
		}
		if filled[index] {
			return in.throwError(ctx, unit, "Error",
				"Named parameter $%s overwrites previous argument", argument.name)
		}
		call.args[index] = argument.value
		filled[index] = true
	}
	// An engine function has no RECV_INIT to fall back on: a hole a name left
	// behind has to be filled here, from the default the declaration wrote.
	internal := false
	if _, known := in.engineSignatures[strings.ToLower(call.name)]; known {
		internal = true
	} else if call.class != nil && call.builtinMethod != "" {
		_, internal = unit.engineMethod(call.class, call.builtinMethod)
	}
	// An engine variadic does not collect names. Only the handful of
	// forwarders declared with Z_PARAM_VARIADIC_WITH_NAMED do; every other one
	// refuses the whole call -- but only once it has enough arguments to get
	// that far. The engine's parameter parsing checks the count first, so
	// `call_user_func_array("array_multisort", ["" => 1])` is "expects at
	// least 1 argument, 0 given", not a complaint about the name.
	given := 0
	for _, occupied := range filled {
		if occupied {
			given++
		}
	}
	if internal && len(extras) > 0 && given >= required && !acceptsNamedVariadic(call) {
		return in.throwError(ctx, unit, "ArgumentCountError",
			"%s() does not accept unknown named parameters", calleeLabel(call))
	}
	// A hole the callee has no default for is an error named after the
	// parameter, which is not the same diagnostic as passing too few.
	for i, occupied := range filled {
		if occupied || i >= len(parameters) {
			continue
		}
		// Everything from the required count on has a default to fall back to.
		if i >= required {
			if !internal {
				continue
			}
			value, ok, err := in.internalDefault(ctx, unit, parameters[i])
			if err != nil {
				return err
			}
			if !ok {
				// The engine refuses to invent one rather than passing null:
				// `array_keys(array: $a, strict: true)` cannot guess what
				// $filter_value was going to be.
				return in.throwError(ctx, unit, "ArgumentCountError",
					"%s(): Argument #%d ($%s) must be passed explicitly, "+
						"because the default value is not known",
					call.name, i+1, parameters[i].GetName())
			}
			call.args[i] = value
			continue
		}
		return in.throwError(ctx, unit, "ArgumentCountError", "%s(): Argument #%d ($%s) not passed",
			call.name, i+1, parameters[i].GetName())
	}
	unit.pending.extraNamed = extras
	return nil
}

// namedVariadicCallees are the engine declarations that take their variadic
// arguments *with* names -- Z_PARAM_VARIADIC_WITH_NAMED rather than
// Z_PARAM_VARIADIC. Every one of them forwards the call somewhere else, which
// is why the names have to survive; everything else with a variadic refuses
// them outright.
var namedVariadicCallees = map[string]bool{
	"call_user_func":                   true,
	"closure::__invoke":                true,
	"closure::call":                    true,
	"fiber::start":                     true,
	"reflectionclass::newinstance":     true,
	"reflectionfunction::invoke":       true,
	"reflectionmethod::invoke":         true,
	"reflectionattribute::newinstance": true,
}

func acceptsNamedVariadic(call *pendingCall) bool {
	return namedVariadicCallees[strings.ToLower(calleeLabel(call))]
}

// calleeLabel names a call the way the engine's diagnostics do: `f` for a
// function, `C::m` for a method.
func calleeLabel(call *pendingCall) string {
	if call.class != nil && call.builtinMethod != "" {
		return php.DisplayName(call.class.Name) + "::" + call.builtinMethod
	}
	return call.name
}

// calleeParameters is the callee's declared parameter list, however the call
// names its target.
func (in *Interp) calleeParameters(unit *Unit, call *pendingCall) ([]*pb.ArgInfo, int, bool) {
	switch {
	case call.method != nil:
		return call.method.GetArgs(), int(call.method.GetRequiredArgs()), true
	case call.closure != nil && call.closure.op != nil:
		return call.closure.op.GetArgs(), int(call.closure.op.GetRequiredArgs()), true
	case call.hookFallback != nil:
		// The trampoline standing in for a hook the parent never wrote has the
		// arity of the hook it replaces, and a set hook's parameter is $value
		// wherever the hook itself does not name it otherwise.
		if call.hookIsSet {
			return []*pb.ArgInfo{{Name: "value"}}, 1, true
		}
		return nil, 0, true
	case call.this != nil && strings.EqualFold(call.builtinMethod, magicInvoke):
		// `$closure->__invoke(...)` is the closure written the long way. The
		// engine answers it with a trampoline carrying the closure's own
		// signature, so a named argument resolves against that and not against
		// anything Closure declares.
		if fn, held := asClosure(php.Obj(call.this)); held && fn.op != nil {
			return fn.op.GetArgs(), int(fn.op.GetRequiredArgs()), true
		}
	case call.this != nil && strings.EqualFold(call.builtinMethod, "call"):
		// Closure::call takes the new $this first and then whatever the
		// closure takes, so a name resolves against the closure one position
		// along. `newThis` is Closure::call's own parameter and is named too.
		if fn, held := asClosure(php.Obj(call.this)); held && fn.op != nil {
			parameters := append([]*pb.ArgInfo{{Name: "newThis"}}, fn.op.GetArgs()...)
			return parameters, 1 + int(fn.op.GetRequiredArgs()), true
		}
	case call.this != nil && strings.EqualFold(call.this.ClassName, "Directory"):
		switch strings.ToLower(call.builtinMethod) {
		case "read", "rewind", "close":
			return nil, 0, true
		}
	case call.this != nil:
		// A reflector's invoke() and newInstance() reach a declaration the
		// reflector holds, and a name given to one is that declaration's.
		if held, ok := call.this.Internal.(*reflector); ok {
			switch strings.ToLower(call.builtinMethod) {
			case "invoke":
				if held.function == nil {
					break
				}
				if held.class == nil {
					return held.function.GetArgs(), int(held.function.GetRequiredArgs()), true
				}
				// ReflectionMethod::invoke takes the object first.
				parameters := append([]*pb.ArgInfo{{Name: "object"}}, held.function.GetArgs()...)
				return parameters, 1 + int(held.function.GetRequiredArgs()), true
			case "newinstance":
				if held.class == nil {
					break
				}
				if ctor, _, found := unit.resolveMethod(held.class, "__construct"); found {
					return ctor.GetArgs(), int(ctor.GetRequiredArgs()), true
				}
			}
		}
	}
	if fn, ok := unit.userFunctionBody(call.name); ok {
		return fn.GetArgs(), int(fn.GetRequiredArgs()), true
	}
	if signature, ok := in.scopedSignatures[strings.ToLower(call.name)]; ok {
		parameters := make([]*pb.ArgInfo, len(signature.Parameters))
		for i, parameter := range signature.Parameters {
			parameters[i] = &pb.ArgInfo{Name: parameter.Name, ByRef: parameter.ByRef, Variadic: parameter.Variadic}
		}
		return parameters, signature.Required, true
	}
	if signature, ok := in.engineSignatures[strings.ToLower(call.name)]; ok {
		return signature.GetArgs(), int(signature.GetRequiredArgs()), true
	}
	// A method the engine supplies has no op_array, but the harvest recorded
	// its signature, which is all a name needs to be resolved against.
	if class, method := call.class, call.builtinMethod; class != nil && method != "" {
		if engine, supplied := unit.engineMethod(class, method); supplied {
			return engine.GetArgs(), int(engine.GetRequiredArgs()), true
		}
	}
	if call.class != nil && strings.HasSuffix(strings.ToLower(call.name), "::__construct") {
		if _, _, declared := unit.resolveMethod(call.class, "__construct"); !declared {
			if engine, supplied := unit.engineMethod(call.class, "__construct"); supplied {
				return engine.GetArgs(), int(engine.GetRequiredArgs()), true
			}
			// A class with no constructor at all takes no arguments, so every
			// name given to `new` is an unknown one. That is an empty parameter
			// list, not a missing one.
			return nil, 0, true
		}
	}
	return nil, 0, false
}

// parameterName is the declared name of the nth parameter of the call being
// assembled, for a diagnostic that has to quote it.
func parameterName(in *Interp, unit *Unit, calls []*pendingCall, number uint32) string {
	if len(calls) == 0 || number == 0 {
		return ""
	}
	parameters, _, known := in.calleeParameters(unit, calls[len(calls)-1])
	if !known {
		return ""
	}
	index := int(number) - 1
	if index < len(parameters) {
		return parameters[index].GetName()
	}
	if len(parameters) > 0 && parameters[len(parameters)-1].GetVariadic() {
		return parameters[len(parameters)-1].GetName()
	}
	return ""
}

// runDestructors is the end of the request. PHP tears the symbol table down in
// reverse insertion order first -- so a global declared later is destroyed
// first -- and then destroys whatever objects are still in the store, in
// creation order. Without refcounts php2go cannot fire a destructor the moment
// its last holder goes away, so an object that PHP destroyed mid-script is
// destroyed here instead: late rather than never.
func (in *Interp) runDestructors(ctx *php.Ctx, unit *Unit) {
	var held []*php.Object
	ctx.Globals.Each(func(_ php.Key, value php.Value) bool {
		held = append(held, value.Deref().Object())
		return true
	})
	// A global is not the only thing that can be holding an object. One a
	// static property or a function static still holds outlives the symbol
	// table, so it is destroyed with the store below rather than here -- which
	// is the difference between a singleton dying in the order its names were
	// declared and dying in the order it was created.
	stored := unit.storedMarker(ctx)
	for i := len(held) - 1; i >= 0; i-- {
		if held[i] != nil && stored.seen[held[i]] {
			continue
		}
		in.destruct(ctx, unit, held[i])
	}
	// The store is walked from the front, and a destructor may create more
	// objects, so the length is re-read rather than captured.
	for i := 0; i < len(unit.live); i++ {
		in.destruct(ctx, unit, unit.live[i])
	}
}

// A lazy object never runs its own destructor. An uninitialized one has no
// state to tear down and its initializer is never called for the purpose; an
// initialized *proxy* still has Lazy set, and it is the instance behind it --
// a separate object -- whose destructor runs. An initialized ghost has dropped
// Lazy by then, so it destructs like any other object.
//
// destruct runs one object's __destruct, once. A destructor that throws during
// shutdown is reported the way any uncaught exception is.
// destructAt runs a destructor at a point in the program where an exception it
// throws has somewhere to go: the enclosing try. Shutdown has no such place,
// which is why destruct swallows it into a fatal instead.
func (in *Interp) destructAt(ctx *php.Ctx, unit *Unit, object *php.Object) error {
	if object == nil || unit.destructed[object] || object.Lazy != nil {
		return nil
	}
	unit.markDestructed(object)
	class, ok := unit.findClass(object.ClassName)
	if !ok {
		return nil
	}
	body, declaring, found := unit.resolveMethod(class, "__destruct")
	if !found || body == nil || body.GetAbstract() {
		return nil
	}
	_ = in.noteDeprecatedCall(ctx, unit, body, declaring, 0)
	// The destructor is a frame of its own in a backtrace, attributed to
	// wherever the program was when the object stopped being reachable.
	file, line := ctx.Location()
	ctx.PushCall(php.StackFrame{
		Function: php.DisplayName(class.Name) + "->__destruct",
		File:     file, Line: line, This: object,
	})
	_, err := in.invoke(ctx, unit, body, object, declaring, class, nil, 0)
	ctx.PopCall()
	return err
}

func (in *Interp) destruct(ctx *php.Ctx, unit *Unit, object *php.Object) {
	if object == nil || unit.destructed[object] || object.Lazy != nil {
		return
	}
	unit.markDestructed(object)
	class, ok := unit.findClass(object.ClassName)
	if !ok {
		return
	}
	body, declaring, found := unit.resolveMethod(class, "__destruct")
	if !found || body == nil || body.GetAbstract() {
		return
	}
	// A destructor is a call like any other, so #[\Deprecated] on one is
	// reported when the engine reaches it.
	_ = in.noteDeprecatedCall(ctx, unit, body, declaring, 0)
	// This is the end-of-request sweep: the engine is doing the calling and
	// has no script location to attribute it to, so a trace taken inside the
	// destructor shows it as an internal frame. destructAt is the other case,
	// where the object died at a point in the program.
	ctx.PushCall(php.StackFrame{
		Function: php.DisplayName(class.Name) + "->__destruct",
		File:     php.InternalFrameFile, This: object,
	})
	defer ctx.PopCall()
	if _, err := in.invoke(ctx, unit, body, object, declaring, class, nil, 0); err != nil {
		if escaped, isThrown := asThrown(err); isThrown {
			// A destructor's exception is an ordinary exception even here, at
			// the end of the request: the installed handler answers for it,
			// and only one nothing handles is fatal.
			if in.callExceptionHandler(ctx, unit, escaped) {
				return
			}
			in.settleMessages(ctx, unit, escaped)
			ctx.Write(escaped.fatalErrorText())
		}
	}
}

// markDestructed records that an object must not be destructed -- because it
// already has been, or because its constructor threw and so it was never fully
// built in the first place.
func (u *Unit) markDestructed(object *php.Object) {
	if object == nil {
		return
	}
	if u.destructed == nil {
		u.destructed = map[*php.Object]bool{}
	}
	u.destructed[object] = true
}

// allowDestruct releases an object whose constructor has returned, so the end
// of the request can destruct it.
func (u *Unit) allowDestruct(object *php.Object) {
	delete(u.destructed, object)
}

// parameterPosition is the 1-based slot a named argument fills, when the callee
// is known and declares that name. Unpacking needs it because a named argument
// does not merely sit beside the positional ones: it *occupies* a slot, and a
// positional argument that follows takes the next one after it. That is why
// `f(...['b' => 20], ...[7])` fills $b and then $c rather than $b and $a.
func parameterPosition(in *Interp, unit *Unit, calls []*pendingCall, name string) (int, bool) {
	if len(calls) == 0 {
		return 0, false
	}
	parameters, _, known := in.calleeParameters(unit, calls[len(calls)-1])
	if !known {
		return 0, false
	}
	for i, param := range parameters {
		if param.GetVariadic() {
			break
		}
		if param.GetName() == name {
			return i + 1, true
		}
	}
	return 0, false
}
