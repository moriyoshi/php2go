<?php
// call_user_func checks its callback where the call is compiled, so a keyword
// callable is judged against the calling scope: none at the top level, and no
// parent in a class that has none.

try {
    call_user_func('self::m');
} catch (Error $e) {
    echo get_class($e), ": ", $e->getMessage(), "\n";
}
try {
    call_user_func(['static', 'm']);
} catch (Error $e) {
    echo get_class($e), ": ", $e->getMessage(), "\n";
}

class Orphan
{
    static function m() { return "m"; }
    static function run()
    {
        try {
            call_user_func('parent::m');
        } catch (Error $e) {
            echo get_class($e), ": ", $e->getMessage(), "\n";
        }
        return call_user_func(['self', 'm']);
    }
}
var_dump(Orphan::run());

// A class answering missing names with __callStatic is callable by any name.
class OnlyStaticMagic
{
    static function __callStatic($name, $args) { echo "$name(", count($args), ")\n"; }
}
call_user_func("OnlyStaticMagic::Two", 'A', 'B');
call_user_func(["OnlyStaticMagic", "Three"], null, 0, false);
