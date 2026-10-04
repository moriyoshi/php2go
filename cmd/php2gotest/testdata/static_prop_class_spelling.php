<?php
// `Access to undeclared static property` names the class as it was
// declared, however the program spelled it: an engine class reached through
// a lowercase string (037.phpt, where the string is a namespace name) and a
// program class spelled in another case.

namespace closure;

class closure { static $x = 1; }
class Declared { static $here = 1; }

$x = __NAMESPACE__;
var_dump(closure::$x);
foreach (['closure', 'STDCLASS', 'arrayobject', 'closure\declared', 'CLOSURE\DECLARED'] as $class) {
    try {
        var_dump($class::$x);
    } catch (\Error $e) {
        echo $e->getMessage(), "\n";
    }
}
