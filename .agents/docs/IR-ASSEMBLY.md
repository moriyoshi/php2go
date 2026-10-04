# IR assembly format

The textual form of the IR described in [IR-DESIGN.md](./IR-DESIGN.md).
Written 2026-08-28. **Status: specified, not implemented.**

## What a lowered body contains

The reference list. Everything below this section is the *reasoning* for one
decision or another, written when it was made; this is the current inventory.

| | |
|---|---|
| `call.intrinsic "<variant>"(args)` | **every operation on values** — arithmetic, comparison, the reads and writes, string conversion, the container and property families, the iterator protocol, the coercions |
| `br` `jmp` `ret` `rethrow` | control flow, and nothing else |
| `region rN { catch -> bbN }` | the try structure |
| `recv` `recv.variadic` | the calling convention |
| `slot.set` `store` | writing a frame slot, and writing through a place |
| `call.fn` `call.method` `call.static` `call.dynamic` `call.ctor` `new` `closure` `closure.from_call` `instanceof` `class_const` `fetch_class` `declare_*` | the **program's** own tables, which `runtime/php` does not have and should not |
| `frame.state` `frame.set_state` `frame.resumed` `fiber.*` | the CPS rewrite's state machine |
| `bind_ref` `bind_global` `bind_static` `bind_lexical` `unset` `make_ref` `separate` | binding and aliasing, which are not value operations |
| `dynvar.*` `silence_begin` `silence_end` `assertions_enabled` `frameless_available` `unsupported` | frame-level and refusal markers |
| `.line` `.file` `.params` `.returns` `.frame` | directives, not instructions |

An opcode that is a value operation and is **not** in the first row is a gap,
not a design choice; `.agents/docs/TODO.md` ranks the ones that remain.

## What it is for, and what it is not

The protobuf is the serialization format. This is a *view* of it, and it exists
for four jobs:

1. **Reading** a lowered function while debugging the lowering.
2. **Testing** — golden files for the lowering pass, so a change to it shows up
   as a diff rather than as a corpus number moving.
3. **Writing** IR by hand, so a pass can be tested without going through PHP.
4. **Diffing** two lowerings of the same input.

Job 4 is the one that constrains the syntax most, and it is why several
otherwise-nice choices below are rejected.

**Round-trip discipline:** `parse(print(ir)) == ir`, and `print(parse(text))`
is byte-identical to `text` for any text this printer would emit. Anything the
printer emits that the parser cannot read is a bug in one of them, and the test
for it is cheap — print every lowered op_array in the corpus, parse it back,
print again, compare.

## Diffability rules

These are decisions, not style preferences.

- **One instruction per line.** No wrapping, however long the operand list.
- **No column alignment.** Aligning `=` looks better and is wrong: when `%9`
  becomes `%10` every aligned line in the function shifts, so a one-value change
  produces a whole-function diff.
- **Types on definitions, never on uses.** LLVM repeats them at uses; that is
  noise here, and the def is unambiguous.
- **Line numbers as sparse directives**, not per-instruction. See below.
- **Stable value numbering** — values are numbered in definition order within a
  function, so inserting an instruction renumbers everything after it. That is
  accepted: the alternative (stable names) makes hand-written tests unreadable.
  Diff noise from insertion is real but bounded; diff noise from alignment is
  not.

## Lexical

| form | meaning |
|---|---|
| `%0`, `%1` | SSA values, numbered in definition order |
| `bb0`, `bb1` | basic blocks |
| `r0`, `r1` | exception regions |
| `$a` | a PHP variable name, where one is named |
| `"…"` | a byte string literal |
| `; …` | comment to end of line |
| `.foo` | a directive |

### Byte strings

PHP strings are byte strings, not text. A literal is written with printable
ASCII verbatim except `"` and `\`, and **everything else as `\xNN`**:

```
"abc"        "a\"b"        "\x00\xff"        "caf\xc3\xa9"
```

No `\n`, `\t` or other mnemonic escapes — one escape form, so printing is
deterministic and round-trip is trivially byte-exact. This matters more than it
looks: `Literal.string_value` is a proto3 `string` today and **cannot hold a
non-UTF-8 byte at all** (`TODO.md`). That must become `bytes` before the format
is honest, and `\xNN` is what makes the text form honest once it is.

### Types

```
i64  f64  bool
*str  *arr  *obj  *res  *ref
zval
place
```

Short forms, because they appear on every definition. `zval` is the boxed
fallback and is what lowering emits before any narrowing.

## Function

```
func <name>(<params>) -> <type> {
  <directives>
  <regions>
  <blocks>
}
```

Directives, all optional, all emitted in this order:

```
.file "/path/to/ex.php"
.lines 2-8
.params [ 0 $a "array", 1 $i "int" ]
.returns "string"
.flags strict_types static
```

`.params` and `.returns` carry the **declared** PHP types as the engine rendered
them, in quotes — they are strings, not IR types, and the distinction is the
whole point of the two lattices. `.flags` carries what the op_array recorded.

## Blocks

```
bb3(%7: zval, %8: i64) in r0:
```

A block declares its parameters and, optionally, the innermost region it belongs
to. Blocks are printed in reverse-postorder from the entry, which is stable
under insertion and puts the common path first.

## Instructions

```
[%N: <type> =] <opcode>[.<spec>] <operand>, ... [, <attr>=<value>]
```

- The result is omitted when the instruction defines nothing.
- `.<spec>` is a specialization suffix chosen by Phase D4 — `add.i64`,
  `concat.str`. **Absence means the generic form.** This makes the effect of
  specialization greppable: `grep -c '\.i64' ` over a dump says how much fired.
- Attributes are `key=value` and always trail the operands.

Effects are **not** written. They are a function of the opcode and the inferred
operand types, so writing them would duplicate `INTRINSICS.md` into every dump
and let the two disagree. A `-effects` dump mode may append them as a comment;
the canonical form has none.

### Reads and write paths are different instructions

The input distinguishes them — `FETCH_DIM_R` yields a value, `FETCH_DIM_W`
yields an indirection — and so does this IR:

```
%2:zval  = dim.get %0, %1, mode=R          ; a read
%5:place = dim.ref %0, %1, mode=W          ; a write path
           store %5, %6
```

`*.get` takes `mode=R|IS`, which picks warn-or-silent. `*.ref` takes
`mode=W|RW|UNSET|FUNC_ARG` and yields a `place`. The families are `dim`, `prop`,
`sprop` and `local`.

**`append` is not a family of its own**, and an earlier draft of this document
was wrong to list it as one. Appending shows up in four structurally different
instructions, and the difference is not spelling:

```
%5:place = call.intrinsic "dim.append"(%0), mode=W   ; a place: `$a[] = v`
           store %5, %6
%7:*arr  = call.intrinsic "array.append"(%6, %2)    ; pure: a literal being built
%8:*strbuf = call.intrinsic "strbuf.append"(%7, %3) ; pure: a rope
           call.intrinsic "write_var"($s, %9)       ; a place write: `$s .= $x`
```

Two of those **define a value** and two **write a place and define nothing**.
Which one is correct at a given site depends on the destination's ownership and
representation, not on the syntax: appending to a uniquely-owned buffer mutates
it, appending to a shared string produces a new one. See IR-DESIGN.md.

A `place` may only be consumed by `load`, `store`, `bind_ref`, `unset` and
`fetch_for_write`. It is never a block argument, never a call operand, never
stored. That is what makes "a temporary holding an indirection" unrepresentable.

### Calls carry send modes per operand

```
%9:zval  = call.fn "strlen"(byval %3)
%10:zval = call.fn "preg_match"(byval %1, byval %2, byref %4)
%11:zval = call.method %obj, "m"(byval %3, name %5 as "opt", unpack %6)
```

Modes: `byval`, `byref`, `preferref`, `unpack`, `name %v as "n"`.
`preferref` cannot be lowered faithfully until `ZEND_SEND_PREFER_REF` is
recovered in the harvest (`TODO.md`).

## Terminators

```
jmp bb4(%2)
br %5, bb6(%2), bb7
switch %5 [ 1 -> bb2, "x" -> bb3, default -> bb4 ]
ret %9
throw %9
yield %k, %v -> bb7
```

`yield` names its resumption block, which declares a parameter for the value
`send()` supplied:

```
   yield %3, %4 -> bb7
bb7(%5: zval):
```

This is why the design takes block arguments over φ nodes: a resumption edge is
an ordinary edge, and the IR commits to neither a goroutine nor a state-machine
back end.

## Regions

```
region r0 { catch -> bb7 }
region r1 parent r0 { finally -> bb9..bb11 }
```

Declared before the blocks, referenced by `in rN` on each block. A region is not
a CFG edge: throwing from any instruction in a member block goes to the
handler implicitly. That is the whole reason for regions — 44 of the intrinsic
rows can throw, and making each an explicit edge would put one instruction in
every block.

`finally` keeps `finally_call` / `finally_ret` as real instructions rather than
being lowered to Go's `defer`, because it can replace the try's return value and
swallow an in-flight exception.

## Lines

A diagnostic names the line it came from, so the line is **semantics, not debug
information**. It is carried as a sparse directive with current-line semantics:

```
.line 4
  %2:zval = dim.get %0, %1, mode=R
  %3:zval = concat %2, %4
.line 6
  ret %5
```

Per-instruction line suffixes were rejected on the diff rule: renumbering a
source file would then touch every line of every dump, where a directive touches
one line per run of instructions.

## Worked example

Real, not invented — this is `php2harvest`'s output for

```php
function f(array $a, int $i): string {
    try { return $a[$i] . "!"; }
    catch (Throwable $e) { return ""; }
}
```

lowered by hand into the format:

```
func f -> zval {
  .file "/…/ex.php"
  .lines 2-8
  .params [ 0 $a "array", 1 $i "int" ]
  .returns "string"

  region r0 { catch -> bb1 }

bb0 in r0:
  .line 2
  %0:zval = recv 0
  %1:zval = recv 1
  .line 4
  %2:zval = dim.get %0, %1, mode=R
  %3:*str = const "!"
  %4:zval = concat %2, %3
  %5:zval = call.intrinsic "coerce_return"(%4)
  ret %5

bb1(%6: zval):
  .line 5
  ; the caught exception arrives as a block argument; $e is %6
  .line 6
  %7:*str = const ""
  %8:zval = call.intrinsic "coerce_return"(%7)
  ret %8
}
```

Four things this example pins down.

**A function has no SSA parameter list.** Parameters arrive through `recv`,
which is where the declared type is checked and a default supplied, so `recv`
defines the value and `.params` is metadata. Writing them as block arguments to
the entry *and* keeping `recv` would define each twice.

**`coerce_return` defines a value.** `ZEND_VERIFY_RETURN_TYPE` has no result
operand in the opline stream — it verifies in place. But the check *coerces*
(`int → float` widens), so in SSA it must be a definition, or the widening is
lost and `float(1)` prints as `int(1)`. It is an intrinsic call because coercing
is an operation with diagnostics of its own; it was an opcode called
`verify_return` that verified nothing, with the coercion hidden in the `ret`.

**The caught exception is a block argument**, exactly like a resumption value.
`ZEND_CATCH` writes it into a CV; here the edge carries it, and the CV
disappears unless something takes its address.

**Two blocks are absent, and that is the point.** The harvest has eleven
oplines; this has ten instructions in two blocks. Dropped: a `ZEND_JMP` after
the `return` in the try, and the implicit `VERIFY_RETURN_TYPE` + `RETURN` tail at
lines 9-10. Both are unreachable here because every path returns, and the engine
emits them regardless. **A dump that still contains them is a bug in the
lowering, not a faithful rendering.**

### There is no instruction for the fall-off-the-end check

The engine emits `VERIFY_RETURN_TYPE` with an **unused op1** where control would
fall off the end of a body. For any declared type but `void`/`never`, *arriving
there is the error* — there is nothing to check, and there is no value to
define.

This used to lower to a `verify_return.none` instruction that rendered to
nothing at all. An instruction with no operands, no result and no output is not
an instruction; the emitter raises the error from the declared type at the
`ret`, and the IR says nothing because there is nothing to say.

## Open

1. **Do `mode=` attributes belong on the opcode instead** — `dim.ref.w` rather
   than `dim.ref … mode=W`? Suffixes are greppable and shorter; attributes are
   uniform. Leaning attributes, since specialization already owns the suffix.
2. **How are folded array literals printed?** A `const` of a large nested array
   on one line defeats the one-instruction-per-line rule. Probably a pooled
   `.data` section referenced by index — the one place a literal pool earns its
   keep.
3. **Is the region table printed per function or per block only?** Both today,
   which is redundant; the declaration is needed for handler targets and the
   `in rN` for membership, but membership could be derived.

## `call.intrinsic`

An operation on values is rendered as a **call**, because that is what it is:

	%2:zval = call.intrinsic "add.int"(%1, 1)
	%3:zval = call.intrinsic "concat"($s, "x")
	%1:zval = call.fn "helper"(byval $who, byval 2)

The callee is the intrinsic's variant name as a quoted string in the first
operand; the arguments are the parenthesised list, in `Inst.ArgList`, exactly as
`call.fn` spells its own. Intrinsic arguments carry **no send mode** -- no
`byval`/`byref`/`preferref` -- because an intrinsic has no PHP calling
convention to vary.

This spelling matters more than it looks. Two earlier forms were rejected:

	%2:zval = add $a, 1                          an opcode
	%2:zval = add $a, 1, intrinsic=add.int       an opcode with a label
	%2:zval = call.intrinsic "add.int", %1, 1    a call in name only

The last one still reads as an opcode whose first operand happens to be a
string. An intrinsic is a *function* -- an essential builtin that drives the
runtime -- and the IR text should not have to be explained before it says so.

## Forming the call is lowering; choosing the variant is not

Two steps, and only the second is optional:

| | who runs it | optional? |
|---|---|---|
| **IntrinsicCalls** -- an operation becomes `call.intrinsic "<general>"(...)` | `lower.Lower`, before it returns | no |
| **Specialize** -- the call's variant narrows to `add.int` | the consumer | yes |

So **every** `.ir` golden is in call form, because the IR is: there is no such
thing as a lowered function with an `add` opcode in it. Skipping `Specialize`
leaves correct code that calls the general variant, which is what "optional"
should mean.

These were one pass at first. That made the *call form itself* look optional and
left every golden showing bare operators, so the IR the docs described and the
IR the goldens recorded were different languages -- which is exactly the
complaint that prompted the split.

`internal/lower/testdata/specialized.ir` is the golden for the second step: the
same shapes with narrowed variant names.

## A compound assignment is three instructions

`$log .= $r` is a read, a call and a write:

	%8:zval = call.intrinsic "concat"($log, $r)
	call.intrinsic "write_var"($log, %8)

and over a place -- an array element, a property, a static property -- the read
is explicit, because a place is not a name and fetching it twice would evaluate
the container twice:

	%3:zval = place.get %2
	%4:zval = call.intrinsic "add"(%3, 1)
	store %2, %4

It arrives from the harvester as one `assign_op $log, $r, op=concat`, which the
builder-promotion pass reads, and `IntrinsicCalls` expands afterwards.

## A read is an instruction

Reading a variable warns when it is unset and dereferences a reference when it
is bound, so it is an operation and the IR says so:

	%15:zval = call.intrinsic "read_var"($a)
	%16:zval = call.intrinsic "read_var"($b)
	%2:zval  = call.intrinsic "add"(%15, %16)

The read takes the **place alone**. `php.ReadVar` also needs the variable's name
-- the warning says which variable was undefined -- but `$a` already says what it
is called, so the IR carried it twice at first: `read_var($a, "a")`. A
derivable copy is a copy, and nothing was keeping the two honest. The catalogue
describes it instead, as the `"var"` argument shape: one IR operand, two Go
arguments.

The silent form is a **different intrinsic**, not a flag: `read_var` warns and
`read_var.silent` is pure, because `$q ?? 5` asks whether the variable is set
and warning that it is not would answer it twice.

A place used as a **target** is not read -- `write_var`, `bind_ref`, `slot.set`,
`unset`, `dim.ref` -- and neither is a compiler temporary. Inventing a read
there invents a diagnostic: `$a[0] = 1` on an unset `$a` autovivifies silently.

`lower.ExplicitReads` runs inside `Lower`, so every consumer sees the reads.

## Places are promoted by default

`lower.Lower` runs mem2reg, so a lowered body carries its locals as SSA values
and its merges as block parameters. `SkipMem2Reg` turns it off, which is how a
measurement separates "this pass broke it" from "it was already broken".

	bb0: br %0, bb1, bb2(1)
	bb1: jmp bb2(2)
	bb2(%4: zval):
	     %1:zval = call.intrinsic "add"(%4, 10)

What stays a place: anything aliased, unset, captured, or addressed; a place
readable before it is written; a store inside a `try`; a body that suspends; and
**the whole file body**, whose locals are globals.

## Writing a place is three operations

Writing a place is three operations, and after lowering each says which:

	call.intrinsic "write_var"($x, %1)   assignment: through a reference, and copies
	call.intrinsic "bind_var"($x, %0)    a by-reference parameter arriving
	slot.set $__iter5, %1                a frame slot -- an iterator, a rope buffer

`slot.set` stays an opcode because it is not a PHP operation: nothing in
`runtime/php` knows what an iterator slot is. It is the one write with no
intrinsic behind it, and the name says so.

`lower.StoreOf` is the only thing that should recognise a store; every analysis
asking "where is this place written" goes through it, because each time a store
changed spelling the analyses that matched the old one stopped finding any --
silently, since "found nothing" and "there is nothing" look identical from
inside.

## A terminator is control flow and nothing else

	br %7, bb5, bb3        jmp bb2(%3)        ret %1        rethrow

Two operations used to hide in there. `br iter.valid $it` tested the iterator
and `br.null %v` tested for null, which made the one step of the loop protocol
and the one part of `??` that are *operations* look like kinds of branch. They
are `iter.valid` and `is_set` now, and the branch takes the value each produced.

## The iteration cursor is a value

	%1:zval = call.intrinsic "iter.new"(%0)
	call.intrinsic "write_var"($__iter5, %1)
	%2:bool = call.intrinsic "iter.valid"($__iter5)

`iter.new` returns an **internal object** whose `Object.Internal` holds the
cursor -- the mechanism a Generator already uses -- so the handle is an ordinary
`php.Value` and every step of the protocol takes it as a plain argument. It
carries no handle ID and a class name no program can spell, because `foreach`
never exposes its iterator.

**`iter.next` returns the advanced state**, so the cursor is a loop-carried
value with a def-use chain rather than a hidden thing each step reaches into:

	br %2, bb1(%1, 0), bb3(%1, 0)
	bb1(%14: zval, %16: zval):
	  %8:bool = call.intrinsic "iter.valid"(%14)
	bb5:
	  %7:zval = call.intrinsic "iter.next"(%14)
	  jmp bb1(%7, %3)

There is still one cursor -- stepping really does mutate it -- but the IR
describes the stepping instead of merely mentioning it. A value that means
something different after each call is not single assignment.

It was a `*php.Iter` before, which is not a `php.Value`, and that cost a
representation type of its own, a frame slot only it could use, and an argument
shape that passed it through unconverted. The string builder is the last thing
still shaped that way.
