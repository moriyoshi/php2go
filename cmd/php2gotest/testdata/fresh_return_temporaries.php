<?php
// An object a call **hands back** is a statement temporary when the caller
// keeps it nowhere: PHP frees it at the end of the statement, so the next call
// takes the same handle. `var_dump(Foo::make())` is `var_dump(new Foo)` as far
// as lifetime goes -- the only difference is that a `new` is visible at the
// call site and a call's result is not, until the callee has been summarised.
class Foo {
    public static function make() { return new Foo; }
    public function copy() { return new Foo; }
    // **A declared return type inserts a check between the value and the
    // `ret`.** For an object the check hands the same object on -- the only
    // coercion it can perform is int-to-float widening -- but the returned
    // value's definition is then the check, not the `new`, so a summary that
    // does not see through it refuses every typed factory. Which is most of
    // them.
    public static function typed(): self { return new Foo; }
    public static function named(): Foo { return new Foo; }
}
function mk() { return new Foo; }
function mkTyped(): Foo { return new Foo; }

// Repeated calls reuse one handle.
var_dump(spl_object_id(Foo::make()));
var_dump(spl_object_id(Foo::make()));
var_dump(spl_object_id(mk()));
var_dump(spl_object_id(Foo::typed()));
var_dump(spl_object_id(Foo::typed()));
var_dump(spl_object_id(Foo::named()));
var_dump(spl_object_id(mkTyped()));
$f = new Foo;
var_dump(spl_object_id($f->copy()));

// **The negative case.** A function that keeps what it returns must not have
// its result freed: the object is still reachable, and freeing the handle
// hands one number to two live objects.
$held = null;
function keeps() { global $held; $held = new Foo; return $held; }
var_dump(spl_object_id(keeps()));
$next = new Foo;                       // must NOT reuse the kept object's id
var_dump(spl_object_id($next) !== spl_object_id($held));
var_dump($held instanceof Foo);

// **A typed factory whose result the caller KEEPS.** Seeing through the return
// check is sound for "is this object fresh"; it is not sound for "does this
// object leave the frame". If the check is treated as pass-through in the
// second question too, the terminator mentions only the checked value, the
// `new` looks frame-local, and the frame drain destroys an object the caller is
// about to receive -- so the next object reuses a live handle.
interface X {} interface Y {}
class Both implements X, Y {}
function makeTyped(): X&Y { return new Both(); }
$kept = makeTyped();
$other = new Both();
var_dump(spl_object_id($kept) !== spl_object_id($other));
var_dump($kept instanceof Both, $other instanceof Both);

// A body returning something it did not make is not fresh either.
function passthrough($o) { return $o; }
var_dump(spl_object_id(passthrough($f)) === spl_object_id($f));
var_dump($f instanceof Foo);
