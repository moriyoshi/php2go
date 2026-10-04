<?php
// `++`/`--` on a property read through `__get` writes the value back even when
// the step fails (zend_post_incdec_overloaded_property): with no `__set` the
// write creates the dynamic property -- deprecation included, unless a user
// error handler is installed, which the pending exception keeps from running
// -- and a `__set` is not called at all. A property already present is
// stepped directly and not written back (temporary_cleaning_013 cases 11 and
// 14). A declared one reached through `__get` is refused, not tested here.

function show(Throwable $t) { echo get_class($t), ": ", $t->getMessage(), "\n"; }

class R {
    public $bar;
    function __construct() { $this->bar = new stdClass; }
    function &__get($x) { echo "get $x\n"; return $this->bar; }
}
class V {
    public $bar;
    function __construct() { $this->bar = [1]; }
    function __get($x) { echo "get $x\n"; return $this->bar; }
}
class S {
    function __get($x) { echo "get $x\n"; return new stdClass; }
    function __set($x, $v) { echo "set $x\n"; }
}
#[AllowDynamicProperties]
class Allowed {
    function __get($x) { echo "get $x\n"; return new stdClass; }
}

$r = new R;
try { $r->foo++; } catch (Throwable $t) { show($t); }
print_r($r); echo "\n";
$r2 = new R;
try { --$r2->foo; } catch (Throwable $t) { show($t); }
var_dump(isset($r2->foo));
$v = new V;
try { $v->foo++; } catch (Throwable $t) { show($t); }
print_r($v); echo "\n";
$ok = new V;
$ok->bar = 1;
$ok->n++;
print_r($ok); echo "\n";
// A property already present is stepped directly: no write-back.
$pd = new V;
$pd->dyn = new stdClass;
try { $pd->dyn++; } catch (Throwable $t) { show($t); }
$s = new S;
try { $s->foo++; } catch (Throwable $t) { show($t); }
$a = new Allowed;
try { $a->foo++; } catch (Throwable $t) { show($t); }
print_r($a); echo "\n";
set_error_handler(function ($n, $m) { echo "handler: $m\n"; return true; });
$h = new R;
try { $h->foo++; } catch (Throwable $t) { show($t); }
var_dump(isset($h->foo));
