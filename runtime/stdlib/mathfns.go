package stdlib

import (
	"fmt"
	"math"
	"math/rand"
	"strconv"

	"github.com/moriyoshi/php2go/runtime/php"
)

// The maths. Each is a pure function of its arguments, so none of them needs
// the interpreter -- which is why they moved out of it unchanged.
//
// They arrived in one file with the path helpers and the process odds and ends,
// because `runtime/interp/mathlib.go` had always held all three: its own doc
// said "the maths, the path helpers and the odds and ends that answer about the
// process". Moving that file wholesale carried the grouping across and put a
// name on it -- `mathfns.go` -- that claimed it was about maths. It is split
// now: `pathfns.go` and `processfns.go` hold the other two. Compiled code had no route to any of it; see
// `stringfns.go` for the measurement that put the library surface ahead of every
// semantic gap.
//
// `first` and `second` are the two argument readers the interpreter's own tables
// use 368 times between them, so they are exported here as `FirstArg` and
// `SecondArg` rather than copied, and the interpreter's names became one-line
// wrappers over them.

func init() {
	for name, fn := range map[string]func(float64) float64{
		"sin": math.Sin, "cos": math.Cos, "tan": math.Tan,
		"asin": math.Asin, "acos": math.Acos, "atan": math.Atan,
		"sinh": math.Sinh, "cosh": math.Cosh, "tanh": math.Tanh,
		"asinh": math.Asinh, "acosh": math.Acosh, "atanh": math.Atanh,
		"exp": math.Exp, "expm1": math.Expm1, "log1p": math.Log1p,
		"log10": math.Log10, "sqrt": math.Sqrt,
		"deg2rad": func(d float64) float64 { return d * math.Pi / 180 },
		"rad2deg": func(r float64) float64 { return r * 180 / math.Pi },
	} {
		php.RegisterBuiltin(name, unaryFloat(fn))
	}
	php.RegisterBuiltin("abs", func(_ *php.Ctx, a []php.Value) (php.Value, error) {
		v := FirstArg(a)
		if v.Kind() == php.KindFloat {
			// `math.Abs`, not `if f < 0 { f = -f }`: the comparison is false
			// for **negative zero**, so `abs(-0.0)` kept its sign and printed
			// `float(-0)` where the engine prints `float(0)`. Measured; it is
			// the only input where the two spellings differ.
			return php.Float(math.Abs(v.Float())), nil
		}
		i := v.Int()
		if i < 0 {
			i = -i
		}
		return php.Int(i), nil
	})
	php.RegisterBuiltin("intdiv", func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		if len(args) < 2 {
			return php.Undef(), fmt.Errorf("intdiv() expects 2 arguments")
		}
		divisor := args[1].Int()
		if divisor == 0 {
			return php.Undef(), php.Raise("DivisionByZeroError", "Division by zero")
		}
		// PHP_INT_MIN / -1 overflows, which PHP raises rather than wrapping.
		if args[0].Int() == math.MinInt64 && divisor == -1 {
			return php.Undef(), php.Raise("ArithmeticError",
				"Division of PHP_INT_MIN by -1 is not an integer")
		}
		return php.Int(args[0].Int() / divisor), nil
	})
	php.RegisterBuiltin("pi", func(_ *php.Ctx, _ []php.Value) (php.Value, error) {
		return php.Float(math.Pi), nil
	})
	php.RegisterBuiltin("atan2", func(_ *php.Ctx, a []php.Value) (php.Value, error) {
		return php.Float(math.Atan2(FirstArg(a).Float(), SecondArg(a).Float())), nil
	})
	php.RegisterBuiltin("hypot", func(_ *php.Ctx, a []php.Value) (php.Value, error) {
		return php.Float(math.Hypot(FirstArg(a).Float(), SecondArg(a).Float())), nil
	})
	php.RegisterBuiltin("fmod", func(_ *php.Ctx, a []php.Value) (php.Value, error) {
		return php.Float(math.Mod(FirstArg(a).Float(), SecondArg(a).Float())), nil
	})
	php.RegisterBuiltin("fdiv", func(_ *php.Ctx, a []php.Value) (php.Value, error) {
		// The whole point of fdiv is that it does not raise on a zero divisor.
		return php.Float(FirstArg(a).Float() / SecondArg(a).Float()), nil
	})
	php.RegisterBuiltin("log", func(_ *php.Ctx, a []php.Value) (php.Value, error) {
		if len(a) > 1 {
			return php.Float(math.Log(FirstArg(a).Float()) / math.Log(a[1].Float())), nil
		}
		return php.Float(math.Log(FirstArg(a).Float())), nil
	})
	php.RegisterBuiltin("is_nan", func(_ *php.Ctx, a []php.Value) (php.Value, error) {
		return php.Bool(math.IsNaN(FirstArg(a).Float())), nil
	})
	php.RegisterBuiltin("is_infinite", func(_ *php.Ctx, a []php.Value) (php.Value, error) {
		return php.Bool(math.IsInf(FirstArg(a).Float(), 0)), nil
	})
	php.RegisterBuiltin("is_finite", func(_ *php.Ctx, a []php.Value) (php.Value, error) {
		f := FirstArg(a).Float()
		return php.Bool(!math.IsInf(f, 0) && !math.IsNaN(f)), nil
	})
	// ceil and floor answer a float even for an integer argument, which is what
	// distinguishes them from an int cast.
	php.RegisterBuiltin("ceil", unaryFloat(math.Ceil))
	php.RegisterBuiltin("floor", unaryFloat(math.Floor))
	php.RegisterBuiltin("round", roundBuiltin)
	php.RegisterBuiltin("pow", func(_ *php.Ctx, a []php.Value) (php.Value, error) {
		// An integer base and a non-negative integer exponent stay integral
		// unless the result overflows, which is the `**` operator's rule too.
		base, exponent := FirstArg(a), SecondArg(a)
		if base.Kind() == php.KindInt && exponent.Kind() == php.KindInt && exponent.Int() >= 0 {
			result := math.Pow(float64(base.Int()), float64(exponent.Int()))
			if result <= math.MaxInt64 && result >= math.MinInt64 {
				return php.Int(int64(result)), nil
			}
			return php.Float(result), nil
		}
		return php.Float(math.Pow(base.Float(), exponent.Float())), nil
	})

	php.RegisterBuiltin("mt_getrandmax", maxRandom)
	php.RegisterBuiltin("getrandmax", maxRandom)
	// The request's Mt19937 engine, seeded and drawn as the engine does --
	// shuffle and array_rand reach it through the library's two leaves.
	for _, name := range []string{"srand", "mt_srand", "rand", "mt_rand",
		`php2go\internal\mt_range`, `php2go\internal\array_random`} {
		name := name
		php.RegisterBuiltin(name, func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
			v, _, err := ctx.RandomCall(name, args)
			return v, err
		})
	}
	php.RegisterBuiltin("random_int", func(_ *php.Ctx, a []php.Value) (php.Value, error) {
		low, high := FirstArg(a).Int(), SecondArg(a).Int()
		if low > high {
			return php.Undef(), php.Raise("ValueError",
				"random_int(): Argument #1 ($min) must be less than or equal to argument #2 ($max)")
		}
		return php.Int(low + rand.Int63n(high-low+1)), nil
	})
	php.RegisterBuiltin("lcg_value", func(_ *php.Ctx, _ []php.Value) (php.Value, error) {
		return php.Float(rand.Float64()), nil
	})

}

// FirstArg is the first argument, or null when there is none. It does **not**
// deref, while SecondArg does. That asymmetry is not tidiness waiting to happen:
// the first argument of a by-reference library function arrives as a Ref and the
// callee needs the box, and the tables were written against it. Preserved as it
// was rather than unified, because the difference is load-bearing at 368 call
// sites.
func FirstArg(args []php.Value) php.Value {
	if len(args) == 0 {
		return php.Null()
	}
	return args[0]
}

// SecondArg is the second argument, dereferenced, or null when there is none.
func SecondArg(args []php.Value) php.Value {
	if len(args) > 1 {
		return args[1].Deref()
	}
	return php.Null()
}

func unaryFloat(fn func(float64) float64) php.Builtin {
	return func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		return php.Float(fn(FirstArg(args).Float())), nil
	}
}

func maxRandom(_ *php.Ctx, _ []php.Value) (php.Value, error) {
	return php.Int(2147483647), nil
}

// roundBuiltin rounds half away from zero at the given precision, and keeps an
// integer argument an integer only when no precision was asked for.
func roundBuiltin(_ *php.Ctx, args []php.Value) (php.Value, error) {
	value := FirstArg(args)
	precision := 0
	if len(args) > 1 {
		precision = int(args[1].Int())
	}
	// The mode: PHP_ROUND_HALF_UP (1, the default), _DOWN (2), _EVEN (3) or
	// _ODD (4) -- how a value exactly halfway is settled. PHP 8.4 also takes
	// a RoundingMode enum case, which is not modelled.
	mode := int64(1)
	if len(args) > 2 {
		m := args[2].Deref()
		if m.Kind() == php.KindObject {
			return php.Undef(), php.Unsupported("round() with a RoundingMode case")
		}
		mode = m.Int()
		if mode < 1 || mode > 4 {
			return php.Undef(), php.Raise("ValueError",
				"round(): Argument #3 ($mode) must be a valid rounding mode (RoundingMode::*)")
		}
	}
	if precision == 0 && value.Kind() == php.KindInt {
		return php.Float(float64(value.Int())), nil
	}
	scale := math.Pow(10, float64(precision))
	scaled := value.Float() * scale
	// Undo the representation error the scaling introduces before rounding:
	// 1.955 * 100 is 195.49999999999997, and PHP rounds it to 196.
	if rounded, err := parseExactFloat(scaled); err == nil {
		scaled = rounded
	}
	return php.Float(roundHalf(scaled, mode) / scale), nil
}

// roundHalf rounds to an integer, settling a value exactly halfway by mode:
// away from zero, towards it, to the even neighbour, or to the odd one.
func roundHalf(x float64, mode int64) float64 {
	t := math.Trunc(x)
	if math.Abs(x-t) != 0.5 {
		return math.Round(x)
	}
	away := t + math.Copysign(1, x)
	switch mode {
	case 2:
		return t
	case 3:
		if math.Mod(t, 2) == 0 {
			return t
		}
		return away
	case 4:
		if math.Mod(t, 2) != 0 {
			return t
		}
		return away
	}
	return away
}

// parseExactFloat re-reads a float through its shortest decimal form, which
// removes the error a multiplication by a power of ten introduces.
func parseExactFloat(f float64) (float64, error) {
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return f, nil
	}
	return strconv.ParseFloat(strconv.FormatFloat(f, 'g', 15, 64), 64)
}
