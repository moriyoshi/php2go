<?php
// Destroying a suspended fiber force-closes it where its last holder lets it
// go -- an unset, an overwrite, the end of the frame that held it -- running
// its finally blocks there. A catch the unwind passes does not catch it, and
// an exception no catch matched still runs the enclosing finally.

// unset: finally runs before "after unset".
$f = new Fiber(function () {
    try {
        try {
            echo "fiber\n";
            Fiber::suspend();
            echo "not reached\n";
        } catch (Throwable $e) {
            echo "not caught\n";
        } finally {
            echo "inner finally\n";
        }
    } finally {
        echo "outer finally\n";
    }
    echo "not reached either\n";
});
$f->start();
unset($f);
echo "after unset\n";

// Overwrite, with a suspend in the finally refused and caught there.
$g = new Fiber(function () {
    try {
        Fiber::suspend();
    } finally {
        echo "overwrite finally\n";
        try {
            Fiber::suspend();
        } catch (FiberError $e) {
            echo $e->getMessage(), "\n";
        }
    }
});
$g->start();
$g = null;
echo "after overwrite\n";

// A throw from a finally is caught by the body's own catch, and the next
// finally still runs.
$h = new Fiber(function () {
    try {
        try {
            Fiber::suspend();
        } finally {
            throw new Exception("from finally");
        }
    } catch (Exception $e) {
        echo "caught ", $e->getMessage(), "\n";
    } finally {
        echo "last finally\n";
    }
});
$h->start();
unset($h);
echo "after throw\n";

// A local fiber dies when its function returns; a suspend in its finally
// is thrown to the caller.
function local(): void {
    $f = new Fiber(function () {
        try {
            Fiber::suspend();
        } finally {
            echo "local finally\n";
            Fiber::suspend();
        }
    });
    $f->start();
    echo "returning\n";
}
try {
    local();
} catch (FiberError $e) {
    echo "caller: ", $e->getMessage(), "\n";
}

// Not started, finished: nothing to close.
$n = new Fiber(function () { echo "never\n"; });
unset($n);
$d = new Fiber(function () {
    try { echo "ran\n"; } finally { echo "done finally\n"; }
});
$d->start();
unset($d);

// A throw into a suspension inside a try within an unwinding finally is
// that try's to catch, and the unwinding resumes after it.
$t = new Fiber(function () {
    try {
        Fiber::suspend();
    } finally {
        echo "in finally\n";
        try {
            Fiber::suspend();
        } catch (LogicException $e) {
            echo "finally caught ", $e->getMessage(), "\n";
        }
    }
});
$t->start();
try {
    $t->throw(new Exception("first"));
    echo "suspended again\n";
    $t->throw(new LogicException("second"));
} catch (Exception $e) {
    echo "out ", $e->getMessage(), "\n";
}

// The same routing without a fiber: an exception no clause matches leaves
// through the enclosing finally, and an enclosing catch still gets it.
function plain(): void {
    try {
        try {
            throw new Exception("plain");
        } catch (TypeError $e) {
            echo "wrong\n";
        }
    } finally {
        echo "plain finally\n";
    }
}
try { plain(); } catch (Exception $e) { echo "caught ", $e->getMessage(), "\n"; }
function outerCatch(): void {
    try {
        try {
            throw new LogicException("routed");
        } catch (TypeError $e) {
            echo "wrong\n";
        }
        echo "not reached\n";
    } catch (LogicException $e) {
        echo "outer caught ", $e->getMessage(), "\n";
    }
}
outerCatch();
function gen() {
    try {
        try {
            yield 1;
        } catch (TypeError $e) {
            echo "wrong\n";
        }
    } finally {
        echo "gen finally\n";
    }
}
$gen = gen();
$gen->current();
unset($gen);
echo "end\n";
