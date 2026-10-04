<?php
// A local that provably cannot outlive its frame is destroyed when the function
// returns, which is **not** an approximation: PHP destroys a local when the
// frame is destroyed.
//
// The order is the engine's and it is not the obvious one -- measured:
//
//	function locals at frame end   creation order   dx, dy, dz
//	globals at script end          reverse          dC, dB, dA
//
// So a `defer` per object would be wrong twice over: LIFO gives the second
// order everywhere. The emitter keeps a frame list and drains it forward.
class Noisy
{
    public function __construct(public string $tag)
    {
    }

    public function __destruct()
    {
        echo "d{$this->tag}\n";
    }
}

function one()
{
    $a = new Noisy('one-a');
    echo "in one\n";
}

one();
echo "after one\n";

function three()
{
    $x = new Noisy('x');
    $y = new Noisy('y');
    $z = new Noisy('z');
    echo "in three\n";
}

three();
echo "after three\n";

// An early return still destroys: the drain is deferred, so every exit path is
// covered.
function early(bool $bail)
{
    $e = new Noisy('early');
    if ($bail) {
        echo "bailing\n";
        return;
    }
    echo "not reached\n";
}

early(true);
echo "after early\n";

// A global is not a frame local: it lives to script end and goes in reverse
// with the others.
$globalA = new Noisy('A');
$globalB = new Noisy('B');
echo "end\n";
