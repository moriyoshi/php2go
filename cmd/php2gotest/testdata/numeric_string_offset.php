<?php
// A numeric-string offset written as a literal reaches an ArrayAccess object
// as the string it was written as -- the compiler folds it to an int for
// arrays and keeps the original beside it -- while an array, and a string,
// still see the integer key.

class Probe implements ArrayAccess
{
    public function offsetExists($o): bool { echo "exists "; var_dump($o); return true; }
    public function offsetGet($o): mixed { echo "get "; var_dump($o); return null; }
    public function offsetSet($o, $v): void { echo "set "; var_dump($o); }
    public function offsetUnset($o): void { echo "unset "; var_dump($o); }
}

$p = new Probe;
isset($p['0']);
unset($p['123']);
$p['0'] = 1;
$x = $p['42'];
$p['0'] .= "x";
$p['07'] = 1;
$p[5] = 1;

$a = [];
$a['0'] = "zero";
$a['12'] = "twelve";
$a['012'] = "leading";
var_dump($a, isset($a['12']), $a[0], array_key_exists('0', $a));
unset($a['0']);
var_dump(array_keys($a));
$s = "abc";
var_dump($s['1']);
['1' => $second] = ['a', 'b'];
var_dump($second);
