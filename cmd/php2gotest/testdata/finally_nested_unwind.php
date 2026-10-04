<?php
// A finally is not protected by its own try: while an exception unwinds
// through `try {...} finally {...}` (no catch), the finally body -- and a
// try/finally nested inside it -- runs once, and a throw from the finally
// replaces the pending exception in the enclosing handler.

try {
    echo "Outer try\n";
    try {
        echo "  Middle try\n";
        throw new Exception("first");
    } finally {
        echo "  Middle finally\n";
        try {
            echo "    Inner try\n";
        } finally {
            echo "    Inner finally\n";
        }
    }
    echo "Outer shouldn't get here\n";
} catch (Exception $e) {
    echo "Outer catch ", $e->getMessage(), "\n";
} finally {
    echo "Outer finally\n";
}

function f() {
    try {
        try {
            throw new Exception("a");
        } finally {
            echo "f finally\n";
            throw new Exception("b");
        }
    } catch (Exception $e) {
        echo "caught ", $e->getMessage(), " prev ", $e->getPrevious()?->getMessage(), "\n";
    }
}
f();

// A nested finally reached by `return` inside an unwinding finally still has
// the unwinding exception in flight: its throw chains onto it.
function g() {
    try {
        throw new Exception("1");
    } finally {
        try {
            return 42;
        } finally {
            throw new Exception("2");
        }
    }
}
try {
    g();
} catch (Exception $e) {
    echo $e->getMessage(), " prev ", $e->getPrevious()?->getMessage(), "\n";
}
