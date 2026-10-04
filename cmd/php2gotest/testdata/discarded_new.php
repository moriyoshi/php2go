<?php
// A `new` whose result is used by nothing dies at the end of its statement, so
// the next object reuses its handle. The engine prints #1 for both dumps below.
//
// The negative half is the important one, and it is why this claim is made only
// in the file body: in a *function*, `$a = new C();` with `$a` never read again
// has its slot promoted away, which after promotion is indistinguishable from a
// bare `new C();` -- but PHP holds a local until the frame dies. Destroying it
// at its constructor prints the destructor before the body runs.
class D {
    public function __construct(public string $tag) { echo "ctor {$this->tag}\n"; }
    public function __destruct() { echo "dtor {$this->tag}\n"; }
}

// Bare statement: dead at once, and the handle comes straight back.
new D('bare');
echo "after bare\n";
$first = new D('kept');
echo spl_object_id($first), "\n";

// A local in a function is NOT this -- it lives to frame end even when nothing
// reads it again.
function holdsLocal() {
    $a = new D('local');
    echo "in holdsLocal\n";
}
holdsLocal();
echo "after holdsLocal\n";

// A constructor that throws means no destructor at all, and the number is still
// handed back.
class Boom {
    public function __construct() { throw new RuntimeException("boom"); }
    public function __destruct() { echo "NOT REACHED\n"; }
}
try { new Boom(); } catch (RuntimeException $e) { echo "caught ", $e->getMessage(), "\n"; }
echo "after boom\n";

// A global assigned from `new` is a real use: it lives to script end.
$globalOne = new D('global-1');
$globalTwo = new D('global-2');
echo "end\n";
