<?php
// Comparing structures that contain themselves is an Error, not an answer:
// the engine protects the left-hand container and raises when the walk
// meets it again (bug63882). Objects compared forever and crashed; arrays
// answered false.
class T { public $x = 5; }
function attempt($label, $f) {
    try {
        var_dump($f());
    } catch (Error $e) {
        echo "$label: ", get_class($e), ": ", $e->getMessage(), "\n";
    }
}
$a = new T; $b = new T; $a->x = $a; $b->x = $b;
attempt('==', fn() => $a == $b);
attempt('!=', fn() => $a != $b);
attempt('<', fn() => $a < $b);
attempt('<=>', fn() => $a <=> $b);
attempt('self', fn() => $a == $a);
$c = new T; $d = new T; $c->x = $d; $d->x = $c;
attempt('cross', fn() => $c == $d);
$e1 = new T; $e2 = new T; $e1->x = [$e1]; $e2->x = [$e2];
attempt('arr', fn() => $e1 == $e2);
$p = [1]; $p[] = &$p; $q = [1]; $q[] = &$q;
attempt('refarr', fn() => $p == $q);
attempt('refarr<', fn() => $p < $q);
attempt('in_array', fn() => in_array($a, [$b]));
// Not cycles: the same inner object twice, siblings, and equal trees.
$s = new T; $u = new T; $u->x = [$s, $s]; $v = new T; $v->x = [$s, $s];
attempt('shared', fn() => $u == $v);
$w1 = new T; $w1->x = new T; $w2 = new T; $w2->x = new T;
attempt('tree', fn() => $w1 == $w2);
attempt('tree<', fn() => $w1 < $w2);
$x = [1]; $y = [[$x, $x], $x]; $z = [[$x, $x], $x];
attempt('arrays', fn() => $y == $z);
attempt('arrays<=>', fn() => $y <=> $z);
