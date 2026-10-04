<?php
// `global` binds BY REFERENCE: the write inside the function is the global's.
$g = 1;
function f() { global $g; $g = 2; var_dump($g); }
f();
var_dump($g);
// A global the file body never assigns is created by the function.
function h() { global $undefined; var_dump($undefined); $undefined = 7; }
h();
var_dump($undefined);
// `unset` drops the local binding only; the global keeps what it had.
function k() { global $g; unset($g); $g = 5; var_dump($g); }
k();
var_dump($g);
// Two functions see the same storage.
function setIt() { global $shared; $shared = "x"; }
function getIt() { global $shared; return $shared; }
setIt();
var_dump(getIt(), $shared);
// An array global written through the binding.
$arr = [1];
function push() { global $arr; $arr[] = 2; }
push();
var_dump($arr);
echo "done\n";
