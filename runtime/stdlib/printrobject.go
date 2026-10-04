package stdlib

import "github.com/moriyoshi/php2go/runtime/php"

// The one question `print_r` cannot answer in PHP: **what does this object
// show?**
//
// Everything else about print_r -- the indentation, the `Array\n(\n    [k] =>
// v\n)` shape, the blank line after a nested container, the recursion walk --
// is string work and lives in the harvested PHP. What cannot go there is this:
//
//   - a Closure prints `[name]`, `[file]`, `[line]` and `[parameter]`, and a
//     Generator prints `[function]`; the engine builds those and no PHP
//     function exposes them, `get_mangled_object_vars` least of all -- it
//     answers nothing for a Closure;
//   - an enum case prints `S Enum:string`, not `S Object`, so the backing type
//     is part of the label;
//   - the visibility annotations are `name:protected` and `name:Class:private`,
//     which need the declaring class of a property the object may not have
//     mangled.
//
// `__debugInfo` is **not** among them, which is worth saying because it looks
// like it should be: a private one is a compile-time warning and then treated
// as public, so PHP can reach it with `method_exists` and a call. It is here
// only because the hook already applies it and asking twice would run it twice.

func init() {
	php.RegisterBuiltin(`php2go\internal\print_r_object`, func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		object := php.Arg(args, 0).Deref().Object()
		if object == nil {
			return php.Undef(), php.Raise("Error",
				"php2go internal print_r_object requires an object")
		}
		label := php.String(printRObjectLabel(object))
		// **The label alone, when that is all that was asked for.** print_r
		// writes it before deciding whether the object is one it is already
		// inside, and running `__debugInfo` for a cycle it is about to refuse
		// would run it twice.
		if len(args) > 1 && !args[1].Deref().Bool() {
			out := php.NewArray()
			out.Set(php.StringKey("label"), label)
			out.Set(php.StringKey("entries"), php.Arr(php.NewArray()))
			return php.Arr(out), nil
		}
		shown, described, err := ctx.ObjectProperties(object)
		if err != nil {
			// A `__debugInfo` that did not return an array. PHP prints that
			// fatal *instead of* the dump, so it travels rather than being
			// rendered into the text.
			return php.Undef(), err
		}
		entries := php.NewArray()
		if shown != nil {
			shown.Each(func(k php.Key, item php.Value) bool {
				// An uninitialised typed property has a slot and no value, and
				// print_r does not list it.
				if item.Deref().Kind() == php.KindUndef {
					return true
				}
				label := k.String()
				if !described {
					label = php.PrintRPropertyLabel(object, label)
				} else if plain, owner, mangled := php.UnmangleProperty(label); mangled && !k.IsInt {
					// Whatever `__debugInfo` returned is an ordinary array, so
					// the declarations have no say -- but a **mangled** key in
					// it is still unmangled for display: `"\0*\0b"` prints
					// `[b:protected]`, as var_dump prints `["b":protected]`.
					label = plain + ":protected"
					if owner != "*" {
						label = plain + ":" + owner + ":private"
					}
				}
				entries.Set(php.StringKey(label), item.Deref())
				return true
			})
		}
		out := php.NewArray()
		out.Set(php.StringKey("label"), label)
		out.Set(php.StringKey("entries"), php.Arr(entries))
		return php.Arr(out), nil
	})
}

// printRObjectLabel is the line print_r writes before the parentheses:
// `C Object`, or `S Enum:string` for an enum case.
func printRObjectLabel(o *php.Object) string {
	kind := "Object"
	if o.EnumCase != "" {
		kind = "Enum"
		if o.EnumBacking != "" {
			kind = "Enum:" + o.EnumBacking
		}
	}
	return php.DisplayName(o.ClassName) + " " + kind
}
