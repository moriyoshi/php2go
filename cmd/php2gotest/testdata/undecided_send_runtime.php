<?php
// An argument whose send mode the call site cannot settle is settled by
// the callee the call reaches: a by-value parameter reads the variable,
// warning when it is unset; a by-reference one binds it.

// Engine objects: Generator's and Fiber's methods take every argument by
// value.
function gen() {
    $g = yield;
    try { $g->send($unsetInGen); } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
    try { $g->nope($neverRead); } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
    try { $g->throw($unsetThrow); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
}
$gen = gen();
$gen->send($gen);
$f = new Fiber(function ($a) { var_dump($a); $b = Fiber::suspend(1); var_dump($b); });
$f->start($unsetStart);
$f->resume($unsetResume);
$c = function (&$r) { $r = 5; };
$c->__invoke($boundByInvoke);
var_dump($boundByInvoke);

// Functions declared by a statement: the declaration that ran decides.
$arr = [];
try { later($arr['k'], $nope); } catch (Error $e) { echo $e->getMessage(), "\n"; }
var_dump($arr);
if (true) {
    function later(&$a, $b) { $a = 'set'; var_dump($b); }
    function byval($a, $b = 2) { var_dump($a); }
    function vr($a, &...$rest) { foreach ($rest as &$r) { $r = 'v'; } }
}
later($arr['k'], $undef1);
var_dump($arr);
byval($undef2);
byval($arr['missing']);
$s = 'abc';
try { later($s[0], 1); } catch (Error $e) { echo $e->getMessage(), "\n"; }
vr(1, $x1, $arr['q']);
var_dump($x1, $arr);
later(b: 5, a: $named);
var_dump($named);

eval('function fromEval(&$a) { $a[] = "e"; }');
$list = ['x' => []];
fromEval($list['x']);
fromEval($list['y']);
var_dump($list);
try { fromEval(chr(65)[0]); } catch (Error $e) { echo $e->getMessage(), "\n"; }
