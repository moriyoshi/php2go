<?php
// unserialize's result is freed when its consumer is done with it, and the next
// object takes the number back -- unless something else may hold it: a hook
// that kept $this, a cycle, an enum case.
class P { public $c; }
class W { public $v = 1; public function __wakeup() { $GLOBALS['keep'] = $this; } }
class U { public $v = 1; public function __unserialize(array $d): void { $GLOBALS['keep2'] = $this; } }
class Test {
    public function __construct(public readonly int $prop = 1) {}
}
enum E { case A; }

var_dump($s = serialize(new Test));
var_dump(unserialize($s));
var_dump(unserialize('O:4:"Test":1:{s:4:"prop";i:2;}'));
var_dump(new stdClass);
echo "-- wakeup keeps\n";
var_dump(unserialize('O:1:"W":0:{}'));
var_dump(new stdClass);
echo "-- __unserialize keeps\n";
var_dump(unserialize('O:1:"U":0:{}'));
var_dump(new stdClass);
echo "-- self cycle\n";
var_dump(unserialize('O:1:"P":1:{s:1:"c";r:1;}'));
var_dump(new stdClass);
echo "-- enum\n";
var_dump(unserialize('E:3:"E:A";'));
var_dump(new stdClass);
echo "-- kept in a variable\n";
$k = unserialize('O:1:"P":1:{s:1:"c";N;}');
var_dump(new stdClass, $k);
var_dump(isset($keep), isset($keep2));
