package stdlib

import (
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/moriyoshi/php2go/runtime/php"
)

// `error_log($message, 3, $destination)` appends through the userland leaf
// `php2go\internal\append_file`, which lived only in the interpreter, so a
// compiled program that merely linked error_log was refused (bug39542). The
// host half is here for both back-ends; the interpreter asks its own loader
// first, which compiled code has none of.
func init() {
	// error_log's default type logs to the SAPI; the CLI's log is stderr,
	// one message per line.
	php.RegisterBuiltin(`php2go\internal\sapi_log`, func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		_, _ = fmt.Fprintln(os.Stderr, php.Arg(args, 0).Deref().String())
		return php.Bool(true), nil
	})
	php.RegisterBuiltin(`php2go\internal\process_identity`, ProcessIdentity)
	php.RegisterBuiltin(`php2go\internal\append_file`, func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		return AppendFile(ctx, php.Arg(args, 0).Deref().String(), php.Arg(args, 1).Deref().String())
	})
}

// AppendFile appends message to requested as error_log's type 3 does, and
// answers [success, [[level, diagnostic], ...]] for the userland caller to
// raise.
func AppendFile(ctx *php.Ctx, requested, message string) (php.Value, error) {
	if strings.HasPrefix(strings.ToLower(requested), "data:") {
		return AppendFileResult(false, [2]string{
			"notice", "error_log(): Stream is not writable",
		}), nil
	}
	scheme, wrapped := StreamScheme(requested)
	if wrapped && strings.EqualFold(scheme, "php") {
		lowered := strings.ToLower(requested)
		// php://temp takes a /maxmemory:N suffix and swallows the write.
		if strings.HasPrefix(lowered, "php://temp/") {
			return AppendFileResult(true), nil
		}
		switch lowered {
		case "php://input":
			// Not writable, and the engine says nothing about it (probed).
			return AppendFileResult(false), nil
		case "php://stderr":
			_, _ = os.Stderr.WriteString(message)
			return AppendFileResult(true), nil
		case "php://stdout":
			_, _ = os.Stdout.WriteString(message)
			return AppendFileResult(true), nil
		case "php://output":
			ctx.Write(message)
			if pending := ctx.TakePendingError(); pending != nil {
				return php.Undef(), pending
			}
			return AppendFileResult(true), nil
		case "php://memory", "php://temp":
			return AppendFileResult(true), nil
		default:
			// php://stdin, php://fd/N, php://filter/... and malformed names
			// each answer differently (a write errno that depends on the host's
			// descriptors, "operation failed", a filtered write, "Invalid
			// php:// URL specified"); the flat "Stream is not writable" this
			// said was wrong for all of them.
			return php.Undef(), php.Unsupported("error_log to %s", requested)
		}
	}
	local, usable, unknown := LocalFilePath(requested, false)
	if unknown != "" {
		return AppendFileResult(false,
			[2]string{"warning", fmt.Sprintf(
				"error_log(): Unable to find the wrapper %q - did you forget to enable it when you configured PHP?",
				unknown,
			)},
			[2]string{"warning", fmt.Sprintf(
				"error_log(%s): Failed to open stream: No such file or directory",
				requested,
			)},
		), nil
	}
	if !usable {
		return AppendFileResult(false, [2]string{
			"notice", "error_log(): Stream is not writable",
		}), nil
	}
	return AppendLocalFile(requested, local, message), nil
}

// AppendLocalFile is the write itself, once the destination is a host path.
func AppendLocalFile(requested, local, message string) php.Value {
	file, err := os.OpenFile(local, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err == nil {
		_, err = file.WriteString(message)
		closeErr := file.Close()
		if err == nil {
			err = closeErr
		}
	}
	if err != nil {
		reason := "Input/output error"
		if pathError, ok := err.(*os.PathError); ok {
			if errno, ok := pathError.Err.(syscall.Errno); ok {
				_, reason = SocketErrno(errno)
			}
		}
		return AppendFileResult(false, [2]string{"warning", fmt.Sprintf(
			"error_log(%s): Failed to open stream: %s", requested, reason,
		)})
	}
	return AppendFileResult(true)
}

// ProcessIdentity answers getmypid/getmyuid/getmygid's host facts; the
// userland facades keep the fixed request answers separate.
func ProcessIdentity(_ *php.Ctx, args []php.Value) (php.Value, error) {
	if len(args) == 0 {
		return php.Undef(), fmt.Errorf("php2go internal process_identity requires an operation")
	}
	switch args[0].Deref().String() {
	case "pid":
		return php.Int(int64(os.Getpid())), nil
	case "uid":
		return php.Int(int64(os.Getuid())), nil
	case "gid":
		return php.Int(int64(os.Getgid())), nil
	}
	return php.Undef(), fmt.Errorf("php2go internal process_identity received an unknown operation")
}
