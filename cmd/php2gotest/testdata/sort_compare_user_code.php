<?php
// A sort compares an object with a string through its __toString, which is
// user code. When that code writes to the array being sorted, the engine
// separates it: the variable keeps the write, in its old order, and the sort's
// result is dropped (gh16648).
class Grow {
    function __toString() {
        global $arr;
        $arr[] = 'g';
        return 'm';
    }
}
$arr = ['x' => 'z', 'y' => new Grow, 'w' => 'a'];
asort($arr);
var_dump(array_keys($arr), count($arr));

$arr = ['z', new Grow, 'a'];
sort($arr);
var_dump(count($arr), $arr[0], $arr[3] ?? null);

// Reading the array from __toString is not a write: the sort stands.
class Look {
    function __toString() {
        global $seen;
        $seen = count($GLOBALS['arr']);
        return 'm';
    }
}
$arr = ['x' => 'z', 'y' => new Look, 'w' => 'a'];
asort($arr);
var_dump(array_keys($arr), $seen);
$arr = ['z', new Look, 'a'];
rsort($arr);
var_dump($arr[0], $arr[2]);
$arr = ['b' => 1, 'a' => 2];
ksort($arr);
var_dump(array_keys($arr));

// A throwing __toString propagates, and the array is still reordered.
class Boom {
    function __toString(): string { throw new Exception('t'); }
}
$arr = ['x' => 'b', 'y' => new Boom, 'z' => 'a'];
try { asort($arr); } catch (Exception $e) { echo 'caught ', $e->getMessage(), "\n"; }
var_dump(array_keys($arr));
$arr = ['b', new Boom, 'a'];
try { sort($arr); } catch (Exception $e) { echo 'caught ', $e->getMessage(), "\n"; }
var_dump(count($arr), $arr[0]);
