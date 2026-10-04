<?php
// `__get` and `offsetGet` reached as **storage** are the same shape as a
// property hook reached that way, with one difference that matters: a hook
// **refuses**, and an overloaded access only **notices**. The write then lands
// on a copy, so the object is untouched.

function push(array &$a): void { $a[] = "pushed"; }

class Overloaded {
    private $store = ["a"];
    public function __get($n) { echo "  __get($n)\n"; return $this->store; }
    public function __set($n, $v) { echo "  __set($n)\n"; }
}
$o = new Overloaded;
echo "append:\n";
$o->absent[] = "appended";
echo "bind:\n";
$bound = &$o->absent;
var_dump($bound);
echo "by-ref argument:\n";
push($o->absent);
echo "untouched:\n";
var_dump($o);

// A `&__get` hands back real storage, and there is nothing to notice.
class ByRefGet {
    private $store = ["a"];
    public function &__get($n) { return $this->store; }
}
$byRef = new ByRefGet;
$q = &$byRef->absent;
$q[] = "bound";
$byRef->absent[] = "appended";
push($byRef->absent);
var_dump($byRef);

// An **object** is written through the handle, which modifies no property, so
// that is silent too.
class Inner implements ArrayAccess {
    public $seen = [];
    public function offsetExists($k): bool { return true; }
    public function offsetGet($k): mixed { return null; }
    public function offsetSet($k, $v): void { echo "  Inner::offsetSet\n"; $this->seen[] = $v; }
    public function offsetUnset($k): void {}
}
class HoldsObject {
    private $inner;
    public function __get($n) { return $this->inner ??= new Inner; }
}
$holds = new HoldsObject;
$holds->anything[] = "through the handle";
var_dump($holds->anything->seen);

// The same three shapes on an ArrayAccess, with the wording that names the
// **class** rather than the offset.
class Storage implements ArrayAccess {
    private $d = ["k" => ["a"]];
    public function offsetExists($k): bool { return isset($this->d[$k]); }
    public function offsetGet($k): mixed { echo "  offsetGet($k)\n"; return $this->d[$k]; }
    public function offsetSet($k, $v): void { echo "  offsetSet\n"; $this->d[$k] = $v; }
    public function offsetUnset($k): void {}
}
$storage = new Storage;
echo "element append:\n";
$storage["k"][] = "appended";
echo "element bind:\n";
$elem = &$storage["k"];
var_dump($elem);
echo "element by-ref argument:\n";
push($storage["k"]);
echo "untouched:\n";
var_dump($storage);

// An `&offsetGet` reaches the real element.
class RefStorage implements ArrayAccess {
    private $d = ["k" => ["a"]];
    public function offsetExists($k): bool { return true; }
    public function &offsetGet($k): mixed { return $this->d[$k]; }
    public function offsetSet($k, $v): void {}
    public function offsetUnset($k): void {}
}
$refStorage = new RefStorage;
$refStorage["k"][] = "appended";
var_dump($refStorage);
