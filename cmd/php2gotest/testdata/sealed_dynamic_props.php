<?php
// Classes that forbid dynamic properties refuse every way of creating one --
// a write, a nested append, a reference, a compound assignment (before any
// read warns) -- even after an unset() of the name. A WeakMap tells an
// append from a null key. An ordinary class's unset dynamic property is gone:
// writing it again is a new creation.

$map = new WeakMap;
foreach ([
    fn() => $map[] = 1,
    fn() => $map[null] = 1,
    function () use ($map) { $map[][1] = 1; },
] as $f) {
    try { $f(); } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
}
var_dump(isset($map->prop));
unset($map->prop);
foreach ([
    function () use ($map) { $map->prop = 1; },
    function () use ($map) { $map->prop[] = 1; },
    function () use ($map) { $r = &$map->prop; },
    function () use ($map) { $map->prop .= "x"; },
    function () use ($map) { $map->prop++; },
] as $f) {
    try { $f(); } catch (Error $e) { echo $e->getMessage(), "\n"; }
}

readonly class R {}
$r = new R;
try { $r->p .= "x"; } catch (Error $e) { echo $e->getMessage(), "\n"; }

class Plain {}
$p = new Plain;
$p->x = 1;
unset($p->x);
$p->x = 2;
var_dump(get_object_vars($p));
