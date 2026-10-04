<?php
// Each `*_exists` asks about a **different kind**, measured against the engine:
//
//   class_exists      class, enum, abstract class   -- not interface, not trait
//   interface_exists  interface only
//   trait_exists      trait only
//   enum_exists       enum only
//
// `class_exists` and `interface_exists` shared one body here, so they agreed
// with each other everywhere and with the engine only by accident; an interface
// answered true to `class_exists` and a class answered true to
// `interface_exists`. `trait_exists` and `enum_exists` were refused outright.
class K {}
interface I {}
trait T {}
enum E { case A; }
enum BE: int { case X = 1; }
abstract class AB {}
final class FC {}

foreach (['K', 'I', 'T', 'E', 'BE', 'AB', 'FC', 'Nope'] as $n) {
    printf("%-5s class=%d interface=%d trait=%d enum=%d\n", $n,
        class_exists($n), interface_exists($n), trait_exists($n), enum_exists($n));
}

// The second argument turns autoloading off; the kind question is the same.
var_dump(class_exists('K', false), interface_exists('I', false));
var_dump(trait_exists('T', false), enum_exists('E', false));
var_dump(class_exists('I', false), interface_exists('K', false));

// An engine class is a class; an engine interface is an interface. Both were
// wrong before: `class_exists('Exception')` was false because `knownClass`
// answers only for classes the program declares, and every engine interface
// that was not iterator-shaped fell through to the class arm -- so
// `interface_exists('Countable')` was false and `class_exists('Countable')`
// true, each the opposite of the engine.
foreach (['Countable', 'ArrayAccess', 'Stringable', 'Traversable', 'Iterator',
          'IteratorAggregate', 'JsonSerializable', 'Throwable',
          'Exception', 'RuntimeException', 'TypeError', 'ArrayObject'] as $n) {
    printf("%-18s class=%d interface=%d trait=%d\n", $n,
        class_exists($n), interface_exists($n), trait_exists($n));
}
echo "done\n";
