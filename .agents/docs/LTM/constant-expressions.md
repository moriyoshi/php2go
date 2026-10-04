# Constant Expressions and Engine State Across Resets

## Summary

Some initializers cannot be folded when a file is compiled, so the engine keeps
them as a `zend_ast` and evaluates them on first use. php2go harvests that AST —
a read-only walk, unlike the three mutating experiments that corrupted memory —
and evaluates it in Go. Doing so also exposed a use-after-free in `Engine.Reset`
that had nothing to do with constants and everything to do with an engine cache
nothing was clearing.

## Key facts

| Fact | Why it matters |
|---|---|
| An unfoldable initializer becomes `IS_CONSTANT_AST` in the zval | Harvesting it needs `Z_ASTVAL_P` plus kind/attr/child accessors, all read-only |
| `zend_compile_const_expr_const` rewrites `ZEND_AST_CONST` into `ZEND_AST_CONSTANT` | The node carries the *resolved* name and `IS_CONSTANT_UNQUALIFIED_IN_NAMESPACE` (`0x800`) in `attr` |
| There is no greater-than in the language | `ZEND_AST_GREATER`/`GREATER_EQUAL` are separate node kinds; evaluate as smaller-than with the operands swapped |
| Property fetch in a constant expression is enum-only (PHP 8.3) | Anything else that is an object is an `Error`; a non-object warns and reads null |
| `new` in an initializer is per-evaluation (PHP 8.1) | It runs the constructor, so evaluation needs an error channel |
| `static $x = <expr>` is not in the static-variable table since PHP 8.3 | It compiles to `BIND_INIT_STATIC_OR_JMP`, the initializer's oplines, then `BIND_STATIC` **with op2** |
| An enum case's backing value can be an expression | `ZEND_AST_CONST_ENUM_INIT`'s `child[2]` is not necessarily a `ZEND_AST_ZVAL` |
| An interned class name carries a **CE cache** slot | `zend_lookup_class_ex` reads it before the class table; deleting the class does not clear it |

## Details

### Evaluation has three outcomes, not two

A value; an unresolvable shape; a PHP error. Collapsing the last two is the
tempting mistake and it is the dishonest one: a class constant whose initializer
the harvest could not read would be reported as `Undefined constant Foo::A`,
which is a plausible PHP error and silently raises the pass rate. `evalExpr`,
`constantLiteral`, `exprClassName` and `classConstant` all return
`(php.Value, bool, error)` for exactly this reason, and `classConstant`
distinguishes "not declared" (`false, nil`) from "not readable"
(`false, *UnsupportedError`).

Errors raised this way come out of `Raise(...)` as a `*phpError` and must go
through `in.materialize` before `frame.raise` sees them, or a `catch` that should
have caught them does not.

### Undefined constants are the program's error

`function f($a = bar)` inside a namespace throws `Undefined constant "Foo\bar"`,
with the same global-namespace fallback `FETCH_CONSTANT` performs. The fallback
is decided by the harvested node's `attr`, not by whether the name contains a
backslash. `Nope::A` where `Nope` does not exist is `Class "Nope" not found`.

### Diagnosing a missing node kind

An unresolved node carries its numeric AST kind into the IR, so a corpus run
reports `constant initializer for A_prop (ast:513)`. Map the number with the
enum in `Zend/zend_ast.h`, remembering the `<< ZEND_AST_NUM_CHILDREN_SHIFT` and
`ZEND_AST_IS_LIST_SHIFT` bases. This turned "some constants do not work" into a
complete list in one run.

### The CE cache outlives `Engine.Reset`

An interned class-name `zend_string` flagged `IS_STR_CLASS_NAME_MAP_PTR` has a
per-request slot holding the last class entry looked up under that name, and
`zend_lookup_class_ex` returns it before consulting `CG(class_table)`. Removing
the class from the table does not clear the slot, so the next script declaring
the same name got the freed entry.

Only a runtime by-name lookup populates it. `new Foo` compiled in the same
op_array uses that op_array's run-time cache, which dies with it — which is why
the bug needed a *deferred class constant* (`const A = self::B;` with `B`
declared later) to reproduce at all.

`php2go_reset_user_symbols` now clears the slot before destroying the entry.

Detection without a debugger: after a reset, `class_exists("Foo", false)` is
`true` while `get_declared_classes()` does not list `Foo`.

## Files

- `internal/zend/zend.go` — `php2go_zval_ast`, `php2go_ast_kind`/`_attr`/
  `_children`/`_child`/`_zval`, `convertAST`, `convertClassConstants`,
  `php2go_enum_case_backing_ast`, `php2go_reset_user_symbols`.
- `runtime/interp/constexpr.go` — `evalExpr` and everything it dispatches to.
- `runtime/interp/class.go` — `classConstant`.
- `runtime/interp/enum.go` — `enumCase`, `enumStaticCall`.
- `runtime/interp/closure.go` — `staticSlot`, `setStatic`.
- `third_party/php-src/Zend/zend_ast.h`, `zend_types.h` (`ZSTR_*_CE_CACHE`),
  `zend_execute_API.c` (`zend_lookup_class_ex`).

## Test coverage

`runtime/interp/differential_test.go` covers deferred constants, `self`/`parent`
scopes, enum-case property fetch, `new` in a parameter default and a static
variable, deferred enum backing values, the backed-type mismatch, and undefined
constants in initializers.

## What the compiler folds is what the evaluator never sees

`zend_ct_eval_binary_op` folds a constant expression **unless doing so would
raise a diagnostic**. So `7 % 2` arrives as a literal and `1 % 1.5`, which owes
a precision deprecation, arrives as an AST. The consequence for testing is
sharp: the evaluator's operator coverage is exercised only by the expressions
that *warn*, and an ordinary-looking case proves nothing about it. php2go's
evaluator concatenated for every opcode it had no case for, and `1 % 1.5`
produced the string `"11.5"` for a long time without any simpler case noticing.

Both the evaluator and the runtime compound-assignment path now go through
`Interp.applyBinary`, and an operator with no entry is an `UnsupportedError`.

## An evaluated constant is cached on the declaring class

The engine evaluates an unfoldable initializer once and keeps the result, so
reading the same constant through a subclass does not evaluate it again -- which
is observable whenever the evaluation reports something. `Unit.constantValues`
is keyed by the *declaring* class and the name, since an inherited constant is
flattened into every subclass's table.

## An array literal evaluates its key before its value

Even though the AST holds the key second. Only visible when both sides fail:
`[UNKNOWN_KEY => UNKNOWN_VALUE]` reports the key.


## Pitfalls

- **Check that the run compiled.** Twice during this work a crash appeared to be
  fixed when the test binary had merely failed to build; `go test` prints `FAIL`
  either way and the tail of the output looks similar.
- A layout-sensitive crash is memory corruption, not a heisenbug to work around.
  Adding an unrelated statement made this one vanish; that hid it, and it came
  straight back when the statement was removed.
- `gdb -batch -ex run -ex bt` on the test binary names the C frame immediately.
  Reach for it before bisecting by hand.
- Reasoning about which AST node a syntax produces has been wrong repeatedly.
  Dump the harvest (`php2harvest`) or read the node kind out of the gap message
  rather than inferring from `zend_compile.c`.

## A class resolves its initializers when it is first used

`zend_update_class_constants` resolves a class's deferred constants **and its
property defaults** when the class is first *used* — not when each one is read.
`Interp.updateClassConstants` does the same, called from `ZEND_NEW`, and caches
the results on the Unit (`updated`, `defaults`) since the harvest is shared.

The timing is observable: an initializer naming an undeclared class autoloads,
and an autoloader can print and can throw. Before this, a property default the
compiler could not fold — `public $prop = A::C;` with `A` not yet declared —
stayed **null**.

**Mark the class resolved only once the walk finishes.** Marking it up front, to
stop an initializer re-entering, breaks the case the engine explicitly supports:
in `gh10709_2` the autoloader triggered by resolving `B::$prop` itself does
`new B()`, and that inner instantiation has to resolve the default rather than
find a half-finished class already flagged done. Cycles are bounded by
`constDepth` and the call depth instead.
