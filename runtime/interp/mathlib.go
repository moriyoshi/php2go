package interp

import (
	"github.com/moriyoshi/php2go/runtime/php"
	"github.com/moriyoshi/php2go/runtime/stdlib"
)

// The maths, the path helpers and the process odds and ends moved to
// `runtime/php` (see `mathfns.go` there): every one is a pure function of its
// arguments or of the environment, so nothing about them was interpreter-shaped,
// and compiled code had no route to any of them. They come back into this
// package's table through the bridge at the foot of `builtins.go`.
//
// What stays here are the two argument readers, as one-line wrappers. They have
// 368 call sites between them in this package's own tables, and the wrappers
// keep those untouched while there is still only one definition of each.

func second(args []php.Value) php.Value { return stdlib.SecondArg(args) }
