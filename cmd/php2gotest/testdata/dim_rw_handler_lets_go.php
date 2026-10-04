<?php
// A read-write fetch of a missing key holds its array while the warning's
// error handler runs. A handler that assigns the variable, or writes to the
// array, makes the fetch give up: nothing is written back, the operation is
// not computed, and a compound assignment evaluates to NULL.

// The handler writes the key itself (bug70662).
$a = [];
set_error_handler(function () use (&$a) { $a['b'] = 2; });
$r = ($a['b'] += 1);
var_dump($a, $r);

// The same over an array nothing else shares, which the handler's write
// would otherwise change in place.
$u = [];
$u['z'] = 0;
set_error_handler(function () use (&$u) { $u['b'] = 2; });
$r = ($u['b'] += 1);
var_dump($u, $r);

// An int key on a packed array, which has a fast write-back of its own.
$p = [];
set_error_handler(function () use (&$p) { $p[0] = 2; });
$r = ($p[0] += 1);
var_dump($p, $r);

// Through $GLOBALS, so the variable is no reference.
$q = [];
set_error_handler(function () { $GLOBALS['q'][0] = 2; });
$r = ($q[0] += 1);
var_dump($q, $r);

// The handler assigns the variable, over null (bug78598).
$v = null;
set_error_handler(function () use (&$v) { $v = 0; });
$r = ($v[0] .= "xyz");
var_dump($v, $r);
$v = null;
$r = ($v["foo"] .= "xyz");
var_dump($v, $r);

// Nested: the first fetch gives up, and the rest go on in a temporary --
// warning each time, computing, writing nowhere anyone sees.
$v = null;
set_error_handler(function ($n, $m) use (&$v) { echo "H: $m\n"; $v = 0; });
$r = ($v[0][1][2] .= "xyz");
var_dump($v, $r);

// Nested: the handler replaces the root at the last key.
$e = [[]];
set_error_handler(function ($n, $m) use (&$e) { echo "H: $m\n"; $e = 5; });
$r = ($e[0]['x'] .= "q");
var_dump($e, $r);

// Nested: the handler replaces the element holding the array.
$e = [[]];
set_error_handler(function ($n, $m) use (&$e) { echo "H: $m\n"; $e[0] = 7; });
$r = ($e[0]['x'] .= "q");
var_dump($e, $r);

// The handler assigns another array.
$c = [];
set_error_handler(function () use (&$c) { $c = [9]; });
$r = ($c['x'] .= "q");
var_dump($c, $r);

// The operation is not computed: no TypeError, no __toString, no division.
class S { function __toString(): string { echo "toString\n"; return "s"; } }
$a = [];
set_error_handler(function () use (&$a) { $a = 5; });
$r = ($a['x'] += []);
var_dump($a, $r);
$a = [];
$r = ($a['x'] .= new S);
var_dump($a, $r);
$a = [];
$r = ($a['x'] /= 0);
var_dump($a, $r);

// Increments step the NULL the fetch read, and write nowhere.
$a = [];
$r = $a['x']++;
var_dump($a, $r);
$a = [];
$r = ++$a['x'];
var_dump($a, $r);

// A property holding the array.
$o = new stdClass;
$o->p = [];
set_error_handler(function () use ($o) { $o->p = 3; });
$r = ($o->p['x'] .= "q");
var_dump($o->p, $r);

// Handlers that let go of nothing: the fetch goes on.
$b = ['y' => 1];
set_error_handler(function () { echo "warned\n"; });
$r = ($b['x'] .= "q");
var_dump($b, $r);
$f = [];
set_error_handler(function () use (&$f) { $t = $f; $t[] = 1; });
$r = ($f['x'] .= "q");
var_dump($f, $r);
$d = [];
set_error_handler(function () use (&$d) { unset($d); });
$r = ($d['x'] .= "q");
var_dump($d, $r);
$g = [[1]];
set_error_handler(function () use (&$g) { $g['other'] = 1; });
$r = ($g[0]['x'] .= "q");
var_dump($g, $r);

// A handler that throws: nothing is written, and the exception arrives.
$t = ['k' => 1];
set_error_handler(function ($n, $m) { throw new ErrorException($m); });
try {
    $t['x'] .= "q";
} catch (ErrorException $ex) {
    echo get_class($ex), ": ", $ex->getMessage(), "\n";
}
var_dump($t);

// Captured by reference and null, with no handler: autovivified first, so
// the missing key is what warns.
restore_error_handler(); restore_error_handler(); restore_error_handler();
restore_error_handler(); restore_error_handler(); restore_error_handler();
restore_error_handler(); restore_error_handler(); restore_error_handler();
restore_error_handler(); restore_error_handler(); restore_error_handler();
$w = null;
$keep = function () use (&$w) {};
$w[0][1] .= "x";
var_dump($w);
