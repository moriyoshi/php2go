<?php
// A deprecated __call or __callStatic reached as a trampoline names the method
// the program called; called by its own name it names itself. The name never
// leaks into a later, direct call.

class Dep
{
    #[\Deprecated]
    function __call(string $name, array $params) { return $name; }

    #[\Deprecated("due to some reason")]
    static function __callStatic(string $name, array $params) { return $name; }
}
class Plain
{
    function __call($name, $params) { return (new Dep)->__call("direct", []); }
}
$d = new Dep();
var_dump($d->test());
var_dump(Dep::test2());
var_dump($d->__call("explicit", []));
var_dump((new Plain)->viaPlain());
