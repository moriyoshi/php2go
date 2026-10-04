<?php
// A literal `C::$p` site keeps the slot and declaration its first successful
// access resolved (OWNERSHIP-PERF.md, static property sites). Each shape here
// is one where a remembered answer would be wrong if the site cached too much,
// too early, or across the wrong boundary.

class Base
{
    public static $plain = 0;
    public static int $typed = 0;
    private static $secret = "s";

    public static function bump()
    {
        for ($i = 0; $i < 3; $i++) {
            self::$plain = self::$plain + 1;
        }
        return self::$plain;
    }

    public static function setTyped($v)
    {
        // One site, several values: coerced, then refused, then coerced.
        self::$typed = $v;
        return self::$typed;
    }

    public static function secret()
    {
        return self::$secret;
    }
}

class Deferred
{
    public static $deferred = LATE * 2;
}

class Child extends Base
{
    public static $own = "child";
}

class Redeclares extends Base
{
    public static $plain = "redeclared";
}

function readSecret()
{
    try {
        return Base::$secret;
    } catch (Error $e) {
        return $e->getMessage();
    }
}

function setTyped($v)
{
    try {
        return Base::setTyped($v);
    } catch (TypeError $e) {
        return $e->getMessage();
    }
}

var_dump(Base::bump(), Base::bump());
var_dump(setTyped("7"), setTyped("x"), setTyped(9.0), setTyped(true));

// A refusal is never remembered as an answer: the same site refuses twice.
var_dump(readSecret(), readSecret(), Base::secret());

// A subclass shares its parent's slot; a redeclaring one has its own.
function childPlain()
{
    Child::$plain = 100;
    return [Child::$plain, Base::$plain, Redeclares::$plain];
}
var_dump(childPlain(), childPlain());

// A deferred initializer runs on the first read, from whichever site.
define("LATE", 21);
function deferred()
{
    return Deferred::$deferred;
}
var_dump(deferred(), deferred());

// A reference to a static: writes through the site reach the reference.
$r = &Base::$plain;
function writePlain($v)
{
    Base::$plain = $v;
}
writePlain(5);
var_dump($r);
$r = 6;
var_dump(Base::$plain);

// isset through a site, before and after the value becomes null.
function issetPlain()
{
    return isset(Base::$plain);
}
var_dump(issetPlain());
writePlain(null);
var_dump(issetPlain());

// A class declared conditionally: the site is used before and after it exists.
function lateRead()
{
    try {
        return Late::$v;
    } catch (Error $e) {
        return get_class($e) . ": " . $e->getMessage();
    }
}
var_dump(lateRead());
if (LATE > 0) {
    class Late
    {
        public static $v = "late";
    }
}
var_dump(lateRead(), lateRead());
echo "end\n";
