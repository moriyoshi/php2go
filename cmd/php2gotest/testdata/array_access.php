<?php
class C implements ArrayAccess {
    public function offsetExists($o): bool { echo "exists:"; var_dump($o); return $o === "a"; }
    public function offsetGet($o): mixed { echo "get:"; var_dump($o); return 41; }
    public function offsetSet($o, $v): void { echo "set:"; var_dump($o, $v); }
    public function offsetUnset($o): void { echo "unset:"; var_dump($o); }
}
$c = new C;
$c["a"] = 1;
$c[] = 2;
var_dump($c["a"]);
var_dump(isset($c["a"]));
var_dump(isset($c["zz"]));
var_dump($c["zz"] ?? "d");
var_dump($c["a"] ?? "d");
var_dump(empty($c["a"]));
unset($c["a"]);
$c[3] += 1;
class N {}
$n = new N;
try { var_dump($n["x"]); } catch (\Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
try { $n["x"] = 1; } catch (\Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
echo "done\n";
