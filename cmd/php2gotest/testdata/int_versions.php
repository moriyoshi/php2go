<?php
// A function whose untyped parameter would unbox its loop gets a second body
// that assumes the parameter is an int (lower.IntVersions), entered only after
// a test at the entry. Every call shape here must behave as the general body
// does: the version is reached for a plain int and never otherwise.

function total($n)
{
    $s = 0;
    for ($i = 0; $i < $n; $i++) {
        $s += $i;
    }
    var_dump($s, $i);
}
total(5);
total(5.5);
total("5");
total(true);
total(null);
total(n: 3);
$x = 4;
$r = &$x;
total($x);
call_user_func('total', 2);
array_map('total', [1, 2.5]);
$f = 'total';
$f(6);

// A missing argument takes the default through the general body.
function withDefault($n = 3)
{
    for ($i = 0; $i < $n; $i++) {
    }
    return $i;
}
var_dump(withDefault(), withDefault(7));

// The parameter is reassigned mid-loop, to a float: the bound is not an int
// after that, and the step must not rely on it.
function reassigned($n)
{
    $steps = 0;
    for ($i = PHP_INT_MAX - 3; $i < $n; $i++) {
        if ($i == PHP_INT_MAX - 2) {
            $n = 1e19;
        }
        if (++$steps > 4) {
            break;
        }
        var_dump($i);
    }
    var_dump($n);
}
reassigned(PHP_INT_MAX);

// The version is the same PHP function: one deprecation, the same name in a
// trace, the same arguments.
#[\Deprecated]
function old($n)
{
    for ($i = 0; $i < $n; $i++) {
    }
    return $i;
}
var_dump(old(3));

function traced($n)
{
    for ($i = 0; $i < $n; $i++) {
    }
    throw new Exception("at $i");
}
try {
    traced(2);
} catch (Exception $e) {
    echo $e->getMessage(), "\n", $e->getTraceAsString(), "\n";
}

function extra($n)
{
    for ($i = 0; $i < $n; $i++) {
    }
    var_dump(func_get_args(), func_num_args());
}
extra(2, "more");

// Recursion re-enters through the entry test at every level.
function fib($n)
{
    return $n < 2 ? $n : fib($n - 1) + fib($n - 2);
}
var_dump(fib(15), fib(10.0), fib("12"));

// A version admits only ints within ±2^62, which is what makes `$n - 1` an
// int in it. PHP_INT_MIN - 1 is a float, and an int past the range runs the
// general body.
function neighbours($n)
{
    var_dump($n - 1, $n + 1, 2 + $n);
}
neighbours(5);
neighbours(PHP_INT_MIN);
neighbours(PHP_INT_MAX);
neighbours(1 << 62);
neighbours(-(1 << 62));
neighbours((1 << 62) + 1);

// The range leaves room for a step of less than 2^62 and no more.
function farNeighbours($n)
{
    var_dump($n + 4611686018427387904, $n - 4611686018427387904);
}
farNeighbours(4611686018427387904);
farNeighbours(-4611686018427387904);
farNeighbours(0);

// Methods get versions too: the receiver, the late-static-binding class and
// the parent chain must reach the version exactly as they reach the body.
trait Counts
{
    public function countTo($n)
    {
        $c = 0;
        for ($i = 0; $i < $n; $i++) {
            $c += $this->step;
        }
        return [static::class, $c];
    }
}

class Base
{
    use Counts;

    public $step = 1;
    public $total = 0;

    public function __construct($n = 0)
    {
        for ($i = 0; $i < $n; $i++) {
            $this->total += $i;
        }
    }

    public function add($n)
    {
        for ($i = 0; $i < $n; $i++) {
            $this->total++;
        }
        return $this->total;
    }

    public static function make($n)
    {
        $out = [];
        for ($i = 0; $i < $n; $i++) {
            $out[] = static::class;
        }
        return $out;
    }
}

class Child extends Base
{
    public $step = 2;

    public function add($n)
    {
        for ($i = 0; $i < $n; $i++) {
            $this->total += 10;
        }
        return parent::add($n);
    }
}

$b = new Base(4);
$c = new Child(3);
var_dump($b->add(3), $c->add(2), $c->add("2"), $b->add(1.5));
var_dump($b->countTo(3), $c->countTo(3), $c->countTo(2.5));
var_dump(Base::make(2), Child::make(2), Child::make("1"));
var_dump(call_user_func([$c, 'add'], 1), array_map([$b, 'add'], [1, 2.0]));

// A function whose int version takes every parameter as an int gets an
// unboxed entry, called directly where the arguments are known ints. Every
// other way in has to behave the same.
function pair($a, $b)
{
    if ($a <= 0) {
        return (new Exception("at the bottom"))->getTraceAsString();
    }
    return pair($a - 1, $b + 1);
}
echo pair(2, 10), "\n";
echo pair(1, 2, "extra"), "\n";
echo call_user_func('pair', 1, 5), "\n";
$name = 'pair';
echo $name(1, 7), "\n";
echo pair(1.5, 2), "\n";
echo pair("1", 2), "\n";
echo pair(1, (1 << 62) + 1), "\n";

// Reading its own argument list keeps a function off the unboxed entry.
function counted($x, $y)
{
    for ($i = 0; $i < $x; $i++) {
    }
    return func_get_args();
}
var_dump(counted(2, 3), counted(2, 3, 4));
