<?php
// A class alias in an override's signature names its class: checked at run
// time, `AliasA` is `A`.
class A {
    function foo(A $param) {}
    function bar(): A { return $this; }
}
class_alias('A', 'AliasA');
eval('
    class B extends A {
        function foo(AliasA $param) {}
        function bar(): AliasA { return $this; }
    }
');
var_dump(get_class((new B)->bar()));
echo "DONE\n";
