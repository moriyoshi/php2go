<?php
// unserialize of a class it cannot find makes an __PHP_Incomplete_Class
// object (bug71841): it shows and serializes as what was read, and its
// property and method handlers refuse -- a read warns and is NULL, a write
// or a method call is an Error naming the missing class.
function msg($e) { return get_class($e) . ": " . substr($e->getMessage(), 0, 60); }
$z = unserialize("O:1:\"A\":2:{s:1:\"a\";i:1;s:4:\"\0*\0p\";i:2;}");
var_dump($z);
print_r($z); echo "\n";
var_export($z); echo "\n";
var_dump(get_class($z), is_object($z), gettype($z), $z instanceof __PHP_Incomplete_Class, $z instanceof stdClass);
var_dump(str_replace("\0", '~', serialize($z)));
var_dump(str_replace("\0", '~', serialize([$z, $z])));
var_dump(array_map(fn($k) => str_replace("\0", '~', $k), array_keys((array)$z)));
var_dump($z->a);
var_dump($z->nope);
var_dump(isset($z->a), empty($z->a), $z->a ?? 'dflt');
foreach (['write' => function () use ($z) { $z->a = 5; },
          'compound' => function () use ($z) { $z->e .= 0; },
          'pre' => function () use ($z) { ++$z->x; },
          'post' => function () use ($z) { $z->y++; },
          'dim' => function () use ($z) { $z->arr[] = 1; },
          'ref' => function () use ($z) { $r = &$z->a; },
          'unset' => function () use ($z) { unset($z->a); },
          'call' => function () use ($z) { $z->m(); },
          'method_exists' => fn() => method_exists($z, 'm'),
          'is_callable' => fn() => is_callable([$z, 'm'])] as $label => $f) {
    try { $f(); echo "$label: no error\n"; } catch (Error $e) { echo "$label: ", msg($e), "\n"; }
}
var_dump(is_callable($z), is_callable([$z, 'm'], true));
var_dump(property_exists($z, 'a'), property_exists('__PHP_Incomplete_Class', 'a'));
var_dump(method_exists('__PHP_Incomplete_Class', 'm'), get_class_methods($z));
function reader($o) { return $o->a; }
class Reader { function read($o) { return $o->a; } static function sread($o) { return $o->a; } }
var_dump(reader($z), (new Reader)->read($z), Reader::sread($z));
// Unchanged by all of it.
var_dump(str_replace("\0", '~', serialize($z)));

// With its own class name the object has no name to stand for.
$w = unserialize('O:22:"__PHP_Incomplete_Class":1:{s:1:"q";i:3;}');
print_r($w); echo "\n";
var_dump(serialize($w));
$v = unserialize('O:22:"__PHP_Incomplete_Class":1:{s:27:"__PHP_Incomplete_Class_Name";s:1:"Z";}');
print_r($v); echo "\n";
var_dump(serialize($v));
$n = new __PHP_Incomplete_Class;
var_dump($n->a);
try { $n->a = 1; } catch (Error $e) { echo msg($e), "\n"; }
print_r($n); echo "\n";
var_dump(serialize($n));

// The autoloaders are asked first; one that makes the class wins.
class Real { public $a; }
spl_autoload_register(function ($c) {
    echo "autoload($c)\n";
    if ($c === 'Aliased') class_alias('Real', 'Aliased');
});
print_r(unserialize('O:7:"Aliased":1:{s:1:"a";i:7;}')); echo "\n";
print_r(unserialize('O:7:"Missing":1:{s:1:"a";i:8;}')); echo "\n";
print_r(unserialize('O:1:"1":0:{}')); echo "\n";
