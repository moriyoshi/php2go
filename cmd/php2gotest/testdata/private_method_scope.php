<?php
// A private method is not overridden. Called from its own class on an
// instance of a subclass that declares the same name -- protected or public
// -- it is still the private one that runs, whether the call is written,
// dynamic, or a callable.

class A
{
    private function test() { return "A::test on " . get_class($this); }
    private function trace() { return (new Exception)->getTrace()[0]['class'] . "::" . __FUNCTION__; }
    public function run($m)
    {
        return [$this->test(), $this->$m(), call_user_func([$this, $m]), $this->trace()];
    }
}
class ProtectedOverride extends A
{
    protected function test() { return "ProtectedOverride::test"; }
}
class PublicOverride extends A
{
    public function test() { return "PublicOverride::test"; }
    public function trace() { return "PublicOverride::trace"; }
}
class Unrelated
{
    public function poke(A $a) { return $a instanceof PublicOverride ? $a->test() : "-"; }
}

var_dump((new A)->run("test"));
var_dump((new ProtectedOverride)->run("test"));
var_dump((new PublicOverride)->run("test"));
var_dump((new Unrelated)->poke(new PublicOverride));
