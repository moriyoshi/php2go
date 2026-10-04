<?php
// A typed property held by reference refuses a write in the reference's
// words: `++` off the end of `int` says "a reference held by property", and a
// static bound to a typed static refuses a compound write through the binding
// and keeps its value.

class T {
    public int $n;
    public static int $i = 123;
    public static $u;
    public static int $j = PHP_INT_MAX;
}

$t = new T;
$t->n = PHP_INT_MAX;
try { $t->n++; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
$r =& $t->n;
try { ++$t->n; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
$t->n = PHP_INT_MIN;
try { $t->n--; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
var_dump($t->n === PHP_INT_MIN);

T::$u =& T::$i;
try { T::$u .= "foobar"; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
T::$u .= "4";
var_dump(T::$u, T::$i);

$s =& T::$j;
try { T::$j++; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
var_dump(T::$j === PHP_INT_MAX);
T::$i = 5;
var_dump(T::$u === 5, T::$i === 5, isset(T::$u));

// An untyped property bound to a typed one answers for it.
$a = new class { public int $foo = PHP_INT_MIN; public $_; };
$a->_ = &$a->foo;
try { $a->_--; } catch (Error $e) { echo $e->getMessage(), "\n"; }
echo gettype($a->foo), "\n";
