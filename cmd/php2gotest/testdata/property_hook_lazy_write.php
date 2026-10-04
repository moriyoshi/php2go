<?php
// A write to a hooked property of an uninitialized lazy object runs the set
// hook on the lazy object as it is; only the hook's own write to the backing
// store initialises it (gh18000). A hook that never touches the backing
// store leaves the object lazy.

class C {
    public $prop { set { echo "set\n"; $this->prop = $value * 2; } }
    public $g { get { echo "get\n"; return 5; } }
    public $both { get { echo "bget\n"; return $this->both; } set { echo "bset\n"; $this->both = $value; } }
    public $v { set { echo "vset $value\n"; } get => 7; }
    public private(set) int $ps { set { echo "psset\n"; $this->ps = $value; } }
    public $t { set { echo "tset\n"; throw new Exception("no"); } }
    public $other { set { echo "oset\n"; $this->plain = $value; } }
    public $plain = 0;
}

$rc = new ReflectionClass(C::class);
$proxy = fn() => $rc->newLazyProxy(function () { echo "init\n"; return new C; });
$ghost = fn() => $rc->newLazyGhost(function ($o) { echo "ginit\n"; });

function write(C $c) {
    $c->prop = 1;
    var_dump($c->prop);
}
write($proxy());
write($ghost());

$o = $proxy(); var_dump($o->g);
$o = $proxy(); $o->both = 3; var_dump($o->both);
$o = $proxy(); $o->v = 1; var_dump($rc->isUninitializedLazyObject($o));
$o = $proxy();
try { $o->ps = 1; } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
var_dump($rc->isUninitializedLazyObject($o));
$o = $proxy();
try { $o->t = 1; } catch (Exception $e) { echo $e->getMessage(), "\n"; }
var_dump($rc->isUninitializedLazyObject($o));
$o = $proxy(); $o->other = 4; var_dump($o->plain, $rc->isUninitializedLazyObject($o));
$o = $proxy(); $o->plain = 5; $o->other = 6; var_dump($o->plain);
$g = $ghost(); $g->v = 2; var_dump($rc->isUninitializedLazyObject($g));
$g->other = 3; var_dump($g->plain);
