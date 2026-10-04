package stdlib

import (
	"strings"

	"github.com/moriyoshi/php2go/runtime/php"
)

// The backtrace as an array, which `debug_backtrace` answers with and which an
// exception's `getTrace` reuses. What kept it in the interpreter was one thing:
// an argument marked `#[\SensitiveParameter]` is replaced by a
// `SensitiveParameterValue`, and building one needs an object store. That is
// `Ctx.HideSensitive` now, so the rest -- which is all of it -- is frame
// shaping over `php.StackFrame`.

// The debug_backtrace() option bits.
const (
	// BacktraceProvideObject is on by default and decides whether the frame
	// carries the receiver under `object`.
	BacktraceProvideObject = 1
	// BacktraceIgnoreArgs drops the `args` key entirely, the same way
	// zend.exception_ignore_args does.
	BacktraceIgnoreArgs = 2
)

func init() {
	php.RegisterBuiltin("debug_backtrace", func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		options := int64(BacktraceProvideObject)
		if len(args) > 0 {
			options = args[0].Deref().Int()
		}
		stack := CallerStack(ctx)
		if options&BacktraceIgnoreArgs != 0 {
			php.DropTraceArgs(stack)
		}
		if len(args) > 1 {
			if limit := int(args[1].Deref().Int()); limit > 0 && limit < len(stack) {
				// The limit counts from the innermost frame, which is the end
				// of the slice.
				stack = stack[len(stack)-limit:]
			}
		}
		return php.Arr(TraceArray(ctx, stack, options&BacktraceProvideObject != 0)), nil
	})
}

// TraceArray shapes captured frames into PHP's trace array, innermost first.
func TraceArray(ctx *php.Ctx, captured []php.StackFrame, withObject bool) *php.Array {
	trace := php.NewArray()
	for i := len(captured) - 1; i >= 0; i-- {
		frame := captured[i]
		entry := php.NewArray()
		// A frame the engine entered on its own has no file and no line, and
		// says so by *omitting* the keys -- `[internal function]` is how
		// getTraceAsString renders their absence, not a value they hold.
		if !php.IsInternalFile(frame.File) {
			entry.Set(php.StringKey("file"), php.String(frame.File))
			entry.Set(php.StringKey("line"), php.Int(int64(frame.Line)))
		}
		if php.IsIncludeFunction(frame.Function) || frame.Function == "eval" {
			// **An include or eval frame is shaped apart** (probed): `eval`
			// never has an `args` key, and an include has one only when it
			// names the file -- placed *before* `function`, the order the
			// engine builds that frame in.
			if frame.Function != "eval" && len(frame.Args) > 0 && !frame.ArgsDropped {
				args := php.NewArray()
				for _, arg := range frame.Args {
					args.Append(arg)
				}
				entry.Set(php.StringKey("args"), php.Arr(args))
			}
			entry.Set(php.StringKey("function"), php.String(frame.Function))
			trace.Append(php.Arr(entry))
			continue
		}
		class, method, kind := SplitCallName(frame.Function)
		entry.Set(php.StringKey("function"), php.String(method))
		if kind != "" {
			entry.Set(php.StringKey("class"), php.String(class))
			if withObject && frame.This != nil {
				entry.Set(php.StringKey("object"), php.Obj(frame.This))
			}
			entry.Set(php.StringKey("type"), php.String(kind))
		}
		if frame.ArgsDropped {
			// The key is absent, not empty: that is how a trace taken under
			// zend.exception_ignore_args differs from a call with no
			// arguments.
			trace.Append(php.Arr(entry))
			continue
		}
		args := php.NewArray()
		for i, arg := range frame.Args {
			if frame.IsSensitive(i) {
				arg = ctx.HideSensitive(arg)
			}
			if name, named := frame.ArgName(i); named {
				args.Set(php.StringKey(name), arg)
				continue
			}
			args.Append(arg)
		}
		entry.Set(php.StringKey("args"), php.Arr(args))
		trace.Append(php.Arr(entry))
	}
	return trace
}

// SplitCallName takes a trace frame's callee apart. A plain function has no
// class and no separator; "C->m" and "C::m" carry both.
func SplitCallName(name string) (class, method, kind string) {
	for _, separator := range []string{"->", "::"} {
		if at := strings.Index(name, separator); at >= 0 {
			return name[:at], name[at+len(separator):], separator
		}
	}
	return "", name, ""
}
