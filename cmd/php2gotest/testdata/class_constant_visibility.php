<?php
// A **private class constant is not inherited**. Walking up the hierarchy finds
// it in the ancestor that declared it, and a visibility check alone then passes
// whenever the caller *is* that ancestor -- so `static::PRIV` inside the
// declaring class, with `static` resolving to a subclass, answered the parent's
// value. The engine says the name is not there at all.

class Declarer {
    private const PRIV = "priv";
    protected const PROT = "prot";
    public const PUB = "pub";

    public static function viaSelf() { return self::PRIV; }
    public static function viaStatic() {
        try { return static::PRIV; } catch (Throwable $e) { return get_class($e) . ": " . $e->getMessage(); }
    }
    public static function protectedViaStatic() { return static::PROT; }
}
class Heir extends Declarer {}

// `self::` is the declaring class wherever it is written, so both work.
var_dump(Declarer::viaSelf(), Heir::viaSelf());

// `static::` is the called class, and the private one is not on it.
var_dump(Declarer::viaStatic());
var_dump(Heir::viaStatic());

// Protected and public are inherited, so `static::` finds them either way.
var_dump(Declarer::protectedViaStatic(), Heir::protectedViaStatic());

// Named directly, the same rule from outside: the subclass does not have it.
try { var_dump(Heir::PRIV); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
try { var_dump(Declarer::PRIV); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
try { var_dump(Heir::PROT); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
var_dump(Heir::PUB, Declarer::PUB);

// An interface constant is inherited, and an interface cannot declare a private
// one -- so the sideways walk never has to make this distinction.
interface Contract { const IFACE = "iface"; }
class Implementer implements Contract {}
var_dump(Implementer::IFACE);
