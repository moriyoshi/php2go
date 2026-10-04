<?php
// ReflectionObject reads a class's attributes as ReflectionClass does; an
// inherited method's attributes are its declaration's, while an override
// has only its own; and an attribute written twice on one target must be
// IS_REPEATABLE to be instantiated -- checked after the target is.

#[Attribute(Attribute::TARGET_METHOD | Attribute::TARGET_CLASS)]
class Once {}
#[Attribute(Attribute::TARGET_CLASS | Attribute::IS_REPEATABLE)]
class Many {}

#[Once]
class Base {
    #[Once]
    public function inherited() {}
    #[Once]
    public function overridden() {}
}
class Child extends Base {
    public function overridden() {}
}

$names = fn ($list) => array_map(fn ($a) => $a->getName(), $list);
print_r($names((new ReflectionObject(new Base))->getAttributes()));
print_r($names((new ReflectionMethod('Child', 'inherited'))->getAttributes()));
print_r($names((new ReflectionMethod('Child', 'overridden'))->getAttributes()));

#[Many] #[Many]
class Twice {}
#[Once] #[Once]
class TwiceOnce {}
foreach (['Twice', 'TwiceOnce'] as $c) {
    $a = (new ReflectionClass($c))->getAttributes()[0];
    var_dump($a->isRepeated());
    try { var_dump(get_class($a->newInstance())); } catch (Error $e) { echo $e->getMessage(), "\n"; }
}
