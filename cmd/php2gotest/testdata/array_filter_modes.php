<?php
// array_filter keeps what the callback accepts, with its key: handed the
// value by default, the key under ARRAY_FILTER_USE_KEY, both under
// ARRAY_FILTER_USE_BOTH; with no callback, what is truthy.

$a = ["a" => 1, "b" => 0, 3 => "x", 4 => "", 5 => null, "c" => [0]];
var_dump(array_filter($a));
var_dump(array_filter($a, fn($v) => is_int($v)));
var_dump(array_filter($a, fn($k) => is_string($k), ARRAY_FILTER_USE_KEY));
var_dump(array_filter($a, fn($v, $k) => $k === 3 || $v === 1, ARRAY_FILTER_USE_BOTH));
var_dump(array_filter([1, 2, 3, 4], 'is_odd'));
function is_odd($n) { return $n % 2; }
try {
    array_filter([1], function () { throw new Exception("stop"); });
} catch (Exception $e) {
    echo $e->getMessage(), "\n";
}
