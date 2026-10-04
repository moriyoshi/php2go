<?php
// Two things look identical to the emitter -- a class name it cannot resolve --
// and they are not the same at all:
//
//   new DateTime()       PHP has the class, php2go has not implemented it: a gap
//   new DoesNotExists()  nobody has it: the engine's own run-time Error
//
// Answering the second for the first would replace a named gap with a wrong
// answer, so the emitter asks the **linked engine** which classes it provides
// rather than keeping a list. This file covers the side that is an answer; the
// gap side stays `unsupported` and so cannot be pinned here.
try {
    new DoesNotExists();
} catch (Error $e) {
    echo get_class($e), ": ", $e->getMessage(), "\n";
}

try {
    new Nope\Deep\Thing();
} catch (Error $e) {
    echo get_class($e), ": ", $e->getMessage(), "\n";
}

// A name that is only ever used in a `catch` is not instantiated at all.
try {
    throw new RuntimeException("caught");
} catch (RuntimeException $e) {
    echo $e->getMessage(), "\n";
}

// And an ordinary declared class still works beside them.
class Real { public $v = 1; }
$r = new Real();
var_dump($r->v);

// Reached through a variable, which was always a run-time lookup.
$name = "AlsoMissing";
try {
    new $name();
} catch (Error $e) {
    echo get_class($e), ": ", $e->getMessage(), "\n";
}
echo "done\n";

// `instanceof` on a name nobody declares is plain **false**, and it does not
// autoload -- nothing about the answer is deferred. Refusing it was right only
// for a class PHP has and this emitter does not, where `false` would be a wrong
// answer rather than the engine's own.
$r2 = new Real();
var_dump($r2 instanceof AlsoNotAThing);
var_dump($r2 instanceof Nope\Deep\Thing);
var_dump($r2 instanceof Real);

// The same name in a `catch` clause simply does not match.
try {
    throw new Exception("thrown");
} catch (NotAnErrorClass $e) {
    echo "wrong arm\n";
} catch (Exception $e) {
    echo "right arm: ", $e->getMessage(), "\n";
}

// An autoloader does not change it: instanceof never calls one.
spl_autoload_register(function ($n) { echo "autoload should not run: $n\n"; });
var_dump($r2 instanceof StillNotAThing);
