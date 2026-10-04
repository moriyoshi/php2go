<?php
// A `finally` body is **copied** at every exit, and a copy can queue work of
// its own: a loop inside it needs body blocks, a nested try needs its own
// copies. It also raises the question the region model cannot answer from
// position alone -- which finally *bodies* a block sits inside -- because an
// exception raised in one is handled by none of them.

function loopInsideFinally() {
    $log = [];
    try {
        $log[] = "try";
    } finally {
        foreach ([1, 2, 3] as $n) {
            $log[] = "n=$n";
        }
    }
    return $log;
}
var_dump(loopInsideFinally());

// The same body reached through the exceptional exit: a different copy.
function loopInFinallyWhileThrowing() {
    $log = [];
    try {
        try {
            throw new LogicException("thrown");
        } finally {
            foreach (["a", "b"] as $s) {
                $log[] = $s;
            }
        }
    } catch (LogicException $e) {
        $log[] = "caught " . $e->getMessage();
    }
    return $log;
}
var_dump(loopInFinallyWhileThrowing());

// A finally that returns, with a loop inside it.
function returnsFromFinallyWithLoop() {
    try {
        throw new Exception("discarded");
    } finally {
        $sum = 0;
        foreach ([1, 2, 3] as $n) {
            $sum += $n;
        }
        return $sum;
    }
}
var_dump(returnsFromFinallyWithLoop());

// A nested finally whose inner body also loops.
function nestedWithLoops() {
    $log = [];
    try {
        $log[] = "outer try";
    } finally {
        try {
            foreach ([1, 2] as $i) { $log[] = "inner $i"; }
        } finally {
            foreach ([3, 4] as $j) { $log[] = "innermost $j"; }
        }
        $log[] = "outer finally";
    }
    return $log;
}
var_dump(nestedWithLoops());
