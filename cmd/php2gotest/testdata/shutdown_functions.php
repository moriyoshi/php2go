<?php
// A shutdown function is a **callback with its own arguments**, run after the
// body in registration order -- including on `exit`, and including after an
// uncaught exception's fatal message.

function named($tag) { echo "named($tag)\n"; }

register_shutdown_function(function () { echo "first\n"; });
register_shutdown_function(function ($a, $b) { echo "second($a,$b)\n"; }, 1, 2);
register_shutdown_function("named", "by name");
register_shutdown_function([new class { public function m() { echo "method\n"; } }, "m"]);

// Registered from inside a function, and from inside another callback.
function registersMore() {
    register_shutdown_function(function () { echo "from a function\n"; });
}
registersMore();

// The cycle collector this program does not have: 0 is the truthful count, and
// it is what PHP returns when there is nothing to collect.
var_dump(gc_collect_cycles());
var_dump(gc_enabled());
gc_disable();
gc_enable();
var_dump(gc_collect_cycles());

echo "body done\n";
