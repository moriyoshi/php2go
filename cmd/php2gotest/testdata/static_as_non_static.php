<?php
// The notice names the INSTANCE's class, and visibility does not enter into it.
class A {
    private   static $a = "a";
    protected static $b = "b";
    public    static $c = "c";
    public function __construct() { var_dump($this->a, $this->b, $this->c); }
}
class B extends A {}
new B;
// It fires on every loud read, including one that finds a dynamic property a
// write of the same name created.
class D { public static $s = 1; }
$d = new D;
$d->s = 4;
var_dump($d->s);
var_dump($d->s, D::$s);
// Silent reads say nothing.
var_dump(isset($d->s), $d->s ?? "x");
class E { public static $t = 1; }
$e = new E;
var_dump(isset($e->t), $e->t ?? "x");
// __get wins over the notice.
class C { public static $s = 1; public function __get($n) { echo "__get($n)\n"; return 7; } }
var_dump((new C)->s);
// A declared instance property is untouched by any of it.
class F { public static $s = 1; public $t = 2; }
var_dump((new F)->t);
echo "done\n";
