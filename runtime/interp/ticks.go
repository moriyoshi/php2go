package interp

import (
	"github.com/moriyoshi/php2go/runtime/php"
)

// tickFunction is one registration, not one callable. PHP permits the same
// callback to be registered more than once with different stored arguments,
// and each entry has its own reentrancy guard.
type tickFunction struct {
	callback php.Value
	target   pendingCall
	args     []php.Value
	calling  bool
	prev     *tickFunction
	next     *tickFunction
}

func copyTickValues(values []php.Value) []php.Value {
	copied := make([]php.Value, len(values))
	for i, value := range values {
		copied[i] = value.Deref().Copy()
	}
	return copied
}

func (u *Unit) addTickFunction(callback php.Value, target *pendingCall, args []php.Value) {
	entry := &tickFunction{
		callback: callback.Deref().Copy(),
		target:   *target,
		args:     copyTickValues(args),
		prev:     u.tickTail,
	}
	if u.tickTail == nil {
		u.tickHead = entry
	} else {
		u.tickTail.next = entry
	}
	u.tickTail = entry
}

func tickFunctionEqual(left, right php.Value) bool {
	left, right = left.Deref(), right.Deref()
	if left.Kind() != right.Kind() {
		return false
	}
	switch left.Kind() {
	case php.KindString:
		// The registry compares the spelling, not function-name identity:
		// unregistering "T" does not remove a registration written as "t".
		return left.RawString() == right.RawString()
	case php.KindArray, php.KindObject:
		return php.LooseEqual(left, right)
	default:
		return false
	}
}

func (u *Unit) unlinkTickFunction(entry *tickFunction) {
	if entry.prev == nil {
		u.tickHead = entry.next
	} else {
		entry.prev.next = entry.next
	}
	if entry.next == nil {
		u.tickTail = entry.prev
	} else {
		entry.next.prev = entry.prev
	}
	entry.prev, entry.next = nil, nil
}

// removeTickFunction deletes the first matching entry that is not executing.
// The C list comparator throws for a matching executing entry but keeps walking,
// so a later duplicate is still removed before that Error reaches PHP.
func (u *Unit) removeTickFunction(callback php.Value) error {
	var calling error
	for entry := u.tickHead; entry != nil; entry = entry.next {
		if !tickFunctionEqual(entry.callback, callback) {
			continue
		}
		if entry.calling {
			if calling == nil {
				calling = Raise("Error",
					"Registered tick function cannot be unregistered while it is being executed")
			}
			continue
		}
		u.unlinkTickFunction(entry)
		return calling
	}
	return calling
}

func (in *Interp) resolveTickFunction(ctx *php.Ctx, unit *Unit,
	callback php.Value) (*pendingCall, error) {
	target, ok := unit.dynamicTarget(ctx, callback.Deref())
	in.canonicalCallName(target)
	if pending := ctx.TakePendingError(); pending != nil {
		return nil, pending
	}
	if !ok {
		// The harvested public signature has already validated the callable.
		// Reaching this path means it names an engine capability this runtime
		// cannot resolve, not that the program passed a different bad value.
		return nil, &UnsupportedError{Opcode: "registered tick callback resolution"}
	}
	unit.forwardThis(ctx, target)
	target.dynamic = true
	return target, nil
}

func (in *Interp) invokeTickFunction(ctx *php.Ctx, unit *Unit,
	registered *tickFunction, file string, line uint32, depth int) error {
	target := registered.target
	target.args = copyTickValues(registered.args)
	in.noteCallbackByRef(ctx, unit, &target)
	frame := php.StackFrame{
		Function: target.traceName(),
		File:     file,
		Line:     line,
		Args:     target.args,
		This:     target.this,
	}
	ctx.PushCall(frame)
	defer ctx.PopCall()
	_, err := in.dispatch(ctx, unit, &target, depth)
	return err
}

// runTick advances the request-wide counter even when no callback is installed.
// Different declare(ticks=N) regions share that counter in Zend.
func (in *Interp) runTick(ctx *php.Ctx, unit *Unit, interval uint32,
	file string, line uint32, depth int) error {
	unit.tickCount++
	if unit.tickCount < interval {
		return nil
	}
	unit.tickCount = 0
	if unit.tickHead == nil {
		return nil
	}

	// Zend blocks fiber switches for the whole user tick list, including nested
	// tick dispatch triggered by a callback body.
	unit.fiberSwitchBlocked++
	defer func() { unit.fiberSwitchBlocked-- }()

	// Follow live next pointers. A callback may remove a later entry or append a
	// new one, and zend_llist_apply observes both mutations in the same pass.
	for entry := unit.tickHead; entry != nil; entry = entry.next {
		if entry.calling {
			continue
		}
		entry.calling = true
		err := in.invokeTickFunction(ctx, unit, entry, file, line, depth)
		entry.calling = false
		if err != nil {
			return err
		}
	}
	return nil
}
