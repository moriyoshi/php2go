<?php
// A function declared in several branches is the body of the branch that
// ran: each declaration is compiled, and the one control reaches binds the
// name. A second declaration reached with the name taken is the engine's
// `Cannot redeclare`, naming the declaration already bound.
$n = count($argv ?? [1]);

// The first declaration runs.
if ($n > 0) {
    function first($a) { return "first-a:$a"; }
} else {
    function first(&$a) { $a .= "?"; return "first-b:$a"; }
}
$s = "x";
echo first($s), " ", $s, "\n";

// A later declaration runs, taking its argument by reference where the
// other would not: the send is decided by the declaration that ran.
if ($n > 100) {
    function second($a) { return "second-a:$a"; }
} else {
    function second(&$a, $b = "d") {
        static $calls = 0;
        $calls++;
        $a .= "!";
        return "second-b:$a:$b:$calls";
    }
}
$t = "y";
echo second($t), " ", $t, "\n";
echo second($t, "e"), " ", $t, "\n";
echo call_user_func_array('second', [&$t]), " ", $t, "\n";
var_dump(function_exists('second'));

// The middle of three.
if ($n > 100) {
    function third() { return "third-a"; }
} elseif ($n > 0) {
    function third() { return "third-b"; }
} else {
    function third() { return "third-c"; }
}
echo third(), "\n";

// Nested branches.
if ($n > 0) {
    if ($n > 100) {
        function fourth() { return "fourth-a"; }
    } else {
        function fourth() { return "fourth-b"; }
    }
}
echo fourth(), "\n";
$fs = get_defined_functions()['user'];
sort($fs);
echo implode(",", $fs), "\n";

// Called before any declaration ran: undefined.
try {
    fifth();
} catch (Error $e) {
    echo get_class($e), ": ", $e->getMessage(), "\n";
}
if ($n > 100) {
    function fifth() { return "fifth-a"; }
} else {
    function fifth() { return "fifth-b"; }
}
echo fifth(), "\n";

// Two declarations that both run: the second is fatal, naming the line of
// the first.
if ($n > 0) {
    function sixth() { return "sixth-a"; }
}
echo sixth(), "\n";
if ($n > 0) {
    function sixth() { return "sixth-b"; }
}
echo "not reached\n";
