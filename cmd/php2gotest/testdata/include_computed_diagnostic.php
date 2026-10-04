<?php
// A computed include of a file whose compilation warns: the engine raises
// the warning through the error handler each time it compiles the file --
// before any of its code, on every `include`, and not for an `include_once`
// that finds the file done (probed).
set_error_handler(function ($no, $str, $file, $line) {
    echo "handler[$no] $str @ ", basename($file), ":$line\n";
    return true;
});
$dir = __DIR__ . '/include_diag/';
$name = 'warned';
echo "before\n";
var_dump(include $dir . $name . '.inc');
var_dump((new Warned)->hi());
var_dump(include $dir . $name . '.inc');
var_dump(include_once $dir . $name . '.inc');
$other = 'clean';
var_dump(include $dir . $other . '.inc');

// A handler that throws stops the include before the file's code runs.
set_error_handler(function ($no, $str) { throw new ErrorException($str); });
try {
    include $dir . $name . '.inc';
} catch (ErrorException $e) {
    echo "caught: ", $e->getMessage(), "\n";
}
echo "done\n";
