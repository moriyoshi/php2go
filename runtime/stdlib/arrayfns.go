package stdlib

import (
	"strings"

	"github.com/moriyoshi/php2go/runtime/php"
)

// The array functions that are pure operations on values, moved here from
// `runtime/interp` so compiled code can reach them. See `stringfns.go` for the
// measurement that put the library surface ahead of every semantic gap.
//
// The sort family and `array_splice` take their array **by reference** and
// rewrite it in place, which is the whole reason `sortTarget` exists: the
// argument arrives as a box and the array behind it is what must change.
// `byrefargs.go` in this package already declared those positions.

func init() {
	php.RegisterBuiltin("array_fill", func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		if len(args) < 3 {
			return php.Null(), nil
		}
		start, count := args[0].Deref().Int(), args[1].Deref().Int()
		if count < 0 {
			return php.Undef(), php.Raise("ValueError",
				"array_fill(): Argument #2 ($count) must be greater than or equal to 0")
		}
		if err := Affordable(count); err != nil {
			return php.Undef(), err
		}
		result := php.NewArray()
		for i := int64(0); i < count; i++ {
			result.Set(php.IntKey(start+i), args[2].Deref().Copy())
		}
		return php.Arr(result), nil
	})
	php.RegisterBuiltin("array_fill_keys", func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		if len(args) < 2 || args[0].Deref().Kind() != php.KindArray {
			return php.Null(), nil
		}
		result := php.NewArray()
		args[0].Deref().Array().Each(func(_ php.Key, key php.Value) bool {
			result.Set(php.ArrayKey(ctx, key), args[1].Deref().Copy())
			return true
		})
		return php.Arr(result), nil
	})
	// array_slice keeps string keys always and integer keys only when asked;
	// a negative offset or length counts from the end.
	php.RegisterBuiltin("array_slice", func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		if len(args) < 2 || args[0].Deref().Kind() != php.KindArray {
			return php.Null(), nil
		}
		source := args[0].Deref().Array()
		keys := source.Keys()
		start, count := sliceBounds(len(keys), args, 1)
		preserve := len(args) > 3 && args[3].Deref().Bool()
		result := php.NewArray()
		for _, key := range keys[start : start+count] {
			value, _ := source.Get(key)
			if key.IsInt && !preserve {
				result.Append(value.Deref().Copy())
				continue
			}
			result.Set(key, value.Deref().Copy())
		}
		return php.Arr(result), nil
	})
	// array_splice removes a run and returns it, putting the replacement in its
	// place. Integer keys are renumbered throughout, as they are for a shift.
	php.RegisterBuiltin("array_splice", func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		target, ok := SortTarget(args)
		if !ok {
			return php.Null(), nil
		}
		start, count := sliceBounds(target.Len(), args, 1)
		var replacement []php.Value
		if len(args) > 3 {
			if list := args[3].Deref(); list.Kind() == php.KindArray {
				list.Array().Each(func(_ php.Key, v php.Value) bool {
					replacement = append(replacement, v.Deref().Copy())
					return true
				})
			} else if list.Kind() != php.KindNull {
				// **Converted as `(array)` converts it**: a scalar is one
				// element, but an object hands over its property values --
				// protected and private included -- and a lazy one is not
				// initialised for it (probed; lazy_objects/convert_to_array).
				php.ToArray(list).Array().Each(func(_ php.Key, v php.Value) bool {
					replacement = append(replacement, v.Deref().Copy())
					return true
				})
			}
		}
		// Whether the caller wanted the removed elements is not a formality:
		// the engine counts them into the position it rebuilds with, so a live
		// foreach over the array ends up somewhere else depending on it.
		return php.Arr(target.Splice(start, count, replacement, ctx.ResultUsed())), nil
	})
	// array_merge appends integer-keyed values under fresh keys and lets a
	// later string key overwrite an earlier one.
	php.RegisterBuiltin("array_merge", func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		result := php.NewArray()
		for i, arg := range args {
			if list := arg.Deref(); list.Kind() != php.KindArray {
				return php.Undef(), php.Raise("TypeError",
					"array_merge(): Argument #%d must be of type array, %s given",
					i+1, php.KindName(list))
			}
		}
		// **Two arrays, one of them empty: the other comes back as itself**
		// when it is a list or has string keys only -- the engine's
		// ZVAL_COPY, so the internal pointer travels and a by-reference
		// foreach over `$a = array_merge($a, [])` goes on where it was.
		if len(args) == 2 {
			var ret *php.Array
			if first := args[0].Deref().Array(); first.Len() == 0 {
				ret = args[1].Deref().Array()
			} else if second := args[1].Deref().Array(); second.Len() == 0 {
				ret = first
			}
			if ret != nil && ret.ReturnedAsIs(true, false) {
				return php.Arr(ret.Share()), nil
			}
		}
		for _, arg := range args {
			list := arg.Deref()
			list.Array().EachRaw(func(k php.Key, raw php.Value) bool {
				if k.IsInt {
					result.Append(php.CopyElement(raw))
					return true
				}
				result.Set(k, php.CopyElement(raw))
				return true
			})
		}
		return php.Arr(result), nil
	})
	php.RegisterBuiltin("array_key_exists", func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		if len(args) < 2 || args[1].Kind() != php.KindArray {
			return php.Bool(false), nil
		}
		_, exists := args[1].Array().Get(php.ArrayKey(ctx, args[0]))
		return php.Bool(exists), nil
	})

	php.RegisterBuiltin("sort", sortBuiltin(false, false))
	php.RegisterBuiltin("rsort", sortBuiltin(false, true))
	php.RegisterBuiltin("ksort", keySortBuiltin(false))
	php.RegisterBuiltin("krsort", keySortBuiltin(true))
	php.RegisterBuiltin("asort", assocSortBuiltin(false))
	php.RegisterBuiltin("arsort", assocSortBuiltin(true))
}

func sortBuiltin(_ bool, reverse bool) php.Builtin {
	return func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		target, ok := SortTarget(args)
		if !ok {
			return php.Bool(false), nil
		}
		guard := guardSort(target)
		values := make([]php.Value, 0, target.Len())
		target.Each(func(_ php.Key, v php.Value) bool {
			values = append(values, v)
			return true
		})
		var failed error
		compare := flaggedCompare(ctx, args, 1)
		order := make([]int, len(values))
		for i := range order {
			order[i] = i
		}
		// In the engine's call order (zendSort), which a `__toString` the
		// comparison runs can see, and stable by original position as
		// every sort has been since PHP 8.0.
		_ = zendSort(len(values), func(i, j int) (int, error) {
			return stableOrder(compare(&failed, values[i], values[j]), reverse, order[i], order[j]), nil
		}, func(i, j int) {
			values[i], values[j] = values[j], values[i]
			order[i], order[j] = order[j], order[i]
		})
		if guard.written(args, target) {
			return php.Bool(true), failed
		}
		ReplaceList(target, values)
		return php.Bool(true), failed
	}
}

// sortCompare compares two elements for a sort the way the engine's compare
// handler does -- an object against a string through its `__toString`, which
// is user code (gh16648). The first error is kept, and the comparison it
// interrupted answers as zend_std_compare_objects does when the cast fails:
// the object is the greater side, whichever operand it is. Once an exception
// is pending no further `__toString` runs, so later comparisons of an object
// with a string answer the same way (probed: a throwing object sorts last and
// `__toString` runs once). The sort carries on, its result stands, and the
// error is thrown after.
func sortCompare(ctx *php.Ctx, failed *error, a, b php.Value) int {
	if *failed != nil {
		return failedCompare(a, b)
	}
	c, err := php.CompareValues(ctx, a, b)
	if err != nil {
		*failed = err
		return failedCompare(a, b)
	}
	return c
}

// failedCompare answers a comparison whose object conversion failed: the
// object side is greater; anything else keeps the pure rule.
func failedCompare(a, b php.Value) int {
	x, y := a.Deref().Kind() == php.KindObject, b.Deref().Kind() == php.KindObject
	switch {
	case x && !y:
		return 1
	case y && !x:
		return -1
	}
	return php.Compare(a, b)
}

// flaggedCompare is the comparison a sort's flags argument (args[at])
// chooses, as php_get_data_compare_func does: SORT_NUMERIC compares the
// values as floats, SORT_STRING (and SORT_LOCALE_STRING, strcoll in the C
// locale) as byte strings -- case-folded under SORT_FLAG_CASE -- and
// SORT_NATURAL by strnatcmp, case-folded likewise; anything else is
// SORT_REGULAR. The flags were ignored, so `sort($a, SORT_STRING)` ordered
// 10 after 9 (a wrong answer), and an object without `__toString` sorted
// in silence where the conversion is an Error (array_multisort_exception).
// The keyed sorts pass keys through the same comparison.
func flaggedCompare(ctx *php.Ctx, args []php.Value, at int) func(*error, php.Value, php.Value) int {
	flags := int64(0)
	if len(args) > at {
		if f := args[at].Deref(); f.Kind() != php.KindUndef && f.Kind() != php.KindNull {
			flags = f.Int()
		}
	}
	fold := flags&8 != 0
	switch flags &^ 8 {
	case 1:
		return func(failed *error, a, b php.Value) int {
			x, y, ok := sortNumbers(failed, a, b)
			if !ok {
				return 1
			}
			switch {
			case x < y:
				return -1
			case x > y:
				return 1
			}
			return 0
		}
	case 2, 5:
		caseFold := fold && flags&^8 == 2
		return func(failed *error, a, b php.Value) int {
			x, y, ok := sortStrings(ctx, failed, a, b)
			if !ok {
				return 1
			}
			if caseFold {
				x, y = ToLowerASCII(x), ToLowerASCII(y)
			}
			return strings.Compare(x, y)
		}
	case 6:
		return func(failed *error, a, b php.Value) int {
			x, y, ok := sortStrings(ctx, failed, a, b)
			if !ok {
				return 1
			}
			if fold {
				x, y = ToLowerASCII(x), ToLowerASCII(y)
			}
			return naturalOrder(x, y)
		}
	}
	return func(failed *error, a, b php.Value) int { return sortCompare(ctx, failed, a, b) }
}

// stableOrder is a sort comparison as the engine finishes it: negated for
// the reverse sorts (php_array_reverse_*: the same operands, in the same
// order, times -1) and ties broken by original position.
func stableOrder(c int, reverse bool, a, b int) int {
	switch {
	case c < 0:
		c = -1
	case c > 0:
		c = 1
	}
	if reverse {
		c = -c
	}
	if c != 0 {
		return c
	}
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// sortStrings converts both sides as zval_get_string does: `__toString`,
// the array warning, or the Error for an object that has none. After the
// first error the sort carries on, answering 1 without converting again.
func sortStrings(ctx *php.Ctx, failed *error, a, b php.Value) (string, string, bool) {
	if *failed != nil {
		return "", "", false
	}
	x, err := php.ToString(ctx, a)
	if err != nil {
		*failed = err
		return "", "", false
	}
	y, err := php.ToString(ctx, b)
	if err != nil {
		*failed = err
		return "", "", false
	}
	return x.String(), y.String(), true
}

// sortNumbers is zval_get_double of both sides. An object's conversion
// notices ("could not be converted to float") and is not modelled here, so
// it is refused rather than guessed.
func sortNumbers(failed *error, a, b php.Value) (float64, float64, bool) {
	if *failed != nil {
		return 0, 0, false
	}
	for _, v := range []php.Value{a, b} {
		if v.Deref().Kind() == php.KindObject {
			*failed = php.Unsupported("SORT_NUMERIC over an object")
			return 0, 0, false
		}
	}
	return numberOf(a.Deref()), numberOf(b.Deref()), true
}

func numberOf(v php.Value) float64 {
	if v.Kind() == php.KindArray {
		if v.Array().Len() > 0 {
			return 1
		}
		return 0
	}
	return v.Float()
}

// sortGuard notices the array a sort is reordering being written while it
// sorts -- a `__toString` the comparison ran assigning to it (gh16648).
// The engine separates it then: the variable keeps what was written, in its
// old order, and the sort's result is dropped (probed). A share held for the
// sort makes any such write separate the body, which is what is compared.
type sortGuard struct {
	body any
	held *php.Array
}

func guardSort(target *php.Array) sortGuard {
	return sortGuard{body: target.Identity(), held: target.Share()}
}

func (g sortGuard) written(args []php.Value, target *php.Array) bool {
	php.ReleaseShared(php.Arr(g.held))
	now := args[0].Deref().Array()
	return now != target || target.Identity() != g.body
}

// keySortBuiltin builds ksort() and krsort(), which reorder by key and keep the
// association.
func keySortBuiltin(reverse bool) php.Builtin {
	return func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		return reorder(ctx, args, reverse, func(k php.Key, _ php.Value) php.Value { return k.Value() })
	}
}

// assocSortBuiltin builds asort() and arsort(), which reorder by value and keep
// the association.
func assocSortBuiltin(reverse bool) php.Builtin {
	return func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		return reorder(ctx, args, reverse, func(_ php.Key, v php.Value) php.Value { return v })
	}
}

// reorder rewrites an array in the order the key function implies, preserving
// which value belongs to which key.
func reorder(ctx *php.Ctx, args []php.Value, reverse bool, by func(php.Key, php.Value) php.Value) (php.Value, error) {
	target, ok := SortTarget(args)
	if !ok {
		return php.Bool(false), nil
	}
	type pair struct {
		key   php.Key
		value php.Value
	}
	guard := guardSort(target)
	pairs := make([]pair, 0, target.Len())
	target.Each(func(k php.Key, v php.Value) bool {
		pairs = append(pairs, pair{k, v})
		return true
	})
	var failed error
	compare := flaggedCompare(ctx, args, 1)
	order := make([]int, len(pairs))
	for i := range order {
		order[i] = i
	}
	_ = zendSort(len(pairs), func(i, j int) (int, error) {
		c := compare(&failed, by(pairs[i].key, pairs[i].value), by(pairs[j].key, pairs[j].value))
		return stableOrder(c, reverse, order[i], order[j]), nil
	}, func(i, j int) {
		pairs[i], pairs[j] = pairs[j], pairs[i]
		order[i], order[j] = order[j], order[i]
	})
	if guard.written(args, target) {
		return php.Bool(true), failed
	}
	rebuilt := php.NewArray()
	for _, p := range pairs {
		rebuilt.Set(p.key, p.value)
	}
	*target = *rebuilt
	return php.Bool(true), failed
}

// sortTarget is the array a sort function rewrites in place. The argument
// arrives as a reference, so the array behind it is what must be modified.
func SortTarget(args []php.Value) (*php.Array, bool) {
	if len(args) == 0 {
		return nil, false
	}
	value := args[0].Deref()
	if value.Kind() != php.KindArray || value.Array() == nil {
		return nil, false
	}
	return value.Array(), true
}

// replaceList rewrites an array as a plain list, which is what the sorts that
// discard keys produce.
func ReplaceList(target *php.Array, values []php.Value) {
	rebuilt := php.NewArray()
	for _, v := range values {
		rebuilt.Append(v)
	}
	*target = *rebuilt
}

// sliceBounds resolves the offset/length pair array_slice and array_splice
// share. A negative offset counts back from the end, and a negative length
// stops that many short of it.
func sliceBounds(size int, args []php.Value, at int) (start, count int) {
	offset := 0
	if len(args) > at {
		offset = int(args[at].Deref().Int())
	}
	if offset < 0 {
		offset += size
	}
	if offset < 0 {
		offset = 0
	}
	if offset > size {
		offset = size
	}
	count = size - offset
	if len(args) > at+1 && args[at+1].Deref().Kind() != php.KindNull &&
		args[at+1].Deref().Kind() != php.KindUndef {
		length := int(args[at+1].Deref().Int())
		if length < 0 {
			count = size + length - offset
		} else {
			count = length
		}
	}
	if count < 0 {
		count = 0
	}
	if offset+count > size {
		count = size - offset
	}
	return offset, count
}
