<?php
// A property name beginning with NUL is the engine's private namespace --
// where `\0Class\0name` mangles a private slot -- so a computed name that
// reaches it is refused rather than answered.

class Plain { public $ok = 1; }
$p = new Plain;
$n = "\0";
try { var_dump($p->$n); } catch (Error $e) { echo "read:   ", get_class($e), ": ", $e->getMessage(), "\n"; }
try { $p->$n = 1; } catch (Error $e) { echo "write:  ", $e->getMessage(), "\n"; }
try { unset($p->$n); } catch (Error $e) { echo "unset:  ", $e->getMessage(), "\n"; }

// `isset` answers in silence. It is the one access that does not raise.
var_dump(isset($p->$n));

// Only the **first** byte counts. An embedded NUL is an ordinary name that
// merely prints truncated, and an empty name is fine.
$mid = "a\0b";
var_dump(@$p->$mid);
$empty = "";
var_dump(@$p->$empty);

// The refusal comes **after** the magic method for the operation, and it is
// the handler for *this* operation that decides: a class with only `__get`
// answers the read and still refuses the write.
class GetOnly { function __get($x) { echo "  __get(len ", strlen($x), ")\n"; return 7; } }
$g = new GetOnly;
var_dump($g->$n);
try { $g->$n = 1; } catch (Error $e) { echo "getonly-write: ", $e->getMessage(), "\n"; }

// `__unset` answers for exactly what ordinary access could not reach.
class Unsettable {
    public $pub = 1;
    private $priv = 2;
    function __unset($p) { echo "  __unset(", $p, ")\n"; }
    function inside() { unset($this->priv); var_dump(isset($this->priv)); }
}
$u = new Unsettable;
unset($u->pub);        // declared and visible: no hook
var_dump(isset($u->pub));
unset($u->absent);     // absent: hook
unset($u->priv);       // invisible from here: hook
$u->inside();          // visible from inside: no hook
unset($u->priv);       // now absent as well: hook

class Dyn { function __unset($p) { echo "  Dyn::__unset\n"; } }
$d = new Dyn;
@$d->added = 5;
unset($d->added);      // a dynamic property that is present: no hook
var_dump(isset($d->added));

// An error raised by the runtime inside a magic method is positioned where it
// was raised, not where it surfaced -- the frame the method pushed is still on
// the stack there.
class Reentrant { function __get($p) { return $this->$p; } }
$r = new Reentrant;
try { var_dump($r->$n); } catch (Error $e) { echo "inner:  ", $e->getMessage(), "\n"; echo $e->getTraceAsString(), "\n"; }

// The question is asked of the **source** name, not of the slot. A shadowed
// private property is stored under the engine's own "\0Class\0name" key, so
// asking it of the mangled slot would refuse every legitimate access to one.
class Base {
    private int $shadowed = 42;
    public function touch() {
        $x = &$this->shadowed;
        unset($this->shadowed);
        $x = "still reachable";
        var_dump($x);
        $this->shadowed = 7;
        var_dump($this->shadowed);
    }
}
class Derived extends Base { private $shadowed; }
(new Derived)->touch();
