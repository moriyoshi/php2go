<?php
// ReflectionClass::getInterfaceNames() in the engine's order: the parent's
// whole list first, then each interface the class names followed by the ones
// it extends, each name once -- including the Stringable a __toString adds.

interface I1 {} interface I2 {} interface I3 extends I1 {} interface I4 {}
class P implements I1 {}
class C extends P implements I2, I1 {}
class D extends C implements I3 {}
class E implements I4, I3 {}
class T { function __toString() { return ""; } }
class U extends T implements I4 { function __toString() { return ""; } }
foreach (['C', 'D', 'E', 'T', 'U'] as $c) {
    echo $c, ": ", implode(",", (new ReflectionClass($c))->getInterfaceNames()), "\n";
}
