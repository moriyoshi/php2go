<?php
// get_parent_class(): with no argument it is the calling scope's parent and
// deprecated; a string must name a class, and anything but an object or such
// a string is a TypeError in the function's own words. An interface, a root
// class and stdClass answer false.

interface I {}
class Foo implements I { function test() { var_dump(get_parent_class()); } }
class Bar extends Foo { function b() { var_dump(get_parent_class($this)); } }
$bar = new Bar;
(new Foo)->test();
$bar->test();
$bar->b();
var_dump(get_parent_class($bar), get_parent_class("BAR"), get_parent_class("foo"), get_parent_class("I"));
var_dump(get_parent_class(new stdClass));
foreach (["", "[[[[", " ", [], 1, null] as $v) {
    try { var_dump(get_parent_class($v)); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
}
