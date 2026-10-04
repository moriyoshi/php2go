<?php
// An uncaught CompileError prints as a fatal error in one line and records
// E_COMPILE_ERROR.
register_shutdown_function(function () {
    $last = error_get_last();
    var_dump($last["type"], $last["message"]);
});
echo "before\n";
throw new CompileError("compiled badly");
