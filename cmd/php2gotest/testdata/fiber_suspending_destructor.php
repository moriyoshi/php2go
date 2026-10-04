<?php
// A destructor that suspends the fiber it runs in, entered as a frame ends
// or as a statement's temporary dies. The drain is a suspension point of the
// body: the fiber parks inside the destructor, and resuming it finishes the
// destructor and the rest of the drain.
class D {
    public function __construct(public string $n, public bool $suspend = true) {}
    public function __destruct() {
        echo "start {$this->n}\n";
        try {
            if ($this->suspend) {
                $got = Fiber::suspend("from {$this->n}");
                echo "resumed {$this->n} with ", var_export($got, true), "\n";
            }
        } finally {
            echo "finally {$this->n}\n";
        }
        echo "end {$this->n}\n";
    }
}

echo "-- two locals, in creation order\n";
$f = new Fiber(function () {
    $a = new D("a");
    $b = new D("b");
    echo "body done\n";
    return "ret";
});
var_dump($f->start());
var_dump($f->resume(1));
var_dump($f->resume(2));
var_dump($f->isTerminated(), $f->getReturn());

echo "-- statement temporaries die at their statement\n";
$f = new Fiber(function () {
    new D("x", false);
    $y = new D("y");
    new D("z");
    echo "body done\n";
});
var_dump($f->start());
var_dump($f->resume("go"));
var_dump($f->resume("again"));
var_dump($f->isTerminated());

echo "-- the fiber destroyed while suspended in one\n";
function run() {
    $f = new Fiber(function () {
        $c = new D("c");
        $d = new D("d", false);
        echo "body done\n";
    });
    var_dump($f->start());
    unset($f);
    echo "after unset\n";
}
run();
$g = new Fiber(function () {
    $e = new D("e");
    $h = new D("h", false);
});
var_dump($g->start());
$g = null;
echo "after null\n";
