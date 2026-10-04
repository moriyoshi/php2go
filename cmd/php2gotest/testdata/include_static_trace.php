<?php
// Where an include appears in a trace: from a function the included file
// calls, its frame names the file; from the included body itself, it does
// not.
try {
    require __DIR__ . '/include_static_trace.inc';
} catch (Exception $e) {
    echo $e->getMessage(), "\n", $e->getTraceAsString(), "\n";
}
$direct = true;
try {
    include __DIR__ . '/include_static_trace2.inc';
} catch (Exception $e) {
    echo $e->getMessage(), "\n", $e->getTraceAsString(), "\n";
}
