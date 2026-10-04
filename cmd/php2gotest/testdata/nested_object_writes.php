<?php
// The receiver of a property write is itself a write target.
class P { public $v = 0; public $q; }
$a = [new P];
$a[0]->v = 7;
var_dump($a[0]->v);
// The object is shared, so the write lands on the one the array holds.
$o = new P;
$b = ["x" => $o];
$b["x"]->v = 9;
var_dump($o->v);
// A missing key reads silently, and the refusal comes from the write.
$c = [];
try { $c["missing"]->v = 1; } catch (\Error $e) { echo $e->getMessage(), "\n"; }
// Two levels deep.
$o->q = new P;
$d = ["y" => $o];
$d["y"]->q->v = 3;
var_dump($o->q->v);
// A property whose value is an object, reached through a property place.
class Holder { public $inner; }
$h = new Holder;
$h->inner = new P;
$e2 = [$h];
$e2[0]->inner->v = 42;
var_dump($h->inner->v);
echo "done\n";
