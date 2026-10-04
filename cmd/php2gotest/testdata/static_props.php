<?php
class C {
    public static $count = 0;
    public static function bump(int $by): int { self::$count += $by; return self::$count; }
    public static function get(): int { return self::$count; }
}
class D extends C {}
echo C::bump(2), "\n";
echo C::bump(3), "\n";
echo C::$count, "\n";
echo D::$count, "\n";
D::bump(10);
echo C::$count, "\n";
echo C::get(), "\n";

// Plain writes, not compound: `C::$p = v` lowers to `sprop.set`, where
// `C::$p += v` still reads a place first. Only the compound form was covered,
// so the intrinsic had no case exercising it.
C::$count = 100;
echo C::$count, "\n";
D::$count = 7;
echo C::$count, " ", D::$count, "\n";
C::$count = C::$count + 1;
echo C::get(), "\n";

// The silent read. `isset` on a static property had shared the dimension form's
// spelling -- (name, class) read as (container, key) -- and answered from a
// string offset, so a null property was reported set.
class S { public static $set = 1; public static $null = null; }
var_dump(isset(S::$set), isset(S::$null), empty(S::$set), empty(S::$null));
var_dump(S::$null ?? "d", S::$set ?? "d");
