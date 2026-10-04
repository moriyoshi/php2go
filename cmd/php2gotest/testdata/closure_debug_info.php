<?php
// `var_dump` of a Closure shows one of **two** layouts, and which one depends
// on how the closure was made. A literal has name/file/line; one made from
// something already named has `function` instead, with no file or line at all.
// Each is bound to a variable rather than dumped as a temporary, so this tests
// the layout and not object-handle reuse.
function g($a, $b = 1) {}
function noargs() {}
class K {
    public function m($z, $w = 2) {}
    public static function s() {}
    public function __invoke($q) {}
}

$literal = function () {};
$literalArgs = function ($x, $y = 2) {};
$literalUse = function () use (&$literal) {};
var_dump($literal);
var_dump($literalArgs);
var_dump($literalUse);

$fromName = Closure::fromCallable("g");
var_dump($fromName);

$fromNoArgs = Closure::fromCallable("noargs");
var_dump($fromNoArgs);

$k = new K();
$fromMethod = $k->m(...);
var_dump($fromMethod);

$fromStatic = K::s(...);
var_dump($fromStatic);

$fromBuiltin = strlen(...);
var_dump($fromBuiltin);

$fromArray = Closure::fromCallable([$k, "m"]);
var_dump($fromArray);

// `static` is one merged array: `use` captures first, then the closure's own
// static variables, with whatever value they hold *now*. Zend keeps both in one
// table, so what separates them is that a capture is bound -- a by-reference
// capture that lost to its own fallback slot printed `&NULL`.
$cap = 7;
$both = function () use ($cap) { static $sv = 1; };
var_dump($both);

$onlyStatic = function () { static $a = 2, $b; };
var_dump($onlyStatic);

$ran = function () { static $n = 0; $n++; };
$ran();
$ran();
var_dump($ran);

// A named callable shows the *callee's* statics, not captures of its own.
function counted() { static $calls = 0; $calls++; }
counted();
$countedRef = counted(...);
var_dump($countedRef);
