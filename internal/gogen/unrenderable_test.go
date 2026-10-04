package gogen

import "testing"

// An operand the emitter cannot render must stop emission, never become undef.
//
// It used to emit `/* UNRENDERABLE: x */ php.Undef()` under a comment claiming
// that was deliberately invalid Go which would fail the build loudly. It is
// perfectly valid Go, so programs built and ran with holes in them: a constant
// array literal left its variable unset and the `foreach` over it died on null.
//
// This is an in-package test on purpose. It used to drive the guard through a
// PHP fixture — a constant array literal — and that fixture stopped reaching it
// the moment array literals became renderable. **A test that reaches a guard
// through a capability stops testing the guard when the capability lands**, and
// this is the third refusal test in this package invalidated that way. Calling
// `operand` directly pins the mechanism rather than one route into it.
func TestAnUnrenderableOperandIsRefusedNotSilentlyUndef(t *testing.T) {
	e := &emitter{fn: "f", defined: map[string]bool{}, abstract: map[string]bool{},
		builtins: map[string]bool{}}

	got := e.operand("<something the renderer has no case for>")

	if e.err == nil {
		t.Fatal("an unrenderable operand was rendered without complaint")
	}
	var u *Unsupported
	if !asUnsupported(e.err, &u) {
		t.Fatalf("refused, but not as a named gap: %v", e.err)
	}
	// Refused by name, so the reason is legible in a census rather than generic.
	if got == "" {
		t.Error("no placeholder was returned, so the caller would emit nothing at all")
	}
}

func asUnsupported(err error, out **Unsupported) bool {
	u, ok := err.(*Unsupported)
	if ok {
		*out = u
	}
	return ok
}
