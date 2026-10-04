<?php
// is_callable() on a callable naming its class by `self`, `parent` or
// `static` reports the deprecation whatever the answer.

class Base { static function b() {} }
class Child extends Base
{
    static function probe()
    {
        var_dump(is_callable("static::ok"), is_callable(["self", "b"]), is_callable("parent::b"));
        var_dump(is_callable("Child::b"), is_callable([self::class, "missing"]));
    }
}
Child::probe();
