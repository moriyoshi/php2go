<?php
// `[$target, "Q::m"]`: `self` and `parent` resolve against the target's class,
// `static` against the calling frame, any other Q must be a class the target
// is an instance of. The form is deprecated whenever that class part resolves
// -- by is_callable as by a call -- and the method must then exist there.

class P { function pm() {} }
class C extends P {
    function cm() {}
    function __call($n, $a) {}
    function inside() {
        var_dump(is_callable([$this, 'static::cm']), is_callable([$this, 'parent::nope']));
    }
}
class D { function dm() {} }
$c = new C;
$cases = [[$c, 'parent::pm'], [$c, 'parent::cm'], [$c, 'self::cm'], [$c, 'static::nope'],
    [$c, 'P::pm'], [$c, 'P::nope'], [$c, 'D::dm'], ['C', 'parent::pm'], [new D, 'parent::dm']];
foreach ($cases as $cb) {
    var_dump(is_callable($cb));
    try { call_user_func($cb); echo "called\n"; } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
}
$c->inside();
var_dump(is_callable([$c, 'parent::pm'], true));
