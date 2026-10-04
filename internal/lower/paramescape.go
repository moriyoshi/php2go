package lower

import (
	"strconv"
	"strings"
)

// Which of a body's parameters it may keep a reference to.
//
// This is the callee half of the handle-numbering question. `DisposableTemporaries`
// can release `var_dump(new P())` at the call because `var_dump` is on a
// hand-written allow-list of library functions known to keep nothing. A call to
// a **compiled** function had no such answer, so
//
//	function test($obj) { $obj->prop = "42"; var_dump($obj); }
//	test(new A);
//	test(new B);
//
// left the first object alive across the second `new`, and the engine's `#1,
// #1` came out `#1, #2`. The fact the allow-list states by hand is derivable
// for a body we compiled: it is escape analysis on the parameter locals.
//
// **Per parameter, not per body.** The coarse question -- "does this function
// keep any argument" -- is yes for `function f($o, $n) { return $n; }`, and
// answering it that way costs the object in the other position a release for a
// reason that has nothing to do with it. Returning a parameter is ordinary PHP,
// so the coarse form gave up on most real callees.
//
// Conservative in the same direction as the rest of this file. "Retained" is
// the safe answer and is what anything unrecognised gets: a body that suspends,
// one that can name a local this analysis cannot see, a by-reference parameter
// (which aliases the caller's storage outright), or any use the walk does not
// recognise. Being wrong that way costs a handle number; being wrong the other
// way releases a live object, which prints as some *other* object.

// RetainedParams reports which parameter positions a body may keep beyond the
// call. `all` true means every position is retained and the map is not
// meaningful -- the body was refused wholesale.
//
// `keepsArg` answers the same question for the callees *this* body calls, which
// is what makes the analysis compositional, and why its caller runs it to a
// fixpoint over the call graph rather than once.
func RetainedParams(fn *Function, keepsArg func(callees []string, param int) bool,
	leaksThis func(method string) bool, argKept func(method string) bool) (retained map[int]bool, all bool) {
	if fn == nil {
		return nil, true
	}
	text := fn.String()
	for _, name := range escapeOpaque {
		if strings.Contains(text, `"`+name+`"`) {
			return nil, true
		}
	}
	// A variable variable can name a parameter's own local.
	if strings.Contains(text, "dynvar") {
		return nil, true
	}
	// A generator's frame outlives the call that made it, so every local in it
	// -- parameters included -- is still reachable after the call returns.
	if strings.Contains(text, "yield") {
		return nil, true
	}
	// A by-reference parameter binds the caller's storage rather than copying
	// it, so "does the callee keep it" is not even the right question.
	for _, p := range fn.Params {
		if strings.Contains(p, "&") {
			return nil, true
		}
	}
	// A variadic collects the rest into an array, and an object put into an
	// array is one this walk stops following.
	if hasVariadicRecv(fn) {
		return nil, true
	}
	ea := &escapes{fn: fn, keepsArg: keepsArg, leaksThis: leaksThis, argKept: argKept}
	out := map[int]bool{}
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		for _, in := range b.Insts {
			idx, ok := receivesArgument(in)
			if !ok || in.Result == nil {
				continue
			}
			if !ea.value(in.Result.String()) {
				out[idx] = true
			}
		}
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, false
}

// receivesArgument reports whether an instruction binds an incoming argument,
// and which position it takes.
func receivesArgument(in Inst) (int, bool) {
	if in.Opcode != "recv" || len(in.Operands) == 0 {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(in.Operands[0]))
	if err != nil {
		return 0, false
	}
	return n, true
}

// hasVariadicRecv reports whether any parameter arrives through the variadic
// collector, whose array this analysis does not follow.
func hasVariadicRecv(fn *Function) bool {
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		for _, in := range b.Insts {
			if in.Opcode == "recv.variadic" {
				return true
			}
		}
	}
	return false
}
