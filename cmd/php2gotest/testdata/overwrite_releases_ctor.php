<?php
// A stored object whose constructor keeps `$this` has another holder: the
// store that replaces it must not destroy it (overwrite_releases holds the
// cases that do die). In a file of its own because a leaking constructor
// anywhere refuses every anonymous class's object.

class Q { static $quiet = false; }
class K { static $k = []; }

class L {
    function __construct() { K::$k[] = $this; }
    function __destruct() { if (!Q::$quiet) echo "~L\n"; }
}

$arr = [new L];
$arr[0] = 1;
echo "after named (K keeps it)\n";
$arr2 = [new class {
    function __construct() { K::$k[] = $this; }
    function __destruct() { if (!Q::$quiet) echo "~anon\n"; }
}];
$arr2[0] = 2;
echo "after anonymous (K keeps it)\n";
$obj = (object)["p" => new L];
$obj->p = 3;
echo "after property (K keeps it)\n";
$v = new L;
$w = 4;
$v = &$w;
echo "after rebind (K keeps it)\n";
echo "end\n";
Q::$quiet = true;
