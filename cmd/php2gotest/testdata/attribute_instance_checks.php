<?php
// What newInstance() and getAttributes() refuse (attributes/005_objects,
// 006_filter): a class without `#[Attribute]` used as one; an attribute
// whose constructor is private, called from no class's scope; getAttributes
// flags other than 0 and IS_INSTANCEOF, named after the method's declaring
// reflector; and an IS_INSTANCEOF filter naming a class that does not exist.

#[Attribute]
class Priv { private function __construct() {} }
class NotAttr {}
#[Attribute]
class Fine { public function __construct(public $v = 1) {} }

#[Priv] #[NotAttr] #[Fine(5)]
class Target {
    #[Fine] public function m() {}
}

function attempt(callable $f) {
    try {
        $r = $f();
        echo is_object($r) ? get_class($r) : var_export($r, true), "\n";
    } catch (Throwable $e) {
        echo get_class($e), ": ", $e->getMessage(), "\n";
    }
}
$attrs = (new ReflectionClass('Target'))->getAttributes();
foreach ($attrs as $a) {
    attempt(fn() => $a->newInstance());
}
attempt(fn() => (new ReflectionClass('Target'))->getAttributes(Fine::class, 3));
attempt(fn() => (new ReflectionMethod('Target', 'm'))->getAttributes(null, 1));
attempt(fn() => count((new ReflectionClass('Target'))->getAttributes(Missing::class, ReflectionAttribute::IS_INSTANCEOF)));
attempt(fn() => count((new ReflectionClass('Target'))->getAttributes(Fine::class, ReflectionAttribute::IS_INSTANCEOF)));
