<?php
// A function reads one global through $GLOBALS by a literal name: that
// global's old object stays alive when it is overwritten at file scope,
// while an unshared global's old object still dies there.
class D {
    public static $quiet = false;
    function __construct(public string $n) {}
    function __destruct() { if (!self::$quiet) { echo "-{$this->n} "; } }
}
class K { public static $k = []; }
function grabLiteral() { K::$k[] = $GLOBALS['p']; }
$p = new D('p1');
grabLiteral();
$p = new D('p2');
echo "| literal superglobal\n";
$t = new D('t1');
$t = new D('t2');
echo "| unshared global\n";
D::$quiet = true;
