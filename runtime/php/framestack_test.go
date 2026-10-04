package php_test

import (
	"io"
	"testing"

	"github.com/moriyoshi/php2go/runtime/php"
)

// A call made with plain ints keeps them unboxed in its frame
// (PushCallInts), and the frame's Args is built when the stack is read. A
// snapshot taken meanwhile has to show them, and has to keep showing them
// after the calls return and later calls reuse the frames.
func TestIntFramesMaterialiseTheirArguments(t *testing.T) {
	ctx := php.NewCtx(io.Discard)
	// Room first: a push into a full stack takes the general path, which
	// boxes the arguments at once, and the unboxed frame is the one to test.
	for i := 0; i < 8; i++ {
		ctx.PushCall(php.StackFrame{Function: "warm"})
	}
	for i := 0; i < 8; i++ {
		ctx.PopCall()
	}
	ctx.SetLocation("a.php", 1)
	ctx.PushCallInts("f", "a.php", 3, 2, 7, -9, 0, 0)
	ctx.PushCallArgs("g", "a.php", 4, []php.Value{php.String("s")})
	ctx.PushCallInts("h", "a.php", 5, 1, 42, 0, 0, 0)
	snap := ctx.CallStack()
	ctx.PopCall()
	ctx.PopCall()
	ctx.PushCallInts("x", "b.php", 9, 3, 1, 2, 3, 0)
	// Stack is the live view: read before the pops, which clear the slots.
	live := ctx.Stack()
	if len(live) != 2 || live[1].Function != "x" || len(live[1].Args) != 3 || live[1].Args[2].RawInt() != 3 {
		t.Fatalf("live stack %+v", live)
	}
	ctx.PopCall()
	ctx.PopCall()

	want := [][]int64{{7, -9}, nil, {42}}
	for i, frame := range snap {
		if want[i] == nil {
			if len(frame.Args) != 1 || frame.Args[0].String() != "s" {
				t.Fatalf("frame %d args = %v", i, frame.Args)
			}
			continue
		}
		if len(frame.Args) != len(want[i]) {
			t.Fatalf("frame %d (%s) has %d args, want %d", i, frame.Function, len(frame.Args), len(want[i]))
		}
		for j, v := range want[i] {
			if frame.Args[j].Kind() != php.KindInt || frame.Args[j].RawInt() != v {
				t.Fatalf("frame %d arg %d = %v, want %d", i, j, frame.Args[j], v)
			}
		}
	}
	// PopCall puts the location back at the popped frame's call site.
	if file, line := ctx.Location(); file != "a.php" || line != 3 {
		t.Fatalf("location after the pops is %s:%d, want a.php:3", file, line)
	}
}

// An int frame writes only its own fields, so the slot it lands in has to be
// clean. Each case leaves something at depth 1 that an int frame pushed there
// next must not inherit.
func TestIntFrameInheritsNothingFromTheSlot(t *testing.T) {
	warm := func() *php.Ctx {
		ctx := php.NewCtx(io.Discard)
		for i := 0; i < 8; i++ {
			ctx.PushCall(php.StackFrame{Function: "warm"})
		}
		for i := 0; i < 8; i++ {
			ctx.PopCall()
		}
		ctx.PushCall(php.StackFrame{Function: "base"})
		return ctx
	}

	// A general frame's receiver and late-static class.
	ctx := warm()
	ctx.PushCall(php.StackFrame{Function: "m", This: php.NewObject("C", 1), LSB: "C", ArgsDropped: true})
	ctx.PopCall()
	ctx.PushCallInts("i", "a.php", 3, 1, 5, 0, 0, 0)
	if f := ctx.CallStack()[1]; f.This != nil || f.LSB != "" || f.ArgsDropped || len(f.Args) != 1 || f.Args[0].RawInt() != 5 {
		t.Fatalf("an int frame inherited a general frame's fields: %+v", f)
	}

	// Sensitive marks set on an int frame.
	ctx = warm()
	ctx.PushCallInts("s", "a.php", 3, 1, 1, 0, 0, 0)
	ctx.MarkSensitiveArgs([]bool{true})
	ctx.PopCall()
	ctx.PushCallInts("t", "a.php", 3, 1, 2, 0, 0, 0)
	if f := ctx.CallStack()[1]; f.IsSensitive(0) {
		t.Fatalf("an int frame inherited sensitive marks: %+v", f)
	}

	// Borrowed arguments: a stale mark would release the slots of the frame
	// below when the int frame pops.
	ctx = php.NewCtx(io.Discard)
	for i := 0; i < 8; i++ {
		ctx.PushCall(php.StackFrame{Function: "warm"})
	}
	for i := 0; i < 8; i++ {
		ctx.PopCall()
	}
	ctx.PushCall(php.StackFrame{Function: "a"})
	ctx.PushCallArgs("b", "a.php", 2, []php.Value{php.String("x")})
	ctx.PopCall()
	ctx.PopCall()
	ctx.PushCallArgs("c", "a.php", 2, []php.Value{php.Int(7), php.Int(8), php.Int(9)})
	ctx.PushCallInts("i", "a.php", 3, 1, 5, 0, 0, 0)
	ctx.PopCall()
	if f := ctx.CallStack()[0]; len(f.Args) != 3 || f.Args[2].Kind() != php.KindInt || f.Args[2].RawInt() != 9 {
		t.Fatalf("the int frame's pop released the arguments below it: %+v", f.Args)
	}
}
