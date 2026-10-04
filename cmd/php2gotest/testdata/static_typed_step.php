<?php
// `++` and `--` running an `int` static off the end of the range fail with
// their own wording and leave the value alone, as for an instance property;
// a compound assignment is the plain refused write, and a type admitting
// float widens instead.

class Base
{
    public static int $max = PHP_INT_MAX;
    public static ?int $min = PHP_INT_MIN;
    public static int|float $wide = PHP_INT_MAX;
}
class Child extends Base { }

foreach ([
    fn() => Base::$max++,
    fn() => ++Child::$max,
    fn() => Base::$min--,
    fn() => Base::$max += 1,
] as $step) {
    try {
        $step();
    } catch (TypeError $e) {
        echo $e->getMessage(), "\n";
    }
}
var_dump(Base::$max, Base::$min);
Base::$wide++;
var_dump(Base::$wide);
