<?php
// A finally body with its own control flow. Inlining splices the body into the
// block that reached it, so this used to emit both arms of the `if`
// unconditionally -- a wrong answer rather than a gap.
function branchingFinally($x): int {
    try {
        return 1;
    } finally {
        if ($x) {
            echo "a";
        } else {
            echo "b";
        }
        echo "c";
    }
}

// A generator whose finally suspends. Each copy of the body is a distinct
// suspension point, which is what the engine has after its own inlining.
function yieldingFinally($x) {
    try {
        yield 1;
        return;
    } finally {
        yield 2;
        echo "f";
    }
}

// Fiber::suspend is an ordinary static call, so a copied finally just copies
// the call. Nothing about suspension appears in the IR.
function suspendingFinally($x): int {
    try {
        return 1;
    } finally {
        Fiber::suspend($x);
        echo "f";
    }
}
