<?php
// `.` in a constant expression converts an object through `__toString`: a
// parameter default `new Foo . ''` runs it on every call that needs it, and a
// failing one is the call's error.

class Foo {
    public function __toString() { static $i = 0; return (string) $i++; }
}
class Bad { public function __toString() { throw new Exception("no string"); } }

function test(string $foo = new Foo() . '') { var_dump($foo); }
test();
test();
test("given");
test();
function bad($s = "x" . new Bad) { var_dump($s); }
try { bad(); } catch (Exception $e) { echo $e->getMessage(), "\n"; }
