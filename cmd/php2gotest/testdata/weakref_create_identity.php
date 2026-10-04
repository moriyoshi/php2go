<?php
// WeakReference::create() hands back the same WeakReference for an object
// while one exists: two calls are identical, and a third object is its own.

$a = new stdClass;
$b = new stdClass;
$ra = WeakReference::create($a);
var_dump($ra === WeakReference::create($a));
var_dump($ra === WeakReference::create($b));
var_dump(WeakReference::create($b) === WeakReference::create($b));
var_dump($ra->get() === $a);
