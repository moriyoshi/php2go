<?php
// A declared type widens an int initializer to a float, and does so at every
// site that has a declaration: parameter, property, static property and class
// constant. It is not weak-mode coercion -- `float|string` widens too, while
// `int|float` does not, because the question is whether the type accepts float
// and not int.

function param(float $a = 0, ?float $b = 1, float|string $c = 2, int|float $d = 3)
{
    var_dump($a, $b, $c, $d);
}
param();
param(7, 8);

class Defaults
{
    public float $flt = 2;
    public ?float $nfl = 3;
    public float|bool $ufl = 4;
    public int|float $mix = 5;
    public int $int = 6;
    public static float $sflt = 7;
    public static int|float $smix = 8;

    const float F = 9;
    const int|float M = 10;
}

var_dump(new Defaults);
var_dump(Defaults::$sflt, Defaults::$smix);
var_dump(Defaults::F, Defaults::M);

// A float literal whose value is integral. The engine folds a typed class
// constant into the opline, so the constants above arrive here as doubles and
// this is the same question asked without a class -- but it is the commoner
// spelling, and it was wrong on its own: IR operand text rendered `9.0` as
// `9`, and every scanner of that text reads `9` as an int.
var_dump(9.0, -9.0, 0.0, 2.5, 1e21, 1.0 + 2.0, PHP_FLOAT_EPSILON > 0.0);

