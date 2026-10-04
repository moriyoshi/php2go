<?php
// error_log type 3 appends to a file through a runtime leaf compiled code now
// has (bug39542 was refused only for linking error_log).
$f = sys_get_temp_dir() . '/php2go_error_log_' . getmypid() . '.txt';
@unlink($f);
var_dump(error_log("one\n", 3, $f));
var_dump(error_log("two", 3, $f));
var_dump(file_get_contents($f));
unlink($f);
var_dump(error_log("x", 3, 'php://memory'));
var_dump(error_log("to output\n", 3, 'php://output'));
var_dump(error_log("x", 3, 'php://input'));
var_dump(error_log("x", 3, 'php://temp/maxmemory:10'));
var_dump(error_log("[upper]\n", 3, 'PHP://OUTPUT'));
var_dump(error_log("x", 3, 'data:text/plain,abc'));
var_dump(error_log("x", 3, '/nonexistent-dir-php2go/x.log'));
try {
    error_log("x", 3, '');
} catch (ValueError $e) {
    echo $e->getMessage(), "\n";
}
