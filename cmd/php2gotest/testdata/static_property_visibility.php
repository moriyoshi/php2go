<?php
// A static property has visibility, and it is checked against the **calling
// scope** like an instance property's -- protected reaches across the
// hierarchy in both directions, private only from the declaring class. The
// message names the class as the access **wrote** it, not the one that
// declared the property.

class Declarer {
    private static $secret = "priv";
    protected static $shared = "prot";
    public static $open = "pub";

    public static function fromDeclarer(): array {
        return [static::$secret, static::$shared, static::$open];
    }
}
class Heir extends Declarer {
    public static function fromHeir(): array { return [static::$shared, static::$open]; }
}
class Stranger {
    public static function fromStranger(): array {
        $out = [];
        foreach ([["Declarer", "shared"], ["Declarer", "open"]] as [$c, $n]) {
            try { $out[] = $c::$$n; } catch (Throwable $e) { $out[] = $e->getMessage(); }
        }
        return $out;
    }
}

var_dump(Declarer::fromDeclarer());
var_dump(Heir::fromHeir());
var_dump(Stranger::fromStranger());

foreach ([["Declarer","secret"],["Declarer","shared"],["Declarer","open"],["Heir","shared"],["Heir","open"]] as [$c, $n]) {
    try { var_dump($c::$$n); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
}

// Writing is refused the same way, and so is taking a reference.
try { Declarer::$secret = "x"; } catch (Throwable $e) { echo "write:  ", $e->getMessage(), "\n"; }
try { $r = &Declarer::$shared; } catch (Throwable $e) { echo "bind:   ", $e->getMessage(), "\n"; }
try { Declarer::$shared .= "!"; } catch (Throwable $e) { echo "append: ", $e->getMessage(), "\n"; }

// `isset` is silent about an unreachable one, as it is for a missing one.
var_dump(isset(Declarer::$secret), isset(Declarer::$open), isset(Declarer::$nope));
