package stdlib

import (
	"fmt"

	"github.com/moriyoshi/php2go/runtime/php"
)

// The object and resource identities. Each is one field of the value, and the
// TypeError for anything else is the engine's own -- the argument check ahead
// of these raises it first, so the bodies only see the shapes they declare.

func init() {
	php.RegisterBuiltin("spl_object_id", func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		object := php.Arg(args, 0).Deref().Object()
		if object == nil {
			return php.Undef(), php.Raise("TypeError",
				"spl_object_id(): Argument #1 ($object) must be of type object, %s given",
				php.ValueName(php.Arg(args, 0).Deref()))
		}
		return php.Int(int64(object.ID)), nil
	})
	php.RegisterBuiltin("spl_object_hash", func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		object := php.Arg(args, 0).Deref().Object()
		if object == nil {
			return php.Undef(), php.Raise("TypeError",
				"spl_object_hash(): Argument #1 ($object) must be of type object, %s given",
				php.ValueName(php.Arg(args, 0).Deref()))
		}
		// PHP's hash is the handle in the first half and a per-request salt in
		// the second. Nothing may read meaning out of it beyond identity, so a
		// zero salt is as good as any -- and it makes the value reproducible.
		return php.String(fmt.Sprintf("%016x%016x", object.ID, 0)), nil
	})
	php.RegisterBuiltin("get_resource_id", func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		handle := php.Arg(args, 0).Deref().Resource()
		if handle == nil {
			return php.Undef(), php.Raise("TypeError",
				"get_resource_id(): Argument #1 ($resource) must be of type resource, %s given",
				php.ValueName(php.Arg(args, 0).Deref()))
		}
		return php.Int(int64(handle.ID)), nil
	})
}
