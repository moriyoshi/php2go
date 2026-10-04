package stdlib

import (
	"fmt"

	"github.com/moriyoshi/php2go/runtime/php"
)

// The E_USER_* levels trigger_error accepts. They are spelled out rather than
// looked up because a builtin has no manifest, and they are fixed language
// constants rather than build settings.
const (
	userError      = 256
	userWarning    = 512
	userNotice     = 1024
	userDeprecated = 16384
)

func init() {
	// trigger_error raises a diagnostic at a user level, which the handler and
	// the error_reporting mask treat exactly like an engine one.
	trigger := func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		level := int64(userNotice)
		if len(args) > 1 {
			level = args[1].Deref().Int()
		}
		message := php.Arg(args, 0).Deref().String()
		switch level {
		case userWarning:
			ctx.UserWarning("%s", message)
		case userDeprecated:
			ctx.UserDeprecated("%s", message)
		case userNotice:
			ctx.UserNotice("%s", message)
		case userError:
			// PHP 8.4 deprecated this level and still honours it: the message
			// is a fatal error, and the request stops -- after its shutdown
			// functions, like any other fatal.
			ctx.Deprecated("Passing E_USER_ERROR to trigger_error() is deprecated since 8.4, " +
				"throw an exception or call exit with a string message instead")
			file, line := ctx.Location()
			ctx.Write(fmt.Sprintf("\nFatal error: %s in %s on line %d\n", message, file, line))
			return php.Undef(), &php.ExitError{Status: 255, Bailout: true}
		default:
			return php.Undef(), php.Raise("ValueError",
				"trigger_error(): Argument #2 ($error_level) must be one of E_USER_ERROR, "+
					"E_USER_WARNING, E_USER_NOTICE, or E_USER_DEPRECATED")
		}
		return php.Bool(true), nil
	}
	php.RegisterBuiltin("trigger_error", trigger)
	php.RegisterBuiltin("user_error", trigger)
}
