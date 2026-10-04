--TEST--
set_exception_handler replaces the fatal error
--FILE--
<?php
set_exception_handler(function ($e) { echo "handled: ", $e->getMessage(), "\n"; });
throw new Exception("x");
?>
--EXPECT--
handled: x
