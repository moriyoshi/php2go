package interp

import "github.com/moriyoshi/php2go/runtime/php"

// randomBuiltin is the request's Mt19937 functions, shared with compiled
// output (php.RandomCall).
func randomBuiltin(ctx *php.Ctx, unit *Unit, name string, args []php.Value) (php.Value, bool, error) {
	return php.RandomCall(ctx, &unit.random, name, args)
}
