<?php
// An anonymous class is an ordinary class under a mangled name.
$o = new class { public $x = 1; public function m() { return "anon"; } };
var_dump($o->x, $o->m());
var_dump($o instanceof stdClass);

// Two in one file must not collide.
$a = new class { public $tag = "a"; };
$b = new class { public $tag = "b"; };
var_dump($a->tag, $b->tag);
var_dump(get_class($a) === get_class($b));

// An anonymous class can extend and implement.
class Base { public function who() { return "base"; } }
$c = new class extends Base { public function who() { return "anon-" . parent::who(); } };
echo $c->who(), "\n";

// A temporary passed where by-ref-ness is unsettled goes by value.
function make() { return [3, 1, 2]; }
var_dump(count(make()));
var_dump(make());

// `parent::m()` on an *instance* method is a static-syntax call that keeps
// $this and bypasses virtual dispatch. Named classes, not just anonymous ones.
class P { public function tag() { return "P"; } }
class Q extends P { public function tag() { return "Q>" . parent::tag(); } }
echo (new Q())->tag(), "\n";
