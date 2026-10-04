package interp

import (
	"github.com/moriyoshi/php2go/runtime/php"
	"github.com/moriyoshi/php2go/runtime/stdlib"
)

func init() {
	// Inside a function these compile to their own opcodes; the builtins are
	// what a call from the global scope, or a dynamic call by name, reaches.
	// trigger_error raises a diagnostic at a user level, which the handler and
	// the error_reporting mask treat exactly like an engine one.

}

// The E_USER_* levels trigger_error accepts. They are spelled out rather than
// looked up because a builtin has no manifest, and they are fixed language
// constants rather than build settings.
// The debug_backtrace() option bits that `debug_backtrace` still needs here --
// it could not move, because a hidden argument becomes a
// SensitiveParameterValue and building one needs the object store.
const (
	backtraceProvideObject = 1
	backtraceIgnoreArgs    = 2
)

const (
	userError      = 256
	userWarning    = 512
	userNotice     = 1024
	userDeprecated = 16384
)

func callerStack(ctx *php.Ctx) []php.StackFrame { return stdlib.CallerStack(ctx) }

// rangeBuiltin builds the inclusive sequence between two bounds. The bounds
// decide the element type: two single characters give a character range, an
// integral pair gives integers, and anything else gives floats.
