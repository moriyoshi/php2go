<?php
// The reflectors' __toString beyond flat shapes: `inherits`, `overwrites`
// and `prototype` from the engine's linking (a parent's prototype or the
// parent itself, none for a private parent, a constructor's only when
// abstract; each newly implemented interface sets it the same way),
// literal parameter defaults as format_default_value writes them,
// ReflectionProperty and ReflectionObject exports (probed).

interface I1 { function a(); }
interface I2 extends I1 { function b(); }
interface I3 { function c(); function __construct(); }

abstract class P {
    function m() {}
    abstract function n();
    private function p() {}
    function __construct() {}
    static function s() {}
}

class C extends P implements I2, I3 {
    function n() {}
    function m($x = [1, 'k' => "v\t"], float $f = 2, ?array $n = null, $s = -1.5) {}
    function p() {}
    function a() {}
    function b() {}
    function c() {}
    function __construct() {}
    static function s() {}
    #[\Deprecated]
    function own() {}
}

class D extends C {
    function a() {}
}

echo new ReflectionClass('C');
echo new ReflectionMethod('C', 'm');
echo new ReflectionMethod('D', 'm');
echo new ReflectionMethod('D', 'a');
echo new ReflectionMethod('D', 's');
echo new ReflectionMethod('D', '__construct');
echo new ReflectionMethod('D', 'OWN');

abstract class Q0 { abstract function __construct(); }
class Q1 extends Q0 { function __construct() {} }
class Q2 extends Q1 { function __construct() {} }
echo new ReflectionMethod('Q2', '__construct');

#[\AllowDynamicProperties]
class Props {
    /** the doc */
    public $a = 'x';
    protected static $s = [1, 2];
    public int $n;
    public $hooked {
        get { return $this->hooked; }
        set { $this->hooked = $value; }
    }
    public $virt {
        get { return 1; }
    }
    function m() {}
}
foreach (['a', 's', 'n', 'hooked', 'virt'] as $p) {
    echo new ReflectionProperty('Props', $p);
}
echo new ReflectionClass('Props');

class Bare { public $d; }
$o = new Bare;
$o->dyn = 1;
$o->{'x y'} = 2;
echo new ReflectionObject($o);
echo new ReflectionObject(new Bare);

class R0 { private function hidden() {} function vis() {} private static function sh() {} }
class R1 extends R0 { private function hidden() {} }
echo new ReflectionClass('R1');
echo new ReflectionMethod('R1', 'hidden');

class E2 extends P { function n() {} function __construct() {} }
echo new ReflectionMethod('E2', '__construct');
