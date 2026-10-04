<?php
function f($a, ...$rest) { var_dump($a, $rest); }
f(1);
f(1, 2, 3);
function g(int ...$ns) { var_dump($ns); }
g(1, 2);
try { g(1, "x"); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
function h(...$all) { return count($all); }
var_dump(h(1,2,3));
function m(string $s, float ...$fs) { var_dump($s, $fs); }
m("a", 1, 2.5);
