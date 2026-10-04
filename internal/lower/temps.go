package lower

import (
	"fmt"
	"strings"
)

// Objects that die at the call that consumes them.
//
// Distinct from ConfinedObjects, and stricter. That one answers "cannot outlive
// this frame", which is enough to decide when a destructor runs. This one
// answers "is dead the moment this call returns", which is what the engine's
// **handle numbering** depends on: PHP reuses the lowest free handle, so
//
//	var_dump(new Point(1));
//	var_dump(new Point(2));
//
// prints `#1` twice -- the first temporary is gone before the second is made.
// Numbering those `#1, #2` is a wrong answer, and it is the single largest
// family in the corpus's failures: 131 files whose *first* divergence from the
// engine is an object handle and nothing else.
//
// Narrow on purpose, and failing closed. A value is claimed only when every one
// of its uses is recognised, one non-retaining call is the only thing that ever
// sees it, and it is sent by value. Anything else -- including any callee this
// cannot name -- leaves it unclaimed, and an unclaimed object keeps the old
// monotonic numbering. Being wrong in that direction costs a number; being
// wrong the other way reuses a *live* handle, which prints as some other
// object.

// DisposableTemporaries answers where object handles may be handed back, keyed
// by the value that appears in the consuming call's argument list.
//
// Keyed that way because the thing sent to the call is not always the object:
//
//	var_dump(new P())              -> "%0" releases ["%0"]
//	var_dump([new P(), new P()])   -> "%4" releases ["%0", "%3"]
//
// In the second the argument is an array built from two temporaries, and all
// three die together. The emitter sees the argument value, so that is the key.
//
// `keepsArg` reports whether a callee keeps a reference to the argument in a
// given **position**. A nil one claims nothing: with no answer there is no safe
// release point. Per position because a callee that returns one parameter --
// `function f($o, $n) { return $n; }` -- keeps nothing of the other, and the
// per-callee form refused the object for the int's sake.
func DisposableTemporaries(fn *Function, keepsArg func(callees []string, param int) bool,
	leaksThis func(method string) bool, freshReturn func(callees []string) bool) map[string][]string {
	if keepsArg == nil {
		return nil
	}
	if leaksThis == nil {
		// No method summary means no method call can be claimed, which is the
		// behaviour before methods were handled at all.
		leaksThis = func(string) bool { return true }
	}
	// The same opacity rules ConfinedObjects uses: a body that can name a local
	// indirectly can reach the object by a route this does not see.
	text := fn.String()
	for _, name := range escapeOpaque {
		// An included file reaches the frame's **variables** by name, never
		// its temporaries, which are all this claims (inheritance008: a
		// file body that requires another still frees `var_dump(f())`).
		if includeKinds[name] {
			continue
		}
		if strings.Contains(text, `"`+name+`"`) {
			return nil
		}
	}
	if strings.Contains(text, "dynvar") {
		return nil
	}
	uses := useCounts(fn)
	out := map[string][]string{}
	// **A promoted local is not a temporary.** It lives until the frame ends,
	// so releasing it at its last use is early whenever anything is allocated
	// in between -- and after promotion it has exactly a temporary's shape.
	// `ConfinedObjects` claims these instead, and the frame drain frees them.
	frameBound := fn.FrameBound
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		for _, in := range b.Insts {
			// **A method call consumes its receiver too.** `(new M)->read()`
			// leaves nothing alive once the call returns and the engine hands
			// the number straight back -- measured, including the negative
			// case: a method that stores `$this` keeps it, and the next object
			// gets a fresh handle. `LeaksThis` is what tells those apart, which
			// is why this could not be done before the method summary existed.
			if in.Opcode == "call.method" && len(in.Operands) >= 2 {
				recv := strings.TrimSpace(in.Operands[0])
				if !strings.HasPrefix(recv, "%") ||
					leaksThis(strings.Trim(in.Operands[1], `"`)) {
					continue
				}
				if _, done := out[recv]; done {
					delete(out, recv)
					continue
				}
				if frameBound[recv] {
					continue
				}
				if handles := disposableThrough(fn, recv, uses, map[string]bool{}, leaksThis, freshReturn); len(handles) > 0 {
					out[recv] = handles
				}
				continue
			}
			// **A clone consumes its operand**: `clone new X` leaves only the
			// copy, and the engine hands the original's number back as soon
			// as the clone is made (objects_031).
			if in.Opcode == "clone" && len(in.Operands) >= 1 {
				src := strings.TrimSpace(in.Operands[0])
				if !strings.HasPrefix(src, "%") || frameBound[src] {
					continue
				}
				if _, done := out[src]; done {
					delete(out, src)
					continue
				}
				if def := definition(fn, src); def != nil && def.Opcode == "new" {
					if handles := disposableThrough(fn, src, uses, map[string]bool{}, leaksThis, freshReturn); len(handles) > 0 {
						out[src] = handles
					}
				}
				continue
			}
			// **A property read consumes a fresh receiver**: `(new B)->z`
			// frees the B once z is fetched, and the next object takes its
			// number (indirect_method_call_004). The read itself may run
			// code -- `__get`, a get hook -- so the summary is asked about
			// `prop.get` as it is about a method.
			if in.Opcode == "call.intrinsic" && len(in.Operands) > 0 &&
				strings.Trim(in.Operands[0], `"`) == "prop.get" {
				args := escapeArgs(in.ArgList)
				if len(args) != 2 || leaksThis("prop.get") {
					continue
				}
				recv := strings.TrimSpace(args[0])
				if !strings.HasPrefix(recv, "%") || frameBound[recv] {
					continue
				}
				if _, done := out[recv]; done {
					delete(out, recv)
					continue
				}
				// A `new`, a clone, or a call handing back an object nobody else
				// holds: `BUG::instance()->error;` frees the BUG once the
				// property is fetched, as `(new BUG)->error` does (bug32596),
				// and `(clone $x)->p` the copy. disposableThrough asks the
				// callee summary, and `__clone`, about the latter two.
				if def := definition(fn, recv); def != nil && (def.Opcode == "new" || def.Opcode == "clone" ||
					def.Opcode == "call.fn" || def.Opcode == "call.static" || def.Opcode == "call.method") {
					if handles := disposableThrough(fn, recv, uses, map[string]bool{}, leaksThis, freshReturn); len(handles) > 0 {
						out[recv] = handles
					}
				}
				continue
			}
			// **A silent property read, and a dimension read of an object,
			// consume a fresh receiver the same way** (temporary_cleaning_013):
			// `isset((new C)->foo->bar)` frees the C once `foo` is fetched,
			// and `(new A)[0]` once `offsetGet` answers. Each may run code on
			// the receiver -- `__isset`/`__get` or a get hook for the first,
			// ArrayAccess's `offsetExists`/`offsetGet` for the second -- so
			// the summary is asked about every one of those.
			if in.Opcode == "call.intrinsic" && len(in.Operands) > 0 {
				op := strings.Trim(in.Operands[0], `"`)
				var runs []string
				switch op {
				case "prop.get.silent":
					runs = []string{"prop.get", ImpliedMethod + "__isset"}
				case "dim.get", "dim.get.silent":
					runs = []string{ImpliedMethod + "offsetget", ImpliedMethod + "offsetexists"}
				}
				if runs != nil {
					args := escapeArgs(in.ArgList)
					if len(args) != 2 {
						continue
					}
					leaky := false
					for _, m := range runs {
						leaky = leaky || leaksThis(m)
					}
					recv := strings.TrimSpace(args[0])
					if leaky || !strings.HasPrefix(recv, "%") || frameBound[recv] {
						continue
					}
					if _, done := out[recv]; done {
						delete(out, recv)
						continue
					}
					if def := definition(fn, recv); def != nil && def.Opcode == "new" {
						if handles := disposableThrough(fn, recv, uses, map[string]bool{}, leaksThis, freshReturn); len(handles) > 0 {
							out[recv] = handles
						}
					}
					continue
				}
			}
			// **A string conversion consumes a fresh object**: `new C . "x"`
			// and `(string) new C` leave only the string, and the engine frees
			// the object -- running its destructor -- the moment the
			// conversion is done (temporary_cleaning_013). The conversion runs
			// `__toString`, which the engine calls implicitly, so it is asked
			// about as an implied method.
			if in.Opcode == "call.intrinsic" && len(in.Operands) > 0 {
				op := strings.Trim(in.Operands[0], `"`)
				if op == "concat" || op == "to_string" || op == "strbuf.append" {
					if leaksThis(ImpliedMethod + "__tostring") {
						continue
					}
					args := escapeArgs(in.ArgList)
					if op == "to_string" && len(args) != 1 || op != "to_string" && len(args) != 2 {
						continue
					}
					// An interpolation's piece (`"bar{$f()}"`, ROPE_ADD) is
					// the second argument; the first is the buffer.
					if op == "strbuf.append" {
						args = args[1:]
					}
					for i, a := range args {
						a = strings.TrimSpace(a)
						if !strings.HasPrefix(a, "%") || frameBound[a] {
							continue
						}
						// `$x . $x` hands the same value twice; the first
						// release would be wrong for the second operand.
						if i == 1 && strings.TrimSpace(args[0]) == a {
							delete(out, a)
							continue
						}
						if _, done := out[a]; done {
							delete(out, a)
							continue
						}
						// A `new`, or a call handing back an object nobody else
						// holds: `"{$f()}foo"` frees what the closure made
						// once the string is built. disposableThrough asks the
						// callee summary about the latter.
						if def := definition(fn, a); def != nil && (def.Opcode == "new" || def.Opcode == "call.fn" ||
							def.Opcode == "call.static" || def.Opcode == "call.method" || def.Opcode == "call.dynamic") {
							if handles := disposableThrough(fn, a, uses, map[string]bool{}, leaksThis, freshReturn); len(handles) > 0 {
								out[a] = handles
							}
						}
					}
					continue
				}
			}
			// **An array cast consumes a fresh object** the same way: `(array)
			// new X` copies the properties out and the object is gone
			// (bug52193). A cast runs no user code.
			if in.Opcode == "call.intrinsic" && len(in.Operands) > 0 &&
				strings.Trim(in.Operands[0], `"`) == "to.array" {
				args := escapeArgs(in.ArgList)
				if len(args) != 1 {
					continue
				}
				recv := strings.TrimSpace(args[0])
				if !strings.HasPrefix(recv, "%") || frameBound[recv] {
					continue
				}
				if _, done := out[recv]; done {
					delete(out, recv)
					continue
				}
				if def := definition(fn, recv); def != nil && def.Opcode == "new" {
					if handles := disposableThrough(fn, recv, uses, map[string]bool{}, leaksThis, freshReturn); len(handles) > 0 {
						out[recv] = handles
					}
				}
				continue
			}
			if in.Opcode != "call.fn" || len(in.Operands) == 0 {
				continue
			}
			names := CalleeNames(in)
			for _, a := range sentByValueAt(in.ArgList) {
				if keepsArg(names, a.index) {
					continue
				}
				a := a.value
				if _, done := out[a]; done {
					// The same value sent to two calls: the first release
					// would be wrong for the second.
					delete(out, a)
					continue
				}
				// **A temporary that lived across a suspension** inside the
				// call's own argument list -- `take(new D, inner())`, where
				// `inner()` may suspend -- arrives through the CPS slot it
				// was demoted to (cpsTemporary).
				if strings.HasPrefix(a, "$__c") {
					if cpsTemporary(fn, a, uses) {
						out[a] = []string{a}
					}
					continue
				}
				if frameBound[a] {
					continue
				}
				if handles := disposableThrough(fn, a, uses, map[string]bool{}, leaksThis, freshReturn); len(handles) > 0 {
					out[a] = handles
				}
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// disposableThrough answers the object handles that die when `value` dies, or
// nil when that cannot be established.
//
// `value` is either an object made right here, or an array built right here out
// of such objects. Anything else -- a local, a parameter, a call result, an
// array that came from somewhere else -- is unclaimable, because this cannot
// see who else holds it.
func disposableThrough(fn *Function, value string, uses map[string]int, seen map[string]bool,
	leaksThis func(method string) bool, freshReturn func(callees []string) bool) []string {
	if seen[value] {
		return nil
	}
	seen[value] = true
	def := definition(fn, value)
	if def == nil {
		return nil
	}
	switch {
	case def.Opcode == "call.fn" || def.Opcode == "call.static" || def.Opcode == "call.method":
		// **A call that hands back a fresh object.** `var_dump(Foo::make())`
		// frees the returned object at the end of the statement exactly as
		// `var_dump(new Foo)` does; the only difference is that a `new` is
		// visible here and a call's result is not, until the callee is
		// summarised. Used once, by the consumer that brought us here.
		if uses[value] != 1 || freshReturn == nil || !freshReturn(calleeIdentity(*def)) {
			return nil
		}
		return []string{value}
	case def.Opcode == "call.dynamic":
		// **A closure written right here and called once**:
		// `(function () { return new C; })()->p` calls a body this can
		// name -- the closure literal's -- and that body's summary says
		// whether what it returns is fresh. Any other dynamic callee is
		// unknown.
		if uses[value] != 1 || freshReturn == nil || len(def.Operands) == 0 {
			return nil
		}
		callee := strings.TrimSpace(def.Operands[0])
		target := ""
		if cdef := definition(fn, callee); cdef != nil && cdef.Opcode == "closure" && len(cdef.Operands) == 1 {
			target = strings.TrimSpace(cdef.Operands[0])
		} else {
			// Or a local that can only hold one closure literal here
			// (ClosureCallees): `$f = function () {...}; $f()`.
			target = fn.ClosureCallees[callee]
		}
		if target == "" || uses[callee] != 1 || !freshReturn([]string{target}) {
			return nil
		}
		return []string{value}
	case def.Opcode == "separate":
		// Separation of an object is the object: `f()->p++` separates the
		// call's result before fetching through it, and the result is used
		// by nothing else.
		if uses[value] != 1 || len(def.Operands) != 1 {
			return nil
		}
		return disposableThrough(fn, strings.TrimSpace(def.Operands[0]), uses, seen, leaksThis, freshReturn)
	case def.Opcode == "closure.from_call" || def.Opcode == "closure":
		// **A closure made for the call alone**: `var_dump(f(...))` and
		// `var_dump(fn() => 1)` free the closure when the call returns, as a
		// `new` would (first_class_callable_optimization). What it captured
		// is not its to free.
		if uses[value] != 1 {
			return nil
		}
		return []string{value}
	case def.Opcode == "clone":
		// **A clone is a fresh object with no constructor**, so unlike a `new`
		// its only use is the consumer that brought us here. Gated on
		// `__clone`: the engine runs it on the copy, and that call is implicit
		// in the opcode rather than an instruction this can see.
		if uses[value] != 1 || leaksThis == nil || leaksThis("__clone") {
			return nil
		}
		return []string{value}
	case def.Opcode == "new":
		// Used by its own constructor and by the one consumer that brought us
		// here, and nowhere else. The `new` itself does not count as a use.
		if uses[value] != 2 {
			return nil
		}
		if !usedOnlyBy(fn, value, "call.ctor") {
			return nil
		}
		return []string{value}
	case def.Opcode == "call.intrinsic" && len(def.Operands) > 0:
		switch strings.Trim(def.Operands[0], `"`) {
		case "to.object":
			// **A cast of an array is a fresh stdClass**; a cast of an
			// object is that object, so only an operand this can see is an
			// array qualifies (object_array_cast).
			if uses[value] != 1 {
				return nil
			}
			// Intrinsic arguments carry no send mode, so the list is read
			// as is: ArgValues would cut a literal array at its first space.
			args := escapeArgs(def.ArgList)
			if len(args) != 1 || !isFreshArray(fn, strings.TrimSpace(args[0])) {
				return nil
			}
			return []string{value}
		case "array.new":
			if uses[value] != 1 {
				return nil
			}
			return []string{}
		case "array.append", "array.set", "array.set.key":
			// The array is threaded through: the container is the first
			// argument and the element the last. Both must die here.
			if uses[value] != 1 {
				return nil
			}
			args := ArgValues(def.ArgList)
			if len(args) == 0 {
				return nil
			}
			container := disposableThrough(fn, args[0], uses, seen, leaksThis, freshReturn)
			if container == nil {
				return nil
			}
			out := container
			for _, el := range args[1:] {
				if !strings.HasPrefix(el, "%") {
					// A literal key or value holds no handle.
					continue
				}
				// **An element this cannot claim is skipped, not fatal.** The
				// array dying releases each element, and one with another
				// owner simply survives it: `[new P(), $held]` frees the
				// temporary and leaves `$held` alone. Refusing the whole group
				// over one kept element gave up the handle beside it for no
				// reason -- the engine reuses it.
				out = append(out, disposableThrough(fn, el, uses, seen, leaksThis, freshReturn)...)
			}
			return out
		}
	}
	return nil
}

// usedOnlyBy reports that every instruction mentioning the value, other than
// its definition and the single consumer already accounted for, has the given
// opcode and takes it as an operand rather than as an argument.
func usedOnlyBy(fn *Function, value, opcode string) bool {
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		for _, in := range b.Insts {
			if in.Opcode == "new" && in.Result != nil && in.Result.String() == value {
				continue
			}
			inOperands := escapeMentions(in.Operands, value)
			inArgs := escapeMentions(escapeArgs(in.ArgList), value)
			if !inOperands && !inArgs {
				continue
			}
			if in.Opcode == opcode && inOperands && !inArgs {
				continue
			}
			if in.Opcode == "call.fn" || in.Opcode == "call.intrinsic" {
				// The consumer that brought us here, counted by useCounts.
				continue
			}
			// A clone is a consumer too, of its operand.
			if in.Opcode == "clone" && inOperands && !inArgs {
				continue
			}
			// A method call is a consumer too, but **only as the receiver**:
			// in the argument list it is handed to a body this cannot see.
			if in.Opcode == "call.method" && inOperands && !inArgs {
				continue
			}
			return false
		}
		if escapeToken(b.Term, value) {
			return false
		}
	}
	return true
}

// definition finds the instruction producing a value.
func definition(fn *Function, value string) *Inst {
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		for i := range b.Insts {
			if b.Insts[i].Result != nil && b.Insts[i].Result.String() == value {
				return &b.Insts[i]
			}
		}
	}
	return nil
}

// ResultUsed reports whether anything reads the value an instruction
// defines. A call whose result nobody reads is the engine's RESULT_UNUSED,
// which array_splice can observe.
func ResultUsed(fn *Function, in Inst) bool {
	if in.Result == nil {
		return false
	}
	return useCounts(fn)[in.Result.String()] > 0
}

// UseCounts is useCounts for the emitter, which asks whether a read has one
// consumer before handing that consumer the read to make itself.
func UseCounts(fn *Function) map[string]int { return useCounts(fn) }

// useCounts counts how many instructions mention each value, not counting the
// instruction that defines it. A terminator mentioning it counts, which is what
// makes a returned value unclaimable.
func useCounts(fn *Function) map[string]int {
	out := map[string]int{}
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		for _, in := range b.Insts {
			if in.Opcode == ReleasePrev {
				// The marker names a value in order to say it **died** here.
				// Counting that as a use would change every disposable claim
				// about it, in the wrong direction.
				continue
			}
			seen := map[string]bool{}
			for _, tok := range append(append([]string{}, in.Operands...), escapeArgs(in.ArgList)...) {
				for _, v := range valueTokens(tok) {
					if in.Result != nil && in.Result.String() == v {
						continue
					}
					if seen[v] {
						// Twice in one instruction is two holders.
						out[v]++
						continue
					}
					seen[v] = true
					out[v]++
				}
			}
		}
		for _, v := range valueTokens(b.Term) {
			out[v]++
		}
	}
	return out
}

// valueTokens pulls `%N` names out of a rendered operand or terminator.
func valueTokens(text string) []string {
	var out []string
	for i := 0; i < len(text); i++ {
		if text[i] != '%' {
			continue
		}
		j := i + 1
		for j < len(text) && text[j] >= '0' && text[j] <= '9' {
			j++
		}
		if j > i+1 {
			out = append(out, text[i:j])
		}
		i = j - 1
	}
	return out
}

// sentByValue is the argument values sent plainly, by value. A by-reference or
// unpacked argument hands over storage rather than a copy.
// calleeIdentity names what a call dispatches to, in the form the summary is
// keyed by: a plain call by function name (with its namespaced fallback), a
// static or method call by **method** name -- which is operand 1, the receiver
// or class being operand 0.
func calleeIdentity(in Inst) []string {
	switch in.Opcode {
	case "call.static", "call.method":
		if len(in.Operands) < 2 {
			return nil
		}
		return []string{in.Operands[1]}
	default:
		return CalleeNames(in)
	}
}

// positioned is an argument together with the parameter position it fills, so
// a per-position retention question can be asked about it.
type positioned struct {
	value string
	index int
}

// sentByValueAt is sentByValue keeping each argument's position.
//
// The index is the position in the **argument list**, which is the parameter it
// binds only while every argument occupies exactly one position. `f(...$rest)`
// breaks that -- one argument stands for however many the array holds, and
// every position after it is unknowable here -- so a call carrying an unpack is
// refused outright rather than mapped wrongly. Positions are taken over the
// whole list, so a `byref` argument in the middle still shifts nothing.
func sentByValueAt(list string) []positioned {
	args := escapeArgs(list)
	for _, a := range args {
		if strings.HasPrefix(strings.TrimSpace(a), "unpack") {
			return nil
		}
	}
	var out []positioned
	for i, a := range args {
		a = strings.TrimSpace(a)
		rest, ok := strings.CutPrefix(a, "byval ")
		if !ok {
			// **`preferref` is by value for every callee this can claim.** The
			// mode means the engine decides at run time, and it decides by
			// reference only for a by-reference parameter -- which no callee
			// on the library allow-list has, and which makes a compiled body
			// refuse wholesale. A namespaced call emits it for every argument,
			// so skipping the mode meant skipping namespaced calls entirely.
			rest, ok = strings.CutPrefix(a, "preferref ")
		}
		if !ok {
			continue
		}
		if rest = strings.TrimSpace(rest); rest != "" {
			out = append(out, positioned{value: rest, index: i})
		}
	}
	return out
}

func sentByValue(list string) []string {
	var out []string
	for _, a := range escapeArgs(list) {
		a = strings.TrimSpace(a)
		rest, ok := strings.CutPrefix(a, "byval ")
		if !ok {
			continue
		}
		if rest = strings.TrimSpace(rest); rest != "" {
			out = append(out, rest)
		}
	}
	return out
}

// ArgValues is the value of each argument in a rendered argument list, with the
// send mode stripped: `(byval %0, byref $x)` gives `%0` and `$x`. The emitter
// needs the values alone to ask whether one is disposable.
func ArgValues(list string) []string {
	var out []string
	for _, a := range escapeArgs(list) {
		a = strings.TrimSpace(a)
		if i := strings.IndexByte(a, ' '); i >= 0 {
			a = a[i+1:]
		}
		if a = strings.TrimSpace(a); a != "" {
			out = append(out, a)
		}
	}
	return out
}

// SoleUseClosures answers the closure values created and handed to exactly one
// call, by value, and mentioned nowhere else.
//
// That is a proof of **sole ownership at the moment of the call**, which is what
// lets a callee take the object over -- a lazy object's initializer is the case
// this exists for. It is deliberately narrower than `DisposableTemporaries`:
// that one also decides *when* the object dies, which for an initializer is not
// at the call at all but whenever the lazy object first initialises, if ever.
//
// Fails closed. A closure stored in a variable, captured, returned, or passed
// twice is not claimed, and an unclaimed one simply keeps its handle -- which
// is the engine's behaviour for a closure something else still holds.
func SoleUseClosures(fn *Function) map[string]bool {
	text := fn.String()
	for _, name := range escapeOpaque {
		// An included file reaches the frame's **variables** by name, never
		// its temporaries, which are all this claims (inheritance008: a
		// file body that requires another still frees `var_dump(f())`).
		if includeKinds[name] {
			continue
		}
		if strings.Contains(text, `"`+name+`"`) {
			return nil
		}
	}
	if strings.Contains(text, "dynvar") {
		return nil
	}
	uses := useCounts(fn)
	out := map[string]bool{}
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		for _, in := range b.Insts {
			if in.Opcode != "closure" || in.Result == nil {
				continue
			}
			v := in.Result.String()
			if uses[v] != 1 {
				continue
			}
			// The one use must be a by-value argument, not a capture binding,
			// a store, or a terminator.
			for _, b2 := range fn.Blocks {
				if b2.Dead {
					continue
				}
				for _, in2 := range b2.Insts {
					if in2.Opcode != "call.fn" && in2.Opcode != "call.method" &&
						in2.Opcode != "call.static" {
						continue
					}
					for _, a := range sentByValue(in2.ArgList) {
						if a == v {
							out[v] = true
						}
					}
				}
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// isFreshArray reports an operand that is certainly an array: an array
// literal, or a value an array-building intrinsic or a cast to array made.
func isFreshArray(fn *Function, value string) bool {
	if strings.HasPrefix(value, "[") {
		return true
	}
	def := definition(fn, value)
	if def == nil || def.Opcode != "call.intrinsic" || len(def.Operands) == 0 {
		return false
	}
	switch strings.Trim(def.Operands[0], `"`) {
	case "to.array", "array.new", "array.set", "array.append":
		return true
	}
	return false
}

// ArrayAddRelease is where the objects of an array `+`'s fresh operand die.
type ArrayAddRelease struct {
	// Handles are the fresh objects the operand array was built from.
	Handles []string
	// AtStore says the sum is stored by the very next instruction -- a
	// compound `+=` -- and the engine frees the operand only once that
	// store is done, so the release waits for it.
	AtStore bool
}

// ArrayAddTemporaries answers the array `+` sums whose operand is an array
// literal built right here out of fresh objects, used by nothing else.
//
// The engine frees such an operand the moment the sum is made (for `+=`, once
// the sum is stored), and each object in it dies unless the sum took it in --
// `[0] + [new C]` keeps key 0 from the left and drops the C, `[] + [new C]`
// keeps it. Which one happened is a run-time question, so the release is
// guarded by it (disposeUnlessHeld): an object the sum holds is left alone,
// and nothing else can hold one -- the literal was the only thing that ever
// did, and the sum runs no code of the program's.
func ArrayAddTemporaries(fn *Function, leaksThis func(method string) bool,
	freshReturn func(callees []string) bool) map[string]ArrayAddRelease {
	text := fn.String()
	for _, name := range escapeOpaque {
		if includeKinds[name] {
			continue
		}
		if strings.Contains(text, `"`+name+`"`) {
			return nil
		}
	}
	if strings.Contains(text, "dynvar") {
		return nil
	}
	uses := useCounts(fn)
	out := map[string]ArrayAddRelease{}
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		for i, in := range b.Insts {
			if in.Opcode != "call.intrinsic" || in.Result == nil || len(in.Operands) == 0 ||
				strings.Trim(in.Operands[0], `"`) != "add" {
				continue
			}
			args := escapeArgs(in.ArgList)
			if len(args) != 2 {
				continue
			}
			var handles []string
			for j, a := range args {
				a = strings.TrimSpace(a)
				if !strings.HasPrefix(a, "%") || fn.FrameBound[a] ||
					j == 1 && strings.TrimSpace(args[0]) == a {
					continue
				}
				def := definition(fn, a)
				if def == nil || def.Opcode != "call.intrinsic" || len(def.Operands) == 0 {
					continue
				}
				switch strings.Trim(def.Operands[0], `"`) {
				case "array.append", "array.set", "array.set.key":
				default:
					continue
				}
				handles = append(handles, disposableThrough(fn, a, uses, map[string]bool{}, leaksThis, freshReturn)...)
			}
			if len(handles) == 0 {
				continue
			}
			sum := in.Result.String()
			release := ArrayAddRelease{Handles: handles}
			if i+1 < len(b.Insts) && StoresValue(b.Insts[i+1], sum) {
				release.AtStore = true
			}
			out[sum] = release
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// StoresValue reports that an instruction stores the value into a variable
// or a place: `write_var($v, %s)`, `slot.set $v, %s`, `store %p, %s`.
func StoresValue(in Inst, value string) bool {
	switch {
	case in.Opcode == "store" || in.Opcode == "slot.set":
		return len(in.Operands) >= 2 && strings.TrimSpace(in.Operands[1]) == value
	case in.Opcode == "call.intrinsic" && len(in.Operands) > 0 && strings.Trim(in.Operands[0], `"`) == "write_var":
		args := escapeArgs(in.ArgList)
		return len(args) == 2 && strings.TrimSpace(args[1]) == value
	}
	return false
}

// LiveOnError answers, for each instruction position ("block.index", the
// emitter's own spelling), the claimed temporaries alive there that the
// engine frees if that instruction throws.
//
// The engine keeps a **live range** for every temporary: from the opcode
// that makes it to the one that consumes it. An exception raised anywhere
// inside the range frees the temporary while it unwinds -- running its
// destructor, whose throw takes the first exception as its previous
// (temporary_cleaning_013: `$a[0] += [new D]` whose `offsetGet` fails frees
// the literal, and the D with it).
//
// Only a claim whose making and consuming sit in one block is covered, from
// the instruction after the last one that builds it (the constructor call,
// an append) up to the consumer. The consumer itself is covered when it is
// an intrinsic -- an operator or a fetch frees its operands when it fails --
// and left out when it is a call: a failing user function's trace keeps its
// arguments, and a method call's receiver is released on its own failure
// path already.
func LiveOnError(fn *Function, disposable map[string][]string, arrayAdd map[string]ArrayAddRelease,
	places map[string]PlaceRelease) map[string][]string {
	if len(disposable) == 0 && len(arrayAdd) == 0 && len(places) == 0 {
		return nil
	}
	out := map[string][]string{}
	mentions := func(in Inst, set map[string]bool) bool {
		for _, tok := range append(append([]string{}, in.Operands...), escapeArgs(in.ArgList)...) {
			for _, v := range valueTokens(tok) {
				if set[v] {
					return true
				}
			}
		}
		return in.Result != nil && set[in.Result.String()]
	}
	// chain grows the set by every array literal built from it: an append
	// naming an element (or the literal built so far) makes the next
	// literal, so the last append is the last instruction that builds it.
	chain := func(b *Block, seed []string) map[string]bool {
		set := map[string]bool{}
		for _, v := range seed {
			set[v] = true
		}
		for changed := true; changed; {
			changed = false
			for _, in := range b.Insts {
				if in.Result == nil || set[in.Result.String()] || in.Opcode != "call.intrinsic" || len(in.Operands) == 0 {
					continue
				}
				switch strings.Trim(in.Operands[0], `"`) {
				case "array.append", "array.set", "array.set.key":
					if mentions(in, set) {
						set[in.Result.String()] = true
						changed = true
					}
				}
			}
		}
		return set
	}
	cover := func(b *Block, handles []string, consumer, release int, withConsumer bool) {
		set := chain(b, handles)
		start := -1
		for i := 0; i < consumer; i++ {
			if mentions(b.Insts[i], set) {
				start = i
			}
		}
		if start < 0 {
			return
		}
		for _, h := range handles {
			made := false
			for i := 0; i <= start; i++ {
				if b.Insts[i].Result != nil && b.Insts[i].Result.String() == h {
					made = true
				}
			}
			if !made {
				return
			}
		}
		end := release
		if !withConsumer && end == consumer {
			end = consumer - 1
		}
		for i := start + 1; i <= end; i++ {
			pos := fmt.Sprintf("%d.%d", b.ID, i)
			out[pos] = append(out[pos], handles...)
		}
	}
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		for _, rel := range places {
			if rel.Block == b.ID {
				cover(b, rel.Handles, rel.Consumer, rel.Release, true)
			}
		}
		for i, in := range b.Insts {
			for key, handles := range disposable {
				if in.Result != nil && in.Result.String() == key {
					continue
				}
				if !mentions(in, map[string]bool{key: true}) || in.Opcode == "call.ctor" {
					continue
				}
				switch {
				case in.Opcode == "call.intrinsic" && len(in.Operands) > 0:
					op := strings.Trim(in.Operands[0], `"`)
					if op == "array.append" || op == "array.set" || op == "array.set.key" {
						continue
					}
					cover(b, handles, i, i, true)
				case in.Opcode == "call.fn" || in.Opcode == "call.method" || in.Opcode == "call.static":
					cover(b, handles, i, i, false)
				case in.Opcode == "clone":
					cover(b, handles, i, i, true)
				}
			}
			if in.Result == nil {
				continue
			}
			if rel, ok := arrayAdd[in.Result.String()]; ok {
				release := i
				if rel.AtStore {
					release = i + 1
				}
				cover(b, rel.Handles, i, release, true)
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// PlaceRelease is a fresh receiver fetched through by one place -- a
// read-write property or dimension fetch -- and where it dies.
type PlaceRelease struct {
	Handles []string
	// Block is the block's ID; Consumer the place-making instruction's
	// index there, Release the index of the place's last use.
	Block, Consumer, Release int
}

// PlaceTemporaries answers the fresh receivers that a read-write fetch --
// `f()->p++`, `f()[0] += 1`, `(new C)->p .= "x"` -- goes through and nothing
// else touches, keyed by the place value.
//
// The engine frees such a receiver once the whole operation is done: after
// the place's last use, its store (temporary_cleaning_013). The fetch and the
// store may run code on the receiver -- `__get`/`__set` or hooks for a
// property, ArrayAccess's methods for a dimension -- so the summary is asked
// about each.
func PlaceTemporaries(fn *Function, leaksThis func(method string) bool,
	freshReturn func(callees []string) bool) map[string]PlaceRelease {
	if leaksThis == nil {
		return nil
	}
	text := fn.String()
	for _, name := range escapeOpaque {
		if includeKinds[name] {
			continue
		}
		if strings.Contains(text, `"`+name+`"`) {
			return nil
		}
	}
	if strings.Contains(text, "dynvar") {
		return nil
	}
	uses := useCounts(fn)
	out := map[string]PlaceRelease{}
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		for i, in := range b.Insts {
			if in.Opcode != "call.intrinsic" || in.Result == nil || len(in.Operands) == 0 {
				continue
			}
			var runs []string
			switch strings.Trim(in.Operands[0], `"`) {
			case "prop.ref", "prop.ref.rw":
				runs = []string{"prop.get", "prop.set", ImpliedMethod + "__set", ImpliedMethod + "__isset"}
			case "dim.ref", "dim.ref.rw":
				runs = []string{ImpliedMethod + "offsetget", ImpliedMethod + "offsetset", ImpliedMethod + "offsetexists"}
			default:
				continue
			}
			leaky := false
			for _, m := range runs {
				leaky = leaky || leaksThis(m)
			}
			args := escapeArgs(in.ArgList)
			if leaky || len(args) != 2 {
				continue
			}
			recv := strings.TrimSpace(args[0])
			if !strings.HasPrefix(recv, "%") || fn.FrameBound[recv] || uses[recv] != 1 {
				continue
			}
			def := definition(fn, recv)
			if def == nil || def.Opcode == "new" {
				// A `new` receiver has its constructor as a second use; the
				// fetch shapes here come from calls.
				continue
			}
			handles := disposableThrough(fn, recv, uses, map[string]bool{}, leaksThis, freshReturn)
			if len(handles) == 0 {
				continue
			}
			// The place's last use, in this block; one used past the
			// block is left alone.
			place := in.Result.String()
			last := -1
			for j := i + 1; j < len(b.Insts); j++ {
				for _, tok := range append(append([]string{}, b.Insts[j].Operands...), escapeArgs(b.Insts[j].ArgList)...) {
					for _, v := range valueTokens(tok) {
						if v == place {
							last = j
						}
					}
				}
			}
			if last < 0 || strings.Contains(b.Term, place) {
				continue
			}
			out[place] = PlaceRelease{Handles: handles, Block: b.ID, Consumer: i, Release: last}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// cpsTemporary reports a compiler slot that carries a fresh object to one
// consumer and nothing else. Two shapes put a call's argument in a slot when
// something between it and the call suspends: the CPS rewrite demotes `%N` to
// `$__cpsN` (demoteAcross) when a later argument is a call that may suspend
// -- `take(new D, inner())` -- and the lowering spills an argument to a
// `$__c` slot when a later one yields -- `take(new D, yield)`. Either way the
// slot is stored once, from a `new` mentioned only by its constructor and that
// store, and read once, by the argument that brought us here. Then the slot is
// the temporary, held across the suspension by the frame and by nothing a
// program can name -- the shape `disposableThrough` claims for a `new` used by
// its constructor and one consumer.
func cpsTemporary(fn *Function, slot string, uses map[string]int) bool {
	value := ""
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		for _, in := range b.Insts {
			if in.Opcode == SlotSet && len(in.Operands) == 2 && strings.TrimSpace(in.Operands[0]) == slot {
				if value != "" {
					return false
				}
				value = strings.TrimSpace(in.Operands[1])
			}
		}
	}
	if !strings.HasPrefix(value, "%") {
		return false
	}
	def := definition(fn, value)
	if def == nil || def.Opcode != "new" || fn.FrameBound[value] || uses[value] != 2 {
		return false
	}
	stores, mentions := 0, 0
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		for _, in := range b.Insts {
			if in.Opcode == "new" && in.Result != nil && in.Result.String() == value {
				continue
			}
			if in.Opcode == SlotSet && len(in.Operands) == 2 && strings.TrimSpace(in.Operands[0]) == slot {
				if strings.TrimSpace(in.Operands[1]) != value {
					return false
				}
				stores++
				continue
			}
			inOperands := escapeMentions(in.Operands, value)
			if escapeMentions(escapeArgs(in.ArgList), value) || inOperands && in.Opcode != "call.ctor" {
				return false
			}
			if escapeMentions(in.Operands, slot) || escapeMentions(escapeArgs(in.ArgList), slot) {
				mentions++
			}
		}
		if escapeToken(b.Term, value) || escapeToken(b.Term, slot) {
			return false
		}
	}
	return stores == 1 && mentions == 1
}
