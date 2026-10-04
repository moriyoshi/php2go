package stdlib

import "github.com/moriyoshi/php2go/runtime/php"

// The stream primitives a userland PHP body needs, one named function each.
//
// They replace `\PHP2Go\Internal\stream_operation($stream, 'flush')` and its
// seven siblings -- a single entry point switched on a string, which is
// `php_stream_set_option(stream, option, value, ptrparam)` carried across from
// C unchanged. In C the selector is a constant passed to one function. In PHP
// and Go it should be separate functions, because a multiplexer has no
// signature to type, no function on the Go side to find, and makes every caller
// pay for the union of what any caller needs. `internal/userland/shape.tsv`
// counts the ones that are left.
//
// The boundary was an interface first, so that the implementation could stay in
// the interpreter while these functions lived here. It does not need to be one
// any more -- `stream.go` is the concrete type now -- and the only part still
// reached through an interface is `UserStream`, the half that is a PHP class.

// streamOf reaches the payload behind a stream resource argument.
func streamOf(args []php.Value, at int) (*Stream, bool) {
	if at >= len(args) {
		return nil, false
	}
	resource := args[at].Deref().Resource()
	if resource == nil {
		return nil, false
	}
	held, ok := resource.Payload.(*Stream)
	return held, ok
}

func init() {
	php.RegisterBuiltin(`php2go\internal\stream_flush`, func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		held, ok := streamOf(args, 0)
		if !ok {
			return php.Bool(false), nil
		}
		flushed, err := held.Flush()
		if err != nil {
			return php.Undef(), err
		}
		return php.Bool(flushed), nil
	})
	php.RegisterBuiltin(`php2go\internal\stream_sync`, func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		held, ok := streamOf(args, 0)
		if !ok {
			return php.Bool(false), nil
		}
		dataOnly := len(args) > 1 && args[1].Deref().Bool()
		status, err := held.Sync(dataOnly)
		if err != nil {
			return php.Undef(), err
		}
		return php.Int(int64(status)), nil
	})
	php.RegisterBuiltin(`php2go\internal\stream_truncate`, func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		held, ok := streamOf(args, 0)
		if !ok {
			return php.Bool(false), nil
		}
		var size int64
		if len(args) > 1 {
			size = args[1].Deref().Int()
		}
		truncated, err := held.Truncate(size)
		if err != nil {
			return php.Undef(), err
		}
		return php.Bool(truncated), nil
	})
	php.RegisterBuiltin(`php2go\internal\stream_stat`, func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		held, ok := streamOf(args, 0)
		if !ok {
			return php.Bool(false), nil
		}
		values, stattable, err := held.Stat()
		if err != nil {
			return php.Undef(), err
		}
		if !stattable {
			return php.Bool(false), nil
		}
		out := php.NewArray()
		for _, value := range values {
			out.Append(php.Int(value))
		}
		return php.Arr(out), nil
	})
	php.RegisterBuiltin(`php2go\internal\stream_supports_lock`, func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		held, ok := streamOf(args, 0)
		if !ok {
			return php.Bool(false), nil
		}
		return php.Bool(held.SupportsLock()), nil
	})
	php.RegisterBuiltin(`php2go\internal\stream_isatty`, func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		held, ok := streamOf(args, 0)
		if !ok {
			return php.Bool(false), nil
		}
		return php.Bool(held.IsTTY()), nil
	})
	php.RegisterBuiltin(`php2go\internal\stream_lock`, func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		held, ok := streamOf(args, 0)
		if !ok {
			return php.Bool(false), nil
		}
		var operation int64
		if len(args) > 1 {
			operation = args[1].Deref().Int()
		}
		locked, wouldBlock, err := held.Lock(operation)
		if err != nil {
			return php.Undef(), err
		}
		out := php.NewArray()
		out.Append(php.Bool(locked))
		blocked := int64(0)
		if wouldBlock {
			blocked = 1
		}
		out.Append(php.Int(blocked))
		return php.Arr(out), nil
	})
}

func init() {
	php.RegisterBuiltin(`php2go\internal\stream_set_blocking`, func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		held, ok := streamOf(args, 0)
		if !ok {
			return php.Bool(false), nil
		}
		set, err := held.SetBlocking(len(args) > 1 && args[1].Deref().Bool())
		if err != nil {
			return php.Undef(), err
		}
		return php.Bool(set), nil
	})
	php.RegisterBuiltin(`php2go\internal\stream_set_timeout`, func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		held, ok := streamOf(args, 0)
		if !ok {
			return php.Bool(false), nil
		}
		var seconds, microseconds int64
		if len(args) > 1 {
			seconds = args[1].Deref().Int()
		}
		if len(args) > 2 {
			microseconds = args[2].Deref().Int()
		}
		set, err := held.SetTimeout(seconds, microseconds)
		if err != nil {
			return php.Undef(), err
		}
		return php.Bool(set), nil
	})
	php.RegisterBuiltin(`php2go\internal\stream_set_read_buffer`, func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		held, ok := streamOf(args, 0)
		if !ok {
			return php.Bool(false), nil
		}
		var size int64
		if len(args) > 1 {
			size = args[1].Deref().Int()
		}
		status, err := held.SetReadBuffer(size)
		if err != nil {
			return php.Undef(), err
		}
		return php.Int(int64(status)), nil
	})
	php.RegisterBuiltin(`php2go\internal\stream_set_write_buffer`, func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		held, ok := streamOf(args, 0)
		if !ok {
			return php.Bool(false), nil
		}
		var size int64
		if len(args) > 1 {
			size = args[1].Deref().Int()
		}
		status, err := held.SetWriteBuffer(size)
		if err != nil {
			return php.Undef(), err
		}
		return php.Int(int64(status)), nil
	})
	php.RegisterBuiltin(`php2go\internal\stream_set_chunk_size`, func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		held, ok := streamOf(args, 0)
		if !ok {
			return php.Bool(false), nil
		}
		var size int64
		if len(args) > 1 {
			size = args[1].Deref().Int()
		}
		return php.Int(int64(held.SetChunkSize(size))), nil
	})
}

func init() {
	php.RegisterBuiltin(`php2go\internal\stream_socket_name`, func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		held, ok := streamOf(args, 0)
		if !ok {
			return php.Bool(false), nil
		}
		return held.SocketName(len(args) > 1 && args[1].Deref().Bool()), nil
	})
	php.RegisterBuiltin(`php2go\internal\stream_socket_recvfrom`, func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		held, ok := streamOf(args, 0)
		if !ok {
			return php.Bool(false), nil
		}
		return held.SocketReceive(int(intArg(args, 1)), int(intArg(args, 2))), nil
	})
	php.RegisterBuiltin(`php2go\internal\stream_socket_sendto`, func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		held, ok := streamOf(args, 0)
		if !ok {
			return php.Bool(false), nil
		}
		var data string
		if len(args) > 1 {
			data = args[1].Deref().String()
		}
		return held.SocketSend(data, int(intArg(args, 2))), nil
	})
	php.RegisterBuiltin(`php2go\internal\stream_socket_sendto_address`, func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		held, ok := streamOf(args, 0)
		if !ok {
			return php.Bool(false), nil
		}
		var data, address string
		if len(args) > 1 {
			data = args[1].Deref().String()
		}
		if len(args) > 2 {
			address = args[2].Deref().String()
		}
		written, errno, message := held.SocketSendTo(data, address, int(intArg(args, 3)))
		out := php.NewArray()
		out.Append(php.Bool(written >= 0))
		if written >= 0 {
			out.Append(php.Int(int64(written)))
		} else {
			out.Append(php.Bool(false))
		}
		out.Append(php.Int(errno))
		out.Append(php.String(message))
		return php.Arr(out), nil
	})
	php.RegisterBuiltin(`php2go\internal\stream_socket_shutdown`, func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		held, ok := streamOf(args, 0)
		if !ok {
			return php.Bool(false), nil
		}
		return php.Bool(held.SocketShutdown(int(intArg(args, 1)))), nil
	})
}

// intArg reads an integer argument, or 0 when it was not passed.
func intArg(args []php.Value, at int) int64 {
	if at >= len(args) {
		return 0
	}
	return args[at].Deref().Int()
}
