package interp

import (
	"strings"

	"github.com/moriyoshi/php2go/runtime/php"
)

// scopeIntrospection are the functions that read or write their caller's own
// frame. The engine refuses to reach any of them through a variable, a callback
// or call_user_func: the compiler has to see the name at the call site, because
// what they act on is the frame that named them.
var scopeIntrospection = map[string]bool{
	"extract": true, "compact": true, "get_defined_vars": true,
	"func_get_args": true, "func_num_args": true, "func_get_arg": true,
}

// noDynamicCall names the function a callee refers to when that function may
// not be reached dynamically, and "" otherwise.
func noDynamicCall(callee php.Value) string {
	held := callee.Deref()
	if held.Kind() != php.KindString {
		return ""
	}
	lower := strings.ToLower(held.String())
	if scopeIntrospection[lower] {
		return lower
	}
	return ""
}

// callValue invokes a PHP callable: a Closure, a function name, a "Class::method"
// string or a [target, method] pair. Resolution is dynamicTarget's, which is the
// same one the call opcodes use, so a callback behaves exactly as it would if
// the program had written the call out.
func (in *Interp) callValue(ctx *php.Ctx, unit *Unit, callee php.Value, args []php.Value, depth int) (php.Value, error) {
	return in.callValueFor(ctx, unit, "", callee, args, depth)
}

// callValueAt is callValue for a callback the engine invokes *at* a place in
// the script rather than from inside an internal function. An error handler is
// the case: the engine calls it where the diagnostic was raised, so that is
// what its stack frame names -- not `[internal function]`.
func (in *Interp) callValueAt(ctx *php.Ctx, unit *Unit, callee php.Value,
	args []php.Value, file string, line uint32, depth int) (php.Value, error) {
	site := php.StackFrame{File: file, Line: line}
	restore := ctx.SetCallSite(&site)
	defer restore()
	return in.callValueFor(ctx, unit, "", callee, args, depth)
}

// callValueFor is callValue for a builtin that names itself in the TypeError an
// unusable callback earns. PHP validates the callback before calling anything,
// and the reason it gives is specific.
func (in *Interp) callValueFor(ctx *php.Ctx, unit *Unit, builtin string,
	callee php.Value, args []php.Value, depth int) (php.Value, error) {
	return in.callValueNamed(ctx, unit, builtin, callee, args, nil, depth)
}

// callValueNamed is callValueFor with named arguments alongside the positional
// ones. Only the unfolded call_user_func_array has any: the folded form sends
// them through the opcodes, where the pending call collects them.
func (in *Interp) callValueNamed(ctx *php.Ctx, unit *Unit, builtin string,
	callee php.Value, args []php.Value, byName []namedArgument, depth int) (php.Value, error) {
	// Resolving the callback can raise a diagnostic -- the deprecated
	// spellings do -- and a user error handler can turn that into a throw.
	// When it does, the resolution never finished: there is no reason to
	// report, so the engine prints the empty one it started with, and what the
	// handler threw hangs off the TypeError as its cause.
	refused := func(pending error) error {
		if builtin == "" {
			return pending
		}
		return in.throwChained(ctx, unit, pending, "TypeError",
			"%s(): Argument #1 ($callback) must be a valid callback, (null)", builtin)
	}
	if builtin != "" {
		reason := in.callableReason(ctx, unit, callee)
		if pending := ctx.TakePendingError(); pending != nil {
			return php.Undef(), refused(pending)
		}
		if reason != "" {
			return php.Undef(), Raise("TypeError",
				"%s(): Argument #1 ($callback) must be a valid callback, %s", builtin, reason)
		}
	}
	target, ok := unit.dynamicTarget(ctx, callee.Deref())
	in.canonicalCallName(target)
	if pending := ctx.TakePendingError(); pending != nil {
		return php.Undef(), refused(pending)
	}
	if !ok {
		return php.Undef(), Raise("TypeError", "Value of type %s is not callable",
			phpTypeName(callee.Deref()))
	}
	// call_user_func and its siblings lend the caller's instance to a callable
	// that named a *class* and an instance method, where a direct `$c()` refuses
	// outright. Probed both ways from the same method: call_user_func(["P", "m"])
	// runs with $this, `$c = ["P", "m"]; $c()` is an Error.
	unit.forwardThis(ctx, target)
	target.args = args
	target.named = append(target.named, byName...)
	if len(target.named) > 0 {
		// The opcode path applies these at DO_FCALL; a call made from Go has
		// to do it itself, before the frame is built from target.args.
		if err := in.applyNamedArguments(ctx, unit, target); err != nil {
			return php.Undef(), err
		}
	}
	// A callback names its callee at run time by definition, whatever the
	// spelling: array_map("extract", ...) is as dynamic as $f = "extract".
	target.dynamic = true
	if isUserCallBuiltin(builtin) {
		// The *folded* form of these calls is marked at SEND_USER instead --
		// this is the path the compiler could not fold, which reaches the
		// callee from here.
		target.viaUserCall = true
		if isUserCallArrayBuiltin(builtin) {
			// call_user_func_array hands the array's elements over as they
			// are, so an element that is already a reference stays one. That
			// is the only way a by-reference parameter is reached this way,
			// and it is what makes the two functions differ: the plain one
			// takes its arguments through its own by-value parameters.
			in.noteCallbackByRef(ctx, unit, target)
		} else {
			// call_user_func takes its arguments by value, so a callee that
			// wanted a slot gets a copy. PHP says so once per such parameter,
			// before the body runs.
			in.noteUserCallByRef(ctx, unit, target)
			for i, arg := range target.args {
				if arg.IsRef() {
					target.args[i] = arg.Deref().Copy()
				}
			}
		}
	} else {
		// Every other call made from Go is in the same position: it is
		// assembled with no caller slots to hand over, so a by-reference
		// parameter gets a copy unless the caller went out of its way to pass a
		// slot -- which array_walk does for the element it hands its callback.
		in.noteCallbackByRef(ctx, unit, target)
	}
	// The call opcodes push a stack frame; a call made from Go has to push its
	// own, or the callee is missing from every backtrace taken inside it. The
	// engine calls it from inside the internal function, and says so: the
	// frame has no file and no line.
	frame := php.StackFrame{
		Function: target.traceName(), File: php.InternalFrameFile,
		Args: args, This: target.this,
	}
	if site := ctx.CallSite(); site != nil {
		frame.File, frame.Line = site.File, site.Line
	}
	ctx.PushCall(frame)
	defer ctx.PopCall()
	return in.dispatch(ctx, unit, target, depth)
}

// forwardStaticCall dispatches a callable with the caller's late static binding
// kept, which is what tells forward_static_call apart from call_user_func.
func (in *Interp) forwardStaticCall(ctx *php.Ctx, unit *Unit, builtin string,
	callee php.Value, args []php.Value, byName []namedArgument, depth int) (php.Value, error) {
	if reason := in.callableReason(ctx, unit, callee); reason != "" {
		return php.Undef(), Raise("TypeError",
			"%s(): Argument #1 ($callback) must be a valid callback, %s", builtin, reason)
	}
	target, ok := unit.dynamicTarget(ctx, callee.Deref())
	if !ok {
		return php.Undef(), Raise("TypeError", "Value of type %s is not callable",
			phpTypeName(callee.Deref()))
	}
	// call_user_func and its siblings lend the caller's instance to a callable
	// that named a *class* and an instance method, where a direct `$c()` refuses
	// outright. Probed both ways from the same method: call_user_func(["P", "m"])
	// runs with $this, `$c = ["P", "m"]; $c()` is an Error.
	unit.forwardThis(ctx, target)
	target.args = args
	target.named = append(target.named, byName...)
	if len(target.named) > 0 {
		// The opcode path applies these at DO_FCALL; a call made from Go has
		// to do it itself, before the frame is built from target.args.
		if err := in.applyNamedArguments(ctx, unit, target); err != nil {
			return php.Undef(), err
		}
		args = target.args
	}
	target.dynamic = true
	// The binding is the caller's whatever the callable named, which is the one
	// thing this does that call_user_func does not.
	if called := unit.callingCalledScope(ctx); called != nil {
		target.called = called
	}
	if target.this == nil {
		target.this = unit.callingThis(ctx)
	}
	file, line := ctx.Location()
	ctx.PushCall(php.StackFrame{
		Function: target.traceName(), File: file, Line: line,
		Args: args, This: target.this,
	})
	defer ctx.PopCall()
	return in.dispatch(ctx, unit, target, depth)
}

// callbackBuiltin implements the array functions that take a PHP callable.
// They cannot live in the plain builtin map, which has no way back into the
// interpreter.
func (in *Interp) callbackBuiltin(ctx *php.Ctx, unit *Unit, name string, args []php.Value, depth int) (php.Value, bool, error) {
	switch name {
	case "register_tick_function":
		if len(args) == 0 {
			return php.Undef(), true, Raise("ArgumentCountError",
				"register_tick_function() expects at least 1 argument, 0 given")
		}
		target, err := in.resolveTickFunction(ctx, unit, args[0])
		if err != nil {
			return php.Undef(), true, err
		}
		unit.addTickFunction(args[0], target, args[1:])
		return php.Bool(true), true, nil

	case "unregister_tick_function":
		if len(args) == 0 {
			return php.Undef(), true, Raise("ArgumentCountError",
				"unregister_tick_function() expects exactly 1 argument, 0 given")
		}
		// Resolve once at the call site even though removal compares the original
		// value. Scope-relative and deprecated callable forms are settled here,
		// as they are when Zend fills its fcall cache.
		if _, err := in.resolveTickFunction(ctx, unit, args[0]); err != nil {
			return php.Undef(), true, err
		}
		if err := unit.removeTickFunction(args[0]); err != nil {
			return php.Undef(), true, err
		}
		return php.Null(), true, nil

	case "exit", "die":
		// exit() ends the request, and the engine flushes the open output
		// buffers *there* rather than afterwards -- so a filter that throws is
		// catchable at the exit, which is what the try around it is for.
		if len(args) > 0 {
			if value := args[0].Deref(); value.Kind() == php.KindString {
				ctx.Write(value.String())
				if err := in.drainBuffers(ctx, unit); err != nil {
					return php.Undef(), true, err
				}
				return php.Undef(), true, php.Exit(0)
			}
		}
		if err := in.drainBuffers(ctx, unit); err != nil {
			return php.Undef(), true, err
		}
		status := 0
		if len(args) > 0 {
			status = int(args[0].Deref().Int())
		}
		return php.Undef(), true, php.Exit(status)
	case "ob_start", "ob_clean", "ob_flush", "ob_end_flush", "ob_end_clean",
		"ob_get_clean", "ob_get_flush":
		// Output buffering is in runtime/stdlib so compiled code has it too.
		// What kept it here was `ob_start`'s callable argument and running the
		// handler at the end; both are hooks now, and this arm exists only to
		// install them.
		in.installCallValue(ctx, unit, depth)
		fn, _ := php.LookupBuiltin(name)
		value, err := fn(ctx, args)
		if err != nil {
			return php.Undef(), true, in.materialize(ctx, unit, err)
		}
		return value, true, nil
	case "php2go\\internal\\validate_callback":
		if len(args) < 3 {
			return php.Undef(), true, Raise("Error", "php2go internal validate_callback requires a callback, name and position")
		}
		publicName := args[1].Deref().String()
		position := args[2].Deref().Int()
		reason := in.callableReason(ctx, unit, args[0])
		if pending := ctx.TakePendingError(); pending != nil {
			return php.Undef(), true, in.throwChained(ctx, unit, pending, "TypeError",
				"%s(): Argument #%d must be a valid callback, (null)", publicName, position)
		}
		if reason != "" {
			return php.Undef(), true, in.throwError(ctx, unit, "TypeError",
				"%s(): Argument #%d must be a valid callback, %s", publicName, position, reason)
		}
		return php.Bool(true), true, nil
	case "usort", "uasort", "uksort":
		// The body is in runtime/stdlib so compiled code has it too; the hook
		// is the one thing it needs from here.
		in.installCallValue(ctx, unit, depth)
		fn, _ := php.LookupBuiltin(name)
		value, err := fn(ctx, args)
		return value, true, err

	case "php2go\\internal\\sort_entries":
		// The comparator machinery is in runtime/stdlib; the hook is the only
		// thing it needs from here. usort and its siblings are PHP now and call
		// this.
		in.installCallValue(ctx, unit, depth)
		fn, _ := php.LookupBuiltin(name)
		value, err := fn(ctx, args)
		return value, true, err

	case "call_user_func":
		// The compiler folds a literal call_user_func("f", ...) into a direct
		// call, so what reaches here is the dynamic form: a callable held in a
		// variable, a [target, method] pair, or a Closure.
		if len(args) == 0 {
			return php.Null(), true, nil
		}
		// A named argument call_user_func has no parameter for is the
		// *callee's*: `call_user_func("f", b: 0)` reports `Unknown named
		// parameter $b` only if f has no $b. They arrive as this call's
		// leftovers, and are consumed here rather than by any frame.
		forwarded := unit.forwardedNamed
		unit.forwardedNamed = nil
		value, err := in.callValueNamed(ctx, unit, "call_user_func", args[0], args[1:],
			forwarded, depth)
		return value, true, in.materialize(ctx, unit, err)

	case "forward_static_call", "forward_static_call_array":
		// The same call call_user_func makes, except that the late static
		// binding travels with it: `static::` inside the callee still means
		// whatever the *calling* method's did.
		//
		// Only the singular form refuses outside a class. The array form has no
		// such check in the engine and simply calls -- an asymmetry, but the
		// engine's, and the probe that found it is the reason this says so.
		if name == "forward_static_call" && unit.callingCalledScope(ctx) == nil {
			return php.Undef(), true, in.materialize(ctx, unit, Raise("Error",
				"Cannot call %s() when no class scope is active", name))
		}
		if len(args) == 0 {
			return php.Null(), true, nil
		}
		forwarded := args[1:]
		var byName []namedArgument
		if name == "forward_static_call_array" {
			forwarded = nil
			if len(args) > 1 && args[1].Deref().Kind() == php.KindArray {
				// A string key names a parameter here as much as in
				// call_user_func_array, and a positional element after one is
				// refused -- forward_static_call_array does *not* accept the
				// names (see namedVariadicCallees), but it still checks the
				// order before it gets that far.
				var outOfOrder error
				afterNamed := false
				source := args[1].Deref().Array()
				source.Each(func(k php.Key, v php.Value) bool {
					if !k.IsInt {
						afterNamed = true
						byName = append(byName, namedArgument{name: k.String(), value: v.Copy()})
						return true
					}
					if afterNamed {
						outOfOrder = Raise("Error",
							"Cannot use positional argument after named argument")
						return false
					}
					forwarded = append(forwarded, v)
					return true
				})
				if outOfOrder != nil {
					return php.Undef(), true, in.materialize(ctx, unit, outOfOrder)
				}
			}
		}
		value, err := in.forwardStaticCall(ctx, unit, name, args[0], forwarded, byName, depth)
		return value, true, in.materialize(ctx, unit, err)

	case "call_user_func_array":
		if len(args) < 2 {
			return php.Null(), true, nil
		}
		list := args[1].Deref()
		if list.Kind() != php.KindArray {
			return php.Undef(), true, in.materialize(ctx, unit, Raise("TypeError",
				"call_user_func_array(): Argument #2 ($args) must be of type array, %s given",
				phpTypeName(list)))
		}
		// An element that is already a reference is passed as one, so a
		// by-reference parameter writes back into whatever it aliases.
		// Dereferencing here would quietly make every such call by value.
		// An element that is already a reference is passed as one, so a
		// by-reference parameter writes back into whatever it aliases. Each
		// dereferences, so the element has to be read raw to tell.
		var passed []php.Value
		var byName []namedArgument
		var outOfOrder error
		afterNamed := false
		source := list.Array()
		source.Each(func(k php.Key, v php.Value) bool {
			if !k.IsInt {
				// A string key names a parameter, exactly as it does in the
				// form the compiler folds. Which slot it fills is settled once
				// the callee is known, which is inside callValueFor.
				afterNamed = true
				byName = append(byName, namedArgument{name: k.String(), value: v.Copy()})
				return true
			}
			if afterNamed {
				outOfOrder = Raise("Error",
					"Cannot use positional argument after named argument")
				return false
			}
			if raw, ok := source.Raw(k); ok {
				if _, isRef := raw.RefOf(); isRef {
					passed = append(passed, raw)
					return true
				}
			}
			passed = append(passed, v)
			return true
		})
		if outOfOrder != nil {
			return php.Undef(), true, in.materialize(ctx, unit, outOfOrder)
		}
		value, err := in.callValueNamed(ctx, unit, "call_user_func_array", args[0], passed, byName, depth)
		return value, true, in.materialize(ctx, unit, err)

	case "array_map":
		// The body is in runtime/stdlib so compiled code has it too; the hook
		// is the one thing it needs from here.
		in.installCallValue(ctx, unit, depth)
		fn, _ := php.LookupBuiltin("array_map")
		value, err := fn(ctx, args)
		return value, true, in.materialize(ctx, unit, err)

	case "array_reduce":
		// The body moved to runtime/php so compiled code has it too; what
		// stayed here is the one thing it needed the interpreter for, supplied
		// as the CallValue hook. See runtime/php/callable.go.
		in.installCallValue(ctx, unit, depth)
		fn, _ := php.LookupBuiltin("array_reduce")
		value, err := fn(ctx, args)
		return value, true, err

	case "array_walk":
		// The body moved to runtime/stdlib so compiled code has it too; the
		// hook and the callback check are the only things it needed from here.
		in.installCallValue(ctx, unit, depth)
		fn, _ := php.LookupBuiltin("array_walk")
		value, err := fn(ctx, args)
		return value, true, err

	case "array_filter":
		// The body moved to runtime/stdlib, as array_walk's did.
		in.installCallValue(ctx, unit, depth)
		fn, _ := php.LookupBuiltin("array_filter")
		value, err := fn(ctx, args)
		return value, true, err
	}
	return php.Undef(), false, nil
}

// arrayMap applies a callback across one or more arrays. With several arrays it
// walks them in parallel, padding the short ones with null; with a null callback
// it zips them instead.
// installCallValue lets a shared builtin invoke a PHP callable. The depth is
// the caller's, so a callback entered from a builtin is one frame deeper than
// the builtin's own call site rather than starting a new stack.
func (in *Interp) installCallValue(ctx *php.Ctx, unit *Unit, depth int) {
	ctx.SetCallValue(func(c *php.Ctx, callable php.Value, args []php.Value) (php.Value, error) {
		return in.callValue(c, unit, callable, args, depth)
	})
	// The other half: deciding whether a value *can* be called, which a shared
	// builtin asks about its callback argument before invoking it. Asked
	// quietly, because the deprecated spellings are reported by whoever
	// resolves the callable for real and reporting them twice would double the
	// diagnostic.
	ctx.SetCallableReason(func(c *php.Ctx, callable php.Value) string {
		return in.callableReasonQuiet(c, unit, callable, true)
	})
	// Opening through a wrapper the program registered: the registry is the
	// Ctx's, but the wrapper is a class and instantiating one needs the table.
	ctx.SetUserStreamOpener(func(c *php.Ctx, wrapper *php.StreamWrapper,
		path, mode string, options int64, context php.Value) (php.Value, error) {
		value, _, err := in.openUserStream(c, unit, wrapper, path, mode, options, context, depth)
		return value, err
	})
}
