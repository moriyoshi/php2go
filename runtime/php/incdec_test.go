package php_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/moriyoshi/php2go/runtime/php"
)

// The `++` / `--` table, one row per case. Every expectation is the engine's own
// answer to the equivalent program rather than an inference from the
// arithmetic:
//
//	foreach ([null, true, 5, "z", "", ...] as $v) {
//	    $x = $v; $x++; var_dump($x);
//	    $y = $v; $y--; var_dump($y);
//	}
//
// The two operators are deliberately not symmetric, which is the whole reason
// this is a table and not `+ 1`: `null++` is 1 silently, `null--` is a no-op
// that warns. `var_dump` lives above this package, so a row records the kind and
// the value rather than the dump text.
func TestIncDecMatchesTheEngine(t *testing.T) {
	str := func(s string) php.Value { return php.String(s) }
	type step struct {
		kind php.Kind
		repr string
		diag string // a fragment of the diagnostic it earns, "" for silence
	}
	for _, c := range []struct {
		in   php.Value
		up   step
		down step
	}{
		{php.Null(),
			step{php.KindInt, "1", ""},
			step{php.KindNull, "", "Decrement on type null has no effect"}},
		{php.Bool(true),
			step{php.KindTrue, "1", "Increment on type bool has no effect"},
			step{php.KindTrue, "1", "Decrement on type bool has no effect"}},
		{php.Bool(false),
			step{php.KindFalse, "", "Increment on type bool has no effect"},
			step{php.KindFalse, "", "Decrement on type bool has no effect"}},
		{php.Int(5), step{php.KindInt, "6", ""}, step{php.KindInt, "4", ""}},
		{php.Float(5.5), step{php.KindFloat, "6.5", ""}, step{php.KindFloat, "4.5", ""}},
		// A numeric string steps as the number it denotes, and comes back an int.
		{str("5"), step{php.KindInt, "6", ""}, step{php.KindInt, "4", ""}},
		// Perl-style string increment, carrying within the character's class.
		{str("z"), step{php.KindString, "aa", ""},
			step{php.KindString, "z", "Decrement on non-numeric string"}},
		{str("Az"), step{php.KindString, "Ba", ""},
			step{php.KindString, "Az", "Decrement on non-numeric string"}},
		{str("zz"), step{php.KindString, "aaa", ""},
			step{php.KindString, "zz", "Decrement on non-numeric string"}},
		{str("a9"), step{php.KindString, "b0", ""},
			step{php.KindString, "a9", "Decrement on non-numeric string"}},
		{str("abc"), step{php.KindString, "abd", ""},
			step{php.KindString, "abc", "Decrement on non-numeric string"}},
		// The empty string is the one non-numeric string decrement changes, and
		// it has wording of its own.
		{str(""),
			step{php.KindString, "1", "Increment on non-alphanumeric string"},
			step{php.KindInt, "-1", "Decrement on empty string is deprecated as non-numeric"}},
		// A non-alphanumeric character is deprecated and then incremented anyway.
		{str("a-b"),
			step{php.KindString, "a-c", "Increment on non-alphanumeric string"},
			step{php.KindString, "a-b", "Decrement on non-numeric string"}},
	} {
		t.Run(fmt.Sprintf("%s_%s", c.in.Kind(), c.in.String()), func(t *testing.T) {
			run := func(what string, apply func(*php.Ctx, php.Value) (php.Value, error), want step) {
				t.Helper()
				var out strings.Builder
				got, err := apply(php.NewCtx(&out), c.in)
				if err != nil {
					t.Fatalf("%s: %v", what, err)
				}
				if got.Kind() != want.kind || got.String() != want.repr {
					t.Errorf("%s gave %s(%q), want %s(%q)",
						what, got.Kind(), got.String(), want.kind, want.repr)
				}
				switch diag := out.String(); {
				case want.diag == "" && diag != "":
					t.Errorf("%s reported %q, want silence", what, diag)
				case want.diag != "" && !strings.Contains(diag, want.diag):
					t.Errorf("%s reported %q, want it to contain %q", what, diag, want.diag)
				}
			}
			run("++", php.Increment, c.up)
			run("--", php.Decrement, c.down)
		})
	}
}

// An operand with no step defined is refused as a sentinel, not as a PHP
// exception: constructing a TypeError needs a class table, which lives above
// this package. The interpreter maps it, and so must anything else that steps a
// value -- which is the point of the sentinel being exported.
func TestIncDecRefusesWhatHasNoStep(t *testing.T) {
	ctx := php.NewCtx(&strings.Builder{})
	arr := php.Arr(php.NewArray())

	if _, err := php.Increment(ctx, arr); !errors.Is(err, php.ErrCannotIncrement) {
		t.Errorf("++ on an array gave %v, want ErrCannotIncrement", err)
	}
	if _, err := php.Decrement(ctx, arr); !errors.Is(err, php.ErrCannotDecrement) {
		t.Errorf("-- on an array gave %v, want ErrCannotDecrement", err)
	}
	// The refused value comes back unchanged, so a caller turning the sentinel
	// into a throw does not also have to undo a partial step.
	if v, _ := php.Increment(ctx, arr); v.Kind() != php.KindArray {
		t.Errorf("a refused ++ returned %s, want the operand unchanged", v.Kind())
	}

	// The name the message needs: an object by its class, everything else by
	// its type.
	if got := php.IncDecTypeName(arr); got != "array" {
		t.Errorf("IncDecTypeName(array) = %q, want %q", got, "array")
	}
	if got := php.IncDecTypeName(php.Obj(php.NewObject("Guard", 1))); got != "Guard" {
		t.Errorf("IncDecTypeName(object) = %q, want %q", got, "Guard")
	}
}
