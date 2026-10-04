<?php
// A by-reference foreach whose variable is handed a copy of the array it
// walks -- `+=`, `+`, a separated copy handed back, a copy of a copy -- goes
// on where it was (the engine's iterator copies, GH-11171); a literal or a
// copy of another array starts again. Every loop is bounded, so a regression
// prints LOOP instead of hanging.
function run($name, $f) { echo "== $name\n"; $f(); }
run('plus-assign', function() {
  $a = [1,2,3]; $n = 0;
  foreach ($a as $k => &$v) { echo "$k "; $a += [5 => 9]; if (++$n > 8) { echo "LOOP"; break; } }
  echo "\n", count($a), "\n";
});
run('plus-expr', function() {
  $a = [1,2,3]; $n = 0;
  foreach ($a as $k => &$v) { echo "$k "; $a = $a + [7 => 8]; if (++$n > 8) { echo "LOOP"; break; } }
  echo "\n";
});
run('array_merge', function() {
  $a = [1,2,3]; $n = 0;
  foreach ($a as $k => &$v) { echo "$k "; $a = array_merge($a, []); if (++$n > 8) { echo "LOOP"; break; } }
  echo "\n";
});
run('self-assign', function() {
  $a = [1,2,3]; $n = 0;
  foreach ($a as $k => &$v) { echo "$k "; $a = $a; if (++$n > 8) { echo "LOOP"; break; } }
  echo "\n";
});
run('via-b', function() {
  $a = [1,2,3]; $n = 0;
  foreach ($a as $k => &$v) { echo "$k "; $b = $a; $a = $b + []; if (++$n > 8) { echo "LOOP"; break; } }
  echo "\n";
});
run('append', function() {
  $a = [1,2,3]; $n = 0;
  foreach ($a as $k => &$v) { echo "$k "; if ($n < 2) $a[] = 'x'; if (++$n > 8) { echo "LOOP"; break; } }
  echo "\n";
});
run('copy-of-copy', function() {
  $a = [1,2,3,4]; $n = 0;
  foreach ($a as $k => &$v) { echo "$k "; $a = ($a + []) + [9 => 1]; if (++$n > 8) { echo "LOOP"; break; } }
  echo "\n";
});
run('literal', function() {
  $a = [1,2,3]; $n = 0;
  foreach ($a as $k => &$v) { echo "$k "; if ($n == 1) $a = [10, 20, 30, 40]; if (++$n > 8) { echo "LOOP"; break; } }
  echo "\n";
});
run('copy-then-literal-of-other', function() {
  $a = [1,2,3]; $o = [4,5,6]; $n = 0;
  foreach ($a as $k => &$v) { echo "$k "; if ($n == 0) $a = $o + []; if (++$n > 8) { echo "LOOP"; break; } }
  echo "\n";
});
run('plus-self', function() {
  $a = [1,2,3]; $n = 0;
  foreach ($a as $k => &$v) { echo "$k "; $a += $a; if (++$n > 8) { echo "LOOP"; break; } }
  echo "\n";
});
run('unset-elem-copy', function() {
  $a = [1,2,3,4]; $n = 0;
  foreach ($a as $k => &$v) { echo "$k "; if ($n == 0) { $b = $a; unset($b[3]); $a = $b; } if (++$n > 8) { echo "LOOP"; break; } }
  echo "\n";
});
run('orig', function() {
  $all = ['test']; 
  foreach ($all as &$item) { $all += [$item]; }
  var_dump($all);
});
// array_merge with one empty side and array_values of a list hand back the
// array itself (RETURN_COPY), so the walk goes on; array_merge of one array
// and array_slice build a new one, which starts again.
function walk($name, $f) { echo "$name: "; $f(); echo "\n"; }
walk('merge-one', function() { $a=[1,2,3]; $n=0; foreach ($a as $k=>&$v) { echo "$k "; if ($n==0) $a = array_merge($a, [7]); if (++$n>8) {echo "LOOP";break;} } });
walk('merge-only', function() { $a=[1,2,3]; $n=0; foreach ($a as $k=>&$v) { echo "$k "; $a = array_merge($a); if (++$n>4) {echo "LOOP";break;} } });
walk('merge-str', function() { $a=['x'=>1,'y'=>2,'z'=>3]; $n=0; foreach ($a as $k=>&$v) { echo "$k "; $a = array_merge($a, []); if (++$n>8) {echo "LOOP";break;} } });
walk('merge-hole', function() { $a=[1,2,3,4]; unset($a[3]); $n=0; foreach ($a as $k=>&$v) { echo "$k "; $a = array_merge($a, []); if (++$n>8) {echo "LOOP";break;} } });
walk('merge-emptyfirst', function() { $a=[1,2,3]; $n=0; foreach ($a as $k=>&$v) { echo "$k "; $a = array_merge([], $a); if (++$n>8) {echo "LOOP";break;} } });
walk('slice', function() { $a=[1,2,3]; $n=0; foreach ($a as $k=>&$v) { echo "$k "; $a = array_slice($a, 0); if (++$n>4) {echo "LOOP";break;} } });
walk('values', function() { $a=[1,2,3]; $n=0; foreach ($a as $k=>&$v) { echo "$k "; $a = array_values($a); if (++$n>8) {echo "LOOP";break;} } });
// The internal pointer travels with the array handed back as itself.
$a=[1,2,3]; next($a); $b = array_merge($a, []); var_dump(current($b));
$a=['x'=>1,'y'=>2]; next($a); $b = array_merge([], $a); var_dump(current($b));
$a=[1,2,3]; next($a); $b = array_merge($a); var_dump(current($b));
$a=[1,2,3]; next($a); $b = array_values($a); var_dump(current($b));
$a=[5=>1,6=>2]; next($a); $b = array_values($a); var_dump(current($b));
$a=[1,2,3]; unset($a[2]); next($a); $b = array_values($a); var_dump(current($b));
$a=[1,2,3]; next($a); $b = array_merge($a, [], []); var_dump(current($b));
$a=['x'=>1, 0=>2]; next($a); $b = array_merge($a, []); var_dump(current($b));
// ... and the copy is still a value: writing one side leaves the other alone.
$a=[1,2,3]; $b = array_merge($a, []); $b[] = 4; $a[0] = 9; var_dump($a, $b);
$c = array_values($a); $c[1] = 'c'; var_dump($a[1], $c[1]);
try { array_merge([1], 5); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
try { array_merge([], 5); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
// A copy through `+` drops deleted buckets, and the position lands on the same
// element; a copy of an array that separated from the walked one first still
// counts; a copy made before the walk moved to another copy is stale (the
// engine deletes the other copies) and starts again; a copy rebuilt before it
// is handed back carries the moved position.
function walk2($name, $f) { echo "$name: "; $f(); echo "\n"; }
walk2('plus-after-hole', function() { $a=[1,2,3,4,5]; unset($a[1]); $n=0; foreach ($a as $k=>&$v) { echo "$k "; if ($k == 2) $a += [9=>9]; if (++$n>9) {echo "LOOP";break;} } });
walk2('separate-then-copy', function() { $a=[1,2,3,4]; $n=0; foreach ($a as $k=>&$v) { echo "$k "; if ($n == 1) { $b = $a; $a[] = 'x'; $a = $a + []; } if (++$n>9) {echo "LOOP";break;} } });
walk2('stale-copy', function() { $a=[1,2,3,4]; $n=0; foreach ($a as $k=>&$v) { echo "$k "; if ($n == 0) { $b = $a + []; $a = $a + [7=>7]; } if ($n == 2) { $a = $b; } if (++$n>9) {echo "LOOP";break;} } });
walk2('copy-unshift', function() { $a=[1,2,3]; $n=0; foreach ($a as $k=>&$v) { echo "$k "; if ($n == 0) { $b = $a + []; array_unshift($b, 0); $a = $b; } if (++$n>9) {echo "LOOP";break;} } });
walk2('copy-then-shared', function() { $a=[1,2,3]; $n=0; foreach ($a as $k=>&$v) { echo "$k "; if ($n == 0) { $b = $a + []; $c = $b; $a = $c; } if (++$n>9) {echo "LOOP";break;} } });
// The walked array separating keeps its walk live on the new elements, so a
// copy made later carries where the walk is then, not where it was.
walk2('separate-advance-copy', function() { $a=[1,2,3,4,5]; $n=0; foreach ($a as $k=>&$v) { echo "$k "; if ($n == 0) { $b = $a; $a[] = 'x'; } if ($n == 2) { $a = $a + []; } if (++$n>9) {echo "LOOP";break;} } });
// array_values of a string-keyed array is a new list.
$a=['x'=>1,'y'=>2]; next($a); $b = array_values($a); var_dump(current($b));
