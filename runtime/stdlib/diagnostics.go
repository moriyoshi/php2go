package stdlib

import "github.com/moriyoshi/php2go/runtime/php"

// The three diagnostics a harvested PHP body raises at an **engine** level.
//
// `trigger_error` cannot serve: it raises at E_USER_* , which an error handler
// and the `error_reporting` mask both tell apart from the engine's own, and a
// library function reimplemented in PHP has to warn exactly as the C one did.
//
// They live here rather than in the interpreter because compiled code needs
// them for the same reason it needs the bodies: the library is linked into an
// emitted program, and nearly every function in it that refuses an argument
// warns on the way.

func init() {
	php.RegisterBuiltin(`php2go\internal\deprecated`, internalDiagnostic((*php.Ctx).Deprecated))
	php.RegisterBuiltin(`php2go\internal\notice`, internalDiagnostic((*php.Ctx).Notice))
	php.RegisterBuiltin(`php2go\internal\warning`, internalDiagnostic((*php.Ctx).Warn))
}

// internalDiagnostic raises one, **at the caller's position**. A diagnostic
// from a library function names the line that called it, not the line inside
// the harvested body -- which is the whole reason this is not just `ctx.Warn`.
func internalDiagnostic(raise func(*php.Ctx, string, ...any)) php.Builtin {
	return func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		file, line := ctx.Location()
		callerFile, callerLine := LibraryCallerLocation(ctx)
		ctx.SetLocation(callerFile, callerLine)
		raise(ctx, "%s", php.Arg(args, 0).Deref().String())
		ctx.SetLocation(file, line)
		return php.Null(), nil
	}
}

// LibraryCallerLocation is the nearest frame that is not in the file the
// diagnostic would otherwise name. Exported because the stream functions
// report at the caller's position for the same reason.
func LibraryCallerLocation(ctx *php.Ctx) (string, uint32) {
	file, line := ctx.Location()
	stack := ctx.Stack()
	for i := len(stack) - 1; i >= 0; i-- {
		frame := stack[i]
		if frame.File != "" && frame.File != file {
			return frame.File, frame.Line
		}
	}
	return file, line
}
