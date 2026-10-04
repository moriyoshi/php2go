<?php
// A function's caught exception dies when the function returns -- the engine
// frees the frame's variables, the catch variable among them, in their order
// -- when the catch variable is its only holder: only asked what it is and
// what its message says -- never an argument, which a call that throws would
// keep in its trace. The next object takes the exception's number.
// Negative cases keep it alive: stored, handed on as a previous, returned,
// captured, or the frame read by name.

class Thing {}

function thrower(string $m) { throw new RuntimeException($m); }

function plain() {
    try { thrower("plain"); } catch (Exception $e) { echo $e::class, ": ", $e->getMessage(), "\n"; }
}
plain();
$n = new Thing; echo "after plain: ", spl_object_id($n), "\n"; unset($n);

// A local made before the try dies first, then the exception: the next object
// takes the exception's number, the one after that the local's.
function withLocal() {
    $local = new Thing;
    try { thrower("with local"); } catch (Exception $e) { echo $e->getMessage(), "\n"; }
    echo "local ", spl_object_id($local), "\n";
}
withLocal();
$a = new Thing; $b = new Thing;
echo "after withLocal: ", spl_object_id($a), " ", spl_object_id($b), "\n";
unset($a, $b);

class Loud extends Exception {
    function __destruct() { echo "Loud destroyed\n"; }
}
function loud() {
    try { throw new Loud("loud"); } catch (Loud $e) { echo "caught ", $e->getMessage(), "\n"; }
    echo "leaving loud\n";
}
loud();
echo "after loud\n";

class Keep { public static $kept = []; }

function stored() {
    try { thrower("stored"); } catch (Exception $e) { Keep::$kept[] = $e; }
}
stored();
$s = new Thing; echo "after stored: ", spl_object_id($s), " kept ", Keep::$kept[0]->getMessage(), "\n"; unset($s);

function chained() {
    try { thrower("inner"); } catch (Exception $e) { return new LogicException("outer", 0, $e); }
}
$c = chained();
$o = new Thing; echo "  next: ", spl_object_id($o), "\n";
echo "previous: ", $c->getPrevious()->getMessage(), "\n";

function returned() {
    try { thrower("returned"); } catch (Exception $e) { return $e; }
}
$r = returned();
$o = new Thing; echo "  next: ", spl_object_id($o), "\n";
echo "returned: ", $r->getMessage(), "\n";

function captured() {
    try { thrower("captured"); } catch (Exception $e) { return function () use ($e) { return $e->getMessage(); }; }
}
$f = captured();
$o = new Thing; echo "  next: ", spl_object_id($o), "\n";
echo "captured: ", $f(), "\n";

function byName() {
    try { thrower("by name"); } catch (Exception $e) { $vars = get_defined_vars(); Keep::$kept[] = $vars['e']; }
}
byName();
$o = new Thing; echo "  next: ", spl_object_id($o), "\n";
echo "by name: ", Keep::$kept[1]->getMessage(), "\n";

function inLoop() {
    for ($i = 0; $i < 2; $i++) {
        try { thrower("loop $i"); } catch (Exception $e) { echo $e->getMessage(), "\n"; }
    }
}
inLoop();
echo "end\n";
