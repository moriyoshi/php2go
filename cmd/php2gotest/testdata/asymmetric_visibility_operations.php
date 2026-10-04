<?php
// PHP 8.4's `private(set)` / `protected(set)` refuses **three** operations with
// three different verbs, and the engine picks the verb by how the property is
// reached, not by what is done to it afterwards: storing is `modify`, removing
// is `unset`, and merely reaching a place -- an element write, an append, an
// `&` -- is `indirectly modify`. Only the first was checked.
class C {
  public private(set) int $p = 1;
  public private(set) array $a = [];
  public protected(set) int $q = 1;
  public function ok() { $this->p = 5; return $this->p; }
}
class D extends C {}
$c = new C;
echo "write: "; try { $c->p = 2; var_dump($c->p); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
echo "write prot: "; try { $c->q = 2; var_dump($c->q); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
echo "unset: "; try { unset($c->p); var_dump($c->p); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
echo "unset prot: "; try { unset($c->q); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
echo "append: "; try { $c->a[] = 1; var_dump($c->a); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
echo "dim: "; try { $c->a["k"] = 1; } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
echo "ref: "; try { $r = &$c->p; } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
echo "inc: "; try { $c->p++; var_dump($c->p); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
echo "compound: "; try { $c->p += 1; var_dump($c->p); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
echo "read: "; var_dump($c->p);
echo "own: "; var_dump($c->ok());

// From the declaring class every one of them is allowed.
class E {
    public private(set) int $p = 1;
    public private(set) array $a = [];
    public function all() {
        $this->p = 2;
        $this->p++;
        $this->a[] = 1;
        $r = &$this->p;
        $r = 9;
        unset($this->a);
        return $this->p;
    }
}
$e = new E;
var_dump($e->all());

// A subclass is not the declaring class for private(set), and is for
// protected(set).
class F { public private(set) int $p = 1; public protected(set) int $q = 1; }
class G extends F {
    public function priv() { $this->p = 2; }
    public function prot() { $this->q = 2; return $this->q; }
}
$g = new G;
echo "sub private: ";
try { $g->priv(); echo "ok\n"; } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
echo "sub protected: ";
try { var_dump($g->prot()); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
