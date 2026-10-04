# References, Places and Storage

## Summary

PHP distinguishes a value, a slot holding a value, a reference box shared by
slots, and a place that can locate storage later. Many bugs came from collapsing
two of these. Reads, writes and bindings are separate operations: assignment
writes through an existing reference, while `=&` replaces the binding.

The durable implementation rule is to keep a place until the consumer decides
whether it needs a value, a writable slot, or the box itself.

## The storage model

| Thing | Meaning |
|---|---|
| `php.Value` | a PHP value; it may contain a `*php.Ref` |
| slot | storage owned by a local, array element, property or frame field |
| `php.Ref` | a box shared by one or more slots |
| place | a delayed address into a local, dimension, property or static property |

Reading dereferences. Writing by value copies the right-hand value and writes
through a box already in the destination. Binding by reference replaces the
destination slot with the source box. A result-producing reference operation
must bind its result slot; storing through a reference left there by a previous
loop iteration silently mutates the old target.

## Fetch modes are semantic

A syntactically identical variable, dimension or property fetch can mean:

- warning read;
- silent read for a write receiver;
- read-modify-write, where the warning precedes the update;
- slot creation without a read;
- by-reference fetch, which must hand out a box;
- function-argument fetch, whose mode is decided by the callee's parameter.

The mode belongs in the IR. Reconstructing it from syntax or from the eventual
opcode loses ordering, diagnostics and reference identity.

A place can be the receiver of another place: `$a[0]->p`, `$a[0][1]`, a static
property whose class is computed, or a hooked/overloaded property reached for
indirect modification. Every consumer that once accepted a named local must be
audited when places become first-class.

## References in arrays and properties

- Copying an array or property table copies its reference boxes; it does not
  clone their contents.
- A foreach-by-reference cursor follows positions in the live array rather than
  a snapshot of values.
- A reference to a typed property carries that property's type source. Rebinding
  or freeing the slot removes one source, not every source with the same
  declaration name.
- Private properties are separate slots per declaring class, even when their
  source names match. Storage keys and source-visible names are not the same.
- An append asks whether the array has a next integer key. Once
  `PHP_INT_MAX` is occupied, append throws and leaves the array unchanged.

Reference holder counts are observable in `var_dump`'s `&` marker. A box being
present is not enough: internal write traversal can box an element temporarily,
and the marker tracks a live second holder.

## Hooked and overloaded storage

Indirect modification first performs the read required to obtain storage.

- A property with `&get` can hand out its box.
- A non-reference hook result is writable only when it is an object handle;
  otherwise PHP raises the hooked-property indirect-modification error.
- `__get` and `ArrayAccess::offsetGet` reached as storage warn that the
  modification has no effect, then modify a copy. An object result is silent
  because mutation reaches the same object.
- Binding *to* a hooked or overloaded property is not the same as binding to a
  plain property and has its own refusal.
- A readonly property can never be opened for indirect writing, including
  before its first direct initialization. Visibility is checked first.

These rules require both the read result and the identity of the source
property/container. Passing only a `php.Value` to the eventual write loses the
diagnostic target.

## Scope and name-based storage

`$GLOBALS`, `global $x`, ordinary file-body compiled variables and computed
variable names all address symbol tables, but not always the same one. The file
body's locals are globals. Function locals normally have static Go slots;
computed names need a name-to-slot map, and arbitrary byte-string names must be
quoted in textual IR so spaces, quotes and newlines survive.

When a scope that created a binding exits, it relinquishes its holder. A normal
function can defer `php.Unshare` for locals it bound with `=&`; it must not do
that for a by-reference parameter whose binding belongs to the caller, or for a
generator whose frame outlives the Go call.

## Relevant files

- `runtime/php/ref.go`, `array.go`, `object.go` — boxes and storage owners.
- `internal/lower/reads.go`, `ir.go` — explicit reads and place representation.
- `internal/gogen/gogen.go` — place rendering, read/write/bind modes and scope
  unsharing.
- `runtime/interp/locals.go`, `property.go`, `hooks.go`, `arrayaccess.go` —
  interpreted storage paths.

## Test coverage

Differential fixtures include `references.php`, `assign_by_reference.php`,
`byref_argument_places.php`, `nested_dimension_writes.php`,
`hooked_property_as_storage.php`, `overloaded_as_storage.php`,
`list_by_reference.php`, and `readonly_indirect_and_full_array.php`. Any
reference-result case should execute the same opline more than once.

## Pitfalls

- `ZEND_MAKE_REF`, sending a by-reference argument and returning by reference
  are different mechanisms.
- A silent write fetch is not a normal read with its warning suppressed after
  the fact; the read may not happen at all.
- A Go map cannot model PHP insertion order, so materializing an entire symbol
  table requires an ordered representation.
- `ZEND_FREE` is not necessarily irrelevant in an SSA compiler: it can end a
  temporary reference holder even when it frees no storage slot.
- **An address can hide behind a read.** `foreach ($a as &$v)` opens `$a`'s
  storage box (`PlaceBox` in the catalogue), but by the time promotion runs,
  `ExplicitReads` has rewritten the argument to `read_var($a)`. A pass that
  looks for places in address positions has to follow a read back to its
  place, or it promotes `$a` and the loop's writes land in a box nothing reads
  (`foreach_by_ref_local.php`).
- **Every container store copies an array**, and each store path has to do it
  itself: `WriteDim` and `ArrayAppend` did, the emitted `propSet` and
  `staticSet` did not, so `$o->a = $arr; $o->a[] = 2;` grew `$arr`
  (`property_store_copies.php`). The ownership trace relies on it: a write
  through a container is not a write to what was stored.
- **An argument is not copied, and neither is a promoted parameter.** Only a
  store copies an array, so a by-value parameter that is never stored is the
  caller's own array, and a write to the original through a global or a
  property shows through it (open in TODO.md). Anything that reasons about
  "this frame's array" has to treat a parameter as borrowed.
- **`php.Array` is a header over a shared body** (2026-09-27). The header is
  the holder's -- internal pointer, foreach cursors -- and the body is the
  elements, shared until a holder writes; every mutator calls `own` first, and
  nothing outside `array.go` touches the body. Identity for cycle detection is
  the body's (`Array.Identity`), and the index inside the body is packed or
  open-addressed (`arrayindex.go`), never a Go map.
