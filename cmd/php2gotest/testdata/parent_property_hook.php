<?php
// `parent::$p::get()` and `::set()` reach the parent's hook when it has one,
// and otherwise PHP's **implicit** accessor for a plain or backed property.
// The implicit one is an internal function, so its arity is exact where a real
// parent hook takes extra arguments happily -- and it answers for the
// property's own refusals. Every answer here came from the engine.
class P { public $prop; public int $typed; private $priv = 1; public $hooked { get { echo "P get\n"; return 99; } set { echo "P set ", $value, "\n"; } } }
class C extends P {
  public $prop { get { return parent::$prop::get(); } set { var_dump(parent::$prop::set($value)); } }
  public int $typed { get { return parent::$typed::get(); } set { parent::$typed::set($value); } }
  public $hooked { get { return parent::$hooked::get(); } set { parent::$hooked::set($value); } }
}
$c = new C();
$c->prop = 42; var_dump($c->prop);
echo "typed coerce: "; $c->typed = "5"; var_dump($c->typed);
echo "hooked: "; $c->hooked = 3; var_dump($c->hooked);
class D extends P { public int $u { get { return parent::$u::get(); } } }
echo "uninit: "; $d = new D; try { var_dump($d->u); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
class E { public $p { get { return parent::$p::get(); } } }
echo "noparent: "; $e = new E; try { var_dump($e->p); } catch (Throwable $e2) { echo get_class($e2), ": ", $e2->getMessage(), "\n"; }
class Q {}
class F extends Q { public $p { get { return parent::$p::get(); } } }
echo "undefprop: "; $f = new F; try { var_dump($f->p); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
class G extends P { public $priv { get { return parent::$priv::get(); } } }
echo "private: "; $g = new G; try { var_dump($g->priv); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
class H extends P { public $prop { get { return parent::$prop::get(1); } set { parent::$prop::set($value, 2); } } }
echo "zpp get: "; $h = new H; try { var_dump($h->prop); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
echo "zpp set: "; try { $h->prop = 1; } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }

// A `private(set)` property with a **set hook** is still refused from outside:
// the hook is what happens once the write is allowed, not a way around the
// modifier. Checking only on the backing-store path let this one through.
class AV {
    public private(set) string $prop1 { set => $value; }
    public private(set) string $prop2 { get => $this->prop2; }
    public function inside() { $this->prop1 = "from inside"; return $this->prop1; }
}
$av = new AV();
foreach (["prop1", "prop2"] as $p) {
    echo $p, ": ";
    try { $av->$p = "hello"; echo "ok\n"; } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
}
var_dump($av->inside());
