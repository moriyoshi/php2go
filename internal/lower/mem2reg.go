package lower

import (
	"fmt"
	"github.com/moriyoshi/php2go/runtime/php"
	"sort"
	"strings"
)

// Promoting places to SSA values.
//
// A CV is a named slot assigned repeatedly; a value is single-assignment.
// Lowering keeps every CV as a place and defers the promotion, which is the
// standard front-end shape -- it produces correct IR without answering a single
// phi question. This pass answers them.
//
//	local.set $sum, 0                      %3:zval = call.intrinsic "add"(0, %2)
//	%1 = call.intrinsic "read_var"($sum)
//	%2 = call.intrinsic "add"(%1, 1)
//	local.set $sum, %2
//
// # Why it could not be written before reads were explicit
//
// mem2reg replaces *loads* with the value the store put there. With reads
// implicit in an operand -- `add $sum, 1` -- there was no load to replace and no
// place to put the answer; a promotion pass would have had to know which operand
// positions of which opcodes were reads, which is exactly the knowledge that
// kept leaking into every consumer. With `read_var` an instruction, this is the
// textbook transform.
//
// # Block arguments, not phi nodes
//
// The IR carries merged values as block **parameters** -- a catch handler
// already receives its exception that way -- so a merge is `jmp bb2(%7)` and
// `bb2(%8: zval)`. Same thing as a phi, written where the value travels.
//
// # What is refused, and why each one would be wrong
//
//   - **Anything aliased.** A place bound by reference, made global or static,
//     captured by a closure, unset, or whose address is taken by a `dim.ref` or
//     `prop.ref` can change without a `local.set`, so its value is not the one
//     the last store put there.
//   - **A frame with variable variables**, `extract` or `compact`: those reach
//     every local without naming one.
//   - **A place read before it is written.** Reading an unset variable warns and
//     yields null; a promoted place would have no value at all. `read_var` is
//     still the instruction that warns, so refusing here keeps the diagnostic.
//   - **A merge an edge cannot carry an argument on.** Exception edges are
//     region membership rather than terminators -- a handler is reachable from
//     anywhere in its region -- so a block whose predecessors do not all name it
//     cannot take a parameter.

// Mem2Reg promotes places to SSA values, and reports how many it promoted and
// how many it considered.
func Mem2Reg(fn *Function) (promoted, candidates int) {
	cfg := Analyze(fn)
	if frameIsOpaque(fn) != "" {
		return 0, 0
	}
	// A body that suspends is rewritten by the CPS pass afterwards, and that
	// rewrite **parks values in places** so they survive a resume: a generator's
	// frame is saved and restored, and an SSA value has nowhere to live across
	// that. Promoting first fights it, and the corpus said so -- a running sum
	// printed the loop counter.
	//
	// Refusing here rather than reordering the passes because CPS's output is
	// not in the shape this one wants either: it introduces state-machine
	// blocks whose predecessors are the resume dispatch, which no terminator
	// names.
	if len(suspensions(fn)) > 0 {
		return 0, 0
	}

	byValue := map[string]bool{}
	for _, p := range fn.Params {
		fields := strings.SplitN(strings.TrimSpace(p), " ", 3)
		if len(fields) >= 2 && !ParamIsByRef(p) {
			byValue[strings.TrimPrefix(fields[1], "&")] = true
		}
	}
	assigned := assignedBeforeEveryRead(fn, byValue)

	places := promotable(fn, cfg, assigned)
	// **The file body's locals are globals.** `$a = 41;` at file scope is
	// readable by `global $a` in any function, by `$GLOBALS["a"]`, and by an
	// included file -- none of which this frame can see, so a store there is
	// never dead and a value is never only this function's. Zend reaches the
	// same conclusion by a different route: it marks the file body
	// ZEND_FUNC_INDIRECT_VAR_ACCESS unconditionally and declines SSA for it.
	//
	// A **compiler temporary is not a global**, though. The program cannot name
	// `$__t3`, so no `global`, no `$GLOBALS` and no include can reach it, and
	// the rope accumulators and foreach iterators that make up most of a script
	// body promote exactly as they would inside a function.
	if fn.Name == "{main}" {
		return 0, 0
	}
	candidates = len(places)
	if candidates == 0 {
		return 0, 0
	}

	// Sorted, so the values a run allocates do not depend on map order.
	names := make([]string, 0, len(places))
	for p := range places {
		names = append(names, p)
	}
	sort.Strings(names)

	// Where each place needs a block parameter: the iterated dominance frontier
	// of its stores. A place whose frontier includes a block an edge cannot
	// carry an argument to is dropped rather than half-promoted.
	frontier := dominanceFrontiers(fn, cfg)
	live := liveIn(fn, cfg, names)
	needsParam := map[string]map[int]bool{}
	keep := names[:0]
	for _, p := range names {
		idf := iteratedFrontier(frontier, places[p])
		// **Pruned SSA**: a parameter only where the value is live in. Without
		// this, a `foreach` value gets a parameter on the loop header, where it
		// has no value yet on the edge from before the loop -- the argument
		// came out empty and the emitter refused the operand. Placing on
		// liveness removes the case rather than inventing a value for it.
		for b := range idf {
			if !live[b][p] {
				delete(idf, b)
			}
		}
		ok := true
		for b := range idf {
			if !argumentsReach(fn, cfg, b) {
				ok = false
				break
			}
		}
		if ok {
			needsParam[p] = idf
			keep = append(keep, p)
		}
	}
	names = keep
	if len(names) == 0 {
		return 0, candidates
	}

	// Insert the parameters, remembering which value each one is.
	// A parameter's type is the **place's**, not `zval`: an iterator slot
	// carries a `*iter` and a rope carries a `*strbuf`, and declaring those as
	// values would emit the wrong Go type for every one of them.
	repr := placeRepresentation(fn)
	param := map[int]map[string]Value{}
	order := map[int][]string{}
	for _, p := range names {
		// **Sorted.** Allocating in map order gave the parameters different
		// value numbers on different runs, so the same input lowered to two
		// different-looking IRs -- a golden that passes and fails with nothing
		// changed, which QUALITY_GATE.md calls flaky and treats as a defect
		// rather than a result. An IR that is not reproducible cannot be
		// diffed, cached, or pinned.
		blocks := make([]int, 0, len(needsParam[p]))
		for b := range needsParam[p] {
			blocks = append(blocks, b)
		}
		sort.Ints(blocks)

		for _, b := range blocks {
			t := repr[p]
			if t == "" {
				t = "zval"
			}
			v := fn.newValue(t)
			if param[b] == nil {
				param[b] = map[string]Value{}
			}
			param[b][p] = v
			order[b] = append(order[b], p)
		}
	}
	for b := range order {
		sort.Strings(order[b])
		for _, p := range order[b] {
			fn.Blocks[b].Params = append(fn.Blocks[b].Params,
				fmt.Sprintf("%s: %s", param[b][p], param[b][p].Type))
		}
	}

	r := &renamer{
		fn: fn, cfg: cfg, places: names, param: param, order: order,
		subst: map[string]string{}, drop: map[[2]int]bool{}, args: map[[2]int][]string{},
	}
	r.rename(entryBlock(cfg), map[string]string{})
	r.apply()
	return len(names), candidates
}

// promotable are the places every access to which this pass can see, mapped to
// the blocks that store them.
func promotable(fn *Function, cfg *CFG, assigned map[string]bool) map[string]map[int]bool {
	stores := map[string]map[int]bool{}
	hazard := map[string]bool{}

	for _, p := range fn.Params {
		if ParamIsByRef(p) {
			fields := strings.SplitN(strings.TrimSpace(p), " ", 3)
			if len(fields) >= 2 {
				hazard[strings.TrimPrefix(fields[1], "&")] = true
			}
		}
	}
	// **A parameter the body writes is read by the frame** -- a backtrace,
	// func_get_args() and an exception's trace report the slot's value, not
	// the one passed -- so it keeps its slot (LiveParams).
	for place := range fn.LiveParams {
		hazard[place] = true
	}
	// **A box is an address.** `foreach ($a as &$v)` opens `$a`'s storage and
	// writes through it, but ExplicitReads has already turned the argument into
	// `read_var($a)` -- so the place never appears where this pass looks for an
	// address, `$a` was promoted, the loop wrote into a box nothing read, and
	// `return $a` answered the array from before the loop. Follow the read back
	// to its place.
	readOf := map[string]string{}
	for _, b := range fn.Blocks {
		for _, in := range b.Insts {
			if in.Result == nil {
				continue
			}
			if _, v, ok := IntrinsicOf(in); ok && strings.HasPrefix(v.Name, "read_var") {
				if args := CallArgs(in); len(args) > 0 && strings.HasPrefix(args[0], "$") {
					readOf[in.Result.String()] = args[0]
				}
			}
		}
	}
	for _, b := range fn.Blocks {
		for _, in := range b.Insts {
			if _, v, ok := IntrinsicOf(in); ok && v.PlaceBox {
				if args := CallArgs(in); len(args) > 0 {
					if place, ok := readOf[strings.TrimSpace(args[0])]; ok {
						hazard[place] = true
					} else if strings.HasPrefix(args[0], "$") {
						hazard[args[0]] = true
					}
				}
			}
		}
	}
	for i, b := range fn.Blocks {
		if b.Dead || !cfg.Reachable(i) {
			continue
		}
		for _, in := range b.Insts {
			if place, _, isStore := StoreOf(in); isStore {
				// **Only a named slot is a place.** `write_var(%2, 2)` writes
				// *through* an SSA place value -- `$GLOBALS["g"] = 2` builds
				// one -- and there is no slot behind it to promote. Recording
				// `%2` here made it a candidate with stores and no reads, so
				// the promotion dropped the store and substituted nothing: the
				// write simply vanished from the emitted body.
				if !strings.HasPrefix(place, "$") {
					continue
				}
				if stores[place] == nil {
					stores[place] = map[int]bool{}
				}
				stores[place][i] = true
				continue
			}
			if _, escapes := placeEscapes[in.Opcode]; escapes {
				for _, op := range in.Operands {
					hazard[op] = true
				}
				continue
			}
			// A place in a **value** position is a use, and the renamer
			// rewrites it. A place anywhere else is out of this pass's sight:
			// an address taken, a builder handle passed through unconverted.
			// The catalogue says which is which, because the argument shapes
			// are exactly that distinction.
			//
			// This marked every `$` argument a hazard, which was right while
			// the only thing that could sit in one was a `*Iter` -- and stopped
			// being right the moment the cursor became an ordinary value. The
			// iterator slot survived promotion for no reason but this line.
			if _, v, isCall := IntrinsicOf(in); isCall {
				for i, a := range CallArgs(in) {
					if !strings.HasPrefix(a, "$") {
						continue
					}
					if !valueArgShape(v, i) {
						hazard[a] = true
					}
				}
				continue
			}
			for _, op := range in.Operands {
				if strings.HasPrefix(op, "$") {
					hazard[op] = true
				}
			}
			// **Every** argument, not just the by-reference ones. A by-value
			// `call.fn "var_dump"(byval $__t1)` is a *read* of the place, and
			// this pass only knows how to rewrite a read that is a `read_var`
			// instruction -- which a compiler temporary never gets, because it
			// cannot warn. Scanning byref alone dropped the stores to `$__t1`
			// and left the use naming a place nothing wrote: `$undef ?? "d"`
			// answered NULL.
			for _, a := range splitCallArgs(strings.TrimSuffix(strings.TrimPrefix(
				strings.TrimSpace(in.ArgList), "("), ")")) {
				// **A named send carries its name after the operand**,
				// `byref $value as "custom"`, and the operand alone is the
				// place: keyed with the suffix, a variable sent under a name
				// was promoted and the send read a slot nothing wrote.
				a, _, _ = CutNamedSend(a)
				for _, mode := range []string{"byval ", "byref ", "preferref ", "unpack "} {
					a = strings.TrimPrefix(a, mode)
				}
				if a = strings.TrimSpace(a); strings.HasPrefix(a, "$") {
					hazard[a] = true
				}
			}
		}

	}

	out := map[string]map[int]bool{}
	for name, at := range stores {
		// A compiler temporary is promotable like any other place. It used to
		// be excluded, which left `local.set` alive for the frame slots -- the
		// rope accumulator, the foreach iterator, the short-circuit merge --
		// and those are exactly the single-assignment values SSA is for.
		if hazard[name] || !assigned[name] {
			continue
		}
		if storedInsideARegion(fn, at) {
			continue
		}
		out[name] = at
	}
	return out
}

// storedInsideARegion reports whether any store is inside a protected range.
//
// The renaming walk carries the value at the **end** of a block into the blocks
// it dominates, and a handler is entered from the *middle* of the protected
// range -- so a store that appears before the throw in program order, and never
// executed, would still be the value a handler sees.
//
//	$a = 1;
//	try { $a /= 0; } catch (DivisionByZeroError) {}
//	var_dump($a);        // int(1): the store never happened
//
// Promotion answered NULL. Found by the corpus, and by four tests in Zend's own
// suite that exist because this is exactly the case PHP is careful about: a
// compound assignment that throws leaves the variable alone.
//
// Refusing is conservative -- a store in a `try` whose operation cannot throw is
// safe too -- but "cannot throw" is the intrinsic's effect contract applied to
// every instruction between the store and the handler, which is a bigger
// analysis than this pass has.
// Note the zero value: a Block built without an explicit Region is *in region
// zero*, not outside every region. The lowering always sets -1; a hand-built
// fixture that forgets is silently protected, which is the safe direction here
// but a trap worth knowing about.
func storedInsideARegion(fn *Function, at map[int]bool) bool {
	for b := range at {
		if b >= 0 && b < len(fn.Blocks) && fn.Blocks[b].Region >= 0 {
			return true
		}
	}
	return false
}

// valueArgShape reports whether an intrinsic's argument at this position is an
// ordinary value the caller reads, as opposed to a place it addresses or a
// handle it takes unconverted.
//
// `var` is the read intrinsic's own operand: the place it reads, which is a use
// rather than an escape.
func valueArgShape(v php.Variant, i int) bool {
	shape := ""
	if i < len(v.Args) {
		shape = v.Args[i]
	}
	switch shape {
	case "", "obj", "read", "var":
		return true
	}
	return false
}

// termVars are the variables a terminator names. Reads are explicit, so a
// terminator naming one at all means something this pass does not model.
func termVars(term string) []string {
	var out []string
	rewriteTermVars(term, func(v string) string {
		out = append(out, v)
		return v
	})
	return out
}

// argumentsReach reports whether every edge into this block comes from a
// terminator that names it. An exception edge does not: a handler is reachable
// from anywhere in its region, so there is no single place to pass a value.
func argumentsReach(fn *Function, cfg *CFG, b int) bool {
	if len(fn.Blocks[b].Params) > 0 {
		// Already receives something -- a catch handler's exception -- and
		// mixing that with promoted values needs an answer this pass does not
		// have.
		return false
	}
	for _, p := range cfg.Preds(b) {
		if !contains(fn.Blocks[p].Succs, b) {
			return false
		}
	}
	return len(cfg.Preds(b)) > 0
}

// dominanceFrontiers is Cytron's algorithm: a block is in the frontier of every
// block that dominates one of its predecessors without strictly dominating it.
func dominanceFrontiers(fn *Function, cfg *CFG) []map[int]bool {
	df := make([]map[int]bool, len(fn.Blocks))
	for i := range df {
		df[i] = map[int]bool{}
	}
	for b := range fn.Blocks {
		preds := cfg.Preds(b)
		if len(preds) < 2 {
			continue
		}
		for _, p := range preds {
			for r := p; r >= 0 && r != cfg.Idom(b); r = cfg.Idom(r) {
				df[r][b] = true
			}
		}
	}
	return df
}

// iteratedFrontier is where a place's stores force a parameter: the frontier of
// the stores, then of the blocks that adds, to a fixed point.
func iteratedFrontier(df []map[int]bool, stores map[int]bool) map[int]bool {
	out := map[int]bool{}
	work := make([]int, 0, len(stores))
	for b := range stores {
		work = append(work, b)
	}
	for len(work) > 0 {
		b := work[len(work)-1]
		work = work[:len(work)-1]
		if b < 0 || b >= len(df) {
			continue
		}
		for f := range df[b] {
			if !out[f] {
				out[f] = true
				work = append(work, f)
			}
		}
	}
	return out
}

func entryBlock(cfg *CFG) int { return 0 }

// renamer walks the dominator tree carrying, for each promoted place, the value
// it currently holds.
type renamer struct {
	fn     *Function
	cfg    *CFG
	places []string
	param  map[int]map[string]Value
	order  map[int][]string
	// subst maps a read's result to the value that read produces.
	subst map[string]string
	// drop marks (block, instruction) pairs the promotion removes.
	drop map[[2]int]bool
	// args are the values each edge carries, keyed by (from, to).
	args map[[2]int][]string
	// released records the stores whose dropped write was the death of what the
	// local held, so a marker can be put back where the write was.
	released []releaseSite
	// stores records every promoted store to a user-visible local, with the
	// value the local takes there. apply turns them into `track` markers for
	// the locals that may hold an object; see ReleaseTrackAttr.
	stores []releaseSite
}

// mayHoldObject reports whether a marker is worth recording for this local and
// value. A compiler temporary is not a PHP variable, so nothing observes when
// it drops what it held; and a value the IR already types as a string, an int
// or an array can have no destructor and no handle. Both checks exist to keep
// the marker off the overwhelming majority of stores -- the rope accumulators
// and loop counters -- rather than for correctness.
func (r *renamer) mayHoldObject(place, value string) bool {
	if !strings.HasPrefix(place, "$") || strings.HasPrefix(place, "$__") {
		return false
	}
	def := definition(r.fn, value)
	if def == nil || def.Result == nil {
		return false
	}
	switch def.Result.Type {
	case "zval", "*obj":
		return true
	}
	return false
}

// releaseSite is a store promotion removed, and the value the local stopped
// holding there.
type releaseSite struct {
	at    [2]int
	value string
	local string
}

func (r *renamer) rename(b int, incoming map[string]string) {
	if b < 0 || b >= len(r.fn.Blocks) || r.fn.Blocks[b].Dead {
		return
	}
	cur := map[string]string{}
	for k, v := range incoming {
		cur[k] = v
	}
	for _, p := range r.order[b] {
		cur[p] = r.param[b][p].String()
	}

	blk := r.fn.Blocks[b]
	for i, in := range blk.Insts {
		if place, value, isStore := StoreOf(in); isStore {
			if _, promoted := r.param[b][place]; promoted || r.tracks(place) {
				// The stored value may itself **be** a promoted place, and then
				// the name is stale the moment its store is dropped:
				// `write_var($k, $__t4)` recorded the literal `$__t4` as the
				// value of `$k`, and every later read of `$k` substituted that
				// name -- naming a slot nothing writes any more.
				//
				// `resolve` cannot answer this: it follows substitutions from
				// *removed reads*, and a place mentioned directly in a value
				// position was never a read. The current value of a tracked
				// place lives in `cur`, which is what the walk is carrying.
				//
				// The visible symptom was a keyed `foreach` inside a function:
				// `foreach ($a as $k => $v)` bound the key through exactly this
				// temporary, so `$k` came out empty in every compiled program.
				if v, live := cur[value]; live && r.tracks(value) {
					value = v
				}
				// **Leave a marker where the store was.** Promotion removes the
				// write, and with it the only place a release can hang: PHP
				// destroys what `$a` held at the line that overwrites it, and
				// after promotion there is no such line. The renamer is the
				// one pass that still knows -- it is walking in order and
				// `cur[place]` is exactly the value being dropped -- so it
				// records that rather than leaving nothing behind.
				//
				// Inert unless a lifetime analysis claims it. See
				// `ReleasePrev`.
				if held, live := cur[place]; live && held != "" && held != value &&
					r.mayHoldObject(place, held) {
					r.released = append(r.released, releaseSite{at: [2]int{b, i}, value: held, local: place})
				}
				cur[place] = r.resolve(value)
				// **Keep the copy the dropped store made.** A store copies --
				// `$b = $a` gives `$b` a value of its own -- and promotion
				// removes the store, so `$b` became `$a`'s own array and a
				// later `$a[] = 2` grew it. The value is marked where it is
				// read out of storage, and the emitter shares it there
				// (Value.Share, O(1)); the instruction stays what it was for
				// every analysis that reads it.
				markShare(r.fn, cur[place])
				if strings.HasPrefix(place, "$") && !strings.HasPrefix(place, "$__") {
					r.stores = append(r.stores, releaseSite{at: [2]int{b, i}, value: cur[place], local: place})
				}
				// **Remember that this value stands for a user-visible local.**
				// Promotion is what makes `$c = clone $o; f($c);`
				// indistinguishable from the temporary in `f(clone $o)`, and
				// the two have different lifetimes: PHP frees a local when the
				// frame dies and a statement temporary at the end of its
				// statement. Without this record the lifetime analyses released
				// a local at its last use, which is early whenever anything is
				// allocated between that use and the return.
				//
				// Compiler temporaries are excluded: the program cannot name
				// `$__t3`, and they are genuinely values rather than locals.
				if strings.HasPrefix(place, "$") && !strings.HasPrefix(place, "$__t") {
					if r.fn.FrameBound == nil {
						r.fn.FrameBound = map[string]bool{}
					}
					r.fn.FrameBound[cur[place]] = true
				}
				r.drop[[2]int{b, i}] = true
				continue
			}
			// **A store that stays can still name a promoted place as its
			// value**, and that place's own store is gone: `write_var($k,
			// $__t4)` where `$k` is kept -- it names a dynamic property in the
			// loop, `$o->$k` -- but the key temporary `$__t4` was promoted.
			// The value is renamed as any other value-position mention is;
			// skipping it left `$k` reading a slot nothing writes, so a keyed
			// foreach over an object bound no key at all.
			if v, live := cur[value]; live && r.tracks(value) && value != v {
				if in.Opcode == "local.set" || in.Opcode == SlotSet {
					in.Operands[1] = v
					blk.Insts[i] = in
				} else if _, iv, isCall := IntrinsicOf(in); isCall && iv.Name == "write_var" {
					if args := CallArgs(in); len(args) == 2 {
						args[1] = v
						in.ArgList = "(" + strings.Join(args, ", ") + ")"
						blk.Insts[i] = in
					}
				}
			}
			continue
		}
		// **`unset($a)` is a death too**, and unlike a store it survives
		// promotion -- so it needs no marker of its own, only the record of
		// what the local held when it ran. Same reasoning as the store above:
		// the renamer is the last pass that knows.
		if in.Opcode == "unset" && len(in.Operands) == 1 {
			place := strings.TrimSpace(in.Operands[0])
			if held, live := cur[place]; live && held != "" && r.mayHoldObject(place, held) {
				r.released = append(r.released, releaseSite{at: [2]int{b, i}, value: held, local: place})
			}
			continue
		}
		if _, v, isCall := IntrinsicOf(in); isCall {
			if in.Result != nil && (v.Name == "read_var" || v.Name == "read_var.silent") {
				args := CallArgs(in)
				if len(args) > 0 && r.tracks(args[0]) {
					if val, live := cur[args[0]]; live {
						r.subst[in.Result.String()] = val
						r.drop[[2]int{b, i}] = true
					}
					continue
				}
			}
			// Any other value-position mention of a promoted place becomes the
			// value it currently holds. Done here rather than in apply(),
			// because the answer depends on where in the walk we are -- which
			// is the whole content of SSA renaming.
			args := CallArgs(in)
			changed := false
			for j, a := range args {
				if !valueArgShape(v, j) || !r.tracks(a) {
					continue
				}
				if val, live := cur[a]; live {
					args[j], changed = val, true
				}
			}
			if changed {
				in.ArgList = "(" + strings.Join(args, ", ") + ")"
				blk.Insts[i] = in
			}
		}
	}

	// The terminator reads at the end of the block, so it sees `cur`.
	blk.Term = rewriteTermVars(blk.Term, func(operand string) string {
		if v, live := cur[operand]; live && r.tracks(operand) {
			return v
		}
		return operand
	})

	// What each outgoing edge carries.
	for _, s := range blk.Succs {
		var vals []string
		for _, p := range r.order[s] {
			vals = append(vals, cur[p])
		}
		if len(vals) > 0 {
			r.args[[2]int{b, s}] = vals
		}
	}

	for c := range r.fn.Blocks {
		if r.cfg.Idom(c) == b && c != b {
			r.rename(c, cur)
		}
	}
}

func (r *renamer) tracks(place string) bool {
	for _, p := range r.places {
		if p == place {
			return true
		}
	}
	return false
}

// resolve follows a chain of substitutions, so a value produced by a read that
// was itself removed lands on the store that defined it.
func (r *renamer) resolve(operand string) string {
	seen := 0
	for {
		next, ok := r.subst[strings.TrimSpace(operand)]
		if !ok || seen > 64 {
			return operand
		}
		operand, seen = next, seen+1
	}
}

// apply removes the promoted stores and reads and rewrites every mention of a
// removed read's result.
func (r *renamer) apply() {
	// The locals a `track` marker is worth leaving for: those that may hold
	// an object after some store.
	objectLocals := map[string]bool{}
	for _, st := range r.stores {
		// Only a store of an object the IR types as one -- what `new` makes
		// -- can be a confined object; an untyped value never is.
		if def := definition(r.fn, st.value); def != nil && def.Result != nil && def.Result.Type == "*obj" {
			objectLocals[st.local] = true
		}
	}
	tracks := map[[2]int]releaseSite{}
	for _, st := range r.stores {
		if objectLocals[st.local] {
			tracks[st.at] = st
		}
	}
	for bi, b := range r.fn.Blocks {
		// A fresh slice, not `b.Insts[:0]`: a dropped store can leave two
		// markers behind, and filtering in place then overwrites
		// instructions not yet read.
		out := make([]Inst, 0, len(b.Insts)+2)
		sites := map[int]releaseSite{}
		for _, rs := range r.released {
			if rs.at[0] == bi {
				sites[rs.at[1]] = rs
			}
		}
		for i, in := range b.Insts {
			if rs, ok := sites[i]; ok && !r.drop[[2]int{bi, i}] {
				// Kept instruction with a death recorded at it -- `unset`.
				// The marker goes **before** it, so the destructor runs while
				// the local still names the object, as the engine does.
				out = append(out, Inst{
					Opcode:   ReleasePrev,
					Operands: []string{rs.local, r.resolve(rs.value)},
				})
			}
			if r.drop[[2]int{bi, i}] {
				// The store is gone, but **where** it was is the one fact no
				// later pass can recover, so a marker takes its place. It
				// names the value the local stopped holding; a lifetime
				// analysis may claim it, and nothing else looks at it.
				if rs, ok := sites[i]; ok {
					out = append(out, Inst{
						Opcode:   ReleasePrev,
						Operands: []string{rs.local, r.resolve(rs.value)},
					})
				}
				if st, ok := tracks[[2]int{bi, i}]; ok {
					out = append(out, Inst{
						Opcode:   ReleasePrev,
						Operands: []string{st.local, r.resolve(st.value)},
						Attrs:    []string{ReleaseTrackAttr},
					})
				}
				continue
			}
			for j := range in.Operands {
				in.Operands[j] = r.resolve(in.Operands[j])
			}
			if in.ArgList != "" {
				in.ArgList = r.rewriteArgs(in.ArgList)
			}
			out = append(out, in)
		}
		b.Insts = out
		b.Term = r.rewriteTerm(bi, b)
	}
}

func (r *renamer) rewriteArgs(list string) string {
	inner := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(list), "("), ")")
	parts := splitCallArgs(inner)
	for i, a := range parts {
		trimmed := strings.TrimSpace(a)
		for _, mode := range []string{"byval ", "byref ", "preferref ", "unpack "} {
			if rest, ok := strings.CutPrefix(trimmed, mode); ok {
				parts[i] = mode + r.resolve(strings.TrimSpace(rest))
				goto next
			}
		}
		parts[i] = r.resolve(trimmed)
	next:
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// rewriteTerm substitutes values in a terminator and appends the arguments each
// named successor carries.
func (r *renamer) rewriteTerm(from int, b *Block) string {
	term := b.Term
	if term == "" {
		return term
	}
	var out strings.Builder
	for i := 0; i < len(term); {
		if term[i] == '%' {
			j := i + 1
			for j < len(term) && term[j] >= '0' && term[j] <= '9' {
				j++
			}
			out.WriteString(r.resolve(term[i:j]))
			i = j
			continue
		}
		if term[i] == 'b' && strings.HasPrefix(term[i:], "bb") {
			j := i + 2
			for j < len(term) && term[j] >= '0' && term[j] <= '9' {
				j++
			}
			label := term[i:j]
			out.WriteString(label)
			var to int
			if _, err := fmt.Sscanf(label, "bb%d", &to); err == nil {
				if vals := r.args[[2]int{from, to}]; len(vals) > 0 {
					for k := range vals {
						vals[k] = r.resolve(vals[k])
					}
					out.WriteString("(" + strings.Join(vals, ", ") + ")")
				}
			}
			i = j
			continue
		}
		out.WriteByte(term[i])
		i++
	}
	return out.String()
}

// liveIn is the standard backward dataflow: a place is live at a block's entry
// when some path from there reads it before writing it.
//
// It is what makes the placement *pruned*, and pruning is not an optimisation
// here: an unpruned parameter can sit at a merge the place has no value on yet,
// and there is nothing correct to pass along that edge.
func liveIn(fn *Function, cfg *CFG, places []string) []map[string]bool {
	tracked := map[string]bool{}
	for _, p := range places {
		tracked[p] = true
	}
	// Upward-exposed uses and the places each block defines.
	uses := make([]map[string]bool, len(fn.Blocks))
	defs := make([]map[string]bool, len(fn.Blocks))
	for i, b := range fn.Blocks {
		uses[i], defs[i] = map[string]bool{}, map[string]bool{}
		for _, name := range termVars(b.Term) {
			// A terminator reads: `ret $sum`, `br iter.valid $__iter5`. Missing
			// those makes a value look dead at a merge it is carried through.
			if tracked[name] {
				uses[i][name] = true
			}
		}
		for _, in := range b.Insts {
			if place, value, isStore := StoreOf(in); isStore {
				// **The stored value is a use.** A store's own place is a
				// definition, but what it stores can be another tracked place:
				// `write_var($atWordStart, $__t20)` is how a short-circuit
				// `&&` lands its result, and skipping straight past it left
				// `$__t20` looking dead at the merge block. Pruned SSA then
				// placed no parameter there, and the promotion read whichever
				// version reached first -- so `$sep !== "" && strpos(...)`
				// answered the *left* operand. Silently, and only inside a
				// function, since `{main}` is never promoted.
				if tracked[value] && !defs[i][value] {
					uses[i][value] = true
				}
				if tracked[place] && !uses[i][place] {
					defs[i][place] = true
				}
				continue
			}
			if _, v, ok := IntrinsicOf(in); ok {
				for j, a := range CallArgs(in) {
					if tracked[a] && valueArgShape(v, j) && !defs[i][a] {
						uses[i][a] = true
					}
				}
			}
		}
	}

	in := make([]map[string]bool, len(fn.Blocks))
	for i := range in {
		in[i] = map[string]bool{}
		for p := range uses[i] {
			in[i][p] = true
		}
	}
	for changed := true; changed; {
		changed = false
		for i := range fn.Blocks {
			if fn.Blocks[i].Dead {
				continue
			}
			for _, s := range cfg.Succs(i) {
				for p := range in[s] {
					if defs[i][p] || in[i][p] {
						continue
					}
					in[i][p] = true
					changed = true
				}
			}
		}
	}
	return in
}

// ShareAttr marks an instruction whose result is read out of storage and
// stands for a promoted variable, so the emitter must give it a holder of its
// own (Value.Share) -- the copy the promoted store used to make.
const ShareAttr = "share"

// markShare puts ShareAttr on the instruction defining value, when that
// instruction reads it out of storage someone else can still write: a
// variable, an element, a property, a foreach's current element, or a call's
// result. A value computed here -- a `new`, a literal, an operation -- has no
// other holder, so it needs nothing.
func markShare(fn *Function, value string) {
	def := definition(fn, value)
	if def == nil || def.Result == nil || hasAttr(*def, ShareAttr) {
		return
	}
	borrowed := false
	switch def.Opcode {
	case "call.fn", "call.method", "call.static", "call.dynamic", "place.get", "local.get":
		borrowed = true
	case IntrinsicCall:
		if len(def.Operands) > 0 {
			switch strings.Trim(def.Operands[0], `"`) {
			case "read_var", "read_var.silent", "dim.get", "dim.get.silent",
				"prop.get", "prop.get.silent", "sprop.get", "sprop.get.silent",
				"iter.current", "copy", "copy.silent":
				borrowed = true
			}
		}
	}
	if borrowed {
		def.Attrs = append(def.Attrs, ShareAttr)
	}
}
