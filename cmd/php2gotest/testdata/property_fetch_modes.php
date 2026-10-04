<?php
// A property fetch has the same four modes a dimension fetch does, and they
// disagree about an **uninitialized** property. Every answer measured.
//
//	$t->prop->prop = 0     W      refuses the modification
//	$t->prop->prop += 1    RW     reads first: "must not be accessed before
//	                              initialization"
//	isset($t->prop->prop)  IS     silent, false
//	unset($t->prop->prop)  UNSET  a no-op -- nothing there to remove
//
// And reaching *through* an object never modifies the property, so an
// initialized `readonly Inner $p` allows `$t->p->x = 1` where an initialized
// `readonly array $a` refuses `$t->a[0] = 9`.
class T {
  public readonly array $a;
  public private(set) array $b;
  public function init() { $this->a = [1,2]; $this->b = [1,2]; }
  public function usDim() { try { unset($this->a[0]); echo "ro us_dim ok\n"; } catch (Throwable $e) { echo "ro us_dim: ", $e->getMessage(), "\n"; } }
  public function wDim() { try { $this->a[0] = 9; echo "ro w_dim ok\n"; } catch (Throwable $e) { echo "ro w_dim: ", $e->getMessage(), "\n"; } }
}
echo "-- uninitialized, inside\n"; $t = new T; $t->usDim(); $t->wDim();
echo "-- initialized, inside\n"; $u = new T; $u->init(); $u->usDim(); $u->wDim();
echo "-- uninitialized, outside\n"; $v = new T;
try { unset($v->a[0]); echo "out us_dim ok\n"; } catch (Throwable $e) { echo "out us_dim: ", $e->getMessage(), "\n"; }
try { unset($v->b[0]); echo "out b us_dim ok\n"; } catch (Throwable $e) { echo "out b us_dim: ", $e->getMessage(), "\n"; }

class Inner { public int $prop = 1; public array $array = []; }
class Nested { public readonly Inner $prop; public function init() { $this->prop = new Inner; } }

foreach ([0, 1] as $init) {
    foreach (['r', 'w', 'rw', 'is', 'us', 'us_dim'] as $op) {
        $n = new Nested;
        if ($init) { $n->init(); }
        echo "Init: $init, op: $op: ";
        try {
            switch ($op) {
                case 'r': echo $n->prop->prop; break;
                case 'w': $n->prop->prop = 0; echo 'done'; break;
                case 'rw': $n->prop->prop += 1; echo 'done'; break;
                case 'is': echo (int) isset($n->prop->prop); break;
                case 'us': unset($n->prop->prop); echo 'done'; break;
                case 'us_dim': unset($n->prop->array[0]); echo 'done'; break;
            }
            echo "\n";
        } catch (Throwable $e) { echo $e->getMessage(), "\n"; }
    }
}
