package stdlib

import "github.com/moriyoshi/php2go/runtime/php"

// `call_user_func` and `call_user_func_array`. The compiler folds a literal
// `call_user_func("f", ...)` into a direct call, so what reaches here is the
// dynamic form -- a callable held in a variable, a `[target, method]` pair, a
// Closure -- which is exactly what `Ctx.Call` is for.
//
// **Named arguments are the part that does not fit.** A string key in
// `call_user_func_array`'s array names a parameter, and `Ctx.Call` takes a
// positional slice with nowhere to put one. Refused rather than dropped: a
// silently positional call would put the value in the wrong parameter.

func init() {
	php.RegisterBuiltin("call_user_func", func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		if len(args) == 0 {
			return php.Null(), nil
		}
		return ctx.Call(args[0], args[1:])
	})

	php.RegisterBuiltin("call_user_func_array", func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		if len(args) < 2 {
			return php.Null(), nil
		}
		list := args[1].Deref()
		if list.Kind() != php.KindArray {
			return php.Undef(), php.Raise("TypeError",
				"call_user_func_array(): Argument #2 ($args) must be of type array, %s given",
				php.ValueName(list))
		}
		passed, names, err := spreadArgs(list.Array())
		if err != nil {
			return php.Undef(), err
		}
		if names != nil {
			ctx.SetArgNames(names)
		}
		return ctx.Call(args[0], passed)
	})
}

// SpreadArgs is spreadArgs for compiled code: ReflectionFunction::invokeArgs,
// ReflectionMethod::invokeArgs and ReflectionClass::newInstanceArgs take their
// arguments as an array whose string keys are named arguments, as
// call_user_func_array does (named_params/call_user_func).
func SpreadArgs(source *php.Array) ([]php.Value, []string, error) {
	return spreadArgs(source)
}

// spreadArgs turns an argument array into a call's arguments: the int-keyed
// elements positionally, a string key naming a parameter. The named values go
// after the positional ones and names lists them, for Ctx.SetArgNames just
// before the call.
func spreadArgs(source *php.Array) (passed []php.Value, names []string, err error) {
	var named bool
	var namedValues []php.Value
	source.Each(func(k php.Key, v php.Value) bool {
		if !k.IsInt {
			named = true
			names = append(names, k.Str)
			if raw, ok := source.Raw(k); ok {
				v = raw
			}
			namedValues = append(namedValues, v)
			return true
		}
		if named {
			err = php.Raise("Error",
				"Cannot use positional argument after named argument")
			return false
		}
		// **An element that is already a reference is passed as one**, so a
		// by-reference parameter writes back into whatever it aliases.
		// Dereferencing here would quietly make every such call by value,
		// and `Each` hands out the dereferenced element, so the raw slot
		// has to be read to tell.
		if raw, ok := source.Raw(k); ok {
			if _, isRef := raw.RefOf(); isRef {
				passed = append(passed, raw)
				return true
			}
		}
		passed = append(passed, v)
		return true
	})
	if err != nil {
		return nil, nil, err
	}
	// **A string key names a parameter**: the values go after the positional
	// ones under their names, which the callee binds -- a compiled function in
	// its prologue, a library function by its arginfo (bug79979). Only
	// compiled code reaches here; the interpreter has its own.
	if named {
		passed = append(passed, namedValues...)
	}
	return passed, names, nil
}
