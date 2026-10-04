<?php
// The value a store replaces dies right after the store when its container
// alone owns it: an element of an array local only these stores touch, a
// property of such a stdClass, and what a variable held when `=&` rebinds
// it. The store's result is taken first (gh10168), so `var_dump($a = new X)`
// prints the new value whatever the old one's destructor does
// (temporary_cleaning_013).

class Q { static $quiet = false; }

class D {
    public $tag;
    function __construct($tag) { $this->tag = $tag; }
    function __destruct() { if (!Q::$quiet) echo "~", $this->tag, "\n"; }
}

class T {
    function __destruct() { throw new Exception("dtor"); }
}

function show(Throwable $e) {
    echo get_class($e), ":", $e->getMessage();
    for ($p = $e->getPrevious(); $p; $p = $p->getPrevious()) {
        echo " <- ", get_class($p), ":", $p->getMessage();
    }
    echo "\n";
}

echo "-- element overwrite\n";
$arr = [new D("a0"), new D("a1")];
$arr[0] = new D("a2");
echo "after a2\n";
var_dump(($arr[1] = new D("a3"))->tag);
echo "after a3\n";
$arr[1] = 5;
echo "after scalar\n";

echo "-- property overwrite\n";
$obj = (object)["p" => new D("p0")];
$obj->p = new D("p1");
echo "after p1\n";
$obj2 = new stdClass;
$obj2->q = new D("q0");
$obj2->q = null;
echo "after q\n";

echo "-- throwing destructor at the store\n";
$tarr = [new T];
try { var_dump($tarr[0] = new D("tv")); } catch (Exception $e) { show($e); }
$tobj = (object)["p" => new T];
try { $tobj->p = 1; } catch (Exception $e) { show($e); }

echo "-- variable overwrite read back\n";
$v = new D("v0");
try { var_dump(($v = new D("v1"))->tag); } catch (Exception $e) { show($e); }
$tv = new T;
try { var_dump($tv = new stdClass); } catch (Exception $e) { show($e); }

// The store is the try's last statement: the destructor's throw is caught.
$last = new T;
try { $last = 7; } catch (Exception $e) { show($e); }
$lastObj = new T;
try { $lastObj = new stdClass; } catch (Exception $e) { show($e); }

echo "-- rebind\n";
$r = new D("r0");
$other = new D("other");
var_dump(($r = &$other)->tag);
echo "after rebind\n";
$rt = new T;
$o2 = new stdClass;
try { var_dump($rt = &$o2); } catch (Exception $e) { show($e); }

echo "-- negatives: another holder\n";
$keep = new D("kept");
$shared = [$keep];
$shared[0] = new D("s1");
echo "after shared (kept stays)\n";
$read = [new D("r-el")];
$alias = $read;
$read[0] = 1;
echo "after copy (copy holds it)\n";
$prop = (object)["p" => new D("prop-el")];
$seen = $prop->p;
$prop->p = 3;
echo "after prop read (read value holds it)\n";
$rebound = new D("rb-held");
$copy = $rebound;
$tgt = 1;
$rebound = &$tgt;
echo "after rebind of shared (copy holds it)\n";
function g() { global $gl; static $h; $h = $gl[0]; }
$gl = [new D("global-el")];
g();
$gl[0] = 1;
echo "after global (another body keeps it)\n";
$iter = [new D("iter-el")];
foreach ($iter as $each) {}
$iter[0] = 1;
echo "after foreach (the loop variable holds it)\n";
$iref = [new D("iref")];
$ref = &$iref[0];
$copyOut = $ref;
$iref[0] = 1;
echo "after reference copy (the copy holds it)\n";
$heldObj = new D("held-obj");
$slots = [1];
$slots[0] = $heldObj;
$slots[0] = 2;
echo "after held store (the variable holds it)\n";
$pre = $heldObj;
$tgt2 = 1;
$pre = &$tgt2;
echo "after held rebind (the variable holds it)\n";
$box = $alsoBox = new stdClass;
$box->q = new D("box-q");
$peek = $alsoBox->q;
$box->q = 3;
echo "after shared container (the read holds it)\n";
$loopArr = [new D("loop0")];
for ($i = 1; $i <= 2; $i++) {
    $loopArr[0] = new D("loop$i");
}
echo "after loop\n";
echo "end\n";
Q::$quiet = true;
