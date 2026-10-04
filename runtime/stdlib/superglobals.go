package stdlib

import "github.com/moriyoshi/php2go/runtime/php"

// The two leaves a library needs to define the request's superglobals in PHP
// (runtime/php/superglobals.go says why they are defined there). Here rather
// than in the interpreter because the library is linked into compiled programs
// too, and each back-end supplies only where a global lives.
func init() {
	// register_superglobal(name, initial) defines a superglobal unless one of
	// that name is defined already, and answers whether it did.
	php.RegisterBuiltin(`php2go\internal\register_superglobal`, func(ctx *php.Ctx, args []php.Value) (php.Value, error) {
		if len(args) == 0 {
			return php.Undef(), php.Raise("Error", "php2go internal register_superglobal requires a name")
		}
		name := args[0].Deref().String()
		if name == "" || name == "GLOBALS" {
			return php.Undef(), php.Raise("Error", "php2go internal register_superglobal received an invalid name")
		}
		initial := php.Null()
		if len(args) > 1 {
			initial = args[1].Deref().Copy()
		}
		return php.Bool(ctx.DefineSuperglobal(name, initial)), nil
	})
	// auto_globals() is the just-in-time superglobals the program names, in
	// the order the engine would have created them.
	php.RegisterBuiltin(`php2go\internal\auto_globals`, func(ctx *php.Ctx, _ []php.Value) (php.Value, error) {
		out := php.NewArray()
		for _, name := range ctx.AutoGlobals() {
			out.Append(php.String(name))
		}
		return php.Arr(out), nil
	})
}
