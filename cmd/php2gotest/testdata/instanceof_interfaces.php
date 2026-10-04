<?php
// The declared hierarchy is **not a chain**. A class has one parent but any
// number of interfaces, and each of those may extend any number more, so a walk
// that follows only `parentOf` answers false for every interface -- and a
// `catch` clause naming one never catches.

interface Top {}
interface Left extends Top {}
interface Right extends Top {}
interface Both extends Left, Right {}

class Base implements Left {}
class Middle extends Base implements Right {}
class Leaf extends Middle {}

$b = new Base;
$m = new Middle;
$l = new Leaf;

var_dump($b instanceof Base, $b instanceof Left, $b instanceof Top, $b instanceof Right);
var_dump($m instanceof Base, $m instanceof Left, $m instanceof Right, $m instanceof Top);
var_dump($l instanceof Base, $l instanceof Middle, $l instanceof Leaf);
var_dump($l instanceof Left, $l instanceof Right, $l instanceof Top);

class Wide implements Both {}
$w = new Wide;
var_dump($w instanceof Both, $w instanceof Left, $w instanceof Right, $w instanceof Top);

// A `catch` clause is the same test, so an interface names a catchable set.
interface Marker {}
class MarkedError extends RuntimeException implements Marker {}
try {
    throw new MarkedError("marked");
} catch (Marker $e) {
    echo "caught by interface: ", $e->getMessage(), "\n";
}
try {
    throw new MarkedError("still an exception");
} catch (RuntimeException $e) {
    echo "caught by class: ", $e->getMessage(), "\n";
}
$e = new MarkedError("both");
var_dump($e instanceof Marker, $e instanceof RuntimeException, $e instanceof Throwable);

// An interface reached only through a class declared at **run time**, whose
// harvested entry is unlinked.
if (true) { class Lazy extends Middle {} }
$z = new Lazy;
var_dump($z instanceof Left, $z instanceof Right, $z instanceof Top, $z instanceof Base);
