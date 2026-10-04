<?php
// `??=` evaluates its **left-hand side once**, which is what the engine's
// ZEND_COPY_TMP exists for: the key is computed, duplicated, and then read by
// the `IS` fetch and written by the assignment.

function keyOf(string $k): string { echo "  keyOf($k)\n"; return $k; }
function value(string $v): string { echo "  value($v)\n"; return $v; }

$a = ["present" => "kept"];

echo "already set:\n";
$a[keyOf("present")] ??= value("ignored");
echo "not set:\n";
$a[keyOf("absent")] ??= value("stored");
var_dump($a);

// The right-hand side is **not** evaluated when the left is already set.
$b = null;
echo "null is not set:\n";
$b ??= value("for null");
var_dump($b);
$b ??= value("never runs");
var_dump($b);

// Nested, so the container expression is single-evaluated too.
$deep = [];
function container(): string { echo "  container()\n"; return "c"; }
$deep[container()][keyOf("inner")] ??= "made";
var_dump($deep);

class Holder { public $p; public array $arr = []; }
$h = new Holder;
$h->p ??= value("property");
$h->arr[keyOf("via property")] ??= value("element");
var_dump($h);
