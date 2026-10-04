<?php
// `unset(C::$x)` is always `Attempt to unset static property C::$x`, once
// the class is fetched; reading or unsetting a static property of a class
// the program does not know runs the autoloader first, which may throw or
// leave it `Class "X" not found` (exception_013, bug61011). In a closure
// written outside any class, `self` is asked when it runs: unbound, it is
// `Cannot access "self" when no class scope is active`.

class C {
    public static $p = 1;
}

function attempt(callable $f) {
    try {
        $f();
        echo "no error\n";
    } catch (Throwable $e) {
        echo get_class($e), ": ", $e->getMessage(), "\n";
    }
}

attempt(function () { unset(C::$p); });
attempt(function () { unset(C::$nope); });
spl_autoload_register(function ($name) {
    echo "autoload $name\n";
    if ($name === 'Thrower') {
        throw new Exception("from autoloader");
    }
});
attempt(function () { echo Thrower::$a; });
attempt(function () { echo Missing::$a; });
attempt(function () { unset(Missing2::$a); });
attempt(function () { unset(Thrower::$a); });

$fn = function () {
    return self::$p;
};
attempt($fn);
var_dump(Closure::bind($fn, null, C::class)());
