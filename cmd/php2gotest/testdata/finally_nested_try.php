<?php
// A try inside a finally that an exception is unwinding through: the copy
// of the finally body has its own handlers, a catch there does not replace
// the exception being unwound, and a throw or return inside it chains or
// discards it as the engine does.
function caughtInside() {
    try {
        throw new Exception(1);
    } finally {
        try {
            try {
                throw new Exception(2);
            } finally {
                echo "inner finally\n";
            }
        } catch (Exception $e) {
            echo "caught inside: ", $e->getMessage(), "\n";
        }
    }
}
try {
    caughtInside();
} catch (Exception $e) {
    echo "caught outside: ", $e->getMessage(), "\n";
}

function returnThenThrow() {
    try {
        throw new Exception(1);
    } finally {
        try {
            try {
                try {
                } finally {
                    return 42;
                }
            } finally {
                throw new Exception(3);
            }
        } catch (Exception $e) {
            echo "caught: ", $e->getMessage(), "\n";
        }
    }
}
try {
    var_dump(returnThenThrow());
} catch (Exception $e) {
    for (; $e; $e = $e->getPrevious()) {
        echo "chain: ", $e->getMessage(), "\n";
    }
}

function escapes() {
    try {
        throw new Exception(1);
    } finally {
        try {
            throw new Exception(2);
        } catch (LogicException $e) {
            echo "not this one\n";
        } finally {
            echo "escaping\n";
        }
    }
}
try {
    escapes();
} catch (Exception $e) {
    for (; $e; $e = $e->getPrevious()) {
        echo "chain: ", $e->getMessage(), "\n";
    }
}

// An empty catch as the last thing in the finally: its handler is the end
// of the finally, which resumes the exception being unwound.
function emptyCatch() {
    try {
        throw new Exception(1);
    } finally {
        try {
            throw new Exception(2);
        } catch (Exception $e) {
        }
    }
}
try {
    emptyCatch();
} catch (Exception $e) {
    echo "after an empty catch: ", $e->getMessage(), "\n";
}
