--TEST--
register_shutdown_function runs after the body
--FILE--
<?php
register_shutdown_function(function () { echo "bye\n"; });
register_shutdown_function(function ($m) { echo "arg:$m\n"; }, "v");
echo "body\n";
?>
--EXPECT--
body
bye
arg:v
