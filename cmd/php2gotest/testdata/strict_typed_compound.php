<?php
declare(strict_types=1);
// Under strict_types a compound assignment to a typed property is checked
// strictly, like a plain assignment: `int += 1.5` is a TypeError, not a
// truncation. The place a compound write goes through carries the file's mode.

class Box
{
    public int $n = 1;
    public float $f = 1.0;
    public static int $s = 1;
}

$b = new Box;
try {
    $b->n += 1.5;
} catch (TypeError $e) {
    echo $e->getMessage(), "\n";
}
var_dump($b->n);
$b->f += 1;
var_dump($b->f);
try {
    $b->n .= "2";
} catch (TypeError $e) {
    echo $e->getMessage(), "\n";
}
var_dump($b->n);
$arr = [$b];
try {
    $arr[0]->n *= 2.5;
} catch (TypeError $e) {
    echo $e->getMessage(), "\n";
}
var_dump($b->n);

// An engine class in a static callable is spelled as the engine declares it.
$cb = ['stdclass', 'missing'];
try {
    $cb();
} catch (Error $e) {
    echo get_class($e), ": ", $e->getMessage(), "\n";
}
$cb = 'ARRAYOBJECT::nope';
try {
    $cb();
} catch (Error $e) {
    echo get_class($e), ": ", $e->getMessage(), "\n";
}
try {
    call_user_func(['stdclass', 'missing']);
} catch (Error $e) {
    echo get_class($e), ": ", $e->getMessage(), "\n";
}
