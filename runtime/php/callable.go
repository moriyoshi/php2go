package php

// Calling back into user code, which a builtin needs and this package cannot do.
//
// `array_reduce` invokes a comparator, `set_error_handler` invokes a handler,
// `usort` invokes a comparison, `call_user_func` is nothing but the invocation.
// A PHP callable is a *value* -- a function name, `[$obj, 'method']`, a Closure,
// an object with `__invoke` -- and resolving any of them needs a function and
// class table, which lives above `runtime/php`.
//
// So it is the same move as `ObjectProperties` and `ResolveTraversable`:
// **declared here, supplied from above**. The measurement behind it is that this
// is one mechanism rather than N functions -- 305 of the 2,479 corpus files that
// call a builtin call one that takes a callback, 12%, spread over
// `set_error_handler` (139 sites), `spl_autoload_register` (69),
// `register_shutdown_function` (44), `set_exception_handler` (43), `array_map`
// (36), `ob_start` (32) and the rest.
//
// It is also independent of an open question. Whether a library function is
// implemented in Go or as harvested PHP, a `usort` still has to invoke its
// comparator, so this hook is needed either way.

// CallValue invokes a PHP callable. The layer that installs it is the one that
// can resolve what a callable value denotes; nothing here needs to know.
//
// Argument passing is the caller's: a builtin passes values, and whether any of
// them is by reference is the implementation's business, not this signature's.
type CallValue func(ctx *Ctx, callable Value, args []Value) (Value, error)

// SetCallValue installs the hook. Per Ctx, like the other two, because PHP is
// shared-nothing per request and a long-lived Go process is not.
func (c *Ctx) SetCallValue(fn CallValue) { c.call = fn }

// Call invokes a PHP callable, and reports honestly when it cannot. A builtin
// that silently skipped its callback would produce a plausible wrong answer --
// `array_reduce` returning its initial value looks like an empty array rather
// than like a missing capability.
func (c *Ctx) Call(callable Value, args []Value) (Value, error) {
	if c.call == nil {
		return Null(), ErrNoCallValue
	}
	return c.call(c, callable, args)
}

// CanCall reports whether the hook is installed, for a caller that wants to
// refuse before doing half the work rather than in the middle of it.
func (c *Ctx) CanCall() bool { return c.call != nil }

// CallableReason answers **why** a value cannot be called, and "" when it can.
// It is CallValue's other half and is supplied from the same place, because
// deciding needs the same function and class tables.
//
// It exists because a builtin's callback argument is checked *before* the call,
// and reports something different from what the call would: PHP says
// `array_reduce(): Argument #2 ($callback) must be a valid callback, function
// "f" not found or invalid function name`, where invoking the same value as
// `$f()` says `Call to undefined function f()`. Compiled code answered the
// second for both, which is a wrong answer rather than a missing feature.
type CallableReason func(ctx *Ctx, callable Value) string

// SetCallableReason installs the hook.
func (c *Ctx) SetCallableReason(fn CallableReason) { c.callableReason = fn }

// CheckCallable is that argument check: it answers the TypeError an internal
// function raises for a callback parameter it cannot use, and nil when the
// value is callable.
//
// The shape test comes first and does not need the hook. Only a string, an
// array or an object can name a callable at all, and everything else is refused
// for **being the wrong shape** rather than for what it names -- which is why
// an int and a plain object both report `no array or string given`.
func (c *Ctx) CheckCallable(function string, position int, name string, callable Value) error {
	return c.checkCallable(function, position, name, callable, "valid callback")
}

// CheckCallableOrNull is CheckCallable for a `?callable` parameter, whose
// refusal says so: `array_map(): Argument #1 ($callback) must be a valid
// callback or null, ...`.
func (c *Ctx) CheckCallableOrNull(function string, position int, name string, callable Value) error {
	return c.checkCallable(function, position, name, callable, "valid callback or null")
}

func (c *Ctx) checkCallable(function string, position int, name string, callable Value, what string) error {
	if c.callableReason == nil {
		// No layer here can decide, so nothing is claimed -- **including by
		// shape**. Refusing a shape without a validator installed would invent
		// a diagnostic for a caller that never asked for the check, and letting
		// it through only means the call reports for itself.
		return nil
	}
	given := callable.Deref()
	reason := "no array or string given"
	switch given.Kind() {
	case KindString, KindArray, KindObject:
		reason = c.callableReason(c, given)
	}
	// **What the check itself threw wins**: resolving a callback's class
	// runs the autoloader, and an autoloader that throws makes that
	// exception the answer rather than the TypeError about the callback
	// (callable_param_exception_leak). The resolver parks it.
	if err := c.TakePendingError(); err != nil {
		return err
	}
	if reason == "" {
		return nil
	}
	return Raise("TypeError", "%s(): Argument #%d ($%s) must be a %s, %s",
		function, position, name, what, reason)
}

// CallableReasonFor answers the hook directly, for `is_callable`, which wants
// the verdict rather than a diagnostic. False when no hook is installed and the
// shape alone cannot decide.
func (c *Ctx) CallableReasonFor(callable Value) (string, bool) {
	given := callable.Deref()
	switch given.Kind() {
	case KindString, KindArray, KindObject:
		if c.callableReason == nil {
			return "", false
		}
		return c.callableReason(c, given), true
	}
	return "no array or string given", true
}

func init() {
	// array_reduce is the first shared builtin that calls back. Its body is
	// entirely this loop -- the callback is the only thing it needed the
	// interpreter for, which is what makes it the right first instance: it
	// proves the hook and nothing else.
	RegisterBuiltin("array_reduce", func(ctx *Ctx, args []Value) (Value, error) {
		if len(args) < 2 || args[0].Deref().Kind() != KindArray {
			return Null(), nil
		}
		// The callback is checked **before** the walk, which is what makes the
		// diagnostic the engine's: an unusable callback is an argument error,
		// not a failure part-way through.
		if err := ctx.CheckCallable("array_reduce", 2, "callback", args[1]); err != nil {
			return Undef(), err
		}
		carry := Null()
		if len(args) > 2 {
			carry = args[2].Deref()
		}
		var failed error
		args[0].Deref().Array().Each(func(_ Key, v Value) bool {
			next, err := ctx.Call(args[1], []Value{carry, v.Deref()})
			if err != nil {
				failed = err
				return false
			}
			carry = next.Deref()
			return true
		})
		if failed != nil {
			return Undef(), failed
		}
		return carry, nil
	})
}

// The error-handler stack, which `set_error_handler` pushes and
// `restore_error_handler` pops.
//
// It lives on the Ctx because it is per-request state -- PHP is shared-nothing
// per request and a long-lived Go process is not, the same argument as the
// constant table. It moved down from the interpreter because both consumers
// need it and the *policy* is subtle enough that two implementations would
// diverge: see PushErrorHandler on what `set_error_handler(null)` does.

// PushErrorHandler installs a handler and answers the one it replaced, which is
// what `set_error_handler` returns.
//
// Passing NULL installs "no handler" **on the stack** rather than clearing it:
// diagnostics go to the default handling until it is restored, and restoring
// brings the previous one back. Probed against the engine -- pushing null then
// restoring twice reaches the handler two below, which clearing could never do.
func (c *Ctx) PushErrorHandler(handler Value) Value {
	previous := Null()
	if n := len(c.errorHandlers); n > 0 {
		previous = c.errorHandlers[n-1]
	}
	c.errorHandlers = append(c.errorHandlers, handler.Deref())
	return previous
}

// PopErrorHandler is `restore_error_handler`, which answers true even with
// nothing to pop.
func (c *Ctx) PopErrorHandler() bool {
	if n := len(c.errorHandlers); n > 0 {
		c.errorHandlers = c.errorHandlers[:n-1]
	}
	return true
}

// CurrentErrorHandler is the handler a diagnostic should be offered to, and
// false when there is none *or* when the top of the stack is the null that
// `set_error_handler(null)` pushed.
func (c *Ctx) CurrentErrorHandler() (Value, bool) {
	n := len(c.errorHandlers)
	if n == 0 {
		return Null(), false
	}
	handler := c.errorHandlers[n-1]
	if handler.Kind() == KindNull {
		return Null(), false
	}
	return handler, true
}

// ErrorHandlers is the whole stack, for a cycle collector that has to treat
// each installed handler as a root: a closure reachable only from here is still
// alive.
func (c *Ctx) ErrorHandlers() []Value { return c.errorHandlers }

func init() {
	RegisterBuiltin("set_error_handler", func(ctx *Ctx, args []Value) (Value, error) {
		handler := Null()
		if len(args) > 0 {
			handler = args[0].Deref()
		}
		return ctx.PushErrorHandler(handler), nil
	})
	RegisterBuiltin("restore_error_handler", func(ctx *Ctx, args []Value) (Value, error) {
		return Bool(ctx.PopErrorHandler()), nil
	})
}

// The callable *registries*: a callable stored now and invoked later by
// something that is not the builtin that stored it.
//
// That is a different shape from `array_reduce`, which calls back inside its own
// body, and it is why this family was worth moving next -- it settles whether
// the hook generalises rather than adding another instance of what already
// worked. The split is the one the error-handler stack established: **the
// registry lives here, the trigger stays above**, because what fires an
// autoloader is class resolution and what fires a shutdown function is the end
// of the script, neither of which this package knows about.
//
// Every rule below is the engine's, probed rather than assumed. Two of them are
// not guessable and one contradicts what the interpreter did.

// PushExceptionHandler installs one and answers the previous, which is what
// `set_exception_handler` returns -- NULL when there was none. Null is pushed
// like any other handler: it installs "no handler, for now", and restoring
// brings back what was under it, exactly as for the error handler.
func (c *Ctx) PushExceptionHandler(handler Value) Value {
	previous := Null()
	if n := len(c.exceptionHandlers); n > 0 {
		previous = c.exceptionHandlers[n-1]
	}
	c.exceptionHandlers = append(c.exceptionHandlers, handler.Deref())
	return previous
}

// PopExceptionHandler is `restore_exception_handler`.
func (c *Ctx) PopExceptionHandler() bool {
	if n := len(c.exceptionHandlers); n > 0 {
		c.exceptionHandlers = c.exceptionHandlers[:n-1]
	}
	return true
}

// CurrentExceptionHandler is the handler an uncaught exception goes to, and
// false when there is none or a null is on top.
func (c *Ctx) CurrentExceptionHandler() (Value, bool) {
	n := len(c.exceptionHandlers)
	if n == 0 {
		return Null(), false
	}
	handler := c.exceptionHandlers[n-1]
	if handler.Kind() == KindNull {
		return Null(), false
	}
	return handler, true
}

// ExceptionHandlers is the whole stack, for a collector treating each as a root.
func (c *Ctx) ExceptionHandlers() []Value { return c.exceptionHandlers }

// ShutdownCall is one registered shutdown function and the arguments it was
// registered with. The callee is stored **resolved**: a shutdown function
// outlives the scope that named it, so `["self", "m"]` has to be settled at
// registration or `self` means nothing by the time it runs.
type ShutdownCall struct {
	Callee Value
	Args   []Value
}

// RegisterShutdown appends. They run in registration order, after the script
// ends -- checked, and it is the opposite of the handler stacks, where the last
// registered wins.
func (c *Ctx) RegisterShutdown(callee Value, args []Value) {
	c.shutdown = append(c.shutdown, ShutdownCall{Callee: callee, Args: args})
}

// ShutdownCalls is the list, in the order they run.
func (c *Ctx) ShutdownCalls() []ShutdownCall { return c.shutdown }

// RegisterAutoloader is `spl_autoload_register`. Two rules from the engine:
//
//   - `prepend` puts it at the front, so it is consulted first.
//   - **A duplicate is not added, and true is still returned.** Registering the
//     same callable twice leaves one entry, which the interpreter did not do --
//     it appended unconditionally, so a double registration ran the autoloader
//     twice. Checked: register f1, f2 with prepend, then f1 again, and
//     `count(spl_autoload_functions())` is **2**.
func (c *Ctx) RegisterAutoloader(callback Value, prepend bool) bool {
	for _, registered := range c.autoloaders {
		if StrictEqual(registered, callback) {
			return true
		}
	}
	if prepend {
		c.autoloaders = append([]Value{callback}, c.autoloaders...)
	} else {
		c.autoloaders = append(c.autoloaders, callback)
	}
	return true
}

// UnregisterAutoloader is `spl_autoload_unregister`, false when it was not
// registered.
func (c *Ctx) UnregisterAutoloader(callback Value) bool {
	for i, registered := range c.autoloaders {
		if StrictEqual(registered, callback) {
			c.autoloaders = append(c.autoloaders[:i:i], c.autoloaders[i+1:]...)
			return true
		}
	}
	return false
}

// Autoloaders is the list, in the order they are consulted.
func (c *Ctx) Autoloaders() []Value { return c.autoloaders }

func init() {
	// These two need no class table -- they store what they are handed -- so
	// they move whole. `register_shutdown_function` and `spl_autoload_register`
	// stay above because they *resolve* and *validate* their callable at
	// registration, which needs one.
	RegisterBuiltin("set_exception_handler", func(ctx *Ctx, args []Value) (Value, error) {
		handler := Null()
		if len(args) > 0 {
			handler = args[0].Deref()
		}
		return ctx.PushExceptionHandler(handler), nil
	})
	RegisterBuiltin("restore_exception_handler", func(ctx *Ctx, args []Value) (Value, error) {
		return Bool(ctx.PopExceptionHandler()), nil
	})
}

// ClearShutdown empties the list. The sequence ends rather than resumes after
// an `exit()` or an unhandled throw inside a shutdown function, so the runner
// clears it rather than leaving entries that will never run.
func (c *Ctx) ClearShutdown() { c.shutdown = nil }

// SuppressExceptionHandler installs "no handler" for the duration of a call and
// returns the undo.
//
// It exists as one operation because the undo is **not a pop**. The engine
// clears the handler while a handler runs, so an exception the handler itself
// throws has none -- *unless the handler installed one*, which is exactly what
// makes a chain of them work. So the null has to be removed from the position it
// was inserted at, leaving anything pushed above it in place, and popping would
// silently discard the handler a nested `set_exception_handler` installed.
//
// The undo is idempotent and does nothing if the stack has already shrunk past
// the mark.
func (c *Ctx) SuppressExceptionHandler() (undo func()) {
	mark := len(c.exceptionHandlers)
	c.exceptionHandlers = append(c.exceptionHandlers, Null())
	done := false
	return func() {
		if done || mark >= len(c.exceptionHandlers) {
			return
		}
		done = true
		c.exceptionHandlers = append(c.exceptionHandlers[:mark],
			c.exceptionHandlers[mark+1:]...)
	}
}
