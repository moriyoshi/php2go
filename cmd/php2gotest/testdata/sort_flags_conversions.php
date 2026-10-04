<?php
class X {}
function t($f) { try { $f(); } catch (Error $e) { echo $e->getMessage(), "\n", $e->getTraceAsString(), "\n"; } }
$a = [10, 9, "2", "1e1", "abc", "B"]; sort($a, SORT_STRING); echo implode(",", $a), "\n";
$a = [10, 9, "2", "1e1"]; sort($a, SORT_NUMERIC); echo implode(",", $a), "\n";
$a = ["img12", "img10", "IMG2", "img1"]; sort($a, SORT_NATURAL); echo implode(",", $a), "\n";
$a = ["img12", "img10", "IMG2", "img1"]; sort($a, SORT_NATURAL | SORT_FLAG_CASE); echo implode(",", $a), "\n";
$a = ["b", "A", "c"]; sort($a, SORT_STRING | SORT_FLAG_CASE); echo implode(",", $a), "\n";
$a = ["x" => "10", "y" => "9"]; asort($a, SORT_STRING); echo json_encode($a), "\n";
$a = ["x" => "10", "y" => "9"]; arsort($a, SORT_STRING); echo json_encode($a), "\n";
$a = ["10" => 1, "9" => 2, "a" => 3]; ksort($a, SORT_STRING); echo json_encode($a), "\n";
$a = [10, 9, 2]; rsort($a, SORT_STRING); echo implode(",", $a), "\n";
$a = [3, "3", 1]; echo implode(",", array_unique($a)), "\n";
t(function () { $a = [new X, new X]; sort($a, SORT_STRING); });
t(function () { $a = [new X, new X]; usort($a, fn($x, $y) => strcmp($x, $y)); });
t(function () { $a = [new X]; array_map(fn($x) => (string)$x, $a); });
t(function () { $a = [new X, new X]; array_unique($a); });
t(function () { $a = [new X, "b"]; implode(",", $a); });
t(function () { $a = [new X, new X]; asort($a, SORT_STRING); });
t(function () { $a = ["k" => new X]; array_flip($a); echo "flip ok\n"; });
class S { function __construct(public $v) {} function __toString(): string { echo "ts({$this->v}) "; return $this->v; } }
$a = [new S("b"), new S("a")]; sort($a, SORT_STRING); echo "\n", $a[0]->v, $a[1]->v, "\n";
echo implode("-", [new S("q"), [1], 2.5, true, null]), "\n";
$u = array_unique([new S("k"), new S("k"), "k"]); echo "\n", count($u), "\n";
$a = ["B" => 1, "a" => 2, "C" => 3]; ksort($a, SORT_STRING | SORT_FLAG_CASE); echo json_encode($a), "\n";
$a = ["x10", "x9", "X1"]; natcasesort($a); echo json_encode($a), "\n";
$a = [1.5, "1.5", "01.5", 1]; sort($a, SORT_STRING); echo json_encode($a), "\n";
$a = [[1], [], 0]; sort($a, SORT_NUMERIC); echo json_encode($a), "\n";
$a = [3, 1, 2]; sort($a, 99); echo json_encode($a), "\n";
