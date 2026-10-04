package php

// **An argument the callee takes over.** A fresh object passed straight to a
// user function, which nothing but that call holds, dies among the callee's
// compiled variables when it returns -- in their order, parameters first,
// so before the callee's own locals. The call site knows the object is its
// alone; the callee knows its parameter keeps nothing. Neither can free it
// at the right moment by itself, so the call site hands the object over
// just before the call and the callee takes it as the first thing it does.
//
// Identity, not position, decides a take: the callee claims an object only
// where it is the very argument at a position it owns. Anything not taken
// is still the call site's after the call (DropHandOver), and it does what
// it did before -- so a callee that cannot own, or a call that never
// reaches the callee, changes nothing.

// HandOver records the objects the next call gives up to its callee. It
// replaces whatever an earlier call left, which DropHandOver has cleared.
func (c *Ctx) HandOver(vs ...Value) {
	c.handOver = c.handOver[:0]
	c.handOverAt = c.pushes
	for _, v := range vs {
		if o := v.Object(); o != nil {
			c.handOver = append(c.handOver, o)
		}
	}
}

// TakeHandOver answers the handed-over objects that are the arguments at
// the owned positions, in position order, and removes them from the hand
// over; the rest stay for the call site.
//
// **Only the call it was made for.** Between a hand over and its callee's
// prologue at most one frame is pushed -- the callee's own. More means some
// other code ran first, or the hand over is a stale one a call that failed
// never took back; either way nothing is taken, and the call site gets its
// object back (DropHandOver). A stale object can only come back as an
// argument out of a trace, and reaching it there pushes the failed
// callee's frame and the next caller's both.
func (c *Ctx) TakeHandOver(args []Value, owned []int) []Value {
	if len(c.handOver) == 0 || c.pushes > c.handOverAt+1 {
		return nil
	}
	var took []Value
	for _, i := range owned {
		if i >= len(args) {
			continue
		}
		// An undecided send (`preferref`) may arrive boxed.
		v := args[i].Deref()
		o := v.Object()
		if o == nil {
			continue
		}
		for j, h := range c.handOver {
			if h == o {
				took = append(took, v)
				c.handOver = append(c.handOver[:j], c.handOver[j+1:]...)
				break
			}
		}
	}
	return took
}

// DropHandOver takes v back from the hand over and reports whether it was
// still there -- not taken by the callee. An entry a failed call left
// behind is cleared by the next HandOver.
func (c *Ctx) DropHandOver(v Value) bool {
	o := v.Object()
	for j, h := range c.handOver {
		if h == o {
			c.handOver = append(c.handOver[:j], c.handOver[j+1:]...)
			return true
		}
	}
	return false
}

// **Arguments a trace holds.** A taker that returns by throwing leaves what
// it took alone: the exception carries the call's arguments in its trace,
// so they die when the exception does. HoldInTrace records them against
// that exception, innermost frame first as they unwind -- the order the
// trace array lists its frames in, and so the order freeing it frees them
// -- and TakeTraceHeld hands them over to whatever frees the exception.
// One that is never freed keeps them, which is the safe side.

// HoldInTrace records vals as held by the trace of the exception err
// carries, and reports whether err carries one.
func (c *Ctx) HoldInTrace(err error, vals []Value) bool {
	o, thrown := AsThrown(err)
	if !thrown || o == nil || len(vals) == 0 {
		return false
	}
	if c.traceHeld == nil {
		c.traceHeld = map[*Object][]Value{}
	}
	c.traceHeld[o] = append(c.traceHeld[o], vals...)
	return true
}

// TakeTraceHeld answers, and forgets, what the trace of o was recorded as
// holding (HoldInTrace).
func (c *Ctx) TakeTraceHeld(o *Object) []Value {
	vals := c.traceHeld[o]
	delete(c.traceHeld, o)
	return vals
}

// TraceHolds reports whether the exception err carries -- or one it chains
// as previous -- recorded o among the arguments of the frames its trace
// took (Object.Trace, the values the engine's trace array holds).
func TraceHolds(err error, o *Object) bool {
	if o == nil {
		return false
	}
	ex, thrown := AsThrown(err)
	if !thrown {
		return false
	}
	for depth := 0; ex != nil && depth < 64; depth++ {
		for _, f := range ex.Trace {
			for _, a := range f.Args {
				if a.Deref().Object() == o {
					return true
				}
			}
		}
		ex = ThrowablePrevious(ex).Object()
	}
	return false
}
