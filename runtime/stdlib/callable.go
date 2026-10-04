package stdlib

import (
	"strings"

	"github.com/moriyoshi/php2go/runtime/php"
)

// `is_callable`, which asks the same question a builtin asks about its callback
// argument and answers it differently in two places -- both read back from the
// engine rather than assumed:
//
//   - **`__call` makes any method name callable.** `is_callable([new C, "nope"])`
//     is true when C declares `__call`, where the callback-argument check on the
//     same value is a TypeError.
//   - **the name is reported even when the answer is false**, and for shapes
//     that are not callables at all: 42 gives "42", null gives "", an array
//     gives "Array".
//
// The verdict comes from the same `CallableReason` hook the argument check uses,
// so there is one decision procedure and this is the second question asked of
// it.

func init() {
	php.RegisterBuiltin("is_callable", func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		if len(args) == 0 {
			return php.Bool(false), nil
		}
		value := args[0].Deref()
		syntaxOnly := len(args) > 1 && args[1].Deref().Bool()

		// The third argument is by reference and is written whatever the
		// verdict, so it is filled before the answer is decided.
		if len(args) > 2 {
			if ref, ok := args[2].RefOf(); ok {
				ref.Set(php.String(callableName(value)))
			}
		}

		// **An incomplete object**: alone it is simply not callable; as the
		// object of a pair, resolving the method is its get_method handler's
		// Error (probed). The syntax-only form resolves nothing.
		if o := value.Object(); value.Kind() == php.KindObject && php.IsIncomplete(o) {
			return php.Bool(false), nil
		}
		if php.IncompleteCallable(value) && !syntaxOnly {
			first, _ := value.Array().Get(php.IntKey(0))
			return php.Value{}, php.IncompleteCallError(ctx, first.Deref().Object())
		}
		// `$syntax_only` skips *resolution*, and an object has nothing to
		// resolve: `is_callable(new stdClass, true)` is false and
		// `is_callable(new D, true)` is true, so the flag does not reach it.
		if syntaxOnly && value.Kind() != php.KindObject {
			return php.Bool(callableShape(value)), nil
		}
		// **A keyword class is deprecated whatever the verdict**:
		// `is_callable("static::m")` and `is_callable(["self", "m"])` both
		// report `Use of "static" in callables is deprecated`, as the check a
		// call makes does.
		//
		// **Only a keyword that resolves**, though: `['parent', 'm']` from a
		// class with no parent, or `['self', 'm']` with no class scope, is
		// false without the deprecation (probed; traits/bug76773-deprecated).
		// The reason says which, so it is asked first.
		reason, decided := ctx.CallableReasonFor(value)
		if kw := callableKeyword(value); kw != "" && !strings.HasPrefix(reason, `cannot access "`) {
			ctx.Deprecated(`Use of "%s" in callables is deprecated`, kw)
			if err := ctx.TakePendingError(); err != nil {
				return php.Value{}, err
			}
		}
		if !decided {
			// Nothing here can judge, and answering false would be a wrong
			// answer rather than a missing one.
			return php.Undef(), php.Unsupported("is_callable without a callable resolver")
		}
		return php.Bool(reason == ""), nil
	})
}

// callableShape is `$syntax_only = true`: whether the value **looks** like a
// callable, without asking whether what it names exists. A string always does;
// an array does when it is exactly a class-or-object and a method name.
func callableShape(value php.Value) bool {
	switch value.Kind() {
	case php.KindString:
		return true
	case php.KindArray:
		items := value.Array()
		if items.Len() != 2 {
			return false
		}
		target, hasTarget := items.Get(php.IntKey(0))
		method, hasMethod := items.Get(php.IntKey(1))
		if !hasTarget || !hasMethod {
			return false
		}
		switch target.Deref().Kind() {
		case php.KindObject, php.KindString:
		default:
			return false
		}
		return method.Deref().Kind() == php.KindString
	}
	return false
}

// callableName is what the third argument receives: the `Class::method`
// spelling of what the value names. It is written even for a value that is not
// a callable at all, which is why the fallback is the ordinary string
// conversion rather than an empty answer.
func callableName(value php.Value) string {
	switch value.Kind() {
	case php.KindString:
		return value.String()
	case php.KindObject:
		// A Closure reports `Closure::__invoke`, and so does anything else
		// reached through `__invoke` -- including a class that does not
		// declare one, which is how `stdClass::__invoke` comes back for a
		// value that is not callable.
		return php.DisplayName(value.Object().ClassName) + "::__invoke"
	case php.KindArray:
		items := value.Array()
		target, hasTarget := items.Get(php.IntKey(0))
		method, hasMethod := items.Get(php.IntKey(1))
		if items.Len() != 2 || !hasTarget || !hasMethod {
			return "Array"
		}
		// Both members have to be the right shape for a name to exist at all:
		// `[new C, 5]` reports "Array", not "C::5".
		if method.Deref().Kind() != php.KindString {
			return "Array"
		}
		owner := ""
		switch got := target.Deref(); got.Kind() {
		case php.KindObject:
			owner = php.DisplayName(got.Object().ClassName)
		case php.KindString:
			owner = got.String()
		default:
			return "Array"
		}
		return owner + "::" + method.Deref().String()
	case php.KindNull, php.KindUndef:
		return ""
	}
	return strings.TrimSpace(value.String())
}

// callableKeyword is `self`, `parent` or `static` when a callable names its
// class by one -- `"static::m"` or `["self", "m"]` -- and "" otherwise.
func callableKeyword(value php.Value) string { return php.CallableKeyword(value) }
