<?php
// A global overwritten at file scope keeps its old object alive while a
// function can still hold it through $GLOBALS by a computed name, which may
// be any global (a literal name: lifetime_global_literal).
class D {
    public static $quiet = false;
    function __construct(public string $n) {}
    function __destruct() { if (!self::$quiet) { echo "-{$this->n} "; } }
}
class K { public static $k = []; }
function grabComputed($n) { K::$k[] = $GLOBALS[$n]; }

$q = new D('q1');
grabComputed('q');
$q = new D('q2');
echo "| computed superglobal\n";
D::$quiet = true;
