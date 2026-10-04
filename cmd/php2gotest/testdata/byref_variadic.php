<?php
// `function f(&...$rest)`: each collected argument keeps its **box**, so
// writing an element writes the variable the caller named.
function setAll(&...$args) {
    foreach ($args as $k => &$v) {
        $v = "set$k";
    }
    return count($args);
}
$a = 1;
$b = 2;
var_dump(setAll($a, $b), $a, $b);

// None at all.
function none(&...$xs) { return count($xs); }
var_dump(none());

// A fixed parameter in front, taken by value.
function mixedArity($first, &...$rest) {
    $first = "ignored";
    $rest[0] = "changed";
    return count($rest);
}
$p = "keep";
$q = "before";
var_dump(mixedArity($p, $q), $p, $q);

// Unpacking into one: the elements that are already boxes bind, and the rest
// go in by value.
function bump(&...$ns) { foreach ($ns as &$n) { $n++; } }
$x = 1;
$spread = [&$x];
bump(...$spread);
var_dump($x);

// Reading is ordinary.
function joined(&...$parts) { return implode("-", $parts); }
$one = "a";
$two = "b";
var_dump(joined($one, $two));
