<?php
// Temporaries consumed by an operator or a fetch die there, running their
// destructor at that point (temporary_cleaning_013), and on the failure path
// they die while the exception unwinds, their destructor's throw taking the
// failure as its previous. Negative cases keep the object alive.

class D {
    public $foo;
    public $tag;
    function __construct($tag = "d") { $this->tag = $tag; $this->foo = new stdClass; }
    function __toString() { return "D"; }
    function __destruct() { if (!Q::$quiet) echo "~", $this->tag, "\n"; }
}

class T {
    public $foo;
    function __construct() { $this->foo = new stdClass; }
    function __toString() { return "T"; }
    function __destruct() { throw new Exception("dtor"); }
}

class Q { static $quiet = false; }

function show(Throwable $e) {
    echo get_class($e), ":", $e->getMessage();
    for ($p = $e->getPrevious(); $p; $p = $p->getPrevious()) {
        echo " <- ", get_class($p), ":", $p->getMessage();
    }
    echo "\n";
}

echo "-- concat and cast\n";
var_dump(new D("c1") . "x");
echo "after c1\n";
var_dump((string) new D("c2"));
echo "after c2\n";
try { var_dump(new T . "x"); } catch (Exception $e) { show($e); }

echo "-- array plus\n";
var_dump([0] + [new D("p1")]);
echo "after p1\n";
$kept = [] + [new D("p2")];
echo "after p2 (kept)\n";
$a = [0];
$a += [new D("p3")];
echo "after p3\n";
$b = [];
$b += [new D("p4")];
echo "after p4 (kept)\n";
var_dump($kept[0]->tag, $b[0]->tag);

// The compound store is done before the literal dies: its destructor's
// throw comes after `$g` took the sum.
$g = [0 => 0];
try { $g += [0 => new T, 1 => 1]; } catch (Exception $e) { show($e); }
var_dump(count($g));

echo "-- failure path\n";
try { var_dump([new T] + 5); } catch (Throwable $e) { show($e); }
function boom() { throw new LogicException("boom"); }
try { var_dump(new T, boom()); } catch (Throwable $e) { show($e); }

// A failing user function's trace keeps its arguments: the temporary
// outlives the failure.
function thrower($o) { throw new LogicException("thrown"); }
try { thrower(new D("arg")); } catch (LogicException $kept_e) { echo "caught ", $kept_e->getMessage(), "\n"; }

echo "-- fetches\n";
class A implements ArrayAccess {
    function offsetGet($x): mixed { return [1]; }
    function offsetSet($x, $y): void {}
    function offsetExists($x): bool { return true; }
    function offsetUnset($x): void {}
    function __destruct() { echo "~A\n"; }
}
var_dump((new A)[0]);
echo "after A\n";
var_dump(isset((new D("i1"))->foo->bar));
echo "after i1\n";
var_dump(get_class((new D("f1"))->foo));
echo "after f1\n";

echo "-- read-write through a fresh call result\n";
var_dump((function () { return new D("rw1"); })()->tag .= "!");
echo "after rw1\n";
try { var_dump((function () { return new T; })()->foo++); } catch (Exception $e) { show($e); }

echo "-- not fresh\n";
function cached() { static $c; return $c ??= new D("cached"); }
var_dump(cached()->tag .= "?");
echo "after cached\n";
$held = new D("held");
var_dump((function () use ($held) { return $held; })()->tag .= "?");
echo "after held\n";
class Cache { static $o; }
Cache::$o = new D("cache");
var_dump((function () { return Cache::$o; })()->tag .= "?");
echo "after cache (a static holds it)\n";
$h2 = new D("h2");
var_dump($h2 . "x");
echo "after h2\n";
echo "end\n";
// What is still alive dies at teardown, in an order this does not test.
Q::$quiet = true;
