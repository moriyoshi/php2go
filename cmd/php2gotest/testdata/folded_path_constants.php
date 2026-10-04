<?php
// `__FILE__`, `__DIR__` and a closure's own `__FUNCTION__` are folded into
// string literals by the Zend compiler, so the emitted program carries the path
// the **harvest** was handed rather than the one the program refers to itself
// by. Nothing here prints a path -- the point is that every spelling of it
// agrees with every other, which is what breaks when only some are reconciled.
$g = function (int $x) { return __METHOD__; };
$name = $g(1);
try {
    $g([]);
} catch (TypeError $e) {
    // The message names the closure the way a diagnostic does;
    // `__METHOD__` names it the way the compiler folded it. Same closure, so same text.
    echo $e->getMessage() === $name . '(): Argument #1 ($x) must be of type int, array given'
        ? "closure name agrees\n" : "closure name differs\n";
}
try {
    throw new Exception("x");
} catch (Throwable $e) {
    echo $e->getFile() === __FILE__ ? "throw names the file\n" : "two paths\n";
}
function outer() { return __FILE__; }
echo outer() === __FILE__ ? "one path\n" : "two paths\n";
class C {
    const D = __DIR__;
    public function m() { return __DIR__; }
}
echo (new C)->m() === __DIR__ ? "one dir\n" : "two dirs\n";
echo C::D === __DIR__ ? "constant agrees\n" : "constant differs\n";

// A magic constant is legal as an array key, and the key of an array *literal*
// never passes through the operand renderer -- so it is a second route the same
// path reaches the program by, and it has to agree with the lookup beside it.
[__FILE__ => $k] = [__FILE__ => "keyed by the file"];
echo $k, "\n";
$d = [__DIR__ => "keyed by the dir"];
echo $d[__DIR__], "\n";
echo isset($d[__DIR__]) ? "key found\n" : "key missing\n";
