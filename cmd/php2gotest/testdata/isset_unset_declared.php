<?php
// isset() and empty() on a declared property that unset() removed ask
// __isset, as for a name the object never had; a typed property that was
// never initialised does not.

class Probe
{
    public $plain = 1;
    public int $typed;
    public int $typedUnset = 5;
    public function __construct() { unset($this->plain, $this->typedUnset); }
    public function __isset($n) { echo "__isset($n)\n"; return true; }
    public function __get($n) { echo "__get($n)\n"; return "magic"; }
}
$p = new Probe;
var_dump(isset($p->plain), empty($p->plain));
var_dump(isset($p->typed));
var_dump(isset($p->typedUnset));
var_dump(isset($p->never));

// With __isset saying yes and no __get to ask, empty() counts the property
// empty.
class OnlyIsset
{
    public function __isset($n) { return true; }
}
$q = new OnlyIsset;
var_dump(isset($q->foo), empty($q->foo));

// unset() of a declared property that an earlier unset() removed goes to
// __unset, ahead of the asymmetric-visibility refusal a present one earns.
class Guarded
{
    public private(set) string $bar;
    public function set($v) { $this->bar = $v; }
    public function drop() { unset($this->bar); }
    public function __unset($n) { echo "__unset($n)\n"; }
}
$g = new Guarded;
foreach (['fresh' => null, 'dropped' => 'drop', 'set' => 'set', 'dropped again' => 'drop'] as $label => $step) {
    if ($step) {
        $g->$step('v');
    }
    echo "$label: ";
    try {
        unset($g->bar);
        echo "\n";
    } catch (Error $e) {
        echo $e->getMessage(), "\n";
    }
}
