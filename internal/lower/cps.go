package lower

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Resumable rewrites a function that suspends into one that returns.
//
// A generator or a fiber body is entered many times and continues where it left
// off. The obvious implementation gives each one a real stack -- a goroutine --
// and that is what makes a runtime *suspension-aware*. The alternative is to
// reify the continuation: the function becomes a state machine, suspension
// becomes a return that records where to resume, and resuming becomes an
// ordinary call that dispatches to the recorded point. Then nothing underneath
// needs to know suspension exists.
//
// # Why this is tractable here
//
// The transform's hard part is normally deciding what must survive a
// suspension, which is a liveness computation over values. This IR answers it by
// construction: **no SSA value is used outside the block that defines it** --
// anything crossing a block is already a place, because that is the rule the
// lowering follows for compiled variables, cross-block temporaries, foreach
// iterators and spanning call operands alike. A suspension is a block
// terminator, so nothing live at one is a value. The frame is exactly the set of
// places, and this pass has no liveness to compute.
//
// That invariant is checked over both corpora by `php2lower -verify`, and it
// held only after two defects were fixed; it is not an assumption.
//
// # What this does not do
//
// It does not allocate the frame, choose a representation for it, or emit
// anything. It rewrites control flow into the shape an emitter can lower without
// coroutines, and stops there.
func Resumable(fn *Function, suspends SuspendingCallee) error {
	// A call that may suspend becomes a terminator first, so the rewrite below
	// treats it as a suspension point like any other. `suspends` is nil where
	// no whole-program answer is available, and then nothing splits and the
	// behaviour is what it was before this existed.
	splitAtSuspendingCalls(fn, suspends)
	points := suspensions(fn)
	if len(points) == 0 {
		return nil
	}
	demoteLiveAcross(fn, points)
	// A suspension in a generator cannot reach a PHP caller: calling a
	// generator function returns a Generator object, and the suspension happens
	// later inside the driver, which is CPS-aware. `Fiber::suspend()` has no
	// such mediator -- it suspends the current fiber from any depth, so the
	// suspension propagates to whoever called this function.
	//
	// That is a decision about **callers**, and it is deliberately not made
	// here. Emitting the suspending function as CPS is the common prefix of
	// every option on the table -- uniform, dual entry, selective, or keeping
	// goroutines -- so doing it commits to none of them. What the caller does is
	// recorded as an open question on the function rather than resolved by
	// refusing to lower it.
	for _, p := range points {
		if p.kind == "fiber.suspend" {
			fn.SuspendsToCaller = true
		}
		// A call that may suspend makes **this** function one too: control
		// leaves it for whoever drives the fiber, so its own callers are in the
		// same position. That is the property propagating up the call graph,
		// and marking it here keeps the IR's answer consistent with the set the
		// split was judged against.
		if p.kind == "call.suspending" {
			fn.SuspendsToCaller = true
		}
	}

	// State 0 is the ordinary entry. Every continuation gets the next number,
	// in block order so the numbering is stable across runs.
	states := []int{fn.Entry}
	stateOf := map[int]int{}
	for _, p := range points {
		if _, seen := stateOf[p.resumeAt]; seen {
			// Two suspensions resuming into one block would need the resumed
			// value to arrive at a single point from several places with
			// different numbering -- the case that cannot be inlined and is why
			// the continuation has to be reified rather than duplicated.
			return &Bail{Opcode: p.kind, Reason: "two suspensions share a continuation"}
		}
		stateOf[p.resumeAt] = len(states)
		states = append(states, p.resumeAt)
	}

	for _, p := range points {
		blk := fn.Blocks[p.at]
		state := stateOf[p.resumeAt]
		blk.Insts = append(blk.Insts, Inst{
			Opcode: "frame.set_state", Operands: []string{fmt.Sprint(state)}})
		// A suspension now leaves the function. It has no successor: control
		// comes back through the dispatch, not along an edge.
		blk.Term = p.leave
		blk.Succs = nil

		// The resumed value arrived as a block argument, which only makes sense
		// on an edge. It now comes out of the frame.
		//
		// Every continuation reads it, including the ones that discard the
		// value. `Generator::throw()` and `Fiber::throw()` resume by *raising*
		// at the suspension point, so this read is where a thrown-in exception
		// surfaces and where the generator's own `try` around the yield can
		// catch it. Emitting it only where a block parameter existed loses the
		// exception in `yield $x;` written as a statement -- silently, since
		// nothing else marks the resumption.
		cont := fn.Blocks[p.resumeAt]
		resumed := Value{N: fn.next, Type: "zval"}
		fn.next++
		if len(cont.Params) > 0 {
			resumed = Value{N: valueNumber(strings.SplitN(cont.Params[0], ":", 2)[0]), Type: "zval"}
			cont.Params = cont.Params[1:]
		}
		cont.Insts = append([]Inst{{Result: &resumed, Opcode: "frame.resumed"}}, cont.Insts...)
	}

	// A finished body must stay finished.
	//
	// Without this, `frame.set_state` records only where to *resume*, so after
	// the last `ret` the state is still the last continuation. Re-entering runs
	// the loop again and returns a second, different value -- the runtime side
	// caught it on the first generated frame: `counter(3)` returns 3, and a
	// further step returns 4. Both drivers there guard against it, on `finished`
	// and on `FiberTerminated`, so nothing hits it; but that makes correctness a
	// property of every driver rather than of the emitted code, and a third
	// driver gets it wrong silently.
	//
	// The return value is parked in a compiler place so the terminal state can
	// answer with it again. That is what makes re-entry idempotent rather than
	// merely guarded, and it needs no opcode the emitter does not already have.
	terminal := len(states)
	done := &Block{ID: len(fn.Blocks), Region: -1, Term: "ret " + retSlot}
	for _, b := range fn.Blocks {
		if b.Dead || !strings.HasPrefix(b.Term, "ret ") || strings.HasPrefix(b.Term, "ret.") {
			continue
		}
		b.Insts = append(b.Insts,
			// A frame slot, not a PHP variable: the return value is parked so
			// the resumed frame can find it. This pass runs after Lower, so it
			// writes the post-lowering spelling itself rather than relying on a
			// conversion that has already happened.
			Inst{Opcode: SlotSet, Operands: []string{retSlot, strings.TrimSpace(strings.TrimPrefix(b.Term, "ret "))}},
			Inst{Opcode: "frame.set_state", Operands: []string{fmt.Sprint(terminal)}})
		b.Term = "ret " + retSlot
	}
	fn.Blocks = append(fn.Blocks, done)
	states = append(states, done.ID)

	// The dispatch is appended rather than inserted, so that a block's index
	// still equals its ID; the function's entry moves to it instead.
	id := len(fn.Blocks)
	sel := Value{N: fn.next, Type: "i64"}
	fn.next++
	labels := make([]string, len(states))
	for i, b := range states {
		labels[i] = fmt.Sprintf("bb%d", b)
	}
	entry := &Block{
		ID:     id,
		Region: -1,
		Insts:  []Inst{{Result: &sel, Opcode: "frame.state"}},
		Term:   fmt.Sprintf("dispatch %s -> %s", sel, strings.Join(labels, ", ")),
		Succs:  append([]int(nil), states...),
	}
	fn.Blocks = append(fn.Blocks, entry)
	fn.Entry = id
	fn.States = states
	fn.Terminal = terminal
	fn.Frame = frameOf(fn)
	return nil
}

// retSlot holds a finished body's return value so the terminal state can answer
// with it again. Named like a compiler temporary because it is one: the program
// cannot spell it, and `frameOf` marks anything `$__`-prefixed as narrowable.
const retSlot = "$__ret"

// placeToken matches a place operand. A `$` inside a string literal is text, so
// literals are removed before scanning -- `echo "$x"` names no variable, the
// compiler already turned that into a rope.
// A leading digit is allowed: `${"10"}` is a legal PHP variable, and the
// scanner and the renderer have to agree about which names exist. They did not,
// so `$10` was spelled in the output and never declared -- `undefined: p_10`.
var placeToken = regexp.MustCompile(`\$[A-Za-z0-9_]+`)
var quotedText = regexp.MustCompile(`"(\\.|[^"\\])*"`)

// quotedPlace matches the `${"..."}` spelling a name outside `[A-Za-z0-9_]`
// gets. **A PHP variable name is an arbitrary byte string** -- `${"b\nb\n d"}`
// is legal -- and spelling one raw put a newline, a space or a quote in the
// middle of an operand, where no scanner could find its end again. Quoting is
// what makes the renderer and the scanner able to agree.
var quotedPlace = regexp.MustCompile(`\$\{"(\\.|[^"\\])*"\}`)

// PlaceOperand spells a variable name as an operand: bare when it is an
// identifier, `${"..."}` when it is not. The inverse is PlaceName, and the two
// sit together because they are one contract.
func PlaceOperand(name string) string {
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' {
			continue
		}
		return "${" + strconv.Quote(name) + "}"
	}
	if name == "" {
		return `${""}`
	}
	return "$" + name
}

// PlaceName is the PHP variable name an operand spells, for a diagnostic that
// has to quote it.
func PlaceName(operand string) string {
	operand = strings.TrimSpace(operand)
	if body, ok := strings.CutPrefix(operand, "${"); ok {
		if body, ok := strings.CutSuffix(body, "}"); ok {
			if name, err := strconv.Unquote(body); err == nil {
				return name
			}
		}
	}
	return strings.TrimPrefix(operand, "$")
}

// PlaceNames returns the places named in a piece of IR text, skipping any `$`
// inside a string literal. The emitter needs exactly what `frameOf` needs -- a
// body's places -- and sharing the scanner keeps the two from disagreeing about
// what counts as one.
func PlaceNames(text string) []string {
	// **The quoted spelling is matched first**, because blanking string
	// literals to hide a `$` inside one would erase the name itself.
	var out []string
	rest := quotedPlace.ReplaceAllStringFunc(text, func(m string) string {
		out = append(out, m)
		return ""
	})
	return append(out, placeToken.FindAllString(quotedText.ReplaceAllString(rest, `""`), -1)...)
}

// frameOf collects what the function must carry between entries: every place it
// mentions, in first-appearance order so the layout is stable.
//
// Every place, not the ones live across a suspension. For the program's own
// variables that is not conservatism but semantics -- PHP keeps a generator's
// locals alive until the generator itself dies, and their destructors fire in
// that order, so narrowing them would change observable output. Compiler
// temporaries carry no such obligation and are marked for a later pass to
// narrow by liveness.
func frameOf(fn *Function) []FrameSlot {
	typeOf := map[string]string{}
	for _, b := range fn.Blocks {
		for _, in := range b.Insts {
			if in.Result != nil {
				typeOf[in.Result.String()] = in.Result.Type
			}
		}
	}

	// A place's type is the join of everything written to it, not the first
	// thing: `$x = new Foo; $x = 5;` is one slot that holds either, and
	// declaring it `*obj` would have the emitter store an int into an object
	// field. Disagreement, or a write whose type is unknown, means `zval`.
	written := map[string]string{}
	note := func(place, typ string) {
		if typ == "" {
			typ = "zval"
		}
		if seen, ok := written[place]; ok && seen != typ {
			written[place] = "zval"
			return
		}
		written[place] = typ
	}

	var order []string
	seen := map[string]bool{}
	add := func(name string) {
		if !seen[name] {
			seen[name] = true
			order = append(order, name)
		}
	}
	scan := func(text string) {
		for _, m := range placeToken.FindAllString(quotedText.ReplaceAllString(text, `""`), -1) {
			add(m)
		}
	}
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		for _, in := range b.Insts {
			if place, value, isStore := StoreOf(in); isStore && strings.HasPrefix(place, "$") {
				add(place)
				note(place, typeOf[value])
			}
			scan(strings.Join(in.Operands, " "))
			scan(in.ArgList)
		}
		scan(b.Term)
	}

	out := make([]FrameSlot, 0, len(order))
	for _, name := range order {
		out = append(out, FrameSlot{
			Name:     name,
			Type:     typeOrZval(written[name]),
			Compiler: strings.HasPrefix(name, "$__"),
		})
	}
	return out
}

func typeOrZval(t string) string {
	if t == "" {
		return "zval"
	}
	return t
}

// suspension is one point where the function gives control back to its caller.
type suspension struct {
	at       int // the block that suspends
	resumeAt int // the block it resumes into
	kind     string
	leave    string // the terminator that replaces the suspension
}

// suspensions finds the points, and what each becomes. `generator_create` is one
// of them: it separates the prologue that runs when the function is *called*
// from the body that runs when it is first advanced, which is the same split as
// a yield with a different value handed back.
func suspensions(fn *Function) []suspension {
	var out []suspension
	for _, b := range fn.Blocks {
		if b.Dead || len(b.Succs) != 1 {
			continue
		}
		arg := func(prefix string) string {
			s := strings.TrimPrefix(b.Term, prefix)
			s = strings.TrimSuffix(s, fmt.Sprintf(" -> bb%d", b.Succs[0]))
			return strings.TrimSpace(s)
		}
		switch {
		case strings.HasPrefix(b.Term, "generator_create"):
			out = append(out, suspension{b.ID, b.Succs[0], "generator_create", "ret.generator"})
		case strings.HasPrefix(b.Term, "yield_from "):
			out = append(out, suspension{b.ID, b.Succs[0], "yield_from",
				"ret.yield_from " + arg("yield_from ")})
		case strings.HasPrefix(b.Term, "yield "):
			out = append(out, suspension{b.ID, b.Succs[0], "yield",
				"ret.yield " + arg("yield ")})
		case strings.HasPrefix(b.Term, "fiber.suspend"):
			out = append(out, suspension{b.ID, b.Succs[0], "fiber.suspend",
				"ret.suspend " + arg("fiber.suspend")})
		case strings.HasPrefix(b.Term, "call.suspending "):
			// The callee's frame is entered and driven from the continuation;
			// this function resumes with its **return value**, which is why
			// the resumed value is the call's result and not anything the
			// callee yielded. `ret.call` is the emitter's half.
			out = append(out, suspension{b.ID, b.Succs[0], "call.suspending",
				"ret.call " + arg("call.suspending ")})
		}
	}
	return out
}

// valueNumber reads the N out of a `%N` block parameter so the continuation's
// reads keep referring to the same name.
func valueNumber(s string) int {
	n := 0
	for i := 1; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			break
		}
		n = n*10 + int(s[i]-'0')
	}
	return n
}

// SuspendingCallee reports whether a call to `name` may suspend the fiber it
// runs on. `name` is the lowercased callee key the call graph uses: `f` for a
// plain function, `c::m` for a method.
//
// It is supplied rather than computed here because the answer is a
// whole-program property -- `CallGraph.Reach` -- and this pass sees one
// function.
// It takes the call's **kind** as well as its name, because the graph keys a
// method as `class::method` while a method call site carries only the bare name
// -- and a receiver whose class inference could not supply. Deciding what an
// unnarrowed method call may reach is policy, and policy belongs with whoever
// computed the set.
type SuspendingCallee func(kind, name, recv string) bool

// splitAtSuspendingCalls makes a call that may suspend into a block terminator,
// so the CPS rewrite can treat it as a suspension point like any other.
//
// **Why a call needs to be one at all.** `Fiber::suspend()` suspends the whole
// stack it runs on, so a caller of a suspending function has to be able to give
// control up too -- and a frame that is not a state machine has nowhere to put
// the outcome. That is the caller question `Resumable` records as
// `SuspendsToCaller` and deliberately does not answer; this is the answer, for
// the CPS half of it.
//
// The shape is the one `yield from` already has: control leaves here, the
// callee's frame runs, and this function resumes with the callee's **return
// value** rather than with anything it yielded. So the call's result becomes the
// continuation's first parameter, which is exactly where `Resumable` looks for
// the resumed value.
//
// A block can hold several such calls, and the remainder of a split block can
// hold more, so this runs to a fixed point rather than once per block.
func splitAtSuspendingCalls(fn *Function, suspends SuspendingCallee) {
	if suspends == nil {
		return
	}
	for again := true; again; {
		again = false
		for i := 0; i < len(fn.Blocks); i++ {
			b := fn.Blocks[i]
			if b == nil || b.Dead {
				continue
			}
			at, ok := suspendingCallIn(b, suspends)
			if !ok {
				continue
			}
			splitBlockAt(fn, b, at)
			again = true
			break
		}
	}
}

// suspendingCallIn finds the first instruction in the block that calls a
// callee which may suspend, and reports its index.
//
// An **opaque** call is not one of these. A sound whole-program answer treats
// an unresolvable callee as reaching anything, but that judgement belongs to
// `Reach`, which the predicate already carries: if opacity is being treated
// soundly, the enclosing function is in the set and its own callers split at
// *it*. Deciding it a second time here would split at every dynamic call in the
// program.
func suspendingCallIn(b *Block, suspends SuspendingCallee) (int, bool) {
	for j, in := range b.Insts {
		// The frame drain the harness puts before a return when an object
		// the body makes may have a suspending destructor (FrameDrains).
		if in.Opcode == FrameDrainOpcode {
			return j, true
		}
		s, ok := siteOf(in)
		if !ok || s.kind == "opaque" {
			continue
		}
		if suspends(s.kind, s.name, s.recv) ||
			(s.backup != "" && suspends(s.kind, s.backup, s.recv)) {
			return j, true
		}
	}
	return 0, false
}

// splitBlockAt turns the call at index `at` into a suspension: the call becomes
// a `frame.of.*`, which **builds the callee's frame without running it**, and
// the block terminates by handing that frame up.
//
// The terminator carries a *value*, which is `ret.yield_from <src>`'s shape and
// is the point: an argument list is rendered by the machinery that emits calls,
// and a terminator has none of it. Putting the whole call in the terminator text
// meant re-implementing argument rendering in the emitter's terminator path.
//
// The continuation inherits the block's own terminator and successors, so the
// edges out of `b` are unchanged -- they simply leave from one block further
// down. It inherits the region and the handler marks too: the call is protected
// by whatever `try` the block sat in, and so is the code after it.
func splitBlockAt(fn *Function, b *Block, at int) {
	call := b.Insts[at]
	frame := Value{N: fn.next, Type: "frame"}
	fn.next++
	cont := &Block{
		ID:          len(fn.Blocks),
		Region:      b.Region,
		Handler:     b.Handler,
		Unwind:      b.Unwind,
		InFinallyOf: append([]int(nil), b.InFinallyOf...),
		Insts:       append([]Inst(nil), b.Insts[at+1:]...),
		Term:        b.Term,
		Succs:       append([]int(nil), b.Succs...),
	}
	// The call's result is what resuming produces -- the callee's **return
	// value**, not anything it suspended with. Handed over as the
	// continuation's first parameter because that is where `Resumable` takes
	// the resumed value from; a call whose result nothing reads still gets one,
	// since the resumption has to be marked either way.
	result := call.Result
	if result == nil {
		v := Value{N: fn.next, Type: "zval"}
		fn.next++
		result = &v
	}
	cont.Params = append(cont.Params, fmt.Sprintf("%%%d: zval", result.N))
	fn.Blocks = append(fn.Blocks, cont)

	// **Values crossing the new boundary have to move into the frame.**
	// The IR's rule is that no SSA value is used outside its defining block,
	// and the frame holds exactly the places -- which is what makes a resumable
	// function re-enterable, since a Go local does not survive the return. The
	// split creates a boundary where there was none, and `mem2reg` has already
	// run by then, so anything it promoted across what is now a suspension has
	// to be demoted again.
	//
	// Without this the value was simply gone on resume: `$a = strlen($v);
	// return $a . inner($v);` came back `"R!"` where the engine says `"2R!"`,
	// with no error anywhere -- the Go local had been reinitialised by the
	// re-entry.
	kept := demoteAcross(fn, b, at, cont, result)

	// The call stays an instruction, with its operands, argument list and
	// attributes intact -- only its opcode and its result change. `fallback=`
	// in particular decides which function a namespaced call reaches, and a
	// terminator that dropped it would resolve to the wrong one.
	built := call
	built.Result = &frame
	built.Opcode = frameOpcodeFor(call.Opcode)
	b.Insts = append(append(b.Insts[:at:at], kept...), built)
	b.Term = fmt.Sprintf("call.suspending %%%d", frame.N)
	b.Succs = []int{cont.ID}
}

// frameOpcodeFor is the frame-building counterpart of a call opcode: `call.fn`
// becomes `frame.of.fn`, `call.method` becomes `frame.of.method`. It builds the
// callee's frame and runs none of it, which is what makes the suspension point
// a value the driver can enter rather than a call already in progress.
func frameOpcodeFor(opcode string) string {
	return strings.Replace(opcode, "call.", "frame.of.", 1)
}

// CallsSuspending reports whether the function calls anything that may suspend.
//
// It is the question that decides whether a body which does **not** suspend
// itself still has to be a state machine: its callee can give control up from
// inside its frame, so it must be able to as well.
func CallsSuspending(fn *Function, suspends SuspendingCallee) bool {
	if suspends == nil {
		return false
	}
	for _, b := range fn.Blocks {
		if b == nil || b.Dead {
			continue
		}
		if _, ok := suspendingCallIn(b, suspends); ok {
			return true
		}
	}
	return false
}

// demoteAcross moves every value defined before the split and read after it
// into a frame place, and answers the stores that put them there.
//
// `skip` is the call's own result: it arrives through `frame.resumed` rather
// than through a slot, so demoting it would shadow the resumption.
func demoteAcross(fn *Function, b *Block, at int, cont *Block, skip *Value) []Inst {
	defined := map[string]string{}
	for _, p := range b.Params {
		if v := strings.TrimSpace(strings.SplitN(p, ":", 2)[0]); strings.HasPrefix(v, "%") {
			defined[v] = ""
		}
	}
	for _, in := range b.Insts[:at] {
		if in.Result != nil {
			defined[in.Result.String()] = ""
		}
	}
	if skip != nil {
		delete(defined, skip.String())
	}
	if len(defined) == 0 {
		return nil
	}

	// Only what the continuation actually reads is worth a slot.
	used := map[string]bool{}
	for _, in := range cont.Insts {
		for _, op := range in.Operands {
			markValueTokens(op, used)
		}
		markValueTokens(in.ArgList, used)
		for _, a := range in.Attrs {
			if strings.HasPrefix(a, LoopSubjectAttr) {
				markValueTokens(a, used)
			}
		}
	}
	markValueTokens(cont.Term, used)

	// In definition order, so the stores come out stable across runs. A
	// **block parameter** is defined here too and has no instruction of its own
	// -- it is the previous split's result, arriving through `frame.resumed` --
	// so walking only the instructions recorded it as crossing and then emitted
	// no store for it. Two suspending calls in one expression lost the first
	// one's value, silently: `f($a->next(), $b->next(), $c->cur())` printed
	// null for everything but the last.
	var order []string
	for _, p := range b.Params {
		if v := strings.TrimSpace(strings.SplitN(p, ":", 2)[0]); strings.HasPrefix(v, "%") {
			order = append(order, v)
		}
	}
	for _, in := range b.Insts[:at] {
		if in.Result != nil {
			order = append(order, in.Result.String())
		}
	}

	var stores []Inst
	sub := map[string]string{}
	for _, name := range order {
		if _, ok := defined[name]; !ok || !used[name] {
			continue
		}
		if _, already := sub[name]; already {
			continue
		}
		// `$__` marks a place the program cannot name, which is what keeps this
		// out of the variable diagnostics: it is compiler storage, not a PHP
		// variable, so nothing warns about it being undefined.
		place := "$__cps" + strings.TrimPrefix(name, "%")
		sub[name] = place
		stores = append(stores, Inst{Opcode: SlotSet, Operands: []string{place, name}})
	}
	if len(sub) == 0 {
		return nil
	}
	for i := range cont.Insts {
		for j, op := range cont.Insts[i].Operands {
			cont.Insts[i].Operands[j] = rewriteValueTokens(op, sub)
		}
		cont.Insts[i].ArgList = rewriteValueTokens(cont.Insts[i].ArgList, sub)
		rewriteSubjectAttr(&cont.Insts[i], sub)
	}
	cont.Term = rewriteValueTokens(cont.Term, sub)
	return stores
}

// demoteLiveAcross moves every value live across a suspension into a frame
// place: stored where it is defined, read from the place in every other block.
//
// **demoteAcross is not enough on its own.** It demotes what a split block
// defines before the call, which is all a straight-line body has. But mem2reg
// promotes a loop's variables to block parameters read from blocks other than
// the one defining them -- the counter of a `for`, the iterator of a
// `foreach` -- and when a suspension sits in the loop body, the value is read
// after the frame was re-entered, from a Go local the re-entry reset. `for
// ($i = 0; ...; $i++) { $g->next(); }` in a fiber printed `$i` empty and ran
// on, and a `foreach` over an array stopped after its first element (no error
// anywhere). Liveness over the graph, exception edges included, finds them.
func demoteLiveAcross(fn *Function, points []suspension) {
	n := len(fn.Blocks)
	defs := make([]map[string]bool, n)
	uses := make([]map[string]bool, n)
	defBlock := map[string]int{}
	for i, b := range fn.Blocks {
		defs[i], uses[i] = map[string]bool{}, map[string]bool{}
		if b == nil || b.Dead {
			continue
		}
		for _, p := range b.Params {
			if v := strings.TrimSpace(strings.SplitN(p, ":", 2)[0]); strings.HasPrefix(v, "%") {
				defs[i][v] = true
				defBlock[v] = i
			}
		}
		for _, in := range b.Insts {
			// SSA: a value read in its own block is defined above the read,
			// so only a value from elsewhere is an upward-exposed use.
			mark := func(text string) {
				forEachValueToken(text, func(tok string) string {
					if !defs[i][tok] {
						uses[i][tok] = true
					}
					return tok
				})
			}
			for _, op := range in.Operands {
				mark(op)
			}
			mark(in.ArgList)
			for _, a := range in.Attrs {
				if strings.HasPrefix(a, LoopSubjectAttr) {
					mark(a)
				}
			}
			if in.Result != nil {
				defs[i][in.Result.String()] = true
				defBlock[in.Result.String()] = i
			}
		}
		forEachValueToken(b.Term, func(tok string) string {
			if !defs[i][tok] {
				uses[i][tok] = true
			}
			return tok
		})
	}
	cfg := Analyze(fn)
	liveIn := make([]map[string]bool, n)
	for i := range liveIn {
		liveIn[i] = map[string]bool{}
		for v := range uses[i] {
			liveIn[i][v] = true
		}
	}
	for changed := true; changed; {
		changed = false
		for i := n - 1; i >= 0; i-- {
			for _, s := range cfg.Succs(i) {
				for v := range liveIn[s] {
					if !defs[i][v] && !liveIn[i][v] {
						liveIn[i][v] = true
						changed = true
					}
				}
			}
		}
	}
	crossing := map[string]bool{}
	for _, p := range points {
		for v := range liveIn[p.resumeAt] {
			if _, ok := defBlock[v]; ok {
				crossing[v] = true
			}
		}
	}
	if len(crossing) == 0 {
		return
	}
	names := make([]string, 0, len(crossing))
	for v := range crossing {
		names = append(names, v)
	}
	sort.Slice(names, func(a, b int) bool { return valueNumber(names[a]) < valueNumber(names[b]) })
	sub := map[string]string{}
	for _, v := range names {
		sub[v] = "$__cps" + strings.TrimPrefix(v, "%")
	}
	// The stores, at each definition: after the instruction, or at the top of
	// the block for a parameter.
	for i, b := range fn.Blocks {
		if b == nil || b.Dead {
			continue
		}
		var top []Inst
		for _, p := range b.Params {
			if v := strings.TrimSpace(strings.SplitN(p, ":", 2)[0]); crossing[v] {
				top = append(top, Inst{Opcode: SlotSet, Operands: []string{sub[v], v}})
			}
		}
		var insts []Inst
		for _, in := range b.Insts {
			insts = append(insts, in)
			if in.Result != nil && crossing[in.Result.String()] {
				v := in.Result.String()
				insts = append(insts, Inst{Opcode: SlotSet, Operands: []string{sub[v], v}})
			}
		}
		b.Insts = append(top, insts...)
		// The reads, everywhere but the defining block: there the value is
		// still the Go local it was defined as.
		local := map[string]string{}
		for v, place := range sub {
			if defBlock[v] != i {
				local[v] = place
			}
		}
		for j := range b.Insts {
			for k, op := range b.Insts[j].Operands {
				b.Insts[j].Operands[k] = rewriteValueTokens(op, local)
			}
			b.Insts[j].ArgList = rewriteValueTokens(b.Insts[j].ArgList, local)
			rewriteSubjectAttr(&b.Insts[j], local)
		}
		b.Term = rewriteValueTokens(b.Term, local)
	}
}

// markValueTokens records every `%N` in the text, ignoring any inside a string
// literal -- a literal can contain anything, including something that looks
// like a value.
func markValueTokens(text string, into map[string]bool) {
	forEachValueToken(text, func(tok string) string {
		into[tok] = true
		return tok
	})
}

// MarkValueTokens is markValueTokens for the emitter.
func MarkValueTokens(text string, into map[string]bool) { markValueTokens(text, into) }

// rewriteValueTokens replaces whole `%N` tokens using `sub`, leaving string
// literals alone. Whole tokens matter: a textual replace of `%1` would also hit
// the `%1` inside `%10`.
func rewriteValueTokens(text string, sub map[string]string) string {
	return forEachValueToken(text, func(tok string) string {
		if to, ok := sub[tok]; ok {
			return to
		}
		return tok
	})
}

// forEachValueToken walks the text outside string literals, handing each `%N`
// to `fn` and rebuilding with what it answers.
func forEachValueToken(text string, fn func(string) string) string {
	var out strings.Builder
	for i := 0; i < len(text); {
		switch c := text[i]; {
		case c == '"':
			j := i + 1
			for j < len(text) {
				if text[j] == '\\' {
					j += 2
					continue
				}
				if text[j] == '"' {
					j++
					break
				}
				j++
			}
			if j > len(text) {
				j = len(text)
			}
			out.WriteString(text[i:j])
			i = j
		case c == '%':
			j := i + 1
			for j < len(text) && text[j] >= '0' && text[j] <= '9' {
				j++
			}
			if j == i+1 {
				out.WriteByte(c)
				i++
				continue
			}
			out.WriteString(fn(text[i:j]))
			i = j
		default:
			out.WriteByte(c)
			i++
		}
	}
	return out.String()
}

// rewriteSubjectAttr renames the value a loop-freeing instruction names as its
// subject (LoopSubjectAttr). It is an attribute, not an operand, but it is a
// read: the emitter frees the loop's object through it at the loop's end, and
// once the loop body suspends that end is in another step, where only the
// CPS slot still holds the object.
func rewriteSubjectAttr(in *Inst, sub map[string]string) {
	for k, a := range in.Attrs {
		if strings.HasPrefix(a, LoopSubjectAttr) {
			in.Attrs[k] = LoopSubjectAttr + rewriteValueTokens(strings.TrimPrefix(a, LoopSubjectAttr), sub)
		}
	}
}
