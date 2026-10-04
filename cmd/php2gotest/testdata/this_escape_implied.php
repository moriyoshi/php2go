<?php
// The engine calls __toString, __debugInfo and count on its own; a method
// that only converts, dumps or counts $this lets it out exactly when that
// implied method does (lower.ImpliedMethod).
class Holder { static $h = []; }

class ViaToString { function printIt() { echo $this, "\n"; } function __toString(): string { Holder::$h[] = $this; return "kept"; } }
class ViaDebugInfo { function dumpIt() { ob_start(); var_dump($this); print_r($this); ob_end_clean(); } function __debugInfo() { Holder::$h[] = $this; return []; } }
class ViaCount implements Countable { function countIt() { return count($this); } function count(): int { Holder::$h[] = $this; return 1; } }

function run() {
    $a = new ViaToString; $a->printIt(); $id = spl_object_id($a); unset($a);
    echo "ViaToString "; var_dump(spl_object_id(new stdClass) === $id);
    $a = new ViaDebugInfo; $a->dumpIt(); $id = spl_object_id($a); unset($a);
    echo "ViaDebugInfo "; var_dump(spl_object_id(new stdClass) === $id);
    $a = new ViaCount; var_dump($a->countIt()); $id = spl_object_id($a); unset($a);
    echo "ViaCount "; var_dump(spl_object_id(new stdClass) === $id);
    var_dump(count(Holder::$h));
}
run();
