<?php
// A `#[\Deprecated]` reason that is a constant expression is evaluated where
// the deprecation fires, in the declaring class's scope: a constant that
// names itself deprecates once with its own value as the reason, one that
// names another deprecated constant deprecates that one first -- and a
// throwing handler stops it there -- and defined() looks without using.

class Clazz {
    #[\Deprecated(self::SELF)]
    public const SELF = "its own value";
    #[\Deprecated]
    public const OTHER = "from another";
    #[\Deprecated(self::OTHER)]
    public const USES_OTHER = 1;
    #[\Deprecated(LATER)]
    public const GLOBAL_REASON = 2;
    #[\Deprecated(self::DEFERRED)]
    public const DEFERRED = LATER;
}
const LATER = "defined later";

var_dump(Clazz::SELF);
var_dump(Clazz::GLOBAL_REASON);
var_dump(Clazz::DEFERRED);
var_dump(defined('Clazz::OTHER'));

set_error_handler(function (int $errno, string $errstr) {
    throw new ErrorException($errstr, 0, $errno);
});
try {
    var_dump(Clazz::USES_OTHER);
} catch (ErrorException $e) {
    echo "Caught: ", $e->getMessage(), "\n";
}
