<?php
// The default Mt19937 engine, seeded: mt_rand, rand, shuffle, array_rand and
// str_shuffle all draw the engine's own sequence.

mt_srand(42);
var_dump(mt_rand(), mt_rand(1, 6), rand(), rand(10, 1), mt_rand(PHP_INT_MIN, PHP_INT_MAX));
$a = range(1, 10);
shuffle($a);
echo implode(",", $a), "\n";
var_dump(array_rand(['a' => 1, 'b' => 2, 'c' => 3]), array_rand(range(0, 9), 3));
echo str_shuffle("abcdefgh"), "\n";
mt_srand(42);
var_dump(mt_rand());
mt_srand(3, MT_RAND_PHP);
var_dump(mt_rand(1, 100), rand(1, 100));
try {
    mt_rand(5, 1);
} catch (ValueError $e) {
    echo $e->getMessage(), "\n";
}
srand(7);
$h = ['x' => 1, 'y' => 2, 'z' => 3, 'w' => 4];
unset($h['y']);
var_dump(array_rand($h, 2), mt_getrandmax());
