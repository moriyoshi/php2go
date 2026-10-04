<?php
// A closure made from something already named is no frame of its own: the
// call it makes pushes the named function's, and a trace shows that once
// (closure_064). It carries the scope it was made in, so `$this->priv(...)`
// made inside the class reaches the private method from anywhere.

function throws() {
    throw new Exception("boom");
}
function where() {
    foreach (debug_backtrace() as $f) {
        echo "  ", ($f['class'] ?? ''), ($f['type'] ?? ''), $f['function'], "\n";
    }
}

$t = throws(...);
try {
    $t();
} catch (Exception $e) {
    foreach ($e->getTrace() as $f) {
        echo "  ", $f['function'], "\n";
    }
}
$w = where(...);
$w();
array_map($w, [1]);

class Inner {
    private function hidden() { where(); return "hidden"; }
    private static function sh() { return "static hidden"; }
    public function expose() { return $this->hidden(...); }
    public static function exposeStatic() { return self::sh(...); }
}
$h = (new Inner)->expose();
var_dump($h());
var_dump(call_user_func(Inner::exposeStatic()));
echo "invoke:\n";
$w->__invoke();
$lit = function () { where(); };
echo "literal invoke:\n";
$lit->__invoke();
