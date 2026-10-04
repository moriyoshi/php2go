// Package lower turns a harvested op_array into the low-level IR that
// IR-DESIGN.md specifies, printed in the format IR-ASSEMBLY.md defines.
//
// The op_array is a flattened AST: a node with more than two operands is spread
// over several oplines, and the temporaries between them are AST edges rather
// than values. Lowering is therefore an *un-flattening* -- recognising the
// opcode groups that were one node and folding each back into one instruction.
// The interpreter already performs every one of those groupings, but
// dynamically, as side structures keyed by temp slot; this does them once,
// statically.
//
// # Compiled variables stay as places
//
// The op_array's CV/TMP split maps onto LLVM's alloca/register split: a CV is a
// named slot assigned repeatedly, a TMP is single-assignment by construction.
// So CVs lower to places (`local.get`/`local.set`) and only TMP/VAR become SSA
// values, leaving CV promotion to a later mem2reg pass. Anything else that
// outlives its block -- a cross-block temporary, a foreach iterator -- becomes a
// place by the same rule. That is the standard front-end shape and it defers
// every phi question without producing anything incorrect.
//
// # What this does not do yet
//
// No SSA promotion, no types beyond `zval`, no optimization. A body that cannot
// be lowered is reported as a [Bail] naming the opcode that stopped it, which is
// the compiler's equivalent of the interpreter's `unsupported` bucket -- never a
// plausible-looking lowering of a construct the pass does not pb.
package lower

import (
	"fmt"
	"io"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	pb "github.com/moriyoshi/php2go/internal/ir/irpb"
)

// Lowerer lowers op_arrays. The zero value is not usable: Names must be the
// opcode table of the build that produced the IR, since opcode numbering is
// version-specific and the IR is only meaningful alongside it.
type Lowerer struct {
	Names []string
	// SkipMem2Reg turns off the promotion of places to SSA values.
	//
	// Promotion is the **default**: the zero value promotes, so a consumer that
	// builds a Lowerer gets the pipeline the compiler actually runs rather than
	// an older shape it has to opt out of. It is verified behaviour-neutral --
	// the corpus reads identically with and without -- and it is what the
	// ownership work needs, because copy elision and escape analysis ask
	// questions about values rather than slots.
	//
	// The switch stays because turning the pass off is how a measurement
	// separates "this pass broke it" from "it was already broken", which this
	// session used to find four wrong answers and a set of analyses that had
	// silently stopped finding anything.
	SkipMem2Reg bool
	// Trace, when set, receives the lowerer's own view of any body it cannot
	// lower -- its block leaders, its cross-block set, and the whole opline
	// list as *it* sees them. Dumping the pass's own view rather than
	// re-deriving one from another tool is deliberate: comparing against a
	// second tool reading a differently-extracted file is what once made an
	// entire class of bails look inexplicable.
	Trace io.Writer
	// Frameless names the functions behind ZEND_FRAMELESS_ICALL_*, indexed by
	// the opcode's extended_value. Those opcodes name no function otherwise, so
	// without this table a frameless call cannot be resolved at all. The
	// manifest carries it as `frameless_functions`. The index is per arity: the
	// two- and three-argument forms of one function get different slots.
	Frameless []string
	// ByValueArg, when set, answers whether position pos of the named
	// function is an ordinary by-value parameter -- for a function the unit
	// being lowered declares unconditionally and that has no variadic
	// parameter. It settles an undecided send (see settleSends).
	ByValueArg func(callee string, pos int) bool
	// ByRefArg is the other answer: position pos of the named function, which
	// the unit declares unconditionally, is declared by reference. It decides
	// a ZEND_SEND_VAR_NO_REF_EX (see noRefSends).
	ByRefArg func(callee string, pos int) bool
	// DeclaresFunction reports whether the unit declares a function of this
	// name. An unqualified call inside a namespace resolves to the
	// namespaced function when there is one and to the global otherwise,
	// and the send checks need to know which.
	DeclaresFunction func(name string) bool
	// LinkFatal marks the file body's ZEND_DECLARE_CLASS at LinkFatalOpline
	// as one the engine's trial link ended in a fatal error (zend.Unit.
	// LinkFatal): declaring it is where the program stops.
	LinkFatal       bool
	LinkFatalOpline int
	// GlobalScope lowers a file body that runs **over another file's
	// variables** -- an included file at global scope: every compiled
	// variable is bound to its global first, as `global $x` binds, so no pass
	// takes one of them for a local it owns.
	GlobalScope bool
}

// Lower converts one op_array. The error is a [*Bail] naming the opcode that
// stopped it.
// deprecationReason is `#[\Deprecated("...")]`'s optional argument. The
// diagnostic appends it after a comma, so it wants the string itself rather
// than the quoted operand text `LiteralText` renders.
// deprecationReason renders `#[\Deprecated]`'s message or since argument as IR
// **operand text**, not as a Go string.
//
// The argument is `?string` but the *value written* need not be a string:
// `#[\Deprecated(1234)]` is legal and, in a weak-mode file, deprecates with
// `, 1234`; under `strict_types=1` the same attribute is a TypeError naming
// `Deprecated::__construct()`. Reading it with `GetStringValue()` returned ""
// for every non-string and silently dropped the reason.
//
// Carrying the operand text lets the emitter render a `php.Value` and put it
// through the ordinary argument coercion, which already knows both halves of
// that rule.
// arrayElementByRef is the bit ZEND_INIT_ARRAY and ZEND_ADD_ARRAY_ELEMENT set
// in extended_value for an element written `&$x`.
const arrayElementByRef = 1

func deprecationReason(lit *pb.Literal) string {
	if lit == nil {
		return ""
	}
	text := LiteralText(lit)
	if text == "" || strings.HasPrefix(text, "<") {
		// A deferred constant expression -- the tree, not a value. Rendering
		// it needs the emitter's constant evaluator, so it is left out rather
		// than guessed at.
		return ""
	}
	return text
}

// staticVarsOf carries the `static $x = ...` table. A closure's `use` list
// shares it in the engine, and those are told apart by having no default: the
// value comes from the enclosing scope rather than an initializer.
func staticVarsOf(oa *pb.OpArray) []StaticVar {
	var out []StaticVar
	for _, sv := range oa.GetStaticVars() {
		v := StaticVar{Name: sv.GetName(), HasDefault: sv.GetHasDefault()}
		if v.HasDefault {
			v.Default = LiteralText(sv.GetDefaultValue())
		}
		out = append(out, v)
	}
	return out
}

func (l *Lowerer) Lower(oa *pb.OpArray) (*Function, error) {
	oplines := oa.GetOplines()
	if len(oplines) == 0 {
		return nil, &Bail{Reason: "empty"}
	}
	b := &lowering{
		cfg:     l,
		oa:      oa,
		oplines: oplines,
		fn: &Function{
			Name:                  displayName(oa),
			File:                  oa.GetFilename(),
			Returns:               oa.GetReturnType(),
			StrictTypes:           oa.GetStrictTypes(),
			Visibility:            oa.GetVisibility(),
			Statics:               staticVarsOf(oa),
			Deprecated:            oa.GetDeprecated(),
			DeprecatedMessage:     deprecationReason(oa.GetDeprecatedMessage()),
			DeprecatedSince:       deprecationReason(oa.GetDeprecatedSince()),
			DeprecatedMessageExpr: oa.GetDeprecatedMessage(),
			DeprecatedSinceExpr:   oa.GetDeprecatedSince(),
		},
		slotValue:    map[uint32]Value{},
		excSaved:     map[int]string{},
		listPlaces:   map[uint32]Value{},
		madeRef:      map[string]bool{},
		finallyAt:    map[uint32][2]uint32{},
		emptyFinally: map[uint32]bool{},
		inFinally:    map[int]bool{},
	}
	if err := b.run(); err != nil {
		return nil, err
	}
	// Forming intrinsic calls is part of lowering, not an optimisation: an
	// operation *is* a call to a builtin that drives the runtime, and every
	// consumer of this IR should see one. Only the choice of a narrower
	// variant is optional, and that is lower.Specialize.
	IntrinsicCalls(b.fn)
	// Reading a variable is an operation -- it warns when unset and reads
	// through a reference -- so it is a call like every other operation.
	ExplicitReads(b.fn)
	b.fn.LiveParams = LiveParams(b.fn)
	if !l.SkipMem2Reg {
		Mem2Reg(b.fn)
	}
	return b.fn, nil
}

// inlineRequest is one finally body still to be copied, and the block whose
// terminator will jump to the copy.
type inlineRequest struct {
	rng    [2]uint32
	cont   int
	unwind bool
	// inFlight is a normal-exit copy requested from inside an unwind copy:
	// an exception is still in flight while it runs.
	inFlight bool
	from     *Block
	region   int
	// regions is the region map of the copy the request was made from.
	regions map[int]int
	// chain is the copy chain the request was made from (copyChain).
	chain map[[2]uint32]bool
}

// pendingInline marks a block whose terminator is waiting for its copy to be
// made. It is never printed: every request is resolved before lowering ends.
const pendingInline = "<pending finally>"

// catchRequest is one `catch` clause: the block the exception arrives at, the
// class it accepts, and where control goes when it does not match.
// catchUnwind is a catch clause whose no-match edge leaves through a region's
// finally. The target is recorded rather than resolved, because the block that
// runs the finally on the way out does not exist yet: `unwindPaths` makes it,
// and it runs after the catch bodies.
type catchUnwind struct {
	from   *Block
	region int
}

type catchRequest struct {
	from  *Block
	exc   string // the caught exception, as a block argument
	class string
	bind  string // the variable to bind, or "" for `catch (E)` with no name
	body  int    // the handler body, entered only on a match
	next  int    // the next catch, or -1 when this is the last one
}

// fetchRequest is one `foreach` step: the block that tests the iterator, and
// the fetch that has to happen only once the test has passed.
type fetchRequest struct {
	from  *Block
	it    string
	value string
	key   string
	fall  int
	exit  int
	// byRef is `foreach ($a as &$v)`: the loop variable **binds** to the
	// element rather than receiving a copy of it, so a write through it is a
	// write into the array. FE_FETCH_R and FE_FETCH_RW are otherwise the same
	// opcode shape, and treating them as the same opcode meant every
	// by-reference loop silently discarded its writes.
	byRef bool
	// reset is the block that created the iterator. It is the loop's **one
	// entry that must not step**, and telling it apart from the back edges is
	// what lets the step move to where the engine performs it.
	reset *Block
}

// pendingCall accumulates one call between its INIT and its DO. This is the
// heart of the pass: the three-phase call protocol is 43.9% of the corpus's
// oplines, and each run of them collapses into a single instruction.
type pendingCall struct {
	opcode   string   // call.fn / call.method / call.static / call.dynamic / ctor
	callee   string   // a literal name, or "" when dynamic
	receiver string   // the object or class operand, when there is one
	args     []string // rendered with their send mode
	// fallback is the unqualified name a namespaced call falls back to when the
	// namespaced one does not exist. Empty for every other call form.
	fallback string
	// spans records that this call is completed in a different block from the
	// one that started it, so its operands are held in places.
	spans bool
	// checked records ZEND_CHECK_UNDEF_ARGS, which the compiler emits after a
	// named or unpacked argument run to guard against a gap in the positions.
	// It is a property of the call, not an instruction of its own.
	checked bool
	// userSend records a ZEND_SEND_USER: the call is `call_user_func`'s,
	// whose arguments go by value and whose callee warns for each one it
	// wanted by reference. Emitted as the `user_send` attribute.
	userSend bool
	// refuseByRef are the positions a value was sent to with
	// ZEND_SEND_VAL_EX in a call whose callee is known only at run time:
	// the engine refuses each where the callee takes a reference.
	refuseByRef []string
	// noRef holds the positions sent by ZEND_SEND_VAR_NO_REF, true for the
	// _EX form: a value the compiler could not prove is a variable, sent
	// where a reference may be wanted. See noRefSends.
	noRef map[int]bool
	// sentAt records, for each by-value send of a variable, the block and
	// instruction position it was sent at. See snapshotSends.
	sentAt map[int][2]int
}

// SeparateAttr marks a `local.set` that replaces a reference the variable
// holds rather than writing through it: the verified return value.
const SeparateAttr = "separate"

// UserSendAttr marks a call whose arguments were sent by ZEND_SEND_USER --
// `call_user_func` and `call_user_func_array` the compiler expanded in place.
// Each goes by value, and a by-reference parameter given one warns.
const UserSendAttr = "user_send"

// StrictAssignAttr marks a store that is a **strict** assignment whatever
// the file declares: the engine binds a caught exception that way, so
// `catch (Exception $ref)` into a reference held by a `string` property is a
// TypeError rather than a `__toString` call.
const StrictAssignAttr = "strict_assign"

// lowering is the state of one op_array in flight.
type lowering struct {
	cfg *Lowerer
	// namedFuncArg is set by a CHECK_FUNC_ARG naming its argument, until
	// the send: the fetch between is for a parameter chosen by name.
	namedFuncArg bool
	oa           *pb.OpArray
	oplines      []*pb.Opline
	fn           *Function

	leaders []int
	blockOf []int
	// litSlot maps an op_array literal index to its slot in Function.Literals,
	// so one deferred initializer read twice is carried once.
	litSlot map[uint32]int
	// crossBlock holds the TMP/VAR slots read in a block that did not define
	// them; each becomes a named place rather than an SSA value.
	crossBlock map[uint32]bool
	// fiberSuspend holds the DO_* oplines that complete a Fiber::suspend().
	fiberSuspend map[int]bool
	// spanningCall holds the INIT oplines whose call finishes in another block,
	// so its operands must be spilled to places rather than carried as values.
	spanningCall map[int]bool
	// spill numbers the places made for those operands.
	spill int
	// finallyAt maps a finally body's first opline to its range, and inFinally
	// marks the blocks that lie inside one. Those blocks are never emitted
	// standalone -- they exist only as copies spliced in at each FAST_CALL.
	finallyAt map[uint32][2]uint32
	// finallyOfVar maps a finally's FAST_CALL variable -- the operand of
	// its FAST_RET and of every DISCARD_EXCEPTION that names it -- to the
	// finally's range.
	finallyOfVar map[uint32][2]uint32
	// copyChain is, while a finally copy is being lowered, every finally
	// range whose copy encloses it, true for an unwind copy. Only the
	// finally's own unwind copy holds its exception in flight.
	copyChain map[[2]uint32]bool
	// emptyFinally marks the finally_op of a `finally {}` with no body.
	emptyFinally map[uint32]bool
	inFinally    map[int]bool

	// Per block.
	blk       *Block
	b         int
	start     int
	end       int
	slotValue map[uint32]Value
	// madeRef is the set of values ZEND_MAKE_REF produced, so that the wrap
	// ASSIGN_REF applies to a non-variable source is not applied twice -- which
	// would notice twice for one binding.
	madeRef  map[string]bool
	todo     []int
	skipNext bool
	spliced  int

	// Across blocks.
	calls         []*pendingCall
	currentLine   uint32
	resumed       Value
	resumeSlot    uint32
	resumePending bool

	// While a finally body is being copied: remap sends the body's own block
	// IDs to the copy's, cont is the block the copy returns to, and unwind says
	// the copy ends by resuming an exception rather than returning.
	// refResult maps the value of an `$a = &$b` to `$a`, for a send that
	// may bind by reference.
	refResult map[string]string
	remap     map[int]int
	cont      int
	unwind    bool
	// inFlight is set while copying anything an exception is unwinding
	// through: an unwind copy, and every copy made from inside one.
	inFlight bool
	// regionMap sends a region nested in the finally body being copied to
	// the copy's own clone of it (inlineFinally), so the copy's blocks are
	// protected by live handlers rather than the original's dead ones.
	regionMap map[int]int
	// excSaved is, for a region cloned into a copy an exception is
	// unwinding through, the value holding that exception: a catch there
	// puts it back once it has taken its own (catchBodies). Allocated up
	// front, so a copy's restore of the lowering keeps what it added.
	excSaved map[int]string
	// listPlaces are the places FETCH_LIST_W produced, by result slot, for
	// a list nested in one of their elements.
	listPlaces map[uint32]Value
	depth      int
	// pending holds the finally copies still to be made. Copies are appended
	// after every original block exists, so that a block's index and its ID stay
	// equal -- allocating them while the main loop is still appending would make
	// the two diverge and every later lookup address the wrong block.
	pending []inlineRequest
	catches []*catchRequest
	// catchUnwinds are the "no clause matched" edges that leave through a
	// region's finally, resolved after `unwindPaths` has made the copy.
	catchUnwinds []catchUnwind
	// iterOwned marks a foreach cursor whose subject was a temporary, so the
	// cursor is the only thing holding it.
	iterOwned map[string]bool
	// loopRefTemp is the compiler temporary a by-reference foreach binds
	// each element to, by cursor, which the loop's free unsets.
	loopRefTemp map[string]string
	// iterSubject is an owned cursor's subject value, carried to its
	// `iter.free.owned` so a subject nothing else holds can die there
	// (LoopOwnedObjects).
	iterSubject map[string]string
	// excChain tells a `catch` clause which place holds the exception, keyed by
	// the opline the clause before it names in op2.
	excChain map[int]string
	// iterResets is the block each live iterator was created in, so a fetch can
	// name its loop's entry edge.
	iterResets map[string]*Block
	// fetches holds the foreach loop bodies still to be made, for the same
	// reason: they are appended after every original block exists.
	fetches []fetchRequest

	// Per opline.
	i  int
	op *pb.Opline
	n  string
}

func (l *lowering) name(i int) string {
	op := l.oplines[i].GetOpcode()
	if int(op) < len(l.cfg.Names) {
		return l.cfg.Names[op]
	}
	return "?"
}

func (l *lowering) run() error {
	l.fiberSuspend = findFiberSuspends(l.oa, l.oplines, l.name)
	l.leaders, l.blockOf = blockLeaders(l.oa, l.oplines, l.name, l.fiberSuspend)
	l.spanningCall = findSpanningCalls(l.oa, l.oplines, l.name, l.blockOf)
	for i, arg := range l.oa.GetArgs() {
		// The `&` is not decoration. A by-reference parameter binds the
		// caller's place rather than receiving a copy, which is a different
		// calling convention -- and dropping it made a consumer unable to tell
		// `f($x)` from `f(&$x)`, so it emitted the by-value one silently.
		// The harvest always had it; this is where it was being lost.
		amp := ""
		if arg.GetByRef() {
			amp = "&"
		}
		l.fn.Params = append(l.fn.Params,
			fmt.Sprintf("%d %s$%s %q", i, amp, arg.GetName(), arg.GetType()))
		l.fn.ParamInfo = append(l.fn.ParamInfo, FunctionParameter{
			Name: arg.GetName(), ByRef: arg.GetByRef(), Variadic: arg.GetVariadic(),
		})
		l.fn.Sensitive = append(l.fn.Sensitive, declaresSensitive(arg))
	}
	if !slices.Contains(l.fn.Sensitive, true) {
		// Nothing to hide: the common case carries no table at all.
		l.fn.Sensitive = nil
	}
	l.fn.LineStart = l.oa.GetLineStart()
	l.fn.LineEnd = l.oa.GetLineEnd()
	l.fn.DocComment = l.oa.GetDocComment()
	l.fn.Final = l.oa.GetFinal()
	l.fn.Abstract = l.oa.GetAbstract()
	l.fn.VarNames = l.oa.GetVarNames()
	l.fn.RequiredParams = int(l.oa.GetRequiredArgs())
	l.fn.ReturnsRef = l.oa.GetReturnsRef()
	l.fn.Static = l.oa.GetStatic()
	l.fn.UsesThis = l.oa.GetUsesThis()
	for _, tc := range l.oa.GetTryCatch() {
		r := Region{Catch: -1, Finally: -1, Parent: -1}
		if tc.GetCatchOp() != 0 {
			r.Catch = l.blockAt(tc.GetCatchOp())
		}
		// An **empty** `finally {}` (finally_end == finally_op) is no
		// handler at all: running nothing and resuming the unwind is what
		// passing the exception straight to the enclosing handler does.
		if tc.GetFinallyOp() != 0 && tc.GetFinallyEnd() > tc.GetFinallyOp() {
			r.Finally = l.blockAt(tc.GetFinallyOp())
		}
		l.fn.Regions = append(l.fn.Regions, r)
	}
	l.linkRegions()
	l.crossBlock = findCrossBlock(l.oplines, l.leaders, l.name)

	for _, tc := range l.oa.GetTryCatch() {
		start, stop := tc.GetFinallyOp(), tc.GetFinallyEnd()
		if start != 0 && stop <= start {
			l.emptyFinally[start] = true
		}
		if start == 0 || stop <= start {
			continue
		}
		l.finallyAt[start] = [2]uint32{start, stop}
		if int(stop) < len(l.oplines) && l.name(int(stop)) == "ZEND_FAST_RET" {
			if l.finallyOfVar == nil {
				l.finallyOfVar = map[uint32][2]uint32{}
			}
			l.finallyOfVar[l.oplines[stop].GetOp1()] = [2]uint32{start, stop}
		}
		for b, at := range l.leaders {
			if uint32(at) >= start && uint32(at) < stop {
				l.inFinally[b] = true
			}
		}
	}

	for b, start := range l.leaders {
		end := len(l.oplines) - 1
		if b+1 < len(l.leaders) {
			end = l.leaders[b+1] - 1
		}
		if l.inFinally[b] {
			l.fn.Blocks = append(l.fn.Blocks, &Block{ID: b, Region: -1, Dead: true})
			continue
		}
		l.b, l.start, l.end = b, start, end
		l.blk = &Block{ID: b, Region: regionOf(l.oa, start), InFinallyOf: l.finallysContaining(start),
			InCatchBodyOf: catchBodyOf(l.oa, start) + 1}
		l.fn.Blocks = append(l.fn.Blocks, l.blk)
		l.slotValue = map[uint32]Value{}
		if l.resumePending {
			// The previous block ended in a yield; its resumption value is this
			// block's parameter.
			l.blk.Params = append(l.blk.Params, fmt.Sprintf("%s: zval", l.resumed))
			l.slotValue[l.resumeSlot] = l.resumed
			if l.crossBlock[l.resumeSlot] {
				l.emit("local.set", []string{tempName(l.resumeSlot), l.resumed.String()})
			}
			l.resumePending = false
		}

		if b == 0 && l.cfg.GlobalScope && displayName(l.oa) == "{main}" {
			for _, name := range l.oa.GetVarNames() {
				if name == "this" {
					continue
				}
				l.emit("bind_global", []string{"$" + name, strconv.Quote(name)})
			}
		}
		l.skipNext = false
		l.spliced = 0
		// A queue rather than a range: hitting FAST_CALL splices the finally
		// body in at this point, which is what inlining is.
		l.todo = make([]int, 0, end-start+1)
		for i := start; i <= end; i++ {
			l.todo = append(l.todo, i)
		}
		if err := l.drain(); err != nil {
			return err
		}
		if l.blk.Term == "" && b+1 < len(l.leaders) {
			l.term(fmt.Sprintf("jmp bb%d", b+1), b+1)
		}
	}
	if err := l.catchBodies(); err != nil {
		return err
	}
	if err := l.fetchBodies(); err != nil {
		return err
	}
	if err := l.unwindPaths(); err != nil {
		return err
	}
	// **Copying a body queues more work, and the queues feed each other.** A
	// `foreach` inside a `finally` reaches `fetchBodies` only after
	// `unwindPaths` has made the copy, and a `try` inside one queues another
	// inline the same way. Running each pass once left those requests sitting
	// in their queue and the copy's terminator unresolved -- which the emitter
	// saw as `terminator <pending finally>`.
	//
	// Each pass consumes its queue, so this converges: the depth limit in
	// inlineFinally is what stops a finally that reaches itself.
	for round := 0; len(l.catches)+len(l.fetches)+len(l.pending) > 0; round++ {
		if round > 32 {
			return l.bail("handler bodies did not converge")
		}
		if err := l.catchBodies(); err != nil {
			return err
		}
		if err := l.fetchBodies(); err != nil {
			return err
		}
		if err := l.drainInlines(); err != nil {
			return err
		}
	}
	l.resolveCatchUnwinds()
	return l.checkEdges()
}

// resolveCatchUnwinds points every "no clause matched" edge at the region's
// exceptional finally copy, now that `unwindPaths` has made it.
func (l *lowering) resolveCatchUnwinds() {
	for _, u := range l.catchUnwinds {
		to := l.fn.Regions[u.region].Finally
		if to < 0 || len(u.from.Succs) != 2 {
			continue
		}
		u.from.Succs[1] = to
		if i := strings.LastIndex(u.from.Term, ", bb"); i >= 0 {
			u.from.Term = fmt.Sprintf("%s, bb%d", u.from.Term[:i], to)
		}
	}
	l.catchUnwinds = nil
}

// linkRegions records which region encloses which, computed from the opline
// ranges the harvest carries. An exception the inner handler does not take goes
// outward, so a sound CFG needs the chain rather than just the innermost entry
// that Block.Region holds.
func (l *lowering) linkRegions() {
	tc := l.oa.GetTryCatch()
	span := func(i int) (int, int) {
		start := int(tc[i].GetTryOp())
		stop := int(tc[i].GetCatchOp())
		if stop == 0 || stop < start {
			stop = int(tc[i].GetFinallyEnd())
		}
		return start, stop
	}
	for i := range l.fn.Regions {
		si, ei := span(i)
		best, bestLen := -1, 1<<30
		for j := range l.fn.Regions {
			if i == j {
				continue
			}
			sj, ej := span(j)
			if sj <= si && ei <= ej && ej-sj < bestLen {
				best, bestLen = j, ej-sj
			}
		}
		l.fn.Regions[i].Parent = best
	}
}

// drain lowers everything queued in todo into the current block.
func (l *lowering) drain() error {
	for len(l.todo) > 0 {
		l.i = l.todo[0]
		l.todo = l.todo[1:]
		if l.skipNext {
			l.skipNext = false
			continue
		}
		l.op = l.oplines[l.i]
		l.n = l.name(l.i)
		if l.op.GetLine() != l.currentLine {
			l.currentLine = l.op.GetLine()
			l.emitLine()
		}
		if err := l.opline(); err != nil {
			return err
		}
	}
	return nil
}

// unwindPaths gives each `finally` a copy of its body for the exceptional exit.
//
// The normal exits already have one: FAST_CALL splices the body in at each of
// them. The exceptional exit had nothing at all -- an exception leaving a
// protected range must run the finally and then keep unwinding, and the region
// pointed at a block marked Dead, so the path simply was not in the IR. That
// made every catch-and-finally analysis quietly incomplete, and it made the
// CFG's exception edge point at a block no pass would look at.
//
// The copy ends in `rethrow` rather than falling through, which is the whole
// difference between the two exits: FAST_RET returns to its call site on the
// normal path and resumes unwinding on this one. Its region is the *enclosing*
// one, because an exception raised inside a finally propagates outward rather
// than back into the try it belongs to.
func (l *lowering) unwindPaths() error {
	tcs := l.oa.GetTryCatch()
	for i := range l.fn.Regions {
		rng, ok := l.finallyRange(i)
		if !ok {
			continue
		}
		// A try inside a finally body protects only the original's blocks,
		// which are dead: every copy of the body has its own clone of the
		// region, with its own unwind copy (inlineFinally). A copy made for
		// the original would route to its dead catch.
		if l.insideFinally(tcs[i].GetTryOp()) {
			continue
		}
		l.pending = append(l.pending, inlineRequest{rng: rng, unwind: true, region: i})
	}
	return l.drainInlines()
}

// drainInlines makes every finally copy still queued. Copying a body can queue
// more work -- a finally inside a finally has its own FAST_CALL -- and reaches
// this loop rather than recursing.
func (l *lowering) drainInlines() error {
	for len(l.pending) > 0 {
		req := l.pending[0]
		l.pending = l.pending[1:]
		entry, err := l.inlineFinally(req.rng, req.cont, req.unwind, req.inFlight, req.regions, req.chain)
		if err != nil {
			return err
		}
		if req.from != nil {
			req.from.Term = fmt.Sprintf("jmp bb%d", entry)
			req.from.Succs = []int{entry}
		} else {
			l.fn.Regions[req.region].Finally = entry
		}
	}
	return nil
}

// insideFinally reports an opline inside some finally body.
func (l *lowering) insideFinally(at uint32) bool {
	for j := range l.oa.GetTryCatch() {
		if rng, ok := l.finallyRange(j); ok && at >= rng[0] && at < rng[1] {
			return true
		}
	}
	return false
}

// regionOfFinally is which region a finally range belongs to, or -1.
//
// The FAST_CALL path knows the range and not the region; `unwindPaths` knows
// both. A copy needs the region to say which finally it *is* -- see
// Block.OfRegion -- so the two paths have to agree, and this is the inverse of
// `finallyRange`.
func (l *lowering) regionOfFinally(rng [2]uint32) int {
	for i := range l.fn.Regions {
		if r, ok := l.finallyRange(i); ok && r == rng {
			return i
		}
	}
	return -1
}

// inlineFinally duplicates a finally body as fresh blocks and returns the copy's
// entry. cont is the block the copy returns to; it is ignored when unwind is
// set, because an exception has no continuation to return to.
//
// Duplication rather than an opline splice is what makes a body with its own
// control flow representable: each of the body's blocks gets a copy, and every
// jump that stays inside the body is remapped to the copy through blockAt.
func (l *lowering) inlineFinally(rng [2]uint32, cont int, unwind, inFlight bool, inherited map[int]int, within map[[2]uint32]bool) (int, error) {
	if l.depth++; l.depth > 8 {
		// A finally reaching itself would copy forever.
		return 0, l.bail("finally inlining did not converge")
	}
	defer func() { l.depth-- }()

	var starts []int
	for _, at := range l.leaders {
		if uint32(at) >= rng[0] && uint32(at) < rng[1] {
			starts = append(starts, at)
		}
	}
	if len(starts) == 0 {
		return 0, l.bail("finally range covers no block")
	}

	// **A try inside the body is copied with it.** Its region names the
	// original catch and finally, which are dead -- only copies run -- so
	// each copy gets a clone of every region whose try starts in the body,
	// with the catch remapped and its own unwind copy of the finally, and
	// the copy's blocks are placed in the clones (try_finally_023,
	// bug72213_2). What the copy queues carries the map, so a nested
	// finally copied later lands in the same clones.
	regions := map[int]int{}
	for k, v := range inherited {
		regions[k] = v
	}
	chain := map[[2]uint32]bool{}
	for k, v := range within {
		chain[k] = v
	}
	chain[rng] = unwind
	var nested []int
	for i, tc := range l.oa.GetTryCatch() {
		if tc.GetTryOp() >= rng[0] && tc.GetTryOp() < rng[1] {
			regions[i] = len(l.fn.Regions) + len(nested)
			nested = append(nested, i)
		}
	}
	mapRegion := func(r int) int {
		if to, ok := regions[r]; ok {
			return to
		}
		return r
	}
	mapRegions := func(rs []int) []int {
		out := make([]int, len(rs))
		for i, r := range rs {
			out[i] = mapRegion(r)
		}
		return out
	}

	// Allocate the copies first, contiguously and in order, so that a block's
	// fallthrough successor is its own ID plus one -- which is what the branch
	// terminators assume.
	remap := map[int]int{}
	for _, at := range starts {
		id := len(l.fn.Blocks)
		l.fn.Blocks = append(l.fn.Blocks, &Block{ID: id, Region: mapRegion(regionOf(l.oa, at)),
			Handler: true, Unwind: unwind || inFlight, InFinallyOf: mapRegions(l.finallysContaining(at))})
		remap[l.blockOf[at]] = id
	}
	// **The exception being unwound survives a catch inside the copy.**
	// There is one slot for the exception in flight, and a try in the body
	// that catches its own overwrites it -- then the copy's trailing rethrow
	// resumed with the caught one (bug72213_2: `caught 2` for `caught 1`).
	// The copy saves it on entry and the catch puts it back.
	//
	// **Saved in a place, not a value**: the restore sits in another block,
	// and a value crossing blocks is lost across a suspension -- a generator
	// yielding inside the copy's try came back with the saved slot empty and
	// the finally's rethrow threw nothing ("php: thrown").
	var inFlightExc *Value
	savedExc := ""
	for _, i := range nested {
		tc := l.oa.GetTryCatch()[i]
		r := Region{Catch: -1, Finally: -1, Parent: mapRegion(l.fn.Regions[i].Parent)}
		if tc.GetCatchOp() != 0 {
			to, ok := remap[l.blockOf[tc.GetCatchOp()]]
			if !ok {
				return 0, l.bail("catch of a try in a finally lies outside it")
			}
			r.Catch = to
		}
		l.fn.Regions = append(l.fn.Regions, r)
		if r.Catch >= 0 && (unwind || inFlight) {
			if inFlightExc == nil {
				v := l.fn.newValue("zval")
				inFlightExc = &v
				savedExc = fmt.Sprintf("$__excsave%d", v.N)
			}
			l.excSaved[regions[i]] = savedExc
		}
		if frng, ok := l.finallyRange(i); ok {
			l.pending = append(l.pending, inlineRequest{rng: frng, unwind: true, region: regions[i], regions: regions, chain: chain})
		}
	}

	saved := *l
	l.remap, l.cont, l.unwind = remap, cont, unwind
	l.regionMap = regions
	l.copyChain = chain
	l.inFlight = unwind || inFlight
	// A yield resumes into the block after it, and inside a copy that is the
	// copy's own next block. Starting clean keeps a suspension outside the body
	// from attaching its resume value to a block in here.
	l.resumePending = false
	l.currentLine = 0

	for k, at := range starts {
		end := int(rng[1]) - 1
		if k+1 < len(starts) {
			end = starts[k+1] - 1
		}
		l.b = remap[l.blockOf[at]]
		l.blk = l.fn.Blocks[l.b]
		l.start, l.end = at, end
		l.slotValue = map[uint32]Value{}
		l.skipNext = false
		if l.resumePending {
			l.blk.Params = append(l.blk.Params, fmt.Sprintf("%s: zval", l.resumed))
			l.slotValue[l.resumeSlot] = l.resumed
			if l.crossBlock[l.resumeSlot] {
				l.emit("local.set", []string{tempName(l.resumeSlot), l.resumed.String()})
			}
			l.resumePending = false
		}
		l.todo = nil
		for j := at; j <= end; j++ {
			l.todo = append(l.todo, j)
		}
		if err := l.drain(); err != nil {
			return 0, err
		}
		if l.blk.Term != "" {
			if k+1 == len(starts) {
				l.suspendingEnd(remap, cont, unwind)
			}
			continue
		}
		if k+1 < len(starts) {
			to := remap[l.blockOf[starts[k+1]]]
			l.term(fmt.Sprintf("jmp bb%d", to), to)
			continue
		}
		// Running off the end of the body is what FAST_RET meant. The engine
		// leaves finally_end short of that opline, so it is applied here: return
		// to this call site's continuation, or resume unwinding.
		if unwind {
			l.term("rethrow")
		} else {
			l.term(fmt.Sprintf("jmp bb%d", cont), cont)
		}
	}
	if l.resumePending {
		// A `yield` as the body's last statement would resume into the copy's
		// continuation, which is shared between copies -- so the resume value
		// would have to arrive at one block from several suspensions with
		// different numbering. Refused rather than guessed at.
		return 0, l.bail("finally ends in a yield")
	}

	if unwind {
		// **An unwind copy has no normal continuation.** Every way out of it
		// resumes unwinding, and the only exits are off its own end. A block
		// whose terminator leaves the copy is the FAST_RET's continuation
		// arriving through a nested finally: the inner copy returns to the
		// enclosing finally's own FAST_RET block, whose fallthrough is the
		// original's *normal* path. Left alone, `try { throw } finally { try {}
		// finally {} }` ran both finallys and then continued past the try as if
		// nothing had been thrown.
		//
		// PHP forbids `break`, `continue` and `goto` out of a finally, so a
		// jump leaving the copy can only be that continuation.
		//
		// The rewritten block takes **the copy's own region**. It is the jump
		// after a nested FAST_CALL, which sits inside the nested try's range;
		// left there, its rethrow was caught by that try's own finally, which
		// then ran a second time.
		inCopy := map[int]bool{}
		for _, id := range remap {
			inCopy[id] = true
		}
		outer := l.fn.Blocks[remap[l.blockOf[int(rng[0])]]].Region
		// **A resume target has work of its own even when it holds no
		// instruction**: the resume is received there, and what a
		// `$fiber->throw()` or a force-close raises at the suspension belongs
		// to the try around it. A bare `Fiber::suspend();` as a try's last
		// statement inside the finally resumes into an empty block.
		resumeTarget := map[int]bool{}
		for _, id := range remap {
			b := l.fn.Blocks[id]
			for _, p := range []string{"yield ", "yield_from ", "fiber.suspend", "call.suspending "} {
				if strings.HasPrefix(b.Term, p) && len(b.Succs) == 1 {
					resumeTarget[b.Succs[0]] = true
				}
			}
		}
		for _, id := range remap {
			blk := l.fn.Blocks[id]
			to, isJump := strings.CutPrefix(blk.Term, "jmp bb")
			if !isJump {
				continue
			}
			if n, convErr := strconv.Atoi(strings.TrimSpace(to)); convErr == nil && !inCopy[n] {
				if (len(blk.Insts) > 0 || resumeTarget[id]) && blk.Region != outer {
					// **Only the rethrow moves out.** A block with work of its
					// own keeps its region for that work: a `yield from` as a
					// try's last statement inside the finally resumes here, and
					// what the resume raises belongs to that try's catch
					// (generator_force_close).
					out := len(l.fn.Blocks)
					l.fn.Blocks = append(l.fn.Blocks, &Block{ID: out, Region: outer, Term: "rethrow",
						Unwind: blk.Unwind, InFinallyOf: blk.InFinallyOf})
					blk.Term, blk.Succs = fmt.Sprintf("jmp bb%d", out), []int{out}
					continue
				}
				blk.Term, blk.Succs = "rethrow", nil
				blk.Region = outer
			}
		}
	} else {
		// **The same continuation on a normal exit.** The jump after a nested
		// FAST_CALL lands on the enclosing finally's own FAST_RET position,
		// which is past the copied range, so it reached the original's normal
		// path instead of this copy's continuation: `try { return 5; } finally
		// { try {} finally {} }` ran both finallys and then fell off the end,
		// returning NULL (bug72188). PHP forbids `break`, `continue` and
		// `goto` out of a finally, so a jump leaving the copy can only be
		// that continuation.
		inCopy := map[int]bool{}
		for _, id := range remap {
			inCopy[id] = true
		}
		for _, id := range remap {
			blk := l.fn.Blocks[id]
			to, isJump := strings.CutPrefix(blk.Term, "jmp bb")
			if !isJump {
				continue
			}
			if n, convErr := strconv.Atoi(strings.TrimSpace(to)); convErr == nil && !inCopy[n] && n != cont {
				blk.Term, blk.Succs = fmt.Sprintf("jmp bb%d", cont), []int{cont}
			}
		}
	}

	entry := remap[l.blockOf[int(rng[0])]]
	if inFlightExc != nil {
		head := l.fn.Blocks[entry]
		head.Insts = append([]Inst{{Result: inFlightExc, Opcode: "exc.pending"},
			{Opcode: "local.set", Operands: []string{savedExc, inFlightExc.String()}}}, head.Insts...)
	}
	// **The accumulators must outlive the restore.** `*l = saved` puts back the
	// per-opline cursor -- the block being filled, the todo list, the slot map
	// -- and it also put back the *work queues*, discarding everything the copy
	// had just queued. A finally containing its own try then left
	// `<pending finally>` in the copy's terminator, because the request that
	// would have resolved it was thrown away; a finally containing a `catch` or
	// a `foreach` lost those the same way.
	resuming := l.resumePending
	pending, catches, fetches := l.pending, l.catches, l.fetches
	*l = saved
	l.resumePending = resuming
	l.pending, l.catches, l.fetches = pending, catches, fetches
	return entry, nil
}

// suspendingEnd gives a finally copy whose last statement suspends -- a
// `yield`, a Fiber::suspend -- the continuation it resumes into. That is the
// block after it, which for the copy's last block is past the copy: an edge
// into nothing, or into an unrelated block. The resumed block does what
// running off the end means, as inlineFinally applies it: back to this call
// site's continuation, or on with the unwinding (generators/finally/
// return_yield).
func (l *lowering) suspendingEnd(remap map[int]int, cont int, unwind bool) {
	next := l.b + 1
	if _, inside := func() (int, bool) {
		for _, id := range remap {
			if id == next {
				return id, true
			}
		}
		return 0, false
	}(); inside {
		return
	}
	suffix := fmt.Sprintf(" -> bb%d", next)
	if !strings.HasSuffix(l.blk.Term, suffix) || len(l.blk.Succs) != 1 || l.blk.Succs[0] != next {
		return
	}
	id := len(l.fn.Blocks)
	resumed := &Block{ID: id, Region: l.blk.Region, Handler: l.blk.Handler, Unwind: l.blk.Unwind,
		InFinallyOf: l.blk.InFinallyOf}
	if l.resumePending {
		resumed.Params = append(resumed.Params, fmt.Sprintf("%s: zval", l.resumed))
		if l.crossBlock[l.resumeSlot] {
			resumed.Insts = append(resumed.Insts, Inst{Opcode: "local.set",
				Operands: []string{tempName(l.resumeSlot), l.resumed.String()}})
		}
		l.resumePending = false
	}
	if unwind {
		resumed.Term = "rethrow"
	} else {
		resumed.Term, resumed.Succs = fmt.Sprintf("jmp bb%d", cont), []int{cont}
	}
	l.fn.Blocks = append(l.fn.Blocks, resumed)
	l.blk.Term = strings.TrimSuffix(l.blk.Term, suffix) + fmt.Sprintf(" -> bb%d", id)
	l.blk.Succs = []int{id}
}

// finallyRange is the opline span of a region's finally body, if it has one.
// finallysContaining is which regions' **finally bodies** an opline sits inside,
// innermost last. It is the fact the region model does not carry: `Region` says
// which try protects an opline, and this says which finallys it is part of.
//
// Positional, so a copied block gets the same answer as the body it came from
// and nothing has to be threaded through the inlining.
func (l *lowering) finallysContaining(at int) []int {
	var out []int
	for i := range l.fn.Regions {
		rng, ok := l.finallyRange(i)
		if !ok {
			continue
		}
		if uint32(at) >= rng[0] && uint32(at) < rng[1] {
			out = append(out, i)
		}
	}
	// Innermost last: a finally nested inside another has the larger start.
	sort.SliceStable(out, func(a, b int) bool {
		ra, _ := l.finallyRange(out[a])
		rb, _ := l.finallyRange(out[b])
		return ra[0] < rb[0]
	})
	return out
}

func (l *lowering) finallyRange(i int) ([2]uint32, bool) {
	tc := l.oa.GetTryCatch()
	if i >= len(tc) {
		return [2]uint32{}, false
	}
	start, stop := tc[i].GetFinallyOp(), tc[i].GetFinallyEnd()
	if start == 0 || stop <= start {
		return [2]uint32{}, false
	}
	return [2]uint32{start, stop}, true
}

// checkEdges refuses a body whose control flow names a block that does not
// exist. Several terminators use "the next block" as their fallthrough, and an
// opcode that ends the *last* block has no next one -- which produced an edge
// into nothing. Leaving it would hand every later pass a dangling successor;
// the CPS rewrite is simply the first thing that dereferenced it.
func (l *lowering) checkEdges() error {
	for _, b := range l.fn.Blocks {
		if b.Dead {
			continue
		}
		for _, s := range b.Succs {
			if s < 0 || s >= len(l.fn.Blocks) {
				return &Bail{Opcode: "?", Reason: "control flow leaves the function's blocks"}
			}
		}
	}
	return nil
}

// includeFlavours names ZEND_INCLUDE_OR_EVAL's flavours other than eval, by
// the engine's bits.
var includeFlavours = map[uint32]string{
	1 << 0: "eval",
	1 << 1: "include",
	1 << 2: "include_once",
	1 << 3: "require",
	1 << 4: "require_once",
}

// placeFor names the place an operand denotes: a compiled variable by name, a
// temporary by its slot. Empty when the operand is neither.
func (l *lowering) placeFor(kind, slot uint32) string {
	switch kind {
	case opCV:
		return "$" + cvName(l.oa, slot)
	case opTmpVar, opVar:
		return tempName(slot)
	}
	return ""
}

// catchBodies gives each `catch` clause its type test, and the bind a block of
// its own so it happens only on a match.
//
// The engine settles both halves. A clause that does not match **does not bind
// its variable** -- after `catch (ValueError $a)` fails to catch a TypeError,
// `isset($a)` is false -- so the test has to precede the assignment rather than
// follow it. And a subclass matches a parent clause, so the test is
// `instanceof` over the hierarchy rather than a name comparison.
func (l *lowering) catchBodies() error {
	reqs := l.catches
	l.catches = nil
	for _, req := range reqs {
		// On a match: bind, then fall into the handler body the clause already
		// had. The body is whatever the catch block fell through to.
		body := req.from.Succs
		if len(body) == 0 && req.from.Term == "rethrow" {
			// **A catch that ends an unwinding finally copy**: its handler
			// falls off the end of the finally, which on this path resumes
			// the unwinding the finally interrupted -- `try { throw } finally
			// { try { ... } catch (E $e) {} }` (bug72213_2). The body is that
			// rethrow, in a block of its own the bind can jump to.
			out := len(l.fn.Blocks)
			l.fn.Blocks = append(l.fn.Blocks, &Block{ID: out, Region: req.from.Region, Term: "rethrow",
				Handler: req.from.Handler, Unwind: req.from.Unwind, InFinallyOf: req.from.InFinallyOf})
			body = []int{out}
		}
		if len(body) != 1 {
			return l.bail("catch does not fall through to one block")
		}
		id := len(l.fn.Blocks)
		bind := &Block{ID: id, Region: req.from.Region, InFinallyOf: req.from.InFinallyOf}
		l.fn.Blocks = append(l.fn.Blocks, bind)
		if req.bind != "" {
			bind.Insts = append(bind.Insts,
				Inst{Opcode: "local.set", Operands: []string{req.bind, req.exc}, Attrs: []string{StrictAssignAttr}})
		} else {
			// **`catch (E)` with no variable releases the exception** there:
			// ZEND_CATCH's OBJ_RELEASE, so its destructor runs at the catch
			// and what it throws replaces the handler (try/catch_novar_2).
			// The emitter decides whether the release may happen.
			bind.Insts = append(bind.Insts, Inst{Opcode: ExcRelease, Operands: []string{req.exc}})
		}
		if r := l.regionCaughtBy(req.from.ID); r >= 0 {
			if v, ok := l.excSaved[r]; ok {
				bind.Insts = append(bind.Insts, Inst{Opcode: "exc.restore", Operands: []string{v}})
			}
		}
		bind.Term = fmt.Sprintf("jmp bb%d", body[0])
		bind.Succs = []int{body[0]}

		// On no match: the next clause, or -- for the last one -- out of the
		// region. `Region.Finally` is the *exceptional* copy, which already ends
		// in `rethrow`, so an unhandled exception runs the finally on its way
		// out exactly as the engine does.
		next := req.next
		if next < 0 {
			// **The region whose catch this is**, not the region this block is
			// in. A catch handler sits *outside* its own try -- an exception
			// thrown inside it is not caught by the same clause -- so
			// `req.from.Region` is the enclosing region or -1, and asking it
			// found no finally for the common `try/catch/finally`. The result
			// was that an exception no clause matched left the function
			// **without running the finally at all**.
			r := req.from.Region
			if own := l.regionCaughtBy(req.from.ID); own >= 0 {
				r = own
			}
			if r >= 0 && l.fn.Regions[r].Finally >= 0 {
				next = l.fn.Regions[r].Finally
				// **Resolved again at the end.** `unwindPaths` replaces
				// `Regions[r].Finally` with the exceptional *copy*, and it runs
				// after this pass -- so the id read here is the pre-copy one
				// and points at a block that will not be emitted. Recording the
				// region lets the terminator be rewritten once the copy exists.
				l.catchUnwinds = append(l.catchUnwinds, catchUnwind{from: req.from, region: r})
			} else {
				// **In the catch's own region**, so the rethrow routes from
				// its parent: an exception no clause matched goes on to an
				// enclosing try's catch or finally, as the engine's does. It
				// was region -1, which left the function from inside
				// `try { try {..} catch (E $e) {} } finally {..}` without
				// running the finally -- the one a destroyed fiber or
				// generator unwinds through (fibers/unfinished-fiber-with-
				// nested-try-catch).
				out := len(l.fn.Blocks)
				l.fn.Blocks = append(l.fn.Blocks, &Block{ID: out, Region: r, Term: "rethrow",
					InFinallyOf: req.from.InFinallyOf})
				next = out
			}
		}

		m := l.fn.newValue("bool")
		req.from.Insts = append(req.from.Insts,
			Inst{Result: &m, Opcode: "instanceof", Operands: []string{req.exc, req.class}})
		req.from.Term = fmt.Sprintf("br %s, bb%d, bb%d", m, id, next)
		req.from.Succs = []int{id, next}
	}
	return nil
}

// fetchBodies gives each foreach its own block for the fetch, reached only when
// the iterator still has an element.
func (l *lowering) fetchBodies() error {
	reqs := l.fetches
	l.fetches = nil
	for _, req := range reqs {
		id := len(l.fn.Blocks)
		blk := &Block{ID: id, Region: req.from.Region, InFinallyOf: req.from.InFinallyOf}
		l.fn.Blocks = append(l.fn.Blocks, blk)

		v := l.fn.newValue("zval")
		set := "local.set"
		current := "iter.current"
		if req.byRef {
			// `iter.current.ref` answers the element's **box**, and the loop
			// variable is bound to it. Over anything that is not an array --
			// an object, a generator -- there is no element to point at, and
			// the intrinsic falls back to the value so the loop still runs.
			// `bind_ref.loop`, not `bind_ref`: the `Only variables should be
			// assigned by reference` notice belongs to `=&` written in the
			// source, and a loop over a Traversable -- which has no element to
			// point at and is iterated by value -- is not that.
			current, set = "iter.current.ref", "bind_ref.loop"
		}
		blk.Insts = append(blk.Insts,
			Inst{Result: &v, Opcode: current, Operands: []string{req.it}},
			Inst{Opcode: set, Operands: []string{req.value, v.String()}})
		if req.key != "" {
			kv := l.fn.newValue("zval")
			blk.Insts = append(blk.Insts,
				Inst{Result: &kv, Opcode: "iter.key", Operands: []string{req.it}},
				Inst{Opcode: "local.set", Operands: []string{req.key, kv.String()}})
		}
		blk.Term = fmt.Sprintf("jmp bb%d", req.fall)
		blk.Succs = []int{req.fall}

		// The step goes on the **back edges**, not here.
		//
		// The engine's order, taken from an Iterator whose methods echo, is
		// `current, key, [body], next, valid`. Stepping in the fetch block puts
		// `next()` before the body, which is invisible for an array -- the
		// element was already read out of a snapshot -- and plainly wrong for a
		// Traversable, whose `next()` is user code that prints. It also made
		// `break` advance an iterator the engine leaves alone.
		//
		// The step **returns** the advanced state, and it is written back, so
		// the loop carries the cursor as a value rather than each step reaching
		// into the same hidden one. Promotion then threads it through the
		// header's block parameter, which is where an iteration's state
		// belongs.
		step := &Block{ID: len(l.fn.Blocks), Region: req.from.Region, InFinallyOf: req.from.InFinallyOf}
		l.fn.Blocks = append(l.fn.Blocks, step)
		stepped := l.fn.newValue("zval")
		step.Insts = []Inst{
			{Result: &stepped, Opcode: "iter.next", Operands: []string{req.it}},
			{Opcode: "local.set", Operands: []string{req.it, stepped.String()}},
		}
		step.Term = fmt.Sprintf("jmp bb%d", req.from.ID)
		step.Succs = []int{req.from.ID}

		// Every edge back into the header steps, and the loop's own entry does
		// not. `continue` is a back edge and *does* advance, which is why this
		// is about edges rather than about the end of the body.
		for _, b := range l.fn.Blocks {
			if b == step || b == req.from || b == req.reset {
				continue
			}
			retargetTerm(b, req.from.ID, step.ID)
		}

		// The test is a **call**, computed at the end of the block the branch
		// leaves from. It was fused into the terminator -- `br iter.valid $it`
		// -- which made the one instruction in the loop protocol that is not a
		// call look like syntax.
		valid := l.fn.newValue("bool")
		req.from.Insts = append(req.from.Insts,
			Inst{Result: &valid, Opcode: "iter.valid", Operands: []string{req.it}})
		req.from.Term = fmt.Sprintf("br %s, bb%d, bb%d", valid, id, req.exit)
		req.from.Succs = []int{id, req.exit}
	}
	return nil
}

// retargetTerm redirects every mention of one block in a terminator to another,
// keeping Succs in step. Matching is on the whole token, so `bb1` never matches
// inside `bb10`.
func retargetTerm(b *Block, from, to int) {
	if b.Term == "" {
		return
	}
	replaced := blockRef.ReplaceAllStringFunc(b.Term, func(tok string) string {
		if tok != fmt.Sprintf("bb%d", from) {
			return tok
		}
		return fmt.Sprintf("bb%d", to)
	})
	if replaced == b.Term {
		return
	}
	b.Term = replaced
	for i, s := range b.Succs {
		if s == from {
			b.Succs[i] = to
		}
	}
}

var blockRef = regexp.MustCompile(`\bbb\d+\b`)

// blockAt maps an opline index -- a jump target, a catch or finally entry -- to
// the block that starts there, clamped because the engine uses a one-past-the-
// end index to mean "no target".
func (l *lowering) blockAt(opline uint32) int {
	i := int(opline)
	if i > len(l.oplines)-1 {
		i = len(l.oplines) - 1
	}
	b := l.blockOf[i]
	if l.remap != nil {
		// Inside a copy of a finally body, a jump that stays within the body
		// must land in *this* copy rather than in the original blocks, which are
		// dead. A jump leaving the body is not remapped and keeps its target.
		if to, ok := l.remap[b]; ok {
			return to
		}
	}
	return b
}

// operand renders a read of an operand as an IR operand string. The bool is
// false when a TMP/VAR slot is not live here, which is a bail rather than a
// silent hole.
func (l *lowering) operand(kind, idx uint32) (string, bool) {
	switch kind {
	case opConst:
		return l.constText(idx), true
	case opCV:
		return PlaceOperand(cvName(l.oa, idx)), true
	case opTmpVar, opVar:
		if v, live := l.slotValue[idx]; live {
			return v.String(), true
		}
		if l.crossBlock[idx] {
			return tempName(idx), true
		}
		return "", false
	case opUnused:
		return "", true
	}
	return "", false
}

// constText is a CONST operand's text, with one escape hatch.
//
// Most literals spell themselves -- `5`, `"x"`, `[1, 2]` -- and a consumer
// reads the value straight back out of the operand. A **deferred constant
// expression** does not: the engine kept it as an AST because it names
// something that may not exist yet, and there is no text that is the value.
// `LiteralText` says so by answering `<lit>`, and every consumer then had a
// hole with nothing behind it.
//
// So the literal is carried on the function and the operand indexes it. The
// spelling stays a `<...>` so nothing mistakes it for a value, and the index
// makes it reachable: `internal/gogen`'s ConstExpr already renders such a tree,
// and lacked only a way to get at it from an instruction.
func (l *lowering) constText(idx uint32) string {
	// **A numeric-string offset is spelled as written.** The compiler folds
	// `$o["0"]` to the int 0 and keeps "0" in the next literal, flagged, for
	// an ArrayAccess object -- which is handed the string, not the int. An
	// array normalises either spelling to the same key.
	if lits := l.oa.GetLiterals(); int(idx)+1 < len(lits) && lits[idx].GetExtraValue() &&
		lits[idx+1].GetKind() == pb.Literal_KIND_STRING {
		return literalText(l.oa, idx+1)
	}
	text := literalText(l.oa, idx)
	if text != "<lit>" {
		return text
	}
	if l.litSlot == nil {
		l.litSlot = map[uint32]int{}
	}
	if n, seen := l.litSlot[idx]; seen {
		return fmt.Sprintf("<lit:%d>", n)
	}
	n := len(l.fn.Literals)
	l.fn.Literals = append(l.fn.Literals, l.oa.GetLiterals()[idx])
	l.litSlot[idx] = n
	return fmt.Sprintf("<lit:%d>", n)
}

func (l *lowering) define(v Value, opcode string, operands []string, attrs ...string) {
	l.blk.Insts = append(l.blk.Insts, Inst{Result: &v, Opcode: opcode, Operands: operands, Attrs: attrs})
}

func (l *lowering) emit(opcode string, operands []string, attrs ...string) {
	l.blk.Insts = append(l.blk.Insts, Inst{Opcode: opcode, Operands: operands, Attrs: attrs})
}

// hold makes an operand survive to wherever its call completes. A value is only
// meaningful inside the block defining it, so a call that finishes elsewhere has
// to keep its receiver and arguments in places instead.
func (l *lowering) hold(operand string, spans bool) string {
	if !spans || !strings.HasPrefix(operand, "%") {
		return operand
	}
	place := fmt.Sprintf("$__c%d", l.spill)
	l.spill++
	// **Bound, not assigned**: the slot is a temporary standing in for the
	// value, so a reference the value is -- a by-reference call result on
	// its way to a by-reference parameter -- stays one. Assigning wrote
	// through to the referenced value, and an unset global then reached
	// the callee as "not passed".
	l.emit("local.set", []string{place, operand}, SeparateAttr)
	return place
}

// term sets the block's terminator and records its successors structurally.
// The successor list has to be built here rather than parsed back out of the
// text: every pass over this IR needs the control-flow graph, and a terminator
// that exists only as a string is not a graph. Blocks that leave the function --
// `ret`, `throw` -- have none. Exception edges are not successors either; they
// are region membership, per IR-DESIGN.md.
func (l *lowering) term(text string, succs ...int) {
	l.blk.Term = text
	l.blk.Succs = succs
}

// emitLine records the current source position, replacing the previous
// directive when no instruction came between them. That happens whenever an
// opline ends its block before emitting anything -- a RETURN inside a try, say,
// whose block ends at the FAST_CALL that splices the finally in -- and a
// position with no instruction under it is noise in a format built to diff.
func (l *lowering) emitLine() {
	if n := len(l.blk.Insts); n > 0 && l.blk.Insts[n-1].Directive() {
		l.blk.Insts = l.blk.Insts[:n-1]
	}
	l.emit(lineDirective, []string{fmt.Sprint(l.currentLine)})
}

// keep records a definition and, when the slot is read from another block,
// mirrors it into its place.
func (l *lowering) keep(slot uint32, v Value) {
	l.slotValue[slot] = v
	if l.crossBlock[slot] {
		l.emit("local.set", []string{tempName(slot), v.String()})
	}
}

// assignTo writes a produced value to the destination an operand names. Which
// kind it is has to be asked rather than assumed: `foreach ($a as $k => $v)`
// puts the value in a CV but the key in a temporary, and treating that
// temporary as a CV both invents a variable name from the slot number and
// leaves the place the next block reads from unwritten.
func (l *lowering) assignTo(kind, slot uint32, v Value) {
	switch kind {
	case opCV:
		l.emit("local.set", []string{"$" + cvName(l.oa, slot), v.String()})
	case opTmpVar, opVar:
		l.keep(slot, v)
	}
}

// definesValue reports whether the result slot is a temporary that something
// later reads, as opposed to the opcode being evaluated for its effect alone.
func (l *lowering) definesValue() bool {
	k := l.op.GetResultType()
	return k == opTmpVar || k == opVar
}

func (l *lowering) bail(why string) error {
	if w := l.cfg.Trace; w != nil {
		fmt.Fprintf(w, "BAIL %s %q lines %d-%d at opline %d (%s) op1=%d:%d\n",
			l.oa.GetFilename(), l.oa.GetName(), l.oa.GetLineStart(), l.oa.GetLineEnd(),
			l.i, l.n, l.op.GetOp1Type(), l.op.GetOp1())
		fmt.Fprintf(w, "  leaders=%v crossBlock=%v block=%d..%d\n",
			l.leaders, l.crossBlock, l.start, l.end)
		for j, o := range l.oplines {
			fmt.Fprintf(w, "  %3d %-28s op1=%d:%-6d op2=%d:%-6d res=%d:%d\n",
				j, l.name(j), o.GetOp1Type(), o.GetOp1(),
				o.GetOp2Type(), o.GetOp2(), o.GetResultType(), o.GetResult())
		}
	}
	return &Bail{Opcode: l.n, Reason: why}
}

const notLive = "operand not live in this block"

// opline lowers the current opline. It returns a [*Bail] for anything the pass
// does not pb.
func (l *lowering) opline() error {
	op, n, oa := l.op, l.n, l.oa

	switch {
	case n == "ZEND_FAST_CALL":
		// A normal exit through a finally. The body is duplicated as real
		// blocks -- an earlier version spliced its *oplines* into this block,
		// which cannot represent a body that branches and silently ran both
		// arms of an `if` -- and this block simply jumps to the copy. The copy
		// returns to whatever followed the FAST_CALL, which is a block leader
		// because FAST_CALL ends one.
		rng, known := l.finallyAt[op.GetOp1()]
		// An **empty** finally has nothing to copy: the call returns at once,
		// so it is a jump to what follows (jump15, bug72213_2).
		if !known && l.emptyFinally[op.GetOp1()] {
			cont := l.blockAt(uint32(l.i + 1))
			l.term(fmt.Sprintf("jmp bb%d", cont), cont)
			break
		}
		if !known {
			return l.bail("FAST_CALL with no finally range")
		}
		// Inside an unwind copy the exception is **still in flight** while
		// this nested finally runs: `finally { try { return 1; } finally {
		// throw new E; } }` makes the unwinding exception E's previous. The
		// copy is made later, after this one's state is gone, so the request
		// carries the fact.
		l.pending = append(l.pending, inlineRequest{
			rng: rng, cont: l.blockAt(uint32(l.i + 1)), from: l.blk,
			region: l.regionOfFinally(rng), inFlight: l.inFlight, regions: l.regionMap, chain: l.copyChain})
		l.blk.Term = pendingInline

	case n == "ZEND_FAST_RET":
		// Never reached: finally_end stops short of it, so it falls outside every
		// copied range, and the original block holding it is dead. Its meaning --
		// return to the call site, or resume unwinding -- is applied by
		// inlineFinally when a copy runs off its end.

	// ---- the call protocol, which is the point of the pass ----
	case n == "ZEND_INIT_FCALL" || n == "ZEND_INIT_FCALL_BY_NAME":
		callee, _ := l.operand(op.GetOp2Type(), op.GetOp2())
		l.calls = append(l.calls, &pendingCall{
			opcode: "call.fn", callee: callee, spans: l.spanningCall[l.i]})

	case n == "ZEND_INIT_NS_FCALL_BY_NAME":
		// An unqualified call inside a namespace names *two* functions, and PHP
		// resolves whichever exists: `in_array()` in namespace App means
		// `App\in_array` if that is defined and the global `in_array`
		// otherwise. The compiler stores all three forms in consecutive
		// literals -- as written, lowercased-and-namespaced, then the
		// unqualified lowercase fallback -- and op2 names only the first.
		// Verified across namespace depth and casing; a fully qualified call
		// compiles to INIT_FCALL_BY_NAME instead and never reaches here.
		callee, _ := l.operand(op.GetOp2Type(), op.GetOp2())
		fallback, ok := l.literalAt(op.GetOp2() + 2)
		if !ok {
			return l.bail("namespaced call has no fallback literal")
		}
		l.calls = append(l.calls, &pendingCall{
			opcode: "call.fn", callee: callee, fallback: fallback,
			spans: l.spanningCall[l.i]})

	case n == "ZEND_INIT_METHOD_CALL":
		recv, ok := l.operand(op.GetOp1Type(), op.GetOp1())
		if !ok {
			return l.bail("receiver not live in this block")
		}
		if op.GetOp1Type() == opUnused {
			recv = "$this"
		}
		callee, _ := l.operand(op.GetOp2Type(), op.GetOp2())
		spans := l.spanningCall[l.i]
		l.calls = append(l.calls, &pendingCall{opcode: "call.method", callee: callee,
			receiver: l.hold(recv, spans), spans: spans})

	case n == "ZEND_INIT_PARENT_PROPERTY_HOOK_CALL":
		// `parent::$x::get()` inside a property hook. op1 is the property name
		// and **op2 is the hook kind**, carried as an unused operand holding 0
		// for `get` and 1 for `set` -- read back by harvesting one class with
		// both hooks and comparing, since an unused operand's value is not
		// otherwise meaningful.
		name, okn := l.operand(op.GetOp1Type(), op.GetOp1())
		if !okn {
			return l.bail("parent hook name not live in this block")
		}
		kind := "get"
		if op.GetOp2() == 1 {
			kind = "set"
		}
		l.calls = append(l.calls, &pendingCall{opcode: "call.parent_hook." + kind,
			callee: name, spans: l.spanningCall[l.i]})

	case n == "ZEND_INIT_STATIC_METHOD_CALL":
		cls, okc := l.fetchClass(op.GetOp1Type(), op.GetOp1())
		if !okc {
			return l.bail("static call on an unrecognised class reference")
		}
		callee, _ := l.operand(op.GetOp2Type(), op.GetOp2())
		if op.GetOp2Type() == opUnused {
			// **An unused op2 means `__construct`.** The compiler resolves a
			// constructor call at compile time and leaves a cache address in
			// the operand rather than a name, so `l.operand` answers "" and the
			// call reached the emitter with no method at all. Measured over the
			// four spellings, all of which arrive this way:
			//
			//	parent::__construct()   UNUSED:514  UNUSED:<addr>
			//	self::__construct()     UNUSED:513  UNUSED:<addr>
			//	A::__construct()        CONST:0     UNUSED:<addr>
			//	static::__construct()   UNUSED:515  UNUSED:<addr>
			//
			// and the address differs run to run, so it is the *unusedness*
			// that carries the meaning and nothing else does.
			callee = `"__construct"`
		}
		pc := &pendingCall{opcode: "call.static", callee: callee, receiver: cls,
			spans: l.spanningCall[l.i]}
		// Fiber's static entry points become intrinsics rather than calls. The
		// runtime must not be suspension-aware: if suspension stays an opaque
		// call, the only way to implement it is real coroutines underneath,
		// whereas an explicit suspension point lets the compiler own it -- the
		// same treatment `yield` already gets.
		if strings.EqualFold(literalString(oa, op.GetOp1Type(), op.GetOp1()), "Fiber") {
			switch strings.ToLower(literalString(oa, op.GetOp2Type(), op.GetOp2())) {
			case "suspend":
				pc.opcode, pc.callee, pc.receiver = "fiber.suspend", "", ""
			case "getcurrent":
				pc.opcode, pc.callee, pc.receiver = "fiber.current", "", ""
			}
		}
		l.calls = append(l.calls, pc)

	case n == "ZEND_INIT_DYNAMIC_CALL" || n == "ZEND_INIT_USER_CALL":
		target, ok := l.operand(op.GetOp2Type(), op.GetOp2())
		if !ok {
			return l.bail("callable not live in this block")
		}
		// **`call_user_func` checks its callback before the arguments.** The
		// compiler turns the call into INIT_USER_CALL, so the argument check
		// the library function would make is the opcode's own: an uncallable
		// value is `call_user_func(): Argument #1 ($callback) must be a valid
		// callback, ...`, not whatever the call itself would have said. The
		// checked value is the one called, as the engine copies it here.
		if n == "ZEND_INIT_USER_CALL" {
			fname, okf := l.operand(op.GetOp1Type(), op.GetOp1())
			if !okf {
				return l.bail("user call with a computed function name")
			}
			v := l.fn.newValue("zval")
			l.define(v, "callable.check", []string{target, fname})
			target = v.String()
		}
		spans := l.spanningCall[l.i]
		// A `call_user_func` is marked `user_send` whether or not it sends
		// anything: with no arguments no SEND_USER says so, and the emitter
		// needs the mark to tell it from `$f()`, which refuses a non-static
		// method named through its class where call_user_func runs it.
		l.calls = append(l.calls, &pendingCall{opcode: "call.dynamic",
			receiver: l.hold(target, spans), spans: spans, userSend: n == "ZEND_INIT_USER_CALL"})

	case n == "ZEND_NEW":
		// NEW pushes a call too: the constructor runs through the same
		// three-phase protocol and a DO_FCALL later pops it. Missing that is
		// what once misaligned the whole pending-call stack.
		// **`fetchClass`, not `operand`.** `new self` has an unused op1 whose
		// value carries the class *type* in its low bits, so reading it as an
		// operand gave the empty string -- `new ` in the IR, which the emitter
		// reported as `operand  is not a literal` and which reads like a
		// mangled literal rather than a dropped class reference.
		cls, okc := l.fetchClass(op.GetOp1Type(), op.GetOp1())
		if !okc {
			return l.bail("new of an unrecognised class reference")
		}
		v := l.fn.newValue("*obj")
		l.define(v, "new", []string{cls})
		l.keep(op.GetResult(), v)
		spans := l.spanningCall[l.i]
		l.calls = append(l.calls, &pendingCall{opcode: "ctor",
			receiver: l.hold(v.String(), spans), spans: spans})

	case strings.HasPrefix(n, "ZEND_SEND_"):
		if len(l.calls) == 0 {
			return l.bail("send with no pending call")
		}
		mode := sendMode(n)
		if mode == "" {
			return l.bail("unmodelled send mode")
		}
		arg, ok := l.operand(op.GetOp1Type(), op.GetOp1())
		if !ok {
			return l.bail("argument not live in this block")
		}
		if mode == "preferref" || mode == "byref" {
			if dst, isRef := l.refResult[arg]; isRef {
				arg = dst
			}
		}
		// **`call_user_func_array($f, array_slice($a, N, $len))` is one
		// opcode.** The compiler folds the slice into SEND_ARRAY -- the offset
		// in extended_value, the length in op2 -- and ignoring both sent the
		// whole array. The folded form sends the slice's values positionally,
		// whatever their keys (measured: `["a" => 1, "b" => 2, 3]` reaches a
		// variadic as `[1, 2, 3]`), and refuses a bad length with
		// array_slice's own words, so it is the library's slice, then values.
		if n == "ZEND_SEND_ARRAY" && op.GetOp2Type() != opUnused {
			length, okl := l.operand(op.GetOp2Type(), op.GetOp2())
			if !okl {
				return l.bail(notLive)
			}
			sliced := l.fn.newValue("zval")
			l.blk.Insts = append(l.blk.Insts, Inst{Result: &sliced, Opcode: "call.fn",
				Operands: []string{`"array_slice"`}, ArgList: fmt.Sprintf("(byval %s, byval %d, byval %s)", arg, op.GetExtendedValue(), length)})
			values := l.fn.newValue("zval")
			l.blk.Insts = append(l.blk.Insts, Inst{Result: &values, Opcode: "call.fn",
				Operands: []string{`"array_values"`}, ArgList: fmt.Sprintf("(byval %s)", sliced)})
			arg = values.String()
		}
		arg = l.hold(arg, l.calls[len(l.calls)-1].spans)
		// op2 is the argument position, or a name for a named argument --
		// except on SEND_ARRAY, where it is the folded slice's length.
		if op.GetOp2Type() == opConst && n != "ZEND_SEND_ARRAY" {
			arg = fmt.Sprintf("%s %s as %s", mode, arg, literalText(oa, op.GetOp2()))
		} else {
			arg = mode + " " + arg
		}
		top := l.calls[len(l.calls)-1]
		top.args = append(top.args, arg)
		l.namedFuncArg = false
		// **A value sent where the callee wants a reference is an Error at
		// the send.** The compiler emits SEND_VAL_EX for `foo(1)` when `foo`
		// takes `&$var`, and the opcode throws `foo(): Argument #1 ($var)
		// could not be passed by reference` before any later argument is
		// evaluated. Settled here for a function the unit declares; an
		// unresolved callee keeps binding the value silently.
		if n == "ZEND_SEND_VAL_EX" && op.GetOp2Type() != opConst && l.cfg.ByRefArg != nil &&
			top.opcode == "call.fn" && top.callee != "" {
			// An unqualified call in a namespace, `similar_text(...)` in
			// `namespace Foo`, is the namespaced function if the unit
			// declares one and the global otherwise -- the compiler could not
			// tell, so it sent SEND_VAL_EX and the engine refuses at run time
			// under the name that resolved (bug78154).
			resolved := top.callee
			if top.fallback != "" {
				name, err := strconv.Unquote(top.callee)
				if err != nil || l.cfg.DeclaresFunction == nil {
					resolved = ""
				} else if !l.cfg.DeclaresFunction(name) {
					resolved = top.fallback // already literal text, quoted
				}
			}
			if callee, err := strconv.Unquote(resolved); err == nil && callee != "" && l.cfg.ByRefArg(callee, len(top.args)-1) {
				l.emit("cannot_pass_by_ref", []string{resolved, strconv.Quote(strconv.Itoa(len(top.args) - 1))})
			}
		}
		// And for a callee only known at run time, `$f(1)`: the call asks
		// the callee it is about to call (nullsafe_operator/016). Carried on
		// the call rather than as an instruction of its own at the send: an
		// extra use of the callee's variable kept it from being forwarded,
		// and its previous value's release from being tracked.
		if n == "ZEND_SEND_VAL_EX" && op.GetOp2Type() != opConst && top.opcode == "call.dynamic" && !top.userSend {
			top.refuseByRef = append(top.refuseByRef, strconv.Itoa(len(top.args)-1))
		}
		// The same for a static call to a method of a class the unit
		// declares, named literally: `Test::usesRef(match (...) {...})`.
		if n == "ZEND_SEND_VAL_EX" && op.GetOp2Type() != opConst && l.cfg.ByRefArg != nil &&
			top.opcode == "call.static" && top.callee != "" {
			class, errc := strconv.Unquote(top.receiver)
			method, errm := strconv.Unquote(top.callee)
			if errc == nil && errm == nil && class != "" {
				if l.cfg.ByRefArg(class+"::"+method, len(top.args)-1) {
					l.emit("cannot_pass_by_ref", []string{strconv.Quote(class + "::" + method), strconv.Quote(strconv.Itoa(len(top.args) - 1))})
				}
			}
		}
		// SEND_ARRAY is `call_user_func_array`'s: an element that is not a
		// reference reaches a by-reference parameter with the same warning.
		// SEND_UNPACK, `f(...$a)`, makes each such element a reference.
		if n == "ZEND_SEND_USER" || n == "ZEND_SEND_ARRAY" {
			top.userSend = true
		}
		if sent := strings.TrimPrefix(arg, "byval "); mode == "byval" && op.GetOp2Type() != opConst &&
			strings.HasPrefix(sent, "$") && sent != "$this" && !strings.HasPrefix(sent, "$__") {
			if top.sentAt == nil {
				top.sentAt = map[int][2]int{}
			}
			top.sentAt[len(top.args)-1] = [2]int{l.b, len(l.blk.Insts)}
		}
		if strings.HasPrefix(n, "ZEND_SEND_VAR_NO_REF") && op.GetOp2Type() != opConst {
			if top.noRef == nil {
				top.noRef = map[int]bool{}
			}
			top.noRef[len(top.args)-1] = n == "ZEND_SEND_VAR_NO_REF_EX"
		}

	case n == "ZEND_CHECK_UNDEF_ARGS":
		if len(l.calls) == 0 {
			return l.bail("undef-args check with no pending call")
		}
		l.calls[len(l.calls)-1].checked = true

	case strings.HasPrefix(n, "ZEND_DO_"):
		if len(l.calls) == 0 {
			return l.bail("call with no pending init")
		}
		call := l.calls[len(l.calls)-1]
		l.calls = l.calls[:len(l.calls)-1]
		l.settleSends(call)
		l.snapshotSends(call)
		l.noRefSends(call)
		operands := []string{}
		if call.receiver != "" {
			operands = append(operands, call.receiver)
		}
		if call.callee != "" {
			operands = append(operands, call.callee)
		}
		rendered := fmt.Sprintf("(%s)", strings.Join(call.args, ", "))
		var callAttrs []string
		if call.fallback != "" {
			callAttrs = append(callAttrs, "fallback="+call.fallback)
		}
		if call.checked {
			callAttrs = append(callAttrs, "check_undef_args")
		}
		if call.userSend {
			callAttrs = append(callAttrs, UserSendAttr)
		}
		if len(call.refuseByRef) > 0 {
			callAttrs = append(callAttrs, "refuse_byref="+strings.Join(call.refuseByRef, ":"))
		}
		if call.opcode == "ctor" {
			l.blk.Insts = append(l.blk.Insts, Inst{Opcode: "call.ctor", Operands: operands, ArgList: rendered, Attrs: callAttrs})
			return nil
		}
		if call.opcode == "fiber.suspend" {
			// A terminator, exactly as `yield` is: the value handed to resume()
			// arrives on the edge, so the continuation is a block of its own and
			// a later CPS rewrite has something to reify.
			l.resumed = Value{N: l.fn.next, Type: "zval"}
			l.fn.next++
			l.resumePending = l.definesValue()
			l.resumeSlot = op.GetResult()
			l.term(fmt.Sprintf("fiber.suspend%s -> bb%d", rendered, l.b+1), l.b+1)
			return nil
		}
		v := l.fn.newValue("zval")
		l.blk.Insts = append(l.blk.Insts, Inst{Result: &v, Opcode: call.opcode, Operands: operands, ArgList: rendered, Attrs: callAttrs})
		if l.definesValue() {
			l.keep(op.GetResult(), v)
		}

	// ---- everything else, kept deliberately thin ----
	case n == "ZEND_RECV" || n == "ZEND_RECV_INIT":
		// RECV_INIT carries the parameter's **default** in op2, and dropping it
		// was a wrong answer rather than a gap: `function f(int $x = 5)` called
		// as `f()` left the slot empty, and the declared-type coercion then
		// threw a TypeError where the engine prints `int(5)`.
		//
		// Settled with the engine rather than from the handler: a default is
		// used **uncoerced**. `function f(int $x = null)` prints NULL, so the
		// default cannot go through the same path a passed argument does.
		operands := []string{fmt.Sprint(op.GetOp1() - 1)}
		if n == "ZEND_RECV_INIT" {
			d, live := l.operand(op.GetOp2Type(), op.GetOp2())
			if !live || d == "" || d == "<lit>" || d == "?" {
				// A default the harvest could not render is a deferred constant
				// expression -- `self::N`, an enum case -- which this pass does
				// not evaluate. Refusing names the gap; emitting the parameter
				// without its default would print the wrong number.
				return l.bail("parameter default that is not a literal")
			}
			operands = append(operands, d)
		}
		v := l.fn.newValue("zval")
		l.define(v, "recv", operands)
		l.assignTo(op.GetResultType(), op.GetResult(), v)

	case n == "ZEND_ECHO":
		a, ok := l.operand(op.GetOp1Type(), op.GetOp1())
		if !ok {
			return l.bail(notLive)
		}
		l.emit("echo", []string{a})

	case n == "ZEND_ASSIGN":
		src, ok := l.operand(op.GetOp2Type(), op.GetOp2())
		if !ok {
			return l.bail(notLive)
		}
		dst, _ := l.operand(op.GetOp1Type(), op.GetOp1())
		l.emit("local.set", []string{dst, src})
		if l.definesValue() {
			v := l.fn.newValue("zval")
			l.define(v, "local.get", []string{dst})
			l.keep(op.GetResult(), v)
		}

	case binaryOps[n]:
		a, ok1 := l.operand(op.GetOp1Type(), op.GetOp1())
		b2, ok2 := l.operand(op.GetOp2Type(), op.GetOp2())
		// `$x instanceof self` leaves op2 unused and carries the keyword in
		// op2's number, flags and all -- `op2=0:897` for self, read with
		// `php2lower -trace` -- the way a class fetch does
		// (self_instanceof_outside_class).
		if n == "ZEND_INSTANCEOF" && op.GetOp2Type() == opUnused {
			b2, ok2 = l.fetchClass(opUnused, op.GetOp2())
		}
		if !ok1 || !ok2 {
			return l.bail(notLive)
		}
		v := l.fn.newValue("zval")
		// in_array's own opcode carries its strict flag in extended_value,
		// which decides how a needle that is not a string is looked for.
		if n == "ZEND_IN_ARRAY" && op.GetExtendedValue() != 0 {
			l.define(v, intrinsicName(n), []string{a, b2}, "strict")
		} else {
			l.define(v, intrinsicName(n), []string{a, b2})
		}
		l.keep(op.GetResult(), v)

	case n == "ZEND_FETCH_DIM_R" || n == "ZEND_FETCH_OBJ_R":
		c, ok1 := l.operand(op.GetOp1Type(), op.GetOp1())
		k, ok2 := l.operand(op.GetOp2Type(), op.GetOp2())
		if !ok1 || !ok2 {
			return l.bail(notLive)
		}
		kind := "dim"
		if n == "ZEND_FETCH_OBJ_R" {
			kind = "prop"
		}
		if op.GetOp1Type() == opUnused {
			c = "$this"
		}
		v := l.fn.newValue("zval")
		l.define(v, kind+".get", []string{c, k}, "mode=R")
		l.keep(op.GetResult(), v)

	case n == "ZEND_CAST":
		// The target type is in extended_value, using the engine's IS_*
		// numbering. Lowering CAST without it produces an instruction that does
		// not say which cast it is -- the exact trap
		// LTM/zend-operand-encoding.md catalogues.
		a, live := l.operand(op.GetOp1Type(), op.GetOp1())
		if !live {
			return l.bail(notLive)
		}
		target, known := castTargets[op.GetExtendedValue()]
		if !known {
			return l.bail("unknown cast target")
		}
		v := l.fn.newValue("zval")
		l.define(v, "to."+target, []string{a})
		l.keep(op.GetResult(), v)

	case n == "ZEND_BW_NOT":
		// Unary, so it is not in binaryOps and was not lowered at all: `~$n`
		// bailed with "ZEND_BW_NOT: not lowered". The catalogue has had the
		// BitNot intrinsic the whole time.
		a, live := l.operand(op.GetOp1Type(), op.GetOp1())
		if !live {
			return l.bail(notLive)
		}
		v := l.fn.newValue("zval")
		l.define(v, intrinsicName(n), []string{a})
		l.keep(op.GetResult(), v)

	case n == "ZEND_STRLEN" || n == "ZEND_COUNT" || n == "ZEND_GET_TYPE" ||
		n == "ZEND_GET_CLASS" || n == "ZEND_BOOL" || n == "ZEND_BOOL_NOT" ||
		n == "ZEND_TYPE_CHECK":
		// The compiler specializes a handful of calls into their own opcode, so
		// they never reach the call path at all.
		a, live := l.operand(op.GetOp1Type(), op.GetOp1())
		if !live {
			return l.bail(notLive)
		}
		operands := []string{a}
		if n == "ZEND_TYPE_CHECK" {
			// **Which types pass is the whole instruction**, and it lives in
			// `extended_value` as a bitmask. Dropping it made `$a === null`
			// and `$a !== null` the same IR -- two opposite questions with one
			// spelling, which no downstream pass could have told apart.
			//
			// The mask is not "which type": `!== null` is 1020, the complement
			// of null over every other type, so it cannot be re-expressed as a
			// type plus a negation flag either.
			operands = append(operands, fmt.Sprint(op.GetExtendedValue()))
		}
		v := l.fn.newValue("zval")
		l.define(v, intrinsicName(n), operands)
		l.keep(op.GetResult(), v)

	case n == "ZEND_ASSIGN_OBJ" || n == "ZEND_ASSIGN_DIM" ||
		n == "ZEND_ASSIGN_STATIC_PROP":
		// The value is in the FOLLOWING opline, as ZEND_OP_DATA -- an opline
		// holds two operands and this node needs three. Folding the pair into
		// one instruction is the whole point of the un-flattening pass.
		data, err := l.opData()
		if err != nil {
			return err
		}
		val, ok3 := l.operand(data.GetOp1Type(), data.GetOp1())
		if !ok3 {
			return l.bail("OP_DATA operand not live in this block")
		}
		container, _ := l.operand(op.GetOp1Type(), op.GetOp1())
		if op.GetOp1Type() == opUnused {
			container = "$this"
		}
		kind := map[string]string{
			"ZEND_ASSIGN_OBJ": "prop", "ZEND_ASSIGN_DIM": "dim",
			"ZEND_ASSIGN_STATIC_PROP": "sprop",
		}[n]
		var key string
		var ok4 bool
		if kind == "sprop" {
			key, ok4 = l.fetchClass(op.GetOp2Type(), op.GetOp2())
		} else {
			key, ok4 = l.operand(op.GetOp2Type(), op.GetOp2())
		}
		if !ok4 {
			return l.bail("key not live in this block")
		}
		// The write is **one operation**: the container, the key and the value.
		// It used to be a place-producing fetch followed by a `store`, and the
		// store then re-derived at run time which kind of place it had been
		// handed. `kind` is known right here.
		//
		var write string
		var args []string
		switch {
		case kind == "dim" && op.GetOp2Type() == opUnused:
			write, args = "dim.append", []string{container, val}
		default:
			write, args = kind+".set", []string{container, key, val}
		}
		if l.definesValue() {
			v := l.fn.newValue("zval")
			l.define(v, write, args)
			l.keep(op.GetResult(), v)
		} else {
			l.emit(write, args)
		}

	case n == "ZEND_OP_DATA":
		// Consumed by the opline before it; never executed on its own.
		return l.bail("OP_DATA reached on its own")

	case n == "ZEND_ADD_ARRAY_UNPACK":
		// `[...$a]`. The accumulator arrives through the result slot the same
		// way ADD_ARRAY_ELEMENT's does, and op1 is what to spread.
		prev, live := l.operand(op.GetResultType(), op.GetResult())
		if !live {
			return l.bail("array accumulator not live in this block")
		}
		spread, oks := l.operand(op.GetOp1Type(), op.GetOp1())
		if !oks {
			return l.bail("unpacked value not live in this block")
		}
		v := l.fn.newValue("*arr")
		l.define(v, "array.unpack", []string{prev, spread})
		l.keep(op.GetResult(), v)

	// arrayElementByRef is the bit ZEND_INIT_ARRAY and ZEND_ADD_ARRAY_ELEMENT
	// set in extended_value for an element written `&$x`. The rest of the word
	// is the literal's size, shifted left by two.
	case n == "ZEND_INIT_ARRAY" || n == "ZEND_ADD_ARRAY_ELEMENT":
		// The array under construction arrives through the RESULT slot, which
		// the engine reuses across the whole literal -- the result is also an
		// input, which is not SSA under any reading. Each step becomes a fresh
		// value here, which is also the signal that this is a builder and needs
		// no aliasing question answered.
		var acc string
		if n == "ZEND_INIT_ARRAY" {
			v0 := l.fn.newValue("*arr")
			l.define(v0, "array.new", nil)
			acc = v0.String()
		} else {
			// The accumulator reaches here through the result slot, and a
			// literal split across blocks -- `[$a ?? $b, $c]`, where the
			// short-circuit ends the block mid-literal -- leaves it in a place
			// rather than a live value. Asking operand() rather than the
			// live-value map is what lets those keep lowering.
			prev, live := l.operand(op.GetResultType(), op.GetResult())
			if !live {
				return l.bail("array accumulator not live in this block")
			}
			acc = prev
		}
		if op.GetOp1Type() != opUnused {
			// **`[&$x]` binds a reference into the element.** The engine says
			// so in `extended_value`'s low bit -- read back from it: `[$c]` is
			// 4 and `[&$c]` is 5, `[$c, &$d]` puts the 1 on the second
			// opline -- and the rest of the word is the literal's size shifted
			// left by two.
			//
			// Lowering it as an ordinary element was a **wrong answer** rather
			// than a missing one: `$a = [&$c]; $a[0]++;` left `$c` at its old
			// value and printed no complaint, because the element had a copy.
			// The source is boxed first and the element keeps the box.
			bound := op.GetExtendedValue()&arrayElementByRef != 0
			val, ok5 := l.operand(op.GetOp1Type(), op.GetOp1())
			if !ok5 {
				return l.bail("element not live in this block")
			}
			if bound {
				boxed := l.fn.newValue("zval")
				l.define(boxed, "make_ref", []string{val})
				val = boxed.String()
			}
			v := l.fn.newValue("*arr")
			switch {
			case op.GetOp2Type() == opUnused && bound:
				l.define(v, "array.append.bound", []string{acc, val})
			case op.GetOp2Type() == opUnused:
				l.define(v, "array.append", []string{acc, val})
			case bound:
				k, _ := l.operand(op.GetOp2Type(), op.GetOp2())
				l.define(v, "array.set.bound", []string{acc, k, val})
			default:
				k, _ := l.operand(op.GetOp2Type(), op.GetOp2())
				l.define(v, "array.set", []string{acc, k, val})
			}
			l.keep(op.GetResult(), v)
		} else if n == "ZEND_INIT_ARRAY" {
			l.keep(op.GetResult(), Value{N: l.fn.next - 1, Type: "*arr"})
		}

	case n == "ZEND_ROPE_INIT" || n == "ZEND_ROPE_ADD" || n == "ZEND_ROPE_END":
		// One interpolation spread over consecutive oplines. This is a *builder*,
		// and it does not need escape analysis to know it: ROPE_ADD's result
		// slot is its own op1 -- the engine's encoding is an in-place append --
		// and the slot is a TMP the compiler invented for this one literal,
		// which no PHP expression can name and therefore no `&` can reach.
		//
		// Lowering it as a chain of pure `concat` would throw that away and make
		// D3 re-derive it by inference. The buffer is its own representation
		// instead, and `strbuf.append` consumes its operand: the definition and
		// the operand are the same storage, which is what lets the emitter reuse
		// one Go builder rather than allocate per piece.
		piece, ok6 := l.operand(op.GetOp2Type(), op.GetOp2())
		if !ok6 {
			return l.bail("rope piece not live in this block")
		}
		if n == "ZEND_ROPE_INIT" {
			v := l.fn.newValue("*strbuf")
			l.define(v, "strbuf.new", []string{piece})
			l.keep(op.GetResult(), v)
			break
		}
		prev, live := l.operand(op.GetOp1Type(), op.GetOp1())
		if !live {
			return l.bail("rope accumulator not live in this block")
		}
		buf := l.fn.newValue("*strbuf")
		l.define(buf, "strbuf.append", []string{prev, piece})
		if n == "ZEND_ROPE_ADD" {
			l.keep(op.GetResult(), buf)
			break
		}
		// ROPE_END appends its own piece and then completes the string, so it is
		// two instructions: the buffer stops being a buffer here.
		str := l.fn.newValue("*str")
		l.define(str, "strbuf.finish", []string{buf.String()})
		l.keep(op.GetResult(), str)

	case n == "ZEND_CATCH":
		// The caught exception arrives on the edge, as a block argument, rather
		// than through a slot. `catch (Exception)` names no variable at all, and
		// its result is IS_UNUSED -- writing it anyway would take slot 0 for a
		// CV index and clobber the first compiled variable.
		// The exception outlives the block that receives it -- every later
		// clause tests the same value -- so it is a **place**, which is the
		// IR's own rule for anything crossing a block. Only the region's entry
		// takes it as a block argument; the rest are reached by an ordinary
		// branch and read the place.
		// The chain is linked through op2, so each clause is told its place by
		// the one before it. Naming from the block's own region would not work:
		// a handler sits *outside* the try it serves, so its region is -1, and
		// two chains would collide on one name.
		exc, chained := l.excChain[l.i]
		if !chained {
			// The head of a chain: some region's catch entry. It is the only
			// clause the exception edge reaches, so it alone takes the block
			// argument, and it parks the value where the rest can read it.
			exc = fmt.Sprintf("$__exc%d", l.blk.ID)
			v := l.fn.newValue("zval")
			l.blk.Params = append(l.blk.Params, fmt.Sprintf("%s: zval", v))
			l.emit("local.set", []string{exc, v.String()})
		}
		if int(op.GetOp2()) != l.i {
			if l.excChain == nil {
				l.excChain = map[int]string{}
			}
			l.excChain[int(op.GetOp2())] = exc
		}

		// The class this clause accepts, which op1 carries and which was being
		// dropped: every handler bound unconditionally, so the *first* catch ran
		// for any exception and `catch (ValueError $a)` caught a TypeError.
		class, ok := l.operand(op.GetOp1Type(), op.GetOp1())
		if !ok {
			return l.bail(notLive)
		}
		// Where a non-match goes. Settled from the engine's own output rather
		// than from the flag bits: with three clauses op2 points at the next
		// catch (7, 9) except the last, where it points at *itself*; with one
		// clause it also points at itself. So op2 == this opline means there is
		// no next handler. `extended_value & 1` agrees independently -- 9 and 25
		// for a last clause against 8 and 16 -- which is why the rule is trusted.
		next := -1
		if int(op.GetOp2()) != l.i {
			next = l.blockAt(op.GetOp2())
		}
		bind := ""
		if op.GetResultType() == opCV {
			bind = "$" + cvName(l.oa, op.GetResult())
		}
		l.catches = append(l.catches, &catchRequest{
			from: l.blk, exc: exc, class: class, bind: bind, next: next,
		})

	case n == "ZEND_FETCH_THIS":
		v := l.fn.newValue("*obj")
		l.define(v, "this", nil)
		l.keep(op.GetResult(), v)

	case n == "ZEND_BIND_GLOBAL":
		k, _ := l.operand(op.GetOp2Type(), op.GetOp2())
		l.emit("bind_global", []string{"$" + cvName(oa, op.GetOp1()), k})

	case n == "ZEND_BIND_INIT_STATIC_OR_JMP":
		// `static $o = new stdClass;` -- an initializer the compiler could not
		// fold, so the value has to be **computed on first entry** and the
		// slot consulted on every later one. op2 is where to jump when the
		// static already holds a value; falling through runs the initializer,
		// which a BIND_STATIC carrying op2 then stores.
		name := "$" + cvName(oa, op.GetOp1())
		v := l.fn.newValue("bool")
		l.define(v, "static.bound", []string{name})
		target := l.blockAt(op.GetOp2())
		l.term(fmt.Sprintf("br %s, bb%d, bb%d", v, target, l.b+1), target, l.b+1)

	case n == "ZEND_BIND_STATIC":
		if op.GetOp2Type() != opUnused {
			// The **storing** form, which follows an unfoldable initializer:
			// op2 is the value just computed. The plain form has no op2 and
			// applies the compiled-in default instead.
			val, okv := l.operand(op.GetOp2Type(), op.GetOp2())
			if !okv {
				return l.bail("static initializer not live in this block")
			}
			l.emit("static.set", []string{"$" + cvName(oa, op.GetOp1()), val})
			break
		}
		l.emit("bind_static", []string{"$" + cvName(oa, op.GetOp1())})

	case n == "ZEND_FE_RESET_R" || n == "ZEND_FE_RESET_RW":
		a, ok7 := l.operand(op.GetOp1Type(), op.GetOp1())
		if !ok7 {
			return l.bail("subject not live in this block")
		}
		it := iterName(op.GetResult())
		// A `zval`, not a representation of its own: the cursor is an internal
		// object now, so the handle is an ordinary value that lives in an SSA
		// value, promotes like any other, and needs no special slot.
		v := l.fn.newValue("zval")
		// FE_RESET_RW builds a **live** cursor rather than a snapshot: a
		// by-reference loop sees an element the body appends and skips one it
		// unsets, because the engine holds a bucket position. The two resets
		// were lowered as the same opcode, which is what left `foreach ($a as
		// &$v)` iterating a copy.
		reset := "iter.new"
		if n == "ZEND_FE_RESET_RW" {
			reset = "iter.new.ref"
		}
		l.define(v, reset, []string{a})
		l.emit("local.set", []string{it, v.String()})
		// The subject is a temporary when it is an SSA value: no name reaches
		// it, so the cursor is its only holder.
		if !strings.HasPrefix(strings.TrimSpace(a), "$") {
			if l.iterOwned == nil {
				l.iterOwned = map[string]bool{}
			}
			l.iterOwned[it] = true
			if l.iterSubject == nil {
				l.iterSubject = map[string]string{}
			}
			l.iterSubject[it] = strings.TrimSpace(a)
		}
		if l.iterResets == nil {
			l.iterResets = map[string]*Block{}
		}
		l.iterResets[it] = l.blk
		exit := l.blockAt(op.GetOp2())
		valid := l.fn.newValue("bool")
		l.define(valid, "iter.valid", []string{it})
		l.term(fmt.Sprintf("br %s, bb%d, bb%d", valid, l.b+1, exit), l.b+1, exit)

	case n == "ZEND_FE_FETCH_R" || n == "ZEND_FE_FETCH_RW":
		// FE_FETCH is the loop *header*, and it tests before it fetches: if the
		// iterator is exhausted it jumps to the exit, otherwise it takes the
		// current element, advances, and falls through to the body.
		//
		// Emitting the fetch first and testing afterwards asks whether there is
		// a *further* element before running the body for this one, which drops
		// the last iteration of every loop -- `[1,2,3]` runs the body twice and
		// `[5]` not at all. The test therefore terminates this block and the
		// fetch moves into a block of its own, created once every original
		// block exists so that a block's index still equals its ID.
		it := iterName(op.GetOp1())
		// op2 receives the value and result the key -- the reverse of the
		// source order `as $k => $v`.
		req := fetchRequest{
			from:  l.blk,
			it:    it,
			value: l.placeFor(op.GetOp2Type(), op.GetOp2()),
			byRef: n == "ZEND_FE_FETCH_RW",
			fall:  l.b + 1,
			exit:  l.blockAt(op.GetExtendedValue()),
			reset: l.iterResets[it],
		}
		if op.GetResultType() != opUnused {
			req.key = l.placeFor(op.GetResultType(), op.GetResult())
		}
		if req.value == "" {
			return l.bail("foreach value target is not a place")
		}
		// A by-reference fetch into a **compiler temporary** -- the value
		// destructured by `foreach ($c as [&$x, $y])` -- holds the element's
		// box for the body; the engine frees it with the loop, which
		// releases that hold (list_reference_004).
		if req.byRef && strings.HasPrefix(req.value, "$__") {
			if l.loopRefTemp == nil {
				l.loopRefTemp = map[string]string{}
			}
			l.loopRefTemp[it] = req.value
		}
		l.fetches = append(l.fetches, req)
		l.blk.Term = pendingInline

	case n == "ZEND_FE_FREE":
		// **Whether the loop owned its subject** decides what freeing the
		// cursor means for an abandoned generator. `foreach (gen() as ...)
		// { break; }` holds the only reference, so PHP destroys the generator
		// here and its `finally` runs; `$g = gen(); foreach ($g as ...)` does
		// not, and the finally waits for `$g` to go. Measured, all three
		// timings.
		//
		// The subject being an SSA value rather than a place is exactly "the
		// program cannot name it", which is what a temporary is.
		free := "iter.free"
		var attrs []string
		if it := iterName(op.GetOp1()); l.iterOwned[it] {
			free = "iter.free.owned"
			if subject := l.iterSubject[it]; subject != "" {
				attrs = append(attrs, LoopSubjectAttr+subject)
			}
		}
		// **An exception freeing it here is thrown at the loop's end.** A
		// free on the way out through `return` or `break` carries
		// ZEND_FREE_ON_RETURN and, in op2, the distance to the loop's own
		// final free, and the engine looks the handler up there: a `try`
		// around the `return` alone does not catch what the destructor of the
		// loop's subject throws (try_finally_022).
		const freeOnReturn = 1
		if op.GetExtendedValue()&freeOnReturn != 0 {
			at := l.i + int(op.GetOp2())
			if r := regionOf(l.oa, at); r != l.blk.Region && catchBodyOf(l.oa, at)+1 == l.blk.InCatchBodyOf {
				attrs = append(attrs, fmt.Sprintf("%s%d", ThrowRegionAttr, r))
			}
		}
		l.emit(free, []string{iterName(op.GetOp1())}, attrs...)
		if temp := l.loopRefTemp[iterName(op.GetOp1())]; temp != "" {
			l.emit("unset", []string{temp})
		}

	// ---- declarations: the definition is module-level, the declaration is an
	// instruction where the engine put it ----
	case n == "ZEND_DECLARE_LAMBDA_FUNCTION":
		target, known := closureRef(oa, op.GetOp2())
		if !known {
			return l.bail("closure index out of range")
		}
		v := l.fn.newValue("*obj")
		l.define(v, "closure", []string{target})
		l.keep(op.GetResult(), v)

	case n == "ZEND_DECLARE_FUNCTION":
		target, known := closureRef(oa, op.GetOp2())
		if !known {
			return l.bail("function index out of range")
		}
		l.emit("declare_function", []string{target})

	case n == "ZEND_DECLARE_CLASS" || n == "ZEND_DECLARE_CLASS_DELAYED":
		cls, _ := l.operand(op.GetOp1Type(), op.GetOp1())
		// Kept as an instruction rather than hoisted: the position is
		// observable. traits/constant_005 prints before the declaration and
		// expects the fatal after it, and a declaration inside an `if` runs
		// only when control reaches it.
		if l.cfg.LinkFatal && l.i == l.cfg.LinkFatalOpline && displayName(l.oa) == "{main}" {
			if line := op.GetLine(); line > 0 {
				l.emit("declare_class", []string{cls}, "link_fatal", "rtdline="+strconv.FormatUint(uint64(line), 10))
				break
			}
			l.emit("declare_class", []string{cls}, "link_fatal")
			break
		}
		// The opline's line is the declaration's first line, which tells two
		// declarations of one name apart: the attribute carries it.
		if line := op.GetLine(); line > 0 {
			l.emit("declare_class", []string{cls}, "rtdline="+strconv.FormatUint(uint64(line), 10))
			break
		}
		l.emit("declare_class", []string{cls})

	case n == "ZEND_DECLARE_ANON_CLASS":
		cls, _ := l.operand(op.GetOp1Type(), op.GetOp1())
		v := l.fn.newValue("*class")
		l.define(v, "declare_anon_class", []string{cls})
		l.keep(op.GetResult(), v)

	// ---- write paths: these produce a place, not a value ----
	case n == "ZEND_FETCH_OBJ_W" || n == "ZEND_FETCH_OBJ_RW" ||
		n == "ZEND_FETCH_DIM_W" || n == "ZEND_FETCH_DIM_RW" ||
		n == "ZEND_FETCH_OBJ_UNSET" || n == "ZEND_FETCH_DIM_UNSET" ||
		n == "ZEND_FETCH_OBJ_FUNC_ARG" || n == "ZEND_FETCH_DIM_FUNC_ARG" ||
		n == "ZEND_FETCH_LIST_W":
		c, okc := l.receiverOperand(op.GetOp1Type(), op.GetOp1())
		if !okc {
			return l.bail("container not live in this block")
		}
		k, okk := l.operand(op.GetOp2Type(), op.GetOp2())
		if !okk {
			return l.bail("key not live in this block")
		}
		// **A FUNC_ARG fetch for a by-value parameter is a read**, which is
		// what the engine makes it once the callee is known: `test($x[][$y])`
		// with `function test($x)` is `Cannot use [] for reading` at the
		// append, where building the chain for write autovivified it and the
		// call recursed without end (func_arg_fetch_optimization). Settled only
		// where the callee and position are known, as settleSends does.
		if (n == "ZEND_FETCH_DIM_FUNC_ARG" || n == "ZEND_FETCH_OBJ_FUNC_ARG") && l.funcArgByValue() {
			v := l.fn.newValue("zval")
			if n == "ZEND_FETCH_DIM_FUNC_ARG" && op.GetOp2Type() == opUnused {
				l.define(v, "error.append_read", nil)
			} else {
				kind := "dim"
				if n == "ZEND_FETCH_OBJ_FUNC_ARG" {
					kind = "prop"
				}
				l.define(v, kind+".get", []string{c, k}, "mode=R")
			}
			l.keep(op.GetResult(), v)
			break
		}
		// `[&$u, &$v] = $src` -- list destructuring **by reference**. It is a
		// dimension fetch for write like any other; the only thing that made
		// it its own opcode is that a list element may be absent, which the
		// read form (`FETCH_LIST_R`) already lowers as an ordinary `dim.get`.
		kind := "dim"
		if strings.Contains(n, "_OBJ_") {
			kind = "prop"
		}
		// A **temporary** container -- a constant, `[0, 1]`, a call the
		// compiler made a TMP -- has no storage to hand out, and the VM's
		// FUNC_ARG fetch sent by reference refuses it:
		// zend_use_tmp_in_write_context_helper (gh12102).
		var extra []string
		if kind == "dim" && (op.GetOp1Type() == opConst || op.GetOp1Type() == opTmpVar) {
			extra = append(extra, "tmp")
		}
		// A property of one refuses the same way; only the FUNC_ARG fetch
		// can have one -- the compiler refuses any other write to it.
		if n == "ZEND_FETCH_OBJ_FUNC_ARG" && (op.GetOp1Type() == opConst || op.GetOp1Type() == opTmpVar) {
			extra = append(extra, "tmp")
		}
		// **A list destructured by reference out of a VAR** -- a call's
		// result -- has nothing to reference unless the value is one: the
		// engine notices and fetches by value (list_reference_003, _005).
		if n == "ZEND_FETCH_LIST_W" && op.GetOp1Type() == opVar {
			extra = append(extra, "list_var")
			// Nested in another list's element, the container arrives
			// boxed, and whether that element had anything to reference is
			// the outer place's to say.
			if outer, ok := l.listPlaces[op.GetOp1()]; ok {
				extra = append(extra, "list_of="+outer.String())
			}
		}
		v := l.place(kind, c, k, op.GetOp2Type(), accessMode(n), extra...)
		l.keep(op.GetResult(), v)
		if n == "ZEND_FETCH_LIST_W" {
			l.listPlaces[op.GetResult()] = v
		}

	case n == "ZEND_FETCH_W" || n == "ZEND_FETCH_RW" || n == "ZEND_FETCH_UNSET" ||
		n == "ZEND_FETCH_FUNC_ARG":
		// This family is *always* a variable variable. Verified against the
		// engine: `$$name = 1` compiles to FETCH_W whose op1 is the name, while
		// `${"lit"}` is folded to an ordinary CV at compile time and never gets
		// here. Rendering it as `local.ref $name` made `$$name` indistinguishable
		// from a static access, which silently defeated every analysis that has
		// to know a frame can reach its own locals by computed name.
		k, okk := l.operand(op.GetOp1Type(), op.GetOp1())
		if !okk {
			return l.bail("name not live in this block")
		}
		v := l.fn.newValue("place")
		l.define(v, "dynvar.ref", []string{k}, "mode="+accessMode(n), fetchScope(op))
		l.keep(op.GetResult(), v)

	case n == "ZEND_FETCH_STATIC_PROP_R" || n == "ZEND_FETCH_STATIC_PROP_IS":
		k, _ := l.operand(op.GetOp1Type(), op.GetOp1())
		// **op2 is the class**, and dropping it left every static property
		// looking like a global of that name -- `C::$n` and `D::$n`
		// indistinguishable in the IR.
		cls, okc := l.fetchClass(op.GetOp2Type(), op.GetOp2())
		if !okc {
			return l.bail("static property of an unrecognised class reference")
		}
		v := l.fn.newValue("zval")
		l.define(v, "sprop.get", []string{k, cls}, "mode="+accessMode(n))
		l.keep(op.GetResult(), v)

	case n == "ZEND_FETCH_STATIC_PROP_W" || n == "ZEND_FETCH_STATIC_PROP_RW" ||
		n == "ZEND_FETCH_STATIC_PROP_UNSET" || n == "ZEND_FETCH_STATIC_PROP_FUNC_ARG":
		k, _ := l.operand(op.GetOp1Type(), op.GetOp1())
		cls, okc := l.fetchClass(op.GetOp2Type(), op.GetOp2())
		if !okc {
			return l.bail("static property of an unrecognised class reference")
		}
		v := l.fn.newValue("place")
		l.define(v, "sprop.ref", []string{k, cls}, "mode="+accessMode(n))
		l.keep(op.GetResult(), v)

	case n == "ZEND_UNSET_VAR":
		// `unset($$n)` and `unset($GLOBALS["g"])`: op1 is a **name**, and the
		// scope bit says which table it is in -- the same shape the FETCH
		// family and ISSET_ISEMPTY_VAR have. `unset($x)` is ZEND_UNSET_CV and
		// does not come here.
		k, okk := l.operand(op.GetOp1Type(), op.GetOp1())
		if !okk {
			return l.bail("name not live in this block")
		}
		l.emit("dynvar.unset", []string{k}, fetchScope(op))

	case n == "ZEND_UNSET_OBJ" || n == "ZEND_UNSET_DIM":
		c, okc := l.operand(op.GetOp1Type(), op.GetOp1())
		if op.GetOp1Type() == opUnused && n == "ZEND_UNSET_OBJ" {
			// `unset($this->p)`, the same unused-op1 receiver as above.
			c, okc = "$this", true
		}
		k, okk := l.operand(op.GetOp2Type(), op.GetOp2())
		if !okc || !okk {
			return l.bail(notLive)
		}
		if op.GetOp1Type() == opUnused {
			c = "$this"
		}
		kind := "dim"
		if n == "ZEND_UNSET_OBJ" {
			kind = "prop"
		}
		// **Through `l.place`**, not by hand: the dimension form of an UNSET
		// fetch is its own intrinsic and carries the container's spelling, and
		// building the place here bypassed both. The same rule the compound
		// assignment path already carries a comment about -- two sites, one
		// rule, and this one had not learned it.
		pl := l.place(kind, c, k, op.GetOp2Type(), "UNSET")
		l.emit("unset", []string{pl.String()})

	case n == "ZEND_UNSET_CV":
		l.emit("unset", []string{"$" + cvName(oa, op.GetOp1())})

	// ---- resolution: all of these can autoload, and an autoloader can print
	// and can throw, so their effect contracts are not empty ----
	case n == "ZEND_DEFINED":
		// `defined()` is compiled to its own opcode rather than a call, so it
		// never reaches the builtin of that name.
		k, ok := l.operand(op.GetOp1Type(), op.GetOp1())
		if !ok {
			return l.bail(notLive)
		}
		v := l.fn.newValue("bool")
		l.define(v, "const.defined", []string{k})
		l.keep(op.GetResult(), v)

	case n == "ZEND_FETCH_CLASS_CONSTANT":
		cls, okc := l.fetchClass(op.GetOp1Type(), op.GetOp1())
		if !okc {
			return l.bail("class constant of an unrecognised class reference")
		}
		k, _ := l.operand(op.GetOp2Type(), op.GetOp2())
		v := l.fn.newValue("zval")
		l.define(v, "class_const", []string{cls, k})
		l.keep(op.GetResult(), v)

	case n == "ZEND_FETCH_CLASS_NAME":
		// `self`, `parent` and `static` arrive with **no operand**: the class is
		// a fetch *type* in op1's value, which is what `fetchClass` decodes.
		// Reading it as an ordinary operand gave the empty string, and the
		// emitter then refused the whole body as an unrenderable operand -- so
		// `static::class` did not compile at all, and neither did `self::class`
		// anywhere the engine could not fold it.
		cls, ok := l.fetchClass(op.GetOp1Type(), op.GetOp1())
		if !ok {
			return l.bail("class name of an unrecognised class reference")
		}
		v := l.fn.newValue("*str")
		l.define(v, "class_name", []string{cls})
		l.keep(op.GetResult(), v)

	case n == "ZEND_FUNC_GET_ARGS" || n == "ZEND_FUNC_NUM_ARGS":
		// **Not library functions.** The engine gives each its own opcode
		// because the answer is a property of the *frame*: what this call was
		// actually passed, which is neither an operand nor anything
		// `runtime/php` can see -- it has no frame. Routing them through the
		// builtin table reported `unsupported: library function
		// func_get_args`, which named the wrong layer.
		//
		// `func_get_args()` answers with the arguments **passed**, not the
		// parameters declared: `function f($a, $b = 2)` called as `f(1)` gives
		// `[1]`, and the default never appears.
		name := "func.args"
		if n == "ZEND_FUNC_NUM_ARGS" {
			name = "func.num_args"
		}
		v := l.fn.newValue("zval")
		l.define(v, name, nil)
		// **`array_slice(func_get_args(), N)` is one opcode.** The compiler
		// folds it into FUNC_GET_ARGS with the offset as a constant op1, so
		// ignoring op1 answered every argument. The slice is the library's,
		// renumbering the keys exactly as the folded form does.
		if n == "ZEND_FUNC_GET_ARGS" && op.GetOp1Type() == opConst {
			skip, oks := l.operand(op.GetOp1Type(), op.GetOp1())
			if !oks {
				return l.bail(notLive)
			}
			sliced := l.fn.newValue("zval")
			l.blk.Insts = append(l.blk.Insts, Inst{Result: &sliced, Opcode: "call.fn",
				Operands: []string{`"array_slice"`}, ArgList: fmt.Sprintf("(byval %s, byval %s)", v, skip)})
			v = sliced
		}
		l.keep(op.GetResult(), v)

	case n == "ZEND_FETCH_GLOBALS":
		// `$GLOBALS` read whole. **A snapshot, not a live view**: since PHP 8.1
		// writing through the array does nothing, and the compiler rewrites
		// `$GLOBALS["x"]` into a plain global fetch rather than emitting this
		// at all -- so what reaches here is only the whole-array read.
		//
		// Not an intrinsic, because the symbol table it reads is not a value
		// `runtime/php` can see: the interpreter keeps one on the Ctx and the
		// generated program keeps its own.
		v := l.fn.newValue("zval")
		l.define(v, "fetch_globals", nil)
		l.keep(op.GetResult(), v)

	case n == "ZEND_FETCH_CLASS":
		cls, _ := l.operand(op.GetOp2Type(), op.GetOp2())
		v := l.fn.newValue("*class")
		l.define(v, "fetch_class", []string{cls})
		l.keep(op.GetResult(), v)

	case n == "ZEND_DECLARE_CONST":
		k, _ := l.operand(op.GetOp1Type(), op.GetOp1())
		val, okv := l.operand(op.GetOp2Type(), op.GetOp2())
		if !okv {
			return l.bail("value not live in this block")
		}
		l.emit("declare_const", []string{k, val})

	case n == "ZEND_BIND_LEXICAL":
		k, _ := l.operand(op.GetOp2Type(), op.GetOp2())
		c, okc := l.operand(op.GetOp1Type(), op.GetOp1())
		if !okc {
			return l.bail("closure not live in this block")
		}
		// extended_value is 0 for `use ($n)` and **1 for `use (&$n)`**, read
		// back from the engine. Dropping it made a by-reference capture behave
		// as a copy -- the closure would see the value at capture time and its
		// writes would go nowhere.
		// **extended_value is a bitmask, not a flag.** Bit 0 is by-reference;
		// the engine also sets bit 5 on every capture after the first, so
		// `== 1` was right for a lone `use (&$x)` and wrong the moment anything
		// preceded it:
		//
		//	use ($n, &$m)   $n -> 0    $m -> 33   (32|1)
		//	use (&$m, $n)   $m -> 1    $n -> 32
		//
		// Read back from the engine by printing the raw value, after `== 1`
		// had silently turned every non-first `use (&$x)` into a copy -- writes
		// through it went nowhere, which is a wrong answer rather than a
		// missing diagnostic.
		mode := "value"
		if op.GetExtendedValue()&1 != 0 {
			mode = "ref"
		}
		attrs := []string{"use=" + mode}
		// Bit 1 marks an **implicit** capture -- an arrow function's -- which
		// binds an undefined variable silently where an explicit `use`
		// warns (closure_027 against attribute_reflection_reach's nested
		// arrow functions).
		if op.GetExtendedValue()&2 != 0 {
			attrs = append(attrs, "implicit")
		}
		l.emit("bind_lexical", []string{c, k}, attrs...)

	// ---- increments; the object and dim forms take a place ----
	case n == "ZEND_PRE_INC" || n == "ZEND_PRE_DEC" ||
		n == "ZEND_POST_INC" || n == "ZEND_POST_DEC":
		target, okt := l.operand(op.GetOp1Type(), op.GetOp1())
		if !okt {
			return l.bail("target not live in this block")
		}
		if l.definesValue() {
			v := l.fn.newValue("zval")
			l.define(v, intrinsicName(n), []string{target})
			l.keep(op.GetResult(), v)
		} else {
			l.emit(intrinsicName(n), []string{target})
		}

	// ---- eval and include are refused at emission, not at lowering ----
	case n == "ZEND_INCLUDE_OR_EVAL":
		// The rest of the body compiles perfectly well; only this construct
		// cannot. Lowering it to an explicit refusal keeps the body compilable
		// and moves the honesty to where it belongs -- a standalone artifact
		// has no compiler at run time, per runtime/interp/include.go.
		a, oka := l.operand(op.GetOp1Type(), op.GetOp1())
		if !oka {
			return l.bail(notLive)
		}
		v := l.fn.newValue("zval")
		// **An include keeps its flavour**: one whose file is known when the
		// program is built can be compiled in (the emitter decides), and the
		// flavour says whether a second inclusion runs or answers true. An
		// eval of a literal is the same shape: code known when the program is
		// built. The bits are the engine's (runtime/interp/include.go).
		if kind, isInclude := includeFlavours[op.GetExtendedValue()]; isInclude {
			l.define(v, "include", []string{strconv.Quote(kind), a})
		} else {
			l.define(v, "unsupported", []string{"\"include_or_eval\"", a})
		}
		if l.definesValue() {
			l.keep(op.GetResult(), v)
		}

	// ---- references ----
	case n == "ZEND_ASSIGN_REF" || n == "ZEND_ASSIGN_OBJ_REF" ||
		n == "ZEND_ASSIGN_STATIC_PROP_REF":
		// The plain form has its source in op2; the object and static forms
		// need three operands, so theirs is in the following OP_DATA. Reading
		// op2 for those would take the property *name* as the value bound.
		var src string
		var oks bool
		if n == "ZEND_ASSIGN_REF" {
			src, oks = l.operand(op.GetOp2Type(), op.GetOp2())
		} else {
			data, err := l.opData()
			if err != nil {
				return err
			}
			src, oks = l.operand(data.GetOp1Type(), data.GetOp1())
		}
		if !oks {
			return l.bail("source not live in this block")
		}
		// **`extended_value & 1` means the source is not a variable** -- a
		// function result, which has no storage to bind to. Measured across
		// the four shapes: `$y["a"]`, `$o->p` and a plain CV all carry 0, and
		// only `f()` carries 1.
		//
		// The engine sometimes wraps such a source in its own MAKE_REF and
		// sometimes does not -- it does for the object and dim targets, not
		// for a plain CV -- so the wrap is applied here when it is missing.
		// Whether the binding is legal is still a run-time question: a
		// function declared `function &f()` hands back a real box and earns no
		// notice.
		if op.GetExtendedValue()&1 != 0 && !l.madeRef[src] {
			v := l.fn.newValue("zval")
			l.define(v, "make_ref", []string{src})
			src = v.String()
		}
		dst, okd := l.operand(op.GetOp1Type(), op.GetOp1())
		if !okd {
			return l.bail("target not live in this block")
		}
		// **The object and static forms name a member, not the container.**
		// `$o->p =& $v` is ASSIGN_OBJ_REF with the object in op1 and the
		// property name in op2, and taking op1 as the target bound `$o` itself
		// to `$v` -- the object gone, silently, in any program that did not go
		// on to read the property. The plain form's op1 really is the target.
		switch n {
		case "ZEND_ASSIGN_OBJ_REF":
			if op.GetOp1Type() == opUnused {
				dst = "$this"
			}
			k, okk := l.operand(op.GetOp2Type(), op.GetOp2())
			if !okk {
				return l.bail("property name not live in this block")
			}
			dst = l.place("prop", dst, k, op.GetOp2Type(), "W").String()
		case "ZEND_ASSIGN_STATIC_PROP_REF":
			cls, okc := l.fetchClass(op.GetOp2Type(), op.GetOp2())
			if !okc {
				return l.bail("static property of an unrecognised class reference")
			}
			v := l.fn.newValue("place")
			l.define(v, "sprop.ref", []string{dst, cls}, "mode=W")
			dst = v.String()
		}
		// bind_ref, not store: `$a = &$b` replaces what the slot is bound to,
		// where `$a = $b` writes *through* whatever reference the slot already
		// holds. Getting these the wrong way round is silently wrong.
		l.emit("bind_ref", []string{dst, src})
		if l.definesValue() {
			// **A value, not the box.** `$e = ($d = &$c)` gives `$e` the value
			// `$c` held at that moment: writing `$c` afterwards changes `$d`
			// and leaves `$e` alone. Asked of the engine, because typing this
			// `*ref` -- which is what the slot holds -- reads plausibly and
			// makes the assignment that consumes it an alias PHP does not have.
			v := l.fn.newValue("zval")
			l.define(v, "local.get", []string{dst})
			l.keep(op.GetResult(), v)
			// ...but **a send that may bind takes the variable**: `f($b =
			// &$a)` into `&$p` binds `$b`'s reference, silently, where the
			// value would earn `Only variables should be passed by
			// reference` and write nowhere (bug72038).
			if strings.HasPrefix(dst, "$") {
				if l.refResult == nil {
					l.refResult = map[string]string{}
				}
				l.refResult[v.String()] = dst
			}
		}

	case n == "ZEND_MAKE_REF":
		a, oka := l.operand(op.GetOp1Type(), op.GetOp1())
		if !oka {
			return l.bail(notLive)
		}
		// `zval`, not `*ref`: what this produces is a **value that may or may
		// not hold a box**. `=& f()` binds when `f` returns by reference and
		// notices-and-copies when it does not, and which one is a run-time
		// question about the returned value. Typing it `*ref` claims the
		// answer is always the first.
		v := l.fn.newValue("zval")
		l.define(v, "make_ref", []string{a})
		// A list element boxed for a list nested in it (FETCH_LIST_W).
		if outer, ok := l.listPlaces[op.GetOp1()]; ok && op.GetOp1Type() == opVar {
			l.listPlaces[op.GetResult()] = outer
		}
		if l.madeRef == nil {
			l.madeRef = map[string]bool{}
		}
		l.madeRef[v.String()] = true
		// **Carried as the box.** A `return $a` from a by-reference function
		// inside a try saves its reference here and returns it after the
		// finally, which may write `$a` in between -- and the caller sees that
		// write (bug72215_3). Assigning the temporary stored the value and
		// dropped the reference; binding keeps it.
		l.slotValue[op.GetResult()] = v
		if l.crossBlock[op.GetResult()] {
			l.emit("local.set", []string{tempName(op.GetResult()), v.String()}, SeparateAttr)
		}

	case n == "ZEND_RETURN_BY_REF":
		a, oka := l.operand(op.GetOp1Type(), op.GetOp1())
		if !oka {
			return l.bail(notLive)
		}
		// **What the engine returned is in the operand's kind**, which the
		// IR loses once a variable is promoted to a value. A literal, a
		// temporary, or a VAR the compiler marked RETURNS_VALUE (2) -- `return
		// $x = 5;` -- has nothing behind it: the engine notices `Only variable
		// references should be returned by reference` and wraps it in a fresh
		// reference. A VAR marked RETURNS_FUNCTION (1) is a call's result and
		// notices only when that call did not return a reference. Anything
		// else is a variable, returned as the reference it is.
		ev := op.GetExtendedValue()
		switch kind := op.GetOp1Type(); {
		case kind == opConst || kind == opTmpVar || kind == opVar && ev&2 != 0:
			v := l.fn.newValue("zval")
			l.define(v, "ref_return", []string{a})
			a = v.String()
		case kind == opVar && ev&1 != 0:
			v := l.fn.newValue("zval")
			l.define(v, "ref_return", []string{a}, "call")
			a = v.String()
		}
		l.term("ret.ref " + a)

	// ---- compound assignment: extended_value names the binary op ----
	case n == "ZEND_ASSIGN_OP" || n == "ZEND_ASSIGN_OBJ_OP" ||
		n == "ZEND_ASSIGN_DIM_OP" || n == "ZEND_ASSIGN_STATIC_PROP_OP":
		binop := "?"
		if int(op.GetExtendedValue()) < len(l.cfg.Names) {
			binop = intrinsicName(l.cfg.Names[op.GetExtendedValue()])
		}
		var target string
		var okt bool
		if n == "ZEND_ASSIGN_OP" {
			// `$x += 1` has no receiver: op1 *is* the variable.
			target, okt = l.operand(op.GetOp1Type(), op.GetOp1())
		} else {
			target, okt = l.receiverOperand(op.GetOp1Type(), op.GetOp1())
		}
		if !okt {
			return l.bail("target not live in this block")
		}
		var val string
		if n == "ZEND_ASSIGN_OP" {
			v2, okv := l.operand(op.GetOp2Type(), op.GetOp2())
			if !okv {
				return l.bail("value not live in this block")
			}
			val = v2
		} else {
			// The dim/prop/sprop forms put the key in op2 and the value in the
			// following OP_DATA.
			data, err := l.opData()
			if err != nil {
				return err
			}
			v2, okv := l.operand(data.GetOp1Type(), data.GetOp1())
			if !okv {
				return l.bail("OP_DATA operand not live in this block")
			}
			kind := map[string]string{
				"ZEND_ASSIGN_OBJ_OP": "prop", "ZEND_ASSIGN_DIM_OP": "dim",
				"ZEND_ASSIGN_STATIC_PROP_OP": "sprop",
			}[n]
			// For a static property op2 is the **class**, and it arrives as a
			// fetch-class code rather than an operand -- `l.operand` returns ""
			// for it, which silently produced `sprop.ref "count", ,`.
			var key string
			var okk bool
			if kind == "sprop" {
				key, okk = l.fetchClass(op.GetOp2Type(), op.GetOp2())
			} else {
				key, okk = l.operand(op.GetOp2Type(), op.GetOp2())
			}
			if !okk {
				return l.bail("key not live in this block")
			}
			// `$a[] .= "x"` has **no key operand**, and an append is a place
			// kind of its own rather than a dim.ref with a missing one -- the
			// same distinction `l.place` draws. Building it by hand here
			// emitted `dim.ref $a` with one operand, which the emitter refused
			// as "wants a  argument the IR does not carry": a gap that was
			// really a second copy of a rule.
			pl := l.place(kind, target, key, op.GetOp2Type(), "RW")
			target, val = pl.String(), v2
		}
		// A compound assignment is an expression: `$x = ($y .= "a")` yields the
		// assigned value, and the engine puts it in the result slot. Emitting
		// it as a statement drops that value and whatever reads it later cannot
		// find it.
		//
		// This stays one instruction *here* because the builder-promotion pass
		// reads it -- `$out .= $x` in a loop is the pattern it looks for, and
		// splitting it before that pass ran turned the append into a read and
		// refused every promotion. IntrinsicCalls expands it afterwards, so the
		// IR that leaves Lower has the read, the call and the write.
		if l.definesValue() {
			v := l.fn.newValue("zval")
			l.define(v, "assign_op", []string{target, val}, "op="+binop)
			l.keep(op.GetResult(), v)
		} else {
			l.emit("assign_op", []string{target, val}, "op="+binop)
		}

	case n == "ZEND_CLONE":
		a, oka := l.operand(op.GetOp1Type(), op.GetOp1())
		if !oka {
			return l.bail(notLive)
		}
		v := l.fn.newValue("*obj")
		l.define(v, "clone", []string{a})
		l.keep(op.GetResult(), v)

	case n == "ZEND_BEGIN_SILENCE":
		v := l.fn.newValue("i64")
		l.define(v, "silence_begin", nil)
		l.keep(op.GetResult(), v)

	case n == "ZEND_END_SILENCE":
		a, oka := l.operand(op.GetOp1Type(), op.GetOp1())
		if !oka {
			return l.bail("mask not live in this block")
		}
		l.emit("silence_end", []string{a})

	case n == "ZEND_FETCH_OBJ_IS" || n == "ZEND_FETCH_DIM_IS" ||
		n == "ZEND_FETCH_LIST_R":
		c, okc := l.operand(op.GetOp1Type(), op.GetOp1())
		k, okk := l.operand(op.GetOp2Type(), op.GetOp2())
		if !okc || !okk {
			return l.bail(notLive)
		}
		if op.GetOp1Type() == opUnused {
			c = "$this"
		}
		kind := "dim"
		if n == "ZEND_FETCH_OBJ_IS" {
			kind = "prop"
		}
		v := l.fn.newValue("zval")
		l.define(v, kind+".get", []string{c, k}, "mode="+accessMode(n))
		l.keep(op.GetResult(), v)

	case n == "ZEND_UNSET_STATIC_PROP":
		// `unset(C::$x)` is always refused -- `Attempt to unset static
		// property C::$x` -- once the class is fetched, which may autoload
		// or fail on its own (`self` with no class scope; bug61011,
		// exception_013, self_method_or_prop_outside_class).
		name, okn := l.operand(op.GetOp1Type(), op.GetOp1())
		if !okn {
			return l.bail(notLive)
		}
		cls, okc := l.fetchClass(op.GetOp2Type(), op.GetOp2())
		if !okc {
			return l.bail("class not live in this block")
		}
		l.emit("sprop.unset", []string{name, cls})

	case n == "ZEND_ISSET_ISEMPTY_STATIC_PROP":
		// A static property's operands are (name, class) -- the **same shape**
		// as a dimension's (container, key) and a completely different meaning.
		// Sharing the generic `isset` spelling below meant the emitter read the
		// property name as a container and the class as a key, and answered
		// from a string offset: `isset(C::$z)` on a null property was `true`.
		//
		// The silent read is what answers this: an undeclared static property
		// is `false` here where a loud read raises an Error, and a declared
		// null one is `false` because isset asks set-and-not-null.
		which := "isset"
		if op.GetExtendedValue()&1 != 0 {
			which = "empty"
		}
		name, okn := l.operand(op.GetOp1Type(), op.GetOp1())
		if !okn {
			return l.bail(notLive)
		}
		cls, okc := l.fetchClass(op.GetOp2Type(), op.GetOp2())
		if !okc {
			return l.bail("class not live in this block")
		}
		read := l.fn.newValue("zval")
		l.define(read, "sprop.get", []string{name, cls}, "mode=IS")
		v := l.fn.newValue("bool")
		l.define(v, which, []string{read.String()})
		l.keep(op.GetResult(), v)

	case strings.HasPrefix(n, "ZEND_ISSET_ISEMPTY_"):
		// isset is answered by __isset alone while empty goes on to ask __get,
		// so these are two intrinsics rather than one with a flag.
		which := "isset"
		if op.GetExtendedValue()&1 != 0 {
			which = "empty"
		}
		c, okc := l.operand(op.GetOp1Type(), op.GetOp1())
		if !okc {
			return l.bail(notLive)
		}
		// **`$this` is an unused op1**, exactly as it is in the FETCH_OBJ
		// family. `isset($this->$name)` lowered to `isset , $name` -- an empty
		// container operand, which reached the emitter as "unrenderable
		// operand" and read like a missing feature rather than a dropped
		// receiver.
		if op.GetOp1Type() == opUnused {
			c = "$this"
		}
		operands := []string{c}
		if op.GetOp2Type() != opUnused {
			k, okk := l.operand(op.GetOp2Type(), op.GetOp2())
			if !okk {
				return l.bail("key not live in this block")
			}
			operands = append(operands, k)
		}
		v := l.fn.newValue("bool")
		if n == "ZEND_ISSET_ISEMPTY_VAR" {
			// **op1 is a name here, not a slot**, and that is the opcode's
			// doing rather than the operand's: `isset($x)` is
			// ISSET_ISEMPTY_CV, while `isset($$n)` and `isset($GLOBALS["g"])`
			// are this one with the name in op1 -- as a CV for the first and a
			// literal for the second, so the spelling cannot tell them apart
			// and the opcode has to say. Both were answered by asking whether
			// the *name string* is set, which is always true.
			//
			// The scope bit then says which table the name is in, the same one
			// the FETCH family carries.
			l.define(v, which, operands, "by=name", fetchScope(op))
		} else if n == "ZEND_ISSET_ISEMPTY_PROP_OBJ" {
			// **`->` is not `[]`**, and the opcode is the only place that says
			// so: both forms lower to the same two-operand `isset`, and the
			// emitter answered both from the container's *shape*. So
			// `isset(["a" => "b"]->a)` found the array key and said true where
			// the engine says false -- an array has no properties -- and an
			// ArrayAccess object answered `isset($o->p)` through offsetExists.
			l.define(v, which, operands, "on=prop")
		} else {
			l.define(v, which, operands)
		}
		l.keep(op.GetResult(), v)

	case n == "ZEND_RECV_VARIADIC":
		v := l.fn.newValue("*arr")
		l.define(v, "recv.variadic", []string{fmt.Sprint(op.GetOp1() - 1)})
		l.assignTo(op.GetResultType(), op.GetResult(), v)

	case n == "ZEND_JMP_NULL":
		a, oka := l.operand(op.GetOp1Type(), op.GetOp1())
		if !oka {
			return l.bail(notLive)
		}
		// `?->` short-circuits the whole chain, and what it yields depends on
		// the construct it was part of -- which is readable only off the LAST
		// opline being jumped over.
		isNull := l.blockAt(op.GetOp2())
		// **The short-circuit writes the chain's result**, and the opcode says
		// which: `extended_value` is IS_NULL, IS_FALSE or IS_TRUE, because
		// `$a?->p` yields null, `isset($a?->p)` yields false and
		// `empty($a?->p)` yields true. Writing it *before* the branch is the
		// same thing as writing it on the taken edge -- the value path always
		// overwrites -- and needs no block of its own. Without it the result
		// temp was never written on the null path and the consumer read an
		// undefined slot: `isset($b?->bar)` printed NULL where the engine says
		// bool(false).
		// `extended_value` carries both facts, read off the engine rather than
		// the header: the low two bits are the **chain kind** -- 0 an ordinary
		// expression, 1 `isset`, 2 `empty` -- and bit 2 is set exactly when the
		// chain is read silently. Measured for all four forms:
		//
		//	$a?->p            0    null,  loud
		//	$a?->p ?? "x"     4    null,  silent
		//	isset($a?->p)     5    false, silent
		//	empty($a?->p)     6    true,  silent
		ev := op.GetExtendedValue()
		if res := op.GetResult(); res != 0 || op.GetResultType() != opUnused {
			short := "null"
			switch ev & 3 {
			case 1:
				short = "false"
			case 2:
				short = "true"
			}
			v := l.fn.newValue("zval")
			l.define(v, "copy", []string{short})
			l.assignTo(op.GetResultType(), res, v)
		}
		// `??` asks whether the value is set, which is an **operation**, not a
		// kind of branch. The targets swap because `is_set` answers the
		// opposite question to the one `br.null` asked.
		//
		// **Silent when the chain is**, which the last opline being jumped over
		// says: `$a?->p` warns about an undefined `$a`, while `$a?->p ?? "d"`
		// and `isset($a?->p)` do not. The guard read its operand loudly in every
		// case, so the two silent forms invented a warning the engine does not
		// emit.
		set := l.fn.newValue("bool")
		attrs := []string{}
		if ev&4 != 0 {
			attrs = append(attrs, "silent")
		}
		l.define(set, "is_set", []string{a}, attrs...)
		l.term(fmt.Sprintf("br %s, bb%d, bb%d", set, l.b+1, isNull), l.b+1, isNull)

	case n == "ZEND_CHECK_FUNC_ARG":
		// Records which argument position the following FETCH_*_FUNC_ARG is
		// about, so the engine can decide read-or-write once the callee's
		// signature is known. The IR carries the mode on the access itself, so
		// nothing is emitted here -- except that a **named** argument (op2 the
		// name) is not at the next position, which funcArgByValue must know
		// (fetched_arg_position: `mixed(c: $v[2], ...)`).
		l.namedFuncArg = op.GetOp2Type() == opConst

	case n == "ZEND_SEPARATE" || n == "ZEND_CHECK_UNDEF_ARGS_SEPARATE":
		a, oka := l.operand(op.GetOp1Type(), op.GetOp1())
		if !oka {
			return l.bail(notLive)
		}
		v := l.fn.newValue("zval")
		l.define(v, "separate", []string{a})
		l.keep(op.GetResult(), v)

	case n == "ZEND_CALLABLE_CONVERT":
		// `$o->m(...)` pops a pending call that is never performed and makes a
		// Closure of it -- the neatest proof that the three-phase stream
		// describes a call node rather than executing one.
		if len(l.calls) == 0 {
			return l.bail("callable convert with no pending call")
		}
		call := l.calls[len(l.calls)-1]
		l.calls = l.calls[:len(l.calls)-1]
		operands := []string{}
		if call.receiver != "" {
			operands = append(operands, call.receiver)
		}
		if call.callee != "" {
			operands = append(operands, call.callee)
		}
		v := l.fn.newValue("*obj")
		var attrs []string
		if call.fallback != "" {
			// `strlen(...)` inside a namespace resolves the same way the call
			// would have, so the closure carries the same pair of names.
			attrs = append(attrs, "fallback="+call.fallback)
		}
		l.define(v, "closure.from_call", operands, attrs...)
		l.keep(op.GetResult(), v)

	case n == "ZEND_FETCH_R" || n == "ZEND_FETCH_IS":
		// The read half of the same family, and dynamic for the same reason.
		k, okk := l.operand(op.GetOp1Type(), op.GetOp1())
		if !okk {
			return l.bail("name not live in this block")
		}
		v := l.fn.newValue("zval")
		l.define(v, "dynvar.get", []string{k}, "mode="+accessMode(n), fetchScope(op))
		l.keep(op.GetResult(), v)

	case n == "ZEND_PRE_INC_OBJ" || n == "ZEND_PRE_DEC_OBJ" ||
		n == "ZEND_POST_INC_OBJ" || n == "ZEND_POST_DEC_OBJ" ||
		n == "ZEND_PRE_INC_STATIC_PROP" || n == "ZEND_PRE_DEC_STATIC_PROP" ||
		n == "ZEND_POST_INC_STATIC_PROP" || n == "ZEND_POST_DEC_STATIC_PROP":
		c, okc := l.operand(op.GetOp1Type(), op.GetOp1())
		if !okc {
			return l.bail(notLive)
		}
		if op.GetOp1Type() == opUnused {
			c = "$this"
		}
		kind := "prop"
		// **For a static property op2 is the class**, and it arrives as a
		// fetch-class code rather than an operand -- `l.operand` answers "" and
		// reports success, so the place was built with an empty class and the
		// emitter refused with `sprop.ref wants a class argument the IR does
		// not carry`. That is the same defect the ASSIGN_*_OP path already
		// carries a comment about: two sites, one rule, and the second did not
		// learn it. `++self::$n` is the shape that reaches here.
		k, okk := l.operand(op.GetOp2Type(), op.GetOp2())
		if strings.Contains(n, "STATIC_PROP") {
			kind = "sprop"
			k, okk = l.fetchClass(op.GetOp2Type(), op.GetOp2())
		}
		if !okk {
			return l.bail("static property of an unrecognised class reference")
		}
		// **An undefined receiver warns** before the refusal, as the
		// compound assignment's does: `$u->c++` on an unset `$u` is
		// `Undefined variable $u`, then `Attempt to increment/decrement
		// property "c" on null` -- where `$u->a[] = 1` and `unset($u->f)`
		// say nothing (bug78531, probed).
		if spelled, named := readableVar(c); named && kind == "prop" {
			l.emit("warn.undefined_container", []string{c, strconv.Quote(spelled)})
		}
		pl := l.fn.newValue("place")
		l.define(pl, kind+".ref", []string{c, k}, "mode=RW")
		name := intrinsicName(n)
		name = strings.TrimSuffix(strings.TrimSuffix(name, "_obj"), "_static_prop")
		if l.definesValue() {
			v := l.fn.newValue("zval")
			l.define(v, name, []string{pl.String()})
			l.keep(op.GetResult(), v)
		} else {
			l.emit(name, []string{pl.String()})
		}

	// ---- generators ----
	case n == "ZEND_GENERATOR_CREATE":
		// The split point. Everything before it -- parameter binding and type
		// verification -- runs eagerly when the function is called; everything
		// after runs on resume. Probed against the engine: a generator's
		// TypeError fires at call time, before the object exists.
		l.term(fmt.Sprintf("generator_create -> bb%d", l.b+1), l.b+1)

	case n == "ZEND_YIELD" || n == "ZEND_YIELD_FROM":
		operands := []string{}
		if op.GetOp1Type() != opUnused {
			a, oka := l.operand(op.GetOp1Type(), op.GetOp1())
			if !oka {
				return l.bail("yielded value not live in this block")
			}
			// **A by-reference generator yielding what is not a variable**
			// notices, in the words `return` uses with "yielded" for
			// "returned", and the operand kinds say which: a literal or a
			// temporary always, a call result (extended_value 1) when the
			// call did not return a reference -- the same encoding
			// ZEND_RETURN_BY_REF carries.
			if n == "ZEND_YIELD" && oa.GetReturnsRef() {
				ev := op.GetExtendedValue()
				switch kind := op.GetOp1Type(); {
				case kind == opConst || kind == opTmpVar || kind == opVar && ev&2 != 0:
					v := l.fn.newValue("zval")
					l.define(v, "yield_ref", []string{a})
					a = v.String()
				case kind == opVar && ev&1 != 0:
					v := l.fn.newValue("zval")
					l.define(v, "yield_ref", []string{a}, "call")
					a = v.String()
				case kind == opCV:
					// **A variable is yielded as its box**, which is what
					// `foreach ($gen as &$v)` binds to: writing `$v` writes
					// the generator's variable, and through it whatever that
					// variable is itself bound to. Reading it here yielded a
					// copy, so every such write went nowhere
					// (typed_properties_034).
					v := l.fn.newValue("zval")
					l.define(v, "make_ref", []string{a})
					a = v.String()
				}
			}
			operands = append(operands, a)
		}
		if op.GetOp2Type() != opUnused {
			k, okk := l.operand(op.GetOp2Type(), op.GetOp2())
			if !okk {
				return l.bail("yielded key not live in this block")
			}
			operands = append(operands, k)
		}
		kind := "yield"
		if n == "ZEND_YIELD_FROM" {
			kind = "yield_from"
		}
		// The result operand IS what send() supplied, so the resumption value
		// arrives on the edge as a block argument. That is not an encoding
		// choice -- it is what the opcode already means.
		l.resumed = Value{N: l.fn.next, Type: "zval"}
		l.fn.next++
		l.resumePending = l.definesValue()
		l.resumeSlot = op.GetResult()
		l.term(fmt.Sprintf("%s %s -> bb%d", kind, strings.Join(operands, ", "), l.b+1), l.b+1)

	case n == "ZEND_QM_ASSIGN" || n == "ZEND_COPY_TMP":
		// `ZEND_COPY_TMP` duplicates a temporary so the **same evaluated
		// value** can be consumed twice: `$a[id($k)] ??= 1` compiles to a
		// DO_CALL, a COPY_TMP of its result, a FETCH_DIM_IS reading the
		// original and an ASSIGN_DIM using the copy -- which is what makes
		// `id()` run once. In the engine it exists because the first consumer
		// would free the temp; in an SSA IR nothing is freed, so the same
		// value-copy `QM_ASSIGN` performs is the whole of it -- except that
		// **COPY_TMP keeps a reference** where QM_ASSIGN takes the value: the
		// engine duplicates the zval, reference and all, so `foo()["k"] ??= 1`
		// on a `function &foo()` writes into what foo returned. The same SSA
		// value stands for both.
		if n == "ZEND_COPY_TMP" && (op.GetOp1Type() == opTmpVar || op.GetOp1Type() == opVar) {
			if src, live := l.slotValue[op.GetOp1()]; live {
				// Kept across blocks by **binding**, so a reference stays one
				// in the temporary too.
				l.slotValue[op.GetResult()] = src
				if l.crossBlock[op.GetResult()] {
					l.emit("local.set", []string{tempName(op.GetResult()), src.String()}, SeparateAttr)
				}
				break
			}
		}
		a, ok := l.operand(op.GetOp1Type(), op.GetOp1())
		if !ok {
			return l.bail(notLive)
		}
		v := l.fn.newValue("zval")
		l.define(v, "copy", []string{a})
		l.keep(op.GetResult(), v)

	case n == "ZEND_FETCH_CONSTANT":
		// An unqualified constant inside a namespace names *two* constants and
		// PHP takes whichever exists: `INI_ALL` in namespace `App` means
		// `App\INI_ALL` if defined and the global `INI_ALL` otherwise. Same
		// three-literal layout as ZEND_INIT_NS_FCALL_BY_NAME -- namespaced,
		// namespaced, unqualified -- with op2 naming only the first.
		//
		// **op1 == 2048 is the marker**, measured rather than read off a
		// header: a fully qualified `\App\LOCAL` and a constant at global
		// scope both carry op1 == 0 and only two literals, while every
		// unqualified fetch inside a namespace carries 2048 and three.
		k, _ := l.operand(op.GetOp2Type(), op.GetOp2())
		operands := []string{k}
		if op.GetOp1() == 2048 {
			fallback, ok := l.literalAt(op.GetOp2() + 2)
			if !ok {
				return l.bail("namespaced constant has no fallback literal")
			}
			operands = append(operands, fallback)
		}
		v := l.fn.newValue("zval")
		l.define(v, "const.fetch", operands)
		l.keep(op.GetResult(), v)

	case n == "ZEND_TICKS":
		// `declare(ticks=N)`: after each statement, count, and every Nth
		// call the functions `register_tick_function` registered.
		l.emit("tick", []string{strconv.FormatUint(uint64(op.GetExtendedValue()), 10)})

	case n == "ZEND_VERIFY_NEVER_TYPE":
		// A `never` function that reaches its end instead of throwing or
		// exiting. The wording distinguishes a **function** from a **method**,
		// read back from the engine:
		//
		//	nv(): never-returning function must not implicitly return
		//	C::m(): never-returning method must not implicitly return
		// A **terminator**, not an instruction: it always throws, so nothing
		// follows it and the block has no other exit. Emitting it as an
		// instruction left the block with no terminator at all, which the
		// emitter reported as `terminator ` with nothing after it.
		l.term("verify_never")
		return nil

	case n == "ZEND_GET_CALLED_CLASS":
		// `get_called_class()`. It is late static binding asked as a value
		// rather than as a class reference, so it resolves the same way
		// `static::` does and differs only in what it answers with.
		v := l.fn.newValue("zval")
		l.define(v, "called_class", nil)
		l.keep(op.GetResult(), v)

	case n == "ZEND_VERIFY_RETURN_TYPE":
		if op.GetOp1Type() == opUnused {
			// **This is the diagnostic, not a no-op.** An operand-less
			// VERIFY_RETURN_TYPE sits at the implicit end of a function that
			// declares a return type, and it means *execution reached the end
			// without returning*:
			//
			//	function a(): ?int {}   a(): Return value must be of type ?int, none returned
			//
			// It was read as "verify whatever the following `ret` returns" and
			// dropped, so every such function quietly answered NULL. The two
			// are not the same: `return null` from `?int` is legal and falling
			// off the end is not, which is why the engine marks the second.
			l.emit("verify_return.none", nil)
			return nil
		}
		a, ok := l.operand(op.GetOp1Type(), op.GetOp1())
		if !ok {
			return l.bail(notLive)
		}
		v := l.fn.newValue("zval")
		l.define(v, "verify_return", []string{a})
		// The check *coerces* -- `function f(): int { return 1.0; }` widens --
		// so it defines a value, and where that value lands depends on the
		// operand. A TMP/VAR operand is verified in place; a CONST one cannot
		// be, so the engine writes the coerced value to the result slot
		// instead. Handling only the first leaves the following RETURN with
		// nothing to read.
		switch op.GetOp1Type() {
		case opTmpVar, opVar:
			l.keep(op.GetOp1(), v)
		case opCV:
			// A CV is verified in place too, and the RETURN that follows reads
			// the variable rather than this instruction's result. Without the
			// write-back the coercion is computed and dropped, and the function
			// returns the value it was asked to widen.
			//
			// **Separated from a reference it holds**, not written through
			// it: `function f(&$x): string { return $x; }` gives the caller
			// its int back and returns the string, and a by-reference function
			// returns a fresh reference -- measured in
			// return_types/return_reference_separation.
			//
			// **Unless the function returns by reference**: then the
			// reference itself is what goes back, and the caller's variable
			// takes the coerced value -- the same test's third case.
			if oa.GetReturnsRef() {
				l.emit("local.set", []string{"$" + cvName(oa, op.GetOp1()), v.String()})
			} else {
				l.emit("local.set", []string{"$" + cvName(oa, op.GetOp1()), v.String()}, SeparateAttr)
			}
		}
		if l.definesValue() {
			l.keep(op.GetResult(), v)
		}

	case n == "ZEND_MATCH" || n == "ZEND_SWITCH_LONG" || n == "ZEND_SWITCH_STRING":
		// `match ($x) { 1, 2 => ..., "s" => ... }`. op2 is a **jump table**
		// mapping each arm's value to the opline that arm starts at, and
		// extended_value is where to go when nothing matches.
		//
		// The table is not an ordinary PHP array, and that is the whole reason
		// it can be used: measured, `match ($x) { 1 => ..., "1" => ... }` keeps
		// `1` as an int key and `"1"` as a **string** key, where an array
		// literal would coerce the second into the first. `ParseArrayLiteral`
		// preserves the distinction for the same reason -- it reads the text,
		// not PHP's key rules -- so the pair round-trips.
		//
		// `switch` gets the same treatment even though it compares with `==`
		// and `match` with `===`. SWITCH_LONG and SWITCH_STRING are a **fast
		// path**, not the whole statement: the table is consulted only when the
		// subject is exactly that type, and anything else falls to
		// extended_value, which is where the sequential ZEND_CASE chain
		// begins. So identity is right inside the table and the loose
		// comparison still happens, just further along -- `switch (1.0) { case
		// 1: }` misses the int table, reaches the chain, and matches there.
		subject, oks := l.operand(op.GetOp1Type(), op.GetOp1())
		if !oks {
			return l.bail("match subject not live in this block")
		}
		table, okt := l.operand(op.GetOp2Type(), op.GetOp2())
		if !okt {
			return l.bail("match jump table not live in this block")
		}
		entries, okp := ParseArrayLiteral(table)
		if !okp {
			return l.bail("match jump table is not an array literal")
		}
		def := l.blockAt(op.GetExtendedValue())
		succs := []int{def}
		var parts []string
		for _, en := range entries {
			at, convErr := strconv.Atoi(strings.TrimSpace(en.Value))
			if convErr != nil {
				return l.bail("match jump target is not an opline")
			}
			to := l.blockAt(uint32(at))
			succs = append(succs, to)
			key := strconv.Quote(en.StringKey)
			if en.IntKey {
				key = fmt.Sprint(en.IntValue)
			}
			parts = append(parts, fmt.Sprintf("%s => bb%d", key, to))
		}
		l.term(fmt.Sprintf("match %s, [%s] -> bb%d", subject, strings.Join(parts, ", "), def), succs...)

	case n == "ZEND_MATCH_ERROR":
		// No arm matched and the `match` had no default. The subject is what
		// the message names, so it travels rather than being reconstructed.
		a, oka := l.operand(op.GetOp1Type(), op.GetOp1())
		if !oka {
			return l.bail(notLive)
		}
		l.term("match.error " + a)

	case n == "ZEND_DISCARD_EXCEPTION":
		// The head of a `finally` body that **leaves by another route** --
		// `return`, `break`, `continue`. Whatever was in flight is abandoned,
		// because the finally is taking over:
		//
		//	function f() { try { throw new Exception("x"); } finally { return 1; } }
		//
		// returns 1 and throws nothing. The engine keeps the in-flight
		// exception in the FAST_CALL's temporary and this drops it; here the
		// finally is **inlined**, so the unwind copy is the one carrying it and
		// clearing the slot is the whole instruction. On the normal copy there
		// is nothing to clear and it is a no-op.
		//
		// **It names its own finally.** A `return` in a finally nested inside
		// another finally's unwind copy discards the inner one's slot -- empty,
		// the inner try ended normally -- then runs any finally it leaves, and
		// only then the outer one's: a throw in between chains the outer
		// exception as its previous (try/bug70228_3). One slot here, so the
		// discard is dropped when the copy it names is a normal one.
		if rng, ok := l.finallyOfVar[op.GetOp1()]; ok {
			if unwinding, within := l.copyChain[rng]; within && !unwinding {
				break
			}
		}
		l.emit("discard_exception", nil)

	case n == "ZEND_FREE" || n == "ZEND_NOP" || n == "ZEND_EXT_NOP":
		// A temporary the engine drops; the IR has no slot to free.

	case n == "ZEND_RETURN" || n == "ZEND_GENERATOR_RETURN":
		a, ok := l.operand(op.GetOp1Type(), op.GetOp1())
		if !ok {
			return l.bail(notLive)
		}
		l.term("ret " + a)

	case n == "ZEND_THROW":
		a, ok := l.operand(op.GetOp1Type(), op.GetOp1())
		if !ok {
			return l.bail(notLive)
		}
		l.term("throw " + a)

	case n == "ZEND_JMP":
		target := l.blockAt(op.GetOp1())
		l.term(fmt.Sprintf("jmp bb%d", target), target)

	// ---- short-circuit forms, which are branches that also yield a value ----
	//
	// All four share a shape: evaluate op1, and on the short-circuit path put a
	// value in the result slot and jump past the fallback; otherwise fall
	// through to an opline that writes *the same slot*. The slot is therefore
	// defined in two blocks and read in a third, which makes it a place under
	// the rule that already covers cross-block temporaries -- no block argument
	// needed. The dead store left on the fall-through path is what the engine
	// does too, and DCE removes it.
	//
	// What lands in the slot differs, and was settled against the engine rather
	// than read off the opcode names: `0 && $b` is bool(false) and `1 || $b` is
	// bool(true), so JMPZ_EX and JMPNZ_EX *boolify*; but `0 ?? $b` is int(0) and
	// `"a" ?: $b` is string("a"), so COALESCE and JMP_SET copy the value
	// through unchanged.
	case n == "ZEND_JMPZ_EX" || n == "ZEND_JMPNZ_EX":
		c, ok := l.operand(op.GetOp1Type(), op.GetOp1())
		if !ok {
			return l.bail(notLive)
		}
		v := l.fn.newValue("bool")
		l.define(v, "bool", []string{c})
		l.keep(op.GetResult(), v)
		skip := l.blockAt(op.GetOp2())
		if n == "ZEND_JMPZ_EX" {
			l.term(fmt.Sprintf("br %s, bb%d, bb%d", v, l.b+1, skip), l.b+1, skip)
		} else {
			l.term(fmt.Sprintf("br %s, bb%d, bb%d", v, skip, l.b+1), skip, l.b+1)
		}

	case n == "ZEND_COALESCE":
		a, ok := l.operand(op.GetOp1Type(), op.GetOp1())
		if !ok {
			return l.bail(notLive)
		}
		v := l.fn.newValue("zval")
		// `mode=IS`, not a bare copy.
		//
		// Suppression is *intrinsic to the opcode*: the engine compiles
		// `$q ?? 5` to a single ZEND_COALESCE, and reading an undefined `$q`
		// through it emits nothing, where `echo $q` warns. Settled by asking
		// the engine, both halves.
		//
		// Lowering it to an unmarked `copy` split one opcode into a read plus a
		// branch and threw the no-warn property away, leaving a read
		// indistinguishable from `echo $q` -- so a consumer that warns on
		// undefined reads warns where PHP does not. The `mode=` vocabulary
		// already rode on the dim and dynvar operations; this is the same
		// tracking, applied consistently.
		l.define(v, "copy", []string{a}, "mode=IS")
		l.keep(op.GetResult(), v)
		// Null falls through to the right-hand side; anything else keeps the
		// value already in the slot and jumps past it.
		skip := l.blockAt(op.GetOp2())
		set := l.fn.newValue("bool")
		l.define(set, "is_set", []string{v.String()})
		l.term(fmt.Sprintf("br %s, bb%d, bb%d", set, skip, l.b+1), skip, l.b+1)

	case n == "ZEND_JMP_SET":
		a, ok := l.operand(op.GetOp1Type(), op.GetOp1())
		if !ok {
			return l.bail(notLive)
		}
		v := l.fn.newValue("zval")
		l.define(v, "copy", []string{a})
		l.keep(op.GetResult(), v)
		skip := l.blockAt(op.GetOp2())
		l.term(fmt.Sprintf("br %s, bb%d, bb%d", v, skip, l.b+1), skip, l.b+1)

	case n == "ZEND_ASSERT_CHECK":
		// Whether assertions run is a property of the engine's configuration at
		// run time, not of this build, so it stays an intrinsic rather than
		// being folded here. Folding it would bake the harvesting build's
		// zend.assertions setting into a standalone artifact.
		v := l.fn.newValue("bool")
		l.define(v, "assertions_enabled", nil)
		skip := l.blockAt(op.GetOp2())
		l.term(fmt.Sprintf("br %s, bb%d, bb%d", v, l.b+1, skip), l.b+1, skip)

	case n == "ZEND_JMP_FRAMELESS":
		// PHP 8.4 emits a fast path for a handful of internal calls and guards
		// it with this: if the name still resolves to the internal function the
		// compiler had in mind, take the frameless path, otherwise fall through
		// to the ordinary three-phase call. Both arms write the same result
		// slot, so the merge is the same cross-block place as above.
		name, _ := l.operand(op.GetOp1Type(), op.GetOp1())
		v := l.fn.newValue("bool")
		l.define(v, "frameless_available", []string{name})
		fast := l.blockAt(op.GetOp2())
		l.term(fmt.Sprintf("br %s, bb%d, bb%d", v, fast, l.b+1), fast, l.b+1)

	case strings.HasPrefix(n, "ZEND_FRAMELESS_ICALL_"):
		callee, known := l.framelessName(op.GetExtendedValue())
		if !known {
			return l.bail("frameless function index out of range")
		}
		args, err := l.framelessArgs(n)
		if err != nil {
			return err
		}
		v := l.fn.newValue("zval")
		l.blk.Insts = append(l.blk.Insts, Inst{
			Result: &v, Opcode: "call.fn", Operands: []string{callee},
			ArgList: fmt.Sprintf("(%s)", strings.Join(args, ", ")),
			Attrs:   []string{"frameless"},
		})
		if l.definesValue() {
			l.keep(op.GetResult(), v)
		}

	case n == "ZEND_JMPZ" || n == "ZEND_JMPNZ":
		c, ok := l.operand(op.GetOp1Type(), op.GetOp1())
		if !ok {
			return l.bail(notLive)
		}
		target := l.blockAt(op.GetOp2())
		fall := l.b + 1
		if n == "ZEND_JMPZ" {
			l.term(fmt.Sprintf("br %s, bb%d, bb%d", c, fall, target), fall, target)
		} else {
			l.term(fmt.Sprintf("br %s, bb%d, bb%d", c, target, fall), target, fall)
		}

	default:
		return l.bail("not lowered")
	}
	return nil
}

// place defines the place a container access denotes. An append -- `$a[] = v`
// -- carries no key operand at all, and it is a place *kind* of its own rather
// than a dim.ref with a missing key: it addresses the next free slot, which is
// not a value any expression can name. Rendering it as `dim.ref $a, ` would
// also emit an empty operand, which the assembly grammar has no term for.
func (l *lowering) place(kind, container, key string, keyType uint32, mode string, extra ...string) Value {
	v := l.fn.newValue("place")
	if keyType == opUnused && kind == "dim" {
		l.define(v, "dim.append.ref", []string{container}, "mode="+mode)
		return v
	}
	name := kind + ".ref"
	if kind == "dim" && (mode == "RW" || mode == "UNSET") {
		name = "dim.ref." + strings.ToLower(mode)
		// **Before the key.** The warning is its own instruction so it lands
		// ahead of the key's read, which ExplicitReads hoists in front of the
		// fetch: `$a[$b] += 1` warns about `$a` and then about `$b`.
		if spelled, named := readableVar(container); named {
			l.emit("warn.undefined_container", []string{container, strconv.Quote(spelled)})
		}
	}
	if kind == "prop" && mode == "UNSET" {
		// An unset fetch has nothing to refuse about an uninitialized property,
		// which the write fetch does refuse -- so it is its own operation, the
		// same split the dimension forms make.
		name = "prop.ref.unset"
	}
	if mode == "RW" && kind == "prop" {
		// **A distinct operation**, not a mode on the one below: the RW fetch
		// reads its container with the ordinary diagnostics where the write
		// fetch is silent, and the mode does not survive far enough downstream
		// to say so. `dim.ref` has the same split waiting -- see TODO -- and is
		// left alone until its two warnings are modelled.
		name = "prop.ref.rw"
	}
	l.define(v, name, []string{container, key}, append([]string{"mode=" + mode}, extra...)...)
	return v
}

// literalAt renders a literal by index, reporting whether that index exists.
// Opcodes that reach past the literal their operand names -- a namespaced call
// reads op2+2 -- must not silently render literalText's "?" placeholder as a
// function name.
func (l *lowering) literalAt(idx uint32) (string, bool) {
	if int(idx) >= len(l.oa.GetLiterals()) {
		return "", false
	}
	return literalText(l.oa, idx), true
}

// framelessName resolves a ZEND_FRAMELESS_ICALL_*'s extended_value against the
// harvested table.
func (l *lowering) framelessName(idx uint32) (string, bool) {
	if int(idx) >= len(l.cfg.Frameless) {
		return "", false
	}
	return fmt.Sprintf("%q", l.cfg.Frameless[idx]), true
}

// framelessArgs collects a frameless call's arguments. The arity is in the
// opcode's own name, and an opline holds two operands -- so the three-argument
// form spills its third into an OP_DATA, exactly like the assignment opcodes.
func (l *lowering) framelessArgs(opcode string) ([]string, error) {
	arity := opcode[len(opcode)-1] - '0'
	if arity > 3 {
		return nil, l.bail("unmodelled frameless arity")
	}
	var args []string
	if arity >= 1 {
		a, ok := l.operand(l.op.GetOp1Type(), l.op.GetOp1())
		if !ok {
			return nil, l.bail("argument not live in this block")
		}
		args = append(args, "byval "+a)
	}
	if arity >= 2 {
		a, ok := l.operand(l.op.GetOp2Type(), l.op.GetOp2())
		if !ok {
			return nil, l.bail("argument not live in this block")
		}
		args = append(args, "byval "+a)
	}
	if arity >= 3 {
		data, err := l.opData()
		if err != nil {
			return nil, err
		}
		a, ok := l.operand(data.GetOp1Type(), data.GetOp1())
		if !ok {
			return nil, l.bail("OP_DATA operand not live in this block")
		}
		args = append(args, "byval "+a)
	}
	return args, nil
}

// opData returns the ZEND_OP_DATA pseudo-opline carrying a third operand, and
// marks it consumed so the block loop does not lower it on its own.
func (l *lowering) opData() (*pb.Opline, error) {
	// Bounded by the opline stream, not by the block. An OP_DATA is glued to
	// the opline before it in the op_array and is never a jump target, so the
	// block it lands in is irrelevant -- and inlining a finally body splices
	// oplines from outside [start, end] into this block, where a block-relative
	// bound rejects an OP_DATA that is sitting right there. That was five
	// CakePHP bodies bailing on `$this->x = y` inside a `finally`.
	if l.i+1 >= len(l.oplines) || l.name(l.i+1) != "ZEND_OP_DATA" {
		return nil, l.bail("no OP_DATA follows")
	}
	l.skipNext = true
	return l.oplines[l.i+1], nil
}

// intrinsicName turns an opcode name into the intrinsic it lowers to.
func intrinsicName(opcode string) string {
	return strings.ToLower(strings.TrimPrefix(opcode, "ZEND_"))
}

// fetchScope says which symbol table a FETCH_* names.
//
// The FETCH family is one opcode for two questions -- `$$n` names a *local* and
// `$GLOBALS["n"]` names a **global** -- and only `extended_value` tells them
// apart. Read back from the engine rather than from the headers, by harvesting
// both shapes in one function and comparing:
//
//	@$u              ZEND_FETCH_R CONST UNUSED TMP  4     local
//	$$n              ZEND_FETCH_R CV    UNUSED TMP  4     local
//	$GLOBALS["g"]    ZEND_FETCH_R CONST UNUSED TMP  2     global
//	$GLOBALS["h"]=1  ZEND_FETCH_W CONST UNUSED VAR  2     global
//
// Dropping it made `$GLOBALS["g"]` inside a function read the *local* `$g` --
// a wrong answer rather than a refusal, and one that reads as an undefined
// variable warning about the right name in the wrong table.
func fetchScope(op *pb.Opline) string {
	if op.GetExtendedValue()&fetchGlobal != 0 {
		return "scope=global"
	}
	return "scope=local"
}

// fetchGlobal is the extended_value bits that mark a global fetch, measured as
// above. The local form carries 4 and the global one 2 -- and `global $$n`,
// whose first FETCH_W names the global it then binds the local to, carries 8:
//
//	global ${sf("a")}  ZEND_FETCH_W VAR UNUSED VAR  8     global
//	                   ZEND_FETCH_W VAR UNUSED VAR  4     local
//
// Reading only 2 bound the local to itself and left the global untouched.
const fetchGlobal = 2 | 8

// fetchClass is the class an opcode names, whether spelled out or implied.
//
// Zend puts the class in a CONST operand when the source named it, and in an
// **UNUSED operand carrying a fetch-class code** when the source wrote `self`,
// `parent` or `static`. The three codes were read back from the engine rather
// than decoded from the headers, per AGENTS.md:
//
//	self -> 513   parent -> 514   static -> 515
//
// An unrecognised code bails rather than guessing. Guessing here is how
// `static::NAME` came to lower as `self`, which returns the *declaring* class's
// constant instead of the called one -- `D::who()` gave "see" for "dee".
func (l *lowering) fetchClass(kind uint32, value uint32) (string, bool) {
	if kind != opUnused {
		c, ok := l.operand(kind, value)
		return c, ok
	}
	// The **low bits are the class type**; the rest are flags. `FETCH_CLASS`
	// carries `512|type` -- 512 is the exception flag -- while
	// `FETCH_CLASS_NAME` carries the bare type: `static::class` arrives as
	// `op1=0:3`, observed with `php2lower -trace` rather than guessed. Matching
	// the flagged spellings alone made `static::class` an unrecognised class
	// reference, so late static binding did not compile at all.
	switch value & 0x0F {
	case 1:
		return "self", true
	case 2:
		return "parent", true
	case 3:
		return "static", true
	}
	return "", false
}

// regionCaughtBy is the region whose catch clause starts at this block, or -1.
//
// A catch handler is not *in* the region it protects -- that is what lets
// `catch (E) { throw; }` escape rather than re-entering its own clause -- so
// the only way back to the region's finally is by asking which region names
// this block as its catch.
func (l *lowering) regionCaughtBy(block int) int {
	for i := range l.fn.Regions {
		if l.fn.Regions[i].Catch == block {
			return i
		}
	}
	return -1
}

// receiverOperand renders the object a member access is against.
//
// **An unused op1 is `$this`.** `$this->p` and `$this->p += 1` inside a method
// leave the receiver operand UNUSED, and `l.operand` renders that as the empty
// string -- a place with one operand where the catalogue declares three, which
// the emitter reports as a complaint about the *next* argument.
//
// It is a helper rather than four copies because it has already been forgotten
// twice: once in the compound-assignment arm, which built its place by hand,
// and once in the append form of the same arm. A rule with more than one site
// keeps being applied at all but one of them.
func (l *lowering) receiverOperand(kind, idx uint32) (string, bool) {
	if kind == opUnused {
		return "$this", true
	}
	return l.operand(kind, idx)
}

// sensitiveAttribute is the attribute that keeps a parameter's value out of a
// backtrace. The engine records it on the parameter, and the leading `\` is
// how a fully-qualified name arrives.
const sensitiveAttribute = "SensitiveParameter"

// declaresSensitive reports whether a parameter carries it.
func declaresSensitive(arg *pb.ArgInfo) bool {
	for _, attribute := range arg.GetAttributes() {
		if strings.EqualFold(strings.TrimPrefix(attribute.GetName(), "\\"), sensitiveAttribute) {
			return true
		}
	}
	return false
}

// settleSends turns an undecided send of a local into a by-value one when the
// callee is known and takes that argument by value.
//
// The engine compiles `Ack($m, ...)` inside `Ack` before `Ack` is declared, so
// the send is ZEND_SEND_VAR_EX -- by-reference-ness left to the callee. Left
// that way the argument names the place, and a place any call argument names
// is never promoted: `$m` stayed a slot read through ReadVar, its comparison
// and arithmetic stayed generic, and an int version could not assume it. The
// emitter already reads such a send as a plain by-value read once it has
// resolved the callee; this settles it at the point promotion can use it.
//
// Only a direct `call.fn` with no namespace fallback, no named and no unpacked
// argument -- the same conditions the emitter's own settling uses, because
// either can move a position.
// snapshotSends takes a by-value argument's value **where it was sent**.
//
// An argument that names a variable is carried to the call as the variable,
// and read there -- after every later argument has been evaluated. The engine
// copies it at the send: `f($a, $a = 1)` passes the old `$a`, `f($i, $i++)`
// passes 0, `f($x, g($x))` with `g(&$r)` passes `$x` from before `g` wrote it.
// So when anything after the send in the same block may write the variable,
// a copy is taken at the send's position and sent instead. Only then: the
// variable itself is what the int paths and ownership read, and a copy for
// every argument would cost them their facts.
func (l *lowering) snapshotSends(call *pendingCall) {
	if len(call.sentAt) == 0 {
		return
	}
	positions := make([]int, 0, len(call.sentAt))
	for i := range call.sentAt {
		positions = append(positions, i)
	}
	// Latest send first, so inserting a copy does not move an earlier one.
	sort.Slice(positions, func(a, b int) bool {
		return call.sentAt[positions[a]][1] > call.sentAt[positions[b]][1]
	})
	for _, i := range positions {
		at := call.sentAt[i]
		if at[0] != l.b || i >= len(call.args) || at[1] > len(l.blk.Insts) {
			continue
		}
		name, ok := strings.CutPrefix(call.args[i], "byval ")
		if !ok || !strings.HasPrefix(name, "$") {
			continue
		}
		written := false
		for _, in := range l.blk.Insts[at[1]:] {
			if mayWriteVar(in, name) {
				written = true
				break
			}
		}
		if !written {
			continue
		}
		v := l.fn.newValue("zval")
		copyInst := Inst{Result: &v, Opcode: "copy", Operands: []string{name}, Attrs: []string{ShareAttr}}
		insts := l.blk.Insts
		insts = append(insts[:at[1]], append([]Inst{copyInst}, insts[at[1]:]...)...)
		l.blk.Insts = insts
		call.args[i] = "byval " + v.String()
	}
}

// mayWriteVar reports whether an instruction may change a variable's value:
// it names the variable as its first operand and is not one of the reads, or
// it passes the variable where a reference may be taken. Erring towards yes
// costs a copy, never an answer.
func mayWriteVar(in Inst, name string) bool {
	if len(in.Operands) > 0 && strings.TrimSpace(in.Operands[0]) == name {
		switch in.Opcode {
		case "read_var", "read_var.silent", "local.get", "copy", "echo", "dim.get", "prop.get",
			"is_set", "isset", "empty", "type_check", "count", "strlen":
		default:
			return true
		}
	}
	if in.ArgList != "" {
		for _, a := range splitArgList(in.ArgList) {
			for _, mode := range []string{"byref ", "preferref "} {
				if rest, ok := strings.CutPrefix(a, mode); ok {
					if target, _, _ := strings.Cut(rest, " as "); strings.TrimSpace(target) == name {
						return true
					}
				}
			}
		}
	}
	return false
}

// splitArgList splits a rendered argument list, `(byval $a, byref $b)`, into
// its items.
func splitArgList(list string) []string {
	list = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(list), "("), ")")
	if strings.TrimSpace(list) == "" {
		return nil
	}
	items := strings.Split(list, ",")
	for i := range items {
		items[i] = strings.TrimSpace(items[i])
	}
	return items
}

// noRefSends puts a `no_ref_arg` in front of each ZEND_SEND_VAR_NO_REF
// argument that reaches a by-reference parameter. The compiler emits that send
// for a value it could not prove is a variable -- a call result, usually --
// and the engine then notices `Only variables should be passed by reference`
// and hands the callee something nobody else can see: `array_splice(
// C::get(), 1, 1)` must not splice the array the getter returned.
//
// The plain form means the compiler knew the parameter is by reference. The
// _EX form means it did not, and is settled here only for a function the unit
// declares; an unresolved callee keeps the old behaviour, silent.
func (l *lowering) noRefSends(call *pendingCall) {
	positions := make([]int, 0, len(call.noRef))
	for i := range call.noRef {
		positions = append(positions, i)
	}
	sort.Ints(positions)
	for _, i := range positions {
		ex := call.noRef[i]
		if i >= len(call.args) {
			continue
		}
		rest, ok := strings.CutPrefix(call.args[i], "preferref ")
		if !ok || !strings.HasPrefix(rest, "%") {
			continue
		}
		if ex {
			if l.cfg.ByRefArg == nil || call.opcode != "call.fn" || call.fallback != "" || call.callee == "" {
				continue
			}
			callee, err := strconv.Unquote(call.callee)
			if err != nil || !l.cfg.ByRefArg(callee, i) {
				continue
			}
		}
		v := l.fn.newValue("zval")
		l.define(v, "no_ref_arg", []string{rest})
		call.args[i] = "preferref " + v.String()
	}
}

// funcArgByValue reports whether the argument about to be sent to the
// innermost pending call is a known by-value parameter: a function the unit
// declares, named literally, with no named or unpacked argument before it.
func (l *lowering) funcArgByValue() bool {
	if l.cfg.ByValueArg == nil || len(l.calls) == 0 || l.namedFuncArg {
		return false
	}
	call := l.calls[len(l.calls)-1]
	if call.opcode != "call.fn" || call.fallback != "" || call.callee == "" {
		return false
	}
	callee, err := strconv.Unquote(call.callee)
	if err != nil {
		return false
	}
	for _, a := range call.args {
		if strings.HasPrefix(a, "unpack ") || strings.Contains(a, " as ") {
			return false
		}
	}
	return l.cfg.ByValueArg(callee, len(call.args))
}

func (l *lowering) settleSends(call *pendingCall) {
	if l.cfg.ByValueArg == nil || call.opcode != "call.fn" || call.fallback != "" || call.callee == "" {
		return
	}
	callee, err := strconv.Unquote(call.callee)
	if err != nil {
		return
	}
	for _, a := range call.args {
		if strings.HasPrefix(a, "unpack ") || strings.Contains(a, " as ") {
			return
		}
	}
	for i, a := range call.args {
		rest, undecided := strings.CutPrefix(a, "preferref ")
		if !undecided || !strings.HasPrefix(rest, "$") || strings.HasPrefix(rest, "$__") {
			continue
		}
		if l.cfg.ByValueArg(callee, i) {
			call.args[i] = "byval " + rest
		}
	}
}
