<?php
// An ArrayObject's dimension handler hands out the stored element itself, so a
// nested write lands where an ordinary ArrayAccess earns "Indirect
// modification". A subclass with its own offsetGet is an ordinary ArrayAccess.
$ao = new ArrayObject(['a' => [1]]);
$ao['a'][] = 2;
$ao['x'][] = 3;          // created silently
$ao['y']['z'] = 4;
$ao[][] = 5;             // the notice, nothing written
$r = &$ao['a'];          // binds the element: &array
$r[] = 9;
$m = &$ao['m'];          // a missing one is created as null
var_dump(count($ao), $m);
$ao['y']['w'] .= 'a';
$ao['n']['m'] ??= 'b';
$ao[1.5][] = 1;
$ao[true][] = 2;
$ao[null][] = 3;
var_dump($ao);

$ai = new ArrayIterator([]);
$ai['q'][] = 1;
var_dump($ai);

class S extends ArrayObject {}
$s = new S;
$s['k'][] = 1;
var_dump($s);

class G extends ArrayObject {
    function offsetGet($k): mixed { echo "get $k\n"; return parent::offsetGet($k); }
}
$g = new G(['k' => []]);
$g['k'][] = 1;
var_dump($g);

foreach ([[], new stdClass] as $k) {
    try { $ao[$k][] = 1; } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
}
try { $s[[]][] = 1; } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }

// A readonly property holding one: the object is written, not the property.
class Holder {
    public function __construct(public readonly object $prop) {}
}
$h = new Holder(new ArrayObject());
$h->prop[] = [];
$h->prop[0][] = 1;
var_dump($h->prop->getArrayCopy());
