<?php
// A static method stores the running Fiber: the fiber made in a function
// must outlive that function. Alone in its file, so no other body's
// `Fiber::getCurrent()` answers the question for it.
class F { public static $f; static function keep() { self::$f = Fiber::getCurrent(); } }
function startFiber() {
    $a = new Fiber(function () { F::keep(); Fiber::suspend(); echo "resumed "; });
    $a->start();
    echo "started ";
}
startFiber();
echo "| fiber started\n";
F::$f->resume();
echo "| end\n";
