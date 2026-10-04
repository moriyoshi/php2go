<?php
// `global $$n` binds the local to the **global** its name spells, evaluating
// the name once, and a write through it is the global's.

function sf($arg) { echo "called\n"; return $arg; }
function viaCall() { global ${sf("a")}; var_dump($a); $a = "changed"; }
function viaVar() { $n = "b"; global $$n; $b++; }
$a = 42;
$b = 1;
viaCall();
viaVar();
var_dump($a, $b);
