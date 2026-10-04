# Zend Operand Encoding

## Summary

`zend_op` fields hold values that are only meaningful inside the engine's own
memory. They serialize into plausible-but-wrong IR, and the wrongness is quiet —
the harvest succeeds, the interpreter runs, and the output diverges somewhere
unrelated. Several distinct instances have been found so far; expect more, and
expect to find them by measurement rather than by reading the headers.

## Key facts

| Field | Trap | Correct reading |
|-------|------|-----------------|
| `IS_CONST` operand | Not a literal index | Opline-relative **byte offset**: `RT_CONSTANT(op, op->opN) - oa->literals` |
| CV / TMP / VAR operand | Not a slot number | Byte offset into the frame, past a header whose size is a build detail: `EX_VAR_TO_NUM` |
| `op1` / `op2` jump target | Not an instruction index | Byte offset, when `ZEND_USE_ABS_JMP_ADDR` is off |
| `ZEND_VM_OP_MASK` | Not a bitmask | Enumerated field. `(flags & ZEND_VM_OP_MASK) == ZEND_VM_OP_JMP_ADDR` |
| `ZEND_VM_EXT_MASK` | Not a bitmask | Enumerated field. A bitwise AND against `ZEND_VM_EXT_JMP_ADDR` (0x03000000) also matches `ZEND_VM_EXT_LAST_CATCH` (0x02000000) |
| `extended_value` | Not only an integer payload | Also carries jump targets — `ZEND_FE_FETCH_R`'s loop exit |
| Operand *roles* | Not the source order | `ZEND_FE_FETCH_R` puts the **value** in op2 and the **key** in result, the reverse of `as $k => $v` |
| `INIT_STATIC_METHOD_CALL` op2 | Not always a name | `IS_UNUSED` means the constructor — `parent::__construct()` is compiled without one. Reading it anyway yields `Call to undefined method A::()` |
| `INIT_PARENT_PROPERTY_HOOK_CALL` op2 | The operand is unused but its payload is not | `0` selects `get`, `1` selects `set`; harvest contrasting hooks rather than interpreting the unused field generically |
| `FETCH_CONSTANT` op1 | Not unused | Carries `IS_CONSTANT_UNQUALIFIED_IN_NAMESPACE` (0x800), the only thing that distinguishes `PHP_EOL` inside a namespace from a written-out `Other\THING` |
| `FETCH_R` / `FETCH_W` `extended_value` | Not only a payload | `& ZEND_FETCH_TYPE_MASK` (0xe) selects the symbol table: `ZEND_FETCH_GLOBAL` is what `$GLOBALS["x"]` compiles to |

## SEND_VAR_NO_REF passes a reference on; it does not make one

`ZEND_SEND_VAR_NO_REF` is the opcode a *call result* is sent with. Its handler
is `ZVAL_COPY_VALUE(arg, varptr)` and nothing else: the zval travels as it
stands, so a `function &f()` result reaches a by-reference parameter as the
reference it already is.

That is not the same as what `SEND_VAR_EX` and the `_FUNC_ARG` forms do, which
is to *make* a reference out of a variable's slot. Handling NO_REF through the
same branch turns a plain value result into a reference and silences
`Only variables should be passed by reference`, which is exactly the diagnostic
the opcode exists to produce.

The `_EX` suffix here means "the compiler could not settle whether the callee
wants a reference", not "by reference".

## A frameless call carries its callee in extended_value, keyed by arity

PHP 8.4 compiles some internal calls without building a frame. The opcodes are
`ZEND_FRAMELESS_ICALL_0` through `_3`, and **none of them names a function**:

- `extended_value` is an index into the manifest's `frameless_functions` table
  (`Engine.FramelessFunctions()`), which is the only way to resolve the callee.
- The index is per **arity**, not per function: `in_array` with two arguments is
  a different slot from `in_array` with three.
- `_3` has three arguments and an opline holds two, so the third spills into a
  following `ZEND_OP_DATA` -- the same encoding as the assignment opcodes.

`ZEND_JMP_FRAMELESS` guards the fast path: op1 is the function name as written,
op2 the frameless block. If the name still resolves to the internal function the
compiler had in mind it jumps, otherwise it falls through to an ordinary
three-phase call. **Both arms write the same result slot**, so downstream sees
one value from two blocks.

Dumping `extended_value` was added to `php2harvest` for exactly this: without it
the opline listing cannot tell `FRAMELESS_ICALL_2` on `in_array` from the same
opcode on anything else, because the name lives nowhere else on the opline.

## Details

The two enumerated-field traps are the same mistake twice, and they are the ones
most likely to recur: a name ending in `_MASK` reads like a bitmask and is not.
Treat any such flag as enumerated until proven otherwise, and always
mask-and-compare rather than test for non-zero.

`$this` is a further encoding surprise, covered in
[class-model-and-harvesting](./class-model-and-harvesting.md): property and
method opcodes encode it as `IS_UNUSED`, so an ordinary operand fetch yields
undef instead of the object.

The three additions above share a shape: the compiler has already decided
something the source only implies, and recorded it in a field that looks
uninteresting. A namespace fallback is the clearest case — the tempting test is
"does the name contain a backslash", which is wrong, because a written-out
`Foo\BAR` does not fall back and an unqualified `BAR` inside `namespace Foo`
does. Only the flag distinguishes them. Confirm such a field by harvesting two
contrasting snippets with `php2harvest` and diffing the operands.

`FETCH_*_FUNC_ARG` and `SEND_FUNC_ARG` take their argument position from the
preceding `ZEND_CHECK_FUNC_ARG`'s op2, *not* from the fetch's `extended_value`.
The callee's arg-info — a user function's, or a harvested builtin's by-ref flags
— then decides whether the fetch is a read or a write.

## Files

- `internal/zend/zend.go` — the cgo harvester. The offset helpers
  (`php2go_var_slot`, the `RT_CONSTANT` pair) and the jump-target predicates live
  around lines 760-830, each with the trap recorded next to it.
- `runtime/interp/interp.go` — `ZEND_CHECK_FUNC_ARG` handling and the
  `ZEND_FE_FETCH_R` / `ZEND_FE_FETCH_RW` operand roles.

## Test coverage

`runtime/interp/differential_test.go`. When adding an opcode to the harvester or
the interpreter, write the differential case *first* and let the engine settle
the operand roles and encodings.

## Pitfalls

- Reasoning about these fields from `zend_vm_def.h` has been wrong more often
  than right. Every one of the six was found by measurement — the differential
  harness, or a targeted `php2eval` probe. See
  [differential-oracle](./differential-oracle.md).
- The values are build-specific. `Manifest` records the layout
  (`sizeof_znode_op`, absolute-jump and absolute-constant flags) precisely
  because IR is only meaningful next to the build that produced it.

## `zend_live_range.var` is not an operand

The seventh way. `zend_op.op1.var` is a byte offset the harvester converts with
`EX_VAR_TO_NUM`; `zend_live_range.var` is the *same* byte offset with the
range's **kind** packed into its low three bits (`ZEND_LIVE_MASK` is 7), and
`convertLiveRanges` was copying it through raw. Harvested for a function whose
oplines use `VAR:3`, `VAR:5`, `VAR:7`:

```
raw:        {Var:129 ...} {Var:164 ...} {Var:193 ...}
normalized: {Var:3 Kind:1} {Var:5 Kind:4} {Var:7 Kind:1}
```

129 = 128|1 = LOOP, 164 = 160|4 = NEW, 193 = 192|1 = LOOP. The kinds are
`ZEND_LIVE_TMPVAR` 0, `LOOP` 1, `SILENCE` 2, `ROPE` 3, `NEW` 4, and each is
released differently -- so the kind has to travel with the range, not be
discarded with the mask.

The tell was that the numbers were plausible: three ascending values for three
ascending slots, differing by 32 rather than by 1. A raw field that *looks*
like an index is the trap this whole document is about.

An unused operand is not synonymous with “no information”. Three independent
families now depend on that distinction: an unused receiver means `$this` for
object fetches, an unused static-method name means `__construct`, and an unused
parent-hook operand carries the get/set selector. `l.operand` returning an empty
string *and success* is particularly dangerous: callers skip their error path
and emit a syntactically valid operation with a missing class or name. Any new
reader of an operand that may be `IS_UNUSED` must ask the opcode-specific
question instead.
