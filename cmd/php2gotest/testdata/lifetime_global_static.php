<?php
// A static method binds a global with `global`: overwriting that global at
// file scope must not free what the static property still holds. Alone in
// its file: a body reading any superglobal elsewhere would refuse the
// release for another reason.
class D {
    public static $quiet = false;
    function __construct(public string $n) {}
    function __destruct() { if (!self::$quiet) { echo "-{$this->n} "; } }
}
class S { public static $k; static function grab() { global $o; self::$k = $o; } }
$o = new D('a');
S::grab();
$o = new D('b');
echo "| global via static method\n";
$t = new D('t1');
$t = new D('t2');
echo "| unshared global\n";
D::$quiet = true;
