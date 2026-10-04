<?php
// A hooked property with one accessor refuses the other direction -- but only
// when it is **virtual**. This distinction is the whole content of the rule and
// my first version missed it, regressing five tests that a probe using only
// virtual properties had passed:
//
//   * virtual (no backing store): `set`-only is write-only, `get`-only is
//     read-only.
//   * backed (the hook writes or reads `$this->prop`): the implicit accessor
//     still works, so reading a `set`-only backed property returns the stored
//     value rather than throwing.
class Virt {
    public $readOnly { get { return "computed"; } }
    public $writeOnly { set { echo "set to $value\n"; } }
    public $both { get { return "g"; } set { echo "s\n"; } }
}

$v = new Virt();
echo $v->readOnly, "\n";
try { $v->readOnly = 1; } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
$v->writeOnly = 5;
try { echo $v->writeOnly; } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
echo $v->both, "\n";
$v->both = 1;

// Backed: the hook touches the backing store, so the other direction is the
// implicit accessor and must NOT throw.
class Backed {
    public $prop {
        set { echo "hook set\n"; $this->prop = $value; }
    }
}
$b = new Backed();
$b->prop = 42;
var_dump($b->prop);
