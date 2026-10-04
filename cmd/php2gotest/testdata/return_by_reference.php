<?php
// `function &f()` hands back the **box**, so `$r = &f()` binds to the storage
// the body named. Every kind of slot a body can name has to survive that.

$global = 1;
function &fromGlobal() { global $global; return $global; }
$g = &fromGlobal();
$g = 3;
var_dump($global);

function &fromStatic() { static $s = 1; return $s; }
$a = &fromStatic();
var_dump($a);
$a = 9;
var_dump(fromStatic());

function &fromParam(&$x) { return $x; }
$z = 1;
$c = &fromParam($z);
$c = 7;
var_dump($z);

class Holder {
    public $p = 1;
    public $arr = [1];
    public function &prop() { return $this->p; }
    public function &element() { return $this->arr[0]; }
}
$h = new Holder;
$b = &$h->prop();
$b = 5;
var_dump($h->p);
$e = &$h->element();
$e = 6;
var_dump($h->arr);

// A reference to a slot that does not exist yet **creates** it, in silence --
// taking the address is not a read.
function &fromNothing() { return $never; }
$n = &fromNothing();
var_dump($n);

// A top-level reference dumps as what it holds, with no `&`. The marker
// belongs to a container's element, and that is a different question.
$v = 1;
$ref = &$v;
var_dump($ref);
$list = [1, 2];
$first = &$list[0];
var_dump($list);

// **By value, the box does not travel.** Only `=&` binds; every other use of a
// by-reference return works on what the box holds.
function &alias(&$of) { return $of; }
$src = "123";
$byValue = alias($src);
var_dump($byValue);
$into = [];
$into[] = alias($src);
var_dump($into);
class Typed { public int $p; }
$typed = new Typed;
$typed->p = alias($src);
var_dump($typed->p);
var_dump(alias($src) . "!");

// `++` steps through a box the same way it steps a plain slot.
$count = 1;
$stepped = alias($count);
$stepped++;
var_dump($stepped, $count);
$shared = &$count;
$shared++;
var_dump($shared, $count);

// **A `&` binding a frame made ends when the frame does.** Until then the
// storage is marked as program-referenced and var_dump prints `&` for it.
class Marked { public $v = 1; }
$marked = new Marked;
function binds(Marked $m): void { $inside = &$m->v; $inside = 2; }
binds($marked);
var_dump($marked);

// A by-reference **parameter** holds a binding the *caller* made, so the callee
// returning must not drop it.
$outer = 1;
$callerBinding = &$outer;
function takes(&$p): void { $p = 5; }
takes($callerBinding);
$holder = ["e" => 1];
$element = &$holder["e"];
takes($element);
var_dump($holder);
