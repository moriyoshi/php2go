package lower

import "strings"

// Whether a body hands back an object nobody else holds.
//
//	class Foo { static function make() { return new Foo; } }
//	var_dump(spl_object_id(Foo::make()));   // 1
//	var_dump(spl_object_id(Foo::make()));   // 1 again
//
// The returned object is a statement temporary: `var_dump` keeps nothing, the
// caller stores it nowhere, so PHP frees it at the end of the statement and the
// next call takes the same handle. `DisposableTemporaries` could not claim it
// because it only recognises a `new` or a `clone` as the definition of a
// disposable value -- a **call** that produces one was invisible.

// ReturnsFreshObject reports whether every object this body returns was made
// here and kept nowhere else.
//
// Conservative: a body with no `ret` of a value, one that returns something it
// did not make, or one whose returned object is also stored somewhere, all
// answer false. Being wrong that way costs a handle number; being wrong the
// other way frees an object the caller is still holding.
func ReturnsFreshObject(fn *Function, keepsArg func(callees []string, param int) bool,
	leaksThis func(method string) bool, argKept func(method string) bool) bool {
	if fn == nil {
		return false
	}
	text := fn.String()
	for _, name := range escapeOpaque {
		if strings.Contains(text, `"`+name+`"`) {
			return false
		}
	}
	if strings.Contains(text, "dynvar") || strings.Contains(text, "yield") {
		return false
	}
	ea := &escapes{fn: fn, keepsArg: keepsArg, leaksThis: leaksThis,
		argKept: argKept, allowReturn: true}
	returned := 0
	for _, b := range fn.Blocks {
		if b.Dead {
			continue
		}
		term := strings.TrimSpace(b.Term)
		rest, ok := strings.CutPrefix(term, "ret ")
		if !ok {
			continue
		}
		v := strings.TrimSpace(rest)
		// `ret null` and `ret 1` hand back no object at all, so they neither
		// help nor hurt: a body whose every other return is fresh is still
		// fresh. `{main}` returning 1 is the shape that makes this matter.
		if v == "" || nonObjectLiteral(v) {
			continue
		}
		// **Peel the return-type check.** `function t(): self { return new Foo; }`
		// returns the *checked* value, whose definition is the check and not
		// the `new` -- so the summary saw a returned value it had not made and
		// refused every typed factory.
		def := definition(fn, v)
		for def != nil {
			src, ok := verifySource(*def)
			if !ok {
				break
			}
			v = src
			def = definition(fn, v)
		}
		if def == nil || !makesFreshObject(*def, leaksThis) {
			return false
		}
		if !ea.value(v) {
			return false
		}
		returned++
	}
	return returned > 0
}
