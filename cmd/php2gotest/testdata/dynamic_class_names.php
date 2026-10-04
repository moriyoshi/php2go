<?php
namespace test\ns1;

// A class named at run time is looked up globally: `new $x` with `$x =
// "Exception"` is the engine's Exception even beside a namespaced class of
// the same short name. And a closure bound to a scope that names no class
// warns and answers null.

class Exception {}
$x = "Exception";
$e = new $x("message");
echo get_class($e), " ", $e->getMessage(), "\n";
$c = function () {};
var_dump($c->bindTo(new \stdClass, "Nope\\Missing"));
var_dump(\Closure::bind($c, null, "AlsoMissing"));
var_dump($c->bindTo(null, Exception::class) instanceof \Closure);
