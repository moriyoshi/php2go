<?php
function h1($errno, $errstr) { echo "h1[", $errno, "]: ", $errstr, "\n"; return true; }
function h2($errno, $errstr) { echo "h2: ", $errstr, "\n"; return true; }
set_error_handler('h1');
echo $a, "\n";
set_error_handler('h2');
echo $b, "\n";
set_error_handler(null);
echo $c, "\n";
restore_error_handler();
echo $d, "\n";
restore_error_handler();
echo $e, "\n";
