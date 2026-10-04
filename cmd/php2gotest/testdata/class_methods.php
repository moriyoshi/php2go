<?php
// get_class_methods reports what the *calling scope* can reach, in the engine's
// order: a class's own declarations first -- with a trait's after them -- then
// each ancestor's, and a name a subclass redeclares appears once, at the
// subclass.
trait T {
    public function tm() {}
    private function tp() {}
}
interface I {
    public function im();
}
abstract class A implements I {
    use T;
    public function ap() {}
    protected function aq() {}
    private function ar() {}
    public static function asx() {}
    abstract public function ab();
    public function fromA() { return get_class_methods("D"); }
}
class D extends A {
    public function im() {}
    public function ab() {}
    private function dp() {}
    public function fromD() { return get_class_methods("D"); }
}

$d = new D;
var_dump(get_class_methods("D"));
var_dump(get_class_methods($d));
var_dump($d->fromA());
var_dump($d->fromD());
var_dump(get_class_methods("I"));
var_dump(get_class_methods("A"));
