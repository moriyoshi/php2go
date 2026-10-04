package lower

import (
	"fmt"
	"strings"

	pb "github.com/moriyoshi/php2go/internal/ir/irpb"
)

// Value is an SSA value produced by one instruction and named by its index.
// Type is a representation type -- `zval`, `*arr`, `*str`, `*obj`, `place`,
// `i64`, `bool` -- never a PHP type. IR-DESIGN.md keeps those apart
// deliberately: PHP types are an analysis fact attached to values, and never
// appear in the IR's own type system.
type Value struct {
	N    int
	Type string
}

func (v Value) String() string { return fmt.Sprintf("%%%d", v.N) }

// StaticVar is one `static $x = ...` declaration: the name, and the initializer
// as IR operand text. `HasDefault` is false for `static $x;`, which starts null
// and is not the same as defaulting to null -- the engine records the two
// separately.
type StaticVar struct {
	Name       string
	Default    string
	HasDefault bool
}

// Inst is one IR instruction.
type Inst struct {
	// Result is nil for an instruction evaluated only for its effects.
	Result   *Value
	Opcode   string
	Operands []string
	// ArgList is a call's parenthesised argument list, kept apart from Operands
	// so it attaches directly to the callee: `call.fn "f"(byval %1)` rather than
	// `call.fn "f", (byval %1)`.
	ArgList string
	Attrs   []string
}

// ParamIsByRef reports whether a `.params` entry declares a by-reference
// parameter. The entry is positional text -- `0 &$var "int"` -- and this is the
// one place that knows the spelling.
func ParamIsByRef(param string) bool {
	fields := strings.SplitN(strings.TrimSpace(param), " ", 3)
	return len(fields) >= 2 && strings.HasPrefix(fields[1], "&")
}

// Directive reports whether this entry is a pseudo-instruction carrying a
// source position rather than an operation. `.line` is the only one, and it
// occupies a slot in the stream so that a position lands exactly where the
// engine put it. Anything counting or emitting instructions must skip these.
func (in Inst) Directive() bool { return in.Opcode == lineDirective }

// Block is a basic block. Values defined here and read elsewhere are not block
// arguments: IR-DESIGN.md's rule is that a value outliving its block becomes a
// place, and mem2reg promotes it later.
type Block struct {
	// Handler marks a block that is part of an **inlined handler body** -- a
	// copy of a `finally`. An exception raised inside a handler is not handled
	// by that same handler: it goes to the enclosing region. The region model
	// cannot say this on its own, because a block only names the region it sits
	// in, and a finally body's oplines sit inside the very region whose handler
	// they are. Without the mark, routing a `throw` to `handlerFor` sent it
	// back into the finally it was raised in, and the generated program looped
	// for ever on `try { throw } finally { throw }`.
	Handler bool
	// Unwind marks the handler copy on the **exceptional** path. A finally is
	// inlined at every exit, and only this copy runs with an exception in
	// flight -- which is the copy where a `throw` has one to chain onto. A
	// finally copied at a normal exit *inside* such a copy is marked too: the
	// exception is still in flight while it runs.
	Unwind bool
	// InFinallyOf are the regions whose **finally body** this block's oplines
	// sit inside, innermost last.
	//
	// **Not the same as Region**, and the difference is the whole reason it
	// exists. `Region` is the innermost try the oplines are protected by; this
	// is which finally *bodies* they are part of. An exception raised here
	// cannot be handled by any of them -- a finally does not catch what it
	// throws, and neither does one it is nested inside:
	//
	//	try { throw } finally { try { return 42; } finally { throw } }
	//
	// The innermost `throw` is inside two finally bodies, and routing it to
	// either made the two copies name each other; the emitted program looped
	// for ever.
	//
	// Computed from opline positions, so a copy gets the same answer as the
	// body it was copied from and nothing has to be threaded through the
	// inlining.
	InFinallyOf []int
	// InCatchBodyOf is the region whose **catch body** these oplines sit in,
	// stored as region+1 so the zero value means none. A catch body is not in
	// the region it belongs to -- `catch (E) { throw; }` must not re-enter its
	// own clause -- but the region's `finally` still runs on the way out, and
	// nothing else in the block model could say that: `Region` is the enclosing
	// try, which for a catch body is the *outer* one or none at all.
	//
	// Without it, `try { throw; } catch (E $e) { throw $e; } finally { ... }`
	// left the function without running the finally, and a finally that
	// `return`s -- which discards the exception in flight -- never got the
	// chance to.
	InCatchBodyOf int
	ID            int
	Params        []string
	// Region is the innermost enclosing try region, or -1.
	Region int
	Insts  []Inst
	Term   string
	// Succs are the blocks control can reach from this one, in the order the
	// terminator names them. Empty for `ret` and `throw`. Exception edges are
	// not listed: they are region membership, not successors.
	Succs []int
	// Dead marks a block that is never emitted standalone because it exists
	// only as inlined copies. Finally bodies are the only case: IR-DESIGN.md
	// inlines them at each exit rather than treating them as a subroutine, so
	// FAST_RET's indirect jump disappears and every CFG stays static.
	Dead bool
}

// Function is one lowered op_array.
type Function struct {
	Name string
	File string
	// LineStart is the line the body was declared on. It is output rather than
	// bookkeeping: `var_dump` of a closure prints `file` and `line`, and the
	// engine's `{closure:FILE:LINE}` name is built from the same pair.
	LineStart uint32
	// LineEnd and DocComment are what the reflectors' __toString prints
	// (`@@ file start - end`, and the doc comment before the header).
	LineEnd    uint32
	DocComment string
	// Final and Abstract are a method's own modifiers, for reflection.
	Final, Abstract bool
	// VarNames are the compiled variables in the engine's order, which is
	// the order the global table lists a script's variables in.
	VarNames  []string
	Params    []string
	ParamInfo []FunctionParameter
	// LiveParams are the parameters the body writes after binding them, by
	// place, answering the argument position (LiveParams). They keep a slot
	// of their own so the frame can read them where a trace is taken.
	LiveParams map[string]int
	// Sensitive marks the parameters carrying `#[\SensitiveParameter]`, whose
	// value a backtrace must not show. It is a property of the **declaration**,
	// so it travels with the body rather than with the call, and `Params` has
	// nowhere to put an attribute.
	Sensitive []bool
	// Literals are the harvested literals no operand text could stand for -- a
	// deferred constant expression, which the engine keeps as an AST because it
	// names something that may not exist yet. An operand spells `<lit:N>` and
	// indexes here. See the lowering's constText.
	Literals []*pb.Literal
	// RequiredParams is how many leading parameters have no default. A
	// closure's debug info prints `<required>` or `<optional>` per parameter,
	// and that is the only place the distinction is observable, so it cannot be
	// recovered from `Params` -- which carries the type, not the default.
	RequiredParams int
	Returns        string
	// Static marks `static function () {}` -- a closure declared static, which
	// binds no `$this` and refuses `bindTo` with an instance. It is the same
	// engine flag a static method carries.
	Static bool
	// UsesThis marks a body that reads `$this` -- the engine's
	// ZEND_ACC_USES_THIS -- which a closure cannot be unbound from.
	UsesThis bool
	// ReturnsRef marks `function &f()`, and a `&get` property hook, which the
	// engine compiles as the same flag. It decides whether a write can reach
	// *through* the result: a `get` hook reached as storage is
	// `Indirect modification of C::$p is not allowed`, and a `&get` hook hands
	// back real storage instead.
	ReturnsRef bool
	// StrictTypes is whether the file this body was compiled from declared
	// `strict_types=1`. It changes what a declared type *does*: weak mode
	// coerces, strict mode only widens int to float and throws otherwise. The
	// engine harvests it per op_array, and a consumer that drops it coerces
	// where PHP throws.
	StrictTypes bool
	// Statics are `static $x = 5;` -- one slot per **function definition**,
	// living across calls. Measured: a method's static is shared by every
	// instance and by a subclass that inherits the method, which is what makes
	// the definition the right key rather than the object or the class.
	Statics []StaticVar
	// Visibility is the method's declared modifier -- "public", "protected" or
	// "private" -- and is empty for anything that is not a method. It travels
	// with the body because the refusal it drives is about the *declaration*:
	// `Call to private method A::p()` names the class that wrote `private`,
	// whichever subclass the receiver happens to be.
	Visibility string
	// Deprecated is PHP 8.4's `#[\Deprecated]` on this function or method, and
	// DeprecatedMessage the attribute's optional reason. **Using** such a
	// function emits `E_USER_DEPRECATED` at the *call site*, so this travels
	// with the callee and is reported by it.
	Deprecated        bool
	DeprecatedMessage string
	// DeprecatedSince is the attribute's `since:` argument, which the engine
	// renders before the message: `is deprecated since 2.0, use b2()`.
	DeprecatedSince string
	// DeprecatedMessageExpr and DeprecatedSinceExpr are the same two arguments
	// as the harvest kept them, for the case the text form cannot carry: a
	// **deferred constant expression**. `#[\Deprecated(SOME_CONST)]` is legal
	// and the constant may be defined after the function, so it can only be
	// evaluated where the deprecation fires. The text form stays for the
	// common literal case; these are consulted when it is empty.
	DeprecatedMessageExpr *pb.Literal
	DeprecatedSinceExpr   *pb.Literal
	Blocks                []*Block
	Regions               []Region
	// Entry is the block control starts at. Normally bb0; a resumable function
	// starts at its dispatch instead, which is appended rather than inserted so
	// that a block's index still equals its ID.
	Entry int
	// States are the dispatch targets of a resumable function, indexed by state
	// number: 0 is the ordinary entry, the last is the terminal state, and the
	// rest are continuations. Empty for a function that does not suspend.
	States []int
	// Terminal is the state a finished body leaves behind, and the index in
	// States of the block that answers for it. Re-entering there returns the
	// recorded value again instead of resuming the last continuation.
	//
	// It is a state but *not* a continuation: it reads no resumed value, because
	// nothing resumes a finished generator. Anything walking States looking for
	// continuations has to stop before it.
	//
	// Zero for a function that does not suspend.
	Terminal int
	// Frame is the state a resumable function carries between entries. Empty
	// for a function that does not suspend.
	Frame []FrameSlot
	// SuspendsToCaller records that a suspension in this function propagates to
	// whoever called it, rather than being mediated by a driver.
	//
	// Set by `Fiber::suspend()`, which suspends the current fiber from any
	// depth. Not set by `yield`, whose suspension is mediated: calling a
	// generator returns a Generator object.
	//
	// It marks a decision this pass deliberately does not make — what the
	// *caller* must be compiled as. The emitter rule that follows has two parts,
	// and stating only the first is a completeness hole the runtime session
	// found: **26 of 95 marked bodies in `Zend/tests` are also generators**, a
	// body that both yields and suspends.
	//
	//  1. Never emit a direct call to a function carrying this.
	//  2. Never emit a *driver* call — `->next()`, `->send()`, `->resume()` —
	//     on an object whose body carries it. For a body that both yields and
	//     suspends, nothing calls it directly (calling it makes a Generator), so
	//     rule 1 never fires and the suspension escapes through the driver call
	//     instead.
	//
	// Rule 2 needs the receiver's class, which is what `InferReceivers` supplies.
	SuspendsToCaller bool
	// ConfinedNew names the `new` instructions whose object provably does not
	// outlive this frame, keyed by the instruction's result value. Such an
	// object can be destroyed when the function returns, which is not an
	// approximation: PHP destroys a local when the frame is destroyed.
	//
	// Computed by ConfinedObjects and set by the compile path, not by lowering
	// -- it is a choice about the emitter, like the CPS rewrite.
	ConfinedNew map[string]bool
	// CatchReleases are the catch places (`$__excN`) whose exception the body
	// keeps to itself (lower.CatchFrameReleases): it dies when the frame
	// does, as the engine frees a frame's compiled variables.
	CatchReleases []string
	// FrameBound names the SSA values that are the promoted form of a
	// user-visible local. Such a value lives until the frame ends, however few
	// uses survive promotion -- which is what separates `$c = clone $o; f($c);`
	// from the temporary in `f(clone $o)` once the slot is gone.
	//
	// Set by Mem2Reg, because promotion is what erases the distinction.
	FrameBound map[string]bool
	// ReassignReleases names the locals whose previous object may be destroyed
	// when the local is written again -- `$obj = new B()` destroying what `$obj`
	// held. Only locals this body provably owns outright.
	//
	// Computed by ReassignedLocals and set by the compile path.
	ReassignReleases map[string]bool
	// DiscardedNew names the `new` instructions whose object is used by nothing
	// beyond its own constructor -- `new foo;` as a bare statement. It dies the
	// moment that constructor returns, in any body including `{main}`.
	//
	// Computed by DiscardedObjects and set by the compile path.
	DiscardedNew map[string]bool
	// DiscardedResults names the call and `clone` results nothing reads that
	// are a fresh value -- `make();` as a statement, where `make` returns an
	// object nobody else holds. Like a DiscardedNew object it dies at the end
	// of its statement (DiscardedResultsOf).
	DiscardedResults map[string]bool
	// LoopOwnedNew names the `new` instructions whose object a `foreach`
	// holds alone, so it dies at that loop's `iter.free.owned`.
	//
	// Computed by LoopOwnedObjects and set by the compile path.
	LoopOwnedNew map[string]bool
	// DisposableNew is the stricter question ConfinedNew does not answer: which
	// `new` results are dead the **moment the call consuming them returns**.
	// The engine reuses the lowest free object handle, so a temporary that is
	// dumped and dropped frees its number for the next one -- and the handle is
	// printed, so getting it wrong is a wrong answer rather than a slow one.
	//
	// Keyed by the value that reaches the consuming call, which is not always
	// the object itself: `var_dump([new P(), new P()])` sends an array, and all
	// three die together.
	//
	// Computed by DisposableTemporaries and set by the compile path, for the
	// same reason ConfinedNew is.
	DisposableNew map[string][]string
	// ArrayAddTemps are the fresh objects inside a fresh array literal that
	// one array `+` consumes, keyed by the sum: `[0] + [new C]` and
	// `$a += [new C]` free the literal once the sum is made -- and the C
	// with it, unless the sum took it in (temporary_cleaning_013).
	//
	// Computed by ArrayAddTemporaries and set by the compile path.
	ArrayAddTemps map[string]ArrayAddRelease
	// LiveOnError maps an instruction position ("block.index") to the claimed
	// temporaries alive there that an exception raised by that instruction
	// frees on its way out, as the engine's live ranges do.
	//
	// Computed by LiveOnError and set by the compile path.
	LiveOnError map[string][]string
	// PlaceTemps are the fresh receivers a read-write fetch goes through,
	// keyed by the place value, released after the place's last use.
	//
	// Computed by PlaceTemporaries and set by the compile path.
	PlaceTemps map[string]PlaceRelease
	// OverwriteReleases are the element and property stores whose replaced
	// value dies right after them, keyed by the store's receiver
	// (OverwriteOwners).
	OverwriteReleases map[string]OverwriteOwner
	// RebindReleases are the locals whose object dies at the `=&` that
	// rebinds them, with the `new` results they held (RebindOwners).
	RebindReleases map[string][]string
	// SiteReleaseAt are the stores whose replaced value is proved owned by
	// the interval since the local's last write in the same block, keyed by
	// position ("block.index"; SiteReleases).
	SiteReleaseAt map[string]SiteRelease
	// ClosureCallees maps a `read_var` result to the one closure literal it
	// can hold (ClosureCallees).
	ClosureCallees map[string]string
	// RejectableNew names the `new` results handed to one store -- a property,
	// a static property, a checked return -- and to nothing else. If the store
	// refuses the object before any code of the program saw it, nothing holds
	// it and it dies right there, before the refusal travels.
	//
	// Computed by RejectableObjects and set by the compile path.
	RejectableNew map[string]bool
	// OwnedParams are the parameter positions whose argument this body may
	// take over from a caller that hands it a fresh object nothing else
	// holds (php.Ctx.HandOver): by value, never written, not retained past
	// the call (RetainedParams), in a program where no trace outlives its
	// frame (TracesDieWithFrames). Such an argument dies among this
	// frame's variables when it returns, before its locals -- the engine
	// frees compiled variables in order, parameters first.
	//
	// Set by the compile path.
	OwnedParams map[int]bool
	// SoleUseClosure names the closure values a callee may take over: created
	// for one call, sent by value, mentioned nowhere else. Computed by
	// SoleUseClosures and set by the compile path.
	SoleUseClosure map[string]bool
	// SharedLiterals are the inline array literals the emitter may build once
	// and share, keyed "block.index|text" (OwnershipTrace.SharedLiterals).
	SharedLiterals map[string]bool
	// SharedCopies are the `copy` results the emitter may take without
	// copying (OwnershipTrace.SharedCopies).
	SharedCopies map[string]bool
	// SharedStores are the `write_var` stores, keyed "block.index", that
	// need not copy (OwnershipTrace.SharedStores).
	SharedStores map[string]bool
	// ReleasableParams are the by-value parameter positions whose share on
	// the caller's array is handed back at exit
	// (OwnershipTrace.ParamReleasable).
	ReleasableParams map[int]bool
	// ReleaseAfter are the promoted copies whose share is handed back after
	// the instruction keyed "block.index" (OwnershipTrace.ReleaseAfter).
	ReleaseAfter map[string][]string
	// AssumedInt marks the parameters this body may take to hold ints,
	// because it is a version reached only after an entry test proved it
	// (IntVersions). The inference reads it as a declared `int`.
	AssumedInt map[int]bool
	// IntVersion is this body's int-assuming version, if IntVersions made one.
	// It is emitted beside the body, not registered as a function of its own.
	IntVersion *Function

	next int
}

// FunctionParameter retains the parts of a source parameter that a call site
// needs even when it is not being compiled with the callee's body: its PHP
// name and the two calling-convention flags.
type FunctionParameter struct {
	Name            string
	ByRef, Variadic bool
}

// FrameSlot is one entry in a resumable function's frame.
//
// A resumable function is entered many times, so whatever outlives a suspension
// cannot be a Go local. That set is exactly the function's **places**: no SSA
// value is used outside the block defining it, and a suspension is a block
// terminator, so nothing live at one is a value. There is no liveness to
// compute -- the invariant does the work, and `php2lower -verify` checks it.
type FrameSlot struct {
	// Name is the place as the IR spells it: `$x` for a compiled variable,
	// `$__tN` for a cross-block temporary, `$__iterN` for a foreach iterator,
	// `$__cN` for an operand of a call that spans blocks.
	Name string
	Type string
	// Compiler marks a slot the program cannot name. Those are candidates for
	// narrowing by liveness; the program's own variables are not, because PHP
	// keeps a generator's locals alive until the generator dies and their
	// destructors are ordered by that.
	Compiler bool
}

// Region is a protected range: a `try` and the handlers an exception in it can
// reach. IR-DESIGN.md models exceptions as regions rather than as LLVM-style
// `invoke` edges out of every call, so these are not successors -- but any
// analysis that must be sound about control flow has to fold them in, which is
// why they are structured rather than rendered straight to text.
type Region struct {
	// Catch and Finally are the blocks an exception reaches, or -1. A `try`
	// with only a `finally` has no catch: the engine leaves catch_op at 0, and
	// opline 0 can never be a handler, so absence is unambiguous.
	Catch   int
	Finally int
	// Parent is the enclosing region, or -1. An exception not handled here goes
	// outward, so a conservative CFG follows this chain.
	Parent int
}

func (r Region) String(id int) string {
	out := fmt.Sprintf("region r%d {", id)
	var parts []string
	if r.Catch >= 0 {
		parts = append(parts, fmt.Sprintf(" catch -> bb%d", r.Catch))
	}
	if r.Finally >= 0 {
		parts = append(parts, fmt.Sprintf(" finally -> bb%d", r.Finally))
	}
	return out + strings.Join(parts, ",") + " }"
}

func (f *Function) newValue(typ string) Value {
	v := Value{N: f.next, Type: typ}
	f.next++
	return v
}

// Bail is the compiler's equivalent of the interpreter's `unsupported` bucket:
// a body that could not be lowered, and the opcode that stopped it. Reporting
// which opcode is the whole point -- a lowering that silently produced
// something plausible for a construct it does not model would be exactly the
// gap-masquerading-as-behaviour that QUALITY_GATE.md forbids.
type Bail struct {
	Opcode string
	Reason string
}

func (b *Bail) Error() string {
	if b.Opcode == "" {
		return b.Reason
	}
	return b.Opcode + ": " + b.Reason
}

// String renders the function in the textual IR of IR-ASSEMBLY.md. The format
// is built to diff: one instruction per line, no column alignment, and source
// positions carried as `.line` directives so an edit above does not rewrite
// every line below it.
func (f *Function) String() string {
	var out strings.Builder
	fmt.Fprintf(&out, "func %s -> zval {\n", f.Name)
	if len(f.Frame) > 0 {
		slots := make([]string, len(f.Frame))
		for i, sl := range f.Frame {
			slots[i] = fmt.Sprintf("%s: %s", sl.Name, sl.Type)
		}
		fmt.Fprintf(&out, "  .frame [ %s ]\n", strings.Join(slots, ", "))
	}
	if f.SuspendsToCaller {
		fmt.Fprintln(&out, "  .suspends_to_caller")
	}
	if len(f.States) > 0 {
		labels := make([]string, len(f.States))
		for i, b := range f.States {
			labels[i] = fmt.Sprintf("bb%d", b)
		}
		fmt.Fprintf(&out, "  .resumable [ %s ]\n", strings.Join(labels, ", "))
	}
	if f.Entry != 0 {
		fmt.Fprintf(&out, "  .entry bb%d\n", f.Entry)
	}
	if f.File != "" {
		fmt.Fprintf(&out, "  .file %q\n", f.File)
	}
	if len(f.Params) > 0 {
		fmt.Fprintf(&out, "  .params [ %s ]\n", strings.Join(f.Params, ", "))
	}
	if f.Returns != "" {
		fmt.Fprintf(&out, "  .returns %q\n", f.Returns)
	}
	for i, r := range f.Regions {
		fmt.Fprintf(&out, "  %s\n", r.String(i))
	}
	for _, b := range f.Blocks {
		if b.Dead {
			continue
		}
		head := fmt.Sprintf("bb%d", b.ID)
		if len(b.Params) > 0 {
			head += "(" + strings.Join(b.Params, ", ") + ")"
		}
		if b.Region >= 0 {
			head += fmt.Sprintf(" in r%d", b.Region)
		}
		fmt.Fprintf(&out, "\n%s:\n", head)
		for _, in := range b.Insts {
			if in.Opcode == lineDirective {
				fmt.Fprintf(&out, ".line %s\n", in.Operands[0])
				continue
			}
			fmt.Fprintln(&out, "  "+in.String())
		}
		if b.Term != "" {
			fmt.Fprintf(&out, "  %s\n", b.Term)
		}
	}
	fmt.Fprintln(&out, "}")
	return out.String()
}

// String renders one instruction, and is the **only** place that knows how an
// instruction is spelled.
//
// It was two places: Function.String rendered ArgList and Block.String did not,
// so `call.fn "f"(byval $x)` printed as `call.fn "f"` to any test that looked at
// a block. That silently un-asserted three tests the moment operations became
// calls -- they were matching text that the block renderer no longer produced,
// and they were the tests guarding finally-body inlining.
//
// A renderer and a second renderer that disagree about what the text says is a
// defect this project has now had three times: the literal renderer and its
// scanner, the intrinsic table and the opcode names, and this.
func (in Inst) String() string {
	line := ""
	if in.Result != nil {
		line += fmt.Sprintf("%s:%s = ", in.Result, in.Result.Type)
	}
	line += in.Opcode
	if len(in.Operands) > 0 {
		line += " " + strings.Join(in.Operands, ", ")
	}
	line += in.ArgList
	for _, a := range in.Attrs {
		line += ", " + a
	}
	return line
}

// String renders just this block, for tests and diagnostics.
func (b *Block) String() string {
	var out strings.Builder
	for _, in := range b.Insts {
		out.WriteString(in.String())
		out.WriteString("\n")
	}
	out.WriteString(b.Term)
	return out.String()
}

// lineDirective is not an instruction; it carries a source position through the
// instruction stream so the printer can emit `.line` where it belongs.
const lineDirective = ".line"

// ExcRelease marks where `catch (E)` with no variable lets go of the caught
// exception, which is in the place it names. Whether that can free it is the
// emitter's question: only when nothing else may hold the object.
const ExcRelease = "exc.release"
