package php

// The builtin surface: how a compiled program calls a PHP library function.
//
// It exists because of a measurement rather than a design preference. Of the
// 3,847 `Zend/tests` files whose bodies lower, **2,532 need at least one library
// function** -- 66% -- against 706 for the exception path and 315 for static
// calls. A compiled program that cannot call a builtin is not blocked on any one
// function; it has no standard library, and that is the largest single
// population in the corpus.
//
// The first-blocker table said otherwise -- `var_dump` 306 against catch-handler
// 415 -- because it reports only what stops each file *first*. Counting
// populations instead put `var_dump` at 1,893, understated six-fold. The
// ranking inverted; see TESTING.md.
//
// # Shape
//
// One function, by name, over values:
//
//	fn, ok := php.LookupBuiltin("var_dump")
//	result, err := fn(ctx, args)
//
// A builtin that needs to re-enter user code -- `var_dump` calling
// `__debugInfo`, `usort` calling a comparator -- does not take an interpreter.
// It reads a hook off the Ctx, installed by the layer that has a class table.
// That keeps this package free of anything that knows what a class is, which is
// the same boundary `ResolveTraversable` draws.

// Builtin is one PHP library function. Every one has this shape, including the
// ones that write output rather than returning it: `var_dump` answers NULL and
// writes through the Ctx, which is what PHP does.
type Builtin func(ctx *Ctx, args []Value) (Value, error)

// builtins is the table. It is package-level rather than per-Ctx because a
// function's *implementation* is not request state -- unlike the constants and
// hooks on Ctx, which are.
var builtins = map[string]Builtin{}

// RegisterBuiltin adds one. Registration happens in this package's init, so the
// table is complete before any Ctx exists.
func RegisterBuiltin(name string, fn Builtin) { builtins[name] = fn }

// LookupBuiltin finds one by the name PHP calls it. The second result is false
// for a function that is not implemented here, which a caller must report as a
// gap rather than treat as a call to nothing.
func LookupBuiltin(name string) (Builtin, bool) {
	fn, ok := builtins[name]
	return fn, ok
}

// BuiltinNames is what is implemented, for a caller that wants to decide at
// emit time whether a call site can be lowered to a builtin call.
func BuiltinNames() []string {
	out := make([]string, 0, len(builtins))
	for name := range builtins {
		out = append(out, name)
	}
	return out
}

func init() {
	RegisterBuiltin("var_dump", func(ctx *Ctx, args []Value) (Value, error) {
		for _, arg := range args {
			text, err := VarDump(ctx, arg)
			if err != nil {
				return Null(), err
			}
			ctx.Write(text)
		}
		return Null(), nil
	})
}
