<?php
// A hooked property reached as **storage** -- `$o->p[] = 1`, `$r = &$o->p`, a
// by-reference argument -- has exactly two answers, and the declaration picks
// between them.

function push(array &$a): void { $a[] = "pushed"; }

class Plain {
    private $backing = ["a"];
    public $p {
        get { echo "  Plain::get\n"; return $this->backing; }
        set { echo "  Plain::set\n"; $this->backing = $value; }
    }
}
$plain = new Plain;
// The read runs **first** and the refusal follows it: the hook's output
// appears before the message.
echo "append:\n";
try { $plain->p[] = 1; } catch (Error $e) { echo "  ", $e->getMessage(), "\n"; }
echo "bind:\n";
try { $r = &$plain->p; } catch (Error $e) { echo "  ", $e->getMessage(), "\n"; }
echo "by-ref argument:\n";
try { push($plain->p); } catch (Error $e) { echo "  ", $e->getMessage(), "\n"; }

class ByRef {
    private $backing = ["a"];
    public $p {
        &get => $this->backing;
        set { $this->backing = $value; }
    }
}
$byRef = new ByRef;
// A `&get` hands back the box its body named, so all three shapes write
// through to the real storage.
$q = &$byRef->p;
$q[] = "bound";
$byRef->p[] = "appended";
push($byRef->p);
var_dump($byRef->p);

// A **virtual** property with only a `set` hook has no get at all, and that is
// a different refusal.
class WriteOnly {
    private $backing;
    public $p { set { $this->backing = $value; } }
}
$writeOnly = new WriteOnly;
try { $writeOnly->p[] = 1; } catch (Error $e) { echo "write-only: ", $e->getMessage(), "\n"; }

// A **backed** property with only a `set` hook still has an implicit get, so
// it reads fine and then refuses like any other hooked property.
class Backed {
    public $p = ["a"] { set { $this->p = $value; } }
}
$backed = new Backed;
try { $backed->p[] = 1; } catch (Error $e) { echo "backed: ", $e->getMessage(), "\n"; }

// Binding **to** a hooked property is refused whatever the hooks are: a
// reference would let a write reach the store without the set hook running.
$other = 5;
try { $plain->p = &$other; } catch (Error $e) { echo "bind-to plain: ", $e->getMessage(), "\n"; }
try { $byRef->p = &$other; } catch (Error $e) { echo "bind-to &get:  ", $e->getMessage(), "\n"; }

// The hooked property never grows a backing field named after itself, which is
// what falling through to the store used to do.
var_dump($plain);

// `unset` of a local drops the program's `&` binding, so what the box lives in
// stops printing one.
$holder = ["x" => 1];
$alias = &$holder["x"];
var_dump($holder);
unset($alias);
var_dump($holder);

// **Inside a hook the property is the raw store**, so every one of these rules
// has to step aside there: `set { $this->p = &$value; }` is a legitimate
// rebinding, not an assignment to an overloaded object.
class Rebinds {
    public $p {
        get => $this->p;
        set { $this->p = &$value; $value = &$this->p; }
    }
}
$rebinds = new Rebinds;
$rebinds->p = 1;
var_dump($rebinds->p);

// **An object needs no storage.** A hooked property holding one is written
// through the handle, so neither of these modifies the property.
class Offsets implements ArrayAccess {
    public $field;
    public function offsetExists(mixed $o): bool { return true; }
    public function offsetGet(mixed $o): mixed { return $o; }
    public function offsetSet(mixed $o, $v): void { echo "  set $o => $v\n"; }
    public function offsetUnset(mixed $o): void { echo "  unset $o\n"; }
}
class HoldsObject {
    public $held { get { return $this->held; } set { $this->held = $value; } }
}
$holds = new HoldsObject;
$holds->held = new Offsets;
$holds->held->field = "through the handle";
var_dump($holds->held->field);
var_dump($holds->held["k"]);
$holds->held["k"] = "v";
unset($holds->held["k"]);
