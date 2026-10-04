<?php
class Foo { static $a = 0; public $b = 0; const TEST = 0; }
function create_object($n) { for ($i = 0; $i < $n; ++$i) { $x = new Foo(); } }
$t = hrtime(true); create_object(5000000); echo "new ", round((hrtime(true)-$t)/1e9, 3), "\n";
