package php_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/moriyoshi/php2go/runtime/php"
)

// These tests are the first execution of anything on the resumable path. The
// CPS rewrite in `internal/lower` has been verified structurally and has never
// run, because nothing emits it yet; the state machines below are transcribed by
// hand from `php2lower -cps -print` output so that the protocol in frame.go is
// exercised against the IR as it actually is rather than as it is imagined.
//
// Where a transcription reproduces a defect in the IR it says so, and the test
// asserts the defective behaviour on purpose -- so that fixing the compiler side
// makes the test fail and the reproduction gets retired rather than quietly
// diverging.
//
// **These transcriptions are no longer current IR.** `internal/lower` now gives
// every body a terminal state -- `.resumable` lists one state more than there
// are suspensions, each `ret` parks its value in `$__ret` and records that
// state, and `Function.Terminal` is its index. The state machines below predate
// that and have no terminal state, which is why re-entering one after it
// returns gives a different answer. They are kept as they are on purpose: they
// exercise the *drivers*, and a driver must work against a body whose author
// was careless. Anything testing what the compiler emits belongs in a test that
// runs generated code, not here.

// testIter stands in for the `*iter` a `.frame` slot declares. `runtime/php` has
// no iterator type yet -- the emitter will need one, and the point of it is that
// it is *not* a Value: boxing an iterator into a zval is exactly what the slot's
// declared type forbids.
type testIter struct {
	arr  *php.Array
	keys []php.Key
	at   int
}

func newTestIter(a *php.Array) *testIter { return &testIter{arr: a, keys: a.Keys()} }

func (it *testIter) Valid() bool { return it.at < len(it.keys) }
func (it *testIter) Next()       { it.at++ }

func (it *testIter) Current() php.Value {
	v, _ := it.arr.Get(it.keys[it.at])
	return v
}

func list(values ...php.Value) *php.Array {
	a := php.NewArray()
	for _, v := range values {
		a.Append(v)
	}
	return a
}

func ints(values ...int64) *php.Array {
	out := make([]php.Value, len(values))
	for i, v := range values {
		out[i] = php.Int(v)
	}
	return list(out...)
}

// ---------------------------------------------------------------------------
// counter: transcribed instruction for instruction from the emitted IR.
// ---------------------------------------------------------------------------

// counterFrame is `internal/lower/testdata/resumable.php`'s `counter`:
//
//	function counter(array $rows) {
//	    $seen = 0;
//	    foreach ($rows as $r) {
//	        $reply = yield $r;
//	        $seen = $seen + 1;
//	        if ($reply) { yield $seen; }
//	    }
//	    return $seen;
//	}
//
// with the frame the compiler declares:
//
//	.frame [ $rows: zval, $seen: zval, $__iter5: *iter, $r: zval, $__t6: zval, $reply: zval ]
//	.resumable [ bb0, bb1, bb4, bb6 ]
//	.entry bb10
//
// The struct is what a per-function generated frame looks like: one field per
// slot, at the slot's declared type, embedding the state machinery.
type counterFrame struct {
	php.FrameState
	args []php.Value

	rows  php.Value
	seen  php.Value
	iter5 *testIter
	r     php.Value
	t6    php.Value
	reply php.Value
}

// counterStates is `.resumable [ bb0, bb1, bb4, bb6 ]` -- the dispatch table,
// indexed by state number.
var counterStates = [...]int{0, 1, 4, 6}

func (f *counterFrame) Step(_ *php.Ctx) (php.Outcome, error) {
	block := counterStates[f.State()] // bb10: frame.state; dispatch
	for {
		switch block {
		case 0:
			f.rows = f.args[0] // recv 0; local.set $rows
			f.SetState(1)
			return php.Outcome{Kind: php.Created}, nil // ret.generator

		case 1:
			if _, err := f.Resumed(); err != nil { // frame.resumed
				return php.Outcome{}, err
			}
			f.seen = php.Int(0)
			f.iter5 = newTestIter(f.rows.Array()) // iter.reset
			if f.iter5.Valid() {                  // br iter.valid $__iter5, bb2, bb7
				block = 2
			} else {
				block = 7
			}

		case 2:
			// The loop header, which tests the iterator and touches nothing
			// else -- FE_FETCH asks before it fetches.
			if f.iter5.Valid() { // br iter.valid $__iter5, bb9, bb7
				block = 9
			} else {
				block = 7
			}

		case 3:
			f.SetState(2)
			return php.Outcome{Kind: php.Yielded, Value: f.r}, nil // ret.yield $r

		case 4:
			v, err := f.Resumed() // frame.resumed
			if err != nil {
				return php.Outcome{}, err
			}
			f.t6, f.reply = v, v
			f.seen = php.Int(f.seen.Int() + 1) // add $seen, 1
			if f.reply.Bool() {                // br $reply, bb5, bb6
				block = 5
			} else {
				block = 6
			}

		case 5:
			f.SetState(3)
			return php.Outcome{Kind: php.Yielded, Value: f.seen}, nil // ret.yield $seen

		case 6:
			// A continuation that discards its resumed value still reads it:
			// the read is where a `throw()` at this suspension surfaces. bb6 is
			// also the join of the `if`, so on that path this re-reads a value
			// bb4 already took. Safe because `Resumed` reports the pending throw
			// *once* -- the effect cannot fire twice -- and separately because no
			// continuation that *uses* its value has a non-suspension
			// predecessor, which `php2lower -cps` now asserts over both corpora.
			if _, err := f.Resumed(); err != nil { // frame.resumed
				return php.Outcome{}, err
			}
			block = 2 // jmp bb2

		case 7:
			// iter.free $__iter5 -- deliberately not clearing the slot; see
			// TestFrameSlotsOutliveTheBody.
			return php.Outcome{Kind: php.Returned, Value: f.seen}, nil // ret $seen

		case 9:
			f.r = f.iter5.Current() // iter.current; local.set $r
			f.iter5.Next()          // iter.next
			block = 3               // jmp bb3

		default:
			return php.Outcome{}, fmt.Errorf("counter: no block bb%d", block)
		}
	}
}

// drain advances a generator to exhaustion, collecting `key=>value` pairs.
func drain(t *testing.T, ctx *php.Ctx, g *php.Generator) []string {
	t.Helper()
	var out []string
	for i := 0; i < 100; i++ {
		ok, err := g.Valid(ctx)
		if err != nil {
			t.Fatalf("valid: %v", err)
		}
		if !ok {
			return out
		}
		k, err := g.Key(ctx)
		if err != nil {
			t.Fatalf("key: %v", err)
		}
		v, err := g.Current(ctx)
		if err != nil {
			t.Fatalf("current: %v", err)
		}
		out = append(out, fmt.Sprintf("%s=>%s", k.String(), v.String()))
		if err := g.Next(ctx); err != nil {
			t.Fatalf("next: %v", err)
		}
	}
	t.Fatal("generator did not finish")
	return nil
}

func TestGeneratorProtocolOverTranscribedIR(t *testing.T) {
	ctx := php.NewCtx(&strings.Builder{})
	fr := &counterFrame{args: []php.Value{php.Arr(ints(10, 20, 30))}}
	g, err := php.StartGenerator(ctx, fr)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	// The prologue ran at the call, and nothing past it has. `$seen` is still
	// the zero Value rather than the 0 that bb1 assigns.
	if !fr.seen.IsUndef() {
		t.Errorf("the body ran before the generator was advanced: $seen = %v", fr.seen)
	}
	if fr.rows.Kind() != php.KindArray {
		t.Errorf("the prologue did not bind $rows: %v", fr.rows.Kind())
	}

	got := drain(t, ctx, g)
	want := []string{"0=>10", "1=>20", "2=>30"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("yields: got %v, want %v", got, want)
	}

	ret, err := g.GetReturn(ctx)
	if err != nil {
		t.Fatalf("getReturn: %v", err)
	}
	if ret.Int() != 3 {
		t.Errorf("return: got %v, want 3", ret.String())
	}
}

// TestThrowAtADiscardingContinuation drives the block the second defect lived
// in. `yield $seen;` discards its result, and its continuation is also the join
// of the enclosing `if`; the read is emitted there anyway, which is the only
// reason a `throw()` at that suspension has anywhere to land.
func TestThrowAtADiscardingContinuation(t *testing.T) {
	ctx := php.NewCtx(&strings.Builder{})
	fr := &counterFrame{args: []php.Value{php.Arr(ints(10, 20))}}
	g, err := php.StartGenerator(ctx, fr)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	// A truthy reply takes the `if` arm, so the next suspension is `yield $seen`
	// -- state 3, whose continuation is the discarding one.
	if _, err := g.Send(ctx, php.Bool(true)); err != nil {
		t.Fatalf("send: %v", err)
	}
	if v, _ := g.Current(ctx); v.Int() != 1 {
		t.Fatalf("expected the second yield to be $seen = 1, got %v", v.String())
	}
	boom := errors.New("boom")
	if _, err := g.Throw(ctx, boom); !errors.Is(err, boom) {
		t.Errorf("throw at a discarding continuation: got %v", err)
	}
	if !g.Finished() {
		t.Error("a body that threw is finished")
	}
	if _, err := g.GetReturn(ctx); !errors.Is(err, php.ErrGeneratorNotReturned) {
		t.Errorf("it never returned, so getReturn stays refused: %v", err)
	}
}

// TestFrameSlotsOutliveTheBody is the constraint the handoff calls
// non-negotiable: a declared slot must live as long as the frame, because PHP
// keeps a generator's locals alive until the generator dies and orders their
// destructors by that.
//
// A generated struct makes it structural -- there is no operation that clears a
// field -- which is the reason for choosing one over a slice.
func TestFrameSlotsOutliveTheBody(t *testing.T) {
	ctx := php.NewCtx(&strings.Builder{})
	fr := &counterFrame{args: []php.Value{php.Arr(ints(1, 2))}}
	g, err := php.StartGenerator(ctx, fr)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	drain(t, ctx, g)
	if !g.Finished() {
		t.Fatal("the generator did not finish")
	}
	// Every slot the frame declares still holds what it last held, including the
	// iterator that `iter.free` ran on and the loop variable past its last read.
	if fr.iter5 == nil {
		t.Error("$__iter5 was cleared; iter.free must not drop the slot")
	}
	if fr.r.IsUndef() {
		t.Error("$r was cleared after its last read")
	}
	if fr.rows.Kind() != php.KindArray {
		t.Error("$rows was cleared")
	}
}

// ---------------------------------------------------------------------------
// The rest of the protocol, on machines written to be correct.
// ---------------------------------------------------------------------------

// keysFrame is `function k() { yield 5 => "a"; yield "b"; $x = yield; return $x; }`
// -- the automatic key rule, which belongs to the instance and so is the
// driver's, not the IR's.
type keysFrame struct{ php.FrameState }

func (f *keysFrame) Step(_ *php.Ctx) (php.Outcome, error) {
	switch f.State() {
	case 0:
		f.SetState(1)
		return php.Outcome{Kind: php.Created}, nil
	case 1:
		f.SetState(2)
		return php.Outcome{Kind: php.Yielded, Key: php.Int(5), Value: php.String("a")}, nil
	case 2:
		f.SetState(3)
		return php.Outcome{Kind: php.Yielded, Value: php.String("b")}, nil
	case 3:
		f.SetState(4)
		return php.Outcome{Kind: php.Yielded, Value: php.Null()}, nil
	case 4:
		v, err := f.Resumed()
		if err != nil {
			return php.Outcome{}, err
		}
		return php.Outcome{Kind: php.Returned, Value: v}, nil
	}
	return php.Outcome{}, fmt.Errorf("keys: no state %d", f.State())
}

func TestGeneratorAutoKeysAndSend(t *testing.T) {
	ctx := php.NewCtx(&strings.Builder{})
	g, err := php.StartGenerator(ctx, &keysFrame{})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	k, _ := g.Key(ctx)
	if k.Int() != 5 {
		t.Errorf("explicit key: got %v, want 5", k.String())
	}
	if err := g.Next(ctx); err != nil {
		t.Fatal(err)
	}
	// An explicit integer key moves the counter past itself, exactly as an
	// array's append key does.
	k, _ = g.Key(ctx)
	if k.Int() != 6 {
		t.Errorf("auto key after an explicit 5: got %v, want 6", k.String())
	}
	if err := g.Next(ctx); err != nil {
		t.Fatal(err)
	}
	if k, _ = g.Key(ctx); k.Int() != 7 {
		t.Errorf("next auto key: got %v, want 7", k.String())
	}

	// send() resumes with the value, and answers with what comes next -- here
	// nothing, since the body returns.
	next, err := g.Send(ctx, php.String("sent"))
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if next.Kind() != php.KindNull {
		t.Errorf("send past the last yield: got %v, want NULL", next.String())
	}
	ret, err := g.GetReturn(ctx)
	if err != nil {
		t.Fatalf("getReturn: %v", err)
	}
	if ret.String() != "sent" {
		t.Errorf("the sent value did not reach the yield's result: %q", ret.String())
	}
}

// A generator whose body threw is finished, but it never returned, so
// getReturn() stays refused rather than answering NULL.
func TestGeneratorThatThrewHasNoReturnValue(t *testing.T) {
	ctx := php.NewCtx(&strings.Builder{})
	g, err := php.StartGenerator(ctx, &delegatingFrame{source: php.String("nope")})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := g.Current(ctx); err == nil {
		t.Fatal("expected the unsupported delegate to be reported")
	}
	if !g.Finished() {
		t.Error("a body that threw is finished")
	}
	if _, err := g.GetReturn(ctx); !errors.Is(err, php.ErrGeneratorNotReturned) {
		t.Errorf("getReturn after a throw: got %v", err)
	}
}

func TestGeneratorGetReturnBeforeFinishing(t *testing.T) {
	ctx := php.NewCtx(&strings.Builder{})
	g, err := php.StartGenerator(ctx, &keysFrame{})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := g.GetReturn(ctx); !errors.Is(err, php.ErrGeneratorNotReturned) {
		t.Errorf("getReturn before the end: got %v", err)
	}
	if _, err := g.Current(ctx); err != nil {
		t.Fatal(err)
	}
	if err := g.Rewind(ctx); err != nil {
		t.Errorf("rewind at the first yield must be allowed: %v", err)
	}
	if err := g.Next(ctx); err != nil {
		t.Fatal(err)
	}
	if err := g.Rewind(ctx); !errors.Is(err, php.ErrGeneratorAlreadyRun) {
		t.Errorf("rewind past the first yield: got %v", err)
	}
}

// delegatingFrame is `function d() { $r = yield from SOURCE; yield $r; }`.
type delegatingFrame struct {
	php.FrameState
	source php.Value
}

func (f *delegatingFrame) Step(_ *php.Ctx) (php.Outcome, error) {
	switch f.State() {
	case 0:
		f.SetState(1)
		return php.Outcome{Kind: php.Created}, nil
	case 1:
		f.SetState(2)
		return php.Outcome{Kind: php.Delegated, Value: f.source}, nil
	case 2:
		v, err := f.Resumed()
		if err != nil {
			return php.Outcome{}, err
		}
		return php.Outcome{Kind: php.Returned, Value: v}, nil
	}
	return php.Outcome{}, fmt.Errorf("delegating: no state %d", f.State())
}

func TestYieldFromAnArray(t *testing.T) {
	ctx := php.NewCtx(&strings.Builder{})
	g, err := php.StartGenerator(ctx, &delegatingFrame{source: php.Arr(ints(7, 8))})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	// `yield from` preserves the source's keys rather than renumbering, which is
	// why a generator can produce the same key twice.
	if got, want := fmt.Sprint(drain(t, ctx, g)), "[0=>7 1=>8]"; got != want {
		t.Errorf("delegated yields: got %s, want %s", got, want)
	}
	ret, err := g.GetReturn(ctx)
	if err != nil {
		t.Fatalf("getReturn: %v", err)
	}
	if ret.Kind() != php.KindNull {
		t.Errorf("yield from an array evaluates to NULL, got %v", ret.String())
	}
}

func TestYieldFromAnEmptyArrayProducesNothing(t *testing.T) {
	ctx := php.NewCtx(&strings.Builder{})
	g, err := php.StartGenerator(ctx, &delegatingFrame{source: php.Arr(ints())})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	// A spent source yields nothing at all, so one advance takes two entries
	// into the body. Getting this wrong shows up as a phantom NULL element.
	if got := drain(t, ctx, g); len(got) != 0 {
		t.Errorf("an empty delegate produced %v", got)
	}
}

func TestYieldFromAGenerator(t *testing.T) {
	ctx := php.NewCtx(&strings.Builder{})
	inner, err := php.StartGenerator(ctx, &keysFrame{})
	if err != nil {
		t.Fatalf("inner: %v", err)
	}
	obj := php.NewObject("Generator", 1)
	obj.Internal = inner

	outer, err := php.StartGenerator(ctx, &delegatingFrame{source: php.Obj(obj)})
	if err != nil {
		t.Fatalf("outer: %v", err)
	}
	if got, want := fmt.Sprint(drain(t, ctx, outer)), `[5=>a 6=>b 7=>]`; got != want {
		t.Errorf("delegated yields: got %s, want %s", got, want)
	}
	// The inner generator's *return* value is what `yield from` evaluates to.
	ret, err := outer.GetReturn(ctx)
	if err != nil {
		t.Fatalf("getReturn: %v", err)
	}
	if ret.Kind() != php.KindNull {
		t.Errorf("inner returned what next() sent, which is NULL; got %v", ret.String())
	}
}

// A `yield from` over a string reaches the driver as a *PHP error*, not as a
// gap: the engine throws `Error: Can use "yield from" only with arrays and
// Traversables`. This test used to assert ErrDelegateUnsupported, which filed a
// program's mistake as our missing capability -- and the correction is what made
// it fail, which is the notification working.
func TestYieldFromANonTraversableSurfacesAsAPhpError(t *testing.T) {
	ctx := php.NewCtx(&strings.Builder{})
	g, err := php.StartGenerator(ctx, &delegatingFrame{source: php.String("nope")})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	// Thrown into the body at the `yield from`, where the body's own try
	// could catch it; uncaught, it comes out as the Error itself.
	_, err = g.Current(ctx)
	var raised *php.Error
	if !errors.As(err, &raised) || raised.Class != "Error" ||
		raised.Message != `Can use "yield from" only with arrays and Traversables` {
		t.Errorf("yield from a string gave %v, want the Error the engine throws", err)
	}
	if errors.Is(err, php.ErrDelegateUnsupported) {
		t.Error("reported as our gap; it is the program being wrong")
	}
}

// ---------------------------------------------------------------------------
// throw(): the gap the IR has.
// ---------------------------------------------------------------------------

// catchingFrame is `function c() { try { yield 1; } catch (E $e) { yield "caught"; } }`
// written the way the emitter *would* have to write it: the continuation begins
// with `frame.resumed`, and a pending throw surfaces there, inside the region.
type catchingFrame struct{ php.FrameState }

func (f *catchingFrame) Step(_ *php.Ctx) (php.Outcome, error) {
	switch f.State() {
	case 0:
		f.SetState(1)
		return php.Outcome{Kind: php.Created}, nil
	case 1:
		f.SetState(2)
		return php.Outcome{Kind: php.Yielded, Value: php.Int(1)}, nil
	case 2:
		if _, err := f.Resumed(); err != nil {
			// The catch arm of the region the yield sits in.
			f.SetState(3)
			return php.Outcome{Kind: php.Yielded, Value: php.String("caught: " + err.Error())}, nil
		}
		return php.Outcome{Kind: php.Returned, Value: php.String("no throw")}, nil
	case 3:
		return php.Outcome{Kind: php.Returned, Value: php.String("after catch")}, nil
	}
	return php.Outcome{}, fmt.Errorf("catching: no state %d", f.State())
}

func TestThrowSurfacesAtTheSuspensionPoint(t *testing.T) {
	ctx := php.NewCtx(&strings.Builder{})
	g, err := php.StartGenerator(ctx, &catchingFrame{})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if v, _ := g.Current(ctx); v.Int() != 1 {
		t.Fatalf("first yield: %v", v.String())
	}
	// The exception has to be raised *inside* the body, or the generator's own
	// try around the yield could not catch it. That is what makes
	// `frame.resumed` fallible rather than the driver simply returning the
	// error.
	next, err := g.Throw(ctx, errors.New("boom"))
	if err != nil {
		t.Fatalf("throw: %v", err)
	}
	if got, want := next.String(), "caught: boom"; got != want {
		t.Errorf("throw: got %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// The representation measurement.
// ---------------------------------------------------------------------------

// sliceFrame is the alternative the handoff left open: slots in a `[]Value`
// indexed by slot number. It can only be benchmarked on a frame whose slots are
// all `zval` -- `counter`'s `$__iter5: *iter` has no representation here at all
// without a new Value kind to box it in, which is the argument against.
type sliceFrame struct {
	php.FrameState
	slots []php.Value
}

const (
	slotN = iota
	slotAcc
)

func (f *sliceFrame) Step(_ *php.Ctx) (php.Outcome, error) {
	for {
		if f.State() == 1 {
			if _, err := f.Resumed(); err != nil {
				return php.Outcome{}, err
			}
		}
		if f.slots[slotN].Int() <= 0 {
			return php.Outcome{Kind: php.Returned, Value: f.slots[slotAcc]}, nil
		}
		f.slots[slotN] = php.Int(f.slots[slotN].Int() - 1)
		f.slots[slotAcc] = php.Int(f.slots[slotAcc].Int() + f.slots[slotN].Int())
		f.SetState(1)
		return php.Outcome{Kind: php.Yielded, Value: f.slots[slotAcc]}, nil
	}
}

// structFrame is the same machine with the slots as fields.
type structFrame struct {
	php.FrameState
	n   php.Value
	acc php.Value
}

func (f *structFrame) Step(_ *php.Ctx) (php.Outcome, error) {
	for {
		if f.State() == 1 {
			if _, err := f.Resumed(); err != nil {
				return php.Outcome{}, err
			}
		}
		if f.n.Int() <= 0 {
			return php.Outcome{Kind: php.Returned, Value: f.acc}, nil
		}
		f.n = php.Int(f.n.Int() - 1)
		f.acc = php.Int(f.acc.Int() + f.n.Int())
		f.SetState(1)
		return php.Outcome{Kind: php.Yielded, Value: f.acc}, nil
	}
}

func runBody(b *testing.B, make func() php.Resumable) {
	b.Helper()
	ctx := php.NewCtx(&strings.Builder{})
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		body := make()
		for {
			out, err := body.Step(ctx)
			if err != nil {
				b.Fatal(err)
			}
			if out.Kind == php.Returned {
				break
			}
			body.Resume(php.Null())
		}
	}
}

func BenchmarkFrameStruct(b *testing.B) {
	runBody(b, func() php.Resumable {
		return &structFrame{n: php.Int(16), acc: php.Int(0)}
	})
}

func BenchmarkFrameSlice(b *testing.B) {
	runBody(b, func() php.Resumable {
		return &sliceFrame{slots: []php.Value{php.Int(16), php.Int(0)}}
	})
}

// ---------------------------------------------------------------------------
// What one control transfer costs, by mechanism.
// ---------------------------------------------------------------------------

// Suspending a computation that has native frames needs either a parked stack,
// which in Go only a goroutine can own, or no native frames, which is CPS.
// There is no third mechanism without cgo. `runtime/interp`'s fibers take the
// first: `toFiber` and `fromFip` are unbuffered, and `switchTo` blocks on the
// reply the instant it hands over, so it is a strict rendezvous with exactly one
// side runnable -- a concurrency primitive standing in for a control-flow one.
//
// These two measure the bare mechanism and nothing else. Neither is a PHP
// benchmark: there is no interpreter on the first side and no generator
// semantics on the second, so the ratio is the cost of the *transfer*, which is
// the number to quote if a fiber-heavy workload ever turns up. Reading it as
// "fibers are N times slower than generators in php2go" would be wrong -- the
// interpreter's own per-opline cost dwarfs both. The rendezvous figure is also a
// floor: the real `switchTo` swaps the interpreter's frames, call stack and
// scope across the handover, which this does not.
//
// Regenerate with:
//
//	go test ./runtime/php/ -run XXX -bench BenchmarkTransfer -benchmem -count 5
//
// **The ratio is the finding, not the absolutes.** Two independent runs gave
// 331-351 ns and 337.8-354.8 ns for the rendezvous against 2.14-2.16 ns for the
// state machine -- about 160x either way. Both were taken on a shared box at a
// load average near 30 on 20 cores, which is enough noise that three
// significant figures of nanoseconds mean nothing and two orders of magnitude
// mean everything. Re-measure on a quiet machine before deciding anything with
// the absolute numbers.

// transferRendezvous is the shape of switchTo: hand over, then block for the
// reply. One iteration is one round trip, which is two scheduler handoffs.
func BenchmarkTransferRendezvous(b *testing.B) {
	in, out := make(chan php.Value), make(chan php.Value)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range in {
			out <- php.Null()
		}
	}()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		in <- php.Null()
		<-out
	}
	b.StopTimer()
	close(in)
	<-done
}

// alwaysYields is the smallest body that comes back every entry, so one
// iteration below is one transfer out and back with no work in it.
type alwaysYields struct{ php.FrameState }

func (f *alwaysYields) Step(_ *php.Ctx) (php.Outcome, error) {
	if _, err := f.Resumed(); err != nil {
		return php.Outcome{}, err
	}
	f.SetState(1)
	return php.Outcome{Kind: php.Yielded, Value: php.Null()}, nil
}

func BenchmarkTransferStateMachine(b *testing.B) {
	ctx := php.NewCtx(&strings.Builder{})
	// Through the interface, which is how a driver holds it. A concrete
	// receiver here would let the compiler inline the whole step and flatter
	// the mechanism by a factor the real thing never sees.
	var body php.Resumable = &alwaysYields{}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		body.Resume(php.Null())
		if _, err := body.Step(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

// ---------------------------------------------------------------------------
// Fibers: transcribed from `php2lower -cps -print internal/lower/testdata/fiber.php`
// ---------------------------------------------------------------------------

// insideFrame is `App\inside`:
//
//	function inside($v): string {
//	    $got = Fiber::suspend($v);
//	    $g2 = \Fiber::suspend($v);
//	    return $got . $g2;
//	}
//
// with what the compiler now declares for it:
//
//	.frame [ $v: zval, $__t3: zval, $got: zval, $__t5: zval, $g2: zval ]
//	.suspends_to_caller
//	.resumable [ bb0, bb1, bb2 ]
//	.entry bb4
//
// The shape a fiber body has and a generator body does not: **no
// `ret.generator`**. bb0 is state 0 and holds the `recv`, so the arguments bind
// on the first Step -- which is `start()`, exactly where PHP binds them.
type insideFrame struct {
	php.FrameState
	args []php.Value

	v   php.Value
	t3  php.Value
	got php.Value
	t5  php.Value
	g2  php.Value
}

// insideStates is `.resumable [ bb0, bb1, bb2 ]`.
var insideStates = [...]int{0, 1, 2}

func (f *insideFrame) Step(_ *php.Ctx) (php.Outcome, error) {
	block := insideStates[f.State()] // bb4: frame.state; dispatch
	for {
		switch block {
		case 0:
			f.v = f.args[0] // recv 0; local.set $v
			f.SetState(1)
			// ret.suspend (byval $v) -- and note there is no key, which is the
			// whole difference from ret.yield at this level.
			return php.Outcome{Kind: php.Suspended, Value: f.v}, nil

		case 1:
			r, err := f.Resumed() // frame.resumed
			if err != nil {
				return php.Outcome{}, err
			}
			f.t3, f.got = r, r
			f.SetState(2)
			return php.Outcome{Kind: php.Suspended, Value: f.v}, nil

		case 2:
			r, err := f.Resumed() // frame.resumed
			if err != nil {
				return php.Outcome{}, err
			}
			f.t5, f.g2 = r, r
			out := php.Concat(f.got, f.g2) // concat; verify_return
			return php.Outcome{Kind: php.Returned, Value: out}, nil

		default:
			// bb3 (`verify_return.none; ret null`) is unreachable after the
			// rewrite, the same way `counter`'s bb8 is: nothing targets it and
			// it is not in the dispatch table.
			return php.Outcome{}, fmt.Errorf("inside: no block bb%d", block)
		}
	}
}

func newInside(arg php.Value) *insideFrame {
	return &insideFrame{args: []php.Value{arg}}
}

// straightFrame is a fiber body that never suspends: `function () { return 5; }`.
// Its whole state machine is one block, because with no suspension the CPS pass
// leaves the function alone -- which is what makes it the right shape to check
// that `start()` answers NULL rather than the return value.
type straightFrame struct {
	php.FrameState
	ret php.Value
}

func (f *straightFrame) Step(_ *php.Ctx) (php.Outcome, error) {
	return php.Outcome{Kind: php.Returned, Value: f.ret}, nil
}

// The protocol end to end, against the IR as it is actually emitted. The
// engine's answers for the same program are in the test below it; this one is
// the mechanism.
func TestFiberProtocolOverTranscribedIR(t *testing.T) {
	ctx := php.NewCtx(&strings.Builder{})
	f := php.NewFiber(newInside(php.String("arg")))

	first, err := f.Start(ctx)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if first.String() != "arg" {
		t.Errorf("start gave %q, want the first suspension's value %q", first.String(), "arg")
	}

	second, err := f.Resume(ctx, php.String("a"))
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if second.String() != "arg" {
		t.Errorf("resume gave %q, want the second suspension's value %q", second.String(), "arg")
	}

	// The body returns rather than suspending again, so resume answers NULL and
	// the return value is only available through getReturn().
	last, err := f.Resume(ctx, php.String("b"))
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if last.Kind() != php.KindNull {
		t.Errorf("a resume that ended the fiber gave %s, want NULL", last.String())
	}
	got, err := f.GetReturn()
	if err != nil {
		t.Fatalf("getReturn: %v", err)
	}
	if got.String() != "ab" {
		t.Errorf("getReturn gave %q, want %q -- the two resumed values concatenated",
			got.String(), "ab")
	}
}

// The four predicates through a fiber's life. Every row is the engine's answer
// for the equivalent program, taken from it rather than reasoned about:
//
//	$f = new Fiber(function () { Fiber::suspend(1); return 2; });
//	var_dump($f->isStarted(), $f->isSuspended(), $f->isRunning(), $f->isTerminated());
func TestFiberStatusMatchesTheEngine(t *testing.T) {
	ctx := php.NewCtx(&strings.Builder{})
	f := php.NewFiber(newInside(php.String("x")))

	check := func(when string, started, suspended, running, terminated bool) {
		t.Helper()
		got := [4]bool{f.IsStarted(), f.IsSuspended(), f.IsRunning(), f.IsTerminated()}
		want := [4]bool{started, suspended, running, terminated}
		if got != want {
			t.Errorf("%s: started/suspended/running/terminated = %v, want %v", when, got, want)
		}
	}

	// Before start: all four false. A fiber is not "not started" in a way any
	// predicate reports positively.
	check("before start", false, false, false, false)
	if _, err := f.Start(ctx); err != nil {
		t.Fatal(err)
	}
	check("suspended", true, true, false, false)
	if _, err := f.Resume(ctx, php.String("a")); err != nil {
		t.Fatal(err)
	}
	check("suspended again", true, true, false, false)
	if _, err := f.Resume(ctx, php.String("b")); err != nil {
		t.Fatal(err)
	}
	// isStarted stays true after termination -- it is "was started", not "is
	// running".
	check("terminated", true, false, false, true)
}

// Both refusals that guard the entry points, and the fact that neither leaves
// the fiber in a different state than it was.
func TestFiberRefusesToStartTwiceOrResumeWhenNotSuspended(t *testing.T) {
	ctx := php.NewCtx(&strings.Builder{})
	f := php.NewFiber(newInside(php.String("x")))

	if _, err := f.Resume(ctx, php.Null()); !errors.Is(err, php.ErrFiberNotSuspended) {
		t.Errorf("resume before start gave %v, want ErrFiberNotSuspended", err)
	}
	if _, err := f.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Start(ctx); !errors.Is(err, php.ErrFiberAlreadyStarted) {
		t.Errorf("second start gave %v, want ErrFiberAlreadyStarted", err)
	}
	if !f.IsSuspended() {
		t.Error("a refused start moved the fiber out of suspended")
	}

	// Run it out, then resume a terminated fiber.
	for i := 0; i < 2 && !f.IsTerminated(); i++ {
		if _, err := f.Resume(ctx, php.String("a")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.Resume(ctx, php.Null()); !errors.Is(err, php.ErrFiberNotSuspended) {
		t.Errorf("resume after termination gave %v, want ErrFiberNotSuspended", err)
	}
}

// PHP words getReturn()'s refusal three different ways -- `The fiber has not
// been started`, `has not returned`, `threw an exception` -- so there are three
// sentinels and not one. A generator has a single message for all of them,
// which is why ErrGeneratorNotReturned stands alone; keeping that asymmetry is
// the point of this test.
func TestFiberGetReturnHasThreeDistinctRefusals(t *testing.T) {
	ctx := php.NewCtx(&strings.Builder{})

	notStarted := php.NewFiber(newInside(php.String("x")))
	if _, err := notStarted.GetReturn(); !errors.Is(err, php.ErrFiberNotStarted) {
		t.Errorf("getReturn before start gave %v, want ErrFiberNotStarted", err)
	}

	suspended := php.NewFiber(newInside(php.String("x")))
	if _, err := suspended.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := suspended.GetReturn(); !errors.Is(err, php.ErrFiberNotReturned) {
		t.Errorf("getReturn while suspended gave %v, want ErrFiberNotReturned", err)
	}

	threw := php.NewFiber(newInside(php.String("x")))
	if _, err := threw.Start(ctx); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	if _, err := threw.Throw(ctx, boom); !errors.Is(err, boom) {
		t.Fatalf("throw gave %v, want the exception back out", err)
	}
	if !threw.IsTerminated() {
		t.Error("a fiber whose body did not catch a thrown-in exception is terminated")
	}
	if _, err := threw.GetReturn(); !errors.Is(err, php.ErrFiberThrew) {
		t.Errorf("getReturn after a throw gave %v, want ErrFiberThrew", err)
	}
}

// A body that returns without ever suspending: start() answers NULL, not the
// return value. The distinction is invisible until a fiber returns something
// truthy, and then it is the difference between `start()` meaning "the first
// suspension's value" and "whatever came back".
func TestFiberThatNeverSuspends(t *testing.T) {
	ctx := php.NewCtx(&strings.Builder{})
	f := php.NewFiber(&straightFrame{ret: php.Int(5)})

	got, err := f.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind() != php.KindNull {
		t.Errorf("start gave %s, want NULL: 5 is the return value, not a suspension", got.String())
	}
	if !f.IsTerminated() {
		t.Error("a body that returned leaves the fiber terminated")
	}
	ret, err := f.GetReturn()
	if err != nil {
		t.Fatalf("getReturn: %v", err)
	}
	if ret.Int() != 5 {
		t.Errorf("getReturn gave %s, want 5", ret.String())
	}
}

// `Fiber::throw()` has to raise *inside* the body, at the parked suspension, or
// the fiber's own try/catch around it cannot see it. Same mechanism as
// Generator::throw, and the same single program point: the `frame.resumed` read.
func TestThrowIntoAFiberSurfacesAtItsSuspensionPoint(t *testing.T) {
	ctx := php.NewCtx(&strings.Builder{})
	f := php.NewFiber(newInside(php.String("x")))
	if _, err := f.Start(ctx); err != nil {
		t.Fatal(err)
	}

	boom := errors.New("thrown into the fiber")
	_, err := f.Throw(ctx, boom)
	if !errors.Is(err, boom) {
		t.Fatalf("throw gave %v, want the exception to come back out uncaught", err)
	}
	// The body did not catch it, so it terminated -- and it never returned, so
	// getReturn is refused with the *threw* message rather than the not-returned
	// one.
	if _, err := f.GetReturn(); !errors.Is(err, php.ErrFiberThrew) {
		t.Errorf("getReturn gave %v, want ErrFiberThrew", err)
	}
}

// ---------------------------------------------------------------------------
// A generator body that suspends a fiber
// ---------------------------------------------------------------------------

// suspendingGenFrame is `gen1` from `Zend/tests/gh15108-004.phpt`, transcribed
// from `php2lower -cps -print`:
//
//	function gen1() {
//	    yield 'foo';
//	    Fiber::suspend();
//	    var_dump("not executed");
//	}
//
//	.suspends_to_caller
//	.resumable [ bb0, bb1, bb2, bb3 ]
//	.entry bb4
//
// It carries `.suspends_to_caller` *and* is a generator -- 26 of the corpus's 95
// marked bodies are. Nothing calls it directly, so the emitter's direct-call
// rule never fires; the suspension escapes through `$g->next()` instead.
type suspendingGenFrame struct {
	php.FrameState
	reached bool // whether bb3 ran -- the engine says it never should
}

var suspendingGenStates = [...]int{0, 1, 2, 3}

func (f *suspendingGenFrame) Step(_ *php.Ctx) (php.Outcome, error) {
	block := suspendingGenStates[f.State()]
	for {
		switch block {
		case 0:
			f.SetState(1)
			return php.Outcome{Kind: php.Created}, nil // ret.generator

		case 1:
			if _, err := f.Resumed(); err != nil {
				return php.Outcome{}, err
			}
			f.SetState(2)
			return php.Outcome{Kind: php.Yielded, Value: php.String("foo")}, nil

		case 2:
			if _, err := f.Resumed(); err != nil {
				return php.Outcome{}, err
			}
			f.SetState(3)
			// ret.suspend () -- no operand, so NULL.
			return php.Outcome{Kind: php.Suspended, Value: php.Null()}, nil

		case 3:
			if _, err := f.Resumed(); err != nil {
				return php.Outcome{}, err
			}
			f.reached = true // var_dump("not executed")
			return php.Outcome{Kind: php.Returned, Value: php.Null()}, nil

		default:
			return php.Outcome{}, fmt.Errorf("gen1: no block bb%d", block)
		}
	}
}

// The gap this leaves is reported, not faked, and not mistaken for a broken
// body.
//
// A `Fiber::suspend()` inside a generator has to travel out through the
// Generator driver and back in on resume, across whatever PHP frame called
// `$g->next()` -- and that frame is not resumable, which is the open caller
// question. So the driver refuses. What it must *not* do is abort the generator:
// the engine leaves it parked at that point, so throwing the state away would
// make a later implementation impossible and would blame the body for a missing
// capability.
func TestSuspendInsideAGeneratorIsReportedNotFaked(t *testing.T) {
	ctx := php.NewCtx(&strings.Builder{})
	body := &suspendingGenFrame{}
	g, err := php.StartGenerator(ctx, body)
	if err != nil {
		t.Fatal(err)
	}

	// The yield before the suspension is ordinary and works.
	v, err := g.Current(ctx)
	if err != nil {
		t.Fatalf("current: %v", err)
	}
	if v.String() != "foo" {
		t.Errorf("current gave %q, want %q", v.String(), "foo")
	}

	err = g.Next(ctx)
	if !errors.Is(err, php.ErrSuspendAcrossDriver) {
		t.Fatalf("advancing into a Fiber::suspend gave %v, want ErrSuspendAcrossDriver", err)
	}
	if errors.Is(err, php.ErrProtocol) {
		t.Error("reported as a protocol violation, but the IR and the emitter are both right")
	}
	if g.Finished() {
		t.Error("the generator was finished off; the engine leaves it parked at the suspension")
	}
	if body.reached {
		t.Error("execution continued past the suspension")
	}

	// A second advance reports the same gap rather than re-entering the
	// continuation with a stale resumed value, which would look like it worked.
	if err := g.Next(ctx); !errors.Is(err, php.ErrSuspendAcrossDriver) {
		t.Errorf("second next gave %v, want the same refusal", err)
	}
	if body.reached {
		t.Error("a second advance ran the continuation")
	}
}

// delegatingToSuspendingFrame is `outer` in
//
//	function inner() { yield 1; Fiber::suspend('escaped'); }
//	function outer($g) { yield from $g; var_dump("not executed"); }
//
// which is `gh15108-004.phpt`'s shape with the names shortened: the delegate
// arrives as a *parameter*, so `outer` contains no literal `Fiber::suspend()`
// and carries no `.suspends_to_caller`. It is the same block layout the compiler
// emits for that test's `gen2` -- `ret.generator`, `ret.yield_from $gen`, then
// the continuation.
type delegatingToSuspendingFrame struct {
	php.FrameState
	args    []php.Value
	gen     php.Value
	t1      php.Value
	reached bool
}

var delegatingToSuspendingStates = [...]int{0, 1, 2}

func (f *delegatingToSuspendingFrame) Step(_ *php.Ctx) (php.Outcome, error) {
	switch delegatingToSuspendingStates[f.State()] {
	case 0:
		f.gen = f.args[0] // recv 0; local.set $gen
		f.SetState(1)
		return php.Outcome{Kind: php.Created}, nil
	case 1:
		if _, err := f.Resumed(); err != nil {
			return php.Outcome{}, err
		}
		f.SetState(2)
		return php.Outcome{Kind: php.Delegated, Value: f.gen}, nil // ret.yield_from $gen
	case 2:
		r, err := f.Resumed()
		if err != nil {
			return php.Outcome{}, err
		}
		f.t1 = r
		f.reached = true // var_dump("not executed")
		return php.Outcome{Kind: php.Returned, Value: php.Null()}, nil
	}
	return php.Outcome{}, fmt.Errorf("outer: no state %d", f.State())
}

// innerSuspendingFrame is `function inner() { yield 1; Fiber::suspend('escaped'); }`.
type innerSuspendingFrame struct {
	php.FrameState
}

func (f *innerSuspendingFrame) Step(_ *php.Ctx) (php.Outcome, error) {
	switch f.State() {
	case 0:
		f.SetState(1)
		return php.Outcome{Kind: php.Created}, nil
	case 1:
		if _, err := f.Resumed(); err != nil {
			return php.Outcome{}, err
		}
		f.SetState(2)
		return php.Outcome{Kind: php.Yielded, Value: php.Int(1)}, nil
	case 2:
		if _, err := f.Resumed(); err != nil {
			return php.Outcome{}, err
		}
		f.SetState(3)
		return php.Outcome{Kind: php.Suspended, Value: php.String("escaped")}, nil
	}
	return php.Outcome{}, fmt.Errorf("inner: no state %d", f.State())
}

// The marker does not survive `yield from`, and the driver must not either.
//
// `outer` carries no `.suspends_to_caller` -- it contains no literal
// `Fiber::suspend()` -- yet advancing it reaches one. The engine's answer for
// the equivalent program is that the *fiber* suspends and both generators stay
// parked; the wrong thing here would be to abort, which discards the whole
// `yield from` chain on behalf of a capability that is merely missing.
func TestASuspensionTravelsUpAYieldFromChain(t *testing.T) {
	ctx := php.NewCtx(&strings.Builder{})
	inner, err := php.StartGenerator(ctx, &innerSuspendingFrame{})
	if err != nil {
		t.Fatal(err)
	}
	obj := php.NewObject("Generator", 1)
	obj.Internal = inner
	body := &delegatingToSuspendingFrame{args: []php.Value{php.Obj(obj)}}
	outer, err := php.StartGenerator(ctx, body)
	if err != nil {
		t.Fatal(err)
	}

	// The delegated yield reads through, as it should.
	v, err := outer.Current(ctx)
	if err != nil {
		t.Fatalf("current: %v", err)
	}
	if v.Int() != 1 {
		t.Errorf("current gave %s, want 1 read through the delegate", v.String())
	}

	// Advancing reaches inner's Fiber::suspend, one level down.
	err = outer.Next(ctx)
	if !errors.Is(err, php.ErrSuspendAcrossDriver) {
		t.Fatalf("advancing the outer generator gave %v, want ErrSuspendAcrossDriver", err)
	}
	if outer.Finished() {
		t.Error("the outer generator was aborted; both generators stay parked")
	}
	if inner.Finished() {
		t.Error("the inner generator was aborted; the engine leaves it parked")
	}
	if body.reached {
		t.Error("the outer body continued past its yield from")
	}
}

// ---------------------------------------------------------------------------
// Iter: the `*iter` frame slot
// ---------------------------------------------------------------------------

// A `foreach` iterates a copy. Every row here is the engine's answer for the
// equivalent program, taken from `php2eval -engine` rather than reasoned about
// from refcount rules:
//
//	$b = ['x'=>1,'y'=>2,'z'=>3];
//	foreach ($b as $k => $v) { if ($k=='x') { $b['z'] = 30; } echo "$k=$v,"; }
//	// x=1,y=2,z=3,
//
// Snapshotting only the *keys* is not enough, which is the trap: it gets the
// append and the unset right and still reports 30 for `z`.
func TestIterSnapshotsValuesNotJustKeys(t *testing.T) {
	a := php.NewArray()
	a.Set(php.StringKey("x"), php.Int(1))
	a.Set(php.StringKey("y"), php.Int(2))
	a.Set(php.StringKey("z"), php.Int(3))

	it := php.NewArrayIter(a)

	// Mutate every way the loop could notice: overwrite an element not yet
	// reached, append, and unset one still ahead.
	a.Set(php.StringKey("z"), php.Int(30))
	a.Append(php.Int(99))
	a.Unset(php.StringKey("y"))

	var got []string
	for it.Valid() {
		k, _ := it.Key(nil)
		v, _ := it.Current(nil)
		got = append(got, fmt.Sprintf("%s=%s", k.String(), v.String()))
		if err := it.Next(nil); err != nil {
			t.Fatalf("next: %v", err)
		}
	}
	if want := "[x=1 y=2 z=3]"; fmt.Sprint(got) != want {
		t.Errorf("iterated %s, want %s -- the snapshot, not the array as it is now",
			fmt.Sprint(got), want)
	}
}

// The loop header may only ask. FE_FETCH tests for exhaustion *before* it
// fetches, so a header that also advanced would drop the last element -- which
// is exactly the defect this path shipped with. This asserts the property of
// Iter that makes the correct emission possible: Valid is pure.
func TestIterValidDoesNotAdvance(t *testing.T) {
	it := php.NewArrayIter(ints(1, 2, 3))
	for i := 0; i < 10; i++ {
		if !it.Valid() {
			t.Fatal("Valid consumed the iterator")
		}
	}
	if first, _ := it.Current(nil); first.Int() != 1 {
		t.Errorf("after ten Valid calls Current is %d, want 1", first.Int())
	}
	// And the full walk yields every element, last one included.
	var got []int64
	for it.Valid() {
		v, _ := it.Current(nil)
		got = append(got, v.Int())
		_ = it.Next(nil)
	}
	if fmt.Sprint(got) != "[1 2 3]" {
		t.Errorf("walked %v, want [1 2 3]", got)
	}
}

// An empty array runs the body zero times rather than once.
func TestIterOverAnEmptyArray(t *testing.T) {
	it := php.NewArrayIter(php.NewArray())
	if it.Valid() {
		t.Error("an empty array is not valid at reset")
	}
	cur, _ := it.Current(nil)
	key, _ := it.Key(nil)
	if cur.Kind() != php.KindNull || key.Kind() != php.KindNull {
		t.Error("reading past the end gives NULL")
	}
}

// A generator is *not* snapshotted -- it has no elements until asked -- so the
// two sources share an interface and nothing else. Keys are the generator's own
// automatic ones, checked against `foreach (g() as $k=>$v)` giving `0=>a,1=>b`.
func TestIterOverAGenerator(t *testing.T) {
	ctx := php.NewCtx(&strings.Builder{})
	g, err := php.StartGenerator(ctx, &keysFrame{})
	if err != nil {
		t.Fatal(err)
	}
	obj := php.NewObject("Generator", 1)
	obj.Internal = g

	it, err := php.NewIter(ctx, php.Obj(obj))
	if err != nil {
		t.Fatalf("reset over a generator: %v", err)
	}
	var got []string
	for it.Valid() {
		k, _ := it.Key(ctx)
		v, _ := it.Current(ctx)
		got = append(got, fmt.Sprintf("%s=>%s", k.String(), v.String()))
		if err := it.Next(ctx); err != nil {
			t.Fatalf("next: %v", err)
		}
	}
	if want := "[5=>a 6=>b 7=>]"; fmt.Sprint(got) != want {
		t.Errorf("iterated %s, want %s", fmt.Sprint(got), want)
	}
}

// The two things `foreach` refuses are not the same thing, and only one is ours.
//
// A **scalar** is the program being wrong: the engine warns and runs the body
// zero times, then carries on --
//
//	foreach (5 as $v) {}   Warning: foreach() argument must be of type array|object, int given
//
// An **object we cannot reach** is a gap: the hop into user code is not
// installed. Reporting a scalar as a gap, which this used to do, files a
// program's mistake as our missing capability -- the same two-populations
// conflation as an `unsupported` bucket that hides "the oracle could not ask".
func TestForeachOverAScalarWarnsAndRunsZeroTimes(t *testing.T) {
	for _, c := range []struct {
		v    php.Value
		want string
	}{
		{php.Int(5), "int given"},
		{php.String("str"), "string given"},
		{php.Null(), "null given"},
	} {
		var out strings.Builder
		ctx := php.NewCtx(&out)
		it, err := php.NewIter(ctx, c.v)
		if err != nil {
			t.Errorf("foreach over %s gave %v, want a warning and an empty loop", c.v.Kind(), err)
			continue
		}
		if it.Valid() {
			t.Errorf("foreach over %s is valid; the body must run zero times", c.v.Kind())
		}
		if !strings.Contains(out.String(), "foreach() argument must be of type array|object, "+c.want) {
			t.Errorf("foreach over %s said %q, want the engine's warning", c.v.Kind(), out.String())
		}
	}
}

// An object with no resolver *is* a gap, and stays one.
func TestForeachOverAnUnreachableObjectIsReportedNotFaked(t *testing.T) {
	ctx := php.NewCtx(&strings.Builder{})
	if _, err := php.NewIter(ctx, php.Obj(php.NewObject("P", 1))); !errors.Is(err, php.ErrIterUnsupported) {
		t.Errorf("foreach over an unreachable object gave %v, want ErrIterUnsupported", err)
	}
}

// `yield from` splits the same way and throws where foreach warns -- checked:
// `Error: Can use "yield from" only with arrays and Traversables`. So the two
// diagnostics differ in kind as well as in text, and neither is a gap.
func TestYieldFromANonTraversableIsAPhpErrorNotAGap(t *testing.T) {
	ctx := php.NewCtx(&strings.Builder{})
	if _, err := php.NewDelegate(ctx, php.Int(5)); !errors.Is(err, php.ErrNotDelegatable) {
		t.Errorf("yield from 5 gave %v, want ErrNotDelegatable", err)
	}
	if _, err := php.NewDelegate(ctx, php.Obj(php.NewObject("P", 1))); !errors.Is(err, php.ErrDelegateUnsupported) {
		t.Errorf("yield from an unreachable object gave %v, want ErrDelegateUnsupported", err)
	}
}

// `yield from` over an array holds a snapshot for the same reason `foreach`
// does, and the engine agrees even when the array arrives by reference:
//
//	$b = [1,2,3];  function h(&$r) { yield from $r; }
//	$it = h($b);  $it->current();  // 1
//	$b[1] = 77;   $it->next();  $it->current();  // 2, not 77
func TestYieldFromAnArrayHoldsASnapshot(t *testing.T) {
	ctx := php.NewCtx(&strings.Builder{})
	arr := ints(1, 2, 3)
	g, err := php.StartGenerator(ctx, &delegatingFrame{source: php.Arr(arr)})
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := g.Current(ctx); v.Int() != 1 {
		t.Fatalf("first element is %s, want 1", v.String())
	}
	// Mutate the array the delegation came from.
	arr.Set(php.IntKey(1), php.Int(77))
	if err := g.Next(ctx); err != nil {
		t.Fatal(err)
	}
	if v, _ := g.Current(ctx); v.Int() != 2 {
		t.Errorf("second element is %s, want 2 -- the delegation holds a snapshot", v.String())
	}
}

// ---------------------------------------------------------------------------
// Traversable: foreach and yield from over a PHP object
// ---------------------------------------------------------------------------

// recordingTraversable stands in for the hop into user code that iterating a PHP
// object needs. It records the order its methods are called, which is the point:
// the sequence is observable from PHP -- an Iterator whose methods echo shows it
// -- so it is behaviour and not an implementation detail.
type recordingTraversable struct {
	items []php.Value
	at    int
	log   []string
}

func (r *recordingTraversable) note(s string) { r.log = append(r.log, s) }

func (r *recordingTraversable) Rewind(_ *php.Ctx) error { r.note("rewind"); r.at = 0; return nil }

func (r *recordingTraversable) Valid(_ *php.Ctx) (bool, error) {
	r.note("valid")
	return r.at < len(r.items), nil
}

func (r *recordingTraversable) Current(_ *php.Ctx) (php.Value, error) {
	r.note("current")
	return r.items[r.at], nil
}

func (r *recordingTraversable) Key(_ *php.Ctx) (php.Value, error) {
	r.note("key")
	return php.String(fmt.Sprintf("k%d", r.at)), nil
}

func (r *recordingTraversable) Next(_ *php.Ctx) error { r.note("next"); r.at++; return nil }

// withTraversable installs a resolver that answers with r for any object.
func withTraversable(r *recordingTraversable) (*php.Ctx, php.Value) {
	ctx := php.NewCtx(&strings.Builder{})
	ctx.SetTraversableResolver(func(_ *php.Ctx, _ php.Value) (php.Traversable, bool, error) {
		return r, true, nil
	})
	return ctx, php.Obj(php.NewObject("It", 1))
}

// The call sequence is the engine's, taken from an Iterator whose methods echo:
//
//	foreach (new It as $k => $v)   rewind valid current key [body] next valid ...
//
// ending on the valid that answers false. Asserting the *log* rather than only
// the values is what makes this a test of the protocol rather than of the
// stand-in.
func TestForeachOverATraversableMatchesTheEngineSequence(t *testing.T) {
	r := &recordingTraversable{items: []php.Value{php.String("x"), php.String("y")}}
	ctx, obj := withTraversable(r)

	it, err := php.NewIter(ctx, obj)
	if err != nil {
		t.Fatalf("reset: %v", err)
	}
	var got []string
	for it.Valid() {
		// current *then* key, which is the engine's order and the IR's --
		// `iter.current` precedes `iter.key` in the lowered block. Iter cannot
		// enforce it: the order is the caller's, so it is the emitter's
		// obligation, and it is observable from PHP through an Iterator whose
		// methods echo.
		v, err := it.Current(ctx)
		if err != nil {
			t.Fatal(err)
		}
		k, err := it.Key(ctx)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, fmt.Sprintf("%s=%s", k.String(), v.String()))
		if err := it.Next(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if want := "[k0=x k1=y]"; fmt.Sprint(got) != want {
		t.Errorf("iterated %s, want %s", fmt.Sprint(got), want)
	}
	want := "rewind valid current key next valid current key next valid"
	if strings.Join(r.log, " ") != want {
		t.Errorf("call sequence:\n got %s\nwant %s", strings.Join(r.log, " "), want)
	}
}

// `foreach ($obj as $v)` -- no key bound -- never calls key(). Checked against
// the engine, and it is why Key is a separate call the emitter makes only where
// the IR has `iter.key`, rather than something Current returns alongside.
func TestForeachOverATraversableWithoutAKeyNeverCallsKey(t *testing.T) {
	r := &recordingTraversable{items: []php.Value{php.String("x"), php.String("y")}}
	ctx, obj := withTraversable(r)

	it, err := php.NewIter(ctx, obj)
	if err != nil {
		t.Fatal(err)
	}
	for it.Valid() {
		if _, err := it.Current(ctx); err != nil {
			t.Fatal(err)
		}
		if err := it.Next(ctx); err != nil {
			t.Fatal(err)
		}
	}
	want := "rewind valid current next valid current next valid"
	if strings.Join(r.log, " ") != want {
		t.Errorf("call sequence:\n got %s\nwant %s", strings.Join(r.log, " "), want)
	}
	for _, call := range r.log {
		if call == "key" {
			t.Error("key() was called for a loop that binds no key")
		}
	}
}

// Valid is called exactly once per iteration however many times the loop header
// asks. It has to be: `valid()` is user code, and an Iterator that echoes would
// show a second call. The answer is cached by NewIter and Next, which is
// faithful because the engine always pairs `next` with the `valid` after it.
func TestTraversableValidIsAskedOncePerIteration(t *testing.T) {
	r := &recordingTraversable{items: []php.Value{php.String("x")}}
	ctx, obj := withTraversable(r)

	it, err := php.NewIter(ctx, obj)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if !it.Valid() {
			t.Fatal("Valid changed its answer without an intervening Next")
		}
	}
	count := 0
	for _, call := range r.log {
		if call == "valid" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("ten Valid calls reached user code %d times, want 1", count)
	}
}

// `yield from` over a Traversable is driven with the same sequence, rewind
// included -- checked against the engine, where `function g() { yield from new
// It; }` logs exactly what a foreach over the same object logs.
func TestYieldFromATraversable(t *testing.T) {
	r := &recordingTraversable{items: []php.Value{php.String("x"), php.String("y")}}
	ctx, obj := withTraversable(r)

	g, err := php.StartGenerator(ctx, &delegatingFrame{source: obj})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := fmt.Sprint(drain(t, ctx, g)), `[k0=>x k1=>y]`; got != want {
		t.Errorf("delegated yields: got %s, want %s", got, want)
	}
	// An Iterator has no return value, so the `yield from` expression is NULL
	// the way it is for an array.
	if ret, err := g.GetReturn(ctx); err != nil || ret.Kind() != php.KindNull {
		t.Errorf("getReturn = %v, %v; want NULL", ret.String(), err)
	}
}

// With no resolver installed the capability is absent, and says so. An iterator
// that yielded nothing would make the loop look like a working walk over an
// empty collection, which is the masquerade QUALITY_GATE.md forbids.
func TestTraversableWithoutAResolverIsReportedNotFaked(t *testing.T) {
	ctx := php.NewCtx(&strings.Builder{})
	obj := php.Obj(php.NewObject("It", 1))
	if _, err := php.NewIter(ctx, obj); !errors.Is(err, php.ErrIterUnsupported) {
		t.Errorf("foreach with no resolver gave %v, want ErrIterUnsupported", err)
	}
	if _, err := php.NewDelegate(ctx, obj); !errors.Is(err, php.ErrDelegateUnsupported) {
		t.Errorf("yield from with no resolver gave %v, want ErrDelegateUnsupported", err)
	}
}

// A driver call made from a frame that *is* resumable can carry the suspension
// out and resume the generator afterwards, which is what the refusal above was
// waiting for.
//
// The generator is left **parked** while the fiber is away -- not finished, not
// broken -- and resuming hands the value to the body exactly where it stopped.
func TestGeneratorDriveCarriesASuspensionOutAndBack(t *testing.T) {
	ctx := php.NewCtx(&strings.Builder{})
	body := &suspendingGenFrame{}
	g, err := php.StartGenerator(ctx, body)
	if err != nil {
		t.Fatal(err)
	}
	if v, err := g.Current(ctx); err != nil || v.String() != "foo" {
		t.Fatalf("current gave %v, %v; want \"foo\"", v, err)
	}

	// `$g->next()` as a resumable: the body suspends, and that reaches here as
	// a suspension rather than as a refusal.
	drive := php.DriveGenerator(g, func(c *php.Ctx) error { return g.Next(c) }, nil)
	out, err := drive.Step(ctx)
	if err != nil {
		t.Fatalf("driving into the suspension failed: %v", err)
	}
	if out.Kind != php.Suspended {
		t.Fatalf("the drive gave %s, want suspended", out.Kind)
	}
	if g.Finished() {
		t.Error("the generator was finished off; it is parked at the suspension")
	}
	if body.reached {
		t.Error("execution continued past the suspension")
	}

	// The fiber comes back, and the body continues from where it stopped.
	drive.Resume(php.String("resumed"))
	out, err = drive.Step(ctx)
	if err != nil {
		t.Fatalf("resuming the parked body failed: %v", err)
	}
	if out.Kind != php.Returned {
		t.Fatalf("after the resume the drive gave %s, want returned", out.Kind)
	}
	if !body.reached {
		t.Error("the body did not continue past the suspension after the resume")
	}
}

// An ordinary driver call on a parked generator still reports the gap: nothing
// there can resume it, and re-entering the body with a stale value would look
// like it worked.
func TestParkedGeneratorStillRefusesAnOrdinaryAdvance(t *testing.T) {
	ctx := php.NewCtx(&strings.Builder{})
	g, err := php.StartGenerator(ctx, &suspendingGenFrame{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Current(ctx); err != nil {
		t.Fatal(err)
	}
	refused := g.Next(ctx)
	if !errors.Is(refused, php.ErrSuspendAcrossDriver) {
		t.Fatalf("advancing into the suspension gave %v, want the gap", refused)
	}
	// Classified as a gap, not just carrying the sentinel: a harness grades a
	// bare sentinel as a failure and a gap as `unsupported`, and this is one.
	if !php.IsUnsupported(refused) {
		t.Errorf("the gap is reported as a plain error (%T), so a harness grades it a failure", refused)
	}
	if err := g.Next(ctx); !errors.Is(err, php.ErrSuspendAcrossDriver) {
		t.Errorf("a second advance gave %v, want the same refusal", err)
	}
}

// yieldAfterSuspendFrame yields, suspends a fiber, then **yields again** on
// resume. That last part is what tells a parked generator that genuinely
// continued from one that only looked like it did.
type yieldAfterSuspendFrame struct {
	php.FrameState
	got php.Value
}

func (f *yieldAfterSuspendFrame) Step(_ *php.Ctx) (php.Outcome, error) {
	switch f.State() {
	case 0:
		f.SetState(1)
		return php.Outcome{Kind: php.Created}, nil
	case 1:
		if _, err := f.Resumed(); err != nil {
			return php.Outcome{}, err
		}
		f.SetState(2)
		return php.Outcome{Kind: php.Yielded, Value: php.String("foo")}, nil
	case 2:
		if _, err := f.Resumed(); err != nil {
			return php.Outcome{}, err
		}
		f.SetState(3)
		return php.Outcome{Kind: php.Suspended, Value: php.String("parked")}, nil
	case 3:
		v, err := f.Resumed()
		if err != nil {
			return php.Outcome{}, err
		}
		f.got = v
		f.SetState(4)
		return php.Outcome{Kind: php.Yielded, Value: php.String("after:" + v.String())}, nil
	default:
		return php.Outcome{Kind: php.Returned, Value: php.Null()}, nil
	}
}

// A generator that resumes and then yields again must report the **new**
// element, not the one it was parked on.
//
// The suspension travelling out is only half of it: what proves the body really
// continued is the element it produces afterwards, and a chain that forgot to
// advance would answer the stale one with nothing to say it had.
func TestParkedGeneratorYieldsAgainAfterResuming(t *testing.T) {
	ctx := php.NewCtx(&strings.Builder{})
	body := &yieldAfterSuspendFrame{}
	g, err := php.StartGenerator(ctx, body)
	if err != nil {
		t.Fatal(err)
	}
	if v, err := g.Current(ctx); err != nil || v.String() != "foo" {
		t.Fatalf("first current gave %v, %v; want \"foo\"", v, err)
	}

	drive := php.DriveGenerator(g, func(c *php.Ctx) error { return g.Next(c) }, nil)
	out, err := drive.Step(ctx)
	if err != nil {
		t.Fatalf("driving into the suspension failed: %v", err)
	}
	if out.Kind != php.Suspended || out.Value.String() != "parked" {
		t.Fatalf("the drive gave %s/%v, want suspended with \"parked\"", out.Kind, out.Value)
	}

	drive.Resume(php.String("R"))
	out, err = drive.Step(ctx)
	if err != nil {
		t.Fatalf("resuming the parked body failed: %v", err)
	}
	if out.Kind != php.Returned {
		t.Fatalf("after the resume the drive gave %s, want returned", out.Kind)
	}
	if body.got.String() != "R" {
		t.Errorf("the body resumed with %q, want \"R\"", body.got.String())
	}
	// The point of the test.
	v, err := g.Current(ctx)
	if err != nil {
		t.Fatalf("current after the resume failed: %v", err)
	}
	if v.String() != "after:R" {
		t.Errorf("current after the resume gave %q, want %q -- the chain did not advance",
			v.String(), "after:R")
	}
}

// genTraversable drives a Generator through the Traversable surface, the way
// `yield from new It()` over an `IteratorAggregate` does once `getIterator()`
// has answered. It names the generator it drives, which is how the parked body
// is found from above.
type genTraversable struct{ g *php.Generator }

func (t genTraversable) SourceGenerator() *php.Generator         { return t.g }
func (t genTraversable) Rewind(ctx *php.Ctx) error               { return t.g.Rewind(ctx) }
func (t genTraversable) Valid(ctx *php.Ctx) (bool, error)        { return t.g.Valid(ctx) }
func (t genTraversable) Current(ctx *php.Ctx) (php.Value, error) { return t.g.Current(ctx) }
func (t genTraversable) Key(ctx *php.Ctx) (php.Value, error)     { return t.g.Key(ctx) }
func (t genTraversable) Next(ctx *php.Ctx) error                 { return t.g.Next(ctx) }

// aggregateFrame is `function f() { yield from $it; }` where `$it` resolves to a
// Traversable rather than to a Generator directly.
type aggregateFrame struct {
	php.FrameState
	source php.Value
}

func (f *aggregateFrame) Step(_ *php.Ctx) (php.Outcome, error) {
	switch f.State() {
	case 0:
		f.SetState(1)
		return php.Outcome{Kind: php.Created}, nil
	case 1:
		if _, err := f.Resumed(); err != nil {
			return php.Outcome{}, err
		}
		f.SetState(2)
		return php.Outcome{Kind: php.Delegated, Value: f.source}, nil
	default:
		return php.Outcome{Kind: php.Returned, Value: php.Null()}, nil
	}
}

// The same resume-and-yield-again, one `yield from` further out and through a
// **Traversable** rather than a generator delegate. That is the shape
// `yield from new It()` takes, and it is the one that answered the stale
// element.
func TestParkedChainYieldsAgainThroughATraversable(t *testing.T) {
	ctx := php.NewCtx(&strings.Builder{})
	inner, err := php.StartGenerator(ctx, &yieldAfterSuspendFrame{})
	if err != nil {
		t.Fatal(err)
	}
	// The aggregate hands back the Traversable over the inner generator.
	source := php.Obj(php.NewObject("It", 1))
	ctx.SetTraversableResolver(func(*php.Ctx, php.Value) (php.Traversable, bool, error) {
		return genTraversable{g: inner}, true, nil
	})
	outer, err := php.StartGenerator(ctx, &aggregateFrame{source: source})
	if err != nil {
		t.Fatal(err)
	}
	if v, err := outer.Current(ctx); err != nil || v.String() != "foo" {
		t.Fatalf("first current gave %v, %v; want \"foo\"", v, err)
	}

	drive := php.DriveGenerator(outer, func(c *php.Ctx) error { return outer.Next(c) }, nil)
	out, err := drive.Step(ctx)
	if err != nil {
		t.Fatalf("driving into the suspension failed: %v", err)
	}
	if out.Kind != php.Suspended {
		t.Fatalf("the drive gave %s, want suspended -- the chain did not carry it out", out.Kind)
	}

	drive.Resume(php.String("R"))
	out, err = drive.Step(ctx)
	if err != nil {
		t.Fatalf("resuming through the chain failed: %v", err)
	}
	if out.Kind != php.Returned {
		t.Fatalf("after the resume the drive gave %s, want returned", out.Kind)
	}
	v, err := outer.Current(ctx)
	if err != nil {
		t.Fatalf("current after the resume failed: %v", err)
	}
	if v.String() != "after:R" {
		t.Errorf("current through the chain gave %q, want %q -- it answered the element it was parked on",
			v.String(), "after:R")
	}
}
