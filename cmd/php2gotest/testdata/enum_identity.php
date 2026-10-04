<?php
// An enum case is a **singleton compared by identity**, which shows up in two
// places that have nothing else to do with each other.

enum Foo { case Bar; case Baz; }
enum Q: int { case A = 1; case B = 2; }

// Cloning one is refused: there cannot be a second of it. This is the only
// member of the uncloneable set that is a user-declared class.
echo "clone: ";
try { $c = clone Foo::Bar; var_dump($c); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }

// And two different cases are **uncomparable**, not ordered. They are the same
// class with different `name` properties, so an ordinary object comparison
// would order them by that string.
$bar = Foo::Bar;
$baz = Foo::Baz;
var_dump($bar > $bar, $bar < $bar, $bar >= $bar, $bar <= $bar);
var_dump($bar > $baz, $bar < $baz, $bar >= $baz, $bar <= $baz);
var_dump($bar === $bar, $bar == $bar, $bar === $baz, $bar == $baz);
var_dump(Q::A == Q::A, Q::A === Q::A, Q::A == Q::B);

// A plain object of the same class still compares by its properties.
class C { public $a = 1; }
class D { public $a = 1; }
$c1 = new C; $c2 = new C; $d = new D;
var_dump($c1 == $c2, $c1 >= $c2, $c1 <= $c2, $c1 > $d, $c1 < $d, $c1 >= $d, $c1 <= $d);

// Against a **bool** is where an enum case differs from an ordinary object:
// `$c >= true` is `true == true` and so equal, while `Foo::Bar >= true` is
// false. The boolish rule has to be asked after the enum one, not before.
var_dump($bar > true, $bar < true, $bar >= true, $bar <= true);
var_dump($bar > false, $bar < false, $bar >= false, $bar <= false);
var_dump($c1 > true, $c1 < true, $c1 >= true, $c1 <= true);
var_dump($c1 > null, $c1 < null, $c1 >= null, $c1 <= null);
var_dump(Q::A == 1, Q::A->value == 1);
var_dump(in_array(Foo::Bar, [Foo::Bar, Foo::Baz], true), in_array(Foo::Bar, [Foo::Baz]));
