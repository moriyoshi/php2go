package php

import (
	"errors"
	"math"
	"strings"
)

// `++` and `--` are not `+ 1` and `- 1`. They have their own table of
// behaviours per type, several of which do nothing at all and say so, and the
// two operators are not symmetric: incrementing null yields 1 silently while
// decrementing it is a no-op with a warning.
//
// The whole table is in `increment_function`/`decrement_function` in
// zend_operators.c, and every entry below was confirmed against the engine
// rather than derived from the arithmetic.
//
// This lives in `runtime/php` rather than in the interpreter because both
// consumers need it: `runtime/interp` steps a value while executing an opline,
// and compiled code steps one for `pre_inc` / `post_dec`. Two implementations of
// one table would be a divergence waiting to be found by whichever consumer was
// exercised less -- so the interpreter delegates here rather than keeping a copy.
//
// What stays above this package is the *throw*. Refusing to increment an array
// is a `TypeError`, and constructing one needs a class table, so the refusal
// comes back as a sentinel and the layer that knows about classes turns it into
// an exception.

var (
	// ErrCannotIncrement is `Cannot increment %s`, where the operand is an
	// array, object, resource or anything else with no step defined.
	ErrCannotIncrement = errors.New("php: cannot increment")
	// ErrCannotDecrement is `Cannot decrement %s`.
	ErrCannotDecrement = errors.New("php: cannot decrement")
)

// Increment applies `++`, reporting whatever diagnostic the value earns. It
// returns the new value, which for several types is the old one unchanged.
func Increment(ctx *Ctx, value Value) (Value, error) {
	// An integer short of the maximum steps by one; the maximum itself
	// overflows to a float, which Add below owns.
	if value.kind == KindInt && value.num != math.MaxInt64 {
		return Int(value.num + 1), nil
	}
	// **Through the box.** `$r = &$n; $r++` steps the shared storage, and a
	// by-reference return hands back a box, so an operand that holds one is
	// ordinary rather than un-incrementable. Refusing it read `Cannot
	// increment reference`, a message the engine has no wording for.
	value = value.Deref()
	switch value.Kind() {
	case KindUndef, KindNull:
		return Int(1), nil
	case KindTrue, KindFalse:
		ctx.Warn("Increment on type bool has no effect, this will change in the next major version of PHP")
		return value, nil
	case KindInt, KindFloat:
		return Add(value, Int(1)), nil
	case KindString:
		if numeric, ok := value.IsNumericString(); ok {
			return Add(numeric, Int(1)), nil
		}
		stepped := incrementString(ctx, value.RawString())
		return reportedStep(ctx, value, stepped)
	}
	return value, ErrCannotIncrement
}

// Decrement applies `--`.
func Decrement(ctx *Ctx, value Value) (Value, error) {
	if value.kind == KindInt && value.num != math.MinInt64 {
		return Int(value.num - 1), nil
	}
	value = value.Deref()
	switch value.Kind() {
	case KindUndef, KindNull:
		ctx.Warn("Decrement on type null has no effect, this will change in the next major version of PHP")
		return value, nil
	case KindTrue, KindFalse:
		ctx.Warn("Decrement on type bool has no effect, this will change in the next major version of PHP")
		return value, nil
	case KindInt, KindFloat:
		return Add(value, Int(-1)), nil
	case KindString:
		if numeric, ok := value.IsNumericString(); ok {
			return Add(numeric, Int(-1)), nil
		}
		if value.RawString() == "" {
			// The empty string is the one non-numeric string decrementing does
			// change, and it has its own wording.
			ctx.Deprecated("Decrement on empty string is deprecated as non-numeric")
			return reportedStep(ctx, value, Int(-1))
		}
		ctx.Deprecated("Decrement on non-numeric string has no effect and is deprecated")
		return reportedStep(ctx, value, value)
	}
	return value, ErrCannotDecrement
}

// incrementString is the Perl-style string increment: the last character is
// advanced within its own class, carrying leftwards, and a carry off the front
// prepends a new digit or letter of that class. A string with anything outside
// [0-9A-Za-z] is deprecated -- and then still incremented, unless its last
// character is what stopped the walk.
func incrementString(ctx *Ctx, text string) Value {
	if text == "" {
		ctx.Deprecated("Increment on non-alphanumeric string is deprecated")
		return String("1")
	}
	if !onlyASCIIAlphanumeric(text) {
		ctx.Deprecated("Increment on non-alphanumeric string is deprecated")
	}

	const (
		lowerCase = iota + 1
		upperCase
		numeric
	)
	s := []byte(text)
	carry, last := false, 0
	for pos := len(s) - 1; ; pos-- {
		ch := s[pos]
		switch {
		case ch >= 'a' && ch <= 'z':
			if ch == 'z' {
				s[pos], carry = 'a', true
			} else {
				s[pos], carry = ch+1, false
			}
			last = lowerCase
		case ch >= 'A' && ch <= 'Z':
			if ch == 'Z' {
				s[pos], carry = 'A', true
			} else {
				s[pos], carry = ch+1, false
			}
			last = upperCase
		case ch >= '0' && ch <= '9':
			if ch == '9' {
				s[pos], carry = '0', true
			} else {
				s[pos], carry = ch+1, false
			}
			last = numeric
		default:
			carry = false
		}
		if !carry || pos == 0 {
			break
		}
	}
	if !carry {
		return String(string(s))
	}
	switch last {
	case numeric:
		return String("1" + string(s))
	case upperCase:
		return String("A" + string(s))
	}
	return String("a" + string(s))
}

// onlyASCIIAlphanumeric matches zend_string_only_has_ascii_alphanumeric.
func onlyASCIIAlphanumeric(text string) bool {
	for i := 0; i < len(text); i++ {
		c := text[i]
		if (c < '0' || c > '9') && (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') {
			return false
		}
	}
	return true
}

// reportedStep settles what a step whose diagnostic a user error handler turned
// into a throw leaves behind: nothing. The engine reports first and checks for
// an exception afterwards, so the value it was about to write is dropped and
// the variable keeps what it had.
func reportedStep(ctx *Ctx, before, after Value) (Value, error) {
	if pending := ctx.TakePendingError(); pending != nil {
		return before, pending
	}
	return after, nil
}

// IncDecTypeName names the type in "Cannot increment %s". An object is named by
// its class, everything else by the type. It is exported because the message it
// feeds is built above this package, where exception classes live.
func IncDecTypeName(value Value) string {
	if object := value.Object(); object != nil {
		return DisplayName(object.ClassName)
	}
	name := value.Kind().String()
	if value.Kind() == KindUndef {
		name = "null"
	}
	return strings.ToLower(name)
}
