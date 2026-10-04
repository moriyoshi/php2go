<?php
// An included file that declares a class the program already has: guarded,
// it returns before the declaration; reached, the declaration is the
// engine's `Cannot redeclare` fatal, naming the first one.
class A {}
class B extends A {}
register_shutdown_function(function () {
    echo "shutdown\n";
});
include __DIR__ . '/include_redeclare_guarded.inc';
echo "after the guarded include\n";
include __DIR__ . '/include_redeclare_reached.inc';
echo "not reached either\n";
