<?php
// An attribute's constructor is called from the file the attribute is written
// in: its strict_types binds the arguments, and the trace and the "called in"
// both name that file and line. The program's own file is weak.

#[Attribute(Attribute::TARGET_ALL)]
class A {
    public function __construct(public int $v) {}
}

#[A("10")]
class W {
    #[A("11")]
    public function m() {}
}

require __DIR__ . '/attribute_strict_include.inc';

class U {
    use T;
}

function show(ReflectionAttribute $a) {
    try {
        var_dump($a->newInstance()->v);
    } catch (TypeError $e) {
        echo basename($e->getMessage()), "\n";
        echo basename($e->getTrace()[0]['file']), ":", $e->getTrace()[0]['line'], "\n";
    }
}

show((new ReflectionClass('W'))->getAttributes()[0]);
show((new ReflectionMethod('W', 'm'))->getAttributes()[0]);
show((new ReflectionClass('S'))->getAttributes()[0]);
show((new ReflectionMethod('S', 'm'))->getAttributes()[0]);
show((new ReflectionMethod('S', 'm'))->getParameters()[0]->getAttributes()[0]);
show((new ReflectionProperty('S', 'p'))->getAttributes()[0]);
show((new ReflectionClassConstant('S', 'K'))->getAttributes()[0]);
show((new ReflectionFunction('sf'))->getAttributes()[0]);
show((new ReflectionMethod('U', 'tm'))->getAttributes()[0]);
show((new ReflectionMethod('U', 'tm'))->getParameters()[0]->getAttributes()[0]);
show((new ReflectionFunction(strictClosure()))->getAttributes()[0]);
