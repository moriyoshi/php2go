<?php
// An object whose constructor threw is freed without its destructor, and
// what its properties alone held dies with it -- before the catch.
class A {
    function __construct(public string $n) {}
    function __destruct() { echo "-{$this->n} "; }
}
class Thrower {
    function __construct(public string $n) {}
    function __destruct() { echo "-{$this->n} "; throw new Exception("from {$this->n}"); }
}

class B {
    public $a;
    function __construct() { $this->a = new A('b'); throw new Exception('B'); }
    function __destruct() { echo "never "; }
}
try { new B; } catch (Exception $e) { echo "caught ", $e->getMessage(), "\n"; }
// The property's object goes first, then the failed object's number --
// which is the one the next object takes.
var_dump(new stdClass);

// Several properties, any visibility, freed in declaration order.
class C {
    private $p;
    protected $q;
    public $r;
    function __construct() {
        $this->r = new A('r');
        $this->p = new A('p');
        $this->q = new A('q');
        throw new Exception('C');
    }
}
try { $c = new C; } catch (Exception $e) { echo "caught ", $e->getMessage(), "\n"; }

// A constructor inherited from a parent.
class D extends B {}
try { new D; } catch (Exception $e) { echo "caught ", $e->getMessage(), "\n"; }

// What the property's destructor throws takes the failure as previous.
class E {
    public $t;
    function __construct() { $this->t = new Thrower('t'); throw new Exception('E'); }
}
try { new E; } catch (Exception $e) {
    echo "caught ", $e->getMessage(), " <- ", $e->getPrevious()->getMessage(), "\n";
}

// Kept elsewhere: the constructor read the property back out.
class F {
    public static $keep;
    public $a;
    function __construct() { $this->a = new A('f'); self::$keep = $this->a; throw new Exception('F'); }
}
try { new F; } catch (Exception $e) { echo "caught ", $e->getMessage(), "\n"; }

// Kept elsewhere: a method on $this copied it out.
class G {
    public $a;
    function __construct() { $this->a = new A('g'); $this->stash(); throw new Exception('G'); }
    function stash() { $GLOBALS['stashed'] = $this->a; }
}
try { new G; } catch (Exception $e) { echo "caught ", $e->getMessage(), "\n"; }

// Kept elsewhere: the object went through a local that was also stored.
class H {
    public $a;
    function __construct() { $x = new A('h'); $this->a = $x; $GLOBALS['alias'] = $x; throw new Exception('H'); }
}
try { new H; } catch (Exception $e) { echo "caught ", $e->getMessage(), "\n"; }

// Kept elsewhere: a parent method reached through `parent::`, which passes
// `$this` without naming it, copied it out.
class K0 {
    public $a;
    function keep() { $GLOBALS['kept'] = $this->a; }
}
class K extends K0 {
    function __construct() { $this->a = new A('k'); parent::keep(); throw new Exception('K'); }
}
try { new K; } catch (Exception $e) { echo "caught ", $e->getMessage(), "\n"; }

// Kept elsewhere: the property took a function's result, which is shared.
function make() { return $GLOBALS['shared'] ??= new A('l'); }
class L {
    public $a;
    function __construct() { $this->a = make(); throw new Exception('L'); }
}
try { new L; } catch (Exception $e) { echo "caught ", $e->getMessage(), "\n"; }

echo "end\n";
