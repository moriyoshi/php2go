package stdlib

import (
	"errors"

	"github.com/moriyoshi/php2go/runtime/php"
)

// `iterator_to_array` and `iterator_count` for compiled code, over the same
// cursor `foreach` opens (php.NewIter): an array, a generator, or any
// Traversable the program's hop can drive. The interpreter answers them
// first, in its own dispatcher.
func init() {
	php.RegisterBuiltin("iterator_to_array", func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		preserve := len(args) < 2 || args[1].Deref().Bool()
		result := php.NewArray()
		err := drainIterable(ctx, "iterator_to_array", php.Arg(args, 0), preserve, true, func(k, v php.Value) {
			if preserve {
				// Converted as an array key, which is where a float key
				// reports losing precision (probed).
				result.Set(php.ArrayKey(ctx, k), v.Deref().Copy())
			} else {
				result.Append(v.Deref().Copy())
			}
		})
		if err != nil {
			return php.Undef(), err
		}
		return php.Arr(result), nil
	})
	// **iterator_apply calls the callback, not with the element**: the
	// iterator is only rewound, tested and advanced -- current() and key()
	// are never asked -- and each call gets the same `$args`. It counts the
	// calls and stops after one whose answer is falsy (probed: `fn() =>
	// null` over two elements is 1). The callable itself is checked by its
	// arginfo where it is bound.
	php.RegisterBuiltin("iterator_apply", func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		subject := php.Arg(args, 0).Deref()
		if subject.Kind() != php.KindObject {
			return php.Undef(), php.Raise("TypeError",
				"iterator_apply(): Argument #1 ($iterator) must be of type Traversable, %s given", php.ValueName(subject))
		}
		var passed []php.Value
		var names []string
		if extra := php.Arg(args, 2).Deref(); extra.Kind() == php.KindArray {
			// A positional element after a named one is refused by the
			// engine in words that depend on the callee's parameters
			// ("Unknown named parameter $a" for a closure with none), not
			// call_user_func_array's; not modelled.
			seenNamed, mixed := false, false
			extra.Array().Each(func(k php.Key, _ php.Value) bool {
				if !k.IsInt {
					seenNamed = true
				} else if seenNamed {
					mixed = true
					return false
				}
				return true
			})
			if mixed {
				return php.Undef(), php.Unsupported("iterator_apply with a positional argument after a named one")
			}
			var err error
			if passed, names, err = spreadArgs(extra.Array()); err != nil {
				return php.Undef(), err
			}
		} else if extra.Kind() != php.KindNull && extra.Kind() != php.KindUndef {
			return php.Undef(), php.Raise("TypeError",
				"iterator_apply(): Argument #3 ($args) must be of type ?array, %s given", php.ValueName(extra))
		}
		count := int64(0)
		stop := errors.New("iterator_apply stop")
		err := drainIterableEach(ctx, "iterator_apply", subject, false, false, func(_, _ php.Value) error {
			count++
			if names != nil {
				ctx.SetArgNames(names)
			}
			answer, err := ctx.Call(php.Arg(args, 1), append([]php.Value(nil), passed...))
			if err != nil {
				return err
			}
			if !answer.Deref().Bool() {
				return stop
			}
			return nil
		})
		if err != nil && err != stop {
			return php.Undef(), err
		}
		return php.Int(count), nil
	})
	php.RegisterBuiltin("iterator_count", func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		count := int64(0)
		// Counting asks neither current() nor key() of an Iterator.
		err := drainIterable(ctx, "iterator_count", php.Arg(args, 0), false, false, func(_, _ php.Value) { count++ })
		if err != nil {
			return php.Undef(), err
		}
		return php.Int(count), nil
	})
}

// drainIterable walks an array or a Traversable to its end. Anything else
// is the TypeError the engine's `Traversable|array` parameter gives.
func drainIterable(ctx *php.Ctx, function string, subject php.Value, wantKey, wantValue bool,
	each func(k, v php.Value)) error {
	return drainIterableEach(ctx, function, subject, wantKey, wantValue, func(k, v php.Value) error {
		each(k, v)
		return nil
	})
}

// drainIterableEach is drainIterable with a step that may end the walk: an
// error it returns is returned, after the iterator is released.
func drainIterableEach(ctx *php.Ctx, function string, subject php.Value, wantKey, wantValue bool,
	each func(k, v php.Value) error) error {
	// Running the iterator -- opening one primes a generator -- moves the
	// location into its body; a diagnostic about an element, a float key
	// losing precision, is the caller's.
	file, line := ctx.Location()
	subject = subject.Deref()
	refuse := func() error {
		given := php.ValueName(subject)
		if o := subject.Object(); o != nil {
			given = php.DisplayName(o.ClassName)
		}
		// iterator_apply's parameter is Traversable alone; the other two
		// take an array as well.
		accepts := "Traversable|array"
		if function == "iterator_apply" {
			accepts = "Traversable"
		}
		return php.Raise("TypeError",
			"%s(): Argument #1 ($iterator) must be of type %s, %s given", function, accepts, given)
	}
	if subject.Kind() != php.KindArray && subject.Kind() != php.KindObject {
		return refuse()
	}
	it, err := php.NewIter(ctx, subject)
	if err != nil {
		if errors.Is(err, php.ErrIterUnsupported) {
			return refuse()
		}
		return err
	}
	defer it.Free()
	for it.Valid() {
		v, k := php.Null(), php.Null()
		var err error
		if wantValue {
			if v, err = it.Current(ctx); err != nil {
				return err
			}
		}
		if wantKey {
			if k, err = it.Key(ctx); err != nil {
				return err
			}
		}
		ctx.SetLocation(file, line)
		if err := each(k, v); err != nil {
			return err
		}
		if err := it.Next(ctx); err != nil {
			return err
		}
	}
	return nil
}
