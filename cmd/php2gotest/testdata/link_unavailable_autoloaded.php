<?php
// The same unchecked override is fine when an autoloader supplies the class
// the check needs: the declaration asks for it, and links (variance/
// trait_success).

spl_autoload_register(function ($name) {
    echo "autoload $name\n";
    if ($name === 'InterfaceB') {
        interface InterfaceB extends InterfaceA {}
    }
});

interface InterfaceA {}
trait SomeTrait {
    abstract public function func(): ?InterfaceA;
}
class Foo {
    public function func(): ?InterfaceB { return null; }
}
class Bar extends Foo {
    use SomeTrait;
}
var_dump((new Bar)->func());
echo "done\n";
