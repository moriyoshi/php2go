<?php
class P {
    public readonly int $x;
    public function __construct(int $x) { $this->x = $x; }
    public function again() { $this->x = 99; }
}
$p = new P(5);
echo $p->x, "\n";
try { $p->x = 7; } catch (Error $e) { echo "outside: ", $e->getMessage(), "\n"; }
try { $p->again(); } catch (Error $e) { echo "inside: ", $e->getMessage(), "\n"; }
class Q { public readonly int $y; public int $t; }
$q = new Q();
try { echo $q->y; } catch (Error $e) { echo "uninit: ", $e->getMessage(), "\n"; }
try { $q->y = 1; } catch (Error $e) { echo "init-outside: ", $e->getMessage(), "\n"; }
try { echo $q->t; } catch (Error $e) { echo "typed: ", $e->getMessage(), "\n"; }
var_dump($p);
