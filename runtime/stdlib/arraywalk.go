package stdlib

import "github.com/moriyoshi/php2go/runtime/php"

// `array_walk`, which is the by-reference member of the callback family: the
// callback's first parameter is bound to the element's own storage, so a
// `function (&$v)` writes back into the array.
//
// It stayed in the interpreter until the callback check existed, because the
// callback has to be refused **before** the walk starts -- an unusable callback
// is an argument error, not a failure part-way through a mutation.

func init() {
	php.RegisterBuiltin("array_walk", func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		if len(args) < 2 {
			return php.Bool(false), nil
		}
		target := args[0].Deref()
		// An object is walked too, and the engine hands over its raw property
		// table -- every property, whatever the calling scope can see.
		sourceFor := func(value php.Value) (*php.Array, *php.Object, bool) {
			value = value.Deref()
			switch value.Kind() {
			case php.KindArray:
				return value.Array(), nil, true
			case php.KindObject:
				// An initialized proxy is walked as the instance behind it.
				object := php.LazyTarget(value.Object())
				// Written through as it is walked: a live table.
				return object.Table(), object, true
			default:
				return nil, nil, false
			}
		}
		source, owner, ok := sourceFor(target)
		if !ok {
			return php.Undef(), php.Raise("TypeError",
				"array_walk(): Argument #1 ($array) must be of type array, %s given",
				php.ValueName(target))
		}
		// The callback is refused **before** the walk begins: an unusable one
		// is an argument error, not a failure part-way through a mutation that
		// has already written some elements back.
		if err := ctx.CheckCallable("array_walk", 2, "callback", args[1]); err != nil {
			return php.Undef(), err
		}
		// **Walking a lazy object's table produces its state first**, after
		// the arguments are checked, and a proxy walks its instance's
		// table (lazy_objects/array_walk).
		if owner != nil && owner.Lazy != nil {
			resolved, err := php.ResolveLazy(ctx, owner, "", "")
			if err != nil {
				return php.Undef(), err
			}
			source, owner = resolved.Table(), resolved
		}

		// array_walk uses the same live hash position as a by-reference foreach:
		// appended slots are reached, deleted future slots are skipped, and array
		// rebuilds carry the position with them.
		pos := 0
		release := source.RegisterCursor(&pos)
		defer func() { release() }()
		for {
			key, value, next, more := source.LiveNext(pos)
			pos = next
			if !more {
				break
			}
			if owner != nil && value.IsUndef() {
				continue
			}

			ref := source.RefAt(key)
			callbackKey := key
			if owner != nil {
				ref = php.NotePropertyType(owner, key.String(), ref)
				callbackKey = php.StringKey(php.MangledName(owner, key.String()))
			}
			callArgs := []php.Value{php.MakeRef(ref), callbackKey.Value()}
			if len(args) > 2 {
				callArgs = append(callArgs, args[2].Deref())
			}
			_, err := ctx.Call(args[1], callArgs)
			source.CollapseUnsharedRef(key, ref)
			if err != nil {
				return php.Undef(), err
			}

			// The callback can replace the by-reference first argument wholesale.
			// Zend reloads it after every call and refuses a scalar replacement.
			nextSource, nextOwner, valid := sourceFor(args[0])
			if !valid {
				return php.Undef(), php.Raise("TypeError",
					"Iterated value is no longer an array or object")
			}
			if nextSource != source {
				release()
				source, owner = nextSource, nextOwner
				release = source.RegisterCursor(&pos)
			} else {
				owner = nextOwner
			}
		}
		return php.Bool(true), nil
	})
}
