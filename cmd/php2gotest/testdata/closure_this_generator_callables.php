<?php
// `$this` in a closure with no object bound is an Error, not NULL; isset
// answers false in silence.
class A {
    public $x = 3;
    function st() { return static function() { return $this->x; }; }
    function st2() { return static function() { return isset($this); }; }
    function st3() { return static function() { return $this; }; }
    function st4() { return static function() { $this->x = 5; }; }
    function st5() { return static function() { return $this->m(); }; }
    function st6() { return static function() { return isset($this->x); }; }
    function st7() { return static function() { return empty($this->x); }; }
    function st8() { return static function() { return strlen($this); }; }
    function st9() { return static function() { return $this::class; }; }
    function st10() { return static function() { return $this instanceof A; }; }
    function k() { return function() { return $this->x; }; }
    function m() { return 1; }
}
$a = new A;
foreach (['st', 'st2', 'st3', 'st4', 'st5', 'st6', 'st7', 'st8', 'st9', 'st10', 'k'] as $m) {
    try { var_dump(($a->$m())()); } catch (Error $e) { echo $m, ": ", get_class($e), ": ", $e->getMessage(), " @", $e->getLine(), "\n"; }
}
$u = function() { return $this?->x; };
try { var_dump($u()); } catch (Error $e) { echo $e->getMessage(), "\n"; }
$c = Closure::bind(fn() => $this->x, null, A::class);
try { var_dump($c()); } catch (Error $e) { echo $e->getMessage(), "\n"; }
$d = Closure::bind(fn() => $this->x, new A, A::class);
var_dump($d());

// A Generator's and a Fiber's methods are real methods: first-class
// callables, fromCallable and method_exists see them.
function g() { $x = yield 1; echo "got $x\n"; yield 2; return 9; }
$gen = g();
$cur = $gen->current(...);
var_dump($cur());
$n = $gen->next(...);
$n();
var_dump($gen->current());
$s = $gen->send(...);
var_dump($s("A"));
$v = Closure::fromCallable([$gen, 'valid']);
var_dump($v());
$gen->next();
$r = $gen->getReturn(...);
var_dump($r());
var_dump(call_user_func([$gen, 'valid']));
var_dump(method_exists($gen, 'send'), method_exists('Generator', 'getReturn'), method_exists($gen, 'nope'));
$f = new Fiber(function () { return 1; });
$st = $f->start(...);
var_dump($st());
var_dump(is_callable([$gen, 'next']), is_callable([$gen, 'nope']));
try { $bad = $gen->nope(...); } catch (Error $e) { echo $e->getMessage(), "\n"; }
