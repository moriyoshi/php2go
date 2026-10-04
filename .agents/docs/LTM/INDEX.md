# Long-Term Memory Index

Long-term memory documents contain durable, topic-oriented project knowledge
distilled from the chronological development journal.

| Document | Summary |
|----------|---------|
| [differential-oracle](./differential-oracle.md) | Grade against the linked engine rather than hand-written expectations, and harvest its tables rather than transcribing them. Equal state, equal capabilities, and a harness as faithful as the interpreter. |
| [phpt-harness-fidelity](./phpt-harness-fidelity.md) | `php2phpt` must reproduce `run-tests.php`, not something reasonable. Three infidelities so far, worth ~850 tests, each disguised as hundreds of interpreter bugs. |
| [zend-operand-encoding](./zend-operand-encoding.md) | Recurring ways `zend_op` fields — byte offsets, enumerated `*_MASK` flags, operand roles — serialize into plausible-but-wrong IR. |
| [class-model-and-harvesting](./class-model-and-harvesting.md) | Class entry tables include what was inherited: filter methods by scope, order properties by offset, and treat `$this` as an unused operand. |
| [variable-fetch-diagnostics](./variable-fetch-diagnostics.md) | PHP's diagnostics follow the fetch type, not the syntax: write fetches create silently, read-modify-write fetches warn first. Also how a diagnostic reaches a user error handler, and which superglobals exist (the compile decides). |
| [type-declarations](./type-declarations.md) | Where parameter and return types are checked, what weak mode converts, and the handful of rules that are not what they look like. |
| [serialization-and-iteration](./serialization-and-iteration.md) | Serialization property policy and mangled names, plus the shared iterator model behind foreach, `yield from`, ArrayAccess and argument unpacking. |
| [constant-expressions](./constant-expressions.md) | Initializers the compiler could not fold: harvesting the AST, the three outcomes evaluation has, and the class-name cache that outlives `Engine.Reset`. |
| [visibility-and-composition](./visibility-and-composition.md) | Visibility, traits and callable scope, including alias table identity, private slots/methods, declaration checks and signature compatibility. |
| [property-hooks](./property-hooks.md) | PHP 8.4 hooks: where the bodies live, backed versus virtual, and why "am I inside this hook?" is a prototype comparison rather than a pointer one. |
| [unsupported-versus-php-errors](./unsupported-versus-php-errors.md) | When a failure is a catchable PHP error and when it is an honest interpreter gap, and why the distinction keeps the pass rate meaningful. |
| [named-arguments](./named-arguments.md) | `f(name: value)` is resolved against the callee, which is not known until DO_FCALL: the eleven callee shapes that each need a parameter list, what fills the hole a name leaves, and which builtins forward names. |
| [lazy-objects](./lazy-objects.md) | PHP 8.4 lazy objects: laziness is per property, not per object. What takes a property out of the deferred set, and the readonly value a reset keeps. |
| [reflection-api](./reflection-api.md) | Reflection answers with what `ext/reflection` computes, not with php2go's own values: the `Closure::__invoke` trampoline, the closure-versus-fake-closure attribute target, and the two unwritable property names. |
| [object-lifetime](./object-lifetime.md) | No refcounts, so php2go walks the roots. Where that is faithful, the three different moments an object has, and why a weak reference clears when the target is *freed*. |
| [lowering-and-corpus-shape](./lowering-and-corpus-shape.md) | What an op_array corpus looks like to a lowering: coverage counted in bodies not oplines, the multi-opline AST node groups, result slots that are not where you expect, and the `runtime/php` / `runtime/interp` boundary. |
| [intrinsics-and-code-generation](./intrinsics-and-code-generation.md) | Compiler/runtime/emitter boundaries, explicit reads and stores, shared semantic-role recognizers, effect contracts, specialization and deterministic emission. |
| [control-flow-frames-and-resumables](./control-flow-frames-and-resumables.md) | CFG and exception-region invariants, cloned finally bodies, generated state machines, and the distinct compiled/interpreted generator and fiber drivers. |
| [references-places-and-storage](./references-places-and-storage.md) | Values, slots, boxes and places; why reads, writes and bindings are separate, and how indirect hooked/overloaded storage preserves identity and diagnostics. |
| [builtin-and-userland-runtime](./builtin-and-userland-runtime.md) | The generated builtin ledger, harvested PHP implementations, narrow native capability leaves, hidden helpers and engine-owned public signatures. |
| [streams-filesystem-and-host-boundaries](./streams-filesystem-and-host-boundaries.md) | Embedded and host paths, live resources, filesystem transactions, DNS/header composites, libc state and build-derived registries. |
| [call-dispatch-and-runtime-declarations](./call-dispatch-and-runtime-declarations.md) | Prepared calls, scopes and receivers, callable parsing, autoload availability, conditional declarations and runtime argument binding. |
| [exceptions-and-diagnostic-provenance](./exceptions-and-diagnostic-provenance.md) | PHP diagnostics versus throwables versus runtime gaps, raise-site stacks, handlers, finally chaining and replayed compile/link errors. |
| [quality-gates-and-corpus-measurement](./quality-gates-and-corpus-measurement.md) | Outcome buckets, differential evidence, reproducible coordination and paths, shared-tree gates, census drift and comparable measurements. |

New documents should use descriptive kebab-case names and be added to this
table. Keep unresolved work in `../TODO.md`, not in LTM.
