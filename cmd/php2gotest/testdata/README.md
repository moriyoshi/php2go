# Differential cases for the compiled path

Each file is graded by emitting Go, building it, running it, and diffing stdout
against the engine's own output for the same source:

```sh
go build -o /tmp/php2eval ./cmd/php2eval
go run ./cmd/php2gotest -php2eval /tmp/php2eval cmd/php2gotest/testdata
```

Every case must be `passed`. A case that stops passing is a regression in the
emitter, the lowering, or `runtime/php` — the harness does not say which, which
is why the cases are small.

## Profile-guided computed evals

Computed `eval($code)` needs the strings observed in a reference execution.
Record them explicitly, then pass the profile to the compiler harness:

```sh
go run ./cmd/php2go profile -root . -o /tmp/evals.profile path/to/entry.php
go run ./cmd/php2gotest -root . -eval-profile /tmp/evals.profile -emit /tmp/program path/to/entry.php
```

`cmd/php2gotest/profiletestdata` contains the differential cases for this
mode. Without `-eval-profile`, computed evals remain unsupported. A generated
program refuses any eval string absent from its profile at runtime.

An emitted module can instead be built with `go build -tags php2go_hybrid`.
That tagged binary keeps executing generated Go. At an eval string absent from
the profile, Zend compiles the string and the IR interpreter runs its body with
the caller's lowered variables copied into and out of an eval frame. The
bridge also adds names eval creates to the generated scope, so later dynamic
variable access sees them. Dynamic `include` and `include_once` of regular host
files use the same scope bridge and share the generated once table. These PHP
constructs use a generated intrinsic and a Go file-provider hook; PHP cannot
call them as ordinary functions. Declarations, calls, nested includes/evals,
virtual stream paths, and resumable caller frames still report `unsupported`.
For a statically resolved include, the generated program selects its lowered
Go body while the resolved file still matches the build's content hash. A
changed file takes the tagged interpreted path.
The ordinary build keeps the profile boundary. The tagged build requires the
CGO environment from `.agents/docs/TESTING.md` and access to included files.

```sh
# Use the module directory printed by php2gotest -emit.
(cd /tmp/program/<emitted-module> && go build -tags php2go_hybrid -o app .)
```

**Expected output is never written down here.** It comes from the engine at
grading time, so these cannot drift into recording what php2go does. That is the
same rule the `.phpt` corpus follows, and the reason a golden regenerated with
`-update` is not evidence about behaviour.

- `calls_and_loop.php` — a whole program: top-level body, a function, a `while`
  loop, and calls between them.
- `undefined_variable.php` — reading a never-assigned name warns, and the
  position it reports is graded byte-exact.
- `coalesce.php` — `??` does **not** warn (suppression is intrinsic to
  `ZEND_COALESCE`), and `br.null` treats an undefined name as null-ish.
- `assign_op.php` — `.=` and `+=` read the place before writing it, so they warn
  on an undefined name.
- `compile_time_warning.php` — some diagnostics are emitted when PHP *compiles*,
  not when it runs: the function here is never called and still warns four
  times. An AOT compiler runs that compile at build time, and the harvest
  already captures the text (`Unit.compile_diagnostic`), so the generated
  program replays it in its prologue.
- `classes.php` — a class with declared properties and defaults, a constructor,
  methods, two independent instances (so per-object state is exercised), and a
  read of an undefined property, whose warning names the class and position.
- `inheritance.php` — an abstract base with an abstract method, a subclass that
  overrides a property default and implements the method, and an inherited
  method called through the chain. The harvest flattens the property table but
  **not** the method table, so the method chain is resolved at emit time.
- `var_dump.php` — the first library function through the shared builtin
  surface, plus an object whose public, protected and private properties each
  carry the annotation `var_dump` prints for them.
- `typed_properties.php` — the three states a declared property can be in, which
  `var_dump` distinguishes: a **typed** property with no initializer is
  *uninitialized*, listed but excluded from the header count; an **untyped** one
  is implicitly null and counted; one with a default holds it. The type is what
  decides, not the emptiness.
- `array_literals.php` — constant array literals: packed, string-keyed, empty,
  nested, and mixed. The IR renders them the way PHP spells them and
  `lower.ParseArrayLiteral` reads them back, so the emitter and the renderer
  cannot disagree about what the text says.
- `debug_info.php` — `__debugInfo` through `ctx.SetObjectProperties`. Its keys
  get *array* rules because whatever it returns is an ordinary array: `[7]=>`
  unquoted and no visibility annotation, unlike a real private property.
- `exceptions.php` — throw, catch, and the clause-matching rules. It pins the
  two facts that are easy to get wrong from memory and never notice:
  **`TypeError` takes the `Error` branch, not `Exception`** (the two roots are
  disjoint), and **`ParseError` takes the `CompileError` branch, not `Error`**.
  Also a user class extending `RuntimeException`, so the type test spans the
  program's hierarchy and the engine's.
- `exception_dump.php` — the *rendered* shape of an exception, which is what a
  property-name assertion misses: seven properties in engine order, with
  `["message":protected]` and `["string":"Exception":private]`. The private ones
  belong to the **root**, so `MyEx extends RuntimeException` still shows
  `"Exception":private` rather than its own class. The objects are held in
  variables on purpose — dumping temporaries would fold in handle reuse, which
  is a separate and still-open gap.
- `callbacks.php` — a builtin invoking a PHP callable through
  `ctx.SetCallValue`, which the generated program supplies because it has the
  function table `runtime/php` does not.
- `error_handler.php` — the handler **stack**, and the rule a second
  implementation would get wrong: `set_error_handler(null)` puts *no handler* on
  the stack rather than clearing it, so the diagnostic gets default handling and
  a later `restore_error_handler()` reaches the handler **two below**. Clearing
  could never do that.
- `conditional_class.php` — a class declared inside a branch is bound only when
  its `declare_class` runs: `if (false) { class No {} }` leaves `new No()`
  throwing `Class "No" not found`, which a `catch (Error $e)` sees. The class is
  in the harvested unit either way, so the opcode is the declaration *point*
  rather than the definition.
- `namespaced_class.php` — a **namespaced** name in a diagnostic. `Foo\Bar`
  contains a backslash, which is a Go escape, so the message has to distinguish
  PHP's quoting from Go's: the inner quotes are part of the text and only the
  outer literal is escaped. Getting it wrong produced `unknown escape` at build
  time, and over-escaping produced `Foo\\Bar` in front of the user.
- `namespace_fallback.php` — an unqualified call inside a namespace falls back
  to the global function, and the **namespaced** name is tried first: a
  `namespace Foo; function var_dump()` wins over the global one. The IR records
  both as `call.fn "Foo\var_dump" ... fallback="var_dump"`.
- `arrays.php` — arrays as a data structure: append, integer and string keys,
  the key coercions (`true`→1 so it overwrites `$a[1]`, `null`→`""`, a float
  truncated *and* deprecated, a numeric string→int), the missing-key warning,
  autovivification from an unset variable, and **assignment copying** —
  `$c = $b; $c[0] = 99` must leave `$b[0]` alone.
- `array_keys.php` — the whole key-coercion table through `php.ArrayKey`, every
  row read back from the engine. The rows that are easy to get wrong: `null` is
  `""` and **not** `0`; a float deprecates on *precision loss* rather than on
  being a float, so `5.0` is silent and `5.9` is not; `NAN` keys as 0 and
  deprecates; and the string boundary is *canonical decimal that fits an int64*
  rather than "looks numeric" — `9223372036854775807` is an int key,
  `9223372036854775808`, `05`, `1e3` and `" 5"` are not.
- `php_errors.php` — four conditions the engine reports as **catchable `Error`s**
  and this emitter used to report as Go failures, which PHP code cannot handle:
  a scalar used as an array, an undefined method, a method call on a non-object,
  and an undefined function. Found by reading each hand-written message back
  from the engine; three were wrong in *kind* and two in capitalisation.
- `hostile_keys.php` — array keys whose *characters* break naive quoting: an
  embedded `"`, a `%`, and a namespace `\`. **PHP escapes nothing** — it wraps
  the bytes in quotes and stops — so `%q` is not PHP quoting, and a name reaching
  a format string as anything but an argument is a second bug waiting. It also
  pins that `$a[k] ?? ""` is silent where `echo $a[k]` warns.
- `multibyte.php` — `var_dump` reports a string's length in **bytes, not
  runes**: `café` is `string(5)`. Any implementation reaching for a rune count is
  wrong on every multibyte string and right on every test written in ASCII, which
  is why the case exists even though nothing here counts runes.
- `visibility_read.php` / `visibility_write.php` — private and protected
  enforcement, which the emitter *declared* to `runtime/php` and then never
  checked, so `$obj->priv` read the value from anywhere. The rule is not the
  obvious one: the message names the **instance's** class (`D::$prot` for a
  property declared in `B`), and an inherited **private** is not an error but
  *invisible* — `Undefined property: D::$priv` on a `D` instance, yet
  `Cannot access private property B::$priv` on a `B` one. Writes are covered
  separately because enforcing reads alone leaves a half-applied rule.
- `uncaught.php` / `uncaught_toplevel.php` — the uncaught-throwable block, which
  PHP writes to **stdout** interleaved with the program's output; this emitter
  wrote a Go-shaped `php2go: ...` line to stderr and 0 bytes of stdout. Measured
  before building: of 38 corpus failures wanting a `Fatal error` line only 4 need
  the bare `#0 {main}`, so this needs a real call stack rather than a formatter.
  The frames name the **call site** (`#0 t.php(2): inner(42)`), not where the
  throw happened, and methods render as `C->go(...)` with strings truncated to
  15 characters, arrays as `Array` and objects as `Object(C)`.
- `declared_types.php` — the whole weak-mode parameter-coercion table, 55 rows.
  A declared type **transforms** as well as verifies, so dropping it corrupts a
  value rather than skipping a diagnostic: `f(float $x)` given `5` must print
  `float(5)`. **Parameter coercion is not arithmetic coercion** — `"5abc"` is a
  `TypeError` here where `"5abc" + 0` is `5` with a warning — and **`null` is a
  TypeError for every non-nullable scalar**, `string` and `bool` included.
  Float→int precision loss deprecates, and a float-*string* deprecates with a
  different message that quotes the original. Mutation-checked against the two
  plausible wrong implementations rather than nonsense ones.
- `static_props.php` / `static_binding.php` — static properties and methods.
  A static property belongs to the **class**, and an inherited one is the *same
  slot*: writing `D::$count` is visible through `C::$count`. The engine says so
  in the harvest — an inherited static's default arrives as an indirection
  (`KIND_OTHER`) rather than a value, so the default is read only from the class
  that declared it. `static::` is late static binding and resolves to the class
  the **call** named, not the one that declared the body: `C::late()` reaches
  `B::late`, and `static::who()` there must still find `C::who`. `self::` is the
  declaring class, so `C::early()` gives `B`. Static frames render as
  `T::boom(7)` with a double colon.
- `readonly.php` — readonly properties, whose two failure modes have **different
  messages because they are different rules**: readonly implies `protected(set)`,
  so *initialising* from outside is a visibility failure
  (`Cannot modify protected(set) readonly property Q::$y from global scope`)
  while any *later* write is a readonly failure (`Cannot modify readonly
  property P::$x`) — and that one fires **inside the declaring class too**,
  because readonly means written once, not written only from inside. Also pins
  that reading a declared *typed* property before it is written is an Error
  (`Typed property Q::$t must not be accessed before initialization`), not the
  `Undefined property` warning an absent one gets: the slot exists holding undef,
  a third state.
- `magic_methods.php` / `magic_recursion.php` — `__get`, `__set`, `__call` and
  `__callStatic`. The rule is uniform and worth stating once: **a magic accessor
  answers for exactly what ordinary access could not reach** — inaccessible *or*
  absent — so a private property read from *outside* runs `__get` and the same
  read from inside does not, and `$m->newprop = 1` runs `__set` rather than
  creating a dynamic property. `__callStatic` is a **static** method and so is
  absent from the instance method table; looking only there found every hook but
  that one. The recursion case pins PHP's guard: a `__get` that reads the very
  property it was called for does not recurse — the inner read falls through to
  the ordinary warning, which is the difference between a diagnostic and a hang.
- `property_hooks.php` — PHP 8.4 hooks. A hooked property is not a field, and
  the load-bearing rule is that **inside a hook the same property is the raw
  backing store**, not a re-entry: `get { return $this->backed * 2; }` reads the
  stored value rather than calling itself, so without the guard the ordinary
  hook body is an infinite loop. A **virtual** property has no slot at all and
  is absent from `var_dump`, so it must not even be declared, while a backed one
  shows the raw stored value rather than what the getter returns. The arrow form
  (`get => expr`) is covered too, since it lowers to the same op_array.
- `constants.php` — global constants. **Redefining warns and keeps the
  original**, answering `false` — "last one wins" is the natural guess and is
  wrong. A constant the program declares is registered when its `declare_const`
  *runs*, so `if (false) { define("NEVER", 1); }` leaves `NEVER` undefined — the
  same rule conditional classes follow, and the reason the engine's own
  constants are seeded separately at start-up.
- `class_introspection.php` — `get_class`, `get_parent_class`, `class_exists`,
  `method_exists`, `property_exists`, `class_alias`. They live in the **generated
  program**, not `runtime/php`, because they need the class table and that
  package is deliberately free of anything that knows what a class is. Grouped
  because they are one implementation over one table — the test a cluster has to
  pass before it counts as a lever. `get_class` is also compiled to its own
  opcode, so a builtin table alone never answers for it, the same shape as
  `defined()`. `get_parent_class` answers **false**, not null, for a class with
  no parent, and `class_alias` is a *runtime* declaration so it cannot be folded
  into the table.
- `class_constants.php` — class constants, whose two failures differ:
  `Undefined constant B::NOPE` for an unknown name, `Cannot access protected
  constant B::P` for a visible-but-not-reachable one. The engine **flattens
  inherited constants down the class chain but not across interfaces**, so `D`
  lists everything `B` declared while `E implements I` lists none of `I`'s — the
  lookup has to walk sideways as well as up. Also covers `Access to undeclared
  static property`, whose message had the same emitted-format bug and had never
  been exercised.
- `references.php` — `&` bindings and by-reference parameters. The rule
  IR-DESIGN.md warns is **silently wrong backwards**: `$a = $b` writes *through*
  a reference sitting in `$a`, while `$a = &$b` *replaces* the slot. Every write
  path has to obey it, not just assignment — `$b++` and `$a += 10` reach the
  shared box too, and each was a separate bug. Written as an attack rather than
  a demonstration: it checks that a **by-value** parameter does not alias even
  when the argument is a reference, and that a fresh local is boxed at the call
  site. A by-ref parameter arrives as `recv` into a temp followed by an ordinary
  `local.set`, which copies — so that one store is really a *bind*, and nothing
  in the instruction says so.
- `closures.php` — closures, arrow functions and captures. Written as an attack
  on the one distinction that is easy to lose: `use ($n)` captures the **value at
  capture time** and `use (&$n)` shares the enclosing box, so the case checks
  both that a later write outside is *invisible* to the first and *visible* to
  the second. The engine carries that in the opcode's `extended_value`, which
  the lowering was dropping — treating both as a copy makes the by-reference
  form silently do nothing. Also covers a closure escaping its defining function
  with its captures intact, and nesting.
- `closures_in_members.php` — a closure declared inside a **method** and inside a
  **static method**, plus a call through a function *name*. Closures hang off
  their enclosing op_array, so each body kind has to be walked separately; two
  of them were missed and produced `undefined: cl__closure_a__foo___N_` at Go
  build time, which lands in `errored`, not `unsupported`.
- `builders.php` — array literals and string interpolation, which the compiler
  emits as **builders**: `INIT_ARRAY`/`ADD_ARRAY_ELEMENT` and
  `ROPE_INIT`/`ROPE_ADD`/`ROPE_END`. The IR threads the accumulator through each
  result, but it is *one* array being filled — the result slot is also an input —
  so the emitter mutates and aliases rather than copying per element. Pins that
  `[5 => "five", "next"]` continues from the next free integer rather than
  restarting, and covers nesting and `{$v}` interpolation.
- `generators.php` — generators, via the CPS rewrite `internal/lower` already
  performs. Two facts the shape depends on: a generator function **splits into an
  eager prologue and a resumable body**, so a parameter TypeError fires before
  the generator object exists; and a resumable body **does not start at its first
  block** — the pass appends a dispatch and points `.entry` at it, so falling
  into `bb0` re-runs the prologue forever and looks exactly like an empty
  generator. Covers keyed yields, state surviving each suspension, and the
  driver methods directly.
- `generator_members.php` — a generator that is a **method** and one that is a
  **closure**. `$this` and the captures have to live in the *frame*, not in the
  call, because the body runs long after the call that bound them.
- `stdclass.php` — the engine's empty class: no methods, no declared properties,
  every property dynamic.
- `isset_unset.php` — `isset`, `empty` and `unset`. `isset` is **set and not
  null**, so it cannot be spelled `!IsUndef`: a null array element or property
  is *not* set. `empty` is not its negation either — it asks whether the value is
  falsy, so `empty($zero)` is true for a variable that is very much set. Neither
  warns: a missing key is the question being asked, so the read bypasses the
  diagnostics an ordinary one raises. Unsetting a local leaves it **undefined**
  rather than null, an array element **goes away**, and a declared property keeps
  its slot holding undef.
- `dynamic_class.php` — a class named at **run time**: `new $n()`, `$n::K`,
  `$n::$s`, `$n::m()`, `$obj::class`. The IR types the resolved reference
  `*class`, and carrying it as the class *name* makes every static-member path
  work through the same tables a literal name uses — one change rather than five.
  `A::class` is folded by the compiler, so only the dynamic form reaches the
  emitter.
- `enums.php` — enums, pure and backed. **A case is a singleton**:
  `Suit::Hearts === Suit::Hearts` is true and survives a round trip through
  `cases()`, so the object is built once and handed out. `name` is a real
  property and a *backed* case also has `value`, which a pure one does not have
  at all. `from` throws a `ValueError` where `tryFrom` answers null, and
  `var_dump` renders `enum(Suit::Hearts)` rather than the object form. Enum
  methods work because the case is an ordinary object of that class.
- `anon_and_temporaries.php` — anonymous classes, and `parent::m()` on an
  *instance* method. The second was a latent wrong answer since static members
  landed: `parent::tag()` is **static syntax for a non-virtual instance call**,
  keeping `$this` and bypassing dispatch, so looking only in the static-method
  table found nothing and threw. Also pins that two anonymous classes in one file
  do not collide, that one can extend a named class, and that a temporary reaching
  a parameter whose by-ref-ness is unsettled goes by value.
- `const_expressions.php` — initializers the compiler could **not fold**, kept by
  the engine as an AST and harvested as a tree. The rule that shapes the
  implementation: they are **evaluated on first use, not at start-up**. A class
  constant can name a global the program defines with `const G = 5;`, which has
  not run when the tables are built — evaluating eagerly gave `G * 2` the value
  0, so the entry holds a thunk and memoises. A property default is naturally
  lazy because it runs at `new`. The binary operator arrives as the engine's
  opcode *number* and is resolved through the harvested table, never transcribed.
- `offsets.php` — reading `$x[$k]` across every subject kind, which is the
  `php.ReadDim` **intrinsic**. Three rows the generated program's private copy
  got wrong: a **string is a container** (`"abc"[-1]` is `"c"`, past the end
  warns and yields `""`), a **non-container warns** rather than silently
  yielding null (and `true`/`false` are spelled out, not `bool`), and a string
  offset that is a float **warns** while a non-numeric string **throws**. Also
  pins that the silent mode `??` uses answers *not set* for an out-of-range
  string offset rather than `""` — the two modes ask different questions, so
  they give different values, not the same value with the warning suppressed.
- `dim_writes.php` — writing `$x[$k] = $v`, the `php.WriteDim` **intrinsic** and
  the mirror of `ReadDim`. A write is not a read with an assignment on the end:
  an unset slot **autovivifies** to an array while a scalar throws, and **a
  string is written by offset rather than converted** — `"abc"` with `[1]="X"`
  is `"aXc"`, writing past the end **pads with spaces**, and assigning more than
  one byte keeps the first and warns. `$s[] = 1` has its own error (`[] operator
  not supported for strings`), distinct from the scalar one. The old open-coded
  version threw on every string case.
- `arith_contracts.php` — the **effect contracts** the intrinsic catalogue
  records for arithmetic, bitwise and comparison. The emitter was producing the
  *values* and none of the *diagnostics*: `2.5 % ""` must report the left
  operand's precision-loss deprecation **and then** the TypeError, while
  `"" % "2abc"` reports only the TypeError because the walk stops at the first
  operand that cannot convert. Also pins `+` as the only operator taking arrays
  (a union), bitwise over two strings as bytewise, and PHP 8 loose equality
  where `0 == "a"` is false. `/`, `%`, `<<`, `>>` and `~` did not compile at all
  until the catalogue forced the surface to be enumerated.
- `param_defaults.php` — **default parameter values**, across plain, typed,
  array, method, static-method and closure parameters. `ZEND_RECV` and
  `ZEND_RECV_INIT` lowered identically, so every default was discarded:
  `function f(int $x = 5)` called as `f()` reached the declared-type coercion
  with an empty slot and threw a TypeError where the engine prints `int(5)` —
  a wrong answer in ordinary code, not a gap. The engine also settles that a
  default is applied **uncoerced**, since `int $x = null` prints NULL, so the
  default cannot travel the path a passed argument does. A default the harvest
  renders as a deferred constant expression (`self::N`) is refused by name.
- `compound_assign.php` — **compound assignment** across every operator and
  every kind of target. This lowered to one `assign_op $x, $v, op=concat`
  instruction carrying the operator as an *attribute*, which the emitter
  resolved through an operator table of its own — a second copy of the
  catalogue, drifted the same way the opcode table had. It spelled the bitwise
  operators `bit_and` where the engine emits `bw_and` and had no entry for
  `div`, `mod`, `sl` or `sr`, so `&=`, `|=`, `^=`, `/=`, `%=`, `<<=` and `>>=`
  did not compile at all. It is now a read, an intrinsic call and a write, which
  also gives compound assignment the intrinsic's **effect contract** for the
  first time: `$a /= 0` raises DivisionByZeroError and `$a -= [1]` reports
  unsupported operand types. Covers local, array-element, object-property and
  static-property targets, the assigned value as an expression, and the
  undefined-variable warning that the read must still produce.
- `tostring.php` — **string conversion**, which is not pure: an object
  re-enters `__toString`, an array warns and still converts to the literal
  `"Array"`, and an object without `__toString` is an `Error` naming its class.
  Compiled code did not convert objects **at all** — `php.Concat` and the rope
  builder both used the value's plain string form, which has no ctx and so
  cannot call a method, so `echo "x$obj"` printed `xy`. The catalogue had said
  Concat `ReEnters` the whole time: *a stated effect the implementation cannot
  perform is the same defect as an uncatalogued function, one level in.* Pins
  interpolation (each piece converts **as it is added**, so an echoing
  `__toString` appears in build order), concatenation, `(string)`, single- and
  multi-argument `echo`, a throwing `__toString`, and `.=` in a loop.
- `count.php` — `count()`: arrays answer directly, a `Countable` re-enters its
  own `count()`, and PHP 8 takes nothing else. The TypeError names the **class**
  for an object, and names a bool by its **value** — `true given`, not
  `bool given`, which is `ValueName` rather than `TypeName` and was settled
  against the engine.
- `coalesce_silence.php` — `??` over every subject: an undefined variable, a
  missing array key, a missing property, a nested undefined dimension, and the
  falsy values that are *set* (`0`, `""`, `false`, `[]`). Asking whether a value
  is set must not warn, and the **IS access mode never reached `propGet`**, so
  `$o->missing ?? "d"` printed `Warning: Undefined property` where the engine
  says nothing. The value was right, so nothing had looked. Also pins that the
  right-hand side is not evaluated when the left is set.
- `incdec.php` — the increment/decrement table, which is asymmetric and
  peculiar: `null++` is 1 silently but `null--` warns; `"5"++` is an *int*;
  `"z"++` is `"aa"`; `""++` and `""--` deprecate with **different wording**; and
  `"a-b"++` deprecates and increments anyway.
