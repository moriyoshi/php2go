package stdlib

import (
	"fmt"

	"github.com/moriyoshi/php2go/runtime/php"
)

// An array's **identity**, which PHP itself has no way to ask about: `===` is
// structural, so two equal-but-distinct arrays compare true and a recursion
// guard built on it reports a cycle that is not there.
//
// It is the primitive behind every userland walk that has to stop somewhere --
// `print_r`, `http_build_query` -- and it takes its argument **by reference**,
// recorded in `php.internalRefArgs`. By value it answered the id of php2go's
// own clone, so the guard never fired and a self-referencing array recursed
// until the stack gave out.
//
// It lives here rather than in the interpreter because compiled code needs it
// for the same reason: the harvested library is linked into an emitted program
// and calls this on the way.

func init() {
	php.RegisterBuiltin(`php2go\internal\array_id`, func(_ *php.Ctx, args []php.Value) (php.Value, error) {
		if len(args) == 0 || args[0].Deref().Kind() != php.KindArray {
			return php.Undef(), php.Raise("Error", "php2go internal array_id requires an array")
		}
		// The elements' identity, not the holder's: a shared array has one
		// body behind several headers (php.Array.Identity).
		return php.String(fmt.Sprintf("%p", args[0].Deref().Array().Identity())), nil
	})
}
