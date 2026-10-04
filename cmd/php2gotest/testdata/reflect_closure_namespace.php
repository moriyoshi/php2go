<?php
namespace Foo;

// A closure is in no namespace -- its name only mentions one -- while a
// namespaced function is in its namespace.

class Bar
{
    public function baz() { return function () {}; }
}
function named() {}

foreach ([(new Bar())->baz(), 'Foo\named'] as $f) {
    $r = new \ReflectionFunction($f);
    var_dump($r->getShortName(), $r->getNamespaceName(), $r->inNamespace());
}
