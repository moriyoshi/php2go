<?php
// `$o->ro++` reads, steps a copy and only then has its write refused, so the
// step's own diagnostics come first and a step that throws becomes the
// readonly Error's previous.
class T {
    public function __construct(public readonly mixed $v) {}
}
class Typed {
    public readonly object $prop;
    public function __construct(object $prop) { $this->prop = $prop; }
}
// The TypeError is made first, so it has the lower handle.
$first = new Typed(new stdClass);
try { $first->prop++; } catch (Error $e) { echo spl_object_id($e), " ", spl_object_id($e->getPrevious()), "\n"; }

function show(Throwable $e) {
    echo get_class($e), ": ", $e->getMessage(), "\n";
    for ($p = $e->getPrevious(); $p; $p = $p->getPrevious()) {
        echo "  prev ", get_class($p), ": ", $p->getMessage(), "\n";
    }
}
foreach ([new stdClass, [1], 1, 1.5, true, null, "a", "", PHP_INT_MAX] as $v) {
    $t = new T($v);
    try { $t->v++; } catch (Error $e) { show($e); }
    try { $t->v--; } catch (Error $e) { show($e); }
    try { ++$t->v; } catch (Error $e) { show($e); }
    try { $x = --$t->v; } catch (Error $e) { show($e); }
    var_dump($t->v);
}
$typed = new Typed(new stdClass);
try { $typed->prop++; } catch (Error $e) { show($e); }
try { --$typed->prop; } catch (Error $e) { show($e); }
try { $typed->prop += 1; } catch (Error $e) { show($e); }

set_error_handler(function ($no, $msg) { throw new Exception("handler: $msg"); });
$t = new T(true);
try { $t->v++; } catch (Throwable $e) { show($e); }
$t = new T(null);
try { $t->v--; } catch (Throwable $e) { show($e); }

// A property the scope cannot read is refused at the read, before any step;
// inside the class the step runs first again.
class PV {
    public function __construct(private readonly mixed $v, protected readonly mixed $w) {}
    public function bump() { $this->v++; }
}
$pv = new PV(new stdClass, [1]);
try { $pv->v++; } catch (Error $e) { show($e); }
try { $pv->w--; } catch (Error $e) { show($e); }
try { $pv->bump(); } catch (Error $e) { show($e); }
