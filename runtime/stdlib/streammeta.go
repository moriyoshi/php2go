package stdlib

import "github.com/moriyoshi/php2go/runtime/php"

// `stream_get_meta_data` for compiled code, which has only this package's
// streams. The interpreter answers it first, in its own dispatcher, because
// it also has a **directory** handle to describe; what reaches here is a
// Stream or something that is not one.
func init() {
	php.RegisterBuiltin("stream_get_meta_data", func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		return StreamMetadata(php.Arg(args, 0), "stream_get_meta_data")
	})
	// socket_get_status is userland, and names itself in the diagnostics.
	php.RegisterBuiltin(`php2go\internal\stream_metadata`, func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		return StreamMetadata(php.Arg(args, 0), php.Arg(args, 1).Deref().String())
	})
}

// StreamMetadata is the array `stream_get_meta_data` returns. A user stream
// asks its wrapper's stream_eof first, as the engine does, so `eof` is
// current.
func StreamMetadata(value php.Value, function string) (php.Value, error) {
	value = value.Deref()
	handle := value.Resource()
	if handle == nil {
		return php.Undef(), php.Raise("TypeError",
			"%s(): Argument #1 ($stream) must be of type resource, %s given",
			function, php.ValueName(value))
	}
	held, _, ok := AsStream(value)
	if !ok {
		return php.Undef(), php.Raise("TypeError",
			"%s(): supplied resource is not a valid stream resource", function)
	}
	if held.filterURL != "" {
		// The engine reports wrapper_type "PHP" and the URL over the
		// target's own stream type; not modelled.
		return php.Undef(), php.Unsupported("%s of a php://filter stream", function)
	}
	if held.User() != nil {
		if err := held.User().RefreshEOF(); err != nil {
			return php.Undef(), err
		}
	}
	return php.Arr(held.Metadata()), nil
}
