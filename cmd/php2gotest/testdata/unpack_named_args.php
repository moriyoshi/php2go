<?php
// A **string key in an unpacked array is a named argument**, which is what the
// engine does with it: `f(...["b" => 20])` binds `$b`. It used to be refused
// rather than bound by position -- binding by position would have been a wrong
// answer, so the refusal was right, but the machinery to do it properly was
// already there: the same `argNames` channel an explicitly written `f(b: 20)`
// uses.
//
// Reading that list back needed a getter. Without one the two sources of names
// -- an unpack and a written named argument beside it -- silently overwrote
// each other.
function f($a = 1, $b = 2, $c = 3) { var_dump($a, $b, $c); }
// Unpack after a positional.
f(5, ...["c" => 30]);
// Variadic collecting named keys.
function v($a = 0, ...$rest) { var_dump($a, $rest); }
v(...["a" => 1, "x" => 9]);
// A method and a builtin.
class K { public function m($p = 1, $q = 2) { var_dump($p, $q); } }
(new K)->m(...["q" => 7]);
var_dump(str_pad(...["string" => "x", "length" => 3, "pad_string" => "-"]));
// Positional after named inside one unpacked array is an Error.
try { f(...["c" => 3, 0 => 9]); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }

// The plain forms still work.
f(...["b" => 20]);
f(...[]);
f(...[7, 8, 9]);

// **An unpack and a written named argument together.** The known-callee path
// places names into positions at emit time and cannot, because which positions
// the unpacked elements leave free is a run-time fact -- so a call mixing the
// two falls back to the trailing-names strategy the unknown-callee path already
// used, and the callee's prologue permutes.
//
// Only for a *compiled* callee: a library function has no prologue of ours to
// permute anything, which is why that path places names at emit time at all.
//
// The order is the part that bites. Names must line up with the trailing values
// in the same order, and the unpack contributes its names at run time after
// everything written before it -- so the emitter's names are **appended** here
// rather than prepended. Prepending gave `f(...["b" => 20], c: 30)` the names
// `c, b` against the values `20, 30`, and each bound the other's.
function g($a = 1, $b = 2, $c = 3, $d = 4) { var_dump($a, $b, $c, $d); }
g(...["b" => 20], c: 30);
g(...["b" => 20, "d" => 40], a: 10, c: 30);
g(...["a" => 10], d: 40);
g(...[10, 20], d: 40);
g(...[5], c: 30);
g(...[], a: 7, b: 8);

class K2 { public function m($p = 1, $q = 2, $r = 3) { var_dump($p, $q, $r); } }
(new K2)->m(...[1], r: 9);

function vv(...$rest) { var_dump($rest); }
vv(...["x" => 1], y: 2);

// And with no unpack at all, unchanged.
g(a: 1, c: 3);
g(1, 2, 3, 4);

// A **Traversable** unpacks the same way, and its string keys are names too.
// This branch had its own copy of the refusal and needed the same treatment --
// the first fix reached only the array case, and the corpus files that were
// still refused are what said so.
function keyed() { yield "b" => 20; yield "c" => 30; }
g(...keyed());
function mixed() { yield 10; yield "c" => 30; }
g(...mixed());
