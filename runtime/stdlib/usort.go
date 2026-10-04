package stdlib

import "github.com/moriyoshi/php2go/runtime/php"

// The user-comparator sorts. What kept them in the interpreter was one thing --
// invoking the comparator -- and `Ctx.Call` is the hook for exactly that, the
// same one `array_reduce` proved. Everything else about them is ordering, and
// the ordering is **observable**: `zendSort` reproduces the engine's
// insertion/quicksort hybrid because a PHP comparator can see how often and in
// what order it is asked, so Go's sort package is not a substitute even when
// the final array agrees.

// sortPair is one bucket from an array being reordered. Original records the
// insertion position Zend uses to make the public user-sort functions stable.
type sortPair struct {
	key      php.Key
	value    php.Value
	original int
}

// sortPairs invokes a PHP comparator in Zend's observable order. Stable adds
// the original-position fallback used by usort(), uasort() and uksort(); the
// extension set operations deliberately request the unstable form.
// SortPairs invokes a PHP comparator over already-extracted buckets, in Zend's
// observable call order. Exported because `php2go\internal\sort_entries`
// needs the same machinery without the rebuild step SortBy does.
func SortPairs(ctx *php.Ctx, pairs []sortPair,
	comparator php.Value, byKey, stable bool, name string) error {
	deprecated := false
	compareUser := func(a, b php.Value) (int, error) {
		answer, err := ctx.Call(comparator, []php.Value{a, b})
		if err != nil {
			return 0, err
		}
		var order int64
		if answer.Deref().Kind() == php.KindTrue || answer.Deref().Kind() == php.KindFalse {
			if !deprecated {
				ctx.Deprecated("%s(): Returning bool from comparison function is deprecated, "+
					"return an integer less than, equal to, or greater than zero", name)
				deprecated = true
			}
			if answer.Deref().Bool() {
				order = 1
			} else {
				reversed, reverseErr := ctx.Call(comparator, []php.Value{b, a})
				if reverseErr != nil {
					return 0, reverseErr
				}
				order = -reversed.Deref().Int()
			}
		} else {
			order = answer.Deref().Int()
		}
		if order < 0 {
			return -1, nil
		}
		if order > 0 {
			return 1, nil
		}
		return 0, nil
	}

	return zendSort(len(pairs), func(i, j int) (int, error) {
		a, b := pairs[i].value.Deref(), pairs[j].value.Deref()
		if byKey {
			a, b = pairs[i].key.Value(), pairs[j].key.Value()
		}
		order, err := compareUser(a, b)
		if err != nil || order != 0 || !stable {
			return order, err
		}
		if pairs[i].original < pairs[j].original {
			return -1, nil
		}
		if pairs[i].original > pairs[j].original {
			return 1, nil
		}
		return 0, nil
	}, func(i, j int) {
		pairs[i], pairs[j] = pairs[j], pairs[i]
	})
}

// sortBy reorders an array through a PHP comparator.

func init() {
	// The one helper the userland sorts call. It takes the comparator, whether
	// to compare keys, and whether the sort must be **stable** -- three
	// booleans and a callable, not an operation name. PHP's user sorts have
	// been stable since 8.0 and the internal ones are not, so the flag is a
	// real difference in the answer rather than a mode selector.
	//
	// It hands the entries back reordered and leaves rebuilding to the PHP,
	// which is what lets `usort` renumber while `uasort` keeps its keys
	// without this knowing which of them asked.
	php.RegisterBuiltin(`php2go\internal\sort_entries`, func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		if len(args) < 3 || args[0].Deref().Kind() != php.KindArray {
			return php.Undef(), php.Raise("Error",
				"php2go internal sort_entries requires an array, callback and key mode")
		}
		source := args[0].Deref().Array()
		pairs := make([]sortPair, 0, source.Len())
		source.EachRaw(func(key php.Key, value php.Value) bool {
			pairs = append(pairs, sortPair{key: key, value: value, original: len(pairs)})
			return true
		})
		stable := len(args) > 3 && args[3].Deref().Bool()
		if err := SortPairs(ctx, pairs, args[1], args[2].Deref().Bool(), stable,
			`PHP2Go\Internal\sort_entries`); err != nil {
			return php.Undef(), err
		}
		out := php.NewArray()
		for _, pair := range pairs {
			out.Set(pair.key, php.CopyElement(pair.value))
		}
		return php.Arr(out), nil
	})
}

// SortBy reorders an array through a PHP comparator, in the engine's own
// comparator-call order, and rebuilds it the way the named function wants.
//
// `usort`, `uasort` and `uksort` stay **native**. Making them userland PHP was
// tried and reverted: the comparator's own argument binding can raise
// `must be passed by reference, value given`, and the engine attributes that to
// the *caller's* line while a diagnostic raised inside a userland body is
// attributed to that body. It is the same wall `array_flip` hit, without
// `\PHP2Go\Internal\warning` to route around it -- see TODO.md.
func SortBy(ctx *php.Ctx, target *php.Array, comparator php.Value, name string) error {
	pairs := make([]sortPair, 0, target.Len())
	target.EachRaw(func(k php.Key, v php.Value) bool {
		pairs = append(pairs, sortPair{key: k, value: v, original: len(pairs)})
		return true
	})
	if err := SortPairs(ctx, pairs, comparator, name == "uksort", true, name); err != nil {
		return err
	}
	rebuilt := php.NewArray()
	for _, pair := range pairs {
		if name == "usort" {
			// usort discards the keys and renumbers; the other two keep the
			// association.
			rebuilt.Append(pair.value)
			continue
		}
		rebuilt.Set(pair.key, pair.value)
	}
	*target = *rebuilt
	return nil
}

func init() {
	for _, name := range []string{"usort", "uasort", "uksort"} {
		php.RegisterBuiltin(name, userSortBuiltin(name))
	}
}

// userSortBuiltin builds the three. They differ in what they keep -- usort
// renumbers, uasort keeps the association, uksort compares the keys -- and
// SortBy reads the name for all three.
func userSortBuiltin(name string) php.Builtin {
	return func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		if len(args) < 2 {
			return php.Bool(false), nil
		}
		target, ok := SortTarget(args)
		if !ok {
			return php.Bool(false), nil
		}
		if err := SortBy(ctx, target, args[1], name); err != nil {
			return php.Undef(), err
		}
		return php.Bool(true), nil
	}
}
