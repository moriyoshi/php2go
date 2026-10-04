<?php
// `$o[] op= v` on an ArrayAccess reads the null offset first -- offsetGet(NULL)
// -- and writes the combined value back with offsetSet(NULL, ...); the
// expression is the combined value.

class C10 implements ArrayAccess {
    public $store = [];
    function offsetExists($o): bool { echo "exists\n"; return false; }
    function offsetGet($o): mixed { echo "get "; var_dump($o); return 100; }
    function offsetSet($o, $v): void { echo "set "; var_dump($o, $v); $this->store[] = $v; }
    function offsetUnset($o): void { echo "unset\n"; }
}
$c = new C10;
var_dump($c[] += 5);
var_dump($c[] .= "x");
$c[] = 1;
var_dump($c->store);
