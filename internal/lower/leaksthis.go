package lower

import "strings"

// Whether a method lets its receiver outlive the call.
//
// `ConfinedObjects` takes a `leaksThis` callback and has always been handed
// **nil**, which makes every method call on an object count as an escape. That
// single nil is the difference between claiming 9% and 26% of `new` sites
// (measured, Phase A item 6), and it is also what blocks releasing the handle
// of an object whose constructor threw.
//
// The lowering makes this unusually easy to answer, which is worth stating
// because it is not obvious from the source. Using the receiver to reach its
// own properties leaves `$this` a **literal token** in the instruction:
//
//	call.intrinsic "prop.set"($this, "x", 1)
//	call.intrinsic "prop.get"($this, "x")
//	call.intrinsic "prop.ref"($this, "arr")
//	isset $this, "x", on=prop
//
// while every way of letting it escape first materialises it as a value:
//
//	%0:*obj = this      <- storing it, capturing it, returning it, passing it
//
// So the question is almost "does the body contain a `this` instruction". Two
// things stop it being exactly that, and both are handled by refusing:
// `call.method $this, "m"()` keeps `$this` literal but hands the receiver to
// another body this does not summarise, and an opaque body can reach it by a
// route none of this sees.

// thisSafeIntrinsics are the operations that take the receiver as a literal and
// cannot let it out: every one of them reaches a property of the object and
// yields the property, never the object.
var thisSafeIntrinsics = map[string]bool{
	"prop.get":        true,
	"prop.get.silent": true,
	"prop.set":        true,
	"prop.ref":        true,
	"prop.ref.rw":     true,
	"prop.ref.unset":  true,
	"prop.isset":      true,
	"prop.unset":      true,
}

// LeaksThis reports whether the receiver can outlive a call to this body,
// knowing nothing about the rest of the unit: every method the body calls on
// `$this` (and every string conversion or dump that runs one) counts as a
// leak. LeaksThisWith answers the same question given a summary of the
// unit's methods.
//
// Fail-closed: anything unrecognised leaks. A caller may therefore treat a
// false as a promise and a true as "no answer", which is the direction that
// keeps a wrong summary from freeing a live object.
func LeaksThis(fn *Function) bool { return LeaksThisWith(fn, nil) }

// thisPureFns are library functions that look at an object argument and run
// no code of it: they answer from the handle or the class alone.
var thisPureFns = map[string]bool{
	"get_class": true, "spl_object_id": true, "spl_object_hash": true,
	"gettype": true, "get_debug_type": true, "is_object": true,
}

// thisMethodFns are library functions that keep nothing of an object argument
// but may run one of its methods on it: the dumps ask `__debugInfo`, count
// asks Countable's `count`. Passing `$this` to one is as safe as calling that
// method on it.
var thisMethodFns = map[string]string{
	"var_dump": "__debuginfo", "print_r": "__debuginfo", "debug_zval_dump": "__debuginfo",
	"count": "count",
}

// thisPureIntrinsics are the intrinsics that ask a value's type and nothing
// else: `$this === null` lowers to a type_check, gettype to get_type.
var thisPureIntrinsics = map[string]bool{"type_check": true, "get_type": true}

// ImpliedMethod prefixes a method name LeaksThisWith asks about because the
// engine runs it implicitly (a dump's `__debugInfo`, a conversion's
// `__toString`, `count`), not because the body calls it by name. Such a call
// never falls back to `__call`, so a summary answers an undeclared one as
// keeping nothing -- the engine's own behaviour runs.
const ImpliedMethod = "implied:"

// thisStaticForwards are the `call.static` scopes that hand the caller's
// `$this` to the callee without naming it: `parent::m()`, `self::m()`,
// `static::m()` -- and `A::m()` naming an ancestor, which the lowering spells
// as a quoted class name, so every quoted name counts as forwarding too.
func thisStaticForwards(scope string) bool {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "parent", "self", "static":
		return true
	}
	return strings.HasPrefix(strings.TrimSpace(scope), `"`)
}

// LeaksThisWith is LeaksThis given methodLeaks, which answers whether a
// method of that lower-case name may keep its receiver (nil: every one may).
//
// **What does not let the receiver out.** Reaching its own properties
// (`$this->p`, `$this->p = v` with v not `$this`), which the lowering keeps
// literal; materialising it (`%0 = this`) only to compare it (`===`,
// `instanceof`), clone it, ask its class or id, or hand it by value to a
// library function in thisPureFns; and calling a method on it -- directly,
// through `parent::`/`self::`/`static::`, through a dump (`__debugInfo`),
// `count` or a string conversion (`__toString`) -- whose summary keeps
// nothing.
//
// **What does.** Anything else the materialised value meets: a store, a
// return or yield, a by-reference or unknown callee, an argument of another
// object's method, a capture (`closure`), a callable (`[$this, 'm']`,
// `$this->m(...)`). And three things that never name the receiver at all:
// an opaque body (dynamic variables, eval, include, compact...), a
// **generator** body, whose Generator keeps its frame -- and so `$this` --
// alive after the call returns (measured: `unset($a)` with `$g = $a->g()`
// live must not destroy `$a`), and a `call.static` that forwards `$this`
// to a method this cannot see.
func LeaksThisWith(fn *Function, methodLeaks func(string) bool) bool {
	if fn == nil {
		return true
	}
	method := func(name string) bool {
		if methodLeaks == nil {
			return true
		}
		return methodLeaks(strings.ToLower(strings.Trim(strings.TrimSpace(name), `"`)))
	}
	// implied is a method the engine runs on its own -- a dump's
	// `__debugInfo`, a conversion's `__toString`, `count`'s Countable::count.
	// Never through `__call`, so an undeclared one is the engine's own and
	// keeps nothing (ImpliedMethod).
	implied := func(name string) bool { return method(ImpliedMethod + name) }
	// A body that can name a local indirectly, or that suspends and so has a
	// frame outliving its own execution, is not summarised at all.
	text := fn.String()
	for _, name := range escapeOpaque {
		if strings.Contains(text, `"`+name+`"`) {
			return true
		}
	}
	if strings.Contains(text, "dynvar") {
		return true
	}
	// The receiver values this body materialises: `%0 = this`.
	receivers := map[string]bool{}
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		// A generator's frame outlives the call inside the Generator it
		// returns. Both spellings: the lowering's terminators and the CPS
		// rewrite's `ret.*` that replaces them.
		for _, p := range []string{"generator_create", "yield", "ret.generator", "ret.yield"} {
			if strings.HasPrefix(b.Term, p) {
				return true
			}
		}
		for _, in := range b.Insts {
			if in.Opcode == "this" && in.Result != nil {
				receivers[in.Result.String()] = true
			}
		}
	}
	isRecv := func(tok string) bool {
		tok = strings.TrimSpace(tok)
		return tok == "$this" || receivers[tok]
	}
	mentions := func(in Inst) bool {
		if escapeMentions(in.Operands, "$this") || escapeMentions(escapeArgs(in.ArgList), "$this") {
			return true
		}
		for r := range receivers {
			if escapeMentions(in.Operands, r) || escapeMentions(escapeArgs(in.ArgList), r) {
				return true
			}
		}
		return false
	}
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		for _, in := range b.Insts {
			// `parent::$p::get()` runs the parent's hook on this receiver,
			// and hook bodies are not in the method summary.
			if strings.HasPrefix(in.Opcode, "call.parent_hook") {
				return true
			}
			switch in.Opcode {
			case "this":
				continue
			case "closure":
				// **A closure binds `$this` without naming it.** `function() {
				// return $this; }` inside a method lowers to a bare `closure`
				// instruction whose body is elsewhere -- nothing in *this* body
				// mentions the receiver at all. A `static function()` binds
				// nothing and is refused with the rest: the cost is a missed
				// claim.
				return true
			case "call.static":
				// `parent::m()` and friends pass the receiver implicitly.
				if len(in.Operands) >= 2 && thisStaticForwards(in.Operands[0]) && method(in.Operands[1]) {
					return true
				}
			}
			if !mentions(in) {
				continue
			}
			args := ArgValues(in.ArgList)
			rawArgs := escapeArgs(in.ArgList)
			// argsFree reports that no argument from index from on is the
			// receiver.
			argsFree := func(list []string, from int) bool {
				for i := from; i < len(list); i++ {
					if isRecv(list[i]) {
						return false
					}
				}
				return true
			}
			switch in.Opcode {
			case "call.intrinsic":
				if len(in.Operands) == 0 {
					return true
				}
				name := strings.Trim(in.Operands[0], `"`)
				switch {
				case thisSafeIntrinsics[name]:
					// Only as the **receiver**, which is the first argument,
					// and only literally: as a later one it is a value being
					// written somewhere.
					if len(args) == 0 || strings.TrimSpace(args[0]) != "$this" || !argsFree(args, 1) {
						return true
					}
				case identityCompare[name]:
					// `===` and `!==` compare handles.
				case thisPureIntrinsics[name]:
					// Asking the type answers from the handle.
				case name == "echo":
					// Printing converts: `__toString` runs on the receiver.
					if implied("__tostring") {
						return true
					}
				case name == "count":
					// Countable::count, when the class has one.
					if implied("count") || !argsFree(args, 1) {
						return true
					}
				default:
					return true
				}
			case "isset":
				// `isset($this->x)` renders the receiver as an operand with an
				// `on=prop` attribute; anything else asking about `$this`
				// itself is not this shape.
				if !hasAttribute(in, "on=prop") {
					return true
				}
			case "get_class", "instanceof", "clone":
				// Asking the class, or copying the properties into a new
				// object: nothing keeps the receiver itself.
				if len(in.Operands) == 0 || !isRecv(in.Operands[0]) || !argsFree(in.Operands, 1) || !argsFree(args, 0) {
					return true
				}
			case "call.method":
				// The receiver is the first operand; as an argument it is
				// handed on to a body this does not summarise.
				if len(in.Operands) < 2 || !isRecv(in.Operands[0]) || !argsFree(args, 0) || method(in.Operands[1]) {
					return true
				}
			case "call.fn":
				// Every name a namespaced call may resolve to must keep
				// nothing, and every argument that is the receiver must go by
				// value.
				if len(in.Operands) == 0 || escapeMentions(in.Operands, "$this") {
					return true
				}
				for r := range receivers {
					if escapeMentions(in.Operands, r) {
						return true
					}
				}
				for _, a := range rawArgs {
					a = strings.TrimSpace(a)
					f := strings.Fields(a)
					if len(f) == 2 && isRecv(f[1]) && f[0] != "byval" || len(f) == 1 && isRecv(f[0]) {
						return true
					}
				}
				for _, callee := range CalleeNames(in) {
					n := strings.ToLower(strings.Trim(strings.TrimSpace(callee), `"`))
					if thisPureFns[n] {
						continue
					}
					if m, ok := thisMethodFns[n]; ok && !implied(m) {
						continue
					}
					return true
				}
			default:
				return true
			}
		}
		if escapeToken(b.Term, "$this") {
			return true
		}
		for r := range receivers {
			if escapeToken(b.Term, r) {
				return true
			}
		}
	}
	return false
}

// MethodThisLeaks summarises, over every method body of a unit keyed by
// lower-case name, which names may keep their receiver: the least fixpoint of
// LeaksThisWith, so a method leaks only if some body of that name reaches a
// leak, directly or through a method it calls on `$this`. Recursion alone
// leaks nothing.
//
// A name no body declares leaks: it is answered by `__call`, an engine
// parent or nothing this can see. So does every name once any `__call` or
// `__callStatic` body leaks, since a declared name in one class may be
// undeclared in the receiver's.
//
// callersSeeThis says a body of the unit can reach its caller's receiver
// without being handed it -- `debug_backtrace()`, whose frames carry the
// object by default -- and makes every name leak.
func MethodThisLeaks(methods map[string][]*Function, callersSeeThis bool) func(string) bool {
	if callersSeeThis {
		return func(string) bool { return true }
	}
	leaks := map[string]bool{}
	anyCall := func() bool { return leaks["__call"] || leaks["__callstatic"] }
	answer := func(name string) bool {
		if n, ok := strings.CutPrefix(name, ImpliedMethod); ok {
			// The engine's implicit call reaches only a declared method.
			return leaks[n]
		}
		if _, ok := methods[name]; !ok {
			return true
		}
		return leaks[name] || anyCall()
	}
	for changed := true; changed; {
		changed = false
		for name, bodies := range methods {
			if leaks[name] {
				continue
			}
			for _, body := range bodies {
				if LeaksThisWith(body, answer) {
					leaks[name] = true
					changed = true
					break
				}
			}
		}
	}
	return answer
}

// ReachesCallerThis reports whether any of the bodies can reach a caller's
// receiver without being handed it: `debug_backtrace()`, whose frames carry
// `object` by default. Any mention of the name counts -- a call, or a string
// a dynamic call or callback could use -- which is the same closed-world
// assumption the rest of the summary makes about names it can read.
func ReachesCallerThis(bodies []*Function) bool {
	for _, fn := range bodies {
		if fn != nil && strings.Contains(strings.ToLower(fn.String()), "debug_backtrace") {
			return true
		}
	}
	return false
}

// hasAttribute reports whether an instruction carries a rendered attribute.
func hasAttribute(in Inst, want string) bool {
	for _, a := range in.Attrs {
		if a == want {
			return true
		}
	}
	return false
}
