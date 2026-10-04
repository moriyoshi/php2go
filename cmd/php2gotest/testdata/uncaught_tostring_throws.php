<?php
// An uncaught exception whose __toString throws: the engine reports the
// exception __toString threw instead, called from the `[internal function]`
// frame, and the original is never printed.
class Loud extends Error {
    function __toString(): string {
        echo "rendering\n";
        throw new LogicException("from __toString");
    }
}
throw new Loud("original");
