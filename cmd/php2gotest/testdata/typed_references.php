<?php
// A reference bound to a **typed property** carries that property's
// declaration: every write through any name sharing the box has to satisfy it.
// The constraint travels with the box, not with the property, and it moves when
// the name is rebound. Every answer here came from the engine.

class T { public int $i = 0; public ?string $s = null; public int|string $u = 0; public ?Type $c; }
$t = new T;
$r = &$t->i;
echo "coerce: "; $r = "21"; var_dump($t->i, $r);
echo "refuse: "; try { $r = "x"; } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
echo "after refuse: "; var_dump($t->i);
echo "float: "; $r = 4.0; var_dump($t->i);
echo "nullable: "; $q = &$t->s; $q = 5; var_dump($t->s);
echo "class type: "; $cr = &$t->c; try { $cr = [1]; } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }

// Through another **property** holding the same box.
class A { public int $foo = 1; public $_; }
$a = new A;
$a->_ = &$a->foo;
$a->_ += 1;
var_dump($a->foo);
$a->_ .= "1";
var_dump($a->foo);
try { $a->_ .= "e50"; } catch (Throwable $e) { echo $e->getMessage(), "\n"; }
var_dump($a->foo);

// A typed **static** property types the box too, and rebinding hands the
// declaration over: the box the name used to hold stops being constrained.
class S { public static int $x = 0; }
class S2 extends S { public static $y = 1; }
$x = &S::$x;
try { $x = "foo"; } catch (Throwable $e) { echo $e->getMessage(), "\n"; }
var_dump($x, S::$x);
S::$x = &S2::$y;
$x = "foo";
var_dump($x, S::$x);

// Unsetting the property takes the declaration off the box: it is no longer
// that property's storage, so a write through a name still holding it is fine.
class U {
    private int $prop = 42;
    public function test() {
        $x = &$this->prop;
        unset($this->prop);
        $x = "foo";
        var_dump($x);
    }
}
(new U)->test();
