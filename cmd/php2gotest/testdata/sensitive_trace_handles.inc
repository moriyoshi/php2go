<?php
// Included by sensitive_trace_handles.php from inside a function, so its frame
// names the file.
shown(5);
var_dump(array_map(fn ($f) => array_keys($f), debug_backtrace()));
