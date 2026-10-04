<?php
namespace ns;

// Names, callables and destructuring, each measured against the engine.

// One leading backslash is the fully-qualified spelling of a name.
const OK = 1;
function here() {}
var_dump(defined('\ns\OK'), defined('ns\OK'), defined('\ns\NOPE'));
var_dump(constant('\ns\OK'), function_exists('\ns\here'), function_exists('\strlen'));
var_dump(is_callable('\strlen'), is_callable('\\\\strlen'), is_callable('\ns\here'));
var_dump(call_user_func('defined', '\ns\OK'), call_user_func('constant', '\ns\OK'));
try {
    constant('\ns\NOPE');
} catch (\Error $e) {
    echo $e->getMessage(), "\n";
}

// Visibility of a callable is judged from the calling scope, and a static
// callable to an instance method is fine where a compatible $this is.
class A
{
    protected static function prot() {}
    private static function priv() {}
    public function inst() {}
}
class B extends A
{
    static function fromB()
    {
        var_dump(is_callable('ns\A::prot'), is_callable('ns\A::priv'));
    }
    function inside()
    {
        var_dump(is_callable(['ns\B', 'inst']), is_callable(['ns\A', 'inst']));
        var_dump(array_map([$this, 'double'], [1, 2]));
    }
    private function double($x) { return $x * 2; }
}
var_dump(is_callable('ns\A::prot'), is_callable(['ns\B', 'inst']));
B::fromB();
(new B)->inside();

// A Closure has __invoke.
var_dump(method_exists(function () {}, '__invoke'), method_exists(function () {}, 'nope'));

// array_slice(func_get_args(), N) is folded into one opcode.
function tail()
{
    return [array_slice(func_get_args(), 1), array_slice(func_get_args(), 5), func_get_args()];
}
var_dump(tail(1, 2, 3));

// Destructuring reads nothing from a value that is not a container.
[$a, $b] = "str";
[$c] = null;
[$d] = 5;
[[$e]] = [7];
[$f, $g] = [1];
list('k' => $h) = true;
[$i] = new \ArrayObject([9]);
var_dump($a, $b, $c, $d, $e, $f, $g, $h, $i);
foreach (["ab", [3, 4]] as [$x, $y]) {
    var_dump($x, $y);
}

// A class named at run time is spelled however the program likes.
class FooBar
{
    public static $prop = 42;
    const K = "k";
    static function m() { return "m"; }
}
$lower = "ns\\foobar";
var_dump($lower::$prop, $lower::K, $lower::m());
$missing = 'ns\Undefined::method';
try {
    $missing();
} catch (\Error $e) {
    echo $e->getMessage(), "\n";
}

// A typed property unset() removed is __get's to answer, and the answer must
// fit the type.
class Typed
{
    public int $n;
    public int $m;
    public function __get($name)
    {
        echo "__get($name)\n";
        return $name === 'n' ? "5" : null;
    }
}
$t = new Typed;
try {
    var_dump($t->n);
} catch (\Error $e) {
    echo $e->getMessage(), "\n";
}
unset($t->n, $t->m);
var_dump($t->n);
try {
    var_dump($t->m);
} catch (\TypeError $e) {
    echo $e->getMessage(), "\n";
}

// An engine class's constants answer.
var_dump(\ArrayObject::STD_PROP_LIST, \ArrayObject::ARRAY_AS_PROPS);
$engine = 'ArrayObject';
var_dump($engine::STD_PROP_LIST, $engine::ARRAY_AS_PROPS);
