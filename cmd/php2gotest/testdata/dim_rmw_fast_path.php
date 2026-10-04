<?php
// `$a[$i] op= v` and `$a[$i]++` keep the element's slot between the read and
// the write (OWNERSHIP-PERF.md). Each shape here is one where writing back to
// the remembered slot would be wrong: the element is a reference, the array is
// shared with another name, the key is missing, the container is not an array.
// (User code between the read and the write -- a __toString that unsets or
// grows the array -- is not here: it corrupts the engine's own heap, so the
// oracle cannot answer it.)

// A reference in the element: the write goes through it.
$a = [1, 2, 3];
$r = &$a[1];
for ($i = 0; $i < 3; $i++) {
    $a[$i] += 10;
}
var_dump($a, $r);
unset($r);

// A shared array: the write separates, the other name keeps its copy.
$b = [1, 2, 3];
$c = $b;
for ($i = 0; $i < 3; $i++) {
    $b[$i] *= 2;
}
var_dump($b, $c);

// A missing key warns and creates; a float key converts.
$d = [5];
$d[3] += 1;
$d[0.0] -= 1;
$d[1]++;
var_dump($d);

// Not an array: ArrayAccess and a string offset.
class Box implements ArrayAccess
{
    private $d = [0, 0];
    public function offsetExists($o): bool { return isset($this->d[$o]); }
    public function offsetGet($o): mixed { echo "get $o\n"; return $this->d[$o]; }
    public function offsetSet($o, $v): void { echo "set $o\n"; $this->d[$o] = $v; }
    public function offsetUnset($o): void { unset($this->d[$o]); }
}
$box = new Box();
$box[1] += 5;
var_dump($box[1]);
$s = "abc";
try {
    $s[0] .= "z";
} catch (Error $err) {
    echo get_class($err), ": ", $err->getMessage(), "\n";
}

// A packed list written in a loop, the shape the fast path is for.
$sum = array_fill(0, 5, 0);
for ($k = 0; $k < 3; $k++) {
    for ($i = 4; $i >= 0; $i--) {
        $sum[$i] += $i + $k;
    }
}
var_dump($sum);
