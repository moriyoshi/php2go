<?php
// SplFixedArray, natively: offsets, refusals, shapes, serialization,
// iteration and the overloaded-element rules.
function t($f) {
    try {
        $r = $f();
        var_dump($r);
    } catch (Throwable $e) {
        echo get_class($e), ": ", $e->getMessage(), "\n";
    }
}
class FA extends SplFixedArray { public $p = 1; }
$f = new FA(2);
$f[1] = 'x';
var_dump($f);
print_r($f);
echo "\n";
var_dump((array)$f, json_encode($f));

$a = new SplFixedArray(3);
var_dump($a);
$a[0] = 'x';
$a[2] = [1];
var_dump(count($a), $a->getSize(), $a->toArray());
t(fn() => $a[3]);
t(fn() => $a[-1]);
t(fn() => $a['1']);
t(fn() => $a['x']);
t(fn() => $a['01']);
t(fn() => $a[1.5]);
t(fn() => $a[true]);
t(fn() => $a[null]);
t(fn() => $a[[]]);
t(fn() => $a[new stdClass]);
t(function () use ($a) { $a[3] = 1; });
t(function () use ($a) { $a[] = 1; });
t(fn() => $a->offsetGet());
t(fn() => $a->toArray(1));
var_dump(isset($a[0]), isset($a[1]), isset($a[5]), empty($a[0]));
unset($a[0]);
var_dump($a[0]);
t(fn() => new SplFixedArray(-1));
t(fn() => new SplFixedArray("x"));
print_r(new SplFixedArray());
echo "\n";
foreach ($a as $k => $v) { echo "$k: "; var_dump($v); }
$a->setSize(1);
var_dump($a->toArray());
t(fn() => $a->setSize(-2));
$b = new SplFixedArray(2);
$b[1] = 3;
$b->setSize(4);
var_dump($b->toArray());

$b = SplFixedArray::fromArray([1 => 'a', 3 => 'b']);
var_dump($b->toArray());
$b = SplFixedArray::fromArray([1 => 'a', 3 => 'b'], false);
var_dump($b->toArray());
t(fn() => SplFixedArray::fromArray(['x' => 1]));
t(fn() => SplFixedArray::fromArray([-1 => 1]));
t(fn() => SplFixedArray::fromArray("x"));
class FC extends SplFixedArray {}
var_dump(get_class(FC::fromArray([1])));
$c = clone $b;
$c[0] = 'z';
var_dump($b[0], $c[0], $b == $c);
var_dump(serialize($b));
print_r(unserialize(serialize($b)));
echo "\n";
var_dump((new SplFixedArray(2))->__serialize());

echo "== overloaded elements\n";
$x = new SplFixedArray(2);
$x[0] = 5;
$x[0]++;
$x[1] .= 'q';
var_dump($x->toArray());
$o = new stdClass;
$x[0] = $o;
$r = &$x[0];
var_dump($r === $o);
$x[1] = 1;
$r2 = &$x[1];
$r2 = 9;
var_dump($x[1]);
list($first) = $x;
var_dump($first === $o);

echo "== iteration is live\n";
$a = new SplFixedArray(3);
$a[0] = 1; $a[1] = 2; $a[2] = 3;
foreach ($a as $k => $v) { echo "$k=$v\n"; if ($k == 0) $a->setSize(2); }
foreach ($a as $k => $v) { echo "$k=$v\n"; if ($k == 0) $a[1] = 'changed'; }
class FB extends SplFixedArray { function __construct() {} }
$g = new FB;
var_dump(count($g), $g->toArray());
t(fn() => $g[0]);
foreach ($g as $v) echo "never\n";
function gen() { list($value) = yield; var_dump($value); }
$fixed = new SplFixedArray(1);
$fixed[0] = 'the element';
gen()->send($fixed);
