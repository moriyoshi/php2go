<?php
// debug_zval_dump names a typed property that holds nothing by its type, as
// var_dump does, and leaves out an untyped one that was unset.

class T {
    public int $a;
    protected ?array $b;
    private string $c;
    public readonly T $d;
    public $e;
    public int|string $f;
}

$t = new T;
debug_zval_dump($t);
$t->a = 5;
unset($t->e);
debug_zval_dump($t);
