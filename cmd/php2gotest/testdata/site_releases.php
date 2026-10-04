<?php
// Ownership proved per write: a local reused for unrelated values across
// statements releases what one write stored when the next store into it, in
// the same block with nothing touching the local between, replaces it
// (temporary_cleaning_013 as a whole). A slot bound as a reference, a read in
// between, or a closure capturing it by reference keeps the value alive.

class Q { static $quiet = false; }
class D {
    public $tag;
    function __construct($tag) { $this->tag = $tag; }
    function __toString() { return "D"; }
    function __destruct() { if (!Q::$quiet) echo "~", $this->tag, "\n"; }
}
class T { function __destruct() { throw new Exception("dtor"); } }
function show(Throwable $e) { echo get_class($e), ":", $e->getMessage(), "\n"; }

echo "-- one local, many uses\n";
$foo = [0];
$foo += [1];
$foo = new ArrayObject([1]);
$foo[0] = 2;
$foo = new D("v1");
$foo = new D("v2");
echo "after v2\n";
$foo = [new D("e0")];
$foo[0] = new D("e1");
echo "after e1\n";
$foo = (object)["p" => new D("p0")];
$foo->p = new D("p1");
echo "after p1\n";
$foo = new D("r0");
$bar = new D("r1");
var_dump(($foo = &$bar)->tag);
echo "after rebind\n";

echo "-- dropping an owned container\n";
class X {
    public $t;
    function __construct($t) { $this->t = $t; }
    function __destruct() { echo "~", $this->t, "\n"; if ($this->t[0] == "x") throw new Exception($this->t); }
}
$ca = [new D("c1"), 5, new D("c2")];
$ca = 1;
echo "after ca\n";
$co = (object)["p" => new D("o1"), "q" => new D("o2")];
$co = 2;
echo "after co\n";
try {
    $cb = [new X("x1"), new X("b2"), new X("x3")];
    $cb = 3;
} catch (Exception $e) { echo get_class($e), ":", $e->getMessage(), " prev=", $e->getPrevious() ? $e->getPrevious()->getMessage() : "none", "\n"; }
echo "after cb\n";

echo "-- in try blocks, throwing\n";
try {
    $baz = new T;
    var_dump($baz = new stdClass);
} catch (Exception $e) { show($e); }
try {
    $baz = [new T];
    var_dump($baz[0] = 1);
} catch (Exception $e) { show($e); }
try {
    $baz = (object)["p" => new T];
    var_dump($baz->p = 1);
} catch (Exception $e) { show($e); }

echo "-- closure held in a local\n";
try {
    $f = function () { return new class { function __toString() { return "a"; } function __destruct() { throw new Exception("f"); } }; };
    var_dump("{$f()}foo");
} catch (Exception $e) { show($e); }
try {
    $f = function () { return new class { function __toString() { return "b"; } function __destruct() { throw new Exception("g"); } }; };
    var_dump("x{$f()}y");
} catch (Exception $e) { show($e); }

echo "-- negatives\n";
$other = null;
$al = &$other;
$al = new D("aliased");
$copy = $other;
$al = 1;
echo "after aliased (the copy holds it)\n";
$rd = new D("read");
$seen = $rd;
$rd = 2;
echo "after read (the copy holds it)\n";
$cap = null;
$cl = function () use (&$cap) { static $k; $k = $cap; };
$cap = new D("captured");
$cl();
$cap = 3;
echo "after captured (the static holds it)\n";
function g() { global $gv; static $s; $s = $gv; }
$gv = new D("global");
g();
$gv = 4;
echo "after global (the static holds it)\n";
$h = function () { return new D("h1"); };
$hh = &$h;
$hh = function () { static $k; return $k ??= new D("kept-by-static"); };
var_dump("{$h()}");
echo "after bound closure local (the static holds it)\n";
$kept = new D("kept");
$m = function () use ($kept) { return $kept; };
var_dump("{$m()}");
echo "after capturing closure (the capture holds it)\n";
$o5 = null;
$ac = &$o5;
$ac = [new D("aliased-el")];
$cp5 = $o5;
$ac[0] = 5;
echo "after aliased container (the copy holds it)\n";
$o6 = null;
$rb = &$o6;
$rb = new D("aliased-rebind");
$cp6 = $o6;
$y6 = 6;
$rb = &$y6;
echo "after aliased rebind (the copy holds it)\n";
$o7 = null;
$ac7 = &$o7;
try {
    $ac7 = [new D("bound-earlier")];
    $cp7 = $o7;
    $ac7[0] = 7;
} catch (Exception $e) { show($e); }
echo "after binding in an earlier block (the copy holds it)\n";
$o8 = null;
try {
    $ac8 = &$o8;
    throw new LogicException("to the handler");
} catch (LogicException $e) {}
$ac8 = [new D("bound-before-throw")];
$cp8 = $o8;
$ac8[0] = 8;
echo "after binding before a throw (the copy holds it)\n";
$g9 = &$f9;
$f9 = function () { return new D("f9-first"); };
$g9 = function () { static $k; return $k ??= new D("f9-kept"); };
var_dump("{$f9()}");
echo "after closure local rebound through an alias (the static holds it)\n";
function mk() { return new D("mk"); }
var_dump(mk() . "x");
echo "after concat of a call\n";
echo "end\n";
Q::$quiet = true;
