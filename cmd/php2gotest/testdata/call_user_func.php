<?php
// The compiler folds a literal `call_user_func("f", ...)` into a direct call,
// so what reaches the library function is the dynamic form: a callable held in
// a variable, a [target, method] pair, a Closure.
function greet($who, $punct = "!") { return "hi $who$punct"; }
$name = "greet";
var_dump(call_user_func($name, "world"));
var_dump(call_user_func($name, "world", "?"));
var_dump(call_user_func_array($name, ["world", "."]));

class Greeter {
    public $prefix = ">";
    public function say($what) { return $this->prefix . $what; }
    public static function shout($what) { return strtoupper($what); }
}
$o = new Greeter;
var_dump(call_user_func([$o, "say"], "instance"));
var_dump(call_user_func(["Greeter", "shout"], "static"));
var_dump(call_user_func_array([$o, "say"], ["array form"]));

$closure = function ($a, $b) { return $a . "/" . $b; };
var_dump(call_user_func($closure, 1, 2));
var_dump(call_user_func_array($closure, [3, 4]));

// A by-reference parameter writes back into whatever the element aliases.
function bump(&$n) { $n++; }
$counter = 5;
$args = [&$counter];
call_user_func_array("bump", $args);
var_dump($counter);
$direct = 7;
call_user_func_array("bump", [&$direct]);
var_dump($direct);

// Nothing to call at all, and the wrong shape for the second argument.
var_dump(call_user_func_array($closure, []) ?? "unreached");
