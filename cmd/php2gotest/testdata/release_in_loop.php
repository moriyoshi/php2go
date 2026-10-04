<?php
// A local written in a loop and never read -- `$x = new D()` -- releases its
// previous object at each assignment, as the engine does (OWNERSHIP-PERF.md):
// the new object is made first, then the old one is destroyed and its handle
// freed, so handles alternate. Each shape here is one where the timing is
// visible. (`f($x, $x = new D())` -- the old object still an argument -- is not
// here: the lowering reads `$x` for the first argument after the assignment,
// an older bug recorded in TODO.)

class D
{
    public $t;
    public function __construct($t) { $this->t = $t; echo "ctor $t\n"; }
    public function __destruct() { echo "dtor {$this->t}\n"; }
}

function loop()
{
    for ($i = 0; $i < 3; $i++) {
        $x = new D("L$i");
    }
    echo "loop done\n";
}
loop();
echo "--\n";

// Handles: the engine reuses the freed one, so they alternate.
function handles()
{
    for ($i = 0; $i < 4; $i++) {
        $h = new stdClass();
        echo spl_object_id($h), " ";
    }
    echo "\n";
}
handles();
echo "--\n";

// A conditional overwrite, and a scalar overwriting an object.
function branches($flip)
{
    $y = new D("B0");
    if ($flip) {
        $y = new D("B1");
    }
    echo "between\n";
    $y = 42;
    echo "scalar $y\n";
}
branches(true);
branches(false);
echo "end\n";
