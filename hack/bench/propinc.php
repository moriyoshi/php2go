<?php
class Foo { public $x = 0; function inc($n) { for ($i = 0; $i < $n; ++$i) { $this->x++; } } }
$t = hrtime(true); (new Foo)->inc(5000000); echo "propinc ", round((hrtime(true)-$t)/1e9, 3), "\n";
