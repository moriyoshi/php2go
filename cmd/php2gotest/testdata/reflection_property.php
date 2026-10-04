<?php
class Base {
    public int $kept = 1;
    private string $hidden = "h";
}
class P extends Base {
    public int $a = 1;
    private string $s = "x";
    protected $prot = null;
    public static $st = "S";
    public int $u;
    public readonly int $r;
    public $untyped;
}

$o = new P();

$ra = new ReflectionProperty("P", "a");
var_dump($ra->getValue($o));
$ra->setValue($o, "7");
var_dump($o->a);

$rs = new ReflectionProperty("P", "s");
var_dump($rs->getValue($o));
$rs->setValue($o, "y");
var_dump($rs->getValue($o));

$rp = new ReflectionProperty("P", "prot");
$rp->setValue($o, 3);
var_dump($rp->getValue($o));

$rh = new ReflectionProperty("Base", "hidden");
var_dump($rh->getValue($o));

$rst = new ReflectionProperty("P", "st");
var_dump($rst->getValue());
$rst->setValue(null, "T");
var_dump(P::$st);

$ru = new ReflectionProperty("P", "u");
var_dump($ru->isInitialized($o));
try {
    $ru->getValue($o);
} catch (Error $e) {
    echo get_class($e), ": ", $e->getMessage(), "\n";
}

try {
    $ra->setValue($o, "z");
} catch (Error $e) {
    echo get_class($e), ": ", $e->getMessage(), "\n";
}

$rr = new ReflectionProperty("P", "r");
$rr->setValue($o, 5);
var_dump($o->r);
try {
    $rr->setValue($o, 6);
} catch (Error $e) {
    echo get_class($e), ": ", $e->getMessage(), "\n";
}

$rn = new ReflectionProperty("P", "untyped");
var_dump($rn->getValue($o));

foreach ((new ReflectionClass("P"))->getProperties() as $p) {
    printf("%s static=%d readonly=%d public=%d private=%d protected=%d default=%d\n",
        $p->getName(), $p->isStatic(), $p->isReadOnly(),
        $p->isPublic(), $p->isPrivate(), $p->isProtected(), $p->hasDefaultValue());
}
var_dump($ra->getDefaultValue(), $ru->getDefaultValue());
