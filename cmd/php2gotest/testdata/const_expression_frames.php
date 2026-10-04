<?php
// A deferred constant expression is evaluated with the reported position moved
// to where it was **written**, and a trace taken under that override gains a
// synthetic `[constant expression]()` frame recording where the program
// actually was. Both halves are one mechanism in the engine, so the frame is
// pushed only when the two positions differ.
//
// Only the outermost evaluation gets one: a constant reaching through another
// moves the position again and adds no second frame.
//
// And a **bare constant fetch is not evaluated in its own frame at all** --
// `const K = SOME_CONSTANT;` reports an undefined constant against the use
// site and adds nothing, while `SOME_CONSTANT + 0` in the same place reports
// against the declaration and does.
function show(callable $f) {
    try { $f(); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n", $e->getTraceAsString(), "\n--\n"; }
}
class N1 { const K = 1 % 0; }
class N2 { const K = N1::K + 1; }
class N3 { const K = N2::K . "x"; }
show(function () { var_dump(N3::K); });
class B1 { const K = NOT_DEFINED_ANYWHERE; }
show(function () { var_dump(B1::K); });
class P { public $p = 1 % 0; }
show(function () { new P; });
function pd($x = 1 % 0) { return $x; }
show(function () { pd(); });
class OK { const A = 2 * 3; const B = self::A . "!"; }
var_dump(OK::B);

// The same frame appears for the enum/ArrayAccess path, which is how the
// corpus reaches it.
enum Suit implements ArrayAccess {
    case H;
    public function offsetGet($k): mixed { throw new Exception("og"); }
    public function offsetExists($k): bool { return true; }
    public function offsetSet($k, $v): void {}
    public function offsetUnset($k): void {}
}
class F { const K = Suit::H[0]; }
show(function () { var_dump(F::K); });
