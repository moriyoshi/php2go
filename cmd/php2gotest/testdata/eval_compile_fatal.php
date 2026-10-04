<?php
// Code eval refuses to compile is fatal where the eval runs, whatever `@`
// says, and the shutdown functions still run.
register_shutdown_function(function () { echo "shutdown\n"; });
eval('function kept() { return "kept"; }');
echo kept(), "\n";
@eval('class self {}');
echo "never\n";
