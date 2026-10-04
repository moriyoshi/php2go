package lower

import "strings"

// Which objects provably do not outlive the frame that made them.
//
// The question this answers is narrow on purpose. PHP destroys a local when the
// frame is destroyed, which is at return -- so for an object that **cannot**
// have been stored anywhere else, "destroy it when the function returns" is not
// an approximation, it is the engine's own timing. An `unset($x)` on such a
// local is likewise exact.
//
// Conservative in one direction only: a site is claimed only when every use of
// it is accounted for, and anything unrecognised leaves it unclaimed. An
// unclaimed object still gets destroyed -- later, by script-end teardown -- so
// the analysis can only ever move a destructor **earlier**, never lose one.
// That asymmetry is what makes it safe to be wrong here, and is why the rules
// below refuse rather than guess.

// A body containing any of these can name a local this analysis cannot see.
var escapeOpaque = []string{"extract", "compact", "get_defined_vars", "eval", "func_get_args",
	"include", "include_once", "require", "require_once"}

// includeKinds are the flavours of an `include` instruction in escapeOpaque.
var includeKinds = map[string]bool{"include": true, "include_once": true, "require": true, "require_once": true}

// ConfinedObjects answers the `new` sites whose object provably does not
// outlive the frame, keyed by the instruction's **result value** (`%0`).
//
// Keyed by the value rather than by a local because there may be no local:
// `$a = new D(); $a->m();` has its slot promoted away when nothing reads `$a`
// afterwards, and the object is confined all the same.
//
// `leaksThis` reports whether a method stores `$this` -- the single largest
// reason a site goes unclaimed, since a method call hands the object over as
// `$this`. Pass nil to treat every method call as an escape.
func ConfinedObjects(fn *Function, leaksThis func(method string) bool) map[string]bool {
	return ConfinedObjectsWith(fn, nil, leaksThis, nil)
}

// ConfinedObjectsWith is the same question with a `keepsArg` predicate in hand:
// a plain call that keeps no reference to the argument the object occupies --
// `var_dump($o)` -- leaves the object in the frame. Without one, any `call.fn`
// is an escape, which is what the two-argument form assumes.
func ConfinedObjectsWith(fn *Function, keepsArg func(callees []string, param int) bool,
	leaksThis func(method string) bool, argKept func(method string) bool) map[string]bool {
	return ConfinedObjectsFor(fn, keepsArg, leaksThis, argKept, nil)
}

// ConfinedObjectsFor is ConfinedObjectsWith with `delegable` in hand: whether
// an object of a class may be handed to `yield from` and stay the frame's --
// an Iterator whose methods keep no `$this` (delegatedOnly). nil refuses
// every delegation.
func ConfinedObjectsFor(fn *Function, keepsArg func(callees []string, param int) bool,
	leaksThis func(method string) bool, argKept func(method string) bool,
	delegable func(class string) bool) map[string]bool {
	ea := &escapes{fn: fn, keepsArg: keepsArg, leaksThis: leaksThis, argKept: argKept, delegable: delegable}
	text := fn.String()
	for _, name := range escapeOpaque {
		if strings.Contains(text, `"`+name+`"`) {
			return nil
		}
	}
	// **A variable variable does not survive lowering as `$$`.** It becomes a
	// `dynvar` place, which is the shape to look for -- the text check this
	// replaced passed a body that writes through `$$n`, which can name any
	// local including the one holding the object.
	if strings.Contains(text, "dynvar") {
		return nil
	}
	out := map[string]bool{}
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		for _, in := range b.Insts {
			if in.Result == nil || !makesFreshObject(in, leaksThis) {
				continue
			}
			ea.class = ""
			if in.Opcode == "new" && len(in.Operands) > 0 {
				ea.class = strings.Trim(strings.TrimSpace(in.Operands[0]), `"`)
			}
			if ea.value(in.Result.String()) {
				out[in.Result.String()] = true
			}
		}
	}
	return out
}

// ConfinedIfInlined is ConfinedObjectsWith with a **return not counted as an
// escape**: the sites this body could confine if it were inlined into its
// caller.
//
// It answers an upper bound and not a fact. Inlining moves the object into the
// caller's frame, so whether it is confined *there* is then the caller's
// question -- this only says the callee itself does nothing else with it. The
// gap between this and `ConfinedObjectsWith` is what inlining has to work with;
// how much of that gap is realised depends on every caller.
//
// Measured rather than assumed, because the two dominant escape shapes pull
// opposite ways: an object that is only returned becomes confinable when the
// callee disappears, and an object stored into a property is no more confined
// in the caller than it was in the callee.
func ConfinedIfInlined(fn *Function, keepsArg func(callees []string, param int) bool,
	leaksThis func(method string) bool, argKept func(method string) bool) map[string]bool {
	ea := &escapes{fn: fn, keepsArg: keepsArg, leaksThis: leaksThis,
		argKept: argKept, allowReturn: true}
	text := fn.String()
	for _, name := range escapeOpaque {
		if strings.Contains(text, `"`+name+`"`) {
			return nil
		}
	}
	if strings.Contains(text, "dynvar") {
		return nil
	}
	out := map[string]bool{}
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		for _, in := range b.Insts {
			if in.Result == nil || !makesFreshObject(in, leaksThis) {
				continue
			}
			if ea.value(in.Result.String()) {
				out[in.Result.String()] = true
			}
		}
	}
	return out
}

// objectFactories are methods that answer a **fresh** object and keep no
// reference to it, so their result is as confinable as a `new`.
//
// A hand-written set, and it has to be: whether a callee retains what it
// returns is not in any signature. Both of these are measured -- a lazy object
// made in a function and dropped there runs its destructor at the frame's end,
// exactly as `new C()` would, which is only true because nothing else holds it.
var objectFactories = map[string]bool{
	"newlazyghost": true,
	"newlazyproxy": true,
}

// makesFreshObject reports whether an instruction produces an object this frame
// is the sole owner of.
//
// `new` is the obvious one and was for a long time the only one, which is why
// **a lazy object never ran its destructor**: it comes from a method call, so
// the analysis never saw a definition to claim and the object was left to
// script-end teardown, where a frame-local's destructor is far too late.
func makesFreshObject(in Inst, leaksThis func(method string) bool) bool {
	if in.Opcode == "new" {
		return true
	}
	// **A clone is as fresh as a `new`**, and is how a frame acquires an object
	// without one: `$c = clone $obj` leaves the copy owned by this frame alone.
	// Gated on `__clone`, because the engine runs it on the copy and this walk
	// never sees that call -- it is implicit in the opcode, so a `__clone` that
	// stores `$this` would hand the copy away invisibly.
	if in.Opcode == "clone" {
		return leaksThis != nil && !leaksThis("__clone")
	}
	if in.Opcode != "call.method" || len(in.Operands) < 2 {
		return false
	}
	return objectFactories[strings.ToLower(strings.Trim(in.Operands[1], `"`))]
}

// escapes carries the two questions the walk cannot answer from the body
// alone: whether a method stores `$this`, and whether a callee keeps a
// reference to an argument. Both are properties of *other* bodies.
type escapes struct {
	fn        *Function
	keepsArg  func(callees []string, param int) bool
	leaksThis func(method string) bool
	// argKept answers a **different question** from leaksThis: whether a
	// method stores an object passed to it as an argument, rather than its
	// receiver. `$r->initializeLazyObject($obj)` keeps neither, and sharing one
	// list for both would make each answer wrong for the other's sake.
	argKept func(method string) bool
	// weakMap answers whether a local only ever holds a `new WeakMap`, so an
	// object used as a key of it is not kept (weakKeyUse). Nil answers no:
	// only the file-body release walk, which can ask whether another body
	// binds the name as a global, sets it.
	weakMap func(local string) bool
	// weakRef is the same for a local holding WeakReferences
	// (WeakRefOnlyLocal).
	weakRef func(local string) bool
	// allowReturn makes `ret` not an escape, for the one question where it is
	// not: whether a body hands back an object **nobody else holds**. Returning
	// is exactly what such a value does, so the ordinary walk -- which counts
	// the terminator as an escape -- can never answer it.
	allowReturn bool
	// delegable and class answer the `yield from` question (delegatedOnly):
	// the class the `new` under question makes, and whether an object of it
	// can be delegated to without being kept.
	delegable func(class string) bool
	class     string
}

// passesThrough reports a return-type check that this walk may see through.
//
// **Only while `allowReturn` is set**, and the asymmetry is the whole point.
// Treating `coerce_return` as pass-through says "the checked value *is* this
// object" -- which is what makes `return new A()` in a typed function a return
// of the `new`. For the freshness question that is exactly right. For the
// confinement question it is backwards: the terminator then mentions only the
// checked value, so the `new` looks like it never leaves the frame, and the
// frame drain destroys an object the caller is about to receive. That cost
// `added_interface_intersection_type` a pass before this was split.
func (e *escapes) passesThrough(in Inst, value string) bool {
	if !e.allowReturn || !isVerifyReturn(in) {
		return false
	}
	src, ok := verifySource(in)
	return ok && escapeToken(src, value)
}

// termEscapes reports whether a block terminator lets the value out.
func (e *escapes) termEscapes(term, value string) bool {
	if !escapeToken(term, value) {
		return false
	}
	if e.delegatedOnly(term, value) {
		return false
	}
	return !(e.allowReturn && strings.HasPrefix(strings.TrimSpace(term), "ret "))
}

// delegatedOnly reports a `yield from` over the value as its only mention in
// the terminator, where no method the delegation calls on it stores `$this`.
//
// **The delegation is not a holder that outlives the frame.** The generator
// keeps its source only while the `yield from` runs, and lets go of it when
// the delegation ends or the generator does -- so a local handed to it still
// dies with the frame (bug71013: `$f = new FooBar; yield from $f;` is
// destroyed as the body ends). A generator source is not a `new`, so what
// can reach here is an Iterator or IteratorAggregate, whose methods run with
// the object as `$this`; one that stores it hands it on. `delegable` answers
// that for the class the `new` made.
func (e *escapes) delegatedOnly(term, value string) bool {
	// The terminator's one operand: termEscapes has seen it mention value.
	if !strings.HasPrefix(strings.TrimSpace(term), "ret.yield_from ") || e.delegable == nil || e.class == "" {
		return false
	}
	return e.delegable(e.class)
}

// keeps reports whether a plain call may retain the argument `value` occupies.
//
// Per **position**, not per callee, because the coarser question answers wrong
// far more often than it looks: `function f($o, $n) { return $n; }` returns a
// parameter, so "does f keep any argument" is yes -- and the object in the
// other position loses its release for a reason that has nothing to do with it.
func (e *escapes) keeps(in Inst, value string) bool {
	if e.keepsArg == nil || len(in.Operands) == 0 {
		return true
	}
	// The value is what is being **called** -- `$o()` reaching `__invoke`,
	// whose body is not this summary's to know. Not an argument, so the
	// per-position question does not apply and the answer is the safe one.
	if escapeToken(in.Operands[0], value) || escapeMentions(in.Operands[1:], value) {
		return true
	}
	names := CalleeNames(in)
	for i, a := range escapeArgs(in.ArgList) {
		if escapeToken(a, value) && e.keepsArg(names, i) {
			return true
		}
	}
	return false
}

// value reports whether a `new` result stays inside the frame.
func (e *escapes) value(value string) bool {
	fn, leaksThis := e.fn, e.leaksThis
	local := ""
	// marked is the promoted local the release markers say holds the value.
	// **A value two locals hold is not confined**: `$q = $p` copies the
	// object into a second local, and once both are promoted the only trace
	// of either binding is its markers -- so `$p = null` released an object
	// `$q` still held. Markers naming the value's CPS slot count too.
	marked := ""
	cpsSlot := "$__cps" + strings.TrimPrefix(value, "%")
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		for _, in := range b.Insts {
			if in.Opcode == ReleasePrev && len(in.Operands) >= 2 {
				if op := strings.TrimSpace(in.Operands[1]); op == value || op == cpsSlot {
					l := strings.TrimSpace(in.Operands[0])
					if marked != "" && marked != l {
						return false
					}
					marked = l
				}
			}
			uses := append(append([]string{}, in.Operands...), escapeArgs(in.ArgList)...)
			if !escapeMentions(uses, value) {
				continue
			}
			switch in.Opcode {
			case "new":
				// its own definition
			case FrameDrainOpcode, "frame.of.frame_drain":
				// The drain that destroys it at its statement's end
				// (InsertSiteDrains): its death, not a holder.
			case "call.ctor":
				// **As an argument, it is contained.** `new Holder(new X)`
				// hands `X` to a constructor that stores it in the new object,
				// so `X` lives as long as `Holder` does -- and treating every
				// mention here as the receiver destroyed `X` at the frame's end
				// while the returned `Holder` still held it. Kept unless the
				// constructor is measured not to keep an argument, which no
				// compiled one is yet: there is no per-method argument summary.
				if escapeMentions(escapeArgs(in.ArgList), value) {
					if e.argKept == nil || e.argKept("__construct") {
						return false
					}
				}
				// As the receiver, whether `$this` escapes is the
				// constructor's own business, answered per class by
				// `CtorLeaksThis` where the site is emitted.
			case "call.method":
				if !methodSafeWith(in, value, leaksThis, e.argKept) {
					return false
				}
			case "call.intrinsic":
				if e.passesThrough(in, value) {
					continue
				}
				// **Binding the value to a local**, which is `slot.set` only
				// when the slot survived promotion. A parameter never does:
				// it arrives as `recv` and is written straight into its local
				// with `write_var`, so a walk that knew only `slot.set` saw an
				// unrecognised use and refused every parameter.
				if boundLocal, ok := writesLocal(in, value); ok {
					if local != "" && local != boundLocal {
						return false
					}
					local = boundLocal
					continue
				}
				if echoedOnly(in, value) && e.class != "" && leaksThis != nil &&
					!leaksThis("string:"+e.class) {
					continue
				}
				if !intrinsicSafe(in, value) {
					return false
				}
			case ReleasePrev:
				// Not a use: the marker records that the local stopped holding
				// this value here, which is the opposite of keeping it.
			case "class_name", "instanceof":
				// Asking an object what class it is, or whether it is one, is
				// a read. `$obj::class` is the shape, and refusing it made any
				// parameter a body asks that question of look retained.
				if in.Result != nil && in.Result.String() == value {
					return false
				}
			case "clone":
				// **Being cloned is a read.** The copy is a new object with a
				// new handle; the source goes nowhere. Treating it as an escape
				// made every parameter that gets cloned look retained, which is
				// most of the lazy-object corpus.
				if in.Result != nil && in.Result.String() == value {
					return false
				}
			case "call.fn", "frame.of.fn":
				// The value reaches a call that keeps nothing in the position
				// it occupies, so it is still the frame's. `var_dump(new D)`
				// is the shape. A call that may suspend builds the callee's
				// frame instead (`frame.of.fn`): the arguments bind to its
				// parameters exactly as the call's would, and the callee's
				// summary answers for them the same way.
				if e.keeps(in, value) {
					return false
				}
			case "slot.set":
				if local != "" && local != in.Operands[0] {
					return false
				}
				local = in.Operands[0]
			default:
				return false
			}
		}
		if e.termEscapes(b.Term, value) {
			return false
		}
	}
	// A marked local and a bound one are two holders, unless the bound one
	// is only the CPS slot the value was demoted to.
	if marked != "" && local != "" && local != marked && local != cpsSlot {
		return false
	}
	// No local at all: the slot was promoted away because nothing reads it
	// again, and the object never left the frame.
	if local == "" {
		return true
	}
	return e.local(local)
}

// local is the same question for the local itself.
func (e *escapes) local(local string) bool {
	fn := e.fn
	// **A WeakMap-only local hands its map nowhere**: every read is a
	// dimension's container, a count, an inspection or a clone into another
	// such local (WeakMapOnlyLocal). Not a WeakReference local: create()
	// answers the one WeakReference an object already has, so another
	// local may hold the same one.
	if e.weakMap != nil && e.weakMap(local) {
		return true
	}
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		for _, in := range b.Insts {
			// The local named directly as a weak container's key.
			if e.weakReferenceCreate(in, local) || e.weakKeyUse(in, local) {
				continue
			}
			if args := escapeArgs(in.ArgList); escapeMentions(args, local) {
				// Reading the local is not an escape; the read's *result* is
				// what decides.
				if in.Opcode == "call.intrinsic" && len(in.Operands) > 0 && escapeIsRead(in.Operands[0]) {
					if in.Result != nil && !e.derived(in.Result.String()) {
						return false
					}
					continue
				}
				// Handing the local to a call that keeps nothing does not put
				// it anywhere the frame cannot still own it.
				if (in.Opcode == "call.fn" || in.Opcode == "frame.of.fn") && !e.keeps(in, local) {
					continue
				}
				// The local's own **binding site**. `write_var($obj, %0)` is
				// where the value arrives; reading it as a use of `$obj` made
				// every local refuse at its own definition.
				if in.Opcode == "call.intrinsic" && len(in.Operands) > 0 &&
					strings.Trim(in.Operands[0], `"`) == "write_var" {
					if a := escapeArgs(in.ArgList); len(a) == 2 && escapeToken(a[0], local) &&
						!escapeToken(a[1], local) {
						continue
					}
					return false
				}
				// Writing **through** the local -- `$obj->prop = "42"` -- puts
				// the value in the object, not the object anywhere. The same
				// rule `value` applies to a `new` result, which `local` was
				// missing: a parameter written through was read as escaping.
				if in.Opcode == "call.intrinsic" &&
					(intrinsicSafe(in, local) || e.passesThrough(in, local)) {
					continue
				}
				if in.Opcode == "call.method" && methodSafeWith(in, local, e.leaksThis, e.argKept) {
					continue
				}
				if readsObject(in) && (in.Result == nil || in.Result.String() != local) {
					continue
				}
				return false
			}
			if !escapeMentions(in.Operands, local) {
				continue
			}
			switch in.Opcode {
			case "slot.set":
				if in.Operands[0] != local {
					return false
				}
			case "call.intrinsic":
				if len(in.Operands) == 0 || !escapeIsRead(in.Operands[0]) {
					return false
				}
			case ReleasePrev:
				// Names the local and the value it dropped; neither is a use.
			case "local.get":
				// **The value of an assignment**, read back right after the
				// write: `var_dump($a = new X)`. The engine hands on the
				// assigned value, taken before the old one is destroyed
				// (gh10168/assign), and the emitter reads it back in that
				// order (readBackAfterRelease) -- so it is the new value and
				// nothing a destructor did, and only where it goes decides.
				// Any other shape is refused.
				if len(in.Operands) != 1 || in.Result == nil || !ReadsBackWrite(b, in) ||
					!e.derived(in.Result.String()) {
					return false
				}
			case "unset":
				// **`unset($a)` is the opposite of an escape.** It is where the
				// local stops holding the object, and treating it as an
				// unrecognised use made every unset local refuse -- so the
				// object was not even confined, let alone released at the
				// unset.
				if in.Operands[0] != local {
					return false
				}
			case "call.method":
				// **A CPS slot as the receiver.** `$__cpsN` is `%N`, demoted to
				// the frame across a suspension (demoteAcross), and the rewrite
				// names the slot where `%N` was the receiver -- so it gets the
				// rule `value` gives the receiver it stands for. A real local
				// is read into a value first and never lands here.
				if !strings.HasPrefix(local, "$__cps") || !methodSafeWith(in, local, e.leaksThis, e.argKept) {
					return false
				}
			case "clone", "class_name", "instanceof":
				// The local named **directly** as the operand of a read-only
				// opcode: `$obj::class` is `class_name $obj`, with the local in
				// the operands rather than in an argument list. The same rule
				// was already allowed for arguments, and not having it here
				// made a body that asks `$obj::class` look like one that keeps
				// `$obj` -- which is most of the lazy-object corpus.
				if in.Result != nil && in.Result.String() == local {
					return false
				}
			default:
				return false
			}
		}
		if e.termEscapes(b.Term, local) {
			return false
		}
	}
	return true
}

// derived follows a value read out of the local.
func (e *escapes) derived(value string) bool {
	fn, leaksThis := e.fn, e.leaksThis
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		for _, in := range b.Insts {
			if in.Result != nil && in.Result.String() == value {
				continue
			}
			args := escapeArgs(in.ArgList)
			// **An instruction that does not mention the value cannot leak
			// it.** Without this the `call.method` arm below fired for *every*
			// method call in the body, receiver or not -- so any body
			// containing a single method call refused every value read out of
			// a local, whatever that call was about. It is the same mention
			// guard `value` and `local` already open with; only this walk was
			// missing it.
			if !escapeMentions(args, value) && !escapeMentions(in.Operands, value) {
				continue
			}
			switch {
			case in.Opcode == ReleasePrev:
				// Not a use; see escape.go's ReleasePrev handling in `value`.
			case in.Opcode == "call.method":
				if !methodSafeWith(in, value, leaksThis, e.argKept) {
					return false
				}
			case readsObject(in) && (in.Result == nil || in.Result.String() != value):
			case in.Opcode == "call.intrinsic" &&
				(intrinsicSafe(in, value) || e.passesThrough(in, value)):
			case in.Opcode == "call.fn" && !e.keeps(in, value) &&
				!escapeMentions(in.Operands[1:], value):
			case in.Opcode == "call.intrinsic" && len(in.Operands) == 1 &&
				identityCompare[strings.Trim(in.Operands[0], `"`)]:
				// `$a === $o` asks whether two values are the same object and
				// answers a bool; nothing is kept and no user code runs.
			case e.weakReferenceCreate(in, value), e.weakKeyUse(in, value):
				// A weak container takes no reference to the object it is
				// keyed by or points at.
			default:
				return false
			}
		}
		if e.termEscapes(b.Term, value) {
			return false
		}
	}
	return true
}

// **Weak containers.** A WeakMap keyed by an object and a WeakReference
// pointing at one take no reference to it, so neither is an escape -- but each
// can hand the object back: iterating the map yields its keys, and `get()`
// yields the target. A program that stores what comes back holds the object
// strongly, and releasing the local then would free something live. So the
// container itself must be one this body can see all of, and everything read
// out of it must only be inspected (weakInspected).

// weakInspectFns are library functions that look at an object and keep
// nothing: the same fact nonRetainingCallee records, by hand and measured,
// but restricted to ones that cannot hand the object on to anything.
var weakInspectFns = map[string]bool{
	"var_dump": true, "debug_zval_dump": true, "print_r": true, "var_export": true,
	"spl_object_id": true, "spl_object_hash": true, "get_class": true,
	"gettype": true, "get_debug_type": true, "is_object": true, "count": true,
}

// identityCompare are `===` and `!==`, which compare handles.
var identityCompare = map[string]bool{"strict_equal": true, "not_strict_equal": true}

// weakInspectOps are the intrinsics that only compare, count or ask a type
// (`=== null` is a type_check).
var weakInspectOps = map[string]bool{"strict_equal": true, "not_strict_equal": true, "count": true,
	"type_check": true}

// weakInspected reports that every use of value only looks at it: an
// argument of a weakInspectFns call or a weakInspectOps intrinsic, and
// nothing else. Each call name a namespaced call may resolve to must be one
// of them.
func weakInspected(fn *Function, value string) bool {
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		if escapeToken(b.Term, value) {
			return false
		}
		for _, in := range b.Insts {
			args := escapeArgs(in.ArgList)
			if !escapeMentions(in.Operands, value) && !escapeMentions(args, value) {
				continue
			}
			if !weakInspecting(in, value) {
				return false
			}
		}
	}
	return true
}

func weakInspecting(in Inst, value string) bool {
	switch in.Opcode {
	case "call.fn":
		if len(in.Operands) == 0 || escapeMentions(in.Operands, value) {
			return false
		}
		for _, name := range CalleeNames(in) {
			if !weakInspectFns[strings.ToLower(strings.Trim(strings.TrimSpace(name), `"`))] {
				return false
			}
		}
		return true
	case "call.intrinsic":
		return len(in.Operands) == 1 && weakInspectOps[strings.Trim(in.Operands[0], `"`)]
	}
	return false
}

// weakLocal checks a local that holds only weak containers of one kind:
// every write stores a value `made` accepts, and every mention is that
// write, a read whose result `read` accepts, an `unset`, an `isset`
// (bool-valued reads) or the container operand of a dimension operation. A
// reference to it, a capture, a by-reference argument or a `global` binding
// elsewhere (reachedByGlobal) could put something else there or hand it
// on, so any other mention refuses.
func weakLocal(fn *Function, local string, reachedByGlobal func(name string) bool,
	made func(value string) bool, read func(value string) bool, dims bool) bool {
	if fn == nil || reachedByGlobal == nil || reachedByGlobal(strings.TrimPrefix(local, "$")) {
		return false
	}
	wrote := false
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		if escapeToken(b.Term, local) {
			return false
		}
		for _, in := range b.Insts {
			args := escapeArgs(in.ArgList)
			if !escapeMentions(in.Operands, local) && !escapeMentions(args, local) {
				continue
			}
			if target, value, ok := localWrite(in); ok && target == local {
				if escapeToken(value, local) || !made(value) {
					return false
				}
				wrote = true
				continue
			}
			switch {
			case in.Opcode == "unset" && len(in.Operands) == 1 && strings.TrimSpace(in.Operands[0]) == local:
			case in.Opcode == "isset" && dims && len(in.Operands) >= 1 &&
				strings.TrimSpace(in.Operands[0]) == local && !escapeMentions(in.Operands[1:], local):
			case in.Opcode == ReleasePrev:
			case in.Opcode == "call.intrinsic" && len(in.Operands) == 1 && len(args) >= 1 &&
				strings.TrimSpace(args[0]) == local && !escapeMentions(args[1:], local):
				op := strings.Trim(in.Operands[0], `"`)
				switch {
				case op == "read_var" || op == "read_var.silent":
					if in.Result == nil || !read(in.Result.String()) {
						return false
					}
				case escapeIsRead(op):
					// isset / empty / the undefined-container warning:
					// a bool or nothing, never the container.
				case dims && strings.HasPrefix(op, "dim."):
				default:
					return false
				}
			default:
				return false
			}
		}
	}
	return wrote
}

// WeakMapOnlyLocal reports a local that only ever holds a fresh
// `new WeakMap` -- or a clone of another such local -- and whose reads are
// only the container of a dimension operation, `count`, cloned into another
// such local, or inspected: never iterated, never handed on, so no key can
// come back out of it.
func WeakMapOnlyLocal(fn *Function, local string, reachedByGlobal func(name string) bool) bool {
	return weakMapOnly(fn, local, reachedByGlobal, map[string]bool{})
}

func weakMapOnly(fn *Function, local string, reachedByGlobal func(name string) bool, visiting map[string]bool) bool {
	if visiting[local] {
		// Asked again while answering: assume it, and let the other
		// conditions refute it -- the same optimistic fixpoint the retention
		// summary runs, sound for the same reason.
		return true
	}
	visiting[local] = true
	other := func(operand string) (string, bool) {
		def := definition(fn, strings.TrimSpace(operand))
		if def == nil || def.Opcode != "call.intrinsic" || len(def.Operands) == 0 ||
			strings.Trim(def.Operands[0], `"`) != "read_var" {
			return "", false
		}
		args := escapeArgs(def.ArgList)
		if len(args) != 1 || !strings.HasPrefix(strings.TrimSpace(args[0]), "$") {
			return "", false
		}
		return strings.TrimSpace(args[0]), true
	}
	made := func(value string) bool {
		def := definition(fn, value)
		if def == nil || len(def.Operands) == 0 {
			return false
		}
		switch def.Opcode {
		case "new":
			return strings.EqualFold(strings.TrimPrefix(strings.Trim(def.Operands[0], `"`), `\`), "WeakMap")
		case "clone":
			src, ok := other(def.Operands[0])
			return ok && weakMapOnly(fn, src, reachedByGlobal, visiting)
		}
		return false
	}
	read := func(value string) bool {
		for _, b := range fn.Blocks {
			if b.Dead {
				continue
			}
			if escapeToken(b.Term, value) {
				return false
			}
			for _, in := range b.Insts {
				args := escapeArgs(in.ArgList)
				if !escapeMentions(in.Operands, value) && !escapeMentions(args, value) {
					continue
				}
				switch {
				case in.Opcode == "call.intrinsic" && len(in.Operands) == 1 &&
					strings.HasPrefix(strings.Trim(in.Operands[0], `"`), "dim.") &&
					len(args) >= 1 && strings.TrimSpace(args[0]) == value && !escapeMentions(args[1:], value):
				case in.Opcode == "call.method" && len(in.Operands) == 2 &&
					strings.TrimSpace(in.Operands[0]) == value && len(args) == 0 &&
					strings.EqualFold(strings.Trim(in.Operands[1], `"`), "count"):
				case in.Opcode == "clone" && len(in.Operands) == 1 && in.Result != nil &&
					strings.TrimSpace(in.Operands[0]) == value:
					// The copy is another WeakMap over the same keys, so it
					// must go only where this one may.
					if !clonedIntoWeakMap(fn, in.Result.String(), reachedByGlobal, visiting) {
						return false
					}
				case weakInspecting(in, value):
				default:
					return false
				}
			}
		}
		return true
	}
	return weakLocal(fn, local, reachedByGlobal, made, read, true)
}

// clonedIntoWeakMap reports a clone whose only use is its store into a
// WeakMap-only local.
func clonedIntoWeakMap(fn *Function, value string, reachedByGlobal func(name string) bool, visiting map[string]bool) bool {
	stored := false
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		if escapeToken(b.Term, value) {
			return false
		}
		for _, in := range b.Insts {
			if !escapeMentions(in.Operands, value) && !escapeMentions(escapeArgs(in.ArgList), value) {
				continue
			}
			target, v, ok := localWrite(in)
			if !ok || strings.TrimSpace(v) != value || !weakMapOnly(fn, target, reachedByGlobal, visiting) {
				return false
			}
			stored = true
		}
	}
	return stored
}

// weakRefHandedBack reports that a WeakReference value only ever gives its
// target to inspection: its own uses are inspected, or `get()` whose result
// is.
func weakRefHandedBack(fn *Function, value string) bool {
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		if escapeToken(b.Term, value) {
			return false
		}
		for _, in := range b.Insts {
			args := escapeArgs(in.ArgList)
			if !escapeMentions(in.Operands, value) && !escapeMentions(args, value) {
				continue
			}
			if !weakRefUse(fn, in, value) {
				return false
			}
		}
	}
	return true
}

// weakRefUse is one use of a WeakReference that gives nothing away: its
// `get()` with the result only inspected, or an inspection of it.
func weakRefUse(fn *Function, in Inst, value string) bool {
	if in.Opcode == "call.method" && len(in.Operands) == 2 &&
		strings.TrimSpace(in.Operands[0]) == value && len(escapeArgs(in.ArgList)) == 0 &&
		strings.EqualFold(strings.Trim(in.Operands[1], `"`), "get") {
		return in.Result == nil || weakInspected(fn, in.Result.String())
	}
	return weakInspecting(in, value)
}

// weakRefCreated reports a value that is a `WeakReference::create(...)`.
func weakRefCreated(fn *Function, value string) bool {
	def := definition(fn, value)
	return def != nil && isWeakReferenceCreate(*def)
}

func isWeakReferenceCreate(in Inst) bool {
	if in.Opcode != "call.static" || len(in.Operands) < 2 {
		return false
	}
	cls := strings.TrimPrefix(strings.Trim(strings.TrimSpace(in.Operands[0]), `"`), `\`)
	return strings.EqualFold(cls, "WeakReference") &&
		strings.EqualFold(strings.Trim(strings.TrimSpace(in.Operands[1]), `"`), "create")
}

// weakReferenceCreate reports `WeakReference::create(value)` whose result
// can never give value back to anything that keeps it: the WeakReference is
// only inspected, or stored in a local (weakRef) whose every read is. The
// engine frees the object at its last strong holder whatever points at it
// weakly (weakrefs_001). WeakReference is a final engine class, so no
// program declares a `create` under that name.
func (e *escapes) weakReferenceCreate(in Inst, value string) bool {
	if e.weakRef == nil || !isWeakReferenceCreate(in) {
		return false
	}
	args := escapeArgs(in.ArgList)
	if len(args) != 1 || !escapeToken(args[0], value) || escapeMentions(in.Operands, value) {
		return false
	}
	if in.Result == nil {
		return true
	}
	made := in.Result.String()
	for _, b := range e.fn.Blocks {
		if b.Dead {
			continue
		}
		if escapeToken(b.Term, made) {
			return false
		}
		for _, use := range b.Insts {
			uargs := escapeArgs(use.ArgList)
			if !escapeMentions(use.Operands, made) && !escapeMentions(uargs, made) {
				continue
			}
			if target, v, ok := localWrite(use); ok && strings.TrimSpace(v) == made {
				if !e.weakRef(target) {
					return false
				}
				continue
			}
			if !weakRefUse(e.fn, use, made) {
				return false
			}
		}
	}
	return true
}

// WeakRefOnlyLocal reports a local that only ever holds a
// `WeakReference::create(...)` result and whose reads only give the target
// to inspection.
func WeakRefOnlyLocal(fn *Function, local string, reachedByGlobal func(name string) bool) bool {
	made := func(value string) bool { return weakRefCreated(fn, value) }
	read := func(value string) bool { return weakRefHandedBack(fn, value) }
	return weakLocal(fn, local, reachedByGlobal, made, read, false)
}

// weakKeyUse reports that value is only the **key** of a dimension operation
// on a WeakMap -- `$map[$o] = 1`, `$map[$o]`, `isset($map[$o])`,
// `unset($map[$o])`, `$map[$o]++`. A WeakMap holds its key weakly, so the key
// has not gone anywhere; the value written (`$map[$o] = $o`) still has.
//
// The container has to be a local that only ever holds a `new WeakMap` and is
// never iterated or handed on (WeakMapOnlyLocal). Any other container may
// keep its key: an ArrayAccess offsetSet, an SplObjectStorage.
func (e *escapes) weakKeyUse(in Inst, value string) bool {
	if e.weakMap == nil {
		return false
	}
	var container, key string
	var rest []string
	switch {
	case in.Opcode == "call.intrinsic" && len(in.Operands) == 1 &&
		strings.HasPrefix(strings.Trim(in.Operands[0], `"`), "dim."):
		args := escapeArgs(in.ArgList)
		if len(args) < 2 {
			return false
		}
		container, key, rest = args[0], args[1], args[2:]
	case in.Opcode == "isset" && len(in.Operands) == 2 && len(in.Attrs) == 0:
		container, key = in.Operands[0], in.Operands[1]
	default:
		return false
	}
	if !escapeToken(key, value) || escapeToken(container, value) || escapeMentions(rest, value) {
		return false
	}
	return e.weakMapContainer(strings.TrimSpace(container))
}

// weakMapContainer resolves a dimension's container to a WeakMap-only local:
// the local itself, or a `read_var` of it.
func (e *escapes) weakMapContainer(operand string) bool {
	if strings.HasPrefix(operand, "$") {
		return e.weakMap(operand)
	}
	def := definition(e.fn, operand)
	if def == nil || def.Opcode != "call.intrinsic" || len(def.Operands) == 0 ||
		strings.Trim(def.Operands[0], `"`) != "read_var" {
		return false
	}
	args := escapeArgs(def.ArgList)
	if len(args) != 1 || !strings.HasPrefix(strings.TrimSpace(args[0]), "$") {
		return false
	}
	return e.weakMap(strings.TrimSpace(args[0]))
}

// methodSafe reports whether a `call.method` keeps the object in the frame. It
// does when the value is the receiver and the method is known not to store
// `$this`; a value appearing as an *argument* always escapes.
func methodSafe(in Inst, value string, leaksThis func(string) bool) bool {
	return methodSafeWith(in, value, leaksThis, nil)
}

// methodSafeWith is methodSafe with the **argument** question answerable too.
// Whether a method stores what it is passed is a different fact from whether it
// stores its receiver, and the two need separate lists: sharing one makes each
// answer wrong for the other's sake.
func methodSafeWith(in Inst, value string, leaksThis func(string) bool,
	argKept func(method string) bool) bool {
	if escapeMentions(escapeArgs(in.ArgList), value) {
		// As an argument. Safe only for a method measured not to keep one;
		// unknown means kept.
		if argKept == nil || len(in.Operands) < 2 {
			return false
		}
		return !argKept(strings.Trim(in.Operands[1], `"`))
	}
	if len(in.Operands) == 0 || !escapeToken(in.Operands[0], value) {
		return false
	}
	if leaksThis == nil {
		return false
	}
	name := ""
	if len(in.Operands) > 1 {
		name = strings.Trim(in.Operands[1], `"`)
	}
	return !leaksThis(name)
}

// intrinsicSafe reports whether an intrinsic keeps the object in the frame.
// A property access **on** it does; anything else is assumed not to.
func intrinsicSafe(in Inst, value string) bool {
	if len(in.Operands) == 0 {
		return false
	}
	name := strings.Trim(in.Operands[0], `"`)
	if escapeIsRead(name) {
		return true
	}
	if !strings.HasPrefix(name, "prop.") {
		return false
	}
	args := escapeArgs(in.ArgList)
	// Only as the receiver: `$o->p = $other` stores `$other` somewhere this
	// analysis does not follow.
	return len(args) > 0 && escapeToken(args[0], value) &&
		!escapeMentions(args[1:], value)
}

// writesLocal reports that this instruction stores `value` into a local, and
// names it. The local is the destination, so a walk must go on to ask whether
// *it* stays in the frame.
func writesLocal(in Inst, value string) (string, bool) {
	if in.Opcode != "call.intrinsic" || len(in.Operands) == 0 {
		return "", false
	}
	if strings.Trim(in.Operands[0], `"`) != "write_var" {
		return "", false
	}
	args := escapeArgs(in.ArgList)
	// Only as the value written, never as the name written to.
	if len(args) != 2 || !escapeToken(args[1], value) || escapeToken(args[0], value) {
		return "", false
	}
	return args[0], true
}

// CalleeNames is every name a call may resolve to: the one written, plus the
// `fallback=` a namespaced call drops to when the namespaced name does not
// exist. Inside `namespace test\baz`, `var_dump($o)` compiles to a call to
// `test\baz\var_dump` with `var_dump` as the fallback, and a summary that
// looked only at the first saw an unknown function -- which is why every
// namespaced test kept objects a global-scope one released.
//
// Both are returned, in resolution order, because which one runs is not known
// here. The caller resolves: the first name it **recognises** is the one that
// answers. OR-ing them instead is wrong in the common case -- a namespaced name
// that exists nowhere cannot keep anything, but it reads as "unknown callee,
// assume it keeps", which is how every namespaced call stayed unclaimed even
// after the fallback was being read.
func CalleeNames(in Inst) []string {
	if len(in.Operands) == 0 {
		return nil
	}
	out := []string{in.Operands[0]}
	for _, a := range in.Attrs {
		if rest, ok := strings.CutPrefix(a, "fallback="); ok && strings.TrimSpace(rest) != "" {
			out = append(out, rest)
		}
	}
	return out
}

// readsObject reports an opcode that only looks at its operand: a clone takes
// a copy and leaves the source where it was, and `$o::class` / `instanceof`
// ask a question about it. None of them stores it anywhere.
func readsObject(in Inst) bool {
	switch in.Opcode {
	case "clone", "class_name", "instanceof":
		return true
	}
	return false
}

// isVerifyReturn reports the return-type check in either of its two shapes: the
// opcode the lowering emits, and the intrinsic call it becomes once the
// specializer has picked `CoerceReturn` for it.
func isVerifyReturn(in Inst) bool {
	if in.Opcode == "verify_return" {
		return true
	}
	if in.Opcode != "call.intrinsic" || len(in.Operands) == 0 {
		return false
	}
	// **The specialized name is `coerce_return`.** The lowering emits
	// `verify_return`; the specializer rewrites it, and matching only the
	// pre-specialization spelling found nothing -- the summary runs after.
	switch strings.Trim(in.Operands[0], `"`) {
	case "verify_return", "coerce_return":
		return true
	}
	return false
}

// verifySource peels a return-type check to the value that went into it.
func verifySource(in Inst) (string, bool) {
	if !isVerifyReturn(in) {
		return "", false
	}
	if in.Opcode == "verify_return" && len(in.Operands) > 0 {
		return strings.TrimSpace(in.Operands[0]), true
	}
	if args := escapeArgs(in.ArgList); len(args) > 0 {
		return strings.TrimSpace(args[0]), true
	}
	return "", false
}

func escapeIsRead(operand string) bool {
	switch strings.Trim(operand, `"`) {
	// `warn.undefined_container` only asks whether its variable is set: a
	// read, not a place the value could escape to. Counting it as a use
	// kept `$obj->a++`'s receiver alive past its last real use, and the
	// handles that follow shifted (lazy_objects/fetch_op_initializes).
	case "read_var", "read_var.silent", "isset", "empty", "warn.undefined_container":
		return true
	}
	return false
}

func escapeMentions(list []string, name string) bool {
	for _, item := range list {
		if escapeToken(item, name) {
			return true
		}
	}
	return false
}

// escapeToken reports whether name appears as a whole token, so that `%1` does
// not match `%10` and `$a` does not match `$ab`.
func escapeToken(text, name string) bool {
	for i := 0; ; {
		j := strings.Index(text[i:], name)
		if j < 0 {
			return false
		}
		j += i
		after := j + len(name)
		if after >= len(text) || !escapeTokenByte(text[after]) {
			return true
		}
		i = after
	}
}

func escapeTokenByte(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b == '_'
}

func escapeArgs(list string) []string {
	inner := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(list), "("), ")")
	if inner == "" {
		return nil
	}
	return strings.Split(inner, ",")
}

// ReadsBackWrite reports a `local.get` that directly follows a write of the
// same local in its block: the lowering's spelling of an assignment
// expression's value.
func ReadsBackWrite(b *Block, in Inst) bool {
	if in.Opcode != "local.get" || len(in.Operands) != 1 || in.Result == nil {
		return false
	}
	local := strings.TrimSpace(in.Operands[0])
	for i := range b.Insts {
		if b.Insts[i].Result == nil || b.Insts[i].Result.String() != in.Result.String() {
			continue
		}
		if i == 0 {
			return false
		}
		target, _, ok := localWrite(b.Insts[i-1])
		return ok && target == local
	}
	return false
}

// echoedOnly reports `echo` of the value itself. Printing an object converts it
// to a string, which either runs its `__toString` with the object as `$this`
// or throws; whether either keeps it is the class's question (`string:<class>`
// of leaksThis), asked by the caller.
func echoedOnly(in Inst, value string) bool {
	if len(in.Operands) == 0 || strings.Trim(in.Operands[0], `"`) != "echo" {
		return false
	}
	args := escapeArgs(in.ArgList)
	return len(args) == 1 && escapeToken(args[0], value)
}
