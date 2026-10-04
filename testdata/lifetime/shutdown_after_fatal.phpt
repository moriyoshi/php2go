--TEST--
shutdown functions still run after an uncaught exception
--FILE--
<?php
register_shutdown_function(function () { echo "shutdown\n"; });
echo "body\n";
throw new Exception("boom");
?>
--EXPECTF--
body

Fatal error: Uncaught Exception: boom in %s:%d
Stack trace:
#0 {main}
  thrown in %s on line %d
shutdown
