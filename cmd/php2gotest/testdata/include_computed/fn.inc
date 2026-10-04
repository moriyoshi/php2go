<?php
function from_computed_include($a) { return $a * 2; }
echo "fn.inc: local = ", $local, "\n";
$local = "changed by fn.inc";
