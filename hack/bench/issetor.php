<?php
function issetor($n) { $val = array(0,1,2,3,4,5,6,7,8,9); for ($i = 0; $i < $n; ++$i) { $x = $val ?: null; } }
$t = hrtime(true); issetor(5000000); echo "issetor ", round((hrtime(true)-$t)/1e9, 3), "\n";
