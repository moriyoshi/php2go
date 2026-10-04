package stdlib

import (
	"strings"
	"syscall"

	"github.com/moriyoshi/php2go/runtime/libc"
	"github.com/moriyoshi/php2go/runtime/php"
)

// `PHP2Go\Internal\directory_operation` for compiled code: the leaf under the
// library's opendir, readdir, rewinddir, closedir and scandir. The interpreter
// answers it in its own dispatcher, which also reaches directories behind a
// program's stream wrapper; here only the engine's own `file` wrapper is
// modelled, and a wrapper the program registered -- or one the engine
// supplies for something other than files -- is refused as unsupported.
//
// The entries come from the C library's readdir, in the order it returns
// them, `.` and `..` included: what PHP's plain-files wrapper hands back.

// dirStream is a directory resource's payload.
type dirStream struct {
	native *libc.Directory
}

func init() {
	php.RegisterBuiltin(`php2go\internal\directory_operation`, func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		operation := php.Arg(args, 0).Deref().String()
		switch operation {
		case "open", "list":
			candidate := php.Arg(args, 1).Deref().String()
			local, err := localDirectory(ctx, candidate)
			if err != nil {
				return php.Undef(), err
			}
			result := php.NewArray()
			fail := func(errorNumber int) php.Value {
				_, message := SocketErrno(syscall.Errno(errorNumber))
				result.Append(php.Bool(false))
				result.Append(php.Int(int64(errorNumber)))
				result.Append(php.String(message))
				return php.Arr(result)
			}
			if operation == "list" {
				names, errorNumber, available := libc.ReadDirectory(local)
				if !available {
					return php.Undef(), php.Unsupported("libc directory iteration without cgo")
				}
				if errorNumber != 0 {
					return fail(errorNumber), nil
				}
				entries := php.NewArray()
				for _, name := range names {
					entries.Append(php.String(name))
				}
				result.Append(php.Bool(true))
				result.Append(php.Int(0))
				result.Append(php.String(""))
				result.Append(php.Arr(entries))
				return php.Arr(result), nil
			}
			native, errorNumber, available := libc.OpenDirectory(local)
			if !available {
				return php.Undef(), php.Unsupported("libc opendir without cgo")
			}
			if errorNumber != 0 {
				return fail(errorNumber), nil
			}
			result.Append(php.Bool(true))
			result.Append(php.Int(0))
			result.Append(php.String(""))
			result.Append(php.Res(ctx.NewResource("stream", &dirStream{native: native})))
			return php.Arr(result), nil
		case "object":
			// dir() hands back a Directory object, which compiled code does
			// not declare.
			return php.Undef(), php.Unsupported("dir() in compiled code")
		case "read", "rewind", "close":
			function := php.Arg(args, 2).Deref().String()
			handle := php.Arg(args, 1).Deref().Resource()
			if handle == nil {
				return php.Undef(), php.Raise("TypeError",
					"%s(): Argument #1 ($dir_handle) must be a valid Directory resource", function)
			}
			if handle.Closed() {
				return php.Undef(), php.Raise("TypeError",
					"%s(): supplied resource is not a valid Directory resource", function)
			}
			held, ok := handle.Payload.(*dirStream)
			if !ok {
				// Any other stream -- a file, php://memory -- is refused in
				// these words (probed). A directory behind a program's
				// wrapper cannot be here: opening one is refused above.
				return php.Undef(), php.Raise("TypeError",
					"%s(): Argument #1 ($dir_handle) must be a valid Directory resource", function)
			}
			switch operation {
			case "read":
				name, _, found := held.native.Read()
				if !found {
					return php.Bool(false), nil
				}
				return php.String(name), nil
			case "rewind":
				held.native.Rewind()
				return php.Null(), nil
			default:
				held.native.Close()
				handle.Close()
				return php.Null(), nil
			}
		}
		return php.Undef(), php.Raise("Error", "php2go internal directory_operation received an unknown operation")
	})
}

// localDirectory is the path opendir's `file` wrapper would open, or a refusal
// for any other wrapper.
func localDirectory(ctx *php.Ctx, candidate string) (string, error) {
	scheme, found := StreamScheme(candidate)
	if !found {
		return candidate, nil
	}
	if registration, registered := ctx.StreamWrapperFor(strings.ToLower(scheme)); registered && !registration.Builtin {
		return "", php.Unsupported("directory through the stream wrapper %s:// in compiled code", scheme)
	}
	local, usable, _ := LocalFilePath(candidate, false)
	if !usable {
		return "", php.Unsupported("directory through the %s:// wrapper in compiled code", scheme)
	}
	return local, nil
}

// chdir for compiled code: the process's own working directory, which the
// compiled getcwd reads. The interpreter answers it in its dispatcher first,
// because a loader may own a virtual working directory there.
func init() {
	php.RegisterBuiltin("chdir", func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		directory := php.Arg(args, 0).Deref().String()
		if strings.IndexByte(directory, 0) >= 0 {
			return php.Undef(), php.Raise("ValueError",
				"chdir(): Argument #1 ($directory) must not contain any null bytes")
		}
		errorNumber, available := libc.Chdir(directory)
		if !available {
			return php.Undef(), php.Unsupported("libc chdir without cgo")
		}
		if errorNumber != 0 {
			_, message := SocketErrno(syscall.Errno(errorNumber))
			ctx.Warn("chdir(): %s (errno %d)", message, errorNumber)
			return php.Bool(false), nil
		}
		return php.Bool(true), nil
	})
}
