package stdlib

import "github.com/moriyoshi/php2go/runtime/php"

// `get_mangled_object_vars` and `get_resource_type`: two questions about a
// value and nothing else.
//
// The mangled names are the engine's storage keys -- `"\0*\0b"` for a
// protected property, `"\0C\0c"` for a private one -- which is what makes this
// different from `get_object_vars`: that one asks what the *calling scope* can
// reach and needs a class table, and this one reports the table as it stands.

func init() {
	php.RegisterBuiltin("get_mangled_object_vars", func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		object := php.Arg(args, 0).Deref().Object()
		if object == nil {
			return php.Undef(), php.Raise("TypeError",
				"get_mangled_object_vars(): Argument #1 ($object) must be of type object, %s given",
				php.ValueName(php.Arg(args, 0).Deref()))
		}
		return php.Arr(MangledProperties(object)), nil
	})

	php.RegisterBuiltin("get_resource_type", func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		handle := php.Arg(args, 0).Deref().Resource()
		if handle == nil {
			return php.Undef(), php.Raise("TypeError",
				"get_resource_type(): Argument #1 ($resource) must be of type resource, %s given",
				php.ValueName(php.Arg(args, 0).Deref()))
		}
		return php.String(handle.TypeName()), nil
	})
}

// MangledProperties is the object's table under the engine's storage keys. The
// `(array)` cast answers the same question, which is why this is shared.
func MangledProperties(object *php.Object) *php.Array {
	out := php.NewArray()
	if php.HidesProperties(object) {
		return out
	}
	// Neither this nor the cast initializes a lazy object, but an initialized
	// proxy's state lives in the instance behind it.
	object = php.LazyState(object)
	object.PropEachRaw(func(k php.Key, raw php.Value) bool {
		if raw.Deref().IsUndef() {
			// An uninitialised typed property is not one of the object's
			// values, so it is not in the array either.
			return true
		}
		// A property holding a *program-made* reference stays one: copying an
		// array keeps a reference two names share as a reference.
		out.Set(php.StringKey(php.MangledName(object, k.String())), php.CopyElement(raw))
		return true
	})
	return out
}
