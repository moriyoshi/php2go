package stdlib

import (
	"math"
	"strings"

	"github.com/moriyoshi/php2go/runtime/php"
)

// The scalar conversions and the internal array pointer, moved here from
// `runtime/interp` so compiled code can reach them. See `stringfns.go` for why
// the library surface came ahead of every semantic gap.
//
// The pointer family takes its array **by reference** -- `byrefargs.go` in this
// package already declared that, which is what made the move possible without
// touching the call protocol on either side.

func init() {
	php.RegisterBuiltin("chr", func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		if len(args) == 0 {
			return php.Null(), nil
		}
		// The argument is taken modulo 256, and negatives wrap rather than
		// erroring, so chr(-1) is "\xff".
		code := args[0].Deref().Int() % 256
		if code < 0 {
			code += 256
		}
		return php.String(string([]byte{byte(code)})), nil
	})
	php.RegisterBuiltin("ord", func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		if len(args) == 0 {
			return php.Int(0), nil
		}
		s := args[0].Deref().String()
		if s == "" {
			return php.Int(0), nil
		}
		// The first byte, not the first character: PHP strings are byte strings.
		return php.Int(int64(s[0])), nil
	})
	php.RegisterBuiltin("range", rangeBuiltin)

	// is_scalar is a type predicate, not arithmetic; it was filed with the
	// maths only because `mathlib.go` was where the odds and ends went. Like
	// the rest of the family it is the `type_check` intrinsic when written
	// directly, and this entry is the dynamic call: 124 is the union of false,
	// true, int, float and string.
	php.RegisterBuiltin("is_scalar", typeCheckBuiltin(124))
	php.RegisterBuiltin("get_debug_type", func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		if len(args) == 0 {
			return php.String("null"), nil
		}
		value := args[0].Deref()
		// Unlike gettype this uses the names the language itself uses, and
		// names an object's class rather than saying "object".
		if object := value.Object(); object != nil {
			return php.String(object.ClassName), nil
		}
		if r := value.Resource(); r != nil {
			return php.String(r.DebugType()), nil
		}
		if value.Kind() == php.KindUndef {
			return php.String("null"), nil
		}
		return php.String(value.Kind().String()), nil
	})
	// The scalar readers. They are pure over a value and were in the
	// interpreter only because that is where they were written; `settype` is
	// the one that stays, because it writes back through a reference the
	// caller owns.
	php.RegisterBuiltin("strlen", func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		if len(args) == 0 {
			return php.Null(), nil
		}
		// Byte length, not rune count: PHP strings are byte strings.
		return php.Int(int64(len(args[0].String()))), nil
	})
	php.RegisterBuiltin("gettype", func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		if len(args) == 0 {
			return php.String("NULL"), nil
		}
		return php.String(gettypeName(args[0])), nil
	})
	php.RegisterBuiltin("intval", func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		v := php.Arg(args, 0).Deref()
		base := int64(10)
		if len(args) > 1 {
			base = args[1].Deref().Int()
		}
		// **The base applies to a string and to nothing else.** `intval(42.9, 8)`
		// is 42, and base 10 is not "strtol with base 10" either -- it is the
		// ordinary numeric-string conversion, which is why `intval("1e3", 10)`
		// is 1000 where `intval("1e3", 0)` is 1.
		if v.Kind() != php.KindString || base == 10 {
			return php.Int(v.Int()), nil
		}
		return php.Int(strtol(v.String(), int(base))), nil
	})
	php.RegisterBuiltin("floatval", func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		return php.Float(php.Arg(args, 0).Deref().Float()), nil
	})
	php.RegisterBuiltin("strval", func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		return php.String(php.Arg(args, 0).Deref().String()), nil
	})
	php.RegisterBuiltin("boolval", func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		return php.Bool(php.Arg(args, 0).Deref().Bool()), nil
	})
}

// gettypeName is `gettype`'s vocabulary, which is **not** `get_debug_type`'s:
// `integer` and `double` where the modern one says `int` and `float`, and a
// closed handle that says `resource (closed)` rather than still calling itself
// a resource.
func gettypeName(v php.Value) string {
	switch v.Deref().Kind() {
	case php.KindUndef, php.KindNull:
		return "NULL"
	case php.KindTrue, php.KindFalse:
		return "boolean"
	case php.KindInt:
		return "integer"
	case php.KindFloat:
		return "double"
	case php.KindString:
		return "string"
	case php.KindArray:
		return "array"
	case php.KindObject:
		return "object"
	case php.KindResource:
		if v.Deref().Resource().Closed() {
			return "resource (closed)"
		}
		return "resource"
	}
	return "unknown type"
}

func rangeBuiltin(_ *php.Ctx, args []php.Value) (php.Value, error) {
	if len(args) < 2 {
		return php.Null(), nil
	}
	start, end := args[0].Deref(), args[1].Deref()

	step := 1.0
	if len(args) > 2 {
		step = args[2].Deref().Float()
		if step < 0 {
			step = -step
		}
		if step == 0 {
			return php.Undef(), php.Raise("ValueError",
				"range(): Argument #3 ($step) cannot be 0")
		}
	}

	result := php.NewArray()
	if isCharBound(start) && isCharBound(end) {
		from, to := int(start.String()[0]), int(end.String()[0])
		by := int(step)
		if by == 0 {
			by = 1
		}
		if from <= to {
			for c := from; c <= to; c += by {
				result.Append(php.String(string([]byte{byte(c)})))
			}
		} else {
			for c := from; c >= to; c -= by {
				result.Append(php.String(string([]byte{byte(c)})))
			}
		}
		return php.Arr(result), nil
	}

	// A fractional bound or step makes the whole sequence floats, which is
	// what keeps range(0, 1, 0.25) from collapsing to integers.
	if start.Kind() == php.KindFloat || end.Kind() == php.KindFloat || step != float64(int64(step)) {
		from, to := start.Float(), end.Float()
		if from <= to {
			for v := from; v <= to+1e-9; v += step {
				result.Append(php.Float(v))
			}
		} else {
			for v := from; v >= to-1e-9; v -= step {
				result.Append(php.Float(v))
			}
		}
		return php.Arr(result), nil
	}

	from, to, by := start.Int(), end.Int(), int64(step)
	if from <= to {
		for v := from; v <= to; v += by {
			result.Append(php.Int(v))
		}
	} else {
		for v := from; v >= to; v -= by {
			result.Append(php.Int(v))
		}
	}
	return php.Arr(result), nil
}

// isCharBound reports whether a bound asks for a character range: a one-byte
// string that is not simply a number written out.
func isCharBound(v php.Value) bool {
	if v.Kind() != php.KindString || len(v.String()) != 1 {
		return false
	}
	return strings.IndexByte("0123456789", v.String()[0]) < 0
}

// The internal array pointer. Each of these takes the array by reference, so
// the pointer they move is the caller's own.
func init() {
	pointed := func(a *php.Array) (php.Key, php.Value, bool) { return a.Pointed() }
	php.RegisterBuiltin("current", pointerBuiltin("current", pointed, false))
	php.RegisterBuiltin("pos", pointerBuiltin("pos", pointed, false))
	php.RegisterBuiltin("key", pointerBuiltin("key", pointed, true))
	php.RegisterBuiltin("next", pointerBuiltin("next", func(a *php.Array) (php.Key, php.Value, bool) {
		return a.MovePointer(1)
	}, false))
	php.RegisterBuiltin("prev", pointerBuiltin("prev", func(a *php.Array) (php.Key, php.Value, bool) {
		return a.MovePointer(-1)
	}, false))
	php.RegisterBuiltin("reset", pointerBuiltin("reset", func(a *php.Array) (php.Key, php.Value, bool) {
		return a.RewindPointer()
	}, false))
	php.RegisterBuiltin("end", pointerBuiltin("end", func(a *php.Array) (php.Key, php.Value, bool) {
		return a.EndPointer()
	}, false))
}

// pointerBuiltin builds one of the internal-pointer functions. They all answer
// false when the pointer is past either end -- except key(), which answers null,
// since false is a perfectly good key.
func pointerBuiltin(name string, move func(*php.Array) (php.Key, php.Value, bool), wantKey bool) php.Builtin {
	return func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		if len(args) == 0 {
			return php.Bool(false), nil
		}
		subject := args[0].Deref()
		table := subject.Array()
		if object := subject.Object(); object != nil {
			// An object has a property table with a pointer of its own, and
			// these functions walk it. PHP still does so, and says it is on the
			// way out -- the message names the function twice, once as the
			// diagnostic's owner and once inside it.
			ctx.Deprecated("%s(): Calling %s() on an object is deprecated", name, name)
			// The internal pointer lives on the table, so the object's
			// properties have to be one.
			table = object.Table()
		}
		if table == nil {
			return php.Bool(false), nil
		}
		key, value, ok := move(table)
		switch {
		case !ok && wantKey:
			return php.Null(), nil
		case !ok:
			return php.Bool(false), nil
		case wantKey:
			return key.Value(), nil
		}
		return value, nil
	}
}

// strtol is `intval`'s base conversion: C's strtol as the linked build provides
// it, plus the one thing PHP does on top of it.
//
// Every rule here was read back from the engine rather than reasoned out, and
// two of them are surprising:
//
//   - **An invalid base is not an error.** `intval("10", 1)` and
//     `intval("10", 37)` are 0, not a ValueError.
//   - **At base 0 or 2, PHP strips a leading `0b` itself and then converts at
//     base 2 regardless.** That is why `intval("0B11", 0)` is 3 and not 11:
//     the strip happens, and what follows is read as binary even though the
//     caller asked for auto-detection. `intval("0b0b1", 2)` is 1 for the same
//     reason -- PHP removes one prefix and the conversion removes the other.
//
// The conversion itself accepts a `0x` prefix at base 16 and 0, a `0b` prefix
// at base 2 and 0, and a leading `0` as octal at base 0; it stops at the first
// character that is not a digit in the base, and saturates at PHP_INT_MAX and
// PHP_INT_MIN rather than wrapping.
func strtol(text string, base int) int64 {
	trimmed := strings.TrimLeft(text, " \t\n\v\f\r")
	// PHP's own step, before the conversion: at base 0 or 2 a `0b` prefix is
	// removed and the rest is read as **binary**.
	if base == 0 || base == 2 {
		if len(trimmed) > 2 {
			at := 0
			if trimmed[0] == '-' || trimmed[0] == '+' {
				at = 1
			}
			if len(trimmed) > at+1 && trimmed[at] == '0' &&
				(trimmed[at+1] == 'b' || trimmed[at+1] == 'B') {
				return strtolDigits(trimmed[:at]+trimmed[at+2:], 2)
			}
		}
	}
	return strtolDigits(trimmed, base)
}

func strtolDigits(text string, base int) int64 {
	if base != 0 && (base < 2 || base > 36) {
		return 0
	}
	negative := false
	if len(text) > 0 && (text[0] == '-' || text[0] == '+') {
		negative = text[0] == '-'
		text = text[1:]
	}
	// A prefix is only a prefix when a digit of the base follows it; otherwise
	// the leading `0` is the whole number and the letter ends it.
	switch {
	case (base == 16 || base == 0) && len(text) > 2 && text[0] == '0' &&
		(text[1] == 'x' || text[1] == 'X') && digitValue(text[2]) < 16:
		base, text = 16, text[2:]
	case (base == 2 || base == 0) && len(text) > 2 && text[0] == '0' &&
		(text[1] == 'b' || text[1] == 'B') && digitValue(text[2]) < 2:
		base, text = 2, text[2:]
	case base == 0 && len(text) > 0 && text[0] == '0':
		base = 8
	case base == 0:
		base = 10
	}
	var out int64
	saturated := false
	seen := false
	for i := 0; i < len(text); i++ {
		d := digitValue(text[i])
		if d >= base {
			break
		}
		seen = true
		if saturated {
			continue
		}
		next := out*int64(base) + int64(d)
		if out > (math.MaxInt64-int64(d))/int64(base) {
			saturated = true
			continue
		}
		out = next
	}
	if !seen {
		return 0
	}
	if saturated {
		if negative {
			return math.MinInt64
		}
		return math.MaxInt64
	}
	if negative {
		return -out
	}
	return out
}

// digitValue is a digit's value in base 36, or 36 for anything that is not one.
func digitValue(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'z':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'Z':
		return int(c-'A') + 10
	}
	return 36
}
