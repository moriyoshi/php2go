<?php
// Echoing a local runs the __toString a trait supplies; one that stores
// $this keeps the object past its frame (its own fixture, so no other kept
// object's shutdown order shows).
class Keeps {
    public static $kept = [];
}
trait KeepsViaTrait {
    function __toString(): string { Keeps::$kept[] = $this; return "tr"; }
}
class ViaTrait {
    use KeepsViaTrait;
    function __destruct() { echo "dV\n"; }
}
function echoViaTrait() { $v = new ViaTrait; echo $v, "\n"; echo "trait end\n"; }
echoViaTrait(); echo "after trait\n";
echo "end\n";
