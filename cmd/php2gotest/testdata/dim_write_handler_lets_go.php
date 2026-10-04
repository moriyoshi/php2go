<?php
// A dimension write holds its container across its own diagnostics, and an
// error handler that lets go of it there -- reassigns the variable, unsets
// it, replaces the array a nested chain came from -- makes the write give up
// and evaluate to NULL. A CV key or value is read inside the write, after
// the container is decided (false deprecates first, a chain is fetched
// first). A compound assignment on a variable reads its value first.

$mode = 0;
set_error_handler(function ($c, $m) {
    global $mode;
    echo "Err: $m\n";
    switch ($mode) {
        case 1: $GLOBALS['s'] = "xyz"; break;
        case 2: $GLOBALS['s'] = [9]; break;
        case 4: $x = $GLOBALS['s']; break;
        case 5: unset($GLOBALS['s']); break;
        case 6: echo "sees: "; var_dump($GLOBALS['s']); break;
        case 7: throw new Exception("from handler");
    }
});

function show($label, $r) {
    global $s;
    echo "-- $label: ";
    var_dump($r);
    var_dump($s ?? "(unset)");
}

echo "== compound assignment reads the value first\n";
$a .= $y;
var_dump($a);
$b += $z;
var_dump($b);
$c[$k] .= $w;
var_dump($c);

echo "== undefined key\n";
foreach ([0, 1, 2, 5] as $m) {
    $s = "abc"; $mode = $m; unset($u); $r = ($s[$u] = 'Q'); $mode = 0; show("string mode $m", $r);
    $s = [1]; $mode = $m; $r = ($s[$u] = 'Q'); $mode = 0; show("array mode $m", $r);
    $s = false; $mode = $m; $r = ($s[$u] = 'Q'); $mode = 0; show("false mode $m", $r);
    $s = null; $mode = $m; $r = ($s[$u] = 'Q'); $mode = 0; show("null mode $m", $r);
    unset($s); $mode = $m; $r = ($s[$u] = 'Q'); $mode = 0; show("unset mode $m", $r);
}
$s = "abc"; $mode = 4; $r = ($s[$u] = 'Q'); $mode = 0; show("string, dead local copy", $r);

echo "== what the handler sees\n";
$s = null; $mode = 6; $s[$u] = 1; $mode = 0;
$s = false; $mode = 6; $s[$u] = 1; $mode = 0;
$s = [1]; $mode = 6; $s[5] = $w; $mode = 0;

echo "== conversions of a constant key\n";
foreach ([1, 2, 5] as $m) {
    $s = [1]; $mode = $m; $r = ($s[1.5] = 'Q'); $mode = 0; show("float key mode $m", $r);
    $s = "abc"; $mode = $m; $r = ($s[null] = 'Q'); $mode = 0; show("null offset mode $m", $r);
    $s = "abc"; $mode = $m; $r = ($s["1x"] = 'Q'); $mode = 0; show("'1x' offset mode $m", $r);
}
$s = "abc"; $mode = 1; $r = ($s[0] = "QQ"); $mode = 0; show("multibyte", $r);
$s = "abc"; $mode = 1; $r = ($s[-9] = "Q"); $mode = 0; show("negative", $r);

echo "== undefined value\n";
$s = [1]; $mode = 1; $r = ($s[5] = $w); $mode = 0; show("array", $r);
$s = "abc"; $mode = 1; try { $r = ($s[0] = $w); } catch (Error $e) { echo $e->getMessage(), "\n"; } $mode = 0; show("string", $r);
$s = "abc"; $mode = 1; $r = ($s[$u] = $w); $mode = 0; show("string, key first", $r);
$s = [1]; $mode = 1; $r = ($s[$u] = $w); $mode = 0; show("array, key first", $r);
$s = "abc"; try { $s[$u] = $w; } catch (Error $e) { echo $e->getMessage(), "\n"; } show("string, both undefined", null);
$s = [1]; $s[$u] = $w; show("array, both undefined", null);
function copies($k) { $t = [1]; $t[$k] = $undefinedArray; $t[$k2] = $t; return $t; }
var_dump(copies(3));

echo "== nested\n";
$s = [[1]]; $mode = 1; $r = ($s[0][$u] = 7); $mode = 0; show("array element", $r);
$s = ['ab']; $mode = 1; $r = ($s[0][$u] = 'Q'); $mode = 0; show("string element", $r);
$s = []; $mode = 1; $r = ($s['x'][$u] = 7); $mode = 0; show("missing element", $r);
$s = [false]; $mode = 1; $r = ($s[0][$u] = 7); $mode = 0; show("false element", $r);
$s = [[[1]]]; $mode = 1; $r = ($s[0][0][$u] = 7); $mode = 0; show("three levels", $r);
$s = [[1]]; $mode = 1; $r = ($s[0][1.5] = 7); $mode = 0; show("float key in element", $r);
$s = [[1]]; $mode = 0; $r = ($s[0][$u] = 7); show("element, handler lets go of nothing", $r);

echo "== a handler that throws\n";
$mode = 7;
$s = false; try { $s[$u] = 1; } catch (Exception $e) { echo "caught\n"; } show("false", null);
$s = [1]; try { $s[$u] = 1; } catch (Exception $e) { echo "caught\n"; } show("array key", null);
$s = [1]; try { $s[1.5] = 1; } catch (Exception $e) { echo "caught\n"; } show("float key", null);
$s = "abc"; try { $s[$u] = 'Q'; } catch (Exception $e) { echo "caught\n"; } show("string key", null);
$s = [1]; try { $s[5] = $w; } catch (Exception $e) { echo "caught\n"; } show("array value", null);
$mode = 0;

echo "== an object is held, not the variable\n";
$s = new ArrayObject([]); $o = $s; $mode = 1; $r = ($s[$u] = 'Q'); $mode = 0;
var_dump($r, $s, $o->getArrayCopy());

echo "== a global in a function\n";
function g() { global $s; $s[$GLOBALS['nothing'] ?? $undefinedHere] = 1; return $s; }
$s = [1]; $mode = 1; var_dump(g()); $mode = 0; show("global", null);

echo "== a property holding the array\n";
class P { public $arr = [1]; }
$p = new P;
set_error_handler(function ($c, $m) use ($p) { echo "Err: $m\n"; $p->arr = "gone"; });
$r = ($p->arr[$u] = 5);
var_dump($r, $p->arr);

echo "== the three targets\n";
set_error_handler(function ($code, $msg) { echo "Err: $msg\n"; $GLOBALS['t'] = null; });
$t[$y] = $t .= ($y);
var_dump($t);
set_error_handler(function ($code, $msg) { echo "Err: $msg\n"; $GLOBALS['t'] = ''; });
$t = ['a'];
$t[0][$d] = 'b';
var_dump($t);
$t = [!'a'];
$t[0][$d] = 'b';
var_dump($t);
