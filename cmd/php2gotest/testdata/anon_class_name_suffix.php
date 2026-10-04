<?php
// An anonymous class's name ends in the engine's runtime-definition counter,
// which starts at 0 for the script a request begins with -- whatever the
// harness compiled before it in the same process (gh13097_b).

$a = new class {};
$b = new class {};
echo str_replace("\0", "\\0", get_class($a)), "\n";
echo str_replace("\0", "\\0", get_class($b)), "\n";
