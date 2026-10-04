package stdlib

import (
	"fmt"
	"math"
	"strings"

	"github.com/moriyoshi/php2go/runtime/libc"

	"github.com/moriyoshi/php2go/runtime/php"
)

// The type predicates, the extrema, and three more that the userland library
// calls. They were entries in the interpreter's own map rather than a file of
// their own, so unlike `stringlib.go` and `arraylib.go` these came across one
// at a time -- chosen by the census, which names what the library cannot
// compile without.
//
// Nothing here needs the interpreter: a predicate asks a value its kind, and
// `max`/`min` walk their arguments through `php.Compare`.

func init() {
	// The is_* family, registered here for the **dynamic** call only:
	// `$f = 'is_int'; $f($v)`. Written directly, each of these is the
	// `type_check` intrinsic instead, because the engine compiles it to
	// ZEND_TYPE_CHECK with a mask -- the same split `count` has, and for the
	// same reason. Both entry points go through `php.TypeCheck`, so there is
	// one implementation and the mask is the only thing stated twice.
	php.RegisterBuiltin("is_int", typeCheckBuiltin(16))
	php.RegisterBuiltin("is_integer", typeCheckBuiltin(16))
	php.RegisterBuiltin("is_long", typeCheckBuiltin(16))
	php.RegisterBuiltin("is_float", typeCheckBuiltin(32))
	php.RegisterBuiltin("is_double", typeCheckBuiltin(32))
	php.RegisterBuiltin("is_string", typeCheckBuiltin(64))
	php.RegisterBuiltin("is_array", typeCheckBuiltin(128))
	php.RegisterBuiltin("is_null", typeCheckBuiltin(2))
	php.RegisterBuiltin("is_object", typeCheckBuiltin(256))
	php.RegisterBuiltin("get_class", func(_ *php.Ctx, a []php.Value) (php.Value, error) {
		if v := FirstArg(a); v.Kind() == php.KindObject {
			return php.String(v.Object().ClassName), nil
		}
		return php.Bool(false), nil
	})
	php.RegisterBuiltin("is_bool", typeCheckBuiltin(12))
	// is_numeric is **not** part of the type-check family: it asks whether a
	// string would read as a number, which no type mask can answer. It derefs
	// because FirstArg deliberately does not, and a dynamic call --
	// `$f = 'is_numeric'; $f($v)` -- hands the argument over as a reference.
	// The rest of the family never noticed, because php.TypeCheck derefs
	// inside.
	php.RegisterBuiltin("is_numeric", func(_ *php.Ctx, a []php.Value) (php.Value, error) {
		v := FirstArg(a).Deref()
		if v.Kind() == php.KindInt || v.Kind() == php.KindFloat {
			return php.Bool(true), nil
		}
		_, ok := v.IsNumericString()
		return php.Bool(ok), nil
	})

	php.RegisterBuiltin("max", extremum(1))
	php.RegisterBuiltin("min", extremum(-1))
	php.RegisterBuiltin(`php2go\internal\max_frameless`, extremumFrameless(1))
	php.RegisterBuiltin(`php2go\internal\min_frameless`, extremumFrameless(-1))
	php.RegisterBuiltin("explode", func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		if len(args) < 2 {
			return php.Undef(), fmt.Errorf("explode() expects at least 2 arguments")
		}
		separator := args[0].String()
		if separator == "" {
			return php.Undef(), php.Raise("ValueError",
				"explode(): Argument #1 ($separator) must not be empty")
		}
		limit := math.MaxInt32
		if len(args) > 2 {
			limit = int(args[2].Int())
		}
		parts := strings.Split(args[1].String(), separator)
		// A negative limit drops that many trailing pieces instead of capping.
		switch {
		case limit > 0 && len(parts) > limit:
			parts = append(parts[:limit-1], strings.Join(parts[limit-1:], separator))
		case limit == 0:
			parts = []string{strings.Join(parts, separator)}
		case limit < 0:
			if drop := -limit; drop >= len(parts) {
				parts = nil
			} else {
				parts = parts[:len(parts)-drop]
			}
		}
		result := php.NewArray()
		for _, part := range parts {
			result.Append(php.String(part))
		}
		return php.Arr(result), nil
	})
	php.RegisterBuiltin("array_pop", func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		target, ok := SortTarget(args)
		if !ok {
			return php.Null(), nil
		}
		_, value, found := target.PopLast()
		if !found {
			return php.Null(), nil
		}
		return value, nil
	})
	php.RegisterBuiltin("strcoll", func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		if len(args) < 2 {
			return php.Undef(), fmt.Errorf("strcoll requires exactly two strings")
		}
		result, available := libc.Strcoll(
			args[0].Deref().String(),
			args[1].Deref().String(),
		)
		if !available {
			return php.Undef(), php.Unsupported("libc strcoll without cgo")
		}
		return php.Int(int64(result)), nil
	})
}

// typeCheckBuiltin builds one is_* entry over the type mask the engine's
// ZEND_TYPE_CHECK uses, so the dynamic call and the opcode answer through the
// same function. The masks are `1 << <the engine's type id>` and are documented
// on php.TypeCheck, which measured them.
func typeCheckBuiltin(mask int64) php.Builtin {
	return func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		return php.Bool(php.TypeCheck(FirstArg(args), mask)), nil
	}
}

// extremum implements max (direction 1) and min (direction -1) as
// PHP_FUNCTION(min) and (max) do, the form a call with other than two
// arguments -- or a dynamic one -- reaches:
//
//   - one argument is an array to search, zend_hash_minmax over zend_compare:
//     the running answer is replaced when it compares greater (min) or less
//     (max) than the element, so an element uncomparable with it replaces it
//     for min and not for max;
//   - several are walked in order, ints and floats on their own paths, and
//     anything else through zend_compare, where an argument replaces the
//     answer when it compares less (min) or greater (max). A tie keeps the
//     earlier argument.
//
// It compared with `Compare` and took one array argument for the answer, so
// `min([3, 1, 2])` gave the array back.
func extremum(direction int) php.Builtin {
	name := "min"
	if direction > 0 {
		name = "max"
	}
	return func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		if len(args) == 0 {
			return php.Undef(), php.Raise("ArgumentCountError", "%s() expects at least 1 argument, 0 given", name)
		}
		if len(args) == 1 {
			list := args[0].Deref()
			if list.Kind() != php.KindArray {
				return php.Undef(), php.Raise("TypeError",
					"%s(): Argument #1 ($value) must be of type array, %s given", name, php.ValueName(list))
			}
			var best php.Value
			found := false
			var failed error
			list.Array().Each(func(_ php.Key, v php.Value) bool {
				if !found {
					best, found = v, true
					return true
				}
				n, err := php.CompareValues(ctx, best, v)
				if err != nil {
					failed = err
					return false
				}
				if n*direction < 0 {
					best = v
				}
				return true
			})
			if failed != nil {
				return php.Undef(), failed
			}
			if !found {
				return php.Undef(), php.Raise("ValueError",
					"%s(): Argument #1 ($value) must contain at least one element", name)
			}
			return best, nil
		}
		best := args[0].Deref()
		// The numeric paths compare in their own type and fall through to
		// zend_compare at the first argument they cannot take. The engine
		// also leaves them for an int a double cannot hold exactly; that
		// changes no answer, since zend_compare converts the int to a double
		// just the same.
		mode := best.Kind()
		var bestF float64
		if mode == php.KindFloat {
			bestF = best.Float()
		}
		for i := 1; i < len(args); i++ {
			arg := args[i].Deref()
			switch {
			case mode == php.KindInt && arg.Kind() == php.KindInt:
				if (direction < 0 && best.Int() > arg.Int()) || (direction > 0 && best.Int() < arg.Int()) {
					best = arg
				}
				continue
			case mode == php.KindInt && arg.Kind() == php.KindFloat:
				mode, bestF = php.KindFloat, float64(best.Int())
			case mode == php.KindInt:
				mode = php.KindNull
			}
			switch {
			case mode == php.KindFloat && arg.Kind() == php.KindFloat:
				if (direction < 0 && bestF > arg.Float()) || (direction > 0 && bestF < arg.Float()) {
					best, bestF = arg, arg.Float()
				}
				continue
			case mode == php.KindFloat && arg.Kind() == php.KindInt:
				if f := float64(arg.Int()); (direction < 0 && bestF > f) || (direction > 0 && bestF < f) {
					best, bestF = arg, f
				}
				continue
			case mode == php.KindFloat:
				mode = php.KindNull
			}
			mode = php.KindNull
			n, err := php.CompareValues(ctx, arg, best)
			if err != nil {
				return php.Undef(), err
			}
			if n*direction > 0 {
				best = arg
			}
		}
		return best, nil
	}
}

// extremumFrameless is the two-argument call the compiler makes frameless
// (ZEND_FRAMELESS_ICALL_2), which is a different function in the engine with
// its own ties: min answers the left only when it is strictly smaller, max
// whenever it is not smaller -- so `min(5, "5")` is "5" and `max(5, "5")` is
// 5, and of two arrays neither of which is comparable with the other min
// answers the right and max the left. Probed against both forms.
func extremumFrameless(direction int) php.Builtin {
	return func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		if len(args) != 2 {
			return extremum(direction)(ctx, args)
		}
		lhs, rhs := args[0].Deref(), args[1].Deref()
		pick := func(lhsWins bool) (php.Value, error) {
			if lhsWins {
				return lhs, nil
			}
			return rhs, nil
		}
		floats := func(l, r float64) (php.Value, error) {
			if direction < 0 {
				return pick(l < r)
			}
			return pick(l >= r)
		}
		switch {
		case lhs.Kind() == php.KindInt && rhs.Kind() == php.KindInt:
			if direction < 0 {
				return pick(lhs.Int() < rhs.Int())
			}
			return pick(lhs.Int() >= rhs.Int())
		case lhs.Kind() == php.KindInt && rhs.Kind() == php.KindFloat:
			return floats(float64(lhs.Int()), rhs.Float())
		case lhs.Kind() == php.KindFloat && rhs.Kind() == php.KindFloat:
			return floats(lhs.Float(), rhs.Float())
		case lhs.Kind() == php.KindFloat && rhs.Kind() == php.KindInt:
			return floats(lhs.Float(), float64(rhs.Int()))
		}
		n, err := php.CompareValues(ctx, lhs, rhs)
		if err != nil {
			return php.Undef(), err
		}
		if direction < 0 {
			return pick(n < 0)
		}
		return pick(n >= 0)
	}
}
