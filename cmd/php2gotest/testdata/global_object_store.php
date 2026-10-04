<?php
// A `global` or `static` local is a second name for storage that lives
// elsewhere, so an assignment must write *through* the binding. Narrowing the
// place on what is stored into it hid that: only a value with a narrower
// representation than `zval` -- an object, an array, a string -- took the
// wrong path, so a scalar store always worked and this stayed invisible.
$k = null;
$arr = null;
$str = null;

class C {
    public $tag;
    public function __construct($tag) { $this->tag = $tag; }
    public function toGlobalObject() { global $k; $k = $this; }
    public function toGlobalArray() { global $arr; $arr = [1, 2]; }
    public function toGlobalString() { global $str; $str = "hello" . "!"; }
    public function counter() { static $seen; $seen = $this; return $seen->tag; }
}

$c = new C("first");
$c->toGlobalObject();
var_dump($k === null ? "null" : get_class($k) . ":" . $k->tag);
$c->toGlobalArray();
var_dump($arr);
$c->toGlobalString();
var_dump($str);
var_dump($c->counter());

function viaFunction() { global $k; $k = new C("second"); }
viaFunction();
var_dump($k->tag);

// A scalar through the same path, which always worked.
function scalarGlobal() { global $n; $n = 7; }
scalarGlobal();
var_dump($n);

// The same hazard through `$b = &$a`: both names share one box, so neither may
// be narrowed on what is written into it. Writing an *object* through either
// side is the case that breaks when one is.
$p = null;
$q = &$p;
$q = new C("through alias");
var_dump($p->tag);

$r = null;
$t = &$r;
$r = new C("through target");
var_dump($t->tag);

$u = null;
$v = &$u;
$v = "join" . "ed";
var_dump($u);

$w = null;
$y = &$w;
$y = [1, 2];
var_dump($w);
