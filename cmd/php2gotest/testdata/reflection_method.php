<?php
class B { protected function prot() {} }
class D extends B {
    public function pub() {}
    private function priv() {}
    public static function st() {}
    protected static function pst() {}
}
foreach ([["D","pub"], ["D","priv"], ["D","st"], ["D","pst"], ["B","prot"]] as [$c, $m]) {
    $r = new ReflectionMethod($c, $m);
    printf("%s::%s static=%d public=%d private=%d protected=%d\n",
        $c, $m, $r->isStatic(), $r->isPublic(), $r->isPrivate(), $r->isProtected());
    var_dump($r->setAccessible(true));
}
try {
    new ReflectionProperty("D", "x");
} catch (ReflectionException $e) {
    echo get_class($e), ": ", $e->getMessage(), "\n";
}
$o = new D();
$o->dyn = 7;
$r = new ReflectionProperty($o, "dyn");
var_dump($r->getName(), $r->getValue($o));
$r->setValue($o, 8);
var_dump($o->dyn);
var_dump((new ReflectionObject($o))->getProperty("dyn")->getValue($o));
