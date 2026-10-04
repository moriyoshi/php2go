package php_test

import (
	"io"
	"testing"

	"github.com/moriyoshi/php2go/runtime/php"
)

// Defining a superglobal is first-wins, with or without a back-end's definer:
// a host that defines `$_GET` before the library's defaults keeps its value.
func TestDefineSuperglobalFirstWins(t *testing.T) {
	ctx := php.NewCtx(io.Discard)
	if !ctx.DefineSuperglobal("_GET", php.String("host")) {
		t.Fatal("first definition refused")
	}
	if ctx.DefineSuperglobal("_GET", php.String("default")) {
		t.Fatal("second definition accepted")
	}
	if v, _ := ctx.Globals.Get(php.StringKey("_GET")); v.Deref().String() != "host" {
		t.Fatalf("_GET = %v, want host", v)
	}

	stored := map[string]php.Value{}
	ctx = php.NewCtx(io.Discard)
	ctx.SetSuperglobalDefiner(func(_ *php.Ctx, name string, v php.Value) bool {
		if _, ok := stored[name]; ok {
			return false
		}
		stored[name] = v
		return true
	})
	ctx.DefineSuperglobal("_ENV", php.String("host"))
	ctx.DefineSuperglobal("_ENV", php.String("default"))
	if stored["_ENV"].String() != "host" {
		t.Fatalf("definer kept %v, want host", stored["_ENV"])
	}
	if _, inGlobals := ctx.Globals.Get(php.StringKey("_ENV")); inGlobals {
		t.Fatal("a back-end definer was bypassed")
	}
}
