<?php
// An engine class's method gets a stack frame, and the frame names the
// **declaring engine class** and that declaration's spelling -- not the
// receiver's class and not the call's spelling. `class MyRef extends
// ReflectionClass` failing in `getMethod` renders
// `ReflectionClass->getMethod('nope')`.
//
// Both halves come from the engine's own class tables rather than a list
// written here: `fn->common.function_name` for the spelling, and the presence
// of a name in a class's table for who declares it. Walking the receiver's
// parent chain is what turns a user subclass into its engine ancestor.
//
// The trace is attached inside the frame. Attached after the pop -- which is
// what the caller does -- the trace opened at the call site with the method
// that raised missing from it.
function show(callable $f) {
    try { $f(); } catch (Throwable $e) { echo $e->getTraceAsString(), "\n--\n"; }
}
class MyRef extends ReflectionClass {}
show(function () { (new ReflectionClass('Fiber'))->getMethod('nope'); });
show(function () { (new MyRef('Fiber'))->getMethod('nope'); });
show(function () { (new ReflectionClass('Fiber'))->GETMETHOD('nope'); });
show(function () { (new ReflectionClass('Fiber'))->getProperty('nope'); });
show(function () { $w = new WeakMap; $x = $w->offsetGet(new stdClass); });
show(function () { $r = new ReflectionClass('Fiber'); var_dump($r->getName()); });

// An engine class's **constructor** gets the same frame, from the same lookup.
// Construction is its own path and never reaches the method dispatcher, so it
// needed the push of its own -- both of these raise, and both opened their
// trace at the `new` with the constructor missing from it.
show(function () { new ReflectionClass('NoSuchClassHere'); });
show(function () { new MyRef('NoSuchClassHere'); });
show(function () { new WeakReference(); });

// The exception classes are deliberately outside the modelled engine set, so
// nothing claims their `__construct` and no frame is pushed for one. That is
// load-bearing: PHP captures a throwable's trace at `new`, before the
// constructor runs, so a frame here would put `__construct` into every
// exception's own trace.
function thrower() { throw new RuntimeException("r"); }
show('thrower');

// A weak store's dimension is **not** a method call: the engine reads it
// through the object's own handler and pushes no frame. Written out as a
// method it is a call, and does get one.
show(function () { $w = new WeakMap; $x = $w[new stdClass]; });
show(function () { $w = new WeakMap; $x = $w->offsetGet(new stdClass); });
$w = new WeakMap;
$k = new stdClass;
$w[$k] = 5;
var_dump($w[$k], count($w), isset($w[$k]));
