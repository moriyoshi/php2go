package stdlib

import "github.com/moriyoshi/php2go/runtime/php"

// The three array functions that **rewrite their argument**: they take it by
// reference and the caller sees the change through the box the send passed in.
// Nothing about them needs a class table, so they belong with the rest of the
// array surface -- they were in the interpreter only because that is where they
// were written, and compiled code could not call them at all.

func init() {
	php.RegisterBuiltin("array_shift", func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		target, ok := SortTarget(args)
		if !ok {
			return php.Null(), nil
		}
		// **The keys renumber**, which `ShiftFirst` owns: an integer-keyed
		// array closes the gap and a string-keyed one keeps its names.
		_, value, found := target.ShiftFirst()
		if !found {
			return php.Null(), nil
		}
		return value, nil
	})

	php.RegisterBuiltin("array_push", func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		target, ok := SortTarget(args)
		if !ok {
			return php.Bool(false), nil
		}
		for _, value := range args[1:] {
			if !target.CanAppend() {
				return php.Undef(), php.Raise("Error",
					"Cannot add element to the array as the next element is already occupied")
			}
			target.Append(value.Deref().Copy())
		}
		return php.Int(int64(target.Len())), nil
	})

	php.RegisterBuiltin("array_unshift", func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		target, ok := SortTarget(args)
		if !ok {
			return php.Bool(false), nil
		}
		added := make([]php.Value, 0, len(args)-1)
		for _, value := range args[1:] {
			added = append(added, value.Deref().Copy())
		}
		target.Prepend(added)
		return php.Int(int64(target.Len())), nil
	})
}
