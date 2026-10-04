<?php
// A class constant in a constant expression -- here a parameter default --
// fetches its class the way code does, running the autoloader first; a class
// nothing loads is then `Class "X" not found`.

spl_autoload_register(function ($className) {
    echo "autoload $className\n";
    if ($className === 'Late') {
        class Late { const X = 42; }
    }
});

function f($c = Nope::constant) { var_dump($c); }
try { f(); } catch (Error $e) { echo $e->getMessage(), "\n"; }
f(1);
function g($c = Late::X) { var_dump($c); }
g();
g();
// And in code: the fetch autoloads too.
spl_autoload_register(function ($c) { if ($c === 'InCode') { class InCode { const Y = "y"; } } });
var_dump(InCode::Y);
