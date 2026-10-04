<?php
// Closure::bindTo() and Closure::bind() take `?object $newThis` and
// `object|string|null $newScope`, checked in that order and numbered from the
// method's own first argument; a scalar scope becomes its string, which then
// names no class.
class A {}
$f = function () { return get_class($this); };
foreach ([[new A, []], [[], 'A'], [5, 'A'], [new A, 5], [new A, 1.5]] as [$t, $s]) {
    try { $g = $f->bindTo($t, $s); var_dump($g === null); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
    try { $g = Closure::bind($f, $t, $s); var_dump($g === null); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
}
