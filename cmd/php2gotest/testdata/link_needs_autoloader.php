<?php
// A class whose parent only an autoloader supplies is no verdict for the
// harvest's trial link -- which runs with no autoloader and would call the
// parent missing -- so it is declared at run time as usual and links.

spl_autoload_register(function ($name) {
    echo "autoload $name\n";
    if ($name === 'Base') {
        class Base { function hello() { return "hi"; } }
    }
});

echo "start\n";
class Child extends Base {}
var_dump((new Child)->hello());
echo "done\n";
