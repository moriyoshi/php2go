<?php
// A property hook gets a stack frame, spelled unlike any other: the declaring
// class, an arrow, the property with its `$`, then `::` and the hook --
// `A->$prop::set('x')`. The arguments are the hook's own, so a `set` names the
// value and a `get` names nothing.
//
// The declaring class, not the receiver's: a hook declared in `A` and reached
// on a `B extends A` still renders `A->$prop::set(...)`. The hook table is
// keyed by the instance's class, so the declaration had to be carried onto the
// entry.
//
// Reached from inside a library function the frame is located at
// `[internal function]`, by the rule `PushCall` already states -- which is how
// `get_object_vars` turned up a second bug: it is scope-sensitive, and the
// scope-sensitive table was dispatched *above* the frame push, so those
// builtins had no frame of their own for anything they called to sit on.
function show(callable $f) {
    try { $f(); } catch (Throwable $e) { echo $e->getTraceAsString(), "\n--\n"; }
}
class A {
    public $prop {
        set($value) { throw new Exception('s'); }
        get { throw new Exception('g'); }
    }
    public $ok {
        set($value) { $this->_o = $value; }
        get { return 7; }
    }
    private $_o = 0;
}
class B extends A {}
show(function () { $c = new A; $c->prop = 'x'; });
show(function () { $c = new A; echo $c->prop; });
show(function () { $c = new B; $c->prop = 'x'; });
show(function () { $c = new B; echo $c->prop; });
show(function () { $c = new A; var_dump(isset($c->prop)); });
show(function () { $c = new A; $c->prop .= 'x'; });
show(function () { $c = new A; var_dump(get_object_vars($c)); });
$a = new A;
$a->ok = 3;
var_dump($a->ok);
