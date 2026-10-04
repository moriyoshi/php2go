<?php
// A `function &f(): T` return is checked by the value its reference holds,
// coerced in place -- the caller bound to the reference sees the coerced
// value -- and a finally that changes the variable after the `return` is
// what the check sees.

class Box { public $v = "5"; function &get(): int { return $this->v; } }
$b = new Box;
$r = &$b->get();
var_dump($r, $b->v);
$r = 9;
var_dump($b->v);

function &bad(): int { static $s = "nope"; return $s; }
try { $x = &bad(); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }

function &late(): int {
    $x = 0;
    try { return $x; } finally { $x = 'late'; }
}
try { $y = &late(); var_dump($y); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
