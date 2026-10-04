<?php
// **Which class a diagnostic names is a per-message fact, not a rule.** Having
// found three places that must name the class that *declared* a member, the
// tempting generalisation is "always the declaring class" -- and it is wrong
// for at least three others, which name the instance's. Both halves are pinned
// here so neither drifts.

class Base
{
    public readonly int $ro;
    public int $typed;
    private $hidden = 1;

    public function initRo()
    {
        $this->ro = 1;
    }
}

class Derived extends Base
{
}

// ---- the DECLARING class ----

$d = new Derived;
try {
    $d->ro = 5;
} catch (\Error $e) {
    echo $e->getMessage(), "\n";
}
$d->initRo();
try {
    $d->ro = 6;
} catch (\Error $e) {
    echo $e->getMessage(), "\n";
}
try {
    unset($d->ro);
} catch (\Error $e) {
    echo $e->getMessage(), "\n";
}
try {
    echo $d->typed;
} catch (\Error $e) {
    echo $e->getMessage(), "\n";
}

// ---- the INSTANCE's class ----

// A private the subclass cannot see is *undefined* on the subclass, and the
// warning names the instance's class rather than the declaring one.
var_dump($d->hidden);

class Plain
{
    public $x = 1;
}
class Sub extends Plain
{
}

$s = new Sub;
try {
    echo (string) $s;
} catch (\Error $e) {
    echo $e->getMessage(), "\n";
}
try {
    count($s);
} catch (\TypeError $e) {
    echo $e->getMessage(), "\n";
}
