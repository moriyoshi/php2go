<?php
// Every instance of a compiled class shares its class's declaration table until
// it declares something of its own, and a class with no constructor anywhere in
// its chain skips constructor dispatch. Each shape here is one where either
// would show: visibility that var_dump and access checks read from the table,
// a subclass shadowing a private, an exception whose engine properties are
// declared before the class's own, a clone, a destructor that must still run.

class Plain
{
    public $a = 1;
    protected $b = 2;
    private $c = 3;
}

class Sub extends Plain
{
    private $c = 30;
    public $d = 4;
}

class Noisy
{
    public $tag;

    public function __destruct()
    {
        echo "destroyed {$this->tag}\n";
    }
}

class Built
{
    public $x;

    public function __construct($x)
    {
        $this->x = $x;
    }
}

class Leaf extends Built
{
    public $y = "leaf";
}

class Failure extends RuntimeException
{
    private $detail = "d";

    public function detail()
    {
        return $this->detail;
    }
}

function access($o, $name)
{
    try {
        return $o->$name;
    } catch (Error $e) {
        return get_class($e) . ": " . $e->getMessage();
    }
}

$p1 = new Plain();
$p2 = new Plain();
$p2->a = 10;
var_dump($p1, $p2);

$s = new Sub();
var_dump($s);

$n = new Noisy();
$n->tag = "n";
unset($n);

$l = new Leaf(5);
var_dump($l);

$f = new Failure("boom");
var_dump($f->detail(), $f->getMessage());

$c = clone $p2;
$c->a = 20;
var_dump($c, $p2);

$dyn = new Plain();
$dyn->extra = "added";
var_dump($dyn, new Plain());

// Last, because a caught Error does not yet give its handle number back in
// compiled output, and every object after it would be numbered differently.
var_dump(access($p1, "b"), access($p1, "c"), access($f, "detail"), access($c, "c"));
echo "end\n";
