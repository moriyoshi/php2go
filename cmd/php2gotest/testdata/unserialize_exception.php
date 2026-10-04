<?php
// An exception survives serialize()/unserialize(): the engine's exception
// classes are known to the reader, their own properties are not dynamic, and
// each lands in the slot its mangled name names -- message, code and the
// previous-chain included, for a user subclass too.

class Mine extends RuntimeException {}
$pairs = [new Exception("outer", 3, new Error("inner")), new Mine("mine", 7, new LogicException("cause"))];
foreach ($pairs as $e) {
    $u = unserialize(serialize($e));
    printf("%s(%s, %d) <- %s(%s)\n", get_class($u), $u->getMessage(), $u->getCode(),
        get_class($u->getPrevious()), $u->getPrevious()->getMessage());
}
