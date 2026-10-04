<?php
// A static method's locals die when it returns, as a function's do.
class D {
    function __construct(public string $n) {}
    function __destruct() { echo "-{$this->n} "; }
}
class P {}
class S {
    public static $kept = [];
    static function locals($a) { $x = new D('x'); $y = new D('y'); echo "s "; }
    static function keeps() { $k = new D('k'); self::$kept[] = $k; echo "keeps "; }
    static function returns() { $r = new D('r'); return $r; }
    static function reassigns() { $v = new D('v1'); $v = new D('v2'); echo "reassigned "; }
    // Temporaries handed to a call that keeps nothing die at the call, so
    // the second takes the first's number back.
    static function temps() { var_dump(new P); var_dump(new P); }
}
S::locals(new D('a'));
echo "| locals\n";
S::keeps();
echo "| kept\n";
$r = S::returns();
echo "| returned\n";
S::reassigns();
echo "| reassigns\n";
S::temps();
echo "| temps\n";
echo "| end\n";
