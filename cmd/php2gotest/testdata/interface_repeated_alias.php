<?php
// One interface named twice in a declaration's own list is a link fatal,
// and an alias is the only way to name it twice: the check runs when the
// declaration does. Inherited duplicates are fine.
interface a {}
interface z {}
class_alias('a', 'b');
interface c extends a, z {}
class P implements a {}
class Q extends P implements b, z {}
echo "ok\n";
register_shutdown_function(function () { echo "shutdown\n"; });
enum E implements z, b, a { case X; }
echo "not reached\n";
