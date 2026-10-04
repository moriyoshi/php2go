<?php
// Assigning a static whose slot holds another typed static's reference must
// satisfy every type the reference carries -- refused in the reference's
// words, nothing written -- and binding an undefined variable to a nullable
// typed static brings it into being as null.

class A {
    static iterable $it = [];
    static ?array $a;
    static int $i = 1;
    static $u;
}
A::$a = &A::$it;
try {
    A::$it = new ArrayIterator();
} catch (TypeError $e) { var_dump($e->getMessage()); }
var_dump(A::$it);
A::$it = [1];
var_dump(A::$a);

A::$a = &$a;
var_dump($a);
try { $a = 1; } catch (TypeError $e) { var_dump($e->getMessage()); }

A::$u = &A::$i;
try { A::$u = "x"; } catch (TypeError $e) { var_dump($e->getMessage()); }
A::$u = "7";
var_dump(A::$i);

// The static's own type is asked first, in its own words.
try { A::$u = &A::$i; A::$i = null; } catch (TypeError $e) { var_dump($e->getMessage()); }
var_dump(A::$i);
