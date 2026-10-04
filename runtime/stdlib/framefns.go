package stdlib

import "github.com/moriyoshi/php2go/runtime/php"

// The frame-reading library functions. Each answers about the **caller's**
// frame, which is what `CallerStack` resolves: an engine function's own frame
// is present in the interpreter and absent in compiled code, so neither of them
// can be written against a fixed offset from the top of the stack.
//
// `debug_backtrace` stayed in the interpreter, because a hidden argument is
// replaced by a SensitiveParameterValue and building one needs the object
// store.

// backtraceIgnoreArgs is the debug_print_backtrace() option bit that matters
// here: DEBUG_BACKTRACE_IGNORE_ARGS. The other one is about the `object` key,
// which this rendering does not show anyway. A fixed language constant rather
// than a build setting, so it is spelled out.
const backtraceIgnoreArgs = 2

func init() {
	php.RegisterBuiltin("func_num_args", func(ctx *php.Ctx, _ []php.Value) (php.Value, error) {
		stack := CallerStack(ctx)
		if len(stack) == 0 {
			return php.Undef(), php.Raise("Error", "func_num_args() must be called from a function context")
		}
		return php.Int(int64(len(stack[len(stack)-1].Args))), nil
	})
	php.RegisterBuiltin("func_get_args", func(ctx *php.Ctx, _ []php.Value) (php.Value, error) {
		stack := CallerStack(ctx)
		if len(stack) == 0 {
			return php.Undef(), php.Raise("Error", "func_get_args() cannot be called from the global scope")
		}
		passed := php.NewArray()
		for _, arg := range stack[len(stack)-1].Args {
			passed.Append(arg.Deref().Copy())
		}
		return php.Arr(passed), nil
	})
	php.RegisterBuiltin("func_get_arg", func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		// The enclosing function's frame is the one below this call, since the
		// call to func_get_arg itself was pushed like any other.
		stack := CallerStack(ctx)
		if len(stack) == 0 {
			return php.Undef(), php.Raise("Error", "func_get_arg() cannot be called from the global scope")
		}
		position := int(FirstArg(args).Deref().Int())
		passed := stack[len(stack)-1].Args
		if position < 0 {
			// Two bounds, two messages: the engine checks the sign before it
			// looks at how many arguments there were.
			return php.Undef(), php.Raise("ValueError", "func_get_arg(): Argument #1 ($position) "+
				"must be greater than or equal to 0")
		}
		if position >= len(passed) {
			return php.Undef(), php.Raise("ValueError", "func_get_arg(): Argument #1 ($position) "+
				"must be less than the number of the arguments passed to the currently executed function")
		}
		return passed[position].Deref(), nil
	})

	php.RegisterBuiltin("debug_print_backtrace", func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		// (options, limit). The options bit that matters here is
		// DEBUG_BACKTRACE_IGNORE_ARGS; the other one is about the `object`
		// key, which this rendering does not show anyway.
		stack := CallerStack(ctx)
		if len(args) > 0 && FirstArg(args).Deref().Int()&backtraceIgnoreArgs != 0 {
			php.DropTraceArgs(stack)
		}
		limit := 0
		if len(args) > 1 {
			limit = int(args[1].Deref().Int())
		}
		// **The engine prints from a trace array**, which it builds and drops
		// again: a hidden argument becomes a SensitiveParameterValue there
		// and goes straight back, and that churn is what numbers the next
		// object. Without it the program's handles run one wrapper behind
		// (sensitive_parameter).
		if ctx.ReleasesHandles() {
			fetched := stack
			if limit > 0 && limit < len(fetched) {
				fetched = fetched[len(fetched)-limit:]
			}
			php.ReleaseTraceHandles(ctx, php.Arr(TraceArray(ctx, fetched, false)))
		}
		ctx.Write(php.BacktraceStringLimit(stack, limit))
		return php.Null(), nil
	})
}

// CallerStack is the stack as the caller of an engine function sees it: the
// trailing internal frames dropped, so what is left ends with the PHP function
// that made the call. See StackFrame.Internal for why this is a loop rather
// than dropping exactly one.
func CallerStack(ctx *php.Ctx) []php.StackFrame {
	stack := ctx.CallStack()
	for len(stack) > 0 && stack[len(stack)-1].Internal {
		stack = stack[:len(stack)-1]
	}
	return stack
}
