<?php
function build(string $a, string $b, array $rows): string
{
    // A plain binary concat: two values, neither of them a buffer.
    $plain = $a . $b;

    // An interpolation: the compiler builds it through a rope, whose slot it
    // invented and which no expression in this function can name.
    $interp = "x{$a}y{$b}z";

    // An array literal, the same shape for arrays.
    $list = [$a, $b, 'c'];

    return $plain . $interp . count($list) . count($rows);
}
