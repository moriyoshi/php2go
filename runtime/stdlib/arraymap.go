package stdlib

import "github.com/moriyoshi/php2go/runtime/php"

// array_map. The only thing it needed the interpreter for was invoking the
// callback, which `Ctx.Call` supplies, and the shape of the answer depends on
// how many arrays it was given -- which is the part worth stating:
//
//   - **one array keeps its keys**, which is what programs rely on;
//   - **several arrays are walked positionally** and the result is a plain
//     list, padded with null where a shorter array ran out;
//   - **a null callback zips** the arrays into tuples -- except that there is
//     nothing to zip one array with, so it comes back as it was, keys included.

// The modes array_filter's third argument selects, as the engine numbers them.
const (
	filterUseKey  = 2
	filterUseBoth = 1
)

func init() {
	// array_filter, moved here from the interpreter so compiled code has it:
	// the callback is handed the value by default, the key, or both, and
	// what it keeps keeps its key.
	php.RegisterBuiltin("array_filter", func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		if len(args) == 0 || args[0].Deref().Kind() != php.KindArray {
			return php.Null(), nil
		}
		result := php.NewArray()
		var failed error
		args[0].Deref().Array().Each(func(k php.Key, v php.Value) bool {
			keep := v.Deref().Bool()
			if len(args) > 1 && args[1].Deref().Kind() != php.KindNull {
				callArgs := []php.Value{v.Deref()}
				if len(args) > 2 {
					switch args[2].Deref().Int() {
					case filterUseKey:
						callArgs = []php.Value{k.Value()}
					case filterUseBoth:
						callArgs = []php.Value{v.Deref(), k.Value()}
					}
				}
				answer, err := ctx.Call(args[1].Deref(), callArgs)
				if err != nil {
					failed = err
					return false
				}
				keep = answer.Bool()
			}
			if keep {
				result.Set(k, v.Deref().Copy())
			}
			return true
		})
		if failed != nil {
			return php.Undef(), failed
		}
		return php.Arr(result), nil
	})
	php.RegisterBuiltin("array_map", func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		if len(args) < 2 {
			return php.Null(), nil
		}
		callback := args[0].Deref()
		lists := make([]*php.Array, 0, len(args)-1)
		for _, arg := range args[1:] {
			value := arg.Deref()
			if value.Kind() != php.KindArray {
				// **Only the second argument is named.** The signature is
				// `array_map(?callable $callback, array $array, array ...$arrays)`,
				// and the engine does not name a variadic slot: #2 reports
				// `($array)` and #3 onwards report nothing but the position.
				position := len(lists) + 2
				named := ""
				if position == 2 {
					named = " ($array)"
				}
				return php.Undef(), php.Raise("TypeError",
					"array_map(): Argument #%d%s must be of type array, %s given",
					position, named, php.ValueName(value))
			}
			lists = append(lists, value.Array())
		}
		if len(lists) == 0 {
			return php.Null(), nil
		}
		if len(lists) == 1 && callback.Kind() == php.KindNull {
			return php.Arr(lists[0].Clone()), nil
		}

		if len(lists) == 1 {
			result := php.NewArray()
			var failed error
			lists[0].Each(func(k php.Key, v php.Value) bool {
				mapped, err := ctx.Call(callback, []php.Value{v.Deref()})
				if err != nil {
					failed = err
					return false
				}
				result.Set(k, mapped)
				return true
			})
			if failed != nil {
				return php.Undef(), failed
			}
			return php.Arr(result), nil
		}

		longest := 0
		columns := make([][]php.Value, len(lists))
		for i, list := range lists {
			list.Each(func(_ php.Key, v php.Value) bool {
				columns[i] = append(columns[i], v.Deref())
				return true
			})
			if len(columns[i]) > longest {
				longest = len(columns[i])
			}
		}
		result := php.NewArray()
		for row := 0; row < longest; row++ {
			values := make([]php.Value, len(lists))
			for i := range lists {
				values[i] = php.Null()
				if row < len(columns[i]) {
					values[i] = columns[i][row]
				}
			}
			if callback.Kind() == php.KindNull {
				zipped := php.NewArray()
				for _, v := range values {
					zipped.Append(v)
				}
				result.Append(php.Arr(zipped))
				continue
			}
			mapped, err := ctx.Call(callback, values)
			if err != nil {
				return php.Undef(), err
			}
			result.Append(mapped)
		}
		return php.Arr(result), nil
	})
}
