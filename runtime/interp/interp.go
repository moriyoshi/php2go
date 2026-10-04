// Package interp executes harvested IR.
//
// The interpreter is the correctness baseline for the whole pipeline: it always
// exists, always runs, and is the oracle a compiled function is checked
// against. It reads the serialized IR and depends on no PHP build, so it runs
// anywhere Go does.
package interp

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	pb "github.com/moriyoshi/php2go/internal/ir/irpb"
	"github.com/moriyoshi/php2go/runtime/php"
	"github.com/moriyoshi/php2go/runtime/stdlib"
)

// Operand kinds, matching the engine's IS_* constants.
const (
	opUnused = 0
	// arrayElementRef marks `[&$x]` -- ZEND_ARRAY_ELEMENT_REF in the opline's
	// extended_value.
	arrayElementRef = 1
	// liveLoop is ZEND_LIVE_LOOP, the live-range kind of a foreach's iterator
	// temporary. Leaving its range is what releases the iterator.
	liveLoop = 1
	// freeOnReturn marks a FREE or FE_FREE the compiler emitted for a `break`,
	// `continue` or `return` leaving a loop -- ZEND_FREE_ON_RETURN. It changes
	// where an exception out of that free is considered to have been thrown.
	freeOnReturn = 1

	opConst  = 1
	opTmpVar = 2
	opVar    = 4
	opCV     = 8
)

// Class references compiled into an UNUSED operand. The low bits hold the kind;
// the rest are flags such as ZEND_FETCH_CLASS_EXCEPTION, so the field is masked
// rather than compared whole.
const (
	fetchClassMask    = 0x03
	fetchClassDefault = 0
	fetchClassSelf    = 1
	fetchClassParent  = 2
	fetchClassStatic  = 3
)

// returnsFunction marks an ASSIGN_REF whose source is a call result rather than
// something the compiler could prove is a variable.
const returnsFunction = 1

// isEmptyCheck selects empty() rather than isset() in the opcode they share.
const isEmptyCheck = 1

// fetchForReference marks a write fetch whose result is about to be bound as a
// reference. It changes only what a refusal is called: a string offset cannot
// be an array, and cannot be referenced either, and PHP words the two
// differently.
const fetchForReference = 1

// A by-name variable fetch says in its extended_value which symbol table to
// look in. The global forms are what $GLOBALS["x"] compiles to; the local ones
// mean an ordinary variable-variable.
const (
	fetchTypeMask   = 0xe
	fetchGlobal     = 1 << 1
	fetchGlobalLock = 1 << 3
)

// unqualifiedInNamespace marks a constant written without a namespace inside
// one, which is the only form that falls back to the global constant. It rides
// in FETCH_CONSTANT's op1, whose type is UNUSED.
const unqualifiedInNamespace = 0x800

// bindByRef marks a `use (&$var)` capture, which shares the enclosing slot
// rather than copying it.
const bindByRef = 1

// extLastCatch marks the final catch clause of a try, after which an unmatched
// exception leaves the frame. It shares extended_value with a cache slot, so it
// is tested as a flag rather than compared.
const extLastCatch = 1

// Cast targets, using the engine's IS_* numbering. _IS_BOOL is a pseudo-type
// that sits outside the ordinary range.
const (
	castNull   = 1
	castInt    = 4
	castFloat  = 5
	castString = 6
	castArray  = 7
	castObject = 8
	castBool   = 18
)

// UnsupportedError names an opcode the interpreter does not implement yet,
// with the source line it came from. Failing loudly and specifically is the
// point: a silently skipped opcode would produce a wrong answer instead of a
// clear gap, and the set of these is the completeness metric.
type UnsupportedError struct {
	Opcode string
	Line   uint32
	File   string
}

func (e *UnsupportedError) Error() string {
	return fmt.Sprintf("%s:%d: unsupported opcode %s", e.File, e.Line, e.Opcode)
}

// Interp runs units harvested from one manifest.
type Interp struct {
	// opcodeNames resolves the numeric opcodes in a unit. It comes from the
	// manifest rather than being compiled in, because opcode numbers shift
	// between PHP releases while names are stable.
	opcodeNames []string

	// constants are what the engine registered before user code ran. Their
	// values are build-specific -- E_ALL has changed across releases -- so they
	// are harvested into the manifest rather than compiled in.
	constants map[string]php.Value
	// deprecatedConstants are the ones the engine has deprecated -- E_STRICT
	// since 8.4. Which they are is the build's answer, not this runtime's.
	deprecatedConstants map[string]bool

	// builtinClasses is the engine's class hierarchy, harvested rather than
	// transcribed: a catch clause naming RuntimeException has to know it
	// descends from Exception, and that shape is version-specific.
	builtinClasses map[string]*Class

	// frameless names the functions ZEND_FRAMELESS_ICALL_* calls, indexed by
	// the extended_value those opcodes carry.
	frameless []string

	// engineFunctions names every function the linked build provides. It is
	// what separates "this program called something that does not exist", which
	// is a PHP Error, from "this runtime has not implemented that builtin yet",
	// which is a gap the metric must keep counting.
	engineFunctions map[string]bool
	// scopedFunctions are generated Go functions made visible to a scoped
	// eval body. They are installed only by the embedding hybrid runtime.
	scopedFunctions  map[string]bool
	scopedSignatures map[string]ScopedFunctionSignature
	scopedDispatch   func(*php.Ctx, string, []php.Value) (php.Value, error)
	scopedUnit       *Unit
	// engineByRef records which parameters of those functions are taken by
	// reference, which decides whether an argument is fetched for reading or
	// for writing when the compiler could not tell.
	engineByRef map[string][]bool
	// engineSignatures are the harvested parameter lists, used to reject an
	// argument the linked build would reject. The declared types are the
	// engine's own rendering, so the TypeError wording comes from the harvest.
	engineSignatures map[string]*pb.Builtin

	// loader compiles the source that include, require and eval name. It is nil
	// unless the embedder supplied one.
	loader Loader

	// defaultErrorReporting is the mask a program starts with. It is an ini
	// default rather than E_ALL, so it is harvested like everything else that
	// varies by build.
	defaultErrorReporting int64

	// iniEntries and extensions are harvested engine registries. Their contents,
	// ownership and ordering vary with the linked build, and bundles must answer
	// ini_get_all() without carrying that engine at run time.
	iniEntries           []*pb.IniEntry
	iniByName            map[string]*pb.IniEntry
	extensions           map[string]bool
	iniRegistryAvailable bool

	// originalUmask is captured by the request's first umask call and restored
	// at teardown, matching ext/standard's request-shutdown behavior.
	originalUmask *int

	// temporaryStreams are tmpfile resources that must be closed and unlinked
	// at request teardown even when PHP code does not explicitly fclose them.
	temporaryStreams map[*stdlib.Stream]struct{}
	// processStreams own live popen children. Request teardown must pclose them
	// so write pipes receive EOF and every child is reaped.
	processStreams map[*stdlib.Stream]struct{}

	// persistentSockets belongs to the embedding application, while leases are
	// request-local. Ordinary interp.New callers receive a private pool.
	persistentSockets      *stdlib.PersistentSocketPool
	persistentSocketLeases map[string]stdlib.PersistentSocketLease

	// assertions is zend.assertions for the build the IR came from, which is
	// what ZEND_ASSERT_CHECK branches on. Harvested rather than assumed: a
	// build with assertions off produces the same opcode and must skip it.
	assertions int64
}

func New(manifest *pb.Manifest) *Interp {
	in := &Interp{
		opcodeNames:            manifest.GetOpcodeNames(),
		constants:              map[string]php.Value{},
		defaultErrorReporting:  manifest.GetDefaultErrorReporting(),
		assertions:             manifest.GetAssertions(),
		persistentSockets:      stdlib.NewPersistentSocketPool(),
		persistentSocketLeases: map[string]stdlib.PersistentSocketLease{},
		iniEntries:             manifest.GetIniEntries(),
		iniByName:              map[string]*pb.IniEntry{},
		extensions:             map[string]bool{},
		iniRegistryAvailable:   manifest.IniEntries != nil || manifest.Extensions != nil,
	}
	for _, entry := range in.iniEntries {
		in.iniByName[entry.GetName()] = entry
	}
	for _, extension := range manifest.GetExtensions() {
		in.extensions[extension] = true
	}
	if in.defaultErrorReporting == 0 {
		// A manifest harvested before this field existed reports nothing rather
		// than "report nothing"; every level stays on.
		in.defaultErrorReporting = -1
	}
	in.deprecatedConstants = map[string]bool{}
	for _, constant := range manifest.GetConstants() {
		in.constants[constant.GetName()] = literalValue(constant.GetValue())
		if constant.GetDeprecated() {
			in.deprecatedConstants[constant.GetName()] = true
		}
	}
	// A manifest harvested without constants still has to resolve the handful
	// the language itself guarantees.
	for name, value := range fallbackConstants {
		if _, ok := in.constants[name]; !ok {
			in.constants[name] = value
		}
	}
	in.frameless = manifest.GetFramelessFunctions()
	in.engineFunctions = map[string]bool{}
	in.engineByRef = map[string][]bool{}
	in.engineSignatures = map[string]*pb.Builtin{}
	for _, builtin := range manifest.GetBuiltins() {
		lower := strings.ToLower(builtin.GetName())
		in.engineFunctions[lower] = true
		byRef := make([]bool, 0, len(builtin.GetArgs()))
		for _, arg := range builtin.GetArgs() {
			byRef = append(byRef, arg.GetByRef())
		}
		in.engineByRef[lower] = byRef
		in.engineSignatures[lower] = builtin
	}
	in.builtinClasses = map[string]*Class{}
	for _, decl := range manifest.GetBuiltinClasses() {
		in.builtinClasses[strings.ToLower(decl.GetName())] = &Class{
			Name:    decl.GetName(),
			Parent:  decl.GetParent(),
			decl:    decl,
			methods: map[string]*pb.OpArray{},
		}
	}
	// stdClass is guaranteed by the language, so a manifest harvested without
	// the class table still has to provide it.
	if _, ok := in.builtinClasses["stdclass"]; !ok {
		in.builtinClasses["stdclass"] = &Class{
			Name: "stdClass", decl: &pb.Class{Name: "stdClass"},
			methods: map[string]*pb.OpArray{},
		}
	}
	return in
}

// SetPersistentSocketPool installs the embedding application's connection
// pool. It must be called before the request starts.
func (in *Interp) SetPersistentSocketPool(pool *stdlib.PersistentSocketPool) {
	if pool == nil {
		pool = stdlib.NewPersistentSocketPool()
	}
	in.persistentSockets = pool
	in.persistentSocketLeases = map[string]stdlib.PersistentSocketLease{}
}

// ReleasePersistentSockets returns this request's live leases to the
// application pool. Explicitly closed streams have already evicted themselves.
func (in *Interp) ReleasePersistentSockets() {
	for _, lease := range in.persistentSocketLeases {
		in.persistentSockets.Release(lease.Entry)
	}
	in.persistentSocketLeases = map[string]stdlib.PersistentSocketLease{}
}

// SetDefaultErrorReporting overrides the mask a program starts with. A .phpt's
// --INI-- section can set error_reporting, and the expected output was recorded
// under it: a run that disregarded the setting would be compared against output
// produced with a different one.
func (in *Interp) SetDefaultErrorReporting(mask int64) { in.defaultErrorReporting = mask }

// DefaultErrorReporting is the mask in force, so a caller that overrode it can
// put the previous one back.
func (in *Interp) DefaultErrorReporting() int64 { return in.defaultErrorReporting }

// lookupConstant resolves a name against script-defined constants first, then
// the engine's. Constant names are case-sensitive in PHP, unlike function and
// class names.
func (in *Interp) lookupConstant(ctx *php.Ctx, name string) (php.Value, bool) {
	// A fully qualified name may carry a leading separator; the tables are
	// keyed without one.
	name = strings.TrimPrefix(name, "\\")
	if value, ok := ctx.Constant(name); ok {
		return value, true
	}
	if value, ok := in.constants[name]; ok {
		return value, true
	}
	if value, ok := stdlib.StandardStream(ctx, name); ok {
		return value, true
	}
	// true, false and null are the only names the parser folds case-insensitively.
	if value, ok := fallbackConstants[strings.ToUpper(name)]; ok && len(name) <= 5 {
		return value, true
	}
	return php.Undef(), false
}

func (in *Interp) name(opcode uint32) string {
	if int(opcode) < len(in.opcodeNames) {
		return in.opcodeNames[opcode]
	}
	return fmt.Sprintf("OPCODE_%d", opcode)
}

// frame is one op_array activation: compiled variables and temporaries share a
// single slot space, numbered as the engine numbers them.
type frame struct {
	op    *pb.OpArray
	slots []php.Value
	// forceClosing marks a generator body that is being unwound because the
	// generator was abandoned. Its finally blocks run, but they may not
	// suspend: there is nobody left to resume them.
	forceClosing bool
	// escaped are the reference boxes a by-reference yield handed out of this
	// frame. They are still the frame's slots, but they are no longer only its,
	// so the frame ending does not unshare them.
	escaped []*php.Ref
	// preserveCVRefs transfers CV references back to the caller-owned slot map
	// when a scoped eval or include finishes.
	preserveCVRefs bool
	// this is bound for a method call and is what FETCH_THIS reads.
	this *php.Object
	// class is the class whose body is executing. A static call binds no $this,
	// so this is what self:: and parent:: resolve against.
	class *Class
	// called is the class the call site named, which is what static:: means.
	// It differs from class whenever an inherited static method is reached
	// through a subclass -- late static binding, which self:: does not do.
	called *Class
	// extraNamed are named arguments with no matching parameter, which only a
	// variadic can take.
	extraNamed []namedArgument
	// callerStrict is `declare(strict_types=1)` in the file the call was made
	// from, which is what decides whether this frame's arguments are coerced.
	callerStrict bool
	// callerInternal marks a frame whose caller is the engine rather than the
	// script -- reached through the `Closure::__invoke` trampoline. There is
	// then no call site for a diagnostic to name.
	callerInternal bool
	// literals are the array literals this frame is still assembling, each
	// with the temporary it lives in. A literal lives only in a temporary
	// until something consumes it, and temporaries are not roots -- so
	// without this the elements of `[new A(1), new A(2)]` are collected while
	// A(2)'s constructor runs. An entry is dropped when an opcode *reads* that
	// temporary, which is exactly where the literal has gone somewhere else.
	literals []arrayLiteral
	// silences are the error_reporting masks the `@` operators open in this
	// frame have to hand back. END_SILENCE pops one; an exception escaping the
	// expression pops all of them, since the catch that receives it is
	// necessarily outside every `@` region here.
	silences []int64
	// unwinding is the stack of exceptions in flight. A stack rather than one
	// slot because a try/catch inside a finally raises and handles its own
	// exception while an outer one is still on its way out; a single slot let
	// the inner throw overwrite the outer and the finally re-entered forever.
	unwinding []unwind
	// dropped records that a jump to a handler left temporaries behind, so the
	// loop knows to ask whether anything they held is now unreachable.
	dropped bool
	// names is the interpreter's opcode table, which the live-range cleanup
	// needs to tell a loop's own free from any other opline.
	names []string
	// writeTargets records, per temporary, the container slot a write fetch
	// handed out a box for. `$a[0] = &$x` needs it: the rebinding has to reach
	// the element rather than the temporary.
	writeTargets map[uint32]writeTarget
	// extra is the overflow symbol table: variables that exist only under a
	// name computed at run time. The compiler gives a slot to every variable
	// the body mentions literally, so a name that arrives from $$n or from
	// extract() has none -- and without somewhere to put it the write would be
	// lost. Ordinary variables never come through here.
	extra map[string]*php.Ref
	// bound are a closure's captured variables, which ZEND_BIND_STATIC pulls
	// into compiled variables at the top of the body.
	bound map[string]php.Value
	// staticSlots maps a compiled variable to the static it stands for, so the
	// value can be written back when the call ends.
	staticSlots map[uint32]string
	// hook identifies the property whose hook this frame is running, if any. An
	// access to that property on this frame's $this reaches the backing store
	// rather than calling the hook again.
	hook hookIdentity

	// ctx is where a diagnostic raised while reading an operand goes. Reading an
	// undefined variable warns in PHP, and only the frame knows the name.
	ctx *php.Ctx

	// pc and calls are saved when a generator frame suspends, so it resumes
	// exactly where it stopped rather than restarting.
	pc    int
	calls []*pendingCall
	// sharedScope marks a frame whose variables are the caller's -- an include
	// or an eval. A name unset there is unset in the caller, since it is one
	// symbol table and not two.
	sharedScope bool
	// global marks the program's top level, whose variables *are* the global
	// symbol table: get_defined_vars() there answers with the whole table,
	// superglobals first, and not only this op_array's compiled variables.
	global bool
	// unsetNames are the names such a frame removed, for the caller to apply.
	unsetNames []string
	// borrowedRefs names the temporaries holding a reference the engine made
	// over a value nothing else names -- a function's by-value result. Reaching
	// *through* one is what earns the "non referenceable value" notice.
	borrowedRefs map[uint32]bool
	// callsRef points at the live call stack while this frame is executing.
	// See execFrame: `calls` is a local, and `calls` above is only written back
	// when a generator suspends.
	callsRef *[]*pendingCall
	// yieldKey and yieldValue hold what the last yield produced, read by
	// whatever is driving the generator.
	yieldKey   php.Value
	yieldValue php.Value
	// autoKey is the counter behind a keyless `yield $v`, which numbers its
	// results from zero like an array append.
	autoKey int64
	// delegate is the source a `yield from` is currently draining.
	delegate *iterator
	// iterators holds foreach state, keyed by the slot FE_RESET_R wrote. The
	// engine stores a real iterator in that slot; here the slot holds the array
	// and the position is tracked alongside.
	iterators map[uint32]*iterator
	// funcArg is the argument position the next FETCH_*_FUNC_ARG is filling,
	// which ZEND_CHECK_FUNC_ARG announces just before it. The fetch needs it to
	// know whether the callee wants a reference.
	funcArg uint32
	// args are the values passed by the caller. Parameters are bound by the
	// callee's own RECV opcodes rather than pushed in from outside, which is how
	// the engine does it and is what makes defaults work.
	args []php.Value
}

// Run executes a standalone op_array with no surrounding program. Calls into
// user functions are unavailable; use RunUnit for that.
func (in *Interp) Run(ctx *php.Ctx, op *pb.OpArray) (php.Value, error) {
	defer in.restoreProcessUmask()
	defer in.cleanupTemporaryStreams()
	defer in.cleanupProcessStreams()
	unit := &Unit{
		file: op, functions: map[string]*pb.OpArray{},
		classes: map[string]*Class{}, builtins: in.builtinClasses,
		iniEntries: in.iniEntries, iniByName: in.iniByName, extensions: in.extensions,
		iniRegistryAvailable: in.iniRegistryAvailable,
	}
	defer cleanupSyslog(unit)
	return in.exec(ctx, unit, op, nil, 0)
}

func (in *Interp) exec(ctx *php.Ctx, unit *Unit, op *pb.OpArray, args []php.Value, depth int) (php.Value, error) {
	return in.execMethod(ctx, unit, op, nil, nil, nil, args, depth)
}

func (in *Interp) execMethod(ctx *php.Ctx, unit *Unit, op *pb.OpArray, this *php.Object, class, called *Class, args []php.Value, depth int) (php.Value, error) {
	return in.run(ctx, unit, op, this, class, called, nil, args, depth)
}

// execClosure runs a closure body with its captured variables available.
func (in *Interp) execClosure(ctx *php.Ctx, unit *Unit, fn *closure, args []php.Value, depth int) (php.Value, error) {
	called := fn.called
	if called == nil {
		called = fn.class
	}
	return in.run(ctx, unit, fn.op, fn.this, fn.class, called, fn.bound, args, depth)
}

// runGlobal executes the top level of a program, whose variables are the global
// scope itself. Each compiled variable is bound to the matching entry in the
// context's globals, so `global $x` inside a function reaches the same slot.
func (in *Interp) runGlobal(ctx *php.Ctx, unit *Unit, op *pb.OpArray) (php.Value, error) {
	f := &frame{
		op:     op,
		slots:  make([]php.Value, len(op.GetVarNames())+int(op.GetNumTemps())+1),
		names:  in.opcodeNames,
		global: true,
	}
	for i, name := range op.GetVarNames() {
		if i < len(f.slots) {
			f.slots[i] = sharedRef(ctx.Globals.RefAt(php.StringKey(name)))
		}
	}
	return in.execFrame(ctx, unit, f, 0)
}

func (in *Interp) run(ctx *php.Ctx, unit *Unit, op *pb.OpArray, this *php.Object, class, called *Class, bound map[string]php.Value, args []php.Value, depth int) (php.Value, error) {
	return in.runFrame(ctx, unit, op, this, class, called, bound, args, hookIdentity{}, depth)
}

// runHook is run for a property hook body, which differs only in that the frame
// remembers which property it belongs to.
func (in *Interp) runHook(ctx *php.Ctx, unit *Unit, hook hookIdentity, op *pb.OpArray,
	this *php.Object, class, called *Class, args []php.Value, depth int) (php.Value, error) {
	if op == nil {
		return php.Undef(), nil
	}
	// A hook is a frame in a backtrace, named for the property rather than for
	// a method: `H->$p::set(2)`. The op_array carries `$p::set` as its name, and
	// the class is the one that declared the property.
	if class != nil {
		site, line := ctx.Location()
		// `#[\SensitiveParameter]` on a set hook's parameter hides the value
		// the same way it does on a method's, so the marks travel with the
		// frame rather than the value being replaced.
		var sensitive []bool
		for i := range args {
			if i >= len(op.GetArgs()) || !declaresSensitive(op.GetArgs()[i]) {
				continue
			}
			for len(sensitive) <= i {
				sensitive = append(sensitive, false)
			}
			sensitive[i] = true
		}
		ctx.PushCall(php.StackFrame{
			Function: php.DisplayName(class.Name) + "->" + op.GetName(),
			File:     site, Line: line, Args: args, This: this,
			Sensitive: sensitive,
		})
		defer ctx.PopCall()
	}
	return in.runFrame(ctx, unit, op, this, class, called, nil, args, hook, depth)
}

func (in *Interp) runFrame(ctx *php.Ctx, unit *Unit, op *pb.OpArray, this *php.Object, class, called *Class, bound map[string]php.Value, args []php.Value, hook hookIdentity, depth int) (php.Value, error) {
	f := &frame{
		op:             op,
		slots:          make([]php.Value, len(op.GetVarNames())+int(op.GetNumTemps())+1),
		hook:           hook,
		names:          in.opcodeNames,
		extraNamed:     unit.pending.extraNamed,
		callerStrict:   unit.pending.strict,
		callerInternal: unit.pending.viaTrampoline,
	}
	unit.pending = pendingFrame{}
	f.args = args
	f.this = this
	f.class = class
	f.called = called
	if f.called == nil {
		// Nothing named a different class, so static:: and self:: agree.
		f.called = class
	}
	f.bound = bound
	in.bindRegisteredSuperglobals(ctx, f)

	value, err := in.execFrame(ctx, unit, f, depth)
	if err == nil {
		// The frame's locals are gone, so anything they were the last name for
		// is collectable now rather than at the end of the request. A
		// destructor that throws here throws out of the call that just
		// returned, which is where PHP puts it too.
		//
		// What the call returned is held aside: it is live, but until the
		// caller stores it no slot names it and a walk would not find it.
		err = in.materialize(ctx, unit, in.releaseCheckKeeping(ctx, unit, value))
	}
	// A generator function does not run when it is called: the first opline
	// asks for the frame to be wrapped in a Generator instead, and the body
	// only advances when something pulls on it.
	if errors.Is(err, errBecomeGenerator) {
		return php.Obj(unit.newGenerator(f, depth)), nil
	}
	return value, err
}

// bindRegisteredSuperglobals aliases every compiled variable whose name an
// extension registered to the request's global slot. The linked compiler
// cannot know names introduced by bundled PHP extensions, so it emits an
// ordinary CV; rebinding that CV supplies the auto-global behavior at runtime.
func (in *Interp) bindRegisteredSuperglobals(ctx *php.Ctx, f *frame) {
	ctx.EachSuperglobal(func(name string) bool {
		if slot, ok := f.slotNamed(name); ok {
			php.Unshare(f.slots[slot])
			f.bindSlot(opCV, slot, sharedRef(ctx.Globals.RefAt(php.StringKey(name))))
		}
		return true
	})
}

// execFrame runs a frame from wherever it left off. A generator frame suspends
// at a yield and is resumed later, so the program counter and the pending-call
// stack live on the frame rather than in this function.
func (in *Interp) execFrame(ctx *php.Ctx, unit *Unit, f *frame, depth int) (php.Value, error) {
	// The executing class travels with the frame, so anything that has to
	// decide visibility -- including a builtin, which has no frame of its own
	// -- can ask the context for it.
	previousScope := ctx.EnterScope(frameScope(f))
	defer ctx.EnterScope(previousScope)
	// `static::` is the *called* class, which is not the scope whenever an
	// inherited method is reached through a subclass. A builtin has no frame,
	// so it asks the context for this too.
	previousCalled := ctx.EnterCalledScope(frameCalledScope(f))
	defer ctx.EnterCalledScope(previousCalled)
	// The frame stack is what the functions that read the caller's variables --
	// extract, compact, get_defined_vars -- consult. A builtin gets no frame of
	// its own, so while one runs the top of this stack is its caller.
	unit.frames = append(unit.frames, f)
	defer func() {
		unit.frames = unit.frames[:len(unit.frames)-1]
		// The frame's slots go with it, so any reference the program bound into
		// one has lost a holder. That is what keeps var_dump from marking a
		// reference whose only other name was a local of a call that returned.
		// A slot a by-reference yield handed out is the exception: it left the
		// frame, so the frame ending is not it losing a holder.
		for i, slot := range f.slots {
			if f.escapedRef(slot) {
				continue
			}
			if f.preserveCVRefs && i < len(f.op.GetVarNames()) {
				if _, ok := slot.RefOf(); ok {
					continue
				}
			}
			php.Unshare(slot)
		}
	}()
	op := f.op
	f.ctx = ctx
	// A static variable is a reference into a table that outlives the call, so
	// what the body writes has to reach it. Syncing on the way out covers every
	// exit -- return, fall-through, suspension, or a thrown exception.
	defer unit.syncStatics(op, f)
	oplines := op.GetOplines()

	// Calls are built up across several oplines: INIT_FCALL names the callee,
	// SEND_* pushes each argument, DO_FCALL performs it. Nested calls make this
	// a stack rather than a single slot.
	calls := f.calls
	// The stack is a local so that the hot path is a plain slice, but a
	// collection can happen in the middle of assembling a call -- and an object
	// under construction is held by nothing else, since the temporary it sits
	// in is not a root. So the frame keeps a pointer to it for the walk.
	f.callsRef = &calls
	defer func() { f.callsRef = nil }()

	steps := 0
	for pc := f.pc; pc < len(oplines); {
		steps++
		if steps > maxSteps {
			return php.Undef(), fmt.Errorf("%s: %s exceeded %d steps, which means a loop the "+
				"interpreter cannot leave rather than a slow program",
				op.GetFilename(), op.GetName(), maxSteps)
		}
		if f.dropped {
			f.dropped = false
			// A jump to a handler let go of the temporaries it left behind.
			// Whatever they were the last name for is collectable here, which
			// is where PHP would have freed it -- and a destructor or a
			// generator's finally that throws throws from here.
			if released := in.collectBudgeted(ctx, unit); released != nil {
				released = in.materialize(ctx, unit, released)
				if target, caught := f.raise(released, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), released
			}
		}
		// A diagnostic can reach a user error handler, and that handler can
		// throw. The throw is parked on the context because a diagnostic is
		// raised from places that cannot unwind, so it is picked up here: the
		// opcode that raised it completes, and the exception lands before the
		// next one, which is the closest the loop can get to PHP's timing.
		if pending := ctx.TakePendingError(); pending != nil {
			if target, caught := f.raise(pending, pc); caught {
				pc = target
				continue
			}
			return php.Undef(), pending
		}
		line := oplines[pc]
		// Diagnostics name the place they came from, and only the loop knows
		// where that is. Two assignments per instruction is cheap next to the
		// dispatch that follows.
		ctx.SetLocation(op.GetFilename(), line.GetLine())
		name := in.name(line.GetOpcode())
		f.consumeLiterals(line)
		if pc+1 < len(oplines) && in.name(oplines[pc+1].GetOpcode()) == "ZEND_OP_DATA" {
			// The value half of a split opcode lives in the OP_DATA line, and
			// that is where a literal being assigned into an element or a
			// property is read from.
			f.consumeLiterals(oplines[pc+1])
		}

		switch name {
		case "ZEND_NOP", "ZEND_EXT_STMT":
			pc++

		case "ZEND_TICKS":
			if err := in.runTick(ctx, unit, line.GetExtendedValue(), op.GetFilename(), line.GetLine(), depth); err != nil {
				err = in.materialize(ctx, unit, err)
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			pc++

		case "ZEND_ECHO":
			text, err := in.toString(ctx, unit, f.fetch(line.GetOp1Type(), line.GetOp1()))
			if err != nil {
				err = in.materialize(ctx, unit, err)
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			ctx.Write(text.String())
			pc++

		case "ZEND_ASSIGN":
			// Copy, not alias: PHP arrays are values.
			value := f.fetch(line.GetOp2Type(), line.GetOp2()).Copy()
			if ref := f.typedRef(line.GetOp1Type(), line.GetOp1()); ref != nil {
				converted, err := in.verifyRefWrite(ctx, unit, ref, value, op.GetStrictTypes())
				if err != nil {
					if target, caught := f.raise(err, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), err
				}
				value = converted
			}
			// What the target held is about to be dropped, and if that was the
			// last name holding an object its destructor runs here.
			replaced := f.fetchQuiet(line.GetOp1Type(), line.GetOp1())
			f.store(line.GetOp1Type(), line.GetOp1(), value)
			// Assignment is an expression: its value is the result when used.
			f.storeResult(line, value)
			if lazyErr := in.releaseCheck(ctx, unit, replaced); lazyErr != nil {
				lazyErr = in.materialize(ctx, unit, lazyErr)
				if target, caught := f.raise(lazyErr, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), lazyErr
			}
			pc++

		case "ZEND_QM_ASSIGN":
			f.storeResult(line, f.fetch(line.GetOp1Type(), line.GetOp1()))
			pc++

		case "ZEND_ADD", "ZEND_SUB", "ZEND_MUL", "ZEND_CONCAT", "ZEND_FAST_CONCAT",
			"ZEND_POW", "ZEND_BW_AND", "ZEND_BW_OR", "ZEND_BW_XOR":
			a := f.fetch(line.GetOp1Type(), line.GetOp1())
			b := f.fetch(line.GetOp2Type(), line.GetOp2())
			if name == "ZEND_CONCAT" || name == "ZEND_FAST_CONCAT" {
				// Both operands become strings first, which for an object
				// means __toString and for a class without one an Error.
				rendered, err := in.toString(ctx, unit, a)
				if err == nil {
					a = rendered
					rendered, err = in.toString(ctx, unit, b)
					b = rendered
				}
				if err != nil {
					err = in.materialize(ctx, unit, err)
					if target, caught := f.raise(err, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), err
				}
			}
			if err := in.checkOperands(ctx, unit, name, a, b); err != nil {
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			f.storeResult(line, arith(name, a, b))
			pc++

		case "ZEND_ROPE_INIT", "ZEND_ROPE_ADD", "ZEND_ROPE_END":
			// A rope is how the compiler builds an interpolated string of three
			// or more pieces. The engine spreads the partial result over several
			// consecutive temps to avoid reallocating; accumulating a Go string
			// in the result slot is observationally identical.
			var built string
			if name != "ZEND_ROPE_INIT" {
				built = f.fetch(line.GetOp1Type(), line.GetOp1()).String()
			}
			// An interpolated object renders through __toString like any other
			// string conversion, and one without it is an Error.
			piece, err := in.toString(ctx, unit, f.fetch(line.GetOp2Type(), line.GetOp2()))
			if err != nil {
				err = in.materialize(ctx, unit, err)
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			f.storeResult(line, php.String(built+piece.String()))
			pc++

		case "ZEND_FRAMELESS_ICALL_0", "ZEND_FRAMELESS_ICALL_1",
			"ZEND_FRAMELESS_ICALL_2", "ZEND_FRAMELESS_ICALL_3":
			// PHP 8.4 compiles some builtin calls into a form with no call
			// frame: the arguments are the operands, and extended_value indexes
			// the engine's table of eligible functions.
			index := int(line.GetExtendedValue())
			if index >= len(in.frameless) {
				return php.Undef(), &UnsupportedError{
					Opcode: "frameless call to function " + strconv.Itoa(index),
					Line:   line.GetLine(), File: op.GetFilename(),
				}
			}
			callee := in.frameless[index]
			var args []php.Value
			if line.GetOp1Type() != opUnused {
				args = append(args, f.fetch(line.GetOp1Type(), line.GetOp1()))
			}
			if line.GetOp2Type() != opUnused {
				args = append(args, f.fetch(line.GetOp2Type(), line.GetOp2()))
			}
			// The third argument of ICALL_3 rides in the following OP_DATA,
			// since an opline has only two operands.
			if name == "ZEND_FRAMELESS_ICALL_3" {
				if pc+1 >= len(oplines) || in.name(oplines[pc+1].GetOpcode()) != "ZEND_OP_DATA" {
					return php.Undef(), fmt.Errorf("%s:%d: frameless call without OP_DATA",
						op.GetFilename(), line.GetLine())
				}
				data := oplines[pc+1]
				args = append(args, f.fetch(data.GetOp1Type(), data.GetOp1()))
			}
			// A frameless call has no SEND opcodes, so the operands are
			// consumed here instead.
			released := f.releaseTemp(unit, line.GetOp1Type(), line.GetOp1())
			released = f.releaseTemp(unit, line.GetOp2Type(), line.GetOp2()) || released
			if name == "ZEND_FRAMELESS_ICALL_3" {
				data := oplines[pc+1]
				released = f.releaseTemp(unit, data.GetOp1Type(), data.GetOp1()) || released
			}
			// There is no INIT opcode to have recorded the call site's setting,
			// so the op_array's own is the one that applies -- which is the
			// same thing, since the call is written in this file.
			unit.callStrict = op.GetStrictTypes()
			unit.resultUnused = line.GetResultType() == opUnused
			returned, err := in.call(ctx, unit, callee, args, depth)
			if err != nil {
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			f.storeResult(line, returned)
			if released {
				if lazyErr := in.releaseCheckKeeping(ctx, unit, returned); lazyErr != nil {
					lazyErr = in.materialize(ctx, unit, lazyErr)
					if target, caught := f.raise(lazyErr, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), lazyErr
				}
			}
			if name == "ZEND_FRAMELESS_ICALL_3" {
				pc += 2
			} else {
				pc++
			}

		case "ZEND_CHECK_FUNC_ARG", "ZEND_CHECK_UNDEF_ARGS", "ZEND_SEPARATE":
			// Bookkeeping the engine needs for its own calling convention:
			// diagnosing unpacked undef holes and splitting a shared array
			// before a write, neither of which is observable here -- arguments
			// carry their own reference boxes and arrays are copied eagerly.
			//
			// CHECK_FUNC_ARG does matter: it names the argument position the
			// following FETCH_*_FUNC_ARG is filling, and that position is what
			// decides whether the fetch is a read or a write.
			if name == "ZEND_CHECK_FUNC_ARG" {
				f.funcArg = line.GetOp2()
				if line.GetOp2Type() == opConst {
					// A *named* argument: op2 holds the parameter's name, and
					// the fetch below needs the position it fills.
					// zend_get_arg_offset_by_name does the same lookup. A name
					// no parameter claims has no position at all, and the
					// engine treats it as by value unless the variadic that
					// will collect it wants slots.
					f.funcArg = 0
					named := f.fetch(line.GetOp2Type(), line.GetOp2()).String()
					if at, ok := parameterPosition(in, unit, calls, named); ok {
						f.funcArg = uint32(at)
					} else if variadicByRef(in, unit, calls) {
						f.funcArg = variadicPosition(in, unit, calls)
					}
				}
			}
			pc++

		case "ZEND_BEGIN_SILENCE":
			// `@expr` suppresses diagnostics for the duration. The previous
			// level goes in the result so END_SILENCE can restore it.
			saved := ctx.BeginSilence()
			f.silences = append(f.silences, saved)
			f.storeResult(line, php.Int(saved))
			pc++

		case "ZEND_END_SILENCE":
			ctx.EndSilence(f.fetch(line.GetOp1Type(), line.GetOp1()).Int())
			if n := len(f.silences); n > 0 {
				f.silences = f.silences[:n-1]
			}
			pc++

		case "ZEND_INCLUDE_OR_EVAL":
			// The path is rendered like anything else PHP needs as a string,
			// so an object goes through __toString -- and one that throws
			// there stops the include before it looks for a file.
			rendered, err := in.toString(ctx, unit, f.fetch(line.GetOp1Type(), line.GetOp1()))
			if err != nil {
				err = in.materialize(ctx, unit, err)
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			operand := rendered.String()
			value, err := in.includeOrEval(ctx, unit, f, operand, line.GetExtendedValue(), depth)
			if err = in.materialize(ctx, unit, err); err != nil {
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			f.storeResult(line, value)
			pc++

		case "ZEND_MATCH", "ZEND_SWITCH_LONG", "ZEND_SWITCH_STRING":
			// op2 is a constant table from case value to target; the harvester
			// has already turned its byte offsets into instruction indices.
			// extended_value is where an unmatched value goes.
			subject := f.fetch(line.GetOp1Type(), line.GetOp1())
			table := f.literal(line.GetOp2())
			target, matched := matchTarget(subject, table, name)
			if matched {
				pc = target
				continue
			}
			pc = int(line.GetExtendedValue())

		case "ZEND_MATCH_ERROR":
			subject := f.fetch(line.GetOp1Type(), line.GetOp1())
			err := in.throwError(ctx, unit, "UnhandledMatchError",
				"Unhandled match case %s", matchErrorSubject(subject))
			if target, caught := f.raise(err, pc); caught {
				pc = target
				continue
			}
			return php.Undef(), err

		case "ZEND_TYPE_CHECK":
			// is_int, is_string and the rest compile to one opcode with a mask
			// of the types that satisfy it.
			value := f.fetch(line.GetOp1Type(), line.GetOp1())
			matched := line.GetExtendedValue()&(1<<uint(value.Kind())) != 0
			if matched && value.Deref().Kind() == php.KindResource {
				// A closed handle keeps the resource *type* but no longer has
				// anything behind it, and is_resource answers about that.
				matched = !value.Deref().Resource().Closed()
			}
			f.storeResult(line, php.Bool(matched))
			pc++

		case "ZEND_BIND_GLOBAL":
			// `global $x` aliases the name in the global scope, so assignment
			// inside the function is visible outside it.
			name := f.fetch(line.GetOp2Type(), line.GetOp2()).String()
			global := ctx.Globals.RefAt(php.StringKey(name))
			if global.Get().IsUndef() {
				// Binding a name that no global has yet *creates* it, holding
				// null. Reading it afterwards is reading a variable that exists,
				// so there is no undefined-variable warning -- only isset() can
				// still tell the difference.
				global.Set(php.Null())
			}
			f.bindSlot(line.GetOp1Type(), line.GetOp1(), sharedRef(global))
			pc++

		case "ZEND_JMPZ_EX", "ZEND_JMPNZ_EX":
			// The short-circuit halves of && and ||, which also keep the value
			// as a boolean rather than only branching on it.
			value := f.fetch(line.GetOp1Type(), line.GetOp1()).Bool()
			f.storeResult(line, php.Bool(value))
			if (name == "ZEND_JMPZ_EX") == !value {
				pc = int(line.GetOp2())
				continue
			}
			pc++

		case "ZEND_RETURN_BY_REF":
			// A function declared `function &f()` returns a slot rather than a
			// value. Callers that do not bind the result by reference see no
			// difference, so the value is what comes back.
			if line.GetOp1Type() == opConst || line.GetOp1Type() == opTmpVar {
				// A literal or a computed temporary has no slot behind it, so
				// there is nothing for the caller's reference to point at. PHP
				// still wraps the value in a fresh reference, which is what
				// keeps the caller's `$x = &f()` from complaining in turn.
				ctx.Notice("Only variable references should be returned by reference")
				return php.MakeRef(php.NewRef(f.fetch(line.GetOp1Type(), line.GetOp1()))), nil
			}
			if line.GetOp1Type() == opVar && in.returnsCallResult(oplines, pc, line.GetOp1()) {
				// The VAR being returned is a *call's* result. A by-reference
				// callee left a reference there and it is handed on; a
				// by-value one left a plain value, which has no slot behind it
				// any more than a literal does, and earns the same notice.
				if _, isRef := f.rawSlot(line.GetOp1Type(), line.GetOp1()).RefOf(); !isRef {
					ctx.Notice("Only variable references should be returned by reference")
					return php.MakeRef(php.NewRef(
						f.fetch(line.GetOp1Type(), line.GetOp1()).Deref())), nil
				}
			}
			if ref, ok := f.slotRef(line.GetOp1Type(), line.GetOp1()); ok {
				// A box the return value carries out of a *temporary* was
				// never a binding the program made -- `return $this->prop`
				// reaches the property's slot and hands it straight back -- so
				// the frame ending is not a holder letting go of it. Without
				// this a `&__get` standing in for a property would cost that
				// property its `&`. A named variable is the other way round:
				// `$r = &$this->prop; return $r;` really did take a hold, and
				// the frame ending really does release it.
				if line.GetOp1Type() != opCV {
					f.escaped = append(f.escaped, ref)
				}
				return php.MakeRef(ref), nil
			}
			return f.fetch(line.GetOp1Type(), line.GetOp1()), nil

		case "ZEND_CALLABLE_CONVERT":
			// `$obj->m(...)` and `strlen(...)` make a Closure out of a callable
			// without calling it. The pending call already names the target.
			if len(calls) == 0 {
				return php.Undef(), fmt.Errorf("%s:%d: callable conversion outside a call",
					op.GetFilename(), line.GetLine())
			}
			pending := calls[len(calls)-1]
			calls = calls[:len(calls)-1]
			converted, ok := unit.closureFromCall(pending)
			if !ok {
				return php.Undef(), &UnsupportedError{
					Opcode: "first-class callable for " + pending.name,
					Line:   line.GetLine(), File: op.GetFilename(),
				}
			}
			f.storeResult(line, converted)
			pc++

		case "ZEND_COALESCE":
			// The `??` operator: jump past the alternative when the left side
			// exists and is not null. Unlike ||, an undefined variable is not a
			// diagnostic here.
			value := f.fetchQuiet(line.GetOp1Type(), line.GetOp1())
			if value.Kind() != php.KindNull && value.Kind() != php.KindUndef {
				f.storeResult(line, value)
				pc = int(line.GetOp2())
				continue
			}
			pc++

		case "ZEND_JMP_SET":
			// The `?:` operator: a truthy left side is the result and the
			// alternative is skipped. Unlike `??` this is a plain truth test,
			// so an undefined variable does warn.
			value := f.fetch(line.GetOp1Type(), line.GetOp1())
			if value.Bool() {
				f.storeResult(line, value)
				pc = int(line.GetOp2())
				continue
			}
			pc++

		case "ZEND_FETCH_GLOBALS":
			// $GLOBALS read as a whole. Since PHP 8.1 that is a snapshot rather
			// than a live view: writing through it does nothing, and the
			// compiler rewrites $GLOBALS["x"] into a plain global fetch instead
			// of coming through here.
			// A name bound but never assigned -- every compiled variable of
			// the top level is, from the start -- is not in the table yet.
			globals := php.NewArray()
			ctx.Globals.Each(func(k php.Key, v php.Value) bool {
				if privateGlobal(ctx, k) {
					return true
				}
				if !v.Deref().IsUndef() {
					globals.Set(k, v.Deref().Copy())
				}
				return true
			})
			f.storeResult(line, php.Arr(globals))
			pc++

		case "ZEND_COPY_TMP":
			// A temporary the compiler needs to survive past the opcode that
			// produced it, copied rather than aliased.
			f.storeResult(line, f.fetch(line.GetOp1Type(), line.GetOp1()).Copy())
			pc++

		case "ZEND_JMP_NULL":
			// The short-circuit behind `?->` and `?[`. What a skipped chain
			// yields depends on what it was part of, and the opcode being
			// jumped over says which: an isset()/empty() test answers false or
			// true, a plain fetch yields null, and the quiet fetch behind `??`
			// yields null. The two quiet forms do not report the subject
			// either -- `isset($undefined?->x)` is silent where `$undefined?->x`
			// on its own warns.
			skipped, quiet := php.Null(), false
			// The *last* opcode being jumped over is the one that decides: a
			// chain of several links jumps over all of them, and only the final
			// one says what the whole chain was for.
			if end := int(line.GetOp2()) - 1; end > pc && end < len(oplines) {
				next := oplines[end]
				switch in.name(next.GetOpcode()) {
				case "ZEND_ISSET_ISEMPTY_PROP_OBJ", "ZEND_ISSET_ISEMPTY_DIM_OBJ":
					skipped = php.Bool(next.GetExtendedValue()&isEmptyCheck != 0)
					quiet = true
				case "ZEND_FETCH_OBJ_IS", "ZEND_FETCH_DIM_IS":
					quiet = true
				}
			}
			subject := php.Undef()
			if quiet {
				subject = f.fetchQuiet(line.GetOp1Type(), line.GetOp1())
			} else {
				subject = f.fetch(line.GetOp1Type(), line.GetOp1())
			}
			if subject.Deref().Kind() == php.KindNull || subject.Deref().Kind() == php.KindUndef {
				f.storeResult(line, skipped)
				pc = int(line.GetOp2())
				continue
			}
			pc++

		case "ZEND_FETCH_CLASS":
			// The name is in op2; when that is UNUSED the self/parent/static
			// code is in op1 instead, which is the reverse of where the static
			// property opcodes keep it.
			kind, operand := line.GetOp2Type(), line.GetOp2()
			if kind == opUnused {
				operand = line.GetOp1()
			}
			className, err := in.classNameOperand(ctx, unit, f, line, kind, operand)
			if err != nil {
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			f.storeResult(line, php.String(className))
			pc++

		case "ZEND_FETCH_CLASS_NAME":
			// `X::class` is the one place the engine words a missing class
			// scope as a refusal to *use* the keyword rather than to access
			// through it. Probed: every other `self::`, `parent::` and
			// `static::` says "Cannot access ... when no class scope is
			// active", and only `::class` says this.
			if f.class == nil && line.GetOp1Type() == opUnused {
				if _, hasCalled := f.calledClass(); !hasCalled {
					err := in.throwError(ctx, unit, "Error",
						"Cannot use %q in the global scope", classKeyword(line.GetOp1()))
					if target, caught := f.raise(err, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), err
				}
			}
			// `$v::class` takes an **object** and nothing else -- not even a
			// class-name string, which every other `$v::` form accepts. The
			// refusal is a TypeError naming what was there instead.
			if line.GetOp1Type() != opUnused {
				subject := f.fetch(line.GetOp1Type(), line.GetOp1()).Deref()
				if subject.Kind() != php.KindObject {
					err := in.throwError(ctx, unit, "TypeError",
						"Cannot use \"::class\" on %s", givenTypeName(subject))
					if target, caught := f.raise(err, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), err
				}
			}
			className, err := in.classNameOperand(ctx, unit, f, line, line.GetOp1Type(), line.GetOp1())
			if err != nil {
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			f.storeResult(line, php.String(className))
			pc++

		case "ZEND_FUNC_NUM_ARGS", "ZEND_FUNC_GET_ARGS":
			// Both report what was *passed*, not what was declared, so they
			// come off the frame rather than the signature -- and there is no
			// frame to ask at top level.
			if f.op.GetName() == "" {
				what := "func_num_args() must be called from a function context"
				if name == "ZEND_FUNC_GET_ARGS" {
					what = "func_get_args() cannot be called from the global scope"
				}
				err := in.throwError(ctx, unit, "Error", "%s", what)
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			if name == "ZEND_FUNC_NUM_ARGS" {
				f.storeResult(line, php.Int(int64(len(f.args))))
				pc++
				continue
			}
			passed := php.NewArray()
			for _, arg := range f.args {
				passed.Append(arg.Deref().Copy())
			}
			f.storeResult(line, php.Arr(passed))
			pc++

		case "ZEND_ASSERT_CHECK":
			// assert() is compiled in either way; this is the runtime guard.
			// With assertions off the whole expression is jumped over and the
			// result is true, which is what assert() returns when disabled.
			if in.assertions <= 0 {
				if line.GetResultType() != opUnused {
					f.storeResult(line, php.Bool(true))
				}
				pc = int(line.GetOp2())
				continue
			}
			pc++

		case "ZEND_ISSET_ISEMPTY_THIS":
			// isset($this) has its own opcode because $this is not a compiled
			// variable: it is a property of the frame.
			f.storeResult(line, php.Bool(f.this != nil))
			pc++

		case "ZEND_GET_CALLED_CLASS":
			// get_called_class() is late static binding asked for by name: the
			// class the call site used, not the one whose body is running.
			called, inClass := f.calledClass()
			if !inClass {
				err := in.throwError(ctx, unit, "Error",
					"get_called_class() must be called from within a class")
				if handler, caught := f.raise(err, pc); caught {
					pc = handler
					continue
				}
				return php.Undef(), err
			}
			f.storeResult(line, php.String(called))
			pc++

		case "ZEND_UNSET_CV_SILENT":
			f.bindSlot(line.GetOp1Type(), line.GetOp1(), php.Undef())
			pc++

		case "ZEND_UNSET_VAR":
			// The by-name form. op1 holds the *name*, not the variable:
			// unset($$n) and unset($GLOBALS["x"]) both compile to this, and
			// unsetting op1 itself would remove the name holder instead.
			name, slot, found, global := f.namedVariable(line)
			_ = slot
			_ = found
			switch {
			case global:
				unit.unbindGlobal(ctx, name)
			default:
				f.namedUnset(name)
			}
			pc++

		case "ZEND_UNSET_OBJ":
			// An unused op1 is $this. unset() does not read, so it is quiet.
			object, _ := f.objectOperand(line, true)
			if object != nil && object.Lazy != nil {
				// Removing a property is touching the state, so it is produced
				// first: there has to be something for unset() to remove.
				lazyName, lazyMagic := lazyProperty(f, line, name)
				resolved, lazyErr := in.resolveLazy(ctx, unit, object, lazyName, lazyMagic, depth)
				if lazyErr != nil {
					lazyErr = in.materialize(ctx, unit, lazyErr)
					if handler, caught := f.raise(lazyErr, pc); caught {
						pc = handler
						continue
					}
					return php.Undef(), lazyErr
				}
				object = resolved
			}
			if object != nil {
				property, nameErr := in.propertyName(ctx, unit, f, line.GetOp2Type(), line.GetOp2())
				if nameErr != nil {
					nameErr = in.materialize(ctx, unit, nameErr)
					if target, caught := f.raise(nameErr, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), nameErr
				}
				// Inside a class that declares its own private property of this
				// name, the name means that class's slot, which is not the one the
				// plain name holds when a subclass declares one too.
				property = propertySlot(object, property, f.class)
				if hooked, declaring, ok := unit.hooksFor(f, object, property); ok {
					// A hooked property cannot be unset at all: there is no
					// state to remove that the hooks would agree about. The
					// object's class is named, not the one that declared the
					// hooks -- a subclass that inherits them is what the
					// program was holding.
					_, _ = hooked, declaring
					err := in.throwError(ctx, unit, "Error", "Cannot unset hooked property %s::$%s",
						php.DisplayName(object.ClassName), property)
					if handler, caught := f.raise(err, pc); caught {
						pc = handler
						continue
					}
					return php.Undef(), err
				}
				verdict, err := in.checkPropertyUnset(ctx, unit, object, property, f.class)
				if err != nil {
					if handler, caught := f.raise(err, pc); caught {
						pc = handler
						continue
					}
					return php.Undef(), err
				}
				removed := verdict == accessAllowed
				if removed {
					// A declared property keeps its slot: var_dump still lists
					// it as uninitialized, and the read diagnostic still names
					// the declaration. Only a dynamic one disappears entirely.
					if declaration, declared := object.Declared[property]; declared &&
						!object.Dynamic[property] && declaration.Type != "" {
						object.Clear(property)
					} else {
						removed = object.Props.Unset(php.StringKey(property))
					}
				}
				if !removed {
					// __unset answers for a property that was not there.
					_, handled, err := in.callMagic(ctx, unit, object, magicUnset,
						property, []php.Value{php.String(property)}, depth)
					if err == nil && !handled && nulProperty(property) {
						// Nothing answered, and the table cannot hold this
						// name: reaching it is an error rather than a no-op.
						err = in.refuseNulProperty(ctx, unit)
					}
					if err != nil {
						if handler, caught := f.raise(err, pc); caught {
							pc = handler
							continue
						}
						return php.Undef(), err
					}
				}
			}
			pc++

		case "ZEND_UNSET_DIM":
			target := f.fetch(line.GetOp1Type(), line.GetOp1())
			if target.Kind() == php.KindArray {
				index := f.fetch(line.GetOp2Type(), line.GetOp2())
				if offending := illegalOffsetType(index); offending != "" {
					// unset() words this its own way: "unset offset" rather
					// than "access offset".
					err := in.throwError(ctx, unit, "TypeError",
						"Cannot unset offset of type %s on array", offending)
					if handler, caught := f.raise(err, pc); caught {
						pc = handler
						continue
					}
					return php.Undef(), err
				}
				key := php.ArrayKey(ctx, index)
				raw, _ := target.Array().Raw(key)
				released := raw.Deref()
				php.Unshare(raw)
				target.Array().Unset(key)
				if lazyErr := in.releaseCheck(ctx, unit, released); lazyErr != nil {
					lazyErr = in.materialize(ctx, unit, lazyErr)
					if handler, caught := f.raise(lazyErr, pc); caught {
						pc = handler
						continue
					}
					return php.Undef(), lazyErr
				}
			}
			if object := target.Deref().Object(); object != nil {
				index := f.fetch(line.GetOp2Type(), line.GetOp2())
				_, handled, err := in.offsetCall(ctx, unit, object, offsetUnset, []php.Value{index}, depth)
				if !handled && err == nil && !unit.implementsArrayAccess(object) {
					err = in.indexedObjectError(ctx, unit, object)
				}
				if err != nil {
					if target, caught := f.raise(err, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), err
				}
			}
			// Unsetting an offset of something that is not a container does
			// nothing, but the engine does not let every shape pass in silence.
			// Probed: null and undefined are silent, false earns the growing-an
			// -array deprecation even though nothing grows, a string has
			// offsets that simply cannot be unset, and every other scalar is an
			// outright Error.
			if err := in.refuseUnsetDim(ctx, unit, target.Deref()); err != nil {
				if handler, caught := f.raise(err, pc); caught {
					pc = handler
					continue
				}
				return php.Undef(), err
			}
			pc++

		case "ZEND_ISSET_ISEMPTY_CV", "ZEND_ISSET_ISEMPTY_VAR",
			"ZEND_ISSET_ISEMPTY_PROP_OBJ", "ZEND_ISSET_ISEMPTY_DIM_OBJ",
			"ZEND_ISSET_ISEMPTY_STATIC_PROP":
			// isset() and empty() share an opcode; a flag in extended_value
			// picks which. isset is "exists and is not null", empty is "missing
			// or falsy", so neither is the negation of the other.
			var value php.Value
			var present bool
			switch name {
			case "ZEND_ISSET_ISEMPTY_CV":
				value = f.fetchQuiet(line.GetOp1Type(), line.GetOp1())
				present = value.Kind() != php.KindUndef
			case "ZEND_ISSET_ISEMPTY_VAR":
				// The by-name form, where op1 holds the name: isset($$n).
				varName, slot, found, global := f.namedVariable(line)
				_, _ = slot, found
				switch {
				case global:
					value, present = ctx.Globals.Get(php.StringKey(varName))
					value = value.Deref()
				default:
					value, present = f.namedValue(varName)
					value = value.Deref()
				}
				present = present && value.Kind() != php.KindUndef
			case "ZEND_ISSET_ISEMPTY_PROP_OBJ":
				// An unused op1 is $this; isset() never warns, so it is quiet.
				object, _ := f.objectOperand(line, true)
				if object != nil && object.Lazy != nil {
					lazyName, lazyMagic := lazyProperty(f, line, name)
					resolved, lazyErr := in.resolveLazy(ctx, unit, object, lazyName, lazyMagic, depth)
					if lazyErr != nil {
						lazyErr = in.materialize(ctx, unit, lazyErr)
						if handler, caught := f.raise(lazyErr, pc); caught {
							pc = handler
							continue
						}
						return php.Undef(), lazyErr
					}
					object = resolved
				}
				if object != nil {
					property, nameErr := in.propertyName(ctx, unit, f, line.GetOp2Type(), line.GetOp2())
					if nameErr != nil {
						nameErr = in.materialize(ctx, unit, nameErr)
						if target, caught := f.raise(nameErr, pc); caught {
							pc = target
							continue
						}
						return php.Undef(), nameErr
					}
					// Inside a class that declares its own private property of this
					// name, the name means that class's slot, which is not the one the
					// plain name holds when a subclass declares one too.
					property = propertySlot(object, property, f.class)
					if hooked, declaring, ok := unit.hooksFor(f, object, property); ok {
						// isset() on a hooked property runs the get hook and
						// asks whether what it produced is null.
						read, err := in.hookedRead(ctx, unit, object, hooked, declaring, depth)
						if err != nil {
							if handler, caught := f.raise(in.materialize(ctx, unit, err), pc); caught {
								pc = handler
								continue
							}
							return php.Undef(), err
						}
						value, present = read, true
						break
					}
					value, present = object.Get(property)
					if _, verdict := unit.propertyAccess(object, property, f.class); verdict != accessAllowed {
						// isset() never raises: a property the caller cannot
						// reach simply reads as not set, and __isset still
						// gets its say.
						value, present = php.Undef(), false
					}
					// A declared typed property that was never initialized is
					// not set, and the magic accessors are not consulted: the
					// property does exist, it merely holds no value. Only
					// unset() -- which removes it outright -- reaches __isset.
					declared := present && value.Deref().Kind() == php.KindUndef &&
						!object.WasCleared(property)
					if declared {
						value, present = php.Null(), false
					} else if present && value.Deref().Kind() == php.KindUndef {
						// unset() removed it, so the magic accessors answer.
						present = false
					}
					if !present && !declared {
						// __isset decides for a property the object does not
						// hold, and __get then supplies the value empty() needs
						// to judge truthiness.
						answer, handled, err := in.callMagic(ctx, unit, object, magicIsset,
							property, []php.Value{php.String(property)}, depth)
						if err != nil {
							if handler, caught := f.raise(err, pc); caught {
								pc = handler
								continue
							}
							return php.Undef(), err
						}
						if handled && answer.Bool() {
							present = true
							// isset() is answered by __isset alone. empty()
							// also has to judge truthiness, so it goes on to
							// ask __get for the value.
							value = php.Bool(true)
							if line.GetExtendedValue()&isEmptyCheck != 0 {
								// Only __get can supply a value. Without one
								// there is nothing to read, so the name is
								// empty however __isset answered -- the class
								// says the property is set and then cannot say
								// what it holds.
								value = php.Null()
								if got, viaGet, err := in.callMagic(ctx, unit, object, magicGet,
									property, []php.Value{php.String(property)}, depth); err == nil && viaGet {
									value = got
								}
							}
						}
					}
				}
			case "ZEND_ISSET_ISEMPTY_DIM_OBJ":
				subject := f.fetchQuiet(line.GetOp1Type(), line.GetOp1())
				switch subject.Kind() {
				case php.KindArray:
					index := f.fetch(line.GetOp2Type(), line.GetOp2())
					if offending := illegalOffsetType(index); offending != "" {
						err := in.throwError(ctx, unit, "TypeError",
							"Cannot access offset of type %s in isset or empty", offending)
						if target, caught := f.raise(err, pc); caught {
							pc = target
							continue
						}
						return php.Undef(), err
					}
					value, present = subject.Array().Get(php.ArrayKey(ctx, index))
				case php.KindString:
					// A string offset is set when the index can be used as one
					// at all and lands within the string. isset() never raises,
					// so an unusable index simply is not set.
					text := subject.String()
					offset, err := stringOffset(ctx, f.fetch(line.GetOp2Type(), line.GetOp2()), true, true)
					if err != nil {
						break
					}
					if offset < 0 {
						offset += len(text)
					}
					if offset >= 0 && offset < len(text) {
						value, present = php.String(text[offset:offset+1]), true
					}
				case php.KindObject:
					// isset($o[$k]) is offsetExists; empty() also needs the
					// value, so it goes on to ask offsetGet.
					//
					// An object that is not an ArrayAccess cannot answer the
					// question at all, and the engine refuses rather than
					// saying "not set" -- isset() and empty() are not quiet
					// here the way they are about a missing array key.
					if !unit.implementsArrayAccess(subject.Object()) {
						err := in.indexedObjectError(ctx, unit, subject.Object())
						if target, caught := f.raise(err, pc); caught {
							pc = target
							continue
						}
						return php.Undef(), err
					}
					index := f.fetch(line.GetOp2Type(), line.GetOp2())
					exists, handled, err := in.offsetCall(ctx, unit, subject.Object(),
						offsetExists, []php.Value{index}, depth)
					if err != nil {
						if target, caught := f.raise(err, pc); caught {
							pc = target
							continue
						}
						return php.Undef(), err
					}
					if handled && exists.Bool() {
						present, value = true, php.Bool(true)
						if line.GetExtendedValue()&isEmptyCheck != 0 {
							if got, ok, err := in.offsetCall(ctx, unit, subject.Object(),
								offsetGet, []php.Value{index}, depth); err == nil && ok {
								value = got
							}
						}
					}
				}
			default:
				className, err := in.classNameOperand(ctx, unit, f, line, line.GetOp2Type(), line.GetOp2())
				if err != nil {
					if target, caught := f.raise(err, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), err
				}
				// isset() on a static property uses the class too, so its
				// deferred initializers resolve before the question is answered
				// -- and whatever they report is reported.
				if class, known := unit.findClass(className); known {
					if err := in.updateClassConstants(ctx, unit, class, depth+1); err != nil {
						err = in.materialize(ctx, unit, err)
						if target, caught := f.raise(err, pc); caught {
							pc = target
							continue
						}
						return php.Undef(), err
					}
				}
				value, present = unit.staticProp(className,
					f.fetch(line.GetOp1Type(), line.GetOp1()).String(), f.class)
			}
			if line.GetExtendedValue()&isEmptyCheck != 0 {
				f.storeResult(line, php.Bool(!present || !value.Bool()))
			} else {
				f.storeResult(line, php.Bool(present && value.Kind() != php.KindNull))
			}
			pc++

		case "ZEND_FETCH_STATIC_PROP_R", "ZEND_FETCH_STATIC_PROP_IS",
			"ZEND_FETCH_STATIC_PROP_W", "ZEND_FETCH_STATIC_PROP_RW",
			"ZEND_FETCH_STATIC_PROP_FUNC_ARG", "ZEND_FETCH_STATIC_PROP_UNSET":
			// op2 is the class reference and op1 the property name -- the
			// reverse of the instance-property opcodes.
			className, err := in.classNameOperand(ctx, unit, f, line, line.GetOp2Type(), line.GetOp2())
			if err != nil {
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			propName := f.fetch(line.GetOp1Type(), line.GetOp1()).String()
			// Reaching a static property *uses* the class, which resolves its
			// deferred initializers. Probed: touching any static property of a
			// class -- reading, writing or isset -- reports whatever every one
			// of that class's constants and property defaults reports, in
			// declaration order, before the access itself happens.
			if class, known := unit.findClass(className); known {
				if err := in.updateClassConstants(ctx, unit, class, depth+1); err != nil {
					err = in.materialize(ctx, unit, err)
					if target, caught := f.raise(err, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), err
				}
			}
			if _, visibility, allowed := unit.staticPropAccess(className, propName, f.class); !allowed {
				if strings.HasSuffix(name, "_IS") {
					// The IS form is isset()/?? , which answers rather than raises.
					f.storeResult(line, php.Null())
					pc++
					continue
				}
				// The class named is the one the *reference* resolves to, not the
				// one that declared the property: `self::$x` inside B reports B
				// even though A wrote it, while `parent::$x` reports A. Probed
				// through self, parent, static and an explicit name.
				err := in.throwError(ctx, unit, "Error", "Cannot access %s property %s::$%s",
					visibility, php.DisplayName(className), propName)
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			unit.noteTraitStaticAccess(ctx, className, propName, true)
			ref, ok := unit.staticPropRef(className, propName)
			if !ok {
				return php.Undef(), &UnsupportedError{
					Opcode: "static property " + className + "::$" + propName,
					Line:   line.GetLine(), File: op.GetFilename(),
				}
			}
			// A write-mode fetch has to alias the property, so the reference is
			// handed out rather than its value.
			if strings.HasSuffix(name, "_W") || strings.HasSuffix(name, "_RW") ||
				strings.HasSuffix(name, "_FUNC_ARG") {
				if strings.HasSuffix(name, "_W") && !in.bindsReference(oplines, pc) {
					// Growing an array in a static property is refused the same
					// way as in an instance one, and named the same way -- the
					// "reference held by" wording is for a reference the
					// program took, not for the property's own slot.
					if err := in.refuseStaticAutoInit(ctx, unit, className, propName, ref); err != nil {
						if handler, caught := f.raise(in.materialize(ctx, unit, err), pc); caught {
							pc = handler
							continue
						}
						return php.Undef(), err
					}
				}
				f.bindResult(line, php.MakeRef(ref))
				pc++
				continue
			}
			if ref.Get().Deref().IsUndef() && !strings.HasSuffix(name, "_IS") &&
				!strings.HasSuffix(name, "_UNSET") {
				// A typed static property that was never assigned. The engine
				// names it as a *static* property, which the instance form does
				// not; isset() still answers false without raising.
				err := in.throwError(ctx, unit, "Error",
					"Typed static property %s::$%s must not be accessed before initialization",
					php.DisplayName(unit.staticPropOwner(className, propName)), propName)
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			f.storeResult(line, ref.Get())
			pc++

		case "ZEND_ASSIGN_STATIC_PROP_OP":
			// `C::$p += 1`. As with the plain form, op1 is the property name
			// and op2 the class -- the reverse of the instance opcodes -- and
			// the value follows in OP_DATA.
			if pc+1 >= len(oplines) || in.name(oplines[pc+1].GetOpcode()) != "ZEND_OP_DATA" {
				return php.Undef(), fmt.Errorf("%s:%d: %s without OP_DATA",
					op.GetFilename(), line.GetLine(), name)
			}
			data := oplines[pc+1]
			className, err := in.classNameOperand(ctx, unit, f, line, line.GetOp2Type(), line.GetOp2())
			if err == nil {
				propName := f.fetch(line.GetOp1Type(), line.GetOp1()).String()
				if _, visibility, allowed := unit.staticPropAccess(className, propName, f.class); !allowed {
					err = in.throwError(ctx, unit, "Error", "Cannot access %s property %s::$%s",
						visibility, php.DisplayName(className), propName)
				} else {
					unit.noteTraitStaticAccess(ctx, className, propName, true)
					ref, ok := unit.staticPropRef(className, propName)
					if !ok {
						return php.Undef(), &UnsupportedError{
							Opcode: "static property " + className + "::$" + propName,
							Line:   line.GetLine(), File: op.GetFilename(),
						}
					}
					if ref.Get().Deref().IsUndef() {
						// The read half comes first, and a typed static that
						// was never assigned has nothing to read.
						err = in.throwError(ctx, unit, "Error",
							"Typed static property %s::$%s must not be accessed before initialization",
							php.DisplayName(unit.staticPropOwner(className, propName)), propName)
					} else {
						operand := f.fetch(data.GetOp1Type(), data.GetOp1())
						var updated php.Value
						if updated, err = in.binaryOp(ctx, unit, f, line, ref.Get().Deref(), operand); err == nil {
							var checked php.Value
							if checked, err = in.verifyStaticPropertyWrite(ctx, unit, className,
								propName, updated, op.GetStrictTypes()); err == nil {
								// The slot may be a box shared with another
								// typed property -- `C::$a =& C::$b` -- whose
								// type applies whatever this one is declared
								// as, and is reported as the reference it is.
								if len(ref.Sources()) > 0 {
									checked, err = in.verifyRefWrite(ctx, unit, ref, checked,
										op.GetStrictTypes())
								}
							}
							if err == nil {
								ref.Set(checked)
								if line.GetResultType() != opUnused {
									f.storeResult(line, checked)
								}
							}
						}
					}
				}
			}
			if err != nil {
				if target, caught := f.raise(in.materialize(ctx, unit, err), pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			pc += 2

		case "ZEND_ASSIGN_STATIC_PROP", "ZEND_ASSIGN_STATIC_PROP_REF":
			if pc+1 >= len(oplines) || in.name(oplines[pc+1].GetOpcode()) != "ZEND_OP_DATA" {
				return php.Undef(), fmt.Errorf("%s:%d: %s without OP_DATA",
					op.GetFilename(), line.GetLine(), name)
			}
			data := oplines[pc+1]
			className, err := in.classNameOperand(ctx, unit, f, line, line.GetOp2Type(), line.GetOp2())
			if err != nil {
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			propName := f.fetch(line.GetOp1Type(), line.GetOp1()).String()
			if _, visibility, allowed := unit.staticPropAccess(className, propName, f.class); !allowed {
				// The class named is the one the *reference* resolves to, not the
				// one that declared the property: `self::$x` inside B reports B
				// even though A wrote it, while `parent::$x` reports A. Probed
				// through self, parent, static and an explicit name.
				err := in.throwError(ctx, unit, "Error", "Cannot access %s property %s::$%s",
					visibility, php.DisplayName(className), propName)
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			unit.noteTraitStaticAccess(ctx, className, propName, true)
			ref, ok := unit.staticPropRef(className, propName)
			if !ok {
				return php.Undef(), &UnsupportedError{
					Opcode: "static property " + className + "::$" + propName,
					Line:   line.GetLine(), File: op.GetFilename(),
				}
			}
			if name == "ZEND_ASSIGN_STATIC_PROP_REF" {
				// The reference form rebinds the storage rather than writing
				// through it, and the value that was there is dropped here --
				// destructor and all.
				source, isVar := f.slotRef(data.GetOp1Type(), data.GetOp1())
				if !isVar {
					return php.Undef(), &UnsupportedError{
						Opcode: "reference to a non-variable",
						Line:   line.GetLine(), File: op.GetFilename(),
					}
				}
				// The binding is itself a write to the property, so the value
				// already in the box has to satisfy the declaration -- and is
				// reported as a write to the property rather than as one
				// through a reference, which is what it is becoming.
				if conflict := in.staticRefBindingConflict(ctx, unit, className,
					propName, source, op.GetStrictTypes()); conflict != nil {
					if target, caught := f.raise(conflict, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), conflict
				}
				checked, err := in.verifyStaticPropertyWrite(ctx, unit, className,
					propName, source.Get(), op.GetStrictTypes())
				if err != nil {
					if target, caught := f.raise(err, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), err
				}
				source.Set(checked)
				// The box takes the property's place as the storage, so it
				// takes its type with it: `$r = "x"` afterwards is checked.
				// Only this property's type travels -- another property that
				// merely shared the box being replaced keeps its own.
				previous, rebound := unit.rebindStaticProp(className, propName, source)
				if !rebound {
					return php.Undef(), &UnsupportedError{
						Opcode: "static property " + className + "::$" + propName,
						Line:   line.GetLine(), File: op.GetFilename(),
					}
				}
				// After the rebind, so that a property rebound to the box it
				// already holds ends up last among that box's sources.
				unit.noteStaticPropType(className, propName, source)
				php.Shared(php.MakeRef(source))
				if line.GetResultType() != opUnused {
					f.bindSlot(line.GetResultType(), line.GetResult(), php.MakeRef(source))
				}
				if lazyErr := in.releaseCheck(ctx, unit, previous); lazyErr != nil {
					lazyErr = in.materialize(ctx, unit, lazyErr)
					if handler, caught := f.raise(lazyErr, pc); caught {
						pc = handler
						continue
					}
					return php.Undef(), lazyErr
				}
				pc += 2
				continue
			}
			value := f.fetch(data.GetOp1Type(), data.GetOp1()).Copy()
			checked, err := in.verifyStaticPropertyWrite(ctx, unit, className, propName,
				value, op.GetStrictTypes())
			if err != nil {
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			value = checked
			// Once the program has bound a reference to the property, the box
			// is shared with whatever else holds it -- and every typed property
			// sharing it constrains the write, reported as one through the
			// reference rather than to the name being assigned.
			if ref.IsShared() {
				converted, refErr := in.verifyRefWrite(ctx, unit, ref, value, op.GetStrictTypes())
				if refErr != nil {
					if target, caught := f.raise(refErr, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), refErr
				}
				value = converted
			}
			ref.Set(value)
			if line.GetResultType() != opUnused {
				f.storeResult(line, value)
			}
			pc += 2

		case "ZEND_ASSIGN_REF":
			// `$a = &$b`: both names end up sharing one box, so the source slot
			// becomes a reference too if it was not one already.
			// A source the program cannot name: a function's by-value result,
			// either straight from the call or already boxed by MAKE_REF.
			// Binding to one is legal but pointless -- the box has nowhere to
			// write back to -- and PHP says so.
			borrowedSource := f.borrowed(line.GetOp2Type(), line.GetOp2())
			if borrowedSource || (line.GetExtendedValue()&returnsFunction != 0 &&
				!f.rawSlot(line.GetOp2Type(), line.GetOp2()).IsRef()) {
				ctx.Notice("Only variables should be assigned by reference")
			}
			// Such a box has exactly one name once it is bound, so it is not
			// one var_dump marks: `$o->p = &g()` prints int(2), not &int(2).
			bindRef := sharedRef
			if borrowedSource {
				bindRef = func(r *php.Ref) php.Value { return php.MakeRef(r) }
			}
			// A typed property that holds nothing has no value to point at,
			// and this is before slotRef fills the box with null.
			if err := in.refuseUninitializedRef(ctx, unit,
				f.rawSlot(line.GetOp2Type(), line.GetOp2())); err != nil {
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			source, ok := f.slotRef(line.GetOp2Type(), line.GetOp2())
			if !ok {
				return php.Undef(), &UnsupportedError{
					Opcode: "reference to a non-variable",
					Line:   line.GetLine(), File: op.GetFilename(),
				}
			}
			// bindSlot, not store: the target stops sharing whatever it shared
			// before rather than writing through it. When the target is an
			// element or a property that a write fetch handed back, the
			// rebinding has to reach the container -- writing the temporary
			// would leave the element pointing at its old box.
			if target, noted := f.writeTarget(line.GetOp1Type(), line.GetOp1()); noted {
				target.bind(bindRef(source))
				// An array holding a reference is the one shape that can put an
				// array in a cycle without an object being in it too.
				unit.hostsRef(target.array)
			}
			// What the target held is dropped by the rebinding, and PHP runs
			// its destructor *there* -- which is observable, since the
			// destructor can write through the box the assignment just made
			// and so change what the expression answers.
			released := f.fetchQuiet(line.GetOp1Type(), line.GetOp1())
			// Rebinding the name lets go of whatever it shared before, so the
			// box it held is shared by one name fewer -- and stops being one
			// var_dump marks once no name is left holding it.
			php.Unshare(f.rawSlot(line.GetOp1Type(), line.GetOp1()))
			if !borrowedSource {
				source.AddBinding()
			}
			bound := bindRef(source)
			f.bindSlot(line.GetOp1Type(), line.GetOp1(), bound)
			if f.op == unit.file && line.GetOp1Type() == opCV {
				// At global scope a compiled variable *is* the entry in the
				// globals table, so rebinding the name has to rebind that too --
				// otherwise `global $c` in a function still reaches the old box,
				// and so does anything walking the roots. It is the one name
				// twice over, not two bindings, so it reuses the box the slot
				// took rather than sharing the source again.
				ctx.Globals.Bind(php.StringKey(f.varName(line.GetOp1())), bound)
			}
			if line.GetResultType() != opUnused {
				f.bindSlot(line.GetResultType(), line.GetResult(), bindRef(source))
			}
			if lazyErr := in.releaseCheck(ctx, unit, released); lazyErr != nil {
				lazyErr = in.materialize(ctx, unit, lazyErr)
				if handler, caught := f.raise(lazyErr, pc); caught {
					pc = handler
					continue
				}
				return php.Undef(), lazyErr
			}
			pc++

		case "ZEND_MAKE_REF":
			if line.GetOp1Type() != opCV && !f.rawSlot(line.GetOp1Type(), line.GetOp1()).IsRef() {
				// A temporary holding a value nothing else names -- a
				// function's by-value result. A reference *to* it is a box of
				// its own, and the fact is remembered: destructuring through
				// one is what the engine reports, not making it.
				//
				held := f.fetch(line.GetOp1Type(), line.GetOp1()).Deref().Copy()
				f.bindSlot(line.GetResultType(), line.GetResult(), php.MakeRef(php.NewRef(held)))
				f.noteBorrowed(line.GetResultType(), line.GetResult())
				pc++
				continue
			}
			ref, ok := f.slotRef(line.GetOp1Type(), line.GetOp1())
			if !ok {
				return php.Undef(), &UnsupportedError{
					Opcode: "reference to a non-variable",
					Line:   line.GetLine(), File: op.GetFilename(),
				}
			}
			f.bindSlot(line.GetResultType(), line.GetResult(), php.MakeRef(ref))
			pc++

		case "ZEND_SEND_UNPACK":
			// `f(...$args)` spreads an array into the argument list. String
			// keys become named arguments, which are not modelled, so they are
			// reported rather than silently passed positionally.
			if len(calls) == 0 {
				return php.Undef(), fmt.Errorf("%s:%d: argument outside a call",
					op.GetFilename(), line.GetLine())
			}
			spread := f.fetch(line.GetOp1Type(), line.GetOp1())
			if spread.Kind() == php.KindObject {
				// A Traversable spreads too, in iteration order. A string key
				// names a parameter here as it does from an array -- which is
				// why a generator can supply named arguments as well.
				delegate, ok, err := in.newObjectIterator(ctx, unit, spread, depth)
				if err == nil && !ok {
					err = &UnsupportedError{
						Opcode: "argument unpacking of " + spread.Object().ClassName,
						Line:   line.GetLine(), File: op.GetFilename(),
					}
				}
				if err == nil {
					current := calls[len(calls)-1]
					afterNamed := false
					for {
						var raw php.Value
						var value php.Value
						var more bool
						if raw, value, more, err = in.iterNext(ctx, unit, delegate); err != nil || !more {
							break
						}
						// Spread into an argument list: the key names a
						// parameter or counts as positional. A Traversable can
						// produce anything, and only an int or a string is a
						// name or a position -- nothing is converted.
						if kind := raw.Deref().Kind(); kind != php.KindInt && kind != php.KindString {
							err = in.throwError(ctx, unit, "Error",
								"Keys must be of type int|string during argument unpacking")
							break
						}
						key := php.ArrayKey(ctx, raw)
						position := uint32(len(current.args)) + 1
						if !key.IsInt {
							afterNamed = true
							if at, ok := parameterPosition(in, unit, calls, key.String()); ok {
								if at <= len(current.args) && !current.args[at-1].IsUndef() {
									err = in.throwError(ctx, unit, "Error",
										"Named parameter $%s overwrites previous argument", key.String())
									break
								}
								position = uint32(at)
								for len(current.args) < at {
									current.args = append(current.args, php.Undef())
								}
							} else {
								current.named = append(current.named, namedArgument{
									name: key.String(), value: value.Deref().Copy(),
								})
								continue
							}
						} else if afterNamed {
							err = in.throwError(ctx, unit, "Error",
								"Cannot use positional argument after named argument during unpacking")
							break
						}
						// A Traversable yields values, not slots, so an argument
						// landing on a by-reference parameter cannot be one.
						// PHP passes it by value and says so.
						if in.argumentByRef(unit, calls, position) {
							ctx.Warn("Cannot pass by-reference argument %d of %s() by unpacking a Traversable, passing by-value instead",
								position, current.name)
						}
						for len(current.args) < int(position) {
							current.args = append(current.args, php.Undef())
						}
						current.args[position-1] = value.Deref().Copy()
					}
				}
				if err != nil {
					if target, caught := f.raise(in.materialize(ctx, unit, err), pc); caught {
						pc = target
						continue
					}
					return php.Undef(), err
				}
				pc++
				continue
			}
			if spread.Kind() != php.KindArray {
				// Unpacking into a *call* is a TypeError, where the same thing
				// in an array literal is a plain Error. The engine words them
				// identically and classes them differently.
				err := in.throwError(ctx, unit, "TypeError",
					"Only arrays and Traversables can be unpacked, %s given",
					givenTypeName(spread))
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			current := calls[len(calls)-1]
			source := spread.Array()
			// Within one array a named key may not be followed by a positional
			// one. Across two spreads it may: `f(...['b' => 1], ...[7])` is
			// fine, because each array is read on its own.
			afterNamed := false
			var outOfOrder error
			source.Each(func(k php.Key, v php.Value) bool {
				if !k.IsInt {
					// A string key names a parameter. Whether that name exists,
					// and whether it was already given, is settled where every
					// other named argument is settled.
					afterNamed = true
					// A named argument occupies its parameter's slot, so a
					// positional one after it takes the *next* slot rather
					// than the first. Placing it positionally is what makes
					// that fall out; a name the callee does not have stays in
					// the named list, where the error for it is raised.
					if at, ok := parameterPosition(in, unit, calls, k.String()); ok {
						if at <= len(current.args) && !current.args[at-1].IsUndef() {
							// The slot already holds an argument, so this name
							// is naming something that was given positionally.
							outOfOrder = in.throwError(ctx, unit, "Error",
								"Named parameter $%s overwrites previous argument", k.String())
							return false
						}
						for len(current.args) < at {
							current.args = append(current.args, php.Undef())
						}
						if in.argumentByRef(unit, calls, uint32(at)) {
							// The named parameter wants a slot, and the array
							// element is one: `test2(...['b' => 0])` on
							// `test2($a, &$b)` writes back into the array.
							current.args[at-1] = php.MakeRef(source.RefAt(k))
							return true
						}
						current.args[at-1] = v.Deref().Copy()
						return true
					}
					current.named = append(current.named, namedArgument{
						name: k.String(), value: v.Deref().Copy(),
					})
					return true
				}
				if afterNamed {
					outOfOrder = in.throwError(ctx, unit, "Error",
						"Cannot use positional argument after named argument during unpacking")
					return false
				}
				// An element landing on a by-reference parameter is passed as
				// the element itself, so the callee writes back into the array
				// -- which is what `f(...$array)` on `f(&...$args)` does.
				position := uint32(len(current.args)) + 1
				if in.argumentByRef(unit, calls, position) {
					current.args = append(current.args, php.MakeRef(source.RefAt(k)))
					return true
				}
				current.args = append(current.args, v.Deref().Copy())
				return true
			})
			if outOfOrder != nil {
				if target, caught := f.raise(outOfOrder, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), outOfOrder
			}
			pc++

		case "ZEND_SEND_ARRAY":
			// call_user_func_array passes the whole argument list as one array,
			// which the compiler hands over in a single opcode rather than as a
			// SEND per argument.
			if len(calls) == 0 {
				return php.Undef(), fmt.Errorf("%s:%d: argument outside a call",
					op.GetFilename(), line.GetLine())
			}
			list := f.fetch(line.GetOp1Type(), line.GetOp1()).Deref()
			if list.Kind() != php.KindArray {
				// The opcode carries call_user_func_array's own wording, since
				// that is the only call it is ever compiled for.
				err := in.throwError(ctx, unit, "TypeError",
					"call_user_func_array(): Argument #2 ($args) must be of type array, %s given",
					givenTypeName(list))
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			current := calls[len(calls)-1]
			source := list.Array()
			// The compiler folds `call_user_func_array($f, array_slice($a, N,
			// $len))` into this one opcode -- op2 carries the length and
			// extended_value the offset, and the slice happens while the
			// arguments are sent. There is no array_slice call left to check
			// its own argument, so the opcode does it, in array_slice's words.
			if line.GetOp2Type() != opUnused {
				skip, count := int(line.GetExtendedValue()), source.Len()
				length := count - skip
				given := f.fetch(line.GetOp2Type(), line.GetOp2()).Deref()
				switch given.Kind() {
				case php.KindInt:
					length = int(given.Int())
				case php.KindNull, php.KindUndef:
					// The whole remainder, which is what array_slice's null is.
				default:
					converted, ok := in.checkType(ctx, unit, "int", given, op.GetStrictTypes())
					if !ok {
						err := in.throwError(ctx, unit, "TypeError",
							"array_slice(): Argument #3 ($length) must be of type ?int, %s given",
							givenTypeName(given))
						if target, caught := f.raise(err, pc); caught {
							pc = target
							continue
						}
						return php.Undef(), err
					}
					length = int(converted.Int())
				}
				if length < 0 {
					length += count - skip
				}
				if length > count-skip {
					length = count - skip
				}
				sliced := php.NewArray()
				if skip < count && length > 0 {
					at := 0
					source.Each(func(k php.Key, _ php.Value) bool {
						if at < skip {
							at++
							return true
						}
						if at-skip >= length {
							return false
						}
						at++
						// The slice keeps the slots, so a by-reference element
						// is still one; array_slice runs here with
						// preserve_keys false, so the keys are not kept.
						raw, _ := source.Raw(k)
						sliced.Append(raw)
						return true
					})
				}
				source = sliced
			}
			afterNamed := false
			var outOfOrder error
			source.Each(func(k php.Key, v php.Value) bool {
				if !k.IsInt {
					// A string key names a parameter, exactly as it does when
					// an array is spread with `...`.
					afterNamed = true
					if at, ok := parameterPosition(in, unit, calls, k.String()); ok {
						if at <= len(current.args) && !current.args[at-1].IsUndef() {
							outOfOrder = in.throwError(ctx, unit, "Error",
								"Named parameter $%s overwrites previous argument", k.String())
							return false
						}
						for len(current.args) < at {
							current.args = append(current.args, php.Undef())
						}
						// A name reaching a by-reference parameter is no
						// more a slot than a position would be, and earns
						// the same complaint -- against the position it
						// landed on, since that is what the callee sees.
						if raw, held := source.Raw(k); (!held || !raw.IsRef()) &&
							in.argumentByRef(unit, calls, uint32(at)) {
							ctx.Warn("%s(): Argument #%d ($%s) must be passed by reference, value given",
								current.name, at, k.String())
						}
						current.args[at-1] = v.Copy()
						return true
					}
					current.named = append(current.named, namedArgument{
						name: k.String(), value: v.Copy(),
					})
					return true
				}
				if afterNamed {
					// The same rule as unpacking, in the wording this spelling
					// gets: there is no `...` here to name.
					outOfOrder = in.throwError(ctx, unit, "Error",
						"Cannot use positional argument after named argument")
					return false
				}
				// An element that is already a reference is handed over as one
				// -- `call_user_func_array($f, [&$x])` is how a by-reference
				// parameter is reached this way. Each dereferences, so telling
				// requires reading the element raw.
				position := uint32(len(current.args)) + 1
				if raw, ok := source.Raw(k); ok {
					if _, isRef := raw.RefOf(); isRef {
						current.args = append(current.args, raw)
						return true
					}
				}
				if in.argumentByRef(unit, calls, position) {
					// The callee wants a slot and this element is not one, so
					// it goes by value and PHP says so.
					ctx.Warn("%s(): Argument #%d ($%s) must be passed by reference, value given",
						current.name, position, parameterName(in, unit, calls, position))
				}
				current.args = append(current.args, v.Copy())
				return true
			})
			if outOfOrder != nil {
				if target, caught := f.raise(outOfOrder, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), outOfOrder
			}
			pc++

		case "ZEND_SEND_REF":
			if len(calls) == 0 {
				return php.Undef(), fmt.Errorf("%s:%d: argument outside a call",
					op.GetFilename(), line.GetLine())
			}
			// slotRef reuses the box a slot already has and makes one otherwise,
			// so it covers both cases -- including defining an unset variable as
			// null, which is what passing it by reference does.
			ref, ok := f.slotRef(line.GetOp1Type(), line.GetOp1())
			if !ok {
				return php.Undef(), &UnsupportedError{
					Opcode: "by-reference argument that is not a variable",
					Line:   line.GetLine(), File: op.GetFilename(),
				}
			}
			argument := php.MakeRef(ref)
			current := calls[len(calls)-1]
			if line.GetOp2Type() == opConst {
				// A named argument: op2 holds the parameter name rather than
				// the position, and where it lands is settled once the callee
				// is known.
				current.named = append(current.named, namedArgument{
					name:  f.fetch(line.GetOp2Type(), line.GetOp2()).String(),
					value: argument,
				})
				pc++
				continue
			}
			current.args = append(current.args, argument)
			pc++

		case "ZEND_UNSET_STATIC_PROP":
			// A static property is part of the class, not of any instance, so
			// there is nothing for unset() to take away. The engine refuses
			// rather than clearing it, and names the class as written.
			className, err := in.classNameOperand(ctx, unit, f, line, line.GetOp2Type(), line.GetOp2())
			if err == nil {
				err = in.throwError(ctx, unit, "Error",
					"Attempt to unset static property %s::$%s",
					php.DisplayName(className), f.fetch(line.GetOp1Type(), line.GetOp1()).String())
			}
			if target, caught := f.raise(err, pc); caught {
				pc = target
				continue
			}
			return php.Undef(), err

		case "ZEND_UNSET_CV":
			// unset() breaks the name's binding without touching what any other
			// name sharing the box still sees -- but the box has one name fewer
			// afterwards, which is what var_dump's & marker is counting.
			released := f.fetchQuiet(line.GetOp1Type(), line.GetOp1())
			php.Unshare(f.rawSlot(line.GetOp1Type(), line.GetOp1()))
			f.bindSlot(line.GetOp1Type(), line.GetOp1(), php.Undef())
			if f.sharedScope && line.GetOp1Type() == opCV {
				// An include shares the caller's variables; removing a name
				// here removes the caller's name too.
				f.unsetNames = append(f.unsetNames, f.varName(line.GetOp1()))
			}
			if f.op == unit.file && line.GetOp1Type() == opCV {
				// At global scope a compiled variable is a name in the globals
				// table, and unset() removes it from there too -- otherwise
				// $GLOBALS would still have it, and so would anything walking
				// the roots.
				ctx.Globals.Unset(php.StringKey(f.varName(line.GetOp1())))
			}
			// The name is gone; if it was the last one holding an object, its
			// destructor runs here rather than at the end of the request -- and
			// what it throws is thrown here too.
			if lazyErr := in.releaseCheck(ctx, unit, released); lazyErr != nil {
				lazyErr = in.materialize(ctx, unit, lazyErr)
				if target, caught := f.raise(lazyErr, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), lazyErr
			}
			pc++

		case "ZEND_DECLARE_ANON_CLASS":
			// The class is harvested under the mangled name the compiler gave
			// it, so the declaration only has to hand that name to the NEW that
			// follows.
			name := f.fetch(line.GetOp1Type(), line.GetOp1()).String()
			anon, ok := unit.findClass(name)
			if !ok {
				return php.Undef(), &UnsupportedError{
					Opcode: "anonymous class declaration",
					Line:   line.GetLine(), File: op.GetFilename(),
				}
			}
			// An anonymous class is linked here like any other, so it asks for
			// its parent, interfaces and traits here too -- and is checked the
			// same way, which is what makes #[\Override] on one an error.
			if err := in.resolveDependencies(ctx, unit, anon); err != nil {
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			if err := in.linkClass(ctx, unit, anon); err != nil {
				return php.Undef(), err
			}
			f.storeResult(line, php.String(name))
			pc++

		case "ZEND_ASSIGN_OBJ_OP", "ZEND_ASSIGN_DIM_OP":
			// Compound assignment through a property or element. The value is in
			// the following OP_DATA, as it is for the plain assignment forms.
			if pc+1 >= len(oplines) || in.name(oplines[pc+1].GetOpcode()) != "ZEND_OP_DATA" {
				return php.Undef(), fmt.Errorf("%s:%d: %s without OP_DATA",
					op.GetFilename(), line.GetLine(), name)
			}
			data := oplines[pc+1]
			operand := f.fetch(data.GetOp1Type(), data.GetOp1())
			var container php.Value
			var object *php.Object
			if name == "ZEND_ASSIGN_OBJ_OP" {
				// An unused op1 is $this, which is how `$this->p .= x` is
				// encoded. The read half warns, so this is not a quiet fetch.
				object, container = f.objectOperand(line, false)
				if object != nil && object.Lazy != nil {
					// Touching a lazy object's state is what produces it. A proxy
					// answers with the instance it forwards to, so everything
					// below works on an ordinary object.
					lazyName, lazyMagic := lazyProperty(f, line, name)
					resolved, lazyErr := in.resolveLazy(ctx, unit, object, lazyName, lazyMagic, depth)
					if lazyErr != nil {
						lazyErr = in.materialize(ctx, unit, lazyErr)
						if handler, caught := f.raise(lazyErr, pc); caught {
							pc = handler
							continue
						}
						return php.Undef(), lazyErr
					}
					object = resolved
					container = php.Obj(object)
				}
			} else {
				container = f.fetch(line.GetOp1Type(), line.GetOp1())
			}

			// A hooked property is read and written through its hooks, so the
			// compound form is get, combine, set rather than a slot update.
			if name == "ZEND_ASSIGN_OBJ_OP" && object != nil {
				property, nameErr := in.propertyName(ctx, unit, f, line.GetOp2Type(), line.GetOp2())
				if nameErr != nil {
					nameErr = in.materialize(ctx, unit, nameErr)
					if target, caught := f.raise(nameErr, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), nameErr
				}
				// Inside a class that declares its own private property of this
				// name, the name means that class's slot, which is not the one the
				// plain name holds when a subclass declares one too.
				property = propertySlot(object, property, f.class)
				if hooked, declaring, ok := unit.hooksFor(f, object, property); ok {
					// Asymmetric visibility is checked before the hooks run:
					// a set hook is the *implementation* of the write, not a
					// way around `private(set)`.
					err := in.checkSetVisibility(ctx, unit, object, hooked, property,
						f.class, "modify")
					var updated php.Value
					if err == nil {
						updated, err = in.hookedCompound(ctx, unit, f, line, object,
							hooked, declaring, operand, depth)
					}
					if err != nil {
						if handler, caught := f.raise(in.materialize(ctx, unit, err), pc); caught {
							pc = handler
							continue
						}
						return php.Undef(), err
					}
					if line.GetResultType() != opUnused {
						f.storeResult(line, updated)
					}
					pc += 2
					continue
				}
			}

			var current php.Value
			var apply func(php.Value) php.Value
			// A typed property can refuse the result, and apply has nowhere to
			// return it from, so the refusal is carried out here.
			var compoundErr error
			if name == "ZEND_ASSIGN_DIM_OP" && container.Deref().Kind() == php.KindObject {
				// `$o[$k] .= x` on an ArrayAccess reads through offsetGet and
				// writes back through offsetSet.
				target := container.Deref().Object()
				// An append names no offset, and the accessors are still called
				// -- with null, which is what `$o[] = x` passes offsetSet too.
				index := php.Null()
				if line.GetOp2Type() != opUnused {
					index = f.fetch(line.GetOp2Type(), line.GetOp2())
				}
				existing, err := in.readOffset(ctx, unit, target, index, false, line, op, depth)
				if err == nil {
					var updated php.Value
					if updated, err = in.binaryOp(ctx, unit, f, line, existing, operand); err == nil {
						_, handled, callErr := in.offsetCall(ctx, unit, target, offsetSet,
							[]php.Value{index, updated}, depth)
						if err = callErr; err == nil && !handled {
							err = in.indexedObjectError(ctx, unit, target)
						}
						if err == nil {
							f.storeResult(line, updated)
						}
					}
				}
				if err != nil {
					if handler, caught := f.raise(in.materialize(ctx, unit, err), pc); caught {
						pc = handler
						continue
					}
					return php.Undef(), err
				}
				pc += 2
				continue
			}
			if name == "ZEND_ASSIGN_OBJ_OP" {
				if object == nil {
					return php.Undef(), &UnsupportedError{
						Opcode: "compound property assignment on " + container.Kind().String(),
						Line:   line.GetLine(), File: op.GetFilename(),
					}
				}
				prop, nameErr := in.propertyName(ctx, unit, f, line.GetOp2Type(), line.GetOp2())
				if nameErr != nil {
					nameErr = in.materialize(ctx, unit, nameErr)
					if target, caught := f.raise(nameErr, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), nameErr
				}
				// A compound assignment reads before it writes, so a typed
				// property holding nothing is reported before the write is
				// judged -- the same order the increment forms use.
				// Inside a class that declares its own private property of this
				// name, the name means that class's slot, which is not the one the
				// plain name holds when a subclass declares one too.
				prop = propertySlot(object, prop, f.class)
				if err := in.uninitializedTyped(ctx, unit, object, prop); err != nil {
					if target, caught := f.raise(err, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), err
				}
				// It writes too, so it faces the same visibility check a plain
				// assignment does -- but only that one. readonly and
				// `private(set)` are judged *after* the operation, in `apply`
				// below, because the engine computes the value first.
				verdict, err := in.checkPropertyWriteAccess(ctx, unit, object, prop, f.class)
				if err != nil {
					if target, caught := f.raise(err, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), err
				}
				step := func(v php.Value) (php.Value, error) {
					return in.binaryOp(ctx, unit, f, line, v, operand)
				}
				if _, now, viaMagic, err := in.magicCompound(ctx, unit, object,
					prop, f.class, step, depth); viaMagic {
					if err != nil {
						if target, caught := f.raise(in.materialize(ctx, unit, err), pc); caught {
							pc = target
							continue
						}
						return php.Undef(), err
					}
					if line.GetResultType() != opUnused {
						f.storeResult(line, now)
					}
					pc += 2
					continue
				}
				var found bool
				current, found = object.Get(prop)
				if !found {
					// The read half of the compound assignment reports the
					// missing property just as a plain read would, and the
					// write half is still creating one the class never had.
					unit.noteDynamicProperty(ctx, object, prop, verdict)
					// The slot is made before the report is handed out, so a
					// handler that throws over it leaves the property behind
					// holding null -- created but never written -- and a
					// handler that *writes* the property writes into this very
					// slot. The read half of the compound assignment is
					// therefore taken again afterwards: `$c->a %= 10` with a
					// handler that sets `$c->a = 12345` computes with 12345.
					object.Set(prop, php.Null())
					ctx.Warn("Undefined property: %s::$%s", php.DisplayName(object.ClassName), prop)
					current, _ = object.Get(prop)
				}
				if pending := ctx.TakePendingError(); pending != nil {
					// A handler that threw over the report leaves the write
					// half undone.
					if target, caught := f.raise(pending, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), pending
				}
				// The write half faces the declared type as well: the result
				// of the operation has to be assignable to the property.
				checkedObject, checkedProp := object, prop
				apply = func(v php.Value) php.Value {
					// The operation has produced a value; now the property may
					// refuse to take it. `$o->readonlyObject += 1` never gets
					// here -- the operand types are reported first.
					if err := in.checkPropertyModify(ctx, unit, checkedObject,
						checkedProp, f.class); err != nil {
						compoundErr = err
						return v
					}
					converted, err := in.verifyPropertyWrite(ctx, unit, checkedObject,
						checkedProp, v, op.GetStrictTypes())
					if err != nil {
						compoundErr = err
						return v
					}
					// The slot may also be bound to a typed property elsewhere,
					// whose type applies whatever this one is declared as.
					if ref := typedRefHeld(checkedObject.Props, php.StringKey(checkedProp)); ref != nil {
						converted, err = in.verifyRefWrite(ctx, unit, ref, converted, op.GetStrictTypes())
						if err != nil {
							compoundErr = err
							return v
						}
					}
					checkedObject.Set(checkedProp, converted)
					return converted
				}
			} else {
				grown, ok, growErr := in.growArray(ctx, unit, f, line.GetOp1Type(), line.GetOp1(), container)
				if growErr != nil {
					if handler, caught := f.raise(in.materialize(ctx, unit, growErr), pc); caught {
						pc = handler
						continue
					}
					return php.Undef(), growErr
				}
				if !ok {
					// A string offset has a further refusal of its own: it can
					// be assigned but not read-modify-written. Appending is
					// refused first and in its own words -- `$s[] .= x` is
					// about the `[]`, not about the operator.
					message := scalarIndexError(container, line, false, false)
					if container.Deref().Kind() == php.KindString &&
						line.GetOp2Type() != opUnused {
						message = "Cannot use assign-op operators with string offsets"
					}
					err := in.throwError(ctx, unit, "Error", "%s", message)
					if target, caught := f.raise(err, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), err
				}
				array := grown.Array()
				var key php.Key
				if line.GetOp2Type() == opUnused {
					// `$a[] .= x` appends, and the element it appends to is
					// new: there is no key to have missed, so nothing is
					// reported and the read half sees null.
					key = array.Append(php.Null())
					current = php.Null()
				} else {
					index := f.fetch(line.GetOp2Type(), line.GetOp2())
					// The offset is checked before it is used, the same way a
					// plain read checks it: an array or an object has no key
					// form at all, so there is nothing to look up and nothing
					// to report missing.
					if offending := illegalOffsetType(index); offending != "" {
						err := in.throwError(ctx, unit, "TypeError",
							"Cannot access offset of type %s on array", offending)
						if target, caught := f.raise(err, pc); caught {
							pc = target
							continue
						}
						return php.Undef(), err
					}
					key = php.ArrayKey(ctx, index)
					var found bool
					current, found = array.Get(key)
					if !found {
						// Compound assignment reads before it writes, so a
						// missing key is reported exactly as a plain read
						// would report it.
						ctx.Warn("Undefined array key %s", quoteKey(index))
					}
				}
				if pending := ctx.TakePendingError(); pending != nil {
					// The report reached a user error handler and that handler
					// threw. The write half never runs: the engine abandons the
					// opcode where the throw happened rather than finishing it.
					if target, caught := f.raise(pending, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), pending
				}
				apply = func(v php.Value) php.Value {
					if ref := typedRefHeld(array, key); ref != nil {
						converted, err := in.verifyRefWrite(ctx, unit, ref, v, op.GetStrictTypes())
						if err != nil {
							compoundErr = err
							return v
						}
						v = converted
					}
					array.Set(key, v)
					return v
				}
			}

			result, err := in.binaryOp(ctx, unit, f, line, current, operand)
			if err != nil {
				if handler, caught := f.raise(err, pc); caught {
					pc = handler
					continue
				}
				return php.Undef(), err
			}
			result = apply(result)
			if compoundErr != nil {
				if handler, caught := f.raise(compoundErr, pc); caught {
					pc = handler
					continue
				}
				return php.Undef(), compoundErr
			}
			if line.GetResultType() != opUnused {
				f.storeResult(line, result)
			}
			// OP_DATA is consumed here, not executed.
			pc += 2

		case "ZEND_ASSIGN_OP":
			// A compound assignment. extended_value holds the opcode of the
			// binary operation to apply, so the same handlers serve `+=` as `+`.
			target := f.fetch(line.GetOp1Type(), line.GetOp1())
			operand := f.fetch(line.GetOp2Type(), line.GetOp2())
			result, err := in.binaryOp(ctx, unit, f, line, target, operand)
			if err != nil {
				if handler, caught := f.raise(err, pc); caught {
					pc = handler
					continue
				}
				return php.Undef(), err
			}
			if ref := f.typedRef(line.GetOp1Type(), line.GetOp1()); ref != nil {
				converted, err := in.verifyRefWrite(ctx, unit, ref, result, op.GetStrictTypes())
				if err != nil {
					if handler, caught := f.raise(err, pc); caught {
						pc = handler
						continue
					}
					return php.Undef(), err
				}
				result = converted
			}
			f.store(line.GetOp1Type(), line.GetOp1(), result)
			if line.GetResultType() != opUnused {
				f.storeResult(line, result)
			}
			pc++

		case "ZEND_BW_NOT":
			operand := f.fetch(line.GetOp1Type(), line.GetOp1())
			// Only a string and the numbers have a complement. An array or an
			// object has no bits, and PHP names the operator rather than
			// talking about operand types.
			switch operand.Kind() {
			case php.KindString, php.KindInt, php.KindFloat:
			default:
				// The message names the *class* of an object, the way
				// "Unsupported operand types" does -- not the word "object".
				err := in.throwError(ctx, unit, "TypeError",
					"Cannot perform bitwise not on %s", php.TypeName(operand))
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			// A float is converted like any other integral operator's operand,
			// and says so when the conversion loses something.
			php.WarnPrecisionLoss(ctx, operand)
			f.storeResult(line, php.BitNot(operand))
			pc++

		case "ZEND_MOD", "ZEND_SL", "ZEND_SR":
			a := f.fetch(line.GetOp1Type(), line.GetOp1())
			b := f.fetch(line.GetOp2Type(), line.GetOp2())
			if err := in.checkOperands(ctx, unit, name, a, b); err != nil {
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			var result php.Value
			var ok bool
			var errClass, errMessage string
			switch name {
			case "ZEND_MOD":
				result, ok = php.Mod(a, b)
				errClass, errMessage = "DivisionByZeroError", "Modulo by zero"
			case "ZEND_SL":
				result, ok = php.ShiftLeft(a, b)
				errClass, errMessage = "ArithmeticError", "Bit shift by negative number"
			default:
				result, ok = php.ShiftRight(a, b)
				errClass, errMessage = "ArithmeticError", "Bit shift by negative number"
			}
			if !ok {
				err := in.throwError(ctx, unit, errClass, "%s", errMessage)
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			f.storeResult(line, result)
			pc++

		case "ZEND_DIV":
			a := f.fetch(line.GetOp1Type(), line.GetOp1())
			b := f.fetch(line.GetOp2Type(), line.GetOp2())
			if err := in.checkOperands(ctx, unit, name, a, b); err != nil {
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			quotient, ok := php.Div(a, b)
			if !ok {
				err := in.throwError(ctx, unit, "DivisionByZeroError", "Division by zero")
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			f.storeResult(line, quotient)
			pc++

		case "ZEND_SPACESHIP":
			// `<=>` yields the ordering itself rather than a boolean, so it is
			// the one comparison that does not go through compare.
			a := f.fetch(line.GetOp1Type(), line.GetOp1())
			b := f.fetch(line.GetOp2Type(), line.GetOp2())
			// An object beside a string or a number converts first, exactly as
			// it does for the boolean comparisons.
			converted, other, err := in.renderForComparison(ctx, unit, a, b)
			if err != nil {
				err = in.materialize(ctx, unit, err)
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			f.storeResult(line, php.Int(int64(php.Compare(converted, other))))
			pc++

		case "ZEND_IS_EQUAL", "ZEND_IS_NOT_EQUAL",
			"ZEND_IS_IDENTICAL", "ZEND_IS_NOT_IDENTICAL",
			"ZEND_IS_SMALLER", "ZEND_IS_SMALLER_OR_EQUAL",
			"ZEND_CASE", "ZEND_CASE_STRICT":
			a := f.fetch(line.GetOp1Type(), line.GetOp1())
			b := f.fetch(line.GetOp2Type(), line.GetOp2())
			// A loose comparison between an object and a string renders the
			// object first. The identical forms do not: those compare types.
			if name != "ZEND_IS_IDENTICAL" && name != "ZEND_IS_NOT_IDENTICAL" &&
				name != "ZEND_CASE_STRICT" {
				var err error
				if a, b, err = in.renderForComparison(ctx, unit, a, b); err != nil {
					err = in.materialize(ctx, unit, err)
					if target, caught := f.raise(err, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), err
				}
			}
			result, recursed := compareChecked(name, a, b)
			if recursed {
				// The engine protects the container it is comparing and raises
				// the moment the walk re-enters it, rather than answering.
				err := in.throwError(ctx, unit, "Error",
					"Nesting level too deep - recursive dependency?")
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			f.storeResult(line, php.Bool(result))
			pc++

		case "ZEND_CAST":
			// extended_value carries the target type, using the engine's IS_*
			// numbering. _IS_BOOL is a pseudo-type outside that range.
			value := f.fetch(line.GetOp1Type(), line.GetOp1())
			switch line.GetExtendedValue() {
			case castNull:
				value = php.Null()
			case castBool:
				value = php.Bool(value.Bool())
			case castInt:
				value = castNumeric(ctx, value, "int")
			case castFloat:
				value = castNumeric(ctx, value, "float")
			case castString:
				// (string)[1] yields "Array" and warns; an object goes through
				// __toString, and one without it is an Error. The compiler also
				// routes a constant array operand of `.` through here, so this
				// covers `[1] . "x"` as well.
				rendered, err := in.toString(ctx, unit, value)
				if err != nil {
					err = in.materialize(ctx, unit, err)
					if target, caught := f.raise(err, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), err
				}
				value = rendered
			case castArray:
				// Deliberately not a trigger: the cast reads the property
				// table as it stands, so a lazy object yields the properties
				// it has -- none -- rather than being made to produce them.
				// var_dump, get_mangled_object_vars and debug_zval_dump are
				// the same; foreach, get_object_vars, json_encode, serialize
				// and var_export are not.
				value = castToArray(value)
			case castObject:
				value = castToObject(unit, value)
			default:
				return php.Undef(), &UnsupportedError{
					Opcode: fmt.Sprintf("%s to type %d", name, line.GetExtendedValue()),
					Line:   line.GetLine(),
					File:   op.GetFilename(),
				}
			}
			f.storeResult(line, value)
			pc++

		case "ZEND_BIND_INIT_STATIC_OR_JMP":
			// `static $x = <expression>`: on later calls the value already
			// exists, so the initializer is skipped. The first call falls
			// through and runs it.
			varName := f.varName(line.GetOp1())
			if value, initialized := unit.staticInitialized(op, varName, f.class); initialized {
				f.bindSlot(line.GetOp1Type(), line.GetOp1(), value)
				f.trackStatic(line.GetOp1(), varName)
				pc = int(line.GetOp2())
				continue
			}
			f.trackStatic(line.GetOp1(), varName)
			pc++

		case "ZEND_BOOL":
			f.storeResult(line, php.Bool(f.fetch(line.GetOp1Type(), line.GetOp1()).Bool()))
			pc++

		case "ZEND_BOOL_NOT":
			f.storeResult(line, php.Bool(!f.fetch(line.GetOp1Type(), line.GetOp1()).Bool()))
			pc++

		case "ZEND_BOOL_XOR":
			// `xor` has no short circuit -- both sides are evaluated -- which
			// is why it has an opcode of its own rather than the jumps `&&` and
			// `||` compile to.
			f.storeResult(line, php.Bool(
				f.fetch(line.GetOp1Type(), line.GetOp1()).Bool() !=
					f.fetch(line.GetOp2Type(), line.GetOp2()).Bool()))
			pc++

		case "ZEND_PRE_INC", "ZEND_PRE_DEC", "ZEND_POST_INC", "ZEND_POST_DEC":
			old := f.fetch(line.GetOp1Type(), line.GetOp1())
			updated, err := in.stepValue(ctx, unit, name, old)
			if err != nil {
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			if ref := f.typedRef(line.GetOp1Type(), line.GetOp1()); ref != nil {
				converted, err := in.verifyRefStep(ctx, unit, ref, old, updated, name, op.GetStrictTypes())
				if err != nil {
					if target, caught := f.raise(err, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), err
				}
				updated = converted
			}
			f.store(line.GetOp1Type(), line.GetOp1(), updated)
			// Post- forms yield the value from before the change.
			if name == "ZEND_POST_INC" || name == "ZEND_POST_DEC" {
				f.storeResult(line, old)
			} else {
				f.storeResult(line, updated)
			}
			pc++

		case "ZEND_PRE_INC_OBJ", "ZEND_PRE_DEC_OBJ", "ZEND_POST_INC_OBJ", "ZEND_POST_DEC_OBJ",
			"ZEND_PRE_INC_STATIC_PROP", "ZEND_PRE_DEC_STATIC_PROP",
			"ZEND_POST_INC_STATIC_PROP", "ZEND_POST_DEC_STATIC_PROP":
			// Increment through a property. The plain forms cannot serve here
			// because the target is not a slot.
			var slot *php.Ref
			// A typed property reports an overflow as a step past the type's
			// limit, so the step needs to know which property it is writing.
			var stepOwner *php.Object
			var stepProp string
			// stepStatic names the class when the target is a static property,
			// which has no instance to blame.
			var stepStatic string
			if strings.HasSuffix(name, "_OBJ") {
				// An unused op1 is $this, which is how `$this->p++` is encoded.
				object, subject := f.objectOperand(line, false)
				if object != nil && object.Lazy != nil {
					// Touching a lazy object's state is what produces it. A proxy
					// answers with the instance it forwards to, so everything
					// below works on an ordinary object.
					lazyName, lazyMagic := lazyProperty(f, line, name)
					resolved, lazyErr := in.resolveLazy(ctx, unit, object, lazyName, lazyMagic, depth)
					if lazyErr != nil {
						lazyErr = in.materialize(ctx, unit, lazyErr)
						if handler, caught := f.raise(lazyErr, pc); caught {
							pc = handler
							continue
						}
						return php.Undef(), lazyErr
					}
					object = resolved
					subject = php.Obj(object)
				}
				if object == nil {
					return php.Undef(), &UnsupportedError{
						Opcode: "increment of a property on " + subject.Kind().String(),
						Line:   line.GetLine(), File: op.GetFilename(),
					}
				}
				prop, nameErr := in.propertyName(ctx, unit, f, line.GetOp2Type(), line.GetOp2())
				if nameErr != nil {
					nameErr = in.materialize(ctx, unit, nameErr)
					if target, caught := f.raise(nameErr, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), nameErr
				}
				// A hooked property has no slot to increment: the value comes
				// from the get hook and goes back through the set hook.
				// Inside a class that declares its own private property of this
				// name, the name means that class's slot, which is not the one the
				// plain name holds when a subclass declares one too.
				prop = propertySlot(object, prop, f.class)
				if hooked, declaring, ok := unit.hooksFor(f, object, prop); ok {
					err := in.checkSetVisibility(ctx, unit, object, hooked, prop,
						f.class, "modify")
					var old php.Value
					if err == nil {
						old, err = in.hookedRead(ctx, unit, object, hooked, declaring, depth)
					}
					var updated php.Value
					if err == nil {
						updated, err = in.stepValue(ctx, unit, name, old)
					}
					if err == nil {
						if err = in.hookedWrite(ctx, unit, object, hooked, declaring, updated, depth); err == nil {
							if strings.HasPrefix(name, "ZEND_POST") {
								f.storeResult(line, old)
							} else {
								f.storeResult(line, updated)
							}
							pc++
							continue
						}
					}
					if handler, caught := f.raise(in.materialize(ctx, unit, err), pc); caught {
						pc = handler
						continue
					}
					return php.Undef(), err
				}
				// An increment reads before it writes, so a typed property
				// that holds nothing is reported before the write is even
				// judged: `$o->p++` on an uninitialized private(set) property
				// says so rather than complaining about the scope.
				if err := in.uninitializedTyped(ctx, unit, object, prop); err != nil {
					if target, caught := f.raise(err, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), err
				}
				// An increment is a write too, so it faces those checks after.
				verdict, err := in.checkPropertyWrite(ctx, unit, object, prop, f.class)
				if err != nil {
					if target, caught := f.raise(err, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), err
				}
				// A name the object does not really have but __get does is
				// stepped through the accessors rather than through a slot.
				step := func(v php.Value) (php.Value, error) {
					return in.stepValue(ctx, unit, name, v)
				}
				if was, now, viaMagic, err := in.magicCompound(ctx, unit, object,
					prop, f.class, step, depth); viaMagic {
					if err != nil {
						if target, caught := f.raise(in.materialize(ctx, unit, err), pc); caught {
							pc = target
							continue
						}
						return php.Undef(), err
					}
					if strings.HasPrefix(name, "ZEND_POST") {
						f.storeResult(line, was)
					} else {
						f.storeResult(line, now)
					}
					pc++
					continue
				}
				if _, present := object.Get(prop); !present {
					// The read half creates the property, and reports both
					// halves of that: the deprecation for making one the class
					// never declared, then the warning for reading nothing.
					unit.noteDynamicProperty(ctx, object, prop, verdict)
					ctx.Warn("Undefined property: %s::$%s",
						php.DisplayName(object.ClassName), prop)
				}
				stepOwner, stepProp = object, prop
				slot = object.Props.RefAt(php.StringKey(prop))
			} else {
				className, err := in.classNameOperand(ctx, unit, f, line, line.GetOp2Type(), line.GetOp2())
				if err != nil {
					if target, caught := f.raise(err, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), err
				}
				propName := f.fetch(line.GetOp1Type(), line.GetOp1()).String()
				unit.noteTraitStaticAccess(ctx, className, propName, true)
				ref, ok := unit.staticPropRef(className, propName)
				if !ok {
					return php.Undef(), &UnsupportedError{
						Opcode: "static property " + className + "::$" + propName,
						Line:   line.GetLine(), File: op.GetFilename(),
					}
				}
				if ref.Get().Deref().IsUndef() {
					// The step reads before it writes, and a typed static
					// property that was never assigned has nothing to read.
					err := in.throwError(ctx, unit, "Error",
						"Typed static property %s::$%s must not be accessed before initialization",
						php.DisplayName(unit.staticPropOwner(className, propName)), propName)
					if target, caught := f.raise(err, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), err
				}
				slot = ref
				// The box a static property's slot *is* carries the property's
				// type as a source, but a write through it is a write to the
				// property, not through a reference to one -- so the overflow
				// is worded the way the instance form words it.
				stepStatic = className
				stepProp = propName
			}
			old := slot.Get()
			updated, err := in.stepValue(ctx, unit, name, old)
			if err != nil {
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			if stepOwner != nil {
				converted, err := in.verifyPropertyStep(ctx, unit, stepOwner, stepProp,
					old, updated, name, op.GetStrictTypes(),
					stepOwner.Props.RefHeld(php.StringKey(stepProp)).IsShared())
				if err != nil {
					if target, caught := f.raise(err, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), err
				}
				updated = converted
			}
			if stepStatic != "" {
				converted, err := in.verifyStaticPropStep(ctx, unit, stepStatic, stepProp,
					old, updated, name, op.GetStrictTypes())
				if err != nil {
					if target, caught := f.raise(err, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), err
				}
				updated = converted
			} else if len(slot.Sources()) > 0 {
				converted, err := in.verifyRefStep(ctx, unit, slot, old, updated, name, op.GetStrictTypes())
				if err != nil {
					if target, caught := f.raise(err, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), err
				}
				updated = converted
			}
			slot.Set(updated)
			if strings.HasPrefix(name, "ZEND_POST") {
				f.storeResult(line, old)
			} else {
				f.storeResult(line, updated)
			}
			pc++

		case "ZEND_FETCH_R", "ZEND_FETCH_IS", "ZEND_FETCH_W", "ZEND_FETCH_RW",
			"ZEND_FETCH_FUNC_ARG", "ZEND_FETCH_UNSET":
			// A variable reached by name rather than by slot: a superglobal, or
			// a variable-variable such as $$name. Slots are numbered, so the
			// name is matched against this frame's compiled variables.
			//
			// The name is a *value* being used as a string, so an object goes
			// through __toString and one without it is an Error -- which is
			// what `global ${new stdClass}` earns.
			rendered, nameErr := in.toString(ctx, unit, f.fetch(line.GetOp1Type(), line.GetOp1()))
			if nameErr != nil {
				nameErr = in.materialize(ctx, unit, nameErr)
				if target, caught := f.raise(nameErr, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), nameErr
			}
			varName := rendered.String()
			slot, found := f.slotNamed(varName)
			wantsRef := name == "ZEND_FETCH_W" || name == "ZEND_FETCH_RW" ||
				name == "ZEND_FETCH_FUNC_ARG"
			if line.GetExtendedValue()&fetchTypeMask&(fetchGlobal|fetchGlobalLock) != 0 {
				// $GLOBALS["x"] does not come through as an array access: the
				// compiler turns it into a fetch by name against the global
				// symbol table, which is not this frame's even at top level.
				if name == "ZEND_FETCH_UNSET" {
					// Unsetting a sub-key of a global reaches the slot only when
					// there is one. A name the table does not hold is neither
					// reported nor created: the unset simply finds nothing.
					if _, exists := ctx.Globals.Get(php.StringKey(varName)); !exists {
						f.storeResult(line, php.Null())
						pc++
						continue
					}
					f.bindResult(line, php.MakeRef(ctx.Globals.RefAt(php.StringKey(varName))))
					pc++
					continue
				}
				if wantsRef {
					if name == "ZEND_FETCH_RW" {
						// The RW form is a read-modify-write, so it reads
						// first, and a name the global table does not have is
						// reported -- in the global wording, as a plain read
						// of one would be. The plain W form creates in silence.
						if _, ok := ctx.Globals.Get(php.StringKey(varName)); !ok {
							ctx.Warn("Undefined global variable $%s", varName)
						}
					}
					f.bindResult(line, php.MakeRef(ctx.Globals.RefAt(php.StringKey(varName))))
					pc++
					continue
				}
				value, ok := ctx.Globals.Get(php.StringKey(varName))
				if !ok {
					// The IS form is what isset() and `??` compile to, and it
					// asks rather than reads: a name that is not there is the
					// answer, not a diagnostic.
					if name != "ZEND_FETCH_IS" {
						// The wording differs from the local case, which is
						// the only way to tell the two apart in a test.
						ctx.Warn("Undefined global variable $%s", varName)
					}
					value = php.Null()
				}
				f.storeResult(line, value.Deref())
				pc++
				continue
			}
			if varName == "this" && wantsRef {
				// `$$a = 0` with $a === "this". A write fetch by name is the
				// only way to reach $this as a variable, and the engine
				// refuses it -- inside a class or not. Reading, isset() and
				// unset() are all allowed.
				err := in.throwError(ctx, unit, "Error", "Cannot re-assign $this")
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			held, defined := f.namedValue(varName)
			switch {
			case wantsRef:
				// The box handed out is the *name's*, so a later rebinding --
				// `global $$n` compiles to a pair of these and an ASSIGN_REF --
				// has to reach the name rather than replace the temporary.
				if line.GetResultType() != opUnused {
					if f.writeTargets == nil {
						f.writeTargets = map[uint32]writeTarget{}
					}
					f.writeTargets[line.GetResult()] = writeTarget{frame: f, variable: varName}
				}
				// A read-modify-write reads before it writes, so it complains
				// about a name that exists in the frame but holds nothing.
				if name == "ZEND_FETCH_RW" && !defined {
					ctx.Warn("Undefined variable $%s", varName)
				}
				ref, _ := f.namedRef(varName, true)
				f.bindResult(line, php.MakeRef(ref))
			case defined:
				f.storeResult(line, held.Deref())
			case found:
				f.storeResult(line, f.fetch(opCV, slot))
			case varName == "GLOBALS":
				f.storeResult(line, php.Arr(ctx.Globals))
			default:
				// The diagnostic follows the fetch type, not the syntax. A
				// plain read complains; so does a read-modify-write, which
				// reads before it writes. A pure write creates silently, and
				// the IS and UNSET forms never complain -- the question
				// already admits the answer may be "not set".
				if name == "ZEND_FETCH_R" || name == "ZEND_FETCH_RW" {
					ctx.Warn("Undefined variable $%s", varName)
				}
				f.storeResult(line, php.Null())
			}
			pc++

		case "ZEND_FAST_CALL":
			// finally is compiled as a subroutine: jump into the block, keeping
			// the address to come back to in the result.
			f.storeResult(line, php.Int(int64(pc+1)))
			pc = int(line.GetOp1())

		case "ZEND_DISCARD_EXCEPTION":
			// A return inside finally abandons whatever the try was doing,
			// including an exception that was on its way out -- but only the
			// one belonging to *that* finally. A return from a nested finally
			// emits one of these for each block it leaves, innermost first, and
			// the operand says which: it is the slot that block's FAST_CALL put
			// its return address in, so the opline just before that address is
			// the FAST_CALL, and its target is the block's own start.
			if top, ok := f.topUnwind(); ok {
				resume := int(f.fetch(line.GetOp1Type(), line.GetOp1()).Int())
				if resume > 0 && resume <= len(oplines) {
					entry := oplines[resume-1]
					if in.name(entry.GetOpcode()) == "ZEND_FAST_CALL" &&
						int(entry.GetOp1()) == top.finallyStart {
						f.popUnwind()
					}
				}
			}
			pc++

		case "ZEND_FAST_RET":
			// Leaving the finally block. If it was entered while unwinding, the
			// exception resumes its search rather than the code continuing.
			if top, ok := f.topUnwind(); ok && top.viaFinally {
				f.popUnwind()
				err := top.bail
				if err == nil {
					err = &thrown{object: top.object}
				}
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			pc = int(f.fetch(line.GetOp1Type(), line.GetOp1()).Int())

		case "ZEND_FETCH_LIST_W":
			// The by-reference form of list(): each target binds to the element
			// rather than copying it, so the result is the element's own box.
			// op1 arrives as a reference to the array, made by MAKE_REF.
			// The raw slot, not the fetched value: MAKE_REF put a reference
			// there, and growing a null source into an array has to write
			// through it rather than into a copy.
			holder, _ := f.rawSlot(line.GetOp1Type(), line.GetOp1()).RefOf()
			source := f.fetch(line.GetOp1Type(), line.GetOp1()).Deref()
			if object := source.Object(); object != nil {
				// An ArrayAccess destructured by reference is asked for the
				// offset the way `&$o[$k]` asks: through offsetGet, whose answer
				// is a slot only when the class declared it `&`. Anything else
				// is a value, and binding to it reaches a copy -- which the
				// engine says rather than refuses.
				where, at := ctx.Location()
				element, err := in.readOffset(ctx, unit, object,
					f.fetch(line.GetOp2Type(), line.GetOp2()), false, line, op, depth)
				ctx.SetLocation(where, at)
				if err != nil {
					err = in.materialize(ctx, unit, err)
					if target, caught := f.raise(err, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), err
				}
				if ref, byRef := element.RefOf(); byRef {
					f.bindResult(line, php.MakeRef(ref))
					pc++
					continue
				}
				ctx.Notice("Indirect modification of overloaded element of %s has no effect",
					php.DisplayName(object.ClassName))
				f.bindResult(line, php.MakeRef(php.NewRef(element.Deref().Copy())))
				pc++
				continue
			}
			if source.Kind() != php.KindArray {
				// By reference the source has to *become* an array, so unlike
				// the read form this cannot shrug and hand out null.
				var err error
				switch source.Kind() {
				case php.KindUndef, php.KindNull:
					grown := php.Arr(php.NewArray())
					if holder != nil {
						holder.Set(grown)
					}
					source = grown
				case php.KindString:
					err = in.throwError(ctx, unit, "Error",
						"Cannot create references to/from string offsets")
				default:
					err = in.throwError(ctx, unit, "Error",
						"Cannot use a scalar value as an array")
				}
				if err != nil {
					if target, caught := f.raise(err, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), err
				}
			}
			key := php.ArrayKey(ctx, f.fetch(line.GetOp2Type(), line.GetOp2()))
			if f.borrowed(line.GetOp1Type(), line.GetOp1()) {
				// Destructuring a value nothing else names: there is no slot
				// for the target to share, so it gets a copy and the engine
				// says so rather than refusing.
				held, _ := source.Array().Get(key)
				ctx.Notice("Attempting to set reference to non referenceable value")
				f.bindResult(line, php.MakeRef(php.NewRef(held.Deref().Copy())))
				pc++
				continue
			}
			// The box, not a share of it: a nested `list(list(&$b))` fetches the
			// intermediate element only to descend into it, and marking that
			// one would print an `&` the engine does not. What actually binds
			// -- the ASSIGN_REF at the leaf -- takes the share it needs.
			f.bindResult(line, php.MakeRef(source.Array().RefAt(key)))
			pc++

		case "ZEND_FETCH_LIST_R":
			// list() destructuring reads one element per target. A missing key
			// yields null with a warning rather than failing.
			container := f.fetch(line.GetOp1Type(), line.GetOp1())
			if object := container.Deref().Object(); object != nil {
				// An ArrayAccess destructures through offsetGet, the same way
				// `$o[$k]` reads one element.
				element, err := in.readOffset(ctx, unit, object,
					f.fetch(line.GetOp2Type(), line.GetOp2()), false, line, op, depth)
				if err != nil {
					err = in.materialize(ctx, unit, err)
					if target, caught := f.raise(err, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), err
				}
				f.storeResult(line, element.Deref())
				pc++
				continue
			}
			if container.Deref().Kind() != php.KindArray {
				f.storeResult(line, php.Null())
				pc++
				continue
			}
			index := f.fetch(line.GetOp2Type(), line.GetOp2())
			if offending := illegalOffsetType(index); offending != "" {
				err := in.throwError(ctx, unit, "TypeError",
					"Cannot access offset of type %s on array", offending)
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			element, ok := container.Array().Get(php.ArrayKey(ctx, index))
			if !ok {
				ctx.Warn("Undefined array key %s", quoteKey(index))
				element = php.Null()
			}
			f.storeResult(line, element)
			pc++

		case "ZEND_ASSIGN_OBJ_REF":
			// `$o->p = &$x`: the property joins the box $x shares, replacing
			// whatever binding it had rather than writing through it.
			if pc+1 >= len(oplines) || in.name(oplines[pc+1].GetOpcode()) != "ZEND_OP_DATA" {
				return php.Undef(), fmt.Errorf("%s:%d: ASSIGN_OBJ_REF without OP_DATA",
					op.GetFilename(), line.GetLine())
			}
			data := oplines[pc+1]
			// An unused op1 is $this.
			object, target := f.objectOperand(line, true)
			if object != nil && object.Lazy != nil {
				// Touching a lazy object's state is what produces it. A proxy
				// answers with the instance it forwards to, so everything
				// below works on an ordinary object.
				lazyName, lazyMagic := lazyProperty(f, line, name)
				resolved, lazyErr := in.resolveLazy(ctx, unit, object, lazyName, lazyMagic, depth)
				if lazyErr != nil {
					lazyErr = in.materialize(ctx, unit, lazyErr)
					if handler, caught := f.raise(lazyErr, pc); caught {
						pc = handler
						continue
					}
					return php.Undef(), lazyErr
				}
				object = resolved
				target = php.Obj(object)
			}
			if object == nil {
				return php.Undef(), &UnsupportedError{
					Opcode: "property reference on " + target.Kind().String(),
					Line:   line.GetLine(), File: op.GetFilename(),
				}
			}
			source, ok := f.slotRef(data.GetOp1Type(), data.GetOp1())
			if !ok {
				return php.Undef(), &UnsupportedError{
					Opcode: "reference to a non-variable",
					Line:   line.GetLine(), File: op.GetFilename(),
				}
			}
			property, nameErr := in.propertyName(ctx, unit, f, line.GetOp2Type(), line.GetOp2())
			if nameErr != nil {
				nameErr = in.materialize(ctx, unit, nameErr)
				if target, caught := f.raise(nameErr, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), nameErr
			}
			// A readonly property cannot be bound to either: the binding would
			// let a write reach it from outside the class, which is the same
			// objection `$r = &$o->p` and `$o->p[] = 1` earn.
			// Inside a class that declares its own private property of this
			// name, the name means that class's slot, which is not the one the
			// plain name holds when a subclass declares one too.
			property = propertySlot(object, property, f.class)
			if refused, _ := in.indirectWriteRefused(ctx, unit, object, property, f.class); refused != nil {
				if handler, caught := f.raise(refused, pc); caught {
					pc = handler
					continue
				}
				return php.Undef(), refused
			}
			// A hooked property is never a slot to bind, whatever its hooks do
			// -- even a `&get` that hands one out, since the binding would
			// replace the property rather than reach through it. Which refusal
			// it earns depends on whether there is a get hook at all: the
			// engine *runs* one first and only then refuses the binding, while
			// a set-only property has nothing to read and says so differently.
			if hooked, declaring, isHooked := unit.hooksFor(f, object, property); isHooked {
				var err error
				if hooked.GetGetHook() == nil {
					err = in.throwError(ctx, unit, "Error",
						"Indirect modification of %s::$%s is not allowed",
						php.DisplayName(declaring.Name), property)
				} else {
					if _, err = in.callGetHook(ctx, unit, object, hooked, declaring, depth); err == nil {
						err = in.throwError(ctx, unit, "Error",
							"Cannot assign by reference to overloaded object")
					}
					err = in.materialize(ctx, unit, err)
				}
				if handler, caught := f.raise(err, pc); caught {
					pc = handler
					continue
				}
				return php.Undef(), err
			}
			// A binding needs a slot of the object's own to bind. A name the
			// caller cannot see, or one that only exists through __get, has
			// none -- and the engine refuses rather than quietly making a
			// dynamic property beside the overloaded one.
			if !unit.bindableProperty(object, property, f.class) {
				var err error
				if unit.forbidsDynamic(object, property) {
					// The binding would grow the object, and this one does not
					// grow -- which is a different refusal from not being able
					// to see the name.
					err = in.throwError(ctx, unit, "Error",
						"Cannot create dynamic property %s::$%s",
						php.DisplayName(object.ClassName), property)
					if handler, caught := f.raise(err, pc); caught {
						pc = handler
						continue
					}
					return php.Undef(), err
				}
				if handled, _, magicErr := in.overloadedWriteFetch(ctx, unit, object,
					property, f.class, op.GetStrictTypes(), depth); magicErr != nil {
					err = magicErr
				} else if handled {
					// The accessor ran -- and warned, if it hands back a value
					// rather than a slot -- but there is still nothing to bind.
					err = in.throwError(ctx, unit, "Error",
						"Cannot assign by reference to overloaded object")
				} else {
					prop, _ := unit.propertyAccess(object, property, f.class)
					err = in.deniedPropertyError(ctx, unit, object, prop, property)
				}
				if handler, caught := f.raise(err, pc); caught {
					pc = handler
					continue
				}
				return php.Undef(), err
			}
			if _, present := object.Get(property); !present {
				if unit.forbidsDynamic(object, property) {
					err := in.throwError(ctx, unit, "Error",
						"Cannot create dynamic property %s::$%s",
						php.DisplayName(object.ClassName), property)
					if handler, caught := f.raise(err, pc); caught {
						pc = handler
						continue
					}
					return php.Undef(), err
				}
				// The binding creates the property, and creating one the class
				// never declared is deprecated like any other.
				_, verdict := unit.propertyAccess(object, property, f.class)
				unit.noteDynamicProperty(ctx, object, property, verdict)
			}
			// The property stops sharing the box it holds now -- and stops
			// lending that box its type -- before it starts sharing the one it
			// is about to hold. Rebinding a property to the box it already
			// holds therefore moves it to the end of that box's sources, which
			// is the order the engine names them in.
			if held, present := object.Props.Raw(php.StringKey(property)); present {
				if old, wasRef := held.RefOf(); wasRef {
					owner := object.ClassName
					if declared, ok := object.Declared[property]; ok && declared.Class != "" {
						owner = declared.Class
					}
					old.DropSource(owner, property)
					php.Unshare(held)
				}
			}
			// Binding to a *typed* property makes the reference typed: every
			// name sharing the box now has to satisfy the declaration, which is
			// why `$y = 42` through such a binding stores the string "42".
			//
			// The value already in the box has to satisfy it too, since the
			// binding is itself a write to the property -- and it is reported as
			// one. "reference held by" is what a write *through* an established
			// binding says; this is the binding being made, so the check runs
			// before the box learns the type.
			if declared, isDeclared := object.Declared[property]; isDeclared &&
				declared.Type != "" && !object.Dynamic[property] {
				owner := declared.Class
				if owner == "" {
					owner = object.ClassName
				}
				if err := in.refBindingConflict(ctx, unit, source, owner, property,
					declared.Type, op.GetStrictTypes()); err != nil {
					if handler, caught := f.raise(err, pc); caught {
						pc = handler
						continue
					}
					return php.Undef(), err
				}
				converted, err := in.verifyPropertyWrite(ctx, unit, object, property,
					source.Get(), op.GetStrictTypes())
				if err != nil {
					if handler, caught := f.raise(err, pc); caught {
						pc = handler
						continue
					}
					return php.Undef(), err
				}
				source.Set(converted)
				notePropertyType(object, property, source)
			}
			if f.borrowed(data.GetOp1Type(), data.GetOp1()) {
				// The source is a box the engine made over a value with no home
				// -- a function's by-value result. Binding a property to one is
				// legal and pointless, and PHP says so, exactly as it does for
				// `$a = &f()`. It is said only once the binding is going ahead:
				// an overloaded property refuses first and says nothing of this.
				ctx.Notice("Only variables should be assigned by reference")
			}
			// The rebinding drops whatever the property held, and PHP runs its
			// destructor here -- where it can still write through the box the
			// assignment just made.
			released, _ := object.Get(property)
			// One name for the box when the source was borrowed, so not one
			// var_dump marks. sharedRef has to stay unevaluated in that case:
			// it marks the box as it builds the value.
			bound := php.MakeRef(source)
			if !f.borrowed(data.GetOp1Type(), data.GetOp1()) {
				bound = sharedRef(source)
			}
			object.Props.Bind(php.StringKey(property), bound)
			if lazyErr := in.releaseCheck(ctx, unit, released); lazyErr != nil {
				lazyErr = in.materialize(ctx, unit, lazyErr)
				if handler, caught := f.raise(lazyErr, pc); caught {
					pc = handler
					continue
				}
				return php.Undef(), lazyErr
			}
			if line.GetResultType() != opUnused {
				// The expression answers with the property, read through the
				// box -- and *after* the release above, because the destructor
				// that runs there can write through that very box. That is what
				// makes `var_dump($box->value = &$tmp)` print NULL when the old
				// value's destructor sets the property to null.
				f.bindResult(line, php.MakeRef(source))
			}
			pc += 2

		case "ZEND_IN_ARRAY":
			// in_array() has its own opcode. extended_value carries the strict
			// flag, which decides whether comparison is === or ==.
			needle := f.fetch(line.GetOp1Type(), line.GetOp1())
			haystack := f.fetch(line.GetOp2Type(), line.GetOp2())
			if haystack.Kind() != php.KindArray {
				return php.Undef(), &UnsupportedError{
					Opcode: "in_array over " + haystack.Kind().String(),
					Line:   line.GetLine(), File: op.GetFilename(),
				}
			}
			strict := line.GetExtendedValue() != 0
			found := false
			haystack.Array().Each(func(_ php.Key, v php.Value) bool {
				if (strict && php.StrictEqual(v, needle)) || (!strict && php.LooseEqual(v, needle)) {
					found = true
					return false
				}
				return true
			})
			f.storeResult(line, php.Bool(found))
			pc++

		case "ZEND_ARRAY_KEY_EXISTS":
			// array_key_exists() has its own opcode. Unlike isset(), it reports
			// true for a key whose value is null.
			container := f.fetch(line.GetOp2Type(), line.GetOp2()).Deref()
			if container.Kind() != php.KindArray {
				// The opcode is the specialised form of the call, and carries
				// the same argument diagnostic.
				err := in.throwError(ctx, unit, "TypeError",
					"array_key_exists(): Argument #2 ($array) must be of type array, %s given",
					givenTypeName(container))
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			_, exists := container.Array().Get(php.ArrayKey(ctx, f.fetch(line.GetOp1Type(), line.GetOp1())))
			f.storeResult(line, php.Bool(exists))
			pc++

		case "ZEND_JMP":
			pc = int(line.GetOp1())

		case "ZEND_JMPZ":
			if f.fetch(line.GetOp1Type(), line.GetOp1()).Bool() {
				pc++
			} else {
				pc = int(line.GetOp2())
			}

		case "ZEND_JMPNZ":
			if f.fetch(line.GetOp1Type(), line.GetOp1()).Bool() {
				pc = int(line.GetOp2())
			} else {
				pc++
			}

		case "ZEND_COUNT", "ZEND_STRLEN", "ZEND_GET_TYPE", "ZEND_GET_CLASS":
			// The compiler specializes a few builtins into dedicated opcodes,
			// so they never reach the call path -- including the argument type
			// check it performs, which has to be repeated here.
			callee := specializedBuiltin(name)
			if line.GetOp1Type() == opUnused {
				// get_class() with no argument names the class whose body is
				// running -- the one that declared the method, not the
				// object's. It is the only specialized form with no operand.
				if f.class == nil {
					err := in.throwError(ctx, unit, "Error",
						"get_class() without arguments must be called from within a class")
					if target, caught := f.raise(err, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), err
				}
				ctx.Deprecated("Calling get_class() without arguments is deprecated")
				f.storeResult(line, php.String(f.class.Name))
				pc++
				continue
			}
			args := []php.Value{f.fetch(line.GetOp1Type(), line.GetOp1())}
			if callee == "count" {
				// A Countable counts itself, which the ctx-only builtin cannot
				// ask it to do.
				if counted, handled, err := in.countOf(ctx, unit, args[0], depth); handled {
					if err != nil {
						if target, caught := f.raise(in.materialize(ctx, unit, err), pc); caught {
							pc = target
							continue
						}
						return php.Undef(), err
					}
					f.storeResult(line, counted)
					pc++
					continue
				}
			}
			if err := in.verifyBuiltinArgs(ctx, unit, callee, args, op.GetStrictTypes()); err != nil {
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			result, err := builtins[callee](ctx, args)
			if err != nil {
				return php.Undef(), err
			}
			f.storeResult(line, result)
			pc++

		case "ZEND_FE_RESET_R", "ZEND_FE_RESET_RW":
			subject := f.fetch(line.GetOp1Type(), line.GetOp1())
			if held := subject.Deref().Object(); held != nil && held.Lazy != nil {
				// Iterating an object walks its properties, which have to
				// exist -- and for a proxy they are the instance's, so the
				// loop runs over what the proxy forwards to.
				resolved, lazyErr := in.resolveLazy(ctx, unit, held, "", "", depth)
				if lazyErr != nil {
					lazyErr = in.materialize(ctx, unit, lazyErr)
					if handler, caught := f.raise(lazyErr, pc); caught {
						pc = handler
						continue
					}
					return php.Undef(), lazyErr
				}
				subject = php.Obj(resolved)
			}

			// A by-reference loop over an object binds to the object's own
			// slots, so which object the flattened property list came from has
			// to travel with the iterator.
			var origin *php.Object
			var deferredHooks map[string]deferredHook
			var hookedObject *php.Object
			if g, isGenerator := asGenerator(subject); isGenerator {
				// Whether a generator is empty cannot be known without running
				// it, so the loop-exit branch is only taken after starting it.
				if err := in.materialize(ctx, unit, in.start(ctx, unit, g)); err != nil {
					if target, caught := f.raise(err, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), err
				}
				if !g.valid() {
					pc = int(line.GetOp2())
					continue
				}
				f.storeResult(line, subject)
				if f.iterators == nil {
					f.iterators = map[uint32]*iterator{}
				}
				f.iterators[line.GetResult()] = &iterator{gen: g, returned: php.Null()}
				pc++
				continue
			}
			if subject.Kind() == php.KindObject {
				// A Traversable drives the loop itself; anything else visits
				// its properties, and only the ones visible from the current
				// scope -- outside the class that means the public ones.
				delegate, ok, err := in.newObjectIterator(ctx, unit, subject, depth)
				if err != nil {
					if target, caught := f.raise(in.materialize(ctx, unit, err), pc); caught {
						pc = target
						continue
					}
					return php.Undef(), err
				}
				if ok {
					// The reset rewinds and asks valid(), and nothing more: the
					// engine leaves current() and key() to the first fetch, and
					// an Iterator whose key() raises in a loop that never reads
					// one must not see it called.
					more, err := in.iterStart(ctx, unit, delegate)
					if err != nil {
						if target, caught := f.raise(in.materialize(ctx, unit, err), pc); caught {
							pc = target
							continue
						}
						return php.Undef(), err
					}
					if !more {
						pc = int(line.GetOp2())
						continue
					}
					f.storeResult(line, subject)
					if f.iterators == nil {
						f.iterators = map[uint32]*iterator{}
					}
					f.iterators[line.GetResult()] = delegate
					pc++
					continue
				}
				if _, isClosure := asClosure(subject); isClosure {
					return php.Undef(), &UnsupportedError{
						Opcode: "foreach over Closure",
						Line:   line.GetLine(), File: op.GetFilename(),
					}
				}
				origin = subject.Object()
				// A get hook runs at the turn its property comes up, not when
				// the loop starts: a hook that prints shows one line per
				// iteration, interleaved with the body. Only a class that has
				// hooks takes the deferred path.
				if props, hooks := in.lazyVisibleProps(unit, f, origin, f.class); props != nil {
					deferredHooks, hookedObject = hooks, origin
					subject = php.Arr(props)
				} else {
					props, hookErr := in.hookedVisibleProps(ctx, unit, f, origin, f.class, depth, true)
					if hookErr != nil {
						hookErr = in.materialize(ctx, unit, hookErr)
						if handler, caught := f.raise(hookErr, pc); caught {
							pc = handler
							continue
						}
						return php.Undef(), hookErr
					}
					subject = php.Arr(props)
				}
			}
			if subject.Kind() != php.KindArray {
				// Anything that is neither an array nor an object is a warning
				// and an empty loop, not an error.
				ctx.Warn("foreach() argument must be of type array|object, %s given",
					givenTypeName(subject))
				pc = int(line.GetOp2())
				continue
			}
			// Iterate a snapshot: PHP's by-value foreach is unaffected by
			// changes made to the array inside the loop.
			keys := subject.Array().Keys()
			if len(keys) == 0 {
				pc = int(line.GetOp2())
				continue
			}
			f.storeResult(line, subject)
			if f.iterators == nil {
				f.iterators = map[uint32]*iterator{}
			}
			it := &iterator{
				array: subject.Array(), keys: keys, origin: origin,
				live:          name == "ZEND_FE_RESET_RW" && origin == nil,
				byRef:         name == "ZEND_FE_RESET_RW",
				scope:         f.class,
				deferredHooks: deferredHooks, hookedObject: hookedObject,
			}
			if name == "ZEND_FE_RESET_RW" && line.GetOp1Type() == opCV {
				// A by-reference loop is bound to the *variable*, not to the
				// array it held: the engine makes it a reference and reads it
				// again every turn. Assigning something that is not iterable
				// ends the loop with a warning; unsetting the name does not,
				// since the reference outlives it.
				it.source, it.hasSource = line.GetOp1(), true
			}
			if it.live {
				// Rebuilding the array has to move the position, so the array
				// is told about it -- and told to forget it when the loop ends.
				it.release = subject.Array().RegisterCursor(&it.pos)
			}
			f.iterators[line.GetResult()] = it
			pc++

		case "ZEND_FE_FETCH_R", "ZEND_FE_FETCH_RW":
			it, ok := f.iterators[line.GetOp1()]
			if !ok {
				return php.Undef(), fmt.Errorf("%s:%d: foreach fetch without a live iterator",
					op.GetFilename(), line.GetLine())
			}
			// A foreach with no key variable never asks the iterator for one,
			// which a user Iterator can tell: `foreach ($it as $v)` does not
			// call key() at all, so a key() that would raise does not. A
			// by-reference loop is the exception here: php2go finds the slot to
			// bind *by* the key, so it needs one whether the program does or
			// not.
			if it.hasSource {
				// Undef is the variable having been unset, which drops the name
				// without disturbing what the loop holds.
				current := f.fetchQuiet(opCV, it.source).Deref()
				if !current.IsUndef() && current.Kind() != php.KindArray && current.Object() == nil {
					ctx.Warn("foreach() argument must be of type array|object, %s given",
						givenTypeName(current))
					pc = int(line.GetExtendedValue())
					continue
				}
			}
			it.skipKey = line.GetResultType() == opUnused && name != "ZEND_FE_FETCH_RW"
			key, value, more, err := in.iterNext(ctx, unit, it)
			// Where a *slot* is being bound the walk is over an array or an
			// object's properties, so the key really is an array key and
			// converting it cannot lose anything or report anything.
			slotKey := func() php.Key { return php.ArrayKey(ctx, key) }
			if err = in.materialize(ctx, unit, err); err != nil {
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			if !more {
				// extended_value holds the loop-exit target for this opcode,
				// because op2 is taken by the value destination.
				pc = int(line.GetExtendedValue())
				continue
			}
			// op2 receives the value and the result receives the key -- the
			// reverse of the source order `as $k => $v` suggests. The result is
			// only written when a key variable was actually written.
			if name == "ZEND_FE_FETCH_RW" {
				// By-reference iteration binds the loop variable to the element
				// itself, so writing to it changes the array. A generator has no
				// element to point at unless it declared that it yields by
				// reference, which PHP rejects rather than silently copying.
				if it.gen != nil && it.gen.yieldsByRef() {
					// A generator declared `function &gen()` hands out the slot
					// it yielded, so the loop variable binds to it.
					if ref, isRef := value.RefOf(); isRef {
						// Moving to the next yield drops the previous binding,
						// exactly as it does for an array: only the slot the
						// loop variable is on now has it as a holder.
						php.Unshare(f.rawSlot(line.GetOp2Type(), line.GetOp2()))
						f.bindSlot(line.GetOp2Type(), line.GetOp2(), sharedRef(ref))
						if line.GetResultType() != opUnused {
							f.storeResult(line, key)
						}
						pc++
						continue
					}
				}
				if it.array == nil && it.backing != nil {
					// An engine iterator: the walk goes through its Go methods,
					// but the slot to bind is in the array behind it. When
					// those slots are an object's properties, a readonly one
					// has no reference to give -- `foreach ($o as &$v)` over an
					// ArrayIterator wrapping $o is refused the same way
					// `$r = &$o->readonly` is.
					if it.backingOwner != nil {
						if owner, refuses := unit.readonlyProperty(it.backingOwner, key.String()); refuses {
							err := in.throwError(ctx, unit, "Error",
								"Cannot acquire reference to readonly property %s::$%s",
								php.DisplayName(owner), key.String())
							if target, caught := f.raise(err, pc); caught {
								pc = target
								continue
							}
							return php.Undef(), err
						}
					}
					php.Unshare(f.rawSlot(line.GetOp2Type(), line.GetOp2()))
					f.bindSlot(line.GetOp2Type(), line.GetOp2(),
						sharedRef(notePropertyType(it.backingOwner, key.String(),
							it.backing.RefAt(slotKey()))))
					if line.GetResultType() != opUnused {
						f.storeResult(line, key)
					}
					pc++
					continue
				}
				if it.weak != nil {
					// A WeakMap's values are slots like an array's, so a
					// by-reference loop binds to them and a write inside the
					// loop reaches the map.
					php.Unshare(f.rawSlot(line.GetOp2Type(), line.GetOp2()))
					f.bindSlot(line.GetOp2Type(), line.GetOp2(),
						sharedRef(it.weak.RefAt(it.pos-1)))
					if line.GetResultType() != opUnused {
						f.storeResult(line, key)
					}
					pc++
					continue
				}
				if it.array == nil {
					// A plain Exception, not an Error: the engine throws this
					// one from the iterator handler rather than the VM.
					err := in.throwError(ctx, unit, "Exception", "You can only iterate a generator "+
						"by-reference if it declared that it yields by-reference")
					if target, caught := f.raise(err, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), err
				}
				if hook, deferred := it.deferredHooks[key.String()]; deferred {
					// A hooked property has no slot of its own to bind. A
					// `&get` hands back the box it wants aliased -- writing
					// through the loop variable then reaches whatever the hook
					// returned -- and a by-value one has nothing to alias, so
					// the value it produced is bound in a box of its own.
					// The fetch already ran the hook -- once per turn is what
					// the engine does -- so what it produced is what binds.
					_ = hook
					php.Unshare(f.rawSlot(line.GetOp2Type(), line.GetOp2()))
					read := value
					if ref, isRef := read.RefOf(); isRef {
						f.bindSlot(line.GetOp2Type(), line.GetOp2(), sharedRef(ref))
					} else {
						f.bindSlot(line.GetOp2Type(), line.GetOp2(),
							sharedRef(php.NewRef(read.Deref())))
					}
					if line.GetResultType() != opUnused {
						f.storeResult(line, key)
					}
					pc++
					continue
				}
				if it.origin != nil {
					// A readonly property cannot be aliased at all: the loop
					// would be able to write it from outside its class.
					if declaring, readonly := unit.readonlyProperty(it.origin, key.String()); readonly {
						err := in.throwError(ctx, unit, "Error",
							"Cannot acquire reference to readonly property %s::$%s",
							php.DisplayName(declaring), key.String())
						if target, caught := f.raise(err, pc); caught {
							pc = target
							continue
						}
						return php.Undef(), err
					}
					// Moving to the next element drops the loop variable's
					// previous binding, so the element it had is shared by one
					// name fewer.
					php.Unshare(f.rawSlot(line.GetOp2Type(), line.GetOp2()))
					// Iterating by reference binds the property's own box,
					// so the loop variable is checked against the property's
					// declared type for as long as it holds it.
					f.bindSlot(line.GetOp2Type(), line.GetOp2(),
						sharedRef(notePropertyType(it.origin, key.String(),
							it.origin.Props.RefAt(slotKey()))))
				} else {
					php.Unshare(f.rawSlot(line.GetOp2Type(), line.GetOp2()))
					f.bindSlot(line.GetOp2Type(), line.GetOp2(), sharedRef(it.array.RefAt(slotKey())))
				}
			} else {
				previous := f.fetchQuiet(line.GetOp2Type(), line.GetOp2())
				f.store(line.GetOp2Type(), line.GetOp2(), value.Copy())
				// Writing the loop variable lets go of what it held, and an
				// object with a destructor can tell: `foreach ([1, 2] as $v)`
				// over a `$v` that held one runs that destructor at the *first*
				// fetch, so what it throws stops the loop before the body ever
				// runs.
				if err := in.releaseCheck(ctx, unit, previous); err != nil {
					if target, caught := f.raise(err, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), err
				}
			}
			if line.GetResultType() != opUnused {
				f.storeResult(line, key)
			}
			pc++

		case "ZEND_FE_FREE":
			// The loop is over, so both the iterator state and the temporary
			// holding what was being looped over are released. Leaving the
			// temporary behind would keep a generator -- and everything its
			// suspended frame holds -- reachable after the loop.
			if held, ok := f.iterators[line.GetOp1()]; ok && held.release != nil {
				held.release()
			}
			delete(f.iterators, line.GetOp1())
			released := f.fetch(line.GetOp1Type(), line.GetOp1())
			f.bindSlot(line.GetOp1Type(), line.GetOp1(), php.Undef())
			// The engine *releases* what was being looped over here, so a
			// destructor among its elements runs at the end of the loop rather
			// than at the end of the request.
			if err := in.releaseCheck(ctx, unit, released); err != nil {
				// Where that exception counts as having been thrown is not
				// where this opline sits. A `break`, `continue` or `return`
				// leaving the loop emits its own FE_FREE inside whatever try
				// the jump was written in, and the engine moves the throw to
				// the loop's own final free instead:
				//
				//	if ((throw_op->opcode == ZEND_FREE || throw_op->opcode == ZEND_FE_FREE)
				//	 && throw_op->extended_value & ZEND_FREE_ON_RETURN) {
				//		new_throw_op_num = throw_op_num + throw_op->op2.opline_num;
				//
				// op2 is a *relative* offset, and the opline it names is
				// outside the try the jump was written in -- which is why
				// `break 2` out of a `foreach (new A ...)` is caught by the
				// outer catch and not the inner one.
				at := pc
				if line.GetExtendedValue()&freeOnReturn != 0 {
					at = pc + int(line.GetOp2())
				}
				if target, caught := f.raise(err, at); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			pc++

		case "ZEND_INIT_STATIC_METHOD_CALL":
			className, err := in.classNameOperand(ctx, unit, f, line, line.GetOp1Type(), line.GetOp1())
			if err != nil {
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			// An unused op2 means the constructor: `parent::__construct()` is
			// compiled without a name because the engine takes it from the
			// class entry rather than looking it up.
			methodName, methodNameValue := "__construct", php.Undef()
			if line.GetOp2Type() != opUnused {
				methodNameValue = f.fetch(line.GetOp2Type(), line.GetOp2())
				methodName = methodNameOf(methodNameValue)
			}
			class, ok, loadErr := in.resolveClass(ctx, unit, className, depth)
			if loadErr != nil {
				if handler, caught := f.raise(loadErr, pc); caught {
					pc = handler
					continue
				}
				return php.Undef(), loadErr
			}
			unit.noteTraitStaticAccess(ctx, className, methodName, false)
			if !ok {
				err := in.throwError(ctx, unit, "Error", "Class \"%s\" not found", className)
				if handler, caught := f.raise(err, pc); caught {
					pc = handler
					continue
				}
				return php.Undef(), err
			}
			// Unlike the object form, the class is settled first: `Missing::$m()`
			// reports the class, and only a class that exists gets as far as
			// asking what the name is.
			if !methodNameValue.IsUndef() {
				if _, nameErr := methodNameOperand(methodNameValue); nameErr != nil {
					nameErr = in.materialize(ctx, unit, nameErr)
					if target, caught := f.raise(nameErr, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), nameErr
				}
			}
			if strings.EqualFold(className, "WeakReference") {
				calls = append(calls, &pendingCall{
					name: "WeakReference::" + methodName, weakStatic: methodName,
				})
				pc++
				continue
			}
			if strings.EqualFold(className, "Fiber") && !strings.EqualFold(methodName, "__construct") {
				calls = append(calls, &pendingCall{
					name: "Fiber::" + methodName, fiberStatic: methodName,
				})
				pc++
				continue
			}
			if strings.EqualFold(className, "Closure") {
				calls = append(calls, &pendingCall{
					name: "Closure::" + methodName, closureStatic: methodName,
				})
				pc++
				continue
			}
			if value, handled, err := in.enumStaticCall(ctx, unit, class, methodName, nil, depth); handled {
				// cases(), from() and tryFrom() take their arguments through the
				// usual SEND_* oplines, so the call is queued rather than run.
				_ = value
				_ = err
				calls = append(calls, &pendingCall{
					name: className + "::" + methodName,
					enum: class, enumMethod: methodName,
				})
				pc++
				continue
			}
			method, declaring, found := unit.resolveMethod(class, methodName)
			if !found && line.GetOp2Type() == opUnused && f.this != nil &&
				unit.isBuiltinOnly(class) && unit.isSubclassOf(f.this.ClassName, class.Name) {
				// `parent::__construct()` where the parent is an engine class
				// this runtime implements in Go. There is no harvested body to
				// resolve, so the call goes to the same Go implementation the
				// instantiation would have used.
				calls = append(calls, &pendingCall{
					name: className + "::__construct", this: f.this,
					builtinMethod: "__construct",
				})
				pc++
				continue
			}
			if !found && line.GetOp2Type() == opUnused {
				// parent::__construct() against a parent that declares none.
				// The message names no method, since the source named none.
				err := in.throwError(ctx, unit, "Error", "Cannot call constructor")
				if handler, caught := f.raise(err, pc); caught {
					pc = handler
					continue
				}
				return php.Undef(), err
			}
			if !found {
				// A static-syntax call still reaches __call when there is a
				// $this that belongs to the class: the engine looks for that
				// before it looks for __callStatic.
				if forwarded, ok := unit.forwardStaticToCall(f, class, className, methodName); ok {
					calls = append(calls, forwarded)
					pc++
					continue
				}
				// __callStatic is the static counterpart of __call, and takes
				// its arguments the same way.
				if body, magicDeclaring, ok := unit.resolveMethod(class, magicCallStat); ok {
					if body.GetAbstract() {
						// An interface may *declare* __callStatic without a
						// body. There is nothing to forward to, and the engine
						// reports the name that was called rather than the
						// accessor that would have answered.
						err := in.throwError(ctx, unit, "Error",
							"Cannot call abstract method %s::%s()",
							php.DisplayName(class.Name), methodName)
						if handler, caught := f.raise(err, pc); caught {
							pc = handler
							continue
						}
						return php.Undef(), err
					}
					calls = append(calls, &pendingCall{
						name: className + "::" + methodName, method: body,
						class: magicDeclaring, called: class, forwardTo: methodName, forwarding: true,
					})
					pc++
					continue
				}
				if !unit.isBuiltinOnly(class) {
					err := in.throwError(ctx, unit, "Error",
						"Call to undefined method %s::%s()", php.DisplayName(class.Name), methodName)
					if handler, caught := f.raise(err, pc); caught {
						pc = handler
						continue
					}
					return php.Undef(), err
				}
				// parent::rewind() where the parent is an engine class this
				// runtime implements in Go: there is no harvested body to
				// resolve, so the call goes to the same Go implementation an
				// instance call would reach, on the receiver already in hand.
				if f.this != nil && unit.isSubclassOf(f.this.ClassName, class.Name) {
					calls = append(calls, &pendingCall{
						name: className + "::" + methodName, this: f.this,
						builtinMethod: methodName,
					})
					pc++
					continue
				}
				return php.Undef(), &UnsupportedError{
					Opcode: "static method " + className + "::" + methodName,
					Line:   line.GetLine(), File: op.GetFilename(),
				}
			}
			if !unit.methodAccessibleAs(method, declaring, f.class, methodName) {
				if forwarded, ok := unit.forwardStaticToCall(f, class, className, methodName); ok {
					calls = append(calls, forwarded)
					pc++
					continue
				}
				// __callStatic is the static counterpart, and answers for an
				// inaccessible method as well as a missing one.
				if body, magicDeclaring, ok := unit.resolveMethod(class, magicCallStat); ok {
					if body.GetAbstract() {
						// An interface may *declare* __callStatic without a
						// body. There is nothing to forward to, and the engine
						// reports the name that was called rather than the
						// accessor that would have answered.
						err := in.throwError(ctx, unit, "Error",
							"Cannot call abstract method %s::%s()",
							php.DisplayName(class.Name), methodName)
						if handler, caught := f.raise(err, pc); caught {
							pc = handler
							continue
						}
						return php.Undef(), err
					}
					calls = append(calls, &pendingCall{
						name: className + "::" + methodName, method: body,
						class: magicDeclaring, called: class, forwardTo: methodName, forwarding: true,
					})
					pc++
					continue
				}
				err := in.throwError(ctx, unit, "Error", "Call to %s method %s::%s() from %s",
					unit.effectiveVisibility(declaring, method, methodName),
					php.DisplayName(declaring.Name), methodName, scopeName(f.class))
				if handler, caught := f.raise(err, pc); caught {
					pc = handler
					continue
				}
				return php.Undef(), err
			}
			if method.GetAbstract() {
				err := in.throwError(ctx, unit, "Error",
					"Cannot call abstract method %s::%s()", php.DisplayName(declaring.Name), methodName)
				if handler, caught := f.raise(err, pc); caught {
					pc = handler
					continue
				}
				return php.Undef(), err
			}
			// self::foo() from inside an instance method keeps $this bound;
			// PHP only drops it when the callee is static or when there is no
			// compatible instance. A static method never has a receiver, and a
			// trace names the call "A::s" rather than "A->s" because of it.
			this := f.this
			if method.GetStatic() ||
				(this != nil && !unit.instanceOf(class, this.ClassName) && !unit.isSubclassOf(this.ClassName, class.Name)) {
				this = nil
			}
			if !method.GetStatic() && this == nil {
				// Static syntax reaching an instance method with no compatible
				// instance to call it on. Probed: `self::m()` and `C::m()` from
				// inside a method of C run with $this, and so does `$obj::m()`
				// there; from a static method, from another class, or at top
				// level -- including `$obj::m()` -- the engine refuses.
				err := in.throwError(ctx, unit, "Error",
					"Non-static method %s::%s() cannot be called statically",
					php.DisplayName(class.Name), methodName)
				if handler, caught := f.raise(err, pc); caught {
					pc = handler
					continue
				}
				return php.Undef(), err
			}
			// A call written as self::, parent:: or static:: *forwards* late
			// static binding rather than rebinding it: `parent::who()` inside
			// B::who() called as D::who() still reports D. Only a call that
			// names a class outright rebinds.
			called := class
			if f.forwardsStaticBinding(line) {
				if name, ok := f.calledClass(); ok {
					if forwarded, found := unit.findClass(name); found {
						called = forwarded
					}
				}
			}
			calls = append(calls, &pendingCall{
				name: className + "::" + methodName, method: method,
				// class is where the body was declared, called is what the
				// call site named: static:: inside an inherited method has to
				// see the subclass.
				this: this, class: declaring, called: called,
			})
			pc++

		case "ZEND_DECLARE_CLASS", "ZEND_DECLARE_CLASS_DELAYED":
			// Classes are harvested with the unit, so a declaration only has to
			// confirm the class is known rather than build it here.
			name := f.fetch(line.GetOp1Type(), line.GetOp1()).String()
			declared, ok := unit.findClass(name)
			if !ok {
				return php.Undef(), &UnsupportedError{
					Opcode: "declaration of class " + name,
					Line:   line.GetLine(),
					File:   op.GetFilename(),
				}
			}
			if err := in.bindDeclaration(ctx, unit, declared); err != nil {
				return php.Undef(), err
			}
			// Linking needs the parent, the interfaces and the traits, and
			// this is where the engine discovers it has not seen one of them.
			if err := in.resolveDependencies(ctx, unit, declared); err != nil {
				// The declaration never took: PHP unlinks a class whose
				// dependencies could not be obtained, so the same declaration
				// may be attempted again.
				unit.unbindDeclaration(declared)
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			if err := in.linkClass(ctx, unit, declared); err != nil {
				return php.Undef(), err
			}
			pc++

		case "ZEND_NEW":
			// `new static()` and `new self()` carry the class reference in an
			// unused op1 rather than as a name, the same encoding FETCH_CLASS
			// uses.
			name, err := in.classNameOperand(ctx, unit, f, line, line.GetOp1Type(), line.GetOp1())
			if err != nil {
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			class, ok, loadErr := in.resolveClass(ctx, unit, name, depth)
			if loadErr != nil {
				if handler, caught := f.raise(loadErr, pc); caught {
					pc = handler
					continue
				}
				return php.Undef(), loadErr
			}
			if !ok {
				// A name neither the program nor the engine declares is a
				// program error rather than a gap here -- and a catchable one.
				// A class that exists but is not modelled still reports as
				// unsupported, below, so the metric stays honest.
				err := in.throwError(ctx, unit, "Error", "Class \"%s\" not found", name)
				if handler, caught := f.raise(err, pc); caught {
					pc = handler
					continue
				}
				return php.Undef(), err
			}
			// An engine class the runtime does not model would instantiate into
			// an object with no behaviour, which produces plausible but wrong
			// output instead of an honest gap. Only the ones actually
			// implemented may be created.
			if unit.isBuiltinOnly(class) && !unit.isThrowable(class) && class.Name != "stdClass" &&
				!isArrayBackedClass(unit, class.Name) &&
				!reflectionClasses[strings.ToLower(class.Name)] &&
				!weakClasses[strings.ToLower(class.Name)] &&
				!fiberClasses[strings.ToLower(class.Name)] &&
				!strings.EqualFold(class.Name, "SensitiveParameterValue") &&
				!strings.EqualFold(class.Name, "Deprecated") &&
				!strings.EqualFold(class.Name, "Directory") &&
				!isFilterIteratorClass(unit, class.Name) &&
				!isEmptyIteratorClass(unit, class.Name) {
				return php.Undef(), &UnsupportedError{
					Opcode: "instantiation of " + class.Name,
					Line:   line.GetLine(), File: op.GetFilename(),
				}
			}
			// An interface, a trait, an enum and an abstract class have no
			// instances, and each is refused in its own words.
			if refusal := instantiable(class); refusal != nil {
				err := in.materialize(ctx, unit, refusal)
				if handler, caught := f.raise(err, pc); caught {
					pc = handler
					continue
				}
				return php.Undef(), err
			}
			// A constructor the calling scope cannot reach refuses before the
			// object exists at all, so nothing is created and no destructor
			// runs for it.
			if body, declaring, found := unit.resolveMethod(class, "__construct"); found &&
				!unit.methodAccessible(body, declaring, f.class) {
				// The class named is the one that *declared* the constructor,
				// which an inherited private one makes different from the
				// class being instantiated: `new Y` on a private X::__construct
				// says X.
				owner := class
				if declaring != nil {
					owner = declaring
				}
				err := in.throwError(ctx, unit, "Error", "Call to %s %s::__construct() from %s",
					body.GetVisibility(), php.DisplayName(owner.Name), scopeName(f.class))
				if handler, caught := f.raise(err, pc); caught {
					pc = handler
					continue
				}
				return php.Undef(), err
			}
			// Instantiating a class resolves its deferred initializers, which
			// is where an autoload named by one of them happens -- before the
			// object exists, and before the constructor runs.
			if err := in.updateClassConstants(ctx, unit, class, depth+1); err != nil {
				err = in.materialize(ctx, unit, err)
				if handler, caught := f.raise(err, pc); caught {
					pc = handler
					continue
				}
				return php.Undef(), err
			}
			// The defaults are checked against their declared types on every
			// instantiation, not once when the class was resolved.
			if err := in.verifyPropertyDefaults(ctx, unit, class); err != nil {
				err = in.materialize(ctx, unit, err)
				if handler, caught := f.raise(err, pc); caught {
					pc = handler
					continue
				}
				return php.Undef(), err
			}
			object := unit.instantiate(class)
			if unit.isThrowable(class) {
				// An exception carries the site where it was created, even one
				// whose class declares its own constructor.
				recordThrowSite(ctx, unit, object)
			}
			f.storeResult(line, php.Obj(object))
			// NEW also opens the constructor call that the following
			// SEND_*/DO_FCALL complete. A class without one still needs the
			// frame, since DO_FCALL will pop it either way.
			ctor, declaring, hasCtor := unit.resolveMethod(class, "__construct")
			pending := &pendingCall{name: name + "::__construct", this: object, class: class, called: class}
			switch {
			case hasCtor:
				pending.method = ctor
				pending.class = declaring
				pending.called = class
			case unit.isThrowable(class):
				// Throwable's constructor is a C function, so the arguments have
				// to be applied here once DO_FCALL has collected them.
				scope := unit.throwableCtorScope(class)
				// A frame names the class that *declares* the constructor, and
				// for a Throwable that is one of the engine's three.
				pending.name = scope + "::__construct"
				pending.builtinCtor = func(o *php.Object, args []php.Value) error {
					return throwableCtor(ctx, scope, o, args)
				}
			case strings.EqualFold(class.Name, "WeakReference"):
				pending.builtinCtor = newWeakReference
			case strings.EqualFold(class.Name, "WeakMap"):
				pending.builtinCtor = noFailure(newWeakMap)
				unit.registerWeak(object)
			case fiberClasses[strings.ToLower(class.Name)]:
				// A Fiber's constructor validates a callable, which needs the
				// class table, so it goes through the method path.
				object.Internal = &fiber{object: object}
				pending.builtinMethod = "__construct"
			case isArrayBackedClass(unit, class.Name):
				// Its argument is checked against `array|object`, and the
				// TypeError names the **engine** class rather than the
				// subclass being built, so it goes through the method path
				// where the refusal can be raised.
				pending.builtinMethod = "__construct"
			case strings.EqualFold(class.Name, "SensitiveParameterValue"):
				pending.builtinCtor = noFailure(newSensitiveParameterValue)
			case strings.EqualFold(class.Name, "Deprecated"):
				// Its two arguments are checked against `?string`, which needs
				// the class table, so it goes through the method path.
				pending.builtinMethod = "__construct"
			case isEmptyIteratorClass(unit, class.Name):
				// Nothing to build: it holds no state.
			case isFilterIteratorClass(unit, class.Name):
				// It validates an Iterator and a callable, both of which need
				// the class table.
				pending.builtinMethod = "__construct"
			case reflectionClasses[strings.ToLower(class.Name)]:
				// A reflection constructor needs the class table, which a
				// builtinCtor has no way to reach, so it goes through the
				// method path instead.
				pending.builtinMethod = "__construct"
			}
			// An engine class's constructor is a builtin rather than an
			// op_array, but the object is just as much under construction
			// while it runs -- and just as invisible to the walk.
			if pending.builtinCtor != nil || pending.builtinMethod == "__construct" {
				hasCtor = true
			}
			if hasCtor {
				// Until the constructor returns the object is not built, and
				// PHP does not destruct what it did not construct -- whether
				// the constructor threw or an argument to it did, so that the
				// call was never even made.
				unit.markDestructed(object)
				pending.ctorObject = object
				// Until the constructor returns, the object lives in a
				// temporary -- which is not a root -- and in a pending call
				// that DO_FCALL pops before the body even runs. Nothing else
				// names it, so the walk is told about it directly.
				unit.building = append(unit.building, object)
			}
			calls = append(calls, pending)
			pc++

		case "ZEND_INIT_PARENT_PROPERTY_HOOK_CALL":
			// `parent::$p::get()` inside a hook. op1 names the property and op2
			// the hook kind, and the scope's parent -- not the object's class --
			// is where the property is looked up.
			property := f.fetch(line.GetOp1Type(), line.GetOp1()).String()
			isSet := line.GetOp2() == 1
			parent, err := unit.parentHookTarget(in, ctx, f, property)
			if err != nil {
				err = in.materialize(ctx, unit, err)
				if handler, caught := f.raise(err, pc); caught {
					pc = handler
					continue
				}
				return php.Undef(), err
			}
			call := &pendingCall{
				name: parent.declaring.Name + "::$" + property, this: f.this,
				class: parent.declaring, called: f.called,
			}
			body := parent.prop.GetGetHook()
			if isSet {
				body = parent.prop.GetSetHook()
			}
			if body != nil {
				call.method = body
				call.hook = unit.hookIdentityOf(parent.declaring, property)
			} else {
				call.hookFallback = parent.prop
				call.hookIsSet = isSet
			}
			calls = append(calls, call)
			pc++

		case "ZEND_INIT_METHOD_CALL":
			// An unused op1 is $this, which is how `$this->m()` is encoded.
			object, target := f.objectOperand(line, false)
			// A receiver that arrived in a temporary -- `(new C)->m()` -- has
			// no other name, so the call is what lets go of it. The engine
			// frees it when the call returns, which is what hands its handle
			// to the next `new`.
			dropsReceiver := f.releaseTemp(unit, line.GetOp1Type(), line.GetOp1())
			// Calling a method does not produce a lazy object's state: the
			// method is resolved on the object's own class, and whatever its
			// body touches produces the state if it needs to. A proxy keeps
			// its own class and its own identity here too -- the method may be
			// a subclass's, $this is the proxy, and only its property reads
			// forward to the instance.
			name, nameErr := methodNameOperand(f.fetch(line.GetOp2Type(), line.GetOp2()))
			if nameErr != nil {
				// The name is checked before the receiver: `$null->$one()`
				// reports the name, not the null.
				nameErr = in.materialize(ctx, unit, nameErr)
				if handler, caught := f.raise(nameErr, pc); caught {
					pc = handler
					continue
				}
				return php.Undef(), nameErr
			}
			if object == nil {
				err := in.throwError(ctx, unit, "Error", "Call to a member function %s() on %s",
					name, givenTypeName(target))
				if handler, caught := f.raise(err, pc); caught {
					pc = handler
					continue
				}
				return php.Undef(), err
			}
			class, ok := unit.findClass(object.ClassName)
			if !ok {
				return php.Undef(), &UnsupportedError{
					Opcode: "method call on " + object.ClassName,
					Line:   line.GetLine(), File: op.GetFilename(),
				}
			}
			method, declaring, found := unit.resolveMethod(class, name)
			if found && f.class != nil && declaring != f.class &&
				unit.instanceOf(class, f.class.Name) {
				// A private method is per-class: the lookup prefers one the
				// *calling scope* declares over the one the object's class
				// resolved to. `$this->t()` inside A::callIt reaches A's `t`
				// even on a B instance that declares its own -- and whatever
				// the visibility of the one it resolved to. A subclass that
				// overrides a parent's private method carries ZEND_ACC_CHANGED
				// and sends the engine to `zend_get_parent_private_method`,
				// which asks only the scope.
				if own, isPrivate := unit.privateInScope(f.class, name); isPrivate {
					method, declaring = own, f.class
				}
			}
			if !found {
				// __call answers for a method the class does not declare. Its
				// arguments arrive as an array, so the call is queued with the
				// name to forward rather than dispatched here.
				if forwarded, ok := unit.forwardToMagicCall(object, class, name); ok {
					forwarded.releasedTemps = forwarded.releasedTemps || dropsReceiver
					calls = append(calls, forwarded)
					pc++
					continue
				}
				if !unit.isThrowable(class) && !isArrayBackedClass(unit, object.ClassName) &&
					!reflectionClasses[strings.ToLower(object.ClassName)] &&
					!weakClasses[strings.ToLower(object.ClassName)] &&
					!fiberClasses[strings.ToLower(object.ClassName)] &&
					!strings.EqualFold(object.ClassName, "SensitiveParameterValue") &&
					!strings.EqualFold(object.ClassName, "Deprecated") &&
					!strings.EqualFold(object.ClassName, "Directory") &&
					!isFilterIteratorClass(unit, object.ClassName) &&
					!isEmptyIteratorClass(unit, object.ClassName) &&
					object.ClassName != "Generator" && object.ClassName != "Closure" {
					_, engineHas := unit.engineMethod(class, name)
					if !unit.isBuiltinOnly(class) || !engineHas {
						// The program declared this class, so every method it
						// has is known: the name is simply wrong. An engine
						// class is asked the same question of its harvested
						// method list -- a name that is not on it is the
						// program's mistake, while one that is may simply not
						// be implemented here, which stays a gap.
						err := in.throwError(ctx, unit, "Error",
							"Call to undefined method %s::%s()", php.DisplayName(object.ClassName), name)
						if handler, caught := f.raise(err, pc); caught {
							pc = handler
							continue
						}
						return php.Undef(), err
					}
					return php.Undef(), &UnsupportedError{
						Opcode: "method " + object.ClassName + "::" + name,
						Line:   line.GetLine(), File: op.GetFilename(),
					}
				}
				calls = append(calls, &pendingCall{
					name: object.ClassName + "::" + name, this: object,
					builtinMethod: name,
					releasedTemps: dropsReceiver,
				})
				pc++
				continue
			}
			if !unit.methodAccessibleAs(method, declaring, f.class, name) {
				// A method the caller cannot see is answered by __call first,
				// exactly as one the class does not declare would be.
				if forwarded, ok := unit.forwardToMagicCall(object, class, name); ok {
					forwarded.releasedTemps = forwarded.releasedTemps || dropsReceiver
					calls = append(calls, forwarded)
					pc++
					continue
				}
				err := in.throwError(ctx, unit, "Error", "Call to %s method %s::%s() from %s",
					unit.effectiveVisibility(declaring, method, name),
					php.DisplayName(declaring.Name), name, scopeName(f.class))
				if handler, caught := f.raise(err, pc); caught {
					pc = handler
					continue
				}
				return php.Undef(), err
			}
			if method.GetAbstract() {
				err := in.throwError(ctx, unit, "Error",
					"Cannot call abstract method %s::%s()", php.DisplayName(declaring.Name), name)
				if handler, caught := f.raise(err, pc); caught {
					pc = handler
					continue
				}
				return php.Undef(), err
			}
			bound := object
			if method.GetStatic() {
				// `$obj->staticMethod()` is legal and reaches the method, but
				// binds no instance: $this is unset inside it.
				bound = nil
			}
			calls = append(calls, &pendingCall{
				name: object.ClassName + "::" + name, this: bound,
				method: method, class: declaring, called: class,
				releasedTemps: dropsReceiver,
			})
			pc++

		case "ZEND_FETCH_THIS":
			if f.this == nil {
				// Not a gap: the program asked for an instance where there is
				// none, which PHP reports as a catchable Error.
				err := in.throwError(ctx, unit, "Error", "Using $this when not in object context")
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			f.storeResult(line, php.Obj(f.this))
			pc++

		case "ZEND_FETCH_OBJ_R", "ZEND_FETCH_OBJ_IS":
			// The IS form is what isset($o->p) and $o->p ?? x compile to, and
			// neither complains: the question already admits the answer may be
			// "not set".
			quiet := name == "ZEND_FETCH_OBJ_IS"
			target, container := f.objectOperand(line, quiet)
			if target != nil && target.Lazy != nil {
				// Touching a lazy object's state is what produces it. A proxy
				// answers with the instance it forwards to, so everything
				// below works on an ordinary object.
				lazyName, lazyMagic := lazyProperty(f, line, name)
				resolved, lazyErr := in.resolveLazy(ctx, unit, target, lazyName, lazyMagic, depth)
				if lazyErr != nil {
					lazyErr = in.materialize(ctx, unit, lazyErr)
					if handler, caught := f.raise(lazyErr, pc); caught {
						pc = handler
						continue
					}
					return php.Undef(), lazyErr
				}
				target = resolved
				container = php.Obj(target)
			}
			property, nameErr := in.propertyName(ctx, unit, f, line.GetOp2Type(), line.GetOp2())
			if nameErr != nil {
				nameErr = in.materialize(ctx, unit, nameErr)
				if target, caught := f.raise(nameErr, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), nameErr
			}
			if target == nil {
				if !quiet {
					// PHP names the type it actually found rather than saying
					// "non-object", and the tests match on that wording.
					ctx.Warn("Attempt to read property \"%s\" on %s", property, givenTypeName(container))
				}
				f.storeResult(line, php.Null())
				pc++
				continue
			}
			// Inside a class that declares its own private property of this
			// name, the name means that class's slot, which is not the one the
			// plain name holds when a subclass declares one too.
			property = propertySlot(target, property, f.class)
			if hooked, declaring, ok := unit.hooksFor(f, target, property); ok {
				if hooked.GetGetHook() == nil {
					// A property with only a set hook reads its backing store,
					// and a virtual one has nothing to read at all -- reading
					// is refused rather than answered with null.
					if hooked.GetVirtual() {
						err := in.throwError(ctx, unit, "Error",
							"Property %s::$%s is write-only",
							php.DisplayName(target.ClassName), property)
						if handler, caught := f.raise(err, pc); caught {
							pc = handler
							continue
						}
						return php.Undef(), err
					}
				} else {
					value, err := in.callGetHook(ctx, unit, target, hooked, declaring, depth)
					if err != nil {
						if handler, caught := f.raise(err, pc); caught {
							pc = handler
							continue
						}
						return php.Undef(), err
					}
					f.storeResult(line, value.Deref())
					pc++
					continue
				}
			}
			if !quiet {
				// isset() asks without accessing, so only the plain read says
				// that the name belongs to a static the class declares.
				unit.noteStaticAsInstance(ctx, target, property, magicGet)
			}
			prop, verdict := unit.propertyAccess(target, property, f.class)
			if verdict == accessDenied && !quiet && !unit.magicAvailable(target, magicGet, property) {
				// __get answers for a property the caller may not reach, the
				// same way it answers for one that does not exist. Only a class
				// without one -- or one already inside __get for this very
				// property, where reading it again reaches the declared slot --
				// turns the access into an error.
				err := in.deniedPropertyError(ctx, unit, target, prop, property)
				if handler, caught := f.raise(err, pc); caught {
					pc = handler
					continue
				}
				return php.Undef(), err
			}
			value, ok := target.Get(property)
			if verdict != accessAllowed {
				// Invisible, or reachable only through __get: the property
				// reads as absent, so the magic accessor gets its turn below.
				value, ok = php.Undef(), false
			}
			if ok && value.Deref().Kind() == php.KindUndef && target.WasCleared(property) &&
				unit.hasMagic(target, magicGet) {
				// unset() removed it, so it reads as absent and __get answers.
				// Without a __get there is nothing to answer, and the slot is
				// still the declaration's: reading it is the uninitialized
				// error rather than a missing property.
				value, ok = php.Undef(), false
			}
			if ok && value.Deref().Kind() == php.KindUndef {
				// A declared typed property that was never assigned. Reading it
				// is an Error, not a warning about a missing property -- the
				// property is there, it just holds nothing. The IS form still
				// answers "not set" without complaining.
				if quiet {
					f.storeResult(line, php.Null())
					pc++
					continue
				}
				declared := target.Declared[property]
				err := in.throwError(ctx, unit, "Error",
					"Typed property %s::$%s must not be accessed before initialization",
					php.DisplayName(declared.Class), property)
				if handler, caught := f.raise(err, pc); caught {
					pc = handler
					continue
				}
				return php.Undef(), err
			}
			if !ok && quiet && unit.hasMagic(target, magicIsset) {
				// The IS form asks __isset first and only then __get, which is
				// why `$o->p ?? x` on such a class can answer without ever
				// running __get.
				present, handled, err := in.callMagic(ctx, unit, target, magicIsset,
					property, []php.Value{php.String(property)}, depth)
				if err != nil {
					if handler, caught := f.raise(err, pc); caught {
						pc = handler
						continue
					}
					return php.Undef(), err
				}
				if handled && !present.Deref().Bool() {
					f.storeResult(line, php.Null())
					pc++
					continue
				}
			}
			if !ok {
				// A class with __get answers for properties it does not hold.
				magic, handled, err := in.callMagic(ctx, unit, target, magicGet,
					property, []php.Value{php.String(property)}, depth)
				switch {
				case err != nil:
					if handler, caught := f.raise(err, pc); caught {
						pc = handler
						continue
					}
					return php.Undef(), err
				case handled:
					value = magic
					// __get answering for a *declared* typed property still has
					// to produce that type: the declaration outlives the unset
					// that sent the read here.
					// Only a property the caller could have seen holds __get to
					// its type. When __get stood in for an *invisible* one the
					// declaration is none of the caller's business, and the
					// engine lets whatever came back through.
					if converted, err := in.magicReadType(ctx, unit, target, property,
						value, verdict == accessAllowed, op.GetStrictTypes()); err != nil {
						if handler, caught := f.raise(err, pc); caught {
							pc = handler
							continue
						}
						return php.Undef(), err
					} else {
						value = converted
					}
				default:
					if nulProperty(property) && !quiet {
						// Nothing answered, and the table cannot hold this
						// name: reaching it is an error rather than a missing
						// property. The IS form still answers "not set".
						err := in.refuseNulProperty(ctx, unit)
						if handler, caught := f.raise(err, pc); caught {
							pc = handler
							continue
						}
						return php.Undef(), err
					}
					if !quiet {
						ctx.Warn("Undefined property: %s::$%s", php.DisplayName(target.ClassName), property)
					}
					value = php.Null()
				}
			}
			f.storeResult(line, value)
			pc++

		case "ZEND_ASSIGN_OBJ":
			// Like ASSIGN_DIM, the value arrives in the following OP_DATA.
			if pc+1 >= len(oplines) || in.name(oplines[pc+1].GetOpcode()) != "ZEND_OP_DATA" {
				return php.Undef(), fmt.Errorf("%s:%d: ASSIGN_OBJ without OP_DATA",
					op.GetFilename(), line.GetLine())
			}
			data := oplines[pc+1]
			value := f.fetch(data.GetOp1Type(), data.GetOp1()).Copy()
			// A write does not warn about an undefined container; the failure
			// is reported as the assignment error alone.
			target, container := f.objectOperand(line, true)
			if target != nil && target.Lazy != nil {
				// Touching a lazy object's state is what produces it. A proxy
				// answers with the instance it forwards to, so everything
				// below works on an ordinary object.
				lazyName, lazyMagic := lazyProperty(f, line, name)
				resolved, lazyErr := in.resolveLazy(ctx, unit, target, lazyName, lazyMagic, depth)
				if lazyErr != nil {
					lazyErr = in.materialize(ctx, unit, lazyErr)
					if handler, caught := f.raise(lazyErr, pc); caught {
						pc = handler
						continue
					}
					return php.Undef(), lazyErr
				}
				target = resolved
				container = php.Obj(target)
			}
			if target == nil {
				err := in.throwError(ctx, unit, "Error",
					"Attempt to assign property \"%s\" on %s",
					f.fetch(line.GetOp2Type(), line.GetOp2()).String(),
					givenTypeName(container))
				if handler, caught := f.raise(err, pc); caught {
					pc = handler
					continue
				}
				return php.Undef(), err
			}
			property, nameErr := in.propertyName(ctx, unit, f, line.GetOp2Type(), line.GetOp2())
			if nameErr != nil {
				nameErr = in.materialize(ctx, unit, nameErr)
				if target, caught := f.raise(nameErr, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), nameErr
			}
			// Inside a class that declares its own private property of this
			// name, the name means that class's slot, which is not the one the
			// plain name holds when a subclass declares one too.
			property = propertySlot(target, property, f.class)
			if err := in.refuseReadonlyEarly(ctx, unit, target, property, f.class); err != nil {
				if handler, caught := f.raise(err, pc); caught {
					pc = handler
					continue
				}
				return php.Undef(), err
			}
			if converted, err := in.verifyPropertyWrite(ctx, unit, target, property,
				value, op.GetStrictTypes()); err != nil {
				if handler, caught := f.raise(err, pc); caught {
					pc = handler
					continue
				}
				return php.Undef(), err
			} else {
				value = converted
			}
			// A slot already bound to a typed property carries that property's
			// type, whatever the name being assigned is declared as.
			if ref := typedRefHeld(target.Props, php.StringKey(property)); ref != nil {
				converted, err := in.verifyRefWrite(ctx, unit, ref, value, op.GetStrictTypes())
				if err != nil {
					if handler, caught := f.raise(err, pc); caught {
						pc = handler
						continue
					}
					return php.Undef(), err
				}
				value = converted
			}
			if hooked, declaring, ok := unit.hooksFor(f, target, property); ok {
				if err := in.checkSetVisibility(ctx, unit, target, hooked, property,
					f.class, "modify"); err != nil {
					if handler, caught := f.raise(err, pc); caught {
						pc = handler
						continue
					}
					return php.Undef(), err
				}
				if hooked.GetSetHook() == nil {
					// With no set hook, a backed property writes its storage
					// directly -- the implicit set. A virtual one has no
					// storage, so the write has nowhere to go and is refused.
					if hooked.GetVirtual() {
						err := in.readOnlyHookError(ctx, unit, target, property)
						if handler, caught := f.raise(err, pc); caught {
							pc = handler
							continue
						}
						return php.Undef(), err
					}
					target.Set(property, value)
					if line.GetResultType() != opUnused {
						f.storeResult(line, value)
					}
					pc += 2
					continue
				}
				if err := in.callSetHook(ctx, unit, target, hooked, declaring, value, depth); err != nil {
					if handler, caught := f.raise(err, pc); caught {
						pc = handler
						continue
					}
					return php.Undef(), err
				}
				if line.GetResultType() != opUnused {
					f.storeResult(line, value)
				}
				pc += 2
				continue
			}
			verdict, err := in.checkPropertyWrite(ctx, unit, target, property, f.class)
			if err != nil {
				if handler, caught := f.raise(err, pc); caught {
					pc = handler
					continue
				}
				return php.Undef(), err
			}
			_, held := target.Get(property)
			if verdict == accessHidden {
				// The declared property is invisible from here, so the write
				// creates a dynamic one alongside it rather than touching it.
				held = false
			}
			if !held {
				// __set answers for a property the object does not hold. A
				// class without one simply grows the property, which is what
				// PHP does for any object not forbidding dynamic properties.
				_, handled, err := in.callMagic(ctx, unit, target, magicSet,
					property, []php.Value{php.String(property), value}, depth)
				if err != nil {
					if handler, caught := f.raise(err, pc); caught {
						pc = handler
						continue
					}
					return php.Undef(), err
				}
				if handled {
					f.storeResult(line, value)
					pc += 2
					continue
				}
				// An enum case has no state beyond name and value, and adding
				// some is refused outright rather than deprecated -- as it is
				// for the engine's own classes that carry the same flag.
				if class, ok := unit.findClass(target.ClassName); ok &&
					(class.decl.GetKind() == pb.Class_KIND_ENUM ||
						class.decl.GetNoDynamicProperties()) {
					err := in.throwError(ctx, unit, "Error",
						"Cannot create dynamic property %s::$%s",
						php.DisplayName(class.Name), property)
					if handler, caught := f.raise(err, pc); caught {
						pc = handler
						continue
					}
					return php.Undef(), err
				}
				if nulProperty(property) {
					// Nothing answered, and the table cannot hold this name.
					err := in.refuseNulProperty(ctx, unit)
					if handler, caught := f.raise(err, pc); caught {
						pc = handler
						continue
					}
					return php.Undef(), err
				}
				// No accessor took it, so the write creates the property.
				unit.noteDynamicProperty(ctx, target, property, verdict)
				if verdict == accessHidden {
					// It shadows an invisible declared property rather than
					// filling it, so later accesses must not be restricted --
					// and the declared one keeps its own value under its own
					// key, which is where the engine had it all along.
					unit.hideDeclaredSlot(target, property)
					target.MarkDynamic(property)
				}
			}
			target.Set(property, value)
			f.storeResult(line, value)
			pc += 2

		case "ZEND_INSTANCEOF":
			target := f.fetch(line.GetOp1Type(), line.GetOp1())
			// An unused op2 carries a class *reference* rather than a name --
			// `instanceof self` -- which resolves the same way every other
			// class reference does, and refuses the same way outside a class.
			name, nameErr := f.classNameFrom(unit, line, line.GetOp2Type(), line.GetOp2())
			if nameErr != nil {
				nameErr = in.materialize(ctx, unit, nameErr)
				if target, caught := f.raise(nameErr, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), nameErr
			}
			result := false
			if target.Kind() == php.KindObject {
				if class, ok := unit.findClass(target.Object().ClassName); ok {
					result = unit.instanceOf(class, name)
				}
			}
			f.storeResult(line, php.Bool(result))
			pc++

		case "ZEND_INIT_ARRAY":
			arr := php.NewArray()
			// An empty literal initializes with no element at all.
			if line.GetOp1Type() != opUnused {
				if err := addElement(ctx, arr, f, line); err != nil {
					err = in.materialize(ctx, unit, err)
					if target, caught := f.raise(err, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), err
				}
			}
			f.storeResult(line, php.Arr(arr))
			// The literal is a root until it lands somewhere: a temporary is
			// not one, and a constructor running for a later element would
			// otherwise collect the elements already in it.
			f.literals = append(f.literals, arrayLiteral{
				kind: line.GetResultType(), slot: line.GetResult(), value: php.Arr(arr),
			})
			pc++

		case "ZEND_ADD_ARRAY_UNPACK":
			// `[...$a]` merges an array into the literal being built. Integer
			// keys are renumbered from the end of what is already there, string
			// keys are kept and overwrite.
			target := f.fetch(line.GetResultType(), line.GetResult())
			if target.Kind() != php.KindArray {
				return php.Undef(), fmt.Errorf("%s:%d: array element added to a %s",
					op.GetFilename(), line.GetLine(), target.Kind())
			}
			spread := f.fetch(line.GetOp1Type(), line.GetOp1()).Deref()
			if spread.Kind() == php.KindObject {
				// A Traversable unpacks into a literal the same way it spreads
				// into an argument list.
				delegate, ok, err := in.newObjectIterator(ctx, unit, spread, depth)
				if err == nil && !ok {
					err = &UnsupportedError{
						Opcode: "array unpacking of " + spread.Object().ClassName,
						Line:   line.GetLine(), File: op.GetFilename(),
					}
				}
				for err == nil {
					raw, value, more, next := in.iterNext(ctx, unit, delegate)
					if err = next; err != nil || !more {
						break
					}
					// Unpacking builds an array, so the key is converted here
					// -- and the conversion's own diagnostics belong here too.
					// A Traversable can produce any key at all, and only an int
					// or a string is one: nothing is converted into shape.
					if kind := raw.Deref().Kind(); kind != php.KindInt && kind != php.KindString {
						err = in.throwError(ctx, unit, "Error",
							"Keys must be of type int|string during array unpacking")
						break
					}
					key := php.ArrayKey(ctx, raw)
					if !key.IsInt {
						target.Array().Set(key, value.Deref().Copy())
						continue
					}
					if !target.Array().CanAppend() {
						// The largest integer key is taken, so the element
						// being unpacked has nowhere to go.
						err = in.throwError(ctx, unit, "Error",
							"Cannot add element to the array as the next element is already occupied")
						break
					}
					target.Array().Append(value.Deref().Copy())
				}
				if err != nil {
					// The Traversable is abandoned here, and a generator that
					// is abandoned runs its finally blocks -- one that throws
					// replaces what abandoned it. Probed: a generator yielding
					// an unusable key from inside a try/finally that throws
					// reports the *finally's* exception, not the key error.
					if delegate != nil && delegate.gen != nil {
						if thrown := in.closeGeneratorFor(ctx, unit, delegate.gen); thrown != nil {
							err = thrown
						}
					}
					if handler, caught := f.raise(in.materialize(ctx, unit, err), pc); caught {
						pc = handler
						continue
					}
					return php.Undef(), err
				}
				pc++
				continue
			}
			if spread.Kind() != php.KindArray {
				// An Error rather than the TypeError the same thing earns in a
				// call: the wording is the engine's own in both places.
				err := in.throwError(ctx, unit, "Error",
					"Only arrays and Traversables can be unpacked, %s given",
					givenTypeName(spread))
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			var refused error
			spread.Array().Each(func(k php.Key, v php.Value) bool {
				if !k.IsInt {
					target.Array().Set(k, v.Copy())
					return true
				}
				if !target.Array().CanAppend() {
					refused = in.throwError(ctx, unit, "Error",
						"Cannot add element to the array as the next element is already occupied")
					return false
				}
				target.Array().Append(v.Copy())
				return true
			})
			if refused != nil {
				if handler, caught := f.raise(refused, pc); caught {
					pc = handler
					continue
				}
				return php.Undef(), refused
			}
			pc++

		case "ZEND_ADD_ARRAY_ELEMENT":
			// The array under construction arrives through the result slot,
			// which the engine reuses across the whole literal.
			target := f.fetch(line.GetResultType(), line.GetResult())
			if target.Kind() != php.KindArray {
				return php.Undef(), fmt.Errorf("%s:%d: array element added to a %s",
					op.GetFilename(), line.GetLine(), target.Kind())
			}
			if line.GetOp2Type() == opUnused && !target.Array().CanAppend() {
				// The largest integer key is taken, so there is no next one.
				err := in.throwError(ctx, unit, "Error",
					"Cannot add element to the array as the next element is already occupied")
				if handler, caught := f.raise(err, pc); caught {
					pc = handler
					continue
				}
				return php.Undef(), err
			}
			if err := addElement(ctx, target.Array(), f, line); err != nil {
				err = in.materialize(ctx, unit, err)
				if handler, caught := f.raise(err, pc); caught {
					pc = handler
					continue
				}
				return php.Undef(), err
			}
			pc++

		case "ZEND_FETCH_DIM_R", "ZEND_FETCH_DIM_IS":
			container := f.fetchOperand(line.GetOp1Type(), line.GetOp1(), name == "ZEND_FETCH_DIM_IS")
			index := f.fetch(line.GetOp2Type(), line.GetOp2())
			// The IS form is what isset() and ?? compile to, and neither
			// complains about a key that is not there -- that is the whole
			// point of asking.
			quiet := name == "ZEND_FETCH_DIM_IS"
			if object := container.Deref().Object(); object != nil {
				value, err := in.readOffset(ctx, unit, object, index, quiet, line, op, depth)
				if err != nil {
					if target, caught := f.raise(err, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), err
				}
				f.storeResult(line, value)
				pc++
				continue
			}
			element, err := dimensionRead(ctx, container, index, quiet)
			if err != nil {
				err = in.materialize(ctx, unit, err)
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			f.storeResult(line, element)
			pc++

		case "ZEND_FETCH_DIM_FUNC_ARG", "ZEND_FETCH_OBJ_FUNC_ARG":
			// The compiler could not tell whether the callee takes this
			// argument by reference, so the decision is made here: by reference
			// it is a write fetch, by value an ordinary read.
			if in.argumentByRef(unit, calls, f.funcArg) &&
				(line.GetOp1Type() == opConst || line.GetOp1Type() == opTmpVar) {
				// There is nothing to write back into. The compiler refuses the
				// same thing when it can see it (`[0, 1][0] = 1`); reached this
				// way the callee was not known until now, so it is an Error
				// rather than a compile-time one.
				err := in.throwError(ctx, unit, "Error",
					"Cannot use temporary expression in write context")
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			if !in.argumentByRef(unit, calls, f.funcArg) {
				if strings.HasPrefix(name, "ZEND_FETCH_DIM") {
					if line.GetOp2Type() == opUnused {
						// The callee turned out to take this argument by value,
						// so `f($a[])` is a read -- and an append has nothing to
						// read.
						err := in.throwError(ctx, unit, "Error", "Cannot use [] for reading")
						if target, caught := f.raise(err, pc); caught {
							pc = target
							continue
						}
						return php.Undef(), err
					}
					container := f.fetch(line.GetOp1Type(), line.GetOp1())
					index := f.fetch(line.GetOp2Type(), line.GetOp2())
					element, err := dimensionRead(ctx, container, index, false)
					if err != nil {
						err = in.materialize(ctx, unit, err)
						if target, caught := f.raise(err, pc); caught {
							pc = target
							continue
						}
						return php.Undef(), err
					}
					f.storeResult(line, element)
					pc++
					continue
				}
				object, container := f.objectOperand(line, false)
				if object != nil && object.Lazy != nil {
					// Touching a lazy object's state is what produces it. A proxy
					// answers with the instance it forwards to, so everything
					// below works on an ordinary object.
					lazyName, lazyMagic := lazyProperty(f, line, name)
					resolved, lazyErr := in.resolveLazy(ctx, unit, object, lazyName, lazyMagic, depth)
					if lazyErr != nil {
						lazyErr = in.materialize(ctx, unit, lazyErr)
						if handler, caught := f.raise(lazyErr, pc); caught {
							pc = handler
							continue
						}
						return php.Undef(), lazyErr
					}
					object = resolved
					container = php.Obj(object)
				}
				property, nameErr := in.propertyName(ctx, unit, f, line.GetOp2Type(), line.GetOp2())
				if nameErr != nil {
					nameErr = in.materialize(ctx, unit, nameErr)
					if target, caught := f.raise(nameErr, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), nameErr
				}
				value := php.Null()
				switch {
				case object == nil:
					ctx.Warn("Attempt to read property \"%s\" on %s", property, givenTypeName(container))
				default:
					// Inside a class that declares its own private property of this
					// name, the name means that class's slot, which is not the one the
					// plain name holds when a subclass declares one too.
					property = propertySlot(object, property, f.class)
					held, ok := object.Get(property)
					if !ok {
						ctx.Warn("Undefined property: %s::$%s", object.ClassName, property)
					} else {
						value = held
					}
				}
				f.storeResult(line, value)
				pc++
				continue
			}
			fallthrough

		case "ZEND_FETCH_DIM_W", "ZEND_FETCH_DIM_RW", "ZEND_FETCH_OBJ_W", "ZEND_FETCH_OBJ_RW",
			"ZEND_FETCH_OBJ_UNSET":
			// These open a write path: `$a[0][1] = x` fetches $a[0] for writing
			// and then assigns into what came back. The result has to alias the
			// container rather than copy it -- an array Value carries a pointer,
			// so returning it unchanged is enough, provided nothing along the
			// way calls Copy.
			//
			// A pure write fetch is silent about an undefined container -- the
			// point of BP_VAR_W is to create it -- while the RW forms, which are
			// compound assignment, read it first and so do warn.
			isObject := strings.HasPrefix(name, "ZEND_FETCH_OBJ")
			// The UNSET forms open the same write path -- `unset($o->p[$k])`
			// reaches the element through $o->p -- but they *read* the
			// container rather than creating it, so an undefined one is
			// reported. A single-level `unset($o->p)` is a different opcode
			// and stays silent.
			var container php.Value
			if isObject && line.GetOp1Type() == opUnused {
				// An unused container is $this, which is how the compiler
				// encodes `$this->prop[] = x`. In a static context there is no
				// instance, and null is what the error below reports on.
				container = php.Null()
				if f.this != nil {
					container = php.Obj(f.this)
				}
			} else {
				quiet := !strings.HasSuffix(name, "_RW") && !strings.HasSuffix(name, "_UNSET")
				container = f.fetchOperand(line.GetOp1Type(), line.GetOp1(), quiet)
			}
			if !isObject && container.Deref().Kind() == php.KindObject &&
				line.GetOp2Type() != opUnused {
				index := f.fetch(line.GetOp2Type(), line.GetOp2())
				if slot, entries, at, held := weakMapSlot(container.Deref().Object(),
					index); held {
					if line.GetResultType() != opUnused {
						if f.writeTargets == nil {
							f.writeTargets = map[uint32]writeTarget{}
						}
						f.writeTargets[line.GetResult()] = writeTarget{
							weak: entries, weakAt: at,
						}
					}
					f.bindResult(line, slot)
					pc++
					continue
				}
				if store, backed := unit.plainArrayStore(container.Deref().Object()); backed {
					// An engine array store -- ArrayObject and its kin -- has a
					// dimension handler that answers with the storage slot
					// itself, so `$o[$k][] = 1`, `$o[$k]++` and `$o[$k] = &$x`
					// all reach the store rather than a copy and none of them
					// earns "Indirect modification of overloaded element". A
					// userland ArrayAccess is the overloaded case and still
					// does, and so does a subclass that wrote its own
					// offsetGet: the engine's handler calls the override.
					key := php.ArrayKey(ctx, index)
					f.bindResult(line, php.MakeRef(notePropertyType(store.Owner(),
						key.String(), store.Items().RefAt(key))))
					if line.GetResultType() != opUnused {
						if f.writeTargets == nil {
							f.writeTargets = map[uint32]writeTarget{}
						}
						f.writeTargets[line.GetResult()] = writeTarget{
							array: store.Items(), key: key,
						}
					}
					pc++
					continue
				}
			}
			if !isObject && container.Deref().Kind() == php.KindObject {
				// An ArrayAccess reached on a write path: `$o[$k][$j] = x`
				// descends through offsetGet, which is what PHP does too.
				// offsetGet is a call and a call moves the reported location;
				// the notice below belongs to the modification site, not to
				// the accessor's body.
				where, at := ctx.Location()
				element, consulted, err := in.writeFetchOffset(ctx, unit,
					container.Deref().Object(), f, line, op, depth)
				ctx.SetLocation(where, at)
				// offsetGet handed back a value, and writing through it reaches
				// a copy. A reference is the class saying otherwise, and an
				// object is one already -- anything else earns the notice, and
				// really is modified to no effect. An accessor that threw earns
				// it too: the engine reports what the descent produced before
				// it looks at whether anything was thrown.
				if _, byRef := element.RefOf(); consulted && !byRef &&
					element.Deref().Kind() != php.KindObject {
					ctx.Notice("Indirect modification of overloaded element of %s has no effect",
						php.DisplayName(container.Deref().Object().ClassName))
					element = element.Deref().Copy()
				}
				if err != nil {
					err = in.materialize(ctx, unit, err)
					if target, caught := f.raise(err, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), err
				}
				f.storeResult(line, element)
				pc++
				continue
			}
			if !isObject {
				grown, ok, growErr := in.growArray(ctx, unit, f, line.GetOp1Type(), line.GetOp1(), container)
				if growErr != nil {
					if handler, caught := f.raise(in.materialize(ctx, unit, growErr), pc); caught {
						pc = handler
						continue
					}
					return php.Undef(), growErr
				}
				if !ok {
					// The offset is checked before the descent is refused: a
					// string offset PHP merely disapproves of still earns its
					// warning here, ahead of the fatal. Unsetting is the one
					// form that checks quietly.
					if container.Deref().Kind() == php.KindString &&
						line.GetOp2Type() != opUnused && !strings.HasSuffix(name, "_UNSET") {
						if _, bad := stringOffset(ctx, f.fetch(line.GetOp2Type(), line.GetOp2()), false, false); bad != nil {
							bad = in.materialize(ctx, unit, bad)
							if target, caught := f.raise(bad, pc); caught {
								pc = target
								continue
							}
							return php.Undef(), bad
						}
						// The offset only earned a *warning*, and an error
						// handler may have thrown over it. That throw is what
						// reaches the program: the refusal below never happens.
						if pending := ctx.TakePendingError(); pending != nil {
							if target, caught := f.raise(pending, pc); caught {
								pc = target
								continue
							}
							return php.Undef(), pending
						}
					}
					// PHP words this by what the container is, and the wording
					// differs again for an append.
					err := in.throwError(ctx, unit, "Error", "%s",
						scalarIndexError(container, line, strings.HasSuffix(name, "_UNSET"),
							in.stepsResult(oplines, pc, line)))
					if target, caught := f.raise(err, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), err
				}
				container = grown
			}
			if isObject && container.Kind() != php.KindObject && strings.HasSuffix(name, "_UNSET") {
				// An unset chain through something that is not an object has
				// nothing to remove and says nothing about it: the rest of the
				// chain runs over the same nothing and stops there.
				f.storeResult(line, php.Undef())
				pc++
				continue
			}
			if isObject && container.Kind() != php.KindObject {
				err := in.throwError(ctx, unit, "Error",
					"Attempt to assign property \"%s\" on %s",
					f.fetch(line.GetOp2Type(), line.GetOp2()).String(), givenTypeName(container))
				if handler, caught := f.raise(err, pc); caught {
					pc = handler
					continue
				}
				return php.Undef(), err
			}

			if isObject && container.Kind() == php.KindObject && container.Object().Lazy != nil {
				// Descending into a property for writing touches the state, so
				// it has to exist: a proxy answers with the instance, and
				// everything below writes through that.
				lazyName, lazyMagic := lazyProperty(f, line, name)
				resolved, lazyErr := in.resolveLazy(ctx, unit, container.Object(), lazyName, lazyMagic, depth)
				if lazyErr != nil {
					lazyErr = in.materialize(ctx, unit, lazyErr)
					if handler, caught := f.raise(lazyErr, pc); caught {
						pc = handler
						continue
					}
					return php.Undef(), lazyErr
				}
				container = php.Obj(resolved)
			}
			if isObject && container.Kind() == php.KindObject {
				property, nameErr := in.propertyName(ctx, unit, f, line.GetOp2Type(), line.GetOp2())
				if nameErr != nil {
					nameErr = in.materialize(ctx, unit, nameErr)
					if target, caught := f.raise(nameErr, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), nameErr
				}
				// Inside a class that declares its own private property of this
				// name, the name means that class's slot, which is not the one the
				// plain name holds when a subclass declares one too.
				property = propertySlot(container.Object(), property, f.class)
				if _, present := container.Object().Get(property); !present &&
					unit.forbidsDynamic(container.Object(), property) {
					// `$o->missing[] = 1` would grow the object, and this one
					// does not grow.
					err := in.throwError(ctx, unit, "Error",
						"Cannot create dynamic property %s::$%s",
						php.DisplayName(container.Object().ClassName), property)
					if handler, caught := f.raise(err, pc); caught {
						pc = handler
						continue
					}
					return php.Undef(), err
				}
				if hooked, declaring, ok := unit.hooksFor(f, container.Object(), property); ok {
					// A `&get` hook hands out a slot, and that slot is what
					// the write reaches through.
					ref, byRef, err := in.hookedRefRead(ctx, unit, container.Object(), hooked, declaring, depth)
					if err == nil && byRef {
						f.bindResult(line, php.MakeRef(ref))
						pc++
						continue
					}
					// Otherwise the property has no slot to hand out. The
					// engine runs the get hook and, unless what comes back is
					// an object -- which can be modified without touching the
					// property -- refuses the write outright.
					var read php.Value
					if err == nil {
						read, err = in.hookedRead(ctx, unit, container.Object(), hooked, declaring, depth)
					}
					if err == nil && read.Deref().Kind() != php.KindObject {
						err = in.throwError(ctx, unit, "Error",
							"Indirect modification of %s::$%s is not allowed",
							php.DisplayName(container.Object().ClassName), property)
					}
					if err != nil {
						if handler, caught := f.raise(in.materialize(ctx, unit, err), pc); caught {
							pc = handler
							continue
						}
						return php.Undef(), err
					}
					f.storeResult(line, read.Deref())
					pc++
					continue
				}
			}
			if isObject {
				property, nameErr := in.propertyName(ctx, unit, f, line.GetOp2Type(), line.GetOp2())
				if nameErr != nil {
					nameErr = in.materialize(ctx, unit, nameErr)
					if target, caught := f.raise(nameErr, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), nameErr
				}
				// Opening a write path to a name the class declares static
				// earns the same complaint a plain write does.
				// Inside a class that declares its own private property of this
				// name, the name means that class's slot, which is not the one the
				// plain name holds when a subclass declares one too.
				property = propertySlot(container.Object(), property, f.class)
				unit.noteStaticAsInstance(ctx, container.Object(), property, magicGet)
				// A property that exists only through __get has no slot to hand
				// out. The engine runs the accessor, says the write will not
				// stick, and gives back the value it produced.
				if overloaded, value, err := in.overloadedWriteFetch(ctx, unit,
					container.Object(), property, f.class, op.GetStrictTypes(), depth); overloaded {
					if err != nil {
						err = in.materialize(ctx, unit, err)
						if handler, caught := f.raise(err, pc); caught {
							pc = handler
							continue
						}
						return php.Undef(), err
					}
					if _, byRef := value.RefOf(); byRef {
						// `&__get` handed back the slot; binding it is what
						// makes a write through the fetch reach the object.
						f.bindResult(line, value)
					} else {
						f.storeResult(line, value)
					}
					pc++
					continue
				}
				// A reference into a readonly property is refused outright.
				// Assigning to one directly is a different opcode, and is what
				// a constructor uses to initialise it.
				//
				// Unset is the exception: `unset($o->p[$k])` on a property that
				// holds nothing has nothing to descend into, so it does nothing
				// at all -- the engine never gets as far as asking who may
				// modify it. A *write* descent still refuses, since it would
				// create the property.
				if strings.HasSuffix(name, "_RW") {
					// A read-modify-write descent reads first, so a typed
					// property holding nothing is reported before who may
					// modify it comes up -- the same order the direct forms
					// use. A pure write descent reads nothing and refuses.
					if err := in.uninitializedTyped(ctx, unit, container.Object(), property); err != nil {
						if handler, caught := f.raise(err, pc); caught {
							pc = handler
							continue
						}
						return php.Undef(), err
					}
				}
				_, isDeclared := container.Object().Declared[property]
				if strings.HasSuffix(name, "_UNSET") && isDeclared &&
					!container.Object().Dynamic[property] &&
					!propertyInitialized(container.Object(), property) {
					f.storeResult(line, php.Null())
					pc++
					continue
				}
				err, copyOnly := in.indirectWriteRefused(ctx, unit, container.Object(), property, f.class)
				if err != nil {
					if handler, caught := f.raise(err, pc); caught {
						pc = handler
						continue
					}
					return php.Undef(), err
				}
				if copyOnly {
					held, _ := container.Object().Get(property)
					f.storeResult(line, held.Deref())
					pc++
					continue
				}
				if strings.HasSuffix(name, "_W") && !in.bindsReference(oplines, pc) {
					// `$o->p[] = 1` where $p is typed and holds nothing would
					// have to grow an array in it, and a type that cannot hold
					// one refuses. Only a plain write descent does: an unset
					// descends into nothing and says nothing, a
					// read-modify-write reads first and warns about the key,
					// and `$r = &$o->p` only takes the slot -- what is done
					// through it is refused when it happens.
					if err := in.refuseArrayAutoInit(ctx, unit, container.Object(), property); err != nil {
						if handler, caught := f.raise(err, pc); caught {
							pc = handler
							continue
						}
						return php.Undef(), err
					}
				}
				if unit.slotAbsent(container.Object(), property) {
					// The fetch is about to make the slot, so this is where the
					// property gets created: `$o->n[] = 1`, `$r = &$o->n` and
					// `unset($o->n[$k])` all earn the deprecation, the same as
					// a plain assignment would.
					_, verdict := unit.propertyAccess(container.Object(), property, f.class)
					unit.noteDynamicProperty(ctx, container.Object(), property, verdict)
					// And it is created holding null, not left with no value:
					// `unset($o->n[$k])` leaves the property behind as null,
					// having descended into nothing.
					container.Object().Set(property, php.Null())
				}
			}
			// An append has to have somewhere to go before a reference to it
			// can be taken, and this refusal comes before anything the
			// assignment itself would say.
			if !isObject && line.GetOp2Type() == opUnused &&
				container.Kind() == php.KindArray && !container.Array().CanAppend() {
				err := in.throwError(ctx, unit, "Error",
					"Cannot add element to the array as the next element is already occupied")
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			// Where the reference came from is recorded, because `$a[0] = &$x`
			// has to replace the *element*, not the temporary this fetch hands
			// back. The temporary carries only the box; the container and key
			// are what a rebinding needs.
			box := elementRef(ctx, container, f, line, isObject)
			// Noted *after* the fetch, since an append has no key until the
			// element exists.
			f.noteWriteTarget(ctx, line, container, isObject)
			f.bindResult(line, php.MakeRef(box))
			pc++

		case "ZEND_FETCH_DIM_UNSET":
			// `unset($a[0][1])` descends to reach the element, but the descent
			// creates nothing: BP_VAR_UNSET is not BP_VAR_W. `unset($x[0][0])`
			// on a null $x leaves it null rather than giving it an element, so
			// the fetch hands back a detached value when there is nothing
			// there and the UNSET_DIM that follows finds no container.
			container := f.fetch(line.GetOp1Type(), line.GetOp1())
			index := f.fetch(line.GetOp2Type(), line.GetOp2())
			if array := container.Deref().Array(); array != nil {
				key := php.ArrayKey(ctx, index)
				if _, present := array.Get(key); present {
					f.bindResult(line, php.MakeRef(array.RefAt(key)))
					f.noteWriteTarget(ctx, line, container, false)
					pc++
					continue
				}
				f.storeResult(line, php.Null())
				pc++
				continue
			}
			if object := container.Deref().Object(); object != nil {
				// offsetGet is a call and a call moves the reported location;
				// the notice below belongs to the unset site.
				where, at := ctx.Location()
				element, err := in.readOffset(ctx, unit, object, index, false, line, op, depth)
				ctx.SetLocation(where, at)
				if err != nil {
					err = in.materialize(ctx, unit, err)
					if handler, caught := f.raise(err, pc); caught {
						pc = handler
						continue
					}
					return php.Undef(), err
				}
				// Descending through offsetGet to unset something *inside* what
				// it returned reaches a copy, and the engine says so before it
				// refuses the unset itself. A reference is the class saying
				// otherwise, and an object is one already.
				if _, byRef := element.RefOf(); !byRef && element.Deref().Kind() != php.KindObject {
					ctx.Notice("Indirect modification of overloaded element of %s has no effect",
						php.DisplayName(object.ClassName))
					element = element.Deref().Copy()
				}
				f.storeResult(line, element)
				pc++
				continue
			}
			// Not a container. The refusals are the ones unset() itself gives,
			// except that a string reports being used *as* an array rather than
			// having an offset unset -- the descent is what fails, not the
			// unset.
			var err error
			if container.Deref().Kind() == php.KindString {
				// The outer offset decides which refusal this is. A legal one
				// picks out a byte, and the complaint is that a byte is being
				// used as an array; an illegal one never gets that far, so what
				// is reported is the unset itself.
				if _, bad := stringOffset(ctx, index, true, false); bad != nil {
					err = in.throwError(ctx, unit, "Error", "Cannot unset string offsets")
				} else {
					err = in.throwError(ctx, unit, "Error", "Cannot use string offset as an array")
				}
			} else {
				err = in.refuseUnsetDim(ctx, unit, container.Deref())
			}
			if err != nil {
				if handler, caught := f.raise(err, pc); caught {
					pc = handler
					continue
				}
				return php.Undef(), err
			}
			f.storeResult(line, php.Null())
			pc++

		case "ZEND_ASSIGN_DIM":
			// op1 is the array, op2 the key, and the value follows in the next
			// opline as OP_DATA -- the engine splits it because an opline has
			// only two operands.
			if pc+1 >= len(oplines) || in.name(oplines[pc+1].GetOpcode()) != "ZEND_OP_DATA" {
				return php.Undef(), fmt.Errorf("%s:%d: ASSIGN_DIM without OP_DATA",
					op.GetFilename(), line.GetLine())
			}
			data := oplines[pc+1]
			// Container, then index, then the value the OP_DATA carries. The
			// order shows twice: `$a[$c] = $b` with both undefined reports $c
			// first, and an error handler that replaces `$a` while the index is
			// being read does not change where the assignment lands -- the
			// container was already taken.
			//
			// Assigning into an element is a write fetch of the container, so
			// an undefined one is created rather than reported.
			container := f.fetchQuiet(line.GetOp1Type(), line.GetOp1())
			// The array is grown out of the slot *now*, before the index is
			// read. A handler that replaces the variable while the index is
			// being evaluated then finds the array already detached, so the
			// assignment lands in it and the variable keeps what the handler
			// gave it. An object or a string is left alone: neither grows.
			grownEarly := false
			if container.Kind() != php.KindString && container.Deref().Object() == nil {
				grown, ok, growErr := in.growArray(ctx, unit, f, line.GetOp1Type(),
					line.GetOp1(), container)
				if growErr != nil {
					if handler, caught := f.raise(in.materialize(ctx, unit, growErr), pc); caught {
						pc = handler
						continue
					}
					return php.Undef(), growErr
				}
				if ok {
					container, grownEarly = grown, true
				}
			}
			var dimIndex php.Value
			if line.GetOp2Type() != opUnused {
				dimIndex = f.fetch(line.GetOp2Type(), line.GetOp2())
			}
			value := f.fetch(data.GetOp1Type(), data.GetOp1()).Copy()
			if container.Kind() == php.KindString && line.GetOp2Type() == opUnused {
				// A string has offsets but no append.
				err := in.throwError(ctx, unit, "Error", "[] operator not supported for strings")
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			if container.Kind() == php.KindString {
				// A string index assigns one byte in place. PHP pads with
				// spaces when the offset is past the end, and takes only the
				// first byte of the value.
				//
				// The empty string is not special. PHP 7.0 grew an array out of
				// it; 7.1 stopped, and 8 treats it as any other string -- so
				// `$s = ""; $s[0] = "a"` yields "a". Excluding it here sent it
				// down the generic scalar path, which answered "Cannot use
				// string offset as an array" for a write the engine performs.
				text := []byte(container.String())
				index := dimIndex
				position, err := stringOffset(ctx, index, false, false)
				if err != nil {
					err = in.materialize(ctx, unit, err)
					if target, caught := f.raise(err, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), err
				}
				offset := int64(position)
				if offset < 0 {
					offset += int64(len(text))
				}
				if offset < 0 {
					// A negative offset that reaches past the front is a
					// warning naming the offset *as written*, and the write
					// simply does not happen -- it is not an error. The
					// assignment answers null rather than the value it was
					// given, since nothing was assigned.
					ctx.Warn("Illegal string offset %d", position)
					f.storeResult(line, php.Null())
					pc += 2
					continue
				}
				replacement := value.String()
				if len(replacement) > 1 {
					ctx.Warn("Only the first byte will be assigned to the string offset")
				}
				if replacement == "" {
					err := in.throwError(ctx, unit, "Error", "Cannot assign an empty string to a string offset")
					if target, caught := f.raise(err, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), err
				}
				for int64(len(text)) <= offset {
					text = append(text, ' ')
				}
				text[offset] = replacement[0]
				f.store(line.GetOp1Type(), line.GetOp1(), php.String(string(text)))
				f.storeResult(line, php.String(replacement[:1]))
				pc += 2
				continue
			}
			if object := container.Deref().Object(); object != nil {
				// $o[$k] = v goes to offsetSet, with a null key for $o[] = v.
				key := php.Null()
				if line.GetOp2Type() != opUnused {
					key = dimIndex
				}
				_, handled, err := in.offsetCall(ctx, unit, object, offsetSet,
					[]php.Value{key, value}, depth)
				if !handled && err == nil {
					if !unit.implementsArrayAccess(object) {
						err = in.indexedObjectError(ctx, unit, object)
					} else {
						return php.Undef(), &UnsupportedError{
							Opcode: "index assignment on " + object.ClassName,
							Line:   line.GetLine(), File: op.GetFilename(),
						}
					}
				}
				if err != nil {
					if target, caught := f.raise(err, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), err
				}
				f.storeResult(line, value)
				pc += 2
				continue
			}
			// Indexing a scalar is a real error but rare enough to fold in with
			// the growing above.
			if !grownEarly {
				err := in.throwError(ctx, unit, "Error", "%s",
					scalarIndexError(container, line, false, false))
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			if line.GetOp2Type() == opUnused {
				if !container.Array().CanAppend() {
					err := in.throwError(ctx, unit, "Error",
						"Cannot add element to the array as the next element is already occupied")
					if target, caught := f.raise(err, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), err
				}
				container.Array().Append(value)
			} else {
				index := dimIndex
				if offending := illegalOffsetType(index); offending != "" {
					err := in.throwError(ctx, unit, "TypeError",
						"Cannot access offset of type %s on array", offending)
					if target, caught := f.raise(err, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), err
				}
				key := php.ArrayKey(ctx, index)
				if pending := ctx.TakePendingError(); pending != nil {
					// Computing the key raised a diagnostic and a handler threw
					// over it, so the element is never written.
					if target, caught := f.raise(pending, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), pending
				}
				if ref := typedRefHeld(container.Array(), key); ref != nil {
					converted, err := in.verifyRefWrite(ctx, unit, ref, value, op.GetStrictTypes())
					if err != nil {
						if target, caught := f.raise(err, pc); caught {
							pc = target
							continue
						}
						return php.Undef(), err
					}
					value = converted
				}
				container.Array().Set(key, value)
			}
			f.storeResult(line, value)
			pc += 2

		case "ZEND_RECV":
			// op1 is the 1-based parameter position.
			index := int(line.GetOp1()) - 1
			if index < 0 || index >= len(f.args) {
				err := in.throwError(ctx, unit, "ArgumentCountError", "%s",
					tooFewArguments(ctx, op, f.class, len(f.args)))
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			argument, err := in.verifyArgument(ctx, unit, f, op, index, f.args[index])
			if err != nil {
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			f.bindSlot(line.GetResultType(), line.GetResult(), argument)
			pc++

		case "ZEND_RECV_VARIADIC":
			// `function f(...$rest)`: everything from this position on becomes
			// one array. op1 is the 1-based position where the collection
			// starts.
			rest := php.NewArray()
			byRefVariadic := false
			if declared := op.GetArgs(); len(declared) > 0 {
				byRefVariadic = declared[len(declared)-1].GetVariadic() &&
					declared[len(declared)-1].GetByRef()
			}
			for i := int(line.GetOp1()) - 1; i >= 0 && i < len(f.args); i++ {
				argument, err := in.verifyArgument(ctx, unit, f, op, i, f.args[i])
				if err != nil {
					if target, caught := f.raise(err, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), err
				}
				if byRefVariadic {
					// `&...$args` collects the caller's slots, not copies of
					// them: writing through an element writes back.
					rest.Append(f.args[i])
					continue
				}
				rest.Append(argument.Deref().Copy())
			}
			// Named arguments the signature has no parameter for land here too,
			// keyed by the name the caller used -- and a by-reference variadic
			// collects their slots, the same as it does for the positional
			// ones above.
			for k, extra := range f.extraNamed {
				// **The declared type applies to a named element too**, and the
				// engine numbers it by its overall position: `t(1, bad: "x")`
				// against `t(int ...$n)` is `Argument #2 must be of type int`.
				// The extras follow the positional arguments, so that position
				// is where this one sits in the whole list -- and `argAt`
				// answers a variadic's ArgInfo for any index past the end.
				value, err := in.verifyArgument(ctx, unit, f, op, len(f.args)+k, extra.value)
				if err != nil {
					if target, caught := f.raise(err, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), err
				}
				if byRefVariadic {
					rest.Set(php.StringKey(extra.name), extra.value)
					continue
				}
				rest.Set(php.StringKey(extra.name), value.Deref().Copy())
			}
			f.bindSlot(line.GetResultType(), line.GetResult(), php.Arr(rest))
			pc++

		case "ZEND_RECV_INIT":
			// A parameter with a default: op2 holds the value used when the
			// caller passed nothing. A default the compiler *folded* is not
			// type-checked -- it already accepted it -- but one that names a
			// constant has no value until the call, and is checked then like
			// any argument: `int $a = STRING_VAL` is an argument TypeError at
			// the call, not a return TypeError from the body.
			index := int(line.GetOp1()) - 1
			// A named-argument call can leave a hole before an argument it did
			// fill. The hole is not "passed": the default applies to it, and
			// func_get_args() shows that default rather than nothing.
			if index >= 0 && index < len(f.args) && f.args[index].Kind() != php.KindUndef {
				argument, err := in.verifyArgument(ctx, unit, f, op, index, f.args[index])
				if err != nil {
					if target, caught := f.raise(err, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), err
				}
				f.bindSlot(line.GetResultType(), line.GetResult(), argument)
			} else {
				def := f.fetch(line.GetOp2Type(), line.GetOp2())
				if initializer, deferred := f.literalAt(line.GetOp2Type(), line.GetOp2()); deferred {
					// A default the compiler could not fold -- one naming a
					// constant, which does not exist until its declaration runs.
					evaluated, ok, err := in.constantLiteral(ctx, unit, initializer, f.class, depth)
					if err != nil {
						err = in.materialize(ctx, unit, err)
						if target, caught := f.raise(err, pc); caught {
							pc = target
							continue
						}
						return php.Undef(), err
					}
					if !ok {
						return php.Undef(), &UnsupportedError{
							Opcode: "default value for " + op.GetName() + unresolvedSuffix(initializer),
							Line:   line.GetLine(), File: op.GetFilename(),
						}
					}
					checked, err := in.verifyArgument(ctx, unit, f, op, index, evaluated)
					if err != nil {
						if target, caught := f.raise(err, pc); caught {
							pc = target
							continue
						}
						return php.Undef(), err
					}
					def = checked
				}
				f.storeResult(line, def)
				if index >= 0 && index < len(f.args) {
					// The hole is now filled, which is what func_get_args and a
					// stack trace report.
					f.args[index] = def
				}
			}
			pc++

		case "ZEND_THROW":
			value := f.fetch(line.GetOp1Type(), line.GetOp1())
			if value.Kind() != php.KindObject {
				// Throwing a non-object is a program error PHP reports as a
				// catchable Error, not a failure of the interpreter.
				err := in.throwError(ctx, unit, "Error", "Can only throw objects")
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			if class, known := unit.findClass(value.Object().ClassName); !known ||
				!unit.isThrowable(class) {
				// An object is not enough: only a Throwable can be thrown, and
				// the engine refuses before anything is in flight.
				err := in.throwError(ctx, unit, "Error",
					"Cannot throw objects that do not implement Throwable")
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			// The throw site is what getLine reports, and it is only known here
			// -- but only for an object whose position was never recorded. A
			// constructor that wrote its own keeps it, zero included: bug64821
			// sets `$this->line = 0` and expects `:0` in the report.
			if _, ok := value.Object().Get("line"); !ok {
				value.Object().Set("line", php.Int(int64(line.GetLine())))
				value.Object().Set("file", php.String(op.GetFilename()))
			}
			err := &thrown{object: value.Object()}
			if target, caught := f.raise(err, pc); caught {
				pc = target
				continue
			}
			return php.Undef(), err

		case "ZEND_CATCH":
			top, inFlight := f.topUnwind()
			if !inFlight {
				return php.Undef(), fmt.Errorf("%s:%d: catch with nothing in flight",
					op.GetFilename(), line.GetLine())
			}
			wanted := f.fetch(line.GetOp1Type(), line.GetOp1()).String()
			class, known := unit.findClass(top.object.ClassName)
			if known && unit.instanceOf(class, wanted) {
				caught := top.object
				f.popUnwind()
				// The result is the variable the clause binds. A `catch (E)`
				// with no variable leaves it unused.
				if line.GetResultType() != opUnused {
					// Binding is an assignment like any other: a variable that
					// is a reference to a typed property is still held to that
					// type, and the TypeError replaces the exception being
					// caught rather than joining it.
					if ref, isRef := f.rawSlot(line.GetResultType(), line.GetResult()).RefOf(); isRef {
						if _, err := in.verifyRefWrite(ctx, unit, ref, php.Obj(caught),
							op.GetStrictTypes()); err != nil {
							if target, handled := f.raise(err, pc); handled {
								pc = target
								continue
							}
							return php.Undef(), err
						}
					}
					f.storeResult(line, php.Obj(caught))
				} else if err := in.releaseCheck(ctx, unit, php.Obj(caught)); err != nil {
					// Nothing holds the exception once it is caught without a
					// variable -- `OBJ_RELEASE(exception)` -- so a destructor
					// on it runs *here*, and what that throws takes the place
					// of the catch body rather than following it.
					if target, handled := f.raise(err, pc); handled {
						pc = target
						continue
					}
					return php.Undef(), err
				}
				pc++
				continue
			}
			// No match. op2 points at the next catch clause; past the last one
			// the exception resumes its search from here -- which is not the
			// same as leaving the frame, because this try may still have a
			// finally, and so may an enclosing one.
			if line.GetExtendedValue()&extLastCatch != 0 {
				f.popUnwind()
				escaped := &thrown{object: top.object}
				if target, caught := f.raise(escaped, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), escaped
			}
			pc = int(line.GetOp2())

		case "ZEND_JMP_FRAMELESS":
			// Emitted for an unqualified call made inside a namespace, where the
			// name could mean either the namespaced function or the global one.
			// op1 is the *namespaced* candidate: if the program defined it the
			// call falls through to the ordinary sequence, and if it did not,
			// the jump leads to the frameless call of the global function.
			candidate := f.fetch(line.GetOp1Type(), line.GetOp1()).String()
			if _, declared := unit.functions[functionKey(candidate)]; declared {
				pc++
				continue
			}
			pc = int(line.GetOp2())

		case "ZEND_DECLARE_FUNCTION":
			// A function declared inside a conditional is not registered until
			// the declaration runs, so its body is attached to the enclosing
			// op_array the way a closure's is rather than sitting in the unit's
			// function table from the start.
			nested := op.GetClosures()
			if int(line.GetOp2()) >= len(nested) {
				return php.Undef(), fmt.Errorf("%s:%d: function definition %d out of range (%d declared)",
					op.GetFilename(), line.GetLine(), line.GetOp2(), len(nested))
			}
			declaredName := strings.ToLower(f.fetch(line.GetOp1Type(), line.GetOp1()).String())
			if in.scopedFunctions[declaredName] {
				return php.Undef(), &UnsupportedError{Opcode: "eval declaration collides with generated function"}
			}
			if previous, taken := unit.functions[declaredName]; taken {
				// A name is claimed once. The diagnostic points at where it was
				// claimed, which is what tells the two declarations apart.
				return php.Undef(), in.fatal(ctx,
					"Cannot redeclare function %s() (previously declared in %s:%d)",
					nested[line.GetOp2()].GetName(),
					previous.GetFilename(), previous.GetLineStart())
			}
			unit.functions[declaredName] = nested[line.GetOp2()]
			unit.functionOrder = append(unit.functionOrder, declaredName)
			pc++

		case "ZEND_DECLARE_LAMBDA_FUNCTION":
			// op2 indexes the nested op_arrays the compiler attached to this
			// one. A closure body is not registered by name, so this is the only
			// way to reach it.
			nested := op.GetClosures()
			if int(line.GetOp2()) >= len(nested) {
				return php.Undef(), fmt.Errorf("%s:%d: closure %d out of range (%d declared)",
					op.GetFilename(), line.GetLine(), line.GetOp2(), len(nested))
			}
			// A `static function () {}` written inside a method has no $this
			// even though one is in scope: the engine leaves `this_ptr` undef,
			// which is what makes unbinding it a no-op rather than a warning.
			declared := nested[line.GetOp2()]
			bound := f.this
			if declared.GetStatic() {
				bound = nil
			}
			f.storeResult(line, php.Obj(unit.newClosure(declared, bound, f.class, f.called)))
			pc++

		case "ZEND_BIND_LEXICAL":
			// The name is the one the enclosing scope uses, and PHP requires the
			// closure to declare the same name, so no index lookup is needed.
			name := f.varName(line.GetOp2())
			// `use (&$x)` shares the enclosing slot; the by-value form captures
			// a copy taken now, not when the closure runs.
			var captured php.Value
			if line.GetExtendedValue()&bindByRef != 0 {
				// Binding by reference does not read the variable, so an unset
				// one is bound silently -- it is about to be written through.
				ref, ok := f.slotRef(line.GetOp2Type(), line.GetOp2())
				if !ok {
					return php.Undef(), &UnsupportedError{
						Opcode: "use (&$var) on a non-variable",
						Line:   line.GetLine(), File: op.GetFilename(),
					}
				}
				captured = sharedRef(ref)
			} else if captured = f.fetch(line.GetOp2Type(), line.GetOp2()); captured.Kind() == php.KindUndef {
				// Capturing an unset variable warns here, once, and what gets
				// captured is the null the read produced. Keeping it undefined
				// instead would warn again on every read inside the body.
				captured = php.Null()
			}
			if !bindLexical(f.fetch(line.GetOp1Type(), line.GetOp1()), name, captured) {
				return php.Undef(), fmt.Errorf("%s:%d: lexical bind on a non-closure",
					op.GetFilename(), line.GetLine())
			}
			pc++

		case "ZEND_BIND_STATIC":
			// One opcode covers two things: pulling in a closure's captured
			// variable, and installing a function's `static $x` on first use.
			name := f.varName(line.GetOp1())
			// The slot is about to be rebound, so whatever it held loses this
			// holder. If that was its last one the destructor runs *here* --
			// inside whatever try is open -- rather than at shutdown.
			displaced := f.rawSlot(line.GetOp1Type(), line.GetOp1())
			switch value, captured := f.bound[name]; {
			case captured:
				f.bindSlot(line.GetOp1Type(), line.GetOp1(), value)
			case line.GetOp2Type() != opUnused:
				// An initializer the compiler could not fold was emitted as
				// oplines instead, and this is where the computed value lands.
				// BIND_INIT_STATIC_OR_JMP jumped past all of it on every call
				// after the first, so reaching here means it must be stored.
				initial := f.fetch(line.GetOp2Type(), line.GetOp2()).Copy()
				if initial.IsUndef() {
					// The initializer read something that was not there. It
					// warned as it went, and what it yields is null -- a
					// static's box always holds a value, which is why later
					// reads of the name are silent.
					initial = php.Null()
				}
				boxed := unit.setStatic(op, name, initial, f.class)
				f.bindSlot(line.GetOp1Type(), line.GetOp1(), boxed)
				bindGlobalStatic(ctx, unit, f, line, boxed)
			default:
				// Statics persist across calls, so the store belongs to the
				// op_array rather than this frame.
				value, ok, err := in.staticSlot(ctx, unit, op, name, f.class, depth)
				if err != nil {
					err = in.materialize(ctx, unit, err)
					if handler, caught := f.raise(err, pc); caught {
						pc = handler
						continue
					}
					return php.Undef(), err
				}
				if ok {
					// The slot *is* the static's box, so a write through it is
					// visible to a call made from inside this one.
					f.bindSlot(line.GetOp1Type(), line.GetOp1(), value)
					bindGlobalStatic(ctx, unit, f, line, value)
				}
			}
			if released := in.releaseCheck(ctx, unit, displaced); released != nil {
				released = in.materialize(ctx, unit, released)
				if handler, caught := f.raise(released, pc); caught {
					pc = handler
					continue
				}
				return php.Undef(), released
			}
			pc++

		case "ZEND_INIT_DYNAMIC_CALL", "ZEND_INIT_USER_CALL":
			callee := f.fetch(line.GetOp2Type(), line.GetOp2())
			if name == "ZEND_INIT_USER_CALL" {
				// call_user_func and its siblings validate the callback before
				// calling anything, and name themselves and the reason. A
				// plain `$f(...)` does not: that is a bare Error instead.
				builtin := f.fetch(line.GetOp1Type(), line.GetOp1()).String()
				reason := in.callableReason(ctx, unit, callee)
				var err error
				if pending := ctx.TakePendingError(); pending != nil {
					// Resolving the callback raised a diagnostic -- the
					// deprecated spellings do -- and a user error handler
					// turned it into a throw. The resolution never finished,
					// so there is no reason to report: the engine prints the
					// empty one it started with, and the handler's exception
					// hangs off the TypeError as its cause.
					err = in.throwChained(ctx, unit, pending, "TypeError",
						"%s(): Argument #1 ($callback) must be a valid callback, (null)",
						builtin)
				} else if reason != "" {
					err = in.throwError(ctx, unit, "TypeError",
						"%s(): Argument #1 ($callback) must be a valid callback, %s",
						builtin, reason)
				}
				if err != nil {
					// The callee arrived in a temporary rather than through a
					// SEND, so nothing else will let go of it: an object that
					// was not a valid callback is dropped here, inside whatever
					// try is open. A throw from its destructor joins the one
					// already on its way out.
					if f.releaseTemp(unit, line.GetOp2Type(), line.GetOp2()) {
						if lazyErr := in.releaseCheck(ctx, unit, callee); lazyErr != nil {
							err = chainOnto(in.materialize(ctx, unit, lazyErr), err)
						}
					}
					if handler, caught := f.raise(err, pc); caught {
						pc = handler
						continue
					}
					return php.Undef(), err
				}
			}
			// call_user_func and its siblings take a `callable` argument, whose
			// parsing is what resolves `self` and friends; a bare `$f(...)`
			// goes nowhere near it.
			userCall := name == "ZEND_INIT_USER_CALL"
			target, ok := unit.dynamicTargetQuiet(ctx, callee, userCall, userCall)
			in.canonicalCallName(target)
			if !ok {
				// A callee the program got wrong is its own error; only a
				// callee the runtime cannot model is a gap.
				if message := unit.callableError(callee); message != "" {
					err := in.throwError(ctx, unit, "Error", "%s", message)
					if handler, caught := f.raise(err, pc); caught {
						pc = handler
						continue
					}
					return php.Undef(), err
				}
				return php.Undef(), &UnsupportedError{
					Opcode: "dynamic call to " + callee.Kind().String(),
					Line:   line.GetLine(), File: op.GetFilename(),
				}
			}
			// Both opcodes name their callee at run time, which is what the
			// scope-introspection functions refuse.
			target.dynamic = true
			target.viaUserCall = name == "ZEND_INIT_USER_CALL"
			if target.viaUserCall {
				// call_user_func and its siblings lend the caller's instance to
				// a callable that named a *class* and an instance method, where
				// a plain `$c()` refuses outright. Probed both ways from the
				// same method: call_user_func(["P", "m"]) runs with $this,
				// `$c = ["P", "m"]; $c()` is an Error.
				unit.forwardThis(ctx, target)
			}
			calls = append(calls, target)
			pc++

		case "ZEND_VERIFY_NEVER_TYPE":
			// A `never` function must not reach its end. The compiler puts this
			// opcode where control would fall off, so arriving at it *is* the
			// error -- there is nothing to check.
			// The word is "method" when it is one: the message names what the
			// declaration was, not what the check is.
			kind := "function"
			if f.class != nil {
				kind = "method"
			}
			err := in.throwError(ctx, unit, "TypeError",
				"%s(): never-returning %s must not implicitly return",
				functionLabel(f, op), kind)
			if target, caught := f.raise(err, pc); caught {
				pc = target
				continue
			}
			return php.Undef(), err

		case "ZEND_VERIFY_RETURN_TYPE":
			// An unused op1 is the end of the body with no return statement.
			// For anything but void and never that is itself the error: PHP
			// requires the value, and "none returned" is how it says so.
			if line.GetOp1Type() == opUnused {
				declared := frameType(declaredReturnType(op), f)
				if declared != "" && declared != "void" && declared != "never" {
					err := in.throwError(ctx, unit, "TypeError",
						"%s(): Return value must be of type %s, none returned",
						functionLabelScoped(f, op), displayType(declared))
					if target, caught := f.raise(err, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), err
				}
			}
			if line.GetOp1Type() != opUnused {
				returned := f.fetch(line.GetOp1Type(), line.GetOp1())
				// `static` in a return type is a promise about the class the
				// call was made on, so it can only be checked here, where that
				// class is known -- and the diagnostic names the class rather
				// than the keyword.
				declared := frameType(declaredReturnType(op), f)
				converted, ok := in.checkType(ctx, unit, declared, returned, op.GetStrictTypes())
				if !ok {
					// Unlike an argument error this names no call site: the
					// return is the callee's own doing. And unlike an argument,
					// a __toString that threw while rendering the value does
					// *not* replace it: the TypeError still comes out, with
					// that exception hanging off it as the cause.
					err := in.throwChained(ctx, unit, coercionThrow(ctx), "TypeError",
						"%s(): Return value must be of type %s, %s returned",
						functionLabelScoped(f, op), displayType(frameType(op.GetReturnType(), f)),
						givenTypeName(returned))
					if target, caught := f.raise(err, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), err
				}
				if line.GetResultType() != opUnused {
					f.storeResult(line, converted)
				} else if ref, isRef := f.rawSlot(line.GetOp1Type(),
					line.GetOp1()).RefOf(); isRef && op.GetReturnsRef() {
					// A by-reference return hands the *box* back, so the
					// conversion goes through it. Replacing the slot would drop
					// the reference -- and with it the second check the
					// compiler emits after the finally, which is the one that
					// sees what the finally did to the variable.
					ref.Set(converted)
				} else {
					// With no result slot the conversion happens in place, in
					// op1, which the RETURN that follows reads. It separates
					// first, so a variable that was a reference does not carry
					// the converted value back to whatever it aliased.
					f.bindSlot(line.GetOp1Type(), line.GetOp1(), converted)
				}
			}
			pc++

		case "ZEND_CLONE":
			source := f.fetch(line.GetOp1Type(), line.GetOp1())
			if held := source.Object(); held != nil && held.Lazy != nil {
				// A copy has to have something to copy, so the state is produced first.
				resolved, lazyErr := in.resolveLazy(ctx, unit, held, "", "", depth)
				if lazyErr != nil {
					lazyErr = in.materialize(ctx, unit, lazyErr)
					if handler, caught := f.raise(lazyErr, pc); caught {
						pc = handler
						continue
					}
					return php.Undef(), lazyErr
				}
				_ = resolved
			}
			if source.Kind() != php.KindObject {
				err := in.throwError(ctx, unit, "Error", "__clone method called on non-object")
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			if class, ok := unit.findClass(source.Object().ClassName); ok &&
				(class.decl.GetKind() == pb.Class_KIND_ENUM || class.decl.GetNoClone()) {
				// An enum case is a singleton; there is nothing a second copy
				// of one could mean. Some engine classes refuse for reasons of
				// their own -- Generator, Fiber, WeakReference -- and say so
				// with the same words. Which ones is the engine's answer, taken
				// from the class rather than from a list here.
				err := in.throwError(ctx, unit, "Error",
					"Trying to clone an uncloneable object of class %s",
					php.DisplayName(class.Name))
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			// clone is shallow: properties are copied with array value semantics,
			// but a property holding an object still points at the same instance.
			cloned := unit.born(php.NewObject(source.Object().ClassName, unit.handle()))
			copyState(source.Object(), cloned)
			adoptPropertyTypes(cloned)
			if lazy := source.Object().Lazy; lazy != nil && lazy.Instance != nil {
				// A proxy stays a proxy when copied: what the copy forwards to
				// is a copy of the instance, not the same one.
				instance := unit.born(php.NewObject(lazy.Instance.ClassName, unit.handle()))
				copyState(lazy.Instance, instance)
				adoptPropertyTypes(instance)
				cloned.Lazy = &php.Lazy{
					Proxy: true, Initializer: lazy.Initializer, Instance: instance,
				}
			}
			f.storeResult(line, php.Obj(cloned))
			// __clone runs on whatever holds the state that was copied. For a
			// proxy that is the new instance, not the proxy: the proxy has no
			// properties of its own for it to work on.
			subject := cloned
			if cloned.Lazy != nil && cloned.Lazy.Instance != nil {
				subject = cloned.Lazy.Instance
			}
			// __clone runs after the copy, and only if the class declares it.
			if class, ok := unit.findClass(subject.ClassName); ok {
				if method, declaring, found := unit.resolveMethod(class, "__clone"); found {
					// Since PHP 8.3 __clone may reinitialize the clone's
					// readonly properties, once each, and only while it runs.
					for _, prop := range unit.properties(class) {
						if prop.GetReadonly() && !prop.GetStatic() {
							subject.AllowReinit(prop.GetName())
						}
					}
					// `__clone` is a frame in a backtrace, at the site of the
					// `clone`, so an exception out of it shows
					// `#0 file(line): C->__clone()`.
					site, at := ctx.Location()
					ctx.PushCall(php.StackFrame{
						Function: php.DisplayName(declaring.Name) + "->__clone",
						File:     site, Line: at, This: subject,
					})
					_, err := in.invoke(ctx, unit, method, subject, declaring, class, nil, depth)
					ctx.PopCall()
					subject.EndReinit()
					if err != nil {
						if target, caught := f.raise(err, pc); caught {
							pc = target
							continue
						}
						return php.Undef(), err
					}
				}
			}
			pc++

		case "ZEND_FREE", "ZEND_FE_FREE_UNUSED":
			// Releasing a temporary. Go's GC makes the release itself a no-op,
			// but clearing the slot keeps a stale value from being read back.
			// bindSlot, not store: the temporary may hold a reference to
			// something still live -- `[&$p] = $a` frees a box aliasing $a --
			// and writing through it would destroy the variable.
			discarded := f.fetchQuiet(line.GetOp1Type(), line.GetOp1())
			f.bindSlot(line.GetOp1Type(), line.GetOp1(), php.Undef())
			// The compiler emits this where a value stops being needed, which
			// is exactly where PHP's refcount would have reached zero: the
			// object a statement built and did not keep dies here.
			if lazyErr := in.releaseCheck(ctx, unit, discarded); lazyErr != nil {
				lazyErr = in.materialize(ctx, unit, lazyErr)
				if target, caught := f.raise(lazyErr, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), lazyErr
			}
			pc++

		case "ZEND_DECLARE_CONST":
			name := f.fetch(line.GetOp1Type(), line.GetOp1()).String()
			value := f.fetch(line.GetOp2Type(), line.GetOp2())
			if initializer, deferred := f.literalAt(line.GetOp2Type(), line.GetOp2()); deferred {
				// The compiler could not fold this one -- it refers to another
				// constant, which only exists once its own declaration has run.
				evaluated, ok, err := in.constantLiteral(ctx, unit, initializer, f.class, depth)
				if err != nil {
					err = in.materialize(ctx, unit, err)
					if target, caught := f.raise(err, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), err
				}
				if !ok {
					return php.Undef(), &UnsupportedError{
						Opcode: "constant initializer for " + name + unresolvedSuffix(initializer),
						Line:   line.GetLine(), File: op.GetFilename(),
					}
				}
				value = evaluated
			}
			if !ctx.Define(name, value.Copy()) {
				ctx.Warn("Constant %s already defined", name)
			}
			pc++

		case "ZEND_FETCH_CLASS_CONSTANT":
			className, err := in.classNameOperand(ctx, unit, f, line, line.GetOp1Type(), line.GetOp1())
			if err != nil {
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			constName := f.fetch(line.GetOp2Type(), line.GetOp2()).String()
			// Naming a class here can trigger autoloading just as `new` does,
			// and a name nothing declares is reported as the missing class
			// rather than as a missing constant on it.
			_, found, loadErr := in.resolveClass(ctx, unit, className, depth)
			if loadErr != nil {
				if handler, caught := f.raise(loadErr, pc); caught {
					pc = handler
					continue
				}
				return php.Undef(), loadErr
			}
			if !found {
				err := in.throwError(ctx, unit, "Error", "Class \"%s\" not found", className)
				if handler, caught := f.raise(err, pc); caught {
					pc = handler
					continue
				}
				return php.Undef(), err
			}
			if constName == "class" {
				// `Foo::{$name}` with $name "class" is the class's own name,
				// not a constant lookup. Written out, `Foo::class` is folded by
				// the compiler and never reaches here; the dynamic form has to
				// answer for itself. The match is exact -- `Foo::{'CLASS'}` is
				// an undefined constant.
				resolved := className
				if class, known := unit.findClass(className); known {
					resolved = class.Name
				}
				f.storeResult(line, php.String(resolved))
				pc++
				continue
			}
			if owner, visibility, allowed := unit.constantAccess(className, constName,
				f.class); !allowed {
				err := in.throwError(ctx, unit, "Error",
					"Cannot access %s constant %s::%s", visibility,
					php.DisplayName(owner), constName)
				if handler, caught := f.raise(err, pc); caught {
					pc = handler
					continue
				}
				return php.Undef(), err
			}
			value, ok, cerr := in.classConstant(ctx, unit, className, constName, depth)
			if cerr != nil {
				cerr = in.materialize(ctx, unit, cerr)
				if handler, caught := f.raise(cerr, pc); caught {
					pc = handler
					continue
				}
				return php.Undef(), cerr
			}
			if !ok {
				// PHP leaves a class constant unquoted here, unlike the plain
				// form, which it quotes.
				err := in.throwError(ctx, unit, "Error", "Undefined constant %s::%s",
					className, constName)
				if handler, caught := f.raise(err, pc); caught {
					pc = handler
					continue
				}
				return php.Undef(), err
			}
			f.storeResult(line, value)
			pc++

		case "ZEND_DEFINED":
			// defined() gets its own opcode so the engine can cache the lookup.
			// It asks whether the name exists without *using* it, so a
			// deprecated constant is not reported here.
			unit.silentDeprecation++
			_, ok, err := in.namedConstant(ctx, unit, f.fetch(line.GetOp1Type(), line.GetOp1()).String(), depth)
			unit.silentDeprecation--
			if err != nil {
				err = in.materialize(ctx, unit, err)
				if handler, caught := f.raise(err, pc); caught {
					pc = handler
					continue
				}
				return php.Undef(), err
			}
			f.storeResult(line, php.Bool(ok))
			pc++

		case "ZEND_FETCH_CONSTANT":
			name := f.fetch(line.GetOp2Type(), line.GetOp2()).String()
			value, ok := in.lookupConstant(ctx, name)
			if !ok && line.GetOp1()&unqualifiedInNamespace != 0 {
				// An unqualified constant inside a namespace falls back to the
				// global one, the same way an unqualified call does. A name
				// written out with its namespace does not fall back, which is
				// why the compiler's flag decides rather than the spelling.
				if at := strings.LastIndexByte(name, '\\'); at >= 0 {
					value, ok = in.lookupConstant(ctx, name[at+1:])
				}
			}
			if ok {
				in.noteDeprecatedConstantName(ctx, unit, name)
			}
			if !ok {
				// A name no constant table knows is a program error, not a gap
				// in the interpreter: PHP throws, and the program may catch it.
				err := in.throwError(ctx, unit, "Error", "Undefined constant \"%s\"", name)
				if handler, caught := f.raise(err, pc); caught {
					pc = handler
					continue
				}
				return php.Undef(), err
			}
			f.storeResult(line, value)
			pc++

		case "ZEND_INIT_FCALL", "ZEND_INIT_FCALL_BY_NAME", "ZEND_INIT_NS_FCALL_BY_NAME":
			callee := f.fetch(line.GetOp2Type(), line.GetOp2()).String()
			if name == "ZEND_INIT_NS_FCALL_BY_NAME" {
				callee = in.resolveNamespacedFunction(unit, callee)
			}
			// The compiler stores the name lowercased; a stack trace shows the
			// declaration's own spelling, so the declared name wins when there
			// is one.
			if fn, declared := unit.functions[functionKey(callee)]; declared && fn.GetName() != "" {
				callee = fn.GetName()
			}
			calls = append(calls, &pendingCall{name: callee})
			pc++

		case "ZEND_SEND_VAL", "ZEND_SEND_VAL_EX", "ZEND_SEND_VAR",
			"ZEND_SEND_VAR_EX", "ZEND_SEND_VAR_NO_REF", "ZEND_SEND_VAR_NO_REF_EX",
			"ZEND_SEND_FUNC_ARG", "ZEND_SEND_USER":
			if len(calls) == 0 {
				return php.Undef(), fmt.Errorf("%s:%d: argument outside a call",
					op.GetFilename(), line.GetLine())
			}
			current := calls[len(calls)-1]
			if name == "ZEND_SEND_USER" {
				// The compiler folds `call_user_func('f', $x)` into a direct
				// call whose arguments are sent with this opcode, so it -- not
				// the INIT -- is what marks a call made that way.
				current.viaUserCall = true
			}
			// SEND_FUNC_ARG and SEND_USER are the forms whose by-reference-ness
			// the compiler could not settle. When the callee does want a
			// reference, the operand is already a box the matching
			// FETCH_*_FUNC_ARG produced, so it is passed on rather than read
			// through.
			// SEND_VAR_EX is the same situation reached the other way: the
			// compiler knew the argument is a variable but not whether the
			// callee wants it by reference, which for a dynamic call is only
			// knowable now.
			if name == "ZEND_SEND_VAL" || name == "ZEND_SEND_VAL_EX" {
				// A literal or a computed value has no slot to hand over, so a
				// parameter that wants one cannot be satisfied. The engine
				// refuses at the send rather than letting the body run against
				// a copy.
				//
				// Only for a body the program wrote: an engine function's
				// by-reference parameter may be *prefer*-ref -- extract()'s is
				// -- which takes a temporary happily, and the harvested table
				// php2go keeps does not tell the two apart.
				if pendingCallee(unit, current) != nil &&
					in.sendArgumentByRef(unit, f, calls, line) {
					// get_function_or_method_name always joins with `::`,
					// whether or not the call has an instance -- and a
					// parameter with no declared name drops the whole
					// ` ($name)` clause rather than leaving it empty.
					clause := ""
					if named := calleeParameterName(unit, current, line.GetOp2()); named != "" {
						clause = " ($" + named + ")"
					}
					err := in.throwError(ctx, unit, "Error",
						"%s(): Argument #%d%s could not be passed by reference",
						staticCallName(current), line.GetOp2(), clause)
					if target, caught := f.raise(err, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), err
				}
			}
			if strings.HasPrefix(name, "ZEND_SEND_VAR_NO_REF") &&
				line.GetOp2Type() != opConst &&
				in.argumentByRef(unit, calls, line.GetOp2()) {
				// This is the form a *call result* is sent with, and it copies
				// the zval as it stands. A `function &f()` returned a
				// reference, so that reference is what the callee gets -- but
				// a value is passed as a value, with the notice below, rather
				// than being made into a reference the way a variable is.
				if raw := f.rawSlot(line.GetOp1Type(), line.GetOp1()); raw.IsRef() {
					current.args = append(current.args, raw)
					pc++
					continue
				}
			}
			if (strings.HasSuffix(name, "_FUNC_ARG") || name == "ZEND_SEND_USER" ||
				name == "ZEND_SEND_VAR_EX") && line.GetOp2Type() != opConst {
				// A *named* argument does not take this path: it has no
				// position to be appended at, and the named branch below binds
				// its slot instead.
				if in.sendArgumentByRef(unit, f, calls, line) {
					if raw := f.rawSlot(line.GetOp1Type(), line.GetOp1()); raw.IsRef() {
						current.args = append(current.args, raw)
						pc++
						continue
					}
					ref, ok := f.slotRef(line.GetOp1Type(), line.GetOp1())
					if ok {
						current.args = append(current.args, php.MakeRef(ref))
						pc++
						continue
					}
				}
			}
			passedByValue := false
			if strings.HasPrefix(name, "ZEND_SEND_VAR_NO_REF") &&
				line.GetOp2Type() != opConst &&
				!f.rawSlot(line.GetOp1Type(), line.GetOp1()).IsRef() &&
				(name == "ZEND_SEND_VAR_NO_REF" ||
					in.userArgumentByRef(unit, calls, line.GetOp2())) {
				// The compiler emits this form when it knows the parameter is
				// by reference but could not prove the argument is a variable
				// -- a call result is the usual case. Passing one is legal but
				// pointless: the callee writes to something nobody can read.
				//
				// The _EX form is the one the compiler could not settle at all
				// -- a function declared later in the file, say -- so the
				// callee has to be asked. Only a callee written in PHP is
				// asked: see userArgumentByRef for why an engine one is not.
				ctx.Notice("Only variables should be passed by reference")
				// The callee writes into something nobody can read, and that
				// has to include whatever the temporary was made from:
				// `array_pop(C::get())` must not empty the array the getter
				// returned. php2go hands arrays over by pointer, so the
				// detachment the engine gets from copy-on-write is made here.
				passedByValue = true
			}
			if line.GetOp2Type() == opConst {
				// A named argument: op2 holds the parameter name rather than
				// the position. Which slot it fills is not known until the
				// callee is, so the value travels with the name.
				sent := passedValue(f.fetch(line.GetOp1Type(), line.GetOp1()))
				if in.sendArgumentByRef(unit, f, calls, line) {
					// The parameter it names wants a slot, and a variable can
					// give one -- the same rule a positional argument follows,
					// reached through the name instead of the position.
					if raw := f.rawSlot(line.GetOp1Type(), line.GetOp1()); raw.IsRef() {
						sent = raw
					} else if ref, ok := f.slotRef(line.GetOp1Type(), line.GetOp1()); ok {
						sent = php.MakeRef(ref)
					}
				}
				current.named = append(current.named, namedArgument{
					name:  f.fetch(line.GetOp2Type(), line.GetOp2()).String(),
					value: sent,
				})
				pc++
				continue
			}
			sent := passedValue(f.fetch(line.GetOp1Type(), line.GetOp1()))
			if passedByValue && sent.Deref().Kind() == php.KindArray {
				sent = sent.Deref().Copy()
			}
			current.args = append(current.args, sent)
			// The call takes the argument; a temporary that held it has no
			// other reader. Clearing it is what lets `f(new C())` destroy the
			// C when f returns, which is where PHP destroys it.
			if f.releaseTemp(unit, line.GetOp1Type(), line.GetOp1()) {
				current.releasedTemps = true
			}
			pc++

		case "ZEND_DO_FCALL", "ZEND_DO_UCALL", "ZEND_DO_ICALL", "ZEND_DO_FCALL_BY_NAME":
			if len(calls) == 0 {
				return php.Undef(), fmt.Errorf("%s:%d: call with no callee",
					op.GetFilename(), line.GetLine())
			}
			current := calls[len(calls)-1]
			calls = calls[:len(calls)-1]
			// PHP resolves a function name when the call is set up, so calling
			// something that does not exist fails before any frame is entered
			// and its trace shows only the caller. Raising it after the push
			// would invent a frame for a callee that was never reached.
			if current.plain() && len(in.engineFunctions) > 0 && !in.functionExists(unit, current.name) {
				err := in.throwError(ctx, unit, "Error", "Call to undefined function %s()", current.name)
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			// The call is on the stack for as long as it runs, so an exception
			// created inside it reports the frames it was created under. The
			// site recorded is this opline: PHP's trace names where the call was
			// made, not where the callee is.
			// #[\Deprecated] is reported when the call is made, before the
			// callee runs. A plain call names its target and resolves it here,
			// so the lookup happens here too.
			var deprecationErr error
			switch callee := current.method; {
			case callee != nil:
				// A call that landed on __call or __callStatic is reported
				// against the name the program wrote.
				deprecationErr = in.noteDeprecatedCallAs(ctx, unit, callee, current.class,
					current.forwardTo, depth)
			case current.closure != nil:
				// A closure is a function, and PHP 8.4 gives it the compiled
				// name the trace uses -- but one that wraps a *method* is
				// reported as that method, without the `Class->` a trace name
				// carries in front of it.
				as := closureTraceName(current.closure)
				if current.closure.class != nil {
					as = current.closure.op.GetName()
				}
				deprecationErr = in.noteDeprecatedCallAs(ctx, unit, current.closure.op,
					current.closure.class, as, depth)
			case current.name != "":
				if fn, ok := unit.userFunctionBody(current.name); ok {
					deprecationErr = in.noteDeprecatedCall(ctx, unit, fn, nil, depth)
				}
			}
			if deprecationErr != nil {
				// The attribute would not construct, so the call never happens:
				// the TypeError is reported against the constructor, at the
				// call site that triggered it. The pending call has already
				// been popped above.
				if target, caught := f.raise(deprecationErr, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), deprecationErr
			}
			// Coercion is the *caller's* decision, so the call site's setting
			// travels with the call rather than being read off the callee.
			unit.pending.strict = op.GetStrictTypes()
			// `[$closure, "__invoke"]` reaches the body through a trampoline
			// the engine builds, and that trampoline is what a diagnostic
			// would have to name a call site for. It has none.
			unit.pending.viaTrampoline = current.invokedAs != ""
			if err := in.applyNamedArguments(ctx, unit, current); err != nil {
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			if current.viaUserCall {
				// The callee wanted slots and got values. PHP says so once per
				// argument, before the body runs -- and the values really are
				// values: an argument that arrived as a reference is copied, so
				// the body writes to nothing the caller can see.
				in.noteUserCallByRef(ctx, unit, current)
				for i, arg := range current.args {
					if arg.IsRef() {
						current.args[i] = arg.Deref().Copy()
					}
				}
			}
			if current.refusal != "" {
				err := in.throwError(ctx, unit, "Error", "%s", current.refusal)
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			shownArgs, shownNames := current.traceArgs(unit.pending.extraNamed)
			ctx.PushCall(php.StackFrame{
				Function: current.traceName(), File: op.GetFilename(),
				Line: line.GetLine(), Args: shownArgs, ArgNames: shownNames,
				Sensitive: in.sensitiveArgs(unit, current, len(shownArgs)),
				This:      current.this,
			})
			var returned php.Value
			var err error
			switch {
			case current.hook.name != "":
				returned, err = in.runHook(ctx, unit, current.hook, current.method,
					current.this, current.class, current.called, current.args, depth)
			case current.hookFallback != nil:
				// The parent declares the property but no hook of this kind, so
				// the call reaches its backing store directly.
				returned, err = unit.hookBackingAccess(current)
				err = in.materialize(ctx, unit, err)
			case current.method != nil:
				returned, err = in.invoke(ctx, unit, current.method, current.this, current.class,
					current.called, current.forwardArgs(), depth)
			case current.fiberStatic != "":
				var handled bool
				returned, handled, err = in.fiberStaticCall(ctx, unit, current.fiberStatic, current.args)
				if !handled {
					err = &UnsupportedError{
						Opcode: "Fiber::" + current.fiberStatic,
						Line:   line.GetLine(), File: op.GetFilename(),
					}
				}
				err = in.materialize(ctx, unit, err)
			case current.weakStatic != "":
				var handled bool
				returned, handled, err = in.weakStaticCall(unit, "WeakReference",
					current.weakStatic, current.args)
				if !handled {
					err = &UnsupportedError{
						Opcode: "WeakReference::" + current.weakStatic,
						Line:   line.GetLine(), File: op.GetFilename(),
					}
				}
				err = in.materialize(ctx, unit, err)
			case current.closureStatic != "":
				var handled bool
				returned, handled, err = in.closureStatic(ctx, unit, current.closureStatic, current.args)
				if !handled {
					err = &UnsupportedError{
						Opcode: "Closure::" + current.closureStatic,
						Line:   line.GetLine(), File: op.GetFilename(),
					}
				}
				err = in.materialize(ctx, unit, err)
			case current.enum != nil:
				var handled bool
				returned, handled, err = in.enumStaticCall(ctx, unit, current.enum, current.enumMethod, current.args, depth)
				if !handled {
					err = &UnsupportedError{
						Opcode: "enum method " + current.name,
						Line:   line.GetLine(), File: op.GetFilename(),
					}
				}
				err = in.materialize(ctx, unit, err)
			case current.closure != nil:
				returned, err = in.invokeClosureNamed(ctx, unit, current.closure,
					current.args, current.named, depth)
			case current.builtinCtor != nil:
				err = in.materialize(ctx, unit, current.builtinCtor(current.this, current.args))
				returned = php.Null()
			case current.builtinMethod != "":
				returned, err = in.builtinMethodCall(ctx, unit, current)
				if unsupported, ok := err.(*UnsupportedError); ok && unsupported.Line == 0 {
					unsupported.Line, unsupported.File = line.GetLine(), op.GetFilename()
				}
			case current.this != nil:
				// A constructor call on a class that declares none: PHP simply
				// does nothing.
				returned = php.Null()
			default:
				unit.dynamicCall = current.dynamic
				unit.resultUnused = line.GetResultType() == opUnused
				unit.callStrict = op.GetStrictTypes()
				returned, err = in.call(ctx, unit, current.name, current.args, depth)
			}
			ctx.PopCall()
			if current.ctorObject != nil {
				unit.built(current.ctorObject)
				if err == nil {
					unit.allowDestruct(current.ctorObject)
				}
			}
			if err != nil {
				// The callee's compiled variables went with its frame, and the
				// engine frees them in the leave helper -- after the frame is
				// off the stack. So a destructor among them runs *before* the
				// exception reaches this frame's catch, and its own stack frame
				// is attributed to the call site rather than to the callee.
				// After the constructor bookkeeping above, so that a `new` that
				// threw has already given up its half-built object.
				ctx.SetLocation(op.GetFilename(), line.GetLine())
				// The exception itself is live but unnamed -- no slot holds it
				// until a CATCH stores it -- and it holds the arguments of
				// every frame it captured. Without it as a root the walk frees
				// what a trace still has a claim on.
				keep := php.Undef()
				if thrown, ok := asThrown(err); ok {
					keep = php.Obj(thrown.object)
				}
				if released := in.releaseCheckKeeping(ctx, unit, keep); released != nil {
					err = chainOnto(in.materialize(ctx, unit, released), err)
				}
			}
			if current.releasedTemps && err != nil {
				// The call's arguments are dropped whether it succeeded or
				// not, so a destructor waiting on one runs here -- inside
				// whatever try is open, which is where the engine runs it. A
				// throw from it joins the one already on its way out.
				keep := returned
				if current.ctorObject != nil {
					keep = php.Obj(current.ctorObject)
				}
				if lazyErr := in.releaseCheckKeeping(ctx, unit, keep); lazyErr != nil {
					err = chainOnto(in.materialize(ctx, unit, lazyErr), err)
				}
			}
			if err != nil {
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			f.storeResult(line, returned)
			if current.releasedTemps {
				// The call owned an argument that came out of a temporary and
				// has now returned it. Whatever that held may have no reader
				// left -- the value the call *produced* still does, so it is
				// held aside while the question is asked.
				//
				// For a constructor the object being built is what to hold: it
				// is not the call's return value, and between ZEND_NEW and the
				// assignment that stores it the only thing naming it is a
				// temporary, which is not a root.
				keep := returned
				if current.ctorObject != nil {
					keep = php.Obj(current.ctorObject)
				}
				if lazyErr := in.releaseCheckKeeping(ctx, unit, keep); lazyErr != nil {
					lazyErr = in.materialize(ctx, unit, lazyErr)
					if target, caught := f.raise(lazyErr, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), lazyErr
				}
			}
			pc++

		case "ZEND_RETURN", "ZEND_GENERATOR_RETURN":
			return f.fetch(line.GetOp1Type(), line.GetOp1()), nil

		case "ZEND_GENERATOR_CREATE":
			// The first opline of every generator function. Calling such a
			// function must not run its body, so the frame -- already built and
			// holding the arguments -- is handed back to be wrapped.
			f.pc = pc + 1
			return php.Undef(), errBecomeGenerator

		case "ZEND_YIELD":
			if f.forceClosing {
				// The generator is being closed, so there is nobody to resume
				// what this would suspend. The engine words it by where the
				// yield can only be -- a finally block, since that is all that
				// still runs during a close.
				err := in.throwError(ctx, unit, "Error",
					"Cannot yield from finally in a force-closed generator")
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			// op1 is the value, op2 an explicit key. A keyless yield numbers
			// its results from zero, and an explicit integer key moves that
			// counter on, exactly as an array append does.
			if line.GetOp2Type() != opUnused {
				f.yieldKey = f.fetch(line.GetOp2Type(), line.GetOp2())
				if f.yieldKey.Kind() == php.KindInt && f.yieldKey.Int() >= f.autoKey {
					f.autoKey = f.yieldKey.Int() + 1
				}
			} else {
				f.yieldKey = php.Int(f.autoKey)
				f.autoKey++
			}
			f.yieldValue = php.Undef()
			switch {
			case line.GetOp1Type() == opUnused:
				f.yieldValue = php.Null()
			case op.GetReturnsRef():
				// A by-reference generator can only yield something there is a
				// reference *to*; a constant or a temporary has no slot, and
				// PHP says so rather than refusing.
				if line.GetOp1Type() == opConst || line.GetOp1Type() == opTmpVar ||
					(line.GetOp1Type() == opVar &&
						in.returnsCallResult(oplines, pc, line.GetOp1()) &&
						!f.rawSlot(line.GetOp1Type(), line.GetOp1()).IsRef()) {
					// A call's result is a temporary too, however the compiler
					// spells the operand: `yield foo()` has nothing to alias --
					// unless foo itself returns by reference, in which case the
					// slot holds the box it handed back.
					ctx.Notice("Only variable references should be yielded by reference")
				}
				// `function &gen()` yields the slot, not a copy of it, so a
				// by-reference foreach can write back through it.
				if ref, ok := f.slotRef(line.GetOp1Type(), line.GetOp1()); ok {
					// The slot itself, without claiming a share: what is in
					// flight is a value, and the holder that counts is whatever
					// the loop on the other side binds it to. The frame records
					// that the slot left, so ending does not take that holder
					// away again.
					f.escaped = append(f.escaped, ref)
					f.yieldValue = php.MakeRef(ref)
					break
				}
				f.yieldValue = f.fetch(line.GetOp1Type(), line.GetOp1()).Copy()
			default:
				f.yieldValue = f.fetch(line.GetOp1Type(), line.GetOp1()).Copy()
			}
			// The result receives whatever send() passes in when the generator
			// is resumed, so it is filled in there rather than here.
			f.pc = pc + 1
			f.calls = calls
			return php.Undef(), errSuspended

		case "ZEND_YIELD_FROM":
			if f.forceClosing {
				err := in.throwError(ctx, unit, "Error",
					"Cannot use \"yield from\" in a force-closed generator")
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			// Delegation drains another generator or an array one element at a
			// time. The opline is re-entered on each resume until the source is
			// exhausted, at which point its return value becomes the result.
			if f.delegate == nil {
				source := f.fetch(line.GetOp1Type(), line.GetOp1())
				delegate, err := in.newDelegate(ctx, unit, source, depth)
				if err = in.materialize(ctx, unit, err); err != nil {
					if target, caught := f.raise(err, pc); caught {
						pc = target
						continue
					}
					return php.Undef(), err
				}
				f.delegate = delegate
			}
			key, value, more, err := in.iterNext(ctx, unit, f.delegate)
			if err = in.materialize(ctx, unit, err); err != nil {
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			if more {
				f.yieldKey, f.yieldValue = key, value
				// pc is left where it is so the resume re-enters this opline.
				f.pc = pc
				f.calls = calls
				return php.Undef(), errSuspended
			}
			// A generator that was abandoned mid-delegation has no return
			// value to hand on. Another delegator still waiting on it finds
			// that out here: the first one saw the exception itself, the rest
			// get this.
			if aborted := f.delegate.gen; aborted != nil && aborted.failed {
				f.delegate = nil
				err := in.throwError(ctx, unit, "ClosedGeneratorException",
					"Generator yielded from aborted, no return value available")
				if target, caught := f.raise(err, pc); caught {
					pc = target
					continue
				}
				return php.Undef(), err
			}
			returned := f.delegate.returned
			f.delegate = nil
			f.storeResult(line, returned)
			pc++

		default:
			return php.Undef(), &UnsupportedError{
				Opcode: name,
				Line:   line.GetLine(),
				File:   op.GetFilename(),
			}
		}
	}
	// Falling off the end is an implicit return of null.
	return php.Null(), nil
}

// addElement appends one element of an array literal. op2 is the key, absent
// for a plain append.
func addElement(ctx *php.Ctx, arr *php.Array, f *frame, line *pb.Opline) error {
	byRef := line.GetExtendedValue()&arrayElementRef != 0
	// `[&$x]` binds the variable's slot rather than reading it, so an $x that
	// does not exist yet is *created* -- silently, the way `$y = &$x` creates
	// one. Reading it first would report a variable the program never read.
	value := php.Undef()
	if byRef {
		value = f.fetchQuiet(line.GetOp1Type(), line.GetOp1())
	} else {
		value = f.fetch(line.GetOp1Type(), line.GetOp1())
	}
	if byRef {
		// `[&$x]` stores the variable's box, so writing the element writes the
		// variable and vice versa.
		if ref, ok := f.slotRef(line.GetOp1Type(), line.GetOp1()); ok {
			value = php.MakeRef(ref)
		}
		// The `&` is the program's own, so the element is one var_dump marks.
		// An operand that arrived already boxed -- `[&$o->p]`, where the
		// property fetch did the boxing -- needs saying so just as much as a
		// variable does.
		if ref, ok := value.RefOf(); ok {
			ref.AddBinding()
		}
		php.Shared(value)
	} else {
		// **A by-value element is a copy.** `$outer = [$inner, $inner]` puts
		// two independent arrays in, and writing one must not be visible
		// through the other -- measured: the engine gives `$outer[0][] = "x"`
		// to element 0 alone, and storing the fetched value put the same
		// `*php.Array` in both slots so the write showed up twice. The
		// compiled path has always copied here; this is the interpreter
		// catching up with its own `ArrayAppend`.
		value = value.Copy()
	}
	if line.GetOp2Type() == opUnused {
		arr.Append(value)
		return nil
	}
	index := f.fetch(line.GetOp2Type(), line.GetOp2())
	if offending := illegalOffsetType(index); offending != "" {
		// A literal `[new stdClass => null]` is refused where the element is
		// added, with the same words indexing an array with one uses.
		return Raise("TypeError", "Cannot access offset of type %s on array", offending)
	}
	arr.Set(php.ArrayKey(ctx, index), value)
	return nil
}

// declaredReturnType is the type a return value is checked against. It is what
// the source wrote, except for __toString: since PHP 8 that method has an
// implicit `string` whether or not it is written, which is why a __toString
// with no return statement is a TypeError rather than an empty string.
func declaredReturnType(op *pb.OpArray) string {
	if declared := op.GetReturnType(); declared != "" {
		return declared
	}
	if strings.EqualFold(op.GetName(), "__toString") {
		return "string"
	}
	return ""
}

// illegalOffsetType names the type of an offset an array cannot take -- an
// object or another array -- or "" when the offset is usable. PHP names the
// class rather than saying "object".
func illegalOffsetType(value php.Value) string {
	switch held := value.Deref(); held.Kind() {
	case php.KindObject:
		return php.DisplayName(held.Object().ClassName)
	case php.KindArray:
		return "array"
	}
	return ""
}

// tooFewArguments words an ArgumentCountError as PHP does. The location is the
// *caller's*, since that is where the mistake is, and it comes off the call
// stack rather than from the frame that noticed.
func tooFewArguments(ctx *php.Ctx, op *pb.OpArray, scope *Class, passed int) string {
	where := ""
	if stack := ctx.CallStack(); len(stack) > 0 {
		top := stack[len(stack)-1]
		where = fmt.Sprintf(" in %s on line %d", top.File, top.Line)
	}
	// "exactly" only when there is nothing optional to make the count a range.
	expected := "exactly"
	if int(op.GetRequiredArgs()) < len(op.GetArgs()) {
		expected = "at least"
	}
	// A method is named with the class that declares it. The op_array carries
	// only the bare name, so the scope the frame is running in supplies it.
	name := op.GetName()
	if scope != nil && op.GetVisibility() != "" {
		name = php.DisplayName(scope.Name) + "::" + name
	}
	return fmt.Sprintf("Too few arguments to function %s(), %d passed%s and %s %d expected",
		name, passed, where, expected, op.GetRequiredArgs())
}

// argumentByRef reports whether the call being assembled takes its nth argument
// by reference. The argument number is 1-based, as the opcode carries it.
//
// Nothing is known about a callee that has not been resolved -- a builtin the
// runtime does not implement, say -- and by value is the safer answer: it reads
// what is there instead of creating something that was never asked for.
// noteUserCallByRef reports each by-reference parameter that call_user_func
// filled with a value. The engine names the callee and the parameter, and the
// call goes ahead with the copy.
func (in *Interp) noteUserCallByRef(ctx *php.Ctx, unit *Unit, current *pendingCall) {
	callee := current.method
	if callee == nil && current.closure != nil {
		callee = current.closure.op
	}
	if callee == nil && current.name != "" {
		callee, _ = unit.userFunctionBody(current.name)
	}
	if current.dynamic && scopeIntrospection[strings.ToLower(current.name)] {
		// A function that refuses to be reached dynamically refuses before it
		// looks at its arguments, so there is nothing to say about them.
		return
	}
	var args []*pb.ArgInfo
	variadic := false
	switch {
	case callee != nil:
		args = callee.GetArgs()
		variadic = variadicArgs(callee)
	default:
		// An engine function: the harvested signature names its parameters too.
		signature, known := in.engineSignatures[strings.ToLower(current.name)]
		if !known {
			return
		}
		args = signature.GetArgs()
		variadic = len(args) > 0 && args[len(args)-1].GetVariadic()
	}
	for i := range current.args {
		arg := argAt(args, i, variadic)
		if arg == nil || !arg.GetByRef() {
			continue
		}
		ctx.Warn("%s(): Argument #%d ($%s) must be passed by reference, value given",
			php.DisplayName(current.diagnosticName()), i+1, arg.GetName())
	}
}

// noteCallbackByRef is the same complaint for a callback an internal function
// invoked. Such a call is assembled in C with no slots to hand over, so a
// parameter that wanted one gets a copy and PHP says so -- unless the internal
// function deliberately passed a reference, which array_walk does for the
// element it hands its callback.
func (in *Interp) noteCallbackByRef(ctx *php.Ctx, unit *Unit, current *pendingCall) {
	callee := current.method
	if callee == nil && current.closure != nil {
		callee = current.closure.op
	}
	if callee == nil && current.name != "" {
		callee, _ = unit.userFunctionBody(current.name)
	}
	if current.dynamic && scopeIntrospection[strings.ToLower(current.name)] {
		// A function that refuses to be reached dynamically refuses before it
		// looks at its arguments, so there is nothing to say about them.
		return
	}
	var args []*pb.ArgInfo
	variadic := false
	switch {
	case callee != nil:
		args, variadic = callee.GetArgs(), variadicArgs(callee)
	default:
		// An engine function has no body, but the harvest still names its
		// parameters -- and str_replace's $count is as by-reference as any.
		signature, known := in.engineSignatures[strings.ToLower(current.name)]
		if !known {
			return
		}
		args = signature.GetArgs()
		variadic = len(args) > 0 && args[len(args)-1].GetVariadic()
	}
	// The complaint belongs to the call the *script* made -- the one that named
	// the internal function -- not to wherever the callee's body left the
	// reported location the last time round the internal function's loop.
	if stack := ctx.Stack(); len(stack) > 0 && stack[len(stack)-1].File != php.InternalFrameFile {
		file, line := ctx.Location()
		ctx.SetLocation(stack[len(stack)-1].File, stack[len(stack)-1].Line)
		defer ctx.SetLocation(file, line)
	}
	for i, given := range current.args {
		arg := argAt(args, i, variadic)
		if arg == nil || !arg.GetByRef() || given.IsRef() {
			continue
		}
		ctx.Warn("%s(): Argument #%d ($%s) must be passed by reference, value given",
			current.diagnosticTraceName(), i+1, arg.GetName())
	}
}

// variadicByRef reports whether the callee's last parameter is a by-reference
// variadic, which is what collects a name no parameter claims.
func variadicByRef(in *Interp, unit *Unit, calls []*pendingCall) bool {
	if len(calls) == 0 {
		return false
	}
	parameters, _, known := in.calleeParameters(unit, calls[len(calls)-1])
	if !known || len(parameters) == 0 {
		return false
	}
	last := parameters[len(parameters)-1]
	return last.GetVariadic() && last.GetByRef()
}

// variadicPosition is the 1-based position of the callee's variadic parameter,
// which is the position an unclaimed name is measured against.
func variadicPosition(in *Interp, unit *Unit, calls []*pendingCall) uint32 {
	if len(calls) == 0 {
		return 0
	}
	parameters, _, known := in.calleeParameters(unit, calls[len(calls)-1])
	if !known || len(parameters) == 0 {
		return 0
	}
	return uint32(len(parameters))
}

// sendArgumentByRef reports whether the parameter a SEND opcode targets is by
// reference. For a positional argument op2 *is* the position; for a **named**
// one it holds the parameter's name instead, and reading it as a position asks
// about whichever parameter happens to sit at that constant's index -- which is
// how `t(b: $y, a: $x)` on `t(&$a, $b, &$c)` ended up passing $y positionally
// and then reporting that $a overwrote it.
func (in *Interp) sendArgumentByRef(unit *Unit, f *frame, calls []*pendingCall,
	line *pb.Opline) bool {
	if line.GetOp2Type() != opConst {
		return in.argumentByRef(unit, calls, line.GetOp2())
	}
	at, ok := parameterPosition(in, unit, calls, f.fetch(line.GetOp2Type(), line.GetOp2()).String())
	if ok {
		return in.argumentByRef(unit, calls, uint32(at))
	}
	// A name no parameter claims goes to the variadic, if there is one, and a
	// by-reference variadic wants a slot for it as much as for a positional
	// argument: `test2(x: $x)` on `test2(&...$refs)` writes back into $x.
	return variadicByRef(in, unit, calls)
}

func (in *Interp) argumentByRef(unit *Unit, calls []*pendingCall, number uint32) bool {
	if len(calls) == 0 || number == 0 {
		return false
	}
	current := calls[len(calls)-1]
	if current.viaUserCall {
		// call_user_func has no way to hand over a slot: its own parameter is
		// by value, so whatever the callee asked for it gets a copy.
		return false
	}
	index := int(number) - 1
	callee := pendingCallee(unit, current)
	if callee != nil {
		args := callee.GetArgs()
		if index < len(args) {
			return args[index].GetByRef()
		}
		// A variadic by-reference parameter covers every argument past it.
		if len(args) > 0 && args[len(args)-1].GetVariadic() {
			return args[len(args)-1].GetByRef()
		}
		return false
	}
	if current.closure != nil && current.closure.builtin != "" {
		if byRef, known := in.engineByRef[strings.ToLower(current.closure.builtin)]; known && index < len(byRef) {
			return byRef[index]
		}
		return false
	}
	if byRef, known := in.engineByRef[strings.ToLower(current.name)]; known && index < len(byRef) {
		return byRef[index]
	}
	return false
}

// returnsCallResult reports whether the operand a by-reference return is about
// was written by the call just before it. Only then is "no reference in the
// slot" evidence that the callee returned by value -- a fetched array element
// or property may have been flattened by a return-type check on the way.
func (in *Interp) returnsCallResult(oplines []*pb.Opline, pc int, operand uint32) bool {
	for at := pc - 1; at >= 0 && at >= pc-4; at-- {
		previous := oplines[at]
		if previous.GetResultType() == opVar && previous.GetResult() == operand {
			return strings.HasPrefix(in.name(previous.GetOpcode()), "ZEND_DO_")
		}
	}
	return false
}

// stepsResult reports that the opline after this fetch increments or decrements
// what the fetch produced. The engine words a refused string offset by what was
// about to be done with it, and `++$s[0]` is not the offset being "used as an
// array".
func (in *Interp) stepsResult(oplines []*pb.Opline, pc int, line *pb.Opline) bool {
	if line.GetResultType() == opUnused || pc+1 >= len(oplines) {
		return false
	}
	next := oplines[pc+1]
	if next.GetOp1Type() != line.GetResultType() || next.GetOp1() != line.GetResult() {
		return false
	}
	switch in.name(next.GetOpcode()) {
	case "ZEND_PRE_INC", "ZEND_PRE_DEC", "ZEND_POST_INC", "ZEND_POST_DEC":
		return true
	}
	return false
}

// pendingCallee is the body an argument's by-reference-ness and parameter name
// have to be read from, when it can be known before DO_FCALL resolves the call.
func pendingCallee(unit *Unit, current *pendingCall) *pb.OpArray {
	// A Closure carries its own body rather than a resolved method, and its
	// parameters are just as capable of being by reference.
	if current.method != nil {
		return current.method
	}
	if current.closure != nil {
		return current.closure.op
	}
	if current.name != "" && unit != nil {
		// A plain call names its target and leaves resolution to DO_FCALL, so
		// the by-reference question has to be answered from the table here.
		if fn, ok := unit.userFunctionBody(current.name); ok {
			return fn
		}
	}
	if current.this != nil && strings.EqualFold(current.builtinMethod, magicInvoke) {
		// `$closure->__invoke($x)` is the closure's own call written the long
		// way, and its parameters decide just as they do for `$closure($x)`.
		if fn, held := asClosure(php.Obj(current.this)); held && fn.op != nil {
			return fn.op
		}
	}
	return nil
}

// calleeParameterName names the parameter an argument position lands on, for a
// diagnostic that has to name it. Empty when the callee is not known yet.
// staticCallName is get_function_or_method_name: a method is always spelled
// `Class::method`, whether or not the call had an instance, unlike a trace
// which distinguishes `->` from `::`.
func staticCallName(current *pendingCall) string {
	return strings.Replace(current.traceName(), "->", "::", 1)
}

func calleeParameterName(unit *Unit, current *pendingCall, number uint32) string {
	callee := pendingCallee(unit, current)
	if callee == nil || number == 0 {
		return ""
	}
	args := callee.GetArgs()
	index := int(number) - 1
	// `func->common.num_args` does not count the variadic parameter, and
	// get_function_arg_name refuses any position past it -- so an argument
	// that lands in a variadic slot has no declared name to report, even
	// though the declaration wrote one.
	if index < len(args) && !args[index].GetVariadic() {
		return args[index].GetName()
	}
	return ""
}

// methodNameOf reads the name a dynamic call names its method with. The engine
// stops at the first NUL byte -- `$obj->{"ab\0cd"}()` asks for "ab", and
// `$obj->{"\0"}()` asks for "" -- which is observable through __call, since the
// name it receives is the truncated one.
// methodNameOperand reads the name of a dynamically named method call. The
// engine takes a string and nothing else -- not a number, not null, not an
// object with __toString -- and says so before it looks at anything else.
func methodNameOperand(value php.Value) (string, error) {
	if held := value.Deref(); held.Kind() != php.KindString {
		return "", Raise("Error", "Method name must be a string")
	}
	return methodNameOf(value), nil
}

func methodNameOf(value php.Value) string {
	name := value.String()
	if at := strings.IndexByte(name, 0); at >= 0 {
		return name[:at]
	}
	return name
}

// scalarIndexError words the refusal to index something that cannot hold
// elements. PHP distinguishes a string, which has offsets but cannot nest, from
// any other scalar, and distinguishes an append from a keyed write.
func scalarIndexError(container php.Value, line *pb.Opline, unsetting, stepping bool) string {
	if container.Deref().Kind() != php.KindString {
		return "Cannot use a scalar value as an array"
	}
	switch {
	case line.GetOp2Type() == opUnused:
		// An append names no offset at all, and that is what PHP complains
		// about first -- ahead of what the offset was going to be used for.
		return "[] operator not supported for strings"
	case line.GetExtendedValue()&fetchForReference != 0:
		return "Cannot create references to/from string offsets"
	case unsetting:
		return "Cannot unset string offsets"
	case stepping:
		return "Cannot increment/decrement string offsets"
	}
	return "Cannot use string offset as an array"
}

// quoteKey renders an array key the way PHP's own diagnostics do: a string key
// appears in quotes and an integer one bare.
func quoteKey(index php.Value) string { return php.QuoteKey(index) }

// arrayForWrite turns an operand into an array that can be written through,
// creating one where PHP creates one. It reports false for a value that cannot
// hold elements at all, leaving the caller to raise the error.
// bindsReference reports whether the fetch at pc is being taken to bind a
// reference rather than to write through. The two are the same opcode and only
// what follows tells them apart.
func (in *Interp) bindsReference(oplines []*pb.Opline, pc int) bool {
	if pc+1 >= len(oplines) {
		return false
	}
	switch in.name(oplines[pc+1].GetOpcode()) {
	case "ZEND_ASSIGN_REF", "ZEND_MAKE_REF":
		return true
	}
	return false
}

// growArray is arrayForWrite with the refusal a *typed* slot earns first. A
// reference held by a typed property carries that property's declaration, and
// growing an array in it is refused in the property's name -- with its own
// wording, which says the reference is what is being written through.
func (in *Interp) growArray(ctx *php.Ctx, unit *Unit, f *frame, kind, operand uint32,
	container php.Value) (php.Value, bool, error) {
	switch container.Kind() {
	case php.KindUndef, php.KindNull, php.KindFalse:
		if ref, isRef := f.rawSlot(kind, operand).RefOf(); isRef {
			for _, source := range ref.Sources() {
				if source.Type == "" {
					continue
				}
				if _, ok := in.checkType(ctx, unit, source.Type, php.Arr(php.NewArray()), true); ok {
					continue
				}
				return container, false, in.throwError(ctx, unit, "TypeError",
					"Cannot auto-initialize an array inside a reference held by "+
						"property %s::$%s of type %s",
					php.DisplayName(source.Class), source.Name, source.Type)
			}
		}
	}
	grown, ok := f.arrayForWrite(ctx, kind, operand, container)
	return grown, ok, nil
}

func (f *frame) arrayForWrite(ctx *php.Ctx, kind, operand uint32, container php.Value) (php.Value, bool) {
	switch container.Kind() {
	case php.KindArray:
		return container, true
	case php.KindUndef, php.KindNull:
		// The usual way a nested structure comes into existence.
	case php.KindFalse:
		// PHP still grows an array out of false, but has deprecated saying so.
		ctx.Deprecated("Automatic conversion of false to array is deprecated")
	default:
		return container, false
	}
	created := php.Arr(php.NewArray())
	f.store(kind, operand, created)
	return created, true
}

// dimensionRead indexes a container, warning as PHP does when the key is
// missing rather than failing. The quiet form is for isset() and ??, which ask
// precisely because the key may be absent.
//
// It can also fail outright: an offset PHP cannot convert to a key is a
// TypeError, so the error is returned rather than swallowed.
func dimensionRead(ctx *php.Ctx, container, index php.Value, quiet bool) (php.Value, error) {
	index = index.Deref()
	switch container.Kind() {
	case php.KindArray:
		switch index.Kind() {
		case php.KindArray, php.KindObject:
			return php.Undef(), Raise("TypeError",
				"Cannot access offset of type %s on array", givenTypeName(index))
		}
		if value, ok := container.Array().Get(php.ArrayKey(ctx, index)); ok {
			return value, nil
		}
		if !quiet {
			ctx.Warn("Undefined array key %s", quoteKey(index))
		}
		return php.Null(), nil
	case php.KindString:
		s := container.String()
		// `$s[$i] ?? x` reports nothing at all, unlike isset(), which reports
		// the precision loss; dimensionRead is only ever reached by the former.
		offset, err := stringOffset(ctx, index, quiet, false)
		if err != nil {
			if quiet {
				// isset() and ?? do not raise; an offset the string cannot use
				// simply is not set.
				return php.Null(), nil
			}
			return php.Undef(), err
		}
		if offset < 0 {
			offset += len(s)
		}
		if offset < 0 || offset >= len(s) {
			if quiet {
				// isset() and ?? ask whether the offset is there, and it is
				// not: the empty string a read would give would make `??`
				// answer with it instead of the default.
				return php.Null(), nil
			}
			ctx.Warn("Uninitialized string offset %d", index.Int())
			return php.String(""), nil
		}
		return php.String(s[offset : offset+1]), nil
	case php.KindUndef, php.KindNull, php.KindInt, php.KindFloat, php.KindTrue,
		php.KindFalse, php.KindResource:
		if !quiet {
			// The type is named as the value's own, so a boolean reads as
			// "true" or "false" rather than "bool". A resource is no more a
			// container than a scalar is, and earns the same warning.
			ctx.Warn("Trying to access array offset on %s", givenTypeName(container))
		}
		return php.Null(), nil
	}
	return php.Null(), nil
}

// stringOffset converts an index used against a string. Anything but an integer
// is cast, and the cast is itself reported -- except for a string that is not a
// number at all, which PHP refuses.
func stringOffset(ctx *php.Ctx, index php.Value, quiet, report bool) (int, error) {
	switch index.Kind() {
	case php.KindInt:
		return int(index.Int()), nil
	case php.KindString:
		// A string offset written as a string has to be an *integer* one.
		// "007" is fine; "1.5" and "1.0E+121" are not, even though both are
		// numeric strings -- a float offset is only allowed as an actual float,
		// which warns and truncates.
		numeric, ok := index.IsNumericString()
		if ok && numeric.Kind() == php.KindInt {
			// An integer string is an exact offset, not a cast: no warning.
			return int(numeric.Int()), nil
		}
		// A string that *starts* with an integer and then stops being a number
		// is usable, and PHP says so rather than refusing: "1abc" is offset 1
		// with a warning. A string that is wholly numeric but not an integer --
		// "1.5", "1.0E+121" -- is refused outright, which is why this is only
		// reached when IsNumericString said no.
		if prefix, leading := leadingIntegerOffset(index.RawString()); leading && !ok {
			if quiet {
				// isset() is stricter than a read: an offset that is not a
				// whole integer string is simply not set, warning or no.
				return 0, Raise("TypeError", "Cannot access offset of type string on string")
			}
			ctx.Warn("Illegal string offset %q", index.RawString())
			return prefix, nil
		}
		return 0, Raise("TypeError", "Cannot access offset of type string on string")
	case php.KindArray, php.KindObject:
		return 0, Raise("TypeError",
			"Cannot access offset of type %s on string", givenTypeName(index))
	}
	if index.Kind() == php.KindResource {
		// A resource is not an offset a string can have, in any position: a
		// read is refused the same way isset() is. An *array* is the odd one
		// out -- there the handle is cast to its id with a warning.
		return 0, Raise("TypeError", "Cannot access offset of type resource on string")
	}
	if index.Kind() == php.KindFloat && quiet {
		// isset() and empty() convert the offset the way an array key does, and
		// report the same precision loss rather than the cast warning a read or
		// a write gives -- spelled at full precision, as var_dump spells one.
		//
		// `??` is quiet in a stronger sense: it reports nothing at all, which is
		// what tells it apart from isset() here.
		if report && float64(int64(index.Float())) != index.Float() {
			ctx.Deprecated("Implicit conversion from float %s to int loses precision",
				php.DumpFloat(index.Float()))
		}
		return int(index.Int()), nil
	}
	if !quiet {
		ctx.Warn("String offset cast occurred")
	}
	return int(index.Int()), nil
}

// leadingIntegerOffset reads the integer a string offset starts with, which PHP
// accepts with a warning. Anything else -- a float string, a bare word -- has
// no offset in it at all.
func leadingIntegerOffset(text string) (int, bool) {
	// PHP asks is_numeric_string whether the *whole* string is a number and
	// takes the answer even when it is not, provided what it read was an
	// integer. So the leading whitespace it always allows is skipped here too,
	// and a prefix that turns out to be a float is no offset at all: "7.5str"
	// reads as a double with trailing data, and a double is refused outright.
	start := 0
	for start < len(text) && (text[start] == ' ' || text[start] == '\t' ||
		text[start] == '\n' || text[start] == '\r' || text[start] == '\v' || text[start] == '\f') {
		start++
	}
	end, seen := start, false
	if end < len(text) && (text[end] == '-' || text[end] == '+') {
		end++
	}
	for end < len(text) && text[end] >= '0' && text[end] <= '9' {
		end, seen = end+1, true
	}
	if !seen {
		return 0, false
	}
	if end < len(text) {
		switch text[end] {
		case '.':
			// "7." and "7.5" are both doubles as far as the parse is
			// concerned, whatever follows them.
			return 0, false
		case 'e', 'E':
			// An exponent only makes a double when it is a real one: "7e" is
			// an integer with the trailing data "e", "7e2" is a double.
			at := end + 1
			if at < len(text) && (text[at] == '-' || text[at] == '+') {
				at++
			}
			if at < len(text) && text[at] >= '0' && text[at] <= '9' {
				return 0, false
			}
		}
	}
	rest := end
	for rest < len(text) && (text[rest] == ' ' || text[rest] == '\t' ||
		text[rest] == '\n' || text[rest] == '\r' || text[rest] == '\v' || text[rest] == '\f') {
		rest++
	}
	if rest == len(text) {
		// Nothing but whitespace after the digits, which is not trailing Data: 		// the string is a whole integer and the caller handled it already.
		return 0, false
	}
	value, err := strconv.Atoi(text[start:end])
	if err != nil {
		// Out of range, which the parse reports as a double -- and a double is
		// no offset.
		return 0, false
	}
	return value, true
}

// binaryOp applies the operation ZEND_ASSIGN_OP names in extended_value. The
// operations that can fail raise the same catchable errors as their standalone
// opcodes.
func (in *Interp) binaryOp(ctx *php.Ctx, unit *Unit, f *frame, line *pb.Opline, a, b php.Value) (php.Value, error) {
	op := in.name(line.GetExtendedValue())
	if op == "ZEND_CONCAT" {
		// `$x .= $y` renders its operands the way `$x . $y` does: an object
		// goes through __toString, and a class without one is an Error. arith
		// has no context to run a method with, so it happens here.
		rendered, err := in.toString(ctx, unit, a)
		if err == nil {
			a = rendered
			rendered, err = in.toString(ctx, unit, b)
			b = rendered
		}
		if err != nil {
			// Turned into a thrown object here: what the caller does with it
			// is raise it at the current opline, which needs one.
			return php.Undef(), in.materialize(ctx, unit, err)
		}
	}
	// `$x += $y` refuses the same operands `$x + $y` refuses.
	if err := in.checkOperands(ctx, unit, op, a, b); err != nil {
		return php.Undef(), err
	}
	value, handled, err := in.applyBinary(ctx, unit, op, a, b)
	if handled || err != nil {
		return value, err
	}
	return php.Undef(), &UnsupportedError{
		// Named opcode-first, which is what the gap report is indexed by
		// and what TestUnsupportedOpcodeIsReported checks for.
		Opcode: op + " as a compound assignment",
		Line:   line.GetLine(), File: f.op.GetFilename(),
	}
}

// applyBinary performs one arithmetic opcode together with the failure each of
// them can raise. It is shared by the compound-assignment path and the
// constant-expression evaluator: the two must not disagree about what `%`
// does, and a constant is the *only* place an unfoldable one shows up, since
// the compiler folds anything that raises no diagnostic.
func (in *Interp) applyBinary(ctx *php.Ctx, unit *Unit, op string,
	a, b php.Value) (php.Value, bool, error) {
	switch op {
	case "ZEND_DIV":
		quotient, ok := php.Div(a, b)
		if !ok {
			return php.Undef(), true,
				in.throwError(ctx, unit, "DivisionByZeroError", "Division by zero")
		}
		return quotient, true, nil
	case "ZEND_MOD":
		remainder, ok := php.Mod(a, b)
		if !ok {
			return php.Undef(), true,
				in.throwError(ctx, unit, "DivisionByZeroError", "Modulo by zero")
		}
		return remainder, true, nil
	case "ZEND_SL", "ZEND_SR":
		var shifted php.Value
		var ok bool
		if op == "ZEND_SL" {
			shifted, ok = php.ShiftLeft(a, b)
		} else {
			shifted, ok = php.ShiftRight(a, b)
		}
		if !ok {
			return php.Undef(), true,
				in.throwError(ctx, unit, "ArithmeticError", "Bit shift by negative number")
		}
		return shifted, true, nil
	case "ZEND_ADD", "ZEND_SUB", "ZEND_MUL", "ZEND_CONCAT", "ZEND_POW",
		"ZEND_BW_AND", "ZEND_BW_OR", "ZEND_BW_XOR":
		return arith(op, a, b), true, nil
	}
	return php.Undef(), false, nil
}

// slotNamed finds the compiled variable a name refers to, which is how a
// variable-variable reaches an ordinary local.
func (f *frame) slotNamed(name string) (uint32, bool) {
	for i, candidate := range f.op.GetVarNames() {
		if candidate == name {
			return uint32(i), true
		}
	}
	return 0, false
}

// writeTarget is where a reference produced by a write fetch actually lives, so
// that assigning a reference over it reaches the container rather than the
// temporary the fetch returned.
type writeTarget struct {
	array  *php.Array
	key    php.Key
	object *php.Object
	prop   string
	// frame and variable name a variable of this frame reached *by name* --
	// `global $$n`, `$$n = &$x`. The box handed out belongs to that name, and
	// rebinding the temporary has to reach the name rather than stop at it.
	frame    *frame
	variable string
	// weak and weakAt name a WeakMap entry, whose slot a write fetch handed
	// out. A WeakMap holds its own boxes, so `$map[$o] = &$x` replaces the
	// entry rather than writing through it.
	weak   *weakMap
	weakAt int
}

func (w writeTarget) bind(value php.Value) {
	switch {
	case w.weak != nil:
		w.weak.BindAt(w.weakAt, value)
	case w.array != nil:
		w.array.Bind(w.key, value)
	case w.object != nil:
		w.object.Props.Bind(php.StringKey(w.prop), value)
	case w.frame != nil:
		w.frame.bindNamed(w.variable, value)
	}
}

// noteWriteTarget remembers what a write fetch is about to hand out a box for.
func (f *frame) noteWriteTarget(ctx *php.Ctx, line *pb.Opline, container php.Value, isObject bool) {
	if line.GetResultType() == opUnused {
		return
	}
	if f.writeTargets == nil {
		f.writeTargets = map[uint32]writeTarget{}
	}
	if isObject {
		object := container.Deref().Object()
		if object == nil {
			return
		}
		f.writeTargets[line.GetResult()] = writeTarget{
			object: object,
			prop:   f.fetch(line.GetOp2Type(), line.GetOp2()).String(),
		}
		return
	}
	array := container.Deref().Array()
	if array == nil {
		return
	}
	key := php.IntKey(0)
	if line.GetOp2Type() == opUnused {
		// An append: the element the fetch created is the last one.
		keys := array.Keys()
		if len(keys) == 0 {
			return
		}
		key = keys[len(keys)-1]
	} else {
		// Bookkeeping, not a conversion the program made: the fetch that
		// produced this slot has already converted the same offset and
		// reported whatever that cost. Converting it again with a context
		// would say it twice.
		key = php.ArrayKey(nil, f.fetch(line.GetOp2Type(), line.GetOp2()))
	}
	f.writeTargets[line.GetResult()] = writeTarget{array: array, key: key}
}

// writeTarget reads back what a write fetch recorded for a temporary, and
// forgets it: a temporary is used once.
func (f *frame) writeTarget(kind, operand uint32) (writeTarget, bool) {
	if kind != opVar && kind != opTmpVar {
		return writeTarget{}, false
	}
	target, noted := f.writeTargets[operand]
	if noted {
		delete(f.writeTargets, operand)
	}
	return target, noted
}

// releaseTemp clears a temporary an opcode has consumed, and reports whether
// what it held was an object whose destructor has still to run -- which is the
// only case worth a collection afterwards.
//
// Only a TMP_VAR is cleared. A VAR may be read again -- a foreach iterates one
// across many oplines -- and the compiler emits FREE or FE_FREE for those.
func (f *frame) releaseTemp(unit *Unit, kind, operand uint32) bool {
	if kind != opTmpVar && kind != opVar {
		return false
	}
	if int(operand) >= len(f.slots) {
		return false
	}
	held := f.slots[operand].Deref()
	if kind == opTmpVar {
		// A TMP slot is written once and read once, so clearing it is safe and
		// keeps a stale value from being mistaken for a live one. A VAR may be
		// read again -- `(new C)->m()->n()` keeps the receiver in one -- so it
		// is left alone; it is not a root either way.
		f.slots[operand] = php.Undef()
	}
	switch held.Kind() {
	case php.KindObject, php.KindArray:
		// Worth asking whether anything still holds it. Not only for a
		// destructor: the engine hands a freed object's handle to the next
		// `new`, and a program can see that even when nothing has a __destruct.
		return true
	}
	return false
}

// namedRef is the box behind a variable named at run time. A compiled variable
// wins; failing that the overflow table answers, and create decides whether a
// name with no home yet gets one.
func (f *frame) namedRef(name string, create bool) (*php.Ref, bool) {
	if slot, ok := f.slotNamed(name); ok {
		return f.slotRef(opCV, slot)
	}
	if ref, ok := f.extra[name]; ok {
		return ref, true
	}
	if !create {
		return nil, false
	}
	if f.extra == nil {
		f.extra = map[string]*php.Ref{}
	}
	ref := php.NewRef(php.Null())
	f.extra[name] = ref
	return ref, true
}

// bindNamed rebinds a variable reached by name to a box outright, which is what
// `$$n = &$x` and the pair of fetches behind `global $$n` do to it.
func (f *frame) bindNamed(name string, value php.Value) {
	if slot, ok := f.slotNamed(name); ok {
		f.bindSlot(opCV, slot, value)
		return
	}
	if ref, ok := value.RefOf(); ok {
		if f.extra == nil {
			f.extra = map[string]*php.Ref{}
		}
		f.extra[name] = ref
	}
}

// namedValue reads a variable named at run time without creating it.
func (f *frame) namedValue(name string) (php.Value, bool) {
	if name == "this" && f.this != nil {
		// `$this` is not a compiled variable -- the frame holds the instance
		// directly -- so a by-name read has to be answered from there. Probed:
		// `$a = "this"; $$a === $this` is true inside a method.
		return php.Obj(f.this), true
	}
	if slot, ok := f.slotNamed(name); ok {
		value := f.fetchQuiet(opCV, slot)
		return value, value.Kind() != php.KindUndef
	}
	ref, ok := f.extra[name]
	if !ok {
		return php.Undef(), false
	}
	return ref.Get(), ref.Get().Kind() != php.KindUndef
}

// escapedRef reports that a slot holds a box a by-reference yield handed out.
func (f *frame) escapedRef(slot php.Value) bool {
	if len(f.escaped) == 0 {
		return false
	}
	ref, ok := slot.RefOf()
	if !ok {
		return false
	}
	for _, gone := range f.escaped {
		if gone == ref {
			return true
		}
	}
	return false
}

// namedUnset removes a variable named at run time.
func (f *frame) namedUnset(name string) {
	if slot, ok := f.slotNamed(name); ok {
		php.Unshare(f.rawSlot(opCV, slot))
		f.bindSlot(opCV, slot, php.Undef())
		return
	}
	delete(f.extra, name)
}

// literal reads a constant of this frame by index.
func (f *frame) literal(index uint32) *pb.Literal {
	literals := f.op.GetLiterals()
	if int(index) < len(literals) {
		return literals[index]
	}
	return nil
}

// matchTarget looks a value up in a match or switch jump table. Only integers
// and strings are in the table; anything else falls to the default, which is
// why a match on other types still works but never hits a case here.
func matchTarget(subject php.Value, table *pb.Literal, opcode string) (int, bool) {
	if table == nil {
		return 0, false
	}
	for _, entry := range table.GetEntries() {
		var hit bool
		switch {
		case entry.GetIntKey():
			// switch on strings compares loosely, but match and switch on
			// integers require the same type -- a match arm is ===.
			hit = subject.Kind() == php.KindInt && subject.Int() == entry.GetIntKeyValue()
		default:
			hit = subject.Kind() == php.KindString && subject.String() == entry.GetStringKey()
		}
		if hit {
			return int(literalValue(entry.GetValue()).Int()), true
		}
	}
	return 0, false
}

// matchErrorSubject renders the value in an UnhandledMatchError the way PHP
// does: quoted for a string, bare otherwise.
func matchErrorSubject(v php.Value) string {
	if v.Kind() == php.KindString {
		// PHP quotes the subject with single quotes here, unlike var_dump.
		return "'" + v.String() + "'"
	}
	if v.Kind() == php.KindNull || v.Kind() == php.KindUndef {
		return "NULL"
	}
	if v.Kind() == php.KindObject {
		// An enum case is named, since a name is what the match arms were
		// written with. No other object has a rendering the engine attempts.
		if object := v.Object(); object != nil && object.EnumCase != "" {
			return object.ClassName + "::" + object.EnumCase
		}
		return "of type " + v.Object().ClassName
	}
	switch v.Kind() {
	case php.KindTrue:
		// A boolean is named, not cast: `true` rather than the "1" a string
		// conversion would give, and `false` rather than nothing at all.
		return "true"
	case php.KindFalse:
		return "false"
	case php.KindArray:
		// An array has no rendering short enough to be useful, so the engine
		// names the type as it does for an object.
		return "of type array"
	case php.KindFloat:
		// The float is written as PHP writes a literal, so a whole one keeps
		// its `.0` and is not mistaken for an int.
		return stdlib.ExportFloat(v.Float())
	}
	return v.String()
}

// A message that names the *value* rather than a declared type spells the
// booleans and null out -- "on false", not "on bool" -- which is what
// givenTypeName does and phpTypeName does not.

// phpTypeName is the spelling PHP uses in error messages, which is not always
// the type's own name: an unset variable reads as null there.
func phpTypeName(v php.Value) string { return php.KindName(v) }

// unbindGlobal removes a global by name. $GLOBALS is the symbol table rather
// than a copy of it, so the top-level compiled variable bound to that slot has
// to stop naming it too -- and it has to *stop naming* it, not be emptied:
// anything else sharing the box keeps what it held, which is what
// `$a =& f()` answers when the destructor of the old $a unsets the name.
func (u *Unit) unbindGlobal(ctx *php.Ctx, name string) {
	for _, frame := range u.frames {
		if frame == nil || frame.op != u.file {
			continue
		}
		if slot, ok := frame.slotNamed(name); ok {
			frame.bindSlot(opCV, slot, php.Undef())
		}
		break
	}
	ctx.Globals.Unset(php.StringKey(name))
}

// noFailure adapts a builtin constructor that cannot refuse its arguments to
// the shape of one that can.
func noFailure(ctor func(*php.Object, []php.Value)) func(*php.Object, []php.Value) error {
	return func(object *php.Object, args []php.Value) error {
		ctor(object, args)
		return nil
	}
}

// bindGlobalStatic keeps the globals table pointing at the box a static was
// just bound to. At the top level a `static $x` is the global $x -- `global $x`
// in a function reaches it, and $GLOBALS shows it -- so the name has to lead to
// one box, not two.
func bindGlobalStatic(ctx *php.Ctx, unit *Unit, f *frame, line *pb.Opline, boxed php.Value) {
	if f.op != unit.file || line.GetOp1Type() != opCV {
		return
	}
	ctx.Globals.Bind(php.StringKey(f.varName(line.GetOp1())), boxed)
}

// noteBorrowed records that a slot holds a reference over a value with no home.
func (f *frame) noteBorrowed(kind, operand uint32) {
	if kind == opCV {
		return
	}
	if f.borrowedRefs == nil {
		f.borrowedRefs = map[uint32]bool{}
	}
	f.borrowedRefs[operand] = true
}

// borrowed reports whether a slot was filled that way.
func (f *frame) borrowed(kind, operand uint32) bool {
	return kind != opCV && f.borrowedRefs[operand]
}

// trackStatic records that a slot stands for a static variable, so its value is
// written back when the call ends.
func (f *frame) trackStatic(slot uint32, name string) {
	if f.staticSlots == nil {
		f.staticSlots = map[uint32]string{}
	}
	f.staticSlots[slot] = name
}

// sharedRef wraps a reference the *program* asked for and that outlives the
// statement making it: an explicit `&`, a `global`, a `use (&$x)`, a
// by-reference foreach or yield. Two kinds of reference deliberately do not go
// through it. The boxes php2go makes for its own write paths are invisible to
// any program. And a reference made for a *call* -- a by-reference parameter, a
// by-reference return -- is shared only while the call is live; php2go has no
// refcount to notice it stop being shared, so marking it would leave the marker
// behind after the call returned.
func sharedRef(r *php.Ref) php.Value { return php.Shared(php.MakeRef(r)) }

// lazyProperty is the property an opcode is about, when it is about one. A
// method call and a clone are about the whole object, so they name nothing and
// always initialize; a property opcode carries the name in op2, and a property
// that has been given a value already does not initialize.
func lazyProperty(f *frame, line *pb.Opline, opcode string) (string, string) {
	magic := ""
	switch opcode {
	case "ZEND_INIT_METHOD_CALL", "ZEND_FETCH_DIM_FUNC_ARG":
		return "", ""
	case "ZEND_FETCH_OBJ_R", "ZEND_FETCH_OBJ_IS", "ZEND_FETCH_OBJ_FUNC_ARG",
		"ZEND_FETCH_OBJ_W", "ZEND_FETCH_OBJ_RW", "ZEND_FETCH_OBJ_UNSET":
		// A write descent asks __get too, not __set: what it wants is a slot to
		// write *through*, which is what `&__get` hands back.
		magic = "__get"
	case "ZEND_ASSIGN_OBJ", "ZEND_ASSIGN_OBJ_OP", "ZEND_ASSIGN_OBJ_REF":
		magic = "__set"
	case "ZEND_ISSET_ISEMPTY_PROP_OBJ":
		magic = "__isset"
	case "ZEND_UNSET_OBJ":
		magic = "__unset"
	}
	if line.GetOp2Type() == opUnused {
		return "", ""
	}
	return f.fetch(line.GetOp2Type(), line.GetOp2()).String(), magic
}

// copyState copies one object's properties into another, which is what clone
// does. The declarations travel with them: a clone's typed property is still
// typed, and both var_dump and a later write have to know that.
func copyState(source, target *php.Object) {
	// A value an engine class keeps outside the property table is part of the
	// object's state too. Only the shapes a clone can meaningfully duplicate
	// are copied: a generator or a fiber is not one of them, and PHP refuses to
	// clone those before reaching here.
	if held, ok := php.SensitiveHeld(source); ok {
		php.InitSensitiveParameterValue(target, held)
	}
	if held, ok := source.Internal.(*closure); ok {
		// A cloned Closure runs the same body with the same bindings -- a
		// `use (&$x)` capture stays shared, so both copies see a later write --
		// but it is a distinct object, which is why `$c === $clone` and even
		// `$c == $clone` are both false.
		copied := *held
		target.Internal = &copied
	}
	source.Props.Each(func(k php.Key, v php.Value) bool {
		if held := source.Props.RefHeld(k); held.IsShared() {
			// A property that holds a reference is *shared* with the copy:
			// both objects' slots are the same box, so a later write through
			// either is seen by both. This is the classic clone gotcha, and it
			// is why the copy is a second type source for that box rather than
			// inheriting the original's.
			target.Props.Set(k, php.MakeRef(held))
			return true
		}
		target.Props.Set(k, v.Copy())
		return true
	})
	for name, declaration := range source.Declared {
		target.Declare(name, declaration)
	}
	for name := range source.Dynamic {
		target.MarkDynamic(name)
	}
	for name := range source.Cleared {
		target.Clear(name)
	}
}

// castToArray implements (array)$v. An object becomes an array of its
// properties; a scalar becomes a one-element list; null becomes empty.
func castToArray(value php.Value) php.Value {
	switch value.Kind() {
	case php.KindArray:
		return value
	case php.KindObject:
		object := value.Object()
		if _, isClosure := object.Internal.(*closure); isClosure {
			// A Closure has no properties, and the cast does not answer with an
			// empty array: `(array) $c` is `[0 => $c]`, so `$h = (array) $c;
			// $h[0]()` calls it. Probed.
			out := php.NewArray()
			out.Append(value)
			return php.Arr(out)
		}
		if store, backed := asArrayStore(object); backed {
			// An engine array store answers the *cast* with its storage --
			// `ZEND_PROP_PURPOSE_ARRAY_CAST` -- while
			// get_mangled_object_vars(), which asks for the property table,
			// still answers with nothing.
			return php.Arr(store.Items().Clone())
		}
		return php.Arr(stdlib.MangledProperties(object))
	case php.KindNull, php.KindUndef:
		return php.Arr(php.NewArray())
	}
	result := php.NewArray()
	result.Append(value)
	return php.Arr(result)
}

// mangledProps is an object's property table with the keys PHP gives them
// outside the object: a private property is prefixed with its declaring class
// and a protected one with a star, both between NUL bytes. That is not a
// decoration -- it is how two classes in a hierarchy can each have a private
// property of the same name and keep them apart.
// mangledName is the engine's storage key for a property, which
// runtime/php owns along with the rest of the object pb.
func mangledName(object *php.Object, name string) string {
	return php.MangledName(object, name)
}

// castNumeric is (int) and (float). An object has no numeric value, and PHP
// says so and yields 1 rather than refusing -- the one conversion where the
// result is a number the value had nothing to do with.
func castNumeric(ctx *php.Ctx, value php.Value, want string) php.Value {
	if object := value.Deref().Object(); object != nil {
		ctx.Warn("Object of class %s could not be converted to %s",
			php.DisplayName(object.ClassName), want)
		if want == "float" {
			return php.Float(1)
		}
		return php.Int(1)
	}
	if want == "float" {
		return php.Float(value.Float())
	}
	return php.Int(value.Int())
}

// castToObject implements (object)$v, which yields a stdClass whose properties
// are the array's elements. An object is returned unchanged.
func castToObject(unit *Unit, value php.Value) php.Value {
	if value.Kind() == php.KindObject {
		return value
	}
	object := unit.born(php.NewObject("stdClass", unit.handle()))
	switch value.Kind() {
	case php.KindArray:
		// Integer keys become properties named by their digits. The name is
		// what a property table holds, so the key is made the same way every
		// property access makes one -- a raw string key here would be a
		// property nothing could look up again.
		value.Array().Each(func(k php.Key, v php.Value) bool {
			object.Props.Set(php.StringKey(k.String()), v.Copy())
			return true
		})
	case php.KindNull, php.KindUndef:
	default:
		object.Set("scalar", value)
	}
	return php.Obj(object)
}

// varName is the source name of a compiled variable, which is how lexical and
// static binds identify what they are binding.
func (f *frame) varName(operand uint32) string {
	names := f.op.GetVarNames()
	if int(operand) < len(names) {
		return names[operand]
	}
	return ""
}

// unwind is one exception on its way out, and how the block now running was
// entered.
type unwind struct {
	object *php.Object
	// bail is set instead of object when what is unwinding is not an exception
	// at all: a fiber being force-closed runs its finally blocks and then
	// carries on out, and no catch clause is offered it.
	bail error
	// viaFinally means a finally block was entered to run its cleanup, after
	// which the exception carries on rather than being considered handled.
	viaFinally bool
	// finallyStart and finallyEnd bound that block. A second exception raised
	// inside it either stays inside -- caught by a try within the finally, in
	// which case this one still resumes -- or escapes past it, in which case
	// this one becomes its previous. The bounds are what tells the two apart.
	finallyStart, finallyEnd int
}

func (f *frame) topUnwind() (unwind, bool) {
	if len(f.unwinding) == 0 {
		return unwind{}, false
	}
	return f.unwinding[len(f.unwinding)-1], true
}

func (f *frame) popUnwind() {
	if len(f.unwinding) > 0 {
		f.unwinding = f.unwinding[:len(f.unwinding)-1]
	}
}

// raise looks for a catch clause in this frame for an exception. A non-PHP
// error -- a gap in the interpreter rather than a program fault -- is never
// caught, because turning one into a catchable exception would let a test pass
// while silently exercising nothing.
func (f *frame) raise(err error, pc int) (int, bool) {
	if errors.Is(err, errFiberExit) || errors.Is(err, errGeneratorClose) {
		// Not an exception: only the finally blocks on the way out see it.
		target, region, found := f.finallyTarget(pc)
		if !found {
			return 0, false
		}
		f.unwinding = append(f.unwinding, unwind{
			bail: err, viaFinally: true,
			finallyStart: region.start, finallyEnd: region.end,
		})
		f.cleanupLiveVars(pc, target)
		return target, true
	}
	exception, ok := asThrown(err)
	if !ok {
		return 0, false
	}
	// An exception leaves every `@` this frame had open: the expression it was
	// suppressing is over, however it ended.
	for i := len(f.silences) - 1; i >= 0; i-- {
		f.ctx.EndSilence(f.silences[i])
	}
	f.silences = f.silences[:0]
	target, region, found := f.catchTarget(pc)
	// An exception already being carried out through a finally is not lost when
	// the finally itself throws: it becomes the new exception's previous. That
	// only applies when the new one *escapes* the finally -- one caught inside
	// it leaves the original still on its way out.
	if len(f.unwinding) > 0 {
		if top := f.unwinding[len(f.unwinding)-1]; top.viaFinally &&
			top.object != exception.object &&
			(!found || target < top.finallyStart || target >= top.finallyEnd) {
			if held, ok := exception.object.Get("previous"); (!ok ||
				held.Deref().Kind() != php.KindObject) &&
				!chainsTo(top.object, exception.object) {
				// Linking would close a loop -- the one already on its way out
				// has this very exception as its own cause -- and the engine
				// leaves the link unmade rather than building a chain that
				// cannot be printed.
				exception.object.Set("previous", php.Obj(top.object))
			}
			f.unwinding = f.unwinding[:len(f.unwinding)-1]
		}
	}
	if !found {
		return 0, false
	}
	entry := unwind{object: exception.object, viaFinally: region.viaFinally}
	if region.viaFinally {
		entry.finallyStart, entry.finallyEnd = region.start, region.end
	}
	f.unwinding = append(f.unwinding, entry)
	f.cleanupLiveVars(pc, target)
	return target, true
}

// cleanupLiveVars releases the temporaries a jump to a handler leaves behind.
// The engine does this on every such jump:
//
//	cleanup_live_vars(execute_data, op_num, try_catch->catch_op);
//
// A foreach's iterator is one of them, so leaving a loop by exception releases
// what the loop was walking *there* -- which force-closes a generator and runs
// its finally blocks at that point rather than at the end of the request.
func (f *frame) cleanupLiveVars(from, to int) {
	oplines := f.op.GetOplines()
	for _, r := range f.op.GetLiveRanges() {
		start, end := int(r.GetStart()), int(r.GetEnd())
		if start > from {
			// The ranges are in start order, so nothing further covers this
			// opline either.
			break
		}
		if from >= end || to < end {
			// Either the temporary was already dead, or the handler is still
			// inside its range and it stays live.
			continue
		}
		// A loop's range is split around a `break`, `continue` or `return`, and
		// the handler can sit in the gap between the pieces. The opline that
		// closes this piece is the free that jump emitted, and its op2 is the
		// distance to the loop's own free -- the same relative offset that
		// decides where an exception out of a free is thrown.
		if end < len(oplines) {
			final := oplines[end]
			if final.GetExtendedValue()&freeOnReturn != 0 && f.isFree(final) &&
				to < end+int(final.GetOp2()) {
				continue
			}
		}
		slot := r.GetVar()
		if r.GetKind() == liveLoop {
			if held, walking := f.iterators[slot]; walking {
				if held != nil && held.release != nil {
					held.release()
				}
				delete(f.iterators, slot)
			}
		}
		if int(slot) < len(f.slots) && !f.slots[slot].IsUndef() {
			f.bindSlot(opVar, slot, php.Undef())
			f.dropped = true
		}
	}
}

// isFree reports whether an opline is one of the two the cleanup treats as a
// loop's own release point.
func (f *frame) isFree(line *pb.Opline) bool {
	if int(line.GetOpcode()) >= len(f.names) {
		return false
	}
	switch f.names[line.GetOpcode()] {
	case "ZEND_FE_FREE", "ZEND_FREE":
		return true
	}
	return false
}

// elementRef boxes the element or property a write path is descending into and
// returns the box. Handing back a reference rather than a copy is what makes
// both `$a[0][1] = x` and `$r = &$a[0]` land in the original container, and it
// removes any need to guess whether the slot should be vivified as an array:
// whatever writes through the box decides.
func elementRef(ctx *php.Ctx, container php.Value, f *frame, line *pb.Opline, isObject bool) *php.Ref {
	if isObject {
		propName := f.fetch(line.GetOp2Type(), line.GetOp2()).String()
		object := container.Object()
		// The declared type travels with the slot: once a name is bound to a
		// typed property, writes through that name are checked against the
		// property's type rather than against nothing.
		return notePropertyType(object, propName,
			object.Props.RefAt(php.StringKey(propName)))
	}
	array := container.Array()
	// An UNUSED key is `$a[] = ...`, which appends, so the element has to exist
	// before a reference to it can be taken.
	if line.GetOp2Type() == opUnused {
		return array.RefAt(array.Append(php.Undef()))
	}
	return array.RefAt(php.ArrayKey(ctx, f.fetch(line.GetOp2Type(), line.GetOp2())))
}

// namedVariable resolves what a by-name variable opcode refers to. The name is
// the *value* of op1 rather than op1 itself -- these are the forms the compiler
// emits for $$n and for $GLOBALS["x"] -- and extended_value says which symbol
// table to look in.
func (f *frame) namedVariable(line *pb.Opline) (name string, slot uint32, found, global bool) {
	name = f.fetch(line.GetOp1Type(), line.GetOp1()).String()
	if line.GetExtendedValue()&fetchTypeMask&(fetchGlobal|fetchGlobalLock) != 0 {
		return name, 0, false, true
	}
	slot, found = f.slotNamed(name)
	return name, slot, found, false
}

// classKeyword names the reference an UNUSED class operand encodes, which is
// what a diagnostic about it quotes.
func classKeyword(operand uint32) string {
	switch operand & fetchClassMask {
	case fetchClassStatic:
		return "static"
	case fetchClassParent:
		return "parent"
	}
	return "self"
}

// classNameOperand resolves a class reference and turns a failure into the
// catchable Error PHP raises: naming self:: outside a class is the program's
// mistake, not a gap in the interpreter.
func (in *Interp) classNameOperand(ctx *php.Ctx, unit *Unit, f *frame, line *pb.Opline,
	kind, operand uint32) (string, error) {
	name, err := f.classNameFrom(unit, line, kind, operand)
	return name, in.materialize(ctx, unit, err)
}

// forwardsStaticBinding reports whether a static call keeps the caller's late
// static binding rather than replacing it. self::, parent:: and static:: all
// forward; a call naming a class outright does not. The compiler encodes the
// keyword as a ZEND_FETCH_CLASS_* code in an unused operand.
func (f *frame) forwardsStaticBinding(line *pb.Opline) bool {
	if line.GetOp1Type() != opUnused {
		return false
	}
	switch line.GetOp1() & fetchClassMask {
	case fetchClassSelf, fetchClassParent, fetchClassStatic:
		return true
	}
	return false
}

// calledClass answers late static binding: the class the call site named, which
// for an instance call is the object's own class and for a static call is
// whatever subclass was written. It reports false outside a class entirely,
// where `static::` has nothing to bind to.
func (f *frame) calledClass() (string, bool) {
	if f.this != nil {
		return f.this.ClassName, true
	}
	if f.called != nil {
		return f.called.Name, true
	}
	if f.class != nil {
		return f.class.Name, true
	}
	return "", false
}

// classNameFrom resolves the class reference held in one operand. Which operand
// that is differs by opcode -- op1 for a static method call, op2 for a static
// property -- so it is always passed in explicitly. Assuming op1 silently read
// the property name as a class name.
//
// A constant names the class outright. An UNUSED operand instead carries a
// ZEND_FETCH_CLASS_* code in its number, packed with flags, so the low bits are
// masked out rather than compared whole.
func (f *frame) classNameFrom(unit *Unit, line *pb.Opline, kind, operand uint32) (string, error) {
	if kind != opUnused {
		value := f.fetch(kind, operand).Deref()
		// `new $obj`, `$obj::CONST` and `$obj::method()` name the class of the
		// object rather than converting it to a string -- which would ask for
		// __toString and, on a class that has none, produce an empty name.
		if object := value.Object(); object != nil {
			return object.ClassName, nil
		}
		if value.Kind() != php.KindString {
			// Nothing else is a class reference. The engine refuses before it
			// looks anything up, so `$undefined::method()` reports this rather
			// than a class named "" -- and reports it before the method name
			// operand is even read.
			return "", Raise("Error", "Class name must be a valid object or a string")
		}
		return value.String(), nil
	}
	switch operand & fetchClassMask {
	case fetchClassSelf, fetchClassStatic:
		// `static::` is late static binding, which calledClass answers.
		// `self::` always names the class whose body is running.
		if operand&fetchClassMask == fetchClassStatic {
			if called, ok := f.calledClass(); ok {
				return called, nil
			}
		}
		if f.class == nil {
			// Naming self:: or static:: where no class is executing is the
			// program's mistake, and PHP words it by the keyword.
			return "", Raise("Error", "Cannot access %q when no class scope is active",
				classKeyword(operand))
		}
		return f.class.Name, nil
	case fetchClassParent:
		if f.class == nil {
			return "", Raise("Error", "Cannot access \"parent\" when no class scope is active")
		}
		if f.class.Parent == "" {
			return "", Raise("Error",
				"Cannot use \"parent\" when current class scope has no parent")
		}
		return f.class.Parent, nil
	}
	return "", &UnsupportedError{Opcode: "class reference that could not be resolved"}
}

// iterator is what foreach and `yield from` pull on: either a snapshot of an
// array's keys, or a generator advanced one step at a time.
type iterator struct {
	array *php.Array
	keys  []php.Key
	pos   int
	// live makes the walk follow the array as the loop mutates it, rather than
	// stepping through the keys it had at the start. That is what a
	// by-reference foreach does: `foreach ($a as &$v)` sees an element appended
	// inside the body, and a by-value one -- which iterates a copy -- does not.
	live bool
	// release stops the array tracking this iterator's position.
	release func()

	// weak is set instead of array when the source is a WeakMap, whose keys are
	// objects. Nothing else can hold those: they are not array keys, which is
	// why iterNext hands back a value rather than a php.Key.
	weak *weakMap

	// gen is set instead of array when the source is a generator. It cannot be
	// snapshotted, since its next value may not exist yet.
	gen *generator
	// pulled records that the generator's current value has already been handed
	// out, so the next pull advances it first.
	pulled bool
	// returned is what a delegated-to generator returned, which is the value of
	// the `yield from` expression.
	returned php.Value

	// skipCurrent and skipKey suppress accessors a caller does not need. The
	// engine only calls what it uses, and a user Iterator can tell.
	skipCurrent bool
	skipKey     bool
	// rewound records that the loop's reset already ran rewind() and asked
	// valid(), so the first fetch reads where the iterator is rather than
	// moving it. The engine splits the protocol that way and an implementation
	// can see the difference.
	rewound bool

	// source is the compiled variable a by-reference loop was started from.
	// The loop follows it: `foreach ($a as &$v) { $a = null; }` stops, because
	// what the loop is iterating is whatever $a holds now.
	source    uint32
	hasSource bool

	// backing is the array behind an engine iterator whose Iterator methods are
	// Go functions. The walk still goes through those methods, but a
	// by-reference loop needs a slot to bind, and this is where it is.
	// backingOwner is the object those slots are the properties of, when the
	// iterator was built over one: the declared type travels with the slot.
	backing      *php.Array
	backingOwner *php.Object

	// deferredHooks are the get hooks a property walk has not run yet, keyed by
	// property name, with hookedObject the instance to run them on. Each runs at
	// the turn its property comes up rather than when the loop starts.
	deferredHooks map[string]deferredHook
	// byRef records that the loop binds its variable to what it walks, which
	// a property with a by-value get hook has nothing to offer.
	byRef        bool
	hookedObject *php.Object

	// origin is the object a by-reference loop is really over, when the keys
	// were flattened out of one. A write through the loop variable has to reach
	// the object rather than the copy.
	origin *php.Object
	// scope is what the walk over an object could see when it started, kept so
	// that a property the body *adds* can be judged the same way.
	scope *Class

	// obj drives a user Iterator, whose rewind/valid/current/key/next are
	// ordinary method calls -- so pulling from one runs arbitrary code, and the
	// object cannot be snapshotted any more than a generator can.
	obj     *php.Object
	objTied *Class
	started bool
	depth   int
	// peeked holds an element pulled to decide whether the loop runs at all,
	// which FE_RESET has to do and FE_FETCH must not repeat.
	peeked *peeked
}

// extendObjectWalk adds any property the object has grown since the walk
// started, and reports whether it found one. An object's property table is
// walked live -- `foreach ($o as $v) { $o->fresh = 1; }` reaches `fresh` --
// while an array is not, so this only ever applies to an iterator that came
// from an object.
//
// The visibility test is the one the walk started with: a property added by the
// body is judged from the same scope as the rest.
func (u *Unit) extendObjectWalk(it *iterator) bool {
	if it == nil || it.origin == nil || it.array == nil {
		return false
	}
	class, known := u.findClass(it.origin.ClassName)
	added := false
	it.origin.Props.Each(func(k php.Key, held php.Value) bool {
		// The walk hands out plain names, so a mangled one is compared and
		// added under the name it was flattened to when the loop started.
		key := k
		if plain, _, mangled := php.UnmangleProperty(k.String()); mangled {
			key = php.StringKey(plain)
		}
		if _, already := it.array.Get(key); already {
			return true
		}
		if held.Deref().IsUndef() {
			return true
		}
		if known && !u.propVisible(class, it.scope, k.String()) {
			return true
		}
		it.array.Set(key, held)
		it.keys = append(it.keys, key)
		added = true
		return true
	})
	return added
}

// peeked is one element already taken from an iterator.
type peeked struct {
	key   php.Value
	value php.Value
}

// iterNext yields the next element. A generator source can run arbitrary code
// here, so this takes the context and can fail -- unlike an array walk.
// The key comes back as a *value*, not as an array key. A foreach over an
// Iterator or a generator hands the key through untouched -- null, a float, a
// bool, an array, an object -- and only a caller that is building an array
// converts it, which is where the conversion's own diagnostics belong.
func (in *Interp) iterNext(ctx *php.Ctx, unit *Unit, it *iterator) (php.Value, php.Value, bool, error) {
	if it.peeked != nil {
		element := it.peeked
		it.peeked = nil
		return element.key, element.value, true, nil
	}
	if it.obj != nil {
		return in.iterNextObject(ctx, unit, it)
	}
	if it.weak != nil {
		// The entries are walked live, so one removed inside the loop is not
		// handed out afterwards.
		held := it.weak.Entries()
		if it.pos >= len(held) {
			return php.Undef(), php.Undef(), false, nil
		}
		entry := held[it.pos]
		it.pos++
		return php.Obj(entry.Key()), entry.Value(), true, nil
	}
	if it.gen == nil {
		if it.live {
			key, value, next, ok := it.array.LiveNext(it.pos)
			it.pos = next
			if !ok {
				return php.Undef(), php.Undef(), false, nil
			}
			return key.Value(), value, true, nil
		}
		for {
			if it.pos >= len(it.keys) {
				// The keys were taken when the loop started, but an object is
				// walked live: a property the body adds is reached before the
				// loop ends. Only an object -- a by-value loop over an array
				// iterates a copy and sees nothing of the sort.
				if !unit.extendObjectWalk(it) {
					break
				}
			}
			key := it.keys[it.pos]
			it.pos++
			// A key removed during the loop is skipped rather than yielding a
			// stale value.
			if value, ok := it.array.Get(key); ok {
				if hook, deferred := it.deferredHooks[key.String()]; deferred {
					// The get hook runs here, at this property's turn. A `&get`
					// hands back the box it wants aliased, so what it returned
					// is passed on undisturbed -- a by-reference loop binds it,
					// and a by-value one copies through it as it would through
					// any reference.
					if hook.prop.GetGetHook().GetReturnsRef() {
						read, err := in.callGetHook(ctx, unit, it.hookedObject,
							hook.prop, hook.declaring, it.depth)
						if err != nil {
							return php.Undef(), php.Undef(), false, err
						}
						return key.Value(), read, true, nil
					}
					if it.byRef {
						// A plain get hook computes a value; there is no box
						// behind it for the loop variable to alias. The hook
						// is not run at all -- the engine refuses before it
						// would have.
						return php.Undef(), php.Undef(), false, in.throwError(ctx, unit,
							"Error", "Cannot create reference to property %s::$%s",
							php.DisplayName(hook.declaring.Name), key.String())
					}
					read, err := in.hookedRead(ctx, unit, it.hookedObject,
						hook.prop, hook.declaring, it.depth)
					if err != nil {
						return php.Undef(), php.Undef(), false, err
					}
					return key.Value(), read, true, nil
				}
				return key.Value(), value, true, nil
			}
		}
		return php.Undef(), php.Undef(), false, nil
	}

	if err := in.start(ctx, unit, it.gen); err != nil {
		return php.Undef(), php.Undef(), false, err
	}
	if it.pulled {
		if err := in.advance(ctx, unit, it.gen); err != nil {
			return php.Undef(), php.Undef(), false, err
		}
	}
	if !it.gen.valid() {
		it.returned = it.gen.returned
		return php.Undef(), php.Undef(), false, nil
	}
	it.pulled = true
	return it.gen.key(), it.gen.current(), true, nil
}

// iterStart is the reset half of the Iterator protocol: rewind, then valid.
// The engine does exactly that and no more when a foreach begins, which is
// observable -- current() and key() belong to the first fetch.
func (in *Interp) iterStart(ctx *php.Ctx, unit *Unit, it *iterator) (bool, error) {
	if it.obj == nil {
		// Not a user Iterator -- an aggregate produced an array or a generator,
		// and neither can say whether it is empty without being pulled. The
		// element pulled here is handed to the first fetch rather than lost.
		key, value, more, err := in.iterNext(ctx, unit, it)
		if more {
			it.peeked = &peeked{key: key, value: value}
		}
		return more, err
	}
	it.started = true
	if _, err := in.iteratorMethod(ctx, unit, it, "rewind"); err != nil {
		return false, err
	}
	valid, err := in.iteratorMethod(ctx, unit, it, "valid")
	if err != nil || !valid.Bool() {
		return false, err
	}
	it.rewound = true
	return true, nil
}

// iterNextObject pulls from a user Iterator. The protocol is rewind once and
// then next before every element after the first, which is the order foreach
// itself uses -- an implementation can observe the difference.
func (in *Interp) iterNextObject(ctx *php.Ctx, unit *Unit, it *iterator) (php.Value, php.Value, bool, error) {
	if it.rewound {
		// The reset has already rewound and found the iterator valid; this
		// fetch reads where it stands.
		it.rewound = false
	} else {
		step := "next"
		if !it.started {
			it.started, step = true, "rewind"
		}
		if _, err := in.iteratorMethod(ctx, unit, it, step); err != nil {
			return php.Undef(), php.Undef(), false, err
		}
		valid, err := in.iteratorMethod(ctx, unit, it, "valid")
		if err != nil {
			return php.Undef(), php.Undef(), false, err
		}
		if !valid.Bool() {
			return php.Undef(), php.Undef(), false, nil
		}
	}
	var err error
	// Which accessors run is observable, and a caller that does not need one
	// does not call it: iterator_count asks for neither, and iterator_to_array
	// without preserve_keys never asks for the key.
	value := php.Undef()
	if !it.skipCurrent {
		if value, err = in.iteratorMethod(ctx, unit, it, "current"); err != nil {
			return php.Undef(), php.Undef(), false, err
		}
	}
	if it.skipKey {
		return php.Undef(), value, true, nil
	}
	key, err := in.iteratorMethod(ctx, unit, it, "key")
	if err != nil {
		return php.Undef(), php.Undef(), false, err
	}
	return key.Deref(), value, true, nil
}

func (in *Interp) iteratorMethod(ctx *php.Ctx, unit *Unit, it *iterator, name string) (php.Value, error) {
	// A subclass that overrides one of these wins over the Go implementation of
	// the engine class it extends, which is the whole point of extending it.
	body, declaring, found := unit.resolveMethod(it.objTied, name)
	if found {
		return in.invokeDriven(ctx, unit, body, it.obj, declaring, it.objTied, nil, it.depth)
	}
	if value, handled, err := in.arrayStoreMethod(ctx, unit, it.obj, name, nil); handled {
		return value, err
	}
	if value, handled, err := emptyIteratorMethod(unit, &pendingCall{this: it.obj, builtinMethod: name}); handled {
		return value, err
	}
	if value, handled, err := in.callbackFilterMethod(ctx, unit,
		&pendingCall{this: it.obj, builtinMethod: name}, it.depth); handled {
		return value, err
	}
	return php.Undef(), &UnsupportedError{
		Opcode: "Iterator::" + name + " on " + it.obj.ClassName,
	}
}

// newObjectIterator builds the iterator for a Traversable. An
// IteratorAggregate hands over whatever getIterator() returns, which may itself
// be a generator, an array-backed iterator, or another aggregate.
func (in *Interp) newObjectIterator(ctx *php.Ctx, unit *Unit, source php.Value, depth int) (*iterator, bool, error) {
	object := source.Deref().Object()
	if object == nil {
		return nil, false, nil
	}
	if g, ok := asGenerator(source.Deref()); ok {
		return &iterator{gen: g, returned: php.Null()}, true, nil
	}
	if held, ok := asWeakMap(object); ok {
		return &iterator{weak: held, returned: php.Null()}, true, nil
	}
	class, known := unit.findClass(object.ClassName)
	if !known {
		return nil, false, nil
	}
	if _, backed := asArrayStore(object); backed {
		// An engine class this runtime implements: its Iterator methods are Go
		// functions, so the generic method-driven path would find no bodies.
		if unit.instanceOf(class, "IteratorAggregate") {
			inner, _, err := in.arrayStoreMethod(ctx, unit, object, "getIterator", nil)
			if err != nil {
				return nil, false, err
			}
			return in.newObjectIterator(ctx, unit, inner, depth)
		}
		store, _ := asArrayStore(object)
		return &iterator{obj: object, objTied: class, backing: store.Items(),
			backingOwner: store.Owner(), returned: php.Null(), depth: depth}, true, nil
	}
	if unit.instanceOf(class, "IteratorAggregate") {
		body, declaring, found := unit.resolveMethod(class, "getIterator")
		if !found {
			return nil, false, &UnsupportedError{
				Opcode: "IteratorAggregate::getIterator on " + object.ClassName,
			}
		}
		inner, err := in.invoke(ctx, unit, body, object, declaring, class, nil, depth)
		if err != nil {
			return nil, false, err
		}
		// Whatever comes back has to be something a foreach can walk. An array
		// is not: the engine refuses it in the same words it refuses a plain
		// object, and names the class the loop was written against. Handing
		// back *this* aggregate is refused too -- it would only ask again.
		if !in.traversableValue(unit, inner.Deref()) || inner.Deref().Object() == object {
			return nil, false, Raise("Exception",
				"Objects returned by %s::getIterator() must be traversable or "+
					"implement interface Iterator", php.DisplayName(object.ClassName))
		}
		return in.newObjectIterator(ctx, unit, inner, depth)
	}
	if unit.instanceOf(class, "Iterator") {
		return &iterator{obj: object, objTied: class, returned: php.Null(), depth: depth}, true, nil
	}
	return nil, false, nil
}

// traversableValue reports whether a value is something foreach can walk
// through the Traversable protocol -- which an array, for all that foreach
// accepts one directly, is not.
func (in *Interp) traversableValue(unit *Unit, value php.Value) bool {
	object := value.Object()
	if object == nil {
		return false
	}
	if _, isGenerator := asGenerator(value); isGenerator {
		return true
	}
	class, known := unit.findClass(object.ClassName)
	return known && unit.instanceOf(class, "Traversable")
}

// newDelegate builds the iterator behind `yield from`, whose source is an
// array, another generator, or any Traversable.
func (in *Interp) newDelegate(ctx *php.Ctx, unit *Unit, source php.Value, depth int) (*iterator, error) {
	if g, ok := asGenerator(source); ok {
		if g.finished || g.frame == nil {
			// The engine tests `execute_data == NULL`, which is true of any
			// generator whose frame is gone -- one that returned normally as
			// much as one an exception escaped. There is nothing left to
			// delegate to either way.
			return nil, Raise("Error", "Generator passed to yield from was "+
				"aborted without proper return and is unable to continue")
		}
		if g.running {
			// Delegating to a generator that is *being resumed* -- itself, or
			// one further up the chain that is waiting on it -- has nowhere to
			// go: draining it would have to re-enter a frame that is already
			// executing.
			return nil, Raise("Error",
				"Impossible to yield from the Generator being currently run")
		}
		return &iterator{gen: g, returned: php.Null()}, nil
	}
	if source.Kind() == php.KindObject {
		it, ok, err := in.newObjectIterator(ctx, unit, source, depth)
		if err != nil {
			return nil, err
		}
		if ok {
			return it, nil
		}
	}
	if source.Kind() != php.KindArray {
		return nil, &UnsupportedError{Opcode: "yield from " + source.Kind().String()}
	}
	return &iterator{array: source.Array(), keys: source.Array().Keys(), returned: php.Null()}, nil
}

// objectOperand resolves op1 to an object, treating an unused operand as $this
// -- which is how the compiler encodes $this->prop. It also returns the value
// it looked at, since a failure is reported in terms of that value's type.
//
// quiet suppresses the undefined-variable warning, which a property *write*
// does not produce: `$u->p = 1` reports only the assignment error.
func (f *frame) objectOperand(line *pb.Opline, quiet bool) (*php.Object, php.Value) {
	if line.GetOp1Type() == opUnused {
		if f.this == nil {
			// A static context has no instance; null is what the caller
			// reports the failure on.
			return nil, php.Null()
		}
		return f.this, php.Obj(f.this)
	}
	value := f.fetchOperand(line.GetOp1Type(), line.GetOp1(), quiet)
	if value.Kind() != php.KindObject {
		return nil, value
	}
	return value.Object(), value
}

// fetch reads an operand.
func (f *frame) fetch(kind, operand uint32) php.Value {
	switch kind {
	case opConst:
		literals := f.op.GetLiterals()
		if int(operand) < len(literals) {
			return literalValue(literals[operand])
		}
		return php.Undef()
	case opCV, opTmpVar, opVar:
		if int(operand) < len(f.slots) {
			// Reading a variable in PHP always reads through a reference, so a
			// slot bound with =& yields the shared value, not the box.
			value := f.slots[operand].Deref()
			// Reading a variable that was never assigned is a warning. Only a
			// compiled variable has a name to report; a temporary is generated
			// and cannot be undefined in a way the program can see.
			if kind == opCV && value.Kind() == php.KindUndef && f.ctx != nil {
				f.ctx.Warn("Undefined variable $%s", f.varName(operand))
			}
			return value
		}
		return php.Undef()
	}
	return php.Undef()
}

// fetchOperand chooses between the warning and quiet reads.
func (f *frame) fetchOperand(kind, operand uint32, quiet bool) php.Value {
	if quiet {
		return f.fetchQuiet(kind, operand)
	}
	return f.fetch(kind, operand)
}

// fetchQuiet reads an operand without complaining about an undefined variable.
// isset(), empty() and ?? all ask precisely because the answer may be "not
// set", so a warning there would be noise PHP does not produce either.
func (f *frame) fetchQuiet(kind, operand uint32) php.Value {
	if kind == opCV {
		return f.rawSlot(kind, operand).Deref()
	}
	return f.fetch(kind, operand)
}

// rawSlot reads a slot without dereferencing, which the opcodes that manipulate
// references themselves need.
func (f *frame) rawSlot(kind, operand uint32) php.Value {
	switch kind {
	case opCV, opTmpVar, opVar:
		if int(operand) < len(f.slots) {
			return f.slots[operand]
		}
	}
	return php.Undef()
}

// slotRef returns the box a slot shares, creating one and installing it if the
// slot holds a plain value. This is what turns `$a` into a reference the first
// time `&$a` is taken.
func (f *frame) slotRef(kind, operand uint32) (*php.Ref, bool) {
	switch kind {
	case opCV, opTmpVar, opVar:
	default:
		return nil, false
	}
	if int(operand) >= len(f.slots) {
		return nil, false
	}
	// Taking a reference to an unset variable defines it as null, which is why
	// `$c = &$undefined` leaves both readable without a warning afterwards. At
	// global scope the slot is already a reference into the globals table, so
	// the emptiness has to be fixed there rather than by making a new box.
	if ref, ok := f.slots[operand].RefOf(); ok {
		if ref.Get().Kind() == php.KindUndef {
			ref.Set(php.Null())
		}
		return ref, true
	}
	current := f.slots[operand]
	if current.Kind() == php.KindUndef {
		current = php.Null()
	}
	ref := php.NewRef(current)
	f.slots[operand] = php.MakeRef(ref)
	return ref, true
}

// store writes an operand slot. Constants are not assignable.
func (f *frame) store(kind, operand uint32, value php.Value) {
	switch kind {
	case opCV, opTmpVar, opVar:
		if int(operand) < len(f.slots) {
			// Writing to a slot bound with =& has to reach the shared box, or
			// the other names would not see the change.
			if ref, ok := f.slots[operand].RefOf(); ok {
				ref.Set(value)
				return
			}
			f.slots[operand] = value
		}
	}
}

// bindSlot replaces what a slot holds outright, discarding any reference
// binding rather than writing through it. This is what `$a = &$b` does to $a.
func (f *frame) bindSlot(kind, operand uint32, value php.Value) {
	switch kind {
	case opCV, opTmpVar, opVar:
		if int(operand) < len(f.slots) {
			f.slots[operand] = value
		}
	}
}

// bindResult writes a *reference* into the result slot, replacing whatever the
// slot held rather than writing through it. That distinction only shows up the
// second time an opline runs: a loop body leaves the previous iteration's
// reference in the temporary, and storeResult would write the new reference
// into the old box instead of replacing it -- so the binding would still point
// at the previous iteration's data.
func (f *frame) bindResult(line *pb.Opline, value php.Value) {
	if line.GetResultType() == opUnused {
		return
	}
	f.bindSlot(line.GetResultType(), line.GetResult(), value)
}

// storeResult writes the result slot when the opline has one. An unused result
// is normal: the engine only materializes it where something reads it.
func (f *frame) storeResult(line *pb.Opline, value php.Value) {
	kind := line.GetResultType()
	if kind == opUnused {
		return
	}
	if kind == opVar || kind == opTmpVar {
		// A result slot is a temporary, not a name. The engine frees whatever
		// it held before reusing it, so a reference an earlier pass through the
		// same opline left there is replaced rather than written through --
		// otherwise a loop calling two by-reference functions in turn writes
		// the second one's value into the first one's variable. A CV can be
		// bound with =& and does have to be written through.
		f.bindSlot(kind, line.GetResult(), value)
		return
	}
	f.store(kind, line.GetResult(), value)
}

// literalAt returns the literal behind a CONST operand when the compiler left
// it unevaluated, which is the only case a caller has to treat specially.
func (f *frame) literalAt(kind, operand uint32) (*pb.Literal, bool) {
	if kind != opConst {
		return nil, false
	}
	literals := f.op.GetLiterals()
	if int(operand) >= len(literals) {
		return nil, false
	}
	lit := literals[operand]
	return lit, lit.GetKind() == pb.Literal_KIND_EXPR
}

func literalValue(lit *pb.Literal) php.Value {
	return literalValueInArray(lit, false)
}

func literalValueInArray(lit *pb.Literal, inArray bool) php.Value {
	switch lit.GetKind() {
	case pb.Literal_KIND_NULL:
		return php.Null()
	case pb.Literal_KIND_BOOL:
		return php.Bool(lit.GetIntValue() != 0)
	case pb.Literal_KIND_INT:
		return php.Int(lit.GetIntValue())
	case pb.Literal_KIND_FLOAT:
		return php.Float(lit.GetFloatValue())
	case pb.Literal_KIND_STRING:
		if inArray {
			return php.String(lit.GetStringValue())
		}
		return php.InternedString(lit.GetStringValue())
	case pb.Literal_KIND_ARRAY:
		arr := php.NewArray()
		for _, entry := range lit.GetEntries() {
			key := php.StringKey(entry.GetStringKey())
			if entry.GetIntKey() {
				key = php.IntKey(entry.GetIntKeyValue())
			}
			arr.Set(key, literalValueInArray(entry.GetValue(), true))
		}
		if arr.Len() == 0 {
			arr.MarkInterned()
		}
		return php.Arr(arr)
	}
	return php.Null()
}

// specializedBuiltin maps an opcode the compiler emitted in place of a call
// back to the function it stands for.
func specializedBuiltin(opcode string) string {
	switch opcode {
	case "ZEND_COUNT":
		return "count"
	case "ZEND_STRLEN":
		return "strlen"
	case "ZEND_GET_CLASS":
		return "get_class"
	}
	return "gettype"
}

// operatorSymbol maps an opcode to the way PHP spells its operator in
// "Unsupported operand types".
var operatorSymbol = map[string]string{
	"ZEND_ADD": php.OpAdd, "ZEND_SUB": php.OpSub, "ZEND_MUL": php.OpMul,
	"ZEND_DIV": php.OpDiv, "ZEND_MOD": php.OpMod, "ZEND_POW": php.OpPow,
	"ZEND_BW_AND": php.OpBitAnd, "ZEND_BW_OR": php.OpBitOr, "ZEND_BW_XOR": php.OpBitXor,
	"ZEND_SL": php.OpShiftLeft, "ZEND_SR": php.OpShiftRight,
}

// checkOperands refuses an operation PHP refuses, returning the TypeError it
// would throw. Concatenation is exempt: it converts anything to a string, and
// only an array is worth a diagnostic.
func (in *Interp) checkOperands(ctx *php.Ctx, unit *Unit, name string, a, b php.Value) error {
	if name == "ZEND_CONCAT" || name == "ZEND_FAST_CONCAT" {
		for _, operand := range [...]php.Value{a, b} {
			if operand.Kind() == php.KindArray {
				ctx.Warn("Array to string conversion")
			}
		}
		return nil
	}
	symbol, known := operatorSymbol[name]
	if !known || php.AcceptsArithmetic(ctx, symbol, a, b) {
		return nil
	}
	return in.throwError(ctx, unit, "TypeError", "Unsupported operand types: %s %s %s",
		php.TypeName(a), symbol, php.TypeName(b))
}

func arith(name string, a, b php.Value) php.Value {
	switch name {
	case "ZEND_ADD":
		return php.Add(a, b)
	case "ZEND_SUB":
		return php.Sub(a, b)
	case "ZEND_MUL":
		return php.Mul(a, b)
	case "ZEND_POW":
		return php.Pow(a, b)
	case "ZEND_BW_AND":
		return php.BitAnd(a, b)
	case "ZEND_BW_OR":
		return php.BitOr(a, b)
	case "ZEND_BW_XOR":
		return php.BitXor(a, b)
	}
	return php.Concat(a, b)
}

// compareChecked is compare with the answer to "did the walk find a cycle?"
// alongside, which only the equality forms can produce.
func compareChecked(name string, a, b php.Value) (result, recursed bool) {
	switch name {
	case "ZEND_IS_EQUAL", "ZEND_CASE":
		return php.LooseEqualChecked(a, b)
	case "ZEND_IS_NOT_EQUAL":
		equal, recursed := php.LooseEqualChecked(a, b)
		return !equal, recursed
	case "ZEND_CASE_STRICT", "ZEND_IS_IDENTICAL":
		return php.StrictEqualChecked(a, b)
	case "ZEND_IS_NOT_IDENTICAL":
		identical, recursed := php.StrictEqualChecked(a, b)
		return !identical, recursed
	}
	return compare(name, a, b), false
}

func compare(name string, a, b php.Value) bool {
	switch name {
	// switch compiles to CASE, which compares the subject in op1 against each
	// label without consuming it. The comparison itself is == or ===, decided
	// by whether the subject and every label are of the same type.
	case "ZEND_IS_EQUAL", "ZEND_CASE":
		return php.LooseEqual(a, b)
	case "ZEND_CASE_STRICT":
		return php.StrictEqual(a, b)
	case "ZEND_IS_NOT_EQUAL":
		return !php.LooseEqual(a, b)
	case "ZEND_IS_IDENTICAL":
		return php.StrictEqual(a, b)
	case "ZEND_IS_NOT_IDENTICAL":
		return !php.StrictEqual(a, b)
	case "ZEND_IS_SMALLER", "ZEND_IS_SMALLER_OR_EQUAL":
		// A pair the engine will not order makes every relational operator
		// false, in both directions: the compiler writes `a > b` as `b < a`,
		// so refusing here covers all four.
		if php.Uncomparable(a, b) {
			return false
		}
		if name == "ZEND_IS_SMALLER" {
			return php.Compare(a, b) < 0
		}
		return php.Compare(a, b) <= 0
	}
	return false
}

// refuseUnsetDim answers `unset($x[$k])` for an $x that holds no container.
// Nothing is ever removed; what differs is what the engine says about it.
func (in *Interp) refuseUnsetDim(ctx *php.Ctx, unit *Unit, target php.Value) error {
	switch target.Kind() {
	case php.KindUndef, php.KindNull, php.KindArray, php.KindObject:
		return nil
	case php.KindFalse:
		// The write paths grow a false into an array; this one has nothing to
		// grow, and reports it anyway.
		ctx.Deprecated("Automatic conversion of false to array is deprecated")
		return nil
	case php.KindString:
		return in.throwError(ctx, unit, "Error", "Cannot unset string offsets")
	}
	return in.throwError(ctx, unit, "Error", "Cannot unset offset in a non-array variable")
}

// userArgumentByRef is argumentByRef restricted to callees written in PHP. It
// answers the question SEND_VAR_NO_REF_EX asks -- "does this parameter want a
// slot?" -- for the callees where php2go can be sure of the answer.
//
// An engine function is left out on purpose. Its arginfo may mark a parameter
// ZEND_SEND_PREFER_REF, which takes a reference when one is offered and a value
// without complaint otherwise: extract() is one, and warning about it would be
// wrong. php2go's engineByRef table records only by-reference-ness, so it
// cannot tell the two apart.
func (in *Interp) userArgumentByRef(unit *Unit, calls []*pendingCall, number uint32) bool {
	if len(calls) == 0 || number == 0 {
		return false
	}
	current := calls[len(calls)-1]
	if current.viaUserCall {
		return false
	}
	callee := current.method
	if callee == nil && current.closure != nil {
		callee = current.closure.op
	}
	if callee == nil && current.name != "" && unit != nil {
		if fn, ok := unit.userFunctionBody(current.name); ok {
			callee = fn
		}
	}
	if callee == nil {
		return false
	}
	index := int(number) - 1
	args := callee.GetArgs()
	if index < len(args) {
		return args[index].GetByRef()
	}
	if len(args) > 0 && args[len(args)-1].GetVariadic() {
		return args[len(args)-1].GetByRef()
	}
	return false
}

// arrayLiteral is an array a literal is being assembled into, and the temporary
// holding it. Both are needed: the value to keep it reachable, and the slot to
// know when it stops being this frame's business.
type arrayLiteral struct {
	kind  uint32
	slot  uint32
	value php.Value
}

// consumeLiterals drops the literals an opline reads. Reading the temporary a
// literal lives in is what hands it to somewhere that keeps it -- a name, a
// call's argument list, a return value -- so the frame no longer has to.
func (f *frame) consumeLiterals(line *pb.Opline) {
	if len(f.literals) == 0 {
		return
	}
	kept := f.literals[:0]
	for _, held := range f.literals {
		read := (line.GetOp1Type() == held.kind && line.GetOp1() == held.slot) ||
			(line.GetOp2Type() == held.kind && line.GetOp2() == held.slot)
		if !read {
			kept = append(kept, held)
		}
	}
	f.literals = kept
}

// isUserCallBuiltin reports whether a name refers to one of the functions that
// take a callable and invoke it with the arguments they were given. Their own
// parameters are by value, so a callee that wanted a reference gets a copy --
// which PHP reports, once per argument.
//
// The namespace is stripped first: `call_user_func` written unqualified inside
// one compiles to a name the resolver leaves prefixed when no function of that
// name is declared there, and the engine still reaches the global builtin.
// isUserCallArrayBuiltin picks out the array form, which differs from the plain
// one in what it does with a reference: it passes it through.
func isUserCallArrayBuiltin(name string) bool {
	if at := strings.LastIndexByte(name, '\\'); at >= 0 {
		name = name[at+1:]
	}
	return functionKey(name) == "call_user_func_array"
}

// passedValue is what an argument the program wrote is worth once it has been
// read. Reading an undefined variable warns and yields null, and the parameter
// it fills is null too -- not undefined, which would warn a second time inside
// the callee. An undefined slot in the argument list is a *hole* the caller
// never filled, which is a different thing, and this never produces one.
func passedValue(v php.Value) php.Value {
	if v.IsUndef() {
		return php.Null()
	}
	return v
}

func isUserCallBuiltin(name string) bool {
	if at := strings.LastIndexByte(name, '\\'); at >= 0 {
		name = name[at+1:]
	}
	switch functionKey(name) {
	case "call_user_func", "call_user_func_array":
		return true
	}
	return false
}

// privateGlobal reports a global that is php2go's own request state -- a
// superglobal a library registered under a name the engine does not define,
// `$_RESP_HEADERS` -- which no program on the engine could see in `$GLOBALS`
// or `get_defined_vars()`.
func privateGlobal(ctx *php.Ctx, k php.Key) bool {
	return !k.IsInt && ctx.SuperglobalRegistered(k.Str) && !php.IsEngineSuperglobal(k.Str)
}
