<?php
// An element write into an undefined or null container makes it an array,
// unless a type governing it cannot hold one: then it is a TypeError and the
// container is left as it was. A property and a static answer for
// themselves; a variable holding a typed property's reference answers in
// the reference's words. An ordinary write to the same null is untouched.

class Foo {
    public ?string $p;
    public ?iterable $i;
    public ?int $n = null;
    public static ?string $s;
    public static ?array $a;
}

$o = new Foo;
$o->i[] = 1;
var_dump($o->i);
try { $o->p[] = "x"; } catch (TypeError $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
try { var_dump($o->p); } catch (Error $e) { echo $e->getMessage(), "\n"; }
$o->p = null;
try { $o->p["k"] = "x"; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
var_dump($o->p);
$o->p .= "tail";
$o->n++;
var_dump($o->p, $o->n);

Foo::$a["bar"] = 2;
var_dump(Foo::$a);
try { Foo::$s["baz"][] = "baz"; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
try { var_dump(Foo::$s); } catch (Error $e) { echo $e->getMessage(), "\n"; }

$q = new Foo;
$q->p = null;
$ref = &$q->p;
try { $ref[] = "bar"; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
try { $ref["baz"][] = "bar"; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
var_dump($ref);
$ref .= "ok";
var_dump($q->p);
// Reached as an object's container rather than an array's, the null is not
// auto-initialized at all: the property write is refused on null.
$z = new Foo;
$z->p = null;
try { $z->p->x = 1; } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
