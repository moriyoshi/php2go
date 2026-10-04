# Project To-Dos

Unresolved work extracted from `.agents/docs/JOURNAL.md`. Items are grouped by
subsystem, deduplicated, and cite the journal sections that established or most
recently refined them. Re-verify an item against the current tree and corpus
before implementing it; historical test counts describe the cited measurement,
not a current promise.

## OSS release review

- [ ] Review the project name before publishing binaries linked with the
  pinned PHP source. Its PHP License 3.01 restricts products derived from PHP
  from using “PHP” in their name without PHP Group permission. The legal
  classification of this compiler and its Zend-linked binaries needs a
  maintainer decision or legal review; an MIT license on original
  php2go code does not remove upstream terms.
- [ ] Audit the exact linked PHP build for component-specific third-party
  notices before distributing binaries. `NOTICE` and `licenses/` cover the
  direct PHP, Zend, TSRM, and protobuf texts, but the PHP tree contains more
  independently licensed components. Package the notices with each binary.

## Resume point (2026-10-03)

The user chose the bounded top-level cycle collector, an explicit
profile-guided CLI mode for computed evals, and a build-tagged hybrid bundle.
All three are now in the tree. Batches 151–157 in the journal record the
implementation and focused validation. The current complete full-corpus
baseline is `m3` (4390 passed / 153 failed /
199 unsupported / 1 errored / 243 noreference). It gained three passes and
lost one against `tk2`; see journal batch 158.

Still pending:

- **Expand top-level collection beyond plain object and dynamic-array cycles.**
  The current collector proves roots and handles those edges, including a
  single observable handle. It refuses array-reference-only cycles, destructors and
  resurrection, WeakMap/WeakReference effects, suspended generators/fibers,
  opaque internals, and ambiguous multi-object handle order. Thirteen
  representative Zend cases yielded 3 passes and 10 unsupported after the
  single-handle refinement. The current broad corpus gained two GC passes
  (`gc_005`, `gc_008`) while 106 GC collection cases remain unsupported. Use
  the probes and corpus categories in
  `.agents-workspace/tmp/pending/cycle-collector-design.md`.
  Calls outside the actual entry body remain on the old refusal path, and
  `gc_status()` remains refused.
- **Profile-guided eval build surface.** `php2go profile` records strings and
  `php2gotest -eval-profile` compiles only those strings; ordinary compilation
  still refuses computed evals. A future general `php2go build` command should
  consume the same profile through a shared compiler path rather than the
  harness-only `fillProgram` path. The `php2go_hybrid` build tag now works for
  bundles by compiling eval IR for the Go interpreter, and for `php2gotest
  -emit` modules by copying lowered locals into and out of an interpreted eval
  frame. Eval-created variable names and runtime `include`/`include_once` of
  regular host files now share the generated scope and once table. Scoped eval
  inherits `$this` for global calls such as `get_class($this)`; object property
  and method operations still need generated class tables. Eval-defined global
  functions, including conditional declarations, persist across eval calls in
  one request; declarations that collide with emitted Go functions report
  unsupported, and declarations from runtime-included files remain open. Extend
  the emitted bridge for class tables, stream providers, and resumable caller
  frames. Scoped eval can call direct global engine functions
  in the harvested manifest and direct generated global functions through the
  emitted dispatcher, including named arguments to by-value parameters. Calls
  that need by-reference binding or named variadic forwarding remain
  unsupported. Direct `$GLOBALS` access and `global $name` bindings share the
  generated context's global table. `php2gotest -hybrid` now grades
  generated programs through the emitted hook; on 36 include/eval cases, 6
  pass and 30 remain unsupported after batch 163. Interpreter refusals map to the shared
  unsupported type instead of appearing as wrong output. This is not a full
  hybrid-corpus result (journal batches 158–159).
- Work lost at the previous stop: recursive SPL iterators/SplFileInfo, the
  lifetime gaps (generator handle after foreach, caught-exception handle,
  `$y = clone new D` order, `$held = make()`), and IIFE release.

## Compiler, lowering and emission

- [ ] **Static includes in compiled code: what batch 107 left.** A literal
  include at global scope and a conditional `include_once(...)` run compiled
  (JOURNAL batches 107 and 157). A changed file can take the tagged
  interpreted route. Still
  refused or wrong:
  - an include inside a **resumable** body (generator, fiber-suspending
    closure), which has no locals map; an included file that does not
    compile (gh11108: its fatal would print at the include);
  - self-inclusion of the main script (`bug35634`, `bug72594`) is refused;
    `bug35634` lost a pass in `m3`, while `bug72594` moved from failed to
    unsupported;
  - **early binding against the includer**: the harvest compiles the file
    alone, so `class B extends A` whose A the includer declared is a
    run-time `declare_class` where the engine bound it while compiling the
    include (early_binding_unreachable); and a class the includer already
    declared (`class declared twice`: bug35634,
    no_early_binding_if_already_declared);
  - an included class's deferred constant expression reports the
    includer's file (constant_expressions_exceptions_002);
  - the harvesting lives in the harness (cmd/php2gotest compileIncludes),
    which is the only producer of compiled programs today; a future
    `php2go build` wants it moved somewhere shared.
  — *source: 2026-09-30 -- batch 107*

- [x] **Phase A item 6: the escape rate, measured.** Statically, over the
  lowered IR of the whole corpus -- 3,211 `new` sites across 3,445 files that
  compile. Static rather than dynamic on purpose: a dynamic rate bounds what
  tier 1 could *theoretically* buy, this bounds what a conservative analysis can
  actually **claim**, which is the number that decides whether to build it.

  | | confined | |
  |---|---|---|
  | conservative -- a method call on the object counts as escaping | 290 | **9.0%** |
  | optimistic -- assume no method stores `$this` | 843 | **26.3%** |

  Why a site is not claimed, conservative run:

  | reason | sites |
  |---|---|
  | receiver of a method call | 777 |
  | value used by `call.intrinsic` | 588 |
  | value in terminator (returned, yielded) | 384 |
  | read value used by `call.intrinsic` | 291 |
  | value never stored | 202 |
  | passed to `call.fn` | 118 + 117 |

  **Three conclusions, and they reorder D3:**

  1. A bottom-up "does this method store `$this`?" summary is the single
     largest lever and roughly **triples** what is claimable. It is the
     precondition for static liveness being worth building at all, not an
     optimisation on top of it. `internal/lower/callgraph.go` already has the
     graph and the propagation shape -- `Reach` and `PropagateMarks` do this for
     suspension.
  2. Even with it, the ceiling is **~26%**. Static liveness will leave the
     majority of objects to the refcount backstop, so **the backstop's quality
     decides D3's outcome**, not the analysis's. Build it first and well.
  3. A 299-file sample gave 5% -> 41%; the full corpus gives 9% -> 26%. The
     sample skewed toward simple programs, which is the direction that flatters
     an escape analysis. Worth remembering before quoting a sample again.

  The analyser is throwaway, in `.agents-workspace/tmp/escape/`, and takes
  `-optimistic`.
  — *source: 2026-09-18 -- the escape rate, and three rules about not destroying*

- [ ] **D3, next increment: destroy at the death point, conservatively.**
  Script-end teardown now runs (see the journal), which turns "no destructor
  output at all" into "output present, position wrong". What remains is the two
  points that need last-reference knowledge: `unset($x)` on the sole reference,
  and a function local at return.

  **Do not reach for a full refcount first.** Retain/release at every reference
  creation and destruction is pervasive, and the plan is explicit that this is
  the tier that fails *wrong* rather than slow -- a missed release leaks, an
  early one destructs a live object, and both print. A half-instrumented
  refcount is worse than none, because the count is then meaningless everywhere
  rather than absent.

  The safe shape is **conservative static liveness** (the plan's tier 1), and it
  is safe for a specific reason: script-end teardown is now the backstop, so an
  object the analysis declines to claim still gets destroyed, just late. The
  analysis only ever moves a destructor *earlier*, and only when it is certain.

  Claim an object only when all of this holds on the lowered IR:

  - it is created by `new` straight into a local;
  - that local is never passed as an argument, returned, yielded, stored into an
    array or property, captured by a closure `use`, or bound by reference;
  - no `&` is taken of it or of anything reachable through it;
  - the body contains no `$$x`, `extract`, `compact`, `get_defined_vars` or
    `eval` -- any of which can name a local this analysis cannot see.

  Then emit the destructor at the local's last use, and skip registration for
  teardown entirely so it is never destroyed twice.

  Every claim owes a differential case **built to break it** -- an aliasing path
  through `&`, a closure capture, a by-reference parameter, an array element
  holding a reference. `QUALITY_GATE.md` asks for the case that would fail if
  the claimed behaviour were absent; here the case has to be the one that fails
  if the claim is *wrong*.

  **Phase A item 6 is measured now, and it caps this work at about a quarter.**
  See the escape-rate table below before investing in the analysis.
  — *source: 2026-09-18 -- destructors run at last*

- [ ] **Phase D3: object lifetime in compiled output.** Chosen as the direction
  after the cheap clusters ran out; it addresses **393 of 1073 failures (36%)**,
  more than the previous ten ticks combined.

  **The baseline is worse than "timing is approximate": compiled output runs no
  destructors at all.** Measured, not assumed --
  `class D { function __destruct() { echo "d\n"; } }` prints nothing from a
  compiled program, at any of the three points the engine fires one. The only
  mention of `__destruct` in `internal/gogen` is the lazy-proxy compatibility
  check. Handles are never reused either: `nextObjectID` only increments, while
  the interpreter keeps a `freeHandles` list.

  So the first increment is not escape analysis. It is *running them*, and the
  engine fires at three points -- measured:

  | Point | Engine |
  |---|---|
  | `unset($x)` on the sole reference | immediately |
  | a function local at return | immediately, before the caller resumes |
  | script end | the survivors, in **reverse** order of the global symbol table |

  The first two need last-reference knowledge, which is the refcount problem.
  The plan's own advice for the array half applies here: **ship the
  always-correct backstop first** -- a refcount on `php.Object` in the shared
  runtime, destructors fired at zero -- because that is what makes conservative
  static analysis safe afterwards. Then tier 1 (static liveness for
  non-escaping objects, carrying no refcount at all) is an optimisation on a
  working mechanism rather than a prerequisite for one.

  Order, per the plan: copy elision and in-place mutation fail *slow*; static
  destruction and selective refcounting fail *wrong* -- a missed release leaks,
  an early one destructs a live object, and both print. Gate the last two behind
  their own corpus measurement, and give every non-escape claim a differential
  case built to break it.

  Worth knowing before committing further: Phase A item 6 -- what fraction of
  objects ever escape -- has still not been measured, and it bounds how much of
  this can be tier 1 rather than tier 2.
  — *source: 2026-09-18 -- a raw write is a write, and the curve flattens*

- [ ] **Ownership trace: make its answers precise enough to act on.**
  `lower.TraceOwnership` (design in `OWNERSHIP.md`) traces every allocation
  through its containers, and tells a move from a share. It is measurement only;
  nothing the emitter destroys depends on it. Before it can decide destruction
  (D3.3), four things:

  1. **A per-method argument summary.** `argKept` answers "keeps" for every
     compiled method, so a constructor argument is a sink even when the
     constructor only puts it in `$this`. The precise edge is *contain into
     the receiver*, and it needs a summary of the form "argument *i* reaches
     only `$this`". `RetainedParams` does the coarser question for functions.
  2. **The catalogue's `ReEnters` misses ArrayAccess.** `dim.get`, `dim.set`,
     `dim.append` and the `dim.ref` family are not marked, though on an object
     each is `offsetGet`/`offsetSet` with the object as `$this`. The ownership
     table declares it itself for now. Settle with the engine whether any
     diagnostic ordering depends on the flag before changing it there.
  3. **Whole-program compilation for Twig and CakePHP.** One file per program
     makes every cross-file method unknown, so "passed to a method" is mostly
     an unknown callee rather than a real escape.
  4. **Using it for destruction is a behavioural change.** Switching
     `ConfinedNew` to the trace confines objects held in a frame-local
     container, which the frame question cannot see; it lands on its own with a
     corpus before-and-after and a differential case built to break each
     claim.
  — *source: 2026-09-25 -- ownership tracing through containers*

- [ ] **Performance from ownership: P0-P4 in `OWNERSHIP-PERF.md`.** Measured
  first: compiled `Zend/bench.php` is 18.1 s against the engine's 1.19 s, and
  10.98 s of it is `strcat` going quadratic because the builder pass misses a
  loop-carried SSA value. Order: P0 a benchmark harness, P1 in-place string
  append from the trace's linear-use fact, P2 no copy at a store of a moved
  array, P3a build never-written literals once, P3b a separate-on-write bit
  for shares the trace cannot bound, P4 object storage reuse at a static
  death. Each is a pure performance change -- corpus and fixtures must not
  move -- and owes the attack cases listed there.

  **The trace's side is done** (2026-09-26): sites with literals, Written,
  moves, in-place appends, returns-fresh and parameter-written, sized on Twig
  and CakePHP in `OWNERSHIP.md`. Measured reach: appends 37-52% of
  concatenations, never-written 72-76% of arrays, moves 1-3% of stores.

  **P0 and P1 are done** (2026-09-26): `hack/bench.sh`, and in-place appends
  (`lower.StringChains`) -- bench.php 19.5 s -> 7.4 s, its `strcat` 11.8 s ->
  0.033 s. **P3a is done** (2026-09-27): `issetor` 85x -> 3.5x, micro_bench
  13.7x -> 9.9x; little reach on library code, where arrays escape. **P3b is
  done** (2026-09-27): copy-on-write by holder, shares handed back where the
  trace proves them dead. The Go map index inside `php.Array` is replaced by
  a packed / open-addressed one (zend_bench 7.7 s -> 7.0 s). **P2 is done**
  (moves take the store that does not share). **P4's cheap half is done** --
  shared declaration tables, no constructor dispatch for constructor-less
  classes, destruction flags instead of maps, a bounded frame drain list --
  `newfoo` 20x -> 7.9x. Open: P4 storage reuse (needs its own attack cases:
  weak references, a destructor storing `$this`, `spl_object_id`); container
  stores of moved values; and the no-share path for a quiet callee.
  **Plain properties and integer compare/step fast paths are done**
  (2026-09-27): `propinc` 18.7x -> 8.7x, zend_bench 5.9x -> 3.9x,
  micro_bench 8.7x -> 4.8x. Open there: property slots by index (the real
  "known object shape"), and the `place` machinery `$o->p++` goes through --
  a large struct copied per call, plus `TakePendingError` after every step.
  — *source: 2026-09-25 -- where compiled output's time goes*

- [x] **A class's pending initializers run together at its first use.**
  Done 2026-09-29: `ensureClass` (see the journal).
  Measured: `new C` and the first static-property access evaluate every
  pending constant, then property default, then static of C and its parents,
  and the first failure is the Error (`bug47572`, `gh10356`,
  `typed_class_constants_diamond_error1`). A class-constant access evaluates
  only that constant; a static method call evaluates nothing. Compiled output
  evaluates statics on their own first read and property defaults at every
  `new`, so an undefined constant in a static's default is silent until read.
  — *source: 2026-09-29 -- back to fidelity*

- [ ] **The oracle never calls `set_exception_handler`'s callback for an
  exception that escapes the file body.** `php2eval -raw` runs the program
  through `zend_eval_string`, which prints the fatal block itself: `set_exception_handler(fn($e) => print "Caught");
  throw new Exception("boom");` gives `Fatal error: Uncaught Exception: boom`
  from the oracle where PHP prints `Caught`. Compiled output does the same --
  it never consults the handler at top level either -- so the two agree on
  the wrong answer and such a file **passes**. 21 corpus files that use
  `set_exception_handler` pass today; some of them are passing this way. A
  throw from a shutdown function does reach the handler in the oracle (the
  engine runs shutdown itself), and compiled output now matches that. Fixing
  it means the oracle calling the handler the way `php_execute_script` does,
  then compiled output following.
  — *source: 2026-09-29 -- names, callables and destructuring*

- [ ] **Checks the engine makes at INIT or SEND time run after the
  arguments in compiled output.** `C::__construct()`'s `Cannot call
  constructor`, a static call's class fetch and `call_user_func`'s
  deprecation for a keyword callable all precede argument evaluation in the
  engine; the IR has already evaluated the arguments by the call
  instruction, so an argument with a side effect shows it first. The
  by-reference send refusal (`cannot_pass_by_ref`) is the exception, emitted
  at the send.
  — *source: 2026-09-29 -- batches 36-40*

- [ ] **SEND_VAL_EX to a by-reference parameter of a callee resolved only at
  run time** (a closure in a variable, a method) still binds the value
  silently; only functions the unit declares get the Error.
  — *source: 2026-09-29 -- batches 36-40*

- [ ] **`self::$p` inside a trait's static method called on the trait
  itself** (`T::foo()` reading `self::$foo`) is deprecated by the engine
  like `T::$foo` is; compiled output deprecates only the literal trait-name
  spellings, since `self` resolves at run time.
  — *source: 2026-09-29 -- batch 53*

- [ ] **`func_get_args()` / `func_get_arg()` report the parameters' current
  values** (probed: `$a = 5` before it makes the answer 5). The `func.args`
  intrinsic reads only the frame's `args`; it would need the parameter
  variables as operands so mem2reg hands it their current values. (A
  parameter a named call skipped now shows its default: batch 72 writes it
  into the argument list.)
  — *source: 2026-09-29 -- batch 62*

- [ ] **A closure's static variables belong to the closure object**, not to
  its declaration: each evaluation of `function () { static $n = 0; }` starts
  its own `$n`, while two calls of one closure share it (bug64979). Statics
  are package-level slots per function (`declareStatics`), so every closure
  made from one declaration shares them. Needs the closure body to reach its
  statics through `closureBody`, and bind/bindTo to copy them.
  — *source: 2026-09-29 -- batch 61*

- [ ] **`__call` receives references a `call_user_func_array` element held
  as values**: `call_user_func_array([$p, "bar"], [&$arg])` gives `__call` an
  `$args` whose element is `&$arg`, where a direct `$p->bar($arg)` gives a
  value even when `$arg` is a reference. callMethod unboxes both, because at
  that point an undecided send's box and a program-made element's box look
  the same -- the send would have to say which it was (bug50394).
  — *source: 2026-09-29 -- batch 60*

- [ ] **`new ReflectionFunction("str_pad")` says the function does not
  exist** for every library function: a wrong answer where it should either
  reflect the builtin (its harvested signature, `invoke`/`invokeArgs` through
  `callValue`) or report the gap as unsupported (bug46106).
  — *source: 2026-09-29 -- batch 59*

- [ ] **`class_alias()` onto a name already taken** must warn `Cannot
  redeclare class FOO (previously declared in FILE:LINE)` and return false,
  where the location is the *aliased* class's declaration. The harvest carries
  no class declaration line, so the warning cannot be worded yet
  (`class_alias_002`).
  — *source: 2026-09-29 -- batch 55*

- [ ] **`self::class` inside an unscoped closure** is refused by the emitter
  (`unrenderable operand self`); the engine answers `Closure` once an object
  is bound with no scope.
  — *source: 2026-09-29 -- batches 36-40*

- [ ] **`"C::m"` called as a string from inside an instance method of C** is
  deprecated by the engine in the array form's words (`Callables of the form
  ["C", "C::m"] are deprecated`) and dispatched on `$this`; compiled output
  calls it statically and says nothing.
  — *source: 2026-09-29 -- back to fidelity*

- [x] **An argument reading a local is lowered after a later argument's
  assignment to it.** `both($x, $x = new D("P1"))` passes P1 twice; the
  engine sends `$x` (P0) before evaluating the second argument. The IR puts
  `read_var($x)` after `slot.set $x`. Compiled output only.
  — *source: 2026-09-28 -- release at overwrite in loops*
  Done 2026-09-29: `snapshotSends` copies the variable where it was sent
  when something later in the block may write it (`argument_order.php`).

- [ ] **The interpreter's by-value `foreach` reads the live array** against a
  snapshot of its keys, so `foreach ($b as $v) { unset($b[2]); }` stops before
  the third element, where the engine (and compiled output) walk all three.
  `runtime/interp`'s own `iterator`; compiled output uses `php.Iter`.
  — *source: 2026-09-28 -- foreach walks a share*

- [ ] `min()` with a NAN argument and `sort()` over an array holding NAN
  disagree with the engine (`min(NAN, 1)` is `int(1)` there, `NAN` here).
  Both predate the NaN comparison fix; each follows its own engine rule for
  unordered operands, to be asked of the engine.
  — *source: 2026-09-28 -- calls and float arithmetic*

- [ ] **A compound assignment on a property overwrites a reference bound
  during it.** `$e->s .= new Binder($e)` where `__toString` does
  `$GLOBALS['bound'] = &$this->target->s`: the engine's ASSIGN_OBJ_OP writes
  the result into the slot it fetched, replacing the reference, so `$e->s` is
  `"ab"` and `$bound` stays `"a"` (then `"a+"`). php2go writes through the
  reference and both read `"ab"`. Predates the inline property path (setMod
  declines a reference slot the same way); asked of the engine with php2eval.
  — *source: 2026-09-28 -- inline property read-modify-write*

- [ ] **Type specialisation, the rest.** `int64`/`float64` representations,
  bounded int steps and int versions of untyped functions landed
  (`lower.IntReps`, `FloatReps`, `IntVersions`; 2026-09-28). Still open:
  - int `+`/`-`/`*` of two variables stays boxed (the inline boxed-int test
    answers it without a call, but the value is still a php.Value); an
    unboxed result needs range facts;
  - a bound reached through `&&`/`||` gives no fact: the branch is on the
    merged boolean, not the comparison (mandel's `$color > 0`);
  - closures get no int version (functions and methods do); ackermann and
    fibo are now call-bound -- a heap-allocated argument slice per call (kept
    for traces) and the frame push/pop -- not arithmetic-bound;
  - a branch on a native comparison still boxes through `php.Bool` and asks
    `Value.Bool` (both inline now, so this is small);
  - hook bodies, linked userland library bodies and resumable bodies do not
    run the representation passes.
  — *source: 2026-09-28 -- unboxed ints and floats*

- [ ] **What is left above 1x on the benchmarks** (2026-09-28, after the
  unboxed entries and int frames: ary3 1.2x, propinc 1.2x, newfoo 1.4x; the
  rest at or under 1x). Earlier notes: ary3 (1.4x then)
  is the element read-modify-write through `dimPlaceRW`, `getModAt` and
  `setMod` on the places path, which PackedAt/PackedSet only partly cover;
  newfoo (1.4x) is object construction and release -- profiled: allocation
  22% (`php.Object` is 216 bytes, so a one-property object is a 256-byte
  allocation where the engine's is ~72; enum case/backing, the four
  per-object property maps, Lazy and Internal are rarely set and could sit
  behind one pointer, but they are exported fields used across interp, stdlib
  and the emitter), the release at overwrite 10%, `appendFrame` 7%,
  `UseDeclarations` 5%, and a `declaredClasses` map lookup per `new` 4% that
  an unconditionally declared class does not need; propinc (1.3x) is the
  property RMW after the site-cache inline. An int frame now writes only
  its own fields (PushCallInts); a general push still copies the whole
  ~190-byte StackFrame.
  — *source: 2026-09-28 -- calls borrow their argument slots*

- [ ] **Constant-index property access.** Compiled `$this->x` for a property
  proved plain in its class and every subclass (slot prefix preserved, no
  subclass hook, type, visibility change or shadowing) could index `obj_C`
  directly instead of going through the site cache. Also: an object in slot
  mode still allocates an empty `Props` array for dynamic properties it
  usually never gets.
  — *source: 2026-09-28 -- objects as Go structs*

- [ ] **Deferred static defaults are initialized per property**, where the
  engine initializes all of a class's at its first static access: with
  `public static $a = 0; public static $b = LATE * 2;` and `LATE` undefined,
  the engine fails `C::$a` too (`Undefined constant "LATE"`), compiled output
  answers it. Found building `static_property_sites.php`.
  — *source: 2026-09-28 -- call sites that remember*

- [ ] **`new` in a loop keeps every dead object until the function returns.**
  A confined object stored in a local joins the frame's destruction list;
  the engine destroys it at the local's next assignment. Five million `new
  Foo()` keep five million objects live (the GC is most of `newfoo`), and
  handle numbers climb where the engine's alternate #2/#3. Destroying at the
  kill point is plan D3.3; `lower.ReleasePrev` marks promoted stores.
  — *source: 2026-09-28 -- call sites that remember*

- [ ] Compiled output reads an **unset typed** property on a class with
  `__get` as uninitialized (an Error) where the engine calls `__get` and
  checks its result against the declaration -- `Value of type string returned
  from M::__get() must be compatible with unset property M::$i of type int`.
  The untyped case and `__set` after unset were fixed on 2026-09-27; this
  one needs the result check, whose coercion rules (strict_types, int to
  float) are to be asked of the engine. Repro:
  `.agents-workspace/tmp/pfp/unset3.php`. The interpreter gets it right.
  — *source: 2026-09-27 -- plain properties and integers skip the questions*

- [x] **A by-value array parameter aliased the caller's array** -- fixed by
  P3b (2026-09-27): `Unbox` shares, and the share is handed back at the
  callee's exit when the trace proves the parameter's holder never leaves the
  frame (`ParamReleasable`).
  - [ ] Skip the share altogether when the callee cannot observe a write to
    the caller's array -- no calls, no re-entry, no global, static or property
    writes -- which would recover `arrpass`'s ~19%.
  - [ ] Compiled output does not print `Only variables should be passed by
    reference` for a call's result sent to a by-reference parameter; with it,
    a fixture could exercise the one raw path to a returned parameter.
  — *source: 2026-09-27 -- P3a, and a parameter that is not a copy*

- [ ] Replace the final non-value representation, `*strbuf`, with an internal
  PHP object if measurement shows that doing so does not add an allocation per
  append. This would remove the raw argument shape and the last special
  representation from generated frames. — *source: 2026-09-05 -- the iteration
  cursor becomes a value; 2026-09-05 -- what is still not a call, measured*

- [ ] Check catalogue effects against Go binding shapes. An intrinsic marked
  `ReEnters` or `Throws` must accept a context and return an error-bearing shape;
  this would have caught `Concat` claiming re-entry while being unable to call
  `__toString`. — *source: 2026-09-04 -- count, strbuf and copy get runtime
  functions; 2026-09-02 -- the intrinsic catalogue*

- [ ] Improve specialization from the remaining evidence sources: harvested
  builtin return information, joined program-constant types, and unpromoted
  file-body/place reads. Keep builder promotion before mem2reg when it becomes
  a mutating pass. — *source: 2026-09-03 -- stop guessing at the number;
  2026-09-05 -- typing constants triples the rate; 2026-09-05 -- mem2reg becomes
  the default*

- [ ] Support a dimension nested inside a property place, including plain and
  compound writes such as `$o->arr["z"] = 1`. The current place builder handles
  each family over a local but not every place-over-place composition. —
  *source: TODO section “Nested places refuse: a dim inside a prop”; 2026-09-10
  -- a place in receiver position*

- [ ] Raise the correct `ArgumentCountError` for missing required arguments in
  compiled calls. The call protocol must retain the call-site file/line and the
  exact arity phrase; allowing `recv` to coerce null produces the wrong error.
  — *source: TODO section “A missing required argument raises the wrong error”;
  2026-09-10 -- named arguments bind at the call site*

- [ ] Render deferred constant expressions used as parameter defaults instead
  of refusing `RECV_INIT`. This shares the constant-expression renderer, but
  default application and required-position rules still belong to `recv`. —
  *source: TODO section “Parameter defaults that are not literals are refused”;
  2026-09-10 -- a deferred initializer had no way out of the IR*

- [ ] Audit remaining `l.operand` readers whose operand may be `IS_UNUSED`.
  Empty string plus success has already lost a static-property class in two
  sites; opcode-specific accessors must decode class, `$this`, constructor and
  hook selectors. — *source: 2026-09-10 -- one rule, two sites, and the second
  never learned it; 2026-09-10 -- parent::$x::get()*

- [ ] Ensure generated callback tables are initialized at startup rather than
  as package literals whenever entries call back into the generated program.
  `userFuncs`, `propHooks` and `classBuiltins` have all encountered Go
  initialization cycles; audit the next table before adding it. — *source:
  2026-09-10 -- function_exists, and a tick that knew it was small*

- [ ] Add dead-block elimination after CPS/finally rewriting. The current
  emitter tolerates unreachable continuation blocks, but leaving them makes
  diagnostics and code size harder to reason about. — *source: 2026-08-29 --
  the CPS rewrite; 2026-08-29 -- a finished body stays finished*

## Control flow, finally and resumable frames

- [ ] **A foreach's generator is not closed when an exception leaves the
  loop**: the engine frees the loop's iterator as the exception unwinds
  through it (live-range cleanup), so the generator's finally prints before
  the exception is caught or reported (bug69740); compiled code frees it
  only on the normal exits.
  — *source: 2026-09-30 -- batch 111*

- [ ] **A generator held in a variable is not closed when the variable is
  unset or the script ends**: the engine runs a suspended generator's
  finally blocks when its last holder goes (`unset($g)`, end of script);
  compiled code closes only a temporary one (batch 111), and
  gc_collect_cycles is a no-op, so the cycle cases (generators/gh9750-*)
  never destruct either.
  — *source: 2026-09-30 -- batch 111*

- [ ] **A by-reference foreach over a variable reassigned in the body**:
  `foreach ($a as &$r) { $a = array_splice($a, 0); }` -- the engine's loop
  iterates the *variable*, and its hash iterator moves onto the new array
  when the old one is gone; the compiled iterator keeps the old array
  (array_splice_empty_ht_iter_removal).
  — *source: 2026-09-30 -- batch 105*

- [ ] Give each inlined-finally copy its own catch-region entries. `unwindPaths`
  remaps `Finally` but not a catch written inside the copied finally, leaving
  the region pointed at dead original blocks. — *source: 2026-09-10 -- which
  finally bodies a block is inside*

- [ ] Settle `FAST_CALL with no finally range`. Its op1 target is absent from
  the current `finallyAt` map and is distinct from ordinary nested-finally
  cloning. — *source: 2026-09-10 -- which finally bodies a block is inside*

- [ ] Correct the remaining deeply nested finally paths that now terminate but
  send the exception to the wrong place or execute a copy twice (`bug70012` and
  the nested return/throw shape). — *source: 2026-09-10 -- which finally bodies
  a block is inside; 2026-09-10 -- which finally a copy is*

- [ ] Store the computed-variable map on a resumable frame. Rebuilding it from
  static fields at every entry loses names dynamically created before a yield.
  — *source: 2026-09-10 -- a variable variable needs the names at run time*

- [ ] Make the compiler's suspension analysis describe operations that advance
  a generator structurally, not by a list of driver method spellings, and add
  value analysis for `yield from $var`/opaque callable flows. Do not merge
  independent `.phpt` files into one invented program to derive the graph. —
  *source: 2026-08-29 -- the call graph; 2026-08-29 -- rule 2 has two more
  closures; 2026-08-29 -- a corpus that is not one program*

- [ ] Revisit the goroutine-backed fiber driver only with workload evidence.
  Uniform CPS is sound but taxes every call; dual entries trade code size and a
  branch for a fast non-fiber path. The current goroutine is a stack-parking
  workaround, not a concurrency requirement. — *source: 2026-08-29 -- fibers
  keep goroutines, on evidence; 2026-08-29 -- a goroutine is the wrong shape
  for a fiber*

- [ ] Preserve and check the narrow continuation invariant: a continuation
  that consumes `frame.resumed` has one resume predecessor, even though a
  continuation block in general can also be a branch join. — *source:
  2026-08-29 -- the CPS rewrite; 2026-08-29 -- a continuation can be a join
  point*

## References, places and lifetime

- [ ] **Weak references in compiled code never see their target die**
  (weakrefs/*, 22 files): the runtime keeps a weak key by pointer and
  compiled programs install no WeakObserve hook. The obvious shortcut --
  count `WeakReference::create($o)` as keeping nothing, so `unset($o)`
  releases the object, and have a released object read as dead -- is
  **unsound**: `$w->get()` hands back a strong reference by a route the
  escape analysis does not follow, and `$keep = $w->get(); unset($o);`
  would free a live object. This wants the D3.4 refcount, not the
  confinement proofs.
  — *source: 2026-09-30 -- batch 112*

- [ ] **Destroying an object drops the type its property put on a
  reference that outlives it** (typed_properties_094): `$r =& $b->prop;
  unset($b); $r = "foo";` is fine once the object is gone. Needs the object
  to die at the `unset`, which file-scope code does not track (its locals
  are globals, so ConfinedObjects skips {main}), and the destruction to
  call DropSourceOwned for each property box.
  — *source: 2026-09-29 -- batch 72*

- [ ] **A `catch` with no variable releases the exception object**
  (try/catch_novar_2): a destructor on it runs at the catch, and one that
  throws replaces the caught exception. Only right when nothing else holds
  the object, which needs the refcount this runtime lacks for thrown
  objects.
  — *source: 2026-09-29 -- batch 72*

- [ ] **Initialising a lazy object releases its initializer's handle** -- 33 of
  the 61 files still off by +1, the largest sub-cluster left in the handle
  family. Measured:

      $r = new ReflectionClass(C::class);            // #1
      $g = $r->newLazyGhost(function ($o) { ... });  // closure #2, ghost #3
      spl_object_id(new P());                        // 4  -- closure alive
      $g->a;                                         // initialises
      spl_object_id(new P());                        // 2  -- closure released

  This is **not** general refcounting: it is one drop at one point, the same
  shape as the constructor-throws release. What makes it awkward is that the
  release point is *later* than the call that consumes the closure, so
  `DisposableTemporaries` cannot express it -- that analysis releases **at** the
  consuming call.

  The shape it needs is "hand ownership to the callee": at
  `newLazyGhost(<temporary closure>)` the closure is a temporary consumed by one
  call and stored nowhere else, which the existing analysis already proves. Mark
  such an object as solely owned by the lazy state, and have `ResolveLazy`
  release the handle when initialisation completes. Releasing unconditionally is
  **not** safe -- a program that also keeps the closure in a variable would then
  see a live handle reused, which is the one direction this machinery must never
  get wrong.

  Touches `runtime/php` (a flag on the object plus the release in
  `ResolveLazy`), so it cannot be developed while a corpus run is in flight.

- [ ] **Object handle reuse: the largest failure family, now partly closed.**
  131 files' **first** divergence from the engine is an object handle number and
  nothing else -- bigger than any refusal cluster. The engine hands the lowest
  free handle to the next `new`, so

      var_dump(new Point(1));   // #1
      var_dump(new Point(2));   // #1 again

  while php2go counted monotonically. `lower.DisposableTemporaries` now claims
  the temporaries that provably die at the call consuming them, and the emitted
  program allocates from a free list. Fixture `object_handle_reuse.php`, built
  as five attempts to break the claim.

  It claimed 8 of the 65 files whose offset was exactly +1 (`zx`: 2356 -> 2364,
  0 lost). Since extended to groups -- `var_dump([new P(), new P()])` frees the
  array and both elements -- with elements claimed **individually**, so one kept
  element no longer forfeits the handle beside it.

  What it does not yet claim, in rough order of value:

  - **The non-retaining allow-list is tiny.** `nonRetainingCallee` in
    `cmd/php2gotest/main.go` names ~18 library functions. The question is not
    answerable from arginfo -- it says how an argument is *passed*, never
    whether the callee keeps it -- so the list has to be written by hand and
    must fail closed. Widening it is cheap and each entry is a claim to check.
  - **A class with `__destruct` is deliberately excluded.** Its death is
    observable, so recycling the handle without also moving the destructor
    would fix the number and keep the wrong output. The two have to be solved
    together, which is the D3.3 work.
  - Offsets of +2, +3 and +4 (55 files) mean more than one handle is in flight;
    they need the same machinery to claim more sites, not different machinery.
  - A **method** call as the consumer (`$o->dump(new P())`) is never claimed:
    it would need the per-method summary `ConfinedObjects` also lacks.
  - **A constructor that throws frees the handle**, and php2go keeps it. 22
    files are off by exactly +2, and `bug30162` is the shape. Measured:

        class B { function __construct() { throw new Exception; } }
        try { new B(); } catch (Exception $e) {}
        $x = new P();            // engine: #1 -- B's handle came back

    but not unconditionally -- a constructor that stores `$this` first keeps
    it:

        function __construct() { global $kept; $kept = $this; throw ...; }
        // $kept is #1, the next object is #3

    so this needs the same `leaksThis` summary as everything else here.

- [ ] **The `leaksThis` method summary, which three separate items now want.**
  `ConfinedObjects` already takes a `leaksThis func(method string) bool` and is
  called with **nil**, which makes every method call on an object count as an
  escape. That single nil is the difference between claiming 9% and 26% of
  `new` sites (measured, Phase A item 6), and it is also what blocks the
  constructor-throws case above and the method-consumer case above it.

  A fail-closed first version needs no call graph: a method leaks `$this`
  unless **every** mention of the receiver in its body is a property read or
  write on itself. That refuses a body that passes `$this` anywhere, stores it
  in a global or static, returns it, or puts it in an array -- which is the
  conservative direction -- and it accepts the overwhelming majority of
  constructors and accessors. Each acceptance owes a differential case built to
  break it, per QUALITY_GATE.md.

  **The free list is a stack, not a sorted list.** An earlier note here said the
  engine reuses the *lowest* free handle. It does not -- measured:

      $a = new P(); $b = new P(); $c = new P();
      unset($a); unset($b);   // freed 1 then 2
      new P(); new P();       // -> 2 then 1

  and reversing the unsets reverses the answer. The two disciplines agree
  whenever only one handle is outstanding, which is why a full corpus run
  (`zx`, 4,986 files, 0 lost) passed without distinguishing them. The group case
  is what separated them.


- [ ] **Writing through a temporary: silent for some containers, an Error for
  others, and the emitter can see which.** `refSlot` used to refuse every write
  through a non-reference. Measured, most of them are **silent**: on a method
  call returning an array, `$o->m()[0] = 1`, `+= 5`, `unset(...)` and `[] = 3`
  all print nothing, raise nothing, and run on. Those now write to scratch
  (fixture `temp_dim_write.php`).

  `dim.ref` -- taking a reference to hand *out* -- still refuses, because two
  containers make it an Error where the method call does not:

      byRef(func_get_args()[0])   // Cannot use temporary expression in write context
      byRef(C[0])                 // same, for a const array
      byRef($o->m()[0])           // silent

  The discriminator is visible in the lowered IR, at the container's **def
  site**:

      %1:zval = call.method %0, "b"()      -> silent
      %4:zval = call.intrinsic "func.args"()  -> Error
      %7:zval = const.fetch "C"               -> Error

  which reads as "a real call's result has storage, a constant's or an
  intrinsic's does not" -- IS_VAR versus IS_TMP_VAR/IS_CONST. **That rule is
  inferred from three samples and must not be implemented on that basis**:
  AGENTS.md's founding rule is that reasoning about Zend operand encodings has
  been wrong more often than right. Settle it with `php2eval` across the
  producers first, then teach the emitter to look at the def site. Worth ~6
  files, and the refusal is honest until then -- handing out scratch there
  would answer silence where PHP throws.


- [ ] Round-trip a **deeply self-referential** structure through
  serialize/unserialize. `bug63882_2.phpt` reassigns `$a = unserialize(
  serialize($GLOBALS))` in a loop, where `$GLOBALS` holds `$a` itself, so each
  pass nests deeper; the reader then reports `Error at offset 756 of 1127`
  against the writer's **own** output. Small self-referential shapes round-trip
  correctly -- `$a[0] =& $a`, and one nested inside another array -- so it is
  depth or repetition, not the `R:`/`r:` forms themselves. The file was
  `unsupported` before unserialize landed, so this is newly *reachable* rather
  than newly broken. — *source: 2026-09-16 -- unserialize*

- [ ] Build a `__PHP_Incomplete_Class` for an `O:` naming a class the program
  never declared. `unserialize('O:7:"Missing":0:{}')` gives an object of that
  class holding `__PHP_Incomplete_Class_Name` plus the serialized properties;
  php2go answers `false` with an offset warning in **both** back-ends. The class
  has no methods and exists only to carry the state until something declares the
  real one. — *source: 2026-09-16 -- unserialize*

- [ ] Mark `#[\SensitiveParameter]` arguments on a **compiled** call frame.
  `php.StackFrame.Sensitive []bool` and `IsSensitive` exist and the trace
  renderer already honours them; `internal/gogen` never sets the field, so a
  trace shows the value raw where the engine shows
  `Object(SensitiveParameterValue)`. The attribute is harvested --
  `zend.ArgInfo.Attributes` -- but does not reach `gogen.Prop`/`.params`, so it
  needs the same plumbing `Property.Order` did. Eleven of the seventeen corpus
  files calling `getTrace()` stop here. — *source: 2026-09-16 -- getTrace, and
  two closures on one line*

- [ ] Push the call site for a **dynamic** call. `$f = "plain"; $f(null)`
  against `function plain(Real $a)` reports `Argument #1 ($a) must be of type
  Real, null given` with no `, called in %s on line %d` suffix, while the same
  call written statically carries it. `CoerceArgument` builds the suffix from
  `ctx.Stack()`, so the dynamic path is not pushing a frame by the time the
  callee verifies its arguments. — *source: 2026-09-16 -- null against a
  class-typed parameter*

- [ ] Reject a declared parameter type naming a class the program does not
  declare. `function f(T $a) {}` then `f(null)` is
  `f(): Argument #1 ($a) must be of type T, null given`; compiled code accepts
  it silently, for a by-value and a by-reference parameter alike.
  `php.InstanceOfClass` answers false for "cannot prove it matches", and a
  program **with** a class table and no autoloader can prove the opposite --
  which is what `declaredClassRejects` is for. Leaves
  `unpack_iterator_by_ref_type_check.phpt` failing after the Traversable-unpack
  work. — *source: 2026-09-16 -- unpacking a Traversable*

- [ ] Bind a **named** argument that arrives by unpacking. A string key in an
  unpacked array or Traversable is a named argument, which the call site binds
  statically and so cannot place: `gh18581.phpt` and the array path share the
  `named argument from unpacking` refusal. Needs run-time binding against the
  callee's signature, the same machinery `named argument to an unknown callee`
  wants. — *source: 2026-09-16 -- unpacking a Traversable*

- [ ] Make a by-reference foreach over a plain object **live**. The compiled
  unit builds a snapshot array of the object's property boxes
  (`foreachPropRefs`), so writes reach the object but a property *added during
  the loop* is not walked -- `property_hooks/foreach_002.phpt` adds `$a->bar`
  inside the loop and the engine yields it. `runtime/interp` iterates the
  object's own table with `it.backing` and filters visibility per step; the
  compiled side would need the same rather than a projection, because the live
  array and the visibility filter want different things. — *source: 2026-09-16
  -- a by-reference foreach over a plain object binds to the slots*

- [ ] Give asymmetric visibility **one home**. The two back-ends enforce
  `protected(set)` in different places from different data: the interpreter in
  `checkSetVisibility` (`runtime/interp/property.go:402`) against the class
  table, using `Unit.protectedOwner`; compiled code in
  `php.CheckPropertyAsymmetric` against `o.Declared`, using the new
  `Declaration.SetClass`. They agree today because the gogen entry was taught
  the interpreter's rule, not because one of them is the rule. The next
  divergence will be silent. — *source: 2026-09-16 -- protected(set) is measured
  against the first declarer*

- [ ] Release a share the *callee* handed back out. `php.Shared` is a counter
  paired with `php.Unshare` at frame teardown, so a binding a frame made is
  released when that frame dies -- but a box the callee put into a **value** it
  returned stays counted for good. `function f(&$r) { return get_defined_vars(); }`
  leaves `&` on the caller's container after the returned array is gone, where
  the engine drops it with the array's refcount. Needs the returned container's
  own lifetime, which is the Phase D3 ownership work. — *source: 2026-09-15 --
  get_defined_vars, and a share with no matching release*

- [ ] Model the reference-holder effect of `ZEND_FREE`. List destructuring can
  box an inner array for a write; PHP drops that temporary box at statement end
  while SSA currently treats FREE as irrelevant. — *source: 2026-09-10 --
  destructuring by reference*

- [ ] Make foreach-by-reference over an array **replaced or resized wholesale**
  follow the new source. The live bucket walk lands appends and unsets
  (2026-09-11), but `$a = [...]` inside the loop still steps the old array. —
  *source: 2026-09-10 -- destructuring by reference; 2026-08-27 -- a
  by-reference foreach holds a position, not a list*

- [ ] Emit `Only variables should be assigned by reference` for a value-return
  expression bound into a dimension (`$a[0] =& returnsVal()`) while still
  completing the engine's copy/binding behavior. — *source: 2026-09-10 -- one
  opcode, two symbol tables*

- [ ] Run an abandoned generator's `finally` when it is held by a **variable**
  rather than by the loop. `$h = g(); foreach ($h as $v) break;` unwinds at
  `unset($h)`, and one held to the end of the script unwinds at shutdown;
  php2go handles only the loop-owned case (`iter.free.owned`), which is the one
  decidable without a refcount. The shutdown case is the nearer of the two -- a
  registry of live generators drained at program end -- but its ordering against
  other destructors is the same open question. — *source: 2026-09-11 -- an
  abandoned generator runs its finally*

- [ ] **Emit destructor calls at all.** The compiled output never runs
  `__destruct` -- not on `$x = null`, not on `unset($x)`, not when a local goes
  out of scope, not at script end. `grep __destruct internal/gogen/gogen.go`
  returns nothing. The other items in this section describe *refining* the
  interpreter's reachability walk and read as though the compiler had a coarse
  version of the same mechanism; it has none. 76 of the 725 remaining failures
  are in files that define `__destruct`, the largest identified cluster left.
  The shutdown pass is the cheapest correct slice and is worth landing on its
  own; the plan's static-liveness/selective-refcount design is the real answer.
  — *source: 2026-09-11 -- a generator's refusals, and the destructors that
  never run*

- [ ] Implement reference-holder counts or an equivalent precise lifetime
  model. It is the shared blocker for `var_dump`'s `&` marker, exact destructor
  timing, object-handle reuse, weak-container pruning and several cycle counts.
  A one-way “was referenced” flag was measured and reverted. — *source:
  2026-08-24 -- CRLF, and the & marker settled for good; 2026-09-10 -- the
  cycle collector this program does not have*

- [ ] Correct object destruction and cycle collection cases the reachability
  walk cannot time: arrays-only cycles, destructor-created/resurrected objects,
  suspended generator teardown and `gc_collect_cycles()` return counts. —
  *source: 2026-08-25 -- what gc_collect_cycles() counts; 2026-08-29 -- a
  destructor makes the walk stale; 2026-09-10 -- the cycle collector this
  program does not have*

- [ ] Preserve reference identity through `get_object_vars()` and object-to-
  array casts where PHP returns aliases rather than detached values. — *source:
  2026-08-28 -- references held by typed properties; TODO section “A reference
  does not survive get_object_vars() or an array cast”*

- [x] ~~Split the **RW** place fetch into its own intrinsic.~~ Done 2026-09-11:
  `prop.ref.rw`, `dim.ref.rw` and `dim.ref.unset` are separate operations, with
  the container warning as its own instruction so it lands before the key read.

- [x] ~~Carry the UNSET access mode into the place so a nested unset does not
  autovivify.~~ Done 2026-09-11: `dim.ref.unset` is its own operation and a
  nested one opens the outer place with `refExisting`, which creates nothing and
  still runs the fetch's own conversion -- `$f = false; unset($f[0][0]);` prints
  one deprecation and leaves `$f` as `bool(false)`.

- [x] ~~The **property** twin of the above.~~ Done 2026-09-11: `prop.ref.rw` and
  `prop.ref.unset` are separate operations, and the four modes now disagree
  about an uninitialized property the way the engine does.


- [x] ~~Autoload the classes a **conditional class's method signatures** name.~~
  Done 2026-09-11: `classNeeds` carries the types of methods an ancestor also
  declares, child's first. The five `class_order_autoload` files now differ only
  in object handle numbers.

- [ ] `parent::m()` and `self::m()` must **forward** the current late static
  binding rather than setting it to the class they name: `static` inside a
  method reached through `parent::` still means the class the original call
  named. `php.StackFrame.LSB` now carries it and `callStatic` sets it to the
  class it was asked for, which is right for an explicit `C::m()` and wrong for
  a keyword. One corpus file (`lsb_022`). — *source: 2026-09-11 -- a keyword can
  be the class of a callable too*

- [ ] **Stale `&` after an array holding references dies** (JOURNAL
  batch 125). bug80037's fix releases element shares only for a local
  that is solely the subject of a by-ref foreach. An array read after the
  loop, overwritten, unset, or an unset element still leaves the
  surviving holder printing `&`. Needs an array-death event (refcount).
- [ ] **Closure static shared with a suspended generator** (JOURNAL
  batch 125). bindTo/clone copy a closure's statics; the engine keeps
  them shared while a generator from the original is suspended (a live
  reference), and var_dump shows `&int(...)`.
- [ ] **Destructor exception's previous outside shutdown** (JOURNAL
  batch 125). A local's destructor exception thrown while another
  exception unwinds should get it as getPrevious (`caught e2 prev=e1`);
  compiled code leaves previous empty.

- [ ] **Temporaries die at the end of their statement** (JOURNAL batch
  126). bug73338: `call_user_func(new class {...})` and
  `[new class {...}, "foo"]()` -- the engine destroys the temporary
  inside the try, so its destructor's throw is caught there; we keep it
  to request end and report it uncaught.
- [ ] **Generators sharing a delegate** (JOURNAL batch 126). bug70965,
  backtrace_multi_yield_from need the engine's multi-child delegation
  tree (a shared leaf position). Also: function-local generators are
  closed at shutdown, not when the function returns; shutdown closes
  generators in creation order, the engine destroys globals in reverse
  symbol-table order.
- [ ] **Dim read-write handler gaps** (JOURNAL batch 126). An error
  handler that kills the container during an undefined-*variable* key
  warning (bug79784, objects_034); an unset intermediate element in a
  chain of three or more levels; a handler that keeps a copy or walks
  the array by reference is reported unsupported.

- [ ] **Locals of suspending bodies die at body end** (JOURNAL batch
  127). A generator or fiber body's local with a destructor is now
  destroyed at shutdown (it used to be at the first suspension); the
  engine destroys it when the body ends. Needs a drain carried across
  Step calls and confinement that treats `yield $o` as an escape.
- [ ] **Runtime link checks through an alias or included trait**
  (JOURNAL batch 127). inter_007 (static/non-static mismatch through a
  class_alias'ed interface) and bug81192 (a trait signature arriving
  through an include) need a per-class method table and a link walk.
- [ ] **Shutdown uncaught paths skip `__toString()`** (JOURNAL batch
  127). shutdownUncaught/runShutdown still format with UncaughtBlock, so
  a user `__toString` or a conversion error is not honoured for a throw
  at shutdown; `reportUncaught` could replace it. Also Exception's typed
  `$file`/`$line` are stored raw.

- [x] **By-ref foreach: copies keep the position** (JOURNAL batch 128, fixed in batch 129;
  gh11171 regressed to a timeout). `Iter.follow` restarts any array
  whose body differs; the engine keeps an iterator's position across
  zend_array_dup of the iterated array, so `$a += [...]` and
  `$a = $a + []` continue. Record copy provenance on array bodies that
  have registered cursors.

- [ ] **Cycle collector beyond the bounded top-level walk** (JOURNAL batches
  129, 150–152; design proposal in the session scratchpad,
  cycle-collector-design.md). The user chose top-level root walking. Direct
  entry-body calls now count plain object and dynamic-array cycles when roots
  and effects are proved. Three of the 13 compared Zend GC cases pass, with
  ten still unsupported; the dynamic-array fixture adds coverage but no pass
  on that 13-case set. Array-reference-only cycles, destructors/resurrection,
  weak effects, suspended generators/fibers, opaque internals, and other call
  sites still need root enumeration or collection semantics. Preserve
  `unsupported` for those cases.

- [ ] **Exception `trace` property never filled for dumps** (JOURNAL
  batch 130). var_dump/print_r/(array) of an exception created inside a
  function show an empty `trace`; only getTrace() builds it. Filling it
  at construction changes SensitiveParameterValue numbering.
- [ ] **Trace args show values passed, not current** (JOURNAL batch
  130; bug73156). debug_backtrace, func_get_args and a trace's `args`
  should read the parameters' live slots.
- [ ] **Suspending `__destruct`, `rewind()`, tick functions** (JOURNAL
  batch 130; batch 150 fixed destructors_007). destructors_001/004/006,
  oss-fuzz-471533782-*,
  gh9916-007/008, fibers/ticks stay unsupported: destructor, iterator
  and tick calls would need resumable frames.

- [ ] **String write whose handler keeps a copy** (JOURNAL batch 131).
  The engine writes into the handler's copy of the string too; php2go
  writes only the variable -- a wrong answer, predating the dim-write
  hold. Detect the kept copy and report unsupported, or model it.

- [ ] **bug67436: candidate include with a compile-time diagnostic** (batch 135: the leak was the harness writing library sources into the work dir, fixed; what remains is b.inc's compile warning, which excludes it as a candidate -- raise the diagnostic at include time). Previously: **bug67436 unsupported after the round-8 merge** (JOURNAL batch
  132). The computed-include agent saw bug67436/bug67436_nohandler pass
  on base8; merged with preg, hook reflection and DateTime they report
  "userland leaf PHP2Go\\Internal\\path_relation / output_rewriter is not
  in the compiled runtime" (standard.php's rename/link and
  output_add_rewrite_var). Something in the candidate compile now links
  those library bodies. Safe (unsupported), but find what pulls them in.

- [x] **Date constructors: argument-count errors** (JOURNAL batch
  133). instantiate_all_classes went passed -> unsupported once
  DateTimeZone was declared: `new DateTimeZone` with no argument is an
  ArgumentCountError in the engine; the opaque date classes refuse it.
  Raise the engine's arity errors for the date constructors.

- [x] **Link-by-name admits closures that reach a missing userland
  leaf** (JOURNAL batch 134). offsets/ArrayObject_ and
  string_container_offset_behaviour (and bug67436, bug67436_nohandler)
  are refused because a library function linked for a string literal
  reaches `PHP2Go\\Internal\\stream_socket_accept`/`path_mutation`/
  `path_relation`/`output_rewriter`, which the compiled runtime lacks.
  probeLink should refuse such closures (the function then stays a
  refused dynamic call only if it is actually called).

- [x] **Soundness: whole-program guards skipped static methods and
  hooks** (found 2026-10-01, fix pending in round 13's hand-over patch).
  `TracesDieWithFrames` (round 12 hand-over) and `throwsOnlyFresh`
  (round 4 catch release) walked only `gogen.Bodies`, which omits static
  methods and property hooks: a static method keeping a caught exception
  or throwing a stored one went unseen, so an argument its trace holds
  could be freed. Fix: `everyBody(prog)`. `markConfined` skips static
  methods for the same reason (their locals die at teardown -- safe).

- [ ] **Reflection export gaps** (JOURNAL batch 140). Constant-
  expression parameter defaults (`INF`, `K::C`, `new K`); traits under
  a parent or interface; a parent that has interfaces (child's
  implements order); constants and ancestor properties in listings;
  abstract methods; ReflectionClass of engine classes (Exception,
  InternalIterator) and engine-inherited methods.
- [ ] **`__PHP_Incomplete_Class` refusals** (JOURNAL batch 140):
  foreach, clone, get_object_vars, json_encode, comparisons, callables
  over one, method calls with arguments, unserialize_callback_func.

- [x] (fixed batch 143; non-first declarations refused) **class declared in both branches of if/else**
  (found batch 141; present since before). The engine's trial link
  declares every class whichever branch runs, so php2go gives a false
  "Cannot redeclare class" fatal where the engine runs the taken
  branch.
- [ ] **DateInterval writes and ReflectionParameter::__toString**
  (batch 141): writes to a DateInterval's known properties are refused
  (the engine converts or ignores them); ReflectionParameter export is
  unsupported.

- [x] (fixed batch 142) **file-body variables stop being globals once the
  file body is resumable** (found batch 142). When `{main}` makes a call
  that may suspend (so CPS splits it), `global $x` in a function reads
  NULL. Also: unset() of a suspended fiber holding a parked generator,
  or of a generator delegating to a Traversable, runs finally blocks at
  shutdown instead of at the unset.

- [ ] **Resumable bodies never release on unset/overwrite** (batch
  142). The ownership and release passes skip bodies with states, so in
  a split `{main}` or a fiber-driven function `unset($x)` / `$x = null`
  destroys nothing: destructors and generator finally blocks run at
  shutdown (late; output order differs from the engine).

## Variables and symbol tables

- [ ] Superglobals follow-ups (the superglobals themselves landed 2026-09-27,
  `LTM/variable-fetch-diagnostics.md`):
  - The interpreter's file-scope `get_defined_vars()` marks **every**
    variable `&`, because the top level binds each compiled variable to the
    global table with `sharedRef`, which is the program-visible marker. The
    engine marks only real reference sets. Predates the superglobal work,
    which only made the table's order and contents right.
  - An include at global scope still answers `get_defined_vars()` from its own
    compiled variables, not the global table.
  - Compiled file-scope `get_defined_vars()` is still refused: the `globals`
    map cannot say which slots a program `&` made references.
  - A standalone compiled binary gives `$_SERVER["argv"]` as `[]`, matching
    the embed engine the oracle runs. A real CLI would pass the process's
    arguments; that is a product decision, not a fidelity one.
  - Superglobals are defined by userland (the library's
    `register_superglobals_impl`), first definition winning. Nothing yet lets
    an application or host contribute its own initializer to a bundle or a
    compiled program -- today only `userland.Libraries()` supplies them.
  - `php2phpt` and `php2eval`'s interpreter arm install no userland library,
    so they have no superglobals (and none of the ~260 userland functions).
  - A resumable (generator) body in compiled output does not bind a
    library-registered superglobal such as `$_RESP_HEADERS` at entry.
  — *source: 2026-09-27 -- superglobals exist because the compile said so*

- [ ] Materialize the whole `$GLOBALS` array only after globals have insertion
  order and the runtime models superglobals. A Go map without `$_SERVER`,
  `$_ENV`, and stable order would turn the present refusal into a confident
  wrong answer. — *source: 2026-09-10 -- one opcode, two symbol tables*

- [ ] Route compiled `compact` and file-scope `get_defined_vars` through the
  generated frame's name-to-slot map. The interpreter implementation is done;
  `extract` now goes through the name table (`extractInto`, 2026-09-29 Batch
  84), and a dynamic call to any of the frame readers is refused after its
  parameter parsing, as the engine does. —
  *source: 2026-09-10 -- a variable variable needs the names at run time;
  2026-09-08 -- func_get_args reached by name needs an escape hatch*

- [ ] Implement `global ${expr}` with the expression's side effects and binding
  behavior. — *source: 2026-08-23 -- Diagnostics that route through PHP, and
  the type system; TODO section “Diagnostics”*

## Types, constants and declarations

- [x] ~~**Run the variance check for a class declared at run time.**~~ Done for plain signatures (batch 118); still open: signatures with defaults, unions or by-reference parameters, and trait methods, which need the engine's declaration spelling. A class
  whose parent or override types are only there once an autoloader or a
  conditional declaration has run is linked by the engine then, and an
  incompatible override is `Fatal error: Declaration of Y::method(): A must
  be compatible with X::method(): B` at that point. Compiled code loads the
  types in the engine's order (batch 113) but never checks them, so it
  declares the class and carries on: type_declarations/variance/
  class_order_autoload_error1-9 print the objects where the engine fatals.
  The harvest-time link_fatal covers only what the engine could link at
  compile time. Needs the override signatures, a runtime subtype test over
  the program's classes, and the engine's declaration formatting (defaults
  included), which is the risky part.
  — *source: 2026-09-30 -- Batch 113: variance load order*

- [ ] Evaluate a backed enum case whose value is a **constant expression**
  (`case C = I::A;`, enum/gh7821, prop_const_expr/enum_initializer):
  php2gotest refuses it (`enum case value <lit>`). Ordinary class constants
  already carry their AST to the emitter's ConstExpr, but the enum tables
  are built by `enumInit` closures with no Ctx, called from
  enumCasesOf/enumCaseNamed, which have none either; the case-value
  validation (enumValueFault) is also a compile-time literal check that
  would have to move to run time. — *source: 2026-09-29 -- Batch 93*

- [ ] **Overflowing a typed property has its own wording, and we use the
  generic one.** The *shape* is already right -- a TypeError, and the property
  keeps its value -- only the message differs:

      want  Cannot increment property C::$n of type int past its maximal value
      got   Cannot assign float to property C::$n of type int

  and through a reference, `Cannot increment a reference held by property ...`
  against `Cannot assign float to reference held by property ...`. Ten files in
  `Zend/tests` expect one of these.

  The write paths reach `CoerceProperty` / `CoerceRefWrite`, neither of which
  knows the operation was an increment. The `store` carries `op=incdec` and
  `setVerb` already receives a verb, but `propSetErr` drops it on the way to
  `propSet`, and `propSet` has many other callers.

  **Done for the direct property write** (`$o->n++`), via
  `php.StepRefusesOverflow` and `ThrowStepOutOfRange`; fixture
  `typed_property_overflow.php`. The **reference** variant is the larger half --
  28 occurrences against 13 -- and remains:

      want  Cannot increment a reference held by property C::$n of type int ...
      got   Cannot assign float to reference held by property C::$n of type int

  `$r++` on a reference bound to the property lowers to
  `call.intrinsic "write_var"($r, %3)` with **no** `op=incdec` attribute, so the
  store cannot tell a step from an assignment. The producing instruction can:
  `%3` comes from `call.intrinsic "increment"`. Track step-produced values at
  their def site in the emitter -- the same shape as `newOf` -- and route those
  stores through a `StoreVarStep`.


- [x] ~~Implement weak scalar-union coercion using an engine-probed preference
  order over only the union's actual arms.~~ Done 2026-09-11: `coerceUnion`,
  preference order `int, float, string, bool` with exact-membership first and a
  numeric string preferring its own numeric type. Covers properties, parameters
  and returns.

- [ ] `++`/`--` on a typed **int** property that overflows must report
  `Cannot increment property C::$i of type int past its maximal value`, not the
  `Cannot assign float to property` the coercion now gives. The engine decides
  on the shape of the *step*, so the increment has to be an operation over the
  place rather than the current `place.get` / `increment` / `store` triple.
  Same for `--` past the minimum. — *source: 2026-09-11 -- a union type was not
  applied at all*

- [x] ~~Register a typed property's declaration on a reference bound to it.~~
  Done 2026-09-11: `refBox`, `propSet`, `StoreVar` and the static path register
  and check, and rebinding and `unset` drop. The multi-source conflict message
  came with it.

- [ ] `++`/`--` **through a typed reference** past the type's limit is
  `Cannot increment a reference held by property C::$p of type int past its
  maximal value`, the reference-shaped twin of the property case above. Same
  blocker: the step has to be an operation over the place. Two corpus files
  (`typed_properties_062`, `typed_properties_044`). — *source: 2026-09-11 -- a
  reference bound to a typed property is itself typed*

- [ ] Verify intersection types and class/interface membership consistently in
  parameters, properties and runtime `instanceof`. `runtime/php` lacks the
  program class graph used by the emitted unit. — *source: 2026-08-24 --
  Intersection and DNF types; 2026-09-10 -- instanceof never matched an
  interface*

- [ ] Complete deferred constant-expression support: deferred enum case values,
  diagnostic binary operators, comparisons, and remaining AST kinds (`and`,
  `greater`, `self`, `parent`, `class_scope`). Preserve class-before-constant
  error order. — *source: 2026-09-10 -- a deferred initializer had no way out
  of the IR; 2026-09-09 -- an error a deferred initializer cannot return*

- [ ] Handle a hooked redeclaration dropping the parent's default, leaving the
  new typed backing slot uninitialized. — *source: 2026-09-10 --
  parent::$x::get()*

- [ ] Replay foldable typed-class-constant failures as compile-time fatals
  rather than a generic unsupported compile, while retaining read-time checks
  for deferred values. — *source: 2026-09-08 -- a typed class constant with a
  foldable initializer is a compile-time fatal*

- [ ] Finish declaration/link checks still absent after trait composition and
  signature work, especially internal-method prototype data and remaining
  trait abstract/static compatibility cases. Do not call Zend link helpers from
  the harvester until their cross-file memory bookkeeping is understood. —
  *source: 2026-08-23 -- Callables, and the harvester's contract; 2026-08-29 --
  why the variance family does not fall to one change*

## Calls, callables and diagnostics

- [ ] **A parameter written or unset in the body is not what traces and
  func_get_args() report**: the engine reads the live slots --
  `function f($a, $b) { $a = "changed"; unset($b); throw ...; }` traces as
  `f('changed', NULL, 3)` -- where compiled frames keep the arguments as
  passed (bug70547). Three pieces: the lowering drops `$a = "changed"` as a
  dead store when `$a` is never read again, which is observable through any
  later call or throw; a function writing a parameter would register the
  slots on its frame after the argument prologue (`BindParams`); and
  CallStack / func_get_args read through them, an unset slot as NULL.
  — *source: 2026-09-30 -- batch 105*

- [ ] Name an **inherited** virtual property by its declaring class in
  unserialize's `Cannot unserialize value for virtual property C::$p`:
  `PropertyFactsFn` answers declared/virtual but not the declarer, so a
  subclass is named instead. — *source: 2026-09-29 -- Batch 90,
  unserialize and virtual properties*

- [ ] Name a bound closure by its **run-time** scope in diagnostics. A
  file-scope closure `bindTo($obj)` gets the dummy scope `Closure`, and the
  engine then names it `Closure::{closure:FILE:LINE}` in a return-type
  TypeError; compiled code uses `e.diagName()`, a literal fixed at emission.
  `closureFrameName` already knows the rule for frames. — *source:
  2026-09-29 -- Batch 89, static return types*
- [ ] Carry the engine's parameter **defaults** for library functions, so a
  named argument that skips an optional parameter can be bound instead of
  reported `unsupported` (`call_user_func('array_slice', $a, 3,
  preserve_keys: true)`; static and dynamic paths alike). — *source:
  2026-09-29 -- Batch 89, dynamic named arguments*

- [ ] Give `Compare` a `ctx` so comparing an object with a string re-enters
  user code. `php.IsSmaller`/`php.Compare` take no ctx, so `__toString` is never
  called and the comparison silently takes a fallback where the engine can throw
  from inside the magic method. `gh418106144` and `oss_fuzz_434346548` compile
  and print nothing where the engine prints `Foo::__toString(): Return value
  must be of type string, none returned`. The signature change reaches
  `LooseEqual`, sorting and `in_array`, so it wants its own measurement. —
  *source: 2026-09-10 -- the constant-expression operator set, and a poor
  return*

- [ ] Push a generated builtin frame in `callBuiltin` so TypeErrors and
  backtraces include `#0 file(line): function(...)`. Frame-sensitive builtins
  need dispatch-site handling rather than an ordinary runtime/php lookup. —
  *source: 2026-09-10 -- exit is a function; 2026-09-08 -- func_get_args
  reached by name needs an escape hatch*

- [ ] Resolve the scope keywords inside `constant()`. `constant("self::X")` and
  `constant("static::X")` answer `Class "self" not found`; the engine resolves
  both against the calling frame. `self::` and `parent::` need only the lexical
  scope, which `scopedBuiltins` already carries after the `property_exists`
  work; `static::` needs the late-static-bound class, the same channel the
  closure LSB item wants. — *source: 2026-09-11 -- two rules about class
  constants, and one wrong fix in between*

- [ ] Give a compiled closure body a **late-static-binding channel**. A static
  closure keeps the called class while binding no `$this`, and the body reads
  LSB off `this.ClassName` -- so `static::class` inside
  `static function () {}` in a method has nowhere to come from.
  `closure_049.phpt` passed only because `$this` was wrongly bound. The body
  signature `(ctx, this, bound, clScope, args)` needs the called class beside
  `clScope`. — *source: 2026-09-11 -- a recursive closure, and a static one*

- [ ] Warn on an **undefined variable** sent to a by-value parameter through a
  dynamically dispatched call. `f($undef)` warns and leaves `$undef` undefined
  when `f` takes by value, and is silent and defines it when `f` takes by
  reference; the engine knows which because it settles `ZEND_SEND_VAR_EX` after
  `INIT` has resolved the callee, while php2go builds its argument list first.
  `php.RefArg` boxes and so defines, matching the by-ref case and losing the
  by-value warning. `named_params/undef_var.phpt` is the witness. The fix is to
  build the argument list **after** the callee is known. — *source: 2026-09-11
  -- 1642 passing: the undecided send, third attempt*

- [ ] Thread caller scope and `$this` through dynamic callable dispatch so
  `$o->$name()`, `call_user_func`, and callback-taking builtins enforce method
  visibility and the non-static/`__call` rules consistently. Unknown callable
  classes must distinguish `Class not found` from `undefined method`. — *source:
  2026-09-09 -- private methods were callable from anywhere; 2026-09-09 -- two
  refusals that were reported as undefined method*

- [ ] Complete compiled named-argument binding for unknown/dynamic callees,
  builtin arginfo, unpacking plus names, variadic string keys and unnamed
  by-reference targets. — *source: 2026-09-10 -- named arguments bind at the
  call site*

- [ ] Emit the prefer-reference warning when a callee wanted a reference but
  received a value through `ZEND_SEND_USER`/dynamic call forwarding. — *source:
  2026-09-10 -- call_user_func's send mode was already named*
  Compiled callbacks (`warnByValue`) exempt the two `@prefer-ref` library
  functions, `extract` and `array_multisort`, by name until the harvest keeps
  ZEND_SEND_PREFER_REF apart from by-reference; that name list is the thing to
  delete once it does.

- [ ] Include target parameter metadata in `var_dump` of a first-class
  callable. Function and bound-object metadata are already present. — *source:
  2026-09-09 -- `f(...)` is a call node nobody performs; 2026-09-10 -- named
  arguments bind at the call site*

- [ ] Normalize `__METHOD__` inside a closure to the reported compile path in
  the same way closure metadata is normalized. — *source: 2026-09-10 --
  autoloading, and what a poor ratio is telling me*

- [ ] Correct `classNameFrom`'s invalid-subject error through `f.raise` so a
  non-string/non-object class expression is catchable and uses PHP's message.
  — *source: 2026-08-23 -- The harness was still lying, and then a batch of
  semantics*

- [ ] Finish the two remaining `#[\Deprecated]` output/order cases: closure
  naming and class-constant notice ordering. — *source: 2026-09-08 --
  `#[\Deprecated]`: what is left*

- [ ] **A generator destroyed mid-iteration must run its `finally`.** When an
  error aborts a `foreach` or an array unpack over a generator, the engine
  destroys it, the `finally` runs, and a `throw` there **supersedes** the
  original error. `generators/gh11028_3` is the case: a key-type error is
  replaced by the `finally`'s exception. Compiled output raises the first error
  and never destroys the generator, so the replacement never happens. Same
  lifetime gap as destructor timing, reached by a different route.
  — *source: 2026-09-18 -- two bugs in six lines of ArrayUnpack*

## Runtime libraries and engine classes

- [ ] **The `precision` INI setting is not modelled**: float-to-string
  conversion always uses 14 significant digits, so `ini_set('precision',
  0)` leaves `(string) 1.75` as "1.75" where PHP says "2" -- and a float
  compared with a non-numeric string is compared through that string
  (string_to_number_comparison). Needs the Ctx in float conversion.
  — *source: 2026-09-30 -- batch 111*

- [ ] **The interpreter's ZEND_IN_ARRAY compares the needle with the
  table's values**, which are all `true` -- the compiler made the haystack's
  values the table's keys -- so a loose search finds almost anything
  (in_array_const mismatches under php2eval). The compiled path reads the
  keys (inArrayConst); the interpreter's decoding of the literal normalizes
  `"1"` to `1`, so it needs the key kinds kept to tell a numeric-string
  haystack from an int one.
  — *source: 2026-09-30 -- batch 109*

- [ ] **The interpreter names the second missing-include warning
  `include()` for every optional form**; the engine says `include_once():
  Failed opening ...` for include_once (probed; the compiled path has it
  right -- runtime/interp/include.go's comment is the stale one).
  — *source: 2026-09-30 -- batch 109*

- [ ] **`zend.exception_string_param_max_len` is not modelled**: ini_set
  answers false and traces always cut string arguments at 15 bytes
  (runtime/php traceStringLimit). The engine accepts 0..1000000, refuses
  others with false, and reads the setting when the trace is rendered --
  which needs a Ctx in traceArg, reached today from ThrowableChainString
  and BacktraceString without one (exception_024, exception_025).
  — *source: 2026-09-30 -- batch 104*

- [ ] **ReflectionClass and the other reflectors have no `__toString`**:
  the engine's export format (`Class [ <user> class A ] { ... }`) is
  refused as unsupported (ctor_promotion_mixing, bug43200, reflection001,
  init_trigger_reflection_object_toString).
  — *source: 2026-09-30 -- batch 104*

- [ ] **A library function refusing a class-qualified callback skips the
  deprecation**: `array_map(["Leaf", "Mid::prot"], ...)` should say
  `Callables of the form ["Leaf", "Mid::prot"] are deprecated` before its
  TypeError, as call_user_func does (checkUserCallable). The library's
  check is ctx.CheckCallable in the runtime; is_callable and
  call_user_func deprecate on their own, so the hook has to be told which
  caller already did.
  — *source: 2026-09-29 -- batch 97*

- [ ] **Link fatals the trial link cannot decide**: batch 101 has the
  engine trial-link the primary script's top-level class declarations, but
  a declaration inside a function, one after the first fatal, and one whose
  link depends on an autoloader (no verdict) are not checked; nor is the
  run-time `Cannot redeclare class` (name_collision_*, refused). 49 of
  bh114's 224 newly graded failures remain after it.
  — *source: 2026-09-30 -- batch 101*

- [ ] **A declared type naming a class alias does not accept the aliased
  class**: `class_alias('X', 'Y'); ... public Y $prop;` then `$b->prop =
  new X` is `Cannot assign X to property B::$prop of type Y`, where the
  engine resolves Y to X. The runtime's type check compares names without
  the program's alias table.
  — *source: 2026-09-30 -- batch 102*

- [ ] **A closure's `static` variables are per closure object**, not per
  declaration: each `function () { static $n; }` evaluated anew starts its
  own, and bindTo copies the current values (bug64979). The emitter keys
  them by declaration (`callableStatics`, staticsKey), so every closure
  made from one declaration shares them.
  — *source: 2026-09-30 -- batch 99*

- [ ] **round ignores `$mode`**: `round(2.5, mode: PHP_ROUND_HALF_DOWN)` is
  2.0 in the engine and 3.0 here.
  — *source: 2026-09-30 -- batch 99*

- [ ] **json_encode ignores `$depth`**: `json_encode([1, [2]], depth: 1)` is
  false in the engine (depth exceeded) and `[1,[2]]` here.
  — *source: 2026-09-30 -- batch 99*

- [ ] **Doc comments are not harvested**, so `getDocComment()` on every
  reflector is unsupported (traits/constant_021).
  — *source: 2026-09-29 -- batch 96*

- [ ] **A by-reference `foreach` over an `ArrayIterator` built on an object
  writes the object's properties** (typed_properties_113, 114), through an
  IteratorAggregate's `getIterator()` too, and a typed property's box
  carries its type (`Cannot assign array to reference held by property
  A::$foo of type string`). NewRefIterator iterates every Traversable by
  value; the ArrayStore knows its owner object, so a live cursor over the
  owner's property table (boxes via PropRefAt, which attaches the type) is
  the shape. Over an array the writes land in the iterator's own copy, which
  is invisible either way.
  — *source: 2026-09-29 -- batch 72*

- [ ] **Four operations still do not initialise a lazy object.** The full table,
  enumerated against the engine rather than reasoned about one at a time:

      initialise:  read, write, clone, serialize, json_encode,
                   get_object_vars, foreach, isset, unset, ==
      do not:      var_dump, print_r, (array) cast, count

  php2go did **none** of them. Done: `clone`, `json_encode`,
  `get_object_vars`, `foreach` -- the last three from one edit in
  `visibleProperties`, which is the path that reads values, deliberately not
  `objectProperties`, which is what the dumps walk.

  Also done: **`isset`** (in `existsProp`) and **`unset`** (in `place.unset`).

  Still missing: **`serialize`** and **`==`**. Both live in `runtime/php`
  rather than in emitted code, which is the only reason they lag -- the
  comparison in `ops.go` would need a `*Ctx` it does not currently take, and
  `serialize` reaches an object's properties through a different path from the
  one `visibleProperties` serves. `serialize` is the one the corpus asks for
  most (`init_trigger_serialize` and friends).

  Fixture `lazy_init_triggers.php` holds the settled half **and the four that
  must stay lazy**, which matter just as much: a fix aimed at the first group
  is one edit away from breaking the second.

- [ ] **`min`/`max` break ties differently with two arguments than with three.**
  Found while building the negative-zero fixture, where it shows up as a sign:

      min(-0.0, 0.0)      float(0)     <- the *second*
      max(-0.0, 0.0)      float(-0)    <- the *first*
      min(1, 1.0)         float(1)     <- second
      max(1, 1.0)         int(1)       <- first

  but with three, both keep the first:

      min(1, 1.0, "1")    int(1)       max(1, 1.0, "1")    int(1)
      min("1", 1.0, 1)    string "1"   max("1", 1.0, 1)    string "1"

  `extremum` in `runtime/stdlib/typefns.go` replaces only on a strict
  improvement, so it keeps the first everywhere -- right for the N-argument
  form and for `max`, wrong for two-argument `min`. PHP special-cases the
  two-argument call (a frameless handler), which is the likely cause.

  **Not encoded**, deliberately: the rule above is four measurements and a
  guess at the mechanism, and a tie-break written from a guess is the kind of
  thing that looks right until some array form disagrees. Settle it against the
  engine across the two-, three- and array-argument forms first.

- [ ] **`var_dump` of a Closure: the layouts are right, the long tail is not.**
  The two layouts are settled (a literal shows name/file/line, a named callable
  shows `function`, plus `this` and `parameter` when non-empty) and
  `first_class_callable_optimization` and `gh8962` now differ from the engine by
  an object *handle number* alone. What remains, each its own sub-case:

  - **A named callable shows the callee's `static` variables.** `func(...)`
    where `function func() { static $i; }` dumps

        object(Closure)#1 (2) { ["function"]=> "func"
                                ["static"]=> array(1) { ["i"]=> NULL } }

    Our `static` carries only a closure's `use` captures, so a named callable
    shows none. Needs a runtime table of each function's statics and their
    current values, which `declareStatics` already emits per function but does
    not index by name. Worth `gh8083`, and probably `bug79778` and
    `call_user_func_003`, which both want one entry more than we give.
  - **An arrow function auto-captures only what exists**, and the IR does not
    say which closures are arrow functions. `fn() => [$undef1, $undef2]` shows
    three entries and no `static` at all, while an explicit
    `function () use ($nope) {}` **warns** and lists `nope => NULL`. Two
    different rules, measured separately; the second is not the first with the
    warning suppressed.

    Both lower to the identical `bind_lexical %0, $undef, use=value`, so
    telling them apart needs `ZEND_ACC_ARROW_FUNCTION` harvested onto the
    closure. Worth `bug81076`, plus whatever silently warns today where PHP
    does not.

- [x] **A lazy *proxy* shows one entry where we showed none.** Diagnosed and
  fixed: an **initialized** proxy presents a single pseudo-property `instance`
  holding the object it forwards to, and is still labelled `lazy proxy` where a
  ghost becomes an ordinary object once its state arrives. It was falling
  through to the proxy shell's own property table, which is empty. Measured
  across all four states -- uninitialized ghost and proxy, initialized ghost and
  proxy -- because only one of them has an `instance` and the other three would
  each have been a plausible guess. Fixture `lazy_proxy_dump.php`.

- [ ] **The rest of the reflection surface, and why the census undercounts it.**
  Probing `ReflectionClass` one method at a time found **19 of 20 missing**
  where the refusal census named only three. The census reports a file's
  *first* refusal, so one missing method hides every later one in the same
  file; a surface is best sized by probing it, not by counting reasons.

  Filled since (2026-09-19): `getValue`/`setValue`/`isInitialized`/`getType`/
  `hasType` and the property predicates; `getProperties`; `getConstants`,
  `getConstant`, `hasConstant`, `hasProperty`, `hasMethod`, `getParentClass`,
  `isAbstract`, `isFinal`, `isEnum`, `isInstantiable`, `implementsInterface`,
  `isSubclassOf`, `getStaticProperties`, `getTraitNames`, `getConstructor`,
  `getMethods`; the `ReflectionMethod` visibility predicates and
  `setAccessible`.

  Still missing, each needing a class that does not exist yet:

  - `ReflectionClass::getReflectionConstant` -- wants `ReflectionClassConstant`.
  - `ReflectionFunction::getParameters`, `ReflectionMethod::getParameters` --
    want `ReflectionParameter`.
  - `getReturnType` on a function or method -- the `ReflectionNamedType` now
    exists, but nothing harvests a **return** type into the emitter's tables.
  - A union or intersection type refuses rather than answering, because
    `ReflectionUnionType` and `ReflectionIntersectionType` do not exist. The
    one exception is `iterable`, which is a union inside the engine
    (`Traversable|array`) and a *named* type to reflection; that rewrite is
    explicit and deliberately covers nothing else.
  - [x] `ReflectionClass::isInterface` -- done. The blocker was real (no
    interface marker on the emitter's `Class`) and was removed as a side
    effect of `get_declared_classes()` needing the same fact. The IR had
    carried `KIND_INTERFACE` all along. `isInstantiable` was wrong for an
    interface for the same reason and is fixed with it.
  - `ReflectionClass::getFileName`.

- [ ] **An engine class's constructor pushes no stack frame.** An uncaught
  `ReflectionException` from `new ReflectionProperty("D", "x")` prints
  `#0 {main}` where PHP prints
  `#0 file(15): ReflectionProperty->__construct('D', 'x')`. `initReflector`
  raises without `ctx.PushCall`, and the same is true of the other engine-class
  constructors, so the gap is one frame wide and general. Found while writing
  `reflection_method.php`, which now catches the exception rather than letting
  it reach the trace.

- [ ] **The 80 files that print nothing at all.** Found by cutting the failures
  a different way -- by *output size* rather than by reason -- which is worth
  remembering as a technique: `got 0B` against a non-empty `want` is 80 files,
  and there are **zero** the other way round.

  The shape is single: **PHP raises where compiled output silently continues.**
  The test's whole output is the caught message, so producing no diagnostic
  produces no bytes. They are not one fix, though. Sampled causes:

  | want | cause |
  |---|---|
  | `Keys must be of type int|string during array unpacking` | spreading a Traversable with a bad key type |
  | `Undefined constant "X"` | a constant fetch that should throw |
  | `Attribute "A" cannot target function` | **done**, see the journal |
  | `Foo::__toString(): Return value must be of type string, none returned` | a magic method's return type |
  | `define(): Argument #N ($constant_name) cannot be a class constant` | builtin argument validation |
  | `by_ref(): Argument #N ($ref) could not be passed by reference` | a by-reference argument that cannot be one |

  Each is its own missing diagnostic, and each is small. This is the most
  tractable remaining seam now that refusals are exhausted -- unlike the
  lifetime cluster, none of it needs a refcount.
  — *source: 2026-09-18 -- the files that print nothing*

- [ ] **`resetAsLazyGhost` / `resetAsLazyProxy`** -- 18 files, and the last
  sizeable piece of the lazy surface. The rule is in
  `.agents/docs/LTM/lazy-objects.md` under "The reset rule", and it has two
  halves that are easy to get half-right:

  - A **readonly** property that already holds a value survives the reset when
    the object's class is **final** -- nothing can reach it to write it
    legitimately, so the value stands and the reconstructing `__construct` is
    refused. A non-final class clears it and `__construct` may set it once more.
  - The kept property must **also be marked `Skipped`**. Keeping the value alone
    is not enough: the ghost's `restoreDefaults` pass wipes it on the way into
    the initializer otherwise. Setting the mark also gets the tail case right
    for free -- reset a final class whose only property is a kept readonly one
    and nothing is deferred, so the object is not lazy and the initializer never
    runs.

  Also: the reset walks the **reflected** class's properties, not the object's,
  so resetting an instance of `B extends A` through `ReflectionClass(A::class)`
  leaves B's own properties alone.

  What is missing to implement it shared: **class finality**. `php.Declaration`
  already carries `Readonly`, so the readonly half needs nothing new, but
  `gogen.Class` has no `Final` field and emits no table for it -- the protobuf
  has `bool final` and the interpreter reads it. Carrying it is the prerequisite,
  the same shape the property descriptor needed.
  — *source: 2026-09-18 -- the lazy raw writers and forced initialization*

- [ ] **The compiled arm's lazy-proxy compatibility check is wrong, not merely
  incomplete.** `internal/gogen` installs `SetProxyCompatible` as
  `strings.EqualFold(proxyClass, instanceClass)`. PHP's rule is broader: a
  **subclass may proxy its parent** when the two have the same number of
  `properties_info` entries and neither overrides `__destruct` or `__clone`.
  So `class D extends B { public int $b; }` -- a redeclaration, which shares the
  parent's entry and adds nothing -- is a legal proxy for a `B`, and compiled
  output raises `TypeError: The real instance class B is not compatible with the
  proxy class D...` instead. **That is a wrong answer where PHP succeeds**, which
  is worse than the refusal it replaced, and it is mine: the narrow hook was
  written as a placeholder and shipped without the rule being read.

  Everything the real rule needs is already emitted: `declaredProps` gives the
  count, `extendsClass` gives the instance-of test, and `methodOwnerOf` resolves
  `__destruct`/`__clone` to a declaring class. Counting is the point --
  `default_properties_count` is the wrong number, and the engine's own comment
  says it is off by the number of overrides. See
  `.agents/docs/LTM/lazy-objects.md`, "What the initializer may hand back".

  Until it is fixed the honest behaviour is a refusal rather than a TypeError,
  but `ProxyCompatible` returns a bool and cannot express one -- so the fix is
  to implement the rule, not to widen the hook's signature.
  — *source: 2026-09-18 -- lazy ghosts reach compiled output*

- [ ] **Lazy objects in compiled output.** The largest remaining cluster: 211
  files (`newLazyGhost` 144, `newLazyProxy` 47, `resetAsLazyGhost` 13,
  `newInstanceWithoutConstructor` 7), of which **69** are clear of the
  handle/`__destruct` screen -- and that screen is unusually conservative here,
  since a lazy-object test dumps the object almost by definition, so some of the
  other 142 should pass too.

  The blocker is gone. `php.PropertyDesc` and the `DeclaredProperties` hook
  exist, `RestoreDeclaredDefaults` is shared, both back-ends supply the hook, and
  the differential suite plus all 245 fixtures pass over it. What remains:

  1. **Extract `resolveLazy`** (`runtime/interp/lazy.go:31`). Its three
     interpreter dependencies are all answerable from the descriptor now:
     `findClass`/`hookedProperty` collapse into `PropertyDesc.Virtual`, and
     `declaresProperty` is `Ctx.DeclaresProperty`. Note its guard is
     `object.Lazy == nil`, so the cost on an ordinary object is one nil check --
     which is what makes step 2 affordable.
  2. ~~**Install the materialization path in compiled output**~~ -- **done.**
     The property-access side landed over several ticks; `SetLazyResolver`
     itself was bound on 2026-09-19, which is what made `serialize` and
     `json_encode` see a lazy object's state instead of an empty shell.
     (Original note: `internal/gogen` installs no `SetLazyResolver`, so nothing
     a ghost does is observable there.) Every property access has to ask first --
     `propGet`/`propGetMode`, `propSet`/`propSetErr`, `propPlace`/`propPlaceRW`/
     `propPlaceUnset`, plus `visible` and `objectProperties` for the dump. That
     is the pervasive part and the reason it was not bolted onto the tick that
     unblocked it.
  3. **The `ReflectionClass` surface**: `newLazyGhost`, `newLazyProxy`,
     `resetAsLazyGhost`, `resetAsLazyProxy`, `initializeLazyObject`,
     `isUninitializedLazyObject`, `markLazyObjectAsInitialized`, and the two
     `ReflectionProperty` raw writers. `.agents/docs/LTM/lazy-objects.md` is the
     specification and is mostly rules that were wrong once -- read it before
     writing any of them, particularly the reset rule for a final class's
     readonly property and the fact that laziness is **per property**, not per
     object.
  — *source: 2026-09-18 -- the property descriptor that unblocks lazy objects*







- [x] ~~**Promotion erases the release point**~~ -- **done 2026-09-20.** The
  renamer leaves a `release_prev` marker where it drops a store, and `unset` is
  recognised by the escape walk as a death rather than a use. Measured +2 on the
  corpus; the handle-number family was unchanged at 125, because those objects
  escape into containers and are beyond static confinement -- see the entry
  "Release points: the machinery works, and it bounds itself". Original note
  follows.

- [ ] ~~**Promotion erases the release point, and that is now the single blocker
  on destructor timing inside functions.**~~ `ReassignedLocals` works on any body
  as of 2026-09-20, but in a *function* it finds nothing to claim, because
  `Mem2Reg` drops the stores:

      function f() { $a = new D("one"); echo $a->t; $a = new D("two"); }
      // engine: one, dtor one, two, dtor two
      // ours:   one, two, ... both dtors at frame end

  Measured: with `-no-mem2reg` the same file **passes**, and the debug shows
  `writes=map[$b:[%0 null]]`, `sole=true`, `local=true`. With promotion on,
  `writes` is empty. So the analysis is right and has nowhere to attach.

  **The fix, concretely.** The renamer already knows everything needed at the
  moment it drops a store: it is walking in order and holds `cur[place]`, the
  value the local held *before* the write. So instead of dropping such a store
  outright, leave a marker instruction carrying the previous value -- a
  `release_prev <value>` -- which the lifetime analyses may claim and the
  emitter turns into `RunConfinedDestructor` + `releaseObjectID`, or ignore.

  **Two things to get right.** The marker mentions a value, so every escape
  walk must be taught it is *not* a use (`value`, `local`, `derived` all refuse
  on unrecognised opcodes -- see the `derived()` bug from the same day, where a
  missing mention guard cost 30 files). And it must not resurrect the
  `FrameBound` confusion: a marker says "this local stopped holding that value
  here", which is exactly the fact promotion destroys and no later pass can
  recover.

  Worth: the `gc_00N` lazy cluster (6), part of the `weakrefs/gh10043` set, and
  an unknown share of the 136 remaining handle-number failures -- every family
  whose remainder is "the object is not collected when its last local drops it".
  — *source: 2026-09-20 -- unset and null-assign inside a function*


- [x] ~~**Engine interfaces beyond `Throwable` are not kind-aware in
  `*_exists`.**~~ -- **done 2026-09-20**, same tick: `engineInterfaceKind` is
  now emitted from the *harvest* (`Program.EngineInterfaces`) unioned with the
  `engineInterfaces` set the emitter already trusts for `instanceof`, rather
  than from the three-entry `engineInterfaceNames` list that exists only to
  order `interfacesOf`. Verified against the engine for `Countable`,
  `ArrayAccess`, `Stringable`, `Traversable`, `Iterator`, `JsonSerializable`,
  `Throwable`, `Exception` and `ArrayObject`. Original note follows.

- [ ] ~~**Engine interfaces beyond `Throwable` are not kind-aware in
  `*_exists`.**~~ `existsAsKind` (2026-09-20) answers correctly for the program's
  own classes, for the modelled engine classes (`engineClassKind`), for the
  three in `engineInterfaceKind`, and for the whole throwable hierarchy via
  `php.IsThrowableClass`. The engine interfaces that are neither -- `Countable`,
  `ArrayAccess`, `Stringable`, `Traversable`, `JsonSerializable` and the rest --
  fall through to the default arm and are reported as *classes*, so
  `interface_exists('Countable')` is false and `class_exists('Countable')` is
  true, both backwards. `interfacesOf` in the generated program holds them but
  cannot say which of its keys are interfaces, which is precisely the
  distinction needed. The fix is the same shape as the one already made: emit
  them into `engineInterfaceKind` from the harvested interface list rather than
  only the subset currently written there.
  — *source: 2026-09-20 -- the *_exists family*


- [ ] **Attributes written on a closure are not carried.** `#[A('x')] function
  () {}` and `#[A('x')] fn() => 1` both give
  `count((new ReflectionFunction($f))->getAttributes()) === 0` where the engine
  gives 1. The rest of the attribute-reflection surface is fine -- measured for
  a class, a named function, a method and a property, all four of which report
  `getName`, `getArguments` and `newInstance` correctly.

  `collectClosures` (cmd/php2gotest/main.go) lowers each closure and records its
  body under `lower.ClosureKey(parent, i, name)` but never reads
  `c.GetAttributes()`, and `Program` has no closure-attribute table beside
  `FunctionAttributes`. Needs: collect them there, emit a table keyed the same
  way, and teach the emitted `ReflectionFunction` to look a Closure up by its
  key rather than by a function name. Worth about 2 corpus files
  (`attributes/005_objects`, `attributes/017_closure_scope`), which is why it
  is filed rather than done.
  — *source: 2026-09-20 -- the attribute reflection surface*


- [ ] **Engine-invoked user code is missing three more stack frames.** Probed
  2026-09-23 against the engine while fixing the method-frame spelling; each is
  a frame the engine pushes where compiled output pushes none, and each is a
  distinct site rather than one shared cause:

  - `strlen($obj)` coercing through `__toString` gives no
    `Bad->__toString()` frame. The `echo "$obj"` spelling does, so the gap is
    the coercion inside the builtin argument check, not `__toString` itself.
  - `ReflectionAttribute->newInstance()` has no frame of its own, and the
    attribute's `__construct` frame is located at the `newInstance()` call
    rather than at the attribute's **declaration site**, which is where the
    engine puts it.
  - A property hook has no frame: the engine renders `Hooked->$p::set(5)`.

  `yield from new It()` reaching `getIterator()` was in the same probe and is
  now right, so the `IteratorAggregate` half of this family is closed.

  **The engine-class half generalises and is not done.** Fiber is fixed
  (2026-09-23) because it was the one branch in `callMethod` that already
  pushed a frame. The others push none, and each is its own branch:
  `weakCall` (WeakMap, WeakReference), `reflectorMethod` (the whole Reflection
  surface), `ctx.ArrayStoreMethod` (the array-backed classes), and the engine
  **constructors**, which are not reached through `callMethod` at all --
  `new WeakReference()` and `new ReflectionClass("NoSuchClass")` both raise
  with the right message and no frame. Probed against the engine 2026-09-23;
  the wanted frames are `WeakReference->__construct()`,
  `ReflectionClass->__construct('NoSuchClassHere')`,
  `ReflectionClass->getMethod('nope')`.

  **Mostly done 2026-09-23.** `engineFramed` gives `weakCall` and
  `reflectorMethod` one shared push; `engineMethodOwner` answers both the
  declaring class and its spelling from the harvest
  (`EngineMethod.Name`, i.e. `fn->common.function_name`), walking the
  receiver's parent chain so a user subclass resolves to its engine ancestor.
  The hand-written Fiber list is gone.

  The **constructors** are done too (2026-09-23): `runCtor` splits, and an
  engine class claiming `__construct` gets the same framed call. Note the
  constraint it works under -- PHP captures a throwable's trace at `new`,
  before the constructor runs, so no frame may be pushed around the
  exception-class arm. It is not, because the exception classes are outside the
  modelled engine set; the fixture asserts that rather than leaving it to
  chance.

  Still open, and a different rule from the rest: an attribute's
  `__construct` reached through `ReflectionAttribute->newInstance()` is located
  at the **attribute's declaration site**, not at the `newInstance()` call and
  not at `[internal function]`. `newInstance` itself wants a frame too.

  While there: `engineHasFiberMethod`, `engineHasWeakMethod` and
  `engineHasReflectorMethod` are each a transcribed method list in gogen, and
  the same harvest could now replace all three.

- [x] ~~**`Fiber::suspend()` propagating into a caller.**~~ **Closed
  2026-09-25.** Steps 1-3 of the CPS plan plus the generator-driver half:
  `396 -> 379` refusals with **+13 passes** across `bg4`, `bg5`, `bg6`. A fiber
  now suspends through an ordinary call, a method call, an `__invoke` object, a
  generator driver call, and a `yield from` chain including an
  `IteratorAggregate`. Four files remain, and the rest of the family is now the
  object-lifetime work below.

- [ ] **`yield from` loses the delegating generator's frame.** A throw two
  levels down traces `#0 from(2)` then `{main}`; the engine has
  `#1 outer()` between them. `Generator.resume` advances the delegate without
  pushing the delegating generator's own frame.

  Probed 2026-09-23, and the naive fix was **tried and reverted**: pushing the
  frame there gives the right structure with the wrong lines. The engine
  locates the delegate's frame at the `yield from` site *inside the delegating
  body*, while `ctx.Location()` at that moment is the driver's -- the body
  moves the reported location as it runs, and the driver call restores it
  before the delegate advances. So the generator has to keep the position it
  suspended at, and `resume` has to set it before `g.delegate.Advance`.
  Structure alone fails the same files, so this is all-or-nothing.

- [ ] **A class constant whose initializer threw answers `NULL` the second
  time.** The engine re-raises on every read; compiled output caches the
  failure as a value. Probed 2026-09-23 while writing the
  `[constant expression]` fixture:

      class N1 { const K = 1 % 0; }
      class N2 { const K = N1::K + 1; }
      var_dump(N2::K);   // DivisionByZeroError, as expected
      var_dump(N2::K);   // engine: raises again. us: NULL

  The fixture avoids a second read so it tests the frames rather than this.

- [ ] **Seven findings from the userland miscompilation probe remain open.**
  The probe (2026-09-20) runs `differential_test.go`'s userland cases through
  `php2gotest` instead of the interpreter; 300 cases gave 289 passes and 9
  failures, each a real defect the 4,986-file corpus does not cover. Two are
  fixed (`$GLOBALS` wording, string-offset references). The rest, with the case
  that shows each:

  - ~~`ob_start("strtoupper")` callback arity~~ -- **fixed 2026-09-20**: the
    cause was `callValue` invoking a looked-up builtin *directly*, skipping
    `callBuiltin` and every argument check with it, so any builtin reached as a
    callable was unverified. The stack-trace half is **fixed 2026-09-23**: two
    causes, both frames that were never pushed rather than frames rendered
    wrongly. `callValue`'s named-function branch called the body directly, and
    `callBuiltin` pushed its frame *after* `verifyBuiltinArgs`, so the argument
    check raised outside it. `PushCall` now states the location rule once -- a
    frame pushed under an `Internal` one is at `[internal function]` unless
    `Ctx.CallSite()` says otherwise -- and the probe has no failures left.
  - **`usort($undefined, "strcmp")` answers `false` where the engine raises**
    `TypeError: usort(): Argument #1 ($array) must be of type array, null
    given`. Found incidentally while writing the callable-builtin fixture. A
    **by-reference** parameter is skipped by `verifyBuiltinArgs` on purpose --
    "the native implementation parses its own and may deliberately narrow the
    declared type" -- and that exemption is too wide: the engine still type-checks
    the by-ref argument for the array functions.
  - `function t(&$v) {} t($arr[]);` -- a by-reference append target. The engine
    creates **one** element; we create two.
  - ~~**A by-reference argument does not autovivify a *nested* target.**~~ --
    **fixed 2026-09-20.** The parent box was released where the inner place was
    *built* rather than where it is *used*, so the storage was gone before
    anything wrote through it. The release now travels on the place
    (`place.release`, composed by `place.chain`). Original note follows.
  - ~~**A by-reference argument does not autovivify a *nested* target.**~~
    `byref($n["a"]["b"])` leaves `$n["a"]` as NULL where the engine builds the
    inner array and binds the element. **Not** about the send mode: the static
    and dynamic spellings fail identically, so it is the place-opening path
    (`refBox` on a nested `dim.ref`) rather than how the argument is sent. Found
    while fixturing the undecided-send fix, which is why the fixture stops short
    of it.
  - ~~**A by-reference parameter is not bound through a *dynamic* call.**~~
    `function byref(&$x){$x=5;} $f="byref"; $f($a["k"]);` leaves `$a` empty and
    warns `Undefined array key "k"`, where the engine autovivifies the element
    and binds it. The same call written statically -- `byref($a["k"])` -- is
    already correct, and so is a closure receiver, so this is specifically the
    *undecided* send: `preferref`/`SEND_VAR_NO_REF`, where only the callee knows
    whether it wanted a reference. The call site currently reads the element
    instead of passing storage. Affects `c0197` and `c0200` in the probe.
  - `$n = "nope"; $$n .= "x";` -- a compound assign through a variable variable
    on an undefined name.
  - A namespaced call to a missing function words its
    `Call to undefined function Ns\nope()` differently.
  - Two array/object cases (`c0197`, `c0200` in the probe directory) where we
    print nothing and the engine prints a value.
  - `print_r` recursion: the long-standing `*RECURSION*` failure, which is also
    the fixture suite's one standing failure.

  Rebuild the probe with the extractor recorded in the journal entry; it takes
  about a minute and is worth re-running after any change to `internal/lower`.
  — *source: 2026-09-20 -- turning the userland library into a miscompilation probe*

- [ ] **No portable way to write a resource fixture for compiled output.** The
  first-resource-id change (2026-09-20) has no `cmd/php2gotest/testdata` case,
  which is a deliberate exception to "a differential case for every behavioural
  change" and should be closed when it can be. Every portable source of a
  resource is `unsupported` in compiled output today -- `tmpfile()`,
  `php://memory`, `php://temp`, `php://stdout`, `tempnam` -- and `fopen(__FILE__)`
  is environment-dependent, because the compiled program runs from a different
  working directory than the fixture lives in. The evidence for the change is
  instead the nine corpus files it fixes plus a direct before/after on both
  harnesses (`php2phpt` 14/14 either way, `php2gotest` 9 failures -> 0).
  Supporting `php://memory` would close this and is worth more than the fixture.
  **php://memory is supported now (batch 112)**, so this case can be written.
  — *source: 2026-09-20 -- resource ids*

- [ ] **`==` on a lazy object does not initialize it.** Measured against the
  engine: of the fourteen surfaces probed, `serialize`, `json_encode`,
  `get_object_vars`, `var_export`, `foreach`, `clone`, `isset`, a property read
  and `==` all initialize, while `print_r`, `var_dump`, `(array)`, `array_walk`
  and `count` deliberately do not. After the `var_export` fix (2026-09-20) the
  only remaining divergence is `==`.

  The obstacle is a signature: `php.LooseEqual(a, b Value) bool` takes no
  `*Ctx`, so it has nowhere to call an initializer from. The mechanism to fix
  it already exists -- `runtime/php/catalogue.go` supports `Ctx: true` on a
  `Variant` (see `concat` and the `increment` family) -- so the work is a
  ctx-taking comparison variant that materializes both operands, not new
  plumbing. Deliberately not done in one pass because it changes the calling
  convention of the comparison path used everywhere; it wants its own tick and
  its own measurement. Worth roughly the `lazy_objects/init_trigger_compare`
  family.
  — *source: 2026-09-20 -- the lazy trigger census*

- [ ] **A lazy object's destructor does not run when the object is dropped, so
  its initializer's captures survive.** The `gc_00N` + `gh15999_001` cluster (6
  files) is one shape: a `Canary` captured by the initializer closure, then
  `$obj = null` inside a *function*. The engine drops the lazy object, releases
  the initializer, releases the closure's captures, and the canary's
  `__destruct` prints. We keep all three. Needs the reassignment release to work
  inside a function body (today it is file-body only, because promotion erases
  the write) **and** transitive release through closure captures -- so it is
  downstream of refcounting, not a lazy-object gap.
  — *source: 2026-09-20 -- the lazy trigger census*

- [ ] **The reassignment release is per *local*, where the engine is per
  *value*.** `ReassignedLocals` claims a local only when **every** object it
  ever holds is solely owned, so

      $q = new D('q1'); $keep[] = $q;   // q1 escapes
      $q = new D('q2');                 // no release claimed for $q at all
      $q = null;                        // engine destroys q2 here; we wait

  destroys `q2` at script end rather than at the `null`. Safe (never earlier
  than the engine) but late. Making it per value needs flow sensitivity the
  walk does not have: `escapes.value` delegates to `escapes.local` as soon as it
  sees the binding, which is exactly where the per-value precision is lost.
  Pinned in `reassign_releases.php`, whose comment says why the case stops where
  it does.
  — *source: 2026-09-20 -- the reassignment release*

- [ ] **Engine methods need a non-retention answer for their *arguments*, not
  just their receiver.** `nonRetainingEngineMethod` answers "does this method
  store `$this`", which is what `leaksThis` needs. But `methodSafe` refuses any
  value appearing as a method **argument**, so

      function test(object $obj) { $r = new ReflectionClass($obj::class);
                                   $r->initializeLazyObject($obj); ... }

  reads as retaining `$obj`, and the caller cannot release it. This is the last
  blocker on the `+2` handle cluster: with `clone`, `class_name` and
  `instanceof` now understood as reads, `initializeLazyObject($obj)` is what is
  left. Measure it the way the receiver list was measured, as its own
  allow-list -- the two questions are different and must not share a list.
  — *source: 2026-09-20 -- the reassignment release*

- [ ] **The `+2` handle cluster is blocked on local reassignment, not on lazy
  objects.** 63 of the remaining same-size handle failures are `+2`, and 42 of
  those are `lazy_objects` -- which reads like a lazy-object gap and is not.
  Traced `clone_initialized.phpt` against the engine: the Ghost section matches
  exactly, and everything after it is off by one because the test does

      $obj = $reflector->newLazyGhost(...);   test('Ghost', $obj);
      $obj = $reflector->newLazyProxy(...);   test('Proxy', $obj);

  and PHP frees the ghost **at the reassignment of `$obj`**. Every reconstructed
  probe passed until the reassignment was included; that is what the cluster is
  waiting on, and the lazy machinery around it is already correct.

  What it needs: at `write_var($local, v)` in a body, release the value the
  local held when that local is its only owner. In `{main}` the slot survives
  promotion, so the write is visible -- but "only owner" there must also account
  for `global $x` in any other body (a `bind_global` of that name) and for
  `$GLOBALS`, neither of which the frame-local escape rules consider.
  — *source: 2026-09-20 -- handle numbering: clone, the reflector receiver*

- [ ] **Two `Ctx` hooks the emitter still does not install, with reasons.** The
  2026-09-19 audit walked all 39 `func (c *Ctx) Set*` hooks in `runtime/php`
  against what `internal/gogen/gogen.go` binds. Eleven are unbound. The rule
  learned there first: **an unbound hook is not automatically a gap** --
  `SetErrorReporting` is unbound and `error_reporting()` works anyway, because
  the behaviour is supplied elsewhere. Probe before filing. What survived the
  probe:

  - `SetWeakObserve` / `SetWeakRelease` -- **a real gap, blocked elsewhere.**
    In compiled output `$a = null` on the last holder of a `WeakMap` key
    neither runs `__destruct` nor drops the entry, so `count($m)` stays 1 and
    `WeakReference::get()` keeps answering. Binding the hooks does not fix it:
    the missing thing is the *trigger*, a last-reference drop, which is the same
    refcounting lever the handle-numbering family waits on. Do not bind these
    until a local reassignment can release its object; a bound hook that never
    fires is worse than an unbound one, because it looks supplied.
  - `SetIniStore`, `SetSourceReader`, `SetStandardStream`,
    `SetUserStreamOpener`, `SetBufferFlusher` -- interpreter-only by design for
    now; the compiled back-end does not model INI or streams.
  - `SetCallSite`, `SetResultUsed`, `SetErrorReporting` -- unbound and not
    observably missing; the compiler answers these statically or elsewhere.
    Re-probe before assuming either way.
  — *source: 2026-09-19 -- a dead hook found by audit*

- [ ] **When binding a hook, port the interpreter's implementation of it.**
  `SetMagicAvailable` was bound as `hasMethod(class, method)`, which is the
  answer the hook's *name* suggests and half the predicate `runtime/interp`
  implements (`magic.go:62`): exists **and is not already running for that name
  on that object**. Combined with keying the generated program's in-flight set
  on `o.ID` rather than `php.GuardOf(o)` -- `Object.Guard` being exactly what a
  lazy proxy shares with its real instance -- it reproduced GH-18038, magic
  accessors firing twice, and cost six `lazy_objects` tests for a tick. Both
  fixed 2026-09-19. The residue is a standing rule, not an open bug: for any
  predicate both back-ends need, the interpreter is the specification.
  — *source: 2026-09-19 -- a dead hook found by audit*

- [ ] **Subclassing a Reflection class does not reach the engine body in the
  interpreter.** Both dispatch gates -- `reflectionMethod`
  (`runtime/interp/reflection.go:132`) and the constructor routing
  (`interp.go:4764`) -- test `reflectionClasses[lower(name)]`, an **exact** name,
  so `class Sub extends ReflectionAttribute {}` never routes and `new Sub()`
  silently succeeds where the engine refuses with the inherited private
  constructor. Found by a differential case, which was then narrowed to what
  this tick actually fixed rather than left red.

  The fix is not just widening the gate: a subclass may **override** a
  Reflection method, and routing on class identity alone would run the engine's
  body instead of the program's. The compiled arm has the guard --
  `!hasCompiledMethod(o.ClassName, lower)` before dispatch, the same shape the
  array store uses -- and the interpreter needs the equivalent before its gate
  can widen. Worth its own measurement, since it touches every Reflection test.
  — *source: 2026-09-18 -- attributes reach compiled output*

- [ ] **Reflection in compiled output, starting with attributes.** The largest
  remaining cluster by a wide margin: 308 files refuse on
  `ReflectionClass` (256), `ReflectionFunction` (27), `ReflectionProperty` (17)
  and `ReflectionMethod` (8). Screened, the picture is narrower than the count:

  | | files |
  |---|---|
  | total, distinct | 308 |
  | lazy-object tests (`newLazyGhost`/`newLazyProxy`/`SKIP_INITIALIZATION`) | 206 |
  | non-lazy | 102 |
  | non-lazy **and** free of `__destruct` / `object(X)#N` | **68** |

  The reachable 68 are really an **attributes** cluster: `getAttributes()` 82
  uses, `getName()` 48, `getArguments()` 33, `newInstance()` 31. So the first
  tick is `#[Attr]` reflection, not reflection in general.

  **This one does not extract the way the array store and the fiber driver did.**
  `runtime/interp/reflection.go` is 1,602 lines with **56 distinct** `unit.`/`in.`
  coupling points -- against 14 for `fiber.go` and 4 for `arrayobject.go` --
  because the surface is mostly *ask the class table*, and the two back-ends
  have genuinely different class tables: `*Unit` on one side, emitted Go maps on
  the other. The precedent still applies, in reverse: share what is **data**
  (the attribute lists, the shape of a `ReflectionAttribute`), and implement the
  **table** questions per back-end rather than hooking 56 of them.

  Prerequisite, and it is missing entirely: **`internal/gogen` carries no
  attributes**. Zero references. The harvest does carry them --
  `encodeAttributes` in `internal/ir/ir.go` puts them on op_arrays, arguments,
  classes, properties and constants, with the arguments left *unevaluated*
  because an attribute is only instantiated when something reflects on it, which
  is exactly the semantics `newInstance()` needs. So step one is carrying them
  from `gogen.Program` into emitted tables; nothing can be built before that.
  — *source: 2026-09-18 -- fibers reach compiled output*

- [ ] **A suspension that crosses a call still refuses.** `Fiber::suspend()`
  parks the whole stack it started, so a body that calls a suspending function
  is itself a suspension point. `internal/lower/cps.go` splits a block only at a
  direct `fiber.suspend` terminator, so the caller is not a state machine and
  the call is refused by rule 1 rather than emitted -- the honest answer, not
  the finished one. Biggest remaining fiber gap: of 103 files, **44 refuse for
  exactly this**, led by `getIterator` (12), a named `g`/`f` (9) and a nested
  closure (9).

  Three pieces, and the third is the real one:

  1. **Build the call graph in the compile path.** `cmd/php2lower` does it
     (`cg.Add` per body, then `Link`, `PropagateMarks`, `Reach`); `cmd/php2go`
     and `cmd/php2gotest` build none. Sound taint is affordable -- opaque calls
     measured at 6.6%.
  2. **Mark the Reach set** as needing CPS, not just the bodies containing
     `fiber.suspend`. Worth noting the taint does *not* climb to `{main}`:
     `Fiber::start()` is the stack boundary, and a fiber body is reached through
     `ctx.ResumableOf` rather than a static call edge, so nothing points into it
     and the propagation stops there on its own.
  3. **Split the block at a suspending call.** This is where the work is, and it
     is not the same shape as a yield. The CPS pass needs the suspension to be a
     *terminator* with one successor; a call is an instruction in the middle of
     a block. Splitting it is mechanical, but the semantics are not: a yield
     always suspends, whereas a suspending **call** has two outcomes -- the
     callee returned normally, or the callee parked. Resuming the second case
     means re-entering **the callee's** frame, not the caller's continuation.

     So the caller's frame has to hold the callee's frame, and a state has to
     mean "I am waiting on my child; resume it, and when it finally returns,
     continue at bbM with its value". A suspension then unwinds by returning
     `Suspended` up the chain of frames, and a resume walks back down it. That
     is a frame *stack* inside the state machine, which is the thing CPS buys
     over goroutines and also the thing goroutines would have given for free.
     `Bail{"two suspensions share a continuation"}` is the existing guard to
     keep in mind while doing it.
  — *source: 2026-09-18 -- fibers reach compiled output*

- [ ] **A fiber suspended and never resumed is not unwound.** PHP force-closes
  it when the Fiber object is destroyed, running `finally` blocks; php2go has no
  refcounts, so there is nothing to trigger it and the blocks never run.
  `runtime/interp/fiber.go` does this at request end through its `closing` flag;
  compiled output has no equivalent. ~8 of the 36 fiber failures are this --
  `want "0: Start destruct" got ""`, `want "inner finally" got "done"` --
  and they are the same Phase D3 lifetime problem WeakMap is. — *source:
  2026-09-18 -- fibers reach compiled output*

- [ ] **Fiber methods do not check arity.** `$f->getReturn(1)` is
  `ArgumentCountError: Fiber::getReturn() expects exactly 0 arguments, 1 given`
  in the engine and is silently accepted in compiled output. Found by writing a
  fixture that passed a stray argument. The same is presumably true of the other
  engine-class methods this emitter answers. — *source: 2026-09-18 -- fibers
  reach compiled output*

- [ ] **Fibers in compiled output** -- the largest reachable cluster at 103
  files (`terminator ret.suspend` 70, `fiber.current` 17, `new Fiber` 16), of
  which 55 of the 70 survive the handle/`__destruct` screen.

  **Settled: CPS, building on the `php.Fiber` already in
  `runtime/php/resumable.go`.** `internal/lower/cps.go` deliberately left the
  choice open, naming four options -- uniform, dual entry, selective, or keeping
  goroutines. The decision is CPS, for fidelity: a suspension stays explicit
  data rather than a Go stack, which is the side that can be made byte-exact for
  backtraces and for destructor ordering, and both are graded byte-exact.

  **Correcting a claim this entry made before it was measured.** An earlier
  version said uniform CPS "taints the whole program", citing the comment at
  `callgraph.go:302`. That is reasoning from the header, which is the one thing
  this project forbids. `php2lower -callgraph` over the 70 suspending files --
  196 functions merged -- says otherwise:

  | | functions | share |
  |---|---|---|
  | suspend directly | 63 | 32% |
  | reach, directly-named callees (floor) | 91 | 46.4% |
  | reach, + every method of a matching name | 91 | 46.4% |
  | reach, + opaque calls treated soundly | 104 | 53.1% |

  Opaque calls cost **6.6%**, not everything, and knowing the receiver's class
  is worth **0.0%** here. Blast radius if any one function suspended: median 0,
  p90 0, p99 22, max 31; only 5 functions (2.6%) exceed a tenth of the program.
  These are small test files and most functions have no transitive callers at
  all. `Reach(methods, sound)` exists precisely to produce this number and its
  own comment says the fiber decision should rest on it.

  The work, in order:

  1. ~~**Answer the caller question in `cps.go`.**~~ **Done 2026-09-23.**
     `Resumable` takes a `SuspendingCallee` predicate and splits a block at any
     call it accepts, turning the call into a `call.suspending` terminator
     (`ret.call` after the rewrite) whose continuation resumes with the callee's
     return value -- `yield from`'s shape, so no new resume machinery. A split
     also marks `SuspendsToCaller` on the splitting function. Every caller
     passes `nil` until step 2, so nothing splits yet.
  2. ~~**Wire the call graph into the compile path.**~~ **Done 2026-09-23** for
     `cmd/php2gotest`. Lowering records bodies and `finishBodies` builds the
     graph, propagates marks and computes the set before any rewrite -- it has
     to be before, because a suspending call *becomes* a terminator and stops
     being an edge the graph can read.

     Seeded by **`ReachFiber`, not `Reach`**: the latter counts `yield`, which
     is right for the report it was built for and wrong here, since calling a
     generator function suspends nobody. Seeding from `Reach` put every
     generator in the set and cost 14 fixtures.

     `cmd/php2go` (the bundle path) does not lower through this code and still
     needs the same wiring.
  3. ~~**`internal/gogen`**~~ **Done 2026-09-23.** `frame.of.fn` builds the
     callee's frame, `ret.call` hands it up as the new `php.Called` outcome, and
     `php.callStack` follows those frames -- itself a `Resumable`, so both
     drivers wrap their body in one and are otherwise unchanged. A fiber now
     suspends through a call, named or closure.

     ~~**Owed by this change:** a call performed as `Called` pushes no stack
     frame.~~ **Done 2026-09-23.** A generated frame answers `TraceName()` and
     `TraceArgs()` and `php.callStack` pushes one per step; the fiber body's own
     frame is pushed by the stack too, at `[internal function]`, which a
     generator's driver already does for itself.
  4. **`Fiber` as an engine class** through the `engineClassInterfaces`
     mechanism added for `ArrayIterator`, plus `Fiber::suspend()` and
     `Fiber::getCurrent()`, and the instance methods routed to `php.Fiber` --
     which already has `Start`, `Resume`, `Throw`, `GetReturn` and the five
     status predicates, with six tests over transcribed CPS IR in
     `runtime/php/frame_test.go`.
  5. **The behaviour `runtime/interp/fiber.go` has and `php.Fiber` does not**:
     the `fiber.stack_size` INI floor, `fiberSwitchBlocked`, per-fiber
     `error_reporting` inheritance, the `closing` force-close unwind at request
     end, and the exact `FiberError` wording. The interpreter keeps its
     goroutine driver -- the two back-ends genuinely differ here -- so this is
     parity work against it, not an extraction.
  — *source: 2026-09-17 -- ArrayIterator and ArrayObject reach compiled output*

- [ ] **WeakMap is not the tick its count suggests.** 28 files, but 18 of them
  also define `__destruct` or expect an `object(X)#N` handle, and that overlap
  is not incidental: a WeakMap's whole observable behaviour is that the entry
  disappears when the object dies, which needs refcounting. It is Phase D3 work
  wearing a library-surface hat, and should be picked up *after* selective
  refcounting, not before. — *source: 2026-09-17 -- ArrayIterator and
  ArrayObject reach compiled output*

- [ ] `print_r` of a **self-referential array** descends one level too far in
  compiled output: `$a = [1]; $a[] = &$a; print_r($a)` prints the inner `Array
  (` where the engine prints ` *RECURSION*`. The guard is
  `PHP2Go\Internal\array_id($value)` taken by reference in harvested PHP, so
  either the id differs between `$a` and the reference held at `$a[1]`, or the
  by-reference send copies. `print_r_library.php` is the failing fixture, and it
  is the only one of the 242 that does not pass. The object cycle in the same
  file is fine, so it is the array identity specifically. Shown **not** to come
  from the ArrayIterator tick: it still fails with `engineClassNames` emptied.
  — *source: 2026-09-17 -- ArrayIterator and ArrayObject reach compiled output*

- [ ] `serialize` and `json_encode` of an `ArrayObject` / `ArrayIterator` read
  the (empty) property table instead of the store, in **both** back-ends:
  `serialize(new ArrayObject([1,2]))` gives `O:11:"ArrayObject":0:{}` where the
  engine gives four elements -- flags, the storage array, an empty property
  array and the iterator class -- and `json_encode` gives `{}` where the engine
  gives `[1,2]`. The `(array)` cast is right. The rest of the class's own
  surface -- `asort`, `ksort`, `uasort`, `natsort`, `exchangeArray`, `setFlags`,
  `getFlags`, `setIteratorClass` -- is refused honestly by both arms and is the
  same piece of work. — *source: 2026-09-17 -- ArrayIterator and ArrayObject
  reach compiled output*

- [ ] Widen the engine-class mechanism beyond `ArrayIterator` / `ArrayObject`.
  `engineClassInterfaces` in `internal/gogen` plus a payload in `runtime/php`
  is now the shape: declare the class, list the interfaces the **engine**
  declares for it, and route the methods through a `Ctx` hook. The next
  candidates by corpus weight are `SplObjectStorage`, `CallbackFilterIterator`,
  `IteratorIterator` and `WeakMap` (29 files). — *source: 2026-09-17 --
  ArrayIterator and ArrayObject reach compiled output*

- [ ] A **numeric-string offset** handed to a user `offsetGet`/`offsetExists`
  arrives as an int in compiled output where PHP passes the string:
  `isset($test['0'])` must give `string(1) "0"`. `bug63217.phpt`. The
  ArrayAccess hop converts the offset with the array-key rule, which is right
  for the engine's own dimension handler and wrong for a user method. —
  *source: 2026-09-17 -- ArrayIterator and ArrayObject reach compiled output*

- [ ] Spreading a **Traversable** with integer-like string keys must renumber
  them: `[...gen()]` where `gen` yields `"42" => 42` gives `[0 => 42]`, not
  `[42 => 42]`. `array_unpack/string_keys.phpt`. — *source: 2026-09-17 --
  ArrayIterator and ArrayObject reach compiled output*

- [ ] Continue behavioral audits of the 272 native builtin dispositions. The
  surface ledger proves dispatch coverage, not parity. Start with
  `debug_zval_dump` ownership approximations and route `htmlentities` /
  `html_entity_decode` through the generated inverse alias data. — *source:
  2026-09-02 -- debug ownership closes the declared 534-function surface;
  2026-09-02 -- translation tables are generated by the linked engine*

- [ ] Complete the FTP stream-wrapper transport before claiming full
  `get_headers(ftp://...)` behavior; the linked wrapper performs observable
  connection/protocol work before returning no header array. — *source:
  2026-09-02 -- get_headers composes over addressed stream sockets*

- [ ] Continue Reflection coverage, including remaining parameter/type objects,
  default-property ordering and lazy-object APIs. Keep object-visible property
  order distinct from flattened class tables. — *source: 2026-08-24 -- The
  member reflectors; 2026-08-29 -- reflection order is declaration order;
  2026-08-29 -- an initialized proxy is followed*

- [ ] Complete PHP 8.4 lazy-object edge cases: proxy writes through `__set`,
  initializer rollback/clone/destructor ordering, and reset interactions with
  references and readonly properties. — *source: 2026-08-25 -- lazy objects:
  rollback, destructors, the proxy rule, and clone; 2026-08-29 -- lazy objects:
  dynamic properties, and the readonly a reset keeps*

- [ ] **`$s .= expr` in a loop is O(n^2), and it is now reachable.**
  `concat_003.phpt` -- a performance test the engine runs in 2 seconds -- does
  not finish in 120. Measured on the same shape at 20k/40k/80k iterations, run
  time goes 0.35s / 2.0s / 5.1s: quadratic, because every append allocates a new
  Go string. It is the mutable-builder item from the plan. The ROPE lowering
  already builds `"$a$b"` through `strbuf`, and what is missing is the same for
  `ASSIGN_OP concat`, which needs to know the target is only appended to --
  ownership analysis, or a `*StrBuf` representation inside `php.Value` that
  converts on read.
  **It is the corpus's only `errored`**, and the classification is right: the
  program builds and runs and does not terminate. It became visible only when
  the harvested library linked, since `microtime` used to block the file at
  compile time.
  -- *source: 2026-09-14 -- six leaves, a source path, and a quadratic append*

- [ ] **Four `PHP2Go\Internal` leaves are not in the compiled runtime.** The
  harvested library is linked into an emitted program now, and a body that
  reaches one of these is refused rather than allowed to invent
  `Call to undefined function`: `directory_operation`, `register_superglobal`,
  `path_stat`, `append_file`. `clock` and `read_file` moved on 2026-09-14 --
  the second through a `SourceReader` hook, since only the interpreter has a
  loader. The rest want the same treatment the stream functions got.
  -- *source: 2026-09-13 -- the harvested library, compiled in*

- [ ] **Nothing verifies a request-ending fatal in compiled code.**
  `trigger_error($m, E_USER_ERROR)` prints a fatal block and ends the request,
  and `php.Exit` carries that to the emitted program's `runShutdown` +
  `os.Exit`. The oracle cannot grade it: `zend_eval_string` bails out on a
  request-ending fatal rather than returning output, so six of the nine corpus
  files mentioning E_USER_ERROR are `noreference`. The compiled path is
  therefore argued-for, not measured. Fixing it means giving the oracle a
  second mode that runs a *file* through the request lifecycle instead of
  `zend_eval_string`, which is also what would make the shutdown-after-`exit`
  artifact go away.
  -- *source: 2026-09-12 -- debug_backtrace, trigger_error, and an exit both back-ends can raise*

- [ ] **A compiled program shows a `#[\SensitiveParameter]` argument.**
  `Ctx.SetSensitiveHider` is the hook that replaces one with a
  `SensitiveParameterValue`; the interpreter installs it and the emitted
  program does not, so a trace there shows the raw value. Building the object
  needs the class table the generated program already has, so this is wiring
  rather than design.
  -- *source: 2026-09-12 -- debug_backtrace, trigger_error, and an exit both back-ends can raise*

- [ ] **The oracle's own output capture is an observable `ob_*` level.**
  `php2go_eval` uses `php_output_start_default()`, so the engine arm answers
  `ob_get_level()` with 1 where the CLI answers 0, and a script that ends more
  buffers than it opened destroys the capture. The fix is to capture at
  `sapi_module.ub_write` instead -- in-process, no file descriptor, and not a
  buffer the script can see. Nothing in the 4,986-file corpus reads the level
  today, so this costs no test; it costs differential *cases*, which have to be
  written around it. Doing it changes the oracle for every file, so it wants a
  tick of its own with a before-and-after count.
  -- *source: 2026-09-12 -- output buffering, and an oracle that buffers its own output*

- [ ] **Compiled argument verification does not reject a closed resource.**
  `fclose($f)` on an already-closed handle is
  `fclose(): supplied resource is not a valid stream resource` -- a TypeError
  from the *argument* check -- and compiled code answers false and carries on.
  The declared parameter type is `resource`, which `CoerceInternalArgument`
  passes through untouched, so the validity check has to be added beside it.
  -- *source: 2026-09-12 -- a compiled program can open a file*

- [x] ~~**A compiled program still cannot open a stream.**~~ Done 2026-09-12: `fopen` resolves a
  wrapper through `unit.streamWrapper(scheme)`, and that registry lives on the
  interpreter's Unit because `stream_wrapper_register` and
  `stream_filter_register` write to it -- and both registries are on the Ctx
  now, with `UserStreamOpener` as the hook for the one part that needs a class
  table. `stream_get_filters` still reads the interpreter's side and has not
  moved.
  -- *source: 2026-09-12 -- the file functions followed the type*

  -- *source: 2026-09-12 -- the library surface has a home now: runtime/stdlib;
  the stream type moved to runtime/stdlib*

- [ ] **The `PHP2Go\Internal\*` helper layer is shaped like the C it came
  from, and it is what blocks the streams library.** Four of them multiplex on
  a string selector -- `stream_operation($s, 'flush'|'stat'|'isatty'|...)` over
  7 call sites and 6 selectors, `stream_context('create'|'merge_options'|...)`
  over 13 and 5, `stream_socket` over 8 and 7, `stream_set_option` over 5 and 5.
  That is `php_stream_set_option(stream, option, value, ptrparam)` transliterated:
  in C the selector is a constant passed to one entry point, and in PHP it
  should be distinct functions with real signatures. Each is also declared
  twice -- a stub in `builtins.go` that errors with "escaped interpreter
  dispatch", and the real arm inside `streams.go`'s switch -- so the
  implementation has no signature at all.

  The consequence is concrete: **51 distinct `\PHP2Go\Internal\*` helpers are
  unreachable from compiled code**, because they live in the interpreter's
  dispatcher rather than in `runtime/stdlib`. Until that changes, no userland
  library that uses one can ever be emitted -- which is most of streams and a
  good part of standard. Splitting the multiplexers into named helpers in
  `runtime/stdlib` is therefore on the critical path to userland linking, not a
  tidy-up. **Measured** by `internal/userland/shape.tsv` (2026-09-12 baseline:
  51 helpers, 15 multiplexers, 50 unreachable); the target for this item is
  `reachable=no` going to zero, and it can be worked helper by helper.
  **The four stream multiplexers are done** (2026-09-12): 11 multiplexers left,
  and 20 of 29 `stream_*` helpers reachable. What remains unreachable there is
  the four that build a stream -- `stream_socket_server`, `_client`, `_accept`,
  `_pair` -- plus `stream_select`, `stream_metadata` and
  `stream_wrapper_registry`; all of them need the `*stream` type itself to move,
  which is the next and much larger step. The remaining 11 multiplexers are
  `network_database` (8 selectors), `directory_operation` (6),
  `process_identity`, `clock`, `path_stat`, `path_mutation`, `path_topology`,
  `temporary_file`, `output_rewriter`, `assert_options`, `array_random`.
  -- *source: user, 2026-09-12; measured the same day*

- [x] **Done 2026-09-12.** `CallableReason`/`CheckCallable` on the Ctx, the
  emitter's `callableReason`, and `is_callable` in `runtime/stdlib`. What is
  *not* done is the wider gap it sits in: **compiled code still does not verify
  builtin arguments against the engine's arginfo at all.**
  `str_repeat("a", [])` answers `""` where the engine raises
  `str_repeat(): Argument #2 ($times) must be of type int, array given`, and 18
  corpus failures are exactly that. The interpreter has `verifyBuiltinArgs`
  (`runtime/interp/types.go`) over `engineSignatures`; the compiled side would
  need the harvested signatures embedded in the artifact, since the arguments
  are dynamic. `php.CoerceInternalArgument` already exists for the coercion
  half. -- *source: 2026-09-12 -- a builtin checks its callback before calling
  it*

- [ ] **A diagnostic raised inside a userland body is attributed to that body,
  and it has now blocked two conversions.** The engine attributes an internal
  function's diagnostic to its *caller*; a PHP body raising one names its own
  file and line. `array_flip` hit it with an explicit warning and was rescued by
  `\PHP2Go\Internal\warning`, which re-attributes through
  `libraryCallerLocation`. `usort` hit it with a diagnostic **no PHP code
  raises** -- a comparator declared `&$x` earns
  `must be passed by reference, value given` from the engine's own argument
  binding -- and had to be reverted. So the fix cannot be another helper: the
  location swap has to happen around *any* call made from inside a userland
  builtin, which is where `Ctx.Call` is reached from. Until then, a function is
  only convertible if it neither warns nor invokes a callback.
  -- *source: 2026-09-12 -- the is_* family is one operation*

- [ ] **Reimplement the byte-string library in PHP under `internal/userland`.**
  `strtoupper`, `strtolower`, `trim`/`ltrim`/`rtrim`, `strcmp`, `strpos`,
  `strrpos`, `str_contains`/`str_starts_with`/`str_ends_with`, `substr`,
  `str_replace`, `strrev`, `str_pad`, `str_repeat` are pure byte manipulation
  and express fine in PHP. What cannot reduce is the floor they stand on:
  `strlen` (already an intrinsic), string offset read/write, `ord` and `chr`.
  So this shrinks the native core rather than removing it. **Started
  2026-09-12**: `str_shuffle`, `ucfirst`, `lcfirst`, `ucwords`, `nl2br`,
  `quotemeta`, `strrev`, `str_pad`, `array_sum`, `array_product` are PHP now,
  at a measured cost of one corpus file (`strrev`), recoverable when linking
  lands. -- *source: user, 2026-09-12*

- [ ] Move the standard functions still registered in `runtime/php` down to
  `runtime/stdlib`: `array_keys`, `array_reduce`, `array_values`, `count`,
  `implode`, `in_array`, `constant`, `define`, `defined`, the `error_*` group,
  `flush`, `getcwd`, the `ob_*` group, `printf`/`sprintf`/`vprintf`/`vsprintf`,
  `set_error_handler` and friends, `var_dump`. They predate the rule and are
  entangled with hooks -- `VarDump`'s renderer and the error-handler stack are
  mechanisms and stay in `runtime/php`; only the registrations move.
  -- *source: 2026-09-12 -- the library surface has a home now: runtime/stdlib*

- [ ] **The compiler's library surface is the largest remaining lever, and it is
  much thinner than the interpreter's.** `runtime/php`'s `RegisterBuiltin`
  registry is the emitter's link-time manifest, and a call to anything outside it
  is refused as `unsupported: library function <name>`. Measured over the y6i
  corpus run (2026-09-11): **661 of 1495 unsupported files** are blocked on a
  missing library function, and **239 more** on `new of undeclared class
  ReflectionClass`. The head of the histogram, by files blocked: `fopen` 51,
  `str_repeat` 38, `print_r` 35, `debug_print_backtrace` 33, `serialize` 31,
  `ob_start` 29, `is_callable` 19, `array_map` 19, `range` 18, `ini_set` 14,
  `get_object_vars` 14, `get_class_vars` 13, `debug_backtrace` 13, `assert` 13,
  `unserialize` 12, `trigger_error` 12, `call_user_func` 12, `chr` 11,
  `get_class_methods` 10. Several of these are a few lines each
  (`str_repeat`, `chr`, `range`, `reset`, `substr`) and several are their own
  projects (`fopen`, `serialize`, `ob_start`). This is the plan's Phase B
  arriving one function at a time; it raises the ceiling rather than the pass
  rate directly, so each addition still has to be measured. Note the split: some
  of these the *interpreter* already implements and the compiler does not, so the
  work is extraction into `runtime/php` rather than new behaviour. **Started
  2026-09-11**: the byte-string batch (18 functions) moved into
  `runtime/php/stringfns.go` and took passing 1790 -> 1821. Still to go, roughly
  by value: the array-pointer family (`reset`/`key`/`current`/`next`/`end`, ~20
  files, needs `pointerBuiltin` and the by-ref argument path `byrefargs.go`
  already declares); the dumpers (`print_r`, `var_export`, `serialize`,
  `json_encode`) which need the class table and so are not a straight move;
  and `fopen`/`ob_start`, each its own project. **`print_r` done 2026-09-12** on
  `var_dump`'s existing hook (1874 -> 1894); `var_export`, `serialize` and
  `json_encode` should follow the same route, since the boundary question is
  the same one.

- [x] **Done 2026-09-11.** `StackFrame.Internal` marks an engine function's
  frame, `php.CallerStack` drops trailing internal frames, and the four frame
  readers moved to `runtime/php`. `debug_backtrace` still cannot move: a hidden
  argument becomes a SensitiveParameterValue and building one needs the object
  store. -- *source: 2026-09-11 -- a frame now says whether it is an engine
  function's* **Second batch done 2026-09-11**: all of `mathlib.go`, the array
  pointer family, `chr`/`ord`/`range`/`get_debug_type`, `abs`/`intdiv` --
  1821 -> 1858.
  -- *source: 2026-09-11 -- a folded magic constant is a path arriving by a back
  door (histogram taken while screening that tick's corpus run)*

- [ ] Implement or honestly classify remaining extension/capability gaps outside
  the completed `main`/`ext/standard` surface, including `preg_*` and standard
  streams (`STDIN`, `STDOUT`, `STDERR`) where the linked test corpus requires
  them. — *source: 2026-08-25 -- the long tail of missing library functions;
  2026-08-23 -- Grading against Zend/tests*

## Harness and bounded approximations

- [x] **Recorded computed evals: explicit profile mode selected** (2026-09-30, patch
  held at the session scratchpad as `par/held-recorded-eval.patch`,
  stacked on the include fixes). `php2eval -record-evals` records every
  string `zend_compile_string` sees during the oracle run; the harness
  compiles each at its site name, and the program runs the body whose
  source equals the computed string byte for byte (any other string is
  refused). It turns 16 of the 18 computed-eval Zend files into passes,
  but the compiled program then depends on the reference engine having
  run the same test. The user chose an explicit profile-guided mode; batch
  151 implemented `php2go profile` and `php2gotest -eval-profile`, with
  ordinary compilation continuing to refuse unseen strings. Batch 153 adds
  a separate tagged hybrid bundle that compiles unseen eval strings at runtime.

- [ ] **php2eval's output capture can be ended by the script.** It captures
  with an output buffer the script can see, so a test whose first statement
  is `ob_end_flush()` or `ob_end_clean()` with no buffer of its own ends the
  capture: the engine reference loses the notice for that call and gains
  stray output after a fatal (bare `ob_end_flush()` even yields `Notice:
  Unknown: Failed to discard buffer`). bug64960's compiled output matches
  the reference through its fatal and differs only in those artifacts.
  Capture below the buffer stack a script can see; until then any test
  calling `ob_end_*` at the bottom level has an unreliable reference.
  — *source: 2026-09-30 -- parallel fix round 1 (bug64960)*

- [ ] Add a per-test timeout to `php2phpt`. `php2gotest` now grades child hangs
  as errored, but the interpreter corpus runner can still be stopped by one
  looping test. — *source: 2026-09-07 -- the harness can report a hang; TODO
  section “Harness fidelity”*

- [ ] Complete `.phpt` fidelity for remaining `--INI--` settings and `%r...%r`
  EXPECTF regex regions, and normalize `cmd/php2eval` source filenames between
  engine and interpreter. Unsupported sections must skip rather than silently
  run under different conditions. — *source: 2026-08-25 -- running the skip
  condition instead of reading it; 2026-08-28 -- the harness was mangling its
  own expectations*

- [ ] Preserve non-ASCII PHP string-literal bytes through IR marshaling. A byte
  above 0x7f currently cannot round-trip in some literal paths. — *source:
  2026-08-23 -- Four small things the corpus kept asking for; TODO section “IR
  fidelity”*

- [ ] Replace silent depth bounds with engine-compatible diagnostics or honest
  gaps: recursive comparison at `maxCompareDepth` and serialization beyond
  `maxSerializeNesting` must not return a normal value. — *source: 2026-08-23
  -- Anonymous classes, recursion depth, and cleaning up after a crash; TODO
  section “Diagnostics php2go bounds silently”*

- [ ] Keep the emitter census locked to the real emitter's traversal and
  context. It has previously skipped closures/hooks/static methods and invented
  `$this` gaps in compilable files. Add a parity test rather than another
  manually maintained walk. — *source: 2026-09-09 -- the census was measuring
  a compiler that does not exist; 2026-09-09 -- a census head that named the
  wrong thing*
