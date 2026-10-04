<?php
// Releasing an object whose class has no `__destruct` anywhere in a chain of
// compiled classes skips the class-table question (Object.NoDestructor). Each
// shape here is one where the flag must not be set: a destructor that arrives
// from a subclass, a trait, or two levels up, and a class whose chain reaches
// an engine class.

trait Noisy
{
    public function __destruct() { echo "trait dtor ", static::class, "\n"; }
}

class Quiet
{
    public $n;
    public function __construct($n) { $this->n = $n; }
}

class Loud extends Quiet
{
    public function __destruct() { echo "dtor {$this->n}\n"; }
}

class ViaTrait
{
    use Noisy;
}

class Grand extends Loud
{
}

class MyError extends RuntimeException
{
}

function churn()
{
    for ($i = 0; $i < 2; $i++) {
        $q = new Quiet($i);
        $l = new Loud("L$i");
        $t = new ViaTrait();
        $g = new Grand("G$i");
        $e = new MyError("e$i");
    }
    echo "end of churn\n";
}
churn();
echo "done\n";
