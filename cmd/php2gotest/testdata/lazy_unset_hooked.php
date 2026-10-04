<?php
// unset() of a hooked property is refused before anything else happens: a
// lazy object stays lazy, where unsetting an ordinary property initializes it.

class C
{
    public $a {
        get { return $this->a; }
        set($value) { $this->a = $value; }
    }
    public int $b = 1;
    public function __construct() { echo "constructed\n"; $this->b = 2; }
}
$r = new ReflectionClass(C::class);
foreach (['ghost' => fn() => $r->newLazyGhost(function ($o) { $o->__construct(); }),
          'proxy' => fn() => $r->newLazyProxy(function () { return new C(); })] as $kind => $make) {
    $obj = $make();
    try {
        unset($obj->a);
    } catch (Error $e) {
        echo "$kind: ", $e->getMessage(), "\n";
    }
    var_dump($r->isUninitializedLazyObject($obj));
    unset($obj->b);
    var_dump($r->isUninitializedLazyObject($obj));
}
