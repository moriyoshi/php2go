<?php
// `foreach ($a as &$v)` binds the loop variable to the **element**, so a write
// through it is a write into the array. It also follows the array as the body
// mutates it, because the engine holds a bucket position rather than a list of
// keys taken at the start -- which a by-value loop, iterating a copy, does not.

$doubled = [1, 2, 3];
foreach ($doubled as &$each) { $each *= 2; }
unset($each);
var_dump($doubled);

$keyed = ["p" => 1, "q" => 2];
foreach ($keyed as $k => &$v) { $v = "$k!"; }
unset($v);
var_dump($keyed);

// An element appended inside the loop **is** reached.
$grows = [1, 2, 3];
foreach ($grows as $k => &$g) { $g *= 2; if ($k === 0) { $grows[] = 99; } }
unset($g);
var_dump($grows);

// An element unset inside the loop is skipped.
$shrinks = [1, 2, 3];
foreach ($shrinks as $k => &$s) { if ($k === 0) { unset($shrinks[1]); } $s = "x$k"; }
unset($s);
var_dump($shrinks);

// A by-value loop sees neither: it iterates a copy.
$copy = [1, 2, 3];
foreach ($copy as $k => $c) { if ($k === 0) { $copy[] = 4; } echo "$k=$c "; }
echo "\n";
var_dump(count($copy));

// Only the element the loop variable is **on** carries var_dump's `&`; moving
// to the next one drops the previous binding.
$marked = [1, 2, 3];
foreach ($marked as &$m) {}
var_dump($marked);
unset($m);
var_dump($marked);

// Nested arrays, written through.
$nested = [["a" => 1], ["a" => 2]];
foreach ($nested as &$row) { $row["a"] *= 10; $row["b"] = "added"; }
unset($row);
var_dump($nested);

// A by-reference loop over a generator has no element to point at, so it
// behaves as a by-value one rather than being refused.
function gen() { yield 1; yield 2; }
foreach (gen() as &$y) { echo $y, " "; }
echo "\n";
