<?php
// What an engine method calls back is called from the engine: the method
// ReflectionMethod::invoke runs sits at `[internal function]` in a trace --
// no file or line -- beneath the invoke frame, which does have them
// (bug45805). An attribute constructor reached through newInstance() is the
// same.

function frames() {
    foreach (array_slice(debug_backtrace(), 1, 2) as $f) {
        echo ($f['class'] ?? ''), ($f['type'] ?? ''), $f['function'], " @ ",
            isset($f['file']) ? basename($f['file']) . ':' . $f['line'] : '[internal]', "\n";
    }
}

class B {
    public function foo($x) {
        frames();
        return $x * 2;
    }
}

#[Attribute]
class Note {
    public function __construct() {
        frames();
    }
}

#[Note]
class Marked {}

$m = new ReflectionMethod('B', 'foo');
var_dump($m->invoke(new B, 21));
var_dump($m->invokeArgs(new B, [5]));
(new ReflectionClass('Marked'))->getAttributes()[0]->newInstance();
