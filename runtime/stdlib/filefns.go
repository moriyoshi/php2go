package stdlib

import (
	"errors"
	"io"
	"os"
	"strings"
	"syscall"

	"github.com/moriyoshi/php2go/runtime/php"
)

// The file functions, now that the stream type is here. They were arms of the
// interpreter's dispatcher and touched nothing but this package by the end, so
// what moved is the registration rather than any behaviour.
//
// `stream_get_filters` did not come with them: it reads the *interpreter's*
// registry of user-registered filters. `stream_get_meta_data` is here for
// compiled code (streammeta.go), but the interpreter answers it first because
// it also describes a **directory** handle, which this package does not have.

// streamArg reads an argument, or null when it was not passed -- the shape the
// interpreter's dispatcher gave these bodies.
func streamArg(args []php.Value, at int) php.Value {
	if at >= len(args) {
		return php.Null()
	}
	return args[at].Deref()
}

func init() {
	php.RegisterBuiltin("fclose", func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		_, handle, ok := AsStream(streamArg(args, 0))
		if !ok {
			return php.Bool(false), nil
		}
		if held, _ := handle.Payload.(*Stream); held != nil {
			if err := held.Close(); err != nil {
				if held.User() != nil || !errors.Is(err, ErrUserStreamOperation) {
					return php.Undef(), err
				}
				return php.Bool(false), nil
			}
		}
		handle.Close()
		return php.Bool(true), nil
	})
	php.RegisterBuiltin("feof", func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		held, _, ok := AsStream(streamArg(args, 0))
		if !ok {
			return php.Bool(true), nil
		}
		// **A user stream is asked again** when nothing is buffered and EOF
		// is not yet set: the engine's liveness check is the wrapper's
		// stream_eof (user_stream_wrapper).
		if user := held.User(); user != nil && !held.AtEOF() && user.Unread() == 0 {
			if err := user.RefreshEOF(); err != nil {
				return php.Undef(), err
			}
		}
		return php.Bool(held.AtEOF()), nil
	})
	php.RegisterBuiltin("fread", func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		held, _, ok := AsStream(streamArg(args, 0))
		if !ok {
			return php.Bool(false), nil
		}
		length := streamArg(args, 1).Int()
		if length <= 0 {
			return php.Undef(), php.Raise("ValueError",
				"fread(): Argument #2 ($length) must be greater than 0")
		}
		buf := make([]byte, length)
		n, err := held.ReadFiltered(buf, "fread")
		// **PHP fills the buffer.** A short read is not the end of the file, so
		// the engine reads again until the request is met or a read returns
		// nothing -- and it is that empty read which sets EOF. Doing one read
		// left `feof` false after `fread($f, 100)` had consumed a 3-byte file,
		// where the engine says true.
		//
		// Not for a socket or a user wrapper: there a short read is the answer,
		// and reading again would block or re-enter PHP. php://memory is a
		// user wrapper here but not to the engine, so it is filled too.
		for err == nil && n < len(buf) && !held.IsSocket() &&
			(held.User() == nil || held.PresentsAsEngine()) {
			more, again := held.ReadFiltered(buf[n:], "fread")
			n += more
			err = again
			if more == 0 {
				break
			}
		}
		if errors.Is(err, ErrStreamFilterFatal) {
			return php.Bool(false), nil
		}
		if held.User() != nil && err != nil && !errors.Is(err, io.EOF) {
			if errors.Is(err, ErrUserStreamOperation) {
				return php.Bool(false), nil
			}
			return php.Undef(), err
		}
		if err == io.EOF {
			held.SetEOF(true)
		}
		if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK) {
			if held.IsSocket() && held.IsBlocking() {
				held.SetTimedOut(true)
				return php.Bool(false), nil
			}
			return php.String(""), nil
		}
		if n > 0 {
			held.SetTimedOut(false)
		}
		return php.String(string(buf[:n])), nil
	})
	php.RegisterBuiltin("fgets", func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		held, _, ok := AsStream(streamArg(args, 0))
		if !ok {
			return php.Bool(false), nil
		}
		line, err := ReadLine(held, "fgets")
		if line == "" && err != nil {
			if (errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK)) &&
				held.IsSocket() && held.IsBlocking() {
				held.SetTimedOut(true)
			}
			return php.Bool(false), nil
		}
		return php.String(line), nil
	})
	php.RegisterBuiltin("fwrite", func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		held, _, ok := AsStream(streamArg(args, 0))
		if !ok {
			return php.Bool(false), nil
		}
		text := streamArg(args, 1).String()
		n, err := held.WriteFiltered(text, "fwrite")
		if errors.Is(err, ErrStreamFilterFatal) {
			return php.Bool(false), nil
		}
		if held.User() != nil && err != nil {
			if errors.Is(err, ErrUserStreamOperation) {
				return php.Bool(false), nil
			}
			return php.Undef(), err
		}
		if err != nil {
			if held.IsSocket() {
				errno, message := SocketErrno(err)
				ctx.Notice(
					"fwrite(): Send of %d bytes failed with errno=%d %s",
					len(text),
					errno,
					message,
				)
			}
			return php.Bool(false), nil
		}
		return php.Int(int64(n)), nil
	})
	php.RegisterBuiltin("fseek", func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		held, _, ok := AsStream(streamArg(args, 0))
		if !ok {
			return php.Int(-1), nil
		}
		whence := io.SeekStart
		if len(args) > 2 {
			whence = int(streamArg(args, 2).Int())
		}
		offset := streamArg(args, 1).Int()
		if held.FilterPositionSet && whence == io.SeekCurrent {
			offset += held.FilteredPosition
			whence = io.SeekStart
		}
		if _, err := held.Seek(offset, whence); err != nil {
			if held.User() != nil && !errors.Is(err, ErrUserStreamOperation) {
				return php.Undef(), err
			}
			return php.Int(-1), nil
		}
		held.SetEOF(false)
		held.ResetReadFilters()
		return php.Int(0), nil
	})
	php.RegisterBuiltin("ftell", func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		held, _, ok := AsStream(streamArg(args, 0))
		if !ok {
			return php.Bool(false), nil
		}
		if held.FilterPositionSet {
			return php.Int(held.FilteredPosition), nil
		}
		if held.User() != nil {
			return php.Int(held.User().Position()), nil
		}
		at, err := held.Seek(0, io.SeekCurrent)
		if err != nil {
			return php.Bool(false), nil
		}
		return php.Int(at), nil
	})
	php.RegisterBuiltin("rewind", func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		held, _, ok := AsStream(streamArg(args, 0))
		if !ok {
			return php.Bool(false), nil
		}
		if _, err := held.Seek(0, io.SeekStart); err != nil {
			if held.User() != nil && !errors.Is(err, ErrUserStreamOperation) {
				return php.Undef(), err
			}
			return php.Bool(false), nil
		}
		held.SetEOF(false)
		held.ResetReadFilters()
		return php.Bool(true), nil
	})
	php.RegisterBuiltin("fputs", func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		fn, _ := php.LookupBuiltin("fwrite")
		return fn(ctx, args)
	})
}

// Open is the plain half of fopen. Only the modes the corpus uses are honoured, and a wrapper is
// refused rather than opened as a plain path.
func Open(ctx *php.Ctx, path, mode string) (php.Value, error) {
	if held, ok := DataStream(path); ok {
		held.Configure(true, 8192, mode, path)
		// A `data:` URI carries its content in the path. There is no wrapper to
		// register and nothing to reach for, so it is opened here.
		return php.Res(ctx.NewResource("stream", held)), nil
	}
	// **php://memory and php://temp are a userland wrapper**, opened through
	// the Ctx's hooks in either back-end (hookedstream.go).
	if _, memory := MemoryStreamURI(path); memory {
		return OpenMemoryStream(ctx, path, mode)
	}
	if strings.Contains(path, "://") {
		return php.Undef(), php.Unsupported("fopen of %s", path)
	}
	flag := os.O_RDONLY
	switch strings.TrimRight(strings.TrimSuffix(mode, "b"), "t") {
	case "r":
	case "r+":
		flag = os.O_RDWR
	case "w":
		flag = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	case "w+":
		flag = os.O_RDWR | os.O_CREATE | os.O_TRUNC
	case "a":
		flag = os.O_WRONLY | os.O_CREATE | os.O_APPEND
	case "a+":
		flag = os.O_RDWR | os.O_CREATE | os.O_APPEND
	case "x":
		flag = os.O_WRONLY | os.O_CREATE | os.O_EXCL
	case "x+":
		flag = os.O_RDWR | os.O_CREATE | os.O_EXCL
	default:
		// **A bad mode is a warning, not a ValueError**, and the message quotes
		// the mode with an opening backtick and a closing apostrophe -- read
		// back from the engine, which we had been raising a fatal for.
		ctx.Warn("fopen(%s): Failed to open stream: `%s' is not a valid mode for fopen", path, mode)
		return php.Bool(false), nil
	}
	file, err := os.OpenFile(path, flag, 0o644)
	if err != nil {
		ctx.Warn("fopen(%s): Failed to open stream: No such file or directory", path)
		return php.Bool(false), nil
	}
	return php.Res(ctx.NewResource("stream", NewFileStream(file, mode, path))), nil
}

func init() {
	php.RegisterBuiltin("fopen", func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		path := streamArg(args, 0).String()
		mode := streamArg(args, 1).String()
		// Refused before any wrapper is asked (probed; dim_assign_001).
		if strings.IndexByte(path, 0) >= 0 {
			return php.Undef(), php.Raise("ValueError",
				"fopen(): Argument #1 ($filename) must not contain any null bytes")
		}
		if path == "" {
			return php.Undef(), php.Raise("ValueError", "Path must not be empty")
		}
		scheme, hasScheme := StreamScheme(path)
		if !hasScheme {
			scheme = "file"
		}
		if IsFilterURL(path) {
			if wrapper, ok := ctx.StreamWrapperFor("php"); ok && wrapper.Builtin {
				_, handle, opened, err := OpenFilterURL(ctx, path, mode, "fopen", false, true)
				if err != nil || !opened {
					return php.Bool(false), err
				}
				return php.Res(handle), nil
			}
		}
		wrapper, registered := ctx.StreamWrapperFor(scheme)
		if !registered {
			// The engine tries the scheme as written, then lowercased:
			// PHP://temp and FOO:// reach `php` and `foo`, not `Bar`.
			wrapper, registered = ctx.StreamWrapperFor(strings.ToLower(scheme))
		}
		if !registered {
			// **Two warnings, not one.** The engine reports the missing wrapper
			// without the path, and then reports the open failing with it --
			// read back from the engine, which had been emitting one combined
			// message here since before the move.
			ctx.Warn(
				"fopen(): Unable to find the wrapper \"%s\" - did you forget to enable it when you configured PHP?",
				scheme,
			)
			ctx.Warn("fopen(%s): Failed to open stream: No such file or directory", path)
			return php.Bool(false), nil
		}
		options := int64(0)
		if len(args) > 2 && streamArg(args, 2).Bool() {
			options = 1
		}
		context := php.Null()
		if len(args) > 3 {
			context = args[3]
		}
		// A wrapper the *program* registered is a class, and only the layer
		// that has a class table can instantiate one.
		if !wrapper.Builtin {
			return ctx.OpenUserStream(wrapper, path, mode, options, context)
		}
		if strings.EqualFold(scheme, "file") && hasScheme {
			if local, ok, _ := LocalFilePath(path, false); ok {
				path = local
			}
		}
		return Open(ctx, path, mode)
	})
}
