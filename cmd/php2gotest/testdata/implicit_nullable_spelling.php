<?php
// A `= null` default makes a parameter nullable, and the TypeError spells
// the type as the engine does: a plain type gains a `?`, while an
// intersection or a union already written with `null` is not prefixed again.

interface X {}
interface Y {}

function plain(int $a = null) { var_dump($a); }
function inter(X&Y $a = null) { var_dump($a); }
function uni(int|string $a = null) { var_dump($a); }

foreach (['plain', 'inter', 'uni'] as $f) {
    try { @$f(null); @$f([]); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
}
