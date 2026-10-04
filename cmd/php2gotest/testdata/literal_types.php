<?php
// PHP 8.2's literal types. `null`, `true` and `false` each admit **exactly one
// value** and coerce nothing -- reading them as a flavour of `bool` is the
// tempting mistake, and it is wrong in the silent direction: a `bool`
// declaration turns `1` into `true`, a `false` declaration refuses it.
//
// Every message here also names the offending value with `true`/`false` rather
// than `bool`, in all three shapes -- property, argument and return.

class Lit
{
    public null $n = null;
    public false $f = false;
    public true $t = true;
    public int $i = 0;
}

// Written out rather than looped: a dynamic property name is a separate gap,
// and this fixture is about the types.
$o = new Lit;
try { $o->n = 1; } catch (\TypeError $e) { echo $e->getMessage(), "\n"; }
try { $o->n = null; var_dump($o->n); } catch (\TypeError $e) { echo $e->getMessage(), "\n"; }
try { $o->f = true; } catch (\TypeError $e) { echo $e->getMessage(), "\n"; }
try { $o->f = false; var_dump($o->f); } catch (\TypeError $e) { echo $e->getMessage(), "\n"; }
try { $o->t = false; } catch (\TypeError $e) { echo $e->getMessage(), "\n"; }
try { $o->t = true; var_dump($o->t); } catch (\TypeError $e) { echo $e->getMessage(), "\n"; }
try { $o->i = true; var_dump($o->i); } catch (\TypeError $e) { echo $e->getMessage(), "\n"; }

function wantsFalse(false $v): false { return $v; }
function returnsWrong(): true { return false; }

var_dump(wantsFalse(false));
try {
    wantsFalse(true);
} catch (\TypeError $e) {
    echo $e->getMessage(), "\n";
}
try {
    returnsWrong();
} catch (\TypeError $e) {
    echo $e->getMessage(), "\n";
}

// A `= null` default makes the parameter implicitly nullable, even for a type
// that cannot otherwise hold null. 8.4 deprecates the spelling and still
// accepts the value.
function implicitly(false $v = null) { return $v; }
var_dump(implicitly(false));
var_dump(implicitly(null));
var_dump(implicitly());
