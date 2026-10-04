package php

// A frame drain whose destructors may suspend.
//
// A resumable body that confines objects destroys them as it returns
// (generated Step, frameObjects). When one of those objects' `__destruct` may
// suspend the running fiber -- `Fiber::suspend()` inside a destructor -- the
// destructor cannot run inside the Go call that ends the body: a suspension
// there has nowhere to go. The CPS rewrite makes the drain a suspending call
// instead (lower: `call.frame_drain` before each return), and this is the
// frame that call enters: each object in creation order, a destructor that
// has a frame of its own entered as a `Called` -- so a suspension inside it
// parks the whole stack and a resume continues it -- and any other run
// synchronously as the ordinary drain runs it.

// DestructorFrameOf answers the frame a suspending `__destruct` runs in, or
// false when the object's destructor has none (it cannot suspend) or there is
// no destructor at all.
type DestructorFrameOf func(ctx *Ctx, o *Object) (Resumable, bool)

type frameDrain struct {
	objects []Value
	frameOf DestructorFrameOf
	release func(Value)
	at      int
	waiting bool
	thrown  error
}

// NewFrameDrain is the drain over objects, release giving each one's number
// back after its destructor (releaseObjectID).
func NewFrameDrain(objects []Value, frameOf DestructorFrameOf, release func(Value)) Resumable {
	return &frameDrain{objects: objects, frameOf: frameOf, release: release}
}

func (d *frameDrain) Resume(Value) {}

func (d *frameDrain) ResumeThrowing(err error) { d.thrown = err }

func (d *frameDrain) Step(ctx *Ctx) (Outcome, error) {
	if d.waiting {
		d.waiting = false
		delete(ctx.suspendedDrains, d)
		if err := d.thrown; err != nil {
			d.thrown = nil
			// The unwind of a fiber being destroyed, an exit or a gap goes
			// on out through the body. An exception the destructor threw
			// belongs to the body's caller, which this frame is not:
			// raised here it would meet the body's own `try`. Not modelled.
			if isGeneratorClose(err) {
				// **The fiber is being destroyed**, and the rest of the
				// frame dies with it, after the suspended destructor's own
				// `finally` and before the destruction returns -- measured:
				// `unset($f)` prints `finally c`, then `start d` ... `end d`,
				// then the next statement. Synchronously: a destructor run
				// here cannot suspend a fiber that is being closed.
				d.release(d.objects[d.at])
				for d.at++; d.at < len(d.objects); d.at++ {
					ctx.RunConfinedDestructor(d.objects[d.at])
					d.release(d.objects[d.at])
				}
				return Outcome{}, err
			}
			if _, thrown := AsThrown(err); !thrown {
				return Outcome{}, err
			}
			return Outcome{}, Unsupported("an exception from a suspending destructor as its frame ends")
		}
		d.release(d.objects[d.at])
		d.at++
	}
	for d.at < len(d.objects) {
		held := d.objects[d.at]
		if o := held.Deref().Object(); o != nil && ctx.destructorRun != nil && !ctx.Destructed(o) &&
			!o.NoDestructor && o.Lazy == nil {
			if frame, ok := d.frameOf(ctx, o); ok {
				ctx.MarkDestructed(o)
				d.waiting = true
				if ctx.suspendedDrains == nil {
					ctx.suspendedDrains = map[*frameDrain]struct{}{}
				}
				ctx.suspendedDrains[d] = struct{}{}
				return Outcome{Kind: Called, Frame: frame}, nil
			}
		}
		ctx.RunConfinedDestructor(held)
		d.release(held)
		d.at++
	}
	return Outcome{Kind: Returned, Value: Null()}, nil
}

// DrainsSuspended reports a destructor entered by a frame drain that has not
// come back -- its fiber is suspended inside it. As the request ends that
// fiber's other objects would be destroyed by the teardown walk before the
// fiber is, where the engine destroys the fiber first and they die inside it;
// the order is not modelled, so teardown refuses.
func (c *Ctx) DrainsSuspended() bool { return len(c.suspendedDrains) > 0 }
