package stdlib

import "github.com/moriyoshi/php2go/runtime/php"

// `assert()` and the option numbering behind `assert_options()`. The options
// themselves are request state and live on the Ctx; what kept the body in the
// interpreter is that a failing assertion can invoke a callback, which is
// `Ctx.Call`.

// The ASSERT_* option numbers, spelled out because a builtin has no manifest
// and these are fixed language constants rather than build settings.
const (
	assertActive    = 1
	assertCallback  = 2
	assertBail      = 3
	assertWarning   = 4
	assertException = 5
)

func assertionOption(ctx *php.Ctx, option int64) php.Value {
	held := ctx.Assertions()
	// Reported as **0 or 1**, not as a bool: that is what assert_options()
	// hands back, and the userland facade prints it.
	switch option {
	case assertActive:
		return php.Int(boolFlag(held.Active))
	case assertCallback:
		return held.Callback.Copy()
	case assertBail:
		return php.Int(boolFlag(held.Bail))
	case assertWarning:
		return php.Int(boolFlag(held.Warning))
	case assertException:
		return php.Int(boolFlag(held.Exception))
	}
	return php.Null()
}

func setAssertionOption(ctx *php.Ctx, option int64, value php.Value) php.Value {
	previous := assertionOption(ctx, option)
	held := ctx.Assertions()
	switch option {
	case assertActive:
		held.Active = value.Deref().Bool()
	case assertCallback:
		held.Callback = value.Copy()
	case assertBail:
		held.Bail = value.Deref().Bool()
	case assertWarning:
		held.Warning = value.Deref().Bool()
	case assertException:
		held.Exception = value.Deref().Bool()
	}
	return previous
}

func init() {
	php.RegisterBuiltin(`php2go\internal\assert_options`, func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		if len(args) < 2 {
			return php.Undef(), php.Raise("Error",
				"php2go internal assert_options requires an action and option")
		}
		action := args[0].Deref().String()
		option := args[1].Deref().Int()
		if action == "get" {
			return assertionOption(ctx, option), nil
		}
		if action == "set" && len(args) > 2 {
			return setAssertionOption(ctx, option, args[2]), nil
		}
		return php.Undef(), php.Raise("Error",
			"php2go internal assert_options received an invalid operation")
	})

	php.RegisterBuiltin("assert", func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		held := ctx.Assertions()
		// The compiler supplies the source-text description for a bare assert.
		if len(args) == 0 || args[0].Deref().Bool() || !held.Active {
			return php.Bool(true), nil
		}
		description := php.String("")
		if len(args) > 1 {
			description = args[1].Deref().Copy()
		}
		message := description.Deref().String()
		callback := held.Callback.Deref()
		if callback.Kind() != php.KindNull && !callback.IsUndef() {
			reason, decided := ctx.CallableReasonFor(callback)
			if pending := ctx.TakePendingError(); pending != nil {
				return php.Undef(), pending
			}
			if decided && reason != "" {
				// **The warning comes first and only sometimes.** A callback
				// that cannot be called is an Error, and the failure is still
				// reported beforehand -- unless an exception is coming anyway,
				// or a handler is installed to see it.
				_, handlerActive := ctx.CurrentErrorHandler()
				if held.Warning && !held.Exception && !handlerActive {
					ctx.Warn("assert(): %s failed", message)
				}
				err := php.Raise(
					"Error", "Invalid callback %s, %s", callback.String(), reason)
				if held.Bail {
					return php.Undef(), &php.ExitError{Uncaught: php.AttachStack(ctx, err), Level: "Warning"}
				}
				return php.Undef(), err
			}
			file, line := ctx.Location()
			if _, err := ctx.Call(callback, []php.Value{
				php.String(file), php.Int(int64(line)), php.Null(), description.Copy(),
			}); err != nil {
				// **Under ASSERT_BAIL the callback's exception is reported, not
				// thrown**: the failure warning first -- unless a handler is
				// installed, which with an exception pending sees nothing and
				// prints nothing -- then `Warning: Uncaught`, and the script ends
				// with no catch or finally running (probed).
				if _, exiting := php.AsExit(err); held.Bail && !held.Exception && !exiting {
					// The failed call left the position inside the callback.
					ctx.SetLocation(file, line)
					if _, handlerActive := ctx.CurrentErrorHandler(); held.Warning && !handlerActive {
						ctx.Warn("assert(): %s failed", message)
					}
					return php.Undef(), &php.ExitError{Uncaught: php.AttachStack(ctx, err), Level: "Warning"}
				}
				return php.Undef(), err
			}
		}
		// **A description that is an object is thrown as it stands**, whatever
		// the exception setting says: `assert(false, new MyError("x"))` raises
		// that object rather than an AssertionError describing it.
		if object := description.Deref().Object(); object != nil {
			return php.Undef(), php.Throw(object)
		}
		if held.Exception {
			err := php.RaiseAt(ctx, "AssertionError", "%s", message)
			if held.Bail {
				// The AssertionError is a fatal error at once, which no
				// catch sees; the shutdown functions still run.
				return php.Undef(), &php.ExitError{Status: 255, Uncaught: php.AttachStack(ctx, err), Level: "Fatal error"}
			}
			return php.Undef(), err
		}
		if held.Warning {
			ctx.Warn("assert(): %s failed", message)
			if pending := ctx.TakePendingError(); pending != nil {
				return php.Undef(), pending
			}
		}
		if held.Bail {
			return php.Undef(), php.Exit(0)
		}
		return php.Bool(false), nil
	})
}

func boolFlag(on bool) int64 {
	if on {
		return 1
	}
	return 0
}
