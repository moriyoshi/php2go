<?php
// Releases inside a generator body, whose locals live in its frame across
// every yield. `unset` and overwrite destroy what a local owns outright at
// that line; a yielded object the consumer keeps is not the local's alone.
class D {
    function __construct(public $n) {}
    function __destruct() { echo "D{$this->n} gone\n"; }
}
function gen() {
    $a = new D(1); unset($a); echo "gen after unset\n";
    $b = new D(2); $b = null; echo "gen after overwrite\n";
    $c = new D(3); $c = new D(4); echo "gen after reassign\n";
    yield 1;
    $e = new D(5); unset($e); echo "gen after unset past a yield\n";
    $k = new D(6);
    $got = yield 2;
    unset($k); echo "gen after unset across a yield ($got)\n";
    // Negative: yielded, and the consumer keeps it.
    $y = new D(7);
    yield $y;
    unset($y); echo "gen after unset of a yielded one\n";
}
$kept = [];
foreach (gen() as $v) {
    if ($v instanceof D) $kept[] = $v;
    echo "got\n";
}
echo "loop done\n";
$g = gen();
$g->current(); $g->send('S');
echo "sent\n";
// Destroyed while suspended at `$got = yield 2`: its frame's objects die.
unset($g);
echo "end\n";
