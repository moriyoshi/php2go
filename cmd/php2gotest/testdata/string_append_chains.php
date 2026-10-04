<?php
// Loop-carried `.=` chains, which the compiler carries as an in-place buffer
// (OWNERSHIP-PERF.md P1). Every shape here is one where an in-place append would
// print something different if the proof behind it were wrong: a snapshot that
// must not grow, a conversion that must not move, a value that must not be
// shared.
class Loud
{
    public function __construct(private string $tag)
    {
    }

    public function __toString(): string
    {
        echo "[to-string {$this->tag}]";
        return $this->tag;
    }
}

function plain($n)
{
    $s = "";
    while ($n-- > 0) {
        $s .= "ab";
    }
    return $s;
}

// Every snapshot stored along the way keeps the length it had.
function snapshots($n)
{
    $s = "<";
    $seen = [];
    for ($i = 0; $i < $n; $i++) {
        $s .= $i;
        $seen[] = $s;
    }
    return [$s, $seen];
}

// Read inside the loop, before and after the append.
function readInside($n)
{
    $s = "x";
    while ($n-- > 0) {
        echo strlen($s), " ";
        $s .= "y";
        echo $s, "\n";
    }
    return $s;
}

// The pieces convert as concat converts them, in order, one per iteration.
function conversions()
{
    $s = "";
    foreach ([1, 2.5, null, true, false, -0.0, 1e20, new Loud("L")] as $piece) {
        $s .= $piece;
        echo "|";
    }
    echo "\n";
    return $s;
}

// Zero iterations: the entry value is returned as it was.
function never()
{
    $s = "start";
    for ($i = 0; $i < 0; $i++) {
        $s .= "never";
    }
    return $s;
}

// Entered with an int, which must stay an int when the loop does not run.
function intEntry($n)
{
    $s = 0;
    while ($n-- > 0) {
        $s .= "x";
    }
    return $s;
}

// Two branches, one append each.
function branches($n)
{
    $s = "";
    for ($i = 0; $i < $n; $i++) {
        if ($i % 2) {
            $s .= "o";
        } else {
            $s .= "e";
        }
    }
    return $s;
}

// Nested loops over one chain, and an entry that is itself a concatenation.
function nested($rows, $cols, $prefix)
{
    $s = $prefix . ":";
    for ($r = 0; $r < $rows; $r++) {
        for ($c = 0; $c < $cols; $c++) {
            $s .= "$r$c,";
        }
        $s .= ";";
    }
    return $s;
}

// The old value kept under another name: it must not see the appends.
function kept($n)
{
    $s = "k";
    $old = [];
    while ($n-- > 0) {
        $t = $s;
        $s .= "!";
        $old[] = $t;
    }
    return [$s, $old];
}

// Appending the chain to itself.
function doubling($n)
{
    $s = "a";
    while ($n-- > 0) {
        $s .= $s;
    }
    return $s;
}

// A snapshot handed to a callee that keeps it.
function handed($n)
{
    $s = "";
    $keep = new ArrayObject();
    while ($n-- > 0) {
        $s .= "z";
        $keep->append($s);
    }
    return [$s, $keep->getArrayCopy()];
}

var_dump(plain(3));
var_dump(snapshots(4));
var_dump(readInside(3));
var_dump(conversions());
var_dump(never());
var_dump(intEntry(0), intEntry(2));
var_dump(branches(5));
var_dump(nested(2, 3, "p"));
var_dump(kept(3));
var_dump(doubling(3));
var_dump(handed(3));
