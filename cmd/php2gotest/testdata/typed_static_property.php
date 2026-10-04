<?php
// A typed **static** property coerces on write, exactly as an instance one
// does -- but a static slot is a plain package-level value with nothing on it
// saying what declared it, so the declaration has to be looked up rather than
// asked. Both write paths needed it: `C::$s = v` goes through `sprop.set`, and
// `C::$s += v` goes through `sprop.ref` and a store into the place.
class Foo {
    public static int $i = 0;
    public static string $s = "1";
    public int $ii = 0;
    public string $ss = "1";
}
Foo::$s .= "1";
var_dump(Foo::$s);
Foo::$s += 2;
var_dump(Foo::$s);
Foo::$i .= "5";
var_dump(Foo::$i);
$f = new Foo();
$f->ss .= "1";
$f->ss += 2;
var_dump($f->ss);
$f->ii .= "5";
var_dump($f->ii);
Foo::$s = 42;
var_dump(Foo::$s);
try { Foo::$i = "x"; } catch (TypeError $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }

// Inherited: a subclass shares the parent's slot, and the parent's declaration
// is the one that governs -- including in the message.
class Child extends Foo {}
Child::$s = 7;
var_dump(Foo::$s, Child::$s);
try { Child::$i = "nope"; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }

// An untyped static takes anything and keeps its type.
class Untyped { public static $u = 1; }
Untyped::$u .= "2";
var_dump(Untyped::$u);
Untyped::$u = [1];
var_dump(Untyped::$u);

// ++ and -- go through the same place.
class Counter { public static string $n = "1"; }
Counter::$n++;
var_dump(Counter::$n);
Counter::$n--;
var_dump(Counter::$n);
