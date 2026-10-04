<?php
// A `readonly class` is not a feature of its own: the engine marks every
// declared property readonly, which the property flags already carry. The one
// thing they do not carry is that a **dynamic** property becomes an Error here
// rather than the 8.2 deprecation.

readonly class Point {
    public function __construct(public int $x, public string $label) {}
    public function shifted(int $by): static { return new static($this->x + $by, $this->label); }
}

// An ordinary class only deprecates the same write.
class Loose { public int $n = 1; }
$l = new Loose;
$l->added = "dynamic";
var_dump($l);

$p = new Point(1, "origin");
var_dump($p->x, $p->label, $p);

try { $p->x = 2; } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
try { $p->label = "other"; } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
try { $p->extra = 3; } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }

// A new instance is a fresh set of slots, so the constructor may write them.
$q = $p->shifted(10);
var_dump($q->x, $q->label);
var_dump($p->x);

// A readonly class with an ordinary property is still all-readonly.
readonly class Holder {
    public array $items;
    public function __construct(array $items) { $this->items = $items; }
}
$h = new Holder([1, 2]);
var_dump($h->items);
try { $h->items = [3]; } catch (Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
