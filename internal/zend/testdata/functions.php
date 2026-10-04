<?php
function greet(string $name, int $times = 1): string {
    return str_repeat($name, $times);
}
function tally(array &$acc, int ...$rest): void {
    $acc[] = $rest;
}
$double = fn(int $x): int => $x * 2;
$sum = function (int $a, int $b) { return $a + $b; };
