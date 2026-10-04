package php

import "errors"

// Ending the script, which `exit`, `die` and `trigger_error(E_USER_ERROR)` all
// do and which is **not** a throwable: no `catch` sees it, `finally` does not
// run, and the only thing that stops it is the top of the program -- after
// which the shutdown functions still run.
//
// It lives here rather than in either back-end because both need to raise it
// and both need to recognise it. What neither can do from here is *act* on it:
// calling `os.Exit` is the program's to do, and the interpreter shares this
// package with a caller it must not end.

// ExitError unwinds everything. Status is the process exit code.
//
// Uncaught is a throwable the exit **reports** rather than throws, at Level
// ("Warning" or "Fatal error"): `assert()` under ASSERT_BAIL prints a pending
// exception that way and ends the script, so no `catch` sees it
// (gh16293_001). The program prints it before the shutdown functions.
type ExitError struct {
	Status   int
	Uncaught error
	Level    string
	// Bailout is an exit that is a fatal error -- trigger_error at
	// E_USER_ERROR -- after which the engine destroys nothing: no destructor
	// runs and no suspended generator or fiber is closed.
	Bailout bool
}

func (e *ExitError) Error() string { return "exit" }

// Exit is the error to return from a builtin that ends the script.
func Exit(status int) error { return &ExitError{Status: status} }

// AsExit reports whether an error is an exit rather than a failure, looking
// through wrapping.
func AsExit(err error) (*ExitError, bool) {
	var exit *ExitError
	if errors.As(err, &exit) {
		return exit, true
	}
	return nil, false
}

// SensitiveHider replaces an argument marked `#[\SensitiveParameter]` in a
// backtrace. What it replaces it with is a `SensitiveParameterValue` object,
// and building one needs an object store -- so it is declared here and
// supplied from above, like the callable and object-property hooks.
type SensitiveHider func(Value) Value

// SetSensitiveHider installs the hook.
func (c *Ctx) SetSensitiveHider(fn SensitiveHider) { c.hideSensitive = fn }

// HideSensitive applies it. With no hook installed the argument is left alone,
// which is what a program with no sensitive parameters would see anyway.
func (c *Ctx) HideSensitive(v Value) Value {
	if c.hideSensitive == nil {
		return v
	}
	return c.hideSensitive(v)
}
