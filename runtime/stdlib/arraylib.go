package stdlib

import (
	"strconv"

	"github.com/moriyoshi/php2go/runtime/php"
)

// The array functions that need neither a callback nor the interpreter. The
// ones that take a callable live with the other interpreter-aware functions,
// because a builtin cannot call back into PHP.

// The comparison modes the sort and set functions take as a flag. Only the
// three php2go distinguishes are named; SORT_LOCALE_STRING and the flag bits
// above them fall back to the string comparison, which is what they are.
const (
	sortRegular = 0
	sortNumeric = 1
	sortString  = 2
)

func init() {
	php.RegisterBuiltin("array_unique", func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		if len(args) == 0 || args[0].Deref().Kind() != php.KindArray {
			return php.Null(), nil
		}
		flag := int64(sortString)
		if len(args) > 1 {
			flag = args[1].Deref().Int()
		}
		result := php.NewArray()
		// The string and numeric modes have a key to hash on; the regular mode
		// does not, since PHP's == is not an equivalence relation and cannot be
		// reduced to one. Each kept value is compared against instead.
		seen := map[string]bool{}
		var kept []php.Value
		var failed error
		args[0].Deref().Array().Each(func(k php.Key, v php.Value) bool {
			value := v.Deref()
			switch flag {
			case sortRegular:
				for _, other := range kept {
					if php.LooseEqual(value, other) {
						return true
					}
				}
				kept = append(kept, value)
			case sortNumeric:
				key := strconv.FormatFloat(value.Float(), 'g', -1, 64)
				if seen[key] {
					return true
				}
				seen[key] = true
			default:
				// zval_get_string: an object's `__toString`, or the
				// Error for one that has none (probed).
				text := value.String()
				if ctx != nil && (value.Kind() == php.KindObject || value.Kind() == php.KindArray) {
					s, err := php.ToString(ctx, value)
					if err != nil {
						failed = err
						return false
					}
					text = s.String()
				}
				if seen[text] {
					return true
				}
				seen[text] = true
			}
			result.Set(k, value.Copy())
			return true
		})
		if failed != nil {
			return php.Undef(), failed
		}
		return php.Arr(result), nil
	})
	php.RegisterBuiltin("array_reverse", func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		preserve := len(args) > 1 && args[1].Bool()
		type pair struct {
			key   php.Key
			value php.Value
		}
		var pairs []pair
		if len(args) > 0 && args[0].Deref().Kind() == php.KindArray {
			args[0].Deref().Array().EachRaw(func(k php.Key, raw php.Value) bool {
				pairs = append(pairs, pair{k, raw})
				return true
			})
		}
		out := php.NewArray()
		for i := len(pairs) - 1; i >= 0; i-- {
			// A string key survives regardless; an integer key is renumbered
			// unless the caller asked for it to be kept.
			if preserve || !pairs[i].key.IsInt {
				out.Set(pairs[i].key, php.CopyElement(pairs[i].value))
				continue
			}
			out.Append(php.CopyElement(pairs[i].value))
		}
		return php.Arr(out), nil
	})
	php.RegisterBuiltin("array_search", func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		needle := FirstArg(args)
		strict := len(args) > 2 && args[2].Bool()
		found := php.Bool(false)
		var err error
		eachArray(args, 1, func(k php.Key, v php.Value) {
			if found.Kind() != php.KindFalse || err != nil {
				return
			}
			match, e := php.SearchEqual(ctx, strict, needle, v)
			if e != nil {
				err = e
				return
			}
			if match {
				found = k.Value()
			}
		})
		if err != nil {
			return php.Undef(), err
		}
		return found, nil
	})

	php.RegisterBuiltin("array_combine", func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		var keys, values []php.Value
		eachArray(args, 0, func(_ php.Key, v php.Value) { keys = append(keys, v) })
		eachArray(args, 1, func(_ php.Key, v php.Value) { values = append(values, v) })
		if len(keys) != len(values) {
			return php.Undef(), php.Raise("ValueError",
				"array_combine(): Argument #1 ($keys) and argument #2 ($values) must have the same number of elements")
		}
		out := php.NewArray()
		for i, key := range keys {
			out.Set(keyOf(key), values[i].Copy())
		}
		return php.Arr(out), nil
	})
	php.RegisterBuiltin("array_pad", func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		var values []php.Value
		eachArray(args, 0, func(_ php.Key, v php.Value) { values = append(values, v) })
		size := int(SecondArg(args).Int())
		filler := php.Null()
		if len(args) > 2 {
			filler = args[2].Deref()
		}
		width := size
		if width < 0 {
			width = -width
		}
		out := php.NewArray()
		if width <= len(values) {
			for _, v := range values {
				out.Append(v.Copy())
			}
			return php.Arr(out), nil
		}
		padding := width - len(values)
		if size < 0 {
			for i := 0; i < padding; i++ {
				out.Append(filler.Copy())
			}
		}
		for _, v := range values {
			out.Append(v.Copy())
		}
		if size > 0 {
			for i := 0; i < padding; i++ {
				out.Append(filler.Copy())
			}
		}
		return php.Arr(out), nil
	})
	php.RegisterBuiltin("array_chunk", func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		size := int(SecondArg(args).Int())
		if size < 1 {
			return php.Undef(), php.Raise("ValueError",
				"array_chunk(): Argument #2 ($length) must be greater than 0")
		}
		preserve := len(args) > 2 && args[2].Bool()
		out := php.NewArray()
		chunk := php.NewArray()
		eachArray(args, 0, func(k php.Key, v php.Value) {
			if preserve {
				chunk.Set(k, v.Copy())
			} else {
				chunk.Append(v.Copy())
			}
			if chunk.Len() == size {
				out.Append(php.Arr(chunk))
				chunk = php.NewArray()
			}
		})
		if chunk.Len() > 0 {
			out.Append(php.Arr(chunk))
		}
		return php.Arr(out), nil
	})
	php.RegisterBuiltin("array_count_values", func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		out := php.NewArray()
		eachArray(args, 0, func(_ php.Key, v php.Value) {
			if v.Kind() != php.KindInt && v.Kind() != php.KindString {
				ctx.Warn("array_count_values(): Can only count string and integer values, " +
					"entry skipped")
				return
			}
			key := keyOf(v)
			previous, _ := out.Get(key)
			out.Set(key, php.Int(previous.Int()+1))
		})
		return php.Arr(out), nil
	})
	php.RegisterBuiltin("array_column", func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		column := SecondArg(args)
		out := php.NewArray()
		eachArray(args, 0, func(_ php.Key, row php.Value) {
			row = row.Deref()
			if row.Kind() != php.KindArray {
				return
			}
			value := row
			if column.Kind() != php.KindNull {
				held, found := row.Array().Get(keyOf(column))
				if !found {
					return
				}
				value = held
			}
			if len(args) > 2 && args[2].Deref().Kind() != php.KindNull {
				if index, found := row.Array().Get(keyOf(args[2].Deref())); found {
					out.Set(keyOf(index), value.Copy())
					return
				}
			}
			out.Append(value.Copy())
		})
		return php.Arr(out), nil
	})
	php.RegisterBuiltin("array_is_list", func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		held := FirstArg(args)
		if held.Kind() != php.KindArray {
			return php.Undef(), php.Raise("TypeError",
				"array_is_list(): Argument #1 ($array) must be of type array, %s given",
				php.ValueName(held))
		}
		return php.Bool(held.Array().IsList()), nil
	})
	php.RegisterBuiltin("array_key_first", edgeKey(false))
	php.RegisterBuiltin("array_key_last", edgeKey(true))

	// The set operations. PHP compares by string for the value-based ones, which
	// is why 0 and "0" fall together but 0 and "a" do not.
	php.RegisterBuiltin("array_diff", setOperation(false, byValueString, false))
	php.RegisterBuiltin("array_intersect", setOperation(true, byValueString, false))
	php.RegisterBuiltin("array_diff_key", setOperation(false, byKey, false))
	php.RegisterBuiltin("array_intersect_key", setOperation(true, byKey, false))
	php.RegisterBuiltin("array_diff_assoc", setOperation(false, byKeyAndValue, false))
	php.RegisterBuiltin("array_intersect_assoc", setOperation(true, byKeyAndValue, false))
	php.RegisterBuiltin("array_merge_recursive", mergeRecursive)
}

// eachArray walks the array at position n, doing nothing if it is not one.
func eachArray(args []php.Value, n int, fn func(php.Key, php.Value)) {
	if n >= len(args) {
		return
	}
	held := args[n].Deref()
	if held.Kind() != php.KindArray || held.Array() == nil {
		return
	}
	held.Array().Each(func(k php.Key, v php.Value) bool {
		fn(k, v.Deref())
		return true
	})
}

// keyOf turns a value into the key it becomes when used as a subscript.
func keyOf(v php.Value) php.Key {
	switch v.Deref().Kind() {
	case php.KindInt, php.KindTrue, php.KindFalse, php.KindFloat, php.KindNull:
		if v.Deref().Kind() == php.KindNull {
			return php.StringKey("")
		}
		return php.IntKey(v.Deref().Int())
	}
	// StringKey applies PHP's own coercion, so "1" lands on the integer slot.
	return php.StringKey(v.Deref().String())
}

// edgeKey builds array_key_first and array_key_last.
func edgeKey(last bool) php.Builtin {
	return func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		held := FirstArg(args)
		if held.Kind() != php.KindArray || held.Array().Len() == 0 {
			return php.Null(), nil
		}
		keys := held.Array().Keys()
		if last {
			return keys[len(keys)-1].Value(), nil
		}
		return keys[0].Value(), nil
	}
}

// The three ways the set operations decide whether two entries are the same.
type matchBy int

const (
	byValueString matchBy = iota
	byKey
	byKeyAndValue
)

// setOperation builds array_diff and array_intersect and their key and assoc
// variants: keep an entry of the first array when it is, or is not, present in
// every one of the rest.
func setOperation(keepPresent bool, by matchBy, _ bool) php.Builtin {
	return func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		out := php.NewArray()
		eachArray(args, 0, func(k php.Key, v php.Value) {
			// An intersection needs the entry in *every* other array; a
			// difference needs it in none.
			present := true
			for n := 1; n < len(args); n++ {
				found := false
				eachArray(args, n, func(k2 php.Key, v2 php.Value) {
					switch by {
					case byKey:
						found = found || k.String() == k2.String()
					case byKeyAndValue:
						found = found || (k.String() == k2.String() && v.String() == v2.String())
					default:
						found = found || v.String() == v2.String()
					}
				})
				if !found {
					present = false
					break
				}
			}
			if present == keepPresent {
				out.Set(k, v.Copy())
			}
		})
		return php.Arr(out), nil
	}
}

// mergeRecursive merges arrays, joining rather than overwriting where both
// sides have the same string key.
func mergeRecursive(_ *php.Ctx, args []php.Value) (php.Value, error) {
	out := php.NewArray()
	for n := range args {
		var failed error
		eachArray(args, n, func(k php.Key, v php.Value) {
			if failed != nil {
				return
			}
			failed = mergeOne(out, k, v)
		})
		if failed != nil {
			return php.Undef(), failed
		}
	}
	return php.Arr(out), nil
}

// mergeOne folds one entry into the array being built. An integer key is
// renumbered; a string key that is already there turns both values into one
// array, and the incoming value merges *into* the existing one -- which is why
// `["a" => [5 => "x"]]` merged with `["a" => ["y"]]` gives keys 5 and 6 rather
// than 0 and 1: the destination keeps its keys and its next free index.
func mergeOne(out *php.Array, k php.Key, v php.Value) error {
	if k.IsInt {
		return appendChecked(out, v.Deref())
	}
	existing, found := out.Get(k)
	if !found {
		out.Set(k, v.Deref().Copy())
		return nil
	}
	merged := php.NewArray()
	if base := existing.Deref(); base.Kind() == php.KindArray {
		// Rebuilt rather than reused, so the entry already in `out` is not
		// written through -- and rebuilding by key carries the next free index
		// with it, saturation included.
		base.Array().Each(func(key php.Key, held php.Value) bool {
			merged.Set(key, held.Deref().Copy())
			return true
		})
	} else if err := appendChecked(merged, base); err != nil {
		return err
	}
	incoming := v.Deref()
	if incoming.Kind() != php.KindArray {
		if err := appendChecked(merged, incoming); err != nil {
			return err
		}
		out.Set(k, php.Arr(merged))
		return nil
	}
	var failed error
	incoming.Array().Each(func(key php.Key, held php.Value) bool {
		failed = mergeOne(merged, key, held)
		return failed == nil
	})
	if failed != nil {
		return failed
	}
	out.Set(k, php.Arr(merged))
	return nil
}

// appendChecked appends where PHP would, and raises where PHP would: an array
// whose next integer key is taken has nowhere to put one.
func appendChecked(target *php.Array, value php.Value) error {
	if !target.CanAppend() {
		return php.Raise("Error",
			"Cannot add element to the array as the next element is already occupied")
	}
	target.Append(value.Copy())
	return nil
}
