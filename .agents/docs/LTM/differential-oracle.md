# The Differential Oracle

## Summary

php2go grades itself against the linked PHP engine rather than against
hand-written expectations, and harvests the engine's tables rather than
transcribing them. Both practices have repeatedly paid off and should be the
default for any new feature. Hand-written expected output only tests what the
author believed PHP does.

## Key facts

- `runtime/interp/differential_test.go` runs every case twice — once through
  libphp via the engine, once through the interpreter — and requires an exact
  match.
- `cmd/php2eval` is the interactive form: `php2eval -c '<php>'` runs a snippet
  through both sides, `-engine` runs the engine alone.
- Opcode names, builtin signatures, constants, the builtin class hierarchy, and
  the default `error_reporting` mask all come from the linked build, recorded in
  `Manifest`.
- For the comparison to mean anything, both sides need equal **state** and equal
  **capabilities**.

## Details

### What the oracle has caught

`(int)"  8"` skipping leading whitespace; `0 == "abc"` being false under PHP 8;
`FE_FETCH_R`'s operand roles; `PHP_INT_MAX ** 2` switching to float; and
`Array to string conversion` being an `E_WARNING` in PHP 8, not a notice — that
last one against a hand-written unit test that asserted the PHP 7 level and had
to be corrected. That is the argument for the oracle in one line.

### Equal state

Anything the engine keeps across an eval but the interpreter gets fresh must be
cleared in `Engine.Reset`. Four leaks found so far:

- **User constants.**
- **`EG(symbol_table)`** — a leftover `$arr` from an earlier case made a
  *correct* interpreter look wrong.
- **`EG(error_reporting)`** — one case calling `error_reporting(0)` silenced
  later ones.
- **The SPL autoload registry** — an autoloader from one case ran in the next.
  It is a file-static in `ext/spl`, unreachable from C, so `Reset` asks PHP:
  `foreach (spl_autoload_functions() as $f) { spl_autoload_unregister($f); }`,
  and only when PHP has actually executed, tracked by `Engine.executed`.

The symptom of this family is always the same: a case that passes alone and
fails in the suite, or the reverse.

### Equal capabilities

The differential harness and `php2eval` must install `ir.EngineLoader` — without
it a case using `eval` or `include` compares working PHP against a
missing-compiler error — and must pass the harvested builtins into the manifest,
or every undefined-function case reads as a gap.

### The oracle only answers questions it is asked

`zend_eval_string` leaves an escaping exception merely *pending*: the
"Fatal error: Uncaught …" text is produced at request shutdown, which never
happens under the embed SAPI. `Eval` therefore calls `zend_exception_error`
itself. Until it did, the single most common shape of expected output in the
whole `.phpt` corpus was invisible to the oracle.

`CompileFile` has the same shape and needed the same answer. A syntax error is a
thrown `ParseError` and a bad modifier a thrown `CompileError`; neither is
printed. Reading the exception's `message` property gives the bare text without
the `Parse error:` prefix, the file or the line — and reading it *wrongly* gives
nothing at all, because `zend_read_property_ex` **returns** the zval rather than
filling the `rv` it is handed. `zend_exception_error(ex, E_ERROR)` inside the
output buffer is the only way to get the wording the tests expect, and it drains
the exception as a side effect.

Two harness limitations remain and are expected: shutdown functions do not run
on the engine side (there is no request end), and the engine's own request
shutdown at `Engine.Close` prints to the process's stdout after `php2eval` has
finished comparing. Both are why `testdata/lifetime` exists.

What the harness cannot observe at all — behaviour that only appears when a
request ends — lives in `testdata/lifetime` instead.

### Normalization is deliberate and narrow

The two sides legitimately disagree about object handle numbers (the oracle's
engine has allocated handles already) and about the file name (the engine
evaluates a string named `php2go-eval`, the interpreter runs a real temporary
file). Both are properties of the harness, not of PHP, so they are normalized
away — line numbers and everything else in the message are still compared.

### Harvest, never transcribe

Values are build- and version-specific: `E_ALL`, the `SORT_*` family, and the
exception hierarchy have all moved across releases. The default error-reporting
mask is 30719 on this build rather than `E_ALL`, because it is an ini default —
harvesting it (`Manifest.default_error_reporting`) removed the last hard-coded
`errorReportingAll = 32767`.

### The harness must be as faithful as the interpreter

`.phpt` failures are not all the interpreter's. Three separate infidelities in
`php2phpt` have been worth roughly 850 tests between them, and each looked like
hundreds of unrelated interpreter bugs. The contract with `run-tests.php` has
its own document: [phpt-harness-fidelity](./phpt-harness-fidelity.md). Before
attributing a failure, check what the real runner does.

`php2phpt -diff N` prints expected against actual, which is what makes a
mismatch diagnosable at all; before it, the only signal was a 60-character
truncated string, not enough to tell a wrong warning from a wrong value.

## Files

- `runtime/interp/differential_test.go` — the oracle and its normalizers.
- `runtime/php/engine_parity_test.go` — the value model against captured engine
  ground truth.
- `internal/zend/zend.go` — `Engine.Reset`, `Eval`, the harvest entry points.
- `internal/ir/loader.go` — `EngineLoader`, the compiler behind `include`/`eval`.
- `cmd/php2eval`, `cmd/php2phpt`, `testdata/lifetime`.

## Test coverage

The oracle *is* the coverage for behavioural work. See
[TESTING.md](../TESTING.md) for how to run it and
[QUALITY_GATE.md](../QUALITY_GATE.md) for what a change owes before it is
complete.

## Pitfalls

- Keep the metric honest. When builtin classes became harvestable,
  `new ReflectionClass` started succeeding into an object with no behaviour —
  plausible-but-wrong output in place of an honest gap. Instantiation is
  deliberately restricted to classes the runtime actually models, costing a
  passing test on purpose. See
  [unsupported-versus-php-errors](./unsupported-versus-php-errors.md).
- Expect more state leaks. When a case passes alone and fails in the suite, look
  at `Reset` before looking at the interpreter.

## Shapes the engine renders that no property table explains

Some values look like objects but are described by a `get_debug_info` handler
rather than by properties, so reproducing them means reading the handler:

| Value | Handler | Shape |
|---|---|---|
| `Closure` | `zend_closure_get_debug_info` | `name`+`file`+`line` for a written closure, `function` for a *fake* one (`f(...)`, `fromCallable`); then `static` (the `use` variables, in static-variable-table order), `this`, `parameter` (`&$x` → `<required>`/`<optional>`) |

Two ordering rules that a numeric fallback silently gets wrong:

- An enum case has **no ordering**. `E::A < E::B`, `>`, `>=` and `<=` are all
  false — `zend_objects_not_comparable` returns `ZEND_UNCOMPARABLE` (`1`), so
  both `< 0` and `<= 0` fail, and the compiler writing `a > b` as `b < a` makes
  that cover all four directions. The identical object still compares equal.
- Two objects of *different* classes are uncomparable the same way; two of the
  same class compare by properties.

And one lookup rule: a fully qualified name may be written with a leading `\`,
which `zend_lookup_class` strips. The class and constant tables are keyed
without it.

Late static binding forwards through `self::`, `parent::` and `static::` and
rebinds only for a call naming a class outright. The keyword survives into the
opline as a `ZEND_FETCH_CLASS_*` code in an unused operand.

## The oracle runs destructors

`php2go_eval` performs the two steps of `shutdown_destructors` *inside* the
output capture, after reporting any escaped exception:

```c
zend_hash_graceful_reverse_destroy(&EG(symbol_table));   /* globals, reverse order */
zend_hash_init(&EG(symbol_table), 8, NULL, ZVAL_PTR_DTOR, 0);
zend_objects_store_call_destructors(&EG(objects_store)); /* the rest, creation order */
```

Two reasons, and both matter:

- **Fidelity.** Destructor output is part of what a script prints. Without this
  the oracle grades correct destructor output as a mismatch.
- **It was a crash.** `php2go_reset_user_symbols` frees the user classes; an
  object of such a class still in the symbol table is destroyed *after* that,
  and calling `__destruct` through a freed `zend_class_entry` segfaults. An
  object with no destructor needs nothing from the class entry when it is
  freed, which is why every case before this one got away with it.

The symbol table has to be re-initialised afterwards, since the process keeps
running and later evals still need one.

## A compile error cannot be asked about, and takes its neighbours with it

An `E_COMPILE_ERROR` bails the shared engine process. The case that caused it
fails, and so does whatever ran near it -- a passing case can be knocked out by
a *new* case added elsewhere in the list, which reads as a regression in code
that did not change.

The refusals that are compile-time rather than run-time are not always the ones
you would guess. `f([0, 1][0])` where `f` takes a reference is an `Error` when
`f` was declared after the caller and an `E_COMPILE_ERROR` when it was declared
before, because early binding lets the compiler see the parameter. A case that
wants the run-time form has to declare the callee *after* the caller, exactly as
the corpus tests do.

Symptom to recognise: `engine could not evaluate ...: zend: evaluation bailed
out`, plus an unrelated case failing in the same run.

The weaker form of the same thing: an ordinary failing case can disturb its
neighbours too. A case that had passed for several runs reported
`needs anonymous class declaration` in one run where a different case was
failing, and passed again -- unchanged -- once that case was fixed. Fix the
first failure before believing the second.

## A method is named with its class

`Too few arguments to function Z::__construct(), 0 passed …` — the op_array
carries only the bare name (`__construct`), so the class has to come from the
scope the frame is running in. php2go printed the bare name for every method
arity error in the corpus until 2026-08-24, and the bug survived because no
differential case had ever called a method with too few arguments. It was found
by reading an unrelated test's expectations.

The lesson is about coverage shape, not about this message: a diagnostic built
from two sources — the declaration and the frame — needs a case that exercises
*both*, and "function" and "method" are two cases, not one.

## Finding families when clustering stops working

First-differing-line clustering separates causes well while the failures are
few and varied; past a certain point every cluster is "same shape, different
value" and it stops earning its keep. Two cheaper questions took over:

- **Count failures by directory.** `traits` at 54 pointed straight at a hole in
  the harvest (`as` and `insteadof` were never read).
- **Count failures whose output is *empty*.** 153 of 1042 produced nothing where
  PHP produced something, and nearly all expected a link-time fatal php2go does
  not raise. Grouping those by the first line they expect turns a diffuse tail
  into a list of named checks to implement.

The complementary count is worth taking at the same time: **zero** tests produce
output where PHP produces none. php2go under-reports rather than inventing, and
a future round where that number is not zero is a regression worth chasing.

## Probe a whole feature with a narrating script

The `__toString` gap (2026-08-25) was found by accident while chasing something
else. The follow-up found two more in one run, by writing a single script that
exercises **every** part of a feature with an `echo` in each accessor body, and
diffing it against the engine:

```php
class M {
  function __get($n)   { echo "get($n) ";   ... }
  function __set($n,$v){ echo "set($n) ";   ... }
  function __isset($n) { echo "isset($n) "; ... }
  ...
}
```

A script that *narrates what it calls* turns "does this work?" into a diff, and
catches the case where the right answer is reached by the wrong route — which is
exactly what a value-only comparison misses.

Worth repeating for: the serialization hooks, the iterator protocol, the
arithmetic/comparison handlers, and anything else with several entry points into
one behaviour.

## The matrix probe

The narrating probe (above) finds wrong *routes*. Its complement finds wrong
*answers*: take a representative value set and print every pairing under every
operator.

```php
$vals = [0, 1, -1, 0.0, 1.5, "", "0", "1", "abc", "1abc", null, true, false, [], [1], "10", "1e1"];
foreach ($vals as $i => $a) foreach ($vals as $j => $b) foreach ($ops as $op) ...
```

17 x 17 x 6 = 1734 answers in one run; 26 were wrong, in an area the whole
`Zend/tests` corpus does not reach (comparison corners live in `ext/standard`).
Kept as a differential case, so the whole table is checked on every run.

Worth doing for any total function over a small type set: comparison, the
arithmetic operators, the casts, `array_*` with mixed key types.

### Label every cell

A matrix probe that prints only results is unusable the moment a *diagnostic*
differs: warnings carry no operands, so one extra line shifts everything after
it and a 324-cell run reports 1039 differences. Print a marker before each cell:

```php
echo "### ", $names[$i], " ", $op, " ", $names[$j], "\n";
```

and diff **per cell**. The same run then reported the 11 that were real.

## A builtin's warning gets a "PHP Request Startup:" prefix

Raised from inside a builtin during the oracle's `eval`, a warning comes out as

    Warning: PHP Request Startup: Cannot unserialize value for virtual property T::$v

while the same program in a file produces no prefix -- which is what the corpus
expects and what the interpreter must print. A differential case therefore
cannot cover the *text* of a warning a builtin raises; only the corpus can.

Symptom: the two sides differ by exactly that prefix and nothing else.

## A diagnostic php2go raises should be greppable in php-src

`Object is not lazy or already initialized` was raised by two reflector methods
for two rounds and appears nowhere in the engine: php2go had invented both the
wording and the rule. It cost three tests, and one of them looked like a *clone*
bug because the refusal fired on the clone.

Grepping php-src for the exact text is a cheap check, and it catches the class of
mistake the differential cannot: a refusal the engine never makes is only
visible in a program that would have succeeded.

Run as a **sweep** it is worth more. Extracting every literal passed to
`Raise`/`throwError`/`fatal` (203 of them), taking each one's longest run with no
`%` directive, and grepping the engine's C left 28 candidates; feeding those to
the engine one at a time found seven real divergences, including a
`Closure::fromCallable` that validated nothing at all. Most of the 28 were false
positives -- `zend_argument_type_error` composes its message at run time, so the
familiar "must be of type X, Y given" exists nowhere as a literal.

## eval is not compiled the way a file is

`zend_compile_file` and `zend_compile_string` end an op_array differently, and
the difference is visible to a program:

| | last opline | so |
|---|---|---|
| a file | `RETURN 1` | `include` of a file that returns nothing yields **1** |
| eval'd code | `RETURN null` | `eval("1;")` yields **NULL** |

Compiling eval's argument as `"<?php " + code` through the file compiler --
which is what php2go did until round 308 -- makes every eval answer 1, and an
`$x = eval("return null;")` then holds an int. `Engine.CompileEvalString` calls
`zend_compile_string(source, name, ZEND_COMPILE_POSITION_AFTER_OPEN_TAG)`.

The position argument is load-bearing: `AT_OPEN_TAG` treats the string as being
*outside* PHP, so the program echoes its own source as inline HTML.

An explicit `return null;` or a bare `return;` answers NULL in **both** forms.
Only an op_array that never reached a `RETURN` falls back to 1, and the file
compiler already emits that 1 itself -- so there is nothing for the runtime to
substitute.
