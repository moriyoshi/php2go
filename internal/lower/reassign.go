package lower

import (
	"sort"
	"strings"
)

// Releasing the object a local held when the local is written again.
//
//	$obj = $reflector->newLazyGhost(...);   test('Ghost', $obj);
//	$obj = $reflector->newLazyProxy(...);   test('Proxy', $obj);
//
// PHP destroys the ghost at the second assignment -- the frame has not ended,
// nothing else holds it, and its handle is free for the proxy. Without this,
// every object made after the reassignment is numbered one too high, which is
// what the whole `+2` handle cluster was waiting on: 63 files, 42 of them in
// `lazy_objects`, where the lazy machinery itself was already right.
//
// **The file body is where this matters and where it is answerable.** `{main}`
// does not promote its slots, so `write_var($obj, v)` survives as an
// instruction; in a function the same statement may have lost its slot, and
// then there is no write to hang the release on. (`Function.FrameBound` records
// the values that were promoted locals, but not the *order* of the writes that
// were dropped, which is what a reassignment needs.)

// ReassignedLocals answers the locals whose previous object may be released
// when they are written again, together with the write instructions that do it.
//
// `reachedByGlobal` reports whether any body in the program binds this name as
// a global -- `global $obj;` or `$GLOBALS['obj']` -- which puts the object
// somewhere this walk cannot see. It is asked per name rather than assumed,
// because the file body's locals *are* the globals.
//
// `makesGenerator` reports a direct call that answers a new Generator -- a
// call to a generator function -- which is as fresh as a `new`: its body has
// not run, so nothing but the caller can hold it. Releasing such a local is
// what closes the generator where the engine does, at the `unset` or the
// overwrite, running its finally blocks there. Nil claims no call.
//
// `caughtFresh` says the whole program throws only objects nobody else holds
// (ThrowsOnlyFresh). Then a local bound by `catch (E $e)` owns what it was
// handed, and the next catch into the same name -- or any other write --
// destroys it, as the engine's CATCH does when it overwrites the variable.
func ReassignedLocals(fn *Function, reachedByGlobal func(name string) bool,
	keepsArg func(callees []string, param int) bool,
	leaksThis func(method string) bool, argKept func(method string) bool,
	makesGenerator func(callees []string) bool, caughtFresh bool) map[string]bool {
	if fn == nil || reachedByGlobal == nil {
		return nil
	}
	text := fn.String()
	for _, name := range escapeOpaque {
		if strings.Contains(text, `"`+name+`"`) {
			return nil
		}
	}
	if strings.Contains(text, "dynvar") {
		return nil
	}
	// Which locals are written more than once, and with what.
	writes := map[string][]string{}
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		for _, in := range b.Insts {
			local, value, ok := localWrite(in)
			if !ok {
				continue
			}
			writes[local] = append(writes[local], value)
		}
	}
	ea := &escapes{fn: fn, keepsArg: keepsArg, leaksThis: leaksThis, argKept: argKept}
	weakMaps := map[string]bool{}
	ea.weakMap = func(local string) bool {
		only, seen := weakMaps[local]
		if !seen {
			only = WeakMapOnlyLocal(fn, local, reachedByGlobal)
			weakMaps[local] = only
		}
		return only
	}
	weakRefs := map[string]bool{}
	ea.weakRef = func(local string) bool {
		only, seen := weakRefs[local]
		if !seen {
			only = WeakRefOnlyLocal(fn, local, reachedByGlobal)
			weakRefs[local] = only
		}
		return only
	}
	out := map[string]bool{}
	for local, values := range writes {
		// One write is enough. The threshold used to be two, on the reasoning
		// that a local written once is never overwritten and so has no release
		// point -- but `unset($a)` is a release point and is not a write, and a
		// release emitted at a local's *first* write frees the nothing it held
		// before, which the emitted nil check already handles.
		if len(values) < 1 || reachedByGlobal(strings.TrimPrefix(local, "$")) {
			continue
		}
		// Every value the local ever holds must be a fresh object this body
		// owns, and the local itself must not hand it anywhere. One value that
		// came from somewhere else -- a parameter, a property read, another
		// local -- means the object has another owner and releasing it here
		// would free something still live.
		sole, caught := true, false
		for _, v := range values {
			// A literal is not an object and brings no second owner with it:
			// `$x = null` and `$x = 1` are the ordinary ways a local drops what
			// it held, and requiring every *written* value to be a fresh object
			// refused exactly the releases this exists for.
			if nonObjectLiteral(v) {
				continue
			}
			// **Nor is a value that can hold no object at any depth**: an
			// element `list()` takes out of a literal array, or out of a local
			// only ever built from scalars (temporary_cleaning_011). It brings
			// no second owner either.
			if deepScalar(fn, v, reachedByGlobal, map[string]int{}) {
				continue
			}
			if caughtFresh && caughtException(fn, v) {
				caught = true
				continue
			}
			if !freshlyOwned(fn, v, leaksThis) && !freshGenerator(fn, v, makesGenerator) {
				sole = false
				break
			}
		}
		if sole && ea.local(local) && (!caught || exceptionLocalConfined(fn, local, leaksThis)) {
			out[local] = true
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// localWrite reports a write to a named local and its two halves.
//
// **Two shapes, because a file-body assignment is lowered as either.**
// `write_var` is the intrinsic form; `slot.set` is what a store becomes when
// the specializer proves the target needs no runtime write semantics. Handling
// only the first silently skipped whole files -- the analysis found no writes
// at all and claimed nothing, which looks exactly like "nothing to claim".
func localWrite(in Inst) (local, value string, ok bool) {
	switch {
	case in.Opcode == SlotSet && len(in.Operands) == 2:
		local = strings.TrimSpace(in.Operands[0])
		value = strings.TrimSpace(in.Operands[1])
	case in.Opcode == "call.intrinsic" && len(in.Operands) > 0 &&
		strings.Trim(in.Operands[0], `"`) == "write_var":
		args := escapeArgs(in.ArgList)
		if len(args) != 2 {
			return "", "", false
		}
		local = strings.TrimSpace(args[0])
		value = strings.TrimSpace(args[1])
	default:
		return "", "", false
	}
	if !strings.HasPrefix(local, "$") || strings.HasPrefix(local, "$__t") {
		return "", "", false
	}
	return local, value, true
}

// caughtException reports a value that is the place a catch parks the
// exception it received -- `$__excN`, written from the handler block's
// argument -- used only for the clause tests and the bindings. The engine has
// no such place: the exception goes straight from the unwinder into the
// variable, so the variable is its only holder.
func caughtException(fn *Function, value string) bool {
	if !strings.HasPrefix(value, "$__exc") || strings.HasPrefix(value, "$__excsave") {
		return false
	}
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
			case in.Opcode == "instanceof" && len(in.Operands) > 0 &&
				strings.TrimSpace(in.Operands[0]) == value:
			case in.Opcode == "call.intrinsic" && len(in.Operands) > 0 &&
				strings.Trim(in.Operands[0], `"`) == "write_var" && len(args) == 2 &&
				!escapeMentions(in.Operands[1:], value):
				// Parked from the handler's argument, or bound to a variable.
				if escapeToken(args[0], value) && strings.HasPrefix(strings.TrimSpace(args[1]), "%") {
					continue
				}
				if strings.TrimSpace(args[1]) != value || escapeToken(args[0], value) {
					return false
				}
			case (in.Opcode == "local.set" || in.Opcode == SlotSet) && len(in.Operands) == 2 &&
				strings.TrimSpace(in.Operands[1]) == value && !escapeToken(in.Operands[0], value):
			default:
				return false
			}
		}
	}
	return true
}

// exceptionLocalConfined is the stricter use rule for a local that holds a
// caught exception: it may be asked what it is and have methods called on it
// that do not keep `$this`, and nothing else. In particular it may not be an
// **argument** of anything, even of a call that keeps nothing: a call that
// throws puts its arguments in the new exception's trace
// (`getTrace()[0]['args']`), and that exception may be caught and kept.
func exceptionLocalConfined(fn *Function, local string, leaksThis func(string) bool) bool {
	reads := map[string]bool{}
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
			switch {
			case in.Opcode == "call.intrinsic" && len(in.Operands) > 0 && escapeIsRead(in.Operands[0]) &&
				len(args) == 1 && strings.TrimSpace(args[0]) == local:
				if in.Result != nil {
					reads[in.Result.String()] = true
				}
			case in.Opcode == "call.intrinsic" && len(in.Operands) > 0 &&
				strings.Trim(in.Operands[0], `"`) == "write_var" && len(args) == 2 &&
				strings.TrimSpace(args[0]) == local && !escapeToken(args[1], local):
			case (in.Opcode == "local.set" || in.Opcode == SlotSet) && len(in.Operands) == 2 &&
				strings.TrimSpace(in.Operands[0]) == local && !escapeToken(in.Operands[1], local):
			case in.Opcode == "unset" || in.Opcode == ReleasePrev:
				if strings.TrimSpace(in.Operands[0]) != local {
					return false
				}
			default:
				if !exceptionUseSafe(in, local, leaksThis) {
					return false
				}
			}
		}
	}
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		for r := range reads {
			if escapeToken(b.Term, r) {
				return false
			}
		}
		for _, in := range b.Insts {
			for r := range reads {
				if (escapeMentions(in.Operands, r) || escapeMentions(escapeArgs(in.ArgList), r)) &&
					!(in.Result != nil && in.Result.String() == r) && !exceptionUseSafe(in, r, leaksThis) {
					return false
				}
			}
		}
	}
	return true
}

// exceptionUseSafe is one use of an exception value that keeps it where it is:
// the receiver of a method that does not store `$this`, or the subject of a
// class test.
func exceptionUseSafe(in Inst, value string, leaksThis func(string) bool) bool {
	switch in.Opcode {
	case "call.method":
		if escapeMentions(escapeArgs(in.ArgList), value) || escapeMentions(in.Operands[1:], value) {
			return false
		}
		return methodSafeWith(in, value, leaksThis, nil)
	case "instanceof", "class_name":
		return len(in.Operands) > 0 && strings.TrimSpace(in.Operands[0]) == value &&
			!escapeMentions(escapeArgs(in.ArgList), value) &&
			(in.Result == nil || in.Result.String() != value)
	}
	return false
}

// nonObjectLiteral reports whether an operand is a literal that cannot be an
// object, so assigning it can only ever drop what the local held.
func nonObjectLiteral(value string) bool {
	v := strings.TrimSpace(value)
	switch strings.ToLower(v) {
	case "null", "true", "false":
		return true
	}
	if strings.HasPrefix(v, `"`) {
		return true
	}
	if v == "" {
		return false
	}
	// A bare numeric literal.
	for i, c := range v {
		if c >= '0' && c <= '9' || c == '.' || (i == 0 && (c == '-' || c == '+')) {
			continue
		}
		return false
	}
	return true
}

// deepScalar reports a value that is no object and holds none at any depth,
// so storing it gives no object a second owner.
//
//   - a non-object literal, or an immediate array literal (an opcode's
//     literal zval cannot be an object);
//   - a `copy` of such a value;
//   - an array built by `array.new` / `array.append` / `array.set` from such
//     values -- not the `.bound` forms, which store references, nor
//     `array.unpack`, whose source is not looked into;
//   - an element read (`dim.get` and its specialized spellings) of such a value: a missing key reads NULL,
//     a string offset reads a string, and an object base is excluded because
//     the base itself must be deepScalar -- an ArrayAccess `offsetGet` could
//     answer anything;
//   - a `read_var` of a local that no other body binds as a global and that
//     is mentioned only by such reads and by writes of deepScalar values. Any
//     other mention -- a dimension write, a reference, a by-reference send,
//     `unset` -- refuses, so nothing can put an object inside it unseen.
//
// `seen` memoises the locals asked about and breaks cycles through them: a
// local still being asked about is answered no, which only ever refuses.
func deepScalar(fn *Function, value string, reachedByGlobal func(string) bool, seen map[string]int) bool {
	v := strings.TrimSpace(value)
	if nonObjectLiteral(v) {
		return true
	}
	if strings.HasPrefix(v, "[") && strings.HasSuffix(v, "]") {
		return literalArray(v)
	}
	def := definition(fn, v)
	if def == nil || def.Opcode != "call.intrinsic" || len(def.Operands) == 0 {
		return false
	}
	args := splitTop(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(def.ArgList), "("), ")"), ',')
	switch strings.Trim(def.Operands[0], `"`) {
	case "array.new":
		return true
	case "copy":
		// A value copy holds what its operand holds.
		return len(args) == 1 && deepScalar(fn, args[0], reachedByGlobal, seen)
	case "array.append":
		return len(args) == 2 && deepScalar(fn, args[0], reachedByGlobal, seen) &&
			deepScalar(fn, args[1], reachedByGlobal, seen)
	case "array.set":
		return len(args) == 3 && deepScalar(fn, args[0], reachedByGlobal, seen) &&
			deepScalar(fn, args[1], reachedByGlobal, seen) && deepScalar(fn, args[2], reachedByGlobal, seen)
	case "dim.get", "dim.get.list", "dim.get.array", "dim.get.list.array", "dim.get.silent", "dim.get.array.silent":
		return len(args) == 2 && deepScalar(fn, args[0], reachedByGlobal, seen)
	case "read_var", "read_var.silent":
		if len(args) != 1 {
			return false
		}
		return scalarOnlyLocal(fn, strings.TrimSpace(args[0]), reachedByGlobal, seen)
	}
	return false
}

// literalArray reports an immediate array literal: brackets holding only
// literal keys and values, no value or place operand.
func literalArray(v string) bool {
	quoted, escaped := false, false
	for _, r := range v {
		switch {
		case escaped:
			escaped = false
		case r == '\\' && quoted:
			escaped = true
		case r == '"':
			quoted = !quoted
		case quoted:
		case r == '%' || r == '$':
			return false
		}
	}
	return !quoted
}

// scalarOnlyLocal reports a local every write of which is deepScalar and which
// nothing else touches (see deepScalar).
func scalarOnlyLocal(fn *Function, local string, reachedByGlobal func(string) bool, seen map[string]int) bool {
	const asking, yes, no = 1, 2, 3
	switch seen[local] {
	case asking, no:
		return false
	case yes:
		return true
	}
	seen[local] = asking
	ok := scalarOnlyWrites(fn, local, reachedByGlobal, seen)
	seen[local] = no
	if ok {
		seen[local] = yes
	}
	return ok
}

func scalarOnlyWrites(fn *Function, local string, reachedByGlobal func(string) bool, seen map[string]int) bool {
	if !strings.HasPrefix(local, "$") || strings.HasPrefix(local, "$__") || local == "$this" {
		return false
	}
	if reachedByGlobal(strings.TrimPrefix(local, "$")) {
		return false
	}
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		for _, in := range b.Insts {
			if in.Directive() || !mentions(in.String(), local) {
				continue
			}
			if in.Opcode == "call.intrinsic" && len(in.Operands) > 0 &&
				(strings.Trim(in.Operands[0], `"`) == "read_var" || strings.Trim(in.Operands[0], `"`) == "read_var.silent") &&
				strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(in.ArgList), "("), ")")) == local {
				continue
			}
			if l, value, ok := localWrite(in); ok && l == local && !mentions(value, local) &&
				deepScalar(fn, value, reachedByGlobal, seen) {
				continue
			}
			return false
		}
	}
	return true
}

// freshGenerator reports a value that is the result of a direct call to a
// generator function.
func freshGenerator(fn *Function, value string, makesGenerator func(callees []string) bool) bool {
	if makesGenerator == nil {
		return false
	}
	def := definition(fn, value)
	return def != nil && def.Opcode == "call.fn" && makesGenerator(CalleeNames(*def))
}

// freshlyOwned reports whether a value is an object this body made and nothing
// else holds: a `new`, a `clone`, or one of the object factories.
func freshlyOwned(fn *Function, value string, leaksThis func(method string) bool) bool {
	def := definition(fn, value)
	if def == nil {
		return false
	}
	return makesFreshObject(*def, leaksThis)
}

// SettleReleaseTracks marks each tracking marker (ReleaseTrackAttr) where
// releasing the local's previous object would be early: another value stored
// to the same local is still live after the store. Only confined objects are
// released at all, and a confined object is bound to one local, so a value
// stored to the local and dead after the store has nothing left to reach it.
func SettleReleaseTracks(fn *Function) {
	type site struct {
		pos   ownPos
		value string
	}
	byLocal := map[string][]site{}
	for bi, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		for i, in := range b.Insts {
			if in.Opcode == ReleasePrev && hasAttrIn(in.Attrs, ReleaseTrackAttr) && len(in.Operands) >= 2 {
				byLocal[in.Operands[0]] = append(byLocal[in.Operands[0]],
					site{ownPos{fn.Blocks[bi].ID, i}, strings.TrimSpace(in.Operands[1])})
			}
		}
	}
	if len(byLocal) == 0 {
		return
	}
	_, after := valueLiveness(fn)
	index := map[int]*Block{}
	for _, b := range fn.Blocks {
		index[b.ID] = b
	}
	for _, sites := range byLocal {
		for _, st := range sites {
			live := after[st.pos]
			for _, other := range sites {
				if other.value != st.value && live[other.value] {
					in := &index[st.pos.Block].Insts[st.pos.Index]
					in.Attrs = append(in.Attrs, ReleaseTrackKeepAttr)
					break
				}
			}
		}
	}
}

func hasAttrIn(attrs []string, want string) bool {
	for _, a := range attrs {
		if a == want {
			return true
		}
	}
	return false
}

// ThrowsOnlyFresh reports whether every exception the program can throw is an
// object nobody but the unwinder holds: each `throw` throws the result of a
// `new` used for nothing but its constructor call, whose constructor does not
// store `$this`. Exceptions the runtime raises itself -- a TypeError from a
// type check -- are fresh too.
//
// Refused outright: a program that names a `throw` method (Generator::throw
// and Fiber::throw raise an object the caller still holds, and a callable
// string can reach them as well as a method call can), and one that evals
// code this walk cannot see.
func ThrowsOnlyFresh(bodies []*Function, ctorLeaks func(class string) bool) bool {
	for _, fn := range bodies {
		if fn == nil {
			continue
		}
		text := strings.ToLower(fn.String())
		if strings.Contains(text, `"throw"`) || strings.Contains(text, `"eval"`) {
			return false
		}
		for _, b := range fn.Blocks {
			if b.Dead {
				continue
			}
			term := strings.TrimSpace(b.Term)
			if !strings.HasPrefix(term, "throw ") {
				continue
			}
			thrown := strings.TrimSpace(strings.TrimPrefix(term, "throw "))
			def := definition(fn, thrown)
			if def == nil || def.Opcode != "new" || len(def.Operands) == 0 {
				return false
			}
			class := def.Operands[0]
			if !strings.HasPrefix(class, `"`) || ctorLeaks(strings.ToLower(strings.Trim(class, `"`))) {
				return false
			}
			if !onlyConstructedAndThrown(fn, thrown) {
				return false
			}
		}
	}
	return true
}

// onlyConstructedAndThrown reports a `new` result whose only uses are its own
// constructor call, as the receiver, and `throw` terminators.
func onlyConstructedAndThrown(fn *Function, value string) bool {
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		if escapeToken(b.Term, value) && strings.TrimSpace(b.Term) != "throw "+value {
			return false
		}
		for _, in := range b.Insts {
			if in.Result != nil && in.Result.String() == value {
				continue
			}
			args := escapeArgs(in.ArgList)
			if !escapeMentions(in.Operands, value) && !escapeMentions(args, value) {
				continue
			}
			if in.Opcode == "call.ctor" && len(in.Operands) > 0 &&
				strings.TrimSpace(in.Operands[0]) == value &&
				!escapeMentions(in.Operands[1:], value) && !escapeMentions(args, value) {
				continue
			}
			return false
		}
	}
	return true
}

// DropSuspendCopied removes from a reassignment claim every local some value
// of which is also held by a CPS slot -- a `$__cps` place the rewrite stores a
// value into because it is live across a suspension (demoteAcross,
// demoteLiveAcross). That slot is a second holder the claim did not see: the
// local's release would destroy an object the slot still answers.
func DropSuspendCopied(fn *Function, claimed map[string]bool) map[string]bool {
	if len(claimed) == 0 {
		return claimed
	}
	copied := map[string]bool{}
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		for _, in := range b.Insts {
			if in.Opcode == SlotSet && len(in.Operands) == 2 &&
				strings.HasPrefix(strings.TrimSpace(in.Operands[0]), "$__cps") {
				copied[strings.TrimSpace(in.Operands[1])] = true
			}
		}
	}
	if len(copied) == 0 {
		return claimed
	}
	out := map[string]bool{}
	for local := range claimed {
		held := false
		for _, b := range fn.Blocks {
			if b.Dead {
				continue
			}
			for _, in := range b.Insts {
				if l, v, ok := localWrite(in); ok && l == local && copied[strings.TrimSpace(v)] {
					held = true
				}
			}
		}
		if !held {
			out[local] = true
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// CatchFrameReleases is the catch places of fn whose exception nothing but the
// place holds, so it dies with the frame: the engine frees a function's
// compiled variables when it returns, the catch variable among them, and the
// exception's number goes back there.
//
// **The place must be the only holder**, which takes two halves. The body
// half is here: the place is only read, class-tested, or the receiver of a
// method that keeps no `$this` -- never an argument (a trace keeps those, a
// `previous` would be one), never copied, never returned
// (exceptionLocalConfined). The program half is the caller's: the program
// throws only fresh objects (ThrowsOnlyFresh), so no `throw $e` hands the
// same exception to a second catch, and the body saves no unwinding
// exception for a finally (`exc.pending`), where a saved copy could be it.
func CatchFrameReleases(fn *Function, leaksThis func(string) bool) []string {
	text := fn.String()
	if strings.Contains(text, "exc.pending") {
		return nil
	}
	// **A frame read by name** hands its variables out with no use this walk
	// can see: `get_defined_vars()`, `compact`, `extract`, an eval or a
	// variable variable all reach the catch place (ConfinedObjectsFor).
	for _, name := range escapeOpaque {
		if strings.Contains(text, `"`+name+`"`) {
			return nil
		}
	}
	if strings.Contains(text, "dynvar") {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		for _, in := range b.Insts {
			local, _, ok := localWrite(in)
			if !ok && in.Opcode == "call.intrinsic" && len(in.Operands) > 0 &&
				strings.Trim(in.Operands[0], `"`) == "write_var" {
				if args := escapeArgs(in.ArgList); len(args) == 2 {
					local, ok = strings.TrimSpace(args[0]), true
				}
			}
			if !ok || seen[local] || !strings.HasPrefix(local, "$__exc") || strings.HasPrefix(local, "$__excsave") {
				continue
			}
			seen[local] = true
			// The lowering writes a catch place only from its handler's
			// argument, so every value it holds is a caught exception.
			if exceptionLocalConfined(fn, local, leaksThis) {
				out = append(out, local)
			}
		}
	}
	// **The catch variable by name.** A catch whose variable is used for more
	// than a method call binds it from the place (`write_var($e, $__exc1)`),
	// and then the variable is the holder the engine has: the place is only
	// class-tested and copied (caughtException). Claimed when every value the
	// variable ever holds is such a place's, or a literal that is no object,
	// and it keeps them to itself. Releasing it and the place it came from is
	// one release: the second finds the number already back.
	writes := map[string][]string{}
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		for _, in := range b.Insts {
			if local, value, ok := localWrite(in); ok {
				writes[local] = append(writes[local], value)
			}
		}
	}
	for local, values := range writes {
		if strings.HasPrefix(local, "$__") {
			continue
		}
		bound := false
		for _, v := range values {
			switch {
			case strings.HasPrefix(v, "$__exc") && caughtException(fn, v):
				bound = true
			case nonObjectLiteral(v):
			default:
				bound = false
				goto next
			}
		}
		if bound && exceptionLocalConfined(fn, local, leaksThis) {
			out = append(out, local)
		}
	next:
	}
	sort.Strings(out)
	return out
}
