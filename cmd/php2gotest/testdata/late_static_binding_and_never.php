<?php
// `get_called_class()` is late static binding asked as a **value** rather than
// as a class reference, so it answers what `static::` resolves to.

class Base {
    public static function report(): void {
        var_dump(get_called_class(), static::class, self::class);
    }
    public static function hidden(): void { echo "  Base::hidden\n"; }
    public function viaInstance(): void { $this->hidden(); }
}
class Derived extends Base {}

Base::report();
Derived::report();

// A **static method reached through an instance** is an ordinary call: no
// `$this` is bound, and `static::` is the receiver's class.
$base = new Base;
$base->report();
$derived = new Derived;
$derived->report();

// Inherited, and reached from inside the class too.
$derived->hidden();
$base->viaInstance();

// Visibility still applies to the instance spelling.
class Restricted {
    private static function secret(): void { echo "  secret\n"; }
    public function open(): void { $this->secret(); }
}
(new Restricted)->open();
try { (new Restricted)->secret(); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }

// `get_called_class()` outside a class is an Error the program can catch.
function outside() { return get_called_class(); }
try { var_dump(outside()); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }

// Reaching the end of a `never` function instead of throwing or exiting. The
// engine names a **method** differently from a function.
function neverReturns(): never { echo "  ran the function\n"; }
try { neverReturns(); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
class Terminal { public function stop(): never { echo "  ran the method\n"; } }
try { (new Terminal)->stop(); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
// A `never` that really does not return is not an error.
function throws(): never { throw new RuntimeException("as declared"); }
try { throws(); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
