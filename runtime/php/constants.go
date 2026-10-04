package php

import "strings"

// The constant builtins. They live here rather than with the general library
// surface because all three answer from `Ctx.constants`, which is request state
// -- a constant defined by one script must not leak into the next.

func init() {
	RegisterBuiltin("define", func(ctx *Ctx, args []Value) (Value, error) {
		if len(args) < 2 {
			return Null(), nil
		}
		name := args[0].String()
		// A class constant is declared in its class, never defined: the
		// name is refused outright, `::` alone included. Measured.
		if strings.Contains(name, "::") {
			return Value{}, Raise("ValueError", "define(): Argument #1 ($constant_name) cannot be a class constant")
		}
		// **An array is stored by value**: a reference in it is resolved to
		// what it holds now, so a later write through it does not reach the
		// constant, and a recursive array is refused (probed).
		value := args[1].Deref()
		if value.Kind() == KindArray {
			resolved, ok := constantArray(value.Array(), map[*Array]bool{})
			if !ok {
				return Value{}, Raise("ValueError", "define(): Argument #2 ($value) cannot be a recursive array")
			}
			value = Arr(resolved)
		}
		// **Redefining warns and keeps the original**, rather than replacing it
		// or throwing, and answers false. Read back from the engine, because
		// "last one wins" is the natural guess and is wrong.
		// **`__COMPILER_HALT_OFFSET__` is __halt_compiler()'s**, refused by
		// its exact spelling whether or not a file claimed it; any other
		// casing is an ordinary name (halt_compiler3, probed).
		if name == "__COMPILER_HALT_OFFSET__" || !ctx.Define(name, value) {
			ctx.Warn("Constant %s already defined", name)
			return Bool(false), nil
		}
		return Bool(true), nil
	})
	RegisterBuiltin("defined", func(ctx *Ctx, args []Value) (Value, error) {
		if len(args) < 1 {
			return Bool(false), nil
		}
		_, ok := ctx.Constant(ConstantName(args[0].String()))
		return Bool(ok), nil
	})
	RegisterBuiltin("constant", func(ctx *Ctx, args []Value) (Value, error) {
		if len(args) < 1 {
			return Null(), nil
		}
		name := args[0].String()
		v, ok := ctx.Constant(ConstantName(name))
		if !ok {
			// The same Error a bare `NOPE` raises, quoted the same way, and
			// naming the constant without the leading backslash.
			return Value{}, ThrowUndefinedConstant(ConstantName(name))
		}
		return v, nil
	})
}

// ConstantName is a constant's name as the engine looks it up: one leading
// backslash is the fully-qualified spelling, not part of the name --
// `defined('\\ns\\ok')` is `ns\ok`. The diagnostics keep the name as written.
func ConstantName(name string) string {
	if len(name) > 0 && name[0] == '\\' {
		return name[1:]
	}
	return name
}

// constantArray is an array with every reference replaced by the value it
// holds, nested arrays included, for a constant. It reports false for an array
// that reaches itself.
func constantArray(a *Array, visiting map[*Array]bool) (*Array, bool) {
	if visiting[a] {
		return nil, false
	}
	visiting[a] = true
	defer delete(visiting, a)
	out := NewArray()
	ok := true
	a.EachRaw(func(k Key, e Value) bool {
		v := e.Deref()
		if v.Kind() == KindArray {
			inner, fine := constantArray(v.Array(), visiting)
			if !fine {
				ok = false
				return false
			}
			v = Arr(inner)
		} else {
			v = v.Copy()
		}
		out.Set(k, v)
		return true
	})
	return out, ok
}
