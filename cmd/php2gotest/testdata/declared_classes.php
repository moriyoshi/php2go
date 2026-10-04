<?php
// `get_declared_classes()` lists the **engine's** own classes first, in the
// order it registered them, then the program's in declaration order. That made
// it unanswerable until the engine's list was harvested -- and it has to be
// harvested rather than written down, because the set depends on which
// extensions the build links.
//
// Two details the count caught: an **enum is a class** and is listed, while an
// interface and a trait are not -- they have their own `get_declared_*`.
interface Shape {}
trait Helper {}
enum Suit { case Hearts; case Spades; }
class First implements Shape { use Helper; }
class Second extends First {}

$classes = get_declared_classes();

// The program's own come last, in declaration order.
echo implode(",", array_slice($classes, -3)), "\n";

// The engine's own come first and are not sorted.
var_dump($classes[0] === "InternalIterator");

// An interface and a trait are absent from this list and present in theirs.
var_dump(in_array("Shape", $classes), in_array("Helper", $classes));
var_dump(get_declared_traits());

$interfaces = get_declared_interfaces();
var_dump(in_array("Shape", $interfaces), in_array("First", $interfaces));
// The engine's own come first there too, unsorted.
var_dump($interfaces[0] === "Traversable");
var_dump(array_slice($interfaces, -1));

// The marker that makes all of this answerable also answers isInterface(),
// which was filed as impossible for exactly this reason.
foreach (["Shape", "First", "Suit"] as $n) {
    $r = new ReflectionClass($n);
    printf("%s iface=%d enum=%d instantiable=%d\n",
        $n, $r->isInterface(), $r->isEnum(), $r->isInstantiable());
}

// Well-known engine classes are there, and a name nobody declares is not.
var_dump(in_array("Exception", $classes), in_array("NotAThing", $classes));
